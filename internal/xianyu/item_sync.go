package xianyu

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Keep one account session across pages, including renewed cookies and the
// ordinary personal page's pagination cursor.
type ItemSyncAccount struct {
	OwnerID                  int64
	AccountID, Cookie, Proxy string
	personal                 bool
	nextPageModel            any
	nextPageNum              any
}

type ItemSyncPage struct {
	Items   []map[string]any
	Fetched int
	HasMore bool
}

func SyncItemPage(ctx context.Context, db *sql.DB, call func(context.Context, MTOPOptions) MTOPResult, a *ItemSyncAccount, page int) (ItemSyncPage, error) {
	out := ItemSyncPage{Items: []map[string]any{}}
	if call == nil {
		call = MTOPCall
	}
	result, err := callItemList(ctx, db, call, a, page)
	if err != nil {
		return out, err
	}
	// Ordinary accounts can use their own personal page, but cannot access
	// COMMONPRO. Do not retry authentication, risk-control or network failures
	// through a different endpoint.
	if !result.Success && !a.personal && strings.Contains(result.Error, "FAIL_BIZ_IDLE_USER_UNAUTHORIZED") {
		a.personal = true
		result, err = callItemList(ctx, db, call, a, page)
		if err != nil {
			return out, err
		}
	}
	if !result.Success {
		return out, fmt.Errorf("获取商品失败：%s", firstNonempty(result.Error, "闲鱼接口调用失败"))
	}
	items, more, err := parseItemList(result.Response, a.personal)
	if err != nil {
		return out, err
	}
	out.Fetched, out.HasMore = len(items), more
	if a.personal {
		data := orderMap(result.Response, "data")
		a.nextPageModel, a.nextPageNum = data["nextPageModel"], data["nextPageNum"]
	}
	for _, item := range items {
		// Personal-page cards may report itemStatus=0 even for sold items.
		// Fetch the authoritative detail only during an explicit sync, never
		// while rendering or paging through the locally saved catalog.
		if a.personal || stringSetting(item, "item_status_desc") == "" || stringSetting(item, "platform_created_at") == "" {
			if err := hydrateItemMetadata(ctx, db, call, a, item); err != nil {
				return out, err
			}
		}
		item["last_synced_at"] = time.Now().UTC().Format(time.RFC3339)
		encoded, err := json.Marshal(item)
		if err != nil {
			return out, fmt.Errorf("商品数据解析失败：%w", err)
		}
		_, err = db.ExecContext(ctx, `INSERT INTO xy_catalog_items(owner_id,account_id,item_id,title,price,status,data) VALUES(?,?,?,?,?,?,?)
		 ON CONFLICT(account_id,item_id) DO UPDATE SET title=excluded.title,price=excluded.price,status=excluded.status,data=json_patch(COALESCE(xy_catalog_items.data,'{}'),excluded.data),updated_at=CURRENT_TIMESTAMP`, a.OwnerID, a.AccountID, item["item_id"], item["title"], item["price"], item["status"], string(encoded))
		if err != nil {
			return out, fmt.Errorf("保存商品 %s 失败：%w", item["item_id"], err)
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func callItemList(ctx context.Context, db *sql.DB, call func(context.Context, MTOPOptions) MTOPResult, a *ItemSyncAccount, page int) (MTOPResult, error) {
	options := MTOPOptions{
		API: "mtop.alibaba.idle.seller.pc.common.item.search", Version: "1.0", Cookie: a.Cookie, Proxy: a.Proxy,
		Origin: "https://seller.goofish.com", Referer: "https://seller.goofish.com/?site=COMMONPRO",
		Data:         map[string]any{"pageNo": page, "pageSize": 30, "bizType": "commonPro", "searchRequest": "{}", "itemStatus": "0"},
		ExtraParams:  map[string]string{"spm_cnt": "a21107h.42826273.0.0", "needLoginPC": "true", "accountSite": "xianyu", "idle_site_biz_code": "COMMONPRO"},
		ExtraHeaders: map[string]string{"idle_site_biz_code": "COMMONPRO", "idle_user_group_member_id": ""},
	}
	if a.personal {
		userID := ParseCookies(a.Cookie)["unb"]
		if userID == "" {
			return MTOPResult{}, fmt.Errorf("账号Cookie缺少用户ID，请重新登录闲鱼账号后重试")
		}
		// Matches goofish.com's personal-page item list. account_id may be a
		// display name or phone number; only the logged-in cookie identifies it.
		options = MTOPOptions{
			API: "mtop.idle.web.xyh.item.list", Version: "1.0", Cookie: a.Cookie, Proxy: a.Proxy,
			Origin: "https://www.goofish.com", Referer: "https://www.goofish.com/personal",
			Data: map[string]any{"userId": userID, "pageNumber": page, "pageSize": 20, "needGroupInfo": page == 1},
		}
		if a.nextPageModel != nil {
			options.Data["nextPageModel"] = a.nextPageModel
		}
		if a.nextPageNum != nil {
			options.Data["nextPageNum"] = a.nextPageNum
		}
	}
	result := call(ctx, options)
	return result, persistItemSyncCookie(ctx, db, a, result.Cookie)
}

func persistItemSyncCookie(ctx context.Context, db *sql.DB, a *ItemSyncAccount, cookie string) error {
	if cookie != "" && cookie != a.Cookie {
		_, err := db.ExecContext(ctx, `UPDATE xy_accounts SET cookie=?,last_refresh_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP WHERE owner_id=? AND account_id=? AND cookie=?`, cookie, a.OwnerID, a.AccountID, a.Cookie)
		if err != nil {
			return fmt.Errorf("保存账号登录状态失败：%w", err)
		}
		if err = db.QueryRowContext(ctx, `SELECT cookie FROM xy_accounts WHERE owner_id=? AND account_id=?`, a.OwnerID, a.AccountID).Scan(&a.Cookie); err != nil {
			return fmt.Errorf("读取账号登录状态失败：%w", err)
		}
	}
	return nil
}

func hydrateItemMetadata(ctx context.Context, db *sql.DB, call func(context.Context, MTOPOptions) MTOPResult, a *ItemSyncAccount, item map[string]any) error {
	id := stringSetting(item, "item_id")
	result := call(ctx, MTOPOptions{
		API: "mtop.taobao.idle.pc.detail", Version: "1.0", Cookie: a.Cookie, Proxy: a.Proxy,
		Origin: "https://www.goofish.com", Referer: "https://www.goofish.com/item?id=" + id,
		Data: map[string]any{"itemId": id},
	})
	if err := persistItemSyncCookie(ctx, db, a, result.Cookie); err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("获取商品 %s 的真实状态和时间失败：%s", id, firstNonempty(result.Error, "闲鱼接口调用失败"))
	}
	detail := orderMap(orderMap(result.Response, "data"), "itemDO")
	if stringSetting(detail, "itemId") != id || stringSetting(detail, "itemStatusStr") == "" {
		return fmt.Errorf("获取商品 %s 的真实状态和时间失败：闲鱼未返回有效商品详情", id)
	}
	applyItemMetadata(item, detail)
	return nil
}

func applyItemMetadata(mapped, item map[string]any) {
	if status := stringSetting(item, "itemStatus"); status != "" {
		mapped["status"] = status
	}
	if description := stringSetting(item, "itemStatusStr", "itemStatusDesc"); description != "" {
		mapped["item_status_desc"] = description
	}
	for _, field := range []struct{ to, numeric, formatted string }{
		{"platform_created_at", "gmtCreate", "GMT_CREATE_DATE_KEY"},
		{"platform_updated_at", "gmtModified", "GMT_MODIFIED_DATE_KEY"},
		{"item_shelf_time", "shelfTime", "shelfTimeStr"},
	} {
		value := platformItemTime(stringSetting(item, field.numeric))
		if value == "" {
			value = platformItemTime(stringSetting(item, field.formatted))
		}
		if value != "" {
			mapped[field.to] = value
		}
	}
	if quantity, exists := item["quantity"]; exists && quantity != nil {
		mapped["quantity"], mapped["item_quantity"] = quantity, quantity
	}
}

// Numeric platform timestamps are Unix seconds or milliseconds. Unzoned
// platform wall times are Beijing time, whereas local SQLite times are UTC.
func platformItemTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if stamp, err := strconv.ParseInt(value, 10, 64); err == nil {
		if stamp <= 0 {
			return ""
		}
		var parsed time.Time
		switch len(value) {
		case 10:
			parsed = time.Unix(stamp, 0)
		case 13:
			parsed = time.UnixMilli(stamp)
		default:
			return ""
		}
		return parsed.UTC().Format(time.RFC3339)
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, time.FixedZone("Asia/Shanghai", 8*60*60)); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func parseItemList(response map[string]any, personal bool) ([]map[string]any, bool, error) {
	data := orderMap(response, "data")
	listKey, moreKey := "itemSearchResponseList", "hasNextPage"
	if personal {
		listKey, moreKey = "cardList", "nextPage"
	} else if nested, ok := data["data"].(map[string]any); ok {
		data = nested
	}
	if data["success"] == false || data["isSuccess"] == false {
		return nil, false, fmt.Errorf("获取商品失败：%s", firstNonempty(stringSetting(data, "message", "msg", "errorMsg"), "闲鱼返回业务失败"))
	}
	value, exists := data[listKey]
	raw, valid := value.([]any)
	if !exists || (!valid && value != nil) {
		return nil, false, fmt.Errorf("闲鱼商品响应格式异常：缺少有效的商品列表")
	}
	more, err := strconv.ParseBool(stringSetting(data, moreKey))
	if err != nil {
		return nil, false, fmt.Errorf("闲鱼商品响应格式异常：缺少有效的分页状态")
	}
	if len(raw) == 0 && more {
		return nil, false, fmt.Errorf("闲鱼商品响应异常：商品列表为空但仍有下一页，请稍后重试")
	}
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		if personal {
			item, ok = item["cardData"].(map[string]any)
		}
		id := stringSetting(item, "itemId", "id")
		if !ok || id == "" {
			return nil, false, fmt.Errorf("闲鱼商品响应格式异常：商品缺少有效的ID")
		}
		mapped := map[string]any{"item_id": id, "id": id, "title": stringSetting(item, "title"), "price": stringSetting(item, "reservePrice"), "status": stringSetting(item, "itemStatus"), "source": "seller", "image_url": stringSetting(item, "itemImageUrl")}
		if personal {
			mapped["source"] = "personal"
			mapped["price"] = stringSetting(orderMap(item, "priceInfo"), "price")
			mapped["image_url"] = stringSetting(orderMap(item, "picInfo"), "picUrl")
		} else {
			for _, field := range []struct{ from, to string }{{"quantity", "quantity"}, {"idleItemSkuList", "idle_item_sku_list"}} {
				if value, exists := item[field.from]; exists {
					mapped[field.to] = value
				}
			}
		}
		applyItemMetadata(mapped, item)
		out = append(out, mapped)
	}
	return out, more, nil
}
