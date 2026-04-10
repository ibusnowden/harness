package status

import (
	"encoding/json"
	"strconv"
	"strings"

	"ascaris/internal/commands"
	"ascaris/internal/config"
	"ascaris/internal/manifest"
	"ascaris/internal/sessions"
	"ascaris/internal/tools"
	"ascaris/internal/version"
)

type Report struct {
	Product      string `json:"product"`
	Version      string `json:"version"`
	Root         string `json:"root"`
	ConfigHome   string `json:"config_home"`
	SessionDir   string `json:"session_dir"`
	Model        string `json:"model"`
	Permission   string `json:"permission_mode"`
	ConfigFiles  int    `json:"loaded_config_files"`
	GoFiles      int    `json:"go_files"`
	CommandCount int    `json:"command_count"`
	ToolCount    int    `json:"tool_count"`
	OAuth        string `json:"oauth"`
}

func Build(root string) (Report, error) {
	m, err := manifest.Build(root)
	if err != nil {
		return Report{}, err
	}
	runtimeConfig, err := config.Load(root)
	if err != nil {
		return Report{}, err
	}
	commandEntries, err := commands.Catalog(root)
	if err != nil {
		return Report{}, err
	}
	toolEntries, err := tools.Catalog(root, tools.CatalogOptions{IncludeMCP: true})
	if err != nil {
		return Report{}, err
	}
	oauthStatus := "disabled"
	if runtimeConfig.OAuth() != nil {
		oauthStatus = "configured"
	}
	return Report{
		Product:      version.Product,
		Version:      version.Version,
		Root:         root,
		ConfigHome:   sessions.ConfigHome(root),
		SessionDir:   sessions.SessionDir(root),
		Model:        runtimeConfig.Model(),
		Permission:   runtimeConfig.PermissionMode(),
		ConfigFiles:  len(runtimeConfig.LoadedEntries()),
		GoFiles:      m.TotalGoFiles,
		CommandCount: len(commandEntries),
		ToolCount:    len(toolEntries),
		OAuth:        oauthStatus,
	}, nil
}

func (r Report) Text() string {
	lines := []string{
		"Product: " + r.Product,
		"Version: " + r.Version,
		"Root: " + r.Root,
		"Config home: " + r.ConfigHome,
		"Session dir: " + r.SessionDir,
		"Model: " + valueOrUnknown(r.Model),
		"Permission mode: " + valueOrUnknown(r.Permission),
		"OAuth: " + valueOrUnknown(r.OAuth),
		"Loaded config files: " + strconv.Itoa(r.ConfigFiles),
		"Go files: " + strconv.Itoa(r.GoFiles),
		"Commands: " + strconv.Itoa(r.CommandCount),
		"Tools: " + strconv.Itoa(r.ToolCount),
	}
	return strings.Join(lines, "\n")
}

func (r Report) JSON() string {
	data, _ := json.MarshalIndent(r, "", "  ")
	return string(data)
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}
