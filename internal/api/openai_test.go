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

func TestParseOpenAIStreamRejectsMalformedToolArguments(t *testing.T) {
	body := strings.NewReader(strings.Join([]string{
		`data: {"id":"chatcmpl_test","model":"qwen3.6-30b-a3b","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"write_file","arguments":"{\"path\":\"broken.txt\",\"content\":\"unterminated"}}]}}]}`,
		``,
		`data: {"id":"chatcmpl_test","choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))

	if _, err := parseOpenAIStream(body, nil); err == nil || !strings.Contains(err.Error(), "arguments are not valid JSON") {
		t.Fatalf("expected malformed tool argument error, got %v", err)
	}
}

func TestParseSSERejectsMalformedToolArguments(t *testing.T) {
	body := strings.NewReader(strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":11,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_bad","name":"write_file","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"broken.txt\",\"content\":\"unterminated"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n"))

	if _, err := parseSSE(body, nil); err == nil || !strings.Contains(err.Error(), "arguments are not valid JSON") {
		t.Fatalf("expected malformed anthropic-style tool argument error, got %v", err)
	}
}
