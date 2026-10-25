package scheduler

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zhinianboke/xianyu-auto-go/internal/database"
	"github.com/zhinianboke/xianyu-auto-go/internal/xianyu"
)

func TestScheduledItemSyncSupportsPersonalAccount(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "items.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO xy_accounts(owner_id,account_id,cookie) VALUES(1,'a','unb=123')`); err != nil {
		t.Fatal(err)
	}
	e := &Engine{db: db}
	calls := 0
	e.mtopCall = func(_ context.Context, opt xianyu.MTOPOptions) xianyu.MTOPResult {
		calls++
		if opt.API == "mtop.taobao.idle.pc.detail" {
			return xianyu.MTOPResult{Success: true, Response: map[string]any{"data": map[string]any{"itemDO": map[string]any{
				"itemId": "item", "itemStatus": "1", "itemStatusStr": "卖掉了", "gmtCreate": "1776149786000",
			}}}}
		}
		if calls == 1 {
			return xianyu.MTOPResult{Error: "FAIL_BIZ_IDLE_USER_UNAUTHORIZED::无权限访问"}
		}
		if opt.API != "mtop.idle.web.xyh.item.list" || opt.Data["userId"] != "123" {
			t.Fatal("scheduler did not use the personal route", opt)
		}
		return xianyu.MTOPResult{Success: true, Response: map[string]any{"data": map[string]any{
			"cardList": []any{map[string]any{"cardData": map[string]any{"id": "item", "title": "商品"}}}, "nextPage": false,
		}}}
	}
	if err = e.fetchItems(context.Background(), nil); err != nil || calls != 3 {
		t.Fatal(calls, err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM xy_catalog_items WHERE account_id='a' AND item_id='item'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
