package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zhinianboke/xianyu-auto-go/internal/xianyu"
)

func imConversationFixture(cid, date string) map[string]any {
	stamp, _ := time.Parse(time.RFC3339, date)
	return map[string]any{"type": 1, "singleChatUserConversation": map[string]any{
		"visible": 1, "redPoint": 2, "modifyTime": stamp.UnixMilli(),
		"singleChatConversation": map[string]any{"cid": cid + "@goofish", "pairFirst": "owner@goofish", "pairSecond": "peer@goofish", "bizType": "1", "extension": map[string]any{"itemTitle": "商品"}},
		"lastMessage":            map[string]any{"message": map[string]any{"createAt": stamp.UnixMilli(), "content": map[string]any{"contentType": 101, "custom": map[string]any{"data": `{"contentType":1,"text":{"text":"最新摘要"}}`}}}},
	}}
}

func TestChatHistoryUsesCurrentIMConnectionForRecentConversations(t *testing.T) {
	s := newCloudChatTestServer(t)
	s.chatMTOPCall = func(context.Context, xianyu.MTOPOptions) xianyu.MTOPResult {
		t.Fatal("current IM fell back to the old snapshot")
		return xianyu.MTOPResult{}
	}
	s.chatHistoryRead = func(_ context.Context, accountID, path string, body []any) (map[string]any, error) {
		if accountID != "account-a" || path != "/r/Conversation/listNewestPagination" || chatInteger(body[0]) != latestIMHistoryCursor || chatInteger(body[1]) != 20 {
			t.Fatalf("incorrect IM history request: %s %v", path, body)
		}
		return map[string]any{"userConvs": []any{imConversationFixture("old", "2025-06-22T18:40:17+08:00"), imConversationFixture("new", "2026-10-25T12:00:00+08:00")}, "hasMore": true, "nextCursor": int64(1750590000000)}, nil
	}
	w := cloudChatRequest(s, User{ID: 1, Role: "MEMBER"}, "/conversations/account-a")
	var response struct {
		Success bool
		Data    chatConversationPage
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || !response.Success || len(response.Data.Conversations) != 2 {
		t.Fatalf("missing IM conversations: %s (%v)", w.Body, err)
	}
	first := response.Data.Conversations[0]
	if first["cid"] != "new" || first["otherUserId"] != "peer" || first["lastMessageSummary"] != "最新摘要" || response.Data.NextCursor == nil || *response.Data.NextCursor != 1750590000000 {
		t.Fatalf("incorrect IM mapping: %+v", response.Data)
	}
}

func TestCurrentIMMessagesPreserveDirectionsAndPlatformCursor(t *testing.T) {
	s := newCloudChatTestServer(t)
	s.chatHistoryRead = func(_ context.Context, accountID, path string, body []any) (map[string]any, error) {
		if path != "/r/MessageManager/listUserMessages" || body[0] != "cid@goofish" || body[1] != false || chatInteger(body[2]) != 1792890000000 || body[4] != false {
			t.Fatalf("incorrect message query: %s %v", path, body)
		}
		message := func(id, sender string, stamp int64, raw string) any {
			return map[string]any{"msgStatus": 1, "message": map[string]any{"messageId": id, "sender": map[string]any{"uid": sender + "@goofish"}, "createAt": stamp, "content": map[string]any{"contentType": 101, "custom": map[string]any{"data": raw}}}}
		}
		return map[string]any{"hasMore": true, "nextCursor": int64(1792889998999), "userMessageModels": []any{
			message("new", "owner", 1792889999999, base64.StdEncoding.EncodeToString([]byte(`{"contentType":1,"text":{"text":"回复"}}`))),
			message("old", "peer", 1792889999000, `{"contentType":2,"image":{"pics":[{"url":"https://example.com/image.jpg"}]}}`),
		}}, nil
	}
	w := cloudChatRequest(s, User{ID: 1, Role: "MEMBER"}, "/messages/account-a/cid?cursor=1792890000000")
	var response struct {
		Success bool
		Data    chatMessagePage
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || !response.Success || len(response.Data.Messages) != 2 {
		t.Fatalf("missing IM messages: %s (%v)", w.Body, err)
	}
	if response.Data.Messages[0]["isSelf"] != false || response.Data.Messages[0]["type"] != "image" || response.Data.Messages[1]["isSelf"] != true || response.Data.Messages[1]["text"] != "回复" || response.Data.NextCursor == nil || *response.Data.NextCursor != 1792889998999 {
		t.Fatalf("incorrect message mapping: %+v", response.Data)
	}
}

func TestCurrentIMFailureDoesNotSilentlyReturnOldHistory(t *testing.T) {
	s := newCloudChatTestServer(t)
	s.chatHistoryRead = func(context.Context, string, string, []any) (map[string]any, error) {
		return nil, errors.New("IM未连接")
	}
	s.chatMTOPCall = func(context.Context, xianyu.MTOPOptions) xianyu.MTOPResult {
		t.Fatal("returned incomplete old history")
		return xianyu.MTOPResult{}
	}
	w := cloudChatRequest(s, User{ID: 1, Role: "MEMBER"}, "/conversations/account-a")
	var response struct{ Success bool }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Success {
		t.Fatalf("failure hidden as success: %s", w.Body)
	}
}

func TestIMCardIsIncludedInHistoryResponse(t *testing.T) {
	data := map[string]any{"hasMore": false, "userMessageModels": []any{map[string]any{"msgStatus": 1, "message": map[string]any{
		"messageId": "card-1", "sender": map[string]any{"uid": "peer@goofish"}, "createAt": int64(1790920000000),
		"content": map[string]any{"contentType": 101, "custom": map[string]any{"data": `{"contentType":7,"itemCard":{"item":{"title":"示例商品","itemId":"123","price":"¥28","mainPic":"https://img.alicdn.com/item.jpg"}}}`}},
	}}}}
	page, err := parseIMMessages(data, "owner", nil)
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("missing history card: %+v %v", page, err)
	}
	card, ok := page.Messages[0]["card"].(*xianyu.ChatCard)
	if !ok || card.Title != "示例商品" || card.Price != "¥28" || page.Messages[0]["type"] != "card" || page.Messages[0]["contentVersion"] != xianyu.ChatContentVersion {
		t.Fatalf("card flattened in API response: %+v", page.Messages)
	}
}
