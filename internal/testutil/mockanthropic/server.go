package mockanthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"ascaris/internal/api"
)

const ScenarioPrefix = "PARITY_SCENARIO:"

type Transport struct{}

func NewTransport() http.RoundTripper {
	return Transport{}
}

func (Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path != "/v1/messages" {
		return responseFor(http.StatusNotFound, "text/plain", "not found"), nil
	}
	var payload api.MessageRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		return responseFor(http.StatusBadRequest, "text/plain", err.Error()), nil
	}
	scenario := detectScenario(payload)
	if scenario == "" {
		return responseFor(http.StatusBadRequest, "text/plain", "missing parity scenario"), nil
	}
	body := buildStreamResponse(payload, scenario)
	return responseFor(http.StatusOK, "text/event-stream", body), nil
}

func responseFor(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func detectScenario(request api.MessageRequest) string {
	for i := len(request.Messages) - 1; i >= 0; i-- {
		message := request.Messages[i]
		for j := len(message.Content) - 1; j >= 0; j-- {
			block := message.Content[j]
			if block.Type != "text" {
				continue
			}
			for _, token := range strings.Fields(block.Text) {
				if value, ok := strings.CutPrefix(token, ScenarioPrefix); ok {
					return value
				}
			}
		}
	}
	return ""
}

func buildStreamResponse(request api.MessageRequest, scenario string) string {
	switch scenario {
	case "streaming_text":
		return finalTextSSE("Mock streaming says hello from the parity harness.", 11, 8)
	case "read_file_roundtrip":
		if output, _ := latestToolResult(request); output != "" {
			return finalTextSSE("read_file roundtrip complete: "+extractReadContent(output), 14, 9)
		}
		return toolUseSSE("toolu_read_fixture", "read_file", []string{`{"path":"fixture.txt"}`})
	case "grep_chunk_assembly":
		if output, _ := latestToolResult(request); output != "" {
			return finalTextSSE(fmt.Sprintf("grep_search matched %d occurrences", extractNumMatches(output)), 14, 8)
		}
		return toolUseSSE("toolu_grep_fixture", "grep_search", []string{
			`{"pattern":"par`,
			`ity","path":"fixture.txt"`,
			`,"output_mode":"count"}`,
		})
	case "write_file_allowed":
		if output, _ := latestToolResult(request); output != "" {
			return finalTextSSE("write_file succeeded: "+extractFilePath(output), 14, 7)
		}
		return toolUseSSE("toolu_write_allowed", "write_file", []string{`{"path":"generated/output.txt","content":"created by mock service\n"}`})
	case "write_file_denied":
		if output, _ := latestToolResult(request); output != "" {
			return finalTextSSE("write_file denied as expected: "+output, 14, 10)
		}
		return toolUseSSE("toolu_write_denied", "write_file", []string{`{"path":"generated/denied.txt","content":"should not exist\n"}`})
	case "multi_tool_turn_roundtrip":
		results := toolResultsByName(request)
		readOutput, readOK := results["read_file"]
		grepOutput, grepOK := results["grep_search"]
		if readOK && grepOK {
			return finalTextSSE(fmt.Sprintf("multi-tool roundtrip complete: %s / %d occurrences", extractReadContent(readOutput.Output), extractNumMatches(grepOutput.Output)), 16, 10)
		}
		return toolUsesSSE([]toolUseSpec{
			{ID: "toolu_multi_read", Name: "read_file", Chunks: []string{`{"path":"fixture.txt"}`}},
			{ID: "toolu_multi_grep", Name: "grep_search", Chunks: []string{
				`{"pattern":"par`,
				`ity","path":"fixture.txt"`,
				`,"output_mode":"count"}`,
			}},
		})
	case "bash_stdout_roundtrip":
		if output, _ := latestToolResult(request); output != "" {
			return finalTextSSE("bash completed: "+extractBashStdout(output), 14, 7)
		}
		return toolUseSSE("toolu_bash_stdout", "bash", []string{`{"command":"printf 'alpha from bash'","timeout":1000}`})
	case "bash_permission_prompt_approved":
		if output, isError := latestToolResult(request); output != "" {
			if isError {
				return finalTextSSE("bash approval unexpectedly failed: "+output, 14, 9)
			}
			return finalTextSSE("bash approved and executed: "+extractBashStdout(output), 14, 9)
		}
		return toolUseSSE("toolu_bash_prompt_allow", "bash", []string{`{"command":"printf 'approved via prompt'","timeout":1000}`})
	case "bash_permission_prompt_denied":
		if output, _ := latestToolResult(request); output != "" {
			return finalTextSSE("bash denied as expected: "+output, 14, 9)
		}
		return toolUseSSE("toolu_bash_prompt_deny", "bash", []string{`{"command":"printf 'should not run'","timeout":1000}`})
	case "auto_compact_triggered":
		return finalTextSSE("auto compact parity complete.", 50000, 200)
	case "token_cost_reporting":
		return finalTextSSE("token cost reporting parity complete.", 1000, 500)
	default:
		return finalTextSSE("unknown parity scenario: "+scenario, 5, 5)
	}
}

type toolResult struct {
	Output  string
	IsError bool
}

func latestToolResult(request api.MessageRequest) (string, bool) {
	for i := len(request.Messages) - 1; i >= 0; i-- {
		for j := len(request.Messages[i].Content) - 1; j >= 0; j-- {
			block := request.Messages[i].Content[j]
			if block.Type != "tool_result" {
				continue
			}
			return flattenToolResult(block.Content), block.IsError
		}
	}
	return "", false
}

func toolResultsByName(request api.MessageRequest) map[string]toolResult {
	namesByID := map[string]string{}
	for _, message := range request.Messages {
		for _, block := range message.Content {
			if block.Type == "tool_use" {
				namesByID[block.ID] = block.Name
			}
		}
	}
	results := map[string]toolResult{}
	for i := len(request.Messages) - 1; i >= 0; i-- {
		for j := len(request.Messages[i].Content) - 1; j >= 0; j-- {
			block := request.Messages[i].Content[j]
			if block.Type != "tool_result" {
				continue
			}
			name := namesByID[block.ToolUseID]
			if name == "" {
				name = block.ToolUseID
			}
			if _, exists := results[name]; exists {
				continue
			}
			results[name] = toolResult{
				Output:  flattenToolResult(block.Content),
				IsError: block.IsError,
			}
		}
	}
	return results
}

func flattenToolResult(content []api.ToolResultContentBlock) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "json":
			parts = append(parts, string(block.Value))
		}
	}
	return strings.Join(parts, "\n")
}

func finalTextSSE(text string, inputTokens, outputTokens int) string {
	var body strings.Builder
	appendSSE(&body, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            "msg_final",
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         "claude-sonnet-4-6",
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         usageJSON(inputTokens, 0),
		},
	})
	appendSSE(&body, "content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         0,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
	appendSSE(&body, "content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
	appendSSE(&body, "content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 0,
	})
	appendSSE(&body, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": usageJSON(inputTokens, outputTokens),
	})
	appendSSE(&body, "message_stop", map[string]any{"type": "message_stop"})
	return body.String()
}

type toolUseSpec struct {
	ID     string
	Name   string
	Chunks []string
}

func toolUseSSE(id, name string, chunks []string) string {
	return toolUsesSSE([]toolUseSpec{{ID: id, Name: name, Chunks: chunks}})
}

func toolUsesSSE(specs []toolUseSpec) string {
	var body strings.Builder
	appendSSE(&body, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            "msg_tool_use",
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         "claude-sonnet-4-6",
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         usageJSON(12, 0),
		},
	})
	for index, spec := range specs {
		appendSSE(&body, "content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    spec.ID,
				"name":  spec.Name,
				"input": map[string]any{},
			},
		})
		for _, chunk := range spec.Chunks {
			appendSSE(&body, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": index,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": chunk},
			})
		}
		appendSSE(&body, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": index,
		})
	}
	appendSSE(&body, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil},
		"usage": usageJSON(12, 4),
	})
	appendSSE(&body, "message_stop", map[string]any{"type": "message_stop"})
	return body.String()
}

func appendSSE(body *strings.Builder, event string, payload map[string]any) {
	data, _ := json.Marshal(payload)
	body.WriteString("event: ")
	body.WriteString(event)
	body.WriteString("\n")
	body.WriteString("data: ")
	body.Write(data)
	body.WriteString("\n\n")
}

func usageJSON(inputTokens, outputTokens int) map[string]any {
	return map[string]any{
		"input_tokens":                inputTokens,
		"cache_creation_input_tokens": 0,
		"cache_read_input_tokens":     0,
		"output_tokens":               outputTokens,
	}
}

func extractReadContent(output string) string {
	var payload struct {
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(output), &payload) == nil && payload.Content != "" {
		return strings.TrimSpace(payload.Content)
	}
	return output
}

func extractNumMatches(output string) int {
	var payload struct {
		NumMatches int `json:"num_matches"`
	}
	if json.Unmarshal([]byte(output), &payload) == nil {
		return payload.NumMatches
	}
	value, _ := strconv.Atoi(strings.TrimSpace(output))
	return value
}

func extractFilePath(output string) string {
	var payload struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(output), &payload) == nil && payload.Path != "" {
		return payload.Path
	}
	return output
}

func extractBashStdout(output string) string {
	var payload struct {
		Stdout string `json:"stdout"`
	}
	if json.Unmarshal([]byte(output), &payload) == nil {
		return payload.Stdout
	}
	return output
}
