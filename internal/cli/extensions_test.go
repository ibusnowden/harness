package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ascaris/internal/api"
	"ascaris/internal/config"
	"ascaris/internal/plugins"
	workerstate "ascaris/internal/state"
	"ascaris/internal/testutil/mockanthropic"
)

func TestDirectCommandsForAgentsSkillsPluginsMCPStateAndMigration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture scripts are unix-only")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	for _, dir := range []string{
		filepath.Join(root, ".ascaris", "skills", "plan"),
		filepath.Join(root, ".ascaris", "agents"),
		filepath.Join(root, ".claw"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".ascaris", "skills", "plan", "SKILL.md"), []byte("---\nname: plan\ndescription: local plan\n---\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ascaris", "agents", "planner.toml"), []byte("name = \"planner\"\n"), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claw.json"), []byte(`{"model":"sonnet"}`), 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	if err := workerstate.Save(root, workerstate.Snapshot{Workers: []workerstate.Worker{{WorkerID: "worker_1", CWD: root, Status: workerstate.WorkerFinished}}}); err != nil {
		t.Fatalf("save worker state: %v", err)
	}

	pluginSource := filepath.Join(root, "plugin-src")
	if err := os.MkdirAll(filepath.Join(pluginSource, ".ascaris-plugin"), 0o755); err != nil {
		t.Fatalf("mkdir plugin source: %v", err)
	}
	pluginScript := filepath.Join(pluginSource, "echo.sh")
	if err := os.WriteFile(pluginScript, []byte("#!/bin/sh\ncat\n"), 0o755); err != nil {
		t.Fatalf("write plugin script: %v", err)
	}
	pluginManifest := []byte(fmt.Sprintf(`{"name":"Demo","version":"1.0.0","description":"demo","permissions":["read"],"defaultEnabled":true,"tools":[{"name":"echo_plugin","description":"Echo","inputSchema":{"type":"object"},"command":%q,"required_permission":"workspace-write"}]}`, pluginScript))
	if err := os.WriteFile(filepath.Join(pluginSource, ".ascaris-plugin", "plugin.json"), pluginManifest, 0o644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}

	mcpScript := filepath.Join(root, "mcp.py")
	if err := os.WriteFile(mcpScript, []byte(`import json, sys
def read_frame():
    headers = {}
    while True:
        line = sys.stdin.buffer.readline()
        if not line:
            return None
        if line == b"\r\n":
            break
        name, value = line.decode().split(":", 1)
        headers[name.strip().lower()] = value.strip()
    payload = sys.stdin.buffer.read(int(headers["content-length"]))
    return json.loads(payload)
def write_frame(payload):
    body = json.dumps(payload).encode()
    sys.stdout.buffer.write(f"Content-Length: {len(body)}\r\n\r\n".encode() + body)
    sys.stdout.buffer.flush()
while True:
    request = read_frame()
    if request is None:
        break
    if request["method"] == "initialize":
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"serverInfo":{"name":"demo","version":"1.0"}}})
    elif request["method"] == "tools/list":
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"tools":[{"name":"echo","description":"Echo","inputSchema":{"type":"object"}}]}})
    elif request["method"] == "resources/list":
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"resources":[]}})
    elif request["method"] == "tools/call":
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"content":[{"type":"text","text":"ok"}],"isError":False}})
    else:
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"contents":[]}})
`), 0o755); err != nil {
		t.Fatalf("write mcp script: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".ascaris"), 0o755); err != nil {
		t.Fatalf("mkdir ascaris config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ascaris", "settings.json"), []byte(fmt.Sprintf(`{"mcpServers":{"demo":{"command":%q,"args":[%q]}}}`, python, mcpScript)), 0o644); err != nil {
		t.Fatalf("write mcp config: %v", err)
	}

	if code, stdout, stderr := runCLI(t, root, "agents"); code != 0 || !strings.Contains(stdout, "planner") {
		t.Fatalf("agents failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "skills"); code != 0 || !strings.Contains(stdout, "plan") {
		t.Fatalf("skills failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "team", "create", "reviewers", "task_1", "task_2"); code != 0 || !strings.Contains(stdout, "reviewers") {
		t.Fatalf("team create failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "cron", "add", "@daily", "summarize"); code != 0 || !strings.Contains(stdout, "@daily") {
		t.Fatalf("cron add failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "worker", "create", "."); code != 0 || !strings.Contains(stdout, "worker_") {
		t.Fatalf("worker create failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "plugins", "install", pluginSource); code != 0 || !strings.Contains(stdout, "installed") {
		t.Fatalf("plugins install failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "plugins"); code != 0 || !strings.Contains(stdout, "Demo") {
		t.Fatalf("plugins list failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "mcp"); code != 0 || !strings.Contains(stdout, "demo") {
		t.Fatalf("mcp failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "state"); code != 0 || !strings.Contains(stdout, "worker_1") {
		t.Fatalf("state failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "/team", "list"); code != 0 || !strings.Contains(stdout, "reviewers") {
		t.Fatalf("/team failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "/cron", "list"); code != 0 || !strings.Contains(stdout, "@daily") {
		t.Fatalf("/cron failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "/worker", "list"); code != 0 || !strings.Contains(stdout, "worker_") {
		t.Fatalf("/worker failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runCLI(t, root, "migrate", "legacy"); code != 0 || !strings.Contains(stdout, ".ascaris.json") {
		t.Fatalf("migrate failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestLivePromptSupportsPluginAndMCPToolsAndPersistsWorkerState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture scripts are unix-only")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}

	t.Run("plugin tool", func(t *testing.T) {
		root := t.TempDir()
		configHome := filepath.Join(t.TempDir(), ".ascaris")
		t.Setenv("ANTHROPIC_API_KEY", "test-key")
		t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
		t.Setenv("ASCARIS_CONFIG_HOME", configHome)

		restoreTransport := api.SetTransportForTesting(scriptedToolTransport{
			toolName:  "echo_plugin",
			toolInput: `{"value":"plugin says hi"}`,
			finalText: "plugin roundtrip complete",
		})
		defer restoreTransport()

		pluginSource := filepath.Join(root, "plugin-src")
		if err := os.MkdirAll(filepath.Join(pluginSource, ".ascaris-plugin"), 0o755); err != nil {
			t.Fatalf("mkdir plugin source: %v", err)
		}
		script := filepath.Join(pluginSource, "echo.sh")
		if err := os.WriteFile(script, []byte("#!/bin/sh\ncat\n"), 0o755); err != nil {
			t.Fatalf("write plugin script: %v", err)
		}
		manifest := []byte(fmt.Sprintf(`{"name":"Demo","version":"1.0.0","description":"demo","permissions":["read"],"defaultEnabled":true,"tools":[{"name":"echo_plugin","description":"Echo","inputSchema":{"type":"object"},"command":%q,"required_permission":"workspace-write"}]}`, script))
		if err := os.WriteFile(filepath.Join(pluginSource, ".ascaris-plugin", "plugin.json"), manifest, 0o644); err != nil {
			t.Fatalf("write plugin manifest: %v", err)
		}
		manager := plugins.NewManagerWithConfig(plugins.ManagerConfig{ConfigHome: configHome})
		if _, err := manager.Install(pluginSource); err != nil {
			t.Fatalf("install plugin: %v", err)
		}

		code, stdout, stderr := runCLI(t, root, "--model", "sonnet", "--output-format=json", "--permission-mode", "workspace-write", "prompt", "plugin roundtrip")
		if code != 0 {
			t.Fatalf("prompt failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		response := parseJSONOutput(t, stdout)
		if !strings.Contains(expectString(t, response["message"]), "plugin says hi") {
			t.Fatalf("expected plugin output in message: %v", response["message"])
		}
		snapshot, err := workerstate.Load(root)
		if err != nil || len(snapshot.Workers) == 0 || snapshot.Workers[0].Status != workerstate.WorkerFinished {
			t.Fatalf("unexpected worker snapshot: %#v err=%v", snapshot, err)
		}
	})

	t.Run("mcp tool", func(t *testing.T) {
		root := t.TempDir()
		configHome := filepath.Join(t.TempDir(), ".ascaris")
		t.Setenv("ANTHROPIC_API_KEY", "test-key")
		t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
		t.Setenv("ASCARIS_CONFIG_HOME", configHome)

		mcpScript := filepath.Join(root, "mcp.py")
		if err := os.WriteFile(mcpScript, []byte(`import json, sys
def read_frame():
    headers = {}
    while True:
        line = sys.stdin.buffer.readline()
        if not line:
            return None
        if line == b"\r\n":
            break
        name, value = line.decode().split(":", 1)
        headers[name.strip().lower()] = value.strip()
    return json.loads(sys.stdin.buffer.read(int(headers["content-length"])))
def write_frame(payload):
    body = json.dumps(payload).encode()
    sys.stdout.buffer.write(f"Content-Length: {len(body)}\r\n\r\n".encode() + body)
    sys.stdout.buffer.flush()
while True:
    request = read_frame()
    if request is None:
        break
    method = request["method"]
    if method == "initialize":
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"serverInfo":{"name":"demo","version":"1.0"}}})
    elif method == "tools/list":
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"tools":[{"name":"echo","description":"Echo","inputSchema":{"type":"object"}}]}})
    elif method == "resources/list":
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"resources":[]}})
    elif method == "tools/call":
        value = (request["params"].get("arguments") or {}).get("value","")
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"content":[{"type":"text","text":value}],"isError":False}})
    else:
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"contents":[]}})
`), 0o755); err != nil {
			t.Fatalf("write mcp script: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(root, ".ascaris"), 0o755); err != nil {
			t.Fatalf("mkdir config dir: %v", err)
		}
		server := config.McpServer{Name: "demo", Transport: config.McpTransportStdio, Command: python, Args: []string{mcpScript}}
		if err := os.WriteFile(filepath.Join(root, ".ascaris", "settings.json"), []byte(fmt.Sprintf(`{"mcpServers":{"demo":{"command":%q,"args":[%q]}}}`, python, mcpScript)), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		restoreTransport := api.SetTransportForTesting(scriptedToolTransport{
			toolName:  server.QualifiedToolName("echo"),
			toolInput: `{"value":"hello from mcp"}`,
			finalText: "mcp roundtrip complete",
		})
		defer restoreTransport()
		code, stdout, stderr := runCLI(t, root, "--model", "sonnet", "--output-format=json", "--permission-mode", "danger-full-access", "prompt", "mcp roundtrip")
		if code != 0 {
			t.Fatalf("prompt failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		response := parseJSONOutput(t, stdout)
		if !strings.Contains(expectString(t, response["message"]), "hello from mcp") {
			t.Fatalf("expected MCP output in message: %v", response["message"])
		}
	})

	t.Run("worker state with plain prompt", func(t *testing.T) {
		restoreTransport := api.SetTransportForTesting(mockanthropic.NewTransport())
		defer restoreTransport()
		root := t.TempDir()
		configHome := filepath.Join(t.TempDir(), ".ascaris")
		t.Setenv("ANTHROPIC_API_KEY", "test-key")
		t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
		t.Setenv("ASCARIS_CONFIG_HOME", configHome)
		code, stdout, stderr := runCLI(t, root, "--model", "sonnet", "--output-format=json", "--permission-mode", "workspace-write", "prompt", mockanthropic.ScenarioPrefix+"streaming_text")
		if code != 0 {
			t.Fatalf("prompt failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		_ = stdout
		snapshot, err := workerstate.Load(root)
		if err != nil {
			t.Fatalf("load worker state: %v", err)
		}
		if len(snapshot.Workers) != 1 || snapshot.Workers[0].Status != workerstate.WorkerFinished {
			t.Fatalf("unexpected worker state: %#v", snapshot)
		}
	})
}

type scriptedToolTransport struct {
	toolName  string
	toolInput string
	finalText string
}

func (t scriptedToolTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var payload api.MessageRequest
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		return textResponse(http.StatusBadRequest, err.Error()), nil
	}
	if output, _ := latestToolResultForTest(payload); output != "" {
		return sseResponse(finalTextSSEForTest(t.finalText + ": " + output)), nil
	}
	return sseResponse(toolUseSSEForTest(t.toolName, t.toolInput)), nil
}

func textResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func sseResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func latestToolResultForTest(request api.MessageRequest) (string, bool) {
	for i := len(request.Messages) - 1; i >= 0; i-- {
		for j := len(request.Messages[i].Content) - 1; j >= 0; j-- {
			block := request.Messages[i].Content[j]
			if block.Type != "tool_result" {
				continue
			}
			var parts []string
			for _, content := range block.Content {
				if content.Type == "text" {
					parts = append(parts, content.Text)
				}
			}
			return strings.Join(parts, "\n"), block.IsError
		}
	}
	return "", false
}

func finalTextSSEForTest(text string) string {
	return strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","usage":{"input_tokens":11,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + strconvQuote(text) + `}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":11,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":8}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
}

func toolUseSSEForTest(name, input string) string {
	return strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_ext","name":` + strconvQuote(name) + `,"input":{}}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + strconvQuote(input) + `}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":4}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
}

func strconvQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
