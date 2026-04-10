package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"ascaris/internal/api"
)

type PermissionMode string

const (
	PermissionReadOnly         PermissionMode = "read-only"
	PermissionWorkspaceWrite   PermissionMode = "workspace-write"
	PermissionDangerFullAccess PermissionMode = "danger-full-access"
)

type ApprovalPrompter interface {
	Approve(toolName string, input string) (bool, error)
}

type LiveCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type LiveResult struct {
	ToolUseID string `json:"tool_use_id"`
	Name      string `json:"name"`
	Output    string `json:"output"`
	IsError   bool   `json:"is_error"`
}

type LiveContext struct {
	Root            string
	PermissionMode  PermissionMode
	AllowedToolName map[string]struct{}
	Prompter        ApprovalPrompter
}

func LiveDefinitions(allowedTools []string) []api.ToolDefinition {
	allowed := allowlist(allowedTools)
	definitions := []api.ToolDefinition{
		toolDefinition("read_file", "Read a file from the current workspace", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		toolDefinition("write_file", "Write a file inside the current workspace", `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
		toolDefinition("edit_file", "Replace a substring inside a workspace file", `{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["path","old_string","new_string"],"additionalProperties":false}`),
		toolDefinition("glob_search", "Expand a glob pattern inside the current workspace", `{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"],"additionalProperties":false}`),
		toolDefinition("grep_search", "Search file contents for a pattern", `{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"output_mode":{"type":"string"}},"required":["pattern","path"],"additionalProperties":false}`),
		toolDefinition("bash", "Execute a shell command", `{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"integer"}},"required":["command"],"additionalProperties":false}`),
	}
	definitions = append(definitions, controlPlaneDefinitions()...)
	if len(allowed) == 0 {
		return definitions
	}
	filtered := make([]api.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if _, ok := allowed[definition.Name]; ok {
			filtered = append(filtered, definition)
		}
	}
	return filtered
}

func ExecuteLive(ctx LiveContext, call LiveCall) LiveResult {
	if len(ctx.AllowedToolName) > 0 {
		if _, ok := ctx.AllowedToolName[strings.ToLower(call.Name)]; !ok {
			return LiveResult{
				ToolUseID: call.ID,
				Name:      call.Name,
				Output:    fmt.Sprintf("%s is not in the allowed tool list", call.Name),
				IsError:   true,
			}
		}
	}
	switch call.Name {
	case "read_file":
		return executeReadFile(ctx, call)
	case "write_file":
		return executeWriteFile(ctx, call)
	case "edit_file":
		return executeEditFile(ctx, call)
	case "glob_search":
		return executeGlobSearch(ctx, call)
	case "grep_search":
		return executeGrepSearch(ctx, call)
	case "bash":
		return executeBash(ctx, call)
	default:
		if result, ok := executeControlPlaneTool(ctx, call); ok {
			return result
		}
		return LiveResult{
			ToolUseID: call.ID,
			Name:      call.Name,
			Output:    "unknown built-in tool: " + call.Name,
			IsError:   true,
		}
	}
}

func allowlist(values []string) map[string]struct{} {
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

func toolDefinition(name, description, schema string) api.ToolDefinition {
	return api.ToolDefinition{
		Name:        name,
		Description: description,
		InputSchema: json.RawMessage(schema),
	}
}

func executeReadFile(ctx LiveContext, call LiveCall) LiveResult {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid read_file input: "+err.Error())
	}
	path, err := resolveWorkspacePath(ctx.Root, input.Path)
	if err != nil {
		return liveError(call, err.Error())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return liveError(call, err.Error())
	}
	return liveJSON(call, map[string]any{
		"path":    path,
		"content": string(data),
	})
}

func executeWriteFile(ctx LiveContext, call LiveCall) LiveResult {
	if ctx.PermissionMode == PermissionReadOnly {
		return liveError(call, "write_file requires workspace-write permission")
	}
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid write_file input: "+err.Error())
	}
	path, err := resolveWorkspacePath(ctx.Root, input.Path)
	if err != nil {
		return liveError(call, err.Error())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return liveError(call, err.Error())
	}
	if err := os.WriteFile(path, []byte(input.Content), 0o644); err != nil {
		return liveError(call, err.Error())
	}
	return liveJSON(call, map[string]any{
		"path":          path,
		"bytes_written": len(input.Content),
	})
}

func executeEditFile(ctx LiveContext, call LiveCall) LiveResult {
	if ctx.PermissionMode == PermissionReadOnly {
		return liveError(call, "edit_file requires workspace-write permission")
	}
	var input struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid edit_file input: "+err.Error())
	}
	path, err := resolveWorkspacePath(ctx.Root, input.Path)
	if err != nil {
		return liveError(call, err.Error())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return liveError(call, err.Error())
	}
	content := string(data)
	if !strings.Contains(content, input.OldString) {
		return liveError(call, "old_string was not found in file")
	}
	updated := strings.Replace(content, input.OldString, input.NewString, 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return liveError(call, err.Error())
	}
	return liveJSON(call, map[string]any{
		"path": path,
	})
}

func executeGlobSearch(ctx LiveContext, call LiveCall) LiveResult {
	var input struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid glob_search input: "+err.Error())
	}
	pattern := filepath.Join(ctx.Root, input.Pattern)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return liveError(call, err.Error())
	}
	return liveJSON(call, map[string]any{
		"matches": matches,
	})
}

func executeGrepSearch(ctx LiveContext, call LiveCall) LiveResult {
	var input struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		OutputMode string `json:"output_mode"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid grep_search input: "+err.Error())
	}
	path, err := resolveWorkspacePath(ctx.Root, input.Path)
	if err != nil {
		return liveError(call, err.Error())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return liveError(call, err.Error())
	}
	re, err := regexp.Compile(input.Pattern)
	if err != nil {
		return liveError(call, "invalid grep pattern: "+err.Error())
	}
	matches := re.FindAllStringIndex(string(data), -1)
	result := map[string]any{
		"path":        path,
		"num_matches": len(matches),
	}
	if input.OutputMode != "count" {
		result["matches"] = re.FindAllString(string(data), -1)
	}
	return liveJSON(call, result)
}

func executeBash(ctx LiveContext, call LiveCall) LiveResult {
	if ctx.PermissionMode == PermissionReadOnly {
		return liveError(call, "bash requires workspace-write permission")
	}
	var input struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid bash input: "+err.Error())
	}
	if ctx.PermissionMode == PermissionWorkspaceWrite {
		if ctx.Prompter == nil {
			return liveError(call, "bash requires an approval prompter in workspace-write mode")
		}
		approved, err := ctx.Prompter.Approve(call.Name, string(call.Input))
		if err != nil {
			return liveError(call, err.Error())
		}
		if !approved {
			return liveError(call, "bash denied by user approval prompt")
		}
	}
	command := exec.Command("zsh", "-lc", input.Command)
	command.Dir = ctx.Root
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return liveError(call, err.Error())
		}
	}
	output := map[string]any{
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"exit_code": exitCode,
	}
	if exitCode != 0 {
		data, _ := json.Marshal(output)
		return LiveResult{
			ToolUseID: call.ID,
			Name:      call.Name,
			Output:    string(data),
			IsError:   true,
		}
	}
	return liveJSON(call, output)
}

func resolveWorkspacePath(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(cleanRoot, candidate)
	}
	cleanPath := filepath.Clean(candidate)
	relative, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace", path)
	}
	return cleanPath, nil
}

func liveJSON(call LiveCall, payload map[string]any) LiveResult {
	data, _ := json.Marshal(payload)
	return LiveResult{
		ToolUseID: call.ID,
		Name:      call.Name,
		Output:    string(data),
	}
}

func liveError(call LiveCall, message string) LiveResult {
	return LiveResult{
		ToolUseID: call.ID,
		Name:      call.Name,
		Output:    message,
		IsError:   true,
	}
}
