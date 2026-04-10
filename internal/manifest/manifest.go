package manifest

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ascaris/internal/models"
)

type Manifest struct {
	Root           string
	TotalGoFiles   int
	TopLevelModule []models.Subsystem
}

func (m Manifest) Markdown() string {
	lines := []string{
		"Port root: `" + m.Root + "`",
		"Total Go files: **" + itoa(m.TotalGoFiles) + "**",
		"",
		"Top-level Go modules:",
	}
	for _, module := range m.TopLevelModule {
		lines = append(lines, "- `"+module.Name+"` ("+itoa(module.FileCount)+" files) - "+module.Notes)
	}
	return strings.Join(lines, "\n")
}

func Build(root string) (Manifest, error) {
	counts := map[string]int{}
	total := 0
	notes := map[string]string{
		"cmd":       "CLI entrypoint",
		"cli":       "command dispatch and help rendering",
		"commands":  "live command registry",
		"context":   "workspace context reporting",
		"manifest":  "workspace manifest generation",
		"models":    "shared structs",
		"query":     "query engine and turn loop",
		"runtime":   "runtime session orchestration",
		"tools":     "live tool registry",
		"reference": "fixture loaders and traceability metadata",
		"sessions":  "session persistence under .ascaris",
		"setup":     "startup and health checks",
		"version":   "build version metadata",
	}
	err := filepath.WalkDir(root, func(current string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".cache", ".git", ".ascaris", "bin", "legacy":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) == 0 {
			return nil
		}
		top := parts[0]
		if top == "internal" && len(parts) > 1 {
			top = parts[1]
		}
		counts[top]++
		total++
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	modules := make([]models.Subsystem, 0, len(counts))
	for name, count := range counts {
		note := notes[name]
		if note == "" {
			note = "Go harness support module"
		}
		modules = append(modules, models.Subsystem{
			Name:      name,
			Path:      "internal/" + name,
			FileCount: count,
			Notes:     note,
		})
	}
	sort.Slice(modules, func(i, j int) bool {
		if modules[i].FileCount == modules[j].FileCount {
			return modules[i].Name < modules[j].Name
		}
		return modules[i].FileCount > modules[j].FileCount
	})
	return Manifest{Root: root, TotalGoFiles: total, TopLevelModule: modules}, nil
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
