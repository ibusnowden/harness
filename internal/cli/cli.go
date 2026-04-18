package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"ascaris/internal/agents"
	"ascaris/internal/api"
	"ascaris/internal/commands"
	"ascaris/internal/config"
	"ascaris/internal/doctor"
	"ascaris/internal/graphs"
	"ascaris/internal/manifest"
	"ascaris/internal/mcp"
	"ascaris/internal/migrations"
	"ascaris/internal/modes"
	"ascaris/internal/oauth"
	"ascaris/internal/outputstyles"
	"ascaris/internal/parity"
	"ascaris/internal/permissions"
	"ascaris/internal/planning"
	"ascaris/internal/plugins"
	"ascaris/internal/pool"
	"ascaris/internal/query"
	"ascaris/internal/repl"
	hruntime "ascaris/internal/runtime"
	"ascaris/internal/securityreview"
	"ascaris/internal/sessions"
	"ascaris/internal/setup"
	"ascaris/internal/skills"
	workerstate "ascaris/internal/state"
	"ascaris/internal/status"
	"ascaris/internal/systeminit"
	"ascaris/internal/tools"
	"ascaris/internal/version"
	"ascaris/internal/workspace"
)

type Context struct {
	Root string
}

type globalOptions struct {
	Model          string
	Provider       api.ProviderKind
	PermissionMode tools.PermissionMode
	AllowedTools   []string
	OutputFormat   string
	Resume         string
}

type livePromptHarness interface {
	RunPrompt(context.Context, string, hruntime.PromptOptions) (hruntime.PromptSummary, error)
}

type promptSpinner interface {
	Start(string)
	Update(string)
	Stop()
}

type spinnerController struct {
	spinner promptSpinner
	label   string
	running bool
}

func newSpinnerController(spinner promptSpinner) *spinnerController {
	if spinner == nil {
		return nil
	}
	return &spinnerController{spinner: spinner}
}

func (c *spinnerController) Start(label string) {
	if c == nil || c.spinner == nil {
		return
	}
	c.label = label
	if c.running {
		c.spinner.Update(label)
		return
	}
	c.spinner.Start(label)
	c.running = true
}

func (c *spinnerController) Update(label string) {
	if c == nil || c.spinner == nil {
		return
	}
	c.label = label
	if !c.running {
		return
	}
	c.spinner.Update(label)
}

func (c *spinnerController) Pause() {
	if c == nil || c.spinner == nil || !c.running {
		return
	}
	c.spinner.Stop()
	c.running = false
}

func (c *spinnerController) Resume() {
	if c == nil || c.spinner == nil || c.running {
		return
	}
	c.spinner.Start(c.label)
	c.running = true
}

func (c *spinnerController) Stop() {
	c.Pause()
}

type spinnerAwarePrompter struct {
	base    tools.ApprovalPrompter
	spinner *spinnerController
}

func (p spinnerAwarePrompter) Approve(toolName string, input string) (bool, error) {
	if p.spinner != nil {
		p.spinner.Pause()
		defer p.spinner.Resume()
	}
	return p.base.Approve(toolName, input)
}

var browserOpener = openBrowser
var oauthStateGenerator = oauth.GenerateState
var oauthWaitForCallback = oauth.WaitForCallback
var oauthCodeExchanger = func(ctx context.Context, client *http.Client, settings *config.OAuthSettings, code, verifier, redirectURI string) (oauth.TokenSet, error) {
	return oauth.ExchangeCode(ctx, client, settings, code, verifier, redirectURI)
}
var newLiveHarness = func(root string) (livePromptHarness, error) {
	return hruntime.NewLiveHarness(root)
}
var newPromptSpinner = func(writer io.Writer) promptSpinner {
	return outputstyles.NewPromptSpinner(writer)
}
var isInteractiveWriter = outputstyles.IsInteractiveWriter
var isInteractiveReader = isInteractiveStream
var launchREPL = repl.Launch

func Run(ctx Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	options, remaining, err := parseGlobalOptions(args)
	if err != nil {
		return fail(stderr, err)
	}
	if len(remaining) == 0 {
		if isInteractiveReader(stdin) {
			return runInteractiveREPL(ctx, options, stdin, stdout, stderr)
		}
		if hruntime.LiveConfigured() || liveConfigAvailable(ctx.Root) {
			prompt, err := hruntime.ReadPrompt(stdin)
			if err != nil {
				return fail(stderr, err)
			}
			if prompt != "" {
				return runPrompt(ctx, options, []string{prompt}, stdin, stdout, stderr)
			}
		}
		printHelp(stdout)
		return 0
	}
	if strings.HasPrefix(remaining[0], "/") {
		return runSlashCommand(ctx, options, remaining, stdout, stderr)
	}
	switch remaining[0] {
	case "help", "--help", "-h":
		printHelp(stdout)
		return 0
	case "version", "--version", "-V":
		_, _ = fmt.Fprintln(stdout, version.Product+" "+version.Version)
		return 0
	case "status":
		return runStatus(ctx, remaining[1:], stdout, stderr)
	case "doctor":
		return runDoctor(ctx, remaining[1:], stdout, stderr)
	case "review":
		return runSecurityWorkflow(ctx, securityreview.ModeReview, securityreview.WorkflowSource, remaining[1:], stdout, stderr)
	case "plan":
		return runPlanCommand(ctx, remaining[1:], stdout, stderr)
	case "security-review":
		return runSecurityWorkflow(ctx, securityreview.ModeSecurityReview, securityreview.WorkflowAuto, remaining[1:], stdout, stderr)
	case "bughunter":
		return runSecurityWorkflow(ctx, securityreview.ModeBugHunter, securityreview.WorkflowSource, remaining[1:], stdout, stderr)
	case "fuzz":
		return runSecurityWorkflow(ctx, securityreview.ModeSecurityReview, securityreview.WorkflowFuzz, remaining[1:], stdout, stderr)
	case "crash-triage":
		return runSecurityWorkflow(ctx, securityreview.ModeSecurityReview, securityreview.WorkflowBinary, remaining[1:], stdout, stderr)
	case "sandbox":
		_, _ = fmt.Fprintln(stdout, "mode=workspace-write\nfilesystem=.ascaris-aware\nnetwork=local-only")
		return 0
	case "login":
		return runLogin(ctx, options.OutputFormat, stdout, stderr)
	case "logout":
		return runLogout(ctx, options.OutputFormat, stdout, stderr)
	case "session":
		return runSessionCommand(ctx, remaining[1:], stdout, stderr)
	case "agents":
		return runAgentsCommand(ctx, remaining[1:], stdout, stderr)
	case "skills":
		return runSkillsCommand(ctx, remaining[1:], stdout, stderr)
	case "team":
		return runTeamCommand(ctx, remaining[1:], stdout, stderr)
	case "cron":
		return runCronCommand(ctx, remaining[1:], stdout, stderr)
	case "worker":
		return runWorkerCommand(ctx, remaining[1:], stdout, stderr)
	case "plugins":
		return runPluginsCommand(ctx, remaining[1:], stdout, stderr)
	case "mcp":
		return runMCPCommand(ctx, remaining[1:], stdout, stderr)
	case "state":
		return runStateCommand(ctx, remaining[1:], stdout, stderr)
	case "migrate":
		return runMigrateCommand(ctx, remaining[1:], stdout, stderr)
	case "summary":
		engine, err := query.FromWorkspace(ctx.Root)
		if err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintln(stdout, engine.RenderSummary())
		return 0
	case "manifest":
		m, err := manifest.Build(ctx.Root)
		if err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintln(stdout, m.Markdown())
		return 0
	case "parity-audit":
		result, err := parity.Run(ctx.Root)
		if err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintln(stdout, result.Markdown())
		return 0
	case "setup-report":
		_, _ = fmt.Fprintln(stdout, setup.Run(ctx.Root, true).Markdown())
		return 0
	case "command-graph":
		_, _ = fmt.Fprintln(stdout, graphs.BuildCommandGraph().Markdown())
		return 0
	case "tool-pool":
		_, _ = fmt.Fprintln(stdout, pool.Assemble(false, true, nil).Markdown())
		return 0
	case "bootstrap-graph":
		_, _ = fmt.Fprintln(stdout, graphs.BuildBootstrapGraph().Markdown())
		return 0
	case "subsystems":
		return runSubsystems(ctx, remaining[1:], stdout, stderr)
	case "commands":
		return runCommands(ctx, remaining[1:], stdout, stderr)
	case "tools":
		return runTools(ctx, remaining[1:], stdout, stderr)
	case "route":
		return runRoute(ctx, remaining[1:], stdout, stderr)
	case "bootstrap":
		return runBootstrap(ctx, remaining[1:], stdout, stderr)
	case "turn-loop":
		return runTurnLoop(ctx, remaining[1:], stdout, stderr)
	case "flush-transcript":
		return runFlushTranscript(ctx, remaining[1:], stdout, stderr)
	case "load-session":
		return runLoadSession(ctx, remaining[1:], stdout, stderr)
	case "remote-mode":
		return runSingleTarget(stdout, remaining[1:], modes.Remote)
	case "ssh-mode":
		return runSingleTarget(stdout, remaining[1:], modes.SSH)
	case "teleport-mode":
		return runSingleTarget(stdout, remaining[1:], modes.Teleport)
	case "direct-connect-mode":
		return runSingleTarget(stdout, remaining[1:], modes.DirectConnect)
	case "deep-link-mode":
		return runSingleTarget(stdout, remaining[1:], modes.DeepLink)
	case "show-command":
		return runShowCommand(ctx, remaining[1:], stdout, stderr)
	case "show-tool":
		return runShowTool(ctx, remaining[1:], stdout, stderr)
	case "exec-command":
		return runExecCommand(ctx, remaining[1:], stdout, stderr)
	case "exec-tool":
		return runExecTool(ctx, remaining[1:], stdout, stderr)
	case "system-init":
		_, _ = fmt.Fprintln(stdout, systeminit.Build(ctx.Root, true))
		return 0
	case "prompt":
		return runPrompt(ctx, options, remaining[1:], stdin, stdout, stderr)
	default:
		return runPrompt(ctx, options, remaining, stdin, stdout, stderr)
	}
}

func runStatus(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	report, err := status.Build(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	if *jsonOut {
		_, _ = fmt.Fprintln(stdout, report.JSON())
		return 0
	}
	_, _ = fmt.Fprintln(stdout, report.Text())
	return 0
}

func runDoctor(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	report := doctor.Build(ctx.Root)
	if *jsonOut {
		_, _ = fmt.Fprintln(stdout, report.JSON())
		return 0
	}
	_, _ = fmt.Fprintln(stdout, report.Text())
	return 0
}

func runSecurityWorkflow(ctx Context, mode securityreview.Mode, defaultWorkflow securityreview.Workflow, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(string(mode), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	workflowValue := fs.String("workflow", string(defaultWorkflow), "")
	formatValue := fs.String("format", string(securityreview.FormatMarkdown), "")
	scopeValue := fs.String("scope", "", "")
	evidenceValue := fs.String("evidence", string(securityreview.EvidenceRepro), "")
	targetCmdValue := fs.String("target-cmd", "", "")
	corpusValue := fs.String("corpus", "", "")
	artifactsValue := fs.String("artifacts-dir", "", "")
	budgetValue := fs.Duration("budget", 0, "")
	timeoutValue := fs.Duration("timeout", 0, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	scope := strings.TrimSpace(*scopeValue)
	if scope == "" {
		remaining := fs.Args()
		if len(remaining) > 1 {
			return fail(stderr, fmt.Errorf("expected at most one scope argument"))
		}
		if len(remaining) == 1 {
			scope = remaining[0]
		}
	}
	report, err := securityreview.Run(ctx.Root, securityreview.Options{
		Mode:         mode,
		Workflow:     securityreview.Workflow(strings.ToLower(strings.TrimSpace(*workflowValue))),
		Scope:        scope,
		Format:       securityreview.OutputFormat(strings.ToLower(strings.TrimSpace(*formatValue))),
		Evidence:     securityreview.EvidencePreference(strings.ToLower(strings.TrimSpace(*evidenceValue))),
		TargetCmd:    strings.TrimSpace(*targetCmdValue),
		CorpusDir:    strings.TrimSpace(*corpusValue),
		ArtifactsDir: strings.TrimSpace(*artifactsValue),
		Budget:       *budgetValue,
		Timeout:      *timeoutValue,
	})
	if err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintln(stdout, report.Render(securityreview.OutputFormat(strings.ToLower(strings.TrimSpace(*formatValue)))))
	return 0
}

func runPlanCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	request := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if request == "" {
		return fail(stderr, fmt.Errorf("plan requires a task description"))
	}
	plan, err := planning.Create(ctx.Root, planning.Options{Request: request})
	if err != nil {
		return fail(stderr, err)
	}
	if *jsonOut {
		data, _ := json.MarshalIndent(plan, "", "  ")
		_, _ = fmt.Fprintln(stdout, string(data))
		return 0
	}
	_, _ = fmt.Fprintln(stdout, planning.RenderMarkdown(plan))
	return 0
}

func runSubsystems(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("subsystems", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 32, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	m, err := manifest.Build(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	for i, subsystem := range m.TopLevelModule {
		if i == *limit {
			break
		}
		_, _ = fmt.Fprintf(stdout, "%s\t%d\t%s\n", subsystem.Name, subsystem.FileCount, subsystem.Notes)
	}
	return 0
}

func runCommands(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("commands", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 20, "")
	queryValue := fs.String("query", "", "")
	noPlugin := fs.Bool("no-plugin-commands", false, "")
	noSkill := fs.Bool("no-skill-commands", false, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	if *queryValue != "" {
		rendered, err := commands.RenderIndexAtRoot(ctx.Root, *limit, *queryValue)
		if err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintln(stdout, rendered)
		return 0
	}
	items, err := commands.ListAtRoot(ctx.Root, !*noPlugin, !*noSkill)
	if err != nil {
		return fail(stderr, err)
	}
	lines := []string{"Command entries: " + strconv.Itoa(len(items)), ""}
	limitValue := min(*limit, len(items))
	for _, module := range items[:limitValue] {
		lines = append(lines, "- "+module.Name+" - "+module.SourceHint)
	}
	_, _ = fmt.Fprintln(stdout, strings.Join(lines, "\n"))
	return 0
}

func runTools(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tools", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 20, "")
	queryValue := fs.String("query", "", "")
	simpleMode := fs.Bool("simple-mode", false, "")
	noMCP := fs.Bool("no-mcp", false, "")
	denyTool := multiString{}
	denyPrefix := multiString{}
	fs.Var(&denyTool, "deny-tool", "")
	fs.Var(&denyPrefix, "deny-prefix", "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	if *queryValue != "" {
		rendered, err := tools.RenderIndexAtRoot(ctx.Root, *limit, *queryValue, tools.CatalogOptions{
			SimpleMode: *simpleMode,
			IncludeMCP: !*noMCP,
		})
		if err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintln(stdout, rendered)
		return 0
	}
	permissionContext := permissions.FromIterables(denyTool, denyPrefix)
	items, err := tools.ListAtRoot(ctx.Root, *simpleMode, !*noMCP, &permissionContext)
	if err != nil {
		return fail(stderr, err)
	}
	lines := []string{"Tool entries: " + strconv.Itoa(len(items)), ""}
	limitValue := min(*limit, len(items))
	for _, module := range items[:limitValue] {
		lines = append(lines, "- "+module.Name+" - "+module.SourceHint)
	}
	_, _ = fmt.Fprintln(stdout, strings.Join(lines, "\n"))
	return 0
}

func runRoute(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("route", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 5, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" {
		return fail(stderr, fmt.Errorf("route requires a prompt"))
	}
	matches := hruntime.Harness{Root: ctx.Root}.RoutePrompt(prompt, *limit)
	if len(matches) == 0 {
		_, _ = fmt.Fprintln(stdout, "No live command/tool matches found.")
		return 0
	}
	for _, match := range matches {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%d\t%s\n", match.Kind, match.Name, match.Score, match.SourceHint)
	}
	return 0
}

func runBootstrap(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 5, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" {
		return fail(stderr, fmt.Errorf("bootstrap requires a prompt"))
	}
	session, err := hruntime.Harness{Root: ctx.Root}.BootstrapSession(prompt, *limit)
	if err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintln(stdout, session.Markdown())
	return 0
}

func runTurnLoop(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("turn-loop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 5, "")
	maxTurns := fs.Int("max-turns", 3, "")
	structuredOutput := fs.Bool("structured-output", false, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" {
		return fail(stderr, fmt.Errorf("turn-loop requires a prompt"))
	}
	results, err := hruntime.Harness{Root: ctx.Root}.RunTurnLoop(prompt, *limit, *maxTurns, *structuredOutput)
	if err != nil {
		return fail(stderr, err)
	}
	for i, result := range results {
		_, _ = fmt.Fprintf(stdout, "## Turn %d\n%s\nstop_reason=%s\n", i+1, result.Output, result.StopReason)
	}
	return 0
}

func runFlushTranscript(ctx Context, args []string, stdout, stderr io.Writer) int {
	prompt := strings.Join(args, " ")
	if prompt == "" {
		return fail(stderr, fmt.Errorf("flush-transcript requires a prompt"))
	}
	engine, err := query.FromWorkspace(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	engine.SubmitMessage(prompt, nil, nil, nil)
	path, err := engine.PersistSession()
	if err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintln(stdout, path)
	_, _ = fmt.Fprintln(stdout, "flushed="+strconv.FormatBool(engine.Transcript.Flushed))
	return 0
}

func runLoadSession(ctx Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return fail(stderr, fmt.Errorf("load-session requires a session id"))
	}
	engine, err := query.FromSavedSession(ctx.Root, args[0])
	if err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintf(stdout, "%s\n%d messages\nin=%d out=%d\n", engine.SessionID, len(engine.Messages), engine.TotalUsage.InputTokens, engine.TotalUsage.OutputTokens)
	return 0
}

func runSingleTarget(stdout io.Writer, args []string, fn func(string) modes.Report) int {
	if len(args) != 1 {
		return 1
	}
	_, _ = fmt.Fprintln(stdout, fn(args[0]).Text())
	return 0
}

func runShowCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return fail(stderr, fmt.Errorf("show-command requires a name"))
	}
	module, err := commands.GetAtRoot(ctx.Root, args[0])
	if err != nil {
		return fail(stderr, err)
	}
	if module == nil {
		return fail(stderr, fmt.Errorf("Command not found: %s", args[0]))
	}
	_, _ = fmt.Fprintln(stdout, strings.Join([]string{module.Name, module.SourceHint, module.Responsibility}, "\n"))
	return 0
}

func runShowTool(ctx Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return fail(stderr, fmt.Errorf("show-tool requires a name"))
	}
	module, err := tools.GetAtRoot(ctx.Root, args[0], tools.CatalogOptions{IncludeMCP: true})
	if err != nil {
		return fail(stderr, err)
	}
	if module == nil {
		return fail(stderr, fmt.Errorf("Tool not found: %s", args[0]))
	}
	_, _ = fmt.Fprintln(stdout, strings.Join([]string{module.Name, module.SourceHint, module.Responsibility}, "\n"))
	return 0
}

func runExecCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		return fail(stderr, fmt.Errorf("exec-command requires a name and prompt"))
	}
	result, err := commands.ExecuteAtRoot(ctx.Root, args[0], strings.Join(args[1:], " "))
	if err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintln(stdout, result.Message)
	if result.Handled {
		return 0
	}
	return 1
}

func runExecTool(ctx Context, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		return fail(stderr, fmt.Errorf("exec-tool requires a name and payload"))
	}
	result, err := tools.ExecuteAtRoot(ctx.Root, args[0], strings.Join(args[1:], " "), tools.CatalogOptions{IncludeMCP: true})
	if err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintln(stdout, result.Message)
	if result.Handled {
		return 0
	}
	return 1
}

func runPrompt(ctx Context, options globalOptions, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prompt", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 5, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" {
		var err error
		prompt, err = hruntime.ReadPrompt(stdin)
		if err != nil {
			return fail(stderr, err)
		}
	}
	if prompt == "" {
		return fail(stderr, fmt.Errorf("prompt text is required"))
	}
	if hruntime.LiveConfigured() || liveConfigAvailable(ctx.Root) {
		if options.OutputFormat != "text" && options.OutputFormat != "json" {
			return fail(stderr, fmt.Errorf("unsupported output format: %s", options.OutputFormat))
		}
		harness, err := newLiveHarness(ctx.Root)
		if err != nil {
			return fail(stderr, err)
		}
		summary, err := runLivePrompt(harness, ctx.Root, prompt, options, stdin, stdout, stderr)
		if err != nil {
			return fail(stderr, err)
		}
		if options.OutputFormat == "json" {
			_, _ = fmt.Fprintln(stdout, summary.JSON())
			return 0
		}
		_, _ = fmt.Fprintln(stdout, summary.Message)
		return 0
	}
	if options.OutputFormat == "json" || options.Resume != "" {
		return fail(stderr, fmt.Errorf("live prompt flags require ANTHROPIC_API_KEY or configured OAuth credentials"))
	}
	session, err := hruntime.Harness{Root: ctx.Root}.BootstrapSession(prompt, *limit)
	if err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintln(stdout, session.TurnResult.Output)
	return 0
}

func runInteractiveREPL(ctx Context, options globalOptions, stdin io.Reader, stdout, stderr io.Writer) int {
	if options.OutputFormat != "text" {
		return fail(stderr, fmt.Errorf("interactive TUI only supports --output-format text"))
	}
	harness, err := newLiveHarness(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	sessionRef := options.Resume
	if strings.TrimSpace(sessionRef) == "" {
		sessionRef = "latest"
	}
	modelLabel, providerLabel, permissionLabel := promptDefaults(ctx.Root, options)
	err = launchREPL(context.Background(), repl.Config{
		In:  stdin,
		Out: stdout,
		Status: repl.Status{
			Product:    version.Product,
			Version:    version.Version,
			Workspace:  ctx.Root,
			SessionID:  resolveSessionLabel(ctx.Root, sessionRef),
			Model:      modelLabel,
			Provider:   providerLabel,
			Permission: permissionLabel,
			Recent:     latestSessionSummary(ctx.Root),
		},
		RunPrompt: func(runCtx context.Context, prompt string, emit func(tea.Msg)) {
			turnOptions := options
			turnOptions.Resume = sessionRef
			promptOptions := hruntime.PromptOptions{
				Model:          turnOptions.Model,
				Provider:       turnOptions.Provider,
				PermissionMode: turnOptions.PermissionMode,
				AllowedTools:   turnOptions.AllowedTools,
				ResumeSession:  turnOptions.Resume,
				Progress: func(progress hruntime.PromptProgress) {
					emit(repl.ProgressEvent{Label: promptSpinnerLabel(progress.Phase)})
				},
				Activity: func(activity hruntime.ActivityEvent) {
					emit(repl.ActivityEvent{
						Kind:    activity.Kind,
						Title:   activity.Title,
						Summary: activity.Summary,
						Detail:  activity.Detail,
						Error:   activity.Error,
						EntryID: activity.EntryID,
					})
				},
			}
			promptOptions.Prompter = tuiApprovalPrompter{emit: emit}
			summary, err := harness.RunPrompt(runCtx, prompt, promptOptions)
			if err != nil {
				emit(repl.TurnFailed{Message: err.Error()})
				return
			}
			if sessionRef == "" {
				sessionRef = "latest"
			}
			emit(repl.TurnComplete{
				Message:   summary.Message,
				EntryID:   resultEntryID(summary.TurnID, summary.Iterations),
				SessionID: fallbackString(summary.SessionID, resolveSessionLabel(ctx.Root, sessionRef)),
				Model:     fallbackString(summary.Model, turnOptions.Model),
				Provider:  summary.Provider,
				TokensIn:  summary.Usage.InputTokens + summary.Usage.CacheCreationInputTokens + summary.Usage.CacheReadInputTokens,
				TokensOut: summary.Usage.OutputTokens,
				CostEst:   summary.EstimatedCost,
			})
		},
		HandleSlash: func(_ context.Context, line string) repl.SlashResult {
			result := runSlashInTUI(ctx, options, line)
			switch {
			case result.ResetState:
				sessionRef = ""
			case strings.HasPrefix(line, "/session switch"):
				if strings.TrimSpace(options.Resume) == "" {
					sessionRef = "latest"
				}
			}
			if result.SessionID == "" {
				result.SessionID = resolveSessionLabel(ctx.Root, sessionRef)
			}
			// Propagate model/provider changes so future RunPrompt calls use them
			if strings.TrimSpace(result.UpdateModel) != "" {
				options.Model = result.UpdateModel
			}
			if strings.TrimSpace(result.UpdateProvider) != "" {
				switch {
				case strings.HasPrefix(line, "/provider") && strings.Contains(line, "auto"):
					options.Provider = ""
				case strings.HasPrefix(line, "/model") && options.Provider == "":
					// Keep provider auto-selected while refreshing the displayed label.
				default:
					options.Provider = api.ProviderKind(strings.TrimSpace(result.UpdateProvider))
				}
			}
			return result
		},
	})
	if err != nil {
		return fail(stderr, err)
	}
	return 0
}

type tuiApprovalPrompter struct {
	emit func(tea.Msg)
}

func (p tuiApprovalPrompter) Approve(toolName string, input string) (bool, error) {
	kind := "bash"
	if toolName == "plan_approval" {
		kind = "plan"
	}
	response := make(chan bool, 1)
	p.emit(repl.ApprovalRequest{
		ToolName: toolName,
		Input:    input,
		Kind:     kind,
		Response: response,
	})
	approved := <-response
	return approved, nil
}

func resultEntryID(turnID string, iteration int) string {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" || iteration <= 0 {
		return ""
	}
	return fmt.Sprintf("%s-iter-%d-result", turnID, iteration)
}

func runMemorySlashResult(ctx Context, args []string) repl.SlashResult {
	subcommand := ""
	if len(args) > 0 {
		subcommand = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch subcommand {
	case "clear":
		if err := workspace.ClearMemory(ctx.Root); err != nil {
			return repl.SlashResult{Output: "Failed to clear memory: " + err.Error(), Error: true}
		}
		return repl.SlashResult{Output: "Workspace memory cleared."}
	case "add":
		note := strings.TrimSpace(strings.Join(args[1:], " "))
		if note == "" {
			return repl.SlashResult{Output: "Usage: /memory add <note text>"}
		}
		if err := workspace.AppendMemory(ctx.Root, note); err != nil {
			return repl.SlashResult{Output: "Failed to save note: " + err.Error(), Error: true}
		}
		return repl.SlashResult{Output: fmt.Sprintf("Saved to workspace memory: %s", note)}
	default:
		mem := workspace.ReadMemory(ctx.Root)
		if mem == "" {
			return repl.SlashResult{Output: "Workspace memory is empty.\nAdd notes with: /memory add <note>"}
		}
		return repl.SlashResult{Output: "## Workspace Memory\n\n" + mem}
	}
}

func runCommitSlashResult(ctx Context) repl.SlashResult {
	diffCmd := exec.Command("git", "diff", "--staged")
	diffCmd.Dir = ctx.Root
	diffOut, err := diffCmd.Output()
	if err != nil {
		return repl.SlashResult{Output: "git diff --staged failed: " + err.Error(), Error: true}
	}
	if strings.TrimSpace(string(diffOut)) == "" {
		return repl.SlashResult{Output: "Nothing staged. Use `git add <file>` first, then /commit."}
	}
	statusCmd := exec.Command("git", "status", "--short")
	statusCmd.Dir = ctx.Root
	statusOut, _ := statusCmd.Output()
	prompt := "You are a senior engineer. Given the following staged git diff, write a single " +
		"conventional commit message (format: `type: subject` where subject is ≤72 chars, lowercase, no period). " +
		"Then immediately run the commit using bash: git commit -m \"<your message>\". " +
		"Do not ask for confirmation — just write the message and commit.\n\n" +
		"Git status:\n" + string(statusOut) + "\nGit diff --staged:\n" + string(diffOut)
	return repl.SlashResult{
		Output:    "Generating commit message from staged changes…",
		RunPrompt: prompt,
	}
}

func runSlashInTUI(ctx Context, options globalOptions, line string) repl.SlashResult {
	args, err := splitInteractiveCommand(line)
	if err != nil {
		return repl.SlashResult{Output: err.Error(), Error: true}
	}
	// Handle TUI-only commands that update session state but don't need runSlashCommand
	if len(args) > 0 {
		switch args[0] {
		case "/model":
			if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
				return repl.SlashResult{Output: renderModelList(options.Model, options.Provider)}
			}
			newModel := strings.TrimSpace(args[1])
			_, providerLabel, _ := promptDefaults(ctx.Root, globalOptions{
				Model:          newModel,
				Provider:       options.Provider,
				PermissionMode: options.PermissionMode,
			})
			return repl.SlashResult{
				Output:         fmt.Sprintf("Model set to %s for this session.", newModel),
				UpdateModel:    newModel,
				UpdateProvider: providerLabel,
			}
		case "/summary":
			return repl.SlashResult{
				Output: "Asking the model to summarize this session…",
				RunPrompt: "Please write a concise summary of everything we have accomplished in this session: " +
					"what was asked, what approach was taken, which files were changed and how, " +
					"what problems were solved, and what the current state is. " +
					"Keep the summary under 300 words and use bullet points where helpful.",
			}
		case "/provider":
			if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
				return repl.SlashResult{Output: renderProviderList(options.Provider)}
			}
			newProvider := strings.TrimSpace(args[1])
			effectiveProvider := newProvider
			if strings.EqualFold(newProvider, "auto") {
				_, providerLabel, _ := promptDefaults(ctx.Root, globalOptions{
					Model:          options.Model,
					PermissionMode: options.PermissionMode,
				})
				effectiveProvider = providerLabel
			}
			return repl.SlashResult{
				Output:         fmt.Sprintf("Provider set to %s for this session.", newProvider),
				UpdateProvider: effectiveProvider,
			}
		case "/plan":
			task := strings.TrimSpace(strings.Join(args[1:], " "))
			taskDesc := task
			if taskDesc == "" {
				taskDesc = "the task described in this conversation"
			}
			plan, err := planning.Create(ctx.Root, planning.Options{Request: taskDesc})
			if err != nil {
				return repl.SlashResult{Output: err.Error(), Error: true}
			}
			return repl.SlashResult{
				Output: planning.RenderMarkdown(plan),
			}
		case "/memory":
			return runMemorySlashResult(ctx, args[1:])
		case "/commit":
			return runCommitSlashResult(ctx)
		}
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runSlashCommand(ctx, options, args, &stdout, &stderr)
	output := strings.TrimSpace(stdout.String())
	errOutput := strings.TrimSpace(stderr.String())
	if errOutput != "" {
		if output != "" {
			output += "\n"
		}
		output += errOutput
	}
	result := repl.SlashResult{
		Output: strings.TrimSpace(output),
		Error:  code != 0,
	}
	switch {
	case line == "/clear" || line == "/clear --confirm" || strings.HasPrefix(line, "/session clear"):
		result.ResetState = true
		result.SessionID = "new"
	case strings.HasPrefix(line, "/session switch") && code == 0:
		result.SessionID = resolveSessionLabel(ctx.Root, "latest")
	case line == "/help":
		result.OutputKind = "help"
	}
	return result
}

func runLivePrompt(harness livePromptHarness, root, prompt string, options globalOptions, stdin io.Reader, stdout, stderr io.Writer) (hruntime.PromptSummary, error) {
	promptOptions := hruntime.PromptOptions{
		Model:          options.Model,
		Provider:       options.Provider,
		PermissionMode: options.PermissionMode,
		AllowedTools:   options.AllowedTools,
		ResumeSession:  options.Resume,
	}
	basePrompter := stdioPrompter{stdin: stdin, stdout: stdout}
	promptOptions.Prompter = basePrompter
	var spinner *spinnerController
	if options.OutputFormat == "text" && isInteractiveWriter(stderr) {
		spinner = newSpinnerController(newPromptSpinner(stderr))
		spinner.Start(promptSpinnerLabel(hruntime.PromptPhaseStarting))
		promptOptions.Prompter = spinnerAwarePrompter{base: basePrompter, spinner: spinner}
		promptOptions.Progress = func(progress hruntime.PromptProgress) {
			spinner.Update(promptSpinnerLabel(progress.Phase))
		}
	}
	summary, err := harness.RunPrompt(context.Background(), prompt, promptOptions)
	if spinner != nil {
		spinner.Stop()
	}
	return summary, err
}

func promptDefaults(root string, options globalOptions) (string, string, string) {
	modelLabel := ""
	providerLabel := "auto"
	permissionLabel := string(hruntime.DefaultPromptOptions().PermissionMode)
	runtimeConfig, err := config.Load(root)
	if err == nil {
		if value := runtimeConfig.Model(); value != "" {
			modelLabel = value
		}
		if value := runtimeConfig.ProviderSettings().Kind; value != "" {
			providerLabel = value
		}
		if value := runtimeConfig.PermissionMode(); value != "" {
			permissionLabel = value
		}
	}
	if value := strings.TrimSpace(options.Model); value != "" {
		modelLabel = value
	}
	if value := strings.TrimSpace(string(options.Provider)); value != "" {
		providerLabel = value
	}
	if value := strings.TrimSpace(string(options.PermissionMode)); value != "" {
		permissionLabel = value
	}
	if modelLabel == "" {
		modelLabel = "not configured"
	}
	// If provider is still "auto", resolve what will actually be used given the
	// current environment so the header shows e.g. "openrouter" not "auto".
	if providerLabel == "auto" || providerLabel == "" {
		settings := runtimeConfig.ProviderSettings()
		route, err := api.ResolveModelRoute(modelLabel, api.ProviderConfig{
			AnthropicBaseURL:  settings.AnthropicBaseURL,
			GoogleBaseURL:     settings.GoogleBaseURL,
			OpenAIBaseURL:     settings.OpenAIBaseURL,
			OpenRouterBaseURL: settings.OpenRouterBaseURL,
			XAIBaseURL:        settings.XAIBaseURL,
			ProxyURL:          settings.ProxyURL,
		})
		if err == nil && route.Provider != "" {
			providerLabel = string(route.Provider)
		}
	}
	return modelLabel, providerLabel, permissionLabel
}

func resolveSessionLabel(root, reference string) string {
	if strings.TrimSpace(reference) == "" {
		return "new"
	}
	session, err := sessions.LoadManaged(root, reference)
	if err != nil {
		if strings.EqualFold(reference, "latest") {
			return "new"
		}
		return strings.TrimSpace(reference)
	}
	return session.Meta.SessionID
}

func latestSessionSummary(root string) repl.RecentSession {
	summary, err := sessions.Latest(root)
	if err != nil {
		return repl.RecentSession{}
	}
	session, err := sessions.LoadManaged(root, summary.ID)
	if err != nil {
		return repl.RecentSession{
			ID:           summary.ID,
			MessageCount: summary.MessageCount,
		}
	}
	recent := repl.RecentSession{
		ID:           session.Meta.SessionID,
		MessageCount: len(session.Messages),
	}
	if session.Meta.UpdatedAtMS > 0 {
		recent.UpdatedLabel = "Updated " + time.UnixMilli(session.Meta.UpdatedAtMS).Format("Jan 2 15:04")
	}
	if count := len(session.Meta.PromptHistory); count > 0 {
		recent.LastPrompt = strings.TrimSpace(session.Meta.PromptHistory[count-1].Text)
	}
	return recent
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func displayProvider(provider api.ProviderKind) string {
	if provider == "" {
		return "auto"
	}
	return string(provider)
}

// modelsByProvider lists curated recommended models for each provider.
var modelsByProvider = map[string][]struct{ name, note string }{
	"anthropic": {
		{"claude-opus-4-7", "most capable"},
		{"claude-sonnet-4-6", "recommended ←"},
		{"claude-haiku-4-5", "fastest"},
	},
	"google": {
		{"gemini-2.0-flash", "recommended ←"},
		{"gemini-2.0-flash-lite", "fastest"},
		{"gemini-1.5-pro", ""},
		{"gemini-1.5-flash", ""},
	},
	"openai": {
		{"gpt-4o", "recommended ←"},
		{"gpt-4o-mini", "fastest"},
		{"o3", "reasoning"},
		{"o4-mini", "fast reasoning"},
	},
	"openrouter": {
		{"anthropic/claude-opus-4-7", "most capable"},
		{"anthropic/claude-sonnet-4-6", "recommended ←"},
		{"openai/gpt-4o", ""},
		{"google/gemini-2.0-flash", ""},
		{"x-ai/grok-3", ""},
		{"x-ai/grok-3-mini", ""},
		{"meta-llama/llama-4-maverick", ""},
		{"qwen/qwen3-235b-a22b", ""},
	},
	"xai": {
		{"grok-3", "recommended ←"},
		{"grok-3-mini", "fastest"},
	},
}

func renderModelList(currentModel string, currentProvider api.ProviderKind) string {
	providerKey := strings.ToLower(string(currentProvider))
	if providerKey == "" {
		providerKey = "auto"
	}
	providerLabel := providerKey
	if providerLabel == "" {
		providerLabel = "auto"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Current model:    %s\n", fallbackString(currentModel, "not set")))
	sb.WriteString(fmt.Sprintf("Current provider: %s\n", providerLabel))

	if models, ok := modelsByProvider[providerKey]; ok {
		sb.WriteString(fmt.Sprintf("\nAvailable models (%s):\n", providerLabel))
		for _, m := range models {
			if m.note != "" {
				sb.WriteString(fmt.Sprintf("  %-42s %s\n", m.name, m.note))
			} else {
				sb.WriteString("  " + m.name + "\n")
			}
		}
	} else {
		sb.WriteString("\nAvailable models by provider — use /provider to switch:\n")
		for provider, models := range modelsByProvider {
			sb.WriteString(fmt.Sprintf("\n  %s:\n", provider))
			for _, m := range models {
				sb.WriteString("    " + m.name + "\n")
			}
		}
	}

	sb.WriteString("\nUsage: /model <model-name>\n")
	sb.WriteString("       /model <provider/model-name>   to switch via OpenRouter\n")
	sb.WriteString("Example: /model gemini-2.0-flash\n")
	sb.WriteString("See /provider to switch providers")
	return sb.String()
}

func renderProviderList(currentProvider api.ProviderKind) string {
	current := fallbackString(string(currentProvider), "auto")
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Current provider: %s\n", current))
	sb.WriteString("\nAvailable providers:\n")
	providers := []struct{ name, desc string }{
		{"anthropic", "Direct Anthropic API  (ANTHROPIC_API_KEY)"},
		{"google", "Google Gemini API     (GOOGLE_API_KEY)"},
		{"openai", "OpenAI-compatible API (OPENAI_API_KEY)"},
		{"openrouter", "OpenRouter gateway    (OPENROUTER_API_KEY) — access all models"},
		{"xai", "xAI Grok API          (XAI_API_KEY)"},
		{"auto", "Auto-detect from credentials"},
	}
	for _, p := range providers {
		marker := "  "
		if p.name == current {
			marker = "→ "
		}
		sb.WriteString(fmt.Sprintf("%s%-12s %s\n", marker, p.name, p.desc))
	}
	sb.WriteString("\nUsage: /provider <name>\n")
	sb.WriteString("Example: /provider google\n")
	sb.WriteString("See /model for available models per provider")
	return sb.String()
}

func promptSpinnerLabel(phase hruntime.PromptPhase) string {
	switch phase {
	case hruntime.PromptPhaseStarting:
		return "Starting"
	case hruntime.PromptPhaseWaitingModel:
		return "Thinking"
	case hruntime.PromptPhaseExecutingTools:
		return "Using tools"
	case hruntime.PromptPhaseFinalizing:
		return "Finalizing"
	default:
		return "Working"
	}
}

func runAgentsCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agents", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	items, err := agents.Load(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	if *jsonOut {
		_, _ = fmt.Fprintln(stdout, agents.RenderReportJSON(ctx.Root, items))
		return 0
	}
	_, _ = fmt.Fprintln(stdout, agents.RenderReport(items))
	return 0
}

func runSkillsCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("skills", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	remaining := fs.Args()
	if len(remaining) > 0 && remaining[0] == "install" {
		if len(remaining) < 2 {
			return fail(stderr, fmt.Errorf("usage: skills install <path>"))
		}
		result, err := skills.Install(ctx.Root, remaining[1])
		if err != nil {
			return fail(stderr, err)
		}
		if *jsonOut {
			_, _ = fmt.Fprintln(stdout, plugins.RenderJSON("skills", result))
			return 0
		}
		_, _ = fmt.Fprintln(stdout, skills.RenderInstall(result))
		return 0
	}
	items, err := skills.Load(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	if *jsonOut {
		_, _ = fmt.Fprintln(stdout, skills.RenderReportJSON(items))
		return 0
	}
	_, _ = fmt.Fprintln(stdout, skills.RenderReport(items))
	return 0
}

func runPluginsCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plugins", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	manager, err := newPluginManager(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	remaining := fs.Args()
	action := "list"
	target := ""
	if len(remaining) > 0 {
		action = remaining[0]
	}
	if len(remaining) > 1 {
		target = remaining[1]
	}
	switch action {
	case "list":
		items, err := manager.ListInstalledPlugins()
		if err != nil {
			return fail(stderr, err)
		}
		if *jsonOut {
			_, _ = fmt.Fprintln(stdout, plugins.RenderJSON("plugins", items))
			return 0
		}
		_, _ = fmt.Fprintln(stdout, plugins.RenderReport(items))
		return 0
	case "install":
		if target == "" {
			return fail(stderr, fmt.Errorf("usage: plugins install <path>"))
		}
		result, err := manager.Install(target)
		if err != nil {
			return fail(stderr, err)
		}
		summary := pluginSummaryByID(manager, result.PluginID)
		if *jsonOut {
			_, _ = fmt.Fprintln(stdout, plugins.RenderJSON("plugins", result))
			return 0
		}
		_, _ = fmt.Fprintln(stdout, plugins.RenderInstallReport(result.PluginID, summary))
		return 0
	case "enable", "disable":
		if target == "" {
			return fail(stderr, fmt.Errorf("usage: plugins %s <name>", action))
		}
		summary, err := resolvePluginSummary(manager, target)
		if err != nil {
			return fail(stderr, err)
		}
		if action == "enable" {
			err = manager.Enable(summary.Metadata.ID)
		} else {
			err = manager.Disable(summary.Metadata.ID)
		}
		if err != nil {
			return fail(stderr, err)
		}
		updated, _ := resolvePluginSummary(manager, summary.Metadata.ID)
		if *jsonOut {
			_, _ = fmt.Fprintln(stdout, plugins.RenderJSON("plugins", updated))
			return 0
		}
		_, _ = fmt.Fprintln(stdout, plugins.RenderActionReport(action, updated))
		return 0
	case "uninstall":
		if target == "" {
			return fail(stderr, fmt.Errorf("usage: plugins uninstall <plugin-id>"))
		}
		if err := manager.Uninstall(target); err != nil {
			return fail(stderr, err)
		}
		if *jsonOut {
			_, _ = fmt.Fprintln(stdout, plugins.RenderJSON("plugins", map[string]string{"result": "uninstalled", "plugin_id": target}))
			return 0
		}
		_, _ = fmt.Fprintf(stdout, "Plugins\n  Result           uninstalled %s\n", target)
		return 0
	case "update":
		if target == "" {
			return fail(stderr, fmt.Errorf("usage: plugins update <plugin-id>"))
		}
		result, err := manager.Update(target)
		if err != nil {
			return fail(stderr, err)
		}
		summary := pluginSummaryByID(manager, result.PluginID)
		if *jsonOut {
			_, _ = fmt.Fprintln(stdout, plugins.RenderJSON("plugins", result))
			return 0
		}
		_, _ = fmt.Fprintln(stdout, plugins.RenderUpdateReport(result, summary))
		return 0
	default:
		return fail(stderr, fmt.Errorf("unknown plugins action %q", action))
	}
}

func runMCPCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	registry, err := newMCPRegistry(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	if err := registry.Discover(); err != nil {
		return fail(stderr, err)
	}
	remaining := fs.Args()
	if len(remaining) > 0 && remaining[0] == "show" {
		if len(remaining) < 2 {
			return fail(stderr, fmt.Errorf("usage: mcp show <server>"))
		}
		state := mcpStateByName(registry.States(), remaining[1])
		if *jsonOut {
			_, _ = fmt.Fprintln(stdout, mcp.RenderServerJSON(state, remaining[1]))
			return 0
		}
		_, _ = fmt.Fprintln(stdout, mcp.RenderServer(state, remaining[1]))
		return 0
	}
	if *jsonOut {
		_, _ = fmt.Fprintln(stdout, mcp.RenderSummaryJSON(registry.States()))
		return 0
	}
	_, _ = fmt.Fprintln(stdout, mcp.RenderSummary(registry.States()))
	return 0
}

func runStateCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("state", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	snapshot, err := workerstate.Load(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	if *jsonOut {
		_, _ = fmt.Fprintln(stdout, workerstate.RenderJSON(snapshot))
		return 0
	}
	_, _ = fmt.Fprintln(stdout, workerstate.RenderText(snapshot))
	return 0
}

func runSessionCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	action := "list"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "list":
		items, err := sessions.List(ctx.Root)
		if err != nil {
			return fail(stderr, err)
		}
		lines := []string{"Sessions"}
		if len(items) == 0 {
			lines = append(lines, "  No managed sessions recorded.")
		}
		for _, item := range items {
			line := "  " + item.ID + " · " + filepath.Base(filepath.Dir(item.Path)) + " · " + strconv.Itoa(item.MessageCount) + " messages"
			if item.ParentSessionID != "" {
				line += " · forked from " + item.ParentSessionID
			}
			lines = append(lines, line)
		}
		_, _ = fmt.Fprintln(stdout, strings.Join(lines, "\n"))
		return 0
	case "show":
		if len(args) < 2 {
			return fail(stderr, fmt.Errorf("usage: session show <session-id|path|latest>"))
		}
		session, err := sessions.LoadManaged(ctx.Root, args[1])
		if err != nil {
			return fail(stderr, err)
		}
		lines := []string{
			"Session",
			"ID               " + session.Meta.SessionID,
			"Model            " + valueOrUnknown(session.Meta.Model),
			"Messages         " + strconv.Itoa(len(session.Messages)),
			"Path             " + session.Path,
			"Workspace root   " + valueOrUnknown(session.Meta.WorkspaceRoot),
		}
		_, _ = fmt.Fprintln(stdout, strings.Join(lines, "\n"))
		return 0
	case "switch":
		if len(args) < 2 {
			return fail(stderr, fmt.Errorf("usage: session switch <session-id|path>"))
		}
		summary, err := sessions.Switch(ctx.Root, args[1])
		if err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintf(stdout, "Sessions\n  Active session    %s\n", summary.ID)
		return 0
	case "fork":
		branch := ""
		if len(args) > 1 {
			branch = args[1]
		}
		session, err := sessions.Fork(ctx.Root, "latest", branch)
		if err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintf(stdout, "Sessions\n  Forked session    %s\n", session.Meta.SessionID)
		return 0
	case "delete":
		if len(args) < 2 {
			return fail(stderr, fmt.Errorf("usage: session delete <session-id|path>"))
		}
		if err := sessions.Delete(ctx.Root, args[1]); err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintf(stdout, "Sessions\n  Deleted           %s\n", args[1])
		return 0
	case "export":
		reference := "latest"
		target := ""
		if len(args) > 1 {
			reference = args[1]
		}
		if len(args) > 2 {
			target = args[2]
		}
		path, err := sessions.Export(ctx.Root, reference, target)
		if err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintf(stdout, "Sessions\n  Exported          %s\n", path)
		return 0
	case "clear":
		if err := sessions.Clear(ctx.Root); err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintln(stdout, "Sessions\n  Cleared           latest session alias")
		return 0
	default:
		return fail(stderr, fmt.Errorf("unknown session action %q", action))
	}
}

func runMigrateCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return fail(stderr, err)
	}
	remaining := fs.Args()
	if len(remaining) != 1 || remaining[0] != "legacy" {
		return fail(stderr, fmt.Errorf("usage: migrate legacy"))
	}
	report, err := migrations.MigrateLegacy(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	if *jsonOut {
		_, _ = fmt.Fprintln(stdout, report.JSON())
		return 0
	}
	_, _ = fmt.Fprintln(stdout, report.Text())
	return 0
}

func runLogin(ctx Context, outputFormat string, stdout, stderr io.Writer) int {
	runtimeConfig, err := config.Load(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	settings := runtimeConfig.OAuth()
	if settings == nil {
		return fail(stderr, fmt.Errorf("oauth settings are not configured"))
	}
	callbackPort := settings.CallbackPort
	if callbackPort <= 0 {
		return fail(stderr, fmt.Errorf("oauth settings are missing callbackPort"))
	}
	redirectURI := oauth.LoopbackRedirectURI(callbackPort)
	pkce, err := oauth.GeneratePKCEPair()
	if err != nil {
		return fail(stderr, err)
	}
	state, err := oauthStateGenerator()
	if err != nil {
		return fail(stderr, err)
	}
	request := oauth.BuildAuthorizationRequest(settings, redirectURI, state, pkce)
	if outputFormat == "json" {
		_, _ = fmt.Fprintf(stdout, "{\"kind\":\"login_start\",\"callback_port\":%d,\"redirect_uri\":%q}\n", callbackPort, redirectURI)
	} else {
		_, _ = fmt.Fprintln(stdout, "Starting Ascaris OAuth login...")
		_, _ = fmt.Fprintf(stdout, "Listening for callback on %s\n", redirectURI)
	}
	authorizeURL := request.URL()
	if err := browserOpener(authorizeURL); err != nil {
		emitLoginBrowserOpenFailure(outputFormat, authorizeURL, err, stdout, stderr)
	}
	params, err := oauthWaitForCallback(context.Background(), callbackPort)
	if err != nil {
		return fail(stderr, err)
	}
	if params.Error != "" {
		return fail(stderr, fmt.Errorf("oauth callback returned %s: %s", params.Error, params.ErrorDescription))
	}
	if params.State != state {
		return fail(stderr, fmt.Errorf("oauth state mismatch"))
	}
	token, err := oauthCodeExchanger(context.Background(), &http.Client{Timeout: 30 * time.Second}, settings, params.Code, pkce.Verifier, redirectURI)
	if err != nil {
		return fail(stderr, err)
	}
	if err := oauth.SaveCredentials(config.ConfigHome(ctx.Root), token); err != nil {
		return fail(stderr, err)
	}
	if outputFormat == "json" {
		payload, _ := json.Marshal(map[string]any{
			"kind":          "login",
			"callback_port": callbackPort,
			"redirect_uri":  redirectURI,
			"message":       "Ascaris OAuth login complete.",
		})
		_, _ = fmt.Fprintln(stdout, string(payload))
		return 0
	}
	_, _ = fmt.Fprintln(stdout, "Ascaris OAuth login complete.")
	return 0
}

func runLogout(ctx Context, outputFormat string, stdout, stderr io.Writer) int {
	if err := oauth.ClearCredentials(config.ConfigHome(ctx.Root)); err != nil {
		return fail(stderr, err)
	}
	if outputFormat == "json" {
		payload, _ := json.Marshal(map[string]any{
			"kind":    "logout",
			"message": "Ascaris OAuth credentials cleared.",
		})
		_, _ = fmt.Fprintln(stdout, string(payload))
		return 0
	}
	_, _ = fmt.Fprintln(stdout, "Ascaris OAuth credentials cleared.")
	return 0
}

func emitLoginBrowserOpenFailure(outputFormat, authorizeURL string, err error, stdout, stderr io.Writer) {
	_, _ = fmt.Fprintf(stderr, "warning: failed to open browser automatically: %v\n", err)
	if outputFormat == "json" {
		_, _ = fmt.Fprintf(stderr, "Open this URL manually:\n%s\n", authorizeURL)
		return
	}
	_, _ = fmt.Fprintf(stdout, "Open this URL manually:\n%s\n", authorizeURL)
}

func openBrowser(url string) error {
	var commands [][]string
	switch {
	case runtime.GOOS == "darwin":
		commands = [][]string{{"open", url}}
	case runtime.GOOS == "windows":
		commands = [][]string{{"cmd", "/C", "start", "", url}}
	default:
		commands = [][]string{{"xdg-open", url}}
	}
	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		if err := cmd.Start(); err == nil {
			return nil
		} else if !isNotFound(err) {
			return err
		}
	}
	return fmt.Errorf("no supported browser opener command found")
}

func isNotFound(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(strings.ToLower(err.Error()), "executable file")
}

type stdioPrompter struct {
	stdin  io.Reader
	stdout io.Writer
}

func (p stdioPrompter) Approve(_ string, _ string) (bool, error) {
	if p.stdout != nil {
		_, _ = fmt.Fprintln(p.stdout, "Permission approval required")
		_, _ = fmt.Fprint(p.stdout, "Approve this tool call? [y/N]: ")
	}
	if p.stdin == nil {
		return false, nil
	}
	line, err := readLine(p.stdin)
	if err != nil && err != io.EOF {
		return false, err
	}
	if p.stdout != nil {
		_, _ = fmt.Fprintln(p.stdout)
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes", nil
}

type readStringer interface {
	ReadString(byte) (string, error)
}

type lineIO interface {
	io.Reader
	readStringer
}

type fileDescriptorCarrier interface {
	Fd() uintptr
}

func readLine(reader io.Reader) (string, error) {
	if reader == nil {
		return "", io.EOF
	}
	if buffered, ok := reader.(readStringer); ok {
		return buffered.ReadString('\n')
	}
	return bufio.NewReader(reader).ReadString('\n')
}

func sharedLineReader(reader io.Reader) lineIO {
	if reader == nil {
		return bufio.NewReader(strings.NewReader(""))
	}
	if buffered, ok := reader.(lineIO); ok {
		return buffered
	}
	return bufio.NewReader(reader)
}

func splitInteractiveCommand(input string) ([]string, error) {
	args := []string{}
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		args = append(args, current.String())
		current.Reset()
	}
	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		return nil, fmt.Errorf("unterminated escape in slash command")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted string in slash command")
	}
	flush()
	return args, nil
}

func isInteractiveStream(stream any) bool {
	carrier, ok := stream.(fileDescriptorCarrier)
	if !ok {
		return false
	}
	file := os.NewFile(carrier.Fd(), "")
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func parseGlobalOptions(args []string) (globalOptions, []string, error) {
	options := globalOptions{
		OutputFormat: "text",
	}
	index := 0
	for index < len(args) {
		arg := args[index]
		if !strings.HasPrefix(arg, "--") {
			break
		}
		name, inline, hasInline := strings.Cut(arg, "=")
		switch name {
		case "--model":
			value, next, err := resolveFlagValue(args, index, hasInline, inline)
			if err != nil {
				return options, nil, err
			}
			options.Model = value
			index = next
		case "--provider":
			value, next, err := resolveFlagValue(args, index, hasInline, inline)
			if err != nil {
				return options, nil, err
			}
			provider, err := api.ParseProviderKind(value)
			if err != nil {
				return options, nil, err
			}
			options.Provider = provider
			index = next
		case "--permission-mode":
			value, next, err := resolveFlagValue(args, index, hasInline, inline)
			if err != nil {
				return options, nil, err
			}
			options.PermissionMode = tools.PermissionMode(value)
			index = next
		case "--allowedTools":
			value, next, err := resolveFlagValue(args, index, hasInline, inline)
			if err != nil {
				return options, nil, err
			}
			options.AllowedTools = splitCSV(value)
			index = next
		case "--output-format":
			value, next, err := resolveFlagValue(args, index, hasInline, inline)
			if err != nil {
				return options, nil, err
			}
			options.OutputFormat = value
			index = next
		case "--resume":
			value, next, err := resolveFlagValue(args, index, hasInline, inline)
			if err != nil {
				return options, nil, err
			}
			options.Resume = value
			index = next
		default:
			return options, args[index:], nil
		}
	}
	return options, args[index:], nil
}

func resolveFlagValue(args []string, index int, hasInline bool, inline string) (string, int, error) {
	if hasInline {
		if strings.TrimSpace(inline) == "" {
			return "", 0, fmt.Errorf("flag value is required")
		}
		return inline, index + 1, nil
	}
	if index+1 >= len(args) {
		return "", 0, fmt.Errorf("flag value is required")
	}
	return args[index+1], index + 2, nil
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	raw := strings.Split(value, ",")
	items := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

type multiString []string

func (m *multiString) String() string {
	return strings.Join(*m, ",")
}

func (m *multiString) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, err.Error())
	return 1
}

func printHelp(stdout io.Writer) {
	help := map[string]any{
		"product": version.Product,
		"version": version.Version,
		"usage": []string{
			"ascaris  # starts the interactive TUI when stdin is a TTY",
			"ascaris [--model sonnet] [--provider anthropic] [--permission-mode workspace-write] [--output-format json] <prompt>",
			"ascaris prompt <text>",
			"ascaris security-review [--workflow auto|source|fuzz|binary] [--format markdown|json|both] [--scope path]",
			"ascaris fuzz [--scope path] [--budget 2s]",
			"ascaris crash-triage --target-cmd /path/to/binary --corpus corpus/",
			"ascaris --resume latest <prompt>",
			"ascaris --provider openai --model GLM-4.7-Flash prompt \"hello\"",
			"ascaris status [--json]",
			"ascaris doctor [--json]",
			"ascaris login",
			"ascaris session [list|show|switch|fork|delete|export|clear]",
			"ascaris team [list|create|delete]",
			"ascaris cron [list|add|remove]",
			"ascaris worker [list|create|get|observe|resolve-trust|await-ready|send-prompt|restart|terminate]",
		},
		"commands": []string{
			"review", "security-review", "bughunter", "fuzz", "crash-triage",
			"summary", "manifest", "parity-audit", "setup-report", "command-graph", "tool-pool",
			"bootstrap-graph", "subsystems", "commands", "tools", "route", "bootstrap", "turn-loop",
			"flush-transcript", "load-session", "remote-mode", "ssh-mode", "teleport-mode",
			"direct-connect-mode", "deep-link-mode", "show-command", "show-tool", "exec-command",
			"exec-tool", "agents", "skills", "team", "cron", "worker", "plugins", "mcp", "state", "session", "login", "logout",
			"migrate legacy", "status", "doctor", "sandbox", "prompt", "version",
		},
		"global_flags": []string{
			"--model <alias|model>",
			"--provider <anthropic|openai|openrouter|xai>",
			"--permission-mode <read-only|workspace-write|danger-full-access>",
			"--allowedTools <csv>",
			"--output-format <text|json>",
			"--resume <session-id|latest>",
			"security flags: --workflow --scope --target-cmd --corpus --artifacts-dir --budget --timeout --evidence --format",
		},
	}
	data, _ := json.MarshalIndent(help, "", "  ")
	_, _ = fmt.Fprintln(stdout, string(data))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func runSlashCommand(ctx Context, options globalOptions, args []string, stdout, stderr io.Writer) int {
	command := args[0]
	switch command {
	case "/help":
		_, _ = fmt.Fprintln(stdout, strings.Join([]string{
			"## Bug Finding",
			"/fuzz|[scope]|Fuzz-test a function or package",
			"/security-review|[scope]|Full source security audit",
			"/bughunter|[scope]|Logic and memory bug hunt",
			"/review|[scope]|Inspect code for bugs",
			"/crash-triage|--target-cmd <bin> --corpus <dir>|Triage crash reproducers",
			"",
			"## Model & Provider",
			"/model|<model-name>|Switch model for this session",
			"/provider|<name>|Switch provider (anthropic|openai|openrouter|xai|auto)",
			"",
			"## Session",
			"/session|[list|show|switch|fork|delete|export|clear]|Manage sessions",
			"/resume|<session-id|path>|Resume a saved session",
			"/summary||Ask the model to summarize this session",
			"/compact||Compact local session history",
			"/clear|[--confirm]|Clear the active managed session alias",
			"/export|[file]|Export session to file",
			"",
			"## Workspace & Info",
			"/status||Show workspace and session status",
			"/sandbox||Show sandbox isolation status",
			"/config|[section]|Inspect merged config",
			"/cost||Show cumulative token usage and estimated cost",
			"/version||Show CLI version and build info",
			"",
			"## Auth",
			"/login||Authenticate using OAuth",
			"/logout||Clear saved OAuth credentials",
			"",
			"## Agents & Extensions",
			"/agents|[--json]|Inspect available agents",
			"/skills|[list|install <path>]|Inspect or install skills",
			"/team|[list|create <name>|delete <id>]|Manage agent teams",
			"/cron|[list|add <schedule> <prompt>|remove <id>]|Scheduled prompts",
			"/worker|[list|create|get|observe|send-prompt|...]|Control coding workers",
			"/plugin|[list|install|enable|disable|uninstall|update]|Manage plugins",
			"/mcp|[list|show <server>]|Inspect MCP servers and tools",
			"/state||Inspect worker and recovery state",
			"/help||Show this command reference",
		}, "\n"))
		return 0
	case "/status":
		report, err := status.Build(ctx.Root)
		if err != nil {
			return fail(stderr, err)
		}
		if strings.TrimSpace(options.Model) != "" {
			report.Model = options.Model
		}
		if options.PermissionMode != "" {
			report.Permission = string(options.PermissionMode)
		}
		_, _ = fmt.Fprintln(stdout, report.Text())
		return 0
	case "/review":
		return runSecurityWorkflow(ctx, securityreview.ModeReview, securityreview.WorkflowSource, args[1:], stdout, stderr)
	case "/security-review":
		return runSecurityWorkflow(ctx, securityreview.ModeSecurityReview, securityreview.WorkflowAuto, args[1:], stdout, stderr)
	case "/bughunter":
		return runSecurityWorkflow(ctx, securityreview.ModeBugHunter, securityreview.WorkflowSource, args[1:], stdout, stderr)
	case "/fuzz":
		return runSecurityWorkflow(ctx, securityreview.ModeSecurityReview, securityreview.WorkflowFuzz, args[1:], stdout, stderr)
	case "/crash-triage":
		return runSecurityWorkflow(ctx, securityreview.ModeSecurityReview, securityreview.WorkflowBinary, args[1:], stdout, stderr)
	case "/sandbox":
		_, _ = fmt.Fprintln(stdout, "mode=workspace-write\nfilesystem=.ascaris-aware\nnetwork=local-only")
		return 0
	case "/config":
		return runConfigCommand(ctx, args[1:], stdout, stderr)
	case "/session":
		return runSessionCommand(ctx, args[1:], stdout, stderr)
	case "/resume":
		if len(args) < 2 {
			return fail(stderr, fmt.Errorf("usage: /resume <session-id|path>"))
		}
		return runSessionCommand(ctx, []string{"show", args[1]}, stdout, stderr)
	case "/compact":
		session, err := sessions.LoadManaged(ctx.Root, "latest")
		if err != nil {
			return fail(stderr, err)
		}
		if len(session.Messages) <= 4 {
			_, _ = fmt.Fprintln(stdout, "Session compact skipped: already below threshold.")
			return 0
		}
		removed := len(session.Messages) - 4
		session.Messages = append([]api.InputMessage(nil), session.Messages[removed:]...)
		session.RecordCompaction("manual slash compaction preserved the most recent four messages", removed)
		if _, err := sessions.SaveManaged(session, ctx.Root); err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintf(stdout, "Session compacted: removed %d messages.\n", removed)
		return 0
	case "/clear":
		rest := args[1:]
		if len(rest) > 1 {
			return fail(stderr, fmt.Errorf("usage: /clear [--confirm]"))
		}
		if len(rest) == 1 && rest[0] != "--confirm" {
			return fail(stderr, fmt.Errorf("usage: /clear [--confirm]"))
		}
		if err := sessions.Clear(ctx.Root); err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintln(stdout, "Cleared latest session alias.")
		return 0
	case "/export":
		target := ""
		if len(args) > 1 {
			target = args[1]
		}
		path, err := sessions.Export(ctx.Root, "latest", target)
		if err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintln(stdout, path)
		return 0
	case "/cost":
		session, err := sessions.LoadManaged(ctx.Root, "latest")
		if err != nil {
			return fail(stderr, err)
		}
		_, _ = fmt.Fprintln(stdout, formatSessionCost(session))
		return 0
	case "/version":
		_, _ = fmt.Fprintln(stdout, version.Product+" "+version.Version)
		return 0
	case "/login":
		return runLogin(ctx, "text", stdout, stderr)
	case "/logout":
		return runLogout(ctx, "text", stdout, stderr)
	case "/agents":
		return runAgentsCommand(ctx, args[1:], stdout, stderr)
	case "/skills", "/skill":
		return runSkillsCommand(ctx, args[1:], stdout, stderr)
	case "/team":
		return runTeamCommand(ctx, args[1:], stdout, stderr)
	case "/cron":
		return runCronCommand(ctx, args[1:], stdout, stderr)
	case "/worker":
		return runWorkerCommand(ctx, args[1:], stdout, stderr)
	case "/plugin", "/plugins":
		return runPluginsCommand(ctx, args[1:], stdout, stderr)
	case "/mcp":
		return runMCPCommand(ctx, args[1:], stdout, stderr)
	case "/state":
		return runStateCommand(ctx, args[1:], stdout, stderr)
	}
	if strings.HasPrefix(command, "/oh-my-claudecode:") {
		return fail(stderr, fmt.Errorf("unknown slash command outside the REPL: %s\nCompatibility note: `%s` uses a legacy Claude Code/OMC plugin prefix. Import supported legacy assets with `ascaris migrate legacy`, then use the native `ascaris` command and plugin surface.", command, command))
	}
	suggestion := closestSlashCommand(command, []string{"/review", "/security-review", "/bughunter", "/fuzz", "/crash-triage", "/model", "/provider", "/help", "/status", "/sandbox", "/config", "/session", "/resume", "/compact", "/clear", "/export", "/cost", "/version", "/login", "/logout", "/agents", "/skills", "/team", "/cron", "/worker", "/plugin", "/mcp", "/state", "/plan", "/memory", "/commit", "/summary"})
	if suggestion != "" {
		return fail(stderr, fmt.Errorf("unknown slash command outside the REPL: %s\nDid you mean %s?", command, suggestion))
	}
	return fail(stderr, fmt.Errorf("unknown slash command outside the REPL: %s", command))
}

func runConfigCommand(ctx Context, args []string, stdout, stderr io.Writer) int {
	runtimeConfig, err := config.Load(ctx.Root)
	if err != nil {
		return fail(stderr, err)
	}
	sectionLabel := "merged"
	sectionValue := any(runtimeConfig.Merged())
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		sectionLabel = args[0]
		sectionValue = runtimeConfig.Section(args[0])
	}
	rendered := "null"
	if sectionValue != nil {
		if data, err := json.MarshalIndent(sectionValue, "", "  "); err == nil {
			rendered = string(data)
		}
	}
	lines := []string{
		"Config",
		"Config home       " + config.ConfigHome(ctx.Root),
		"Loaded files      " + strconv.Itoa(len(runtimeConfig.LoadedEntries())),
		"Merged section: " + sectionLabel,
		rendered,
	}
	for _, entry := range runtimeConfig.LoadedEntries() {
		lines = append(lines, filepath.Clean(entry.Path))
	}
	_, _ = fmt.Fprintln(stdout, strings.Join(lines, "\n"))
	return 0
}

func liveConfigAvailable(root string) bool {
	runtimeConfig, err := config.Load(root)
	if err != nil {
		return false
	}
	return runtimeConfig.OAuth() != nil
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func formatSessionCost(session sessions.ManagedSession) string {
	usage := session.Meta.Usage
	totalInput := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	total := totalInput + usage.OutputTokens
	cost := float64(totalInput)/1_000_000.0*15.0 + float64(usage.OutputTokens)/1_000_000.0*75.0
	lines := []string{
		"Usage",
		"Input tokens      " + strconv.Itoa(totalInput),
		"Output tokens     " + strconv.Itoa(usage.OutputTokens),
		"Total tokens      " + strconv.Itoa(total),
		"Estimated cost    " + fmt.Sprintf("$%.4f", cost),
	}
	return strings.Join(lines, "\n")
}

func closestSlashCommand(input string, commands []string) string {
	best := ""
	bestScore := 1 << 30
	for _, candidate := range commands {
		score := levenshteinDistance(input, candidate)
		if score < bestScore {
			bestScore = score
			best = candidate
		}
	}
	if bestScore > 4 {
		return ""
	}
	return best
}

func levenshteinDistance(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			current[j] = min3(
				current[j-1]+1,
				prev[j]+1,
				prev[j-1]+cost,
			)
		}
		prev = current
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if a <= b && a <= c {
		return a
	}
	if b <= c {
		return b
	}
	return c
}

func newPluginManager(root string) (plugins.Manager, error) {
	runtimeConfig, err := config.Load(root)
	if err != nil {
		return plugins.Manager{}, err
	}
	return plugins.NewManager(root, runtimeConfig), nil
}

func newMCPRegistry(root string) (*mcp.Registry, error) {
	runtimeConfig, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	return mcp.FromConfig(runtimeConfig), nil
}

func pluginSummaryByID(manager plugins.Manager, pluginID string) *plugins.Summary {
	items, err := manager.ListInstalledPlugins()
	if err != nil {
		return nil
	}
	for _, item := range items {
		if item.Metadata.ID == pluginID {
			copy := item
			return &copy
		}
	}
	return nil
}

func resolvePluginSummary(manager plugins.Manager, target string) (plugins.Summary, error) {
	items, err := manager.ListPlugins()
	if err != nil {
		return plugins.Summary{}, err
	}
	var matches []plugins.Summary
	for _, item := range items {
		if item.Metadata.ID == target || item.Metadata.Name == target {
			matches = append(matches, item)
		}
	}
	switch len(matches) {
	case 0:
		return plugins.Summary{}, fmt.Errorf("plugin %q is not installed or discoverable", target)
	case 1:
		return matches[0], nil
	default:
		return plugins.Summary{}, fmt.Errorf("plugin name %q is ambiguous; use the full plugin id", target)
	}
}

func mcpStateByName(states []mcp.ServerState, name string) *mcp.ServerState {
	for _, state := range states {
		if state.ServerName == name {
			copy := state
			return &copy
		}
	}
	return nil
}
