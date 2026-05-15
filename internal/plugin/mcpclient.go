package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MCPClient is a minimal MCP (Model Context Protocol) client that communicates
// with a plugin subprocess over stdin/stdout using JSON-RPC 2.0.
type MCPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex // serializes writes to stdin
	nextID atomic.Int64
	info   *MCPServerInfo
	tools  []MCPTool
	closed bool
}

// MCPClientConfig configures how the MCP subprocess is launched.
type MCPClientConfig struct {
	// Command is the path to the plugin binary.
	Command string
	// Args are additional arguments to the plugin binary.
	Args []string
	// EnvAllowlist is the list of extra environment variable names the plugin
	// may receive beyond the baseline (PATH, HOME, LANG).
	EnvAllowlist []string
	// StartupTimeout is how long to wait for the initialize handshake.
	StartupTimeout time.Duration
	// CallTimeout is the default timeout for tool calls.
	CallTimeout time.Duration
}

// MCPServerInfo is returned by the initialize handshake.
type MCPServerInfo struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	ProtocolVersion string `json:"protocolVersion"`
}

// MCPTool describes a tool exposed by the MCP server.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// JSON-RPC 2.0 types.
type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *jsonrpcError) Error() string {
	return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message)
}

// StartMCP launches an MCP subprocess and performs the initialize handshake.
func StartMCP(ctx context.Context, cfg MCPClientConfig) (*MCPClient, error) {
	if cfg.StartupTimeout == 0 {
		cfg.StartupTimeout = 2 * time.Second
	}
	if cfg.CallTimeout == 0 {
		cfg.CallTimeout = 30 * time.Second
	}

	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...) //nolint:gosec // command path from trusted lockfile
	cmd.Env = scrubbedEnv(cfg.EnvAllowlist)
	cmd.Stderr = os.Stderr // let plugin errors surface

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %s: %w", cfg.Command, err)
	}

	c := &MCPClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdoutPipe),
	}

	// Initialize handshake with timeout.
	initCtx, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancel()

	initResult, err := c.call(initCtx, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "beads",
			"version": "1.0",
		},
	})
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp: initialize handshake failed: %w", err)
	}

	var initResp struct {
		ServerInfo      MCPServerInfo `json:"serverInfo"`
		ProtocolVersion string        `json:"protocolVersion"`
	}
	if err := json.Unmarshal(initResult, &initResp); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp: parse initialize response: %w", err)
	}
	c.info = &initResp.ServerInfo
	c.info.ProtocolVersion = initResp.ProtocolVersion

	// Send initialized notification (no response expected).
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp: initialized notification: %w", err)
	}

	// Discover tools.
	toolsResult, err := c.call(initCtx, "tools/list", nil)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp: tools/list: %w", err)
	}
	var toolsResp struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(toolsResult, &toolsResp); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp: parse tools: %w", err)
	}
	c.tools = toolsResp.Tools

	return c, nil
}

// ServerInfo returns information about the connected MCP server.
func (c *MCPClient) ServerInfo() *MCPServerInfo {
	return c.info
}

// Tools returns the list of tools the server exposes.
func (c *MCPClient) Tools() []MCPTool {
	return c.tools
}

// HasTool checks whether the server exposes a tool with the given name.
func (c *MCPClient) HasTool(name string) bool {
	for _, t := range c.tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// CallTool invokes a tool on the MCP server and returns the raw JSON result.
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (json.RawMessage, error) {
	params := map[string]interface{}{
		"name": name,
	}
	if args != nil {
		params["arguments"] = args
	}

	result, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("tool %q: %w", name, err)
	}

	// MCP tool results have content array; extract text content.
	var toolResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &toolResult); err != nil {
		return nil, fmt.Errorf("tool %q: parse result: %w", name, err)
	}
	if toolResult.IsError {
		var errText string
		for _, c := range toolResult.Content {
			if c.Type == "text" {
				errText = c.Text
				break
			}
		}
		return nil, fmt.Errorf("tool %q error: %s", name, errText)
	}

	// Return the text content as raw JSON.
	for _, c := range toolResult.Content {
		if c.Type == "text" {
			return json.RawMessage(c.Text), nil
		}
	}
	return nil, fmt.Errorf("tool %q: no text content in response", name)
}

// Close gracefully shuts down the MCP subprocess.
func (c *MCPClient) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true

	// Try graceful shutdown first.
	_ = c.stdin.Close()

	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		return fmt.Errorf("mcp: subprocess did not exit within 5s, killed")
	}
}

// call sends a JSON-RPC request and waits for the response.
func (c *MCPClient) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	c.mu.Lock()
	data, err := json.Marshal(req)
	if err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	c.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Read response lines until we get one matching our ID.
	// Skip notifications (no id field).
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		var resp jsonrpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue // skip malformed lines
		}
		if resp.ID == nil {
			continue // notification, skip
		}
		if *resp.ID != id {
			continue // response for different request
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// notify sends a JSON-RPC notification (no response expected).
func (c *MCPClient) notify(_ context.Context, method string, params interface{}) error {
	// Notifications have no ID field.
	type notification struct {
		JSONRPC string      `json:"jsonrpc"`
		Method  string      `json:"method"`
		Params  interface{} `json:"params,omitempty"`
	}
	req := notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

// scrubbedEnv returns a minimal environment for subprocess execution.
// Only PATH, HOME, LANG are included by default, plus any explicitly
// allowed variables from the plugin manifest.
func scrubbedEnv(allowlist []string) []string {
	allowed := map[string]bool{
		"PATH": true,
		"HOME": true,
		"LANG": true,
	}
	for _, k := range allowlist {
		allowed[k] = true
	}

	var env []string
	for _, e := range os.Environ() {
		idx := strings.IndexByte(e, '=')
		if idx < 0 {
			continue
		}
		key := e[:idx]
		if allowed[key] {
			env = append(env, e)
		}
	}
	return env
}
