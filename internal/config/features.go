package config

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type HookSettings struct {
	PreToolUse         []string `json:"PreToolUse,omitempty"`
	PostToolUse        []string `json:"PostToolUse,omitempty"`
	PostToolUseFailure []string `json:"PostToolUseFailure,omitempty"`
}

func (h HookSettings) IsZero() bool {
	return len(h.PreToolUse) == 0 && len(h.PostToolUse) == 0 && len(h.PostToolUseFailure) == 0
}

type PermissionRules struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
	Ask   []string `json:"ask,omitempty"`
}

type OAuthSettings struct {
	ClientID          string   `json:"clientId,omitempty"`
	AuthorizeURL      string   `json:"authorizeUrl,omitempty"`
	TokenURL          string   `json:"tokenUrl,omitempty"`
	CallbackPort      int      `json:"callbackPort,omitempty"`
	ManualRedirectURL string   `json:"manualRedirectUrl,omitempty"`
	Scopes            []string `json:"scopes,omitempty"`
}

type SandboxSettings struct {
	Mode          string   `json:"mode,omitempty"`
	Network       string   `json:"network,omitempty"`
	WritableRoots []string `json:"writableRoots,omitempty"`
}

type ProviderFallbacks struct {
	Primary   string   `json:"primary,omitempty"`
	Fallbacks []string `json:"fallbacks,omitempty"`
}

type McpTransport string

const (
	McpTransportStdio        McpTransport = "stdio"
	McpTransportSSE          McpTransport = "sse"
	McpTransportHTTP         McpTransport = "http"
	McpTransportWS           McpTransport = "ws"
	McpTransportSDK          McpTransport = "sdk"
	McpTransportManagedProxy McpTransport = "managed-proxy"
)

type McpOAuthConfig struct {
	ClientID              string `json:"clientId,omitempty"`
	CallbackPort          int    `json:"callbackPort,omitempty"`
	AuthServerMetadataURL string `json:"authServerMetadataUrl,omitempty"`
	XAA                   bool   `json:"xaa,omitempty"`
}

type McpServer struct {
	Name              string            `json:"name"`
	Source            Source            `json:"source"`
	Transport         McpTransport      `json:"transport"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	ToolCallTimeoutMS int               `json:"toolCallTimeoutMs,omitempty"`
	URL               string            `json:"url,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	HeadersHelper     string            `json:"headersHelper,omitempty"`
	SDKName           string            `json:"sdkName,omitempty"`
	ProxyID           string            `json:"proxyId,omitempty"`
	OAuth             *McpOAuthConfig   `json:"oauth,omitempty"`
}

func (c RuntimeConfig) Hooks() HookSettings {
	root := nestedMap(c.merged, "hooks")
	return HookSettings{
		PreToolUse:         stringSliceAtMap(root, "PreToolUse"),
		PostToolUse:        stringSliceAtMap(root, "PostToolUse"),
		PostToolUseFailure: stringSliceAtMap(root, "PostToolUseFailure"),
	}
}

func (c RuntimeConfig) EnabledPlugins() map[string]bool {
	values := nestedMap(c.merged, "enabledPlugins")
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]bool, len(values))
	for key, value := range values {
		if enabled, ok := value.(bool); ok {
			out[key] = enabled
		}
	}
	return out
}

func (c RuntimeConfig) PluginSettings() PluginSettings {
	plugins := nestedMap(c.merged, "plugins")
	return PluginSettings{
		MaxOutputTokens: intAtMap(plugins, "maxOutputTokens"),
		Enabled:         c.EnabledPlugins(),
		ExternalDirs:    stringSliceAtMap(plugins, "externalDirectories"),
		InstallRoot:     stringAtMap(plugins, "installRoot"),
		RegistryPath:    stringAtMap(plugins, "registryPath"),
		BundledRoot:     stringAtMap(plugins, "bundledRoot"),
	}
}

func (c RuntimeConfig) PermissionRules() PermissionRules {
	root := nestedMap(c.merged, "permissions")
	return PermissionRules{
		Allow: stringSliceAtMap(root, "allow"),
		Deny:  stringSliceAtMap(root, "deny"),
		Ask:   stringSliceAtMap(root, "ask"),
	}
}

func (c RuntimeConfig) OAuth() *OAuthSettings {
	root := nestedMap(c.merged, "oauth")
	if len(root) == 0 {
		return nil
	}
	return &OAuthSettings{
		ClientID:          stringAtMap(root, "clientId"),
		AuthorizeURL:      stringAtMap(root, "authorizeUrl"),
		TokenURL:          stringAtMap(root, "tokenUrl"),
		CallbackPort:      intAtMap(root, "callbackPort"),
		ManualRedirectURL: stringAtMap(root, "manualRedirectUrl"),
		Scopes:            stringSliceAtMap(root, "scopes"),
	}
}

func (c RuntimeConfig) Aliases() map[string]string {
	root := nestedMap(c.merged, "aliases")
	if len(root) == 0 {
		return nil
	}
	out := make(map[string]string, len(root))
	for key, value := range root {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			out[key] = strings.TrimSpace(text)
		}
	}
	return out
}

func (c RuntimeConfig) ProviderFallbacks() ProviderFallbacks {
	root := nestedMap(c.merged, "providerFallbacks")
	if len(root) == 0 {
		root = nestedMap(nestedMap(c.merged, "provider"), "fallbacks")
	}
	return ProviderFallbacks{
		Primary:   stringAtMap(root, "primary"),
		Fallbacks: stringSliceAtMap(root, "fallbacks"),
	}
}

func (c RuntimeConfig) Sandbox() SandboxSettings {
	root := nestedMap(c.merged, "sandbox")
	return SandboxSettings{
		Mode:          stringAtMap(root, "mode"),
		Network:       stringAtMap(root, "network"),
		WritableRoots: stringSliceAtMap(root, "writableRoots"),
	}
}

func (c RuntimeConfig) MCPServers() map[string]McpServer {
	servers := map[string]McpServer{}
	sources := map[string]Source{}
	for _, entry := range c.loadedEntries {
		object, exists, err := readOptionalObject(entry.Path)
		if err != nil || !exists {
			continue
		}
		rawServers := nestedMap(object, "mcpServers")
		for name, value := range rawServers {
			serverObject, ok := value.(map[string]any)
			if !ok {
				continue
			}
			server, err := parseMcpServer(name, entry.Source, serverObject)
			if err != nil {
				continue
			}
			servers[name] = server
			sources[name] = entry.Source
		}
	}
	for name, server := range servers {
		server.Source = sources[name]
		servers[name] = server
	}
	return servers
}

func parseMcpServer(name string, source Source, object map[string]any) (McpServer, error) {
	server := McpServer{
		Name:   name,
		Source: source,
		Args:   stringSliceAtMap(object, "args"),
		Env:    stringMapAtMap(object, "env"),
	}
	transportName := strings.ToLower(strings.TrimSpace(stringAtMap(object, "type")))
	switch {
	case transportName != "":
		server.Transport = McpTransport(transportName)
	case stringAtMap(object, "command") != "":
		server.Transport = McpTransportStdio
	case stringAtMap(object, "name") != "":
		server.Transport = McpTransportSDK
	case stringAtMap(object, "id") != "":
		server.Transport = McpTransportManagedProxy
	case strings.HasPrefix(strings.ToLower(stringAtMap(object, "url")), "ws"):
		server.Transport = McpTransportWS
	case stringAtMap(object, "url") != "":
		server.Transport = McpTransportHTTP
	default:
		return McpServer{}, fmt.Errorf("unknown transport for MCP server %s", name)
	}
	server.Command = stringAtMap(object, "command")
	server.ToolCallTimeoutMS = intAtMap(object, "toolCallTimeoutMs")
	server.URL = stringAtMap(object, "url")
	server.Headers = stringMapAtMap(object, "headers")
	server.HeadersHelper = stringAtMap(object, "headersHelper")
	server.SDKName = firstNonEmpty(stringAtMap(object, "sdkName"), stringAtMap(object, "name"))
	server.ProxyID = stringAtMap(object, "id")
	if oauthRoot := nestedMap(object, "oauth"); len(oauthRoot) > 0 {
		server.OAuth = &McpOAuthConfig{
			ClientID:              stringAtMap(oauthRoot, "clientId"),
			CallbackPort:          intAtMap(oauthRoot, "callbackPort"),
			AuthServerMetadataURL: stringAtMap(oauthRoot, "authServerMetadataUrl"),
			XAA:                   boolAtMap(oauthRoot, "xaa"),
		}
	}
	return server, nil
}

func (s McpServer) NormalizedName() string {
	var b strings.Builder
	lastUnderscore := false
	for _, ch := range s.Name {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '_', ch == '-':
			b.WriteRune(ch)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func (s McpServer) ToolPrefix() string {
	return "mcp__" + s.NormalizedName() + "__"
}

func (s McpServer) QualifiedToolName(toolName string) string {
	return s.ToolPrefix() + normalizeMcpToken(toolName)
}

func (s McpServer) Signature() string {
	switch s.Transport {
	case McpTransportStdio:
		parts := append([]string{s.Command}, s.Args...)
		return "stdio:[" + strings.Join(parts, "|") + "]"
	case McpTransportHTTP, McpTransportSSE, McpTransportWS, McpTransportManagedProxy:
		return "url:" + s.UnwrappedURL()
	case McpTransportSDK:
		return ""
	default:
		return ""
	}
}

func (s McpServer) ConfigHash() string {
	payload, _ := json.Marshal(s)
	hash := uint64(0xcbf29ce484222325)
	for _, b := range payload {
		hash ^= uint64(b)
		hash *= 0x100000001b3
	}
	return fmt.Sprintf("%016x", hash)
}

func (s McpServer) UnwrappedURL() string {
	url := s.URL
	if !strings.Contains(url, "mcp_url=") {
		return url
	}
	parts := strings.Split(url, "?")
	if len(parts) != 2 {
		return url
	}
	for _, pair := range strings.Split(parts[1], "&") {
		key, value, ok := strings.Cut(pair, "=")
		if ok && key == "mcp_url" {
			if decoded, err := urlQueryUnescape(value); err == nil {
				return decoded
			}
		}
	}
	return url
}

func normalizeMcpToken(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '_', ch == '-':
			b.WriteRune(ch)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func urlQueryUnescape(value string) (string, error) {
	replacer := strings.NewReplacer("+", " ")
	value = replacer.Replace(value)
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '%' || i+2 >= len(value) {
			out.WriteByte(value[i])
			continue
		}
		var decoded byte
		_, err := fmt.Sscanf(value[i+1:i+3], "%02X", &decoded)
		if err != nil {
			_, err = fmt.Sscanf(value[i+1:i+3], "%02x", &decoded)
			if err != nil {
				out.WriteByte(value[i])
				continue
			}
		}
		out.WriteByte(decoded)
		i += 2
	}
	return out.String(), nil
}

func stringSliceAtMap(root map[string]any, key string) []string {
	if root == nil {
		return nil
	}
	value, ok := root[key]
	if !ok {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			out = append(out, strings.TrimSpace(text))
		}
	}
	return out
}

func stringMapAtMap(root map[string]any, key string) map[string]string {
	mapped := nestedMap(root, key)
	if len(mapped) == 0 {
		return nil
	}
	out := make(map[string]string, len(mapped))
	for field, value := range mapped {
		if text, ok := value.(string); ok {
			out[field] = text
		}
	}
	return out
}

func boolAtMap(root map[string]any, key string) bool {
	if root == nil {
		return false
	}
	value, ok := root[key]
	if !ok {
		return false
	}
	flag, _ := value.(bool)
	return flag
}
