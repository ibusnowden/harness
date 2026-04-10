package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ascaris/internal/commands"
	"ascaris/internal/manifest"
	"ascaris/internal/tools"
)

type TraceabilityManifest struct {
	RootFiles map[string]string `json:"root_files"`
	RootDirs  map[string]string `json:"root_dirs"`
}

type AuditResult struct {
	TraceabilityPresent     bool
	RootFileCoverage        [2]int
	DirectoryCoverage       [2]int
	TotalGoFiles            int
	CommandCount            int
	ToolCount               int
	MissingRootTargets      []string
	MissingDirectoryTargets []string
}

func (a AuditResult) Markdown() string {
	lines := []string{"# Traceability Audit"}
	if !a.TraceabilityPresent {
		lines = append(lines, "Traceability fixtures unavailable; audit cannot verify mapped coverage.")
		return strings.Join(lines, "\n")
	}
	lines = append(lines,
		"",
		"Root file coverage: **"+strconv.Itoa(a.RootFileCoverage[0])+"/"+strconv.Itoa(a.RootFileCoverage[1])+"**",
		"Directory coverage: **"+strconv.Itoa(a.DirectoryCoverage[0])+"/"+strconv.Itoa(a.DirectoryCoverage[1])+"**",
		"Total Go files: **"+strconv.Itoa(a.TotalGoFiles)+"**",
		"Live commands: **"+strconv.Itoa(a.CommandCount)+"**",
		"Live tools: **"+strconv.Itoa(a.ToolCount)+"**",
		"",
		"Missing root targets:",
	)
	if len(a.MissingRootTargets) == 0 {
		lines = append(lines, "- none")
	} else {
		for _, item := range a.MissingRootTargets {
			lines = append(lines, "- "+item)
		}
	}
	lines = append(lines, "", "Missing directory targets:")
	if len(a.MissingDirectoryTargets) == 0 {
		lines = append(lines, "- none")
	} else {
		for _, item := range a.MissingDirectoryTargets {
			lines = append(lines, "- "+item)
		}
	}
	return strings.Join(lines, "\n")
}

func LoadTraceability(root string) (TraceabilityManifest, error) {
	data, err := os.ReadFile(filepath.Join(root, "testdata", "parity", "traceability.json"))
	if err != nil {
		return TraceabilityManifest{}, err
	}
	var manifest TraceabilityManifest
	err = json.Unmarshal(data, &manifest)
	return manifest, err
}

func Run(root string) (AuditResult, error) {
	trace, err := LoadTraceability(root)
	if err != nil {
		if os.IsNotExist(err) {
			return AuditResult{TraceabilityPresent: false}, nil
		}
		return AuditResult{}, err
	}
	rootHits := 0
	missingRoots := []string{}
	rootNames := sortedKeys(trace.RootFiles)
	for _, name := range rootNames {
		target := trace.RootFiles[name]
		if target != "" && exists(filepath.Join(root, filepath.FromSlash(target))) {
			rootHits++
		} else {
			missingRoots = append(missingRoots, name)
		}
	}
	dirHits := 0
	missingDirs := []string{}
	dirNames := sortedKeys(trace.RootDirs)
	for _, name := range dirNames {
		target := trace.RootDirs[name]
		if target != "" && exists(filepath.Join(root, filepath.FromSlash(target))) {
			dirHits++
		} else {
			missingDirs = append(missingDirs, name)
		}
	}
	m, err := manifest.Build(root)
	if err != nil {
		return AuditResult{}, err
	}
	commandEntries, err := commands.Catalog(root)
	if err != nil {
		return AuditResult{}, err
	}
	toolEntries, err := tools.Catalog(root, tools.CatalogOptions{IncludeMCP: true})
	if err != nil {
		return AuditResult{}, err
	}
	sort.Strings(missingRoots)
	sort.Strings(missingDirs)
	return AuditResult{
		TraceabilityPresent:     true,
		RootFileCoverage:        [2]int{rootHits, len(rootNames)},
		DirectoryCoverage:       [2]int{dirHits, len(dirNames)},
		TotalGoFiles:            m.TotalGoFiles,
		CommandCount:            len(commandEntries),
		ToolCount:               len(toolEntries),
		MissingRootTargets:      missingRoots,
		MissingDirectoryTargets: missingDirs,
	}, nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
