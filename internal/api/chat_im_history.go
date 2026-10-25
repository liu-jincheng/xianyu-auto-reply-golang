package api

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zhinianboke/xianyu-auto-go/internal/xianyu"
)

const latestIMHistoryCursor int64 = 9007199254740991

func (s *Server) imChatConversations(ctx context.Context, account platformAccount, cursor *int64, limit int) (chatConversationPage, error) {
	start := latestIMHistoryCursor
	if cursor != nil {
		start = *cursor
	}
	data, err := s.chatHistoryRead(ctx, account.AccountID, "/r/Conversation/listNewestPagination", []any{start, limit})
	if err != nil {
		return chatConversationPage{}, err
	}
	return parseIMConversations(data, xianyu.ParseCookies(account.Cookie)["unb"], cursor)
}

func parseIMConversations(data map[string]any, selfID string, cursor *int64) (chatConversationPage, error) {
	page := chatConversationPage{Conversations: []map[string]any{}}
	conversations, valid := data["userConvs"].([]any)
	if !valid {
		return page, fmt.Errorf("闲鱼IM未返回有效会话列表")
	}
	seen := map[string]bool{}
	for _, value := range conversations {
		model, _ := value.(map[string]any)
		user := childMap(model, "singleChatUserConversation")
		conv := childMap(user, "singleChatConversation")
		cid := normalizeChatUserID(conv["cid"])
		if cid == "" || seen[cid] {
			continue
		}
		if visible, exists := user["visible"]; exists && chatInteger(visible) != 1 {
			continue
		}
		if biz := textValue(conv["bizType"]); biz != "" && biz != "1" {
			continue
		}
		seen[cid] = true
		peer := normalizeChatUserID(conv["pairFirst"])
		if peer == selfID {
			peer = normalizeChatUserID(conv["pairSecond"])
		}
		ext := childMap(conv, "extension")
		last := childMap(childMap(user, "lastMessage"), "message")
		kind, summary, _ := imMessageContent(last)
		if kind == "image" {
			summary = "[图片]"
		}
		if summary == "" || summary == "[其他消息]" {
			summary = textValue(childMap(last, "extension")["reminderContent"])
		}
		timestamp := chatTimestamp(last["createAt"])
		if timestamp == 0 {
			timestamp = chatTimestamp(user["last_message_time"])
		}
		if timestamp == 0 {
			timestamp = chatTimestamp(user["modifyTime"])
		}
		page.Conversations = append(page.Conversations, map[string]any{
			"cid": cid, "rawCid": textValue(conv["cid"]), "otherUserId": peer,
			"otherUserName": "", "otherUserAvatar": "", "itemTitle": textValue(ext["itemTitle"]),
			"lastMessageSummary": summary, "lastMessageTime": timestamp, "unreadCount": chatInteger(user["redPoint"]),
		})
	}
	sort.SliceStable(page.Conversations, func(i, j int) bool {
		return chatInteger(page.Conversations[i]["lastMessageTime"]) > chatInteger(page.Conversations[j]["lastMessageTime"])
	})
	page.HasMore = boolValue(data["hasMore"])
	if page.HasMore {
		next := chatInteger(data["nextCursor"])
		if next <= 0 || (cursor != nil && next >= *cursor) {
			return page, fmt.Errorf("闲鱼IM会话分页游标未推进")
		}
		page.NextCursor = &next
	}
	return page, nil
}

func (s *Server) imChatMessages(ctx context.Context, account platformAccount, cid string, cursor *int64, limit int) (chatMessagePage, error) {
	start := latestIMHistoryCursor
	if cursor != nil {
		start = *cursor
	}
	data, err := s.chatHistoryRead(ctx, account.AccountID, "/r/MessageManager/listUserMessages", []any{strings.TrimSuffix(cid, "@goofish") + "@goofish", false, start, limit, false})
	if err != nil {
		return chatMessagePage{}, err
	}
	return parseIMMessages(data, xianyu.ParseCookies(account.Cookie)["unb"], cursor)
}

func parseIMMessages(data map[string]any, selfID string, cursor *int64) (chatMessagePage, error) {
	page := chatMessagePage{Messages: []map[string]any{}}
	models, valid := data["userMessageModels"].([]any)
	if !valid {
		return page, fmt.Errorf("闲鱼IM未返回有效聊天记录")
	}
	seen := map[string]bool{}
	for _, value := range models {
		model, _ := value.(map[string]any)
		message := childMap(model, "message")
		timestamp := chatTimestamp(message["createAt"])
		id := textValue(message["messageId"])
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		sender := normalizeChatUserID(childMap(message, "sender")["uid"])
		if sender == "" {
			if raw, ok := message["sender"].(string); ok {
				sender = normalizeChatUserID(raw)
			}
		}
		display := imMessageDisplay(message)
		if replacement := childMap(model, "userExtension")["replaceContent"]; replacement != nil {
			if content := xianyu.DecodeChatContent(textValue(replacement)); content != nil {
				display = xianyu.ParseChatContent(content)
			}
		}
		status, hasStatus := model["msgStatus"]
		if hasStatus && (chatInteger(status) == 0 || chatInteger(status) == 3) {
			continue
		} else if chatInteger(status) == 2 {
			display = xianyu.ChatContent{Type: "system", Text: "[消息已撤回]", Images: []string{}}
		}
		page.Messages = append(page.Messages, map[string]any{"messageId": id, "senderId": sender, "senderName": "", "senderAvatar": "", "isSelf": sender == selfID, "type": display.Type, "text": display.Text, "images": display.Images, "card": display.Card, "contentVersion": xianyu.ChatContentVersion, "time": timestamp})
	}
	sort.SliceStable(page.Messages, func(i, j int) bool {
		return chatInteger(page.Messages[i]["time"]) < chatInteger(page.Messages[j]["time"])
	})
	page.HasMore = boolValue(data["hasMore"])
	if page.HasMore {
		next := chatInteger(data["nextCursor"])
		if next <= 0 || (cursor != nil && next >= *cursor) {
			return page, fmt.Errorf("闲鱼IM消息分页游标未推进")
		}
		page.NextCursor = &next
	}
	return page, nil
}

func imMessageContent(message map[string]any) (string, string, []string) {
	display := imMessageDisplay(message)
	return display.Type, display.Text, display.Images
}

func imMessageDisplay(message map[string]any) xianyu.ChatContent {
	content := childMap(message, "content")
	if replacement := childMap(message, "extension")["replaceContent"]; replacement != nil {
		if decoded := xianyu.DecodeChatContent(textValue(replacement)); decoded != nil {
			content = decoded
		}
	}
	display := xianyu.ParseChatContent(content)
	if display.Card != nil && display.Card.Fallback {
		if reminder := textValue(childMap(message, "extension")["reminderContent"]); reminder != "" {
			display.Card.Title, display.Card.Text = reminder, "此卡片的完整内容可在闲鱼中查看。"
			display.Text = reminder
		}
	}
	return display
}
