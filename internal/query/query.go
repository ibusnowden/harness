package query

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"ascaris/internal/commands"
	"ascaris/internal/manifest"
	"ascaris/internal/models"
	"ascaris/internal/sessions"
	"ascaris/internal/tools"
	"ascaris/internal/transcript"
)

type Config struct {
	MaxTurns             int
	MaxBudgetTokens      int
	CompactAfterTurns    int
	StructuredOutput     bool
	StructuredRetryLimit int
}

func DefaultConfig() Config {
	return Config{
		MaxTurns:             8,
		MaxBudgetTokens:      2000,
		CompactAfterTurns:    12,
		StructuredOutput:     false,
		StructuredRetryLimit: 2,
	}
}

type TurnResult struct {
	Prompt            string                    `json:"prompt"`
	Output            string                    `json:"output"`
	MatchedCommands   []string                  `json:"matched_commands"`
	MatchedTools      []string                  `json:"matched_tools"`
	PermissionDenials []models.PermissionDenial `json:"permission_denials"`
	Usage             models.UsageSummary       `json:"usage"`
	StopReason        string                    `json:"stop_reason"`
}

type Engine struct {
	Manifest          manifest.Manifest
	Config            Config
	SessionID         string
	Messages          []string
	PermissionDenials []models.PermissionDenial
	TotalUsage        models.UsageSummary
	Transcript        transcript.Store
	Root              string
}

func FromWorkspace(root string) (*Engine, error) {
	m, err := manifest.Build(root)
	if err != nil {
		return nil, err
	}
	return &Engine{
		Manifest:  m,
		Config:    DefaultConfig(),
		SessionID: newSessionID(),
		Root:      root,
	}, nil
}

func FromSavedSession(root, sessionID string) (*Engine, error) {
	stored, err := sessions.Load(sessionID, root)
	if err != nil {
		return nil, err
	}
	m, err := manifest.Build(root)
	if err != nil {
		return nil, err
	}
	return &Engine{
		Manifest:   m,
		Config:     DefaultConfig(),
		SessionID:  stored.SessionID,
		Messages:   append([]string(nil), stored.Messages...),
		TotalUsage: models.UsageSummary{InputTokens: stored.InputTokens, OutputTokens: stored.OutputTokens},
		Transcript: transcript.Store{Entries: append([]string(nil), stored.Messages...), Flushed: true},
		Root:       root,
	}, nil
}

func (e *Engine) SubmitMessage(prompt string, matchedCommands, matchedTools []string, deniedTools []models.PermissionDenial) TurnResult {
	if len(e.Messages) >= e.Config.MaxTurns {
		return TurnResult{
			Prompt:            prompt,
			Output:            "Max turns reached before processing prompt: " + prompt,
			MatchedCommands:   append([]string(nil), matchedCommands...),
			MatchedTools:      append([]string(nil), matchedTools...),
			PermissionDenials: append([]models.PermissionDenial(nil), deniedTools...),
			Usage:             e.TotalUsage,
			StopReason:        "max_turns_reached",
		}
	}
	summary := []string{
		"Prompt: " + prompt,
		"Matched commands: " + joinOrNone(matchedCommands),
		"Matched tools: " + joinOrNone(matchedTools),
		"Permission denials: " + strconv.Itoa(len(deniedTools)),
	}
	output := e.formatOutput(summary)
	projected := e.TotalUsage.AddTurn(prompt, output)
	stopReason := "completed"
	if projected.InputTokens+projected.OutputTokens > e.Config.MaxBudgetTokens {
		stopReason = "max_budget_reached"
	}
	e.Messages = append(e.Messages, prompt)
	e.Transcript.Append(prompt)
	e.PermissionDenials = append(e.PermissionDenials, deniedTools...)
	e.TotalUsage = projected
	e.compactIfNeeded()
	return TurnResult{
		Prompt:            prompt,
		Output:            output,
		MatchedCommands:   append([]string(nil), matchedCommands...),
		MatchedTools:      append([]string(nil), matchedTools...),
		PermissionDenials: append([]models.PermissionDenial(nil), deniedTools...),
		Usage:             e.TotalUsage,
		StopReason:        stopReason,
	}
}

func (e *Engine) StreamSubmitMessage(prompt string, matchedCommands, matchedTools []string, deniedTools []models.PermissionDenial) []map[string]any {
	events := []map[string]any{
		{"type": "message_start", "session_id": e.SessionID, "prompt": prompt},
	}
	if len(matchedCommands) > 0 {
		events = append(events, map[string]any{"type": "command_match", "commands": append([]string(nil), matchedCommands...)})
	}
	if len(matchedTools) > 0 {
		events = append(events, map[string]any{"type": "tool_match", "tools": append([]string(nil), matchedTools...)})
	}
	if len(deniedTools) > 0 {
		names := make([]string, 0, len(deniedTools))
		for _, denial := range deniedTools {
			names = append(names, denial.ToolName)
		}
		events = append(events, map[string]any{"type": "permission_denial", "denials": names})
	}
	result := e.SubmitMessage(prompt, matchedCommands, matchedTools, deniedTools)
	events = append(events,
		map[string]any{"type": "message_delta", "text": result.Output},
		map[string]any{
			"type":            "message_stop",
			"usage":           map[string]int{"input_tokens": result.Usage.InputTokens, "output_tokens": result.Usage.OutputTokens},
			"stop_reason":     result.StopReason,
			"transcript_size": len(e.Transcript.Entries),
		},
	)
	return events
}

func (e *Engine) PersistSession() (string, error) {
	e.Transcript.Flush()
	return sessions.Save(sessions.StoredSession{
		SessionID:    e.SessionID,
		Messages:     append([]string(nil), e.Messages...),
		InputTokens:  e.TotalUsage.InputTokens,
		OutputTokens: e.TotalUsage.OutputTokens,
	}, e.Root)
}

func (e *Engine) RenderSummary() string {
	commandBacklog, commandErr := commands.BacklogAtRoot(e.Root)
	if commandErr != nil {
		commandBacklog = commands.Backlog()
	}
	toolBacklog, toolErr := tools.BacklogAtRoot(e.Root, tools.CatalogOptions{IncludeMCP: true})
	if toolErr != nil {
		toolBacklog = tools.Backlog()
	}
	lines := []string{
		"# Ascaris Go Harness Summary",
		"",
		e.Manifest.Markdown(),
		"",
		"Command surface: " + strconv.Itoa(len(commandBacklog.Modules)) + " live entries",
	}
	lines = append(lines, commandBacklog.SummaryLines()[:min(10, len(commandBacklog.Modules))]...)
	lines = append(lines,
		"",
		"Tool surface: "+strconv.Itoa(len(toolBacklog.Modules))+" live entries",
	)
	lines = append(lines, toolBacklog.SummaryLines()[:min(10, len(toolBacklog.Modules))]...)
	lines = append(lines,
		"",
		"Session id: "+e.SessionID,
		"Conversation turns stored: "+strconv.Itoa(len(e.Messages)),
		"Permission denials tracked: "+strconv.Itoa(len(e.PermissionDenials)),
		"Usage totals: in="+strconv.Itoa(e.TotalUsage.InputTokens)+" out="+strconv.Itoa(e.TotalUsage.OutputTokens),
		"Max turns: "+strconv.Itoa(e.Config.MaxTurns),
		"Max budget tokens: "+strconv.Itoa(e.Config.MaxBudgetTokens),
		"Transcript flushed: "+strconv.FormatBool(e.Transcript.Flushed),
	)
	return strings.Join(lines, "\n")
}

func (e *Engine) formatOutput(summary []string) string {
	if !e.Config.StructuredOutput {
		return strings.Join(summary, "\n")
	}
	payload := map[string]any{
		"summary":    summary,
		"session_id": e.SessionID,
	}
	attempts := e.Config.StructuredRetryLimit
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		data, err := json.MarshalIndent(payload, "", "  ")
		if err == nil {
			return string(data)
		}
		lastErr = err
		payload = map[string]any{"summary": []string{"structured output retry"}, "session_id": e.SessionID}
	}
	errorMessage := "structured output unavailable"
	if lastErr != nil {
		errorMessage = lastErr.Error()
	}
	return "{\n  \"summary\": [\n    \"structured output unavailable\"\n  ],\n  \"session_id\": " + strconv.Quote(e.SessionID) + ",\n  \"error\": " + strconv.Quote(errorMessage) + "\n}"
}

func (e *Engine) compactIfNeeded() {
	if len(e.Messages) > e.Config.CompactAfterTurns {
		e.Messages = append([]string(nil), e.Messages[len(e.Messages)-e.Config.CompactAfterTurns:]...)
	}
	e.Transcript.Compact(e.Config.CompactAfterTurns)
}

func newSessionID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
