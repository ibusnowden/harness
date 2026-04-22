package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
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

type OpenAICompatHTTPError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *OpenAICompatHTTPError) Error() string {
	return fmt.Sprintf("openai-compatible request failed: %s: %s", e.Status, e.Body)
}

func (c *OpenAICompatClient) ProviderKind() ProviderKind {
	return c.kind
}

func (c *OpenAICompatClient) StreamMessage(ctx context.Context, request MessageRequest) (MessageResponse, error) {
	return c.StreamMessageEvents(ctx, request, nil)
}

func (c *OpenAICompatClient) StreamMessageEvents(ctx context.Context, request MessageRequest, emit func(StreamEvent)) (MessageResponse, error) {
	openAIRequest, err := toOpenAIRequest(request)
	if err != nil {
		return MessageResponse{}, err
	}
	payload, err := json.Marshal(openAIRequest)
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
		return MessageResponse{}, &OpenAICompatHTTPError{
			StatusCode: response.StatusCode,
			Status:     response.Status,
			Body:       msg,
		}
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

func toOpenAIRequest(request MessageRequest) (map[string]any, error) {
	messages := make([]map[string]any, 0, len(request.Messages)+1)
	if strings.TrimSpace(request.System) != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": request.System,
		})
	}
	for _, message := range request.Messages {
		converted, err := convertOpenAIMessages(message)
		if err != nil {
			return nil, err
		}
		messages = append(messages, converted...)
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
		payload["tool_choice"] = "auto"
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
	return payload, nil
}

func convertOpenAIMessages(message InputMessage) ([]map[string]any, error) {
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
			arguments, err := normalizeToolCallArgumentsRaw(block.Input, block.Name, block.ID)
			if err != nil {
				return nil, fmt.Errorf("cannot send malformed tool call in conversation history: %w", err)
			}
			toolCalls = append(toolCalls, map[string]any{
				"id":   block.ID,
				"type": "function",
				"function": map[string]any{
					"name":      block.Name,
					"arguments": string(arguments),
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
		return toolMessages, nil
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
	return []map[string]any{out}, nil
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
	reasoning := strings.Builder{}
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
					Content          string `json:"content"`
					Reasoning        string `json:"reasoning"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
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
			reasoningDelta := choice.Delta.ReasoningContent
			if reasoningDelta == "" {
				reasoningDelta = choice.Delta.Reasoning
			}
			if reasoningDelta != "" {
				reasoning.WriteString(reasoningDelta)
				emitStreamEvent(emit, StreamEvent{Type: "thinking_delta", Text: reasoningDelta})
			}
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
	extractedTextToolCalls := []parsedTextToolCall(nil)
	content := make([]OutputContentBlock, 0, len(toolCalls)+2)
	if reasoning.Len() > 0 {
		content = append(content, OutputContentBlock{Type: "thinking", Thinking: reasoning.String()})
	}
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
		input, err := normalizeToolCallArgumentsString(call.Arguments.String(), call.Name, call.ID)
		if err != nil {
			return MessageResponse{}, err
		}
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
	// Fallback: when the model embeds tool calls as <tool_call>…</tool_call> text blocks
	// (happens when vLLM's tool-call parser is misconfigured), extract them from the
	// accumulated text and promote them to proper tool_use blocks.
	if len(toolCalls) == 0 && text.Len() > 0 {
		extracted, remainingText := extractTextToolCalls(text.String())
		if len(extracted) > 0 {
			extractedTextToolCalls = extracted
			// Rebuild content: keep reasoning, replace text with stripped version, add tool blocks.
			rebuilt := make([]OutputContentBlock, 0, len(content)+len(extracted))
			for _, b := range content {
				if b.Type == "text" {
					if strings.TrimSpace(remainingText) != "" {
						rebuilt = append(rebuilt, OutputContentBlock{Type: "text", Text: remainingText})
					}
				} else {
					rebuilt = append(rebuilt, b)
				}
			}
			for i, tc := range extracted {
				input, err := normalizeToolCallArgumentsString(tc.arguments, tc.name, fmt.Sprintf("fallback-%d", i))
				if err != nil {
					return MessageResponse{}, err
				}
				rebuilt = append(rebuilt, OutputContentBlock{
					Type:  "tool_use",
					ID:    fmt.Sprintf("fallback-%d", i),
					Name:  tc.name,
					Input: input,
				})
			}
			content = rebuilt
			if stopReason == "" {
				stopReason = "tool_calls"
			}
		}
	}
	if stopReason == "tool_calls" {
		if len(toolCalls) == 0 && len(extractedTextToolCalls) == 0 {
			return MessageResponse{}, fmt.Errorf("openai-compatible stream ended with tool_calls finish reason but emitted no tool calls")
		}
		if len(extractedTextToolCalls) > 0 {
			for index, call := range extractedTextToolCalls {
				input, err := normalizeToolCallArgumentsString(call.arguments, call.name, fmt.Sprintf("fallback-%d", index))
				if err != nil {
					return MessageResponse{}, err
				}
				emitStreamEvent(emit, StreamEvent{
					Type:          "tool_call_ready",
					ToolCallIndex: index,
					ToolCallID:    fmt.Sprintf("fallback-%d", index),
					ToolName:      call.name,
					ToolInput:     input,
				})
			}
		}
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

var toolCallBlockRE = regexp.MustCompile(`(?s)<tool_call>\s*(\{.*?\})\s*</tool_call>`)

type parsedTextToolCall struct {
	name      string
	arguments string
}

func extractTextToolCalls(text string) ([]parsedTextToolCall, string) {
	matches := toolCallBlockRE.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil, text
	}
	var calls []parsedTextToolCall
	for _, m := range matches {
		raw := text[m[2]:m[3]]
		var obj struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj.Name == "" {
			continue
		}
		calls = append(calls, parsedTextToolCall{
			name:      obj.Name,
			arguments: compactJSONString(string(obj.Arguments)),
		})
	}
	if len(calls) == 0 {
		return nil, text
	}
	remaining := toolCallBlockRE.ReplaceAllString(text, "")
	return calls, strings.TrimSpace(remaining)
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
