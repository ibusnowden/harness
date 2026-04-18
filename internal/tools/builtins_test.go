package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"ascaris/internal/subagents"
)

func TestWebToolsDisabledByDefault(t *testing.T) {
	t.Setenv("ASCARIS_ENABLE_WEB", "")
	for _, name := range []string{"web_search", "web_fetch"} {
		input := map[string]string{"query": "ascaris", "url": "https://example.com"}
		data, _ := json.Marshal(input)
		result := ExecuteLive(LiveContext{Root: t.TempDir()}, LiveCall{ID: "tool-1", Name: name, Input: data})
		if !result.IsError {
			t.Fatalf("%s: expected disabled error, got %#v", name, result)
		}
		if !strings.Contains(result.Output, "disabled") {
			t.Fatalf("%s: unexpected output: %q", name, result.Output)
		}
	}
}

func TestWebToolsAreListed(t *testing.T) {
	defs := LiveDefinitions(nil)
	seen := map[string]bool{}
	for _, def := range defs {
		seen[def.Name] = true
	}
	for _, name := range []string{"web_search", "web_fetch"} {
		if !seen[name] {
			t.Fatalf("missing tool definition %s", name)
		}
	}
}

func TestDelegateTaskCreatesSubagentAssignment(t *testing.T) {
	root := t.TempDir()
	input := map[string]any{
		"role":                "explorer",
		"prompt":              "inspect api provider routing",
		"context":             "focus on internal/api",
		"allowed_tools":       []string{"read_file", "grep_search"},
		"acceptance_criteria": []string{"return findings only"},
	}
	data, _ := json.Marshal(input)
	result := ExecuteLive(LiveContext{Root: root, PermissionMode: PermissionWorkspaceWrite}, LiveCall{ID: "tool-1", Name: "delegate_task", Input: data})
	if result.IsError {
		t.Fatalf("delegate_task failed: %s", result.Output)
	}
	if !strings.Contains(result.Output, "assignment_id") || !strings.Contains(result.Output, "worker_id") {
		t.Fatalf("unexpected result: %s", result.Output)
	}
	registry, err := subagents.LoadRegistry(root)
	if err != nil {
		t.Fatalf("load subagent registry: %v", err)
	}
	snapshot := registry.Snapshot()
	if len(snapshot.Assignments) != 1 || snapshot.Assignments[0].Role != "explorer" {
		t.Fatalf("unexpected assignments: %#v", snapshot.Assignments)
	}
}
