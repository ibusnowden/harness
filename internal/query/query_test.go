package query

import (
	"encoding/json"
	"testing"
)

func TestFormatOutputStructuredDoesNotPanicWhenRetryLimitIsZero(t *testing.T) {
	engine := &Engine{
		SessionID: "session-test",
		Config: Config{
			StructuredOutput:     true,
			StructuredRetryLimit: 0,
		},
	}

	output := engine.formatOutput([]string{"Prompt: test"})
	if !json.Valid([]byte(output)) {
		t.Fatalf("expected valid json output, got %q", output)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got := payload["session_id"]; got != "session-test" {
		t.Fatalf("unexpected session_id: %#v", got)
	}
}
