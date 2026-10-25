package xianyu

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// ReadChatHistory reuses the registered IM connection. These RPCs only read
// history; their replies bypass incoming-message dispatch and automatic replies.
func (m *Manager) ReadChatHistory(ctx context.Context, accountID, path string, body []any) (map[string]any, error) {
	switch path {
	case "/r/Conversation/listNewestPagination", "/r/MessageManager/listUserMessages":
	default:
		return nil, fmt.Errorf("不支持的聊天历史查询")
	}
	m.mu.Lock()
	active := m.writers[accountID]
	m.mu.Unlock()
	if active == nil {
		return nil, fmt.Errorf("账号IM未连接，请等待连接成功后刷新")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	mid := Mid()
	waiter := make(chan map[string]any, 1)
	active.pendingMu.Lock()
	active.pending[mid] = waiter
	active.pendingMu.Unlock()
	defer func() { active.pendingMu.Lock(); delete(active.pending, mid); active.pendingMu.Unlock() }()
	payload, err := json.Marshal(map[string]any{"lwp": path, "headers": map[string]string{"mid": mid}, "body": body})
	if err != nil {
		return nil, err
	}
	if err = active.send(websocket.TextMessage, payload); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("聊天历史查询未完成：%w", ctx.Err())
	case result := <-waiter:
		if fmt.Sprint(result["code"]) != "200" {
			return nil, fmt.Errorf("闲鱼IM历史查询失败：%s", responseError(result))
		}
		data, ok := result["body"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("闲鱼IM历史响应格式异常")
		}
		return data, nil
	}
}
