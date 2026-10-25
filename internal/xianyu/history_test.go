package xianyu

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestReadChatHistoryReusesWriterAndCleansPendingRPC(t *testing.T) {
	active := &activeWriter{pending: map[string]chan map[string]any{}}
	active.send = func(_ int, raw []byte) error {
		var request map[string]any
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		if request["lwp"] != "/r/Conversation/listNewestPagination" {
			t.Fatalf("unexpected route: %v", request["lwp"])
		}
		mid := responseMID(request)
		active.pendingMu.Lock()
		waiter := active.pending[mid]
		active.pendingMu.Unlock()
		if waiter == nil {
			t.Fatal("RPC waiter not installed before sending")
		}
		waiter <- map[string]any{"code": json.Number("200"), "body": map[string]any{"userConvs": []any{}, "hasMore": false}}
		return nil
	}
	m := &Manager{writers: map[string]*activeWriter{"a": active}}
	result, err := m.ReadChatHistory(context.Background(), "a", "/r/Conversation/listNewestPagination", []any{9007199254740991, 20})
	if err != nil || result["hasMore"] != false || len(active.pending) != 0 {
		t.Fatalf("query or waiter cleanup failed: %v %v", result, err)
	}
	if _, err := m.ReadChatHistory(context.Background(), "a", "/r/MessageStatus/read", nil); err == nil {
		t.Fatal("history reader allowed a read receipt")
	}
}

func TestReadChatHistoryCancellationAndDisconnectedAccount(t *testing.T) {
	active := &activeWriter{pending: map[string]chan map[string]any{}, send: func(int, []byte) error { return nil }}
	m := &Manager{writers: map[string]*activeWriter{"a": active}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := m.ReadChatHistory(ctx, "a", "/r/MessageManager/listUserMessages", nil); err == nil || len(active.pending) != 0 {
		t.Fatal("cancelled RPC was retained")
	}
	if _, err := m.ReadChatHistory(context.Background(), "missing", "/r/Conversation/listNewestPagination", nil); err == nil {
		t.Fatal("missing connection accepted")
	}
}
