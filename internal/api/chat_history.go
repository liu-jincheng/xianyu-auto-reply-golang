package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/zhinianboke/xianyu-auto-go/internal/xianyu"
)

type chatConversationPage struct {
	Conversations []map[string]any `json:"conversations"`
	HasMore       bool             `json:"hasMore"`
	NextCursor    *int64           `json:"nextCursor"`
}

type chatMessagePage struct {
	Messages   []map[string]any `json:"messages"`
	HasMore    bool             `json:"hasMore"`
	NextCursor *int64           `json:"nextCursor"`
}

func chatCursor(r *http.Request) (*int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if raw == "" {
		return nil, nil
	}
	cursor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || cursor < 1 {
		return nil, fmt.Errorf("聊天分页游标无效")
	}
	return &cursor, nil
}

func chatHistoryOptions(account platformAccount, api, version string, data map[string]any) xianyu.MTOPOptions {
	return xianyu.MTOPOptions{
		API: api, Version: version, Cookie: account.Cookie, Proxy: accountProxy(account),
		Origin: "https://www.goofish.com", Referer: "https://www.goofish.com/im", Data: data,
		ExtraParams: map[string]string{"spm_cnt": "a21ybx.im.0.0"},
	}
}

func (s *Server) chatHistoryCall(ctx context.Context, account platformAccount, options xianyu.MTOPOptions) (map[string]any, error) {
	call := s.chatMTOPCall
	if call == nil {
		call = xianyu.MTOPCall
	}
	result := call(ctx, options)
	if result.Cookie != "" && result.Cookie != account.Cookie {
		_, _ = s.db.ExecContext(ctx, `UPDATE xy_accounts SET cookie=?,last_refresh_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP WHERE id=?`, result.Cookie, account.ID)
	}
	if !result.Success {
		return nil, fmt.Errorf("%s", firstNonemptyAPI(result.Error, "闲鱼聊天接口调用失败"))
	}
	data, ok := result.Response["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("闲鱼聊天接口未返回有效数据")
	}
	return data, nil
}

func (s *Server) cloudChatConversations(ctx context.Context, account platformAccount, cursor *int64, limit int) (chatConversationPage, error) {
	if s.chatHistoryRead != nil {
		return s.imChatConversations(ctx, account, cursor, limit)
	}
	request := map[string]any{"fetchNum": limit, "sessionTypes": "[1]"}
	if cursor != nil {
		request["sortIndex"] = *cursor
	}
	data, err := s.chatHistoryCall(ctx, account, chatHistoryOptions(account, "mtop.taobao.idlemessage.pc.session.sync", "3.0", request))
	if err != nil {
		return chatConversationPage{}, err
	}
	return parseCloudConversations(data, xianyu.ParseCookies(account.Cookie)["unb"], cursor)
}

func parseCloudConversations(data map[string]any, selfID string, cursor *int64) (chatConversationPage, error) {
	page := chatConversationPage{Conversations: []map[string]any{}}
	sessions, valid := data["sessions"].([]any)
	if !valid {
		return page, fmt.Errorf("闲鱼聊天接口未返回有效会话列表")
	}
	var next int64
	seen := map[string]bool{}
	for _, value := range sessions {
		record, _ := value.(map[string]any)
		session := childMap(record, "session")
		summary := childMap(childMap(record, "message"), "summary")
		sortIndex := chatInteger(summary["sortIndex"])
		if sortIndex > 0 && (next == 0 || sortIndex < next) {
			next = sortIndex
		}
		rawCID := textValue(session["sessionId"])
		cid := strings.TrimSuffix(rawCID, "@goofish")
		if cid == "" || seen[cid] {
			continue
		}
		seen[cid] = true
		_, user := cloudChatParticipants(session, selfID)
		item, attributes := childMap(session, "itemInfo"), childMap(session, "attributeMap")
		page.Conversations = append(page.Conversations, map[string]any{
			"cid": cid, "rawCid": rawCID,
			"otherUserId": strings.TrimSuffix(textValue(user["userId"]), "@goofish"), "otherUserName": chatUserName(user),
			"otherUserAvatar":    firstNonemptyAPI(textValue(user["logo"]), textValue(user["avatar"])),
			"itemTitle":          firstNonemptyAPI(textValue(item["title"]), textValue(item["itemTitle"]), textValue(attributes["itemTitle"])),
			"lastMessageSummary": textValue(summary["summary"]), "lastMessageTime": chatTimestamp(summary["ts"]),
			"unreadCount": chatInteger(summary["unread"]),
		})
	}
	page.HasMore = boolValue(data["hasMore"])
	sort.SliceStable(page.Conversations, func(i, j int) bool {
		return chatInteger(page.Conversations[i]["lastMessageTime"]) > chatInteger(page.Conversations[j]["lastMessageTime"])
	})
	if len(sessions) > 0 && len(page.Conversations) == 0 {
		return page, fmt.Errorf("闲鱼会话数据缺少有效的会话ID")
	}
	if page.HasMore {
		if next <= 0 || (cursor != nil && next >= *cursor) {
			return page, fmt.Errorf("闲鱼会话分页游标未推进，请刷新后重试")
		}
		page.NextCursor = &next
	}
	return page, nil
}

func (s *Server) cloudChatMessages(ctx context.Context, account platformAccount, cid string, cursor *int64, limit int) (chatMessagePage, error) {
	if s.chatHistoryRead != nil {
		return s.imChatMessages(ctx, account, cid, cursor, limit)
	}
	request := map[string]any{"type": 1, "fetchs": limit, "sessionId": strings.TrimSuffix(cid, "@goofish"), "start": 0, "includeRequestMsg": true}
	if cursor != nil {
		request["version"] = *cursor
	}
	raw, _ := json.Marshal(request)
	data, err := s.chatHistoryCall(ctx, account, chatHistoryOptions(account, "mtop.taobao.idlemessage.pc.message.sync", "1.0", map[string]any{"req": string(raw)}))
	if err != nil {
		return chatMessagePage{}, err
	}
	return parseCloudMessages(data, xianyu.ParseCookies(account.Cookie)["unb"], cursor)
}

func parseCloudMessages(data map[string]any, selfID string, cursor *int64) (chatMessagePage, error) {
	selfID = normalizeChatUserID(selfID)
	page := chatMessagePage{Messages: []map[string]any{}}
	if result := childMap(data, "result"); result != nil && result["success"] == false {
		return page, fmt.Errorf("闲鱼聊天记录查询失败：%s", firstNonemptyAPI(textValue(result["errMsg"]), textValue(result["errCode"]), "未知错误"))
	}
	messages, valid := data["messages"].([]any)
	if !valid {
		return page, fmt.Errorf("闲鱼聊天接口未返回有效消息列表")
	}
	var next int64
	seen := map[string]bool{}
	for _, value := range messages {
		message, _ := value.(map[string]any)
		version := chatInteger(message["version"])
		if version > 0 && (next == 0 || version < next) {
			next = version
		}
		extension := map[string]any{}
		decoder := json.NewDecoder(strings.NewReader(textValue(message["extJson"])))
		decoder.UseNumber()
		_ = decoder.Decode(&extension)
		id := firstNonemptyAPI(textValue(extension["messageId"]), textValue(message["messageId"]), textValue(message["messageUuid"]))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		sender, content := childMap(message, "senderInfo"), childMap(message, "content")
		senderID := normalizeChatUserID(sender["userId"])
		isSelf := cloudChatSenderIsSelf(sender, childMap(message, "sessionInfo"), selfID)
		if isSelf && selfID != "" {
			senderID = selfID
		}
		display := xianyu.ParseChatContent(content)
		page.Messages = append(page.Messages, map[string]any{
			"messageId": id, "senderId": senderID, "senderName": chatUserName(sender),
			"senderAvatar": chatUserAvatar(sender), "isSelf": isSelf,
			"type": display.Type, "text": display.Text, "images": display.Images, "card": display.Card, "contentVersion": xianyu.ChatContentVersion, "time": chatTimestamp(message["timeStamp"]),
		})
	}
	sort.SliceStable(page.Messages, func(i, j int) bool {
		return chatInteger(page.Messages[i]["time"]) < chatInteger(page.Messages[j]["time"])
	})
	page.HasMore = boolValue(data["hasMore"])
	if len(messages) > 0 && len(page.Messages) == 0 {
		return page, fmt.Errorf("闲鱼聊天记录缺少有效的消息ID")
	}
	if page.HasMore {
		if next <= 0 || (cursor != nil && next >= *cursor) {
			return page, fmt.Errorf("闲鱼消息分页游标未推进，请刷新后重试")
		}
		page.NextCursor = &next
	}
	return page, nil
}

func normalizeChatUserID(value any) string {
	return strings.TrimSuffix(textValue(value), "@goofish")
}

// session.sync describes the logged-in participant in userInfo and the other
// participant in ownerInfo. Prefer exact IDs when available so ownerInfo is
// never mistaken for the logged-in user on a differently oriented response.
func cloudChatParticipants(session map[string]any, selfID string) (self, peer map[string]any) {
	user, owner := childMap(session, "userInfo"), childMap(session, "ownerInfo")
	selfID = normalizeChatUserID(selfID)
	if selfID != "" {
		if normalizeChatUserID(user["userId"]) == selfID {
			return user, owner
		}
		if normalizeChatUserID(owner["userId"]) == selfID {
			return owner, user
		}
	}
	// message.sync masks user IDs (for example 2***5), so its embedded session
	// must retain the same participant roles as session.sync.
	if len(owner) > 0 {
		return user, owner
	}
	return nil, user
}

func cloudChatSenderIsSelf(sender, session map[string]any, selfID string) bool {
	senderID := normalizeChatUserID(sender["userId"])
	selfID = normalizeChatUserID(selfID)
	if senderID != "" && selfID != "" && senderID == selfID {
		return true
	}
	self, peer := cloudChatParticipants(session, selfID)
	selfRef, peerRef := normalizeChatUserID(self["userId"]), normalizeChatUserID(peer["userId"])
	if senderID != "" && selfRef != peerRef {
		if senderID == selfRef {
			return true
		}
		if senderID == peerRef {
			return false
		}
	}
	// A genuine, unmasked sender ID is stronger evidence than profile fields.
	if senderID != "" && !strings.Contains(senderID, "*") {
		return false
	}
	// Masked IDs can collide. Compare the two participants' avatars first,
	// then their names, only when those fields distinguish the participants.
	for _, field := range []func(map[string]any) string{chatUserAvatar, chatUserName} {
		value, mine, theirs := field(sender), field(self), field(peer)
		if value != "" && mine != theirs {
			if value == mine {
				return true
			}
			if value == theirs {
				return false
			}
		}
	}
	return false
}

func chatUserAvatar(user map[string]any) string {
	return firstNonemptyAPI(textValue(user["logo"]), textValue(user["avatar"]))
}

func cloudMessageContent(content map[string]any) (string, string, []string) {
	display := xianyu.ParseChatContent(content)
	return display.Type, display.Text, display.Images
}

func chatUserName(user map[string]any) string {
	if boolValue(user["hasFishNick"]) && textValue(user["fishNick"]) != "" {
		return textValue(user["fishNick"])
	}
	return firstNonemptyAPI(textValue(user["nick"]), textValue(user["fishNick"]))
}

func chatInteger(value any) int64 {
	switch n := value.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		parsed, _ := strconv.ParseInt(textValue(value), 10, 64)
		return parsed
	}
}

func chatTimestamp(value any) int64 {
	n := chatInteger(value)
	if n > 0 && n < 1_000_000_000_000 {
		n *= 1000
	}
	return n
}
