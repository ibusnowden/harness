package commands

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ascaris/internal/config"
	"ascaris/internal/models"
	"ascaris/internal/plugins"
)

type Spec struct {
	Name            string
	Aliases         []string
	Summary         string
	Usage           string
	SourceHint      string
	Responsibility  string
	ResumeSupported bool
}

type Execution struct {
	Name       string
	SourceHint string
	Prompt     string
	Handled    bool
	Message    string
}

var builtinSpecs = []Spec{
	{Name: "help", Summary: "Show available commands", Usage: "ascaris help", SourceHint: "built-in", Responsibility: "Render top-level CLI help", ResumeSupported: true},
	{Name: "version", Summary: "Show CLI version and build information", Usage: "ascaris version", SourceHint: "built-in", Responsibility: "Report product and version information", ResumeSupported: true},
	{Name: "status", Summary: "Show current workspace and session status", Usage: "ascaris status [--json]", SourceHint: "built-in", Responsibility: "Render runtime, config, and session status", ResumeSupported: true},
	{Name: "doctor", Summary: "Run local environment health checks", Usage: "ascaris doctor [--json]", SourceHint: "built-in", Responsibility: "Render config, registry, and auth health checks", ResumeSupported: true},
	{Name: "sandbox", Summary: "Show sandbox isolation status", Usage: "ascaris sandbox", SourceHint: "built-in", Responsibility: "Describe active sandbox mode", ResumeSupported: true},
	{Name: "prompt", Summary: "Run a one-shot prompt", Usage: "ascaris prompt <text>", SourceHint: "built-in", Responsibility: "Run the live prompt execution loop", ResumeSupported: true},
	{Name: "review", Summary: "Inspect code for bugs, regressions, and missing tests", Usage: "ascaris review [scope]", SourceHint: "built-in", Responsibility: "Run the code review workflow", ResumeSupported: false},
	{Name: "plan", Summary: "Draft a detailed implementation plan", Usage: "/plan [task]", SourceHint: "built-in", Responsibility: "Run the planning workflow", ResumeSupported: false},
	{Name: "security-review", Summary: "Inspect the codebase for security issues", Usage: "ascaris security-review [scope]", SourceHint: "built-in", Responsibility: "Run the security review workflow", ResumeSupported: false},
	{Name: "bughunter", Summary: "Inspect the codebase for likely bugs", Usage: "ascaris bughunter [scope]", SourceHint: "built-in", Responsibility: "Run the bughunter workflow", ResumeSupported: false},
	{Name: "login", Summary: "Authenticate using configured OAuth settings", Usage: "ascaris login", SourceHint: "built-in", Responsibility: "Run the OAuth login flow and persist credentials", ResumeSupported: false},
	{Name: "logout", Summary: "Clear saved OAuth credentials", Usage: "ascaris logout", SourceHint: "built-in", Responsibility: "Remove saved OAuth credentials", ResumeSupported: true},
	{Name: "session", Summary: "List, switch, fork, delete, export, or clear managed sessions", Usage: "ascaris session [list|show|switch|fork|delete|export|clear]", SourceHint: "built-in", Responsibility: "Manage JSONL conversation sessions", ResumeSupported: true},
	{Name: "resume", Summary: "Resume a saved session in prompt mode", Usage: "ascaris --resume <session-id|latest> <prompt>", SourceHint: "built-in", Responsibility: "Resolve and load a saved session", ResumeSupported: true},
	{Name: "compact", Summary: "Compact local session history", Usage: "/compact", SourceHint: "built-in", Responsibility: "Summarize and trim stored session history", ResumeSupported: true},
	{Name: "clear", Summary: "Clear the active managed session alias", Usage: "/clear [--confirm]", SourceHint: "built-in", Responsibility: "Reset the current session pointer", ResumeSupported: true},
	{Name: "export", Summary: "Export a managed session to a file", Usage: "/export [file]", SourceHint: "built-in", Responsibility: "Write a persisted session export", ResumeSupported: true},
	{Name: "cost", Summary: "Show cumulative token usage and estimated cost", Usage: "/cost", SourceHint: "built-in", Responsibility: "Report cumulative usage for the active session", ResumeSupported: true},
	{Name: "config", Summary: "Inspect merged config or a config section", Usage: "/config [section]", SourceHint: "built-in", Responsibility: "Render merged runtime config and loaded sources", ResumeSupported: true},
	{Name: "agents", Summary: "Inspect available agents", Usage: "ascaris agents [--json]", SourceHint: "built-in", Responsibility: "Render agent definitions and origins", ResumeSupported: true},
	{Name: "skills", Summary: "Inspect or install available skills", Usage: "ascaris skills [list|install <path>] [--json]", SourceHint: "built-in", Responsibility: "Render skill definitions and installer output", ResumeSupported: true},
	{Name: "team", Summary: "List, create, or delete agent teams", Usage: "ascaris team [list|create <name>|delete <team-id>] [--json]", SourceHint: "built-in", Responsibility: "Manage persisted team control-plane state", ResumeSupported: true},
	{Name: "cron", Summary: "List, add, or remove scheduled prompts", Usage: "ascaris cron [list|add <schedule> <prompt>|remove <cron-id>] [--json]", SourceHint: "built-in", Responsibility: "Manage persisted cron control-plane state", ResumeSupported: true},
	{Name: "worker", Summary: "Inspect and control coding worker boot state", Usage: "ascaris worker [list|create|get|observe|resolve-trust|await-ready|send-prompt|restart|terminate] [--json]", SourceHint: "built-in", Responsibility: "Manage persisted worker control-plane state", ResumeSupported: true},
	{Name: "plugins", Aliases: []string{"plugin"}, Summary: "Inspect or manage plugins", Usage: "ascaris plugins [list|install|enable|disable|uninstall|update] [--json]", SourceHint: "built-in", Responsibility: "Render plugin registry state and lifecycle actions", ResumeSupported: true},
	{Name: "mcp", Summary: "Inspect configured MCP servers and tools", Usage: "ascaris mcp [list|show <server>] [--json]", SourceHint: "built-in", Responsibility: "Render MCP discovery state and tool catalog", ResumeSupported: true},
	{Name: "state", Summary: "Inspect worker and recovery state", Usage: "ascaris state [--json]", SourceHint: "built-in", Responsibility: "Render persisted worker and recovery state", ResumeSupported: true},
	{Name: "migrate", Summary: "Migrate legacy config and runtime assets", Usage: "ascaris migrate legacy", SourceHint: "built-in", Responsibility: "Import legacy assets into the .ascaris layout", ResumeSupported: true},
	{Name: "commands", Summary: "List live command handlers", Usage: "ascaris commands [--query value]", SourceHint: "built-in", Responsibility: "Render the live command registry", ResumeSupported: true},
	{Name: "tools", Summary: "List live tool handlers", Usage: "ascaris tools [--query value]", SourceHint: "built-in", Responsibility: "Render the live tool registry", ResumeSupported: true},
}

func BuiltInNames() map[string]struct{} {
	names := map[string]struct{}{}
	for _, spec := range builtinSpecs {
		names[spec.Name] = struct{}{}
		for _, alias := range spec.Aliases {
			names[alias] = struct{}{}
		}
	}
	return names
}

func MustModules() []models.PortingModule {
	return specsToModules(builtinSpecs)
}

func Catalog(root string) ([]models.PortingModule, error) {
	modules := MustModules()
	runtimeConfig, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	manager := plugins.NewManager(root, runtimeConfig)
	definitions, err := manager.AggregatedCommands()
	if err != nil {
		return nil, err
	}
	for _, definition := range definitions {
		modules = append(modules, models.PortingModule{
			Name:           definition.Name,
			Responsibility: definition.Description,
			SourceHint:     "plugin/" + definition.PluginID,
			Status:         "live",
		})
	}
	sort.Slice(modules, func(i, j int) bool {
		if modules[i].Name == modules[j].Name {
			return modules[i].SourceHint < modules[j].SourceHint
		}
		return modules[i].Name < modules[j].Name
	})
	return modules, nil
}

func Backlog() models.PortingBacklog {
	return models.PortingBacklog{Title: "Command surface", Modules: MustModules()}
}

func BacklogAtRoot(root string) (models.PortingBacklog, error) {
	modules, err := Catalog(root)
	if err != nil {
		return models.PortingBacklog{}, err
	}
	return models.PortingBacklog{Title: "Command surface", Modules: modules}, nil
}

func Get(name string) *models.PortingModule {
	return getFromList(MustModules(), name)
}

func GetAtRoot(root, name string) (*models.PortingModule, error) {
	modules, err := Catalog(root)
	if err != nil {
		return nil, err
	}
	return getFromList(modules, name), nil
}

func List(includePluginCommands, includeSkillCommands bool) []models.PortingModule {
	return filterModules(MustModules(), includePluginCommands, includeSkillCommands)
}

func ListAtRoot(root string, includePluginCommands, includeSkillCommands bool) ([]models.PortingModule, error) {
	modules, err := Catalog(root)
	if err != nil {
		return nil, err
	}
	return filterModules(modules, includePluginCommands, includeSkillCommands), nil
}

func Find(query string, limit int) []models.PortingModule {
	return findInList(MustModules(), query, limit)
}

func FindAtRoot(root, query string, limit int) ([]models.PortingModule, error) {
	modules, err := Catalog(root)
	if err != nil {
		return nil, err
	}
	return findInList(modules, query, limit), nil
}

func Execute(name, prompt string) Execution {
	module := Get(name)
	if module == nil {
		return Execution{Name: name, Prompt: prompt, Handled: false, Message: "Unknown command: " + name}
	}
	message := fmt.Sprintf("Registered command %q from %s accepted input %s.", module.Name, module.SourceHint, quote(prompt))
	return Execution{Name: module.Name, SourceHint: module.SourceHint, Prompt: prompt, Handled: true, Message: message}
}

func ExecuteAtRoot(root, name, prompt string) (Execution, error) {
	module, err := GetAtRoot(root, name)
	if err != nil {
		return Execution{}, err
	}
	if module == nil {
		return Execution{Name: name, Prompt: prompt, Handled: false, Message: "Unknown command: " + name}, nil
	}
	message := fmt.Sprintf("Registered command %q from %s accepted input %s.", module.Name, module.SourceHint, quote(prompt))
	return Execution{Name: module.Name, SourceHint: module.SourceHint, Prompt: prompt, Handled: true, Message: message}, nil
}

func RenderIndex(limit int, query string) string {
	return renderIndex(MustModules(), limit, query)
}

func RenderIndexAtRoot(root string, limit int, query string) (string, error) {
	modules, err := Catalog(root)
	if err != nil {
		return "", err
	}
	return renderIndex(modules, limit, query), nil
}

func specsToModules(specs []Spec) []models.PortingModule {
	modules := make([]models.PortingModule, 0, len(specs))
	for _, spec := range specs {
		modules = append(modules, models.PortingModule{
			Name:           spec.Name,
			Responsibility: spec.Responsibility,
			SourceHint:     spec.SourceHint,
			Status:         "live",
		})
	}
	return modules
}

func getFromList(modules []models.PortingModule, name string) *models.PortingModule {
	needle := strings.ToLower(strings.TrimSpace(name))
	for _, module := range modules {
		if strings.ToLower(module.Name) == needle {
			copy := module
			return &copy
		}
	}
	return nil
}

func filterModules(modules []models.PortingModule, includePluginCommands, includeSkillCommands bool) []models.PortingModule {
	filtered := make([]models.PortingModule, 0, len(modules))
	for _, module := range modules {
		source := strings.ToLower(module.SourceHint)
		if !includePluginCommands && strings.Contains(source, "plugin/") {
			continue
		}
		if !includeSkillCommands && strings.Contains(source, "skills") {
			continue
		}
		filtered = append(filtered, module)
	}
	return filtered
}

func findInList(modules []models.PortingModule, query string, limit int) []models.PortingModule {
	needle := strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 {
		limit = len(modules)
	}
	matches := make([]models.PortingModule, 0, limit)
	for _, module := range modules {
		if needle == "" ||
			strings.Contains(strings.ToLower(module.Name), needle) ||
			strings.Contains(strings.ToLower(module.SourceHint), needle) ||
			strings.Contains(strings.ToLower(module.Responsibility), needle) {
			matches = append(matches, module)
			if len(matches) == limit {
				break
			}
		}
	}
	return matches
}

func renderIndex(modules []models.PortingModule, limit int, query string) string {
	lines := []string{"Command entries: " + itoa(len(modules)), ""}
	if strings.TrimSpace(query) != "" {
		lines = append(lines, "Filtered by: "+query, "")
		modules = findInList(modules, query, limit)
	} else if limit > 0 && len(modules) > limit {
		modules = modules[:limit]
	}
	for _, module := range modules {
		lines = append(lines, "- "+module.Name+" - "+module.SourceHint)
	}
	return strings.Join(lines, "\n")
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
