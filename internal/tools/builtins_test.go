package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func TestWriteFileNewFileEmitsDiffPayload(t *testing.T) {
	root := t.TempDir()
	result, activities := executeWriteFileForTest(t, root, "created.txt", "hello\nworld\n")
	if result.IsError {
		t.Fatalf("write_file failed: %s", result.Output)
	}
	event := requireActivityKind(t, activities, "file_write")
	diff := requireDiffPayload(t, event.Detail)
	if len(diff.Removed) != 0 {
		t.Fatalf("new file should not have removed lines: %#v", diff.Removed)
	}
	if diff.HunkHeader != "@@ -0,0 +1,2 @@" {
		t.Fatalf("unexpected hunk header: %q", diff.HunkHeader)
	}
	if !containsLine(diff.Added, "hello") || !containsLine(diff.Added, "world") {
		t.Fatalf("expected added file contents, got %#v", diff.Added)
	}
}

func TestWriteFileOverwriteEmitsDiffPayload(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	result, activities := executeWriteFileForTest(t, root, "existing.txt", "new\n")
	if result.IsError {
		t.Fatalf("write_file failed: %s", result.Output)
	}
	event := requireActivityKind(t, activities, "file_write")
	diff := requireDiffPayload(t, event.Detail)
	if diff.HunkHeader != "@@ -1,1 +1,1 @@" {
		t.Fatalf("unexpected hunk header: %q", diff.HunkHeader)
	}
	if !containsLine(diff.Removed, "old") || !containsLine(diff.Added, "new") {
		t.Fatalf("expected overwrite diff, got removed=%#v added=%#v", diff.Removed, diff.Added)
	}
}

func TestWriteFileUnchangedContentDoesNotEmitDiffPayload(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "same.txt"), []byte("same\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	result, activities := executeWriteFileForTest(t, root, "same.txt", "same\n")
	if result.IsError {
		t.Fatalf("write_file failed: %s", result.Output)
	}
	event := requireActivityKind(t, activities, "file_write")
	if strings.HasPrefix(strings.TrimSpace(event.Detail), "{") {
		t.Fatalf("unchanged write should not emit diff payload: %s", event.Detail)
	}
	if !strings.Contains(event.Detail, filepath.Join(root, "same.txt")) {
		t.Fatalf("expected normal file path detail, got %q", event.Detail)
	}
}

func TestWriteFileLargeDiffPayloadIsTruncated(t *testing.T) {
	root := t.TempDir()
	var content strings.Builder
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&content, "line %02d\n", i)
	}
	result, activities := executeWriteFileForTest(t, root, "large.txt", content.String())
	if result.IsError {
		t.Fatalf("write_file failed: %s", result.Output)
	}
	event := requireActivityKind(t, activities, "file_write")
	diff := requireDiffPayload(t, event.Detail)
	if len(diff.Added) != maxDiffHunkLines+1 {
		t.Fatalf("expected %d added display lines, got %d: %#v", maxDiffHunkLines+1, len(diff.Added), diff.Added)
	}
	if !strings.Contains(diff.Added[len(diff.Added)-1], "3 more lines") {
		t.Fatalf("expected truncation marker, got %#v", diff.Added)
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

func executeWriteFileForTest(t *testing.T, root, path, content string) (LiveResult, []LiveToolEvent) {
	t.Helper()
	input := map[string]string{"path": path, "content": content}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	var activities []LiveToolEvent
	result := ExecuteLive(LiveContext{
		Root:           root,
		PermissionMode: PermissionWorkspaceWrite,
		Activity: func(event LiveToolEvent) {
			activities = append(activities, event)
		},
	}, LiveCall{ID: "tool-1", Name: "write_file", Input: data})
	return result, activities
}

func requireActivityKind(t *testing.T, activities []LiveToolEvent, kind string) LiveToolEvent {
	t.Helper()
	for _, event := range activities {
		if event.Kind == kind {
			return event
		}
	}
	t.Fatalf("missing activity kind %q in %#v", kind, activities)
	return LiveToolEvent{}
}

func requireDiffPayload(t *testing.T, detail string) fileDiff {
	t.Helper()
	var diff fileDiff
	if err := json.Unmarshal([]byte(detail), &diff); err != nil {
		t.Fatalf("expected diff payload, got %q: %v", detail, err)
	}
	if diff.HunkHeader == "" || (len(diff.Removed) == 0 && len(diff.Added) == 0) {
		t.Fatalf("incomplete diff payload: %#v", diff)
	}
	return diff
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}
