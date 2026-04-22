// Package contextbudget contains the model-window lookup, budget arithmetic,
// and lightweight message compaction shared by the live runtime and the
// subagent runner. The richer, session-aware compaction path (semantic
// summarization, context-compaction notices, prompt-history integration)
// lives in internal/runtime; this package is the minimum subset that works
// on raw []api.InputMessage and has no import of sessions/runtime.
package contextbudget

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"ascaris/internal/api"
)

// ModelContextWindow maps a model alias to its maximum context length. Zero
// means "unknown" and disables budget-driven compaction for that model.
func ModelContextWindow(model string) int {
	normalized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case normalized == "qwen3.6-30b-a3b", strings.Contains(normalized, "qwen3.6-35b-a3b"):
		return 262144
	case normalized == "glm-4.7-flash":
		return 32768
	default:
		return 0
	}
}

// SafetyTokens reserves a small slice of the context window for model
// bookkeeping (tool-call wrappers, chat-template overhead). Scales with
// window size so it does not eat most of a small window.
func SafetyTokens(contextWindow int) int {
	if contextWindow <= 0 {
		return 0
	}
	if fraction := contextWindow / 24; fraction > 384 {
		return fraction
	}
	return 384
}

// MinResponseTokens is the floor for max_tokens reserved for the model's
// next reply when we're computing the input budget.
func MinResponseTokens(requested int) int {
	if requested <= 0 {
		return 512
	}
	if requested > 512 {
		return 512
	}
	if requested < 128 {
		return 128
	}
	return requested
}

// InputBudget returns how many input tokens may be spent given a model
// window and a reservation for the model's reply.
func InputBudget(contextWindow, minResponseTokens int) int {
	if contextWindow <= 0 {
		return 0
	}
	return contextWindow - SafetyTokens(contextWindow) - MinResponseTokens(minResponseTokens)
}

// ValidToolHistory reports whether every tool_result in messages is paired
// with a prior tool_use whose ID matches. Compaction must preserve this
// invariant or the upstream API rejects the request.
func ValidToolHistory(messages []api.InputMessage) bool {
	seen := map[string]struct{}{}
	for _, message := range messages {
		for _, block := range message.Content {
			switch block.Type {
			case "tool_use":
				if strings.TrimSpace(block.ID) != "" {
					seen[block.ID] = struct{}{}
				}
			case "tool_result":
				id := strings.TrimSpace(block.ToolUseID)
				if id == "" {
					return false
				}
				if _, ok := seen[id]; !ok {
					return false
				}
			}
		}
	}
	return true
}

// TruncateContentBlocks returns a deep copy of messages with every text and
// raw JSON payload truncated to maxChars bytes. The original slice is not
// mutated.
func TruncateContentBlocks(messages []api.InputMessage, maxChars int) []api.InputMessage {
	out := make([]api.InputMessage, len(messages))
	for i, message := range messages {
		out[i] = api.InputMessage{
			Role:    message.Role,
			Content: make([]api.InputContentBlock, len(message.Content)),
		}
		for j, block := range message.Content {
			block.Text = api.TruncateMiddle(block.Text, maxChars)
			if len(block.Content) > 0 {
				items := make([]api.ToolResultContentBlock, len(block.Content))
				for k, item := range block.Content {
					item.Text = api.TruncateMiddle(item.Text, maxChars)
					items[k] = item
				}
				block.Content = items
			}
			out[i].Content[j] = block
		}
	}
	return out
}

// CompactSubagentMessages returns a message slice that fits within the model's
// input budget. It preserves the first message (the subagent's user prompt)
// and the most recent valid tool-history window that satisfies the budget.
// Returns the new slice, the number of messages removed, and whether any
// compaction actually happened.
//
// Unlike the live runtime's compaction, this path does not insert a semantic
// summary — subagents are short-lived and one-shot, so losing older context
// is acceptable. The live path is still responsible for durable summaries.
func CompactSubagentMessages(messages []api.InputMessage, system string, tools []api.ToolDefinition, model string, minResponseTokens int) ([]api.InputMessage, int, bool) {
	if len(messages) <= 1 {
		return messages, 0, false
	}
	window := ModelContextWindow(model)
	budget := InputBudget(window, minResponseTokens)
	if budget <= 0 {
		return messages, 0, false
	}
	baseRequest := api.MessageRequest{
		Model:    model,
		System:   system,
		Tools:    tools,
		Messages: messages,
	}
	if api.EstimateRequestInputTokens(baseRequest) <= budget {
		return messages, 0, false
	}
	first := messages[0]
	for start := 1; start < len(messages); start++ {
		tail := messages[start:]
		candidate := append([]api.InputMessage{first}, tail...)
		if !ValidToolHistory(candidate) {
			continue
		}
		truncated := TruncateContentBlocks(candidate, 4000)
		baseRequest.Messages = truncated
		if api.EstimateRequestInputTokens(baseRequest) <= budget {
			return truncated, len(messages) - len(candidate), true
		}
	}
	return messages, 0, false
}

// ServedContextLimit captures what the upstream server told us when it
// rejected a request for exceeding its context window.
type ServedContextLimit struct {
	ContextWindow int
	InputTokens   int
}

var (
	maximumContextLengthRE = regexp.MustCompile(`(?i)maximum context length is\s+([0-9,]+)`)
	promptInputTokensRE    = regexp.MustCompile(`(?i)prompt contains at least\s+([0-9,]+)\s+input tokens`)
	inputTokensValueRE     = regexp.MustCompile(`(?i)"value"\s*:\s*([0-9,]+)`)
)

// ParseServedContextLimitError extracts the server-reported maximum context
// length (and, if available, the observed input-token count) from a vLLM
// 400 error. Returns ok=false when the error does not look like a context
// overflow.
func ParseServedContextLimitError(err error) (ServedContextLimit, bool) {
	if err == nil {
		return ServedContextLimit{}, false
	}
	text := err.Error()
	var httpErr *api.OpenAICompatHTTPError
	if errors.As(err, &httpErr) {
		text = strings.TrimSpace(httpErr.Body + "\n" + httpErr.Error())
	}
	if !strings.Contains(strings.ToLower(text), "maximum context length") {
		return ServedContextLimit{}, false
	}
	limit := ServedContextLimit{
		ContextWindow: firstRegexInt(maximumContextLengthRE, text),
		InputTokens:   firstRegexInt(promptInputTokensRE, text),
	}
	if limit.InputTokens == 0 && strings.Contains(text, `"input_tokens"`) {
		limit.InputTokens = firstRegexInt(inputTokensValueRE, text)
	}
	return limit, limit.ContextWindow > 0
}

func firstRegexInt(pattern *regexp.Regexp, text string) int {
	matches := pattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return 0
	}
	value, err := strconv.Atoi(strings.ReplaceAll(matches[1], ",", ""))
	if err != nil {
		return 0
	}
	return value
}
