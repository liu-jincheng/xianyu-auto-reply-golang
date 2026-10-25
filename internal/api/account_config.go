package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) accountConfigRoutes(r chi.Router, kind string, defaults map[string]any) {
	r.Get("/", s.accountConfigs(kind))
	r.Get("/{accountID}", s.accountConfigGet(kind, defaults))
	r.Put("/{accountID}", s.accountConfigPut(kind))
	r.Delete("/{accountID}", s.accountConfigDelete(kind))
}
func (s *Server) accountConfigs(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := current(r)
		rows, e := s.db.QueryContext(r.Context(), `SELECT account_id,data FROM resource_records WHERE owner_id=? AND kind=? ORDER BY id`, u.ID, kind)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		defer rows.Close()
		out := map[string]any{}
		for rows.Next() {
			var ac sql.NullString
			var data string
			_ = rows.Scan(&ac, &data)
			var m map[string]any
			_ = json.Unmarshal([]byte(data), &m)
			out[ac.String] = m
		}
		write(w, 200, out)
	}
}
func (s *Server) accountConfigGet(kind string, defaults map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := current(r)
		var data string
		e := s.db.QueryRowContext(r.Context(), `SELECT data FROM resource_records WHERE owner_id=? AND kind=? AND account_id=? ORDER BY id DESC LIMIT 1`, u.ID, kind, chi.URLParam(r, "accountID")).Scan(&data)
		if e == sql.ErrNoRows {
			copy := map[string]any{}
			for k, v := range defaults {
				copy[k] = v
			}
			write(w, 200, copy)
			return
		}
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		var m map[string]any
		_ = json.Unmarshal([]byte(data), &m)
		write(w, 200, m)
	}
}
func (s *Server) accountConfigPut(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := current(r)
		var p map[string]any
		if decode(r, &p) != nil || p == nil {
			fail(w, 400, "请求格式错误")
			return
		}
		conn, e := s.db.Conn(r.Context())
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		defer conn.Close()
		tx, e := conn.BeginTx(r.Context(), nil)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		defer tx.Rollback()
		p, e = saveAccountConfig(r.Context(), tx, u.ID, kind, chi.URLParam(r, "accountID"), p)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		if e = tx.Commit(); e != nil {
			// SQLite may keep a failed COMMIT open after sql.Tx is done. Keep
			// the connection reserved until that transaction has rolled back.
			_, _ = conn.ExecContext(context.WithoutCancel(r.Context()), "ROLLBACK")
			fail(w, 500, e.Error())
			return
		}
		ok(w, "配置更新成功", p)
	}
}

func saveAccountConfig(ctx context.Context, tx *sql.Tx, ownerID int64, kind, accountID string, updates map[string]any) (map[string]any, error) {
	settings := updates
	if kind == "ai_reply" {
		// The account switch submits only ai_enabled. Preserve all other fields,
		// including credentials and prompts, when applying a partial AI update.
		settings = map[string]any{}
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT data FROM resource_records WHERE owner_id=? AND kind=? AND account_id=? ORDER BY id DESC LIMIT 1`, ownerID, kind, accountID).Scan(&raw)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil {
			if err = json.Unmarshal([]byte(raw), &settings); err != nil {
				return nil, err
			}
			if settings == nil {
				settings = map[string]any{}
			}
		}
		for key, value := range updates {
			settings[key] = value
		}
		if enabled, ok := updates["enabled"]; ok {
			if _, canonical := updates["ai_enabled"]; !canonical {
				settings["ai_enabled"] = enabled
			}
		}
	}
	payload, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM resource_records WHERE owner_id=? AND kind=? AND account_id=?`, ownerID, kind, accountID); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO resource_records(owner_id,kind,account_id,data) VALUES(?,?,?,?)`, ownerID, kind, accountID, string(payload))
	return settings, err
}

func (s *Server) accountConfigDelete(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := current(r)
		res, e := s.db.ExecContext(r.Context(), `DELETE FROM resource_records WHERE owner_id=? AND kind=? AND account_id=?`, u.ID, kind, chi.URLParam(r, "accountID"))
		affected(w, res, e, "配置删除成功")
	}
}

func (s *Server) bulkAccountConfigs(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := current(r)
		var settings map[string]map[string]any
		if decode(r, &settings) != nil || len(settings) == 0 {
			fail(w, http.StatusBadRequest, "请至少填写一个修改项")
			return
		}
		conn, err := s.db.Conn(r.Context())
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer conn.Close()
		tx, err := conn.BeginTx(r.Context(), nil)
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback()
		updated := 0
		for accountID, setting := range settings {
			var owned int
			if err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM xy_accounts WHERE owner_id=? AND account_id=?`, u.ID, accountID).Scan(&owned); err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			if owned == 0 || len(setting) == 0 {
				continue
			}
			if _, err = saveAccountConfig(r.Context(), tx, u.ID, kind, accountID, setting); err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			updated++
		}
		if updated == 0 {
			write(w, http.StatusOK, response{Success: false, Message: "未找到任何可更新的账号"})
			return
		}
		if err = tx.Commit(); err != nil {
			_, _ = conn.ExecContext(context.WithoutCancel(r.Context()), "ROLLBACK")
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		ok(w, fmt.Sprintf("已更新 %d 个账号的AI回复设置", updated), nil)
	}
}
