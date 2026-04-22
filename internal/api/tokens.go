package api

import (
	"encoding/json"
	"strings"
)

// Token estimators used across the runtime and subagent packages to reason
// about context-window budgets without calling a tokenizer. The heuristic
// (char/4 for message text, char/5 for system/tool descriptions) overestimates
// slightly — which is the safe direction: compaction fires a bit early rather
// than missing a threshold.

// EstimateRequestInputTokens returns an approximate input-token count for
// the full request (system + messages + tools + tool_choice).
func EstimateRequestInputTokens(request MessageRequest) int {
	total := EstimateSystemTokenCount(request.System) + 24
	total += EstimateMessagesTokens(request.Messages)
	total += EstimateToolsTokens(request.Tools)
	if request.ToolChoice != nil {
		total += 16
		total += EstimateTokenCount(request.ToolChoice.Type)
		total += EstimateTokenCount(request.ToolChoice.Name)
	}
	if total < 1 {
		return 1
	}
	return total
}

// EstimateMessagesTokens approximates the total token cost of a message list.
// Safe on an empty slice (returns 0).
func EstimateMessagesTokens(messages []InputMessage) int {
	total := 0
	for _, message := range messages {
		total += 12
		total += EstimateTokenCount(message.Role)
		for _, block := range message.Content {
			total += 8
			total += EstimateTokenCount(block.Type)
			total += EstimateTokenCount(block.Text)
			total += EstimateTokenCount(block.ID)
			total += EstimateTokenCount(block.Name)
			total += EstimateTokenCount(block.ToolUseID)
			total += EstimateRawJSONTokens(block.Input)
			for _, item := range block.Content {
				total += 4
				total += EstimateTokenCount(item.Type)
				total += EstimateTokenCount(item.Text)
				total += EstimateRawJSONTokens(item.Value)
			}
		}
	}
	return total
}

// EstimateToolsTokens approximates the token cost of the tool catalogue.
func EstimateToolsTokens(tools []ToolDefinition) int {
	total := 0
	for _, tool := range tools {
		total += 24
		total += EstimateTokenCount(tool.Name)
		total += EstimateSystemTokenCount(tool.Description)
		total += EstimateRawJSONTokens(tool.InputSchema)
	}
	return total
}

// EstimateSystemTokenCount uses a slightly looser divisor appropriate for
// system prompts and tool descriptions, which tend to compress a bit better
// than conversational text.
func EstimateSystemTokenCount(text string) int {
	return estimateTokenCountWithDivisor(text, 5)
}

// EstimateTokenCount is the conversational-text estimator: chars/4.
func EstimateTokenCount(text string) int {
	return estimateTokenCountWithDivisor(text, 4)
}

// EstimateRawJSONTokens approximates the token cost of a raw JSON payload
// (tool input, tool-result value) at chars/4.
func EstimateRawJSONTokens(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	return (len(raw) + 3) / 4
}

func estimateTokenCountWithDivisor(text string, divisor int) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	return (len(text) + divisor - 1) / divisor
}
