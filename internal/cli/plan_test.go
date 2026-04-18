package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hruntime "ascaris/internal/runtime"
)

func TestPlanCommandExecuteUsesSharedPlanExecutor(t *testing.T) {
	previousHarness := newLiveHarness
	defer func() {
		newLiveHarness = previousHarness
	}()

	root := t.TempDir()
	executeCalls := 0
	runCalls := 0
	newLiveHarness = func(string) (livePromptHarness, error) {
		return spinnerHarnessStub{
			run: func(context.Context, string, hruntime.PromptOptions) (hruntime.PromptSummary, error) {
				runCalls++
				return hruntime.PromptSummary{}, nil
			},
			exec: func(context.Context, hruntime.PromptOptions) (hruntime.PromptSummary, error) {
				executeCalls++
				return hruntime.PromptSummary{Message: "plan executed"}, nil
			},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(Context{Root: root}, []string{"plan", "--execute", "implement", "the", "approved", "plan"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	if executeCalls != 1 || runCalls != 0 {
		t.Fatalf("expected ExecutePlan once and RunPrompt never, got execute=%d run=%d", executeCalls, runCalls)
	}
	if strings.TrimSpace(stdout.String()) != "plan executed" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".ascaris", "tasks.json")); err != nil {
		t.Fatalf("expected tasks.json to be created, got %v", err)
	}
}

func TestRunSlashInTUIPlanRequestsApproval(t *testing.T) {
	root := t.TempDir()
	result := runSlashInTUI(Context{Root: root}, globalOptions{}, "/plan implement orchestrator hardening")
	if result.Error {
		t.Fatalf("expected successful slash plan result, got %#v", result)
	}
	if !result.RequestPlanApproval {
		t.Fatalf("expected plan approval request, got %#v", result)
	}
	if !strings.Contains(result.Output, "# Implementation Plan") {
		t.Fatalf("expected rendered plan output, got %q", result.Output)
	}
	if _, err := os.Stat(filepath.Join(root, ".ascaris", "tasks.json")); err != nil {
		t.Fatalf("expected tasks.json to be created, got %v", err)
	}
}
