package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"ascaris/internal/api"
	controlstate "ascaris/internal/state"
	"ascaris/internal/subagents"
	"ascaris/internal/tasks"
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
	Context         context.Context
	PermissionMode  PermissionMode
	AllowedToolName map[string]struct{}
	Prompter        ApprovalPrompter
	Activity        func(LiveToolEvent)
	DelegateTask    func(context.Context, subagents.Assignment) (subagents.Assignment, error)
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
		toolDefinition("web_search", "Search the web when ASCARIS_ENABLE_WEB=1", `{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}},"required":["query"],"additionalProperties":false}`),
		toolDefinition("web_fetch", "Fetch a web page when ASCARIS_ENABLE_WEB=1", `{"type":"object","properties":{"url":{"type":"string"},"max_bytes":{"type":"integer"}},"required":["url"],"additionalProperties":false}`),
		toolDefinition("bash", "Execute a shell command", `{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"integer"}},"required":["command"],"additionalProperties":false}`),
		toolDefinition("task_create", "Create a task in the workspace task list", `{"type":"object","properties":{"title":{"type":"string"},"blocked_by":{"type":"array","items":{"type":"integer"}}},"required":["title"],"additionalProperties":false}`),
		toolDefinition("task_update", "Update the status of a task (open, in_progress, done, cancelled)", `{"type":"object","properties":{"id":{"type":"integer"},"status":{"type":"string"}},"required":["id","status"],"additionalProperties":false}`),
		toolDefinition("task_list", "List all tasks in the workspace task list", `{"type":"object","properties":{},"additionalProperties":false}`),
		toolDefinition("request_plan_approval", "Show the task list to the user and request approval before implementing. Call this after creating all tasks.", `{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"],"additionalProperties":false}`),
		toolDefinition("delegate_task", "Create a scoped subagent assignment and worker lane. Returns pending assignment state for an external runner.", `{"type":"object","properties":{"role":{"type":"string"},"prompt":{"type":"string"},"context":{"type":"string"},"allowed_tools":{"type":"array","items":{"type":"string"}},"acceptance_criteria":{"type":"array","items":{"type":"string"}}},"required":["prompt"],"additionalProperties":false}`),
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
	case "web_search":
		return executeWebSearch(ctx, call)
	case "web_fetch":
		return executeWebFetch(ctx, call)
	case "bash":
		return executeBash(ctx, call)
	case "task_create":
		return executeTaskCreate(ctx, call)
	case "task_update":
		return executeTaskUpdate(ctx, call)
	case "task_list":
		return executeTaskList(ctx, call)
	case "request_plan_approval":
		return executeRequestPlanApproval(ctx, call)
	case "delegate_task":
		return executeDelegateTask(ctx, call)
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
	After      []string `json:"after"`      // context lines following the change
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

func executeWebSearch(ctx LiveContext, call LiveCall) LiveResult {
	if strings.TrimSpace(os.Getenv("ASCARIS_ENABLE_WEB")) != "1" {
		return liveError(call, "web_search is disabled; set ASCARIS_ENABLE_WEB=1 to enable native web access")
	}
	var input struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid web_search input: "+err.Error())
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return liveError(call, "web_search query is required")
	}
	limit := input.Limit
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	body, err := fetchURL("https://duckduckgo.com/html/?q="+url.QueryEscape(query), 256*1024)
	if err != nil {
		return liveError(call, "web_search: "+err.Error())
	}
	return liveJSON(call, map[string]any{
		"query":   query,
		"results": extractSearchResults(string(body), limit),
	})
}

func executeWebFetch(ctx LiveContext, call LiveCall) LiveResult {
	if strings.TrimSpace(os.Getenv("ASCARIS_ENABLE_WEB")) != "1" {
		return liveError(call, "web_fetch is disabled; set ASCARIS_ENABLE_WEB=1 to enable native web access")
	}
	var input struct {
		URL      string `json:"url"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid web_fetch input: "+err.Error())
	}
	maxBytes := input.MaxBytes
	if maxBytes <= 0 || maxBytes > 512*1024 {
		maxBytes = 256 * 1024
	}
	body, err := fetchURL(input.URL, maxBytes)
	if err != nil {
		return liveError(call, "web_fetch: "+err.Error())
	}
	return liveJSON(call, map[string]any{
		"url":  strings.TrimSpace(input.URL),
		"text": strings.TrimSpace(stripHTML(string(body))),
	})
}

func fetchURL(raw string, maxBytes int) ([]byte, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("unsupported URL scheme: %s", parsed.Scheme)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("user-agent", "ascaris-web-tool/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
}

func extractSearchResults(html string, limit int) []map[string]string {
	re := regexp.MustCompile(`(?s)<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	matches := re.FindAllStringSubmatch(html, limit)
	results := make([]map[string]string, 0, len(matches))
	for _, match := range matches {
		results = append(results, map[string]string{
			"title": strings.TrimSpace(stripHTML(match[2])),
			"url":   htmlUnescape(match[1]),
		})
	}
	return results
}

func stripHTML(value string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	return strings.Join(strings.Fields(htmlUnescape(re.ReplaceAllString(value, " "))), " ")
}

func htmlUnescape(value string) string {
	replacer := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'")
	return replacer.Replace(value)
}

var bashCriticalPatterns = []string{"rm -rf /", "dd if=", ":(){:|:&};:", "mkfs", "shutdown", "reboot"}
var bashHighPatterns = []string{"rm -rf", "git push -f", "git push --force", "reset --hard", "drop table", "drop database", "git branch -d"}
var bashMediumPatterns = []string{"rm ", "mv ", "git push", "chmod ", "chown ", "sudo "}

// bashCommandTitle returns a short, scannable label for a shell command.
// It identifies the primary executable and formats the first meaningful
// argument in parentheses, e.g. "git(diff --stat)", "go(test ./...)",
// truncating to keep the label under ~32 chars.
func bashCommandTitle(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "bash"
	}
	// Take only the first line so multi-line scripts get a clean label.
	firstLine := strings.SplitN(cmd, "\n", 2)[0]
	fields := strings.Fields(firstLine)
	if len(fields) == 0 {
		return "bash"
	}
	// Executable: strip any leading path (e.g. /usr/bin/git → git).
	exe := filepath.Base(fields[0])
	if len(fields) == 1 {
		return exe
	}
	// Collect args up to ~24 chars to fit the label budget.
	args := strings.Join(fields[1:], " ")
	const maxArgs = 24
	if len(args) > maxArgs {
		args = args[:maxArgs] + "…"
	}
	return fmt.Sprintf("%s(%s)", exe, args)
}

// bashOutputPreview returns the first few lines of stderr (preferred) or
// stdout for surfacing in the activity panel on failure.
func bashOutputPreview(stdoutStr, stderrStr string) string {
	src := strings.TrimSpace(stderrStr)
	if src == "" {
		src = strings.TrimSpace(stdoutStr)
	}
	if src == "" {
		return ""
	}
	lines := strings.SplitN(src, "\n", 4)
	if len(lines) > 3 {
		lines = append(lines[:3], "…")
	}
	return strings.Join(lines, "\n")
}

func bashRiskLevel(cmd string) string {
	lower := strings.ToLower(cmd)
	for _, p := range bashCriticalPatterns {
		if strings.Contains(lower, p) {
			return "CRITICAL"
		}
	}
	for _, p := range bashHighPatterns {
		if strings.Contains(lower, p) {
			return "HIGH"
		}
	}
	for _, p := range bashMediumPatterns {
		if strings.Contains(lower, p) {
			return "MEDIUM"
		}
	}
	return "LOW"
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
	title := bashCommandTitle(input.Command)
	if ctx.PermissionMode == PermissionWorkspaceWrite {
		if ctx.Prompter == nil {
			return liveError(call, "bash requires an approval prompter in workspace-write mode")
		}
		risk := bashRiskLevel(input.Command)
		emitToolActivity(ctx, LiveToolEvent{
			Kind:    "approval",
			Title:   fmt.Sprintf("%s [risk: %s]", title, risk),
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
		Title:   title,
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
	data, _ := json.Marshal(output)
	if exitCode != 0 {
		preview := bashOutputPreview(stdout.String(), stderr.String())
		summary := fmt.Sprintf("Exited %d.", exitCode)
		if preview != "" {
			summary = fmt.Sprintf("Exited %d: %s", exitCode, preview)
		}
		emitToolActivity(ctx, LiveToolEvent{
			Kind:    "bash_result",
			Title:   title,
			Summary: summary,
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
	emitToolActivity(ctx, LiveToolEvent{
		Kind:    "bash_result",
		Title:   title,
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

func executeTaskCreate(ctx LiveContext, call LiveCall) LiveResult {
	var input struct {
		Title     string `json:"title"`
		BlockedBy []int  `json:"blocked_by"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid task_create input: "+err.Error())
	}
	if strings.TrimSpace(input.Title) == "" {
		return liveError(call, "task title is required")
	}
	t, err := tasks.Create(ctx.Root, strings.TrimSpace(input.Title), input.BlockedBy)
	if err != nil {
		return liveError(call, "task_create: "+err.Error())
	}
	emitToolActivity(ctx, LiveToolEvent{
		Kind:    "task",
		Title:   fmt.Sprintf("task #%d created", t.ID),
		Summary: t.Title,
	})
	result := map[string]any{"id": t.ID, "title": t.Title, "status": t.Status}
	return liveJSON(call, result)
}

func executeTaskUpdate(ctx LiveContext, call LiveCall) LiveResult {
	var input struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid task_update input: "+err.Error())
	}
	t, err := tasks.Update(ctx.Root, input.ID, input.Status)
	if err != nil {
		return liveError(call, "task_update: "+err.Error())
	}
	emitToolActivity(ctx, LiveToolEvent{
		Kind:    "task",
		Title:   fmt.Sprintf("task #%d → %s", t.ID, t.Status),
		Summary: t.Title,
	})
	result := map[string]any{"id": t.ID, "title": t.Title, "status": t.Status}
	return liveJSON(call, result)
}

func executeTaskList(ctx LiveContext, call LiveCall) LiveResult {
	tl, err := tasks.Load(ctx.Root)
	if err != nil {
		return liveError(call, "task_list: "+err.Error())
	}
	data, _ := json.Marshal(tl.Tasks)
	return LiveResult{ToolUseID: call.ID, Name: call.Name, Output: string(data)}
}

func executeRequestPlanApproval(ctx LiveContext, call LiveCall) LiveResult {
	var input struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid request_plan_approval input: "+err.Error())
	}
	if ctx.Prompter == nil {
		return liveError(call, "request_plan_approval requires an approval prompter")
	}
	// Pass the task list as JSON so the TUI can render it properly.
	tl, _ := tasks.Load(ctx.Root)
	taskData, _ := json.Marshal(tl.Tasks)
	emitToolActivity(ctx, LiveToolEvent{
		Kind:    "approval",
		Title:   "plan_approval",
		Summary: "Awaiting plan approval.",
		Detail:  string(taskData),
	})
	approved, err := ctx.Prompter.Approve("plan_approval", string(taskData))
	if err != nil {
		return liveError(call, err.Error())
	}
	if !approved {
		return LiveResult{
			ToolUseID: call.ID,
			Name:      call.Name,
			Output:    `{"approved":false,"message":"User wants to adjust the plan. Ask what they would like to change."}`,
		}
	}
	return LiveResult{
		ToolUseID: call.ID,
		Name:      call.Name,
		Output:    `{"approved":true,"message":"Plan approved. Begin executing all tasks now following the Agentic Task Execution protocol. Start with task #1."}`,
	}
}

func executeDelegateTask(ctx LiveContext, call LiveCall) LiveResult {
	var input struct {
		Role               string   `json:"role"`
		Prompt             string   `json:"prompt"`
		Context            string   `json:"context"`
		AllowedTools       []string `json:"allowed_tools"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return liveError(call, "invalid delegate_task input: "+err.Error())
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return liveError(call, "delegate_task prompt is required")
	}
	workerRegistry, err := controlstate.LoadWorkerRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	worker := workerRegistry.Create(ctx.Root, []string{ctx.Root}, true)
	if _, err := workerRegistry.Observe(worker.WorkerID, "Ascaris> ready for prompt"); err != nil {
		return liveError(call, err.Error())
	}
	worker, err = workerRegistry.SendPrompt(worker.WorkerID, input.Prompt)
	if err != nil {
		return liveError(call, err.Error())
	}
	if err := controlstate.SaveWorkerRegistry(ctx.Root, workerRegistry); err != nil {
		return liveError(call, err.Error())
	}
	subagentRegistry, err := subagents.LoadRegistry(ctx.Root)
	if err != nil {
		return liveError(call, err.Error())
	}
	assignment, err := subagentRegistry.Create(worker.WorkerID, input.Role, input.Prompt, input.Context, input.AllowedTools, input.AcceptanceCriteria)
	if err != nil {
		return liveError(call, err.Error())
	}
	if err := subagents.SaveRegistry(ctx.Root, subagentRegistry); err != nil {
		return liveError(call, err.Error())
	}
	if ctx.DelegateTask != nil {
		runCtx := ctx.Context
		if runCtx == nil {
			runCtx = context.Background()
		}
		assignment, err = ctx.DelegateTask(runCtx, assignment)
		if err != nil {
			return liveError(call, err.Error())
		}
	}
	emitToolActivity(ctx, LiveToolEvent{
		Kind:    "subagent",
		Title:   assignment.AssignmentID,
		Summary: "Delegated task to " + assignment.Role + ".",
		Detail:  assignment.Prompt,
	})
	return liveJSON(call, map[string]any{
		"assignment_id":       assignment.AssignmentID,
		"worker_id":           assignment.WorkerID,
		"role":                assignment.Role,
		"status":              assignment.Status,
		"prompt":              assignment.Prompt,
		"allowed_tools":       assignment.AllowedTools,
		"acceptance_criteria": assignment.AcceptanceCriteria,
		"result_summary":      assignment.ResultSummary,
		"error":               assignment.Error,
		"verification":        assignment.Verification,
		"worker_status":       worker.Status,
		"message":             "subagent assignment recorded",
	})
}
