package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zhinianboke/xianyu-auto-go/internal/xianyu"
)

func publishCapabilityResponse(shop bool) map[string]any {
	title := "基础软件服务费"
	if shop {
		title = "鱼小铺软件服务费"
	}
	return map[string]any{"data": map[string]any{
		"supportSkuOrInventory": shop,
		"commissionConfig":      map[string]any{"defaultCommissionTitle": title},
	}}
}

func TestResolvePublishSessionWithoutWorkbenchPermission(t *testing.T) {
	for _, shop := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "shop without workbench"}[shop], func(t *testing.T) {
			calls := 0
			session, err := resolvePublishSession(context.Background(), platformAccount{Cookie: "initial"}, func(ctx context.Context, opt xianyu.MTOPOptions) xianyu.MTOPResult {
				calls++
				if calls == 1 {
					if opt.API != "mtop.idle.pc.backend.idleitem.preget" {
						t.Fatalf("unexpected workbench probe: %s", opt.API)
					}
					return xianyu.MTOPResult{Error: "FAIL_BIZ_IDLE_USER_UNAUTHORIZED::无权限访问", Cookie: "backend-refreshed"}
				}
				if calls != 2 || opt.API != "mtop.idle.pc.idleitem.preget" || opt.Cookie != "backend-refreshed" || opt.Origin != "https://www.goofish.com" || opt.ExtraHeaders["idle_site_biz_code"] != "" {
					t.Fatalf("incorrect ordinary initialization request: api=%s cookie=%s origin=%s", opt.API, opt.Cookie, opt.Origin)
				}
				return xianyu.MTOPResult{Success: true, Response: publishCapabilityResponse(shop), Cookie: "personal-refreshed"}
			})
			if err != nil || calls != 2 || session.Route != personalPublishRoute || session.Cookie != "personal-refreshed" || session.Capability["is_fish_shop"] != shop {
				t.Fatalf("wrong route after workbench denial: %+v (%v)", session, err)
			}
		})
	}
}

func TestResolvePublishSessionKeepsAuthorizedWorkbench(t *testing.T) {
	calls := 0
	session, err := resolvePublishSession(context.Background(), platformAccount{Cookie: "initial"}, func(ctx context.Context, opt xianyu.MTOPOptions) xianyu.MTOPResult {
		calls++
		return xianyu.MTOPResult{Success: true, Response: publishCapabilityResponse(true), Cookie: "shop-refreshed"}
	})
	if err != nil || calls != 1 || session.Route != sellerPublishRoute || session.Cookie != "shop-refreshed" {
		t.Fatalf("authorized workbench route lost: %+v (%v)", session, err)
	}
}

func TestResolvePublishSessionDoesNotInventPermissions(t *testing.T) {
	for _, test := range []struct {
		name     string
		personal xianyu.MTOPResult
	}{
		{"expired login", xianyu.MTOPResult{Error: "FAIL_SYS_SESSION_EXPIRED", Cookie: "latest"}},
		{"unknown response", xianyu.MTOPResult{Success: true, Response: map[string]any{"data": map[string]any{}}, Cookie: "latest"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			session, err := resolvePublishSession(context.Background(), platformAccount{}, func(ctx context.Context, opt xianyu.MTOPOptions) xianyu.MTOPResult {
				calls++
				if calls == 1 {
					return xianyu.MTOPResult{Error: "FAIL_BIZ_IDLE_USER_UNAUTHORIZED"}
				}
				return test.personal
			})
			if err == nil || session.Route != "" || session.Capability != nil || session.Cookie != "latest" {
				t.Fatalf("failed probe produced a usable route: %+v (%v)", session, err)
			}
		})
	}
}

func TestPersonalPublishPrecheck(t *testing.T) {
	for _, test := range []struct {
		name    string
		result  xianyu.MTOPResult
		wantErr string
	}{
		{"unrestricted", xianyu.MTOPResult{Success: true, Response: map[string]any{"data": map[string]any{"limited": false}}}, ""},
		{"verification required", xianyu.MTOPResult{Success: true, Response: map[string]any{"data": map[string]any{"limited": true, "publishLimitInfo": map[string]any{"title": "完成实名认证", "content": "请先完成实人认证"}}}}, "请先完成实人认证"},
		{"platform error", xianyu.MTOPResult{Error: "FAIL_SYS_SESSION_EXPIRED"}, "FAIL_SYS_SESSION_EXPIRED"},
		{"unknown response", xianyu.MTOPResult{Success: true, Response: map[string]any{"data": map[string]any{}}}, "未返回有效的限制状态"},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.result.Cookie = "checked-cookie"
			cookie, err := checkPersonalPublish(context.Background(), platformAccount{Cookie: "initial"}, func(ctx context.Context, opt xianyu.MTOPOptions) xianyu.MTOPResult {
				if opt.API != "mtop.idle.pc.idleitem.prepublish.check" || opt.ExtraHeaders["idle_site_biz_code"] != "" {
					t.Fatalf("wrong precheck endpoint: %s", opt.API)
				}
				return test.result
			})
			if cookie != "checked-cookie" || (test.wantErr == "" && err != nil) || (test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr))) {
				t.Fatalf("unexpected precheck: cookie=%s err=%v", cookie, err)
			}
		})
	}
}

func TestPublishRequestContracts(t *testing.T) {
	payload := map[string]any{
		"sourceId": "pcBackendPublish", "bizcode": "pcMainPublish", "publishScene": "pcBackendPublish", "quantity": 1,
		"itemTextDTO":     map[string]any{"title": "test", "desc": "description", "titleDescSeparate": true},
		"imageInfoDOList": []any{map[string]any{"url": "https://example.com/image.jpg", "major": true}},
	}
	account := platformAccount{Cookie: "refreshed-session"}
	ordinary := publishRequestOptions(personalPublishRoute, account, payload)
	if ordinary.API != "mtop.idle.pc.idleitem.publish" || ordinary.Origin != "https://www.goofish.com" || ordinary.Referer != "https://www.goofish.com/publish" || ordinary.Cookie != account.Cookie {
		t.Fatalf("wrong ordinary endpoint: %s %s %s", ordinary.API, ordinary.Origin, ordinary.Referer)
	}
	if ordinary.ExtraHeaders["idle_site_biz_code"] != "" || ordinary.ExtraParams["idle_site_biz_code"] != "" || ordinary.Data["inputJson"] != nil || childMap(ordinary.Data, "itemTextDTO")["title"] != "test" {
		t.Fatal("ordinary publish retained seller headers or the inputJson wrapper")
	}
	if ordinary.Data["quantity"] != "1" {
		t.Fatalf("ordinary quantity does not match the web contract: %v", ordinary.Data["quantity"])
	}
	for _, key := range []string{"sourceId", "bizcode", "publishScene"} {
		if ordinary.Data[key] != "pcMainPublish" {
			t.Errorf("ordinary %s = %v", key, ordinary.Data[key])
		}
	}
	if payload["sourceId"] != "pcBackendPublish" || payload["publishScene"] != "pcBackendPublish" {
		t.Fatal("ordinary request mutated the shared seller payload")
	}
	seller := publishRequestOptions(sellerPublishRoute, account, payload)
	var wrapped map[string]any
	if seller.API != "mtop.idle.pc.backend.idleitem.publish" || seller.ExtraHeaders["idle_site_biz_code"] != "COMMONPRO" || json.Unmarshal([]byte(textValue(seller.Data["inputJson"])), &wrapped) != nil || wrapped["publishScene"] != "pcBackendPublish" || childMap(wrapped, "itemTextDTO")["title"] != "test" {
		t.Fatal("authorized seller request contract changed")
	}
}

func TestValidatePublishCapability(t *testing.T) {
	ordinary := map[string]any{"is_fish_shop": false, "support_sku_or_inventory": false}
	for _, item := range []map[string]any{
		{"quantity": 1},
		{"price": 2.5},
	} {
		if err := validatePublishCapability(item, ordinary); err != nil {
			t.Fatalf("ordinary single item rejected: %v", err)
		}
	}
	for _, item := range []map[string]any{
		{"quantity": 2},
		{"quantity": 1, "specifications": []any{map[string]any{"name": "颜色"}}},
		{"quantity": 1, "sku_rows": []any{map[string]any{"stock": 2}}},
		{"quantity": 1, "shipping_method": "template"},
	} {
		if err := validatePublishCapability(item, ordinary); err == nil {
			t.Errorf("unsupported ordinary item accepted: %v", item)
		}
		if err := validatePublishCapability(item, map[string]any{"is_fish_shop": true, "support_sku_or_inventory": true}); err != nil {
			t.Errorf("shop features rejected: %v", err)
		}
	}
}
