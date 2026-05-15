package automations

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/plugin"
	"github.com/steveyegge/beads/internal/types"
)

// TestEndToEndPluginHook exercises the full lifecycle:
//
//	manifest → install → grant → verify → WASM load → hook fire
//
// This proves the trust layer and WASM runtime work together end-to-end.
func TestEndToEndPluginHook(t *testing.T) {
	root := t.TempDir()
	paths := plugin.PathsFromRoot(root)

	// --- Build the plugin source package ---
	pluginSrcDir := filepath.Join(root, "src", "hello-hook")
	if err := os.MkdirAll(pluginSrcDir, 0755); err != nil {
		t.Fatal(err)
	}

	manifest := &plugin.Manifest{
		Name:         "hello-hook",
		Version:      "0.1.0",
		Tier:         plugin.TierAutomation,
		Description:  "Tiny test hook that returns 0 on create",
		Entrypoint:   "hello-hook.wasm",
		Capabilities: []plugin.Capability{plugin.CapHookExecute},
	}
	if err := plugin.WriteManifest(filepath.Join(pluginSrcDir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}

	wasmBytes := buildHookModuleBytes()
	if err := os.WriteFile(filepath.Join(pluginSrcDir, "hello-hook.wasm"), wasmBytes, 0644); err != nil {
		t.Fatal(err)
	}

	// --- Install via trust layer ---
	mgr, err := plugin.NewManager(paths)
	if err != nil {
		t.Fatal(err)
	}

	entry, err := mgr.InstallLocal(pluginSrcDir)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if entry.Name != "hello-hook" {
		t.Fatalf("expected name hello-hook, got %s", entry.Name)
	}
	if entry.Tier != plugin.TierAutomation {
		t.Fatalf("expected tier automation, got %s", entry.Tier)
	}
	if entry.Digest == "" {
		t.Fatal("expected non-empty digest")
	}

	// --- Grant hooks.execute ---
	if !mgr.Grants.AddGrant("hello-hook", CapHookExecute, "test") {
		t.Fatal("expected grant to be added (was not)")
	}
	if err := mgr.Grants.Save(); err != nil {
		t.Fatal(err)
	}

	// --- Verify installation integrity ---
	problems := mgr.Verify()
	if len(problems) > 0 {
		t.Fatalf("verify found problems: %v", problems)
	}

	// --- Create WASM runtime + hook runner ---
	ctx := context.Background()
	rt, err := NewRuntime(ctx, filepath.Join(root, "kv"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx) //nolint:errcheck

	runner, err := NewWASMHookRunner(rt, paths)
	if err != nil {
		t.Fatalf("hook runner: %v", err)
	}

	// --- Fire the hook ---
	issue := &types.Issue{
		ID:     "test-123",
		Title:  "End-to-end test issue",
		Status: "open",
	}

	err = runner.RunSync("create", issue)
	if err != nil {
		t.Fatalf("RunSync create: %v", err)
	}

	// Hook exists should return true.
	if !runner.HookExists("create") {
		t.Fatal("expected HookExists(create) = true")
	}

	// Non-existent hook should return false.
	if runner.HookExists("nonexistent") {
		t.Fatal("expected HookExists(nonexistent) = false")
	}
}

// TestEndToEndNoGrant verifies that plugins without grants are skipped.
func TestEndToEndNoGrant(t *testing.T) {
	root := t.TempDir()
	paths := plugin.PathsFromRoot(root)

	pluginSrcDir := filepath.Join(root, "src", "ungrantd")
	if err := os.MkdirAll(pluginSrcDir, 0755); err != nil {
		t.Fatal(err)
	}

	manifest := &plugin.Manifest{
		Name:         "ungrantd",
		Version:      "0.1.0",
		Tier:         plugin.TierAutomation,
		Entrypoint:   "ungrantd.wasm",
		Capabilities: []plugin.Capability{plugin.CapHookExecute},
	}
	if err := plugin.WriteManifest(filepath.Join(pluginSrcDir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	wasmBytes := buildHookModuleBytes()
	if err := os.WriteFile(filepath.Join(pluginSrcDir, "ungrantd.wasm"), wasmBytes, 0644); err != nil {
		t.Fatal(err)
	}

	mgr, err := plugin.NewManager(paths)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.InstallLocal(pluginSrcDir); err != nil {
		t.Fatal(err)
	}
	// Deliberately NOT granting hooks.execute.

	ctx := context.Background()
	rt, err := NewRuntime(ctx, filepath.Join(root, "kv"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx) //nolint:errcheck

	runner, err := NewWASMHookRunner(rt, paths)
	if err != nil {
		t.Fatal(err)
	}

	issue := &types.Issue{ID: "test-456", Title: "No grant", Status: "open"}
	err = runner.RunSync("create", issue)
	if err != nil {
		t.Fatalf("expected no error (plugin skipped), got: %v", err)
	}
}

// TestEndToEndRemovePlugin verifies install → remove → hook no longer fires.
func TestEndToEndRemovePlugin(t *testing.T) {
	root := t.TempDir()
	paths := plugin.PathsFromRoot(root)

	pluginSrcDir := filepath.Join(root, "src", "removeme")
	if err := os.MkdirAll(pluginSrcDir, 0755); err != nil {
		t.Fatal(err)
	}

	manifest := &plugin.Manifest{
		Name:         "removeme",
		Version:      "0.1.0",
		Tier:         plugin.TierAutomation,
		Entrypoint:   "removeme.wasm",
		Capabilities: []plugin.Capability{plugin.CapHookExecute},
	}
	if err := plugin.WriteManifest(filepath.Join(pluginSrcDir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	wasmBytes := buildHookModuleBytes()
	if err := os.WriteFile(filepath.Join(pluginSrcDir, "removeme.wasm"), wasmBytes, 0644); err != nil {
		t.Fatal(err)
	}

	mgr, err := plugin.NewManager(paths)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.InstallLocal(pluginSrcDir); err != nil {
		t.Fatal(err)
	}
	mgr.Grants.AddGrant("removeme", CapHookExecute, "test")
	if err := mgr.Grants.Save(); err != nil {
		t.Fatal(err)
	}

	// Remove the plugin.
	if err := mgr.Remove("removeme"); err != nil {
		t.Fatal(err)
	}

	// Hook runner should find no plugins now.
	ctx := context.Background()
	rt, err := NewRuntime(ctx, filepath.Join(root, "kv"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx) //nolint:errcheck

	runner, err := NewWASMHookRunner(rt, paths)
	if err != nil {
		t.Fatal(err)
	}

	if runner.HookExists("create") {
		t.Fatal("expected HookExists = false after removal")
	}

	issue := &types.Issue{ID: "test-789", Title: "After removal", Status: "open"}
	err = runner.RunSync("create", issue)
	if err != nil {
		t.Fatalf("expected no error (no plugins), got: %v", err)
	}
}
