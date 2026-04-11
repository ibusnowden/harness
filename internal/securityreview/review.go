package securityreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

type Workflow string

const (
	WorkflowAuto   Workflow = "auto"
	WorkflowSource Workflow = "source"
	WorkflowFuzz   Workflow = "fuzz"
	WorkflowBinary Workflow = "binary"
)

type TargetType string

const (
	TargetTypeSourceRepo  TargetType = "source_repo"
	TargetTypeLocalBinary TargetType = "local_binary"
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
	EvidenceKindCrashReproducer    EvidenceKind = "crash_reproducer"
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
	Mode         Mode
	Workflow     Workflow
	Scope        string
	Format       OutputFormat
	Evidence     EvidencePreference
	TargetCmd    string
	CorpusDir    string
	ArtifactsDir string
	Budget       time.Duration
	Timeout      time.Duration
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
	ID                string         `json:"id"`
	Title             string         `json:"title"`
	Severity          Severity       `json:"severity"`
	Confidence        Confidence     `json:"confidence"`
	CWE               string         `json:"cwe,omitempty"`
	Location          Location       `json:"location"`
	Summary           string         `json:"summary"`
	Impact            string         `json:"impact"`
	TargetType        TargetType     `json:"target_type,omitempty"`
	EvidenceKind      EvidenceKind   `json:"evidence_kind"`
	ReproSteps        []string       `json:"repro_steps,omitempty"`
	FailingTest       *ReproEvidence `json:"failing_test,omitempty"`
	PatchGuidance     []string       `json:"patch_guidance,omitempty"`
	VariantNotes      []string       `json:"variant_notes,omitempty"`
	ToolTrace         []string       `json:"tool_trace,omitempty"`
	CrashSignature    string         `json:"crash_signature,omitempty"`
	RootCause         string         `json:"root_cause,omitempty"`
	ReproducerPath    string         `json:"reproducer_path,omitempty"`
	ArtifactPaths     []string       `json:"artifact_paths,omitempty"`
	RegressionCommand string         `json:"regression_command,omitempty"`
	Sanitizer         string         `json:"sanitizer,omitempty"`
	DedupeKey         string         `json:"dedupe_key,omitempty"`
	matchSignals      []string
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
	Workflow      Workflow           `json:"workflow"`
	Evidence      EvidencePreference `json:"evidence"`
	Root          string             `json:"root"`
	Scope         string             `json:"scope"`
	TargetType    TargetType         `json:"target_type"`
	RunDir        string             `json:"run_dir,omitempty"`
	TestCommand   string             `json:"test_command,omitempty"`
	ExecutedCheck *CommandRun        `json:"executed_check,omitempty"`
	ArtifactPaths []string           `json:"artifact_paths,omitempty"`
	Findings      []Finding          `json:"findings"`
	Summary       Summary            `json:"summary"`
}

type runArtifacts struct {
	RunDir       string
	ArtifactsDir string
	ReproDir     string
	LogDir       string
	MarkdownPath string
	JSONPath     string
}

type fuzzTarget struct {
	Name  string
	Dir   string
	Seeds [][]byte
}

type commandTemplate struct {
	Args []string
}

var (
	insecureSkipVerifyPattern = regexp.MustCompile(`InsecureSkipVerify\s*:\s*true`)
	shellExecPattern          = regexp.MustCompile(`exec\.Command\(\s*"(?:/bin/)?(?:sh|bash|zsh)"\s*,\s*"(?:-c|-lc)"`)
	worldWritablePattern      = regexp.MustCompile(`(?:0o777|0777|0o666|0666)`)
	goFuzzFuncPattern         = regexp.MustCompile(`func\s+(Fuzz[A-Za-z0-9_]+)\s*\(\s*f\s+\*testing\.F\s*\)\s*{`)
	goFuzzStringSeedPattern   = regexp.MustCompile(`f\.Add\(\s*"((?:[^"\\]|\\.)*)"\s*\)`)
	goFuzzBytesSeedPattern    = regexp.MustCompile(`f\.Add\(\s*\[\]byte\(\s*"((?:[^"\\]|\\.)*)"\s*\)\s*\)`)
	goFuzzSeedFailurePattern  = regexp.MustCompile(`failure while testing seed corpus entry:\s+([A-Za-z0-9_]+)/seed#([0-9]+)`)
	rootCausePathPattern      = regexp.MustCompile(`([A-Za-z0-9_./-]+\.go:[0-9]+)`)
)

func Run(root string, opts Options) (Report, error) {
	root = filepath.Clean(root)
	var err error
	opts, err = normalizeOptions(opts)
	if err != nil {
		return Report{}, err
	}
	scopePath, scopeLabel, err := resolveScope(root, opts.Scope, opts.CorpusDir)
	if err != nil {
		return Report{}, err
	}
	artifacts, err := prepareRunArtifacts(root, opts.ArtifactsDir)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		Mode:       opts.Mode,
		Workflow:   opts.Workflow,
		Evidence:   opts.Evidence,
		Root:       root,
		Scope:      scopeLabel,
		RunDir:     artifacts.RunDir,
		TargetType: targetTypeForOptions(opts),
		Summary: Summary{
			BySeverity: map[Severity]int{},
		},
		ArtifactPaths: []string{artifacts.MarkdownPath, artifacts.JSONPath},
	}

	switch opts.Workflow {
	case WorkflowSource:
		findings, filesScanned, testRun, err := runSourceWorkflow(root, scopePath, opts)
		if err != nil {
			return Report{}, err
		}
		report.Findings = findings
		report.Summary.FilesScanned = filesScanned
		report.TestCommand = testRun.Command
		if testRun.Command != "" {
			report.ExecutedCheck = &testRun
		}
	case WorkflowFuzz:
		findings, filesScanned, err := runFuzzWorkflow(root, scopePath, opts, artifacts)
		if err != nil {
			return Report{}, err
		}
		report.Findings = findings
		report.Summary.FilesScanned = filesScanned
	case WorkflowBinary:
		findings, err := runBinaryWorkflow(root, scopePath, opts, artifacts)
		if err != nil {
			return Report{}, err
		}
		report.Findings = findings
	default:
		return Report{}, fmt.Errorf("unsupported workflow: %s", opts.Workflow)
	}

	for _, finding := range report.Findings {
		report.Summary.BySeverity[finding.Severity]++
	}
	report.Summary.Findings = len(report.Findings)
	applyEvidenceProfile(report.Findings, opts.Evidence)
	if err := persistReport(report, artifacts); err != nil {
		return Report{}, err
	}
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
	if opts.Workflow == "" {
		opts.Workflow = WorkflowAuto
	}
	switch opts.Workflow {
	case WorkflowAuto, WorkflowSource, WorkflowFuzz, WorkflowBinary:
	default:
		return opts, fmt.Errorf("unsupported workflow: %s", opts.Workflow)
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
	if opts.Budget <= 0 {
		opts.Budget = 2 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.Workflow == WorkflowAuto {
		if strings.TrimSpace(opts.TargetCmd) != "" || strings.TrimSpace(opts.CorpusDir) != "" {
			opts.Workflow = WorkflowBinary
		} else {
			opts.Workflow = WorkflowSource
		}
	}
	if opts.Workflow == WorkflowBinary && strings.TrimSpace(opts.TargetCmd) == "" {
		return opts, fmt.Errorf("binary workflow requires --target-cmd")
	}
	if (opts.Workflow == WorkflowBinary || opts.Workflow == WorkflowFuzz) && strings.TrimSpace(opts.TargetCmd) != "" && strings.TrimSpace(opts.CorpusDir) == "" {
		return opts, fmt.Errorf("%s workflow with --target-cmd requires --corpus", opts.Workflow)
	}
	return opts, nil
}

func resolveScope(root, scope, corpus string) (string, string, error) {
	if strings.TrimSpace(corpus) != "" {
		target := filepath.Clean(filepath.Join(root, corpus))
		rel, err := filepath.Rel(root, target)
		if err != nil {
			return "", "", err
		}
		if strings.HasPrefix(rel, "..") {
			return "", "", fmt.Errorf("corpus must stay inside the workspace")
		}
		info, err := os.Stat(target)
		if err != nil {
			return "", "", err
		}
		if !info.IsDir() && info.Mode().IsRegular() == false {
			return "", "", fmt.Errorf("corpus must be a directory or regular file")
		}
		return target, filepath.ToSlash(rel), nil
	}
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

func targetTypeForOptions(opts Options) TargetType {
	if strings.TrimSpace(opts.TargetCmd) != "" {
		return TargetTypeLocalBinary
	}
	return TargetTypeSourceRepo
}

func prepareRunArtifacts(root, override string) (runArtifacts, error) {
	runDir := strings.TrimSpace(override)
	if runDir == "" {
		runDir = filepath.Join(root, ".ascaris", "security", "runs", runID())
	}
	artifacts := runArtifacts{
		RunDir:       runDir,
		ArtifactsDir: filepath.Join(runDir, "artifacts"),
		ReproDir:     filepath.Join(runDir, "reproducers"),
		LogDir:       filepath.Join(runDir, "logs"),
		MarkdownPath: filepath.Join(runDir, "report.md"),
		JSONPath:     filepath.Join(runDir, "findings.json"),
	}
	for _, dir := range []string{artifacts.RunDir, artifacts.ArtifactsDir, artifacts.ReproDir, artifacts.LogDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return runArtifacts{}, err
		}
	}
	return artifacts, nil
}

func runID() string {
	return time.Now().UTC().Format("20060102T150405.000000000")
}

func persistReport(report Report, artifacts runArtifacts) error {
	if err := os.WriteFile(artifacts.MarkdownPath, []byte(report.Markdown()), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(artifacts.JSONPath, []byte(report.JSON()), 0o644); err != nil {
		return err
	}
	return nil
}

func runSourceWorkflow(root, scopePath string, opts Options) ([]Finding, int, CommandRun, error) {
	files, err := collectSourceFiles(scopePath)
	if err != nil {
		return nil, 0, CommandRun{}, err
	}
	findings := make([]Finding, 0, 8)
	for _, path := range files {
		fileFindings, err := analyzeFile(root, path)
		if err != nil {
			return nil, 0, CommandRun{}, err
		}
		findings = append(findings, fileFindings...)
	}
	sortFindings(findings)
	var testRun CommandRun
	if opts.Evidence != EvidenceFindings && len(findings) > 0 {
		if run, ok := runDeterministicCheck(root, scopePath, opts.Timeout); ok {
			testRun = run
			attachEvidence(findings, run)
		}
	}
	return findings, len(files), testRun, nil
}

func runFuzzWorkflow(root, scopePath string, opts Options, artifacts runArtifacts) ([]Finding, int, error) {
	if strings.TrimSpace(opts.TargetCmd) != "" {
		findings, err := runBinaryWorkflow(root, scopePath, opts, artifacts)
		return findings, 0, err
	}
	sourceFindings, filesScanned, _, err := runSourceWorkflow(root, scopePath, Options{
		Mode:     opts.Mode,
		Workflow: WorkflowSource,
		Scope:    filepath.ToSlash(mustRelative(root, scopePath)),
		Format:   opts.Format,
		Evidence: EvidenceFindings,
		Timeout:  opts.Timeout,
	})
	if err != nil {
		return nil, 0, err
	}
	targets, err := discoverGoFuzzTargets(root, scopePath)
	if err != nil {
		return nil, 0, err
	}
	if len(targets) == 0 {
		return nil, 0, fmt.Errorf("no Go fuzz targets discovered; provide --target-cmd and --corpus for binary fuzzing")
	}
	findings := append([]Finding(nil), sourceFindings...)
	for _, target := range targets {
		targetFindings, err := runGoFuzzTarget(root, target, opts, artifacts)
		if err != nil {
			return nil, 0, err
		}
		findings = append(findings, targetFindings...)
	}
	sortFindings(findings)
	return findings, filesScanned, nil
}

func runBinaryWorkflow(root, scopePath string, opts Options, artifacts runArtifacts) ([]Finding, error) {
	template, err := parseCommandTemplate(opts.TargetCmd)
	if err != nil {
		return nil, err
	}
	inputs, err := collectCorpusInputs(scopePath)
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("no corpus inputs found")
	}
	findingsByKey := map[string]Finding{}
	for _, input := range inputs {
		run, signature, sanitizer, rootCause, err := executeTemplate(template, root, input, opts.Timeout)
		if err != nil {
			return nil, err
		}
		if run.Succeeded {
			continue
		}
		if signature == "" {
			signature = "non-zero exit"
		}
		dedupeKey := dedupeKey(signature, sanitizer, filepath.Base(opts.TargetCmd))
		if _, ok := findingsByKey[dedupeKey]; ok {
			continue
		}
		minimizedInput, err := minimizeInput(root, template, input, signature, opts.Timeout)
		if err != nil {
			return nil, err
		}
		reproPath, logPath, err := writeBinaryArtifacts(artifacts, dedupeKey, minimizedInput, run.OutputExcerpt)
		if err != nil {
			return nil, err
		}
		location := Location{Path: filepath.ToSlash(mustRelative(root, input))}
		if rootCause != "" {
			location = rootCauseLocation(rootCause)
		}
		findingsByKey[dedupeKey] = Finding{
			ID:                "crash-" + dedupeKey,
			Title:             "Crash signature reproduced from local input corpus",
			Severity:          SeverityHigh,
			Confidence:        ConfidenceHigh,
			Location:          location,
			Summary:           "A local corpus input causes the authorized target to exit unsuccessfully with a stable crash signature.",
			Impact:            "The target can be forced into a crashing or sanitizer-failing state, which should be triaged and patched before disclosure.",
			TargetType:        TargetTypeLocalBinary,
			EvidenceKind:      EvidenceKindCrashReproducer,
			ReproSteps:        []string{"Replay the minimized reproducer with the recorded regression command and confirm the crash signature remains stable."},
			PatchGuidance:     []string{"Add input validation or guard conditions around the failing path.", "Keep the minimized reproducer as a regression artifact after the fix lands."},
			VariantNotes:      []string{"Search the corpus and nearby parsers for sibling inputs that reach the same crash signature or sanitizer class."},
			ToolTrace:         []string{"exec-template", "binary-triage", "minimize"},
			CrashSignature:    signature,
			RootCause:         rootCause,
			ReproducerPath:    reproPath,
			ArtifactPaths:     []string{reproPath, logPath},
			RegressionCommand: regressionCommand(template, reproPath),
			Sanitizer:         sanitizer,
			DedupeKey:         dedupeKey,
		}
	}
	findings := make([]Finding, 0, len(findingsByKey))
	for _, finding := range findingsByKey {
		findings = append(findings, finding)
	}
	sortFindings(findings)
	return findings, nil
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
		if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
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
	rel := filepath.ToSlash(mustRelative(root, path))
	findings := []Finding{}

	if match := insecureSkipVerifyPattern.FindStringIndex(content); match != nil {
		findings = append(findings, Finding{
			ID:           findingID("tls-skip-verify", rel, lineNumber(content, match[0])),
			Title:        "TLS verification disabled in production code path",
			Severity:     SeverityHigh,
			Confidence:   ConfidenceHigh,
			CWE:          "CWE-295",
			Location:     Location{Path: rel, Line: lineNumber(content, match[0])},
			TargetType:   TargetTypeSourceRepo,
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
			TargetType:   TargetTypeSourceRepo,
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
			TargetType:   TargetTypeSourceRepo,
			Summary:      "The code creates files or directories with world-writable permissions.",
			Impact:       "Other local users or processes can tamper with generated files or directories, which can invalidate trust assumptions and create follow-on risks.",
			EvidenceKind: EvidenceKindAnalysisOnly,
			ReproSteps: []string{
				"Review the surrounding file-creation code to confirm whether the path is reachable in normal runtime.",
				"Run the repo-local test or build command, then inspect resulting file modes if the path is exercised during tests.",
			},
			PatchGuidance: []string{
				"Reduce permissions to the minimum required, typically `0o755` for directories or `0o644` or `0o600` for files.",
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

func runDeterministicCheck(root, scopePath string, timeout time.Duration) (CommandRun, bool) {
	goModuleRoot := findAncestor(scopePath, root, "go.mod")
	if goModuleRoot == "" {
		return CommandRun{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./...")
	cmd.Dir = goModuleRoot
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(goModuleRoot, ".cache", "go-build"))
	output, err := cmd.CombinedOutput()
	return CommandRun{
		Command:       "go test ./...",
		ExitCode:      exitCode(err),
		Succeeded:     err == nil,
		OutputExcerpt: truncate(strings.TrimSpace(string(output)), 4000),
	}, true
}

func discoverGoFuzzTargets(root, scopePath string) ([]fuzzTarget, error) {
	targets := []fuzzTarget{}
	info, err := os.Stat(scopePath)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	if info.IsDir() {
		err = filepath.WalkDir(scopePath, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", ".cache", ".ascaris", "bin", "vendor", "node_modules", "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else if strings.HasSuffix(scopePath, "_test.go") {
		paths = append(paths, scopePath)
	}
	sort.Strings(paths)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		content := string(data)
		matches := goFuzzFuncPattern.FindAllStringSubmatchIndex(content, -1)
		for index, match := range matches {
			name := content[match[2]:match[3]]
			start := match[1]
			end := len(content)
			if index+1 < len(matches) {
				end = matches[index+1][0]
			}
			block := content[start:end]
			seeds := extractGoFuzzSeeds(block)
			targets = append(targets, fuzzTarget{
				Name:  name,
				Dir:   filepath.Dir(path),
				Seeds: seeds,
			})
		}
	}
	return targets, nil
}

func extractGoFuzzSeeds(block string) [][]byte {
	seeds := make([][]byte, 0, 4)
	for _, match := range goFuzzStringSeedPattern.FindAllStringSubmatch(block, -1) {
		if decoded, err := strconv.Unquote(`"` + match[1] + `"`); err == nil {
			seeds = append(seeds, []byte(decoded))
		}
	}
	for _, match := range goFuzzBytesSeedPattern.FindAllStringSubmatch(block, -1) {
		if decoded, err := strconv.Unquote(`"` + match[1] + `"`); err == nil {
			seeds = append(seeds, []byte(decoded))
		}
	}
	return seeds
}

func runGoFuzzTarget(root string, target fuzzTarget, opts Options, artifacts runArtifacts) ([]Finding, error) {
	ctx, cancel := context.WithTimeout(context.Background(), maxDuration(opts.Timeout, opts.Budget+time.Second))
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", ".", "-run=^$", "-fuzz="+target.Name, "-fuzztime="+fuzzBudgetArg(opts.Budget))
	cmd.Dir = target.Dir
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(target.Dir, ".cache", "go-build"))
	output, err := cmd.CombinedOutput()
	run := CommandRun{
		Command:       "go test . -run=^$ -fuzz=" + target.Name + " -fuzztime=" + fuzzBudgetArg(opts.Budget),
		ExitCode:      exitCode(err),
		Succeeded:     err == nil,
		OutputExcerpt: truncate(strings.TrimSpace(string(output)), 4000),
	}
	if run.Succeeded {
		return nil, nil
	}
	signature := crashSignature(run.OutputExcerpt)
	sanitizer := crashSanitizer(run.OutputExcerpt)
	rootCause := crashRootCause(run.OutputExcerpt, target.Dir, root)
	dedupe := dedupeKey(signature, sanitizer, target.Name)
	reproPath := ""
	if data := extractSeedData(run.OutputExcerpt, target); len(data) > 0 {
		reproPath = filepath.Join(artifacts.ReproDir, safeToken(target.Name)+"-"+dedupe+".bin")
		if err := os.WriteFile(reproPath, data, 0o644); err != nil {
			return nil, err
		}
	}
	logPath := filepath.Join(artifacts.LogDir, safeToken(target.Name)+"-"+dedupe+".log")
	if err := os.WriteFile(logPath, []byte(run.OutputExcerpt), 0o644); err != nil {
		return nil, err
	}
	finding := Finding{
		ID:                "fuzz-" + safeToken(target.Name) + "-" + dedupe,
		Title:             "Go fuzz target crashes on the current seed corpus",
		Severity:          SeverityHigh,
		Confidence:        ConfidenceHigh,
		Location:          locationFromRootCause(rootCause),
		TargetType:        TargetTypeSourceRepo,
		Summary:           "A discovered Go fuzz target exits unsuccessfully when exercised with its current seed corpus.",
		Impact:            "The target reaches a crashing state under local fuzz execution, which should be triaged and fixed before disclosure.",
		EvidenceKind:      evidenceKindForReproducer(reproPath),
		ReproSteps:        []string{"Replay the regression command and confirm the crash signature remains stable.", "Keep the crashing seed as a regression artifact after the fix lands."},
		PatchGuidance:     []string{"Harden the parsing or validation path that leads to the reported crash.", "Add a regression test or fuzz corpus entry that keeps the target from regressing."},
		VariantNotes:      []string{"Inspect sibling fuzz targets or parsers in the same package for equivalent crash conditions."},
		ToolTrace:         []string{"go-test-fuzz", "seed-corpus", "artifact-log"},
		CrashSignature:    signature,
		RootCause:         rootCause,
		ReproducerPath:    reproPath,
		ArtifactPaths:     compactPaths(reproPath, logPath),
		RegressionCommand: run.Command,
		Sanitizer:         sanitizer,
		DedupeKey:         dedupe,
	}
	return []Finding{finding}, nil
}

func evidenceKindForReproducer(path string) EvidenceKind {
	if strings.TrimSpace(path) != "" {
		return EvidenceKindCrashReproducer
	}
	return EvidenceKindBuildFailure
}

func fuzzBudgetArg(duration time.Duration) string {
	if duration < time.Second {
		return "1x"
	}
	return duration.Round(time.Second).String()
}

func extractSeedData(output string, target fuzzTarget) []byte {
	match := goFuzzSeedFailurePattern.FindStringSubmatch(output)
	if len(match) == 3 && match[1] == target.Name {
		index, err := strconv.Atoi(match[2])
		if err == nil && index >= 0 && index < len(target.Seeds) {
			return append([]byte(nil), target.Seeds[index]...)
		}
	}
	seedMatch := regexp.MustCompile(`seed#([0-9]+)`).FindStringSubmatch(output)
	if len(seedMatch) == 2 {
		index, err := strconv.Atoi(seedMatch[1])
		if err == nil && index >= 0 && index < len(target.Seeds) {
			return append([]byte(nil), target.Seeds[index]...)
		}
	}
	if len(target.Seeds) == 1 {
		return append([]byte(nil), target.Seeds[0]...)
	}
	return nil
}

func collectCorpusInputs(scopePath string) ([]string, error) {
	info, err := os.Stat(scopePath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{scopePath}, nil
	}
	inputs := []string{}
	err = filepath.WalkDir(scopePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		inputs = append(inputs, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(inputs)
	return inputs, nil
}

func parseCommandTemplate(value string) (commandTemplate, error) {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return commandTemplate{}, fmt.Errorf("target command is required")
	}
	for _, field := range fields {
		if strings.Contains(field, "://") {
			return commandTemplate{}, fmt.Errorf("target command must stay local-only and may not include URLs")
		}
	}
	switch fields[0] {
	case "sh", "bash", "zsh", "curl", "wget", "nc", "ncat", "telnet":
		return commandTemplate{}, fmt.Errorf("target command %q is not allowed in defensive research mode", fields[0])
	}
	return commandTemplate{Args: fields}, nil
}

func executeTemplate(template commandTemplate, root, input string, timeout time.Duration) (CommandRun, string, string, string, error) {
	args := make([]string, 0, len(template.Args)+1)
	replaced := false
	for _, arg := range template.Args {
		if arg == "{{input}}" {
			args = append(args, input)
			replaced = true
			continue
		}
		args = append(args, arg)
	}
	if !replaced {
		args = append(args, input)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	run := CommandRun{
		Command:       strings.Join(args, " "),
		ExitCode:      exitCode(err),
		Succeeded:     err == nil,
		OutputExcerpt: truncate(strings.TrimSpace(string(output)), 4000),
	}
	return run, crashSignature(run.OutputExcerpt), crashSanitizer(run.OutputExcerpt), crashRootCause(run.OutputExcerpt, root, root), nil
}

func minimizeInput(root string, template commandTemplate, inputPath, expectedSignature string, timeout time.Duration) ([]byte, error) {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, err
	}
	if len(data) <= 1 {
		return data, nil
	}
	best := append([]byte(nil), data...)
	granularity := 2
	for len(best) >= 2 {
		reduced := false
		chunkSize := int(math.Ceil(float64(len(best)) / float64(granularity)))
		if chunkSize < 1 {
			chunkSize = 1
		}
		for start := 0; start < len(best); start += chunkSize {
			end := start + chunkSize
			if end > len(best) {
				end = len(best)
			}
			candidate := append(append([]byte(nil), best[:start]...), best[end:]...)
			if len(candidate) == 0 {
				continue
			}
			matches, err := replayCandidate(root, template, candidate, expectedSignature, timeout)
			if err != nil {
				return nil, err
			}
			if matches {
				best = candidate
				granularity = 2
				reduced = true
				break
			}
		}
		if reduced {
			continue
		}
		if granularity >= len(best) {
			break
		}
		granularity = minInt(len(best), granularity*2)
	}
	return best, nil
}

func replayCandidate(root string, template commandTemplate, candidate []byte, expectedSignature string, timeout time.Duration) (bool, error) {
	file, err := os.CreateTemp("", "ascaris-crash-candidate-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(candidate); err != nil {
		_ = file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	run, signature, _, _, err := executeTemplate(template, root, file.Name(), timeout)
	if err != nil {
		return false, err
	}
	return !run.Succeeded && signature == expectedSignature, nil
}

func writeBinaryArtifacts(artifacts runArtifacts, dedupe string, minimized []byte, log string) (string, string, error) {
	reproPath := filepath.Join(artifacts.ReproDir, dedupe+".bin")
	if err := os.WriteFile(reproPath, minimized, 0o644); err != nil {
		return "", "", err
	}
	logPath := filepath.Join(artifacts.LogDir, dedupe+".log")
	if err := os.WriteFile(logPath, []byte(log), 0o644); err != nil {
		return "", "", err
	}
	return reproPath, logPath, nil
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
			matches = fallbackEvidenceSignals(findings[index], lowered)
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

func fallbackEvidenceSignals(finding Finding, loweredOutput string) []string {
	switch finding.CWE {
	case "CWE-295":
		if strings.Contains(loweredOutput, "insecureskipverify") || strings.Contains(loweredOutput, "must remain false") {
			return []string{"tls regression"}
		}
	case "CWE-732":
		if strings.Contains(loweredOutput, "permission") || strings.Contains(loweredOutput, "mode") {
			return []string{"permission regression"}
		}
	}
	return nil
}

func applyEvidenceProfile(findings []Finding, preference EvidencePreference) {
	for index := range findings {
		switch preference {
		case EvidenceFindings:
			findings[index].ReproSteps = nil
			findings[index].FailingTest = nil
			findings[index].PatchGuidance = nil
			findings[index].ReproducerPath = ""
			findings[index].ArtifactPaths = nil
			findings[index].RegressionCommand = ""
			if findings[index].EvidenceKind != EvidenceKindCrashReproducer {
				findings[index].EvidenceKind = EvidenceKindAnalysisOnly
			}
		case EvidenceRepro:
			findings[index].PatchGuidance = nil
		case EvidencePatch:
			if len(findings[index].PatchGuidance) > 0 {
				findings[index].PatchGuidance = append(findings[index].PatchGuidance,
					"Validate the fix with the recorded failing test, fuzz, or crash regression command and keep the regression in CI.",
				)
			}
		}
	}
}

func sortFindings(findings []Finding) {
	sort.Slice(findings, func(i, j int) bool {
		if severityRank(findings[i].Severity) == severityRank(findings[j].Severity) {
			if findings[i].Location.Path == findings[j].Location.Path {
				return findings[i].Location.Line < findings[j].Location.Line
			}
			return findings[i].Location.Path < findings[j].Location.Path
		}
		return severityRank(findings[i].Severity) < severityRank(findings[j].Severity)
	})
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

func crashSignature(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.Contains(trimmed, "AddressSanitizer"):
			return trimmed
		case strings.Contains(trimmed, "UndefinedBehaviorSanitizer"):
			return trimmed
		case strings.HasPrefix(trimmed, "panic:"):
			return trimmed
		case strings.Contains(trimmed, "failure while testing seed corpus entry"):
			return trimmed
		}
	}
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func crashSanitizer(output string) string {
	switch {
	case strings.Contains(output, "AddressSanitizer"):
		return "asan"
	case strings.Contains(output, "UndefinedBehaviorSanitizer"):
		return "ubsan"
	case strings.Contains(output, "panic:"):
		return "panic"
	default:
		return ""
	}
}

func crashRootCause(output, baseDir, root string) string {
	matches := rootCausePathPattern.FindAllStringSubmatch(output, -1)
	for _, match := range matches {
		value := match[1]
		candidate := value
		if strings.HasPrefix(candidate, filepath.ToSlash(root)+"/") {
			return candidate
		}
		if !filepath.IsAbs(value) {
			joined := filepath.Join(baseDir, filepath.FromSlash(value))
			if rel, err := filepath.Rel(root, joined); err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(rel)
			}
			return filepath.ToSlash(value)
		}
	}
	return ""
}

func locationFromRootCause(rootCause string) Location {
	if strings.TrimSpace(rootCause) == "" {
		return Location{}
	}
	parts := strings.Split(rootCause, ":")
	if len(parts) < 2 {
		return Location{Path: rootCause}
	}
	line, _ := strconv.Atoi(parts[len(parts)-1])
	return Location{Path: strings.Join(parts[:len(parts)-1], ":"), Line: line}
}

func rootCauseLocation(rootCause string) Location {
	return locationFromRootCause(rootCause)
}

func dedupeKey(parts ...string) string {
	joined := strings.Join(parts, "|")
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:8])
}

func regressionCommand(template commandTemplate, inputPath string) string {
	args := make([]string, 0, len(template.Args)+1)
	replaced := false
	for _, arg := range template.Args {
		if arg == "{{input}}" {
			args = append(args, inputPath)
			replaced = true
			continue
		}
		args = append(args, arg)
	}
	if !replaced {
		args = append(args, inputPath)
	}
	return strings.Join(args, " ")
}

func compactPaths(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
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

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func safeToken(value string) string {
	return strings.NewReplacer("/", "-", ".", "-", " ", "-", "_", "-").Replace(value)
}

func mustRelative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
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
		"- Workflow: `" + string(r.Workflow) + "`",
		"- Target type: `" + string(r.TargetType) + "`",
		"- Evidence profile: `" + string(r.Evidence) + "`",
		"- Run directory: `" + filepath.ToSlash(r.RunDir) + "`",
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
	if len(r.ArtifactPaths) > 0 {
		lines = append(lines, "- Report artifacts: "+strings.Join(r.ArtifactPaths, ", "))
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
		if finding.TargetType != "" {
			lines = append(lines, "- Target type: `"+string(finding.TargetType)+"`")
		}
		lines = append(lines,
			"- Evidence: "+string(finding.EvidenceKind),
			"",
			finding.Summary,
			"",
			"Impact: "+finding.Impact,
		)
		if finding.CrashSignature != "" {
			lines = append(lines, "- Crash signature: `"+finding.CrashSignature+"`")
		}
		if finding.RootCause != "" {
			lines = append(lines, "- Root cause: `"+finding.RootCause+"`")
		}
		if finding.ReproducerPath != "" {
			lines = append(lines, "- Reproducer: `"+finding.ReproducerPath+"`")
		}
		if finding.RegressionCommand != "" {
			lines = append(lines, "- Regression command: `"+finding.RegressionCommand+"`")
		}
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
		if len(finding.ArtifactPaths) > 0 {
			lines = append(lines, "", "Artifacts:")
			for _, path := range finding.ArtifactPaths {
				lines = append(lines, "- `"+path+"`")
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
