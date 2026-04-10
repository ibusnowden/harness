package migrations

import (
	"os"
	"path/filepath"
	"testing"

	"ascaris/internal/sessions"
)

func TestMigrateLegacyCopiesConfigStateAndPluginManifest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ASCARIS_CONFIG_HOME", filepath.Join(root, ".ascaris-home"))
	for _, dir := range []string{
		filepath.Join(root, ".claw"),
		filepath.Join(root, ".claw", "skills"),
		filepath.Join(root, ".claw", "agents"),
		filepath.Join(root, ".claw", "sessions"),
		filepath.Join(root, "demo", ".claude-plugin"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".claw.json"), []byte(`{"model":"sonnet"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claw", "worker-state.json"), []byte(`{"workers":[]}`), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claw", "skills", "legacy.md"), []byte("# legacy"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claw", "agents", "planner.toml"), []byte("name = \"planner\"\n"), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claw", "sessions", "legacy.json"), []byte(`{"session_id":"legacy","messages":["hello"],"input_tokens":12,"output_tokens":7}`), 0o644); err != nil {
		t.Fatalf("write legacy session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claw", "sessions", "latest"), []byte("legacy"), 0o644); err != nil {
		t.Fatalf("write latest alias: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "demo", ".claude-plugin", "plugin.json"), []byte(`{"name":"Demo","version":"1.0.0","description":"x","permissions":[]}`), 0o644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	report, err := MigrateLegacy(root)
	if err != nil {
		t.Fatalf("migrate legacy: %v", err)
	}
	if len(report.Changes) == 0 {
		t.Fatalf("expected migration changes")
	}
	for _, path := range []string{
		filepath.Join(root, ".ascaris.json"),
		filepath.Join(root, ".ascaris", "worker-state.json"),
		filepath.Join(root, ".ascaris", "skills", "legacy.md"),
		filepath.Join(root, ".ascaris", "agents", "planner.toml"),
		filepath.Join(root, "demo", ".ascaris-plugin", "plugin.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected migrated path %s: %v", path, err)
		}
	}
	session, err := sessions.LoadManaged(root, "latest")
	if err != nil {
		t.Fatalf("load migrated session: %v", err)
	}
	if session.Meta.SessionID != "legacy" || len(session.Messages) != 1 {
		t.Fatalf("unexpected migrated session: %#v", session)
	}
}
