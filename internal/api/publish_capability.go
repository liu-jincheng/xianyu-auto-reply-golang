package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zhinianboke/xianyu-auto-go/internal/xianyu"
)

func (s *Server) publishAccountCapability(w http.ResponseWriter, r *http.Request) {
	u := current(r)
	accountID := chi.URLParam(r, "account_id")
	var account platformAccount
	query := `SELECT id,owner_id,account_id,cookie,COALESCE(proxy_type,'none'),COALESCE(proxy_host,''),proxy_port,proxy_user,proxy_pass FROM xy_accounts WHERE account_id=?`
	args := []any{accountID}
	if u.Role != "ADMIN" {
		query += ` AND owner_id=?`
		args = append(args, u.ID)
	}
	if err := s.db.QueryRowContext(r.Context(), query, args...).Scan(&account.ID, &account.OwnerID, &account.AccountID, &account.Cookie, &account.ProxyType, &account.ProxyHost, &account.ProxyPort, &account.ProxyUser, &account.ProxyPass); err != nil {
		write(w, 200, response{Success: false, Message: "发布账号不存在或无权使用"})
		return
	}
	if strings.TrimSpace(account.Cookie) == "" {
		write(w, 200, response{Success: false, Message: "发布账号缺少Cookie，请重新登录账号"})
		return
	}
	session, err := resolvePublishSession(r.Context(), account, xianyu.MTOPCall)
	s.persistPublishCookie(r.Context(), account, session.Cookie)
	if err != nil {
		write(w, 200, response{Success: false, Message: err.Error()})
		return
	}
	s.writePublishCapability(w, accountID, session.Capability)
}

func parsePublishCapability(response map[string]any) (map[string]any, bool) {
	data, _ := response["data"].(map[string]any)
	if nested, ok := data["data"].(map[string]any); ok {
		data = nested
	}
	commission, _ := data["commissionConfig"].(map[string]any)
	tipURL := fmt.Sprint(commission["tipUrl"])
	title := fmt.Sprint(commission["commissionTitle"])
	defaultTitle := fmt.Sprint(commission["defaultCommissionTitle"])
	isShop := strings.Contains(tipURL, "fish-shop-service-fee-rule") || strings.Contains(title, "鱼小铺") || strings.Contains(defaultTitle, "鱼小铺")
	isPersonal := strings.Contains(tipURL, "fish-seller-service-fee-rule") || defaultTitle == "基础软件服务费"
	support, supportKnown := data["supportSkuOrInventory"].(bool)
	if !isShop && !isPersonal && !supportKnown {
		return nil, false
	}
	if !isShop && !isPersonal {
		isShop = support
	}
	config := map[string]any{"title": fmt.Sprint(commission["commissionTitle"]), "default_title": defaultTitle, "tips": fmt.Sprint(commission["commissionTips"]), "percent": fmt.Sprint(commission["percent"]), "max_commission": fmt.Sprint(commission["maxCommission"]), "tip_url": tipURL}
	return map[string]any{"is_fish_shop": isShop, "support_sku_or_inventory": map[bool]bool{true: support, false: isShop}[supportKnown], "commission_config": config}, true
}

func (s *Server) writePublishCapability(w http.ResponseWriter, accountID string, capability map[string]any) {
	capability["account_id"] = accountID
	ok(w, "账号发布能力检测成功", capability)
}
