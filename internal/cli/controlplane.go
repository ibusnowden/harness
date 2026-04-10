package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	controlstate "ascaris/internal/state"
)

func runWorkerCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	trustedRoots := multiString{}
	fs.Var(&trustedRoots, "trusted-root", "")
	autoRecover := fs.Bool("auto-recover-prompt-misdelivery", true, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	remaining := fs.Args()
	action := "list"
	if len(remaining) > 0 {
		action = remaining[0]
		remaining = remaining[1:]
	}
	registry, err := controlstate.LoadWorkerRegistry(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	switch action {
	case "list":
		snapshot := registry.Snapshot()
		if *jsonOut {
			_, _ = fmt.Fprintln(stdout, controlstate.RenderJSON(snapshot))
			return 0
		}
		_, _ = fmt.Fprintln(stdout, controlstate.RenderText(snapshot))
		return 0
	case "create":
		if len(remaining) < 1 {
			return fail(stderr, fmt.Errorf("usage: worker [--trusted-root path] [--auto-recover-prompt-misdelivery=false] create <cwd>"))
		}
		cwd := filepath.Clean(remaining[0])
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(ctx.Root, cwd)
		}
		worker := registry.Create(cwd, trustedRoots, *autoRecover)
		if err := controlstate.SaveWorkerRegistry(ctx.Root, registry); err != nil {
			return fail(stderr, err)
		}
		return emitWorker(stdout, *jsonOut, worker)
	case "get":
		if len(remaining) < 1 {
			return fail(stderr, fmt.Errorf("usage: worker get <worker-id>"))
		}
		worker, ok := registry.Get(remaining[0])
		if !ok {
			return fail(stderr, fmt.Errorf("worker not found: %s", remaining[0]))
		}
		return emitWorker(stdout, *jsonOut, worker)
	case "observe":
		if len(remaining) < 2 {
			return fail(stderr, fmt.Errorf("usage: worker observe <worker-id> <screen text>"))
		}
		worker, err := registry.Observe(remaining[0], strings.Join(remaining[1:], " "))
		if err != nil {
			return fail(stderr, err)
		}
		if err := controlstate.SaveWorkerRegistry(ctx.Root, registry); err != nil {
			return fail(stderr, err)
		}
		return emitWorker(stdout, *jsonOut, worker)
	case "resolve-trust":
		if len(remaining) < 1 {
			return fail(stderr, fmt.Errorf("usage: worker resolve-trust <worker-id>"))
		}
		worker, err := registry.ResolveTrust(remaining[0])
		if err != nil {
			return fail(stderr, err)
		}
		if err := controlstate.SaveWorkerRegistry(ctx.Root, registry); err != nil {
			return fail(stderr, err)
		}
		return emitWorker(stdout, *jsonOut, worker)
	case "await-ready":
		if len(remaining) < 1 {
			return fail(stderr, fmt.Errorf("usage: worker await-ready <worker-id>"))
		}
		snapshot, err := registry.AwaitReady(remaining[0])
		if err != nil {
			return fail(stderr, err)
		}
		return emitValue(stdout, *jsonOut, renderReadySnapshot(snapshot), snapshot)
	case "send-prompt":
		if len(remaining) < 1 {
			return fail(stderr, fmt.Errorf("usage: worker send-prompt <worker-id> [prompt]"))
		}
		prompt := ""
		if len(remaining) > 1 {
			prompt = strings.Join(remaining[1:], " ")
		}
		worker, err := registry.SendPrompt(remaining[0], prompt)
		if err != nil {
			return fail(stderr, err)
		}
		if err := controlstate.SaveWorkerRegistry(ctx.Root, registry); err != nil {
			return fail(stderr, err)
		}
		return emitWorker(stdout, *jsonOut, worker)
	case "restart":
		if len(remaining) < 1 {
			return fail(stderr, fmt.Errorf("usage: worker restart <worker-id>"))
		}
		worker, err := registry.Restart(remaining[0])
		if err != nil {
			return fail(stderr, err)
		}
		if err := controlstate.SaveWorkerRegistry(ctx.Root, registry); err != nil {
			return fail(stderr, err)
		}
		return emitWorker(stdout, *jsonOut, worker)
	case "terminate":
		if len(remaining) < 1 {
			return fail(stderr, fmt.Errorf("usage: worker terminate <worker-id>"))
		}
		worker, err := registry.Terminate(remaining[0])
		if err != nil {
			return fail(stderr, err)
		}
		if err := controlstate.SaveWorkerRegistry(ctx.Root, registry); err != nil {
			return fail(stderr, err)
		}
		return emitWorker(stdout, *jsonOut, worker)
	default:
		return fail(stderr, fmt.Errorf("unknown worker action %q", action))
	}
}

func runTeamCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("team", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	taskIDs := multiString{}
	fs.Var(&taskIDs, "task", "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	remaining := fs.Args()
	action := "list"
	if len(remaining) > 0 {
		action = remaining[0]
		remaining = remaining[1:]
	}
	registry, err := controlstate.LoadTeamRegistry(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	switch action {
	case "list":
		items := registry.List()
		return emitValue(stdout, *jsonOut, renderTeams(items), map[string]any{"teams": items, "count": len(items)})
	case "create":
		if len(remaining) < 1 {
			return fail(stderr, fmt.Errorf("usage: team create <name> [task-id ...]"))
		}
		name := remaining[0]
		ids := append([]string{}, taskIDs...)
		ids = append(ids, remaining[1:]...)
		team := registry.Create(name, ids)
		if err := controlstate.SaveTeamRegistry(ctx.Root, registry); err != nil {
			return fail(stderr, err)
		}
		payload := map[string]any{
			"team_id":    team.TeamID,
			"name":       team.Name,
			"task_count": len(team.TaskIDs),
			"task_ids":   team.TaskIDs,
			"status":     team.Status,
			"created_at": team.CreatedAt,
		}
		return emitValue(stdout, *jsonOut, renderTeam(team), payload)
	case "delete":
		if len(remaining) < 1 {
			return fail(stderr, fmt.Errorf("usage: team delete <team-id>"))
		}
		team, err := registry.Delete(remaining[0])
		if err != nil {
			return fail(stderr, err)
		}
		if err := controlstate.SaveTeamRegistry(ctx.Root, registry); err != nil {
			return fail(stderr, err)
		}
		payload := map[string]any{
			"team_id": team.TeamID,
			"name":    team.Name,
			"status":  team.Status,
			"message": "Team deleted",
		}
		return emitValue(stdout, *jsonOut, "Teams\n  Deleted           "+team.TeamID, payload)
	default:
		return fail(stderr, fmt.Errorf("unknown team action %q", action))
	}
}

func runCronCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cron", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	description := fs.String("description", "", "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	remaining := fs.Args()
	action := "list"
	if len(remaining) > 0 {
		action = remaining[0]
		remaining = remaining[1:]
	}
	registry, err := controlstate.LoadCronRegistry(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	switch action {
	case "list":
		items := registry.List(false)
		return emitValue(stdout, *jsonOut, renderCrons(items), map[string]any{"crons": items, "count": len(items)})
	case "create", "add":
		if len(remaining) < 2 {
			return fail(stderr, fmt.Errorf("usage: cron add <schedule> <prompt> [description]"))
		}
		var desc *string
		if strings.TrimSpace(*description) != "" {
			value := strings.TrimSpace(*description)
			desc = &value
		} else if len(remaining) > 2 {
			value := strings.Join(remaining[2:], " ")
			desc = &value
		}
		entry := registry.Create(remaining[0], remaining[1], desc)
		if err := controlstate.SaveCronRegistry(ctx.Root, registry); err != nil {
			return fail(stderr, err)
		}
		return emitValue(stdout, *jsonOut, renderCron(entry), entry)
	case "delete", "remove":
		if len(remaining) < 1 {
			return fail(stderr, fmt.Errorf("usage: cron remove <cron-id>"))
		}
		entry, err := registry.Delete(remaining[0])
		if err != nil {
			return fail(stderr, err)
		}
		if err := controlstate.SaveCronRegistry(ctx.Root, registry); err != nil {
			return fail(stderr, err)
		}
		payload := map[string]any{
			"cron_id":  entry.CronID,
			"schedule": entry.Schedule,
			"status":   "deleted",
			"message":  "Cron entry removed",
		}
		return emitValue(stdout, *jsonOut, "Crons\n  Deleted           "+entry.CronID, payload)
	default:
		return fail(stderr, fmt.Errorf("unknown cron action %q", action))
	}
}

func emitWorker(stdout io.Writer, jsonOut bool, worker controlstate.Worker) int {
	if jsonOut {
		_, _ = fmt.Fprintln(stdout, marshalJSON(worker))
		return 0
	}
	_, _ = fmt.Fprintln(stdout, controlstate.RenderText(controlstate.Snapshot{Workers: []controlstate.Worker{worker}}))
	return 0
}

func emitValue(stdout io.Writer, jsonOut bool, text string, payload any) int {
	if jsonOut {
		_, _ = fmt.Fprintln(stdout, marshalJSON(payload))
		return 0
	}
	_, _ = fmt.Fprintln(stdout, text)
	return 0
}

func marshalJSON(payload any) string {
	data, _ := json.MarshalIndent(payload, "", "  ")
	return string(data)
}

func renderReadySnapshot(snapshot controlstate.WorkerReadySnapshot) string {
	lines := []string{
		"Worker ready state",
		"  Worker ID         " + snapshot.WorkerID,
		"  Status            " + string(snapshot.Status),
		"  Ready             " + strconv.FormatBool(snapshot.Ready),
		"  Blocked           " + strconv.FormatBool(snapshot.Blocked),
		"  Replay prompt     " + strconv.FormatBool(snapshot.ReplayPromptReady),
	}
	if snapshot.LastError != nil {
		lines = append(lines, "  Last error        "+snapshot.LastError.Message)
	}
	return strings.Join(lines, "\n")
}

func renderTeams(teams []controlstate.Team) string {
	if len(teams) == 0 {
		return "Teams\n  No teams recorded."
	}
	lines := []string{"Teams"}
	for _, team := range teams {
		lines = append(lines, fmt.Sprintf("  %s · %s · %s · %d tasks", team.TeamID, team.Name, team.Status, len(team.TaskIDs)))
	}
	return strings.Join(lines, "\n")
}

func renderTeam(team controlstate.Team) string {
	lines := []string{
		"Teams",
		"  Team ID           " + team.TeamID,
		"  Name              " + team.Name,
		"  Status            " + string(team.Status),
		"  Tasks             " + strconv.Itoa(len(team.TaskIDs)),
	}
	if len(team.TaskIDs) > 0 {
		lines = append(lines, "  Task IDs          "+strings.Join(team.TaskIDs, ", "))
	}
	return strings.Join(lines, "\n")
}

func renderCrons(entries []controlstate.CronEntry) string {
	if len(entries) == 0 {
		return "Crons\n  No cron entries recorded."
	}
	lines := []string{"Crons"}
	for _, entry := range entries {
		line := fmt.Sprintf("  %s · %s · enabled=%t · runs=%d", entry.CronID, entry.Schedule, entry.Enabled, entry.RunCount)
		if entry.Description != nil && strings.TrimSpace(*entry.Description) != "" {
			line += " · " + *entry.Description
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func renderCron(entry controlstate.CronEntry) string {
	lines := []string{
		"Crons",
		"  Cron ID           " + entry.CronID,
		"  Schedule          " + entry.Schedule,
		"  Enabled           " + strconv.FormatBool(entry.Enabled),
		"  Prompt            " + entry.Prompt,
	}
	if entry.Description != nil && strings.TrimSpace(*entry.Description) != "" {
		lines = append(lines, "  Description       "+*entry.Description)
	}
	return strings.Join(lines, "\n")
}
