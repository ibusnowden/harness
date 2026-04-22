package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEstimateMessagesTokens_EmptySliceIsZero(t *testing.T) {
	if got := EstimateMessagesTokens(nil); got != 0 {
		t.Fatalf("expected 0 for nil, got %d", got)
	}
	if got := EstimateMessagesTokens([]InputMessage{}); got != 0 {
		t.Fatalf("expected 0 for empty, got %d", got)
	}
}

func TestEstimateMessagesTokens_MonotonicOnAppend(t *testing.T) {
	msgs := []InputMessage{UserTextMessage("hello world")}
	base := EstimateMessagesTokens(msgs)
	msgs = append(msgs, UserTextMessage(strings.Repeat("x", 400)))
	after := EstimateMessagesTokens(msgs)
	if after <= base {
		t.Fatalf("expected token estimate to grow after append, base=%d after=%d", base, after)
	}
}

func TestEstimateRequestInputTokens_AccountsForSystemAndTools(t *testing.T) {
	plain := MessageRequest{Model: "qwen3.6-30b-a3b", Messages: []InputMessage{UserTextMessage("hi")}}
	withSystem := plain
	withSystem.System = strings.Repeat("sys ", 500)
	withTools := plain
	withTools.Tools = []ToolDefinition{{
		Name:        "read_file",
		Description: strings.Repeat("reads a file ", 100),
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	base := EstimateRequestInputTokens(plain)
	if EstimateRequestInputTokens(withSystem) <= base {
		t.Fatalf("system prompt did not raise estimate")
	}
	if EstimateRequestInputTokens(withTools) <= base {
		t.Fatalf("tools did not raise estimate")
	}
}
