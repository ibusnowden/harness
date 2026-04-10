package setup

import (
	"path/filepath"
	gruntime "runtime"
	"strconv"
	"strings"
)

type WorkspaceSetup struct {
	GoVersion   string
	Compiler    string
	Platform    string
	TestCommand string
}

func (w WorkspaceSetup) StartupSteps() []string {
	return []string{
		"start top-level prefetch side effects",
		"build workspace context",
		"load live command registry",
		"load live tool registry",
		"prepare parity audit hooks",
		"apply trust-gated deferred init",
	}
}

type PrefetchResult struct {
	Name    string
	Started bool
	Detail  string
}

type DeferredInitResult struct {
	Trusted      bool
	PluginInit   bool
	SkillInit    bool
	MCPPrefetch  bool
	SessionHooks bool
}

func (d DeferredInitResult) Lines() []string {
	return []string{
		"- plugin_init=" + strconv.FormatBool(d.PluginInit),
		"- skill_init=" + strconv.FormatBool(d.SkillInit),
		"- mcp_prefetch=" + strconv.FormatBool(d.MCPPrefetch),
		"- session_hooks=" + strconv.FormatBool(d.SessionHooks),
	}
}

type Report struct {
	Setup        WorkspaceSetup
	Prefetches   []PrefetchResult
	DeferredInit DeferredInitResult
	Trusted      bool
	CWD          string
}

func (r Report) Markdown() string {
	lines := []string{
		"# Setup Report",
		"",
		"- Go: " + r.Setup.GoVersion + " (" + r.Setup.Compiler + ")",
		"- Platform: " + r.Setup.Platform,
		"- Trusted mode: " + strconv.FormatBool(r.Trusted),
		"- CWD: " + r.CWD,
		"",
		"Prefetches:",
	}
	for _, prefetch := range r.Prefetches {
		lines = append(lines, "- "+prefetch.Name+": "+prefetch.Detail)
	}
	lines = append(lines, "", "Deferred init:")
	lines = append(lines, r.DeferredInit.Lines()...)
	return strings.Join(lines, "\n")
}

func BuildWorkspaceSetup() WorkspaceSetup {
	return WorkspaceSetup{
		GoVersion:   gruntime.Version(),
		Compiler:    gruntime.Compiler,
		Platform:    gruntime.GOOS + "/" + gruntime.GOARCH,
		TestCommand: "go test ./...",
	}
}

func Run(root string, trusted bool) Report {
	return Report{
		Setup: BuildWorkspaceSetup(),
		Prefetches: []PrefetchResult{
			{Name: "mdm_raw_read", Started: true, Detail: "Simulated MDM raw-read prefetch for workspace bootstrap"},
			{Name: "keychain_prefetch", Started: true, Detail: "Simulated keychain prefetch for trusted startup path"},
			{Name: "project_scan", Started: true, Detail: "Scanned project root " + filepath.Clean(root)},
		},
		DeferredInit: DeferredInitResult{
			Trusted:      trusted,
			PluginInit:   trusted,
			SkillInit:    trusted,
			MCPPrefetch:  trusted,
			SessionHooks: trusted,
		},
		Trusted: trusted,
		CWD:     filepath.Clean(root),
	}
}
