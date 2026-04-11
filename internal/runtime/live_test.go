package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"ascaris/internal/api"
	"ascaris/internal/testutil/mockanthropic"
	"ascaris/internal/tools"
)

func TestLiveHarnessRunPromptEmitsProgressForTextTurn(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(mockanthropic.NewTransport())
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	harness, err := NewLiveHarness(root)
	if err != nil {
		t.Fatalf("new live harness: %v", err)
	}
	var phases []PromptPhase
	_, err = harness.RunPrompt(context.Background(), mockanthropic.ScenarioPrefix+"streaming_text", PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		Progress: func(progress PromptProgress) {
			phases = append(phases, progress.Phase)
		},
	})
	if err != nil {
		t.Fatalf("run prompt: %v", err)
	}
	expected := []PromptPhase{
		PromptPhaseStarting,
		PromptPhaseWaitingModel,
		PromptPhaseFinalizing,
	}
	assertPromptPhases(t, phases, expected)
}

func TestLiveHarnessRunPromptEmitsProgressForToolTurn(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(mockanthropic.NewTransport())
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	if err := os.WriteFile(filepath.Join(root, "fixture.txt"), []byte("alpha parity line\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	harness, err := NewLiveHarness(root)
	if err != nil {
		t.Fatalf("new live harness: %v", err)
	}
	var phases []PromptPhase
	_, err = harness.RunPrompt(context.Background(), mockanthropic.ScenarioPrefix+"read_file_roundtrip", PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		Progress: func(progress PromptProgress) {
			phases = append(phases, progress.Phase)
		},
	})
	if err != nil {
		t.Fatalf("run prompt: %v", err)
	}
	expected := []PromptPhase{
		PromptPhaseStarting,
		PromptPhaseWaitingModel,
		PromptPhaseExecutingTools,
		PromptPhaseWaitingModel,
		PromptPhaseFinalizing,
	}
	assertPromptPhases(t, phases, expected)
}

func assertPromptPhases(t *testing.T, got, want []PromptPhase) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("unexpected phase count: got=%v want=%v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("unexpected phase order: got=%v want=%v", got, want)
		}
	}
}
