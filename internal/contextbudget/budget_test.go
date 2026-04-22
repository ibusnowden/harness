package contextbudget

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ascaris/internal/api"
)

func TestModelContextWindow_Known(t *testing.T) {
	if got := ModelContextWindow("qwen3.6-30b-a3b"); got != 262144 {
		t.Fatalf("qwen window wrong: %d", got)
	}
	if got := ModelContextWindow("glm-4.7-flash"); got != 32768 {
		t.Fatalf("glm window wrong: %d", got)
	}
	if got := ModelContextWindow("unknown-model"); got != 0 {
		t.Fatalf("unknown should be 0, got %d", got)
	}
}

func TestCompactSubagentMessages_NoOpUnderBudget(t *testing.T) {
	messages := []api.InputMessage{
		api.UserTextMessage("short prompt"),
		{Role: "assistant", Content: []api.InputContentBlock{{Type: "text", Text: "short reply"}}},
	}
	out, removed, triggered := CompactSubagentMessages(messages, "sys", nil, "qwen3.6-30b-a3b", 512)
	if triggered || removed != 0 {
		t.Fatalf("expected no-op, got triggered=%v removed=%d", triggered, removed)
	}
	if len(out) != len(messages) {
		t.Fatalf("expected passthrough, got len=%d", len(out))
	}
}

func TestCompactSubagentMessages_DropsOldestWhenOverBudget(t *testing.T) {
	big := strings.Repeat("x", 50000)
	prompt := api.UserTextMessage("hello")
	// Build several assistant+tool_use/tool_result pairs that together
	// comfortably exceed the glm-4.7-flash (32k) input budget.
	messages := []api.InputMessage{prompt}
	for i := 0; i < 4; i++ {
		messages = append(messages, api.InputMessage{
			Role: "assistant",
			Content: []api.InputContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_" + string(rune('a'+i)),
				Name:  "read_file",
				Input: json.RawMessage(`{"path":"x"}`),
			}},
		})
		messages = append(messages, api.ToolResultMessage([]api.ToolResultEnvelope{{
			ToolUseID: "toolu_" + string(rune('a'+i)),
			Output:    big,
		}}))
	}
	out, _, triggered := CompactSubagentMessages(messages, "sys", nil, "glm-4.7-flash", 512)
	if !triggered {
		t.Fatalf("expected compaction to trigger")
	}
	if !ValidToolHistory(out) {
		t.Fatalf("compaction broke tool history pairing: %#v", out)
	}
	if out[0].Role != "user" {
		t.Fatalf("expected user prompt preserved at position 0, got %+v", out[0])
	}
	// The compacted request must now fit the budget.
	req := api.MessageRequest{Model: "glm-4.7-flash", System: "sys", Messages: out}
	budget := InputBudget(ModelContextWindow("glm-4.7-flash"), 512)
	if got := api.EstimateRequestInputTokens(req); got > budget {
		t.Fatalf("compacted request still over budget: got=%d budget=%d", got, budget)
	}
}

func TestCompactSubagentMessages_UnknownModelIsNoOp(t *testing.T) {
	messages := []api.InputMessage{api.UserTextMessage(strings.Repeat("x", 100000))}
	out, removed, triggered := CompactSubagentMessages(messages, "", nil, "no-such-model", 512)
	if triggered || removed != 0 {
		t.Fatalf("unknown model should disable compaction, got triggered=%v", triggered)
	}
	if len(out) != 1 {
		t.Fatalf("expected passthrough, got len=%d", len(out))
	}
}

func TestParseServedContextLimitError_VLLMMessage(t *testing.T) {
	err := errors.New(`This model's maximum context length is 262144 tokens. However, you requested 300000 tokens (prompt contains at least 290000 input tokens).`)
	got, ok := ParseServedContextLimitError(err)
	if !ok {
		t.Fatalf("expected parse ok")
	}
	if got.ContextWindow != 262144 {
		t.Fatalf("wrong window: %d", got.ContextWindow)
	}
	if got.InputTokens != 290000 {
		t.Fatalf("wrong input tokens: %d", got.InputTokens)
	}
}

func TestParseServedContextLimitError_UnrelatedError(t *testing.T) {
	if _, ok := ParseServedContextLimitError(errors.New("some other error")); ok {
		t.Fatalf("expected no match for unrelated error")
	}
	if _, ok := ParseServedContextLimitError(nil); ok {
		t.Fatalf("expected no match for nil")
	}
}

func TestValidToolHistory_DetectsDanglingResult(t *testing.T) {
	messages := []api.InputMessage{
		api.ToolResultMessage([]api.ToolResultEnvelope{{ToolUseID: "missing", Output: "orphan"}}),
	}
	if ValidToolHistory(messages) {
		t.Fatalf("expected invalid (dangling tool_result)")
	}
}
