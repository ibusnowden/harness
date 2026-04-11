package securityreview

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunFindsVulnerabilitiesAndAttachesRepro(t *testing.T) {
	root := copyFixtureRepo(t, "vulnerable_go")
	report, err := Run(root, Options{
		Mode:     ModeSecurityReview,
		Format:   FormatJSON,
		Evidence: EvidenceRepro,
	})
	if err != nil {
		t.Fatalf("run review: %v", err)
	}
	if report.Summary.Findings < 2 {
		t.Fatalf("expected multiple findings, got %d", report.Summary.Findings)
	}
	if report.ExecutedCheck == nil || report.ExecutedCheck.Succeeded {
		t.Fatalf("expected failing deterministic check, got %#v", report.ExecutedCheck)
	}
	var tlsFinding *Finding
	var shellFinding *Finding
	for i := range report.Findings {
		switch report.Findings[i].CWE {
		case "CWE-295":
			tlsFinding = &report.Findings[i]
		case "CWE-78":
			shellFinding = &report.Findings[i]
		}
	}
	if tlsFinding == nil || tlsFinding.EvidenceKind != EvidenceKindExistingTestFailed || tlsFinding.FailingTest == nil {
		t.Fatalf("expected TLS finding with failing test evidence, got %#v", tlsFinding)
	}
	if shellFinding == nil || shellFinding.EvidenceKind != EvidenceKindAnalysisOnly {
		t.Fatalf("expected shell finding without matched test evidence, got %#v", shellFinding)
	}
	if !strings.Contains(report.Markdown(), "TLS verification disabled") {
		t.Fatalf("expected markdown report to describe TLS finding")
	}
	var decoded Report
	if err := json.Unmarshal([]byte(report.JSON()), &decoded); err != nil {
		t.Fatalf("decode report json: %v", err)
	}
}

func TestRunSupportsScopeFiltering(t *testing.T) {
	root := copyFixtureRepo(t, "vulnerable_go")
	report, err := Run(root, Options{
		Mode:     ModeSecurityReview,
		Format:   FormatMarkdown,
		Evidence: EvidenceFindings,
		Scope:    "vulnerable",
	})
	if err != nil {
		t.Fatalf("run review with scope: %v", err)
	}
	if report.Scope != "vulnerable" {
		t.Fatalf("unexpected scope label: %q", report.Scope)
	}
}

func TestRunAppliesEvidenceProfiles(t *testing.T) {
	root := copyFixtureRepo(t, "vulnerable_go")
	findingsOnly, err := Run(root, Options{
		Mode:     ModeSecurityReview,
		Format:   FormatJSON,
		Evidence: EvidenceFindings,
	})
	if err != nil {
		t.Fatalf("run findings-only review: %v", err)
	}
	for _, finding := range findingsOnly.Findings {
		if finding.FailingTest != nil || len(finding.ReproSteps) != 0 || len(finding.PatchGuidance) != 0 {
			t.Fatalf("expected findings-only profile to suppress repro and patch details: %#v", finding)
		}
	}

	patchReport, err := Run(root, Options{
		Mode:     ModeSecurityReview,
		Format:   FormatJSON,
		Evidence: EvidencePatch,
	})
	if err != nil {
		t.Fatalf("run patch review: %v", err)
	}
	foundValidationStep := false
	for _, finding := range patchReport.Findings {
		for _, item := range finding.PatchGuidance {
			if strings.Contains(item, "Validate the fix with the recorded failing test, fuzz, or crash regression command") {
				foundValidationStep = true
			}
		}
	}
	if !foundValidationStep {
		t.Fatalf("expected patch profile to add validation guidance")
	}
}

func TestRunSupportsFuzzWorkflow(t *testing.T) {
	root := copyFixtureRepo(t, "fuzz_go")
	report, err := Run(root, Options{
		Mode:     ModeSecurityReview,
		Workflow: WorkflowFuzz,
		Format:   FormatJSON,
		Evidence: EvidenceRepro,
		Budget:   time.Second,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("run fuzz workflow: %v", err)
	}
	if report.Workflow != WorkflowFuzz {
		t.Fatalf("unexpected workflow: %s", report.Workflow)
	}
	var fuzzFinding *Finding
	for i := range report.Findings {
		if strings.HasPrefix(report.Findings[i].ID, "fuzz-") {
			fuzzFinding = &report.Findings[i]
			break
		}
	}
	if fuzzFinding == nil {
		t.Fatalf("expected fuzz finding, got %#v", report.Findings)
	}
	if fuzzFinding.TargetType != TargetTypeSourceRepo {
		t.Fatalf("expected source repo target, got %#v", fuzzFinding)
	}
	if fuzzFinding.EvidenceKind != EvidenceKindCrashReproducer {
		t.Fatalf("expected crash reproducer evidence, got %#v", fuzzFinding)
	}
	if !strings.Contains(fuzzFinding.RegressionCommand, "-fuzz=FuzzCrash") {
		t.Fatalf("expected fuzz regression command, got %q", fuzzFinding.RegressionCommand)
	}
	if fuzzFinding.ReproducerPath == "" {
		t.Fatalf("expected reproducer path, got %#v", fuzzFinding)
	}
	if data, err := os.ReadFile(fuzzFinding.ReproducerPath); err != nil || !strings.Contains(string(data), "panic") {
		t.Fatalf("expected reproducer artifact with panic seed, err=%v", err)
	}
}

func TestRunSupportsCrashTriageWorkflow(t *testing.T) {
	root := copyFixtureRepo(t, "exec_crash")
	binaryPath := filepath.Join(t.TempDir(), "crasher")
	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/crasher")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(root, ".cache", "go-build"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build crash fixture: %v\n%s", err, string(output))
	}

	report, err := Run(root, Options{
		Mode:      ModeSecurityReview,
		Workflow:  WorkflowBinary,
		Format:    FormatJSON,
		Evidence:  EvidenceRepro,
		TargetCmd: binaryPath + " {{input}}",
		CorpusDir: "corpus",
		Timeout:   3 * time.Second,
	})
	if err != nil {
		t.Fatalf("run binary workflow: %v", err)
	}
	if report.Workflow != WorkflowBinary {
		t.Fatalf("unexpected workflow: %s", report.Workflow)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("expected one deduped crash finding, got %#v", report.Findings)
	}
	finding := report.Findings[0]
	if finding.TargetType != TargetTypeLocalBinary {
		t.Fatalf("expected local binary target, got %#v", finding)
	}
	if finding.DedupeKey == "" || finding.ReproducerPath == "" {
		t.Fatalf("expected dedupe key and reproducer path, got %#v", finding)
	}
	if len(finding.ArtifactPaths) < 2 {
		t.Fatalf("expected reproducer and log artifacts, got %#v", finding.ArtifactPaths)
	}
	if _, err := os.Stat(filepath.Join(report.RunDir, "report.md")); err != nil {
		t.Fatalf("expected report markdown artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(report.RunDir, "findings.json")); err != nil {
		t.Fatalf("expected report json artifact: %v", err)
	}
}

func copyFixtureRepo(t *testing.T, name string) string {
	t.Helper()
	srcRoot := filepath.Join("testdata", name)
	dstRoot := t.TempDir()
	if err := filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstRoot, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	}); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	return dstRoot
}
