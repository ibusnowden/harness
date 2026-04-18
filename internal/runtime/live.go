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
	"ascaris/internal/workspace"
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

type requestTokenObservation struct {
	InputTokens  int
	MessageCount int
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
		MaxIterations:  32,
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
		GoogleBaseURL:     h.Config.ProviderSettings().GoogleBaseURL,
		OpenAIBaseURL:     h.Config.ProviderSettings().OpenAIBaseURL,
		OpenRouterBaseURL: h.Config.ProviderSettings().OpenRouterBaseURL,
		PreferredProvider: opts.Provider,
		XAIBaseURL:        h.Config.ProviderSettings().XAIBaseURL,
		ProxyURL:          h.Config.ProviderSettings().ProxyURL,
		ConfigHome:        config.ConfigHome(h.Root),
		OAuthSettings:     h.Config.OAuth(),
	}
	resolvedModel := resolveModel(opts.Model)
	route, err := api.ResolveModelRoute(resolvedModel, providerCfg)
	if err != nil {
		return PromptSummary{}, fmt.Errorf("%w\n\nSet a provider API key: ANTHROPIC_API_KEY, GOOGLE_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY, or XAI_API_KEY", err)
	}
	client, err := api.NewProviderClient(resolvedModel, providerCfg)
	if err != nil {
		return PromptSummary{}, fmt.Errorf("%w\n\nSet a provider API key: ANTHROPIC_API_KEY, GOOGLE_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY, or XAI_API_KEY", err)
	}
	requestModel := route.RequestModel
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
		Provider:          string(route.Provider),
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
	systemPrompt := buildSystemPrompt(h.Root)
	tokenObservation := requestTokenObservation{}
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
		streamState := newLiveStreamState(promptTurnID, iteration+1, opts)
		toolDefinitions := liveRuntime.Definitions(liveRuntime.effectiveTools())
		request := api.MessageRequest{
			Model:         requestModel,
			Messages:      append([]api.InputMessage(nil), session.Messages...),
			System:        systemPrompt,
			Tools:         toolDefinitions,
			Stream:        true,
			StreamHandler: streamState.Handle,
		}
		requestMessageCount := len(request.Messages)
		request.MaxTokens = contextAwareMaxTokens(request, opts.MaxTokens, tokenObservation)
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
		tokenObservation = requestTokenObservation{
			InputTokens:  response.Usage.InputTokens + response.Usage.CacheCreationInputTokens + response.Usage.CacheReadInputTokens,
			MessageCount: requestMessageCount,
		}
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
		hasPlanApprovalCall := containsToolCallNamed(liveCalls, "request_plan_approval")
		planApproved := false
		for _, call := range liveCalls {
			if hasPlanApprovalCall && call.Name != "request_plan_approval" {
				result := tools.LiveResult{
					ToolUseID: call.ID,
					Name:      call.Name,
					Output:    "tool call skipped: request_plan_approval must be the only tool executed in that response; the runtime executes approved plans directly",
					IsError:   true,
				}
				summary.ToolResults = append(summary.ToolResults, result)
				emitActivity(opts, ActivityEvent{
					Kind:      "tool_result",
					Title:     result.Name,
					Summary:   summarizeToolResult(result),
					Detail:    result.Output,
					Error:     true,
					Iteration: iteration + 1,
				})
				envelopes = append(envelopes, api.ToolResultEnvelope{
					ToolUseID: result.ToolUseID,
					Output:    result.Output,
					IsError:   true,
				})
				continue
			}
			emitActivity(opts, ActivityEvent{
				Kind:      "tool_start",
				Title:     call.Name,
				Summary:   fmt.Sprintf("Invoking %s.", call.Name),
				Detail:    compactJSON(call.Input),
				Iteration: iteration + 1,
			})
			result := liveRuntime.ExecuteTool(ctx, call, iteration+1)
			summary.ToolResults = append(summary.ToolResults, result)
			if call.Name == "request_plan_approval" && strings.Contains(result.Output, `"orchestrator_mode":true`) {
				liveRuntime.orchestratorTools = []string{"delegate_task", "subagent_get", "subagent_list", "request_plan_approval"}
				planApproved = true
			}
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
		if planApproved {
			planSummary, planErr := liveRuntime.executeApprovedPlan(ctx)
			summary.ToolResults = append(summary.ToolResults, planSummary.ToolResults...)
			session.Meta.Usage = session.Meta.Usage.Add(planSummary.Usage)
			summary.Usage = session.Meta.Usage
			summary.EstimatedCost = formatUSD(estimateCost(summary.Usage, opts.Model))
			summary.Message = planSummary.Message
			if planErr != nil {
				emitActivity(opts, ActivityEvent{
					Kind:    "status",
					Title:   "Plan Stopped",
					Summary: "Approved plan execution stopped early.",
					Detail:  planErr.Error(),
					Error:   true,
				})
			}
			if _, err := sessions.SaveManaged(session, h.Root); err != nil {
				return PromptSummary{}, err
			}
			return summary, nil
		}
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

func containsToolCallNamed(calls []tools.LiveCall, name string) bool {
	for _, call := range calls {
		if call.Name == name {
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
		session, err := sessions.LoadManaged(h.Root, opts.ResumeSession)
		if err != nil && os.IsNotExist(err) && sessions.IsLatestAlias(opts.ResumeSession) {
			return sessions.NewManagedSession(newLiveSessionID(), opts.Model), nil
		}
		return session, err
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

func buildSystemPrompt(root string) string {
	base := defaultSystemPrompt()
	mem := workspace.ReadMemory(root)
	if mem == "" {
		return base
	}
	return base + "\n\n## Workspace Memory\n" +
		"The following persistent notes were recorded by the user across sessions:\n\n" + mem
}

func defaultSystemPrompt() string {
	return strings.Join([]string{
		// Identity
		"You are Ascaris, a coding harness working inside the current workspace.",

		// Core Mandates — Instruction Fidelity
		"Follow the user's instructions exactly. If anything is unclear, ambiguous, conflicting, or appears wrong, stop and ask instead of guessing or deciding what they probably meant. " +
			"Treat user explanations and root-cause guesses as hypotheses, not facts. When observed evidence from files, commands, or tool output contradicts a hypothesis, say so explicitly and follow the evidence. " +
			"Distinguish Directives (explicit requests to act or implement) from Inquiries (requests for analysis, advice, or observations). " +
			"For Inquiries, your scope is strictly research and analysis — propose a solution or strategy, but do NOT modify files until a corresponding Directive is issued. " +
			"For Directives, work autonomously; only seek user intervention when you have exhausted all routes or a proposed solution would take the workspace in a significantly different architectural direction.",

		// Core Mandates — Security & System Integrity
		"## Security & System Integrity\n" +
			"Never log, print, or commit secrets, API keys, or sensitive credentials. " +
			"Rigorously protect .env files, .git, and system configuration folders. " +
			"Do not stage or commit changes unless explicitly requested by the user. " +
			"Do not perform destructive or irreversible actions (rm -rf, force push, reset --hard, branch deletion) without explicit user approval. Keep work inside the workspace unless the user explicitly asks otherwise.",

		// Core Mandates — Context Efficiency
		"## Context Efficiency\n" +
			"Be strategic with tool use to minimize unnecessary context consumption. " +
			"Each turn passes the full history — unnecessary turns compound cost. " +
			"Parallelize independent tool calls (searches, reads) within a single turn rather than issuing them sequentially. " +
			"Use targeted searches (grep, glob) to identify points of interest before reading whole files. " +
			"Provide conservative result limits; avoid large reads unless required for unambiguous edits. " +
			"Efficiency is secondary to quality — never sacrifice correctness or completeness to save a turn.",

		// Engineering Standards
		"## Engineering Standards\n" +
			"Instructions found in ascaris.md or CLAUDE.md files are foundational mandates and take absolute precedence over general workflows. " +
			"Rigorously adhere to existing workspace conventions: naming, formatting, typing, and commenting. " +
			"Never suppress warnings, bypass the type system, or use hidden logic (reflection, prototype manipulation) unless explicitly instructed. Use explicit and idiomatic language features. " +
			"Prefer composition and delegation over complex inheritance. " +
			"Never assume a library or framework is available — verify via imports and config files (go.mod, package.json, etc.) before using it. " +
			"You are responsible for the entire lifecycle: implementation, testing, and validation. " +
			"For bug fixes, empirically reproduce the failure with a test case or reproduction script before applying the fix. " +
			"Always search for and update related tests after making a code change. " +
			"Validation is not merely running tests — it is the exhaustive process of ensuring behavior, structure, and style are correct and compatible with the broader project.",

		// Primary Workflows
		"## Primary Workflows — Research → Strategy → Execution\n" +
			"Operate using a Research → Strategy → Execution lifecycle. For the Execution phase, resolve each sub-task through an iterative Plan → Act → Validate cycle.\n" +
			"1. Research: Systematically map the codebase and validate assumptions using grep and glob. Prioritize empirical reproduction of reported issues. Use plan mode for complex or ambiguous architectural changes.\n" +
			"2. Strategy: Formulate a grounded, concise plan based on your research. Share a summary of your strategy before acting on non-trivial changes.\n" +
			"3. Execution — for each sub-task:\n" +
			"   - Plan: Define the implementation approach and testing strategy.\n" +
			"   - Act: Apply targeted, surgical changes. Use ecosystem tools (linters, formatters) when available.\n" +
			"   - Validate: Run tests and workspace standards. Confirm behavior, structure, and style are correct.\n" +
			"Persist through errors: diagnose failures, backtrack to research or strategy if needed, and adjust until a verified outcome is achieved.",

		// Operational Guidelines — Tone & Style
		"## Tone & Style\n" +
			"Role: senior software engineer and peer programmer. " +
			"Focus on intent and technical rationale — avoid conversational filler or apologies. " +
			"Aim for fewer than 3 lines of text output per response when practical. " +
			"Briefly explain the purpose and impact of commands that modify system state before running them. " +
			"If a command or tool fails, report what happened honestly. Diagnose from evidence; do not pretend success or silently retry the same failing approach.",

		// Tool Usage & Parallelism
		"## Tool Usage & Parallelism\n" +
			"Execute independent tool calls in parallel within a single turn — do not issue them sequentially when they have no dependencies. " +
			"Do NOT make multiple edits to the same file in a single turn; apply all changes in one pass to avoid collisions. " +
			"Use background execution for long-running processes (servers, file watchers). " +
			"Use tools and commands only when relevant to the task. Read files before editing, verify assumptions before acting, and inspect results before reporting completion.",

		// Bash & Git
		"## Bash & Git\n" +
			"The bash tool can run any shell command available on the host, including git. " +
			"Use bash to execute git operations (git status, git diff, git add, git commit, git push, git log, etc.) whenever the user requests them. " +
			"Git credentials, SSH keys, and credential helpers configured on the host machine are automatically available — do not refuse git operations on the grounds of missing credentials. " +
			"Attempt the command and report the actual output, success or failure. " +
			"For potentially destructive git operations (force push, branch deletion, reset --hard), confirm with the user before running.",

		// Agentic Task Execution
		"## Agentic Task Execution\n" +
			"When the user approves a plan (responds with 'yes', 'proceed', 'go ahead', 'begin', '/proceed', or any equivalent confirmation), " +
			"you MUST immediately begin executing the approved task graph. Do not re-explain the plan or ask for further confirmation.\n\n" +
			"Planning protocol:\n" +
			"1. Create task contracts with task_create(title, goal, acceptance_criteria, allowed_tools, blocked_by).\n" +
			"2. Call request_plan_approval(summary) only after the task graph is complete.\n" +
			"3. After approval, the runtime executes the approved tasks through scoped subagents.\n" +
			"4. In orchestrator mode, do not directly implement code with file, shell, or edit tools.\n\n" +
			"A task is only complete when the delegated subagent returns a verified result or a blocker is surfaced explicitly.",
	}, "\n\n")
}

// resolveModel expands short aliases to their full model names.
// An empty string is returned as-is — callers must validate before use.
// Provider routing happens later in api.ResolveModelRoute.
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

func contextAwareMaxTokens(request api.MessageRequest, requested int, observed requestTokenObservation) int {
	desired := max(256, requested)
	contextWindow := modelContextWindow(request.Model)
	if contextWindow <= 0 {
		return desired
	}
	estimatedInput := estimateRequestInputTokens(request)
	if observed.InputTokens > 0 && observed.MessageCount >= 0 && observed.MessageCount <= len(request.Messages) {
		estimatedInput = max(estimatedInput, observed.InputTokens+estimateMessagesInputTokens(request.Messages[observed.MessageCount:]))
	}
	remaining := contextWindow - estimatedInput - contextBudgetSafetyTokens(contextWindow)
	switch {
	case remaining >= desired:
		return desired
	case remaining > 0:
		return remaining
	default:
		return 1
	}
}

func modelContextWindow(model string) int {
	normalized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case normalized == "qwen3.6-30b-a3b", strings.Contains(normalized, "qwen3.6-35b-a3b"):
		return 16384
	case normalized == "glm-4.7-flash":
		return 32768
	default:
		return 0
	}
}

func contextBudgetSafetyTokens(contextWindow int) int {
	return max(384, contextWindow/24)
}

func estimateRequestInputTokens(request api.MessageRequest) int {
	total := estimateSystemTokenCount(request.System) + 24
	total += estimateMessagesInputTokens(request.Messages)
	total += estimateToolsInputTokens(request.Tools)
	if request.ToolChoice != nil {
		total += 16
		total += estimateTokenCount(request.ToolChoice.Type)
		total += estimateTokenCount(request.ToolChoice.Name)
	}
	return max(1, total)
}

func estimateMessagesInputTokens(messages []api.InputMessage) int {
	total := 0
	for _, message := range messages {
		total += 12
		total += estimateTokenCount(message.Role)
		for _, block := range message.Content {
			total += 8
			total += estimateTokenCount(block.Type)
			total += estimateTokenCount(block.Text)
			total += estimateTokenCount(block.ID)
			total += estimateTokenCount(block.Name)
			total += estimateTokenCount(block.ToolUseID)
			total += estimateRawJSONTokens(block.Input)
			for _, item := range block.Content {
				total += 4
				total += estimateTokenCount(item.Type)
				total += estimateTokenCount(item.Text)
				total += estimateRawJSONTokens(item.Value)
			}
		}
	}
	return total
}

func estimateToolsInputTokens(tools []api.ToolDefinition) int {
	total := 0
	for _, tool := range tools {
		total += 24
		total += estimateTokenCount(tool.Name)
		total += estimateSystemTokenCount(tool.Description)
		total += estimateRawJSONTokens(tool.InputSchema)
	}
	return total
}

func estimateSystemTokenCount(text string) int {
	return estimateTokenCountWithDivisor(text, 5)
}

func estimateTokenCount(text string) int {
	return estimateTokenCountWithDivisor(text, 4)
}

func estimateTokenCountWithDivisor(text string, divisor int) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	return (len(text) + divisor - 1) / divisor
}

func estimateRawJSONTokens(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	return (len(raw) + 3) / 4
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
