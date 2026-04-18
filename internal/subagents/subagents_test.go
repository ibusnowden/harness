package subagents

import "testing"

func TestRegistryCreatePersistsAssignment(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	assignment, err := registry.Create("worker-1", "explorer", "inspect provider routing", "focus on api package", []string{"read_file"}, []string{"summarize findings"})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if assignment.AssignmentID == "" || assignment.Status != StatusPending {
		t.Fatalf("unexpected assignment: %#v", assignment)
	}
	if err := SaveRegistry(root, registry); err != nil {
		t.Fatalf("save registry: %v", err)
	}
	loaded, err := LoadRegistry(root)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	snapshot := loaded.Snapshot()
	if len(snapshot.Assignments) != 1 || snapshot.Assignments[0].Prompt != "inspect provider routing" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}
