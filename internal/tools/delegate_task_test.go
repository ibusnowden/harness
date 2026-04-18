package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ascaris/internal/subagents"
)

func TestSubagentListReturnsEmptyList(t *testing.T) {
	result := ExecuteLive(LiveContext{Root: t.TempDir()}, LiveCall{ID: "1", Name: "subagent_list", Input: json.RawMessage(`{}`)})
	if result.IsError {
		t.Fatalf("subagent_list failed: %s", result.Output)
	}
	var items []subagents.Assignment
	if err := json.Unmarshal([]byte(result.Output), &items); err != nil {
		t.Fatalf("expected JSON array, got: %s", result.Output)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty list, got %d items", len(items))
	}
}

func TestSubagentListReturnsCreatedAssignment(t *testing.T) {
	root := t.TempDir()
	input := map[string]any{"prompt": "explore the codebase"}
	data, _ := json.Marshal(input)
	ExecuteLive(LiveContext{Root: root, PermissionMode: PermissionWorkspaceWrite}, LiveCall{ID: "1", Name: "delegate_task", Input: data})

	result := ExecuteLive(LiveContext{Root: root}, LiveCall{ID: "2", Name: "subagent_list", Input: json.RawMessage(`{}`)})
	if result.IsError {
		t.Fatalf("subagent_list failed: %s", result.Output)
	}
	var items []subagents.Assignment
	if err := json.Unmarshal([]byte(result.Output), &items); err != nil {
		t.Fatalf("expected JSON array, got: %s", result.Output)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(items))
	}
	if items[0].Prompt != "explore the codebase" {
		t.Fatalf("unexpected prompt: %q", items[0].Prompt)
	}
}

func TestSubagentGetReturnsAssignment(t *testing.T) {
	root := t.TempDir()
	input := map[string]any{"prompt": "read and summarize"}
	data, _ := json.Marshal(input)
	delegateResult := ExecuteLive(LiveContext{Root: root, PermissionMode: PermissionWorkspaceWrite}, LiveCall{ID: "1", Name: "delegate_task", Input: data})
	if delegateResult.IsError {
		t.Fatalf("delegate_task failed: %s", delegateResult.Output)
	}
	var delegateOut struct {
		AssignmentID string `json:"assignment_id"`
	}
	if err := json.Unmarshal([]byte(delegateResult.Output), &delegateOut); err != nil {
		t.Fatalf("decode delegate_task output: %v", err)
	}

	getInput, _ := json.Marshal(map[string]string{"assignment_id": delegateOut.AssignmentID})
	result := ExecuteLive(LiveContext{Root: root}, LiveCall{ID: "2", Name: "subagent_get", Input: getInput})
	if result.IsError {
		t.Fatalf("subagent_get failed: %s", result.Output)
	}
	if !strings.Contains(result.Output, delegateOut.AssignmentID) {
		t.Fatalf("expected assignment_id in output, got: %s", result.Output)
	}
}

func TestSubagentGetReturnsErrorForUnknownID(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"assignment_id": "nonexistent_123"})
	result := ExecuteLive(LiveContext{Root: t.TempDir()}, LiveCall{ID: "1", Name: "subagent_get", Input: input})
	if !result.IsError {
		t.Fatalf("expected error for unknown assignment ID, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "not found") {
		t.Fatalf("expected 'not found' in error, got: %s", result.Output)
	}
}

func TestDelegateTaskCallbackReturnsCompletedResult(t *testing.T) {
	root := t.TempDir()
	called := false
	ctx := LiveContext{
		Root:           root,
		Context:        context.Background(),
		PermissionMode: PermissionWorkspaceWrite,
		DelegateTask: func(runCtx context.Context, a subagents.Assignment) (subagents.Assignment, error) {
			called = true
			a.Status = "completed"
			a.ResultSummary = "task completed successfully"
			return a, nil
		},
	}
	input, _ := json.Marshal(map[string]any{"prompt": "do something useful"})
	result := ExecuteLive(ctx, LiveCall{ID: "1", Name: "delegate_task", Input: input})
	if result.IsError {
		t.Fatalf("delegate_task failed: %s", result.Output)
	}
	if !called {
		t.Fatalf("DelegateTask callback was not called")
	}
	if !strings.Contains(result.Output, "task completed successfully") {
		t.Fatalf("expected result_summary in output, got: %s", result.Output)
	}
}

func TestDelegateTaskCallbackErrorIsReturned(t *testing.T) {
	root := t.TempDir()
	ctx := LiveContext{
		Root:           root,
		Context:        context.Background(),
		PermissionMode: PermissionWorkspaceWrite,
		DelegateTask: func(runCtx context.Context, a subagents.Assignment) (subagents.Assignment, error) {
			return a, errors.New("subagent execution failed")
		},
	}
	input, _ := json.Marshal(map[string]any{"prompt": "trigger failure"})
	result := ExecuteLive(ctx, LiveCall{ID: "1", Name: "delegate_task", Input: input})
	if !result.IsError {
		t.Fatalf("expected error when DelegateTask callback fails")
	}
	if !strings.Contains(result.Output, "subagent execution failed") {
		t.Fatalf("expected error message in output, got: %s", result.Output)
	}
}
