package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ascaris/internal/api"
	"ascaris/internal/testutil/mockanthropic"
	"ascaris/internal/tools"
)

func TestLiveHarnessRunPromptEmitsProgressForTextTurn(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(mockanthropic.NewTransport())
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	harness, err := NewLiveHarness(root)
	if err != nil {
		t.Fatalf("new live harness: %v", err)
	}
	var phases []PromptPhase
	_, err = harness.RunPrompt(context.Background(), mockanthropic.ScenarioPrefix+"streaming_text", PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		Progress: func(progress PromptProgress) {
			phases = append(phases, progress.Phase)
		},
	})
	if err != nil {
		t.Fatalf("run prompt: %v", err)
	}
	expected := []PromptPhase{
		PromptPhaseStarting,
		PromptPhaseWaitingModel,
		PromptPhaseFinalizing,
	}
	assertPromptPhases(t, phases, expected)
}

func TestLiveHarnessRunPromptEmitsProgressForToolTurn(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(mockanthropic.NewTransport())
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	if err := os.WriteFile(filepath.Join(root, "fixture.txt"), []byte("alpha parity line\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	harness, err := NewLiveHarness(root)
	if err != nil {
		t.Fatalf("new live harness: %v", err)
	}
	var phases []PromptPhase
	_, err = harness.RunPrompt(context.Background(), mockanthropic.ScenarioPrefix+"read_file_roundtrip", PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		Progress: func(progress PromptProgress) {
			phases = append(phases, progress.Phase)
		},
	})
	if err != nil {
		t.Fatalf("run prompt: %v", err)
	}
	expected := []PromptPhase{
		PromptPhaseStarting,
		PromptPhaseWaitingModel,
		PromptPhaseExecutingTools,
		PromptPhaseWaitingModel,
		PromptPhaseFinalizing,
	}
	assertPromptPhases(t, phases, expected)
}

func TestLiveHarnessRunPromptEmitsActivityForToolTurn(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(mockanthropic.NewTransport())
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	if err := os.WriteFile(filepath.Join(root, "fixture.txt"), []byte("alpha parity line\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	harness, err := NewLiveHarness(root)
	if err != nil {
		t.Fatalf("new live harness: %v", err)
	}
	var activities []ActivityEvent
	_, err = harness.RunPrompt(context.Background(), mockanthropic.ScenarioPrefix+"read_file_roundtrip", PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		Activity: func(activity ActivityEvent) {
			activities = append(activities, activity)
		},
	})
	if err != nil {
		t.Fatalf("run prompt: %v", err)
	}
	if len(activities) == 0 {
		t.Fatalf("expected activity events")
	}
	if !containsActivityKind(activities, "tool_start") || !containsActivityKind(activities, "tool_result") {
		t.Fatalf("expected tool activity events, got %#v", activities)
	}
	if !containsActivityKind(activities, "file_read") {
		t.Fatalf("expected file_read activity event, got %#v", activities)
	}
	if !containsActivityKind(activities, "tool_call_delta") || !containsActivityKind(activities, "tool_call_ready") {
		t.Fatalf("expected streamed tool call formation events, got %#v", activities)
	}
}

func TestLiveHarnessRunPromptEmitsStreamingResultActivity(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(mockanthropic.NewTransport())
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	harness, err := NewLiveHarness(root)
	if err != nil {
		t.Fatalf("new live harness: %v", err)
	}
	var activities []ActivityEvent
	_, err = harness.RunPrompt(context.Background(), mockanthropic.ScenarioPrefix+"streaming_text", PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		Activity: func(activity ActivityEvent) {
			activities = append(activities, activity)
		},
	})
	if err != nil {
		t.Fatalf("run prompt: %v", err)
	}
	if !containsActivityKind(activities, "result_stream") {
		t.Fatalf("expected result_stream activity, got %#v", activities)
	}
}

func TestLiveHarnessUsesDistinctStreamEntryIDsAcrossTurns(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(mockanthropic.NewTransport())
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	harness, err := NewLiveHarness(root)
	if err != nil {
		t.Fatalf("new live harness: %v", err)
	}

	var firstActivities []ActivityEvent
	firstSummary, err := harness.RunPrompt(context.Background(), mockanthropic.ScenarioPrefix+"streaming_text", PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		Activity: func(activity ActivityEvent) {
			firstActivities = append(firstActivities, activity)
		},
	})
	if err != nil {
		t.Fatalf("first run prompt: %v", err)
	}

	var secondActivities []ActivityEvent
	_, err = harness.RunPrompt(context.Background(), mockanthropic.ScenarioPrefix+"streaming_text", PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		ResumeSession:  firstSummary.SessionID,
		Activity: func(activity ActivityEvent) {
			secondActivities = append(secondActivities, activity)
		},
	})
	if err != nil {
		t.Fatalf("second run prompt: %v", err)
	}

	firstResultID := firstActivityEntryID(firstActivities, "result_stream")
	secondResultID := firstActivityEntryID(secondActivities, "result_stream")
	if firstResultID == "" || secondResultID == "" {
		t.Fatalf("expected result stream entry ids, got first=%q second=%q", firstResultID, secondResultID)
	}
	if firstResultID == secondResultID {
		t.Fatalf("expected distinct turn-scoped result ids, got %q", firstResultID)
	}
}

func TestDefaultSystemPromptIncludesHarnessGuardrails(t *testing.T) {
	prompt := defaultSystemPrompt()
	required := []string{
		"stop and ask instead of guessing",
		"Treat user explanations and root-cause guesses as hypotheses, not facts.",
		"follow the evidence",
		"Read files before editing",
		"Do not perform destructive or irreversible actions",
		"report what happened honestly",
		"silently retry the same failing approach",
		"Security & System Integrity",
		"Context Efficiency",
		"Engineering Standards",
		"Research → Strategy → Execution",
		"Tool Usage & Parallelism",
	}
	for _, item := range required {
		if !strings.Contains(prompt, item) {
			t.Fatalf("expected system prompt to contain %q, got %q", item, prompt)
		}
	}
}

func TestLiveHarnessSendsDefaultSystemPrompt(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var payload api.MessageRequest
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if payload.System != defaultSystemPrompt() {
			t.Fatalf("expected system prompt %q, got %q", defaultSystemPrompt(), payload.System)
		}
		return sseResponse(finalTextSSEForTest("system prompt captured")), nil
	}))
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	harness, err := NewLiveHarness(root)
	if err != nil {
		t.Fatalf("new live harness: %v", err)
	}
	summary, err := harness.RunPrompt(context.Background(), "verify system prompt wiring", PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
	})
	if err != nil {
		t.Fatalf("run prompt: %v", err)
	}
	if summary.Message != "system prompt captured" {
		t.Fatalf("unexpected summary message: %q", summary.Message)
	}
}

func assertPromptPhases(t *testing.T, got, want []PromptPhase) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("unexpected phase count: got=%v want=%v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("unexpected phase order: got=%v want=%v", got, want)
		}
	}
}

func containsActivityKind(items []ActivityEvent, kind string) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func firstActivityEntryID(items []ActivityEvent, kind string) string {
	for _, item := range items {
		if item.Kind == kind && strings.TrimSpace(item.EntryID) != "" {
			return item.EntryID
		}
	}
	return ""
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func sseResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func finalTextSSEForTest(text string) string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":11,"output_tokens":0}}}`,
		"",
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}`,
		"",
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		"",
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":11,"output_tokens":5}}`,
		"",
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
}
