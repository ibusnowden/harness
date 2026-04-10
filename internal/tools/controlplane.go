package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"ascaris/internal/api"
	controlstate "ascaris/internal/state"
)

func controlPlaneDefinitions() []api.ToolDefinition {
	return []api.ToolDefinition{
		toolDefinition("WorkerCreate", "Create a coding worker boot session with trust-gate and prompt-delivery guards.", `{"type":"object","properties":{"cwd":{"type":"string"},"trusted_roots":{"type":"array","items":{"type":"string"}},"auto_recover_prompt_misdelivery":{"type":"boolean"}},"required":["cwd"],"additionalProperties":false}`),
		toolDefinition("WorkerGet", "Fetch the current worker boot state, last error, and event history.", `{"type":"object","properties":{"worker_id":{"type":"string"}},"required":["worker_id"],"additionalProperties":false}`),
		toolDefinition("WorkerObserve", "Feed a terminal snapshot into worker boot detection to resolve trust gates, ready handshakes, and prompt misdelivery.", `{"type":"object","properties":{"worker_id":{"type":"string"},"screen_text":{"type":"string"}},"required":["worker_id","screen_text"],"additionalProperties":false}`),
		toolDefinition("WorkerResolveTrust", "Resolve a detected trust prompt so worker boot can continue.", `{"type":"object","properties":{"worker_id":{"type":"string"}},"required":["worker_id"],"additionalProperties":false}`),
		toolDefinition("WorkerAwaitReady", "Return the current ready-handshake verdict for a coding worker.", `{"type":"object","properties":{"worker_id":{"type":"string"}},"required":["worker_id"],"additionalProperties":false}`),
		toolDefinition("WorkerSendPrompt", "Send a task prompt only after the worker reaches ready_for_prompt; can replay a recovered prompt.", `{"type":"object","properties":{"worker_id":{"type":"string"},"prompt":{"type":"string"}},"required":["worker_id"],"additionalProperties":false}`),
		toolDefinition("WorkerRestart", "Restart worker boot state after a failed or stale startup.", `{"type":"object","properties":{"worker_id":{"type":"string"}},"required":["worker_id"],"additionalProperties":false}`),
		toolDefinition("WorkerTerminate", "Terminate a worker and mark the lane finished from the control plane.", `{"type":"object","properties":{"worker_id":{"type":"string"}},"required":["worker_id"],"additionalProperties":false}`),
		toolDefinition("TeamCreate", "Create a team of sub-agents for parallel task execution.", `{"type":"object","properties":{"name":{"type":"string"},"tasks":{"type":"array","items":{"type":"object","properties":{"task_id":{"type":"string"},"prompt":{"type":"string"},"description":{"type":"string"}}}}},"required":["name","tasks"],"additionalProperties":false}`),
		toolDefinition("TeamDelete", "Delete a team and stop all its running tasks.", `{"type":"object","properties":{"team_id":{"type":"string"}},"required":["team_id"],"additionalProperties":false}`),
		toolDefinition("CronCreate", "Create a scheduled recurring task.", `{"type":"object","properties":{"schedule":{"type":"string"},"prompt":{"type":"string"},"description":{"type":"string"}},"required":["schedule","prompt"],"additionalProperties":false}`),
		toolDefinition("CronDelete", "Delete a scheduled recurring task by ID.", `{"type":"object","properties":{"cron_id":{"type":"string"}},"required":["cron_id"],"additionalProperties":false}`),
		toolDefinition("CronList", "List all scheduled recurring tasks.", `{"type":"object","properties":{},"additionalProperties":false}`),
	}
}

func executeControlPlaneTool(ctx LiveContext, call LiveCall) (LiveResult, bool) {
	switch call.Name {
	case "WorkerCreate":
		return executeWorkerCreate(ctx, call), true
	case "WorkerGet":
		return executeWorkerGet(ctx, call), true
	case "WorkerObserve":
		return executeWorkerObserve(ctx, call), true
	case "WorkerResolveTrust":
		return executeWorkerResolveTrust(ctx, call), true
	case "WorkerAwaitReady":
		return executeWorkerAwaitReady(ctx, call), true
	case "WorkerSendPrompt":
		return executeWorkerSendPrompt(ctx, call), true
	case "WorkerRestart":
		return executeWorkerRestart(ctx, call), true
	case "WorkerTerminate":
		return executeWorkerTerminate(ctx, call), true
	case "TeamCreate":
		return executeTeamCreate(ctx, call), true
	case "TeamDelete":
		return executeTeamDelete(ctx, call), true
	case "CronCreate":
		return executeCronCreate(ctx, call), true
	case "CronDelete":
		return executeCronDelete(ctx, call), true
	case "CronList":
		return executeCronList(ctx, call), true
	default:
		return LiveResult{}, false
	}
}

func executeWorkerCreate(ctx LiveContext, call LiveCall) LiveResult {
	if err := requirePermission(call.Name, ctx.PermissionMode, PermissionDangerFullAccess); err != nil {
		return liveError(call, err.Error())
	}
	var input struct {
		CWD                          string   `json:"cwd"`
		TrustedRoots                 []string `json:"trusted_roots"`
		AutoRecoverPromptMisdelivery *bool    `json:"auto_recover_prompt_misdelivery"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid WorkerCreate input: "+err.Error())
	}
	if strings.TrimSpace(input.CWD) == "" {
		return liveError(call, "cwd is required")
	}
	registry, err := controlstate.LoadWorkerRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	cwd := filepath.Clean(input.CWD)
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(ctx.Root, cwd)
	}
	autoRecover := true
	if input.AutoRecoverPromptMisdelivery != nil {
		autoRecover = *input.AutoRecoverPromptMisdelivery
	}
	worker := registry.Create(cwd, input.TrustedRoots, autoRecover)
	if err := controlstate.SaveWorkerRegistry(ctx.Root, registry); err != nil {
		return liveError(call, err.Error())
	}
	return liveJSONAny(call, worker)
}

func executeWorkerGet(ctx LiveContext, call LiveCall) LiveResult {
	var input struct {
		WorkerID string `json:"worker_id"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid WorkerGet input: "+err.Error())
	}
	registry, err := controlstate.LoadWorkerRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	worker, ok := registry.Get(input.WorkerID)
	if !ok {
		return liveError(call, "worker not found: "+input.WorkerID)
	}
	return liveJSONAny(call, worker)
}

func executeWorkerObserve(ctx LiveContext, call LiveCall) LiveResult {
	var input struct {
		WorkerID   string `json:"worker_id"`
		ScreenText string `json:"screen_text"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid WorkerObserve input: "+err.Error())
	}
	registry, err := controlstate.LoadWorkerRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	worker, err := registry.Observe(input.WorkerID, input.ScreenText)
	if err != nil {
		return liveError(call, err.Error())
	}
	if err := controlstate.SaveWorkerRegistry(ctx.Root, registry); err != nil {
		return liveError(call, err.Error())
	}
	return liveJSONAny(call, worker)
}

func executeWorkerResolveTrust(ctx LiveContext, call LiveCall) LiveResult {
	if err := requirePermission(call.Name, ctx.PermissionMode, PermissionDangerFullAccess); err != nil {
		return liveError(call, err.Error())
	}
	var input struct {
		WorkerID string `json:"worker_id"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid WorkerResolveTrust input: "+err.Error())
	}
	registry, err := controlstate.LoadWorkerRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	worker, err := registry.ResolveTrust(input.WorkerID)
	if err != nil {
		return liveError(call, err.Error())
	}
	if err := controlstate.SaveWorkerRegistry(ctx.Root, registry); err != nil {
		return liveError(call, err.Error())
	}
	return liveJSONAny(call, worker)
}

func executeWorkerAwaitReady(ctx LiveContext, call LiveCall) LiveResult {
	var input struct {
		WorkerID string `json:"worker_id"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid WorkerAwaitReady input: "+err.Error())
	}
	registry, err := controlstate.LoadWorkerRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	snapshot, err := registry.AwaitReady(input.WorkerID)
	if err != nil {
		return liveError(call, err.Error())
	}
	return liveJSONAny(call, snapshot)
}

func executeWorkerSendPrompt(ctx LiveContext, call LiveCall) LiveResult {
	if err := requirePermission(call.Name, ctx.PermissionMode, PermissionDangerFullAccess); err != nil {
		return liveError(call, err.Error())
	}
	var input struct {
		WorkerID string `json:"worker_id"`
		Prompt   string `json:"prompt"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid WorkerSendPrompt input: "+err.Error())
	}
	registry, err := controlstate.LoadWorkerRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	worker, err := registry.SendPrompt(input.WorkerID, input.Prompt)
	if err != nil {
		return liveError(call, err.Error())
	}
	if err := controlstate.SaveWorkerRegistry(ctx.Root, registry); err != nil {
		return liveError(call, err.Error())
	}
	return liveJSONAny(call, worker)
}

func executeWorkerRestart(ctx LiveContext, call LiveCall) LiveResult {
	if err := requirePermission(call.Name, ctx.PermissionMode, PermissionDangerFullAccess); err != nil {
		return liveError(call, err.Error())
	}
	var input struct {
		WorkerID string `json:"worker_id"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid WorkerRestart input: "+err.Error())
	}
	registry, err := controlstate.LoadWorkerRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	worker, err := registry.Restart(input.WorkerID)
	if err != nil {
		return liveError(call, err.Error())
	}
	if err := controlstate.SaveWorkerRegistry(ctx.Root, registry); err != nil {
		return liveError(call, err.Error())
	}
	return liveJSONAny(call, worker)
}

func executeWorkerTerminate(ctx LiveContext, call LiveCall) LiveResult {
	if err := requirePermission(call.Name, ctx.PermissionMode, PermissionDangerFullAccess); err != nil {
		return liveError(call, err.Error())
	}
	var input struct {
		WorkerID string `json:"worker_id"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid WorkerTerminate input: "+err.Error())
	}
	registry, err := controlstate.LoadWorkerRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	worker, err := registry.Terminate(input.WorkerID)
	if err != nil {
		return liveError(call, err.Error())
	}
	if err := controlstate.SaveWorkerRegistry(ctx.Root, registry); err != nil {
		return liveError(call, err.Error())
	}
	return liveJSONAny(call, worker)
}

func executeTeamCreate(ctx LiveContext, call LiveCall) LiveResult {
	if err := requirePermission(call.Name, ctx.PermissionMode, PermissionDangerFullAccess); err != nil {
		return liveError(call, err.Error())
	}
	var input struct {
		Name  string                   `json:"name"`
		Tasks []map[string]interface{} `json:"tasks"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid TeamCreate input: "+err.Error())
	}
	registry, err := controlstate.LoadTeamRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	taskIDs := make([]string, 0, len(input.Tasks))
	for _, task := range input.Tasks {
		if raw, ok := task["task_id"].(string); ok && strings.TrimSpace(raw) != "" {
			taskIDs = append(taskIDs, strings.TrimSpace(raw))
		}
	}
	team := registry.Create(input.Name, taskIDs)
	if err := controlstate.SaveTeamRegistry(ctx.Root, registry); err != nil {
		return liveError(call, err.Error())
	}
	return liveJSONAny(call, map[string]any{
		"team_id":    team.TeamID,
		"name":       team.Name,
		"task_count": len(team.TaskIDs),
		"task_ids":   team.TaskIDs,
		"status":     team.Status,
		"created_at": team.CreatedAt,
	})
}

func executeTeamDelete(ctx LiveContext, call LiveCall) LiveResult {
	if err := requirePermission(call.Name, ctx.PermissionMode, PermissionDangerFullAccess); err != nil {
		return liveError(call, err.Error())
	}
	var input struct {
		TeamID string `json:"team_id"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid TeamDelete input: "+err.Error())
	}
	registry, err := controlstate.LoadTeamRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	team, err := registry.Delete(input.TeamID)
	if err != nil {
		return liveError(call, err.Error())
	}
	if err := controlstate.SaveTeamRegistry(ctx.Root, registry); err != nil {
		return liveError(call, err.Error())
	}
	return liveJSONAny(call, map[string]any{
		"team_id": team.TeamID,
		"name":    team.Name,
		"status":  team.Status,
		"message": "Team deleted",
	})
}

func executeCronCreate(ctx LiveContext, call LiveCall) LiveResult {
	if err := requirePermission(call.Name, ctx.PermissionMode, PermissionDangerFullAccess); err != nil {
		return liveError(call, err.Error())
	}
	var input struct {
		Schedule    string  `json:"schedule"`
		Prompt      string  `json:"prompt"`
		Description *string `json:"description"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid CronCreate input: "+err.Error())
	}
	registry, err := controlstate.LoadCronRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	entry := registry.Create(input.Schedule, input.Prompt, input.Description)
	if err := controlstate.SaveCronRegistry(ctx.Root, registry); err != nil {
		return liveError(call, err.Error())
	}
	return liveJSONAny(call, entry)
}

func executeCronDelete(ctx LiveContext, call LiveCall) LiveResult {
	if err := requirePermission(call.Name, ctx.PermissionMode, PermissionDangerFullAccess); err != nil {
		return liveError(call, err.Error())
	}
	var input struct {
		CronID string `json:"cron_id"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid CronDelete input: "+err.Error())
	}
	registry, err := controlstate.LoadCronRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	entry, err := registry.Delete(input.CronID)
	if err != nil {
		return liveError(call, err.Error())
	}
	if err := controlstate.SaveCronRegistry(ctx.Root, registry); err != nil {
		return liveError(call, err.Error())
	}
	return liveJSONAny(call, map[string]any{
		"cron_id":  entry.CronID,
		"schedule": entry.Schedule,
		"status":   "deleted",
		"message":  "Cron entry removed",
	})
}

func executeCronList(ctx LiveContext, call LiveCall) LiveResult {
	registry, err := controlstate.LoadCronRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	entries := registry.List(false)
	return liveJSONAny(call, map[string]any{
		"crons": entries,
		"count": len(entries),
	})
}

func requirePermission(toolName string, current, required PermissionMode) error {
	switch required {
	case PermissionReadOnly:
		return nil
	case PermissionWorkspaceWrite:
		if current == PermissionReadOnly {
			return fmt.Errorf("%s requires workspace-write permission", toolName)
		}
		return nil
	case PermissionDangerFullAccess:
		if current != PermissionDangerFullAccess {
			return fmt.Errorf("%s requires danger-full-access permission", toolName)
		}
		return nil
	default:
		return nil
	}
}

func liveJSONAny(call LiveCall, payload any) LiveResult {
	data, _ := json.Marshal(payload)
	return LiveResult{
		ToolUseID: call.ID,
		Name:      call.Name,
		Output:    string(data),
	}
}
