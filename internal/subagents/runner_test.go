package subagents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"ascaris/internal/api"
	"ascaris/internal/config"
)

func TestRunnerRunMarksAssignmentFailedOnInvalidStructuredResult(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(subagentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return subagentSSETextResponse(subagentFinalTextSSE(`{"summary":"ok","verification":"done","blockers":[]} trailing`)), nil
	}))
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	registry := NewRegistry()
	assignment, err := registry.Create("worker-1", "explorer", "inspect fixture", "read only", []string{"read_file"}, []string{"Inspect fixture.txt"})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if err := SaveRegistry(root, registry); err != nil {
		t.Fatalf("save registry: %v", err)
	}

	runner := Runner{
		Root:          root,
		Config:        config.Empty(),
		Model:         "sonnet",
		MaxIterations: 4,
	}
	_, err = runner.Run(context.Background(), assignment)
	if err == nil || !strings.Contains(err.Error(), "invalid subagent result") {
		t.Fatalf("expected invalid structured result error, got %v", err)
	}

	loaded, err := LoadRegistry(root)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	snapshot := loaded.Snapshot()
	if len(snapshot.Assignments) != 1 {
		t.Fatalf("unexpected assignments: %#v", snapshot.Assignments)
	}
	stored := snapshot.Assignments[0]
	if stored.Status != StatusFailed {
		t.Fatalf("expected failed assignment, got %#v", stored)
	}
	if !strings.Contains(stored.Error, "invalid subagent result") {
		t.Fatalf("expected persisted parse error, got %#v", stored)
	}
	if stored.TokenUsage.OutputTokens != 5 {
		t.Fatalf("expected token usage to persist on parse failure, got %#v", stored.TokenUsage)
	}
}

func TestRunnerRunPersistsTokenUsageOnProviderFailure(t *testing.T) {
	callCount := 0
	restoreTransport := api.SetTransportForTesting(subagentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		callCount++
		switch callCount {
		case 1:
			return subagentSSETextResponse(subagentToolUseSSE("toolu_read", "read_file", `{"path":"fixture.txt"}`)), nil
		case 2:
			return nil, errors.New("provider failed after tool call")
		default:
			t.Fatalf("unexpected HTTP call %d", callCount)
			return nil, nil
		}
	}))
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	registry := NewRegistry()
	assignment, err := registry.Create("worker-1", "explorer", "inspect fixture", "read only", []string{"read_file"}, []string{"Inspect fixture.txt"})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if err := SaveRegistry(root, registry); err != nil {
		t.Fatalf("save registry: %v", err)
	}

	runner := Runner{
		Root:          root,
		Config:        config.Empty(),
		Model:         "sonnet",
		MaxIterations: 4,
		ExecuteTool: func(ctx context.Context, call ToolCall, iteration int, allowedTools []string) ToolResult {
			if call.Name != "read_file" {
				t.Fatalf("unexpected tool call: %#v", call)
			}
			if len(allowedTools) != 1 || allowedTools[0] != "read_file" {
				t.Fatalf("unexpected allowed tools: %#v", allowedTools)
			}
			return ToolResult{
				ToolUseID: call.ID,
				Name:      call.Name,
				Output:    `{"path":"fixture.txt","content":"fixture body"}`,
			}
		},
	}
	_, err = runner.Run(context.Background(), assignment)
	if err == nil || !strings.Contains(err.Error(), "provider failed after tool call") {
		t.Fatalf("expected provider failure, got %v", err)
	}

	loaded, err := LoadRegistry(root)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	snapshot := loaded.Snapshot()
	if len(snapshot.Assignments) != 1 {
		t.Fatalf("unexpected assignments: %#v", snapshot.Assignments)
	}
	stored := snapshot.Assignments[0]
	if stored.Status != StatusFailed {
		t.Fatalf("expected failed assignment, got %#v", stored)
	}
	if !strings.Contains(stored.Error, "provider failed after tool call") {
		t.Fatalf("expected provider error to persist, got %#v", stored)
	}
	if stored.TokenUsage.OutputTokens != 4 {
		t.Fatalf("expected first-iteration token usage to persist, got %#v", stored.TokenUsage)
	}
}

type subagentRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn subagentRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func subagentSSETextResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func subagentFinalTextSSE(text string) string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_final","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":11,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + subagentJSONString(text) + `}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":11,"output_tokens":5}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
}

func subagentToolUseSSE(id, name, input string) string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":11,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + subagentJSONString(input) + `}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":11,"output_tokens":4}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
}

func subagentJSONString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestRunnerTruncatesHugeToolOutputBeforeNextRequest(t *testing.T) {
	huge := strings.Repeat("X", 60_000)
	callCount := 0
	var secondRequest api.MessageRequest
	restoreTransport := api.SetTransportForTesting(subagentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		callCount++
		switch callCount {
		case 1:
			return subagentSSETextResponse(subagentToolUseSSE("toolu_read", "read_file", `{"path":"big.txt"}`)), nil
		case 2:
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &secondRequest); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			return subagentSSETextResponse(subagentFinalTextSSE(`{"summary":"ok","verification":"done","blockers":[]}`)), nil
		default:
			t.Fatalf("unexpected HTTP call %d", callCount)
			return nil, nil
		}
	}))
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	registry := NewRegistry()
	assignment, err := registry.Create("worker-1", "explorer", "inspect huge", "read only", []string{"read_file"}, []string{"Read big.txt"})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if err := SaveRegistry(root, registry); err != nil {
		t.Fatalf("save registry: %v", err)
	}

	runner := Runner{
		Root:          root,
		Config:        config.Empty(),
		Model:         "sonnet",
		MaxIterations: 4,
		ExecuteTool: func(_ context.Context, call ToolCall, _ int, _ []string) ToolResult {
			return ToolResult{ToolUseID: call.ID, Name: call.Name, Output: huge}
		},
	}
	if _, err := runner.Run(context.Background(), assignment); err != nil {
		t.Fatalf("run: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 HTTP calls, got %d", callCount)
	}
	// The tool_result content in the second request must never carry the
	// full 60k payload — it is capped at MaxToolOutputChars + elision marker.
	foundToolResult := false
	for _, msg := range secondRequest.Messages {
		for _, block := range msg.Content {
			if block.Type != "tool_result" {
				continue
			}
			for _, item := range block.Content {
				foundToolResult = true
				if len(item.Text) > api.MaxToolOutputChars+256 {
					t.Fatalf("tool_result not truncated: len=%d cap=%d", len(item.Text), api.MaxToolOutputChars)
				}
			}
		}
	}
	if !foundToolResult {
		t.Fatalf("expected a tool_result block in the second request, got %#v", secondRequest.Messages)
	}
}
