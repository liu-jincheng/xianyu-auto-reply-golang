package xianyu

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestChatItemCardContentAndNativeItemLink(t *testing.T) {
	content := map[string]any{"contentType": 7, "itemCard": map[string]any{
		"itemTip": "我想要", "item": map[string]any{"itemId": "9000000000000000001", "title": "商品名称", "mainPic": "//img.alicdn.com/example.jpg", "price": "¥120.00"},
		"action": map[string]any{"page": map[string]any{"url": "fleamarket://item?id=9000000000000000001"}},
	}}
	raw, _ := json.Marshal(content)
	display := ParseChatContent(map[string]any{"contentType": 101, "custom": map[string]any{"data": base64.StdEncoding.EncodeToString(raw)}})
	if display.Type != "card" || display.Card == nil || display.Card.Kind != "item" || display.Card.Title != "商品名称" || display.Card.Tag != "我想要" || display.Card.Price != "¥120.00" || display.Card.Image != "https://img.alicdn.com/example.jpg" || display.Card.URL != "https://www.goofish.com/item?id=9000000000000000001" {
		t.Fatalf("item card was flattened: %+v", display)
	}
}

func TestTextAndActionCardsArePlainDisplayData(t *testing.T) {
	for _, field := range []string{"textCard", "actionCard"} {
		display := ParseChatContent(map[string]any{"contentType": 6, field: map[string]any{
			"title": "<b>买家已付款</b>", "content": "<div>请尽快发货</div><script>alert('bad')</script><div>订单金额 ¥99</div>",
			"action": map[string]any{"page": map[string]any{"url": "javascript:alert(1)", "actionName": "确认收货"}},
		}})
		if display.Card == nil || display.Card.Title != "买家已付款" || !strings.Contains(display.Card.Text, "请尽快发货") || strings.Contains(display.Card.Text, "alert") || display.Card.URL != "" {
			t.Fatalf("unsafe or unreadable %s: %+v", field, display.Card)
		}
	}
}

func TestDynamicTradeCardExtractsReadableFields(t *testing.T) {
	display := ParseChatContent(map[string]any{"contentType": 20, "dxCard": map[string]any{
		"template": map[string]any{"name": "notify_center_trade_card", "url": "https://example.com/template.js"},
		"item": map[string]any{"main": map[string]any{"targetUrl": "https://www.goofish.com/order?orderId=123", "exContent": map[string]any{
			"title": "交易成功", "tag": "订单通知", "orderId": "123", "descInfo": map[string]any{"title": "示例商品", "imgUrl": "https://img.alicdn.com/item.jpg", "price": "99.00"},
		}}},
	}})
	if display.Card == nil || display.Card.Kind != "order" || display.Card.Title != "交易成功" || display.Card.Fallback || !strings.Contains(display.Card.Text, "示例商品") || display.Card.Price != "99.00" || len(display.Card.Fields) != 1 {
		t.Fatalf("trade card lost fields: %+v", display.Card)
	}
}

func TestUnknownCardMediaAndUntrustedLinks(t *testing.T) {
	if card := ParseChatContent(map[string]any{"contentType": 999}).Card; card == nil || !card.Fallback {
		t.Fatal("unknown content is not explained")
	}
	for _, link := range []string{"javascript:alert(1)", "data:text/html,test", "https://goofish.com.attacker.example/item", "https://user:pass@www.goofish.com/item"} {
		if ChatCardURL(link) != "" {
			t.Fatalf("unsafe link accepted: %s", link)
		}
	}
	if ChatCardURL("https://www.goofish.com/item?id=123") == "" {
		t.Fatal("ordinary item link lost")
	}
	var incoming Incoming
	applyContent(&incoming, map[string]any{"picUrl": "https://img.alicdn.com/item.jpg"})
	if incoming.Type != "image" {
		t.Fatalf("untyped image changed to a card: %+v", incoming)
	}
	applyContent(&incoming, map[string]any{"audio": map[string]any{"url": "audio"}})
	if incoming.Type != "audio" {
		t.Fatal("audio changed to a card")
	}
}

func TestLiveCardUsesSameContentAsHistory(t *testing.T) {
	incoming := Incoming{Text: "交易提醒"}
	applyContent(&incoming, map[string]any{"contentType": 6, "textCard": map[string]any{"title": "买家已付款", "content": "请尽快发货"}})
	if incoming.Type != "card" || incoming.Card == nil || incoming.Card.Title != "买家已付款" || incoming.Text != "交易提醒" {
		t.Fatalf("live card lost metadata: %+v", incoming)
	}
}

func TestTipMessagesUseTheirActualTipText(t *testing.T) {
	display := ParseChatContent(map[string]any{"contentType": 14, "tip": map[string]any{"tip": "<span>交易提醒</span>", "argInfo": []any{}}})
	if display.Type != "system" || display.Text != "交易提醒" || display.Card != nil {
		t.Fatalf("tip hidden as unsupported card: %+v", display)
	}
}
