package migrations

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ascaris/internal/config"
	"ascaris/internal/sessions"
)

type Change struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

type Report struct {
	Changes []Change `json:"changes"`
}

func MigrateLegacy(root string) (Report, error) {
	configHome := config.ConfigHome(root)
	report := Report{}
	copyIfExists := func(source, destination string) error {
		info, err := os.Stat(source)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			if err := copyDir(source, destination); err != nil {
				return err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return err
			}
			data, err := os.ReadFile(source)
			if err != nil {
				return err
			}
			if strings.EqualFold(filepath.Base(source), "plugin.json") && strings.Contains(source, ".claude-plugin") {
				destination = filepath.Join(filepath.Dir(filepath.Dir(destination)), ".ascaris-plugin", "plugin.json")
				if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
					return err
				}
			}
			if err := os.WriteFile(destination, data, info.Mode()); err != nil {
				return err
			}
		}
		report.Changes = append(report.Changes, Change{Source: source, Destination: destination})
		return nil
	}

	for _, pair := range [][2]string{
		{filepath.Join(root, ".claw.json"), filepath.Join(root, ".ascaris.json")},
		{filepath.Join(root, ".claw", "settings.json"), filepath.Join(root, ".ascaris", "settings.json")},
		{filepath.Join(root, ".claw", "settings.local.json"), filepath.Join(root, ".ascaris", "settings.local.json")},
		{filepath.Join(root, ".claw", "worker-state.json"), filepath.Join(root, ".ascaris", "worker-state.json")},
		{filepath.Join(root, ".claw", "skills"), filepath.Join(root, ".ascaris", "skills")},
		{filepath.Join(root, ".omc", "skills"), filepath.Join(root, ".ascaris", "skills")},
		{filepath.Join(root, ".codex", "skills"), filepath.Join(root, ".ascaris", "skills")},
		{filepath.Join(root, ".claude", "skills"), filepath.Join(root, ".ascaris", "skills")},
		{filepath.Join(root, ".claude", "skills", "omc-learned"), filepath.Join(root, ".ascaris", "skills")},
		{filepath.Join(root, ".claw", "commands"), filepath.Join(root, ".ascaris", "commands")},
		{filepath.Join(root, ".codex", "commands"), filepath.Join(root, ".ascaris", "commands")},
		{filepath.Join(root, ".claude", "commands"), filepath.Join(root, ".ascaris", "commands")},
		{filepath.Join(root, ".claw", "agents"), filepath.Join(root, ".ascaris", "agents")},
		{filepath.Join(root, ".codex", "agents"), filepath.Join(root, ".ascaris", "agents")},
		{filepath.Join(root, ".claude", "agents"), filepath.Join(root, ".ascaris", "agents")},
		{filepath.Join(filepath.Dir(configHome), ".claw.json"), filepath.Join(filepath.Dir(configHome), ".ascaris.json")},
		{filepath.Join(configHome, "..", ".codex", "skills"), filepath.Join(configHome, "skills")},
	} {
		if err := copyIfExists(pair[0], pair[1]); err != nil {
			return Report{}, err
		}
	}
	if err := migrateLegacySessions(root, &report, copyIfExists); err != nil {
		return Report{}, err
	}

	pluginRoots := []string{
		root,
		filepath.Join(root, "plugins"),
		filepath.Join(root, ".plugins"),
	}
	for _, pluginRoot := range pluginRoots {
		_ = filepath.WalkDir(pluginRoot, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry == nil || entry.IsDir() || entry.Name() != "plugin.json" {
				return nil
			}
			if filepath.Base(filepath.Dir(path)) != ".claude-plugin" {
				return nil
			}
			destination := filepath.Join(filepath.Dir(filepath.Dir(path)), ".ascaris-plugin", "plugin.json")
			return copyIfExists(path, destination)
		})
	}

	sort.Slice(report.Changes, func(i, j int) bool {
		return report.Changes[i].Destination < report.Changes[j].Destination
	})
	return report, nil
}

func migrateLegacySessions(root string, report *Report, copyIfExists func(source, destination string) error) error {
	sourceDir := filepath.Join(root, ".claw", "sessions")
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var requestedLatest string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		sourcePath := filepath.Join(sourceDir, entry.Name())
		switch {
		case entry.Name() == "latest":
			data, err := os.ReadFile(sourcePath)
			if err != nil {
				return err
			}
			requestedLatest = strings.TrimSpace(string(data))
		case strings.EqualFold(filepath.Ext(entry.Name()), ".json"):
			data, err := os.ReadFile(sourcePath)
			if err != nil {
				return err
			}
			var session sessions.StoredSession
			if err := json.Unmarshal(data, &session); err != nil {
				return err
			}
			if strings.TrimSpace(session.SessionID) == "" {
				return fmt.Errorf("legacy session %s is missing session_id", sourcePath)
			}
			destination, err := sessions.Save(session, root)
			if err != nil {
				return err
			}
			report.Changes = append(report.Changes, Change{Source: sourcePath, Destination: destination})
		case strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl"):
			destination := filepath.Join(sessions.LegacySessionDir(root), entry.Name())
			if err := copyIfExists(sourcePath, destination); err != nil {
				return err
			}
		}
	}
	if requestedLatest != "" {
		summary, err := sessions.Switch(root, requestedLatest)
		if err != nil {
			return err
		}
		report.Changes = append(report.Changes, Change{
			Source:      filepath.Join(sourceDir, "latest"),
			Destination: filepath.Join(filepath.Dir(summary.Path), "latest"),
		})
	}
	return nil
}

func (r Report) Text() string {
	if len(r.Changes) == 0 {
		return "Legacy migration\n  No legacy state found."
	}
	lines := []string{"Legacy migration"}
	for _, change := range r.Changes {
		lines = append(lines, fmt.Sprintf("  %s -> %s", change.Source, change.Destination))
	}
	return strings.Join(lines, "\n")
}

func (r Report) JSON() string {
	data, _ := json.MarshalIndent(r, "", "  ")
	return string(data)
}

func copyDir(source, destination string) error {
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
			if err := os.MkdirAll(dstPath, info.Mode()); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}
