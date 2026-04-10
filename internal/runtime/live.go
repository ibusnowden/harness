package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"ascaris/internal/api"
	"ascaris/internal/config"
	"ascaris/internal/promptcache"
	"ascaris/internal/sessions"
	"ascaris/internal/tools"
)

type PromptOptions struct {
	Model                  string
	PermissionMode         tools.PermissionMode
	AllowedTools           []string
	ResumeSession          string
	MaxIterations          int
	MaxTokens              int
	AutoCompactInputTokens int
	Prompter               tools.ApprovalPrompter
}

type PromptSummary struct {
	Message           string             `json:"message"`
	Model             string             `json:"model"`
	Iterations        int                `json:"iterations"`
	AutoCompaction    *AutoCompaction    `json:"auto_compaction"`
	ToolUses          []ToolUseRecord    `json:"tool_uses"`
	ToolResults       []tools.LiveResult `json:"tool_results"`
	PromptCacheEvents []any              `json:"prompt_cache_events"`
	Usage             api.Usage          `json:"usage"`
	EstimatedCost     string             `json:"estimated_cost"`
	SessionID         string             `json:"session_id,omitempty"`
}

type ToolUseRecord struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

type AutoCompaction struct {
	RemovedMessages int    `json:"removed_messages"`
	Notice          string `json:"notice"`
}

type LiveHarness struct {
	Root   string
	Config config.RuntimeConfig
}

func LiveConfigured() bool {
	return api.ConfiguredFromEnv()
}

func NewLiveHarness(root string) (*LiveHarness, error) {
	runtimeConfig, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	return &LiveHarness{Root: root, Config: runtimeConfig}, nil
}

func DefaultPromptOptions() PromptOptions {
	return PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		MaxIterations:  8,
		MaxTokens:      4096,
	}
}

func (h LiveHarness) RunPrompt(ctx context.Context, prompt string, opts PromptOptions) (PromptSummary, error) {
	if strings.TrimSpace(prompt) == "" {
		return PromptSummary{}, fmt.Errorf("prompt text is required")
	}
	opts = withPromptDefaults(opts, h.Config)
	session, err := h.loadOrCreateSession(opts)
	if err != nil {
		return PromptSummary{}, err
	}
	client, err := api.NewProviderClient(resolveModel(opts.Model), api.ProviderConfig{
		AnthropicBaseURL:  h.Config.ProviderSettings().AnthropicBaseURL,
		OpenAIBaseURL:     h.Config.ProviderSettings().OpenAIBaseURL,
		OpenRouterBaseURL: h.Config.ProviderSettings().OpenRouterBaseURL,
		XAIBaseURL:        h.Config.ProviderSettings().XAIBaseURL,
		ProxyURL:          h.Config.ProviderSettings().ProxyURL,
		ConfigHome:        config.ConfigHome(h.Root),
		OAuthSettings:     h.Config.OAuth(),
	})
	if err != nil {
		return PromptSummary{}, err
	}
	cache := promptcache.New(h.Root, session.Meta.SessionID)
	autoCompaction := applyAutoCompaction(&session, effectiveAutoCompactThreshold(opts))
	session.RecordPrompt(prompt)
	session.Messages = append(session.Messages, api.UserTextMessage(prompt))
	session.Meta.Model = opts.Model
	liveRuntime, err := newLiveRuntime(h.Root, h.Config, opts, prompt)
	if err != nil {
		return PromptSummary{}, err
	}
	defer liveRuntime.close()
	summary := PromptSummary{
		Model:             opts.Model,
		AutoCompaction:    autoCompaction,
		ToolUses:          []ToolUseRecord{},
		ToolResults:       []tools.LiveResult{},
		PromptCacheEvents: []any{},
		SessionID:         session.Meta.SessionID,
	}
	for iteration := 0; iteration < max(1, opts.MaxIterations); iteration++ {
		request := api.MessageRequest{
			Model:     resolveModel(opts.Model),
			MaxTokens: max(256, opts.MaxTokens),
			Messages:  append([]api.InputMessage(nil), session.Messages...),
			System:    defaultSystemPrompt(),
			Tools:     liveRuntime.Definitions(opts.AllowedTools),
			Stream:    true,
		}
		response, event, ok := cache.Lookup(request)
		if ok {
			summary.PromptCacheEvents = append(summary.PromptCacheEvents, event)
		} else {
			response, err = liveRuntime.StreamMessage(ctx, client, request)
			if err != nil {
				return PromptSummary{}, err
			}
			if event, err := cache.Store(request, response); err == nil {
				summary.PromptCacheEvents = append(summary.PromptCacheEvents, event)
			}
		}
		summary.Iterations++
		session.Meta.Usage = session.Meta.Usage.Add(response.Usage)
		assistantMessage := assistantMessageFromResponse(response)
		session.Messages = append(session.Messages, assistantMessage)
		liveCalls := collectToolCalls(response)
		for _, call := range liveCalls {
			summary.ToolUses = append(summary.ToolUses, ToolUseRecord{
				ID:    call.ID,
				Name:  call.Name,
				Input: compactJSON(call.Input),
			})
		}
		if len(liveCalls) == 0 {
			summary.Message = response.FinalText()
			summary.Usage = session.Meta.Usage
			summary.EstimatedCost = formatUSD(estimateCost(session.Meta.Usage, opts.Model))
			if _, err := sessions.SaveManaged(session, h.Root); err != nil {
				return PromptSummary{}, err
			}
			return summary, nil
		}
		envelopes := make([]api.ToolResultEnvelope, 0, len(liveCalls))
		for _, call := range liveCalls {
			result := liveRuntime.ExecuteTool(ctx, call)
			summary.ToolResults = append(summary.ToolResults, result)
			envelopes = append(envelopes, api.ToolResultEnvelope{
				ToolUseID: result.ToolUseID,
				Output:    result.Output,
				IsError:   result.IsError,
			})
		}
		session.Messages = append(session.Messages, api.ToolResultMessage(envelopes))
	}
	summary.Usage = session.Meta.Usage
	summary.EstimatedCost = formatUSD(estimateCost(session.Meta.Usage, opts.Model))
	if _, err := sessions.SaveManaged(session, h.Root); err != nil {
		return PromptSummary{}, err
	}
	if summary.Message == "" {
		summary.Message = "prompt stopped before the model produced a final assistant message"
	}
	return summary, nil
}

func (s PromptSummary) JSON() string {
	data, err := json.Marshal(s)
	if err != nil {
		return `{"message":"failed to serialize prompt summary"}`
	}
	return string(data)
}

func ReadPrompt(stdin io.Reader) (string, error) {
	if stdin == nil {
		return "", nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (h LiveHarness) loadOrCreateSession(opts PromptOptions) (sessions.ManagedSession, error) {
	if strings.TrimSpace(opts.ResumeSession) != "" {
		return sessions.LoadManaged(h.Root, opts.ResumeSession)
	}
	return sessions.NewManagedSession(newLiveSessionID(), opts.Model), nil
}

func assistantMessageFromResponse(response api.MessageResponse) api.InputMessage {
	blocks := make([]api.InputContentBlock, 0, len(response.Content))
	for _, block := range response.Content {
		switch block.Type {
		case "text":
			blocks = append(blocks, api.InputContentBlock{
				Type: "text",
				Text: block.Text,
			})
		case "tool_use":
			blocks = append(blocks, api.InputContentBlock{
				Type:  "tool_use",
				ID:    block.ID,
				Name:  block.Name,
				Input: cloneRawJSON(block.Input),
			})
		}
	}
	return api.InputMessage{
		Role:    "assistant",
		Content: blocks,
	}
}

func collectToolCalls(response api.MessageResponse) []tools.LiveCall {
	calls := []tools.LiveCall{}
	for _, block := range response.Content {
		if block.Type != "tool_use" {
			continue
		}
		calls = append(calls, tools.LiveCall{
			ID:    block.ID,
			Name:  block.Name,
			Input: cloneRawJSON(block.Input),
		})
	}
	return calls
}

func defaultSystemPrompt() string {
	return "You are Ascaris, a coding harness. Prefer direct answers, use tools when needed, and keep work inside the current workspace."
}

func resolveModel(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "", "sonnet":
		return "claude-sonnet-4-6"
	case "opus":
		return "claude-opus-4-6"
	case "haiku":
		return "claude-haiku-4-5"
	default:
		return model
	}
}

func estimateCost(usage api.Usage, model string) float64 {
	pricing := modelPricingFor(model)
	return costForTokens(usage.InputTokens, pricing.InputCostPerMillion) +
		costForTokens(usage.OutputTokens, pricing.OutputCostPerMillion) +
		costForTokens(usage.CacheCreationInputTokens, pricing.CacheCreationCostPerMillion) +
		costForTokens(usage.CacheReadInputTokens, pricing.CacheReadCostPerMillion)
}

type modelPricing struct {
	InputCostPerMillion         float64
	OutputCostPerMillion        float64
	CacheCreationCostPerMillion float64
	CacheReadCostPerMillion     float64
}

func modelPricingFor(model string) modelPricing {
	normalized := strings.ToLower(model)
	switch {
	case strings.Contains(normalized, "haiku"):
		return modelPricing{InputCostPerMillion: 1.0, OutputCostPerMillion: 5.0, CacheCreationCostPerMillion: 1.25, CacheReadCostPerMillion: 0.1}
	case strings.Contains(normalized, "opus"):
		return modelPricing{InputCostPerMillion: 15.0, OutputCostPerMillion: 75.0, CacheCreationCostPerMillion: 18.75, CacheReadCostPerMillion: 1.5}
	default:
		return modelPricing{InputCostPerMillion: 15.0, OutputCostPerMillion: 75.0, CacheCreationCostPerMillion: 18.75, CacheReadCostPerMillion: 1.5}
	}
}

func costForTokens(tokens int, usdPerMillion float64) float64 {
	return float64(tokens) / 1_000_000.0 * usdPerMillion
}

func formatUSD(amount float64) string {
	return fmt.Sprintf("$%.4f", amount)
}

func applyAutoCompaction(session *sessions.ManagedSession, threshold int) *AutoCompaction {
	if threshold <= 0 {
		return nil
	}
	inputTokens := session.Meta.Usage.InputTokens + session.Meta.Usage.CacheCreationInputTokens + session.Meta.Usage.CacheReadInputTokens
	if inputTokens < threshold || len(session.Messages) <= 4 {
		return nil
	}
	removed := len(session.Messages) - 4
	session.Messages = append([]api.InputMessage(nil), session.Messages[removed:]...)
	session.RecordCompaction("auto-compacted preserved the most recent four messages", removed)
	return &AutoCompaction{
		RemovedMessages: removed,
		Notice:          fmt.Sprintf("[auto-compacted: removed %d messages]", removed),
	}
}

func effectiveAutoCompactThreshold(opts PromptOptions) int {
	if opts.AutoCompactInputTokens > 0 {
		return opts.AutoCompactInputTokens
	}
	value := strings.TrimSpace(os.Getenv("ASCARIS_AUTO_COMPACT_INPUT_TOKENS"))
	if value == "" {
		return 0
	}
	threshold, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return threshold
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err == nil {
		return out.String()
	}
	return string(raw)
}

func toAllowMap(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func cloneRawJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func newLiveSessionID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func withPromptDefaults(opts PromptOptions, runtimeConfig config.RuntimeConfig) PromptOptions {
	defaults := DefaultPromptOptions()
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = runtimeConfig.Model()
	}
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = defaults.Model
	}
	if opts.PermissionMode == "" {
		opts.PermissionMode = tools.PermissionMode(runtimeConfig.PermissionMode())
	}
	if opts.PermissionMode == "" {
		opts.PermissionMode = defaults.PermissionMode
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = defaults.MaxIterations
	}
	if opts.MaxTokens <= 0 {
		if override := runtimeConfig.PluginSettings().MaxOutputTokens; override > 0 {
			opts.MaxTokens = override
		} else {
			opts.MaxTokens = defaults.MaxTokens
		}
	}
	return opts
}
