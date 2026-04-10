package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ascaris/internal/config"
)

type Scope string

const (
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
)

type Root struct {
	Scope Scope  `json:"scope"`
	Path  string `json:"path"`
}

type Summary struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	Scope           Scope  `json:"scope"`
	Path            string `json:"path"`
	ShadowedBy      *Scope `json:"shadowed_by,omitempty"`
}

func DiscoverRoots(root string) []Root {
	configHome := config.ConfigHome(root)
	seen := map[string]struct{}{}
	push := func(list *[]Root, scope Scope, path string) {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		*list = append(*list, Root{Scope: scope, Path: path})
	}
	var roots []Root
	push(&roots, ScopeProject, filepath.Join(root, ".ascaris", "agents"))
	push(&roots, ScopeUser, filepath.Join(configHome, "agents"))
	return roots
}

func Load(root string) ([]Summary, error) {
	return LoadFromRoots(DiscoverRoots(root))
}

func LoadFromRoots(roots []Root) ([]Summary, error) {
	var agents []Summary
	active := map[string]Scope{}
	for _, root := range roots {
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			return nil, err
		}
		var current []Summary
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".toml") {
				continue
			}
			path := filepath.Join(root.Path, entry.Name())
			contents, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			name := parseTomlString(string(contents), "name")
			if name == "" {
				name = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			}
			current = append(current, Summary{
				Name:            name,
				Description:     parseTomlString(string(contents), "description"),
				Model:           parseTomlString(string(contents), "model"),
				ReasoningEffort: parseTomlString(string(contents), "model_reasoning_effort"),
				Scope:           root.Scope,
				Path:            path,
			})
		}
		sort.Slice(current, func(i, j int) bool {
			return strings.ToLower(current[i].Name) < strings.ToLower(current[j].Name)
		})
		for _, agent := range current {
			key := strings.ToLower(agent.Name)
			if winner, ok := active[key]; ok {
				agent.ShadowedBy = &winner
			} else {
				active[key] = agent.Scope
			}
			agents = append(agents, agent)
		}
	}
	return agents, nil
}

func RenderReport(agents []Summary) string {
	if len(agents) == 0 {
		return "No agents found."
	}
	activeCount := 0
	for _, agent := range agents {
		if agent.ShadowedBy == nil {
			activeCount++
		}
	}
	lines := []string{"Agents", "  " + strconv.Itoa(activeCount) + " active agents", ""}
	for _, scope := range []Scope{ScopeProject, ScopeUser} {
		lines = append(lines, strings.Title(string(scope))+":")
		wrote := false
		for _, agent := range agents {
			if agent.Scope != scope {
				continue
			}
			wrote = true
			detail := agent.Name
			for _, value := range []string{agent.Description, agent.Model, agent.ReasoningEffort} {
				if strings.TrimSpace(value) != "" {
					detail += " · " + value
				}
			}
			if agent.ShadowedBy != nil {
				lines = append(lines, "  (shadowed by "+string(*agent.ShadowedBy)+") "+detail)
			} else {
				lines = append(lines, "  "+detail)
			}
		}
		if wrote {
			lines = append(lines, "")
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func RenderReportJSON(root string, agents []Summary) string {
	activeCount := 0
	for _, agent := range agents {
		if agent.ShadowedBy == nil {
			activeCount++
		}
	}
	payload := map[string]any{
		"kind":              "agents",
		"working_directory": root,
		"summary": map[string]any{
			"total":    len(agents),
			"active":   activeCount,
			"shadowed": len(agents) - activeCount,
		},
		"agents": agents,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return string(data)
}

func parseTomlString(contents, key string) string {
	prefix := key + " ="
	for _, line := range strings.Split(contents, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		value = strings.Trim(value, `"`)
		if value != "" {
			return value
		}
	}
	return ""
}
