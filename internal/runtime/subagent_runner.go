package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ascaris/internal/api"
	"ascaris/internal/config"
	"ascaris/internal/subagents"
	workerstate "ascaris/internal/state"
	"ascaris/internal/tools"
)

func (r *liveRuntime) runSubagentAssignment(ctx context.Context, assignment subagents.Assignment) (subagents.Assignment, error) {
	registry, err := subagents.LoadRegistry(r.root)
	if err != nil {
		return assignment, err
	}
	assignment, err = registry.MarkRunning(assignment.AssignmentID)
	if err != nil {
		return assignment, err
	}
	if err := subagents.SaveRegistry(r.root, registry); err != nil {
		return assignment, err
	}

	result, runErr := r.executeSubagent(ctx, assignment)
	registry, err = subagents.LoadRegistry(r.root)
	if err != nil {
		return assignment, err
	}
	if runErr != nil {
		assignment, _ = registry.Fail(assignment.AssignmentID, runErr, result.Usage)
		_ = subagents.SaveRegistry(r.root, registry)
		r.markSubagentWorkerComplete(assignment.WorkerID, "error", result.Usage.OutputTokens)
		return assignment, runErr
	}
	assignment, err = registry.Complete(assignment.AssignmentID, result)
	if err != nil {
		return assignment, err
	}
	if err := subagents.SaveRegistry(r.root, registry); err != nil {
		return assignment, err
	}
	r.markSubagentWorkerComplete(assignment.WorkerID, "stop", result.Usage.OutputTokens)
	return assignment, nil
}

func (r *liveRuntime) executeSubagent(ctx context.Context, assignment subagents.Assignment) (subagents.Result, error) {
	providerCfg := api.ProviderConfig{
		AnthropicBaseURL:  r.config.ProviderSettings().AnthropicBaseURL,
		GoogleBaseURL:     r.config.ProviderSettings().GoogleBaseURL,
		OpenAIBaseURL:     r.config.ProviderSettings().OpenAIBaseURL,
		OpenRouterBaseURL: r.config.ProviderSettings().OpenRouterBaseURL,
		PreferredProvider: r.options.Provider,
		XAIBaseURL:        r.config.ProviderSettings().XAIBaseURL,
		ProxyURL:          r.config.ProviderSettings().ProxyURL,
		ConfigHome:        config.ConfigHome(r.root),
		OAuthSettings:     r.config.OAuth(),
	}
	model := resolveModel(r.options.Model)
	route, err := api.ResolveModelRoute(model, providerCfg)
	if err != nil {
		return subagents.Result{}, err
	}
	client, err := api.NewProviderClient(model, providerCfg)
	if err != nil {
		return subagents.Result{}, err
	}

	allowedTools := assignment.AllowedTools
	if len(allowedTools) == 0 {
		allowedTools = []string{"read_file", "glob_search", "grep_search", "web_search", "web_fetch"}
	}
	messages := []api.InputMessage{api.UserTextMessage(subagentUserPrompt(assignment))}
	system := subagentSystemPrompt()
	usage := api.Usage{}
	inspected := []string{}
	changed := []string{}

	for iteration := 0; iteration < max(1, min(max(1, r.options.MaxIterations), 8)); iteration++ {
		request := api.MessageRequest{
			Model:     route.RequestModel,
			MaxTokens: max(256, r.options.MaxTokens),
			Messages:  append([]api.InputMessage(nil), messages...),
			System:    system,
			Tools:     r.Definitions(allowedTools),
			Stream:    true,
		}
		response, err := client.StreamMessageEvents(ctx, request, nil)
		if err != nil {
			return subagents.Result{Usage: usage}, err
		}
		usage = usage.Add(response.Usage)
		messages = append(messages, assistantMessageFromResponse(response))
		calls := collectToolCalls(response)
		if len(calls) == 0 {
			return subagents.Result{
				Summary:        strings.TrimSpace(response.FinalText()),
				InspectedFiles: uniqueStrings(inspected),
				ChangedFiles:   uniqueStrings(changed),
				Verification:   "subagent completed with final model response",
				Usage:          usage,
			}, nil
		}
		envelopes := make([]api.ToolResultEnvelope, 0, len(calls))
		for _, call := range calls {
			trackSubagentFileUse(call, &inspected, &changed)
			result := r.ExecuteTool(ctx, call)
			envelopes = append(envelopes, api.ToolResultEnvelope{
				ToolUseID: result.ToolUseID,
				Output:    result.Output,
				IsError:   result.IsError,
			})
		}
		messages = append(messages, api.ToolResultMessage(envelopes))
	}
	return subagents.Result{
		Summary:        "subagent stopped before final response",
		InspectedFiles: uniqueStrings(inspected),
		ChangedFiles:   uniqueStrings(changed),
		Verification:   "max subagent iterations reached",
		Usage:          usage,
	}, fmt.Errorf("subagent %s exceeded iteration limit", assignment.AssignmentID)
}

func (r *liveRuntime) markSubagentWorkerComplete(workerID, finishReason string, outputTokens int) {
	registry, err := workerstate.LoadWorkerRegistry(r.root)
	if err != nil {
		return
	}
	if _, err := registry.ObserveCompletion(workerID, finishReason, outputTokens); err != nil {
		return
	}
	_ = workerstate.SaveWorkerRegistry(r.root, registry)
}

func subagentSystemPrompt() string {
	return strings.Join([]string{
		"You are a scoped subagent.",
		"You only work on the assignment below.",
		"Do not assume access to prior conversation.",
		"Use only the tools available to you.",
		"Return a concise structured result with summary, inspected files, changed files, validation, and blockers.",
	}, "\n")
}

func subagentUserPrompt(assignment subagents.Assignment) string {
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

func trackSubagentFileUse(call tools.LiveCall, inspected, changed *[]string) {
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

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
