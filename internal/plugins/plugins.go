package plugins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"ascaris/internal/config"
)

const ManifestRelativePath = ".ascaris-plugin/plugin.json"

type Kind string

const (
	KindBuiltin  Kind = "builtin"
	KindBundled  Kind = "bundled"
	KindExternal Kind = "external"
)

type Permission string

const (
	PermissionRead    Permission = "read"
	PermissionWrite   Permission = "write"
	PermissionExecute Permission = "execute"
)

type ToolPermission string

const (
	ToolPermissionReadOnly         ToolPermission = "read-only"
	ToolPermissionWorkspaceWrite   ToolPermission = "workspace-write"
	ToolPermissionDangerFullAccess ToolPermission = "danger-full-access"
)

type Hooks struct {
	PreToolUse         []string `json:"PreToolUse,omitempty"`
	PostToolUse        []string `json:"PostToolUse,omitempty"`
	PostToolUseFailure []string `json:"PostToolUseFailure,omitempty"`
}

func (h Hooks) Merge(other Hooks) Hooks {
	return Hooks{
		PreToolUse:         append(append([]string{}, h.PreToolUse...), other.PreToolUse...),
		PostToolUse:        append(append([]string{}, h.PostToolUse...), other.PostToolUse...),
		PostToolUseFailure: append(append([]string{}, h.PostToolUseFailure...), other.PostToolUseFailure...),
	}
}

type Lifecycle struct {
	Init     []string `json:"Init,omitempty"`
	Shutdown []string `json:"Shutdown,omitempty"`
}

type ToolManifest struct {
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	InputSchema        json.RawMessage `json:"inputSchema"`
	Command            string          `json:"command"`
	Args               []string        `json:"args,omitempty"`
	RequiredPermission ToolPermission  `json:"required_permission"`
}

type CommandManifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Command     string `json:"command"`
}

type Manifest struct {
	Name           string            `json:"name"`
	Version        string            `json:"version"`
	Description    string            `json:"description"`
	Permissions    []Permission      `json:"permissions"`
	DefaultEnabled bool              `json:"defaultEnabled,omitempty"`
	Hooks          Hooks             `json:"hooks,omitempty"`
	Lifecycle      Lifecycle         `json:"lifecycle,omitempty"`
	Tools          []ToolManifest    `json:"tools,omitempty"`
	Commands       []CommandManifest `json:"commands,omitempty"`
}

type Metadata struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	Description    string `json:"description"`
	Kind           Kind   `json:"kind"`
	Source         string `json:"source"`
	DefaultEnabled bool   `json:"default_enabled"`
	Root           string `json:"root,omitempty"`
}

type Definition struct {
	Metadata Metadata `json:"metadata"`
	Manifest Manifest `json:"manifest"`
}

type Summary struct {
	Metadata Metadata `json:"metadata"`
	Enabled  bool     `json:"enabled"`
}

type ToolDefinition struct {
	PluginID           string          `json:"plugin_id"`
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	InputSchema        json.RawMessage `json:"input_schema"`
	Command            string          `json:"command"`
	Args               []string        `json:"args,omitempty"`
	RequiredPermission ToolPermission  `json:"required_permission"`
	Root               string          `json:"root"`
}

type CommandDefinition struct {
	PluginID     string `json:"plugin_id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Command      string `json:"command"`
	PluginSource string `json:"plugin_source"`
}

type InstallOutcome struct {
	PluginID    string `json:"plugin_id"`
	Version     string `json:"version"`
	InstallPath string `json:"install_path"`
}

type UpdateOutcome struct {
	PluginID    string `json:"plugin_id"`
	OldVersion  string `json:"old_version"`
	NewVersion  string `json:"new_version"`
	InstallPath string `json:"install_path"`
}

type ManagerConfig struct {
	ConfigHome     string
	EnabledPlugins map[string]bool
	ExternalDirs   []string
	InstallRoot    string
	RegistryPath   string
	BundledRoot    string
}

type Manager struct {
	config ManagerConfig
}

type registryRecord struct {
	Kind              Kind   `json:"kind"`
	ID                string `json:"id"`
	Name              string `json:"name"`
	Version           string `json:"version"`
	Description       string `json:"description"`
	InstallPath       string `json:"install_path"`
	SourcePath        string `json:"source_path"`
	InstalledAtUnixMS int64  `json:"installed_at_unix_ms"`
	UpdatedAtUnixMS   int64  `json:"updated_at_unix_ms"`
}

type installedRegistry struct {
	Plugins map[string]registryRecord `json:"plugins"`
}

func NewManager(root string, runtimeConfig config.RuntimeConfig) Manager {
	settings := runtimeConfig.PluginSettings()
	cfg := ManagerConfig{
		ConfigHome:     config.ConfigHome(root),
		EnabledPlugins: settings.Enabled,
		ExternalDirs:   settings.ExternalDirs,
		InstallRoot:    settings.InstallRoot,
		RegistryPath:   settings.RegistryPath,
		BundledRoot:    settings.BundledRoot,
	}
	return NewManagerWithConfig(cfg)
}

func NewManagerWithConfig(cfg ManagerConfig) Manager {
	return Manager{config: cfg}
}

func (m Manager) InstallRoot() string {
	if strings.TrimSpace(m.config.InstallRoot) != "" {
		return m.config.InstallRoot
	}
	return filepath.Join(m.config.ConfigHome, "plugins", "installed")
}

func (m Manager) RegistryPath() string {
	if strings.TrimSpace(m.config.RegistryPath) != "" {
		return m.config.RegistryPath
	}
	return filepath.Join(m.config.ConfigHome, "plugins", "installed.json")
}

func (m Manager) SettingsPath() string {
	return filepath.Join(m.config.ConfigHome, "settings.json")
}

func (m Manager) BundledRoot() string {
	return strings.TrimSpace(m.config.BundledRoot)
}

func (m Manager) ListInstalledPlugins() ([]Summary, error) {
	return m.listPlugins(true)
}

func (m Manager) ListPlugins() ([]Summary, error) {
	return m.listPlugins(false)
}

func (m Manager) AggregatedHooks() (Hooks, error) {
	definitions, err := m.enabledDefinitions()
	if err != nil {
		return Hooks{}, err
	}
	merged := Hooks{}
	for _, definition := range definitions {
		merged = merged.Merge(definition.Manifest.Hooks)
	}
	return merged, nil
}

func (m Manager) AggregatedTools() ([]ToolDefinition, error) {
	definitions, err := m.enabledDefinitions()
	if err != nil {
		return nil, err
	}
	var tools []ToolDefinition
	for _, definition := range definitions {
		for _, tool := range definition.Manifest.Tools {
			tools = append(tools, ToolDefinition{
				PluginID:           definition.Metadata.ID,
				Name:               tool.Name,
				Description:        tool.Description,
				InputSchema:        cloneJSON(tool.InputSchema),
				Command:            tool.Command,
				Args:               append([]string{}, tool.Args...),
				RequiredPermission: tool.RequiredPermission,
				Root:               definition.Metadata.Root,
			})
		}
	}
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].PluginID == tools[j].PluginID {
			return tools[i].Name < tools[j].Name
		}
		return tools[i].PluginID < tools[j].PluginID
	})
	return tools, nil
}

func (m Manager) AggregatedCommands() ([]CommandDefinition, error) {
	definitions, err := m.enabledDefinitions()
	if err != nil {
		return nil, err
	}
	var commands []CommandDefinition
	for _, definition := range definitions {
		for _, command := range definition.Manifest.Commands {
			commands = append(commands, CommandDefinition{
				PluginID:     definition.Metadata.ID,
				Name:         command.Name,
				Description:  command.Description,
				Command:      command.Command,
				PluginSource: definition.Metadata.Source,
			})
		}
	}
	return commands, nil
}

func (m *Manager) Install(source string) (InstallOutcome, error) {
	root, manifest, err := loadManifestFromSource(source)
	if err != nil {
		return InstallOutcome{}, err
	}
	pluginID := pluginID(manifest.Name, KindExternal)
	installPath := filepath.Join(m.InstallRoot(), sanitizePathComponent(pluginID))
	if err := os.MkdirAll(filepath.Dir(installPath), 0o755); err != nil {
		return InstallOutcome{}, err
	}
	if _, err := os.Stat(installPath); err == nil {
		if err := os.RemoveAll(installPath); err != nil {
			return InstallOutcome{}, err
		}
	}
	if err := os.MkdirAll(installPath, 0o755); err != nil {
		return InstallOutcome{}, err
	}
	if err := copyDir(root, installPath); err != nil {
		return InstallOutcome{}, err
	}
	registry, err := m.loadRegistry()
	if err != nil {
		return InstallOutcome{}, err
	}
	now := time.Now().UnixMilli()
	registry.Plugins[pluginID] = registryRecord{
		Kind:              KindExternal,
		ID:                pluginID,
		Name:              manifest.Name,
		Version:           manifest.Version,
		Description:       manifest.Description,
		InstallPath:       installPath,
		SourcePath:        root,
		InstalledAtUnixMS: now,
		UpdatedAtUnixMS:   now,
	}
	if err := m.storeRegistry(registry); err != nil {
		return InstallOutcome{}, err
	}
	if err := m.writeEnabledState(pluginID, true); err != nil {
		return InstallOutcome{}, err
	}
	return InstallOutcome{PluginID: pluginID, Version: manifest.Version, InstallPath: installPath}, nil
}

func (m *Manager) Enable(pluginID string) error {
	if _, err := m.resolvePlugin(pluginID); err != nil {
		return err
	}
	return m.writeEnabledState(pluginID, true)
}

func (m *Manager) Disable(pluginID string) error {
	if _, err := m.resolvePlugin(pluginID); err != nil {
		return err
	}
	return m.writeEnabledState(pluginID, false)
}

func (m *Manager) Uninstall(pluginID string) error {
	registry, err := m.loadRegistry()
	if err != nil {
		return err
	}
	record, ok := registry.Plugins[pluginID]
	if !ok {
		return fmt.Errorf("plugin %q is not installed", pluginID)
	}
	if record.Kind == KindBundled {
		return fmt.Errorf("plugin %q is bundled and managed automatically; disable it instead", pluginID)
	}
	if err := os.RemoveAll(record.InstallPath); err != nil {
		return err
	}
	delete(registry.Plugins, pluginID)
	if err := m.storeRegistry(registry); err != nil {
		return err
	}
	return m.clearEnabledState(pluginID)
}

func (m *Manager) Update(pluginID string) (UpdateOutcome, error) {
	registry, err := m.loadRegistry()
	if err != nil {
		return UpdateOutcome{}, err
	}
	record, ok := registry.Plugins[pluginID]
	if !ok {
		return UpdateOutcome{}, fmt.Errorf("plugin %q is not installed", pluginID)
	}
	root, manifest, err := loadManifestFromSource(record.SourcePath)
	if err != nil {
		return UpdateOutcome{}, err
	}
	if err := os.RemoveAll(record.InstallPath); err != nil {
		return UpdateOutcome{}, err
	}
	if err := os.MkdirAll(record.InstallPath, 0o755); err != nil {
		return UpdateOutcome{}, err
	}
	if err := copyDir(root, record.InstallPath); err != nil {
		return UpdateOutcome{}, err
	}
	oldVersion := record.Version
	record.Version = manifest.Version
	record.Name = manifest.Name
	record.Description = manifest.Description
	record.UpdatedAtUnixMS = time.Now().UnixMilli()
	registry.Plugins[pluginID] = record
	if err := m.storeRegistry(registry); err != nil {
		return UpdateOutcome{}, err
	}
	return UpdateOutcome{
		PluginID:    pluginID,
		OldVersion:  oldVersion,
		NewVersion:  manifest.Version,
		InstallPath: record.InstallPath,
	}, nil
}

func (m Manager) ExecuteTool(tool ToolDefinition, input json.RawMessage) (string, error) {
	args := append([]string{}, tool.Args...)
	cmd := exec.Command(tool.Command, args...)
	cmd.Dir = tool.Root
	cmd.Stdin = bytes.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "", err
	}
	if stderr.Len() > 0 && stdout.Len() == 0 {
		return strings.TrimSpace(stderr.String()), nil
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (m Manager) RunInit() error {
	return m.runLifecycle("init")
}

func (m Manager) RunShutdown() error {
	return m.runLifecycle("shutdown")
}

func (m Manager) runLifecycle(stage string) error {
	definitions, err := m.enabledDefinitions()
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		var commands []string
		switch stage {
		case "init":
			commands = definition.Manifest.Lifecycle.Init
		case "shutdown":
			commands = definition.Manifest.Lifecycle.Shutdown
		}
		for _, command := range commands {
			cmd := shellCommand(command)
			cmd.Dir = definition.Metadata.Root
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("plugin %s %s failed: %w", definition.Metadata.ID, stage, err)
			}
		}
	}
	return nil
}

func (m Manager) listPlugins(installedOnly bool) ([]Summary, error) {
	definitions, err := m.definitions(installedOnly)
	if err != nil {
		return nil, err
	}
	summaries := make([]Summary, 0, len(definitions))
	for _, definition := range definitions {
		summaries = append(summaries, Summary{
			Metadata: definition.Metadata,
			Enabled:  m.isEnabled(definition.Metadata),
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Metadata.ID < summaries[j].Metadata.ID
	})
	return summaries, nil
}

func (m Manager) enabledDefinitions() ([]Definition, error) {
	definitions, err := m.definitions(false)
	if err != nil {
		return nil, err
	}
	out := make([]Definition, 0, len(definitions))
	for _, definition := range definitions {
		if m.isEnabled(definition.Metadata) {
			out = append(out, definition)
		}
	}
	return out, nil
}

func (m Manager) resolvePlugin(target string) (Summary, error) {
	summaries, err := m.ListPlugins()
	if err != nil {
		return Summary{}, err
	}
	var matches []Summary
	for _, summary := range summaries {
		if summary.Metadata.ID == target || summary.Metadata.Name == target {
			matches = append(matches, summary)
		}
	}
	switch len(matches) {
	case 0:
		return Summary{}, fmt.Errorf("plugin %q is not installed or discoverable", target)
	case 1:
		return matches[0], nil
	default:
		return Summary{}, fmt.Errorf("plugin name %q is ambiguous; use the full plugin id", target)
	}
}

func (m Manager) definitions(installedOnly bool) ([]Definition, error) {
	registry, err := m.loadRegistry()
	if err != nil {
		return nil, err
	}
	var definitions []Definition
	seen := map[string]struct{}{}
	for _, record := range registry.Plugins {
		definition, err := loadDefinition(record.InstallPath, record.Kind, record.SourcePath)
		if err != nil {
			continue
		}
		definitions = append(definitions, definition)
		seen[definition.Metadata.ID] = struct{}{}
	}
	if !installedOnly {
		for _, directory := range m.config.ExternalDirs {
			entries, err := os.ReadDir(directory)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				root := filepath.Join(directory, entry.Name())
				definition, err := loadDefinition(root, KindExternal, root)
				if err != nil {
					continue
				}
				if _, ok := seen[definition.Metadata.ID]; ok {
					continue
				}
				definitions = append(definitions, definition)
				seen[definition.Metadata.ID] = struct{}{}
			}
		}
		if bundledRoot := m.BundledRoot(); bundledRoot != "" {
			entries, err := os.ReadDir(bundledRoot)
			if err == nil {
				for _, entry := range entries {
					if !entry.IsDir() {
						continue
					}
					root := filepath.Join(bundledRoot, entry.Name())
					definition, err := loadDefinition(root, KindBundled, root)
					if err != nil {
						continue
					}
					if _, ok := seen[definition.Metadata.ID]; ok {
						continue
					}
					definitions = append(definitions, definition)
					seen[definition.Metadata.ID] = struct{}{}
				}
			}
		}
	}
	sort.Slice(definitions, func(i, j int) bool {
		return definitions[i].Metadata.ID < definitions[j].Metadata.ID
	})
	return definitions, nil
}

func (m Manager) isEnabled(metadata Metadata) bool {
	if enabled, ok := m.config.EnabledPlugins[metadata.ID]; ok {
		return enabled
	}
	if enabledPlugins := m.loadEnabledOverrides(); enabledPlugins != nil {
		if enabled, ok := enabledPlugins[metadata.ID]; ok {
			return enabled
		}
	}
	switch metadata.Kind {
	case KindBuiltin, KindBundled:
		return metadata.DefaultEnabled
	default:
		return metadata.DefaultEnabled
	}
}

func (m Manager) loadRegistry() (installedRegistry, error) {
	path := m.RegistryPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return installedRegistry{Plugins: map[string]registryRecord{}}, nil
		}
		return installedRegistry{}, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return installedRegistry{Plugins: map[string]registryRecord{}}, nil
	}
	var registry installedRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return installedRegistry{}, err
	}
	if registry.Plugins == nil {
		registry.Plugins = map[string]registryRecord{}
	}
	return registry, nil
}

func (m Manager) loadEnabledOverrides() map[string]bool {
	data, err := os.ReadFile(m.SettingsPath())
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil
	}
	root, ok := settings["enabledPlugins"].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]bool, len(root))
	for key, value := range root {
		if enabled, ok := value.(bool); ok {
			out[key] = enabled
		}
	}
	return out
}

func (m Manager) storeRegistry(registry installedRegistry) error {
	if registry.Plugins == nil {
		registry.Plugins = map[string]registryRecord{}
	}
	path := m.RegistryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (m *Manager) writeEnabledState(pluginID string, enabled bool) error {
	if m.config.EnabledPlugins == nil {
		m.config.EnabledPlugins = map[string]bool{}
	}
	settings := map[string]any{}
	if data, err := os.ReadFile(m.SettingsPath()); err == nil && strings.TrimSpace(string(data)) != "" {
		_ = json.Unmarshal(data, &settings)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	enabledPlugins, _ := settings["enabledPlugins"].(map[string]any)
	if enabledPlugins == nil {
		enabledPlugins = map[string]any{}
	}
	enabledPlugins[pluginID] = enabled
	settings["enabledPlugins"] = enabledPlugins
	if err := os.MkdirAll(filepath.Dir(m.SettingsPath()), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	m.config.EnabledPlugins[pluginID] = enabled
	return os.WriteFile(m.SettingsPath(), data, 0o644)
}

func (m *Manager) clearEnabledState(pluginID string) error {
	if m.config.EnabledPlugins == nil {
		m.config.EnabledPlugins = map[string]bool{}
	}
	settings := map[string]any{}
	if data, err := os.ReadFile(m.SettingsPath()); err == nil && strings.TrimSpace(string(data)) != "" {
		_ = json.Unmarshal(data, &settings)
	}
	if settings != nil {
		if enabledPlugins, ok := settings["enabledPlugins"].(map[string]any); ok {
			delete(enabledPlugins, pluginID)
			settings["enabledPlugins"] = enabledPlugins
		}
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	delete(m.config.EnabledPlugins, pluginID)
	if err := os.MkdirAll(filepath.Dir(m.SettingsPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(m.SettingsPath(), data, 0o644)
}

func loadManifestFromSource(source string) (string, Manifest, error) {
	root := source
	if !filepath.IsAbs(root) {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return "", Manifest{}, err
		}
		root = absolute
	}
	return loadManifestFromDirectory(root)
}

func loadManifestFromDirectory(root string) (string, Manifest, error) {
	manifestPath := filepath.Join(root, ManifestRelativePath)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", Manifest{}, err
	}
	if err := validateManifest(root, manifest); err != nil {
		return "", Manifest{}, err
	}
	return root, manifest, nil
}

func loadDefinition(root string, kind Kind, source string) (Definition, error) {
	root, manifest, err := loadManifestFromDirectory(root)
	if err != nil {
		return Definition{}, err
	}
	return Definition{
		Metadata: Metadata{
			ID:             pluginID(manifest.Name, kind),
			Name:           manifest.Name,
			Version:        manifest.Version,
			Description:    manifest.Description,
			Kind:           kind,
			Source:         source,
			DefaultEnabled: manifest.DefaultEnabled,
			Root:           root,
		},
		Manifest: manifest,
	}, nil
}

func validateManifest(root string, manifest Manifest) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"name", manifest.Name},
		{"version", manifest.Version},
		{"description", manifest.Description},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("plugin manifest %s cannot be empty", field.name)
		}
	}
	seenPermissions := map[Permission]struct{}{}
	for _, permission := range manifest.Permissions {
		switch permission {
		case PermissionRead, PermissionWrite, PermissionExecute:
		default:
			return fmt.Errorf("plugin manifest permission %q must be one of read, write, or execute", permission)
		}
		if _, ok := seenPermissions[permission]; ok {
			return fmt.Errorf("plugin manifest permission %q is duplicated", permission)
		}
		seenPermissions[permission] = struct{}{}
	}
	seenTools := map[string]struct{}{}
	for _, tool := range manifest.Tools {
		if strings.TrimSpace(tool.Name) == "" || strings.TrimSpace(tool.Description) == "" || strings.TrimSpace(tool.Command) == "" {
			return fmt.Errorf("plugin tool fields cannot be empty")
		}
		if _, ok := seenTools[tool.Name]; ok {
			return fmt.Errorf("plugin tool %q is duplicated", tool.Name)
		}
		seenTools[tool.Name] = struct{}{}
		if !validJSONObject(tool.InputSchema) {
			return fmt.Errorf("plugin tool %q inputSchema must be a JSON object", tool.Name)
		}
		switch tool.RequiredPermission {
		case ToolPermissionReadOnly, ToolPermissionWorkspaceWrite, ToolPermissionDangerFullAccess:
		default:
			return fmt.Errorf("plugin tool %q required_permission %q must be read-only, workspace-write, or danger-full-access", tool.Name, tool.RequiredPermission)
		}
		if !filepath.IsAbs(tool.Command) {
			commandPath := filepath.Join(root, tool.Command)
			if _, err := os.Stat(commandPath); err != nil {
				if _, lookErr := exec.LookPath(tool.Command); lookErr != nil {
					return fmt.Errorf("plugin tool path %q does not exist", tool.Command)
				}
			}
		}
	}
	seenCommands := map[string]struct{}{}
	for _, command := range manifest.Commands {
		if strings.TrimSpace(command.Name) == "" || strings.TrimSpace(command.Description) == "" || strings.TrimSpace(command.Command) == "" {
			return fmt.Errorf("plugin command fields cannot be empty")
		}
		if _, ok := seenCommands[command.Name]; ok {
			return fmt.Errorf("plugin command %q is duplicated", command.Name)
		}
		seenCommands[command.Name] = struct{}{}
	}
	return nil
}

func validJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	_, ok := value.(map[string]any)
	return ok
}

func pluginID(name string, kind Kind) string {
	return string(kind) + "/" + sanitizePathComponent(name)
}

func sanitizePathComponent(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var b strings.Builder
	lastSeparator := false
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b.WriteRune(ch)
			lastSeparator = false
			continue
		}
		if !lastSeparator && b.Len() > 0 {
			b.WriteByte('-')
			lastSeparator = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-lc", command)
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
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
		if err := os.WriteFile(dstPath, data, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}
