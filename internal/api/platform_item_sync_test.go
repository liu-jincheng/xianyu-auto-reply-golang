package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhinianboke/xianyu-auto-go/internal/database"
	"github.com/zhinianboke/xianyu-auto-go/internal/xianyu"
)

func TestFetchAllItemsReportsActualOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, mode, role, payload, message string
		accounts, succeeded, failed, saved int
		success                            bool
	}{
		{name: "all succeeded", mode: "success", accounts: 2, succeeded: 2, saved: 2, success: true, message: "商品同步成功"},
		{name: "all failed", mode: "failure", accounts: 2, failed: 2, message: "商品同步失败"},
		{name: "mixed accounts", mode: "mixed", accounts: 2, succeeded: 1, failed: 1, saved: 1, message: "商品同步部分成功"},
		{name: "later page failed", mode: "later", accounts: 1, failed: 1, saved: 1, message: "商品同步部分成功"},
		{name: "no accounts", message: "没有可同步的账号"},
		{name: "member cannot sync another owner", mode: "success", role: "MEMBER", payload: `{"cookie_id":"b"}`, message: "没有可同步的账号"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := database.Open(filepath.Join(t.TempDir(), "items.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if tc.mode != "" {
				if _, err = db.Exec(`INSERT INTO xy_accounts(owner_id,account_id,cookie) VALUES(1,'a','a'),(2,'b','b');
				 INSERT INTO xy_accounts(owner_id,account_id,cookie,status) VALUES(1,'disabled','disabled','inactive')`); err != nil {
					t.Fatal(err)
				}
			}
			if tc.mode == "later" {
				_, _ = db.Exec(`DELETE FROM xy_accounts WHERE account_id='b'`)
			}
			s := &Server{db: db}
			calls := 0
			s.itemMTOPCall = func(_ context.Context, opt xianyu.MTOPOptions) xianyu.MTOPResult {
				calls++
				if opt.Cookie == "disabled" || tc.accounts == 0 {
					t.Fatal("fetched an inaccessible or disabled account")
				}
				if tc.mode == "failure" || (tc.mode == "mixed" && opt.Cookie == "b") || (tc.mode == "later" && opt.Data["pageNo"] == 2) {
					return xianyu.MTOPResult{Error: "FAIL_SYS_SESSION_EXPIRED::登录过期"}
				}
				return xianyu.MTOPResult{Success: true, Response: map[string]any{"data": map[string]any{"data": map[string]any{
					"itemSearchResponseList": []any{map[string]any{"itemId": "item-" + opt.Cookie, "title": "商品", "itemStatusDesc": "在售", "gmtCreate": "1776149786000"}}, "hasNextPage": tc.mode == "later",
				}}}}
			}
			role, payload := tc.role, tc.payload
			if role == "" {
				role = "ADMIN"
			}
			if payload == "" {
				payload = `{}`
			}
			r := httptest.NewRequest("POST", "/api/v1/items/get-all-from-account", strings.NewReader(payload))
			r = r.WithContext(context.WithValue(r.Context(), userKey{}, User{ID: 1, Role: role}))
			w := httptest.NewRecorder()
			s.fetchItemsAll(w, r)
			var body struct {
				Success   bool     `json:"success"`
				Message   string   `json:"message"`
				Accounts  int      `json:"account_count"`
				Succeeded int      `json:"success_account_count"`
				Failed    int      `json:"failed_account_count"`
				Saved     int      `json:"saved_count"`
				Failures  []string `json:"failed_accounts"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 || body.Success != tc.success || body.Accounts != tc.accounts || body.Succeeded != tc.succeeded || body.Failed != tc.failed || body.Saved != tc.saved || !strings.Contains(body.Message, tc.message) {
				t.Fatalf("unexpected summary: %s, %v", w.Body.String(), err)
			}
			if body.Failed > 0 && (len(body.Failures) != body.Failed || !strings.Contains(body.Message, "登录过期")) {
				t.Fatal("lost the actual failure", body)
			}
			var count int
			if err = db.QueryRow(`SELECT count(*) FROM xy_catalog_items`).Scan(&count); err != nil || count != tc.saved || (tc.accounts == 0 && calls != 0) {
				t.Fatal(count, calls, err)
			}
		})
	}
}

func TestFetchItemsRejectsMalformedRequest(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.fetchItemsAll(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"cookie_id":`)))
	if !strings.Contains(w.Body.String(), "请求格式错误") || strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatal(w.Body.String())
	}
}

func TestFetchItemPageReplaysPersonalCursor(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "items.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO xy_accounts(owner_id,account_id,cookie) VALUES(1,'a','unb=123')`); err != nil {
		t.Fatal(err)
	}
	s := &Server{db: db}
	calls := 0
	detailCalls := 0
	s.itemMTOPCall = func(_ context.Context, opt xianyu.MTOPOptions) xianyu.MTOPResult {
		if opt.API == "mtop.taobao.idle.pc.detail" {
			detailCalls++
			return xianyu.MTOPResult{Success: true, Response: map[string]any{"data": map[string]any{"itemDO": map[string]any{
				"itemId": opt.Data["itemId"], "itemStatus": "1", "itemStatusStr": "卖掉了", "gmtCreate": "1776149786000",
			}}}}
		}
		calls++
		if calls == 1 {
			return xianyu.MTOPResult{Error: "FAIL_BIZ_IDLE_USER_UNAUTHORIZED::无权限访问"}
		}
		page := calls - 1
		if opt.Data["pageNumber"] != page || (page == 2 && opt.Data["nextPageModel"] != "cursor") {
			t.Fatal("invalid cursor", opt.Data)
		}
		return xianyu.MTOPResult{Success: true, Response: map[string]any{"data": map[string]any{
			"cardList": []any{map[string]any{"cardData": map[string]any{"id": fmt.Sprint(page), "title": "商品"}}},
			"nextPage": page == 1, "nextPageModel": "cursor",
		}}}
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"cookie_id":"a","page":2}`))
	r = r.WithContext(context.WithValue(r.Context(), userKey{}, User{ID: 1, Role: "MEMBER"}))
	w := httptest.NewRecorder()
	s.fetchItemsPage(w, r)
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil || !body.Success || calls != 3 || detailCalls != 2 || len(body.Data.Items) != 1 || body.Data.Items[0]["item_id"] != "2" {
		t.Fatalf("unexpected page: %s, %v", w.Body.String(), err)
	}
}
