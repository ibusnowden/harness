package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	hruntime "ascaris/internal/runtime"
)

type spinnerHarnessStub struct {
	run func(context.Context, string, hruntime.PromptOptions) (hruntime.PromptSummary, error)
}

func (s spinnerHarnessStub) RunPrompt(ctx context.Context, prompt string, opts hruntime.PromptOptions) (hruntime.PromptSummary, error) {
	return s.run(ctx, prompt, opts)
}

type spinnerStub struct {
	events  *[]string
	started bool
	stopped bool
	labels  []string
}

func (s *spinnerStub) Start(label string) {
	s.started = true
	s.labels = append(s.labels, "start:"+label)
	if s.events != nil {
		*s.events = append(*s.events, "spinner:start:"+label)
	}
}

func (s *spinnerStub) Update(label string) {
	s.labels = append(s.labels, "update:"+label)
	if s.events != nil {
		*s.events = append(*s.events, "spinner:update:"+label)
	}
}

func (s *spinnerStub) Stop() {
	s.stopped = true
	if s.events != nil {
		*s.events = append(*s.events, "spinner:stop")
	}
}

type recordingWriter struct {
	buffer *bytes.Buffer
	events *[]string
	name   string
}

func (w recordingWriter) Write(p []byte) (int, error) {
	if w.events != nil {
		*w.events = append(*w.events, w.name+":"+string(p))
	}
	if w.buffer != nil {
		return w.buffer.Write(p)
	}
	return len(p), nil
}

func TestPromptStartsSpinnerOnlyForInteractiveTextMode(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	previousHarness := newLiveHarness
	previousSpinner := newPromptSpinner
	previousInteractive := isInteractiveWriter
	defer func() {
		newLiveHarness = previousHarness
		newPromptSpinner = previousSpinner
		isInteractiveWriter = previousInteractive
	}()

	spinner := &spinnerStub{}
	newLiveHarness = func(root string) (livePromptHarness, error) {
		return spinnerHarnessStub{
			run: func(_ context.Context, prompt string, opts hruntime.PromptOptions) (hruntime.PromptSummary, error) {
				if opts.Progress == nil {
					t.Fatalf("expected progress callback")
				}
				opts.Progress(hruntime.PromptProgress{Phase: hruntime.PromptPhaseWaitingModel, Iteration: 1})
				opts.Progress(hruntime.PromptProgress{Phase: hruntime.PromptPhaseFinalizing, Iteration: 1})
				return hruntime.PromptSummary{Message: "done"}, nil
			},
		}, nil
	}
	newPromptSpinner = func(writer io.Writer) promptSpinner {
		return spinner
	}
	isInteractiveWriter = func(io.Writer) bool { return true }

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(Context{Root: t.TempDir()}, []string{"prompt", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	if stdout.String() != "done\n" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if !spinner.started || !spinner.stopped {
		t.Fatalf("expected spinner lifecycle, got %#v", spinner)
	}
	if len(spinner.labels) < 3 || spinner.labels[0] != "start:Starting" {
		t.Fatalf("unexpected spinner labels: %#v", spinner.labels)
	}
}

func TestPromptSkipsSpinnerForJSONAndNonInteractiveMode(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	previousHarness := newLiveHarness
	previousSpinner := newPromptSpinner
	previousInteractive := isInteractiveWriter
	defer func() {
		newLiveHarness = previousHarness
		newPromptSpinner = previousSpinner
		isInteractiveWriter = previousInteractive
	}()

	spinnerCreated := false
	newLiveHarness = func(root string) (livePromptHarness, error) {
		return spinnerHarnessStub{
			run: func(_ context.Context, prompt string, opts hruntime.PromptOptions) (hruntime.PromptSummary, error) {
				return hruntime.PromptSummary{Message: "done"}, nil
			},
		}, nil
	}
	newPromptSpinner = func(writer io.Writer) promptSpinner {
		spinnerCreated = true
		return &spinnerStub{}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	isInteractiveWriter = func(io.Writer) bool { return true }
	code := Run(Context{Root: t.TempDir()}, []string{"--output-format=json", "prompt", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected json prompt success, got %d with stderr %q", code, stderr.String())
	}
	if spinnerCreated {
		t.Fatalf("expected spinner to stay disabled for json mode")
	}

	spinnerCreated = false
	stdout.Reset()
	stderr.Reset()
	isInteractiveWriter = func(io.Writer) bool { return false }
	code = Run(Context{Root: t.TempDir()}, []string{"prompt", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected text prompt success, got %d with stderr %q", code, stderr.String())
	}
	if spinnerCreated {
		t.Fatalf("expected spinner to stay disabled for non-interactive mode")
	}
}

func TestPromptPausesSpinnerAroundApprovalPrompt(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	previousHarness := newLiveHarness
	previousSpinner := newPromptSpinner
	previousInteractive := isInteractiveWriter
	defer func() {
		newLiveHarness = previousHarness
		newPromptSpinner = previousSpinner
		isInteractiveWriter = previousInteractive
	}()

	var events []string
	spinner := &spinnerStub{events: &events}
	newLiveHarness = func(root string) (livePromptHarness, error) {
		return spinnerHarnessStub{
			run: func(_ context.Context, prompt string, opts hruntime.PromptOptions) (hruntime.PromptSummary, error) {
				opts.Progress(hruntime.PromptProgress{Phase: hruntime.PromptPhaseWaitingModel, Iteration: 1})
				approved, err := opts.Prompter.Approve("bash", `{"command":"printf 'ok'"}`)
				if err != nil {
					t.Fatalf("approve: %v", err)
				}
				if !approved {
					t.Fatalf("expected approval")
				}
				opts.Progress(hruntime.PromptProgress{Phase: hruntime.PromptPhaseFinalizing, Iteration: 1})
				return hruntime.PromptSummary{Message: "done"}, nil
			},
		}, nil
	}
	newPromptSpinner = func(writer io.Writer) promptSpinner {
		return spinner
	}
	isInteractiveWriter = func(io.Writer) bool { return true }

	var stdoutBuf bytes.Buffer
	stdout := recordingWriter{buffer: &stdoutBuf, events: &events, name: "stdout"}
	var stderr bytes.Buffer
	code := Run(Context{Root: t.TempDir()}, []string{"prompt", "hello"}, strings.NewReader("y\n"), stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdoutBuf.String(), "Permission approval required") {
		t.Fatalf("expected approval prompt in stdout: %q", stdoutBuf.String())
	}
	if count := countExact(events, "spinner:stop"); count < 2 {
		t.Fatalf("expected spinner to stop for approval and final output, got events %#v", events)
	}
	firstStop := indexOf(events, "spinner:stop")
	firstApproval := indexContaining(events, "stdout:Permission approval required")
	if firstStop == -1 || firstApproval == -1 || firstStop > firstApproval {
		t.Fatalf("expected spinner stop before approval prompt, got events %#v", events)
	}
	if countStarts(events, "spinner:start:Thinking") == 0 {
		t.Fatalf("expected spinner to resume after approval, got events %#v", events)
	}
}

func TestPromptStopsSpinnerBeforeFinalOutput(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	previousHarness := newLiveHarness
	previousSpinner := newPromptSpinner
	previousInteractive := isInteractiveWriter
	defer func() {
		newLiveHarness = previousHarness
		newPromptSpinner = previousSpinner
		isInteractiveWriter = previousInteractive
	}()

	var events []string
	spinner := &spinnerStub{events: &events}
	newLiveHarness = func(root string) (livePromptHarness, error) {
		return spinnerHarnessStub{
			run: func(_ context.Context, prompt string, opts hruntime.PromptOptions) (hruntime.PromptSummary, error) {
				opts.Progress(hruntime.PromptProgress{Phase: hruntime.PromptPhaseFinalizing, Iteration: 1})
				return hruntime.PromptSummary{Message: "done"}, nil
			},
		}, nil
	}
	newPromptSpinner = func(writer io.Writer) promptSpinner {
		return spinner
	}
	isInteractiveWriter = func(io.Writer) bool { return true }

	var stdoutBuf bytes.Buffer
	stdout := recordingWriter{buffer: &stdoutBuf, events: &events, name: "stdout"}
	var stderr bytes.Buffer
	code := Run(Context{Root: t.TempDir()}, []string{"prompt", "hello"}, strings.NewReader(""), stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	lastStop := lastIndexOf(events, "spinner:stop")
	outputWrite := indexContaining(events, "stdout:done\n")
	if lastStop == -1 || outputWrite == -1 || lastStop > outputWrite {
		t.Fatalf("expected spinner stop before final output, got events %#v", events)
	}
}

func TestPromptStopsSpinnerBeforeErrorOutput(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	previousHarness := newLiveHarness
	previousSpinner := newPromptSpinner
	previousInteractive := isInteractiveWriter
	defer func() {
		newLiveHarness = previousHarness
		newPromptSpinner = previousSpinner
		isInteractiveWriter = previousInteractive
	}()

	var events []string
	spinner := &spinnerStub{events: &events}
	newLiveHarness = func(root string) (livePromptHarness, error) {
		return spinnerHarnessStub{
			run: func(_ context.Context, prompt string, opts hruntime.PromptOptions) (hruntime.PromptSummary, error) {
				opts.Progress(hruntime.PromptProgress{Phase: hruntime.PromptPhaseWaitingModel, Iteration: 1})
				return hruntime.PromptSummary{}, io.EOF
			},
		}, nil
	}
	newPromptSpinner = func(writer io.Writer) promptSpinner {
		return spinner
	}
	isInteractiveWriter = func(io.Writer) bool { return true }

	var stdout bytes.Buffer
	var stderrBuf bytes.Buffer
	stderr := recordingWriter{buffer: &stderrBuf, events: &events, name: "stderr"}
	code := Run(Context{Root: t.TempDir()}, []string{"prompt", "hello"}, strings.NewReader(""), &stdout, stderr)
	if code == 0 {
		t.Fatalf("expected failure")
	}
	lastStop := lastIndexOf(events, "spinner:stop")
	errorWrite := indexContaining(events, "stderr:EOF\n")
	if lastStop == -1 || errorWrite == -1 || lastStop > errorWrite {
		t.Fatalf("expected spinner stop before error output, got events %#v", events)
	}
}

func indexOf(items []string, want string) int {
	for index, item := range items {
		if item == want {
			return index
		}
	}
	return -1
}

func lastIndexOf(items []string, want string) int {
	for index := len(items) - 1; index >= 0; index-- {
		if items[index] == want {
			return index
		}
	}
	return -1
}

func indexContaining(items []string, want string) int {
	for index, item := range items {
		if strings.Contains(item, want) {
			return index
		}
	}
	return -1
}

func countStarts(items []string, prefix string) int {
	count := 0
	for _, item := range items {
		if strings.HasPrefix(item, prefix) {
			count++
		}
	}
	return count
}

func countExact(items []string, want string) int {
	count := 0
	for _, item := range items {
		if item == want {
			count++
		}
	}
	return count
}
