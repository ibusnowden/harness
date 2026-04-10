package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"ascaris/internal/api"
	"ascaris/internal/config"
)

type ConnectionStatus string

const (
	StatusDisconnected ConnectionStatus = "disconnected"
	StatusConnected    ConnectionStatus = "connected"
	StatusUnsupported  ConnectionStatus = "unsupported"
	StatusError        ConnectionStatus = "error"
)

type ResourceInfo struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
}

type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	ServerName  string          `json:"server_name,omitempty"`
	Qualified   string          `json:"qualified_name,omitempty"`
}

type ServerState struct {
	ServerName   string              `json:"server_name"`
	Transport    config.McpTransport `json:"transport"`
	Status       ConnectionStatus    `json:"status"`
	Signature    string              `json:"signature,omitempty"`
	ConfigHash   string              `json:"config_hash,omitempty"`
	Tools        []ToolInfo          `json:"tools,omitempty"`
	Resources    []ResourceInfo      `json:"resources,omitempty"`
	ServerInfo   string              `json:"server_info,omitempty"`
	ErrorMessage string              `json:"error_message,omitempty"`
	Unsupported  bool                `json:"unsupported,omitempty"`
}

type Registry struct {
	servers map[string]config.McpServer
	states  map[string]ServerState
}

func NewRegistry(servers map[string]config.McpServer) *Registry {
	cloned := make(map[string]config.McpServer, len(servers))
	for name, server := range servers {
		cloned[name] = server
	}
	return &Registry{servers: cloned, states: map[string]ServerState{}}
}

func FromConfig(cfg config.RuntimeConfig) *Registry {
	return NewRegistry(cfg.MCPServers())
}

func (r *Registry) Discover() error {
	r.states = map[string]ServerState{}
	for name, server := range r.servers {
		state := ServerState{
			ServerName: name,
			Transport:  server.Transport,
			Signature:  server.Signature(),
			ConfigHash: server.ConfigHash(),
		}
		switch server.Transport {
		case config.McpTransportStdio:
			tools, resources, info, err := discoverViaStdio(server)
			if err != nil {
				state.Status = StatusError
				state.ErrorMessage = err.Error()
			} else {
				state.Status = StatusConnected
				state.Tools = tools
				state.Resources = resources
				state.ServerInfo = info
			}
		default:
			state.Status = StatusUnsupported
			state.Unsupported = true
			state.ErrorMessage = fmt.Sprintf("transport %q is not supported by the live MCP manager", server.Transport)
		}
		r.states[name] = state
	}
	return nil
}

func (r *Registry) States() []ServerState {
	states := make([]ServerState, 0, len(r.states))
	for _, state := range r.states {
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool {
		return states[i].ServerName < states[j].ServerName
	})
	return states
}

func (r *Registry) ToolDefinitions() []api.ToolDefinition {
	var definitions []api.ToolDefinition
	for _, state := range r.States() {
		for _, tool := range state.Tools {
			definitions = append(definitions, api.ToolDefinition{
				Name:        tool.Qualified,
				Description: tool.Description,
				InputSchema: cloneJSON(tool.InputSchema),
			})
		}
	}
	return definitions
}

func (r *Registry) ListResources(serverName string) ([]ResourceInfo, error) {
	if len(r.states) == 0 {
		if err := r.Discover(); err != nil {
			return nil, err
		}
	}
	state, ok := r.states[serverName]
	if !ok {
		return nil, fmt.Errorf("server %q not found", serverName)
	}
	if state.Status != StatusConnected {
		return nil, fmt.Errorf("server %q is not connected (status: %s)", serverName, state.Status)
	}
	return append([]ResourceInfo(nil), state.Resources...), nil
}

func (r *Registry) ReadResource(serverName, uri string) (string, error) {
	server, ok := r.servers[serverName]
	if !ok {
		return "", fmt.Errorf("server %q not found", serverName)
	}
	if server.Transport != config.McpTransportStdio {
		return "", fmt.Errorf("server %q transport %q is not supported", serverName, server.Transport)
	}
	process, err := spawnStdio(server)
	if err != nil {
		return "", err
	}
	defer process.Close()
	if _, err := process.Initialize(); err != nil {
		return "", err
	}
	result, err := process.ReadResource(uri)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(result)
	return string(data), nil
}

func (r *Registry) CallQualifiedTool(qualifiedName string, input json.RawMessage) (string, error) {
	_, rawToolName, server, err := r.resolveQualifiedTool(qualifiedName)
	if err != nil {
		return "", err
	}
	process, err := spawnStdio(server)
	if err != nil {
		return "", err
	}
	defer process.Close()
	if _, err := process.Initialize(); err != nil {
		return "", err
	}
	result, err := process.CallTool(rawToolName, input)
	if err != nil {
		return "", err
	}
	if result.IsError {
		payload, _ := json.Marshal(result)
		return string(payload), fmt.Errorf("mcp tool call returned an error")
	}
	if len(result.Content) == 0 {
		payload, _ := json.Marshal(result)
		return string(payload), nil
	}
	var parts []string
	for _, content := range result.Content {
		if strings.TrimSpace(content.Text) != "" {
			parts = append(parts, content.Text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n"), nil
	}
	payload, _ := json.Marshal(result)
	return string(payload), nil
}

func (r *Registry) resolveQualifiedTool(qualifiedName string) (string, string, config.McpServer, error) {
	for name, server := range r.servers {
		prefix := server.ToolPrefix()
		if !strings.HasPrefix(qualifiedName, prefix) {
			continue
		}
		if server.Transport != config.McpTransportStdio {
			return "", "", config.McpServer{}, fmt.Errorf("server %q transport %q is not supported", name, server.Transport)
		}
		raw := strings.TrimPrefix(qualifiedName, prefix)
		return name, raw, server, nil
	}
	return "", "", config.McpServer{}, fmt.Errorf("unknown MCP tool: %s", qualifiedName)
}

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonRPCResponse[T any] struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Result  *T            `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

type initializeResult struct {
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type listToolsResult struct {
	Tools []mcpTool `json:"tools"`
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type listResourcesResult struct {
	Resources []ResourceInfo `json:"resources"`
}

type readResourceResult struct {
	Contents []map[string]any `json:"contents"`
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type callToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type callToolResult struct {
	Content []callToolContent `json:"content,omitempty"`
	IsError bool              `json:"isError,omitempty"`
}

type stdioProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID int
}

func discoverViaStdio(server config.McpServer) ([]ToolInfo, []ResourceInfo, string, error) {
	process, err := spawnStdio(server)
	if err != nil {
		return nil, nil, "", err
	}
	defer process.Close()
	info, err := process.Initialize()
	if err != nil {
		return nil, nil, "", err
	}
	toolsResult, err := process.ListTools()
	if err != nil {
		return nil, nil, "", err
	}
	resourcesResult, err := process.ListResources()
	if err != nil {
		return nil, nil, "", err
	}
	tools := make([]ToolInfo, 0, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		tools = append(tools, ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: cloneJSON(tool.InputSchema),
			ServerName:  server.Name,
			Qualified:   server.QualifiedToolName(tool.Name),
		})
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Qualified < tools[j].Qualified
	})
	return tools, resourcesResult.Resources, strings.TrimSpace(info.ServerInfo.Name + " " + info.ServerInfo.Version), nil
}

func spawnStdio(server config.McpServer) (*stdioProcess, error) {
	cmd := exec.Command(server.Command, server.Args...)
	for key, value := range server.Env {
		cmd.Env = append(cmd.Environ(), key+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &stdioProcess{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), nextID: 1}, nil
}

func (p *stdioProcess) Close() error {
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	if p.cmd != nil {
		_, _ = p.cmd.Process.Wait()
	}
	return nil
}

func (p *stdioProcess) Initialize() (initializeResult, error) {
	params := initializeParams{ProtocolVersion: "2024-11-05"}
	params.ClientInfo.Name = "ascaris"
	params.ClientInfo.Version = "0"
	return request[initializeResult](p, "initialize", params)
}

func (p *stdioProcess) ListTools() (listToolsResult, error) {
	return request[listToolsResult](p, "tools/list", map[string]any{})
}

func (p *stdioProcess) CallTool(name string, arguments json.RawMessage) (callToolResult, error) {
	params := callToolParams{Name: name, Arguments: arguments}
	return request[callToolResult](p, "tools/call", params)
}

func (p *stdioProcess) ListResources() (listResourcesResult, error) {
	return request[listResourcesResult](p, "resources/list", map[string]any{})
}

func (p *stdioProcess) ReadResource(uri string) (readResourceResult, error) {
	return request[readResourceResult](p, "resources/read", map[string]any{"uri": uri})
}

func request[T any](p *stdioProcess, method string, params interface{}) (T, error) {
	var zero T
	id := p.nextID
	p.nextID++
	payload, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return zero, err
	}
	if err := writeFrame(p.stdin, payload); err != nil {
		return zero, err
	}
	responseBytes, err := readFrame(p.stdout)
	if err != nil {
		return zero, err
	}
	var response jsonRPCResponse[T]
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return zero, err
	}
	if response.JSONRPC != "2.0" {
		return zero, fmt.Errorf("MCP response for %s used unsupported jsonrpc version %q", method, response.JSONRPC)
	}
	if response.ID != id {
		return zero, fmt.Errorf("MCP response for %s used mismatched id: expected %d, got %d", method, id, response.ID)
	}
	if response.Error != nil {
		return zero, fmt.Errorf("MCP server returned JSON-RPC error for %s: %s (%d)", method, response.Error.Message, response.Error.Code)
	}
	if response.Result == nil {
		return zero, fmt.Errorf("MCP server returned no result for %s", method)
	}
	return *response.Result, nil
}

func writeFrame(w io.Writer, payload []byte) error {
	header := "Content-Length: " + strconv.Itoa(len(payload)) + "\r\n\r\n"
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if line == "\r\n" {
			break
		}
		header := strings.TrimRight(line, "\r\n")
		name, value, ok := strings.Cut(header, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			contentLength = parsed
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}
