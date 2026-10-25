package xianyu

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhinianboke/xianyu-auto-go/internal/database"
)

func personalItemResponse(id string, more bool) map[string]any {
	return map[string]any{"data": map[string]any{
		"cardList": []any{map[string]any{"cardData": map[string]any{
			"id": json.Number(id), "title": "普通账号商品", "itemStatus": json.Number("0"),
			"priceInfo": map[string]any{"price": "12.50"}, "picInfo": map[string]any{"picUrl": "https://example.com/item.jpg"},
		}}}, "nextPage": more,
	}}
}

func TestItemSyncPersonalFallbackCarriesCookieAndCursor(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "items.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO xy_accounts(owner_id,account_id,cookie) VALUES(1,'display-name','unb=123; token=old');
	 INSERT INTO xy_catalog_items(owner_id,account_id,item_id,title,data) VALUES(1,'display-name','9000000000000000001','old','{"is_polished":true,"quantity":9}')`); err != nil {
		t.Fatal(err)
	}
	a := ItemSyncAccount{OwnerID: 1, AccountID: "display-name", Cookie: "unb=123; token=old", Proxy: "http://localhost:9999"}
	calls := 0
	call := func(_ context.Context, opt MTOPOptions) MTOPResult {
		calls++
		if opt.Proxy != a.Proxy {
			t.Fatal("lost account proxy")
		}
		switch calls {
		case 1:
			if opt.API != "mtop.alibaba.idle.seller.pc.common.item.search" || opt.Data["pageNo"] != 1 {
				t.Fatalf("unexpected seller request: %+v", opt)
			}
			return MTOPResult{Error: "FAIL_BIZ_IDLE_USER_UNAUTHORIZED::无权限访问", Cookie: "unb=123; token=seller"}
		case 2:
			if opt.API != "mtop.idle.web.xyh.item.list" || opt.Data["userId"] != "123" || opt.Data["pageNumber"] != 1 || opt.Data["needGroupInfo"] != true || opt.Cookie != "unb=123; token=seller" || opt.Origin != "https://www.goofish.com" || len(opt.ExtraHeaders) != 0 {
				t.Fatalf("unexpected personal request: %+v", opt)
			}
			response := personalItemResponse("9000000000000000001", true)
			data := orderMap(response, "data")
			data["nextPageModel"], data["nextPageNum"] = "opaque-cursor", json.Number("2")
			return MTOPResult{Success: true, Cookie: "unb=123; token=personal", Response: response}
		case 3:
			if opt.API != "mtop.taobao.idle.pc.detail" || opt.Cookie != "unb=123; token=personal" || opt.Data["itemId"] != "9000000000000000001" {
				t.Fatalf("unexpected detail request: %+v", opt)
			}
			return MTOPResult{Success: true, Cookie: "unb=123; token=detail", Response: map[string]any{"data": map[string]any{"itemDO": map[string]any{
				"itemId": "9000000000000000001", "itemStatus": json.Number("1"), "itemStatusStr": "卖掉了", "gmtCreate": json.Number("1776149786000"),
			}}}}
		case 4:
			if opt.API != "mtop.idle.web.xyh.item.list" || opt.Cookie != "unb=123; token=detail" || opt.Data["pageNumber"] != 2 || opt.Data["needGroupInfo"] != false || opt.Data["nextPageModel"] != "opaque-cursor" || opt.Data["nextPageNum"] != json.Number("2") {
				t.Fatalf("pagination lost route, cookie or cursor: %+v", opt)
			}
			return MTOPResult{Success: true, Response: personalItemResponse("9000000000000000002", false)}
		case 5:
			if opt.API != "mtop.taobao.idle.pc.detail" || opt.Cookie != "unb=123; token=detail" || opt.Data["itemId"] != "9000000000000000002" {
				t.Fatalf("unexpected second detail request: %+v", opt)
			}
			return MTOPResult{Success: true, Response: map[string]any{"data": map[string]any{"itemDO": map[string]any{
				"itemId": "9000000000000000002", "itemStatus": "1", "itemStatusStr": "卖掉了", "GMT_CREATE_DATE_KEY": "2026-04-14 14:56:26",
			}}}}
		default:
			t.Fatal("unexpected request")
			return MTOPResult{}
		}
	}
	for page := 1; page <= 2; page++ {
		result, err := SyncItemPage(context.Background(), db, call, &a, page)
		if err != nil || result.Fetched != 1 || len(result.Items) != 1 || result.HasMore != (page == 1) {
			t.Fatalf("unexpected page %d: %+v, %v", page, result, err)
		}
	}
	var count, polished, quantity int
	var cookie, price, image, source string
	if err = db.QueryRow(`SELECT count(*) FROM xy_catalog_items`).Scan(&count); err != nil || count != 2 || calls != 5 {
		t.Fatal(count, calls, err)
	}
	if err = db.QueryRow(`SELECT price,json_extract(data,'$.image_url'),json_extract(data,'$.source'),json_extract(data,'$.is_polished'),json_extract(data,'$.quantity') FROM xy_catalog_items WHERE item_id='9000000000000000001'`).Scan(&price, &image, &source, &polished, &quantity); err != nil || price != "12.50" || image != "https://example.com/item.jpg" || source != "personal" || polished != 1 || quantity != 9 {
		t.Fatal(price, image, source, polished, quantity, err)
	}
	if err = db.QueryRow(`SELECT cookie FROM xy_accounts`).Scan(&cookie); err != nil || cookie != a.Cookie {
		t.Fatal("renewed cookie not saved", err)
	}
	var status, statusText, created string
	var updated any
	if err = db.QueryRow(`SELECT status,json_extract(data,'$.item_status_desc'),json_extract(data,'$.platform_created_at'),json_extract(data,'$.platform_updated_at') FROM xy_catalog_items WHERE item_id='9000000000000000001'`).Scan(&status, &statusText, &created, &updated); err != nil || status != "1" || statusText != "卖掉了" || created != "2026-04-14T06:56:26Z" || updated != nil {
		t.Fatal("incorrect authoritative metadata", status, statusText, created, updated, err)
	}
}

func TestItemSyncSellerPaginationUsesRenewedCookie(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "items.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO xy_accounts(owner_id,account_id,cookie) VALUES(1,'seller','old')`); err != nil {
		t.Fatal(err)
	}
	a := ItemSyncAccount{OwnerID: 1, AccountID: "seller", Cookie: "old"}
	calls := 0
	call := func(_ context.Context, opt MTOPOptions) MTOPResult {
		calls++
		if opt.API != "mtop.alibaba.idle.seller.pc.common.item.search" || opt.Data["pageNo"] != calls || opt.ExtraHeaders["idle_site_biz_code"] != "COMMONPRO" || (calls == 2 && opt.Cookie != "new") {
			t.Fatalf("unexpected seller request: %+v", opt)
		}
		return MTOPResult{Success: true, Cookie: "new", Response: map[string]any{"data": map[string]any{"data": map[string]any{
			"itemSearchResponseList": []any{map[string]any{"itemId": json.Number("9000000000000000001"), "title": "卖家商品", "reservePrice": "8.88", "quantity": json.Number("5"), "itemStatus": "0", "itemStatusDesc": "在售", "gmtCreate": json.Number("1776149786000")}}, "hasNextPage": calls == 1,
		}}}}
	}
	for page := 1; page <= 2; page++ {
		result, err := SyncItemPage(context.Background(), db, call, &a, page)
		if err != nil || result.Fetched != 1 || len(result.Items) != 1 || result.Items[0]["item_id"] != "9000000000000000001" || result.Items[0]["quantity"] != json.Number("5") || result.HasMore != (page == 1) {
			t.Fatal(result, err)
		}
	}
}

func TestItemSyncReportsSaveFailureAndRetainsProgress(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "items.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER reject_item BEFORE INSERT ON xy_catalog_items WHEN NEW.item_id='bad' BEGIN SELECT RAISE(FAIL,'cannot save'); END`); err != nil {
		t.Fatal(err)
	}
	call := func(context.Context, MTOPOptions) MTOPResult {
		items := []any{}
		for _, id := range []string{"good", "bad"} {
			items = append(items, map[string]any{"itemId": id, "itemStatusDesc": "在售", "gmtCreate": "1776149786000"})
		}
		return MTOPResult{Success: true, Response: map[string]any{"data": map[string]any{"data": map[string]any{
			"itemSearchResponseList": items, "hasNextPage": false,
		}}}}
	}
	result, err := SyncItemPage(context.Background(), db, call, &ItemSyncAccount{OwnerID: 1, AccountID: "a"}, 1)
	if err == nil || !strings.Contains(err.Error(), "保存商品 bad 失败") || result.Fetched != 2 || len(result.Items) != 1 {
		t.Fatal(result, err)
	}
}

func TestItemSyncDoesNotFallbackOnLoginOrNetworkFailure(t *testing.T) {
	for _, message := range []string{"FAIL_SYS_SESSION_EXPIRED::登录过期", "context deadline exceeded"} {
		t.Run(message, func(t *testing.T) {
			calls := 0
			call := func(context.Context, MTOPOptions) MTOPResult {
				calls++
				return MTOPResult{Error: message}
			}
			_, err := SyncItemPage(context.Background(), nil, call, &ItemSyncAccount{}, 1)
			if err == nil || !strings.Contains(err.Error(), message) || calls != 1 {
				t.Fatal(calls, err)
			}
		})
	}
}

func TestParseItemListRejectsMalformedResponses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     map[string]any
		personal bool
	}{
		{"missing list", map[string]any{"hasNextPage": false}, false},
		{"wrong list type", map[string]any{"itemSearchResponseList": "bad", "hasNextPage": false}, false},
		{"missing pagination", map[string]any{"itemSearchResponseList": []any{}}, false},
		{"missing id", map[string]any{"itemSearchResponseList": []any{map[string]any{"title": "invalid"}}, "hasNextPage": false}, false},
		{"business failure", map[string]any{"success": false, "message": "无权限", "itemSearchResponseList": []any{}, "hasNextPage": false}, false},
		{"null personal card", map[string]any{"cardList": []any{nil}, "nextPage": false}, true},
		{"empty unfinished page", map[string]any{"cardList": []any{}, "nextPage": true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parseItemList(map[string]any{"data": tc.data}, tc.personal); err == nil {
				t.Fatal("malformed response reported as successful")
			}
		})
	}
	for _, personal := range []bool{false, true} {
		data := map[string]any{"itemSearchResponseList": []any{}, "hasNextPage": false}
		if personal {
			data = map[string]any{"cardList": []any{}, "nextPage": false}
		}
		if items, more, err := parseItemList(map[string]any{"data": data}, personal); err != nil || more || len(items) != 0 {
			t.Fatal("valid empty account rejected", items, more, err)
		}
	}
}

func TestPlatformItemTimeUsesCorrectUnitsAndTimezone(t *testing.T) {
	for _, raw := range []string{"1776149786", "1776149786000", "2026-04-14 14:56:26", "2026-04-14T14:56:26", "2026-04-14T14:56:26+08:00", "2026-04-14T06:56:26Z"} {
		if got := platformItemTime(raw); got != "2026-04-14T06:56:26Z" {
			t.Fatalf("%q parsed as %q", raw, got)
		}
	}
	for _, raw := range []string{"", "0", "-1", "123", "invalid", "<nil>"} {
		if got := platformItemTime(raw); got != "" {
			t.Fatalf("invalid platform timestamp %q became %q", raw, got)
		}
	}
}

func TestItemMetadataMapsRealFieldsWithoutInventingTimes(t *testing.T) {
	item := map[string]any{}
	applyItemMetadata(item, map[string]any{
		"itemStatus": json.Number("1"), "itemStatusStr": "卖掉了", "quantity": json.Number("1"),
		"gmtCreate": "0", "GMT_CREATE_DATE_KEY": "2026-04-14 14:56:26",
		"gmtModified": "2026-04-15 16:00:00", "shelfTimeStr": "2026-04-14 15:00:00",
	})
	if item["status"] != "1" || item["item_status_desc"] != "卖掉了" || item["platform_created_at"] != "2026-04-14T06:56:26Z" || item["platform_updated_at"] != "2026-04-15T08:00:00Z" || item["item_shelf_time"] != "2026-04-14T07:00:00Z" || item["item_quantity"] != json.Number("1") {
		t.Fatal(item)
	}
	unknown := map[string]any{}
	applyItemMetadata(unknown, map[string]any{"gmtCreate": "1776149786000"})
	if _, exists := unknown["platform_updated_at"]; exists {
		t.Fatal("created time substituted for modification time", unknown)
	}
	if _, exists := unknown["item_shelf_time"]; exists {
		t.Fatal("created time substituted for shelf time", unknown)
	}
}

func TestItemSyncDoesNotOverwriteSoldStatusWhenDetailFails(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "items.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO xy_catalog_items(owner_id,account_id,item_id,status,data) VALUES(1,'a','9000000000000000001','1','{"item_status_desc":"卖掉了","platform_created_at":"2026-04-14T06:56:26Z"}')`)
	if err != nil {
		t.Fatal(err)
	}
	a := ItemSyncAccount{OwnerID: 1, AccountID: "a", Cookie: "unb=123", personal: true}
	calls := 0
	call := func(_ context.Context, opt MTOPOptions) MTOPResult {
		calls++
		if opt.API == "mtop.idle.web.xyh.item.list" {
			return MTOPResult{Success: true, Response: personalItemResponse("9000000000000000001", false)}
		}
		if opt.API != "mtop.taobao.idle.pc.detail" {
			t.Fatal("unexpected fallback", opt.API)
		}
		return MTOPResult{Error: "FAIL_SYS_SESSION_EXPIRED::登录过期"}
	}
	result, err := SyncItemPage(context.Background(), db, call, &a, 1)
	if err == nil || !strings.Contains(err.Error(), "真实状态和时间失败") || result.Fetched != 1 || len(result.Items) != 0 || calls != 2 {
		t.Fatal(result, calls, err)
	}
	var status, description, created string
	if err = db.QueryRow(`SELECT status,json_extract(data,'$.item_status_desc'),json_extract(data,'$.platform_created_at') FROM xy_catalog_items`).Scan(&status, &description, &created); err != nil || status != "1" || description != "卖掉了" || created != "2026-04-14T06:56:26Z" {
		t.Fatal("a personal-page placeholder overwrote known metadata", status, description, created, err)
	}
}
