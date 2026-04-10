package securityreview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			if strings.Contains(item, "Validate the fix with the recorded failing test/build command") {
				foundValidationStep = true
			}
		}
	}
	if !foundValidationStep {
		t.Fatalf("expected patch profile to add validation guidance")
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
