package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/zhinianboke/xianyu-auto-go/internal/xianyu"
)

type publishRoute string

const (
	personalPublishRoute publishRoute = "personal"
	sellerPublishRoute   publishRoute = "seller"
)

type publishSession struct {
	Route      publishRoute
	Cookie     string
	Capability map[string]any
}

type publishMTOPCaller func(context.Context, xianyu.MTOPOptions) xianyu.MTOPResult

// A fish-shop identity and access to COMMONPRO are separate capabilities. Pick
// the route from successful initialization calls, never from a client flag.
func resolvePublishSession(ctx context.Context, account platformAccount, call publishMTOPCaller) (publishSession, error) {
	session := publishSession{Cookie: account.Cookie}
	backend := call(ctx, xianyu.MTOPOptions{
		API: "mtop.idle.pc.backend.idleitem.preget", Version: "1.0", Cookie: session.Cookie, Proxy: accountProxy(account),
		Origin: sellerOrigin, Referer: sellerOrigin + "/?site=COMMONPRO", Data: map[string]any{"publishScene": "pcBackendPublish"},
		ExtraParams:  map[string]string{"idle_site_biz_code": "COMMONPRO", "spm_cnt": "a21107h.42826273.0.0"},
		ExtraHeaders: map[string]string{"idle_site_biz_code": "COMMONPRO", "idle_user_group_member_id": ""},
	})
	session.Cookie = firstNonemptyAPI(backend.Cookie, session.Cookie)
	if backend.Success {
		if capability, recognized := parsePublishCapability(backend.Response); recognized && capability["is_fish_shop"] == true {
			session.Route, session.Capability = sellerPublishRoute, capability
			return session, nil
		}
	}
	personal := call(ctx, xianyu.MTOPOptions{
		API: "mtop.idle.pc.idleitem.preget", Version: "1.0", Cookie: session.Cookie, Proxy: accountProxy(account),
		Origin: "https://www.goofish.com", Referer: "https://www.goofish.com/publish", Data: map[string]any{},
		ExtraParams: map[string]string{"spm_cnt": "a21ybx.publish.0.0"},
	})
	session.Cookie = firstNonemptyAPI(personal.Cookie, session.Cookie)
	if !personal.Success {
		return session, fmt.Errorf("账号发布能力检测失败：%s", firstNonemptyAPI(personal.Error, "闲鱼接口调用失败"))
	}
	capability, recognized := parsePublishCapability(personal.Response)
	if !recognized {
		return session, fmt.Errorf("账号发布能力检测失败：无法识别账号是否支持多规格和库存")
	}
	session.Route, session.Capability = personalPublishRoute, capability
	return session, nil
}

func checkPersonalPublish(ctx context.Context, account platformAccount, call publishMTOPCaller) (string, error) {
	result := call(ctx, xianyu.MTOPOptions{
		API: "mtop.idle.pc.idleitem.prepublish.check", Version: "1.0", Cookie: account.Cookie, Proxy: accountProxy(account),
		Origin: "https://www.goofish.com", Referer: "https://www.goofish.com/publish", Data: map[string]any{},
		ExtraParams: map[string]string{"spm_cnt": "a21ybx.publish.0.0"},
	})
	cookie := firstNonemptyAPI(result.Cookie, account.Cookie)
	if !result.Success {
		return cookie, fmt.Errorf("闲鱼发布前检查失败：%s", firstNonemptyAPI(result.Error, "闲鱼接口调用失败"))
	}
	data := childMap(result.Response, "data")
	limited, known := data["limited"].(bool)
	if !known {
		return cookie, fmt.Errorf("闲鱼发布前检查未返回有效的限制状态，请稍后重试")
	}
	if limited {
		info := childMap(data, "publishLimitInfo")
		message := firstNonemptyAPI(textValue(info["content"]), textValue(info["title"]), "当前账号暂时限制发布")
		return cookie, fmt.Errorf("闲鱼限制发布：%s；请在闲鱼官方发布页处理后重试", message)
	}
	return cookie, nil
}

func validatePublishCapability(item, capability map[string]any) error {
	if !boolValue(capability["support_sku_or_inventory"]) {
		if len(mapSlice(item["specifications"])) > 0 || len(mapSlice(item["sku_rows"])) > 0 {
			return fmt.Errorf("当前账号不支持多规格商品，请使用无规格素材")
		}
		if clampQuantity(item["quantity"]) != 1 {
			return fmt.Errorf("当前账号不支持多库存商品，请将库存设为1")
		}
	}
	if !boolValue(capability["is_fish_shop"]) && textValue(item["shipping_method"]) == "template" {
		return fmt.Errorf("普通卖家账号不支持运费模板，请重新选择发货方式")
	}
	return nil
}

func publishRequestOptions(route publishRoute, account platformAccount, payload map[string]any) xianyu.MTOPOptions {
	if route == personalPublishRoute {
		// The ordinary web page sends the DTO directly as MTOP data. The seller
		// console instead wraps its DTO in inputJson; changing only the API fails.
		data := mapCopy(payload)
		data["sourceId"], data["bizcode"], data["publishScene"] = "pcMainPublish", "pcMainPublish", "pcMainPublish"
		data["quantity"] = strconv.Itoa(clampQuantity(payload["quantity"]))
		return xianyu.MTOPOptions{
			API: "mtop.idle.pc.idleitem.publish", Version: "1.0", Cookie: account.Cookie, Proxy: accountProxy(account), Method: http.MethodPost,
			Origin: "https://www.goofish.com", Referer: "https://www.goofish.com/publish", Data: data,
			ExtraParams: map[string]string{"spm_cnt": "a21ybx.publish.0.0"},
		}
	}
	raw, _ := json.Marshal(payload)
	return xianyu.MTOPOptions{
		API: "mtop.idle.pc.backend.idleitem.publish", Version: "1.0", Cookie: account.Cookie, Proxy: accountProxy(account), Method: http.MethodPost,
		Origin: sellerOrigin, Referer: sellerOrigin + "/?site=COMMONPRO", Data: map[string]any{"inputJson": string(raw)},
		ExtraParams:  map[string]string{"idle_site_biz_code": "COMMONPRO", "spm_cnt": "a21107h.42826273.0.0"},
		ExtraHeaders: map[string]string{"idle_site_biz_code": "COMMONPRO"},
	}
}

func (s *Server) persistPublishCookie(ctx context.Context, account platformAccount, next string) string {
	if next == "" {
		return account.Cookie
	}
	if next != account.Cookie {
		_, _ = s.db.ExecContext(ctx, `UPDATE xy_accounts SET cookie=?,last_refresh_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP WHERE id=?`, next, account.ID)
	}
	return next
}
