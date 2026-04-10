package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ascaris/internal/api"
	"ascaris/internal/config"
	"ascaris/internal/hooks"
	"ascaris/internal/mcp"
	"ascaris/internal/plugins"
	"ascaris/internal/policy"
	"ascaris/internal/recovery"
	"ascaris/internal/stalebranch"
	workerstate "ascaris/internal/state"
	"ascaris/internal/tools"
)

type liveRuntime struct {
	root           string
	config         config.RuntimeConfig
	options        PromptOptions
	currentPrompt  string
	pluginManager  plugins.Manager
	pluginTools    []plugins.ToolDefinition
	mcpRegistry    *mcp.Registry
	hookRunner     hooks.Runner
	workerRegistry *workerstate.Registry
	workerID       string
}

func newLiveRuntime(root string, runtimeConfig config.RuntimeConfig, options PromptOptions, prompt string) (*liveRuntime, error) {
	workers := workerstate.NewRegistry()
	if snapshot, err := workerstate.Load(root); err == nil {
		workers.Replace(snapshot)
	}
	worker := workers.Create(root, []string{root}, true)
	runtime := &liveRuntime{
		root:           root,
		config:         runtimeConfig,
		options:        options,
		currentPrompt:  prompt,
		pluginManager:  plugins.NewManager(root, runtimeConfig),
		mcpRegistry:    mcp.FromConfig(runtimeConfig),
		hookRunner:     hooks.New(runtimeConfig.Hooks()),
		workerRegistry: workers,
		workerID:       worker.WorkerID,
	}
	if err := runtime.bootstrap(context.Background()); err != nil {
		return nil, err
	}
	return runtime, nil
}

func (r *liveRuntime) bootstrap(ctx context.Context) error {
	if err := r.pluginManager.RunInit(); err != nil {
		if recoveryErr := r.handleStartupFailure(ctx, recovery.ScenarioPartialPluginStartup, err); recoveryErr != nil {
			return recoveryErr
		}
	}
	if err := r.refreshPluginState(); err != nil {
		if recoveryErr := r.handleStartupFailure(ctx, recovery.ScenarioPartialPluginStartup, err); recoveryErr != nil {
			return recoveryErr
		}
	}
	if err := r.refreshMCPState(ctx); err != nil {
		if recoveryErr := r.handleStartupFailure(ctx, recovery.ScenarioMCPHandshakeFailure, err); recoveryErr != nil {
			return recoveryErr
		}
	}
	if err := r.runPreflight(ctx); err != nil {
		return err
	}
	if _, err := r.workerRegistry.Observe(r.workerID, "Ascaris> ready for prompt"); err == nil {
		r.saveWorkerState()
	}
	if _, err := r.workerRegistry.SendPrompt(r.workerID, r.currentPrompt); err == nil {
		r.saveWorkerState()
	}
	return nil
}

func (r *liveRuntime) close() {
	_ = r.pluginManager.RunShutdown()
	if r.workerRegistry != nil && r.workerID != "" {
		_, _ = r.workerRegistry.Terminate(r.workerID)
		r.saveWorkerState()
	}
}

func (r *liveRuntime) Definitions(allowedTools []string) []api.ToolDefinition {
	definitions := append([]api.ToolDefinition{}, tools.LiveDefinitions(allowedTools)...)
	allowed := toAllowMap(allowedTools)
	for _, tool := range r.pluginTools {
		if len(allowed) > 0 {
			if _, ok := allowed[strings.ToLower(tool.Name)]; !ok {
				continue
			}
		}
		definitions = append(definitions, api.ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: append(json.RawMessage(nil), tool.InputSchema...),
		})
	}
	for _, definition := range r.mcpRegistry.ToolDefinitions() {
		if len(allowed) > 0 {
			if _, ok := allowed[strings.ToLower(definition.Name)]; !ok {
				continue
			}
		}
		definitions = append(definitions, definition)
	}
	return definitions
}

func (r *liveRuntime) ExecuteTool(ctx context.Context, call tools.LiveCall) tools.LiveResult {
	rawInput := compactJSON(call.Input)
	pre := r.hookRunner.RunPreToolUse(call.Name, rawInput)
	if pre.Denied {
		return tools.LiveResult{ToolUseID: call.ID, Name: call.Name, Output: strings.Join(pre.Messages, "\n"), IsError: true}
	}
	if pre.Failed {
		return tools.LiveResult{ToolUseID: call.ID, Name: call.Name, Output: strings.Join(pre.Messages, "\n"), IsError: true}
	}
	effectiveCall := call
	if strings.TrimSpace(pre.UpdatedInput) != "" {
		effectiveCall.Input = json.RawMessage(pre.UpdatedInput)
		rawInput = pre.UpdatedInput
	}
	permissionMode := r.options.PermissionMode
	if pre.PermissionOverride != "" {
		permissionMode = pre.PermissionOverride
	}
	result := r.executeByKind(ctx, effectiveCall, permissionMode)
	if result.IsError {
		r.recordToolFailure(ctx, result.Name)
		post := r.hookRunner.RunPostToolUseFailure(result.Name, rawInput, result.Output)
		if len(post.Messages) > 0 {
			result.Output = strings.TrimSpace(result.Output + "\n" + strings.Join(post.Messages, "\n"))
		}
		return result
	}
	post := r.hookRunner.RunPostToolUse(result.Name, rawInput, result.Output, false)
	if len(post.Messages) > 0 {
		result.Output = strings.TrimSpace(result.Output + "\n" + strings.Join(post.Messages, "\n"))
	}
	return result
}

func (r *liveRuntime) StreamMessage(ctx context.Context, client api.MessageClient, request api.MessageRequest) (api.MessageResponse, error) {
	attemptedRecovery := false
	for {
		response, err := client.StreamMessage(ctx, request)
		if err != nil {
			if attemptedRecovery {
				return api.MessageResponse{}, err
			}
			if r.attemptRecovery(ctx, recovery.ScenarioProviderFailure).Kind == recovery.ResultRecovered {
				attemptedRecovery = true
				continue
			}
			return api.MessageResponse{}, err
		}
		failure := r.observeCompletion(response)
		if failure == nil {
			return response, nil
		}
		if attemptedRecovery {
			return api.MessageResponse{}, errors.New(failure.Message)
		}
		if r.attemptRecovery(ctx, recovery.ScenarioProviderFailure).Kind != recovery.ResultRecovered {
			return api.MessageResponse{}, errors.New(failure.Message)
		}
		attemptedRecovery = true
	}
}

func (r *liveRuntime) recordToolFailure(ctx context.Context, name string) {
	switch {
	case strings.HasPrefix(name, "mcp__"):
		_ = r.attemptRecovery(ctx, recovery.ScenarioMCPHandshakeFailure)
	default:
		for _, tool := range r.pluginTools {
			if tool.Name == name {
				_ = r.attemptRecovery(ctx, recovery.ScenarioPartialPluginStartup)
				return
			}
		}
	}
}

func (r *liveRuntime) observeCompletion(response api.MessageResponse) *workerstate.WorkerFailure {
	if r.workerRegistry == nil || r.workerID == "" {
		return nil
	}
	worker, err := r.workerRegistry.ObserveCompletion(r.workerID, response.StopReason, response.Usage.OutputTokens)
	if err != nil {
		return nil
	}
	r.saveWorkerState()
	if worker.LastError != nil && worker.LastError.Kind == workerstate.WorkerFailureProvider {
		return worker.LastError
	}
	return nil
}

func (r *liveRuntime) attemptRecovery(ctx context.Context, scenario recovery.FailureScenario) recovery.Result {
	if r.workerRegistry == nil || r.workerID == "" {
		return recovery.Result{Kind: recovery.ResultEscalationRequired, Reason: "worker registry unavailable"}
	}
	_, result, err := r.workerRegistry.ApplyRecoveryWithExecutor(r.workerID, scenario, func(step recovery.Step) error {
		return r.executeRecoveryStep(ctx, scenario, step)
	})
	if err != nil {
		return recovery.Result{Kind: recovery.ResultEscalationRequired, Reason: err.Error()}
	}
	r.saveWorkerState()
	return result
}

func (r *liveRuntime) executeRecoveryStep(ctx context.Context, scenario recovery.FailureScenario, step recovery.Step) error {
	switch step.Kind {
	case recovery.StepAcceptTrustPrompt:
		if _, err := r.workerRegistry.ResolveTrust(r.workerID); err != nil {
			return err
		}
		_, _ = r.workerRegistry.Observe(r.workerID, "Ascaris> ready for prompt")
		return nil
	case recovery.StepRedirectPrompt:
		_, _ = r.workerRegistry.Observe(r.workerID, "Ascaris> ready for prompt")
		_, err := r.workerRegistry.SendPrompt(r.workerID, "")
		return err
	case recovery.StepRebaseBranch:
		return rebaseCurrentBranch(r.root)
	case recovery.StepCleanBuild:
		return runCleanBuildProbe(r.root)
	case recovery.StepRetryMCPHandshake:
		return r.refreshMCPState(ctx)
	case recovery.StepRestartPlugin:
		_ = r.pluginManager.RunShutdown()
		if err := r.pluginManager.RunInit(); err != nil {
			return err
		}
		if err := r.refreshPluginState(); err != nil {
			return err
		}
		return r.refreshMCPState(ctx)
	case recovery.StepRestartWorker:
		if _, err := r.workerRegistry.Restart(r.workerID); err != nil {
			return err
		}
		if _, err := r.workerRegistry.Observe(r.workerID, "Ascaris> ready for prompt"); err != nil {
			return err
		}
		_, err := r.workerRegistry.SendPrompt(r.workerID, r.currentPrompt)
		return err
	default:
		if scenario == recovery.ScenarioCompileRedCrossCrate {
			return runCleanBuildProbe(r.root)
		}
		return nil
	}
}

func (r *liveRuntime) executeByKind(_ context.Context, call tools.LiveCall, permissionMode tools.PermissionMode) tools.LiveResult {
	if builtIn := tools.ExecuteLive(tools.LiveContext{
		Root:            r.root,
		PermissionMode:  permissionMode,
		AllowedToolName: toAllowMap(r.options.AllowedTools),
		Prompter:        r.options.Prompter,
	}, call); !strings.HasPrefix(builtIn.Output, "unknown built-in tool:") || !builtIn.IsError {
		return builtIn
	}
	for _, tool := range r.pluginTools {
		if tool.Name != call.Name {
			continue
		}
		if !permitsPluginPermission(permissionMode, tool.RequiredPermission) {
			return tools.LiveResult{ToolUseID: call.ID, Name: call.Name, Output: fmt.Sprintf("%s requires %s permission", call.Name, tool.RequiredPermission), IsError: true}
		}
		output, err := r.pluginManager.ExecuteTool(tool, call.Input)
		return tools.LiveResult{ToolUseID: call.ID, Name: call.Name, Output: outputOrError(output, err), IsError: err != nil}
	}
	if strings.HasPrefix(call.Name, "mcp__") {
		if permissionMode == tools.PermissionReadOnly {
			return tools.LiveResult{ToolUseID: call.ID, Name: call.Name, Output: "MCP tool calls require workspace-write permission", IsError: true}
		}
		if permissionMode == tools.PermissionWorkspaceWrite && r.options.Prompter != nil {
			approved, err := r.options.Prompter.Approve(call.Name, compactJSON(call.Input))
			if err != nil {
				return tools.LiveResult{ToolUseID: call.ID, Name: call.Name, Output: err.Error(), IsError: true}
			}
			if !approved {
				return tools.LiveResult{ToolUseID: call.ID, Name: call.Name, Output: "MCP tool call denied by user approval prompt", IsError: true}
			}
		}
		output, err := r.mcpRegistry.CallQualifiedTool(call.Name, call.Input)
		return tools.LiveResult{ToolUseID: call.ID, Name: call.Name, Output: outputOrError(output, err), IsError: err != nil}
	}
	return tools.LiveResult{ToolUseID: call.ID, Name: call.Name, Output: "unknown tool: " + call.Name, IsError: true}
}

func (r *liveRuntime) handleStartupFailure(ctx context.Context, scenario recovery.FailureScenario, original error) error {
	engine := defaultPolicyEngine()
	actions := engine.Evaluate(policy.LaneContext{
		LaneID:       r.workerID,
		Blocker:      policy.LaneBlockerStartup,
		ReviewStatus: policy.ReviewPending,
		DiffScope:    policy.DiffScopeFull,
	})
	for _, action := range actions {
		switch action.Kind {
		case policy.ActionRecoverOnce:
			if r.attemptRecovery(ctx, scenario).Kind == recovery.ResultRecovered {
				return nil
			}
		case policy.ActionEscalate, policy.ActionBlock:
			if action.Reason != "" {
				return fmt.Errorf("%s: %w", action.Reason, original)
			}
			return original
		}
	}
	return original
}

func (r *liveRuntime) runPreflight(ctx context.Context) error {
	branch, mainRef, ok := currentBranchAndMain(r.root)
	if !ok || branch == "" || mainRef == "" || branch == mainRef {
		return nil
	}
	freshness := stalebranch.CheckFreshness(branch, mainRef, r.root)
	if freshness.Kind == stalebranch.FreshnessFresh {
		return nil
	}
	actions := defaultPolicyEngine().Evaluate(policy.LaneContext{
		LaneID:          r.workerID,
		BranchFreshness: policy.StaleBranchThreshold + time.Minute,
		ReviewStatus:    policy.ReviewPending,
		DiffScope:       policy.DiffScopeFull,
	})
	for _, action := range actions {
		switch action.Kind {
		case policy.ActionRecoverOnce:
			if result := r.attemptRecovery(ctx, recovery.ScenarioStaleBranch); result.Kind == recovery.ResultRecovered {
				return nil
			}
			return fmt.Errorf("stale branch recovery failed for %s against %s", branch, mainRef)
		case policy.ActionBlock, policy.ActionEscalate:
			if action.Reason != "" {
				return errors.New(action.Reason)
			}
			return fmt.Errorf("stale branch blocks prompt execution")
		}
	}
	return nil
}

func (r *liveRuntime) refreshPluginState() error {
	pluginTools, err := r.pluginManager.AggregatedTools()
	if err != nil {
		return err
	}
	pluginHooks, err := r.pluginManager.AggregatedHooks()
	if err != nil {
		return err
	}
	r.pluginTools = pluginTools
	r.hookRunner = hooks.New(mergeHookSettings(r.config.Hooks(), pluginHooks))
	return nil
}

func (r *liveRuntime) refreshMCPState(_ context.Context) error {
	if err := r.mcpRegistry.Discover(); err != nil {
		return err
	}
	return nil
}

func (r *liveRuntime) saveWorkerState() {
	if r.workerRegistry == nil {
		return
	}
	_ = workerstate.Save(r.root, r.workerRegistry.Snapshot())
}

func defaultPolicyEngine() policy.Engine {
	return policy.NewEngine([]policy.Rule{
		{
			Name:      "stale-branch-recovery",
			Condition: policy.StaleBranch(),
			Action:    policy.RecoverOnce(),
			Priority:  10,
		},
		{
			Name:      "startup-recovery",
			Condition: policy.StartupBlocked(),
			Action:    policy.Chain(policy.RecoverOnce(), policy.Escalate("startup remained blocked")),
			Priority:  15,
		},
		{
			Name:      "lane-closeout",
			Condition: policy.LaneCompleted(),
			Action:    policy.Chain(policy.CloseoutLane(), policy.CleanupSession()),
			Priority:  30,
		},
	})
}

func currentBranchAndMain(root string) (string, string, bool) {
	if !statExists(filepath.Join(root, ".git")) {
		return "", "", false
	}
	branchOutput, err := runGit(root, "branch", "--show-current")
	if err != nil {
		return "", "", false
	}
	branch := strings.TrimSpace(branchOutput)
	if branch == "" {
		return "", "", false
	}
	for _, candidate := range []string{"main", "master"} {
		if _, err := runGit(root, "rev-parse", "--verify", candidate); err == nil {
			return branch, candidate, true
		}
	}
	return branch, "main", true
}

func rebaseCurrentBranch(root string) error {
	branch, mainRef, ok := currentBranchAndMain(root)
	if !ok || branch == "" || mainRef == "" || branch == mainRef {
		return nil
	}
	_, err := runGit(root, "rebase", mainRef)
	return err
}

func runCleanBuildProbe(root string) error {
	switch {
	case statExists(filepath.Join(root, "go.mod")):
		cmd := exec.Command("go", "test", "./...")
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("clean build failed: %s", strings.TrimSpace(string(output)))
		}
	case statExists(filepath.Join(root, "Cargo.toml")):
		cmd := exec.Command("cargo", "test", "--quiet")
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("clean build failed: %s", strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func runGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("%s", message)
	}
	return string(output), nil
}

func mergeHookSettings(left config.HookSettings, right plugins.Hooks) config.HookSettings {
	return config.HookSettings{
		PreToolUse:         append(append([]string{}, left.PreToolUse...), right.PreToolUse...),
		PostToolUse:        append(append([]string{}, left.PostToolUse...), right.PostToolUse...),
		PostToolUseFailure: append(append([]string{}, left.PostToolUseFailure...), right.PostToolUseFailure...),
	}
}

func permitsPluginPermission(mode tools.PermissionMode, required plugins.ToolPermission) bool {
	switch required {
	case plugins.ToolPermissionReadOnly:
		return true
	case plugins.ToolPermissionWorkspaceWrite:
		return mode == tools.PermissionWorkspaceWrite || mode == tools.PermissionDangerFullAccess
	case plugins.ToolPermissionDangerFullAccess:
		return mode == tools.PermissionDangerFullAccess
	default:
		return false
	}
}

func outputOrError(output string, err error) string {
	if err != nil {
		if strings.TrimSpace(output) != "" {
			return output
		}
		return err.Error()
	}
	return output
}

func statExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
