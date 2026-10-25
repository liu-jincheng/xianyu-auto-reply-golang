package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zhinianboke/xianyu-auto-go/internal/database"
	"github.com/zhinianboke/xianyu-auto-go/internal/xianyu"
)

func TestCloudConversationsSortByFullTimeWithoutChangingPlatformCursor(t *testing.T) {
	data := cloudConversationFixture()
	base := data["sessions"].([]any)[0].(map[string]any)
	data["sessions"] = []any{}
	for index, date := range []string{"2024-12-31T23:59:59+08:00", "2026-01-01T00:00:01+08:00", "2025-11-30T12:00:00+08:00"} {
		parsed, err := time.Parse(time.RFC3339, date)
		if err != nil {
			t.Fatal(err)
		}
		copySession := map[string]any{}
		for key, value := range base["session"].(map[string]any) {
			copySession[key] = value
		}
		copySession["sessionId"] = date
		data["sessions"] = append(data["sessions"].([]any), map[string]any{
			"session": copySession,
			"message": map[string]any{"summary": map[string]any{"ts": parsed.UnixMilli(), "sortIndex": 5000 - index}},
		})
	}
	page, err := parseCloudConversations(data, "owner", nil)
	if err != nil || len(page.Conversations) != 3 || page.Conversations[0]["cid"] != "2026-01-01T00:00:01+08:00" || page.Conversations[1]["cid"] != "2025-11-30T12:00:00+08:00" || page.NextCursor == nil || *page.NextCursor != 4998 {
		t.Fatalf("incorrect ordering or pagination: %+v (%v)", page, err)
	}
}

func cloudConversationFixture() map[string]any {
	return map[string]any{"hasMore": true, "sessions": []any{
		map[string]any{
			"session": map[string]any{
				"sessionId": json.Number("9000000000000000001"), "sessionType": 1,
				"userInfo":  map[string]any{"userId": "owner", "nick": "我自己", "logo": "https://example.com/self.jpg"},
				"ownerInfo": map[string]any{"userId": "buyer@goofish", "nick": "昵称", "fishNick": "买家", "hasFishNick": true, "logo": "https://example.com/avatar.jpg"},
				"itemInfo":  map[string]any{"title": "商品"},
			},
			"message": map[string]any{"summary": map[string]any{"sortIndex": int64(1700000000000), "summary": "你好", "ts": int64(1700000000), "unread": 3}},
		},
	}}
}

func cloudMessageFixture(id, sender string, timestamp, version int64, content map[string]any) map[string]any {
	return map[string]any{
		"messageUuid": "uuid-" + id, "extJson": `{"messageId":` + id + `}`,
		"senderInfo": map[string]any{"userId": sender, "nick": "昵称"},
		"timeStamp":  timestamp, "version": version, "content": content,
	}
}

func newCloudChatTestServer(t *testing.T) *Server {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`INSERT INTO xy_accounts(owner_id,account_id,cookie) VALUES(1,'account-a','unb=owner'),(2,'account-b','unb=other')`); err != nil {
		t.Fatal(err)
	}
	return &Server{db: db}
}

func cloudChatRequest(s *Server, user User, path string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, user)))
		})
	})
	router.Get("/conversations/{account_id}", s.chatConversations)
	router.Get("/messages/{account_id}/{cid}", s.chatMessages)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestChatConversationsLoadsCloudWithoutLocalMessages(t *testing.T) {
	s := newCloudChatTestServer(t)
	calls := 0
	s.chatMTOPCall = func(ctx context.Context, opt xianyu.MTOPOptions) xianyu.MTOPResult {
		calls++
		if opt.API != "mtop.taobao.idlemessage.pc.session.sync" || opt.Version != "3.0" || opt.Data["sessionTypes"] != "[1]" || opt.Data["fetchNum"] != 20 || opt.Data["sortIndex"] != int64(1800000000000) || opt.ExtraHeaders["idle_site_biz_code"] != "" {
			t.Fatalf("incorrect cloud session request: %s v%s data=%v", opt.API, opt.Version, opt.Data)
		}
		return xianyu.MTOPResult{Success: true, Response: map[string]any{"data": cloudConversationFixture()}, Cookie: "unb=owner; refreshed=yes"}
	}
	w := cloudChatRequest(s, User{ID: 1, Role: "MEMBER"}, "/conversations/account-a?cursor=1800000000000&limit=20")
	var body struct {
		Success bool
		Data    chatConversationPage
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 || !body.Success || len(body.Data.Conversations) != 1 || calls != 1 {
		t.Fatalf("cloud conversations missing: %s (%v)", w.Body.String(), err)
	}
	conversation := body.Data.Conversations[0]
	if conversation["cid"] != "9000000000000000001" || conversation["otherUserId"] != "buyer" || conversation["otherUserName"] != "买家" || conversation["unreadCount"] != float64(3) || conversation["lastMessageTime"] != float64(1700000000000) || !body.Data.HasMore || body.Data.NextCursor == nil || *body.Data.NextCursor != 1700000000000 {
		t.Fatalf("incorrect conversation mapping: %+v", body.Data)
	}
	var cookie string
	if err := s.db.QueryRow(`SELECT cookie FROM xy_accounts WHERE account_id='account-a'`).Scan(&cookie); err != nil || cookie != "unb=owner; refreshed=yes" {
		t.Fatalf("refreshed chat cookie not saved: %v", err)
	}
	var events int
	if err := s.db.QueryRow(`SELECT count(*) FROM event_logs`).Scan(&events); err != nil || events != 0 {
		t.Fatal("reading chat history generated message events", err)
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("chat response may be cached")
	}
}

func TestChatMessagesLoadsCloudHistoryAndPreservesDirection(t *testing.T) {
	s := newCloudChatTestServer(t)
	s.chatMTOPCall = func(ctx context.Context, opt xianyu.MTOPOptions) xianyu.MTOPResult {
		var request map[string]any
		decoder := json.NewDecoder(strings.NewReader(textValue(opt.Data["req"])))
		decoder.UseNumber()
		if err := decoder.Decode(&request); err != nil || opt.API != "mtop.taobao.idlemessage.pc.message.sync" || opt.Version != "1.0" || request["sessionId"] != "cid" || chatInteger(request["version"]) != 9000 || request["includeRequestMsg"] != true {
			t.Fatalf("incorrect cloud message request: %s data=%v (%v)", opt.API, request, err)
		}
		return xianyu.MTOPResult{Success: true, Response: map[string]any{"data": map[string]any{
			"hasMore": true, "result": map[string]any{"success": true},
			"messages": []any{
				cloudMessageFixture("9000000000000000002", "buyer", 1700000002000, 8000, map[string]any{"contentType": 2, "image": map[string]any{"pics": []any{map[string]any{"url": "https://example.com/image.jpg"}}}}),
				cloudMessageFixture("9000000000000000001", "owner@goofish", 1700000001000, 7000, map[string]any{"contentType": 1, "text": map[string]any{"text": "你好"}}),
			},
		}}}
	}
	w := cloudChatRequest(s, User{ID: 1, Role: "MEMBER"}, "/messages/account-a/cid?cursor=9000")
	var body struct {
		Success bool
		Data    chatMessagePage
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || !body.Success || len(body.Data.Messages) != 2 || body.Data.NextCursor == nil || *body.Data.NextCursor != 7000 {
		t.Fatalf("cloud history missing: %s (%v)", w.Body.String(), err)
	}
	first, second := body.Data.Messages[0], body.Data.Messages[1]
	if first["messageId"] != "9000000000000000001" || first["isSelf"] != true || second["isSelf"] != false || second["type"] != "image" || len(second["images"].([]any)) != 1 {
		t.Fatalf("history order, direction or image mapping incorrect: %v", body.Data.Messages)
	}
}

func TestCloudChatRejectsUnauthorizedAccountsAndInvalidCursors(t *testing.T) {
	s := newCloudChatTestServer(t)
	s.chatMTOPCall = func(context.Context, xianyu.MTOPOptions) xianyu.MTOPResult {
		t.Fatal("unapproved account or invalid cursor reached the platform")
		return xianyu.MTOPResult{}
	}
	for _, path := range []string{"/conversations/account-b", "/messages/account-b/cid", "/conversations/account-a?cursor=invalid", "/messages/account-a/cid?cursor=-1"} {
		w := cloudChatRequest(s, User{ID: 1, Role: "MEMBER"}, path)
		if w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest {
			t.Errorf("invalid request succeeded: %s %s", path, w.Body.String())
		}
	}
}

func TestCloudChatErrorsAreNotReportedAsEmptyHistory(t *testing.T) {
	s := newCloudChatTestServer(t)
	s.chatMTOPCall = func(context.Context, xianyu.MTOPOptions) xianyu.MTOPResult {
		return xianyu.MTOPResult{Error: "FAIL_SYS_SESSION_EXPIRED"}
	}
	for _, path := range []string{"/conversations/account-a", "/messages/account-a/cid"} {
		w := cloudChatRequest(s, User{ID: 1, Role: "MEMBER"}, path)
		var body response
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Success || !strings.Contains(body.Message, "FAIL_SYS_SESSION_EXPIRED") {
			t.Fatalf("cloud error disguised as empty history: %s (%v)", w.Body.String(), err)
		}
	}
}

func TestCloudChatPaginationAndSchemaFailures(t *testing.T) {
	cursor := int64(1700000000000)
	if _, err := parseCloudConversations(cloudConversationFixture(), "owner", &cursor); err == nil {
		t.Fatal("stuck conversation cursor accepted")
	}
	if _, err := parseCloudConversations(map[string]any{"hasMore": false}, "owner", nil); err == nil {
		t.Fatal("missing cloud session list accepted")
	}
	if _, err := parseCloudMessages(map[string]any{"hasMore": false}, "owner", nil); err == nil {
		t.Fatal("missing cloud message list accepted")
	}
	if _, err := parseCloudMessages(map[string]any{"result": map[string]any{"success": false, "errCode": "denied"}, "messages": []any{}}, "owner", nil); err == nil {
		t.Fatal("platform business error accepted")
	}
	conversations, err := parseCloudConversations(map[string]any{"sessions": []any{}, "hasMore": false}, "owner", nil)
	if err != nil || conversations.Conversations == nil || len(conversations.Conversations) != 0 || conversations.NextCursor != nil {
		t.Fatal("valid empty conversation history rejected", err)
	}
	messages, err := parseCloudMessages(map[string]any{"messages": []any{}, "hasMore": false}, "owner", nil)
	if err != nil || messages.Messages == nil || len(messages.Messages) != 0 || messages.NextCursor != nil {
		t.Fatal("valid empty message history rejected", err)
	}
}

func TestCloudConversationPeerUsesTheOtherParticipant(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		data := cloudConversationFixture()
		session := childMap(data["sessions"].([]any)[0].(map[string]any), "session")
		if reverse {
			session["userInfo"], session["ownerInfo"] = session["ownerInfo"], session["userInfo"]
		}
		page, err := parseCloudConversations(data, "owner@goofish", nil)
		if err != nil || len(page.Conversations) != 1 {
			t.Fatalf("cannot parse participants: %v", err)
		}
		peer := page.Conversations[0]
		if peer["otherUserId"] != "buyer" || peer["otherUserName"] != "买家" || peer["otherUserAvatar"] != "https://example.com/avatar.jpg" {
			t.Fatalf("conversation displays the account instead of the peer (reversed=%v): %v", reverse, peer)
		}
	}
}

func TestCloudMaskedMessagesKeepBothSenderDirections(t *testing.T) {
	for _, collision := range []bool{false, true} {
		peerMask := "1***3"
		if collision {
			peerMask = "2***5"
		}
		self := map[string]any{"userId": "2***5", "nick": "我方", "logo": "https://example.com/self.jpg"}
		peer := map[string]any{"userId": peerMask, "nick": "对方", "logo": "https://example.com/peer.jpg"}
		session := map[string]any{"userInfo": self, "ownerInfo": peer}
		outgoing := cloudMessageFixture("9000000000000000001", "2***5", 1700000001000, 8000, map[string]any{"contentType": 1, "text": map[string]any{"text": "我发的"}})
		outgoing["senderInfo"], outgoing["sessionInfo"] = self, session
		incoming := cloudMessageFixture("9000000000000000002", peerMask, 1700000002000, 8001, map[string]any{"contentType": 1, "text": map[string]any{"text": "对方发的"}})
		incoming["senderInfo"], incoming["sessionInfo"] = peer, session
		page, err := parseCloudMessages(map[string]any{"hasMore": false, "messages": []any{incoming, outgoing}}, "20005@goofish", nil)
		if err != nil || len(page.Messages) != 2 {
			t.Fatalf("cannot parse masked messages: %v", err)
		}
		if page.Messages[0]["isSelf"] != true || page.Messages[1]["isSelf"] != false || page.Messages[0]["senderId"] != "20005" || page.Messages[0]["senderAvatar"] != self["logo"] || page.Messages[1]["senderAvatar"] != peer["logo"] {
			t.Fatalf("masked participants collapsed to one side (collision=%v): %v", collision, page.Messages)
		}
	}
}

func TestCloudMessageDirectionPrefersExactIDsOverSharedProfiles(t *testing.T) {
	session := map[string]any{
		"userInfo":  map[string]any{"userId": "1000", "nick": "同名", "logo": "https://example.com/default.jpg"},
		"ownerInfo": map[string]any{"userId": "2000", "nick": "同名", "logo": "https://example.com/default.jpg"},
	}
	if cloudChatSenderIsSelf(childMap(session, "ownerInfo"), session, "1000") {
		t.Fatal("shared name and avatar caused the peer to be attributed to self")
	}
	if !cloudChatSenderIsSelf(childMap(session, "userInfo"), session, "1000") {
		t.Fatal("self was not recognized from the exact user ID")
	}
}
