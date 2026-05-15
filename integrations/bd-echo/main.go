// bd-echo is a minimal tracker driver for testing the tracker driver protocol.
// It speaks MCP (JSON-RPC 2.0 over stdio) and implements the IssueTracker
// interface by echoing back synthetic issues. No external dependencies.
//
// Usage:
//
//	bd plugin install ./integrations/bd-echo
//	bd plugin trust bd-echo tracker.read
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const serverName = "bd-echo"
const serverVersion = "0.1.0"

// jsonrpcRequest is an incoming JSON-RPC 2.0 request.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is an outgoing JSON-RPC 2.0 response.
type jsonrpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// tools lists the MCP tools this driver exposes.
var tools = []map[string]interface{}{
	{"name": "tracker_info", "description": "Returns tracker metadata", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{"name": "validate", "description": "Validates tracker configuration", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{"name": "fetch_issues", "description": "Fetches issues", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"state": map[string]string{"type": "string"}, "limit": map[string]string{"type": "integer"}}}},
	{"name": "fetch_issue", "description": "Fetches a single issue", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"identifier": map[string]string{"type": "string"}}, "required": []string{"identifier"}}},
	{"name": "create_issue", "description": "Creates an issue", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{"name": "update_issue", "description": "Updates an issue", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{"name": "is_external_ref", "description": "Checks if ref belongs to this tracker", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ref": map[string]string{"type": "string"}}}},
	{"name": "extract_identifier", "description": "Extracts identifier from ref", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ref": map[string]string{"type": "string"}}}},
	{"name": "build_external_ref", "description": "Builds external ref", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{"name": "map_status_to_beads", "description": "Maps external status to beads", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"status": map[string]string{"type": "string"}}}},
	{"name": "map_status_from_beads", "description": "Maps beads status to external", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"status": map[string]string{"type": "string"}}}},
	{"name": "map_priority_to_beads", "description": "Maps external priority to beads", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"priority": map[string]string{"type": "string"}}}},
	{"name": "map_priority_from_beads", "description": "Maps beads priority to external", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"priority": map[string]string{"type": "string"}}}},
	{"name": "map_type_to_beads", "description": "Maps external type to beads", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"issue_type": map[string]string{"type": "string"}}}},
	{"name": "map_type_from_beads", "description": "Maps beads type to external", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"issue_type": map[string]string{"type": "string"}}}},
}

func main() {
	fmt.Fprintf(os.Stderr, "[%s] starting tracker driver\n", serverName)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			writeError(nil, -32700, "parse error")
			continue
		}

		switch req.Method {
		case "initialize":
			writeResult(req.ID, map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{"tools": map[string]bool{}},
				"serverInfo":      map[string]string{"name": serverName, "version": serverVersion},
			})

		case "notifications/initialized":
			// No response needed for notifications.

		case "tools/list":
			writeResult(req.ID, map[string]interface{}{"tools": tools})

		case "tools/call":
			handleToolCall(req.ID, req.Params)

		case "shutdown":
			writeResult(req.ID, nil)
			os.Exit(0)

		default:
			writeError(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
		}
	}
}

func handleToolCall(id interface{}, params json.RawMessage) {
	var call struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		writeError(id, -32602, "invalid params")
		return
	}

	var result interface{}

	switch call.Name {
	case "tracker_info":
		result = map[string]string{
			"name":          "echo",
			"display_name":  "Echo (Test Driver)",
			"config_prefix": "echo",
		}

	case "validate":
		result = map[string]interface{}{"ok": true}

	case "fetch_issues":
		now := time.Now().UTC().Format(time.RFC3339)
		result = []map[string]interface{}{
			{
				"ID":          "ECHO-1",
				"Identifier":  "ECHO-1",
				"Title":       "Test issue from echo driver",
				"Description": "This issue was returned by the bd-echo test tracker driver.",
				"State":       "open",
				"Priority":    2,
				"Type":        "task",
				"URL":         "https://echo.test/ECHO-1",
				"UpdatedAt":   now,
			},
		}

	case "fetch_issue":
		identifier, _ := call.Arguments["identifier"].(string)
		if identifier == "" {
			writeError(id, -32602, "identifier required")
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		result = map[string]interface{}{
			"ID":          identifier,
			"Identifier":  identifier,
			"Title":       fmt.Sprintf("Echo issue %s", identifier),
			"Description": "Echoed by bd-echo tracker driver.",
			"State":       "open",
			"Priority":    2,
			"Type":        "task",
			"URL":         fmt.Sprintf("https://echo.test/%s", identifier),
			"UpdatedAt":   now,
		}

	case "create_issue":
		result = map[string]interface{}{
			"ID":         "ECHO-NEW",
			"Identifier": "ECHO-NEW",
			"Title":      "Created via echo driver",
			"State":      "open",
			"URL":        "https://echo.test/ECHO-NEW",
		}

	case "update_issue":
		result = map[string]interface{}{
			"ID":         "ECHO-UPDATED",
			"Identifier": "ECHO-UPDATED",
			"Title":      "Updated via echo driver",
			"State":      "open",
			"URL":        "https://echo.test/ECHO-UPDATED",
		}

	case "is_external_ref":
		ref, _ := call.Arguments["ref"].(string)
		result = len(ref) > 7 && ref[:7] == "echo://"

	case "extract_identifier":
		ref, _ := call.Arguments["ref"].(string)
		if len(ref) > 7 && ref[:7] == "echo://" {
			result = ref[7:]
		} else {
			result = ref
		}

	case "build_external_ref":
		id, _ := call.Arguments["identifier"].(string)
		result = fmt.Sprintf("echo://%s", id)

	case "map_status_to_beads":
		result = map[string]string{"status": "open"}
	case "map_status_from_beads":
		result = map[string]string{"status": "Open"}
	case "map_priority_to_beads":
		result = map[string]interface{}{"priority": 2}
	case "map_priority_from_beads":
		result = map[string]string{"priority": "Medium"}
	case "map_type_to_beads":
		result = map[string]string{"issue_type": "task"}
	case "map_type_from_beads":
		result = map[string]string{"issue_type": "Task"}

	default:
		writeError(id, -32601, fmt.Sprintf("unknown tool: %s", call.Name))
		return
	}

	// Wrap in MCP content array.
	encoded, _ := json.Marshal(result)
	writeResult(id, map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": string(encoded)},
		},
	})
}

func writeResult(id interface{}, result interface{}) {
	resp := jsonrpcResponse{JSONRPC: "2.0", ID: id, Result: result}
	data, _ := json.Marshal(resp)
	fmt.Println(string(data))
}

func writeError(id interface{}, code int, msg string) {
	resp := jsonrpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
	data, _ := json.Marshal(resp)
	fmt.Println(string(data))
}
