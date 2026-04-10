package promptcache

import (
	"testing"

	"ascaris/internal/api"
)

func TestCacheStoresAndRetrievesCompletion(t *testing.T) {
	root := t.TempDir()
	cache := New(root, "session:/with spaces")
	request := api.MessageRequest{
		Model:     "claude-sonnet-4-6",
		MaxTokens: 1024,
		Messages: []api.InputMessage{
			api.UserTextMessage("hello"),
		},
		System: "be concise",
		Stream: true,
	}
	response := api.MessageResponse{
		ID:    "msg_1",
		Kind:  "message",
		Role:  "assistant",
		Model: "claude-sonnet-4-6",
		Content: []api.OutputContentBlock{
			{Type: "text", Text: "hello back"},
		},
		StopReason: "end_turn",
		Usage: api.Usage{
			InputTokens:  10,
			OutputTokens: 4,
		},
	}

	if _, err := cache.Store(request, response); err != nil {
		t.Fatalf("store cache entry: %v", err)
	}
	cached, event, ok := cache.Lookup(request)
	if !ok {
		t.Fatalf("expected cache hit")
	}
	if cached.FinalText() != "hello back" {
		t.Fatalf("unexpected cached response: %q", cached.FinalText())
	}
	if event.Type != "prompt_cache_hit" || event.Source != "completion-cache" {
		t.Fatalf("unexpected cache event: %+v", event)
	}
}
