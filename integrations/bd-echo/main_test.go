package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// TestEchoDriverProtocol verifies the driver speaks valid MCP JSON-RPC 2.0.
func TestEchoDriverProtocol(t *testing.T) {
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = "."

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	reader := bufio.NewReader(stdout)

	// Helper: send request, read response.
	send := func(method string, id int, params interface{}) map[string]interface{} {
		t.Helper()
		req := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  method,
		}
		if params != nil {
			req["params"] = params
		}
		data, _ := json.Marshal(req)
		data = append(data, '\n')
		if _, err := stdin.Write(data); err != nil {
			t.Fatalf("write %s: %v", method, err)
		}

		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			t.Fatalf("read %s: %v", method, err)
		}
		var resp map[string]interface{}
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("parse %s response: %v\nraw: %s", method, err, line)
		}
		return resp
	}

	// 1. Initialize handshake.
	resp := send("initialize", 1, map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
	})
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %v", resp)
	}
	serverInfo := result["serverInfo"].(map[string]interface{})
	if serverInfo["name"] != "bd-echo" {
		t.Fatalf("expected server name bd-echo, got %v", serverInfo["name"])
	}

	// 2. List tools.
	resp = send("tools/list", 2, nil)
	result = resp["result"].(map[string]interface{})
	toolsList := result["tools"].([]interface{})
	if len(toolsList) < 10 {
		t.Fatalf("expected >= 10 tools, got %d", len(toolsList))
	}

	// 3. Call tracker_info.
	resp = send("tools/call", 3, map[string]interface{}{
		"name": "tracker_info",
	})
	content := extractContent(t, resp)
	if !strings.Contains(content, `"name":"echo"`) {
		t.Fatalf("tracker_info didn't return echo: %s", content)
	}

	// 4. Call validate.
	resp = send("tools/call", 4, map[string]interface{}{
		"name": "validate",
	})
	content = extractContent(t, resp)
	if !strings.Contains(content, `"ok":true`) {
		t.Fatalf("validate didn't return ok: %s", content)
	}

	// 5. Call fetch_issues.
	resp = send("tools/call", 5, map[string]interface{}{
		"name": "fetch_issues",
	})
	content = extractContent(t, resp)
	if !strings.Contains(content, "ECHO-1") {
		t.Fatalf("fetch_issues didn't return ECHO-1: %s", content)
	}

	// 6. Call fetch_issue.
	resp = send("tools/call", 6, map[string]interface{}{
		"name":      "fetch_issue",
		"arguments": map[string]string{"identifier": "ECHO-42"},
	})
	content = extractContent(t, resp)
	if !strings.Contains(content, "ECHO-42") {
		t.Fatalf("fetch_issue didn't return ECHO-42: %s", content)
	}

	// 7. Call is_external_ref.
	resp = send("tools/call", 7, map[string]interface{}{
		"name":      "is_external_ref",
		"arguments": map[string]string{"ref": "echo://ECHO-1"},
	})
	content = extractContent(t, resp)
	if content != "true" {
		t.Fatalf("is_external_ref didn't return true: %s", content)
	}

	// 8. Non-matching ref.
	resp = send("tools/call", 8, map[string]interface{}{
		"name":      "is_external_ref",
		"arguments": map[string]string{"ref": "jira://FOO-1"},
	})
	content = extractContent(t, resp)
	if content != "false" {
		t.Fatalf("is_external_ref should return false for jira://: %s", content)
	}
}

func extractContent(t *testing.T, resp map[string]interface{}) string {
	t.Helper()
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %v", resp)
	}
	contentArr, ok := result["content"].([]interface{})
	if !ok || len(contentArr) == 0 {
		t.Fatalf("expected content array, got %v", result)
	}
	item := contentArr[0].(map[string]interface{})
	text, _ := item["text"].(string)
	return text
}
