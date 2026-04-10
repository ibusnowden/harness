package state

import (
	"path/filepath"
	"testing"

	"ascaris/internal/recovery"
)

func TestRegistryTracksTrustReadyAndSnapshotPersistence(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	worker := registry.Create(root, []string{root}, true)
	observed, err := registry.Observe(worker.WorkerID, "Trust this workspace before continuing")
	if err != nil {
		t.Fatalf("observe trust prompt: %v", err)
	}
	if observed.Status != WorkerSpawning || !observed.TrustGateCleared {
		t.Fatalf("expected auto-resolved trust prompt, got %#v", observed)
	}
	if _, err := registry.Observe(worker.WorkerID, "Ascaris> ready for prompt"); err != nil {
		t.Fatalf("observe ready prompt: %v", err)
	}
	ready, err := registry.AwaitReady(worker.WorkerID)
	if err != nil {
		t.Fatalf("await ready: %v", err)
	}
	if !ready.Ready {
		t.Fatalf("expected worker ready snapshot, got %#v", ready)
	}
	if _, err := registry.SendPrompt(worker.WorkerID, "review repo"); err != nil {
		t.Fatalf("send prompt: %v", err)
	}
	if _, err := registry.ObserveCompletion(worker.WorkerID, "end_turn", 12); err != nil {
		t.Fatalf("observe completion: %v", err)
	}
	snapshot := registry.Snapshot()
	if len(snapshot.Workers) != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if err := Save(root, snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if len(loaded.Workers) != 1 || loaded.Workers[0].WorkerID != worker.WorkerID {
		t.Fatalf("unexpected loaded snapshot: %#v", loaded)
	}
	if got := StatePath(root); got != filepath.Join(root, ".ascaris", "worker-state.json") {
		t.Fatalf("unexpected state path: %s", got)
	}
}

func TestRegistryArmsReplayForPromptMisdelivery(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	worker := registry.Create(root, []string{root}, true)
	if _, err := registry.Observe(worker.WorkerID, "Ascaris> ready for prompt"); err != nil {
		t.Fatalf("observe ready: %v", err)
	}
	if _, err := registry.SendPrompt(worker.WorkerID, "fix the failing test"); err != nil {
		t.Fatalf("send prompt: %v", err)
	}
	observed, err := registry.Observe(worker.WorkerID, "command not found: fix the failing test")
	if err != nil {
		t.Fatalf("observe misdelivery: %v", err)
	}
	if observed.Status != WorkerReadyForPrompt || observed.ReplayPrompt == "" {
		t.Fatalf("expected prompt replay to be armed, got %#v", observed)
	}
}

func TestRegistryRecordsRecoveryEvents(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	worker := registry.Create(root, []string{root}, true)
	if _, result, err := registry.ApplyRecovery(worker.WorkerID, recovery.ScenarioProviderFailure); err != nil {
		t.Fatalf("apply recovery: %v", err)
	} else if result.Kind != recovery.ResultRecovered {
		t.Fatalf("expected recovered result, got %#v", result)
	}
	updated, ok := registry.Get(worker.WorkerID)
	if !ok {
		t.Fatalf("expected worker after recovery")
	}
	if len(updated.RecoveryEvents) == 0 {
		t.Fatalf("expected recovery events to be recorded")
	}
}
