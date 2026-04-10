package pool

import (
	"strconv"
	"strings"

	"ascaris/internal/models"
	"ascaris/internal/permissions"
	"ascaris/internal/tools"
)

type ToolPool struct {
	Tools      []models.PortingModule
	SimpleMode bool
	IncludeMCP bool
}

func (p ToolPool) Markdown() string {
	lines := []string{
		"# Tool Pool",
		"",
		"Simple mode: " + strconv.FormatBool(p.SimpleMode),
		"Include MCP: " + strconv.FormatBool(p.IncludeMCP),
		"Tool count: " + strconv.Itoa(len(p.Tools)),
	}
	limit := 15
	if len(p.Tools) < limit {
		limit = len(p.Tools)
	}
	for _, tool := range p.Tools[:limit] {
		lines = append(lines, "- "+tool.Name+" - "+tool.SourceHint)
	}
	return strings.Join(lines, "\n")
}

func Assemble(simpleMode, includeMCP bool, permissionContext *permissions.ToolPermissionContext) ToolPool {
	return ToolPool{
		Tools:      tools.List(simpleMode, includeMCP, permissionContext),
		SimpleMode: simpleMode,
		IncludeMCP: includeMCP,
	}
}
