package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type captureTransport struct {
	lastRequest *http.Request
	lastBody    string
	status      int
	contentType string
	body        string
}

func (t *captureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.lastRequest = request.Clone(request.Context())
	data, _ := io.ReadAll(request.Body)
	t.lastBody = string(data)
	return &http.Response{
		StatusCode: t.status,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{t.contentType}},
		Body:       io.NopCloser(strings.NewReader(t.body)),
	}, nil
}

func TestProviderClientRoutesGrokThroughXAI(t *testing.T) {
	transport := &captureTransport{
		status:      200,
		contentType: "text/event-stream",
		body: strings.Join([]string{
			`data: {"id":"chatcmpl_test","model":"grok-3","choices":[{"delta":{"content":"Hello from Grok"}}]}`,
			``,
			`data: {"id":"chatcmpl_test","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			``,
			`data: {"id":"chatcmpl_test","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":5}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"),
	}
	restore := SetTransportForTesting(transport)
	defer restore()
	t.Setenv("XAI_API_KEY", "xai-test-key")
	t.Setenv("XAI_BASE_URL", "https://example.xai.test/v1")

	client, err := NewProviderClient("grok-mini", ProviderConfig{})
	if err != nil {
		t.Fatalf("new provider client: %v", err)
	}
	response, err := client.StreamMessage(context.Background(), MessageRequest{
		Model:     "grok-3",
		MaxTokens: 256,
		System:    "Keep the answer short.",
		Messages:  []InputMessage{UserTextMessage("hello")},
		Tools: []ToolDefinition{
			{Name: "weather", Description: "Fetch weather", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	})
	if err != nil {
		t.Fatalf("stream message: %v", err)
	}
	if client.ProviderKind() != ProviderXAI {
		t.Fatalf("unexpected provider kind: %s", client.ProviderKind())
	}
	if response.FinalText() != "Hello from Grok" {
		t.Fatalf("unexpected response text: %q", response.FinalText())
	}
	if transport.lastRequest == nil || transport.lastRequest.URL.Path != "/v1/chat/completions" {
		t.Fatalf("unexpected request path: %#v", transport.lastRequest)
	}
	if got := transport.lastRequest.Header.Get("authorization"); got != "Bearer xai-test-key" {
		t.Fatalf("unexpected auth header: %q", got)
	}
	if !strings.Contains(transport.lastBody, `"role":"system"`) || !strings.Contains(transport.lastBody, `"tools":[`) {
		t.Fatalf("unexpected openai-compatible request body: %s", transport.lastBody)
	}
}

func TestProviderClientRoutesOpenRouterSlugsThroughOpenAICompat(t *testing.T) {
	transport := &captureTransport{
		status:      200,
		contentType: "text/event-stream",
		body: strings.Join([]string{
			`data: {"id":"chatcmpl_test","model":"openai/gpt-4.1-mini","choices":[{"delta":{"content":"Hello from OpenRouter"}}]}`,
			``,
			`data: {"id":"chatcmpl_test","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			``,
			`data: {"id":"chatcmpl_test","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"),
	}
	restore := SetTransportForTesting(transport)
	defer restore()
	t.Setenv("OPENROUTER_API_KEY", "openrouter-test-key")
	t.Setenv("OPENROUTER_BASE_URL", "https://openrouter.example/v1")

	client, err := NewProviderClient("openai/gpt-4.1-mini", ProviderConfig{})
	if err != nil {
		t.Fatalf("new provider client: %v", err)
	}
	response, err := client.StreamMessage(context.Background(), MessageRequest{
		Model:     "openai/gpt-4.1-mini",
		MaxTokens: 256,
		Messages:  []InputMessage{UserTextMessage("hello")},
	})
	if err != nil {
		t.Fatalf("stream message: %v", err)
	}
	if client.ProviderKind() != ProviderOpenRouter {
		t.Fatalf("unexpected provider kind: %s", client.ProviderKind())
	}
	if response.FinalText() != "Hello from OpenRouter" {
		t.Fatalf("unexpected response text: %q", response.FinalText())
	}
	if transport.lastRequest == nil || transport.lastRequest.URL.String() != "https://openrouter.example/v1/chat/completions" {
		t.Fatalf("unexpected request url: %#v", transport.lastRequest)
	}
	if got := transport.lastRequest.Header.Get("authorization"); got != "Bearer openrouter-test-key" {
		t.Fatalf("unexpected auth header: %q", got)
	}
}

func TestProviderClientPrefersOpenRouterForSlashGrokModels(t *testing.T) {
	transport := &captureTransport{
		status:      200,
		contentType: "text/event-stream",
		body: strings.Join([]string{
			`data: {"id":"chatcmpl_test","model":"x-ai/grok-3-mini","choices":[{"delta":{"content":"Hello from OpenRouter Grok"}}]}`,
			``,
			`data: {"id":"chatcmpl_test","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			``,
			`data: {"id":"chatcmpl_test","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"),
	}
	restore := SetTransportForTesting(transport)
	defer restore()
	t.Setenv("OPENROUTER_API_KEY", "openrouter-test-key")
	t.Setenv("OPENROUTER_BASE_URL", "https://openrouter.example/v1")
	t.Setenv("XAI_API_KEY", "xai-test-key")
	t.Setenv("XAI_BASE_URL", "https://xai.example/v1")

	client, err := NewProviderClient("x-ai/grok-3-mini", ProviderConfig{})
	if err != nil {
		t.Fatalf("new provider client: %v", err)
	}
	response, err := client.StreamMessage(context.Background(), MessageRequest{
		Model:     "x-ai/grok-3-mini",
		MaxTokens: 256,
		Messages:  []InputMessage{UserTextMessage("hello")},
	})
	if err != nil {
		t.Fatalf("stream message: %v", err)
	}
	if client.ProviderKind() != ProviderOpenRouter {
		t.Fatalf("unexpected provider kind: %s", client.ProviderKind())
	}
	if response.FinalText() != "Hello from OpenRouter Grok" {
		t.Fatalf("unexpected response text: %q", response.FinalText())
	}
	if transport.lastRequest == nil || transport.lastRequest.URL.String() != "https://openrouter.example/v1/chat/completions" {
		t.Fatalf("unexpected request url: %#v", transport.lastRequest)
	}
	if got := transport.lastRequest.Header.Get("authorization"); got != "Bearer openrouter-test-key" {
		t.Fatalf("unexpected auth header: %q", got)
	}
}

func TestProviderClientKeepsDirectOpenAIPathForPlainModels(t *testing.T) {
	transport := &captureTransport{
		status:      200,
		contentType: "text/event-stream",
		body: strings.Join([]string{
			`data: {"id":"chatcmpl_test","model":"gpt-4.1-mini","choices":[{"delta":{"content":"Hello from OpenAI"}}]}`,
			``,
			`data: {"id":"chatcmpl_test","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			``,
			`data: {"id":"chatcmpl_test","choices":[],"usage":{"prompt_tokens":8,"completion_tokens":3}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"),
	}
	restore := SetTransportForTesting(transport)
	defer restore()
	t.Setenv("OPENAI_API_KEY", "openai-test-key")
	t.Setenv("OPENAI_BASE_URL", "https://openai.example/v1")
	t.Setenv("OPENROUTER_API_KEY", "openrouter-test-key")
	t.Setenv("OPENROUTER_BASE_URL", "https://openrouter.example/v1")

	client, err := NewProviderClient("gpt-4.1-mini", ProviderConfig{})
	if err != nil {
		t.Fatalf("new provider client: %v", err)
	}
	response, err := client.StreamMessage(context.Background(), MessageRequest{
		Model:     "gpt-4.1-mini",
		MaxTokens: 256,
		Messages:  []InputMessage{UserTextMessage("hello")},
	})
	if err != nil {
		t.Fatalf("stream message: %v", err)
	}
	if client.ProviderKind() != ProviderOpenAI {
		t.Fatalf("unexpected provider kind: %s", client.ProviderKind())
	}
	if response.FinalText() != "Hello from OpenAI" {
		t.Fatalf("unexpected response text: %q", response.FinalText())
	}
	if transport.lastRequest == nil || transport.lastRequest.URL.String() != "https://openai.example/v1/chat/completions" {
		t.Fatalf("unexpected request url: %#v", transport.lastRequest)
	}
	if got := transport.lastRequest.Header.Get("authorization"); got != "Bearer openai-test-key" {
		t.Fatalf("unexpected auth header: %q", got)
	}
}

func TestProviderClientAllowsExplicitOpenAIOverrideForLocalCompatModels(t *testing.T) {
	transport := &captureTransport{
		status:      200,
		contentType: "text/event-stream",
		body: strings.Join([]string{
			`data: {"id":"chatcmpl_test","model":"GLM-4.7-Flash","choices":[{"delta":{"content":"Hello from local compat"}}]}`,
			``,
			`data: {"id":"chatcmpl_test","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			``,
			`data: {"id":"chatcmpl_test","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":4}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"),
	}
	restore := SetTransportForTesting(transport)
	defer restore()
	t.Setenv("OPENAI_API_KEY", "openai-test-key")
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:8000/v1")

	client, err := NewProviderClient("GLM-4.7-Flash", ProviderConfig{PreferredProvider: ProviderOpenAI})
	if err != nil {
		t.Fatalf("new provider client: %v", err)
	}
	response, err := client.StreamMessage(context.Background(), MessageRequest{
		Model:     "GLM-4.7-Flash",
		MaxTokens: 256,
		Messages:  []InputMessage{UserTextMessage("hello")},
	})
	if err != nil {
		t.Fatalf("stream message: %v", err)
	}
	if client.ProviderKind() != ProviderOpenAI {
		t.Fatalf("unexpected provider kind: %s", client.ProviderKind())
	}
	if response.FinalText() != "Hello from local compat" {
		t.Fatalf("unexpected response text: %q", response.FinalText())
	}
	if transport.lastRequest == nil || transport.lastRequest.URL.String() != "http://127.0.0.1:8000/v1/chat/completions" {
		t.Fatalf("unexpected request url: %#v", transport.lastRequest)
	}
	if !strings.Contains(transport.lastBody, `"model":"GLM-4.7-Flash"`) {
		t.Fatalf("expected raw model name in request body: %s", transport.lastBody)
	}
}

func TestProviderClientReportsMissingXAIKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	if _, err := NewProviderClient("grok-3", ProviderConfig{}); err == nil || !strings.Contains(err.Error(), "XAI_API_KEY") {
		t.Fatalf("expected missing XAI_API_KEY error, got %v", err)
	}
}
