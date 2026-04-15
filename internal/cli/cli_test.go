package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ascaris/internal/commands"
	"ascaris/internal/manifest"
	"ascaris/internal/parity"
	"ascaris/internal/query"
	"ascaris/internal/tools"
)

func repoRoot() string {
	return filepath.Clean(filepath.Join("..", ".."))
}

func runCLI(t *testing.T, root string, args ...string) (int, string, string) {
	return runCLIWithInput(t, root, "", args...)
}

func runCLIWithInput(t *testing.T, root, input string, args ...string) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(Context{Root: root}, args, strings.NewReader(input), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestManifestCountsGoFiles(t *testing.T) {
	m, err := manifest.Build(repoRoot())
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	if m.TotalGoFiles < 20 {
		t.Fatalf("expected at least 20 go files, got %d", m.TotalGoFiles)
	}
	if len(m.TopLevelModule) == 0 {
		t.Fatalf("expected top-level modules")
	}
}

func TestSummaryMentionsWorkspace(t *testing.T) {
	engine, err := query.FromWorkspace(repoRoot())
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	summary := engine.RenderSummary()
	for _, fragment := range []string{"Ascaris Go Harness Summary", "Command surface:", "Tool surface:"} {
		if !strings.Contains(summary, fragment) {
			t.Fatalf("expected summary to contain %q", fragment)
		}
	}
}

func TestParityAuditCoverage(t *testing.T) {
	audit, err := parity.Run(repoRoot())
	if err != nil {
		t.Fatalf("run parity audit: %v", err)
	}
	if !strings.Contains(audit.Markdown(), "Traceability Audit") {
		t.Fatalf("expected traceability wording, got %q", audit.Markdown())
	}
	if audit.RootFileCoverage[0] != audit.RootFileCoverage[1] {
		t.Fatalf("expected full root file coverage, got %v", audit.RootFileCoverage)
	}
	if audit.DirectoryCoverage[0] != audit.DirectoryCoverage[1] {
		t.Fatalf("expected full directory coverage, got %v", audit.DirectoryCoverage)
	}
}

func TestCommandAndToolRegistriesAreNontrivial(t *testing.T) {
	if len(commands.MustModules()) < 10 {
		t.Fatalf("expected live command registry entries")
	}
	if len(tools.MustModules()) < 6 {
		t.Fatalf("expected live built-in tool entries")
	}
}

func TestCommandsToolsAndRoutingCommandsRun(t *testing.T) {
	root := repoRoot()
	if code, stdout, stderr := runCLI(t, root, "commands", "--limit", "5", "--query", "review"); code != 0 || !strings.Contains(stdout, "Command entries:") {
		t.Fatalf("commands query failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "tools", "--limit", "5", "--query", "read"); code != 0 || !strings.Contains(stdout, "Tool entries:") {
		t.Fatalf("tools query failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "route", "--limit", "5", "review MCP tool"); code != 0 || !strings.Contains(strings.ToLower(stdout), "review") {
		t.Fatalf("route failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestBootstrapAndTurnLoopCommandsRun(t *testing.T) {
	root := repoRoot()
	if code, stdout, stderr := runCLI(t, root, "bootstrap", "--limit", "5", "review MCP tool"); code != 0 || !strings.Contains(stdout, "Runtime Session") || strings.Contains(stdout, "Archive ") {
		t.Fatalf("bootstrap failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "turn-loop", "--max-turns", "2", "--structured-output", "review MCP tool"); code != 0 || !strings.Contains(stdout, "## Turn 1") {
		t.Fatalf("turn-loop failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestShowAndExecCommandsRun(t *testing.T) {
	root := repoRoot()
	if code, stdout, stderr := runCLI(t, root, "show-command", "review"); code != 0 || !strings.Contains(strings.ToLower(stdout), "review") {
		t.Fatalf("show-command failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "show-tool", "read_file"); code != 0 || !strings.Contains(strings.ToLower(stdout), "read_file") {
		t.Fatalf("show-tool failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "exec-command", "review", "inspect security review"); code != 0 || !strings.Contains(stdout, "Registered command") {
		t.Fatalf("exec-command failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "exec-tool", "read_file", `{"path":"README.md"}`); code != 0 || !strings.Contains(stdout, "Registered tool") {
		t.Fatalf("exec-tool failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestPromptFlushAndLoadSessionRun(t *testing.T) {
	root := repoRoot()
	t.Setenv("ASCARIS_CONFIG_HOME", filepath.Join(t.TempDir(), ".ascaris"))
	if code, stdout, stderr := runCLI(t, root, "prompt", "review MCP tool"); code != 0 || !strings.Contains(stdout, "Prompt: review MCP tool") {
		t.Fatalf("prompt failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr := runCLI(t, root, "flush-transcript", "review MCP tool")
	if code != 0 || !strings.Contains(stdout, "flushed=true") {
		t.Fatalf("flush-transcript failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) < 2 {
		t.Fatalf("unexpected flush output: %q", stdout)
	}
	sessionID := strings.TrimSuffix(filepath.Base(lines[0]), filepath.Ext(lines[0]))
	code, stdout, stderr = runCLI(t, root, "load-session", sessionID)
	if code != 0 || !strings.Contains(stdout, sessionID) || !strings.Contains(stdout, "messages") {
		t.Fatalf("load-session failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestModesAndJSONHealthCommandsRun(t *testing.T) {
	root := repoRoot()
	for _, args := range [][]string{
		{"remote-mode", "workspace"},
		{"ssh-mode", "workspace"},
		{"teleport-mode", "workspace"},
		{"direct-connect-mode", "workspace"},
		{"deep-link-mode", "workspace"},
	} {
		code, stdout, stderr := runCLI(t, root, args...)
		if code != 0 || !strings.Contains(stdout, "mode=") {
			t.Fatalf("%v failed: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	for _, args := range [][]string{{"status", "--json"}, {"doctor", "--json"}} {
		code, stdout, stderr := runCLI(t, root, args...)
		if code != 0 || !json.Valid([]byte(stdout)) {
			t.Fatalf("%v failed: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}

func TestSlashCommandsAndConfigCompatibilityRun(t *testing.T) {
	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	if err := os.MkdirAll(filepath.Join(root, ".ascaris"), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.MkdirAll(configHome, 0o755); err != nil {
		t.Fatalf("mkdir config home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "settings.json"), []byte(`{"model":"haiku"}`), 0o644); err != nil {
		t.Fatalf("write user config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ascaris.json"), []byte(`{"model":"sonnet"}`), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ascaris", "settings.local.json"), []byte(`{"model":"opus"}`), 0o644); err != nil {
		t.Fatalf("write local config: %v", err)
	}

	if code, stdout, stderr := runCLI(t, root, "/help"); code != 0 || !strings.Contains(stdout, "## Bug Finding") {
		t.Fatalf("/help failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "/config", "model"); code != 0 || !strings.Contains(stdout, "Loaded files      3") || !strings.Contains(stdout, "opus") {
		t.Fatalf("/config failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, _, stderr := runCLI(t, root, "/oh-my-claudecode:hud"); code == 0 || !strings.Contains(stderr, "legacy Claude Code/OMC plugin prefix") {
		t.Fatalf("expected OMC compatibility error, got code=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := runCLI(t, root, "/zstats"); code == 0 || !strings.Contains(stderr, "Did you mean /status?") {
		t.Fatalf("expected slash suggestion, got code=%d stderr=%q", code, stderr)
	}
}
