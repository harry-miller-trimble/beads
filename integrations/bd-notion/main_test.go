package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// testPipe provides a paired reader/writer for testing the MCP server.
type testPipe struct {
	reqBuf  bytes.Buffer
	respBuf bytes.Buffer
}

func newTestServer() (*server, *testPipe) {
	tp := &testPipe{}
	s := &server{
		reader: bufio.NewReader(&tp.reqBuf),
		writer: &tp.respBuf,
	}
	return s, tp
}

func (tp *testPipe) sendRequest(t *testing.T, id int, method string, params interface{}) {
	t.Helper()
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	tp.reqBuf.Write(data)
	tp.reqBuf.WriteByte('\n')
}

func (tp *testPipe) sendNotification(t *testing.T, method string) {
	t.Helper()
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
	}
	data, _ := json.Marshal(req)
	tp.reqBuf.Write(data)
	tp.reqBuf.WriteByte('\n')
}

func (tp *testPipe) readResponse(t *testing.T) map[string]interface{} {
	t.Helper()
	line, err := bufio.NewReader(&tp.respBuf).ReadBytes('\n')
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", string(line), err)
	}
	return resp
}

func TestMCPInitializeHandshake(t *testing.T) {
	s, tp := newTestServer()
	tp.sendRequest(t, 1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
	})
	tp.sendNotification(t, "notifications/initialized")

	if err := s.run(); err != nil {
		t.Fatal(err)
	}

	resp := tp.readResponse(t)
	if resp["error"] != nil {
		t.Fatalf("expected no error, got %v", resp["error"])
	}
	result := resp["result"].(map[string]interface{})
	serverInfo := result["serverInfo"].(map[string]interface{})
	if serverInfo["name"] != "bd-notion" {
		t.Errorf("server name = %q, want bd-notion", serverInfo["name"])
	}
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %q, want 2024-11-05", result["protocolVersion"])
	}
}

func TestMCPToolsList(t *testing.T) {
	s, tp := newTestServer()
	tp.sendRequest(t, 1, "tools/list", nil)

	if err := s.run(); err != nil {
		t.Fatal(err)
	}

	resp := tp.readResponse(t)
	result := resp["result"].(map[string]interface{})
	tools := result["tools"].([]interface{})

	expectedTools := []string{
		"tracker_info", "validate", "fetch_issues", "fetch_issue",
		"create_issue", "update_issue", "is_external_ref",
		"extract_identifier", "build_external_ref",
		"priority_to_beads", "priority_to_tracker",
		"status_to_beads", "status_to_tracker",
		"type_to_beads", "type_to_tracker",
	}

	toolNames := make(map[string]bool)
	for _, t := range tools {
		tool := t.(map[string]interface{})
		toolNames[tool["name"].(string)] = true
	}

	for _, name := range expectedTools {
		if !toolNames[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestToolTrackerInfo(t *testing.T) {
	s, tp := newTestServer()
	tp.sendRequest(t, 1, "tools/call", map[string]interface{}{
		"name": "tracker_info",
	})

	if err := s.run(); err != nil {
		t.Fatal(err)
	}

	result := extractToolResult(t, tp.readResponse(t))
	var info map[string]interface{}
	if err := json.Unmarshal([]byte(result), &info); err != nil {
		t.Fatal(err)
	}
	if info["name"] != "notion" {
		t.Errorf("name = %q, want notion", info["name"])
	}
	if info["display_name"] != "Notion" {
		t.Errorf("display_name = %q, want Notion", info["display_name"])
	}
}

func TestToolValidateNoToken(t *testing.T) {
	s, tp := newTestServer()
	tp.sendRequest(t, 1, "tools/call", map[string]interface{}{
		"name": "validate",
	})

	if err := s.run(); err != nil {
		t.Fatal(err)
	}

	result := extractToolResult(t, tp.readResponse(t))
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(result), &v); err != nil {
		t.Fatal(err)
	}
	if v["ok"] != false {
		t.Errorf("expected ok=false when no token")
	}
	errMsg, _ := v["error"].(string)
	if !strings.Contains(errMsg, "NOTION_TOKEN") {
		t.Errorf("error should mention NOTION_TOKEN, got %q", errMsg)
	}
}

func TestToolIsExternalRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"https://www.notion.so/My-Page-12345678abcdef1234567890abcdef12", true},
		{"https://notion.so/12345678abcdef1234567890abcdef12", true},
		{"https://example.com/page", false},
		{"not-a-url", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			s, tp := newTestServer()
			tp.sendRequest(t, 1, "tools/call", map[string]interface{}{
				"name":      "is_external_ref",
				"arguments": map[string]interface{}{"ref": tc.ref},
			})
			if err := s.run(); err != nil {
				t.Fatal(err)
			}
			result := extractToolResult(t, tp.readResponse(t))
			var got bool
			if err := json.Unmarshal([]byte(result), &got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("is_external_ref(%q) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

func TestToolExtractIdentifier(t *testing.T) {
	s, tp := newTestServer()
	tp.sendRequest(t, 1, "tools/call", map[string]interface{}{
		"name":      "extract_identifier",
		"arguments": map[string]interface{}{"ref": "https://www.notion.so/My-Page-12345678abcdef1234567890abcdef12"},
	})
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	result := extractToolResult(t, tp.readResponse(t))
	var id string
	if err := json.Unmarshal([]byte(result), &id); err != nil {
		t.Fatal(err)
	}
	if id != "12345678-abcd-ef12-3456-7890abcdef12" {
		t.Errorf("got %q", id)
	}
}

func TestToolBuildExternalRef(t *testing.T) {
	s, tp := newTestServer()
	tp.sendRequest(t, 1, "tools/call", map[string]interface{}{
		"name": "build_external_ref",
		"arguments": map[string]interface{}{
			"identifier": "12345678-abcd-ef12-3456-7890abcdef12",
		},
	})
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	result := extractToolResult(t, tp.readResponse(t))
	var url string
	if err := json.Unmarshal([]byte(result), &url); err != nil {
		t.Fatal(err)
	}
	if url != "https://www.notion.so/12345678abcdef1234567890abcdef12" {
		t.Errorf("got %q", url)
	}
}

func TestFieldMapperTools(t *testing.T) {
	tests := []struct {
		tool   string
		input  interface{}
		expect string
	}{
		{"priority_to_beads", "High", "1"},
		{"priority_to_beads", "Critical", "0"},
		{"priority_to_beads", "unknown", "2"},
		{"priority_to_tracker", float64(0), `"Critical"`},
		{"priority_to_tracker", float64(3), `"Low"`},
		{"status_to_beads", "In Progress", `"in_progress"`},
		{"status_to_beads", "Closed", `"closed"`},
		{"status_to_tracker", "blocked", `"Blocked"`},
		{"type_to_beads", "Bug", `"bug"`},
		{"type_to_beads", "Feature", `"feature"`},
		{"type_to_tracker", "epic", `"Epic"`},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s/%v", tc.tool, tc.input), func(t *testing.T) {
			s, tp := newTestServer()
			tp.sendRequest(t, 1, "tools/call", map[string]interface{}{
				"name":      tc.tool,
				"arguments": map[string]interface{}{"value": tc.input},
			})
			if err := s.run(); err != nil {
				t.Fatal(err)
			}
			result := extractToolResult(t, tp.readResponse(t))
			if strings.TrimSpace(result) != tc.expect {
				t.Errorf("%s(%v) = %s, want %s", tc.tool, tc.input, result, tc.expect)
			}
		})
	}
}

func TestTrackerIssueFromPage(t *testing.T) {
	page := Page{
		ID:             "12345678-abcd-ef12-3456-7890abcdef12",
		URL:            "https://www.notion.so/12345678abcdef1234567890abcdef12",
		CreatedTime:    time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
		LastEditedTime: time.Date(2025, 1, 16, 14, 30, 0, 0, time.UTC),
		Properties: map[string]PageProperty{
			"Name":        {Title: []RichText{{PlainText: "Test Issue"}}},
			"Description": {RichText: []RichText{{PlainText: "A test issue"}}},
			"Status":      {Select: &SelectOption{Name: "Open"}},
			"Priority":    {Select: &SelectOption{Name: "High"}},
			"Type":        {Select: &SelectOption{Name: "Bug"}},
			"Assignee":    {RichText: []RichText{{PlainText: "alice"}}},
			"Labels":      {MultiSelect: []SelectOption{{Name: "urgent"}, {Name: "frontend"}}},
		},
	}

	issue := trackerIssueFromPage(page)

	if issue.Title != "Test Issue" {
		t.Errorf("title = %q", issue.Title)
	}
	if issue.Description != "A test issue" {
		t.Errorf("description = %q", issue.Description)
	}
	if issue.Priority != 1 {
		t.Errorf("priority = %d, want 1 (High)", issue.Priority)
	}
	if issue.State != "open" {
		t.Errorf("state = %q, want open", issue.State)
	}
	if issue.Type != "bug" {
		t.Errorf("type = %q, want bug", issue.Type)
	}
	if issue.Assignee != "alice" {
		t.Errorf("assignee = %q", issue.Assignee)
	}
	if len(issue.Labels) != 2 || issue.Labels[0] != "urgent" {
		t.Errorf("labels = %v", issue.Labels)
	}
	if !strings.Contains(issue.URL, "notion.so") {
		t.Errorf("url = %q, should contain notion.so", issue.URL)
	}
}

func TestBuildPagePropertiesFromIssue(t *testing.T) {
	issue := map[string]interface{}{
		"title":       "New Bug",
		"description": "Something broke",
		"status":      "open",
		"priority":    float64(0),
		"issue_type":  "bug",
		"assignee":    "bob",
		"labels":      []interface{}{"critical", "backend"},
	}

	props := buildPagePropertiesFromIssue(issue)

	if _, ok := props["Name"]; !ok {
		t.Error("missing Name property")
	}
	if _, ok := props["Status"]; !ok {
		t.Error("missing Status property")
	}
	if _, ok := props["Priority"]; !ok {
		t.Error("missing Priority property")
	}
	if _, ok := props["Labels"]; !ok {
		t.Error("missing Labels property")
	}

	// Verify priority mapped correctly.
	priorityProp := props["Priority"].(map[string]interface{})
	selectProp := priorityProp["select"].(map[string]interface{})
	if selectProp["name"] != "Critical" {
		t.Errorf("priority name = %q, want Critical", selectProp["name"])
	}
}

func TestUnknownMethod(t *testing.T) {
	s, tp := newTestServer()
	tp.sendRequest(t, 1, "nonexistent/method", nil)

	if err := s.run(); err != nil {
		t.Fatal(err)
	}

	resp := tp.readResponse(t)
	if resp["error"] == nil {
		t.Fatal("expected error for unknown method")
	}
	rpcErr := resp["error"].(map[string]interface{})
	if rpcErr["code"].(float64) != -32601 {
		t.Errorf("error code = %v, want -32601", rpcErr["code"])
	}
}

func TestUnknownTool(t *testing.T) {
	s, tp := newTestServer()
	tp.sendRequest(t, 1, "tools/call", map[string]interface{}{
		"name": "nonexistent_tool",
	})

	if err := s.run(); err != nil {
		t.Fatal(err)
	}

	resp := tp.readResponse(t)
	result := resp["result"].(map[string]interface{})
	if result["isError"] != true {
		t.Error("expected isError=true for unknown tool")
	}
}

func TestMultipleRequestsInSequence(t *testing.T) {
	s, tp := newTestServer()

	// Send initialize + tools/list + tracker_info in sequence.
	tp.sendRequest(t, 1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
	})
	tp.sendNotification(t, "notifications/initialized")
	tp.sendRequest(t, 2, "tools/list", nil)
	tp.sendRequest(t, 3, "tools/call", map[string]interface{}{
		"name": "tracker_info",
	})

	if err := s.run(); err != nil {
		t.Fatal(err)
	}

	// Read all three responses.
	reader := bufio.NewReader(&tp.respBuf)
	for i := 0; i < 3; i++ {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			t.Fatalf("response %d: %v", i+1, err)
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(line, &resp); err != nil {
			t.Fatalf("response %d unmarshal: %v", i+1, err)
		}
		if resp["error"] != nil {
			t.Errorf("response %d has error: %v", i+1, resp["error"])
		}
	}
}

// extractToolResult parses the MCP tool result and returns the text content.
func extractToolResult(t *testing.T, resp map[string]interface{}) string {
	t.Helper()
	result := resp["result"].(map[string]interface{})
	if result["isError"] == true {
		content := result["content"].([]interface{})
		text := content[0].(map[string]interface{})["text"].(string)
		t.Fatalf("tool error: %s", text)
	}
	content := result["content"].([]interface{})
	return content[0].(map[string]interface{})["text"].(string)
}
