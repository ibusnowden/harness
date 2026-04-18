package api

import (
	"strings"
	"testing"
)

func TestParseOpenAIStreamAcceptsVLLMReasoningField(t *testing.T) {
	body := strings.NewReader(strings.Join([]string{
		`data: {"id":"chatcmpl_test","model":"qwen3.6-30b-a3b","choices":[{"delta":{"reasoning":"step 1 "}}]}`,
		``,
		`data: {"id":"chatcmpl_test","choices":[{"delta":{"reasoning_content":"step 2 "}}]}`,
		``,
		`data: {"id":"chatcmpl_test","choices":[{"delta":{"content":"final answer"}}]}`,
		``,
		`data: {"id":"chatcmpl_test","choices":[{"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: {"id":"chatcmpl_test","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":5}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))

	response, err := parseOpenAIStream(body, nil)
	if err != nil {
		t.Fatalf("parse stream: %v", err)
	}
	if response.FinalText() != "final answer" {
		t.Fatalf("unexpected final text: %q", response.FinalText())
	}
	if len(response.Content) < 2 || response.Content[0].Type != "thinking" {
		t.Fatalf("expected leading thinking block, got %#v", response.Content)
	}
	if response.Content[0].Thinking != "step 1 step 2 " {
		t.Fatalf("unexpected thinking content: %q", response.Content[0].Thinking)
	}
}

func TestParseOpenAIStreamRejectsToolFinishWithoutCalls(t *testing.T) {
	body := strings.NewReader(strings.Join([]string{
		`data: {"id":"chatcmpl_test","model":"qwen3.6-30b-a3b","choices":[{"delta":{"content":"\n\n","tool_calls":[]}}]}`,
		``,
		`data: {"id":"chatcmpl_test","choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))

	if _, err := parseOpenAIStream(body, nil); err == nil || !strings.Contains(err.Error(), "tool_calls finish reason") {
		t.Fatalf("expected tool_calls finish error, got %v", err)
	}
}
