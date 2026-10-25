package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/zhinianboke/xianyu-auto-go/internal/database"
)

func aiSettingsRequest(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Route("/ai-reply-settings", func(r chi.Router) {
		s.accountConfigRoutes(r, "ai_reply", map[string]any{"ai_enabled": false})
		r.Put("/", s.bulkAccountConfigs("ai_reply"))
	})
	router.Put("/ai-reply-settings", s.bulkAccountConfigs("ai_reply"))
	router.Get("/cookies/details", s.accountList)
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), userKey{}, User{ID: 1, Role: "MEMBER"}))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func assertAISettings(t *testing.T, s *Server, want map[string]any) {
	t.Helper()
	w := aiSettingsRequest(t, s, http.MethodGet, "/ai-reply-settings/a", "")
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK {
		t.Fatalf("read settings: status=%d err=%v", w.Code, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("settings changed beyond the submitted fields")
	}
}

func TestAIReplySettingsPreserveFieldsAcrossToggleAndRestart(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "bulk"}[bulk], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ai.db")
			db, err := database.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { db.Close() }()
			if _, err = db.Exec(`INSERT INTO xy_accounts(owner_id,account_id,cookie) VALUES(1,'a','cookie'),(1,'b','cookie'),(2,'c','cookie')`); err != nil {
				t.Fatal(err)
			}
			s := &Server{db: db}
			payload := `{"ai_enabled":false,"provider_type":"openai_compatible","base_url":"https://example.invalid/v1","api_key":"test-key","model_name":"test-model","custom_prompts":"test prompt","max_discount_percent":15,"max_discount_amount":50,"max_bargain_rounds":4,"ai_time_range_start":"22:00","ai_time_range_end":"06:00","manual_reply_ai_pause_enabled":true,"manual_reply_ai_pause_minutes":20}`
			var want map[string]any
			if err = json.Unmarshal([]byte(payload), &want); err != nil {
				t.Fatal(err)
			}
			w := aiSettingsRequest(t, s, http.MethodPut, "/ai-reply-settings/a", payload)
			if w.Code != http.StatusOK {
				t.Fatalf("save: status=%d", w.Code)
			}
			assertAISettings(t, s, want)
			for _, patch := range []string{`{"ai_enabled":true}`, `{"ai_enabled":false}`, `{"ai_enabled":true}`, `{"custom_prompts":"","max_discount_percent":0,"manual_reply_ai_pause_enabled":false}`} {
				var updates map[string]any
				if err = json.Unmarshal([]byte(patch), &updates); err != nil {
					t.Fatal(err)
				}
				for key, value := range updates {
					want[key] = value
				}
				url := "/ai-reply-settings/a"
				if bulk {
					url = "/ai-reply-settings"
					patch = `{"a":` + patch + `,"c":{"ai_enabled":true}}`
				}
				w = aiSettingsRequest(t, s, http.MethodPut, url, patch)
				var result response
				if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || !result.Success {
					t.Fatalf("update: status=%d err=%v", w.Code, err)
				}
				assertAISettings(t, s, want)
				for _, filter := range []string{"", "&ai_reply=true", "&ai_reply=false"} {
					w = aiSettingsRequest(t, s, http.MethodGet, "/cookies/details?page=1"+filter, "")
					var list struct {
						Total int `json:"total"`
						Data  []struct {
							ID      string `json:"id"`
							Enabled bool   `json:"ai_enabled"`
						} `json:"data"`
					}
					if err = json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != http.StatusOK {
						t.Fatalf("account list: status=%d err=%v", w.Code, err)
					}
					wantTotal := 2
					if filter == "&ai_reply=true" {
						wantTotal = 0
						if want["ai_enabled"] == true {
							wantTotal = 1
						}
					} else if filter == "&ai_reply=false" && want["ai_enabled"] == true {
						wantTotal = 1
					}
					if list.Total != wantTotal || len(list.Data) != wantTotal {
						t.Fatalf("filter %q: total=%d rows=%d want=%d", filter, list.Total, len(list.Data), wantTotal)
					}
					for _, account := range list.Data {
						if account.Enabled != (account.ID == "a" && want["ai_enabled"] == true) {
							t.Fatal("account list returned a stale AI switch state")
						}
					}
				}
			}
			var otherConfigs int
			if err = db.QueryRow(`SELECT count(*) FROM resource_records WHERE kind='ai_reply' AND account_id<>'a'`).Scan(&otherConfigs); err != nil || otherConfigs != 0 {
				t.Fatalf("unexpected settings for other accounts: %d err=%v", otherConfigs, err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = database.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			s.db = db
			assertAISettings(t, s, want)
			w = aiSettingsRequest(t, s, http.MethodGet, "/ai-reply-settings/", "")
			var all map[string]map[string]any
			if err = json.Unmarshal(w.Body.Bytes(), &all); err != nil || !reflect.DeepEqual(all["a"], want) {
				t.Fatal("collection settings disagree with persisted account settings")
			}
		})
	}
}

func TestAIReplySettingsSaveFailureRollsBack(t *testing.T) {
	for _, failure := range []string{"delete", "insert", "commit"} {
		for _, bulk := range []bool{false, true} {
			t.Run(failure+map[bool]string{false: "/single", true: "/bulk"}[bulk], func(t *testing.T) {
				db, err := database.Open(filepath.Join(t.TempDir(), "ai.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err = db.Exec(`INSERT INTO xy_accounts(owner_id,account_id,cookie) VALUES(1,'a','cookie'); INSERT INTO resource_records(owner_id,kind,account_id,data) VALUES(1,'ai_reply','a','{"ai_enabled":false,"api_key":"test-key"}')`); err != nil {
					t.Fatal(err)
				}
				trigger := `CREATE TRIGGER fail_save BEFORE ` + strings.ToUpper(failure) + ` ON resource_records BEGIN SELECT RAISE(ABORT,'save failed'); END`
				if failure == "commit" {
					trigger = `CREATE TABLE save_parent(id INTEGER PRIMARY KEY); CREATE TABLE save_child(parent_id INTEGER REFERENCES save_parent(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fail_save AFTER INSERT ON resource_records BEGIN INSERT INTO save_child(parent_id) VALUES(999); END`
				}
				if _, err = db.Exec(trigger); err != nil {
					t.Fatal(err)
				}
				s := &Server{db: db}
				url, patch := "/ai-reply-settings/a", `{"ai_enabled":true}`
				if bulk {
					url, patch = "/ai-reply-settings", `{"a":{"ai_enabled":true}}`
				}
				w := aiSettingsRequest(t, s, http.MethodPut, url, patch)
				var result response
				if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusInternalServerError || result.Success {
					t.Fatalf("save failure reported incorrectly: status=%d success=%v err=%v", w.Code, result.Success, err)
				}
				assertAISettings(t, s, map[string]any{"ai_enabled": false, "api_key": "test-key"})
			})
		}
	}
}

func TestAIReplyAccountListUsesLatestOwnedSettings(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		INSERT INTO xy_accounts(owner_id,account_id,cookie,metadata) VALUES
		(1,'a','cookie','{"ai_reply_settings":{"ai_enabled":true}}'),
		(1,'b','cookie','{"ai_reply_settings":{"ai_enabled":true}}'),
		(1,'c','cookie','{}'),(1,'d','cookie','{}'),(1,'e','cookie','{}'),(1,'f','cookie','{}'),(2,'g','cookie','{}');
		INSERT INTO resource_records(owner_id,kind,account_id,enabled,data) VALUES
		(1,'ai_reply','a',1,'{"ai_enabled":true}'),
		(1,'ai_reply','a',1,'{"ai_enabled":false}'),
		(2,'ai_reply','c',1,'{"ai_enabled":true}'),
		(1,'ai_reply','d',1,'{"enabled":true}'),
		(1,'ai_reply','e',1,'{"ai_enabled":false,"enabled":true}'),
		(1,'ai_reply','f',0,'{"ai_enabled":true}'),
		(2,'ai_reply','g',1,'{"ai_enabled":true}')`)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{db: db}
	for _, filter := range []string{"", "&ai_reply=true", "&ai_reply=false"} {
		w := aiSettingsRequest(t, s, http.MethodGet, "/cookies/details?page=1"+filter, "")
		var list struct {
			Total int `json:"total"`
			Data  []struct {
				ID      string `json:"id"`
				Enabled bool   `json:"ai_enabled"`
			} `json:"data"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != http.StatusOK {
			t.Fatalf("account list: status=%d err=%v", w.Code, err)
		}
		wantTotal := map[string]int{"": 6, "&ai_reply=true": 1, "&ai_reply=false": 5}[filter]
		if list.Total != wantTotal || len(list.Data) != wantTotal {
			t.Fatalf("filter %q: total=%d rows=%d want=%d", filter, list.Total, len(list.Data), wantTotal)
		}
		for _, account := range list.Data {
			if account.ID == "g" || account.Enabled != (account.ID == "d") {
				t.Fatal("account list did not use latest owned AI settings")
			}
		}
	}
}
