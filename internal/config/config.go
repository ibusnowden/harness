package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Source string

const (
	SourceUser    Source = "user"
	SourceProject Source = "project"
	SourceLocal   Source = "local"
)

type Entry struct {
	Source Source `json:"source"`
	Path   string `json:"path"`
}

type RuntimeConfig struct {
	merged        map[string]any
	loadedEntries []Entry
}

type ProviderSettings struct {
	AnthropicBaseURL  string
	OpenAIBaseURL     string
	OpenRouterBaseURL string
	Kind              string
	XAIBaseURL        string
	ProxyURL          string
}

type PluginSettings struct {
	MaxOutputTokens int
	Enabled         map[string]bool
	ExternalDirs    []string
	InstallRoot     string
	RegistryPath    string
	BundledRoot     string
}

func ConfigHome(root string) string {
	if override := os.Getenv("ASCARIS_CONFIG_HOME"); strings.TrimSpace(override) != "" {
		return override
	}
	return filepath.Join(root, ".ascaris")
}

type Loader struct {
	cwd        string
	configHome string
}

func NewLoader(cwd, configHome string) Loader {
	return Loader{cwd: cwd, configHome: configHome}
}

func DefaultLoader(root string) Loader {
	return Loader{
		cwd:        root,
		configHome: ConfigHome(root),
	}
}

func (l Loader) Discover() []Entry {
	userLegacyPath := filepath.Join(filepath.Dir(l.configHome), ".ascaris.json")
	return []Entry{
		{Source: SourceUser, Path: userLegacyPath},
		{Source: SourceUser, Path: filepath.Join(l.configHome, "settings.json")},
		{Source: SourceProject, Path: filepath.Join(l.cwd, ".ascaris.json")},
		{Source: SourceProject, Path: filepath.Join(l.cwd, ".ascaris", "settings.json")},
		{Source: SourceLocal, Path: filepath.Join(l.cwd, ".ascaris", "settings.local.json")},
	}
}

func (l Loader) Load() (RuntimeConfig, error) {
	merged := map[string]any{}
	loaded := make([]Entry, 0, 5)
	for _, entry := range l.Discover() {
		object, exists, err := readOptionalObject(entry.Path)
		if err != nil {
			return RuntimeConfig{}, err
		}
		if !exists {
			continue
		}
		deepMergeObjects(merged, object)
		loaded = append(loaded, entry)
	}
	return RuntimeConfig{merged: merged, loadedEntries: loaded}, nil
}

func Load(root string) (RuntimeConfig, error) {
	return DefaultLoader(root).Load()
}

func Empty() RuntimeConfig {
	return RuntimeConfig{merged: map[string]any{}, loadedEntries: nil}
}

func (c RuntimeConfig) LoadedEntries() []Entry {
	return append([]Entry(nil), c.loadedEntries...)
}

func (c RuntimeConfig) Merged() map[string]any {
	return cloneValueMap(c.merged)
}

func (c RuntimeConfig) Section(key string) any {
	return c.merged[key]
}

func (c RuntimeConfig) Model() string {
	return stringAt(c.merged, "model")
}

func (c RuntimeConfig) PermissionMode() string {
	if value := stringAtMap(nestedMap(c.merged, "permissions"), "defaultMode"); value != "" {
		return value
	}
	return stringAt(c.merged, "permissionMode")
}

func (c RuntimeConfig) ProviderSettings() ProviderSettings {
	provider := nestedMap(c.merged, "provider")
	return ProviderSettings{
		AnthropicBaseURL: firstNonEmpty(
			stringAtMap(provider, "anthropicBaseURL"),
			stringAtMap(provider, "anthropic_base_url"),
		),
		OpenAIBaseURL: firstNonEmpty(
			stringAtMap(provider, "openaiBaseURL"),
			stringAtMap(provider, "openai_base_url"),
		),
		OpenRouterBaseURL: firstNonEmpty(
			stringAtMap(provider, "openRouterBaseURL"),
			stringAtMap(provider, "openrouterBaseURL"),
			stringAtMap(provider, "openrouter_base_url"),
		),
		Kind: firstNonEmpty(
			stringAtMap(provider, "kind"),
			stringAtMap(provider, "providerKind"),
			stringAt(c.merged, "provider"),
		),
		XAIBaseURL: firstNonEmpty(
			stringAtMap(provider, "xaiBaseURL"),
			stringAtMap(provider, "xai_base_url"),
		),
		ProxyURL: firstNonEmpty(
			stringAtMap(provider, "proxyUrl"),
			stringAt(c.merged, "proxyUrl"),
		),
	}
}

func (c RuntimeConfig) JSONIndent() string {
	data, err := json.MarshalIndent(c.merged, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func readOptionalObject(path string) (map[string]any, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, true, nil
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("%s: config root must be a JSON object", path)
	}
	return object, true, nil
}

func deepMergeObjects(dst, src map[string]any) {
	for key, srcValue := range src {
		srcObject, srcIsObject := srcValue.(map[string]any)
		dstObject, dstIsObject := dst[key].(map[string]any)
		if srcIsObject && dstIsObject {
			deepMergeObjects(dstObject, srcObject)
			dst[key] = dstObject
			continue
		}
		dst[key] = cloneValue(srcValue)
	}
}

func cloneValueMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = cloneValue(value)
	}
	return dst
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneValueMap(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, cloneValue(item))
		}
		return out
	default:
		return typed
	}
}

func nestedMap(root map[string]any, key string) map[string]any {
	value, ok := root[key]
	if !ok {
		return nil
	}
	mapped, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return mapped
}

func stringAt(root map[string]any, key string) string {
	return stringAtMap(root, key)
}

func stringAtMap(root map[string]any, key string) string {
	if root == nil {
		return ""
	}
	value, ok := root[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func intAtMap(root map[string]any, key string) int {
	if root == nil {
		return 0
	}
	value, ok := root[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
