package mcp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"ascaris/internal/config"
)

func TestRegistryDiscoversAndCallsStdioMCPTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("python fixture is unix-only")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	script := filepath.Join(root, "server.py")
	if err := os.WriteFile(script, []byte(`import json, sys
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
    length = int(headers["content-length"])
    payload = sys.stdin.buffer.read(length)
    return json.loads(payload)
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
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"tools":[{"name":"echo","description":"Echo payload","inputSchema":{"type":"object"}}]}})
    elif method == "resources/list":
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"resources":[{"uri":"demo://readme","name":"readme","description":"demo","mime_type":"text/plain"}]}})
    elif method == "resources/read":
        uri = request["params"]["uri"]
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"contents":[{"uri":uri,"text":"resource body"}]}})
    elif method == "tools/call":
        args = request["params"].get("arguments") or {}
        write_frame({"jsonrpc":"2.0","id":request["id"],"result":{"content":[{"type":"text","text":args.get("value","")}],"isError":False}})
    else:
        write_frame({"jsonrpc":"2.0","id":request["id"],"error":{"code":-32601,"message":"unknown method"}})
`), 0o755); err != nil {
		t.Fatalf("write server script: %v", err)
	}
	server := config.McpServer{
		Name:      "demo server",
		Transport: config.McpTransportStdio,
		Command:   python,
		Args:      []string{script},
	}
	registry := NewRegistry(map[string]config.McpServer{"demo server": server, "remote": {
		Name:      "remote",
		Transport: config.McpTransportHTTP,
		URL:       "https://vendor.example/mcp",
	}})
	if err := registry.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}
	states := registry.States()
	if len(states) != 2 {
		t.Fatalf("unexpected states: %#v", states)
	}
	if states[0].ServerName != "demo server" || states[0].Status != StatusConnected {
		t.Fatalf("unexpected connected state: %#v", states[0])
	}
	if states[1].Status != StatusUnsupported {
		t.Fatalf("expected unsupported state: %#v", states[1])
	}
	output, err := registry.CallQualifiedTool(server.QualifiedToolName("echo"), json.RawMessage(`{"value":"hello from mcp"}`))
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if output != "hello from mcp" {
		t.Fatalf("unexpected tool output: %q", output)
	}
	resource, err := registry.ReadResource("demo server", "demo://readme")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if resource == "" {
		t.Fatalf("expected resource payload")
	}
}
