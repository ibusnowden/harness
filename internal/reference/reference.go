package reference

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

//go:embed data/*.json data/subsystems/*.json
var fsys embed.FS

type ArchiveSurfaceSnapshot struct {
	ArchiveRoot       string   `json:"archive_root"`
	RootFiles         []string `json:"root_files"`
	RootDirs          []string `json:"root_dirs"`
	TotalTSLikeFiles  int      `json:"total_ts_like_files"`
	CommandEntryCount int      `json:"command_entry_count"`
	ToolEntryCount    int      `json:"tool_entry_count"`
}

type SnapshotEntry struct {
	Name           string `json:"name"`
	SourceHint     string `json:"source_hint"`
	Responsibility string `json:"responsibility"`
}

type SubsystemSnapshot struct {
	ArchiveName string   `json:"archive_name"`
	PackageName string   `json:"package_name"`
	ModuleCount int      `json:"module_count"`
	SampleFiles []string `json:"sample_files"`
}

func decodeJSON(name string, out any) error {
	data, err := fsys.ReadFile(name)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func LoadArchiveSurface() (ArchiveSurfaceSnapshot, error) {
	var snapshot ArchiveSurfaceSnapshot
	err := decodeJSON("data/archive_surface_snapshot.json", &snapshot)
	return snapshot, err
}

func LoadCommandSnapshot() ([]SnapshotEntry, error) {
	var entries []SnapshotEntry
	err := decodeJSON("data/commands_snapshot.json", &entries)
	return entries, err
}

func LoadToolSnapshot() ([]SnapshotEntry, error) {
	var entries []SnapshotEntry
	err := decodeJSON("data/tools_snapshot.json", &entries)
	return entries, err
}

func LoadSubsystemSnapshot(name string) (SubsystemSnapshot, error) {
	var snapshot SubsystemSnapshot
	clean := strings.TrimSpace(name)
	if clean == "" {
		return snapshot, fmt.Errorf("subsystem name is required")
	}
	err := decodeJSON(path.Join("data", "subsystems", clean+".json"), &snapshot)
	return snapshot, err
}
