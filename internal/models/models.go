package models

import "strings"

type Subsystem struct {
	Name      string
	Path      string
	FileCount int
	Notes     string
}

type PortingModule struct {
	Name           string `json:"name"`
	Responsibility string `json:"responsibility"`
	SourceHint     string `json:"source_hint"`
	Status         string `json:"status"`
}

type PermissionDenial struct {
	ToolName string `json:"tool_name"`
	Reason   string `json:"reason"`
}

type UsageSummary struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (u UsageSummary) AddTurn(prompt, output string) UsageSummary {
	return UsageSummary{
		InputTokens:  u.InputTokens + len(strings.Fields(prompt)),
		OutputTokens: u.OutputTokens + len(strings.Fields(output)),
	}
}

type PortingBacklog struct {
	Title   string
	Modules []PortingModule
}

func (b PortingBacklog) SummaryLines() []string {
	lines := make([]string, 0, len(b.Modules))
	for _, module := range b.Modules {
		lines = append(lines, "- "+module.Name+" ["+module.Status+"] - "+module.Responsibility+" (from "+module.SourceHint+")")
	}
	return lines
}
