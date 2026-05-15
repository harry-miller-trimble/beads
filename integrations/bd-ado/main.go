// bd-ado is a standalone tracker driver for Azure DevOps.
// It speaks MCP (JSON-RPC 2.0 over stdio) and implements the beads IssueTracker
// interface by delegating to the Azure DevOps REST API.
//
// Configuration is via environment variables:
//
//	AZURE_DEVOPS_PAT       - Personal access token (required)
//	AZURE_DEVOPS_ORG       - Organization name (required unless AZURE_DEVOPS_URL set)
//	AZURE_DEVOPS_URL       - Custom base URL (optional, overrides org)
//	AZURE_DEVOPS_PROJECTS  - Comma-separated project names
//	AZURE_DEVOPS_PROJECT   - Single project name (fallback)
//
// Usage:
//
//	bd plugin install ./integrations/bd-ado
//	bd plugin trust bd-ado tracker.read tracker.write
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const serverName = "bd-ado"
const serverVersion = "0.1.0"

// tracker is the singleton ADO tracker instance, initialized on first use.
var tracker *Tracker

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
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
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var tools = []map[string]interface{}{
	{"name": "tracker_info", "description": "Returns tracker metadata", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{"name": "validate", "description": "Validates ADO configuration and connectivity", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{"name": "fetch_issues", "description": "Fetches work items from Azure DevOps", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"state": map[string]string{"type": "string", "description": "Filter by state"},
		"since": map[string]string{"type": "string", "description": "ISO 8601 timestamp for incremental sync"},
		"limit": map[string]string{"type": "integer", "description": "Max results"},
	}}},
	{"name": "fetch_issue", "description": "Fetches a single work item by ID", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"identifier": map[string]string{"type": "string", "description": "Work item numeric ID"},
	}, "required": []string{"identifier"}}},
	{"name": "create_issue", "description": "Creates a new work item", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"issue": map[string]string{"type": "object", "description": "BeadsIssue to create"},
	}}},
	{"name": "update_issue", "description": "Updates an existing work item", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"external_id": map[string]string{"type": "string", "description": "ADO work item ID"},
		"issue":       map[string]string{"type": "object", "description": "BeadsIssue fields to update"},
	}}},
	{"name": "is_external_ref", "description": "Checks if a ref belongs to this ADO tracker", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"ref": map[string]string{"type": "string"},
	}}},
	{"name": "extract_identifier", "description": "Extracts work item ID from an ADO URL or ref", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"ref": map[string]string{"type": "string"},
	}}},
	{"name": "build_external_ref", "description": "Builds an ADO URL for a tracker issue", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"id":         map[string]string{"type": "string"},
		"identifier": map[string]string{"type": "string"},
		"url":        map[string]string{"type": "string"},
	}}},
	// Field mapping tools
	{"name": "priority_to_beads", "description": "Maps ADO priority to beads priority", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"value": map[string]string{"type": "number"},
	}}},
	{"name": "priority_to_tracker", "description": "Maps beads priority to ADO priority", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"value": map[string]string{"type": "integer"},
	}}},
	{"name": "status_to_beads", "description": "Maps ADO state to beads status", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"value": map[string]string{"type": "string"},
	}}},
	{"name": "status_to_tracker", "description": "Maps beads status to ADO state", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"value": map[string]string{"type": "string"},
	}}},
	{"name": "type_to_beads", "description": "Maps ADO work item type to beads type", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"value": map[string]string{"type": "string"},
	}}},
	{"name": "type_to_tracker", "description": "Maps beads type to ADO work item type", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"value": map[string]string{"type": "string"},
	}}},
	{"name": "issue_to_beads", "description": "Converts a TrackerIssue to a BeadsIssue with dependencies", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"issue": map[string]string{"type": "object"},
	}}},
	{"name": "issue_to_tracker", "description": "Converts a BeadsIssue to ADO field map", "inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"issue": map[string]string{"type": "object"},
	}}},
}

func main() {
	logf("starting tracker driver")

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
			initTracker()
			writeResult(req.ID, map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{"tools": map[string]bool{}},
				"serverInfo":      map[string]string{"name": serverName, "version": serverVersion},
			})

		case "notifications/initialized":
			// No response needed.

		case "tools/list":
			writeResult(req.ID, map[string]interface{}{"tools": tools})

		case "tools/call":
			handleToolCall(req.ID, req.Params)

		case "shutdown":
			if tracker != nil {
				_ = tracker.Close()
			}
			writeResult(req.ID, nil)
			os.Exit(0)

		default:
			writeError(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
		}
	}
}

func initTracker() {
	if tracker != nil {
		return
	}
	tracker = &Tracker{}
	if err := tracker.Init(context.Background()); err != nil {
		logf("warning: init deferred — %v", err)
		tracker = nil
	} else {
		logf("connected to %s (projects: %v)", tracker.org, tracker.projects)
	}
}

func ensureTracker(id interface{}) bool {
	if tracker != nil {
		return true
	}
	initTracker()
	if tracker == nil {
		writeToolError(id, "tracker not initialized — check AZURE_DEVOPS_PAT, AZURE_DEVOPS_ORG, AZURE_DEVOPS_PROJECT env vars")
		return false
	}
	return true
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

	switch call.Name {
	case "tracker_info":
		writeToolResult(id, map[string]string{
			"name":          "ado",
			"display_name":  "Azure DevOps",
			"config_prefix": "ado",
		})

	case "validate":
		if !ensureTracker(id) {
			return
		}
		if err := tracker.Validate(); err != nil {
			writeToolResult(id, map[string]interface{}{"ok": false, "error": err.Error()})
		} else {
			writeToolResult(id, map[string]interface{}{"ok": true})
		}

	case "fetch_issues":
		if !ensureTracker(id) {
			return
		}
		handleFetchIssues(id, call.Arguments)

	case "fetch_issue":
		if !ensureTracker(id) {
			return
		}
		handleFetchIssue(id, call.Arguments)

	case "create_issue":
		if !ensureTracker(id) {
			return
		}
		handleCreateIssue(id, call.Arguments)

	case "update_issue":
		if !ensureTracker(id) {
			return
		}
		handleUpdateIssue(id, call.Arguments)

	case "is_external_ref":
		if !ensureTracker(id) {
			return
		}
		ref, _ := call.Arguments["ref"].(string)
		writeToolResult(id, tracker.IsExternalRef(ref))

	case "extract_identifier":
		if !ensureTracker(id) {
			return
		}
		ref, _ := call.Arguments["ref"].(string)
		writeToolResult(id, tracker.ExtractIdentifier(ref))

	case "build_external_ref":
		if !ensureTracker(id) {
			return
		}
		ti := &TrackerIssue{
			ID:         stringArg(call.Arguments, "id"),
			Identifier: stringArg(call.Arguments, "identifier"),
			URL:        stringArg(call.Arguments, "url"),
		}
		writeToolResult(id, tracker.BuildExternalRef(ti))

	// Field mapping tools
	case "priority_to_beads":
		if !ensureTracker(id) {
			return
		}
		writeToolResult(id, tracker.FieldMapper().PriorityToBeads(call.Arguments["value"]))

	case "priority_to_tracker":
		if !ensureTracker(id) {
			return
		}
		p := intArg(call.Arguments, "value")
		writeToolResult(id, tracker.FieldMapper().PriorityToTracker(p))

	case "status_to_beads":
		if !ensureTracker(id) {
			return
		}
		writeToolResult(id, tracker.FieldMapper().StatusToBeads(call.Arguments["value"]))

	case "status_to_tracker":
		if !ensureTracker(id) {
			return
		}
		s, _ := call.Arguments["value"].(string)
		writeToolResult(id, tracker.FieldMapper().StatusToTracker(s))

	case "type_to_beads":
		if !ensureTracker(id) {
			return
		}
		writeToolResult(id, tracker.FieldMapper().TypeToBeads(call.Arguments["value"]))

	case "type_to_tracker":
		if !ensureTracker(id) {
			return
		}
		t, _ := call.Arguments["value"].(string)
		writeToolResult(id, tracker.FieldMapper().TypeToTracker(t))

	case "issue_to_beads":
		if !ensureTracker(id) {
			return
		}
		handleIssueToBeads(id, call.Arguments)

	case "issue_to_tracker":
		if !ensureTracker(id) {
			return
		}
		handleIssueToTracker(id, call.Arguments)

	default:
		writeError(id, -32601, fmt.Sprintf("unknown tool: %s", call.Name))
	}
}

func handleFetchIssues(id interface{}, args map[string]interface{}) {
	var opts FetchOptions
	if s, ok := args["state"].(string); ok {
		opts.State = s
	}
	if since, ok := args["since"].(string); ok && since != "" {
		if ts, err := time.Parse(time.RFC3339, since); err == nil {
			opts.Since = &ts
		}
	}
	if limit, ok := args["limit"].(float64); ok {
		opts.Limit = int(limit)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	issues, err := tracker.FetchIssues(ctx, opts)
	if err != nil {
		writeToolError(id, fmt.Sprintf("fetch_issues: %v", err))
		return
	}
	writeToolResult(id, issues)
}

func handleFetchIssue(id interface{}, args map[string]interface{}) {
	identifier, _ := args["identifier"].(string)
	if identifier == "" {
		writeToolError(id, "identifier required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	issue, err := tracker.FetchIssue(ctx, identifier)
	if err != nil {
		writeToolError(id, fmt.Sprintf("fetch_issue: %v", err))
		return
	}
	if issue == nil {
		writeToolResult(id, nil)
		return
	}
	writeToolResult(id, issue)
}

func handleCreateIssue(id interface{}, args map[string]interface{}) {
	issueData, ok := args["issue"]
	if !ok {
		writeToolError(id, "issue required")
		return
	}

	raw, err := json.Marshal(issueData)
	if err != nil {
		writeToolError(id, fmt.Sprintf("marshal issue: %v", err))
		return
	}
	var issue BeadsIssue
	if err := json.Unmarshal(raw, &issue); err != nil {
		writeToolError(id, fmt.Sprintf("parse issue: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := tracker.CreateIssue(ctx, &issue)
	if err != nil {
		writeToolError(id, fmt.Sprintf("create_issue: %v", err))
		return
	}
	writeToolResult(id, result)
}

func handleUpdateIssue(id interface{}, args map[string]interface{}) {
	externalID, _ := args["external_id"].(string)
	if externalID == "" {
		writeToolError(id, "external_id required")
		return
	}

	issueData, ok := args["issue"]
	if !ok {
		writeToolError(id, "issue required")
		return
	}

	raw, err := json.Marshal(issueData)
	if err != nil {
		writeToolError(id, fmt.Sprintf("marshal issue: %v", err))
		return
	}
	var issue BeadsIssue
	if err := json.Unmarshal(raw, &issue); err != nil {
		writeToolError(id, fmt.Sprintf("parse issue: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := tracker.UpdateIssue(ctx, externalID, &issue)
	if err != nil {
		writeToolError(id, fmt.Sprintf("update_issue: %v", err))
		return
	}
	writeToolResult(id, result)
}

func handleIssueToBeads(id interface{}, args map[string]interface{}) {
	issueData, ok := args["issue"]
	if !ok {
		writeToolError(id, "issue required")
		return
	}

	raw, err := json.Marshal(issueData)
	if err != nil {
		writeToolError(id, fmt.Sprintf("marshal issue: %v", err))
		return
	}
	var ti TrackerIssue
	if err := json.Unmarshal(raw, &ti); err != nil {
		writeToolError(id, fmt.Sprintf("parse issue: %v", err))
		return
	}

	result := tracker.FieldMapper().IssueToBeads(&ti)
	writeToolResult(id, result)
}

func handleIssueToTracker(id interface{}, args map[string]interface{}) {
	issueData, ok := args["issue"]
	if !ok {
		writeToolError(id, "issue required")
		return
	}

	raw, err := json.Marshal(issueData)
	if err != nil {
		writeToolError(id, fmt.Sprintf("marshal issue: %v", err))
		return
	}
	var issue BeadsIssue
	if err := json.Unmarshal(raw, &issue); err != nil {
		writeToolError(id, fmt.Sprintf("parse issue: %v", err))
		return
	}

	result := tracker.FieldMapper().IssueToTracker(&issue)
	writeToolResult(id, result)
}

// --- helpers ---

func stringArg(args map[string]interface{}, key string) string {
	v, _ := args[key].(string)
	return v
}

func intArg(args map[string]interface{}, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func writeToolResult(id interface{}, result interface{}) {
	encoded, _ := json.Marshal(result)
	writeResult(id, map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": string(encoded)},
		},
	})
}

func writeToolError(id interface{}, msg string) {
	writeResult(id, map[string]interface{}{
		"isError": true,
		"content": []map[string]interface{}{
			{"type": "text", "text": msg},
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

func logf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[%s] %s\n", serverName, fmt.Sprintf(format, args...))
}
