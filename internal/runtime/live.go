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
	Provider               api.ProviderKind
	PermissionMode         tools.PermissionMode
	AllowedTools           []string
	ResumeSession          string
	MaxIterations          int
	MaxTokens              int
	AutoCompactInputTokens int
	Prompter               tools.ApprovalPrompter
	Progress               func(PromptProgress)
	Activity               func(ActivityEvent)
}

type PromptPhase string

const (
	PromptPhaseStarting       PromptPhase = "starting"
	PromptPhaseWaitingModel   PromptPhase = "waiting_model"
	PromptPhaseExecutingTools PromptPhase = "executing_tools"
	PromptPhaseFinalizing     PromptPhase = "finalizing"
)

type PromptProgress struct {
	Phase     PromptPhase `json:"phase"`
	Iteration int         `json:"iteration"`
	ToolCount int         `json:"tool_count,omitempty"`
}

type ActivityEvent struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	Detail    string `json:"detail,omitempty"`
	Error     bool   `json:"error,omitempty"`
	Iteration int    `json:"iteration,omitempty"`
	EntryID   string `json:"entry_id,omitempty"`
}

type PromptSummary struct {
	Message           string             `json:"message"`
	Model             string             `json:"model"`
	RequestModel      string             `json:"request_model,omitempty"`
	Provider          string             `json:"provider,omitempty"`
	TurnID            string             `json:"turn_id,omitempty"`
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
		// Model has no hardcoded default — it must come from .ascaris/settings.json
		// (model field) or be passed explicitly via CLI flag / /model command.
		Model:          "",
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
	if strings.TrimSpace(opts.Model) == "" {
		return PromptSummary{}, fmt.Errorf(
			"no model configured\n\n" +
				"Set the model in .ascaris/settings.json:\n" +
				"  {\"model\": \"thudm/glm-4-9b\", \"provider\": {\"kind\": \"openrouter\"}}\n\n" +
				"Or switch model in the TUI with: /model <name>\n" +
				"Examples: /model thudm/glm-4-9b  /model anthropic/claude-sonnet-4-6  /model openai/gpt-4o",
		)
	}
	session, err := h.loadOrCreateSession(opts)
	if err != nil {
		return PromptSummary{}, err
	}
	providerCfg := api.ProviderConfig{
		AnthropicBaseURL:  h.Config.ProviderSettings().AnthropicBaseURL,
		OpenAIBaseURL:     h.Config.ProviderSettings().OpenAIBaseURL,
		OpenRouterBaseURL: h.Config.ProviderSettings().OpenRouterBaseURL,
		PreferredProvider: opts.Provider,
		XAIBaseURL:        h.Config.ProviderSettings().XAIBaseURL,
		ProxyURL:          h.Config.ProviderSettings().ProxyURL,
		ConfigHome:        config.ConfigHome(h.Root),
		OAuthSettings:     h.Config.OAuth(),
	}
	client, err := api.NewProviderClient(resolveModel(opts.Model), providerCfg)
	if err != nil {
		return PromptSummary{}, fmt.Errorf("%w\n\nSet a provider API key: OPENROUTER_API_KEY, ANTHROPIC_API_KEY, OPENAI_API_KEY, or XAI_API_KEY", err)
	}
	// Compute the model name as the chosen provider expects it (e.g. OpenRouter
	// requires "anthropic/claude-sonnet-4-6" rather than "claude-sonnet-4-6").
	requestModel := resolvedModelForClient(resolveModel(opts.Model), client)
	cache := promptcache.New(h.Root, session.Meta.SessionID)
	autoCompaction := applyAutoCompaction(&session, effectiveAutoCompactThreshold(opts))
	session.RecordPrompt(prompt)
	session.Messages = append(session.Messages, api.UserTextMessage(prompt))
	promptTurnID := fmt.Sprintf("%s-turn-%d", session.Meta.SessionID, len(session.Messages))
	session.Meta.Model = opts.Model
	liveRuntime, err := newLiveRuntime(h.Root, h.Config, opts, prompt)
	if err != nil {
		return PromptSummary{}, err
	}
	defer liveRuntime.close()
	summary := PromptSummary{
		Model:             opts.Model,
		RequestModel:      requestModel,
		Provider:          string(client.ProviderKind()),
		TurnID:            promptTurnID,
		AutoCompaction:    autoCompaction,
		ToolUses:          []ToolUseRecord{},
		ToolResults:       []tools.LiveResult{},
		PromptCacheEvents: []any{},
		SessionID:         session.Meta.SessionID,
	}
	emitPromptProgress(opts, PromptProgress{Phase: PromptPhaseStarting})
	emitActivity(opts, ActivityEvent{
		Kind:    "status",
		Title:   "Starting",
		Summary: "Preparing the runtime and session state.",
	})
	for iteration := 0; iteration < max(1, opts.MaxIterations); iteration++ {
		emitPromptProgress(opts, PromptProgress{
			Phase:     PromptPhaseWaitingModel,
			Iteration: iteration + 1,
		})
		emitActivity(opts, ActivityEvent{
			Kind:      "model",
			Title:     "Thinking",
			Summary:   fmt.Sprintf("Waiting for model output on iteration %d.", iteration+1),
			Iteration: iteration + 1,
		})
		request := api.MessageRequest{
			Model:     requestModel,
			MaxTokens: max(256, opts.MaxTokens),
			Messages:  append([]api.InputMessage(nil), session.Messages...),
			System:    defaultSystemPrompt(),
			Tools:     liveRuntime.Definitions(opts.AllowedTools),
			Stream:    true,
		}
		response, event, ok := cache.Lookup(request)
		if ok {
			summary.PromptCacheEvents = append(summary.PromptCacheEvents, event)
			emitActivity(opts, ActivityEvent{
				Kind:      "cache",
				Title:     "Prompt Cache",
				Summary:   "Reused a cached model response for this turn.",
				Detail:    compactJSON(json.RawMessage(summaryJSON(event))),
				Iteration: iteration + 1,
			})
		} else {
			streamState := newLiveStreamState(promptTurnID, iteration+1, opts)
			response, err = liveRuntime.StreamMessage(ctx, client, request, streamState.Handle)
			if err != nil {
				return PromptSummary{}, err
			}
			if event, err := cache.Store(request, response); err == nil {
				summary.PromptCacheEvents = append(summary.PromptCacheEvents, event)
			}
		}
		summary.Iterations++
		session.Meta.Usage = session.Meta.Usage.Add(response.Usage)
		if containsThinkingContent(response) {
			emitActivity(opts, ActivityEvent{
				Kind:      "model",
				Title:     "Thinking",
				Summary:   "Model reasoning content was received.",
				Iteration: iteration + 1,
			})
		}
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
			emitPromptProgress(opts, PromptProgress{
				Phase:     PromptPhaseFinalizing,
				Iteration: iteration + 1,
			})
			emitActivity(opts, ActivityEvent{
				Kind:      "status",
				Title:     "Finalizing",
				Summary:   "Preparing the final assistant response.",
				Iteration: iteration + 1,
			})
			summary.Message = response.FinalText()
			summary.Usage = session.Meta.Usage
			summary.EstimatedCost = formatUSD(estimateCost(session.Meta.Usage, opts.Model))
			if _, err := sessions.SaveManaged(session, h.Root); err != nil {
				return PromptSummary{}, err
			}
			return summary, nil
		}
		emitPromptProgress(opts, PromptProgress{
			Phase:     PromptPhaseExecutingTools,
			Iteration: iteration + 1,
			ToolCount: len(liveCalls),
		})
		envelopes := make([]api.ToolResultEnvelope, 0, len(liveCalls))
		for _, call := range liveCalls {
			emitActivity(opts, ActivityEvent{
				Kind:      "tool_start",
				Title:     call.Name,
				Summary:   fmt.Sprintf("Invoking %s.", call.Name),
				Detail:    compactJSON(call.Input),
				Iteration: iteration + 1,
			})
			result := liveRuntime.ExecuteTool(ctx, call)
			summary.ToolResults = append(summary.ToolResults, result)
			emitActivity(opts, ActivityEvent{
				Kind:      "tool_result",
				Title:     result.Name,
				Summary:   summarizeToolResult(result),
				Detail:    result.Output,
				Error:     result.IsError,
				Iteration: iteration + 1,
			})
			envelopes = append(envelopes, api.ToolResultEnvelope{
				ToolUseID: result.ToolUseID,
				Output:    result.Output,
				IsError:   result.IsError,
			})
		}
		session.Messages = append(session.Messages, api.ToolResultMessage(envelopes))
	}
	emitPromptProgress(opts, PromptProgress{
		Phase:     PromptPhaseFinalizing,
		Iteration: summary.Iterations,
	})
	emitActivity(opts, ActivityEvent{
		Kind:      "status",
		Title:     "Stopped",
		Summary:   "The turn loop ended before a final assistant response was produced.",
		Iteration: summary.Iterations,
		Error:     true,
	})
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

func emitPromptProgress(opts PromptOptions, progress PromptProgress) {
	if opts.Progress == nil {
		return
	}
	opts.Progress(progress)
}

func emitActivity(opts PromptOptions, activity ActivityEvent) {
	if opts.Activity == nil {
		return
	}
	opts.Activity(activity)
}

func summarizeToolResult(result tools.LiveResult) string {
	if result.IsError {
		return fmt.Sprintf("%s returned an error.", result.Name)
	}
	return fmt.Sprintf("%s completed successfully.", result.Name)
}

func activityForToolEvent(event tools.LiveToolEvent, iteration int) ActivityEvent {
	return ActivityEvent{
		Kind:      event.Kind,
		Title:     event.Title,
		Summary:   event.Summary,
		Detail:    event.Detail,
		Error:     event.Error,
		Iteration: iteration,
	}
}

type liveStreamState struct {
	turnID    string
	iteration int
	opts      PromptOptions
	text      strings.Builder
	toolCalls map[int]*liveToolCallState
}

type liveToolCallState struct {
	id    string
	name  string
	input strings.Builder
}

func newLiveStreamState(turnID string, iteration int, opts PromptOptions) *liveStreamState {
	return &liveStreamState{
		turnID:    turnID,
		iteration: iteration,
		opts:      opts,
		toolCalls: map[int]*liveToolCallState{},
	}
}

func (s *liveStreamState) Handle(event api.StreamEvent) {
	switch event.Type {
	case "text_delta":
		if strings.TrimSpace(event.Text) == "" {
			return
		}
		s.text.WriteString(event.Text)
		emitActivity(s.opts, ActivityEvent{
			Kind:      "result_stream",
			Title:     "Result",
			Detail:    s.text.String(),
			Iteration: s.iteration,
			EntryID:   s.entryID("result"),
		})
	case "tool_call_delta":
		call := s.toolCalls[event.ToolCallIndex]
		if call == nil {
			call = &liveToolCallState{}
			s.toolCalls[event.ToolCallIndex] = call
		}
		if strings.TrimSpace(event.ToolCallID) != "" {
			call.id = event.ToolCallID
		}
		if strings.TrimSpace(event.ToolName) != "" {
			call.name = event.ToolName
		}
		if event.ToolInputDelta != "" {
			call.input.WriteString(event.ToolInputDelta)
		}
		emitActivity(s.opts, ActivityEvent{
			Kind:      "tool_call_delta",
			Title:     firstNonEmptyString(call.name, "Tool Call"),
			Summary:   "Forming tool call.",
			Detail:    renderToolCallPreview(call.id, call.name, call.input.String()),
			Iteration: s.iteration,
			EntryID:   s.entryID(fmt.Sprintf("tool-call-%d", event.ToolCallIndex)),
		})
	case "tool_call_ready":
		call := s.toolCalls[event.ToolCallIndex]
		name := event.ToolName
		inputPreview := ""
		if call != nil {
			if name == "" {
				name = call.name
			}
			inputPreview = call.input.String()
		}
		if len(event.ToolInput) > 0 {
			inputPreview = compactJSON(event.ToolInput)
		}
		emitActivity(s.opts, ActivityEvent{
			Kind:      "tool_call_ready",
			Title:     firstNonEmptyString(name, "Tool Call"),
			Summary:   "Tool call ready.",
			Detail:    renderToolCallPreview(event.ToolCallID, name, inputPreview),
			Iteration: s.iteration,
			EntryID:   s.entryID(fmt.Sprintf("tool-call-%d", event.ToolCallIndex)),
		})
	case "thinking_delta":
		emitActivity(s.opts, ActivityEvent{
			Kind:      "model",
			Title:     "Thinking",
			Summary:   "Model reasoning content was received.",
			Iteration: s.iteration,
			EntryID:   s.entryID("thinking"),
		})
	}
}

func (s *liveStreamState) entryID(suffix string) string {
	return fmt.Sprintf("%s-iter-%d-%s", s.turnID, s.iteration, suffix)
}

func renderToolCallPreview(id, name, input string) string {
	lines := []string{}
	if strings.TrimSpace(id) != "" {
		lines = append(lines, "id="+strings.TrimSpace(id))
	}
	if strings.TrimSpace(name) != "" {
		lines = append(lines, "name="+strings.TrimSpace(name))
	}
	if strings.TrimSpace(input) != "" {
		lines = append(lines, strings.TrimSpace(input))
	}
	return strings.Join(lines, "\n")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func summaryJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func containsThinkingContent(response api.MessageResponse) bool {
	for _, block := range response.Content {
		if block.Type == "thinking" && strings.TrimSpace(block.Thinking) != "" {
			return true
		}
	}
	return false
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
	return strings.Join([]string{
		"You are Ascaris, a coding harness working inside the current workspace.",
		"Follow the user's instructions exactly. If anything is unclear, ambiguous, conflicting, or appears wrong, stop and ask instead of guessing or deciding what they probably meant.",
		"Treat user explanations and root-cause guesses as hypotheses, not facts. When observed evidence from files, commands, or tool output contradicts a hypothesis, say so explicitly and follow the evidence.",
		"Use tools and commands only when they are relevant to the task. Read the relevant files before editing, verify assumptions before acting, and inspect results before reporting completion.",
		"Do not perform destructive or irreversible actions without the user's explicit approval. Keep work inside the workspace unless the user explicitly asks otherwise.",
		"If a command or tool fails, report what happened honestly. Diagnose the cause from the evidence, avoid pretending success, and do not silently retry the same failing approach or modify tests and checks just to force a pass.",
		"Adapt when needed, but only by trying a materially different evidence-based approach that still respects the user's instructions and constraints.",
		"Stay efficient: explore purposefully, avoid unnecessary research, and keep answers direct.",
	}, "\n\n")
}

// resolveModel expands short aliases to their full model names.
// An empty string is returned as-is — callers must validate before use.
// Short Claude aliases (sonnet/opus/haiku) still work and will be routed
// through OpenRouter as anthropic/claude-* when ANTHROPIC_API_KEY is absent.
func resolveModel(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "sonnet":
		return "claude-sonnet-4-6"
	case "opus":
		return "claude-opus-4-6"
	case "haiku":
		return "claude-haiku-4-5"
	default:
		return strings.TrimSpace(model)
	}
}

// resolvedModelForClient maps a canonical model name to the format expected
// by the given client. OpenRouter requires provider-prefixed slugs such as
// "anthropic/claude-sonnet-4-6"; all other clients use the name as-is.
func resolvedModelForClient(model string, client api.MessageClient) string {
	if client.ProviderKind() != api.ProviderOpenRouter {
		return model
	}
	if strings.Contains(model, "/") {
		return model // already prefixed, e.g. "openai/gpt-4o"
	}
	if strings.HasPrefix(model, "claude-") {
		return "anthropic/" + model
	}
	return model
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
	return fmt.Sprintf("$%.2f", amount)
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
	if opts.Provider == "" {
		if provider, err := api.ParseProviderKind(runtimeConfig.ProviderSettings().Kind); err == nil {
			opts.Provider = provider
		}
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
