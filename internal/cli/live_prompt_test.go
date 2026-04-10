package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ascaris/internal/api"
	"ascaris/internal/sessions"
	"ascaris/internal/testutil/mockanthropic"
)

func TestLivePromptParityScenarios(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(mockanthropic.NewTransport())
	defer restoreTransport()

	cases := []struct {
		name           string
		permissionMode string
		stdin          string
		prepare        func(t *testing.T, root string)
		extraArgs      []string
		assert         func(t *testing.T, root string, stdout string, response map[string]any)
	}{
		{
			name:           "streaming_text",
			permissionMode: "workspace-write",
			assert: func(t *testing.T, _ string, _ string, response map[string]any) {
				assertEqualString(t, response["message"], "Mock streaming says hello from the parity harness.")
				assertEqualFloat(t, response["iterations"], 1)
				assertArrayLen(t, response["tool_uses"], 0)
				assertArrayLen(t, response["tool_results"], 0)
			},
		},
		{
			name:           "read_file_roundtrip",
			permissionMode: "workspace-write",
			prepare: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, "fixture.txt"), []byte("alpha parity line\n"), 0o644); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
			},
			assert: func(t *testing.T, root string, _ string, response map[string]any) {
				assertEqualFloat(t, response["iterations"], 2)
				toolUses := expectArray(t, response["tool_uses"])
				assertEqualString(t, expectObject(t, toolUses[0])["name"], "read_file")
				assertEqualString(t, expectObject(t, toolUses[0])["input"], `{"path":"fixture.txt"}`)
				if !strings.Contains(expectString(t, response["message"]), "alpha parity line") {
					t.Fatalf("expected final message to include file content: %v", response["message"])
				}
				toolResults := expectArray(t, response["tool_results"])
				output := expectString(t, expectObject(t, toolResults[0])["output"])
				if !strings.Contains(output, filepath.Join(root, "fixture.txt")) || !strings.Contains(output, "alpha parity line") {
					t.Fatalf("unexpected tool output: %s", output)
				}
			},
		},
		{
			name:           "grep_chunk_assembly",
			permissionMode: "workspace-write",
			prepare: func(t *testing.T, root string) {
				data := "alpha parity line\nbeta line\ngamma parity line\n"
				if err := os.WriteFile(filepath.Join(root, "fixture.txt"), []byte(data), 0o644); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
			},
			assert: func(t *testing.T, _ string, _ string, response map[string]any) {
				assertEqualFloat(t, response["iterations"], 2)
				toolUses := expectArray(t, response["tool_uses"])
				assertEqualString(t, expectObject(t, toolUses[0])["name"], "grep_search")
				assertEqualString(t, expectObject(t, toolUses[0])["input"], `{"pattern":"parity","path":"fixture.txt","output_mode":"count"}`)
				if !strings.Contains(expectString(t, response["message"]), "2 occurrences") {
					t.Fatalf("expected grep count in final message: %v", response["message"])
				}
				toolResults := expectArray(t, response["tool_results"])
				if expectObject(t, toolResults[0])["is_error"] != false {
					t.Fatalf("expected grep tool result to succeed: %v", toolResults[0])
				}
			},
		},
		{
			name:           "write_file_allowed",
			permissionMode: "workspace-write",
			assert: func(t *testing.T, root string, _ string, response map[string]any) {
				assertEqualFloat(t, response["iterations"], 2)
				toolUses := expectArray(t, response["tool_uses"])
				assertEqualString(t, expectObject(t, toolUses[0])["name"], "write_file")
				generated := filepath.Join(root, "generated", "output.txt")
				data, err := os.ReadFile(generated)
				if err != nil {
					t.Fatalf("expected generated file: %v", err)
				}
				if string(data) != "created by mock service\n" {
					t.Fatalf("unexpected generated content: %q", string(data))
				}
				if !strings.Contains(expectString(t, response["message"]), "generated/output.txt") {
					t.Fatalf("expected generated path in message: %v", response["message"])
				}
			},
		},
		{
			name:           "write_file_denied",
			permissionMode: "read-only",
			assert: func(t *testing.T, root string, _ string, response map[string]any) {
				assertEqualFloat(t, response["iterations"], 2)
				toolResults := expectArray(t, response["tool_results"])
				output := expectString(t, expectObject(t, toolResults[0])["output"])
				if !strings.Contains(output, "requires workspace-write permission") {
					t.Fatalf("expected permission denial, got %q", output)
				}
				if expectObject(t, toolResults[0])["is_error"] != true {
					t.Fatalf("expected write_file denial to be marked as error")
				}
				if _, err := os.Stat(filepath.Join(root, "generated", "denied.txt")); !os.IsNotExist(err) {
					t.Fatalf("denied file should not exist")
				}
			},
		},
		{
			name:           "multi_tool_turn_roundtrip",
			permissionMode: "workspace-write",
			prepare: func(t *testing.T, root string) {
				data := "alpha parity line\nbeta line\ngamma parity line\n"
				if err := os.WriteFile(filepath.Join(root, "fixture.txt"), []byte(data), 0o644); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
			},
			assert: func(t *testing.T, _ string, _ string, response map[string]any) {
				assertEqualFloat(t, response["iterations"], 2)
				toolUses := expectArray(t, response["tool_uses"])
				if len(toolUses) != 2 {
					t.Fatalf("expected two tool uses, got %d", len(toolUses))
				}
				assertEqualString(t, expectObject(t, toolUses[0])["name"], "read_file")
				assertEqualString(t, expectObject(t, toolUses[1])["name"], "grep_search")
				if !strings.Contains(expectString(t, response["message"]), "alpha parity line") || !strings.Contains(expectString(t, response["message"]), "2 occurrences") {
					t.Fatalf("expected combined multi-tool summary: %v", response["message"])
				}
			},
		},
		{
			name:           "bash_stdout_roundtrip",
			permissionMode: "danger-full-access",
			assert: func(t *testing.T, _ string, _ string, response map[string]any) {
				assertEqualFloat(t, response["iterations"], 2)
				toolResults := expectArray(t, response["tool_results"])
				output := expectString(t, expectObject(t, toolResults[0])["output"])
				var payload map[string]any
				if err := json.Unmarshal([]byte(output), &payload); err != nil {
					t.Fatalf("parse bash output: %v", err)
				}
				assertEqualString(t, payload["stdout"], "alpha from bash")
				if !strings.Contains(expectString(t, response["message"]), "alpha from bash") {
					t.Fatalf("expected bash stdout in final message: %v", response["message"])
				}
			},
		},
		{
			name:           "bash_permission_prompt_approved",
			permissionMode: "workspace-write",
			stdin:          "y\n",
			assert: func(t *testing.T, _ string, stdout string, response map[string]any) {
				if !strings.Contains(stdout, "Permission approval required") || !strings.Contains(stdout, "Approve this tool call? [y/N]:") {
					t.Fatalf("expected approval prompt in stdout: %q", stdout)
				}
				assertEqualFloat(t, response["iterations"], 2)
				if !strings.Contains(expectString(t, response["message"]), "approved and executed") {
					t.Fatalf("expected approved execution message: %v", response["message"])
				}
			},
		},
		{
			name:           "bash_permission_prompt_denied",
			permissionMode: "workspace-write",
			stdin:          "n\n",
			assert: func(t *testing.T, _ string, stdout string, response map[string]any) {
				if !strings.Contains(stdout, "Permission approval required") || !strings.Contains(stdout, "Approve this tool call? [y/N]:") {
					t.Fatalf("expected approval prompt in stdout: %q", stdout)
				}
				assertEqualFloat(t, response["iterations"], 2)
				toolResults := expectArray(t, response["tool_results"])
				output := expectString(t, expectObject(t, toolResults[0])["output"])
				if !strings.Contains(output, "denied by user approval prompt") {
					t.Fatalf("expected denial message, got %q", output)
				}
			},
		},
		{
			name:           "auto_compact_triggered",
			permissionMode: "workspace-write",
			extraArgs:      []string{"--resume", "seed"},
			prepare: func(t *testing.T, root string) {
				session := sessions.NewManagedSession("seed", "sonnet")
				session.Meta.Usage = api.Usage{InputTokens: 60000}
				session.Messages = []api.InputMessage{
					api.UserTextMessage("step one of the parity scenario"),
					{Role: "assistant", Content: []api.InputContentBlock{{Type: "text", Text: "acknowledged step one"}}},
					api.UserTextMessage("step two of the parity scenario"),
					{Role: "assistant", Content: []api.InputContentBlock{{Type: "text", Text: "acknowledged step two"}}},
					api.UserTextMessage("step three of the parity scenario"),
					{Role: "assistant", Content: []api.InputContentBlock{{Type: "text", Text: "acknowledged step three"}}},
				}
				if _, err := sessions.SaveManaged(session, root); err != nil {
					t.Fatalf("save seeded session: %v", err)
				}
			},
			assert: func(t *testing.T, _ string, _ string, response map[string]any) {
				assertEqualFloat(t, response["iterations"], 1)
				if response["auto_compaction"] == nil {
					t.Fatalf("expected auto_compaction field to be present")
				}
				auto := expectObject(t, response["auto_compaction"])
				if auto["removed_messages"] == nil {
					t.Fatalf("expected removed_messages in auto_compaction: %v", auto)
				}
				if inputTokens := expectObject(t, response["usage"])["input_tokens"].(float64); inputTokens < 50000 {
					t.Fatalf("expected large input token count, got %v", inputTokens)
				}
			},
		},
		{
			name:           "token_cost_reporting",
			permissionMode: "workspace-write",
			assert: func(t *testing.T, _ string, _ string, response map[string]any) {
				assertEqualFloat(t, response["iterations"], 1)
				usage := expectObject(t, response["usage"])
				if usage["input_tokens"].(float64) <= 0 || usage["output_tokens"].(float64) <= 0 {
					t.Fatalf("expected non-zero usage: %v", usage)
				}
				if !strings.HasPrefix(expectString(t, response["estimated_cost"]), "$") {
					t.Fatalf("expected dollar-prefixed estimated cost: %v", response["estimated_cost"])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			configHome := filepath.Join(t.TempDir(), ".ascaris")
			t.Setenv("ANTHROPIC_API_KEY", "test-key")
			t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
			t.Setenv("ASCARIS_CONFIG_HOME", configHome)
			t.Setenv("ASCARIS_AUTO_COMPACT_INPUT_TOKENS", "50000")
			if tc.prepare != nil {
				tc.prepare(t, root)
			}
			args := []string{
				"--model", "sonnet",
				"--permission-mode", tc.permissionMode,
				"--output-format=json",
			}
			args = append(args, tc.extraArgs...)
			args = append(args, mockanthropic.ScenarioPrefix+tc.name)
			code, stdout, stderr := runCLIWithInput(t, root, tc.stdin, args...)
			if code != 0 {
				t.Fatalf("run failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			response := parseJSONOutput(t, stdout)
			tc.assert(t, root, stdout, response)
		})
	}
}

func TestLivePromptRecordsPromptCacheEvents(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(mockanthropic.NewTransport())
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	seed := func() {
		session := sessions.NewManagedSession("cache-seed", "sonnet")
		if _, err := sessions.SaveManaged(session, root); err != nil {
			t.Fatalf("save seed session: %v", err)
		}
	}
	seed()
	args := []string{
		"--model", "sonnet",
		"--output-format=json",
		"--resume", "cache-seed",
		mockanthropic.ScenarioPrefix + "streaming_text",
	}
	if code, stdout, stderr := runCLIWithInput(t, root, "", args...); code != 0 {
		t.Fatalf("first run failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	seed()
	code, stdout, stderr := runCLIWithInput(t, root, "", args...)
	if code != 0 {
		t.Fatalf("second run failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	response := parseJSONOutput(t, stdout)
	events := expectArray(t, response["prompt_cache_events"])
	if len(events) == 0 {
		t.Fatalf("expected prompt cache event on resumed replay")
	}
	if expectObject(t, events[0])["type"] != "prompt_cache_hit" {
		t.Fatalf("unexpected prompt cache event: %#v", events[0])
	}
}

func parseJSONOutput(t *testing.T, stdout string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err == nil {
			return payload
		}
	}
	t.Fatalf("no json payload found in stdout:\n%s", stdout)
	return nil
}

func expectArray(t *testing.T, value any) []any {
	t.Helper()
	values, ok := value.([]any)
	if !ok {
		t.Fatalf("expected array, got %T", value)
	}
	return values
}

func expectObject(t *testing.T, value any) map[string]any {
	t.Helper()
	obj, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", value)
	}
	return obj
}

func expectString(t *testing.T, value any) string {
	t.Helper()
	text, ok := value.(string)
	if !ok {
		t.Fatalf("expected string, got %T", value)
	}
	return text
}

func assertEqualString(t *testing.T, value any, want string) {
	t.Helper()
	if got := expectString(t, value); got != want {
		t.Fatalf("unexpected string: got %q want %q", got, want)
	}
}

func assertEqualFloat(t *testing.T, value any, want float64) {
	t.Helper()
	got, ok := value.(float64)
	if !ok {
		t.Fatalf("expected float64, got %T", value)
	}
	if got != want {
		t.Fatalf("unexpected number: got %v want %v", got, want)
	}
}

func assertArrayLen(t *testing.T, value any, want int) {
	t.Helper()
	if got := len(expectArray(t, value)); got != want {
		t.Fatalf("unexpected array length: got %d want %d", got, want)
	}
}
