package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ascaris/internal/config"
)

type Scope string

const (
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
)

type Origin string

const (
	OriginSkillsDir      Origin = "skills_dir"
	OriginCommandsDir    Origin = "commands_dir"
	defaultMarkdownPerms        = 0o644
)

type Root struct {
	Scope  Scope  `json:"scope"`
	Path   string `json:"path"`
	Origin Origin `json:"origin"`
}

type Summary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Scope       Scope  `json:"scope"`
	Origin      Origin `json:"origin"`
	Path        string `json:"path"`
	ShadowedBy  *Scope `json:"shadowed_by,omitempty"`
}

type InstallResult struct {
	InvocationName string `json:"invocation_name"`
	DisplayName    string `json:"display_name,omitempty"`
	Source         string `json:"source"`
	RegistryRoot   string `json:"registry_root"`
	InstalledPath  string `json:"installed_path"`
}

func DiscoverRoots(root string) []Root {
	configHome := config.ConfigHome(root)
	seen := map[string]struct{}{}
	push := func(list *[]Root, scope Scope, path string, origin Origin) {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		*list = append(*list, Root{Scope: scope, Path: path, Origin: origin})
	}
	var roots []Root
	push(&roots, ScopeProject, filepath.Join(root, ".ascaris", "skills"), OriginSkillsDir)
	push(&roots, ScopeProject, filepath.Join(root, ".ascaris", "commands"), OriginCommandsDir)
	push(&roots, ScopeUser, filepath.Join(configHome, "skills"), OriginSkillsDir)
	push(&roots, ScopeUser, filepath.Join(configHome, "commands"), OriginCommandsDir)
	return roots
}

func Load(root string) ([]Summary, error) {
	return LoadFromRoots(DiscoverRoots(root))
}

func LoadFromRoots(roots []Root) ([]Summary, error) {
	var skills []Summary
	active := map[string]Scope{}
	for _, root := range roots {
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			return nil, err
		}
		var rootSkills []Summary
		for _, entry := range entries {
			summary, ok, err := loadEntry(root, entry)
			if err != nil {
				return nil, err
			}
			if ok {
				rootSkills = append(rootSkills, summary)
			}
		}
		sort.Slice(rootSkills, func(i, j int) bool {
			return strings.ToLower(rootSkills[i].Name) < strings.ToLower(rootSkills[j].Name)
		})
		for _, skill := range rootSkills {
			key := strings.ToLower(skill.Name)
			if winner, ok := active[key]; ok {
				skill.ShadowedBy = &winner
			} else {
				active[key] = skill.Scope
			}
			skills = append(skills, skill)
		}
	}
	return skills, nil
}

func ResolvePath(root, name string) (string, error) {
	needle := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(name, "$"), "/"))
	if needle == "" {
		return "", fmt.Errorf("skill must not be empty")
	}
	skills, err := Load(root)
	if err != nil {
		return "", err
	}
	for _, skill := range skills {
		if strings.EqualFold(skill.Name, needle) && skill.ShadowedBy == nil {
			return skill.Path, nil
		}
	}
	return "", fmt.Errorf("unknown skill: %s", needle)
}

func Install(root, source string) (InstallResult, error) {
	registryRoot := filepath.Join(config.ConfigHome(root), "skills")
	return InstallInto(root, source, registryRoot)
}

func InstallInto(root, source, registryRoot string) (InstallResult, error) {
	sourcePath, skillPath, err := resolveInstallSource(root, source)
	if err != nil {
		return InstallResult{}, err
	}
	contents, err := os.ReadFile(skillPath)
	if err != nil {
		return InstallResult{}, err
	}
	displayName, _ := parseFrontmatter(string(contents))
	invocationName := sanitizeInvocationName(firstNonEmpty(displayName, fallbackInstallName(sourcePath)))
	if invocationName == "" {
		return InstallResult{}, fmt.Errorf("unable to derive an installable skill name from %s", sourcePath)
	}
	installedPath := filepath.Join(registryRoot, invocationName)
	if _, err := os.Stat(installedPath); err == nil {
		return InstallResult{}, fmt.Errorf("skill %q is already installed at %s", invocationName, installedPath)
	}
	if err := os.MkdirAll(installedPath, 0o755); err != nil {
		return InstallResult{}, err
	}
	if stat, err := os.Stat(sourcePath); err == nil && stat.IsDir() {
		if err := copyDirContents(sourcePath, installedPath); err != nil {
			_ = os.RemoveAll(installedPath)
			return InstallResult{}, err
		}
	} else {
		if err := os.WriteFile(filepath.Join(installedPath, "SKILL.md"), contents, defaultMarkdownPerms); err != nil {
			_ = os.RemoveAll(installedPath)
			return InstallResult{}, err
		}
	}
	return InstallResult{
		InvocationName: invocationName,
		DisplayName:    displayName,
		Source:         sourcePath,
		RegistryRoot:   registryRoot,
		InstalledPath:  installedPath,
	}, nil
}

func RenderReport(skills []Summary) string {
	if len(skills) == 0 {
		return "No skills found."
	}
	activeCount := 0
	for _, skill := range skills {
		if skill.ShadowedBy == nil {
			activeCount++
		}
	}
	lines := []string{"Skills", fmt.Sprintf("  %d available skills", activeCount), ""}
	for _, scope := range []Scope{ScopeProject, ScopeUser} {
		lines = append(lines, strings.Title(string(scope))+":")
		wrote := false
		for _, skill := range skills {
			if skill.Scope != scope {
				continue
			}
			wrote = true
			detail := skill.Name
			if skill.Description != "" {
				detail += " · " + skill.Description
			}
			if skill.Origin == OriginCommandsDir {
				detail += " · command"
			}
			if skill.ShadowedBy != nil {
				lines = append(lines, fmt.Sprintf("  (shadowed by %s) %s", *skill.ShadowedBy, detail))
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

func RenderReportJSON(skills []Summary) string {
	activeCount := 0
	for _, skill := range skills {
		if skill.ShadowedBy == nil {
			activeCount++
		}
	}
	payload := map[string]any{
		"kind": "skills",
		"summary": map[string]any{
			"total":    len(skills),
			"active":   activeCount,
			"shadowed": len(skills) - activeCount,
		},
		"skills": skills,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return string(data)
}

func RenderInstall(result InstallResult) string {
	lines := []string{
		"Skills",
		"  Result           installed " + result.InvocationName,
		"  Invoke as        $" + result.InvocationName,
	}
	if strings.TrimSpace(result.DisplayName) != "" {
		lines = append(lines, "  Display name     "+result.DisplayName)
	}
	lines = append(lines,
		"  Source           "+result.Source,
		"  Registry         "+result.RegistryRoot,
		"  Installed path   "+result.InstalledPath,
	)
	return strings.Join(lines, "\n")
}

func loadEntry(root Root, entry os.DirEntry) (Summary, bool, error) {
	switch root.Origin {
	case OriginSkillsDir:
		if !entry.IsDir() {
			return Summary{}, false, nil
		}
		skillPath := filepath.Join(root.Path, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillPath); err != nil {
			return Summary{}, false, nil
		}
		contents, err := os.ReadFile(skillPath)
		if err != nil {
			return Summary{}, false, err
		}
		name, description := parseFrontmatter(string(contents))
		if name == "" {
			name = entry.Name()
		}
		return Summary{Name: name, Description: description, Scope: root.Scope, Origin: root.Origin, Path: skillPath}, true, nil
	case OriginCommandsDir:
		path := filepath.Join(root.Path, entry.Name())
		skillPath := path
		info, err := os.Stat(path)
		if err != nil {
			return Summary{}, false, err
		}
		if info.IsDir() {
			skillPath = filepath.Join(path, "SKILL.md")
		} else if !strings.EqualFold(filepath.Ext(path), ".md") {
			return Summary{}, false, nil
		}
		if _, err := os.Stat(skillPath); err != nil {
			return Summary{}, false, nil
		}
		contents, err := os.ReadFile(skillPath)
		if err != nil {
			return Summary{}, false, err
		}
		name, description := parseFrontmatter(string(contents))
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(skillPath), filepath.Ext(skillPath))
		}
		return Summary{Name: name, Description: description, Scope: root.Scope, Origin: root.Origin, Path: skillPath}, true, nil
	default:
		return Summary{}, false, nil
	}
}

func resolveInstallSource(root, source string) (string, string, error) {
	candidate := source
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		skillPath := filepath.Join(absolute, "SKILL.md")
		if _, err := os.Stat(skillPath); err != nil {
			return "", "", fmt.Errorf("skill directory %q must contain SKILL.md", absolute)
		}
		return absolute, skillPath, nil
	}
	if strings.EqualFold(filepath.Ext(absolute), ".md") {
		return absolute, absolute, nil
	}
	return "", "", fmt.Errorf("skill source %q must be a directory with SKILL.md or a markdown file", absolute)
}

func parseFrontmatter(contents string) (name string, description string) {
	lines := strings.Split(contents, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		if value, ok := strings.CutPrefix(trimmed, "name:"); ok {
			name = unquote(value)
			continue
		}
		if value, ok := strings.CutPrefix(trimmed, "description:"); ok {
			description = unquote(value)
		}
	}
	return strings.TrimSpace(name), strings.TrimSpace(description)
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return strings.TrimSpace(value)
}

func sanitizeInvocationName(candidate string) string {
	candidate = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(candidate, "/"), "$"))
	if candidate == "" {
		return ""
	}
	var b strings.Builder
	lastSeparator := false
	for _, ch := range candidate {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
			b.WriteByte(byte(strings.ToLower(string(ch))[0]))
			lastSeparator = false
			continue
		}
		if !lastSeparator && b.Len() > 0 {
			b.WriteByte('-')
			lastSeparator = true
		}
	}
	return strings.Trim(b.String(), "-_.")
}

func fallbackInstallName(source string) string {
	base := filepath.Base(source)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func copyDirContents(source, destination string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(source, entry.Name())
		dstPath := filepath.Join(destination, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				return err
			}
			if err := copyDirContents(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
