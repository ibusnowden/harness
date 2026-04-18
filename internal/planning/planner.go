package planning

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"ascaris/internal/tasks"
)

type Status string

const (
	StatusDraft    Status = "draft"
	StatusApproved Status = "approved"
)

type TaskContract struct {
	ID                 int      `json:"id"`
	Title              string   `json:"title"`
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	AllowedTools       []string `json:"allowed_tools"`
	BlockedBy          []int    `json:"blocked_by,omitempty"`
}

type Plan struct {
	ID        string         `json:"id"`
	Request   string         `json:"request"`
	Status    Status         `json:"status"`
	CreatedMS int64          `json:"created_at_ms"`
	Tasks     []TaskContract `json:"tasks"`
}

type Options struct {
	Request string
}

func Create(root string, opts Options) (Plan, error) {
	request := strings.TrimSpace(opts.Request)
	if request == "" {
		return Plan{}, fmt.Errorf("plan request is required")
	}
	now := time.Now().UnixMilli()
	plan := Plan{
		ID:        fmt.Sprintf("plan_%d", now),
		Request:   request,
		Status:    StatusDraft,
		CreatedMS: now,
		Tasks:     decompose(request),
	}
	if err := persist(root, plan); err != nil {
		return Plan{}, err
	}
	taskItems := make([]tasks.Task, 0, len(plan.Tasks))
	for _, item := range plan.Tasks {
		taskItems = append(taskItems, tasks.Task{
			ID:                 item.ID,
			Title:              item.Title,
			Goal:               item.Goal,
			AcceptanceCriteria: append([]string(nil), item.AcceptanceCriteria...),
			AllowedTools:       append([]string(nil), item.AllowedTools...),
			Status:             tasks.StatusOpen,
			BlockedBy:          append([]int(nil), item.BlockedBy...),
		})
	}
	if err := tasks.Replace(root, taskItems); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func RenderMarkdown(plan Plan) string {
	lines := []string{
		"# Implementation Plan",
		"",
		"- Plan ID: `" + plan.ID + "`",
		"- Status: `" + string(plan.Status) + "`",
		"- Request: " + plan.Request,
		"",
		"## Task Contracts",
	}
	for _, task := range plan.Tasks {
		lines = append(lines,
			"",
			fmt.Sprintf("### %d. %s", task.ID, task.Title),
			"",
			"Goal: "+task.Goal,
			"",
			"Acceptance criteria:",
		)
		for _, criterion := range task.AcceptanceCriteria {
			lines = append(lines, "- "+criterion)
		}
		if len(task.BlockedBy) > 0 {
			lines = append(lines, "", "Blocked by: "+joinInts(task.BlockedBy))
		}
		lines = append(lines, "", "Allowed tools: `"+strings.Join(task.AllowedTools, "`, `")+"`")
	}
	lines = append(lines, "", "Use `/plan` approval in the TUI or `ascaris plan --execute ...` to run this task graph.")
	return strings.Join(lines, "\n")
}

func decompose(request string) []TaskContract {
	parts := splitRequest(request)
	if len(parts) == 0 {
		parts = []string{request}
	}
	tasksOut := make([]TaskContract, 0, len(parts)+2)
	tasksOut = append(tasksOut, TaskContract{
		ID:                 1,
		Title:              "Inspect current implementation and constraints",
		Goal:               "Gather the repo facts needed to execute: " + request,
		AcceptanceCriteria: []string{"Relevant files and existing behavior are identified.", "Risks and unknowns are recorded before edits."},
		AllowedTools:       []string{"read_file", "glob_search", "grep_search", "web_search", "web_fetch"},
	})
	for _, part := range parts {
		id := len(tasksOut) + 1
		title := normalizeTitle(part)
		tasksOut = append(tasksOut, TaskContract{
			ID:                 id,
			Title:              title,
			Goal:               "Implement or resolve: " + strings.TrimSpace(part),
			AcceptanceCriteria: []string{"Code or configuration changes are scoped to this task.", "Behavior is covered by an appropriate test or verification command."},
			AllowedTools:       []string{"read_file", "glob_search", "grep_search", "bash", "write_file", "edit_file"},
			BlockedBy:          []int{1},
		})
	}
	verifyID := len(tasksOut) + 1
	deps := make([]int, 0, len(tasksOut)-1)
	for _, task := range tasksOut[1:] {
		deps = append(deps, task.ID)
	}
	sort.Ints(deps)
	tasksOut = append(tasksOut, TaskContract{
		ID:                 verifyID,
		Title:              "Validate and summarize final result",
		Goal:               "Verify the full requested change and produce a concise handoff.",
		AcceptanceCriteria: []string{"Focused tests/checks pass or failures are documented.", "Final summary lists changes, validation, and residual risks."},
		AllowedTools:       []string{"read_file", "grep_search", "bash"},
		BlockedBy:          deps,
	})
	return tasksOut
}

func splitRequest(request string) []string {
	re := regexp.MustCompile(`(?i)\s+(?:and|then|,|;)\s+`)
	chunks := re.Split(request, -1)
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if trimmed := strings.TrimSpace(chunk); len(trimmed) >= 8 {
			out = append(out, trimmed)
		}
	}
	return out
}

func normalizeTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Implement requested change"
	}
	words := strings.Fields(value)
	if len(words) > 10 {
		words = words[:10]
	}
	title := strings.Join(words, " ")
	if len(title) > 72 {
		title = strings.TrimSpace(title[:72])
	}
	return strings.ToUpper(title[:1]) + title[1:]
}

func persist(root string, plan Plan) error {
	dir := filepath.Join(root, ".ascaris", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, plan.ID+".json"), data, 0o644)
}

func joinInts(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("#%d", value))
	}
	return strings.Join(parts, ", ")
}
