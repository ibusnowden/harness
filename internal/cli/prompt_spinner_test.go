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
	started bool
	stopped bool
	labels  []string
}

func (s *spinnerStub) Start(label string) {
	s.started = true
	s.labels = append(s.labels, "start:"+label)
}

func (s *spinnerStub) Update(label string) {
	s.labels = append(s.labels, "update:"+label)
}

func (s *spinnerStub) Stop() {
	s.stopped = true
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
