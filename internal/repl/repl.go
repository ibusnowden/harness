package repl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	minWidth  = 60
	minHeight = 16
)

type Status struct {
	Product    string
	Version    string
	Workspace  string
	SessionID  string
	Model      string
	Provider   string
	Permission string
	Recent     RecentSession
	// Token usage populated after each completed turn
	TokensIn  int
	TokensOut int
	CostEst   string // pre-formatted, e.g. "$0.003"
}

type RecentSession struct {
	ID           string
	MessageCount int
	LastPrompt   string
	UpdatedLabel string
}

type ProgressEvent struct {
	Label string
}

type ActivityEvent struct {
	Kind    string
	Title   string
	Summary string
	Detail  string
	Error   bool
	EntryID string
}

type ApprovalRequest struct {
	ToolName string
	Input    string
	Response chan bool
}

type TurnComplete struct {
	Message   string
	EntryID   string
	SessionID string
	Model     string
	Provider  string
	TokensIn  int
	TokensOut int
	CostEst   string // pre-formatted, e.g. "$0.003"
}

type TurnFailed struct {
	Message string
}

type Notice struct {
	Title   string
	Message string
	Error   bool
}

type SlashResult struct {
	Output         string
	Error          bool
	ResetState     bool
	SessionID      string
	UpdateModel    string // non-empty → update displayed model in header
	UpdateProvider string // non-empty → update displayed provider in header
	RunPrompt      string // non-empty → dispatch this as a new prompt turn
	OutputKind     string // if set, overrides "system" transcript kind for custom rendering
}

type Config struct {
	In          io.Reader
	Out         io.Writer
	Status      Status
	RunPrompt   func(context.Context, string, func(tea.Msg))
	HandleSlash func(context.Context, string) SlashResult
}

type transcriptEntry struct {
	id    string
	label string
	body  string
	kind  string
}

type activityRecord struct {
	id      string
	title   string
	summary string
	detail  string
	kind    string
	err     bool
}

type focusTarget int

const (
	focusTranscript focusTarget = iota
	focusActivity
	focusInput
)

type spinnerTick time.Time

// slashItem is a single autocomplete entry for the command picker.
type slashItem struct {
	name    string
	summary string
}

// completion holds the state for the slash-command autocomplete overlay.
type completion struct {
	active bool
	query  string
	items  []slashItem
	cursor int
	offset int // first visible row in the overlay list
}

// slashRegistry is the authoritative list of slash commands shown in autocomplete.
// NOTE: keep this in sync with runSlashCommand in internal/cli/cli.go — any new
// slash command added there must also appear here, or it will not show in the picker.
var slashRegistry = []slashItem{
	{"/help", "Show available commands"},
	{"/status", "Show workspace and session status"},
	{"/review", "Inspect code for bugs"},
	{"/security-review", "Full source security audit"},
	{"/bughunter", "Logic and memory bug hunt"},
	{"/fuzz", "Fuzz-test a function or package"},
	{"/crash-triage", "Triage crash reproducers for binary targets"},
	{"/sandbox", "Show sandbox isolation status"},
	{"/config", "Inspect merged config or a config section"},
	{"/session", "List, switch, fork, delete, export, or clear sessions"},
	{"/resume", "Resume a saved session by ID or path"},
	{"/compact", "Compact local session history"},
	{"/clear", "Clear the active managed session alias"},
	{"/export", "Export a managed session to file"},
	{"/cost", "Show cumulative token usage and estimated cost"},
	{"/version", "Show CLI version and build info"},
	{"/login", "Authenticate using OAuth"},
	{"/logout", "Clear saved OAuth credentials"},
	{"/agents", "Inspect available agents"},
	{"/skills", "Inspect or install available skills"},
	{"/team", "List, create, or delete agent teams"},
	{"/cron", "List, add, or remove scheduled prompts"},
	{"/worker", "Inspect and control coding worker boot state"},
	{"/plugin", "Inspect or manage plugins"},
	{"/mcp", "Inspect configured MCP servers and tools"},
	{"/state", "Inspect worker and recovery state"},
	{"/model", "Switch model for this session"},
	{"/provider", "Switch provider for this session"},
	{"/summary", "Ask the model to summarize this session"},
}

// verbForActivityKind maps an activity event kind to a human-readable spinner verb.
func verbForActivityKind(kind string) string {
	switch kind {
	case "file_read":
		return "Reading"
	case "file_write":
		return "Writing"
	case "file_edit":
		return "Editing"
	case "search":
		return "Searching"
	case "bash_start":
		return "Running"
	case "bash_result":
		return ""
	case "mcp_start":
		return "Querying"
	case "mcp_result":
		return ""
	case "plugin_start":
		return "Invoking"
	case "plugin_result":
		return ""
	case "tool_start":
		return "Executing"
	case "tool_result":
		return ""
	case "model":
		return "Thinking"
	case "approval":
		return "Waiting"
	case "error":
		return "Recovering"
	default:
		return "Working"
	}
}

type model struct {
	ctx        context.Context
	cancel     context.CancelFunc
	cfg        Config
	status     Status
	statusText string

	transcript viewport.Model
	activity   viewport.Model
	input      textarea.Model

	width  int
	height int

	transcriptEntries []transcriptEntry
	activities        []activityRecord
	expanded          map[int]bool
	selectedActivity  int
	showActivity      bool
	focus             focusTarget

	eventCh      chan tea.Msg
	busy         bool
	approval     *ApprovalRequest
	spinFrame    int
	spinVerb     string
	cwdShort     string // shortenPath(workspace), cached at init
	activityHint string
	startupNote  string
	entrySeq     int

	comp        completion
	pasteBlocks []string // full content of each paste block; index+1 = block number

	theme Theme
	md    *markdownRenderer
}

// activityKindGlyph maps activity event kinds to a single display glyph.
var activityKindGlyph = map[string]string{
	"tool_start":    "⚙",
	"tool_result":   "✓",
	"approval":      "?",
	"error":         "✗",
	"status":        "·",
	"prompt":        "›",
	"system":        "·",
	"model":         "·",
	"cache":         "⟲",
	"file_read":     "→",
	"file_write":    "←",
	"file_edit":     "≈",
	"search":        "⌕",
	"bash_start":    "⌘",
	"bash_result":   "⊣",
	"mcp_start":     "⎇",
	"mcp_result":    "⎈",
	"plugin_start":  "⋄",
	"plugin_result": "◆",
}

func Launch(parent context.Context, cfg Config) error {
	if cfg.RunPrompt == nil {
		return fmt.Errorf("repl run prompt handler is required")
	}
	if cfg.HandleSlash == nil {
		return fmt.Errorf("repl slash handler is required")
	}
	ctx, cancel := context.WithCancel(parent)
	m := newModel(ctx, cancel, cfg)

	// Bubble Tea requires a real TTY character device for raw-mode input.
	// When stdin has been redirected (e.g. inside Claude Code's terminal or any
	// subprocess), opening /dev/tty directly gives us the controlling terminal
	// regardless of how the file descriptors were wired up.
	ttyIn := cfg.In
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer tty.Close()
		ttyIn = tty
	}

	program := tea.NewProgram(m, tea.WithAltScreen(), tea.WithInput(ttyIn), tea.WithOutput(cfg.Out))
	_, err := program.Run()
	cancel()
	return err
}

func newModel(ctx context.Context, cancel context.CancelFunc, cfg Config) model {
	input := textarea.New()
	input.Prompt = "› "
	input.Placeholder = "Dispatch a task to the harness"
	input.ShowLineNumbers = false
	input.SetHeight(1)
	input.CharLimit = 0
	input.Focus()
	input.KeyMap.InsertNewline.SetEnabled(false)
	input.Cursor.SetMode(cursor.CursorBlink)
	applyTextareaTheme(&input)

	status := cfg.Status
	if strings.TrimSpace(status.SessionID) == "" {
		status.SessionID = "new"
	}
	m := model{
		ctx:          ctx,
		cancel:       cancel,
		cfg:          cfg,
		status:       status,
		statusText:   "Idle",
		cwdShort:     shortenPath(strings.TrimSpace(status.Workspace)),
		transcript:   viewport.New(80, 10),
		activity:     viewport.New(40, 10),
		input:        input,
		expanded:     map[int]bool{},
		showActivity: true,
		focus:        focusInput,
		eventCh:      make(chan tea.Msg, 64),
		theme:        DefaultTheme(),
		md:           newMarkdownRenderer(80),
	}
	m.refreshTranscript()
	m.refreshActivity()
	return m
}

func applyTextareaTheme(input *textarea.Model) {
	focused := input.FocusedStyle
	focused.Base = lipgloss.NewStyle().
		Foreground(lipgloss.Color("216")) // warm peach text
	focused.Text = lipgloss.NewStyle().
		Foreground(lipgloss.Color("216"))
	focused.CursorLine = lipgloss.NewStyle().
		Foreground(lipgloss.Color("216"))
	focused.CursorLineNumber = lipgloss.NewStyle().
		Foreground(lipgloss.Color("172")) // amber
	focused.Placeholder = lipgloss.NewStyle().
		Foreground(lipgloss.Color("137")) // brown-tan dimmed
	focused.Prompt = lipgloss.NewStyle().
		Foreground(lipgloss.Color("220")). // bright gold prompt glyph
		Bold(true)

	blurred := input.BlurredStyle
	blurred.Base = lipgloss.NewStyle().
		Foreground(lipgloss.Color("179")) // warm tan
	blurred.Text = lipgloss.NewStyle().
		Foreground(lipgloss.Color("179"))
	blurred.CursorLine = lipgloss.NewStyle().
		Foreground(lipgloss.Color("179"))
	blurred.CursorLineNumber = lipgloss.NewStyle().
		Foreground(lipgloss.Color("137"))
	blurred.Placeholder = lipgloss.NewStyle().
		Foreground(lipgloss.Color("137"))
	blurred.Prompt = lipgloss.NewStyle().
		Foreground(lipgloss.Color("172")) // deep orange

	input.FocusedStyle = focused
	input.BlurredStyle = blurred
}

func (m model) Init() tea.Cmd {
	// Enable bracketed paste so we can detect pasted content and show a compact
	// placeholder instead of dumping raw text into the input line.
	return tea.Batch(textarea.Blink, tea.EnableBracketedPaste)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.refreshTranscript()
		m.refreshActivity()
		return m, nil
	case spinnerTick:
		if !m.busy {
			// Turn ended while tick was in flight; don't reschedule.
			return m, nil
		}
		m.spinFrame = (m.spinFrame + 1) % len(wormFrames)
		return m, spinnerTickCmd()
	case ProgressEvent:
		if strings.TrimSpace(msg.Label) != "" {
			m.statusText = msg.Label
		}
		return m, waitForRuntimeEvent(m.eventCh)
	case ActivityEvent:
		m.handleActivityEvent(msg)
		return m, waitForRuntimeEvent(m.eventCh)
	case ApprovalRequest:
		m.approval = &msg
		m.statusText = "Approval required"
		return m, nil
	case TurnComplete:
		m.busy = false
		m.spinVerb = ""
		m.approval = nil
		m.statusText = "Idle"
		m.focus = focusTranscript
		if strings.TrimSpace(msg.SessionID) != "" {
			m.status.SessionID = msg.SessionID
		}
		if msg.TokensIn > 0 || msg.TokensOut > 0 {
			m.status.TokensIn = msg.TokensIn
			m.status.TokensOut = msg.TokensOut
			m.status.CostEst = msg.CostEst
		}
		if strings.TrimSpace(msg.Model) != "" {
			m.status.Model = msg.Model
		}
		if strings.TrimSpace(msg.Provider) != "" {
			m.status.Provider = msg.Provider
		}
		if strings.TrimSpace(msg.EntryID) != "" {
			m.upsertTranscript(msg.EntryID, "Result", msg.Message, "result")
		} else {
			m.appendTranscript("Result", msg.Message, "result")
		}
		m.recordActivity(activityRecord{
			title:   "Completed",
			summary: "The run completed with a final response.",
			detail:  strings.TrimSpace(msg.Message),
			kind:    "status",
		})
		return m, nil
	case TurnFailed:
		m.busy = false
		m.spinVerb = ""
		m.approval = nil
		m.statusText = "Error"
		m.focus = focusTranscript
		m.appendTranscript("Error", msg.Message, "error")
		m.recordActivity(activityRecord{
			title:   "Error",
			summary: msg.Message,
			detail:  msg.Message,
			kind:    "error",
			err:     true,
		})
		return m, nil
	case Notice:
		label := "System"
		kind := "system"
		if msg.Error {
			label = "Error"
			kind = "error"
		} else if strings.TrimSpace(msg.Title) != "" {
			label = msg.Title
		}
		m.appendTranscript(label, msg.Message, kind)
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Intercept bracketed paste before it reaches the textarea.
	if msg.Paste {
		return m.handlePaste(msg.String())
	}
	if m.approval != nil {
		switch msg.String() {
		case "y", "Y":
			m.approval.Response <- true
			m.recordActivity(activityRecord{title: "Approval", summary: "Approved by user.", kind: "approval"})
			m.approval = nil
			m.statusText = "Working"
			return m, waitForRuntimeEvent(m.eventCh)
		case "n", "N", "esc":
			m.approval.Response <- false
			m.recordActivity(activityRecord{title: "Approval", summary: "Denied by user.", kind: "approval", err: true})
			m.approval = nil
			m.statusText = "Working"
			return m, waitForRuntimeEvent(m.eventCh)
		default:
			return m, nil
		}
	}

	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		if m.busy {
			m.appendTranscript("Run State", "A turn is still running. Wait for completion before exiting.", "system")
			return m, nil
		}
		m.cancel()
		return m, tea.Quit
	case "f2", "ctrl+o":
		m.showActivity = !m.showActivity
		if !m.showActivity && m.focus == focusActivity {
			m.focus = focusInput
		}
		m.layout()
		m.refreshTranscript()
		m.refreshActivity()
		return m, nil
	}

	// When the autocomplete overlay is open, intercept navigation keys before
	// passing anything to the textarea or focus system.
	if m.comp.active && len(m.comp.items) > 0 {
		switch msg.String() {
		case "up":
			if m.comp.cursor > 0 {
				m.comp.cursor--
				m.clampCompletionOffset()
			}
			return m, nil
		case "down":
			if m.comp.cursor < len(m.comp.items)-1 {
				m.comp.cursor++
				m.clampCompletionOffset()
			}
			return m, nil
		case "tab", "enter":
			selected := m.comp.items[m.comp.cursor]
			m.input.SetValue(selected.name + " ")
			m.input.CursorEnd()
			m.comp.active = false
			return m, nil
		case "esc":
			m.comp.active = false
			return m, nil
		}
	}

	switch msg.String() {
	case "tab":
		m.focus = m.nextFocus()
		return m, nil
	case "shift+tab":
		m.focus = m.prevFocus()
		return m, nil
	}

	switch m.focus {
	case focusTranscript:
		switch msg.String() {
		case "up":
			m.transcript.LineUp(1)
		case "down":
			m.transcript.LineDown(1)
		case "pgup":
			m.transcript.HalfViewUp()
		case "pgdown":
			m.transcript.HalfViewDown()
		case "home":
			m.transcript.GotoTop()
		case "end":
			m.transcript.GotoBottom()
		default:
			m.focus = focusInput
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		return m, nil
	case focusActivity:
		visible := m.visibleActivityIndexes()
		switch msg.String() {
		case "up":
			m.moveActivitySelection(visible, -1)
		case "down":
			m.moveActivitySelection(visible, 1)
		case "pgup":
			m.activity.HalfViewUp()
		case "pgdown":
			m.activity.HalfViewDown()
		case "home":
			if len(visible) > 0 {
				m.selectedActivity = visible[0]
				m.refreshActivity()
			}
		case "end":
			if len(visible) > 0 {
				m.selectedActivity = visible[len(visible)-1]
				m.refreshActivity()
			}
		case " ", "enter", "right", "left":
			if len(visible) > 0 {
				m.expanded[m.selectedActivity] = !m.expanded[m.selectedActivity]
				m.refreshActivity()
			}
		default:
			return m, nil
		}
		return m, nil
	}

	// focusInput: Up/Down scroll the transcript without stealing focus,
	// so the user can read long output without needing to Tab away.
	switch msg.String() {
	case "up":
		m.transcript.LineUp(1)
		return m, nil
	case "down":
		m.transcript.LineDown(1)
		return m, nil
	case "pgup":
		m.transcript.HalfViewUp()
		return m, nil
	case "pgdown":
		m.transcript.HalfViewDown()
		return m, nil
	case "home":
		m.transcript.GotoTop()
		return m, nil
	case "end":
		m.transcript.GotoBottom()
		return m, nil
	case "ctrl+j", "alt+enter":
		m.input.InsertString("\n")
		return m, nil
	case "enter":
		return m.submitInput()
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// Update completion state whenever input changes.
	m.updateCompletion()
	return m, cmd
}

// expandPastePlaceholders replaces [Pasted text #N +M lines] tokens in s with
// the stored full content from m.pasteBlocks.
func (m model) expandPastePlaceholders(s string) string {
	for i, content := range m.pasteBlocks {
		extra := strings.Count(content, "\n")
		placeholder := pasteBlockPlaceholder(i+1, extra)
		s = strings.ReplaceAll(s, placeholder, content)
	}
	return s
}

// handlePaste handles a bracketed-paste event. Short pastes are inserted
// directly; long pastes are stored and replaced with a compact placeholder:
//
//	[Pasted text #1 +15 lines]
//
// The placeholder is expanded back to the full content at submit time.
func (m model) handlePaste(text string) (tea.Model, tea.Cmd) {
	const longLines = 3
	const longChars = 150

	lineCount := strings.Count(text, "\n") + 1
	if lineCount <= longLines && len(text) <= longChars {
		// Short paste — insert as-is.
		m.input.InsertString(text)
		m.updateCompletion()
		return m, nil
	}

	// Long paste — store full content and insert a compact pill.
	m.pasteBlocks = append(m.pasteBlocks, text)
	n := len(m.pasteBlocks)
	extra := lineCount - 1
	placeholder := fmt.Sprintf("[Pasted text #%d +%d lines]", n, extra)
	m.input.InsertString(placeholder)
	m.updateCompletion()
	return m, nil
}

// pasteBlockPlaceholder returns the placeholder string for paste block n (1-indexed).
func pasteBlockPlaceholder(n, extraLines int) string {
	return fmt.Sprintf("[Pasted text #%d +%d lines]", n, extraLines)
}

func (m model) submitInput() (tea.Model, tea.Cmd) {
	line := strings.TrimSpace(m.input.Value())
	if line == "" {
		return m, nil
	}
	if line == "/exit" || line == "exit" || line == "quit" {
		if m.busy {
			m.appendTranscript("Run State", "A turn is still running. Wait for completion before exiting.", "system")
			return m, nil
		}
		m.cancel()
		return m, tea.Quit
	}
	if strings.HasPrefix(line, "/") {
		if m.busy {
			m.appendTranscript("System", "Slash commands are disabled while a turn is running.", "system")
			return m, nil
		}
		m.input.Reset()
		result := m.cfg.HandleSlash(m.ctx, line)
		if result.ResetState {
			m.transcriptEntries = nil
			m.activities = nil
			m.expanded = map[int]bool{}
			m.selectedActivity = 0
			m.pasteBlocks = nil
			m.status.SessionID = "new"
			m.startupNote = strings.TrimSpace(result.Output)
			if strings.TrimSpace(result.Output) == "" {
				m.startupNote = "Session cleared. The next prompt will start fresh."
			}
		}
		if strings.TrimSpace(result.SessionID) != "" {
			m.status.SessionID = result.SessionID
		}
		if strings.TrimSpace(result.UpdateModel) != "" {
			m.status.Model = result.UpdateModel
		}
		if strings.TrimSpace(result.UpdateProvider) != "" {
			m.status.Provider = result.UpdateProvider
		}
		if strings.TrimSpace(result.Output) != "" && !result.ResetState {
			label := "System"
			kind := "system"
			if result.Error {
				label = "Error"
				kind = "error"
			} else if strings.TrimSpace(result.OutputKind) != "" {
				kind = result.OutputKind
			}
			m.appendTranscript(label, result.Output, kind)
			m.recordActivity(activityRecord{
				title:   label,
				summary: firstLine(result.Output),
				detail:  result.Output,
				kind:    kind,
				err:     result.Error,
			})
		}
		if result.ResetState && strings.TrimSpace(m.startupNote) != "" {
			m.recordActivity(activityRecord{
				title:   "System",
				summary: firstLine(m.startupNote),
				detail:  m.startupNote,
				kind:    "system",
			})
		}
		// If the slash command wants to dispatch a model turn, start it now.
		if strings.TrimSpace(result.RunPrompt) != "" && !m.busy {
			prompt := strings.TrimSpace(result.RunPrompt)
			m.startupNote = ""
			m.appendTranscript("Summary Request", "Asking the model to summarize this session…", "task")
			m.recordActivity(activityRecord{
				title:   "Summary Request",
				summary: "Model is writing a session summary.",
				kind:    "prompt",
			})
			m.busy = true
			m.spinVerb = "Summarizing"
			m.statusText = "Summarizing"
			return m, tea.Batch(runPromptCmd(m.ctx, m.cfg.RunPrompt, prompt, m.eventCh), waitForRuntimeEvent(m.eventCh), spinnerTickCmd())
		}
		return m, nil
	}
	if m.busy {
		m.appendTranscript("Run State", "A turn is already running. Wait for completion before sending another prompt.", "system")
		return m, nil
	}
	rawPrompt := m.input.Value()
	// Expand paste placeholders back to their full content before submitting.
	prompt := m.expandPastePlaceholders(rawPrompt)
	m.input.Reset()
	m.pasteBlocks = nil // placeholders consumed; reset for next turn
	m.startupNote = ""
	m.appendTranscript("Run Request", prompt, "task")
	m.recordActivity(activityRecord{
		title:   "Run Request",
		summary: firstLine(prompt),
		detail:  strings.TrimSpace(prompt),
		kind:    "prompt",
	})
	m.busy = true
	m.spinVerb = "Working"
	m.statusText = "Starting"
	return m, tea.Batch(runPromptCmd(m.ctx, m.cfg.RunPrompt, prompt, m.eventCh), waitForRuntimeEvent(m.eventCh), spinnerTickCmd())
}

func (m model) View() string {
	if m.width < minWidth || m.height < minHeight {
		return m.renderSmallTerminal()
	}
	header := m.panel(m.renderHeader(), m.width-2, 3, false)
	strip := m.renderActivityStrip()
	if m.approval != nil {
		main := m.panel(m.renderApprovalScreen(), m.width-2, max(10, m.height-10), false)
		composer := m.panel(m.theme.Help().Render("Approval pending. Choose approve once or deny to continue."), m.width-2, 1, false)
		footer := m.theme.Help().Render("Y approve once • N deny • Esc deny")
		return lipgloss.JoinVertical(lipgloss.Left, header, strip, main, composer, footer)
	}
	main := m.renderMainContent()
	completionOverlay := m.renderCompletion()
	composer := m.panel(m.renderComposerInner(), m.width-2, 2, m.focus == focusInput)
	footer := m.theme.Help().Render("Enter send • Up/Down scroll • / autocomplete • Ctrl+J newline • F2 activity • Tab focus • Ctrl+D exit")
	parts := []string{header, strip, main}
	if completionOverlay != "" {
		parts = append(parts, completionOverlay)
	}
	parts = append(parts, composer, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *model) layout() {
	if m.width <= 0 {
		m.width = 100
	}
	if m.height <= 0 {
		m.height = 32
	}
	if m.width < minWidth || m.height < minHeight {
		return
	}
	m.input.SetWidth(max(18, m.width-8))
	m.input.SetHeight(1)

	headerHeight := 5
	stripHeight := 1
	composerHeight := 4 // 2 content lines (cwd + input) + 2 border
	footerHeight := 1
	mainHeight := m.height - headerHeight - stripHeight - composerHeight - footerHeight - 4
	if mainHeight < 8 {
		mainHeight = 8
	}

	if m.showActivity {
		if m.width >= 120 {
			m.transcript.Width = max(24, ((m.width-6)*2/3)-2)
			m.activity.Width = max(24, (m.width-6)-m.transcript.Width-3)
			m.transcript.Height = mainHeight
			m.activity.Height = mainHeight
		} else {
			activityHeight := max(6, mainHeight/3)
			m.transcript.Width = max(18, m.width-6)
			m.activity.Width = max(18, m.width-6)
			m.activity.Height = activityHeight
			m.transcript.Height = max(6, mainHeight-activityHeight-1)
		}
	} else {
		m.transcript.Width = max(18, m.width-6)
		m.transcript.Height = mainHeight
		m.activity.Width = max(18, m.width-6)
		m.activity.Height = max(6, mainHeight/3)
	}
	m.md.Resize(m.transcript.Width)
}

func (m *model) appendTranscript(label, body, kind string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	m.entrySeq++
	m.transcriptEntries = append(m.transcriptEntries, transcriptEntry{
		id:    fmt.Sprintf("entry-%d", m.entrySeq),
		label: label,
		body:  body,
		kind:  kind,
	})
	m.refreshTranscript()
}

func (m *model) upsertTranscript(id, label, body, kind string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	for index := range m.transcriptEntries {
		if m.transcriptEntries[index].id != id {
			continue
		}
		m.transcriptEntries[index].label = label
		m.transcriptEntries[index].body = body
		m.transcriptEntries[index].kind = kind
		m.refreshTranscript()
		return
	}
	m.transcriptEntries = append(m.transcriptEntries, transcriptEntry{
		id:    id,
		label: label,
		body:  body,
		kind:  kind,
	})
	m.refreshTranscript()
}

func (m *model) handleActivityEvent(msg ActivityEvent) {
	// Update the contextual spinner verb while the harness is busy.
	if m.busy {
		if v := verbForActivityKind(msg.Kind); v != "" {
			m.spinVerb = v
		}
	}
	// For file_edit events, render a proper unified diff inline in the transcript.
	// The Detail already contains a fileDiff JSON payload from builtins.go.
	if msg.Kind == "file_edit" {
		if diff := renderInlineDiff(msg.Detail, m.theme, m.transcript.Width); diff != "" {
			// Keep summary as-is (already set by builtins.go: "Editing <path>").
			// Replace Detail with the rendered diff so timelineEntryForActivity
			// can include it directly.
			msg.Detail = diff
		}
	}
	record := activityRecord{
		id:      msg.EntryID,
		title:   fallback(msg.Title, "Activity"),
		summary: msg.Summary,
		detail:  msg.Detail,
		kind:    msg.Kind,
		err:     msg.Error,
	}
	m.recordActivity(record)
	if !m.busy && msg.Kind != "status" && msg.Kind != "error" && msg.Kind != "system" {
		return
	}
	label, body, kind := timelineEntryForActivity(msg)
	if strings.TrimSpace(body) == "" {
		return
	}
	if strings.TrimSpace(msg.EntryID) != "" {
		m.upsertTranscript(msg.EntryID, label, body, kind)
		return
	}
	m.appendTranscript(label, body, kind)
}

func timelineEntryForActivity(msg ActivityEvent) (string, string, string) {
	switch msg.Kind {
	case "model":
		return "Thinking", formatTimelineBody(msg.Summary, msg.Detail, 0), "phase"
	case "status":
		return fallback(msg.Title, "Status"), formatTimelineBody(msg.Summary, msg.Detail, 0), "phase"

	case "file_edit":
		label := fallback(msg.Title, "Edit")
		// Detail is a pre-rendered diff string set in handleActivityEvent.
		body := formatTimelineBody(msg.Summary, msg.Detail, 20)
		return label, body, "file_edit"

	// Noisy result/completion events: suppress from transcript.
	// Full output is always accessible in the activity pane (F2 / Ctrl+O).
	case "tool_result", "bash_result", "mcp_result", "plugin_result",
		"tool_call_delta", "tool_call_ready", "cache":
		return "", "", ""

	// bash: show the command being run, not the generic "Running shell command." label.
	case "bash_start":
		cmd := firstLine(msg.Detail)
		if runes := []rune(cmd); len(runes) > 72 {
			cmd = string(runes[:72]) + "…"
		}
		return "bash", cmd, "operation"

	// File ops: show path only, no content.
	case "file_read":
		return fallback(msg.Title, "read_file"), firstLine(fallback(msg.Detail, msg.Summary)), "operation"
	case "file_write":
		return fallback(msg.Title, "write_file"), firstLine(msg.Summary), "operation"

	// Search: show the pattern / scope hint.
	case "search":
		hint := firstLine(msg.Detail)
		if hint == "" {
			hint = msg.Summary
		}
		return fallback(msg.Title, "search"), hint, "operation"

	// Generic tool/MCP/plugin starts: one-line summary only.
	case "tool_start":
		return fallback(msg.Title, "tool"), msg.Summary, "operation"
	case "mcp_start":
		return fallback(msg.Title, "mcp"), msg.Summary, "operation"
	case "plugin_start":
		return fallback(msg.Title, "plugin"), msg.Summary, "operation"

	case "result_stream":
		return "Result", strings.TrimSpace(msg.Detail), "operation"
	case "approval":
		return "Approval", formatTimelineBody(msg.Summary, msg.Detail, 6), "operation"
	case "error":
		return fallback(msg.Title, "Error"), formatTimelineBody(msg.Summary, msg.Detail, 6), "error"
	case "prompt":
		return "Run Request", formatTimelineBody(msg.Summary, msg.Detail, 0), "task"
	default:
		return fallback(msg.Title, "Activity"), msg.Summary, "operation"
	}
}

func (m *model) recordActivity(record activityRecord) {
	if strings.TrimSpace(record.title) == "" && strings.TrimSpace(record.summary) == "" {
		return
	}
	if strings.TrimSpace(record.id) != "" {
		for index := range m.activities {
			if m.activities[index].id != record.id {
				continue
			}
			m.activities[index].title = record.title
			m.activities[index].summary = record.summary
			m.activities[index].detail = record.detail
			m.activities[index].kind = record.kind
			m.activities[index].err = record.err
			m.selectedActivity = index
			m.refreshActivity()
			return
		}
	}
	m.activities = append(m.activities, record)
	m.selectedActivity = len(m.activities) - 1
	// Build a compact hint: glyph + title + first line of summary
	glyph := activityKindGlyph[record.kind]
	if glyph == "" {
		glyph = "·"
	}
	hint := glyph + " " + fallback(record.title, "Activity")
	if snippet := firstLine(record.summary); snippet != "" {
		hint += "  " + snippet
	}
	m.activityHint = hint
	m.refreshActivity()
}

func (m *model) refreshTranscript() {
	if m.width < minWidth || m.height < minHeight {
		return
	}
	if len(m.transcriptEntries) == 0 {
		m.transcript.SetContent(m.renderWelcome())
		m.transcript.GotoBottom()
		return
	}
	blocks := make([]string, 0, len(m.transcriptEntries))
	for _, item := range m.transcriptEntries {
		labelStyle := m.theme.Success()
		switch item.kind {
		case "task":
			labelStyle = m.theme.Accent()
		case "result":
			labelStyle = m.theme.Primary()
		case "error":
			labelStyle = m.theme.Err()
		case "operation":
			labelStyle = m.theme.Meta()
		case "file_edit":
			labelStyle = m.theme.Meta()
		case "phase":
			labelStyle = m.theme.Help()
		case "help":
			labelStyle = m.theme.Primary()
		}
		var body string
		switch item.kind {
		case "result", "system":
			// Render markdown for assistant responses and slash command output.
			body = m.md.Render(item.body)
		case "help":
			// Render with the styled help card renderer.
			body = m.renderHelpContent(item.body)
		case "operation":
			// Compact single-line: glyph + label › detail. No rule, no block.
			body = m.renderCompactOperation(item)
		default:
			body = m.renderTimelineBlock(item)
		}
		if item.kind == "result" || item.kind == "system" || item.kind == "help" {
			blocks = append(blocks, labelStyle.Render(item.label)+"\n"+body)
			continue
		}
		blocks = append(blocks, body)
	}
	m.transcript.SetContent(strings.Join(blocks, "\n\n"))
	m.transcript.GotoBottom()
}

// renderHelpContent renders the structured help text with full ascaris color palette.
// Format expected (from cli.go /help):
//   ## Section Name        → amber bold section header + rule
//   /cmd|[args]|Description → gold command, dim args, tan description
//   (blank lines ignored)
func (m model) renderHelpContent(content string) string {
	width := max(40, m.transcript.Width-4)
	cmdWidth := 20
	argWidth := 24
	descWidth := max(10, width-cmdWidth-argWidth-4)

	var lines []string
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			lines = append(lines, "")
			continue
		}
		// Section header
		if strings.HasPrefix(line, "## ") {
			header := strings.TrimPrefix(line, "## ")
			rule := m.theme.Help().Render(strings.Repeat("─", max(8, min(width-2, 48))))
			lines = append(lines, "", m.theme.Accent().Bold(true).Render(header), rule)
			continue
		}
		// Command row: /cmd|[args]|Description
		if strings.HasPrefix(line, "/") {
			parts := strings.SplitN(line, "|", 3)
			cmd := parts[0]
			args := ""
			desc := ""
			if len(parts) > 1 {
				args = parts[1]
			}
			if len(parts) > 2 {
				desc = parts[2]
			}
			// Pad each column
			cmdPad := strings.Repeat(" ", max(0, cmdWidth-len([]rune(cmd))))
			argPad := strings.Repeat(" ", max(0, argWidth-len([]rune(args))))
			// Truncate desc if needed
			descRunes := []rune(desc)
			if len(descRunes) > descWidth {
				desc = string(descRunes[:descWidth-1]) + "…"
			}
			row := m.theme.Primary().Bold(true).Render(cmd) + cmdPad +
				m.theme.Help().Render(args) + argPad +
				m.theme.Meta().Render(desc)
			lines = append(lines, "  "+row)
			continue
		}
		// Plain text fallback
		lines = append(lines, m.theme.Help().Render(line))
	}
	return strings.Join(lines, "\n")
}

// renderCompactOperation renders a tool/operation event as a compact single dim line:
//   · bash  go test ./...
// Full output stays in the activity pane (F2 / Ctrl+O).
func (m model) renderCompactOperation(item transcriptEntry) string {
	glyph := activityKindGlyph[item.kind]
	if glyph == "" {
		glyph = "·"
	}
	maxWidth := max(20, m.transcript.Width-4)
	label := item.label
	detail := item.body
	var line string
	if strings.TrimSpace(detail) != "" {
		separator := m.theme.Help().Render(" › ")
		line = m.theme.Help().Render(glyph+" "+label) + separator + m.theme.Help().Render(detail)
	} else {
		line = m.theme.Help().Render(glyph + " " + label)
	}
	_ = maxWidth // lipgloss wraps naturally; explicit width not needed for single-line
	return "  " + line
}

func (m model) renderTimelineBlock(item transcriptEntry) string {
	glyph := timelineGlyph(item.kind, item.label)
	width := max(10, m.transcript.Width-6)
	headerStyle := m.theme.Body().Bold(true)
	bodyStyle := m.theme.Body().Width(width)
	borderStyle := m.theme.Help()
	switch item.kind {
	case "task":
		headerStyle = m.theme.Accent()
		borderStyle = m.theme.Accent()
	case "phase":
		headerStyle = m.theme.Help().Bold(true)
	case "operation":
		headerStyle = m.theme.Meta().Bold(true)
	case "file_edit":
		headerStyle = m.theme.Meta().Bold(true)
		borderStyle = m.theme.Meta()
	case "error":
		headerStyle = m.theme.Err()
		borderStyle = m.theme.Err()
	}
	lines := []string{
		headerStyle.Render(glyph + " " + item.label),
		bodyStyle.Render(item.body),
	}
	rule := borderStyle.Render(strings.Repeat("─", max(12, min(width, 32))))
	return strings.Join([]string{rule, strings.Join(lines, "\n")}, "\n")
}

func timelineGlyph(kind, label string) string {
	switch kind {
	case "task":
		return "›"
	case "phase":
		return "·"
	case "operation":
		return "·"
	case "file_edit":
		return "≈"
	case "result":
		return "✓"
	case "error":
		return "✗"
	default:
		if glyph := activityKindGlyph[strings.ToLower(strings.TrimSpace(label))]; glyph != "" {
			return glyph
		}
		return "·"
	}
}

func (m *model) refreshActivity() {
	if m.width < minWidth || m.height < minHeight {
		return
	}
	visible := m.visibleActivityIndexes()
	if len(visible) == 0 {
		m.activity.SetContent(m.theme.Meta().Render("No agent activity yet. Run a prompt to inspect model phases, tools, approvals, and results."))
		m.activity.GotoTop()
		return
	}
	m.ensureVisibleActivitySelection(visible)
	lines := make([]string, 0, len(visible)*3)
	for _, i := range visible {
		item := m.activities[i]
		prefix := "  "
		titleStyle := m.theme.Meta()
		if i == m.selectedActivity {
			prefix = "› "
			titleStyle = m.theme.Body().Bold(true)
		}
		if item.err {
			titleStyle = m.theme.Err()
		} else if strings.HasSuffix(item.kind, "_result") || item.kind == "tool_result" {
			titleStyle = m.theme.Success()
		}
		glyph := activityKindGlyph[item.kind]
		if glyph == "" {
			glyph = "·"
		}
		lines = append(lines, prefix+titleStyle.Render(glyph+" "+fallback(item.title, "Activity")))
		lines = append(lines, "  "+m.theme.Body().Width(max(10, m.activity.Width-3)).Render(fallback(item.summary, "No summary.")))
		if m.expanded[i] && strings.TrimSpace(item.detail) != "" {
			lines = append(lines, "  "+m.theme.Help().Render(strings.TrimSpace(item.detail)))
		}
		lines = append(lines, "")
	}
	m.activity.SetContent(strings.TrimSpace(strings.Join(lines, "\n")))
	m.activity.GotoBottom()
}

func (m *model) moveActivitySelection(visible []int, delta int) {
	if len(visible) == 0 {
		return
	}
	m.ensureVisibleActivitySelection(visible)
	current := 0
	for i, index := range visible {
		if index == m.selectedActivity {
			current = i
			break
		}
	}
	next := current + delta
	if next < 0 {
		next = 0
	}
	if next >= len(visible) {
		next = len(visible) - 1
	}
	if visible[next] != m.selectedActivity {
		m.selectedActivity = visible[next]
		m.refreshActivity()
	}
}

func (m *model) ensureVisibleActivitySelection(visible []int) {
	if len(visible) == 0 {
		return
	}
	for _, index := range visible {
		if index == m.selectedActivity {
			return
		}
	}
	m.selectedActivity = visible[len(visible)-1]
}

func (m model) visibleActivityIndexes() []int {
	indexes := make([]int, 0, len(m.activities))
	for i, item := range m.activities {
		if !showInActivityPane(item.kind) {
			continue
		}
		indexes = append(indexes, i)
	}
	return indexes
}

func showInActivityPane(kind string) bool {
	switch kind {
	case "approval", "error", "tool_start", "tool_result", "file_read", "file_write", "file_edit",
		"search", "bash_start", "bash_result", "mcp_start", "mcp_result",
		"plugin_start", "plugin_result", "cache", "tool_call_delta", "tool_call_ready":
		return true
	default:
		return false
	}
}

// renderPermissionBadge returns the permission mode styled by risk level:
// read-only → olive (safe), workspace-write → tan (moderate), danger-full-access → red (high).
func (m model) renderPermissionBadge() string {
	perm := fallback(m.status.Permission, "workspace-write")
	switch perm {
	case "read-only":
		return m.theme.Success().Render(perm)
	case "danger-full-access":
		return m.theme.Err().Render(perm)
	default:
		return m.theme.Meta().Render(perm)
	}
}

func (m model) renderHeader() string {
	var wormPart string
	if m.busy && m.approval == nil {
		worm := wormFrames[m.spinFrame%len(wormFrames)]
		wormStr := m.theme.Accent().Render(worm + " ")
		verb := m.spinVerb
		if verb == "" {
			verb = "Working"
		}
		wormPart = wormStr + m.theme.Accent().Bold(true).Render(verb+"…")
	} else {
		// Idle: static worm glyph + product name, no animation.
		wormPart = m.theme.Primary().Render(wormFrames[0]+" ") + m.theme.Primary().Render(m.status.Product)
	}
	left := []string{
		wormPart,
		m.theme.Meta().Render(m.cwdShort),
	}
	right := []string{
		m.theme.Meta().Render(fmt.Sprintf("Session %s", fallback(m.status.SessionID, "new"))),
		m.theme.Meta().Render(fmt.Sprintf("%s • %s", fallback(m.status.Model, "unknown"), fallback(m.status.Provider, "auto"))) + " • " + m.renderPermissionBadge(),
		m.theme.Meta().Render(fmt.Sprintf("Status %s", fallback(m.statusText, "Idle"))),
	}
	if strings.TrimSpace(m.status.CostEst) != "" {
		right = append(right, m.theme.Meta().Render(m.status.CostEst))
	} else if m.status.TokensIn > 0 || m.status.TokensOut > 0 {
		right = append(right, m.theme.Meta().Render(fmt.Sprintf("↑%d ↓%d tok", m.status.TokensIn, m.status.TokensOut)))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(max(10, (m.width-6)/2)).Render(strings.Join(left, "\n")),
		lipgloss.NewStyle().Width(max(10, (m.width-6)/2)).Align(lipgloss.Right).Render(strings.Join(right, "\n")),
	)
}

func (m model) renderActivityStrip() string {
	summary := fallback(strings.TrimSpace(m.activityHint), "Idle: waiting for a prompt.")
	suffix := " [F2 details]"
	if m.showActivity {
		suffix = " [F2 hide]"
	}
	return m.theme.Accent().Width(max(10, m.width-2)).Render(summary + suffix)
}

func (m model) renderMainContent() string {
	if m.showStartup() {
		return m.renderStartupView()
	}
	transcriptPanel := m.panel(m.transcript.View(), m.transcript.Width+2, m.transcript.Height+2, m.focus == focusTranscript)
	if !m.showActivity {
		return transcriptPanel
	}
	activityPanel := m.panel(m.activity.View(), m.activity.Width+2, m.activity.Height+2, m.focus == focusActivity)
	if m.width >= 120 {
		return lipgloss.JoinHorizontal(lipgloss.Top, transcriptPanel, " ", activityPanel)
	}
	return lipgloss.JoinVertical(lipgloss.Left, transcriptPanel, activityPanel)
}

func (m model) renderWelcome() string {
	logo := renderLogo(m.transcript.Width, m.theme)
	subtitle := m.theme.Meta().Render("Dispatch a task and watch the harness work, or start with /help.")
	contextLine := m.theme.Meta().Render(fmt.Sprintf("%s • %s • %s", fallback(m.status.Model, "unknown"), fallback(m.status.Provider, "auto"), fallback(m.status.Permission, "workspace-write")))
	content := strings.Join([]string{logo, "", contextLine, subtitle}, "\n")
	return lipgloss.Place(m.transcript.Width, m.transcript.Height, lipgloss.Center, lipgloss.Center, content)
}

func (m model) showStartup() bool {
	if len(m.transcriptEntries) != 0 || m.busy {
		return false
	}
	if len(m.activities) == 0 {
		return true
	}
	return strings.TrimSpace(m.startupNote) != "" && len(m.activities) == 1
}

func (m model) renderStartupView() string {
	if m.width >= 120 {
		leftWidth := max(32, ((m.width - 6) * 3 / 5))
		rightWidth := max(24, (m.width-6)-leftWidth-1)

		// Derive right panel heights from leftH so all three panels share the
		// same total rendered height (panel border adds 2 rows per box):
		//   left total  = leftH + 2
		//   right total = (topH + 2) + (bottomH + 2) = topH + bottomH + 4
		// For alignment: leftH + 2 = topH + bottomH + 4  →  topH + bottomH = leftH - 2
		leftH := max(14, m.transcript.Height+2)
		topH := max(8, (leftH-2+1)/2) // ceiling half, minimum 8
		bottomH := max(6, (leftH-2)/2) // floor half, minimum 6
		// If minimums pushed the sum over leftH-2, extend leftH to compensate.
		if topH+bottomH != leftH-2 {
			leftH = topH + bottomH + 2
		}

		left := m.panel(m.renderHeroCard(leftWidth-2), leftWidth, leftH, false)
		right := lipgloss.JoinVertical(lipgloss.Left,
			m.panel(m.renderRecentCard(rightWidth-2), rightWidth, topH, false),
			m.panel(m.renderTipsCard(rightWidth-2), rightWidth, bottomH, false),
		)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	}
	stackWidth := max(18, m.width-2)
	return lipgloss.JoinVertical(lipgloss.Left,
		m.panel(m.renderHeroCard(stackWidth-2), stackWidth, max(12, m.transcript.Height/2), false),
		m.panel(m.renderRecentCard(stackWidth-2), stackWidth, max(6, m.transcript.Height/4), false),
		m.panel(m.renderTipsCard(stackWidth-2), stackWidth, max(6, m.transcript.Height/4), false),
	)
}

func (m model) renderHeroCard(width int) string {
	logo := renderLogo(width, m.theme)
	infoLine := fmt.Sprintf("%s  %s  %s",
		fallback(m.status.Version, ""),
		fallback(m.status.Model, "unknown"),
		filepathBase(m.status.Workspace),
	)
	lines := []string{
		logo,
		"",
		lipgloss.PlaceHorizontal(width, lipgloss.Center, m.theme.Body().Bold(true).Render("Welcome back")),
		lipgloss.PlaceHorizontal(width, lipgloss.Center, m.theme.Meta().Render(strings.TrimSpace(infoLine))),
		"",
		lipgloss.PlaceHorizontal(width, lipgloss.Center, m.theme.Meta().Render(fmt.Sprintf("%s • %s • %s",
			fallback(m.status.Model, "unknown"),
			fallback(m.status.Provider, "auto"),
			fallback(m.status.Permission, "workspace-write"),
		))),
	}
	return lipgloss.Place(width, max(10, m.transcript.Height/2), lipgloss.Center, lipgloss.Center, strings.Join(lines, "\n"))
}

func (m model) renderRecentCard(width int) string {
	title := m.theme.Primary().Render("Recent Session")
	sep := m.theme.Help().Render(strings.Repeat("─", max(10, width)))
	if strings.TrimSpace(m.status.Recent.ID) == "" {
		return strings.Join([]string{
			title,
			sep,
			m.theme.Meta().Render("No recent managed session."),
			m.theme.Meta().Render("Start a prompt to create one."),
		}, "\n")
	}
	lines := []string{
		title,
		sep,
		m.theme.Meta().Render("ID  " + m.status.Recent.ID),
		m.theme.Meta().Render(fmt.Sprintf("Msgs  %d", m.status.Recent.MessageCount)),
	}
	if strings.TrimSpace(m.status.Recent.UpdatedLabel) != "" {
		lines = append(lines, m.theme.Meta().Render(m.status.Recent.UpdatedLabel))
	}
	if strings.TrimSpace(m.status.Recent.LastPrompt) != "" {
		lines = append(lines, "", m.theme.Help().Render("Last:"),
			m.theme.Body().Width(width).Render(m.status.Recent.LastPrompt))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderTipsCard(width int) string {
	lines := []string{
		m.theme.Primary().Render("Quick Start — Bug Finding"),
		m.theme.Help().Render(strings.Repeat("─", max(10, width))),
		m.theme.Body().Width(width).Render("• /fuzz [scope]         — fuzz-test a function or package"),
		m.theme.Body().Width(width).Render("• /security-review      — full source security audit"),
		m.theme.Body().Width(width).Render("• /crash-triage         — triage crash reproducers"),
		m.theme.Body().Width(width).Render("• /bughunter [scope]    — logic & memory bug hunt"),
		"",
		m.theme.Help().Render("/help for all commands  •  F2 activity  •  Tab focus"),
	}
	if strings.TrimSpace(m.startupNote) != "" {
		lines = append(lines, "", m.theme.Meta().Render(m.startupNote))
	}
	return strings.Join(lines, "\n")
}

// renderComposerInner renders the content inside the composer panel:
// a dimmed CWD line above the textarea input.
func (m model) renderComposerInner() string {
	cwd := m.theme.Help().Render(m.cwdShort)
	return cwd + "\n" + m.input.View()
}

// renderCompletion renders the slash-command autocomplete overlay.
// Returns an empty string when the overlay is not active.
func (m model) renderCompletion() string {
	if !m.comp.active || len(m.comp.items) == 0 {
		return ""
	}
	const maxVisible = 8
	nameWidth := 22
	summaryWidth := max(20, m.width-nameWidth-12)
	rows := make([]string, 0, maxVisible)
	end := min(m.comp.offset+maxVisible, len(m.comp.items))
	for i := m.comp.offset; i < end; i++ {
		item := m.comp.items[i]
		name := item.name
		summary := item.summary
		if runes := []rune(summary); len(runes) > summaryWidth {
			summary = string(runes[:summaryWidth-1]) + "…"
		}
		namePad := strings.Repeat(" ", max(0, nameWidth-len(name)))
		line := name + namePad + "  " + summary
		if i == m.comp.cursor {
			line = m.theme.CompletionSelected().Width(m.width - 6).Render(line)
		} else {
			line = m.theme.CompletionRow().Width(m.width - 6).Render(line)
		}
		rows = append(rows, line)
	}
	if len(m.comp.items) > maxVisible {
		scrollInfo := fmt.Sprintf("  %d/%d  ↑↓ navigate", m.comp.cursor+1, len(m.comp.items))
		rows = append(rows, m.theme.Help().Render(scrollInfo))
	}
	inner := strings.Join(rows, "\n")
	return m.theme.CompletionFrame().Width(m.width - 4).Render(inner)
}

// updateCompletion refreshes the completion state based on the current input value.
func (m *model) updateCompletion() {
	val := m.input.Value()
	if !strings.HasPrefix(val, "/") || strings.ContainsAny(val, " \t\n") {
		m.comp.active = false
		return
	}
	query := strings.ToLower(val)
	filtered := make([]slashItem, 0, len(slashRegistry))
	for _, item := range slashRegistry {
		if strings.HasPrefix(item.name, query) {
			filtered = append(filtered, item)
		}
	}
	m.comp.items = filtered
	m.comp.active = len(filtered) > 0
	// Reset cursor when query changes.
	if query != m.comp.query {
		m.comp.query = query
		m.comp.cursor = 0
		m.comp.offset = 0
	}
	m.clampCompletionOffset()
}

// clampCompletionOffset ensures the scroll offset keeps the cursor visible.
func (m *model) clampCompletionOffset() {
	const maxVisible = 8
	if m.comp.cursor < m.comp.offset {
		m.comp.offset = m.comp.cursor
	}
	if m.comp.cursor >= m.comp.offset+maxVisible {
		m.comp.offset = m.comp.cursor - maxVisible + 1
	}
}

// shortenPath replaces the home directory prefix with "~".
func shortenPath(p string) string {
	if p == "" {
		return "."
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func (m model) renderApprovalScreen() string {
	if m.approval == nil {
		return ""
	}
	width := max(42, min(96, m.width-12))
	commandPreview := m.formatApprovalInput(max(24, width-10))
	riskLabel, riskStyle := approvalRiskLabel(m.approval.ToolName, m.theme)
	approveChip := m.theme.Success().Render("[Y] Approve once")
	denyChip := m.theme.Err().Render("[N] Deny")
	body := []string{
		m.theme.Primary().Render("Approval Required"),
		m.theme.Meta().Render(fmt.Sprintf("Tool %s wants permission to continue.", m.approval.ToolName)),
		riskStyle.Render(riskLabel),
		"",
		m.theme.Body().Bold(true).Render("Command Preview"),
		m.panel(commandPreview, width-4, 8, false),
		"",
		lipgloss.JoinHorizontal(lipgloss.Left, approveChip, "  ", denyChip),
		m.theme.Help().Render("Approve once runs this action for the current request only. Deny keeps the harness in the current turn and returns an error to the tool call."),
		m.theme.Help().Render("Keys: y approve once • n deny • esc deny"),
	}
	return lipgloss.Place(m.width-4, max(10, m.height-12), lipgloss.Center, lipgloss.Center,
		m.theme.Modal().Width(width).Render(strings.Join(body, "\n")),
	)
}

func (m model) renderSmallTerminal() string {
	lines := []string{
		m.theme.Primary().Render("Ascaris"),
		m.theme.Meta().Render(fmt.Sprintf("Terminal too small: need at least %dx%d.", minWidth, minHeight)),
		m.theme.Help().Render("Resize the terminal to continue using the TUI."),
		m.theme.Help().Render("Shortcuts: F2 activity • Ctrl+D exit"),
	}
	return strings.Join(lines, "\n")
}

func (m model) panel(content string, width, height int, focused bool) string {
	style := m.theme.Frame()
	if focused {
		style = m.theme.FocusFrame()
	}
	if width > 0 {
		style = style.Width(width)
	}
	if height > 0 {
		style = style.Height(height)
	}
	return style.Render(content)
}

func (m model) nextFocus() focusTarget {
	order := []focusTarget{focusTranscript, focusActivity, focusInput}
	if !m.showActivity {
		order = []focusTarget{focusTranscript, focusInput}
	}
	for index, target := range order {
		if target == m.focus {
			return order[(index+1)%len(order)]
		}
	}
	return order[0]
}

func (m model) prevFocus() focusTarget {
	order := []focusTarget{focusTranscript, focusActivity, focusInput}
	if !m.showActivity {
		order = []focusTarget{focusTranscript, focusInput}
	}
	for index, target := range order {
		if target == m.focus {
			return order[(index-1+len(order))%len(order)]
		}
	}
	return order[len(order)-1]
}

func runPromptCmd(ctx context.Context, run func(context.Context, string, func(tea.Msg)), prompt string, events chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		run(ctx, prompt, func(msg tea.Msg) {
			events <- msg
		})
		return nil
	}
}

func waitForRuntimeEvent(events chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-events
	}
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTick(t)
	})
}

func (m model) formatApprovalInput(width int) string {
	if m.approval == nil {
		return ""
	}
	input := strings.TrimSpace(m.approval.Input)
	if input == "" {
		return m.theme.Meta().Render("No input provided.")
	}
	lines := []string{}
	command := ""
	if strings.Contains(m.approval.ToolName, "bash") {
		command = extractApprovalCommand(input)
	}
	if command != "" {
		lines = append(lines, m.theme.Body().Width(width).Render(command))
	}
	if len(lines) == 0 {
		lines = append(lines, m.theme.Body().Width(width).Render(truncateApprovalInput(input, 8, 480)))
	}
	if raw := approvalRawPreview(input, command); raw != "" {
		lines = append(lines, "", m.theme.Help().Render("Raw input"), m.theme.Help().Width(width).Render(raw))
	}
	return strings.Join(lines, "\n")
}

func extractApprovalCommand(input string) string {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "{") {
		return ""
	}
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Command)
}

func approvalRiskLabel(toolName string, theme Theme) (string, lipgloss.Style) {
	name := strings.ToLower(strings.TrimSpace(toolName))
	switch {
	case strings.Contains(name, "bash"):
		return "Risk High • shell execution", theme.Err()
	case strings.Contains(name, "write_file"), strings.Contains(name, "edit_file"):
		return "Risk Medium • workspace mutation", theme.Accent()
	case strings.HasPrefix(name, "mcp"), strings.Contains(name, "plugin"):
		return "Risk Medium • external tool action", theme.Accent()
	case strings.Contains(name, "read_file"), strings.Contains(name, "grep"), strings.Contains(name, "glob"), strings.Contains(name, "search"):
		return "Risk Low • read or search only", theme.Success()
	default:
		return "Risk Medium • tool action", theme.Accent()
	}
}

func approvalRawPreview(input, command string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if command != "" {
		preview := strings.TrimSpace(strings.ReplaceAll(input, "\n", " "))
		if preview == "" {
			return ""
		}
		return truncateApprovalInput(preview, 2, 180)
	}
	return truncateApprovalInput(input, 8, 480)
}

func truncateApprovalInput(input string, maxLines, maxChars int) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	truncated := false
	if maxChars > 0 && len(input) > maxChars {
		input = strings.TrimSpace(input[:maxChars])
		truncated = true
	}
	if maxLines > 0 {
		lines := strings.Split(input, "\n")
		if len(lines) > maxLines {
			input = strings.Join(lines[:maxLines], "\n")
			truncated = true
		}
	}
	if truncated {
		input = strings.TrimRight(input, "\n ")
		input += "…"
	}
	return input
}

// fileDiff mirrors the fileDiff struct in internal/tools/builtins.go.
// It is the JSON payload written into the file_edit activity event Detail field.
type fileDiff struct {
	HunkHeader string   `json:"hunk"`
	Before     []string `json:"before"`
	Removed    []string `json:"removed"`
	Added      []string `json:"added"`
	After      []string `json:"after"`
	StartLine  int      `json:"start_line"`
}

// renderInlineDiff parses a file_edit Detail JSON (produced by builtins.go) and
// returns a unified-diff style string ready for display in the transcript.
// Returns "" if the detail is not a valid fileDiff payload.
// width controls the full-line highlight width (pass m.transcript.Width).
func renderInlineDiff(detail string, theme Theme, width int) string {
	detail = strings.TrimSpace(detail)
	if detail == "" || detail[0] != '{' {
		return ""
	}
	var d fileDiff
	if err := json.Unmarshal([]byte(detail), &d); err != nil {
		return ""
	}
	if len(d.Removed) == 0 && len(d.Added) == 0 {
		return ""
	}

	lineWidth := max(40, width-4)

	// Hunk header: steel-blue, no background.
	hunkStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("74")).Bold(true)

	// Context lines: dim, no background.
	ctxStyle := theme.Help()

	// Removed lines: dark red background + bright red prefix, full-width.
	removeBg := lipgloss.NewStyle().
		Background(lipgloss.Color("52")).  // dark red
		Foreground(lipgloss.Color("203")). // bright red text
		Width(lineWidth)
	removePfx := lipgloss.NewStyle().
		Background(lipgloss.Color("52")).
		Foreground(lipgloss.Color("203")).
		Bold(true)

	// Added lines: dark green background + bright green prefix, full-width.
	addBg := lipgloss.NewStyle().
		Background(lipgloss.Color("22")).  // dark green
		Foreground(lipgloss.Color("114")). // bright green text
		Width(lineWidth)
	addPfx := lipgloss.NewStyle().
		Background(lipgloss.Color("22")).
		Foreground(lipgloss.Color("114")).
		Bold(true)

	renderLine := func(bgStyle, pfxStyle lipgloss.Style, prefix, content string) string {
		return bgStyle.Render(pfxStyle.Render(prefix) + content)
	}

	var lines []string

	if d.HunkHeader != "" {
		lines = append(lines, hunkStyle.Render(d.HunkHeader))
	}
	for _, l := range d.Before {
		lines = append(lines, ctxStyle.Render("  "+l))
	}
	for _, l := range d.Removed {
		lines = append(lines, renderLine(removeBg, removePfx, "- ", l))
	}
	for _, l := range d.Added {
		lines = append(lines, renderLine(addBg, addPfx, "+ ", l))
	}
	for _, l := range d.After {
		lines = append(lines, ctxStyle.Render("  "+l))
	}

	return strings.Join(lines, "\n")
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	line, _, _ := strings.Cut(value, "\n")
	return strings.TrimSpace(line)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func formatTimelineBody(summary, detail string, detailLines int) string {
	parts := []string{}
	if strings.TrimSpace(summary) != "" {
		parts = append(parts, strings.TrimSpace(summary))
	}
	if rendered := renderDetailPreview(detail, detailLines); rendered != "" {
		parts = append(parts, rendered)
	}
	return strings.Join(parts, "\n")
}

func renderDetailPreview(detail string, limit int) string {
	detail = strings.TrimSpace(detail)
	if detail == "" || limit == 0 {
		return ""
	}
	lines := strings.Split(detail, "\n")
	if len(lines) > limit {
		lines = append(lines[:limit], "...")
	}
	for i := range lines {
		lines[i] = "  " + strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, "\n")
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func filepathBase(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "."
	}
	parts := strings.Split(value, "/")
	return parts[len(parts)-1]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
