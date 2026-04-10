package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestManagerInstallsListsAndExecutesPluginTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is unix-only")
	}
	root := t.TempDir()
	configHome := filepath.Join(root, ".ascaris")
	source := filepath.Join(root, "demo-plugin")
	if err := os.MkdirAll(filepath.Join(source, ".ascaris-plugin"), 0o755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	toolScript := filepath.Join(source, "tool.sh")
	if err := os.WriteFile(toolScript, []byte("#!/bin/sh\ncat\n"), 0o755); err != nil {
		t.Fatalf("write tool script: %v", err)
	}
	manifest := Manifest{
		Name:           "Demo Plugin",
		Version:        "1.0.0",
		Description:    "Plugin for tests",
		DefaultEnabled: true,
		Hooks:          Hooks{PreToolUse: []string{"printf 'plugin-hook'"}},
		Tools: []ToolManifest{
			{
				Name:               "echo_plugin",
				Description:        "Echo stdin",
				InputSchema:        json.RawMessage(`{"type":"object"}`),
				Command:            toolScript,
				RequiredPermission: ToolPermissionWorkspaceWrite,
			},
		},
		Commands: []CommandManifest{{Name: "plugin-cmd", Description: "cmd", Command: "printf ok"}},
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(source, ManifestRelativePath), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	manager := NewManagerWithConfig(ManagerConfig{ConfigHome: configHome})
	install, err := manager.Install(source)
	if err != nil {
		t.Fatalf("install plugin: %v", err)
	}
	if !strings.HasPrefix(install.PluginID, "external/") {
		t.Fatalf("unexpected plugin id: %s", install.PluginID)
	}
	installed, err := manager.ListInstalledPlugins()
	if err != nil {
		t.Fatalf("list installed: %v", err)
	}
	if len(installed) != 1 || !installed[0].Enabled {
		t.Fatalf("unexpected installed plugins: %#v", installed)
	}
	tools, err := manager.AggregatedTools()
	if err != nil {
		t.Fatalf("aggregate tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo_plugin" {
		t.Fatalf("unexpected aggregated tools: %#v", tools)
	}
	output, err := manager.ExecuteTool(tools[0], json.RawMessage(`{"value":"hello"}`))
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	if output != `{"value":"hello"}` {
		t.Fatalf("unexpected tool output: %q", output)
	}
	if err := manager.Disable(install.PluginID); err != nil {
		t.Fatalf("disable plugin: %v", err)
	}
	reloaded := NewManagerWithConfig(ManagerConfig{ConfigHome: configHome})
	items, err := reloaded.ListInstalledPlugins()
	if err != nil {
		t.Fatalf("reload installed: %v", err)
	}
	if len(items) != 1 || items[0].Enabled {
		t.Fatalf("expected disabled plugin, got %#v", items)
	}
}
