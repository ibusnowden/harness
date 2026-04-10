package securityreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Mode string

const (
	ModeReview         Mode = "review"
	ModeSecurityReview Mode = "security-review"
	ModeBugHunter      Mode = "bughunter"
)

type OutputFormat string

const (
	FormatMarkdown OutputFormat = "markdown"
	FormatJSON     OutputFormat = "json"
	FormatBoth     OutputFormat = "both"
)

type EvidencePreference string

const (
	EvidenceFindings EvidencePreference = "findings"
	EvidenceRepro    EvidencePreference = "repro"
	EvidencePatch    EvidencePreference = "patch"
)

type EvidenceKind string

const (
	EvidenceKindAnalysisOnly       EvidenceKind = "analysis_only"
	EvidenceKindExistingTestFailed EvidenceKind = "existing_test_failure"
	EvidenceKindBuildFailure       EvidenceKind = "build_failure"
)

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

type Options struct {
	Mode     Mode
	Scope    string
	Format   OutputFormat
	Evidence EvidencePreference
}

type Location struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
}

type ReproEvidence struct {
	Command        string   `json:"command"`
	ExitCode       int      `json:"exit_code"`
	OutputExcerpt  string   `json:"output_excerpt"`
	MatchedSignals []string `json:"matched_signals,omitempty"`
}

type Finding struct {
	ID            string         `json:"id"`
	Title         string         `json:"title"`
	Severity      Severity       `json:"severity"`
	Confidence    Confidence     `json:"confidence"`
	CWE           string         `json:"cwe,omitempty"`
	Location      Location       `json:"location"`
	Summary       string         `json:"summary"`
	Impact        string         `json:"impact"`
	EvidenceKind  EvidenceKind   `json:"evidence_kind"`
	ReproSteps    []string       `json:"repro_steps,omitempty"`
	FailingTest   *ReproEvidence `json:"failing_test,omitempty"`
	PatchGuidance []string       `json:"patch_guidance,omitempty"`
	VariantNotes  []string       `json:"variant_notes,omitempty"`
	ToolTrace     []string       `json:"tool_trace,omitempty"`
	matchSignals  []string
}

type CommandRun struct {
	Command       string `json:"command"`
	ExitCode      int    `json:"exit_code"`
	Succeeded     bool   `json:"succeeded"`
	OutputExcerpt string `json:"output_excerpt,omitempty"`
}

type Summary struct {
	FilesScanned int              `json:"files_scanned"`
	Findings     int              `json:"findings"`
	BySeverity   map[Severity]int `json:"by_severity"`
}

type Report struct {
	Mode          Mode               `json:"mode"`
	Evidence      EvidencePreference `json:"evidence"`
	Root          string             `json:"root"`
	Scope         string             `json:"scope"`
	TestCommand   string             `json:"test_command,omitempty"`
	ExecutedCheck *CommandRun        `json:"executed_check,omitempty"`
	Findings      []Finding          `json:"findings"`
	Summary       Summary            `json:"summary"`
}

var (
	insecureSkipVerifyPattern = regexp.MustCompile(`InsecureSkipVerify\s*:\s*true`)
	shellExecPattern          = regexp.MustCompile(`exec\.Command\(\s*"(?:/bin/)?(?:sh|bash|zsh)"\s*,\s*"(?:-c|-lc)"`)
	worldWritablePattern      = regexp.MustCompile(`(?:0o777|0777|0o666|0666)`)
)

func Run(root string, opts Options) (Report, error) {
	root = filepath.Clean(root)
	var err error
	opts, err = normalizeOptions(opts)
	if err != nil {
		return Report{}, err
	}
	scopePath, scopeLabel, err := resolveScope(root, opts.Scope)
	if err != nil {
		return Report{}, err
	}
	files, err := collectSourceFiles(scopePath)
	if err != nil {
		return Report{}, err
	}
	findings := make([]Finding, 0, 8)
	for _, path := range files {
		fileFindings, err := analyzeFile(root, path)
		if err != nil {
			return Report{}, err
		}
		findings = append(findings, fileFindings...)
	}
	sort.Slice(findings, func(i, j int) bool {
		if severityRank(findings[i].Severity) == severityRank(findings[j].Severity) {
			if findings[i].Location.Path == findings[j].Location.Path {
				return findings[i].Location.Line < findings[j].Location.Line
			}
			return findings[i].Location.Path < findings[j].Location.Path
		}
		return severityRank(findings[i].Severity) < severityRank(findings[j].Severity)
	})

	report := Report{
		Mode:     opts.Mode,
		Evidence: opts.Evidence,
		Root:     root,
		Scope:    scopeLabel,
		Findings: findings,
		Summary: Summary{
			FilesScanned: len(files),
			Findings:     len(findings),
			BySeverity:   map[Severity]int{},
		},
	}
	for _, finding := range findings {
		report.Summary.BySeverity[finding.Severity]++
	}
	if opts.Evidence != EvidenceFindings && len(findings) > 0 {
		run, ok := runDeterministicCheck(root, scopePath)
		if ok {
			report.TestCommand = run.Command
			report.ExecutedCheck = &run
			attachEvidence(report.Findings, run)
		}
	}
	applyEvidenceProfile(report.Findings, opts.Evidence)
	return report, nil
}

func normalizeOptions(opts Options) (Options, error) {
	if opts.Mode == "" {
		opts.Mode = ModeSecurityReview
	}
	switch opts.Mode {
	case ModeReview, ModeSecurityReview, ModeBugHunter:
	default:
		return opts, fmt.Errorf("unsupported review mode: %s", opts.Mode)
	}
	if opts.Format == "" {
		opts.Format = FormatBoth
	}
	switch opts.Format {
	case FormatMarkdown, FormatJSON, FormatBoth:
	default:
		return opts, fmt.Errorf("unsupported format: %s", opts.Format)
	}
	if opts.Evidence == "" {
		opts.Evidence = EvidenceRepro
	}
	switch opts.Evidence {
	case EvidenceFindings, EvidenceRepro, EvidencePatch:
	default:
		return opts, fmt.Errorf("unsupported evidence level: %s", opts.Evidence)
	}
	return opts, nil
}

func resolveScope(root, scope string) (string, string, error) {
	if strings.TrimSpace(scope) == "" || strings.EqualFold(strings.TrimSpace(scope), "auto") {
		return root, ".", nil
	}
	target := filepath.Clean(filepath.Join(root, scope))
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", "", fmt.Errorf("scope must stay inside the workspace")
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", "", err
	}
	if !info.IsDir() && !strings.HasSuffix(strings.ToLower(target), ".go") {
		return "", "", fmt.Errorf("scope must be a directory or Go source file")
	}
	return target, filepath.ToSlash(rel), nil
}

func collectSourceFiles(scopePath string) ([]string, error) {
	files := []string{}
	info, err := os.Stat(scopePath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{scopePath}, nil
	}
	err = filepath.WalkDir(scopePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".cache", ".ascaris", "bin", "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(name) != ".go" {
			return nil
		}
		if strings.HasSuffix(name, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func analyzeFile(root, path string) ([]Finding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(data)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)
	findings := []Finding{}

	if match := insecureSkipVerifyPattern.FindStringIndex(content); match != nil {
		findings = append(findings, Finding{
			ID:           findingID("tls-skip-verify", rel, lineNumber(content, match[0])),
			Title:        "TLS verification disabled in production code path",
			Severity:     SeverityHigh,
			Confidence:   ConfidenceHigh,
			CWE:          "CWE-295",
			Location:     Location{Path: rel, Line: lineNumber(content, match[0])},
			Summary:      "The code constructs a TLS client configuration with certificate verification disabled.",
			Impact:       "An attacker on the network path can impersonate upstream services and intercept or alter traffic.",
			EvidenceKind: EvidenceKindAnalysisOnly,
			ReproSteps: []string{
				"Inspect the TLS configuration at the reported location and confirm it is reachable from non-test code paths.",
				"Run the repo-local test command, if available, to capture any failing security regression tied to TLS verification.",
			},
			PatchGuidance: []string{
				"Remove `InsecureSkipVerify: true` from production code paths.",
				"If custom trust is needed, configure a dedicated root CA pool or certificate pinning instead of disabling verification.",
			},
			VariantNotes: []string{
				"Check neighboring files for additional TLS configs or transport clones that carry the same setting.",
			},
			ToolTrace:    []string{"filesystem.walk", "pattern.go.tls_skip_verify"},
			matchSignals: []string{"InsecureSkipVerify", "must remain false", filepath.Base(rel)},
		})
	}

	if match := shellExecPattern.FindStringIndex(content); match != nil && !guardedShellExecution(content) {
		findings = append(findings, Finding{
			ID:           findingID("shell-dispatch", rel, lineNumber(content, match[0])),
			Title:        "Shell interpreter dispatch from application code",
			Severity:     SeverityHigh,
			Confidence:   ConfidenceMedium,
			CWE:          "CWE-78",
			Location:     Location{Path: rel, Line: lineNumber(content, match[0])},
			Summary:      "The code invokes a shell interpreter with `-c` style argument parsing instead of direct argv execution.",
			Impact:       "If any caller-controlled data reaches this command string, shell metacharacters can change command behavior or execute unintended actions.",
			EvidenceKind: EvidenceKindAnalysisOnly,
			ReproSteps: []string{
				"Trace the call sites of the reported helper and verify whether untrusted input can reach the shell command string.",
				"Prefer a benign sentinel value in a local regression test to confirm that interpolation changes the executed argv, rather than attempting exploit behavior.",
			},
			PatchGuidance: []string{
				"Replace shell dispatch with direct `exec.Command` argv construction where each argument is passed separately.",
				"Introduce an allowlist for accepted commands or subcommands if shelling out is still required.",
			},
			VariantNotes: []string{
				"Search the repo for other `exec.Command(..., \"-c\")`, `bash -lc`, or wrapper helpers that centralize shell execution.",
			},
			ToolTrace:    []string{"filesystem.walk", "pattern.go.shell_dispatch"},
			matchSignals: []string{"exec.Command", filepath.Base(rel)},
		})
	}

	for _, match := range worldWritablePattern.FindAllStringIndex(content, -1) {
		line := lineNumber(content, match[0])
		lineText := lineAt(content, line)
		if !strings.Contains(lineText, "WriteFile") && !strings.Contains(lineText, "Chmod") && !strings.Contains(lineText, "MkdirAll") && !strings.Contains(lineText, "OpenFile") {
			continue
		}
		findings = append(findings, Finding{
			ID:           findingID("world-writable-perms", rel, line),
			Title:        "World-writable filesystem permissions",
			Severity:     SeverityMedium,
			Confidence:   ConfidenceHigh,
			CWE:          "CWE-732",
			Location:     Location{Path: rel, Line: line},
			Summary:      "The code creates files or directories with world-writable permissions.",
			Impact:       "Other local users or processes can tamper with generated files or directories, which can invalidate trust assumptions and create follow-on risks.",
			EvidenceKind: EvidenceKindAnalysisOnly,
			ReproSteps: []string{
				"Review the surrounding file-creation code to confirm whether the path is reachable in normal runtime.",
				"Run the repo-local test or build command, then inspect resulting file modes if the path is exercised during tests.",
			},
			PatchGuidance: []string{
				"Reduce permissions to the minimum required, typically `0o755` for directories or `0o644`/`0o600` for files.",
				"Use process umask plus explicit ownership validation when creating shared artifacts.",
			},
			VariantNotes: []string{
				"Search for repeated `0777`, `0o777`, `0666`, or `0o666` literals in adjacent packages.",
			},
			ToolTrace:    []string{"filesystem.walk", "pattern.go.world_writable_perms"},
			matchSignals: []string{filepath.Base(rel), "0777", "0o777", "0666", "0o666"},
		})
	}

	return findings, nil
}

func guardedShellExecution(content string) bool {
	for _, token := range []string{
		"PermissionDangerFullAccess",
		"PermissionWorkspaceWrite",
		"approval prompt",
		"Approve(",
		"requires workspace-write permission",
	} {
		if strings.Contains(content, token) {
			return true
		}
	}
	return false
}

func runDeterministicCheck(root, scopePath string) (CommandRun, bool) {
	goModuleRoot := findAncestor(scopePath, root, "go.mod")
	if goModuleRoot == "" {
		return CommandRun{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./...")
	cmd.Dir = goModuleRoot
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(goModuleRoot, ".cache", "go-build"))
	output, err := cmd.CombinedOutput()
	exitCode := 0
	succeeded := err == nil
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return CommandRun{
		Command:       "go test ./...",
		ExitCode:      exitCode,
		Succeeded:     succeeded,
		OutputExcerpt: truncate(strings.TrimSpace(string(output)), 4000),
	}, true
}

func findAncestor(start, root, name string) string {
	current := start
	info, err := os.Stat(current)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		current = filepath.Dir(current)
	}
	root = filepath.Clean(root)
	for {
		if _, err := os.Stat(filepath.Join(current, name)); err == nil {
			return current
		}
		if current == root {
			return ""
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func attachEvidence(findings []Finding, run CommandRun) {
	if run.Succeeded || strings.TrimSpace(run.OutputExcerpt) == "" {
		return
	}
	lowered := strings.ToLower(run.OutputExcerpt)
	for index := range findings {
		matches := []string{}
		for _, signal := range findings[index].matchSignals {
			if strings.Contains(lowered, strings.ToLower(signal)) {
				matches = append(matches, signal)
			}
		}
		if len(matches) == 0 {
			continue
		}
		findings[index].EvidenceKind = EvidenceKindExistingTestFailed
		findings[index].FailingTest = &ReproEvidence{
			Command:        run.Command,
			ExitCode:       run.ExitCode,
			OutputExcerpt:  run.OutputExcerpt,
			MatchedSignals: matches,
		}
		findings[index].ToolTrace = append(findings[index].ToolTrace, "exec:"+run.Command)
	}
}

func applyEvidenceProfile(findings []Finding, preference EvidencePreference) {
	for index := range findings {
		switch preference {
		case EvidenceFindings:
			findings[index].ReproSteps = nil
			findings[index].FailingTest = nil
			findings[index].PatchGuidance = nil
			findings[index].EvidenceKind = EvidenceKindAnalysisOnly
		case EvidenceRepro:
			findings[index].PatchGuidance = nil
		case EvidencePatch:
			if len(findings[index].PatchGuidance) > 0 {
				findings[index].PatchGuidance = append(findings[index].PatchGuidance,
					"Validate the fix with the recorded failing test/build command and keep the regression in CI.",
				)
			}
		}
	}
}

func findingID(prefix, rel string, line int) string {
	id := strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(rel)
	return prefix + "-" + id + "-" + strconv.Itoa(line)
}

func lineNumber(content string, offset int) int {
	return 1 + strings.Count(content[:offset], "\n")
}

func lineAt(content string, line int) string {
	lines := strings.Split(content, "\n")
	if line <= 0 || line > len(lines) {
		return ""
	}
	return lines[line-1]
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityCritical:
		return 0
	case SeverityHigh:
		return 1
	case SeverityMedium:
		return 2
	default:
		return 3
	}
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n...[truncated]"
}

func (r Report) JSON() string {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return `{"error":"failed to render report"}`
	}
	return string(data)
}

func (r Report) Markdown() string {
	title := "Security Review"
	switch r.Mode {
	case ModeReview:
		title = "Review"
	case ModeBugHunter:
		title = "Bughunter"
	}
	lines := []string{
		"# " + title,
		"",
		"- Root: `" + filepath.ToSlash(r.Root) + "`",
		"- Scope: `" + r.Scope + "`",
		"- Evidence profile: `" + string(r.Evidence) + "`",
		"- Files scanned: " + strconv.Itoa(r.Summary.FilesScanned),
		"- Findings: " + strconv.Itoa(r.Summary.Findings),
	}
	if r.TestCommand != "" {
		status := "passed"
		if r.ExecutedCheck != nil && !r.ExecutedCheck.Succeeded {
			status = "failed"
		}
		lines = append(lines, "- Evidence command: `"+r.TestCommand+"` ("+status+")")
	}
	lines = append(lines, "", "Severity summary:")
	for _, severity := range []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow} {
		if count := r.Summary.BySeverity[severity]; count > 0 {
			lines = append(lines, "- "+string(severity)+": "+strconv.Itoa(count))
		}
	}
	if len(r.Findings) == 0 {
		lines = append(lines, "", "No defensive findings matched the current workflow heuristics.")
		return strings.Join(lines, "\n")
	}
	for _, finding := range r.Findings {
		lines = append(lines,
			"",
			"## "+finding.Title,
			"",
			"- ID: `"+finding.ID+"`",
			"- Severity: "+string(finding.Severity),
			"- Confidence: "+string(finding.Confidence),
			"- Location: `"+finding.Location.Path+":"+strconv.Itoa(finding.Location.Line)+"`",
		)
		if finding.CWE != "" {
			lines = append(lines, "- CWE: "+finding.CWE)
		}
		lines = append(lines,
			"- Evidence: "+string(finding.EvidenceKind),
			"",
			finding.Summary,
			"",
			"Impact: "+finding.Impact,
		)
		if len(finding.ReproSteps) > 0 {
			lines = append(lines, "", "Repro steps:")
			for _, step := range finding.ReproSteps {
				lines = append(lines, "- "+step)
			}
		}
		if finding.FailingTest != nil {
			lines = append(lines,
				"",
				"Failing test/build evidence:",
				"- Command: `"+finding.FailingTest.Command+"`",
				"- Exit code: "+strconv.Itoa(finding.FailingTest.ExitCode),
			)
			if len(finding.FailingTest.MatchedSignals) > 0 {
				lines = append(lines, "- Matched signals: "+strings.Join(finding.FailingTest.MatchedSignals, ", "))
			}
			if finding.FailingTest.OutputExcerpt != "" {
				lines = append(lines, "", "```text", finding.FailingTest.OutputExcerpt, "```")
			}
		}
		if len(finding.PatchGuidance) > 0 {
			lines = append(lines, "", "Patch guidance:")
			for _, item := range finding.PatchGuidance {
				lines = append(lines, "- "+item)
			}
		}
		if len(finding.VariantNotes) > 0 {
			lines = append(lines, "", "Variant notes:")
			for _, item := range finding.VariantNotes {
				lines = append(lines, "- "+item)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (r Report) Render(format OutputFormat) string {
	switch format {
	case FormatJSON:
		return r.JSON()
	case FormatMarkdown:
		return r.Markdown()
	default:
		return r.Markdown() + "\n\n---\n\n" + r.JSON()
	}
}
