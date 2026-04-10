package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolveAndInstallSkills(t *testing.T) {
	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	projectSkill := filepath.Join(root, ".ascaris", "skills", "plan")
	legacyCommand := filepath.Join(root, ".ascaris", "commands", "handoff.md")
	userSkill := filepath.Join(configHome, "skills", "plan")
	for _, dir := range []string{projectSkill, filepath.Dir(legacyCommand), userSkill} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(projectSkill, "SKILL.md"), []byte("---\nname: plan\ndescription: Project plan\n---\n"), 0o644); err != nil {
		t.Fatalf("write project skill: %v", err)
	}
	if err := os.WriteFile(legacyCommand, []byte("---\nname: handoff\ndescription: Legacy command\n---\n"), 0o644); err != nil {
		t.Fatalf("write legacy command: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userSkill, "SKILL.md"), []byte("---\nname: plan\ndescription: User plan\n---\n"), 0o644); err != nil {
		t.Fatalf("write user skill: %v", err)
	}

	items, err := Load(root)
	if err != nil {
		t.Fatalf("load skills: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 skill entries, got %d", len(items))
	}
	path, err := ResolvePath(root, "$plan")
	if err != nil {
		t.Fatalf("resolve project skill: %v", err)
	}
	if path != filepath.Join(projectSkill, "SKILL.md") {
		t.Fatalf("unexpected resolved path: %s", path)
	}
	source := filepath.Join(root, "verify")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("mkdir source skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: verify\ndescription: Verify things\n---\n"), 0o644); err != nil {
		t.Fatalf("write source skill: %v", err)
	}
	installed, err := Install(root, source)
	if err != nil {
		t.Fatalf("install skill: %v", err)
	}
	if installed.InvocationName != "verify" {
		t.Fatalf("unexpected install result: %#v", installed)
	}
	if _, err := os.Stat(filepath.Join(configHome, "skills", "verify", "SKILL.md")); err != nil {
		t.Fatalf("expected installed skill: %v", err)
	}
}
