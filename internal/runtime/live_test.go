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
	"ascaris/internal/sessions"
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

func TestLiveHarnessClampsMaxTokensForContextConstrainedQwenRequests(t *testing.T) {
	var seenMaxTokens int
	restoreTransport := api.SetTransportForTesting(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var payload struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		seenMaxTokens = payload.MaxTokens
		if payload.Model != "qwen3.6-30b-a3b" {
			t.Fatalf("unexpected model: %q", payload.Model)
		}
		return sseResponse(openAITextSSEForTest("ok")), nil
	}))
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", "https://mock.openai.local/v1")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	harness, err := NewLiveHarness(root)
	if err != nil {
		t.Fatalf("new live harness: %v", err)
	}
	_, err = harness.RunPrompt(context.Background(), strings.Repeat("alpha beta gamma delta\n", 3200), PromptOptions{
		Model:          "qwen3.6-30b-a3b",
		Provider:       api.ProviderOpenAI,
		PermissionMode: tools.PermissionWorkspaceWrite,
	})
	if err != nil {
		t.Fatalf("run prompt: %v", err)
	}
	if seenMaxTokens <= 0 {
		t.Fatalf("expected a max_tokens value, got %d", seenMaxTokens)
	}
	if seenMaxTokens >= 4096 {
		t.Fatalf("expected context-aware max_tokens clamp, got %d", seenMaxTokens)
	}
}

func TestContextAwareMaxTokensLeavesRequestedBudgetWhenItFits(t *testing.T) {
	request := api.MessageRequest{
		Model:     "qwen3.6-30b-a3b",
		MaxTokens: 4096,
		System:    "Keep it short.",
		Messages: []api.InputMessage{
			api.UserTextMessage("Reply with exactly ok."),
		},
	}
	if got := contextAwareMaxTokens(request, 4096, requestTokenObservation{}); got != 4096 {
		t.Fatalf("expected unclamped max tokens, got %d", got)
	}
}

func TestContextAwareMaxTokensUsesObservedInputTokensForLaterIterations(t *testing.T) {
	request := api.MessageRequest{
		Model:     "qwen3.6-30b-a3b",
		MaxTokens: 4096,
		System:    "Keep it short.",
		Messages: []api.InputMessage{
			api.UserTextMessage("Read the repo and explain the harness."),
			{
				Role: "assistant",
				Content: []api.InputContentBlock{
					{
						Type:  "tool_use",
						ID:    "toolu_1",
						Name:  "read_file",
						Input: json.RawMessage(`{"path":"README.md"}`),
					},
				},
			},
			api.ToolResultMessage([]api.ToolResultEnvelope{{
				ToolUseID: "toolu_1",
				Output:    strings.Repeat("alpha beta gamma delta\n", 600),
			}}),
		},
	}
	observed := requestTokenObservation{
		InputTokens:  14950,
		MessageCount: 1,
	}
	got := contextAwareMaxTokens(request, 4096, observed)
	if got >= 4096 {
		t.Fatalf("expected observed input tokens to clamp max tokens, got %d", got)
	}
	if got <= 0 {
		t.Fatalf("expected positive max tokens, got %d", got)
	}
}

func TestContextWindowCompactionKeepsValidToolHistory(t *testing.T) {
	session := sessions.NewManagedSession("session-1", "qwen3.6-30b-a3b")
	session.Messages = []api.InputMessage{
		api.UserTextMessage("implement a long plan"),
		{
			Role: "assistant",
			Content: []api.InputContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_old",
				Name:  "read_file",
				Input: json.RawMessage(`{"path":"old.txt"}`),
			}},
		},
		api.ToolResultMessage([]api.ToolResultEnvelope{{
			ToolUseID: "toolu_old",
			Output:    strings.Repeat("old output\n", 8000),
		}}),
		{
			Role: "assistant",
			Content: []api.InputContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_recent",
				Name:  "grep_search",
				Input: json.RawMessage(`{"pattern":"TODO","path":"."}`),
			}},
		},
		api.ToolResultMessage([]api.ToolResultEnvelope{{
			ToolUseID: "toolu_recent",
			Output:    "recent result",
		}}),
	}
	request := api.MessageRequest{
		Model:    "qwen3.6-30b-a3b",
		System:   "system prompt",
		Messages: append([]api.InputMessage(nil), session.Messages...),
	}

	compaction := applyContextWindowCompaction(&session, &request, "implement a long plan", 512)
	if compaction == nil {
		t.Fatal("expected context window compaction")
	}
	if !messageStartsWithText(session.Messages[0], contextCompactionNoticePrefix) {
		t.Fatalf("expected leading compaction notice, got %#v", session.Messages[0])
	}
	if !validToolHistory(session.Messages) {
		t.Fatalf("compacted messages contain dangling tool results: %#v", session.Messages)
	}
	if got := estimateRequestInputTokens(request); got > contextWindowInputBudget(request.Model, 512) {
		t.Fatalf("compacted request still exceeds budget: got=%d budget=%d", got, contextWindowInputBudget(request.Model, 512))
	}
}

func TestContextWindowCompactionCompactsLargeToolInputs(t *testing.T) {
	largeContent := strings.Repeat("x", 100000)
	session := sessions.NewManagedSession("session-1", "qwen3.6-30b-a3b")
	session.Messages = []api.InputMessage{
		api.UserTextMessage("write a large generated file"),
		{
			Role: "assistant",
			Content: []api.InputContentBlock{{
				Type: "tool_use",
				ID:   "toolu_write",
				Name: "write_file",
				Input: json.RawMessage(`{"path":"generated.txt","content":"` +
					largeContent + `"}`),
			}},
		},
		api.ToolResultMessage([]api.ToolResultEnvelope{{
			ToolUseID: "toolu_write",
			Output:    `{"path":"generated.txt","bytes_written":100000}`,
		}}),
	}
	request := api.MessageRequest{
		Model:    "qwen3.6-30b-a3b",
		System:   "system prompt",
		Messages: append([]api.InputMessage(nil), session.Messages...),
	}

	compaction := applyContextWindowCompaction(&session, &request, "write a large generated file", 512)
	if compaction == nil {
		t.Fatal("expected context window compaction")
	}
	var input struct {
		Content string `json:"content"`
	}
	found := false
	for _, message := range session.Messages {
		for _, block := range message.Content {
			if block.Type == "tool_use" && block.ID == "toolu_write" {
				found = true
				if err := json.Unmarshal(block.Input, &input); err != nil {
					t.Fatalf("compacted tool input is invalid JSON: %v", err)
				}
			}
		}
	}
	if !found {
		t.Fatal("expected retained write_file tool_use")
	}
	if len(input.Content) >= len(largeContent) || !strings.Contains(input.Content, "truncated") {
		t.Fatalf("expected compacted tool input content, got len=%d", len(input.Content))
	}
	if !validToolHistory(session.Messages) {
		t.Fatalf("compacted messages contain dangling tool results: %#v", session.Messages)
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

func openAITextSSEForTest(text string) string {
	return strings.Join([]string{
		`data: {"id":"chatcmpl_test","model":"qwen3.6-30b-a3b","choices":[{"delta":{"content":"` + text + `"}}]}`,
		"",
		`data: {"id":"chatcmpl_test","choices":[{"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl_test","choices":[],"usage":{"prompt_tokens":12000,"completion_tokens":2}}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")
}
