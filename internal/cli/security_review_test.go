package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityReviewCommandAndSlashRun(t *testing.T) {
	root := copySecurityFixture(t, "vulnerable_go")
	if code, stdout, stderr := runCLI(t, root, "security-review", "--format", "json"); code != 0 || !strings.Contains(stdout, `"mode": "security-review"`) || !strings.Contains(stdout, `"CWE-295"`) {
		t.Fatalf("security-review failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "/security-review", "--format", "markdown"); code != 0 || !strings.Contains(stdout, "# Security Review") || !strings.Contains(stdout, "TLS verification disabled") {
		t.Fatalf("/security-review failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func copySecurityFixture(t *testing.T, name string) string {
	t.Helper()
	srcRoot := filepath.Join("..", "securityreview", "testdata", name)
	dstRoot := t.TempDir()
	if err := filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstRoot, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	}); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	return dstRoot
}
