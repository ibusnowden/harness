package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAgentsWithShadowing(t *testing.T) {
	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	projectRoot := filepath.Join(root, ".ascaris", "agents")
	userRoot := filepath.Join(configHome, "agents")
	for _, dir := range []string{projectRoot, userRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	projectAgent := "name = \"planner\"\ndescription = \"Project planner\"\nmodel = \"sonnet\"\nmodel_reasoning_effort = \"high\"\n"
	userAgent := "name = \"planner\"\ndescription = \"User planner\"\nmodel = \"haiku\"\n"
	if err := os.WriteFile(filepath.Join(projectRoot, "planner.toml"), []byte(projectAgent), 0o644); err != nil {
		t.Fatalf("write project agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userRoot, "planner.toml"), []byte(userAgent), 0o644); err != nil {
		t.Fatalf("write user agent: %v", err)
	}
	items, err := Load(root)
	if err != nil {
		t.Fatalf("load agents: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(items))
	}
	if items[0].ShadowedBy != nil {
		t.Fatalf("expected first agent to be active, got %#v", items[0])
	}
	if items[1].ShadowedBy == nil || *items[1].ShadowedBy != ScopeProject {
		t.Fatalf("expected second agent to be shadowed by project scope, got %#v", items[1])
	}
}
