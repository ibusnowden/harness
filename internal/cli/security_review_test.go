package cli

import (
	"os"
	"os/exec"
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

func TestFuzzCommandAndSlashRun(t *testing.T) {
	root := copySecurityFixture(t, "fuzz_go")
	if code, stdout, stderr := runCLI(t, root, "fuzz", "--format", "json", "--budget", "1s"); code != 0 || !strings.Contains(stdout, `"workflow": "fuzz"`) || !strings.Contains(stdout, `"crash_reproducer"`) {
		t.Fatalf("fuzz failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "/fuzz", "--format", "markdown", "--budget", "1s"); code != 0 || !strings.Contains(stdout, "# Security Review") || !strings.Contains(stdout, "Go fuzz target crashes") {
		t.Fatalf("/fuzz failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCrashTriageCommandAndSlashRun(t *testing.T) {
	root := copySecurityFixture(t, "exec_crash")
	binaryPath := filepath.Join(t.TempDir(), "crasher")
	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/crasher")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(root, ".cache", "go-build"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build crash fixture: %v\n%s", err, string(output))
	}
	if code, stdout, stderr := runCLI(t, root, "crash-triage", "--format", "json", "--target-cmd", binaryPath+" {{input}}", "--corpus", "corpus"); code != 0 || !strings.Contains(stdout, `"workflow": "binary"`) || !strings.Contains(stdout, `"target_type": "local_binary"`) {
		t.Fatalf("crash-triage failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "/crash-triage", "--format", "markdown", "--target-cmd", binaryPath+" {{input}}", "--corpus", "corpus"); code != 0 || !strings.Contains(stdout, "Crash signature reproduced") {
		t.Fatalf("/crash-triage failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
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
