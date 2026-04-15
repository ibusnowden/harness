package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
	Activity        func(LiveToolEvent)
}

type LiveToolEvent struct {
	Kind    string
	Title   string
	Summary string
	Detail  string
	Error   bool
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
	emitToolActivity(ctx, LiveToolEvent{
		Kind:    "file_read",
		Title:   input.Path,
		Summary: "Reading file.",
		Detail:  path,
	})
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
	emitToolActivity(ctx, LiveToolEvent{
		Kind:    "file_write",
		Title:   input.Path,
		Summary: fmt.Sprintf("Writing %d bytes.", len(input.Content)),
		Detail:  path,
	})
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

// fileDiff is the payload written into the file_edit activity event Detail field.
// The TUI parses this to render a proper unified diff with context lines.
type fileDiff struct {
	HunkHeader string   `json:"hunk"`
	Before     []string `json:"before"` // context lines preceding the change
	Removed    []string `json:"removed"`
	Added      []string `json:"added"`
	After      []string `json:"after"` // context lines following the change
	StartLine  int      `json:"start_line"` // 1-indexed line where removed block begins
}

// buildUnifiedDiff computes a unified-diff hunk for a single exact-string replacement.
// It reads the original content, locates oldStr, extracts ±3 context lines, and returns
// a JSON-encoded fileDiff. Returns "" on any failure (caller falls back gracefully).
func buildUnifiedDiff(content, oldStr, newStr string) string {
	const ctxLines = 3
	const maxHunkLines = 12 // cap removed/added shown; very large edits stay readable

	idx := strings.Index(content, oldStr)
	if idx < 0 {
		return ""
	}

	allLines := strings.Split(content, "\n")
	startLine := strings.Count(content[:idx], "\n") + 1 // 1-indexed
	firstIdx := startLine - 1                           // 0-indexed into allLines
	oldLineCount := strings.Count(oldStr, "\n") + 1

	ctxStart := max(0, firstIdx-ctxLines)
	lastOldIdx := firstIdx + oldLineCount - 1
	ctxEnd := min(len(allLines)-1, lastOldIdx+ctxLines)

	before := cloneLines(allLines[ctxStart:firstIdx])
	removed := strings.Split(oldStr, "\n")
	added := strings.Split(newStr, "\n")

	afterStart := lastOldIdx + 1
	var after []string
	if afterStart <= ctxEnd && afterStart < len(allLines) {
		after = cloneLines(allLines[afterStart : ctxEnd+1])
	}

	// Cap removed/added to keep the diff readable for massive edits.
	truncatedRemoved := false
	if len(removed) > maxHunkLines {
		removed = append(removed[:maxHunkLines], fmt.Sprintf("… (%d more lines)", len(removed)-maxHunkLines))
		truncatedRemoved = true
	}
	if len(added) > maxHunkLines && !truncatedRemoved {
		added = append(added[:maxHunkLines], fmt.Sprintf("… (%d more lines)", len(added)-maxHunkLines))
	}

	// Unified diff hunk header: @@ -old_start,old_count +new_start,new_count @@
	oldCount := len(before) + oldLineCount + len(after)
	newLineCount := strings.Count(newStr, "\n") + 1
	newCount := len(before) + newLineCount + len(after)
	hunk := fmt.Sprintf("@@ -%d,%d +%d,%d @@", ctxStart+1, oldCount, ctxStart+1, newCount)

	payload := fileDiff{
		HunkHeader: hunk,
		Before:     before,
		Removed:    removed,
		Added:      added,
		After:      after,
		StartLine:  startLine,
	}
	enc, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(enc)
}

func cloneLines(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
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
	// Read the file first so we can compute a real unified diff with context lines
	// before emitting the activity event.
	data, err := os.ReadFile(path)
	if err != nil {
		return liveError(call, err.Error())
	}
	content := string(data)
	if !strings.Contains(content, input.OldString) {
		return liveError(call, "old_string was not found in file")
	}
	// Compute unified diff while we still have the original content.
	diffDetail := buildUnifiedDiff(content, input.OldString, input.NewString)
	if diffDetail == "" {
		diffDetail = path // graceful fallback
	}
	emitToolActivity(ctx, LiveToolEvent{
		Kind:    "file_edit",
		Title:   input.Path,
		Summary: "Editing " + input.Path,
		Detail:  diffDetail,
	})
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
	emitToolActivity(ctx, LiveToolEvent{
		Kind:    "search",
		Title:   "glob_search",
		Summary: fmt.Sprintf("Expanding glob %s.", input.Pattern),
		Detail:  pattern,
	})
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
	emitToolActivity(ctx, LiveToolEvent{
		Kind:    "search",
		Title:   "grep_search",
		Summary: fmt.Sprintf("Searching %s.", input.Path),
		Detail:  fmt.Sprintf("pattern=%s", input.Pattern),
	})
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
		emitToolActivity(ctx, LiveToolEvent{
			Kind:    "approval",
			Title:   "bash",
			Summary: "Awaiting approval for shell command.",
			Detail:  input.Command,
		})
		approved, err := ctx.Prompter.Approve(call.Name, string(call.Input))
		if err != nil {
			return liveError(call, err.Error())
		}
		if !approved {
			return liveError(call, "bash denied by user approval prompt")
		}
	}
	emitToolActivity(ctx, LiveToolEvent{
		Kind:    "bash_start",
		Title:   "bash",
		Summary: "Running shell command.",
		Detail:  input.Command,
	})
	command := shellCommand(input.Command)
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
		emitToolActivity(ctx, LiveToolEvent{
			Kind:    "bash_result",
			Title:   "bash",
			Summary: fmt.Sprintf("Shell command exited with status %d.", exitCode),
			Detail:  string(data),
			Error:   true,
		})
		return LiveResult{
			ToolUseID: call.ID,
			Name:      call.Name,
			Output:    string(data),
			IsError:   true,
		}
	}
	data, _ := json.Marshal(output)
	emitToolActivity(ctx, LiveToolEvent{
		Kind:    "bash_result",
		Title:   "bash",
		Summary: "Shell command completed successfully.",
		Detail:  string(data),
	})
	return liveJSON(call, output)
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-lc", command)
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

func emitToolActivity(ctx LiveContext, event LiveToolEvent) {
	if ctx.Activity == nil {
		return
	}
	ctx.Activity(event)
}
