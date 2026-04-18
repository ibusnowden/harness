package runtime

import (
	"context"
	"strings"

	"ascaris/internal/api"
	"ascaris/internal/config"
	workerstate "ascaris/internal/state"
	"ascaris/internal/subagents"
	"ascaris/internal/tools"
)

func (r *liveRuntime) runSubagentAssignment(ctx context.Context, assignment subagents.Assignment) (subagents.Assignment, error) {
	runner := subagents.Runner{
		Root:   r.root,
		Config: r.config,
		Provider: api.ProviderConfig{
			AnthropicBaseURL:  r.config.ProviderSettings().AnthropicBaseURL,
			GoogleBaseURL:     r.config.ProviderSettings().GoogleBaseURL,
			OpenAIBaseURL:     r.config.ProviderSettings().OpenAIBaseURL,
			OpenRouterBaseURL: r.config.ProviderSettings().OpenRouterBaseURL,
			PreferredProvider: r.options.Provider,
			XAIBaseURL:        r.config.ProviderSettings().XAIBaseURL,
			ProxyURL:          r.config.ProviderSettings().ProxyURL,
			ConfigHome:        config.ConfigHome(r.root),
			OAuthSettings:     r.config.OAuth(),
		},
		Model:         resolveModel(r.options.Model),
		MaxIterations: r.options.MaxIterations,
		MaxTokens:     r.options.MaxTokens,
		Definitions: func(allowedTools []string) []api.ToolDefinition {
			return r.Definitions(allowedTools)
		},
		ExecuteTool: func(runCtx context.Context, call subagents.ToolCall, iteration int, allowedTools []string) subagents.ToolResult {
			result := r.ExecuteToolWithAllowed(runCtx, tools.LiveCall{
				ID:    call.ID,
				Name:  call.Name,
				Input: call.Input,
			}, iteration, allowedTools)
			return subagents.ToolResult{
				ToolUseID: result.ToolUseID,
				Name:      result.Name,
				Output:    result.Output,
				IsError:   result.IsError,
			}
		},
		OnComplete: r.markSubagentWorkerComplete,
	}
	return runner.Run(ctx, assignment)
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

func subagentPromptForTask(taskTitle, taskGoal string, acceptanceCriteria []string) string {
	lines := []string{strings.TrimSpace(taskTitle)}
	if strings.TrimSpace(taskGoal) != "" {
		lines = append(lines, "", strings.TrimSpace(taskGoal))
	}
	if len(acceptanceCriteria) > 0 {
		lines = append(lines, "", "Acceptance criteria:")
		for _, item := range acceptanceCriteria {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				lines = append(lines, "- "+trimmed)
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
