package stalebranch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckFreshnessAndApplyPolicy(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "--quiet", "-b", "main")
	runGit(t, root, "config", "user.email", "tests@example.com")
	runGit(t, root, "config", "user.name", "Stale Branch Tests")
	writeFile(t, filepath.Join(root, "init.txt"), "initial\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "initial commit", "--quiet")

	runGit(t, root, "checkout", "-b", "topic")
	runGit(t, root, "checkout", "main")
	writeFile(t, filepath.Join(root, "fix.txt"), "fix\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "fix: resolve timeout", "--quiet")
	runGit(t, root, "checkout", "topic")

	freshness := CheckFreshness("topic", "main", root)
	if freshness.Kind != FreshnessStale || freshness.CommitsBehind != 1 {
		t.Fatalf("unexpected freshness: %#v", freshness)
	}
	if action := ApplyPolicy(freshness, PolicyAutoRebase); action.Kind != ActionRebase {
		t.Fatalf("expected rebase action, got %#v", action)
	}
	if action := ApplyPolicy(freshness, PolicyWarnOnly); action.Kind != ActionWarn || !strings.Contains(action.Message, "1 commit(s) behind main") {
		t.Fatalf("expected warn action, got %#v", action)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}
