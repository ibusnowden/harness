package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"ascaris/internal/config"
	"ascaris/internal/tools"
)

type Event string

const (
	EventPreToolUse         Event = "PreToolUse"
	EventPostToolUse        Event = "PostToolUse"
	EventPostToolUseFailure Event = "PostToolUseFailure"
)

type RunResult struct {
	Denied             bool
	Failed             bool
	Cancelled          bool
	Messages           []string
	PermissionOverride tools.PermissionMode
	PermissionReason   string
	UpdatedInput       string
}

func Allow(messages ...string) RunResult {
	return RunResult{Messages: compactMessages(messages...)}
}

type Runner struct {
	settings config.HookSettings
}

func New(settings config.HookSettings) Runner {
	return Runner{settings: settings}
}

func FromConfig(cfg config.RuntimeConfig) Runner {
	return New(cfg.Hooks())
}

func (r Runner) RunPreToolUse(toolName, toolInput string) RunResult {
	return runCommands(EventPreToolUse, r.settings.PreToolUse, toolName, toolInput, "", false)
}

func (r Runner) RunPostToolUse(toolName, toolInput, toolOutput string, isError bool) RunResult {
	return runCommands(EventPostToolUse, r.settings.PostToolUse, toolName, toolInput, toolOutput, isError)
}

func (r Runner) RunPostToolUseFailure(toolName, toolInput, toolError string) RunResult {
	return runCommands(EventPostToolUseFailure, r.settings.PostToolUseFailure, toolName, toolInput, toolError, true)
}

func runCommands(event Event, commands []string, toolName, toolInput, toolOutput string, isError bool) RunResult {
	if len(commands) == 0 {
		return Allow()
	}
	payload := buildPayload(event, toolName, toolInput, toolOutput, isError)
	encodedPayload, _ := json.Marshal(payload)
	result := Allow()
	for _, command := range commands {
		outcome := runCommand(event, command, toolName, toolInput, toolOutput, isError, encodedPayload)
		result.Messages = append(result.Messages, outcome.Messages...)
		if outcome.PermissionOverride != "" {
			result.PermissionOverride = outcome.PermissionOverride
			result.PermissionReason = outcome.PermissionReason
		}
		if strings.TrimSpace(outcome.UpdatedInput) != "" {
			result.UpdatedInput = outcome.UpdatedInput
		}
		if outcome.Denied || outcome.Failed || outcome.Cancelled {
			result.Denied = outcome.Denied
			result.Failed = outcome.Failed
			result.Cancelled = outcome.Cancelled
			return result
		}
	}
	return result
}

func runCommand(event Event, command, toolName, toolInput, toolOutput string, isError bool, payload []byte) RunResult {
	cmd := shellCommand(command)
	cmd.Env = append(cmd.Environ(),
		"HOOK_EVENT="+string(event),
		"HOOK_TOOL_NAME="+toolName,
		"HOOK_TOOL_INPUT="+toolInput,
	)
	if toolOutput != "" {
		if isError {
			cmd.Env = append(cmd.Env, "HOOK_TOOL_ERROR="+toolOutput)
		}
		cmd.Env = append(cmd.Env, "HOOK_TOOL_OUTPUT="+toolOutput)
	}
	if isError {
		cmd.Env = append(cmd.Env, "HOOK_TOOL_IS_ERROR=1")
	} else {
		cmd.Env = append(cmd.Env, "HOOK_TOOL_IS_ERROR=0")
	}
	cmd.Stdin = bytes.NewReader(payload)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	rawStdout := strings.TrimSpace(stdout.String())
	rawStderr := strings.TrimSpace(stderr.String())
	parsed := parseHookOutput(rawStdout)
	switch {
	case err == nil:
		return parsed
	default:
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			return RunResult{
				Failed:   true,
				Messages: compactMessages(fmt.Sprintf("%s hook `%s` failed to start for `%s`: %v", event, command, toolName, err)),
			}
		}
		if exitErr.ExitCode() == 2 {
			parsed.Denied = true
			if len(parsed.Messages) == 0 {
				parsed.Messages = []string{fmt.Sprintf("%s hook denied tool `%s`", event, toolName)}
			}
			return parsed
		}
		message := fmt.Sprintf("Hook `%s` exited with status %d", command, exitErr.ExitCode())
		if rawStdout != "" {
			message += ": " + rawStdout
		}
		if rawStderr != "" {
			message += " (" + rawStderr + ")"
		}
		return RunResult{
			Failed:   true,
			Messages: compactMessages(message),
		}
	}
}

func buildPayload(event Event, toolName, toolInput, toolOutput string, isError bool) map[string]any {
	base := map[string]any{
		"hook_event_name":      string(event),
		"tool_name":            toolName,
		"tool_input_json":      toolInput,
		"tool_result_is_error": isError,
	}
	if parsed := parseJSONObject(toolInput); parsed != nil {
		base["tool_input"] = parsed
	} else {
		base["tool_input"] = toolInput
	}
	if event == EventPostToolUseFailure {
		base["tool_error"] = toolOutput
	} else {
		base["tool_output"] = toolOutput
	}
	return base
}

func parseHookOutput(stdout string) RunResult {
	if strings.TrimSpace(stdout) == "" {
		return Allow()
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		return Allow(stdout)
	}
	result := Allow()
	message := stringFromNested(parsed, "systemMessage")
	if message == "" {
		message = stringFromNested(parsed, "message")
	}
	if hookSpecific, ok := parsed["hookSpecificOutput"].(map[string]any); ok {
		if message == "" {
			message = stringFromAnyMap(hookSpecific, "message")
		}
		if updated := stringFromAnyMap(hookSpecific, "updatedInput"); updated != "" {
			result.UpdatedInput = updated
		}
		if decision := permissionMode(stringFromAnyMap(hookSpecific, "permissionDecision")); decision != "" {
			result.PermissionOverride = decision
			result.PermissionReason = stringFromAnyMap(hookSpecific, "permissionDecisionReason")
		}
	}
	if updated := stringFromNested(parsed, "updatedInput"); updated != "" {
		result.UpdatedInput = updated
	}
	if decision := permissionMode(stringFromNested(parsed, "permissionDecision")); decision != "" {
		result.PermissionOverride = decision
		if result.PermissionReason == "" {
			result.PermissionReason = stringFromNested(parsed, "permissionDecisionReason")
		}
	}
	result.Messages = compactMessages(message)
	return result
}

func parseJSONObject(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	return parsed
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-lc", command)
}

func compactMessages(messages ...string) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		if strings.TrimSpace(message) != "" {
			out = append(out, strings.TrimSpace(message))
		}
	}
	return out
}

func permissionMode(value string) tools.PermissionMode {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case string(tools.PermissionReadOnly):
		return tools.PermissionReadOnly
	case string(tools.PermissionWorkspaceWrite), "allow":
		return tools.PermissionWorkspaceWrite
	case string(tools.PermissionDangerFullAccess):
		return tools.PermissionDangerFullAccess
	default:
		return ""
	}
}

func stringFromNested(root map[string]any, key string) string {
	return stringFromAnyMap(root, key)
}

func stringFromAnyMap(root map[string]any, key string) string {
	if root == nil {
		return ""
	}
	value, ok := root[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}
