package planning

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ascaris/internal/tasks"
)

func TestCreatePersistsPlanAndTasks(t *testing.T) {
	root := t.TempDir()
	plan, err := Create(root, Options{Request: "add web search and then validate provider routing"})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if plan.ID == "" || plan.Status != StatusDraft {
		t.Fatalf("unexpected plan metadata: %#v", plan)
	}
	if len(plan.Tasks) < 4 {
		t.Fatalf("expected decomposed tasks, got %#v", plan.Tasks)
	}
	if _, err := os.Stat(filepath.Join(root, ".ascaris", "plans", plan.ID+".json")); err != nil {
		t.Fatalf("expected plan artifact: %v", err)
	}
	taskList, err := tasks.Load(root)
	if err != nil {
		t.Fatalf("load tasks: %v", err)
	}
	if len(taskList.Tasks) != len(plan.Tasks) {
		t.Fatalf("expected %d tasks, got %d", len(plan.Tasks), len(taskList.Tasks))
	}
}

func TestRenderMarkdownIncludesContracts(t *testing.T) {
	plan, err := Create(t.TempDir(), Options{Request: "refactor api client"})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	out := RenderMarkdown(plan)
	for _, want := range []string{"# Implementation Plan", "Task Contracts", "Acceptance criteria", "Allowed tools"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output:\n%s", want, out)
		}
	}
}
