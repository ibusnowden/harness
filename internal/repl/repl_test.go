package repl

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func newSizedModel() model {
	m := newModel(context.Background(), func() {}, Config{
		Status: Status{
			Product:    "ascaris",
			Model:      "sonnet",
			Provider:   "auto",
			Permission: "workspace-write",
			Workspace:  "/Users/test/ascaris",
			Recent: RecentSession{
				ID:           "seed-session",
				MessageCount: 8,
				LastPrompt:   "summarize the repository",
				UpdatedLabel: "Updated Apr 14 14:00",
			},
		},
		RunPrompt:   func(context.Context, string, func(tea.Msg)) {},
		HandleSlash: func(context.Context, string) SlashResult { return SlashResult{} },
	})
	m.width = 130
	m.height = 40
	m.layout()
	return m
}

func TestModelWelcomeStateShowsBranding(t *testing.T) {
	m := newSizedModel()
	m.refreshTranscript()

	view := m.View()
	if !strings.Contains(view, "Welcome back") {
		t.Fatalf("expected startup hero in view, got %q", view)
	}
	if !strings.Contains(view, "Recent Session") || !strings.Contains(view, "Quick Start") {
		t.Fatalf("expected startup cards in view, got %q", view)
	}
}

func TestHeaderActivityGlyphChangesWhileBusy(t *testing.T) {
	m := newSizedModel()
	m.busy = true
	m.spinFrame = 0
	first := m.renderHeader()
	m.spinFrame = 1
	second := m.renderHeader()
	if first == second {
		t.Fatalf("expected animated activity glyph to change header output")
	}
	if !strings.Contains(first, "◜") && !strings.Contains(first, "◠") && !strings.Contains(first, "◝") &&
		!strings.Contains(first, "◞") && !strings.Contains(first, "◡") && !strings.Contains(first, "◟") {
		t.Fatalf("expected circular worm frame in header, got %q", first)
	}
}

func TestModelRecordsActivityAndShowsInspector(t *testing.T) {
	m := newSizedModel()
	m.showActivity = true
	m.recordActivity(activityRecord{
		title:   "bash",
		summary: "Invoking bash.",
		detail:  `{"command":"printf ok"}`,
		kind:    "tool_start",
	})
	m.recordActivity(activityRecord{
		title:   "bash",
		summary: "bash completed successfully.",
		detail:  `{"stdout":"ok"}`,
		kind:    "tool_result",
	})
	m.expanded[1] = true
	m.refreshActivity()

	view := m.View()
	if !strings.Contains(view, "Invoking bash.") {
		t.Fatalf("expected activity summary in view, got %q", view)
	}
	if !strings.Contains(view, `"stdout":"ok"`) {
		t.Fatalf("expected expanded activity detail in view, got %q", view)
	}
}

func TestFileWriteDiffActivityRendersInlineDiff(t *testing.T) {
	m := newSizedModel()
	m.busy = true
	payload, err := json.Marshal(fileDiff{
		HunkHeader: "@@ -1,1 +1,1 @@",
		Removed:    []string{"old"},
		Added:      []string{"new"},
		StartLine:  1,
	})
	if err != nil {
		t.Fatalf("marshal diff: %v", err)
	}
	m.handleActivityEvent(ActivityEvent{
		Kind:    "file_write",
		Title:   "sample.go",
		Summary: "Writing 4 bytes.",
		Detail:  string(payload),
	})

	view := stripANSI(m.transcript.View())
	for _, want := range []string{"sample.go", "@@ -1,1 +1,1 @@", "- old", "+ new"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected transcript to contain %q, got %q", want, view)
		}
	}
}

func TestFileWriteInvalidDiffFallsBackToCompactSummary(t *testing.T) {
	m := newSizedModel()
	m.busy = true
	m.handleActivityEvent(ActivityEvent{
		Kind:    "file_write",
		Title:   "sample.go",
		Summary: "Writing 4 bytes.",
		Detail:  "/tmp/sample.go",
	})

	view := stripANSI(m.transcript.View())
	if !strings.Contains(view, "Writing 4 bytes.") {
		t.Fatalf("expected compact file_write summary, got %q", view)
	}
	if strings.Contains(view, "@@") || strings.Contains(view, "- old") || strings.Contains(view, "+ new") {
		t.Fatalf("did not expect diff content for invalid payload, got %q", view)
	}
}

func TestStartupViewStacksOnNarrowTerminal(t *testing.T) {
	m := newSizedModel()
	m.width = 90
	m.height = 34
	m.layout()
	view := m.View()
	if !strings.Contains(view, "Recent Session") || !strings.Contains(view, "Quick Start") {
		t.Fatalf("expected stacked startup cards in narrow view, got %q", view)
	}
}

func TestModelToggleActivityPaneAndFocusCycle(t *testing.T) {
	m := newSizedModel()
	if !m.showActivity {
		t.Fatalf("expected activity pane visible by default")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyF2})
	m = next.(model)
	if m.showActivity {
		t.Fatalf("expected activity pane to toggle off")
	}
	current := m.focus
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(model)
	if m.focus == current {
		t.Fatalf("expected focus to advance")
	}
}

func TestModelSmallTerminalFallback(t *testing.T) {
	m := newSizedModel()
	m.width = 40
	m.height = 10
	view := m.View()
	if !strings.Contains(view, "Terminal too small") {
		t.Fatalf("expected small-terminal fallback, got %q", view)
	}
}

func TestModelApprovalFlow(t *testing.T) {
	m := newSizedModel()
	reply := make(chan bool, 1)
	next, _ := m.Update(ApprovalRequest{ToolName: "bash", Input: `{"command":"printf ok"}`, Response: reply})
	m = next.(model)
	if m.approval == nil {
		t.Fatalf("expected approval modal")
	}
	view := m.View()
	if !strings.Contains(view, "Approval Required") {
		t.Fatalf("expected approval overlay in view")
	}
	if !strings.Contains(view, "Approve once") || !strings.Contains(view, "Risk High") {
		t.Fatalf("expected approval actions and risk label in view, got %q", view)
	}
	done := make(chan struct{})
	go func() {
		<-reply
		close(done)
	}()
	next, wait := m.Update(tea.KeyMsg{Runes: []rune("y"), Type: tea.KeyRunes})
	m = next.(model)
	if wait == nil {
		t.Fatalf("expected wait command after approval")
	}
	<-done
}

func TestApprovalPreviewTruncatesRawJSONWhenCommandExists(t *testing.T) {
	m := newSizedModel()
	reply := make(chan bool, 1)
	longJSON := `{"command":"find . -name '*.go' | xargs wc -l","cwd":"/tmp/demo","env":{"A":"alpha","B":"beta","C":"gamma"},"notes":"` + strings.Repeat("x", 260) + `"}`
	next, _ := m.Update(ApprovalRequest{ToolName: "bash", Input: longJSON, Response: reply})
	m = next.(model)

	view := m.View()
	if !strings.Contains(view, "find . -name '*.go' | xargs wc -l") {
		t.Fatalf("expected clean command preview, got %q", view)
	}
	if !strings.Contains(view, "Raw input") {
		t.Fatalf("expected raw input section, got %q", view)
	}
	if !strings.Contains(view, "…") {
		t.Fatalf("expected truncated raw input preview, got %q", view)
	}
	if strings.Count(view, strings.Repeat("x", 40)) > 1 {
		t.Fatalf("expected raw input noise to be truncated, got %q", view)
	}
}

func TestApprovalRequestDoesNotDuplicateActivity(t *testing.T) {
	m := newSizedModel()
	reply := make(chan bool, 1)
	next, _ := m.Update(ApprovalRequest{ToolName: "bash", Input: `{"command":"printf ok"}`, Response: reply})
	m = next.(model)
	if m.approval == nil {
		t.Fatalf("expected approval modal")
	}
	if len(m.activities) != 0 {
		t.Fatalf("expected approval request to remain UI-only, got %d activity records", len(m.activities))
	}
}

func TestModelSlashResetClearsTranscriptAndActivity(t *testing.T) {
	m := newModel(context.Background(), func() {}, Config{
		Status:    Status{Product: "ascaris", SessionID: "seed"},
		RunPrompt: func(context.Context, string, func(tea.Msg)) {},
		HandleSlash: func(context.Context, string) SlashResult {
			return SlashResult{
				Output:     "Session cleared.",
				ResetState: true,
				SessionID:  "new",
			}
		},
	})
	m.width = 120
	m.height = 40
	m.layout()
	m.transcriptEntries = append(m.transcriptEntries, transcriptEntry{label: "Task", body: "old", kind: "task"})
	m.activities = append(m.activities, activityRecord{title: "bash", summary: "old activity"})
	m.refreshTranscript()
	m.refreshActivity()
	m.input.SetValue("/clear")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.status.SessionID != "new" {
		t.Fatalf("expected session reset, got %q", m.status.SessionID)
	}
	if len(m.activities) != 1 {
		t.Fatalf("expected reset notice activity, got %d records", len(m.activities))
	}
	view := m.View()
	if !strings.Contains(view, "Session cleared.") || !strings.Contains(view, "Welcome back") {
		t.Fatalf("expected reset notice and startup composition in view")
	}
}

func TestRenderLogoIsNonEmpty(t *testing.T) {
	m := newSizedModel()
	logo := renderLogo(80, m.theme)
	if strings.TrimSpace(logo) == "" {
		t.Fatal("expected non-empty logo render")
	}
	lines := strings.Split(logo, "\n")
	if len(lines) < 6 {
		t.Fatalf("expected at least 6 logo rows, got %d", len(lines))
	}
}

func TestSlashResultModelUpdatePropagates(t *testing.T) {
	m := newModel(context.Background(), func() {}, Config{
		Status:    Status{Product: "ascaris", Model: "old-model"},
		RunPrompt: func(context.Context, string, func(tea.Msg)) {},
		HandleSlash: func(_ context.Context, _ string) SlashResult {
			return SlashResult{Output: "Model set to new-model.", UpdateModel: "new-model"}
		},
	})
	m.width = 120
	m.height = 40
	m.layout()
	m.input.SetValue("/model new-model")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(model)
	if updated.status.Model != "new-model" {
		t.Fatalf("expected model updated to new-model, got %q", updated.status.Model)
	}
}

func TestAgentTimelineUsesTaskAndResultLabels(t *testing.T) {
	m := newSizedModel()
	m.width = 120
	m.height = 40
	m.layout()
	m.busy = true
	m.handleActivityEvent(ActivityEvent{Kind: "model", Title: "Thinking", Summary: "Waiting for model output."})
	m.appendTranscript("Run Request", "inspect the workspace", "task")
	m.appendTranscript("Result", "done", "result")

	view := m.View()
	if strings.Contains(view, "You") || strings.Contains(view, "Assistant") {
		t.Fatalf("expected agent-first timeline labels, got %q", view)
	}
	if !strings.Contains(view, "Run Request") || !strings.Contains(view, "Result") || !strings.Contains(view, "Thinking") {
		t.Fatalf("expected task/result/thinking timeline, got %q", view)
	}
	if strings.Contains(view, "◌ Thinking") {
		t.Fatalf("expected thinking phase to avoid a second spinner glyph, got %q", view)
	}
}

func TestActivityPaneDoesNotMirrorPromptAndThinkingTimeline(t *testing.T) {
	m := newSizedModel()
	m.width = 130
	m.height = 40
	m.layout()
	m.showActivity = true
	m.appendTranscript("Run Request", "what is ascaris?", "task")
	m.handleActivityEvent(ActivityEvent{Kind: "prompt", Title: "Run Request", Summary: "what is ascaris?"})
	m.handleActivityEvent(ActivityEvent{Kind: "model", Title: "Thinking", Summary: "Waiting for model output on iteration 1."})

	m.refreshActivity()
	activityView := m.activity.View()
	if strings.Contains(activityView, "Run Request") || strings.Contains(activityView, "Thinking") {
		t.Fatalf("expected activity pane to exclude prompt/model timeline echoes, got %q", activityView)
	}
}

func TestResultStreamUpdatesInPlace(t *testing.T) {
	m := newSizedModel()
	m.width = 120
	m.height = 40
	m.layout()
	m.busy = true
	m.handleActivityEvent(ActivityEvent{Kind: "result_stream", Title: "Result", Detail: "alpha", EntryID: "result-live"})
	m.handleActivityEvent(ActivityEvent{Kind: "result_stream", Title: "Result", Detail: "alpha beta", EntryID: "result-live"})

	count := 0
	for _, item := range m.transcriptEntries {
		if item.id == "result-live" {
			count++
			if item.body != "alpha beta" {
				t.Fatalf("expected updated live result body, got %q", item.body)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected one live result entry, got %d", count)
	}
}

func TestConsecutiveTurnStreamEntriesRemainDistinct(t *testing.T) {
	m := newSizedModel()
	m.width = 120
	m.height = 40
	m.layout()
	m.busy = true
	m.handleActivityEvent(ActivityEvent{Kind: "result_stream", Title: "Result", Detail: "first", EntryID: "turn-1-iter-1-result"})
	m.handleActivityEvent(ActivityEvent{Kind: "result_stream", Title: "Result", Detail: "second", EntryID: "turn-2-iter-1-result"})

	var firstSeen, secondSeen bool
	for _, item := range m.transcriptEntries {
		switch item.id {
		case "turn-1-iter-1-result":
			firstSeen = item.body == "first"
		case "turn-2-iter-1-result":
			secondSeen = item.body == "second"
		}
	}
	if !firstSeen || !secondSeen {
		t.Fatalf("expected distinct streamed result entries, got %#v", m.transcriptEntries)
	}
}

func TestTurnCompleteMovesFocusToTranscript(t *testing.T) {
	m := newSizedModel()
	m.focus = focusInput
	next, _ := m.Update(TurnComplete{Message: "done"})
	m = next.(model)
	if m.focus != focusTranscript {
		t.Fatalf("expected focus to move to transcript after completion, got %v", m.focus)
	}
}

func TestEmptyInputArrowKeysScrollTranscript(t *testing.T) {
	m := newSizedModel()
	m.width = 120
	m.height = 20
	m.layout()
	for i := 0; i < 40; i++ {
		m.appendTranscript("Result", "line content", "result")
	}
	m.transcript.GotoBottom()
	bottom := m.transcript.YOffset
	m.focus = focusInput
	m.input.Reset()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	// Up arrow should scroll the transcript without stealing input focus.
	if m.focus != focusInput {
		t.Fatalf("expected focus to remain on input after up key, got %v", m.focus)
	}
	if m.transcript.YOffset >= bottom {
		t.Fatalf("expected transcript to scroll up, got offset=%d bottom=%d", m.transcript.YOffset, bottom)
	}
}

func TestMarkdownRendererFallback(t *testing.T) {
	r := newMarkdownRenderer(80)
	input := "**bold text**"
	out := r.Render(input)
	if out == "" {
		t.Fatal("expected non-empty render output from markdown renderer")
	}
}

func TestVerbForActivityKind(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{"file_read", "Reading"},
		{"file_write", "Writing"},
		{"file_edit", "Editing"},
		{"search", "Searching"},
		{"bash_start", "Running"},
		{"bash_result", ""},
		{"mcp_start", "Querying"},
		{"mcp_result", ""},
		{"plugin_start", "Invoking"},
		{"plugin_result", ""},
		{"tool_start", "Executing"},
		{"tool_result", ""},
		{"model", "Thinking"},
		{"approval", "Waiting"},
		{"error", "Recovering"},
		{"unknown_kind", "Working"},
		{"", "Working"},
	}
	for _, tc := range cases {
		got := verbForActivityKind(tc.kind)
		if got != tc.want {
			t.Errorf("verbForActivityKind(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestUpdateCompletionFiltering(t *testing.T) {
	m := newSizedModel()

	// Empty input → no completion.
	m.input.SetValue("")
	m.updateCompletion()
	if m.comp.active {
		t.Fatal("expected comp.active == false for empty input")
	}

	// "/" alone → all registry items shown.
	m.input.SetValue("/")
	m.updateCompletion()
	if !m.comp.active {
		t.Fatal("expected comp.active == true for \"/\"")
	}
	if len(m.comp.items) != len(slashRegistry) {
		t.Fatalf("expected %d items for \"/\", got %d", len(slashRegistry), len(m.comp.items))
	}

	// "/se" → only items whose name starts with "/se".
	m.input.SetValue("/se")
	m.updateCompletion()
	if !m.comp.active {
		t.Fatal("expected comp.active == true for \"/se\"")
	}
	for _, item := range m.comp.items {
		if !strings.HasPrefix(item.name, "/se") {
			t.Errorf("unexpected item %q does not start with \"/se\"", item.name)
		}
	}
	if len(m.comp.items) == 0 {
		t.Fatal("expected at least one match for \"/se\" (/session, /security-review)")
	}

	// "/zzz" → no match, overlay dismissed.
	m.input.SetValue("/zzz")
	m.updateCompletion()
	if m.comp.active {
		t.Fatal("expected comp.active == false for \"/zzz\"")
	}

	// Space in value → overlay dismissed even if prefix matches.
	m.input.SetValue("/session args")
	m.updateCompletion()
	if m.comp.active {
		t.Fatal("expected comp.active == false when input contains a space")
	}

	// Changing query resets cursor to 0.
	m.input.SetValue("/s")
	m.updateCompletion()
	m.comp.cursor = 3
	m.comp.query = "/s" // simulate already-set query
	m.input.SetValue("/se")
	m.updateCompletion()
	if m.comp.cursor != 0 {
		t.Fatalf("expected cursor reset to 0 on query change, got %d", m.comp.cursor)
	}
}

func TestCompletionNavigation(t *testing.T) {
	m := newSizedModel()
	m.input.SetValue("/")
	m.updateCompletion()
	if !m.comp.active || len(m.comp.items) == 0 {
		t.Fatal("expected active completion after \"/\"")
	}

	// Down key increments cursor.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.comp.cursor != 1 {
		t.Fatalf("expected cursor == 1 after Down, got %d", m.comp.cursor)
	}

	// Up key decrements cursor.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if m.comp.cursor != 0 {
		t.Fatalf("expected cursor == 0 after Up, got %d", m.comp.cursor)
	}

	// Up at top clamps at 0.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if m.comp.cursor != 0 {
		t.Fatalf("expected cursor clamped at 0, got %d", m.comp.cursor)
	}

	// Enter selects the item: input = name + space, overlay dismissed.
	selected := m.comp.items[0].name
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.comp.active {
		t.Fatal("expected comp.active == false after Enter")
	}
	if m.input.Value() != selected+" " {
		t.Fatalf("expected input %q after Enter, got %q", selected+" ", m.input.Value())
	}

	// Re-open completion, then Esc dismisses without changing input.
	m.input.SetValue("/")
	m.updateCompletion()
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.comp.active {
		t.Fatal("expected comp.active == false after Esc")
	}
	if m.input.Value() != "/" {
		t.Fatalf("expected input unchanged after Esc, got %q", m.input.Value())
	}
}

func TestRenderCompletionInactive(t *testing.T) {
	m := newSizedModel()

	// comp.active == false → empty string.
	m.comp.active = false
	if out := m.renderCompletion(); out != "" {
		t.Fatalf("expected empty string when comp.active == false, got %q", out)
	}

	// comp.active == true but no items → empty string.
	m.comp.active = true
	m.comp.items = nil
	if out := m.renderCompletion(); out != "" {
		t.Fatalf("expected empty string when comp.items is empty, got %q", out)
	}
}

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(value string) string {
	return ansiEscapeRE.ReplaceAllString(value, "")
}
