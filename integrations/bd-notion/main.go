// bd-notion is a standalone MCP stdio server that exposes Notion as a
// beads tracker plugin. It speaks JSON-RPC 2.0 over stdin/stdout per the
// Model Context Protocol specification.
//
// Usage:
//
//	bd plugin install ./integrations/bd-notion
//	NOTION_TOKEN=secret_xxx bd-notion   # (launched automatically by bd)
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const (
	serverName    = "bd-notion"
	serverVersion = "0.1.0"
	protoVersion  = "2024-11-05"
)

func main() {
	s := &server{
		reader: bufio.NewReader(os.Stdin),
		writer: os.Stdout,
	}
	if err := s.run(); err != nil {
		fmt.Fprintf(os.Stderr, "bd-notion: %v\n", err)
		os.Exit(1)
	}
}

// --- JSON-RPC 2.0 types ---

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *json.Number    `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// --- MCP server ---

type server struct {
	reader *bufio.Reader
	writer io.Writer
	client *NotionClient
}

func (s *server) run() error {
	for {
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}

		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue // skip malformed lines
		}

		// Notifications have no ID — don't send a response.
		if req.ID == nil {
			s.handleNotification(req)
			continue
		}

		resp := s.handleRequest(req)
		if err := s.writeResponse(resp); err != nil {
			return fmt.Errorf("write: %w", err)
		}
	}
}

func (s *server) handleNotification(req jsonrpcRequest) {
	// notifications/initialized — nothing to do.
}

func (s *server) handleRequest(req jsonrpcRequest) *jsonrpcResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	default:
		return &jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32601,
				Message: fmt.Sprintf("method not found: %s", req.Method),
			},
		}
	}
}

func (s *server) handleInitialize(req jsonrpcRequest) *jsonrpcResponse {
	// Initialize the Notion client from environment.
	token := os.Getenv("NOTION_TOKEN")
	if token != "" {
		s.client = NewNotionClient(token)
	}

	return &jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"protocolVersion": protoVersion,
			"serverInfo": map[string]interface{}{
				"name":    serverName,
				"version": serverVersion,
			},
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
		},
	}
}

func (s *server) handleToolsList(req jsonrpcRequest) *jsonrpcResponse {
	return &jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"tools": toolDefinitions(),
		},
	}
}

func (s *server) handleToolsCall(req jsonrpcRequest) *jsonrpcResponse {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32602,
				Message: "invalid params: " + err.Error(),
			},
		}
	}

	result, toolErr := s.dispatchTool(params.Name, params.Arguments)
	if toolErr != nil {
		return &jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  mcpToolError(toolErr.Error()),
		}
	}

	// Marshal the result to JSON text content.
	data, err := json.Marshal(result)
	if err != nil {
		return &jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  mcpToolError("marshal result: " + err.Error()),
		}
	}

	return &jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  mcpToolSuccess(string(data)),
	}
}

func (s *server) writeResponse(resp *jsonrpcResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = s.writer.Write(data)
	return err
}

// mcpToolSuccess wraps a result string in MCP tool result format.
func mcpToolSuccess(text string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": text},
		},
	}
}

// mcpToolError wraps an error in MCP tool result format.
func mcpToolError(msg string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": msg},
		},
		"isError": true,
	}
}
