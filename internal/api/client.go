package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultBaseURL = "https://api.anthropic.com"

var (
	transportOverrideMu sync.Mutex
	transportOverride   http.RoundTripper
)

type Client struct {
	BaseURL     string
	APIKey      string
	BearerToken string
	HTTPClient  *http.Client
}

func NewClientFromEnv() (*Client, error) {
	return NewAnthropicClientFromEnv(ProviderConfig{})
}

func SetTransportForTesting(transport http.RoundTripper) func() {
	transportOverrideMu.Lock()
	previous := transportOverride
	transportOverride = transport
	transportOverrideMu.Unlock()
	return func() {
		transportOverrideMu.Lock()
		transportOverride = previous
		transportOverrideMu.Unlock()
	}
}

func newHTTPClient(proxyURL string) *http.Client {
	transportOverrideMu.Lock()
	defer transportOverrideMu.Unlock()
	client := &http.Client{Timeout: 60 * time.Second}
	if transportOverride != nil {
		client.Transport = transportOverride
		return client
	}
	if transport := transportFromProxyURL(proxyURL); transport != nil {
		client.Transport = transport
	}
	return client
}

func (c *Client) StreamMessage(ctx context.Context, request MessageRequest) (MessageResponse, error) {
	return c.StreamMessageEvents(ctx, request, nil)
}

func (c *Client) StreamMessageEvents(ctx context.Context, request MessageRequest, emit func(StreamEvent)) (MessageResponse, error) {
	request.Stream = true
	body, err := json.Marshal(request)
	if err != nil {
		return MessageResponse{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return MessageResponse{}, err
	}
	httpRequest.Header.Set("content-type", "application/json")
	if strings.TrimSpace(c.APIKey) != "" {
		httpRequest.Header.Set("x-api-key", c.APIKey)
	}
	if strings.TrimSpace(c.BearerToken) != "" {
		httpRequest.Header.Set("authorization", "Bearer "+c.BearerToken)
	}
	httpRequest.Header.Set("anthropic-version", "2023-06-01")
	response, err := c.HTTPClient.Do(httpRequest)
	if err != nil {
		return MessageResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		payload, _ := io.ReadAll(response.Body)
		return MessageResponse{}, fmt.Errorf("anthropic request failed: %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	if strings.Contains(response.Header.Get("content-type"), "text/event-stream") {
		return parseSSE(response.Body, emit)
	}
	var message MessageResponse
	if err := json.NewDecoder(response.Body).Decode(&message); err != nil {
		return MessageResponse{}, err
	}
	return message, nil
}

func (c *Client) ProviderKind() ProviderKind {
	return ProviderAnthropic
}

type streamAssembler struct {
	response MessageResponse
	blocks   map[int]*streamBlock
	handler  func(StreamEvent)
}

type streamBlock struct {
	Index     int
	Type      string
	Text      strings.Builder
	Signature strings.Builder
	ID        string
	Name      string
	Input     strings.Builder
	Data      json.RawMessage
	Closed    bool
}

func parseSSE(body io.Reader, emit func(StreamEvent)) (MessageResponse, error) {
	assembler := &streamAssembler{blocks: map[int]*streamBlock{}}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	var eventName string
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		if err := assembler.handle(eventName, data.String(), emit); err != nil {
			return err
		}
		eventName = ""
		data.Reset()
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return MessageResponse{}, err
			}
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return MessageResponse{}, err
	}
	if err := flush(); err != nil {
		return MessageResponse{}, err
	}
	assembler.finalize()
	emitStreamEvent(emit, StreamEvent{
		Type:       "message_stop",
		StopReason: assembler.response.StopReason,
		Usage:      assembler.response.Usage,
	})
	return assembler.response, nil
}

func (a *streamAssembler) handle(eventName, payload string, emit func(StreamEvent)) error {
	switch eventName {
	case "message_start":
		var event struct {
			Message MessageResponse `json:"message"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return err
		}
		a.response = event.Message
	case "content_block_start":
		var event struct {
			Index        int                `json:"index"`
			ContentBlock OutputContentBlock `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return err
		}
		block := &streamBlock{
			Index: event.Index,
			Type:  event.ContentBlock.Type,
			ID:    event.ContentBlock.ID,
			Name:  event.ContentBlock.Name,
			Data:  event.ContentBlock.Data,
		}
		if len(event.ContentBlock.Input) > 0 && string(event.ContentBlock.Input) != "{}" {
			block.Input.Write(event.ContentBlock.Input)
		}
		if event.ContentBlock.Text != "" {
			block.Text.WriteString(event.ContentBlock.Text)
		}
		a.blocks[event.Index] = block
		switch block.Type {
		case "text":
			if event.ContentBlock.Text != "" {
				emitStreamEvent(emit, StreamEvent{Type: "text_delta", Text: event.ContentBlock.Text})
			}
		case "thinking":
			if event.ContentBlock.Text != "" {
				emitStreamEvent(emit, StreamEvent{Type: "thinking_delta", Thinking: event.ContentBlock.Text})
			}
		case "tool_use":
			emitStreamEvent(emit, StreamEvent{
				Type:          "tool_call_delta",
				ToolCallID:    block.ID,
				ToolCallIndex: event.Index,
				ToolName:      block.Name,
			})
			if block.Input.Len() > 0 {
				emitStreamEvent(emit, StreamEvent{
					Type:           "tool_call_delta",
					ToolCallID:     block.ID,
					ToolCallIndex:  event.Index,
					ToolName:       block.Name,
					ToolInputDelta: block.Input.String(),
				})
			}
		}
	case "content_block_delta":
		var event struct {
			Index int               `json:"index"`
			Delta ContentBlockDelta `json:"delta"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return err
		}
		block, ok := a.blocks[event.Index]
		if !ok {
			return fmt.Errorf("received delta for unknown block index %d", event.Index)
		}
		switch event.Delta.Type {
		case "text_delta":
			block.Text.WriteString(event.Delta.Text)
			emitStreamEvent(emit, StreamEvent{Type: "text_delta", Text: event.Delta.Text})
		case "input_json_delta":
			block.Input.WriteString(event.Delta.PartialJSON)
			emitStreamEvent(emit, StreamEvent{
				Type:           "tool_call_delta",
				ToolCallID:     block.ID,
				ToolCallIndex:  event.Index,
				ToolName:       block.Name,
				ToolInputDelta: event.Delta.PartialJSON,
			})
		case "thinking_delta":
			block.Text.WriteString(event.Delta.Thinking)
			emitStreamEvent(emit, StreamEvent{Type: "thinking_delta", Thinking: event.Delta.Thinking})
		case "signature_delta":
			block.Signature.WriteString(event.Delta.Signature)
			emitStreamEvent(emit, StreamEvent{Type: "thinking_delta", Signature: event.Delta.Signature})
		}
	case "content_block_stop":
		var event struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return err
		}
		if block, ok := a.blocks[event.Index]; ok {
			block.Closed = true
			if block.Type == "tool_use" {
				input := json.RawMessage(`{}`)
				if block.Input.Len() > 0 {
					input = json.RawMessage(block.Input.String())
				}
				emitStreamEvent(emit, StreamEvent{
					Type:          "tool_call_ready",
					ToolCallID:    block.ID,
					ToolCallIndex: event.Index,
					ToolName:      block.Name,
					ToolInput:     input,
				})
			}
		}
	case "message_delta":
		var event struct {
			Delta struct {
				StopReason   string `json:"stop_reason"`
				StopSequence string `json:"stop_sequence"`
			} `json:"delta"`
			Usage Usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return err
		}
		a.response.StopReason = event.Delta.StopReason
		a.response.StopSequence = event.Delta.StopSequence
		a.response.Usage = event.Usage
		emitStreamEvent(emit, StreamEvent{
			Type:       "usage",
			StopReason: event.Delta.StopReason,
			Usage:      event.Usage,
		})
	case "message_stop":
		return nil
	}
	return nil
}

func (a *streamAssembler) emit(event StreamEvent) {
	if a.handler == nil {
		return
	}
	a.handler(event)
}

func (a *streamAssembler) finalize() {
	indices := make([]int, 0, len(a.blocks))
	for index := range a.blocks {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	content := make([]OutputContentBlock, 0, len(indices))
	for _, index := range indices {
		block := a.blocks[index]
		item := OutputContentBlock{
			Type: block.Type,
			ID:   block.ID,
			Name: block.Name,
			Data: block.Data,
		}
		switch block.Type {
		case "text":
			item.Text = block.Text.String()
		case "tool_use":
			if block.Input.Len() > 0 {
				item.Input = json.RawMessage(block.Input.String())
			} else {
				item.Input = json.RawMessage(`{}`)
			}
		case "thinking":
			item.Thinking = block.Text.String()
			item.Signature = block.Signature.String()
		}
		content = append(content, item)
	}
	a.response.Content = content
}
