package main

import (
	"encoding/json"
	"testing"

	"github.com/zhinianboke/xianyu-auto-go/internal/xianyu"
)

func TestIncomingChatPushPreservesCards(t *testing.T) {
	raw := incomingChatPush(xianyu.Incoming{AccountID: "a", ChatID: "cid", SenderID: "peer", Type: "card", Text: "商品", Card: &xianyu.ChatCard{Kind: "item", Title: "商品", Price: "¥28"}})
	var envelope struct {
		Event   string
		Message struct {
			Type           string
			Card           *xianyu.ChatCard
			ContentVersion int
		}
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Event != "new_message" || envelope.Message.Type != "card" || envelope.Message.Card == nil || envelope.Message.Card.Price != "¥28" || envelope.Message.ContentVersion != xianyu.ChatContentVersion {
		t.Fatalf("live card lost: %s (%v)", raw, err)
	}
}
