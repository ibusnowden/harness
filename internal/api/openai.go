package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
)

type OpenAICompatClient struct {
	kind       ProviderKind
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func (c *OpenAICompatClient) ProviderKind() ProviderKind {
	return c.kind
}

func (c *OpenAICompatClient) StreamMessage(ctx context.Context, request MessageRequest) (MessageResponse, error) {
	return c.StreamMessageEvents(ctx, request, nil)
}

func (c *OpenAICompatClient) StreamMessageEvents(ctx context.Context, request MessageRequest, emit func(StreamEvent)) (MessageResponse, error) {
	payload, err := json.Marshal(toOpenAIRequest(request))
	if err != nil {
		return MessageResponse{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, normalizeChatCompletionsURL(c.baseURL), bytes.NewReader(payload))
	if err != nil {
		return MessageResponse{}, err
	}
	httpRequest.Header.Set("content-type", "application/json")
	httpRequest.Header.Set("authorization", "Bearer "+c.apiKey)
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return MessageResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		payload, _ := io.ReadAll(response.Body)
		msg := strings.TrimSpace(string(payload))
		if response.StatusCode == 404 && c.kind == ProviderOpenRouter {
			return MessageResponse{}, fmt.Errorf(
				"model not found on OpenRouter: %s\n\nCheck available models at https://openrouter.ai/models\nUpdate the model in .ascaris/settings.json then restart.",
				msg,
			)
		}
		return MessageResponse{}, fmt.Errorf("openai-compatible request failed: %s: %s", response.Status, msg)
	}
	return parseOpenAIStream(response.Body, emit)
}

func normalizeChatCompletionsURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/chat/completions"
	}
	return baseURL + "/chat/completions"
}

func toOpenAIRequest(request MessageRequest) map[string]any {
	messages := make([]map[string]any, 0, len(request.Messages)+1)
	if strings.TrimSpace(request.System) != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": request.System,
		})
	}
	for _, message := range request.Messages {
		messages = append(messages, convertOpenAIMessages(message)...)
	}
	payload := map[string]any{
		"model":          request.Model,
		"messages":       messages,
		"stream":         true,
		"max_tokens":     request.MaxTokens,
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(request.Tools) > 0 {
		tools := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        tool.Name,
					"description": tool.Description,
					"parameters":  rawJSONOrEmptyObject(tool.InputSchema),
				},
			})
		}
		payload["tools"] = tools
	}
	if request.ToolChoice != nil {
		switch request.ToolChoice.Type {
		case "any", "auto":
			payload["tool_choice"] = request.ToolChoice.Type
		case "tool":
			payload["tool_choice"] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": request.ToolChoice.Name,
				},
			}
		}
	}
	return payload
}

func convertOpenAIMessages(message InputMessage) []map[string]any {
	textParts := []string{}
	toolCalls := []map[string]any{}
	toolMessages := []map[string]any{}
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			toolCalls = append(toolCalls, map[string]any{
				"id":   block.ID,
				"type": "function",
				"function": map[string]any{
					"name":      block.Name,
					"arguments": compactRawJSON(block.Input),
				},
			})
		case "tool_result":
			content := ""
			for _, item := range block.Content {
				switch item.Type {
				case "text":
					content += item.Text
				case "json":
					content += string(item.Value)
				}
			}
			toolMessages = append(toolMessages, map[string]any{
				"role":         "tool",
				"tool_call_id": block.ToolUseID,
				"content":      content,
			})
		}
	}
	if len(toolMessages) > 0 {
		return toolMessages
	}
	out := map[string]any{"role": message.Role}
	if len(textParts) > 0 {
		out["content"] = strings.Join(textParts, "")
	} else {
		out["content"] = ""
	}
	if len(toolCalls) > 0 {
		out["tool_calls"] = toolCalls
	}
	return []map[string]any{out}
}

func parseOpenAIStream(body io.Reader, emit func(StreamEvent)) (MessageResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	type toolCall struct {
		ID        string
		Name      string
		Arguments strings.Builder
	}
	text := strings.Builder{}
	toolCalls := map[int]*toolCall{}
	response := MessageResponse{
		Kind:  "message",
		Role:  "assistant",
		Model: "",
	}
	stopReason := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return MessageResponse{}, err
		}
		if chunk.ID != "" {
			response.ID = chunk.ID
		}
		if chunk.Model != "" {
			response.Model = chunk.Model
		}
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			response.Usage = Usage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
			}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				text.WriteString(choice.Delta.Content)
				emitStreamEvent(emit, StreamEvent{
					Type: "text_delta",
					Text: choice.Delta.Content,
				})
			}
			for _, item := range choice.Delta.ToolCalls {
				call := toolCalls[item.Index]
				if call == nil {
					call = &toolCall{}
					toolCalls[item.Index] = call
				}
				if item.ID != "" {
					call.ID = item.ID
				}
				if item.Function.Name != "" {
					call.Name = item.Function.Name
				}
				if item.Function.Arguments != "" {
					call.Arguments.WriteString(item.Function.Arguments)
				}
				emitStreamEvent(emit, StreamEvent{
					Type:           "tool_call_delta",
					ToolCallID:     call.ID,
					ToolCallIndex:  item.Index,
					ToolName:       call.Name,
					ToolInputDelta: item.Function.Arguments,
				})
			}
			if choice.FinishReason != "" {
				stopReason = choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return MessageResponse{}, err
	}
	content := make([]OutputContentBlock, 0, len(toolCalls)+1)
	if text.Len() > 0 {
		content = append(content, OutputContentBlock{Type: "text", Text: text.String()})
	}
	indices := make([]int, 0, len(toolCalls))
	for index := range toolCalls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		call := toolCalls[index]
		input := json.RawMessage(compactJSONString(call.Arguments.String()))
		content = append(content, OutputContentBlock{
			Type:  "tool_use",
			ID:    call.ID,
			Name:  call.Name,
			Input: input,
		})
		emitStreamEvent(emit, StreamEvent{
			Type:          "tool_call_ready",
			ToolCallID:    call.ID,
			ToolCallIndex: index,
			ToolName:      call.Name,
			ToolInput:     input,
		})
	}
	if response.Model == "" {
		response.Model = requestModelFallbackFromContent(content)
	}
	response.Content = content
	response.StopReason = mapOpenAIFinishReason(stopReason)
	emitStreamEvent(emit, StreamEvent{
		Type:       "message_stop",
		StopReason: response.StopReason,
		Usage:      response.Usage,
	})
	return response, nil
}

func emitStreamEvent(emit func(StreamEvent), event StreamEvent) {
	if emit == nil {
		return
	}
	emit(event)
}

func rawJSONOrEmptyObject(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return map[string]any{}
}

func compactRawJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return compactJSONString(string(raw))
}

func compactJSONString(value string) string {
	if strings.TrimSpace(value) == "" {
		return "{}"
	}
	var out bytes.Buffer
	if err := json.Compact(&out, []byte(value)); err == nil {
		return out.String()
	}
	return value
}

func mapOpenAIFinishReason(reason string) string {
	switch reason {
	case "tool_calls":
		return "tool_use"
	case "stop":
		return "end_turn"
	default:
		return reason
	}
}

func requestModelFallbackFromContent(content []OutputContentBlock) string {
	if len(content) == 0 {
		return ""
	}
	types := make([]string, 0, len(content))
	for _, block := range content {
		types = append(types, block.Type)
	}
	slices.Sort(types)
	return strings.Join(types, "+")
}
