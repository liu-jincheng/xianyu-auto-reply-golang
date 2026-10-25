package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/zhinianboke/xianyu-auto-go/internal/database"
)

func TestItemListsUsePlatformTimesAndKeepLocalTimesSeparate(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "items.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO xy_catalog_items(owner_id,account_id,item_id,title,status,data,created_at,updated_at) VALUES
	 (1,'a','complete','商品','1','{"item_status_desc":"卖掉了","platform_created_at":"2026-04-14T06:56:26Z","platform_updated_at":"2026-04-15T08:00:00Z"}','2026-10-25 08:00:00','2026-10-25 09:00:00'),
	 (1,'a','no-update','商品','1','{"item_status_desc":"卖掉了","platform_created_at":"2026-04-14T06:56:26Z","created_at":"2026-10-25 08:00:00","updated_at":"2026-10-25 09:00:00"}','2026-10-25 08:00:00','2026-10-25 09:00:00'),
	 (1,'a','unknown','商品','','{}','2026-10-25 08:00:00','2026-10-25 09:00:00')`)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{db: db}
	for _, paged := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/api/v1/items?page=1&page_size=20", nil)
		r = r.WithContext(context.WithValue(r.Context(), userKey{}, User{ID: 1, Role: "MEMBER"}))
		w := httptest.NewRecorder()
		s.itemQuery(w, r, "a", paged)
		var body struct {
			Items []map[string]any `json:"items"`
			Data  []map[string]any `json:"data"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		items := body.Items
		if paged {
			items = body.Data
		}
		if len(items) != 3 {
			t.Fatal(w.Body.String())
		}
		for _, item := range items {
			if item["local_created_at"] != "2026-10-25 08:00:00" || item["local_updated_at"] != "2026-10-25 09:00:00" {
				t.Fatal("local record times were lost", item)
			}
			switch item["item_id"] {
			case "complete":
				if item["created_at"] != "2026-04-14T06:56:26Z" || item["updated_at"] != "2026-04-15T08:00:00Z" || item["item_status_desc"] != "卖掉了" {
					t.Fatal("platform metadata was overwritten by sync times", item)
				}
			case "no-update":
				if item["created_at"] != "2026-04-14T06:56:26Z" || item["updated_at"] != "" {
					t.Fatal("missing modification time was fabricated", item)
				}
			case "unknown":
				if item["created_at"] != "" || item["updated_at"] != "" {
					t.Fatal("local time leaked into platform fields", item)
				}
			}
		}
	}
}
