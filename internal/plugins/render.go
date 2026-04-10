package plugins

import (
	"encoding/json"
	"strings"
)

func RenderReport(plugins []Summary) string {
	lines := []string{"Plugins"}
	if len(plugins) == 0 {
		lines = append(lines, "  No plugins installed.")
		return strings.Join(lines, "\n")
	}
	for _, plugin := range plugins {
		status := "disabled"
		if plugin.Enabled {
			status = "enabled"
		}
		lines = append(lines, "  "+plugin.Metadata.Name+" v"+plugin.Metadata.Version+" "+status)
	}
	return strings.Join(lines, "\n")
}

func RenderInstallReport(pluginID string, plugin *Summary) string {
	name := pluginID
	version := "unknown"
	status := "disabled"
	if plugin != nil {
		name = plugin.Metadata.Name
		version = plugin.Metadata.Version
		if plugin.Enabled {
			status = "enabled"
		}
	}
	lines := []string{
		"Plugins",
		"  Result           installed " + pluginID,
		"  Name             " + name,
		"  Version          " + version,
		"  Status           " + status,
	}
	return strings.Join(lines, "\n")
}

func RenderActionReport(action string, plugin Summary) string {
	status := action
	lines := []string{
		"Plugins",
		"  Result           " + action + " " + plugin.Metadata.ID,
		"  Name             " + plugin.Metadata.Name,
		"  Version          " + plugin.Metadata.Version,
		"  Status           " + status,
	}
	return strings.Join(lines, "\n")
}

func RenderUpdateReport(update UpdateOutcome, plugin *Summary) string {
	name := update.PluginID
	status := "unknown"
	if plugin != nil {
		name = plugin.Metadata.Name
		if plugin.Enabled {
			status = "enabled"
		} else {
			status = "disabled"
		}
	}
	lines := []string{
		"Plugins",
		"  Result           updated " + update.PluginID,
		"  Name             " + name,
		"  Old version      " + update.OldVersion,
		"  New version      " + update.NewVersion,
		"  Status           " + status,
	}
	return strings.Join(lines, "\n")
}

func RenderJSON(kind string, payload any) string {
	root := map[string]any{
		"kind":    kind,
		"payload": payload,
	}
	data, _ := json.MarshalIndent(root, "", "  ")
	return string(data)
}
