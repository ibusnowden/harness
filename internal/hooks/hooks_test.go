package hooks

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"ascaris/internal/config"
)

func TestRunnerParsesStructuredPreToolOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is unix-only")
	}
	root := t.TempDir()
	script := filepath.Join(root, "hook.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '{\"systemMessage\":\"hook ok\",\"hookSpecificOutput\":{\"permissionDecision\":\"danger-full-access\",\"permissionDecisionReason\":\"approved by hook\",\"updatedInput\":\"{\\\"command\\\":\\\"git status\\\"}\"}}'\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	runner := New(config.HookSettings{PreToolUse: []string{script}})
	result := runner.RunPreToolUse("bash", `{"command":"pwd"}`)
	if result.Denied || result.Failed {
		t.Fatalf("unexpected hook failure: %#v", result)
	}
	if result.PermissionOverride != "danger-full-access" {
		t.Fatalf("expected permission override, got %#v", result)
	}
	if result.UpdatedInput != `{"command":"git status"}` {
		t.Fatalf("unexpected updated input: %q", result.UpdatedInput)
	}
	if len(result.Messages) != 1 || result.Messages[0] != "hook ok" {
		t.Fatalf("unexpected messages: %#v", result.Messages)
	}
}

func TestRunnerDeniesOnExitCodeTwo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is unix-only")
	}
	root := t.TempDir()
	script := filepath.Join(root, "deny.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'denied by policy'\nexit 2\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	runner := New(config.HookSettings{PreToolUse: []string{script}})
	result := runner.RunPreToolUse("bash", `{"command":"pwd"}`)
	if !result.Denied || result.Failed {
		t.Fatalf("expected denied hook result, got %#v", result)
	}
}
