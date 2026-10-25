package api

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/zhinianboke/xianyu-auto-go/internal/proxy"
	"github.com/zhinianboke/xianyu-auto-go/internal/xianyu"
)

type platformAccount struct {
	ID, OwnerID          int64
	AccountID, Cookie    string
	ProxyType, ProxyHost string
	ProxyPort            sql.NullInt64
	ProxyUser, ProxyPass sql.NullString
}

func (s *Server) loadPlatformAccounts(r *http.Request, id string) ([]platformAccount, error) {
	u := current(r)
	q := `SELECT id,owner_id,account_id,cookie,COALESCE(proxy_type,'none'),COALESCE(proxy_host,''),proxy_port,proxy_user,proxy_pass FROM xy_accounts WHERE status='active'`
	args := []any{}
	if u.Role != "ADMIN" {
		q += ` AND owner_id=?`
		args = append(args, u.ID)
	}
	if id != "" {
		q += ` AND account_id=?`
		args = append(args, id)
	}
	rows, e := s.db.QueryContext(r.Context(), q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []platformAccount{}
	for rows.Next() {
		var a platformAccount
		if e = rows.Scan(&a.ID, &a.OwnerID, &a.AccountID, &a.Cookie, &a.ProxyType, &a.ProxyHost, &a.ProxyPort, &a.ProxyUser, &a.ProxyPass); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func accountProxy(a platformAccount) string {
	if proxy.Enabled() {
		return proxy.URL()
	}
	if a.ProxyType == "" || a.ProxyType == "none" || !a.ProxyPort.Valid {
		return ""
	}
	auth := ""
	if a.ProxyUser.Valid && a.ProxyUser.String != "" {
		auth = a.ProxyUser.String + ":" + a.ProxyPass.String + "@"
	}
	return fmt.Sprintf("%s://%s%s:%d", a.ProxyType, auth, a.ProxyHost, a.ProxyPort.Int64)
}
func (s *Server) fetchItemsPage(w http.ResponseWriter, r *http.Request) {
	var p struct {
		CookieID string `json:"cookie_id"`
		Page     int    `json:"page"`
	}
	if decode(r, &p) != nil || p.CookieID == "" {
		fail(w, 400, "cookie_id不能为空")
		return
	}
	if p.Page < 1 {
		p.Page = 1
	}
	if p.Page > 200 {
		fail(w, 400, "商品页码不能超过200")
		return
	}
	accounts, e := s.loadPlatformAccounts(r, p.CookieID)
	if e != nil || len(accounts) == 0 {
		fail(w, 404, "账号不存在或未启用")
		return
	}
	items, more, e := s.fetchSellerItems(r, accounts[0], p.Page)
	if e != nil {
		write(w, 200, response{false, e.Error(), nil})
		return
	}
	ok(w, "商品同步成功", map[string]any{"items": items, "current_count": len(items), "has_more": more, "page": p.Page})
}
func (s *Server) fetchItemsAll(w http.ResponseWriter, r *http.Request) {
	var p struct {
		CookieID string `json:"cookie_id"`
	}
	if decode(r, &p) != nil {
		fail(w, 400, "请求格式错误")
		return
	}
	accounts, e := s.loadPlatformAccounts(r, p.CookieID)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	total, saved, success := 0, 0, 0
	failed := []string{}
	for _, a := range accounts {
		session := xianyu.ItemSyncAccount{OwnerID: a.OwnerID, AccountID: a.AccountID, Cookie: a.Cookie, Proxy: accountProxy(a)}
		accountOK := true
		for page := 1; page <= 200; page++ {
			result, e := xianyu.SyncItemPage(r.Context(), s.db, s.itemMTOPCall, &session, page)
			total += result.Fetched
			saved += len(result.Items)
			if e == nil && page == 200 && result.HasMore {
				e = fmt.Errorf("商品超过单次同步的200页上限，尚未全部获取")
			}
			if e != nil {
				failed = append(failed, a.AccountID+": "+e.Error())
				accountOK = false
				break
			}
			if !result.HasMore {
				break
			}
		}
		if accountOK {
			success++
		}
	}
	message := fmt.Sprintf("商品同步成功：%d 个账号，共获取 %d 件，保存 %d 件", success, total, saved)
	if len(accounts) == 0 {
		message = "没有可同步的账号，请先添加并启用闲鱼账号"
	} else if len(failed) > 0 {
		if success > 0 || saved > 0 {
			message = fmt.Sprintf("商品同步部分成功：%d/%d 个账号成功，保存 %d 件，%d 个账号失败；%s", success, len(accounts), saved, len(failed), failed[0])
		} else {
			message = "商品同步失败：" + failed[0]
		}
	}
	write(w, 200, map[string]any{"success": len(accounts) > 0 && len(failed) == 0, "message": message, "total_count": total, "saved_count": saved, "account_count": len(accounts), "success_account_count": success, "failed_account_count": len(failed), "failed_accounts": failed})
}
func (s *Server) fetchSellerItems(r *http.Request, a platformAccount, page int) ([]map[string]any, bool, error) {
	// The personal page uses an opaque cursor, so replay earlier pages when a
	// caller requests a standalone later page.
	session := xianyu.ItemSyncAccount{OwnerID: a.OwnerID, AccountID: a.AccountID, Cookie: a.Cookie, Proxy: accountProxy(a)}
	for currentPage := 1; currentPage <= page; currentPage++ {
		result, err := xianyu.SyncItemPage(r.Context(), s.db, s.itemMTOPCall, &session, currentPage)
		if err != nil || currentPage == page || !result.HasMore {
			if err == nil && currentPage < page {
				return []map[string]any{}, false, nil
			}
			return result.Items, result.HasMore, err
		}
	}
	return []map[string]any{}, false, nil
}
func nestedMap(root map[string]any, keys ...string) map[string]any {
	cur := root
	for _, k := range keys {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return map[string]any{}
		}
		cur = next
	}
	return cur
}

func (s *Server) fetchOrders(w http.ResponseWriter, r *http.Request) {
	var p struct {
		CookieID string `json:"cookie_id"`
	}
	_ = decode(r, &p)
	accounts, e := s.loadPlatformAccounts(r, p.CookieID)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	fetched, inserted, updated, failed := 0, 0, 0, 0
	errs := []string{}
	for _, a := range accounts {
		for page := 1; page <= 200; page++ {
			data := map[string]any{"pageNumber": page, "rowsPerPage": 30, "orderIds": "", "queryCode": xianyu.SoldOrderQueryAll, "orderSearchParam": "{}"}
			res := s.callOrderMTOP(r.Context(), xianyu.MTOPOptions{API: "mtop.taobao.idle.trade.merchant.sold.get", Version: "1.0", Cookie: a.Cookie, Proxy: accountProxy(a), Origin: "https://seller.goofish.com", Referer: "https://seller.goofish.com/", Data: data, ExtraParams: map[string]string{"valueType": "string", "spm_cnt": "a21107h.42826273.0.0"}})
			if !res.Success {
				failed++
				errs = append(errs, a.AccountID+": "+res.Error)
				break
			}
			if res.Cookie != a.Cookie && res.Cookie != "" {
				a.Cookie = res.Cookie
				_, _ = s.db.ExecContext(r.Context(), `UPDATE xy_accounts SET cookie=?,last_refresh_at=CURRENT_TIMESTAMP WHERE id=?`, a.Cookie, a.ID)
			}
			module := nestedMap(res.Response, "data", "module")
			items, _ := module["items"].([]any)
			for _, raw := range items {
				m, okv := raw.(map[string]any)
				if !okv {
					continue
				}
				account := xianyu.OrderSyncAccount{OwnerID: a.OwnerID, AccountID: a.AccountID, Cookie: a.Cookie, Proxy: accountProxy(a)}
				isNew, e := xianyu.SyncSoldOrder(r.Context(), s.db, s.callOrderMTOP, &account, m, xianyu.SoldOrderQueryAll)
				a.Cookie = account.Cookie
				if e != nil {
					failed++
					continue
				}
				fetched++
				if isNew {
					inserted++
				} else {
					updated++
				}
			}
			if fmt.Sprint(module["nextPage"]) != "true" {
				break
			}
		}
	}
	ok(w, "同步完成", map[string]any{"total_fetched": fetched, "new_inserted": inserted, "updated": updated, "failed": failed, "accounts_processed": len(accounts), "errors": errs})
}
