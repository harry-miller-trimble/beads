package tracker_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/steveyegge/beads/internal/plugin"
	"github.com/steveyegge/beads/internal/tracker"
)

// TestTrackerDriverEndToEnd exercises the full tracker driver lifecycle:
//
//	build driver → install → grant → create adapter → call IssueTracker methods
//
// This proves that external tracker drivers work end-to-end through the
// trust layer and MCP adapter.
func TestTrackerDriverEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Build the echo driver binary.
	echoSrc := filepath.Join("..", "..", "integrations", "bd-echo")
	if _, err := os.Stat(filepath.Join(echoSrc, "main.go")); err != nil {
		t.Skipf("bd-echo source not found at %s: %v", echoSrc, err)
	}

	tmpBuild := t.TempDir()
	binaryName := "bd-echo"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(tmpBuild, binaryName)

	buildCmd := exec.Command("go", "build", "-o", binaryPath, ".")
	buildCmd.Dir = echoSrc
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build bd-echo: %v\n%s", err, out)
	}

	// Create plugin source directory with manifest + binary.
	pluginDir := filepath.Join(tmpBuild, "plugin-src")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}

	manifest := &plugin.Manifest{
		Name:         "bd-echo",
		Version:      "0.1.0",
		Tier:         plugin.TierProvider,
		Description:  "Test tracker driver",
		Entrypoint:   binaryName,
		Capabilities: []plugin.Capability{plugin.CapTrackerRead, plugin.CapTrackerWrite},
	}
	if err := plugin.WriteManifest(filepath.Join(pluginDir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	// Copy binary to plugin source.
	data, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, binaryName), data, 0755); err != nil {
		t.Fatal(err)
	}

	// Install via trust layer.
	root := t.TempDir()
	paths := plugin.PathsFromRoot(root)
	mgr, err := plugin.NewManager(paths)
	if err != nil {
		t.Fatal(err)
	}

	entry, err := mgr.InstallLocal(pluginDir)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	t.Logf("installed %s (digest=%s)", entry.Name, entry.Digest)

	// Grant tracker.read.
	mgr.Grants.AddGrant("bd-echo", plugin.CapTrackerRead, "test")
	if err := mgr.Grants.Save(); err != nil {
		t.Fatal(err)
	}

	// Verify integrity.
	problems := mgr.Verify()
	if len(problems) > 0 {
		t.Fatalf("verify: %v", problems)
	}

	// Create MCP adapter — this starts the driver process and does the handshake.
	ctx := context.Background()
	entrypointPath := filepath.Join(paths.PluginCacheDir("bd-echo"), binaryName)

	adapter, err := tracker.NewMCPAdapter(ctx, tracker.MCPAdapterConfig{
		PluginName: "bd-echo",
		Command:    entrypointPath,
		Manifest:   manifest,
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	defer adapter.Close()

	// --- Test IssueTracker interface methods ---

	// Name/DisplayName/ConfigPrefix
	if adapter.Name() != "echo" {
		t.Errorf("Name() = %q, want %q", adapter.Name(), "echo")
	}
	if adapter.DisplayName() != "Echo (Test Driver)" {
		t.Errorf("DisplayName() = %q, want %q", adapter.DisplayName(), "Echo (Test Driver)")
	}

	// Validate
	if err := adapter.Validate(); err != nil {
		t.Errorf("Validate() = %v", err)
	}

	// FetchIssues
	issues, err := adapter.FetchIssues(ctx, tracker.FetchOptions{})
	if err != nil {
		t.Fatalf("FetchIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("FetchIssues returned %d issues, want 1", len(issues))
	}
	if issues[0].ID != "ECHO-1" {
		t.Errorf("FetchIssues[0].ID = %q, want ECHO-1", issues[0].ID)
	}
	t.Logf("FetchIssues: got %d issue(s), first=%s", len(issues), issues[0].ID)

	// FetchIssue
	issue, err := adapter.FetchIssue(ctx, "ECHO-42")
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if issue == nil {
		t.Fatal("FetchIssue returned nil")
	}
	if issue.ID != "ECHO-42" {
		t.Errorf("FetchIssue.ID = %q, want ECHO-42", issue.ID)
	}
	t.Logf("FetchIssue: %s - %s", issue.ID, issue.Title)

	// IsExternalRef
	if !adapter.IsExternalRef("echo://ECHO-1") {
		t.Error("IsExternalRef(echo://ECHO-1) = false, want true")
	}
	if adapter.IsExternalRef("jira://FOO-1") {
		t.Error("IsExternalRef(jira://FOO-1) = true, want false")
	}

	// ExtractIdentifier
	id := adapter.ExtractIdentifier("echo://ECHO-1")
	if id != "ECHO-1" {
		t.Errorf("ExtractIdentifier = %q, want ECHO-1", id)
	}

	t.Log("✓ All IssueTracker interface methods work through the tracker driver protocol")
}
