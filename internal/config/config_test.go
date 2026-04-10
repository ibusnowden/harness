package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesConfigPrecedence(t *testing.T) {
	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	if err := os.MkdirAll(filepath.Join(root, ".ascaris"), 0o755); err != nil {
		t.Fatalf("mkdir project config: %v", err)
	}
	if err := os.MkdirAll(configHome, 0o755); err != nil {
		t.Fatalf("mkdir config home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "settings.json"), []byte(`{"model":"haiku","permissions":{"defaultMode":"read-only"},"plugins":{"maxOutputTokens":12345}}`), 0o644); err != nil {
		t.Fatalf("write user settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ascaris.json"), []byte(`{"model":"sonnet"}`), 0o644); err != nil {
		t.Fatalf("write project legacy settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ascaris", "settings.local.json"), []byte(`{"model":"opus","permissions":{"defaultMode":"danger-full-access"}}`), 0o644); err != nil {
		t.Fatalf("write local settings: %v", err)
	}

	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got := loaded.Model(); got != "opus" {
		t.Fatalf("unexpected model: %q", got)
	}
	if got := loaded.PermissionMode(); got != "danger-full-access" {
		t.Fatalf("unexpected permission mode: %q", got)
	}
	if got := loaded.PluginSettings().MaxOutputTokens; got != 12345 {
		t.Fatalf("unexpected plugin max output tokens: %d", got)
	}
	if got := len(loaded.LoadedEntries()); got != 3 {
		t.Fatalf("unexpected loaded config count: %d", got)
	}
}

func TestLoadParsesHooksOAuthAndMCPSettings(t *testing.T) {
	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	if err := os.MkdirAll(filepath.Join(root, ".ascaris"), 0o755); err != nil {
		t.Fatalf("mkdir project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ascaris", "settings.json"), []byte(`{
  "hooks": {"PreToolUse": ["printf pre"], "PostToolUseFailure": ["printf fail"]},
  "enabledPlugins": {"external/demo": true},
  "plugins": {"externalDirectories": ["/tmp/plugins"]},
  "oauth": {"clientId": "client-1", "authorizeUrl": "https://console.test/auth", "tokenUrl": "https://console.test/token", "callbackPort": 4545, "scopes": ["repo:read"]},
  "mcpServers": {
    "demo": {"command": "python3", "args": ["server.py"], "toolCallTimeoutMs": 15000},
    "remote": {"type": "http", "url": "https://api.example/mcp", "headers": {"X-Test": "1"}, "oauth": {"clientId": "mcp-client"}}
  }
}`), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := loaded.Hooks().PreToolUse; len(got) != 1 || got[0] != "printf pre" {
		t.Fatalf("unexpected hooks: %#v", loaded.Hooks())
	}
	if got := loaded.EnabledPlugins()["external/demo"]; !got {
		t.Fatalf("expected enabled plugin override")
	}
	if oauth := loaded.OAuth(); oauth == nil || oauth.ClientID != "client-1" || oauth.CallbackPort != 4545 {
		t.Fatalf("unexpected oauth settings: %#v", oauth)
	}
	servers := loaded.MCPServers()
	if len(servers) != 2 {
		t.Fatalf("unexpected mcp server count: %#v", servers)
	}
	if servers["demo"].Transport != McpTransportStdio || servers["demo"].ToolCallTimeoutMS != 15000 {
		t.Fatalf("unexpected stdio server: %#v", servers["demo"])
	}
	if servers["remote"].Transport != McpTransportHTTP || servers["remote"].OAuth == nil || servers["remote"].OAuth.ClientID != "mcp-client" {
		t.Fatalf("unexpected remote server: %#v", servers["remote"])
	}
}
