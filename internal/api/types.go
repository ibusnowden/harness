package api

import (
	"encoding/json"
	"strings"
)

type MessageRequest struct {
	Model         string            `json:"model"`
	MaxTokens     int               `json:"max_tokens"`
	Messages      []InputMessage    `json:"messages"`
	System        string            `json:"system,omitempty"`
	Tools         []ToolDefinition  `json:"tools,omitempty"`
	ToolChoice    *ToolChoice       `json:"tool_choice,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	StreamHandler func(StreamEvent) `json:"-"`
}

type StreamEvent struct {
	Type           string          `json:"type"`
	Text           string          `json:"text,omitempty"`
	ToolCallID     string          `json:"tool_call_id,omitempty"`
	ToolCallIndex  int             `json:"tool_call_index,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`
	ToolInputDelta string          `json:"tool_input_delta,omitempty"`
	Thinking       string          `json:"thinking,omitempty"`
	Signature      string          `json:"signature,omitempty"`
	Usage          Usage           `json:"usage,omitempty"`
	StopReason     string          `json:"stop_reason,omitempty"`
}

type ToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type InputMessage struct {
	Role    string              `json:"role"`
	Content []InputContentBlock `json:"content"`
}

func UserTextMessage(text string) InputMessage {
	return InputMessage{
		Role: "user",
		Content: []InputContentBlock{
			{Type: "text", Text: text},
		},
	}
}

func ToolResultMessage(results []ToolResultEnvelope) InputMessage {
	blocks := make([]InputContentBlock, 0, len(results))
	for _, result := range results {
		blocks = append(blocks, InputContentBlock{
			Type:      "tool_result",
			ToolUseID: result.ToolUseID,
			Content: []ToolResultContentBlock{
				{Type: "text", Text: result.Output},
			},
			IsError: result.IsError,
		})
	}
	return InputMessage{Role: "user", Content: blocks}
}

type InputContentBlock struct {
	Type      string                   `json:"type"`
	Text      string                   `json:"text,omitempty"`
	ID        string                   `json:"id,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Input     json.RawMessage          `json:"input,omitempty"`
	ToolUseID string                   `json:"tool_use_id,omitempty"`
	Content   []ToolResultContentBlock `json:"content,omitempty"`
	IsError   bool                     `json:"is_error,omitempty"`
}

type ToolResultContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type MessageResponse struct {
	ID           string               `json:"id"`
	Kind         string               `json:"type"`
	Role         string               `json:"role"`
	Content      []OutputContentBlock `json:"content"`
	Model        string               `json:"model"`
	StopReason   string               `json:"stop_reason,omitempty"`
	StopSequence string               `json:"stop_sequence,omitempty"`
	Usage        Usage                `json:"usage"`
	RequestID    string               `json:"request_id,omitempty"`
}

func (m MessageResponse) FinalText() string {
	var parts []string
	for _, block := range m.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "")
}

type OutputContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type Usage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:              u.InputTokens + other.InputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens + other.CacheCreationInputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens + other.CacheReadInputTokens,
		OutputTokens:             u.OutputTokens + other.OutputTokens,
	}
}

type ContentBlockDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
}

type ToolResultEnvelope struct {
	ToolUseID string
	Output    string
	IsError   bool
}

