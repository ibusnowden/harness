package subagents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"ascaris/internal/api"
	"ascaris/internal/config"
	"ascaris/internal/contextbudget"
)

type Runner struct {
	Root          string
	Config        config.RuntimeConfig
	Provider      api.ProviderConfig
	Model         string
	MaxIterations int
	MaxTokens     int
	Definitions   func([]string) []api.ToolDefinition
	ExecuteTool   func(context.Context, ToolCall, int, []string) ToolResult
	OnComplete    func(string, string, int)
}

type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type ToolResult struct {
	ToolUseID string
	Name      string
	Output    string
	IsError   bool
}

func (r Runner) Run(ctx context.Context, assignment Assignment) (Assignment, error) {
	registry, err := LoadRegistry(r.Root)
	if err != nil {
		return assignment, err
	}
	assignment, err = registry.MarkRunning(assignment.AssignmentID)
	if err != nil {
		return assignment, err
	}
	if err := SaveRegistry(r.Root, registry); err != nil {
		return assignment, err
	}

	result, runErr := r.runAssignment(ctx, assignment)

	registry, err = LoadRegistry(r.Root)
	if err != nil {
		return assignment, err
	}
	if runErr != nil {
		assignment, _ = registry.Fail(assignment.AssignmentID, runErr, result.TokenUsage)
		_ = SaveRegistry(r.Root, registry)
		r.markComplete(assignment.WorkerID, "error", result.TokenUsage.OutputTokens)
		return assignment, runErr
	}
	assignment, err = registry.Complete(assignment.AssignmentID, result)
	if err != nil {
		return assignment, err
	}
	if err := SaveRegistry(r.Root, registry); err != nil {
		return assignment, err
	}
	r.markComplete(assignment.WorkerID, "stop", result.TokenUsage.OutputTokens)
	return assignment, nil
}

func (r Runner) runAssignment(ctx context.Context, assignment Assignment) (Result, error) {
	model := strings.TrimSpace(resolveRunnerModel(r.Model, r.Config.Model()))
	if model == "" {
		return Result{}, fmt.Errorf("subagent runner model is not configured")
	}
	route, err := api.ResolveModelRoute(model, r.Provider)
	if err != nil {
		return Result{}, err
	}
	client, err := api.NewProviderClient(model, r.Provider)
	if err != nil {
		return Result{}, err
	}

	allowedTools := cleanList(assignment.AllowedTools)
	if len(allowedTools) == 0 {
		allowedTools = []string{"read_file", "glob_search", "grep_search", "web_search", "web_fetch"}
	}
	messages := []api.InputMessage{api.UserTextMessage(subagentUserPrompt(assignment))}
	tokenUsage := api.Usage{}
	inspectedFiles := []string{}
	changedFiles := []string{}
	maxIterations := max(1, r.MaxIterations)
	if maxIterations > 8 {
		maxIterations = 8
	}
	maxTokens := max(256, r.MaxTokens)

	system := subagentSystemPrompt()
	toolDefs := r.toolDefinitions(allowedTools)

	for iteration := 0; iteration < maxIterations; iteration++ {
		if compacted, _, triggered := contextbudget.CompactSubagentMessages(messages, system, toolDefs, route.RequestModel, maxTokens); triggered {
			messages = compacted
		}
		request := api.MessageRequest{
			Model:     route.RequestModel,
			MaxTokens: maxTokens,
			Messages:  append([]api.InputMessage(nil), messages...),
			System:    system,
			Tools:     toolDefs,
			Stream:    true,
		}
		response, err := client.StreamMessageEvents(ctx, request, nil)
		if err != nil {
			if _, ok := contextbudget.ParseServedContextLimitError(err); ok {
				retryMessages, _, retried := contextbudget.CompactSubagentMessages(messages, system, toolDefs, route.RequestModel, maxTokens)
				if retried {
					messages = retryMessages
					request.Messages = append([]api.InputMessage(nil), messages...)
					response, err = client.StreamMessageEvents(ctx, request, nil)
				}
			}
			if err != nil {
				return Result{TokenUsage: tokenUsage}, err
			}
		}
		tokenUsage = tokenUsage.Add(response.Usage)
		messages = append(messages, assistantMessageFromResponse(response))
		calls := collectToolCalls(response)
		if len(calls) == 0 {
			parsed, err := parseStructuredResult(response.FinalText())
			if err != nil {
				return Result{TokenUsage: tokenUsage}, fmt.Errorf("invalid subagent result: %w", err)
			}
			return Result{
				Summary:        parsed.Summary,
				InspectedFiles: unionStrings(inspectedFiles, parsed.InspectedFiles),
				ChangedFiles:   unionStrings(changedFiles, parsed.ChangedFiles),
				Blockers:       cleanList(parsed.Blockers),
				Verification:   parsed.Verification,
				TokenUsage:     tokenUsage,
			}, nil
		}
		envelopes := make([]api.ToolResultEnvelope, 0, len(calls))
		for _, call := range calls {
			trackSubagentFileUse(call, &inspectedFiles, &changedFiles)
			result := r.executeTool(ctx, call, iteration+1, allowedTools)
			envelopes = append(envelopes, api.ToolResultEnvelope{
				ToolUseID: result.ToolUseID,
				Output:    api.TruncateToolOutput(result.Output, api.MaxToolOutputChars),
				IsError:   result.IsError,
			})
		}
		messages = append(messages, api.ToolResultMessage(envelopes))
	}
	return Result{
		Summary:        "subagent stopped before final response",
		InspectedFiles: cleanList(inspectedFiles),
		ChangedFiles:   cleanList(changedFiles),
		Blockers:       []string{"max subagent iterations reached"},
		Verification:   "max subagent iterations reached",
		TokenUsage:     tokenUsage,
	}, fmt.Errorf("subagent %s exceeded iteration limit", assignment.AssignmentID)
}

func (r Runner) toolDefinitions(allowedTools []string) []api.ToolDefinition {
	if r.Definitions != nil {
		return r.Definitions(allowedTools)
	}
	return nil
}

func (r Runner) executeTool(ctx context.Context, call ToolCall, iteration int, allowedTools []string) ToolResult {
	if r.ExecuteTool != nil {
		return r.ExecuteTool(ctx, call, iteration, allowedTools)
	}
	return ToolResult{
		ToolUseID: call.ID,
		Name:      call.Name,
		Output:    "subagent runner has no tool executor",
		IsError:   true,
	}
}

func (r Runner) markComplete(workerID, finishReason string, outputTokens int) {
	if r.OnComplete == nil || strings.TrimSpace(workerID) == "" {
		return
	}
	r.OnComplete(workerID, finishReason, outputTokens)
}

type structuredResult struct {
	Summary        string   `json:"summary"`
	Verification   string   `json:"verification"`
	Blockers       []string `json:"blockers"`
	InspectedFiles []string `json:"inspected_files,omitempty"`
	ChangedFiles   []string `json:"changed_files,omitempty"`
}

func parseStructuredResult(raw string) (structuredResult, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return structuredResult{}, fmt.Errorf("final response is empty")
	}
	var required map[string]json.RawMessage
	if err := decodeStrictJSON(text, &required); err != nil {
		return structuredResult{}, err
	}
	for _, key := range []string{"summary", "verification", "blockers"} {
		if _, ok := required[key]; !ok {
			return structuredResult{}, fmt.Errorf("missing required field %q", key)
		}
	}
	var parsed structuredResult
	if err := decodeStrictJSON(text, &parsed); err != nil {
		return structuredResult{}, err
	}
	if strings.TrimSpace(parsed.Summary) == "" {
		return structuredResult{}, fmt.Errorf("summary is required")
	}
	if strings.TrimSpace(parsed.Verification) == "" {
		return structuredResult{}, fmt.Errorf("verification is required")
	}
	parsed.Blockers = cleanList(parsed.Blockers)
	parsed.InspectedFiles = cleanList(parsed.InspectedFiles)
	parsed.ChangedFiles = cleanList(parsed.ChangedFiles)
	return parsed, nil
}

func decodeStrictJSON(text string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return fmt.Errorf("unexpected trailing JSON content")
	}
	var extra any
	if err := decoder.Decode(&extra); err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	return fmt.Errorf("unexpected trailing JSON content")
}

func assistantMessageFromResponse(response api.MessageResponse) api.InputMessage {
	content := make([]api.InputContentBlock, 0, len(response.Content))
	for _, block := range response.Content {
		entry := api.InputContentBlock{
			Type:      block.Type,
			Text:      block.Text,
			ID:        block.ID,
			Name:      block.Name,
			Input:     cloneRawJSON(block.Input),
			ToolUseID: block.ID,
		}
		if block.Type == "thinking" && strings.TrimSpace(block.Thinking) != "" {
			entry.Text = block.Thinking
		}
		content = append(content, entry)
	}
	return api.InputMessage{Role: "assistant", Content: content}
}

func collectToolCalls(response api.MessageResponse) []ToolCall {
	calls := make([]ToolCall, 0)
	for _, block := range response.Content {
		if block.Type != "tool_use" || strings.TrimSpace(block.Name) == "" {
			continue
		}
		calls = append(calls, ToolCall{
			ID:    block.ID,
			Name:  block.Name,
			Input: cloneRawJSON(block.Input),
		})
	}
	return calls
}

func subagentSystemPrompt() string {
	return strings.Join([]string{
		"You are a scoped subagent.",
		"You only work on the assignment below.",
		"Do not assume access to prior conversation.",
		"Use only the tools available to you.",
		"Return ONLY valid JSON with keys: summary, verification, blockers, inspected_files, changed_files.",
		"summary and verification must be strings.",
		"blockers must be an array of strings and may be empty.",
		"inspected_files and changed_files are optional arrays of strings.",
	}, "\n")
}

func subagentUserPrompt(assignment Assignment) string {
	payload := map[string]any{
		"role":                assignment.Role,
		"prompt":              assignment.Prompt,
		"context":             assignment.Context,
		"acceptance_criteria": assignment.AcceptanceCriteria,
		"allowed_tools":       assignment.AllowedTools,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return "Subagent assignment:\n" + string(data)
}

func trackSubagentFileUse(call ToolCall, inspected, changed *[]string) {
	var input struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(call.Input, &input)
	if strings.TrimSpace(input.Path) == "" {
		return
	}
	switch call.Name {
	case "read_file", "grep_search":
		*inspected = append(*inspected, input.Path)
	case "write_file", "edit_file":
		*changed = append(*changed, input.Path)
	}
}

func unionStrings(left, right []string) []string {
	return uniqueStrings(append(append([]string{}, left...), right...))
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			out = append(out, trimmed)
		}
	}
	return out
}

func resolveRunnerModel(values ...string) string {
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "":
			continue
		case "sonnet":
			return "claude-sonnet-4-6"
		case "opus":
			return "claude-opus-4-6"
		case "haiku":
			return "claude-haiku-4-5"
		default:
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return json.RawMessage(out)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
