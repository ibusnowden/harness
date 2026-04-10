package mcp

import (
	"encoding/json"
	"strconv"
	"strings"
)

func RenderSummary(states []ServerState) string {
	lines := []string{"MCP"}
	if len(states) == 0 {
		lines = append(lines, "  No MCP servers configured.")
		return strings.Join(lines, "\n")
	}
	for _, state := range states {
		line := "  " + state.ServerName + " · " + string(state.Status) + " · " + string(state.Transport)
		if state.ErrorMessage != "" {
			line += " · " + state.ErrorMessage
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func RenderSummaryJSON(states []ServerState) string {
	data, _ := json.MarshalIndent(map[string]any{
		"kind":    "mcp",
		"action":  "list",
		"servers": states,
	}, "", "  ")
	return string(data)
}

func RenderServer(state *ServerState, name string) string {
	if state == nil {
		return "MCP\n  Server not found: " + name
	}
	lines := []string{
		"MCP",
		"  Name             " + state.ServerName,
		"  Status           " + string(state.Status),
		"  Transport        " + string(state.Transport),
	}
	if state.Signature != "" {
		lines = append(lines, "  Signature        "+state.Signature)
	}
	if state.ConfigHash != "" {
		lines = append(lines, "  Config hash      "+state.ConfigHash)
	}
	if state.ServerInfo != "" {
		lines = append(lines, "  Server info      "+state.ServerInfo)
	}
	if state.ErrorMessage != "" {
		lines = append(lines, "  Error            "+state.ErrorMessage)
	}
	lines = append(lines, "  Tools            "+strconv.Itoa(len(state.Tools)), "  Resources        "+strconv.Itoa(len(state.Resources)))
	return strings.Join(lines, "\n")
}

func RenderServerJSON(state *ServerState, name string) string {
	data, _ := json.MarshalIndent(map[string]any{
		"kind":   "mcp",
		"action": "show",
		"name":   name,
		"server": state,
	}, "", "  ")
	return string(data)
}
