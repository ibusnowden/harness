package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ascaris/internal/subagents"
	"ascaris/internal/tasks"
	"ascaris/internal/tools"
)

func (h LiveHarness) ExecutePlan(ctx context.Context, opts PromptOptions) (PromptSummary, error) {
	opts = withPromptDefaults(opts, h.Config)
	if strings.TrimSpace(opts.Model) == "" {
		return PromptSummary{}, fmt.Errorf("no model configured")
	}
	liveRuntime, err := newLiveRuntime(h.Root, h.Config, opts, "")
	if err != nil {
		return PromptSummary{}, err
	}
	defer liveRuntime.close()
	return liveRuntime.executeApprovedPlan(ctx)
}

func (r *liveRuntime) executeApprovedPlan(ctx context.Context) (PromptSummary, error) {
	emitActivity(r.options, ActivityEvent{
		Kind:    "status",
		Title:   "Executing Plan",
		Summary: "Running approved tasks through scoped subagents.",
	})

	summary := PromptSummary{
		Model:       r.options.Model,
		Provider:    string(r.options.Provider),
		ToolResults: []tools.LiveResult{},
	}
	initialAssignments, err := subagentAssignments(r.root)
	if err != nil {
		return summary, err
	}
	seenAssignments := make(map[string]struct{}, len(initialAssignments))
	for _, assignment := range initialAssignments {
		seenAssignments[assignment.AssignmentID] = struct{}{}
	}
	var stoppedErr error
	for {
		taskList, err := tasks.Load(r.root)
		if err != nil {
			return summary, err
		}
		task, ok := tasks.NextOpenUnblocked(taskList.Tasks)
		if !ok {
			break
		}
		emitActivity(r.options, ActivityEvent{
			Kind:    "task",
			Title:   fmt.Sprintf("Task #%d", task.ID),
			Summary: "Delegating " + task.Title + ".",
			Detail:  task.Goal,
		})
		if _, err := tasks.UpdateWithNotes(r.root, task.ID, tasks.StatusInProgress, ""); err != nil {
			return summary, err
		}
		result := r.delegateApprovedTask(ctx, task)
		summary.ToolResults = append(summary.ToolResults, result)
		var output struct {
			AssignmentID  string `json:"assignment_id"`
			ResultSummary string `json:"result_summary"`
			Error         string `json:"error"`
		}
		_ = json.Unmarshal([]byte(result.Output), &output)
		if result.IsError {
			stoppedErr = fmt.Errorf("task #%d failed: %s", task.ID, firstNonEmptyString(output.Error, strings.TrimSpace(result.Output)))
			if _, err := tasks.UpdateWithNotes(r.root, task.ID, tasks.StatusOpen, stoppedErr.Error()); err != nil {
				return summary, err
			}
			emitActivity(r.options, ActivityEvent{
				Kind:    "task",
				Title:   fmt.Sprintf("Task #%d", task.ID),
				Summary: "Task execution failed.",
				Detail:  strings.TrimSpace(result.Output),
				Error:   true,
			})
			break
		}
		if _, err := tasks.UpdateWithNotes(r.root, task.ID, tasks.StatusDone, ""); err != nil {
			return summary, err
		}
		emitActivity(r.options, ActivityEvent{
			Kind:    "task",
			Title:   fmt.Sprintf("Task #%d", task.ID),
			Summary: "Task completed.",
			Detail:  strings.TrimSpace(output.ResultSummary),
		})
	}

	assignments, err := subagentAssignments(r.root)
	if err != nil {
		return summary, err
	}
	assignments = filterNewAssignments(assignments, seenAssignments)
	for _, assignment := range assignments {
		summary.Usage = summary.Usage.Add(assignment.TokenUsage)
	}
	summary.EstimatedCost = formatUSD(estimateCost(summary.Usage, r.options.Model))
	summary.Message = renderPlanExecutionSummary(assignments, stoppedErr)
	if stoppedErr != nil {
		return summary, stoppedErr
	}
	return summary, nil
}

func (r *liveRuntime) delegateApprovedTask(ctx context.Context, task tasks.Task) tools.LiveResult {
	input := map[string]any{
		"role":                roleForTask(task),
		"prompt":              subagentPromptForTask(task.Title, task.Goal, task.AcceptanceCriteria),
		"context":             task.Goal,
		"allowed_tools":       task.AllowedTools,
		"acceptance_criteria": task.AcceptanceCriteria,
	}
	data, _ := json.Marshal(input)
	return tools.ExecuteLive(tools.LiveContext{
		Root:           r.root,
		Context:        ctx,
		PermissionMode: r.options.PermissionMode,
		Activity: func(event tools.LiveToolEvent) {
			emitActivity(r.options, activityForToolEvent(event, 0))
		},
		DelegateTask: r.runSubagentAssignment,
	}, tools.LiveCall{
		ID:    fmt.Sprintf("delegate_task_task_%d", task.ID),
		Name:  "delegate_task",
		Input: data,
	})
}

func roleForTask(task tasks.Task) string {
	for _, toolName := range task.AllowedTools {
		switch strings.ToLower(strings.TrimSpace(toolName)) {
		case "write_file", "edit_file", "bash":
			return "worker"
		}
	}
	return "explorer"
}

func subagentAssignments(root string) ([]subagents.Assignment, error) {
	registry, err := subagents.LoadRegistry(root)
	if err != nil {
		return nil, err
	}
	return registry.Snapshot().Assignments, nil
}

func renderPlanExecutionSummary(assignments []subagents.Assignment, runErr error) string {
	if len(assignments) == 0 {
		if runErr != nil {
			return "Plan execution stopped before any subagent assignments completed: " + runErr.Error()
		}
		return "Plan execution completed with no subagent assignments."
	}
	lines := []string{fmt.Sprintf("Plan execution processed %d subagent assignment(s).", len(assignments))}
	for _, assignment := range assignments {
		switch assignment.Status {
		case subagents.StatusCompleted:
			lines = append(lines, fmt.Sprintf("- %s: %s", assignment.AssignmentID, firstNonEmptyString(assignment.ResultSummary, "completed")))
		case subagents.StatusFailed:
			lines = append(lines, fmt.Sprintf("- %s: failed: %s", assignment.AssignmentID, firstNonEmptyString(assignment.Error, "unknown error")))
		default:
			lines = append(lines, fmt.Sprintf("- %s: %s", assignment.AssignmentID, assignment.Status))
		}
	}
	if runErr != nil {
		lines = append(lines, "", "Plan execution stopped early: "+runErr.Error())
	}
	return strings.Join(lines, "\n")
}

func filterNewAssignments(assignments []subagents.Assignment, seen map[string]struct{}) []subagents.Assignment {
	out := make([]subagents.Assignment, 0, len(assignments))
	for _, assignment := range assignments {
		if _, ok := seen[assignment.AssignmentID]; ok {
			continue
		}
		out = append(out, assignment)
	}
	return out
}
