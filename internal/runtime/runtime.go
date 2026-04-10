package runtime

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"ascaris/internal/commands"
	"ascaris/internal/context"
	"ascaris/internal/execution"
	"ascaris/internal/history"
	"ascaris/internal/models"
	"ascaris/internal/query"
	"ascaris/internal/setup"
	"ascaris/internal/systeminit"
	"ascaris/internal/tools"
)

type RoutedMatch struct {
	Kind       string
	Name       string
	SourceHint string
	Score      int
}

type Session struct {
	Prompt                   string
	Context                  context.PortContext
	Setup                    setup.WorkspaceSetup
	SetupReport              setup.Report
	SystemInitMessage        string
	History                  history.Log
	RoutedMatches            []RoutedMatch
	TurnResult               query.TurnResult
	CommandExecutionMessages []string
	ToolExecutionMessages    []string
	StreamEvents             []map[string]any
	PersistedSessionPath     string
}

func (s Session) Markdown() string {
	lines := []string{
		"# Runtime Session",
		"",
		"Prompt: " + s.Prompt,
		"",
		"## Context",
		context.Render(s.Context),
		"",
		"## Setup",
		"- Go: " + s.Setup.GoVersion + " (" + s.Setup.Compiler + ")",
		"- Platform: " + s.Setup.Platform,
		"- Test command: " + s.Setup.TestCommand,
		"",
		"## Startup Steps",
	}
	for _, step := range s.Setup.StartupSteps() {
		lines = append(lines, "- "+step)
	}
	lines = append(lines, "", "## System Init", s.SystemInitMessage, "", "## Routed Matches")
	if len(s.RoutedMatches) == 0 {
		lines = append(lines, "- none")
	} else {
		for _, match := range s.RoutedMatches {
			lines = append(lines, "- ["+match.Kind+"] "+match.Name+" ("+strconv.Itoa(match.Score)+") - "+match.SourceHint)
		}
	}
	lines = append(lines, "", "## Command Execution")
	if len(s.CommandExecutionMessages) == 0 {
		lines = append(lines, "none")
	} else {
		lines = append(lines, s.CommandExecutionMessages...)
	}
	lines = append(lines, "", "## Tool Execution")
	if len(s.ToolExecutionMessages) == 0 {
		lines = append(lines, "none")
	} else {
		lines = append(lines, s.ToolExecutionMessages...)
	}
	lines = append(lines, "", "## Stream Events")
	for _, event := range s.StreamEvents {
		lines = append(lines, "- "+event["type"].(string)+": "+renderMap(event))
	}
	lines = append(lines, "", "## Turn Result", s.TurnResult.Output, "", "Persisted session path: "+s.PersistedSessionPath, "", s.History.Markdown())
	return strings.Join(lines, "\n")
}

type Harness struct {
	Root string
}

func (h Harness) RoutePrompt(prompt string, limit int) []RoutedMatch {
	tokens := tokenize(prompt)
	commandEntries, _ := commands.Catalog(h.Root)
	toolEntries, _ := tools.Catalog(h.Root, tools.CatalogOptions{IncludeMCP: true})
	byKind := map[string][]RoutedMatch{
		"command": h.collect(tokens, commandEntries, "command"),
		"tool":    h.collect(tokens, toolEntries, "tool"),
	}
	selected := make([]RoutedMatch, 0, limit)
	for _, kind := range []string{"command", "tool"} {
		if len(byKind[kind]) > 0 {
			selected = append(selected, byKind[kind][0])
			byKind[kind] = byKind[kind][1:]
		}
	}
	leftovers := append(append([]RoutedMatch(nil), byKind["command"]...), byKind["tool"]...)
	sort.Slice(leftovers, func(i, j int) bool {
		if leftovers[i].Score == leftovers[j].Score {
			if leftovers[i].Kind == leftovers[j].Kind {
				return leftovers[i].Name < leftovers[j].Name
			}
			return leftovers[i].Kind < leftovers[j].Kind
		}
		return leftovers[i].Score > leftovers[j].Score
	})
	for _, match := range leftovers {
		if len(selected) == limit {
			break
		}
		selected = append(selected, match)
	}
	return selected
}

func (h Harness) BootstrapSession(prompt string, limit int) (Session, error) {
	ctx, err := context.Build(h.Root)
	if err != nil {
		return Session{}, err
	}
	setupReport := setup.Run(h.Root, true)
	engine, err := query.FromWorkspace(h.Root)
	if err != nil {
		return Session{}, err
	}
	var log history.Log
	log.Add("context", "go_files="+strconv.Itoa(ctx.GoFileCount)+", test_files="+strconv.Itoa(ctx.TestFileCount)+", assets="+strconv.Itoa(ctx.AssetFileCount))
	commandEntries, _ := commands.Catalog(h.Root)
	toolEntries, _ := tools.Catalog(h.Root, tools.CatalogOptions{IncludeMCP: true})
	log.Add("registry", "commands="+strconv.Itoa(len(commandEntries))+", tools="+strconv.Itoa(len(toolEntries)))
	matches := h.RoutePrompt(prompt, limit)
	registry, err := execution.Build(h.Root)
	if err != nil {
		return Session{}, err
	}
	commandExecs := []string{}
	toolExecs := []string{}
	for _, match := range matches {
		switch match.Kind {
		case "command":
			if command := registry.Command(match.Name); command != nil {
				commandExecs = append(commandExecs, command.Execute(prompt))
			}
		case "tool":
			if tool := registry.Tool(match.Name); tool != nil {
				toolExecs = append(toolExecs, tool.Execute(prompt))
			}
		}
	}
	denials := h.inferPermissionDenials(matches)
	stream := engine.StreamSubmitMessage(prompt, matchedNames(matches, "command"), matchedNames(matches, "tool"), denials)
	turn := engine.SubmitMessage(prompt, matchedNames(matches, "command"), matchedNames(matches, "tool"), denials)
	path, err := engine.PersistSession()
	if err != nil {
		return Session{}, err
	}
	log.Add("routing", "matches="+strconv.Itoa(len(matches))+" for prompt="+quote(prompt))
	log.Add("execution", "command_execs="+strconv.Itoa(len(commandExecs))+" tool_execs="+strconv.Itoa(len(toolExecs)))
	log.Add("turn", "commands="+strconv.Itoa(len(turn.MatchedCommands))+" tools="+strconv.Itoa(len(turn.MatchedTools))+" denials="+strconv.Itoa(len(turn.PermissionDenials))+" stop="+turn.StopReason)
	log.Add("session_store", path)
	return Session{
		Prompt:                   prompt,
		Context:                  ctx,
		Setup:                    setupReport.Setup,
		SetupReport:              setupReport,
		SystemInitMessage:        systeminit.Build(h.Root, true),
		History:                  log,
		RoutedMatches:            matches,
		TurnResult:               turn,
		CommandExecutionMessages: commandExecs,
		ToolExecutionMessages:    toolExecs,
		StreamEvents:             stream,
		PersistedSessionPath:     path,
	}, nil
}

func (h Harness) RunTurnLoop(prompt string, limit, maxTurns int, structuredOutput bool) ([]query.TurnResult, error) {
	engine, err := query.FromWorkspace(h.Root)
	if err != nil {
		return nil, err
	}
	engine.Config.MaxTurns = maxTurns
	engine.Config.StructuredOutput = structuredOutput
	matches := h.RoutePrompt(prompt, limit)
	commandNames := matchedNames(matches, "command")
	toolNames := matchedNames(matches, "tool")
	results := make([]query.TurnResult, 0, maxTurns)
	for turn := 0; turn < maxTurns; turn++ {
		turnPrompt := prompt
		if turn > 0 {
			turnPrompt = prompt + " [turn " + strconv.Itoa(turn+1) + "]"
		}
		result := engine.SubmitMessage(turnPrompt, commandNames, toolNames, nil)
		results = append(results, result)
		if result.StopReason != "completed" {
			break
		}
	}
	return results, nil
}

func (h Harness) inferPermissionDenials(matches []RoutedMatch) []models.PermissionDenial {
	denials := []models.PermissionDenial{}
	for _, match := range matches {
		if match.Kind == "tool" && strings.Contains(strings.ToLower(match.Name), "bash") {
			denials = append(denials, models.PermissionDenial{
				ToolName: match.Name,
				Reason:   "destructive shell execution remains gated in the Go harness",
			})
		}
	}
	return denials
}

func (h Harness) collect(tokens []string, modules []models.PortingModule, kind string) []RoutedMatch {
	matches := []RoutedMatch{}
	for _, module := range modules {
		score := score(tokens, module)
		if score > 0 {
			matches = append(matches, RoutedMatch{
				Kind:       kind,
				Name:       module.Name,
				SourceHint: module.SourceHint,
				Score:      score,
			})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Name < matches[j].Name
		}
		return matches[i].Score > matches[j].Score
	})
	return matches
}

func tokenize(prompt string) []string {
	prompt = strings.NewReplacer("/", " ", "-", " ").Replace(prompt)
	return strings.Fields(strings.ToLower(prompt))
}

func score(tokens []string, module models.PortingModule) int {
	haystacks := []string{
		strings.ToLower(module.Name),
		strings.ToLower(module.SourceHint),
		strings.ToLower(module.Responsibility),
	}
	score := 0
	for _, token := range tokens {
		for _, haystack := range haystacks {
			if strings.Contains(haystack, token) {
				score++
				break
			}
		}
	}
	return score
}

func matchedNames(matches []RoutedMatch, kind string) []string {
	values := []string{}
	for _, match := range matches {
		if match.Kind == kind {
			values = append(values, match.Name)
		}
	}
	return values
}

func renderMap(value map[string]any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
}
