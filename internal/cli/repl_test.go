package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ascaris/internal/repl"
	hruntime "ascaris/internal/runtime"
	"ascaris/internal/sessions"
)

func TestRunLaunchesTUIForInteractiveStdin(t *testing.T) {
	previousHarness := newLiveHarness
	previousInteractive := isInteractiveReader
	previousLaunch := launchREPL
	defer func() {
		newLiveHarness = previousHarness
		isInteractiveReader = previousInteractive
		launchREPL = previousLaunch
	}()

	newLiveHarness = func(string) (livePromptHarness, error) {
		return spinnerHarnessStub{
			run: func(context.Context, string, hruntime.PromptOptions) (hruntime.PromptSummary, error) {
				return hruntime.PromptSummary{}, nil
			},
		}, nil
	}
	isInteractiveReader = func(any) bool { return true }

	called := false
	launchREPL = func(_ context.Context, cfg repl.Config) error {
		called = true
		if !strings.Contains(cfg.Status.Product, "ascaris") {
			t.Fatalf("unexpected product label: %q", cfg.Status.Product)
		}
		if cfg.Status.SessionID != "new" {
			t.Fatalf("unexpected session label: %q", cfg.Status.SessionID)
		}
		if cfg.Status.Recent.ID != "" {
			t.Fatalf("expected no recent session summary, got %#v", cfg.Status.Recent)
		}
		if cfg.RunPrompt == nil || cfg.HandleSlash == nil {
			t.Fatalf("expected TUI callbacks to be configured")
		}
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(Context{Root: t.TempDir()}, nil, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	if !called {
		t.Fatalf("expected TUI launcher to be called")
	}
}

func TestTUIRunPromptResumesLatestAndRefreshesSessionID(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	previousHarness := newLiveHarness
	previousInteractive := isInteractiveReader
	previousLaunch := launchREPL
	defer func() {
		newLiveHarness = previousHarness
		isInteractiveReader = previousInteractive
		launchREPL = previousLaunch
	}()

	root := t.TempDir()
	if _, err := sessions.SaveManaged(sessions.NewManagedSession("seed-session", "sonnet"), root); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	var resumes []string
	newLiveHarness = func(string) (livePromptHarness, error) {
		return spinnerHarnessStub{
			run: func(_ context.Context, prompt string, opts hruntime.PromptOptions) (hruntime.PromptSummary, error) {
				resumes = append(resumes, opts.ResumeSession)
				return hruntime.PromptSummary{Message: "reply:" + prompt, SessionID: "fresh-session"}, nil
			},
		}, nil
	}
	isInteractiveReader = func(any) bool { return true }

	launchREPL = func(_ context.Context, cfg repl.Config) error {
		if cfg.Status.SessionID != "seed-session" {
			t.Fatalf("expected latest session label, got %q", cfg.Status.SessionID)
		}
		if cfg.Status.Recent.ID != "seed-session" {
			t.Fatalf("expected recent session summary, got %#v", cfg.Status.Recent)
		}
		var messages []tea.Msg
		cfg.RunPrompt(context.Background(), "hello", func(msg tea.Msg) {
			messages = append(messages, msg)
		})
		if len(resumes) != 1 || resumes[0] != "latest" {
			t.Fatalf("unexpected resume refs: %#v", resumes)
		}
		last := messages[len(messages)-1]
		done, ok := last.(repl.TurnComplete)
		if !ok {
			t.Fatalf("expected TurnComplete, got %T", last)
		}
		if done.SessionID != "fresh-session" {
			t.Fatalf("unexpected session id: %#v", done)
		}
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(Context{Root: root}, nil, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
}

func TestLatestSessionSummaryUsesPromptHistory(t *testing.T) {
	root := t.TempDir()
	session := sessions.NewManagedSession("seed-session", "sonnet")
	session.RecordPrompt("review the latest changes")
	if _, err := sessions.SaveManaged(session, root); err != nil {
		t.Fatalf("save session: %v", err)
	}
	summary := latestSessionSummary(root)
	if summary.ID != "seed-session" {
		t.Fatalf("unexpected summary id: %#v", summary)
	}
	if !strings.Contains(summary.LastPrompt, "review the latest changes") {
		t.Fatalf("expected last prompt in summary: %#v", summary)
	}
	if summary.UpdatedLabel == "" {
		t.Fatalf("expected updated label in summary: %#v", summary)
	}
}

func TestRunSlashInTUIResetsSessionAndCapturesOutput(t *testing.T) {
	root := t.TempDir()
	result := runSlashInTUI(Context{Root: root}, globalOptions{}, "/clear")
	if !result.ResetState {
		t.Fatalf("expected clear to reset state")
	}
	if result.SessionID != "new" {
		t.Fatalf("expected clear to reset session label, got %q", result.SessionID)
	}
	if !strings.Contains(result.Output, "Cleared") {
		t.Fatalf("unexpected slash output: %q", result.Output)
	}
}
