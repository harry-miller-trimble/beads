package automations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// Minimal WASM modules built by hand.
// These are valid WebAssembly binaries (magic + version + sections).

// minimalModule is a valid WASM module with no exports (empty).
var minimalModule = []byte{
	0x00, 0x61, 0x73, 0x6d, // magic: \0asm
	0x01, 0x00, 0x00, 0x00, // version: 1
}

// buildHookModule creates a WASM module that:
//   - imports the beads host module functions
//   - exports hook functions (on_create, on_update, on_close)
//   - each hook returns 0 (success)
//
// This is built using the WAT text format compiled via wazero.
func buildHookModule(t *testing.T) []byte {
	t.Helper()

	// Construct a minimal WASM module with exports using the binary encoding.
	// We use wazero's own module compilation to validate, but we need the
	// binary format. The simplest approach: build the module by hand.
	//
	// Module layout:
	//   Type section: one func type () -> (i32)
	//   Function section: three functions
	//   Export section: on_create, on_update, on_close
	//   Code section: three functions returning 0

	// Type section: 1 type - () -> (i32)
	typeSection := []byte{
		0x01,       // section id: type
		0x05,       // section size
		0x01,       // num types
		0x60,       // func type
		0x00,       // 0 params
		0x01, 0x7f, // 1 result: i32
	}

	// Function section: 3 functions, all type index 0
	funcSection := []byte{
		0x03,             // section id: function
		0x04,             // section size
		0x03,             // num functions
		0x00, 0x00, 0x00, // all use type 0
	}

	// Memory section: 1 memory (1 page min, no max)
	memSection := []byte{
		0x05,       // section id: memory
		0x03,       // section size
		0x01,       // num memories
		0x00, 0x01, // limits: no max, 1 page min
	}

	// Export section: 3 exports + memory
	exportNames := []string{"on_create", "on_update", "on_close"}
	var exportData []byte
	exportData = append(exportData, byte(len(exportNames)+1)) // num exports

	for i, name := range exportNames {
		exportData = append(exportData, byte(len(name)))
		exportData = append(exportData, []byte(name)...)
		exportData = append(exportData, 0x00)    // export kind: func
		exportData = append(exportData, byte(i)) // func index
	}
	// Export memory
	exportData = append(exportData, 0x06) // "memory" length
	exportData = append(exportData, []byte("memory")...)
	exportData = append(exportData, 0x02) // export kind: memory
	exportData = append(exportData, 0x00) // memory index

	exportSection := []byte{0x07, byte(len(exportData))}
	exportSection = append(exportSection, exportData...)

	// Code section: 3 functions, each returns i32 const 0
	// Function body: [body_size] [num_locals] [i32.const 0] [end]
	funcBody := []byte{
		0x04,       // body size: 4 bytes follow
		0x00,       // 0 local declarations
		0x41, 0x00, // i32.const 0
		0x0b, // end
	}
	codeData := []byte{0x03} // num functions
	for i := 0; i < 3; i++ {
		codeData = append(codeData, funcBody...)
	}
	codeSection := []byte{0x0a, byte(len(codeData))}
	codeSection = append(codeSection, codeData...)

	var module []byte
	module = append(module, 0x00, 0x61, 0x73, 0x6d) // magic
	module = append(module, 0x01, 0x00, 0x00, 0x00) // version 1
	module = append(module, typeSection...)
	module = append(module, funcSection...)
	module = append(module, memSection...)
	module = append(module, exportSection...)
	module = append(module, codeSection...)

	return module
}

func TestNewRuntime(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	rt, err := NewRuntime(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx) //nolint:errcheck

	if rt.KV == nil {
		t.Error("KV store is nil")
	}
	if rt.Logger == nil {
		t.Error("Logger is nil")
	}
}

func TestLoadMinimalModule(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	rt, err := NewRuntime(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx) //nolint:errcheck

	mod, err := rt.LoadModule(ctx, "empty", minimalModule)
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(ctx) //nolint:errcheck

	if len(mod.Hooks()) != 0 {
		t.Errorf("expected no hooks, got %v", mod.Hooks())
	}
}

func TestLoadHookModule(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	rt, err := NewRuntime(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx) //nolint:errcheck

	wasmBytes := buildHookModule(t)
	mod, err := rt.LoadModule(ctx, "test-hooks", wasmBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(ctx) //nolint:errcheck

	if !mod.HasHook("on_create") {
		t.Error("missing on_create hook")
	}
	if !mod.HasHook("on_update") {
		t.Error("missing on_update hook")
	}
	if !mod.HasHook("on_close") {
		t.Error("missing on_close hook")
	}
	if mod.HasHook("on_nonexistent") {
		t.Error("should not have on_nonexistent")
	}
}

func TestCallHook(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	rt, err := NewRuntime(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx) //nolint:errcheck

	wasmBytes := buildHookModule(t)
	mod, err := rt.LoadModule(ctx, "test-hooks", wasmBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(ctx) //nolint:errcheck

	issue := &types.Issue{
		ID:    "test-1",
		Title: "Test Issue",
	}

	rc, err := mod.CallHook(ctx, "on_create", issue)
	if err != nil {
		t.Fatal(err)
	}
	if rc != 0 {
		t.Errorf("on_create returned %d, want 0", rc)
	}
}

func TestCallHook_NotFound(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	rt, err := NewRuntime(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx) //nolint:errcheck

	mod, err := rt.LoadModule(ctx, "empty", minimalModule)
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(ctx) //nolint:errcheck

	_, err = mod.CallHook(ctx, "on_create", &types.Issue{})
	if err == nil {
		t.Error("expected error for missing hook")
	}
}

func TestKVStore(t *testing.T) {
	dir := t.TempDir()
	kv, err := NewKVStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Set and get.
	if err := kv.Set("plugin1:key1", []byte("value1")); err != nil {
		t.Fatal(err)
	}
	val, err := kv.Get("plugin1:key1")
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != "value1" {
		t.Errorf("got %q, want value1", val)
	}

	// Get missing key.
	val, err = kv.Get("plugin1:missing")
	if err != nil {
		t.Fatal(err)
	}
	if val != nil {
		t.Errorf("expected nil for missing key, got %q", val)
	}

	// Cross-plugin isolation: different namespace.
	val, err = kv.Get("plugin2:key1")
	if err != nil {
		t.Fatal(err)
	}
	if val != nil {
		t.Error("plugin2 should not see plugin1's keys")
	}

	// Keys listing.
	kv.Set("plugin1:key2", []byte("v2")) //nolint:errcheck
	keys := kv.Keys("plugin1")
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d: %v", len(keys), keys)
	}
	keys = kv.Keys("plugin2")
	if len(keys) != 0 {
		t.Errorf("expected 0 keys for plugin2, got %d", len(keys))
	}

	// Delete.
	if err := kv.Delete("plugin1:key1"); err != nil {
		t.Fatal(err)
	}
	val, err = kv.Get("plugin1:key1")
	if err != nil {
		t.Fatal(err)
	}
	if val != nil {
		t.Error("key should be deleted")
	}
}

func TestKVStore_Persistence(t *testing.T) {
	dir := t.TempDir()

	// Write.
	kv1, err := NewKVStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv1.Set("p:data", []byte(`{"count":42}`)); err != nil {
		t.Fatal(err)
	}

	// Read from new instance.
	kv2, err := NewKVStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	val, err := kv2.Get("p:data")
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != `{"count":42}` {
		// JSON round-trip may change formatting; compare parsed values.
		var got, want map[string]interface{}
		json.Unmarshal(val, &got)
		json.Unmarshal([]byte(`{"count":42}`), &want)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("persistence failed: got %q", val)
		}
	}
}

func TestRuntimeClose(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err := rt.Close(ctx); err != nil {
		t.Fatal(err)
	}

	// Double close should be safe.
	if err := rt.Close(ctx); err != nil {
		t.Fatal(err)
	}

	// Load after close should fail.
	_, err = rt.LoadModule(ctx, "test", minimalModule)
	if err == nil {
		t.Error("expected error after close")
	}
}

func TestEventToHook(t *testing.T) {
	tests := []struct {
		event string
		want  string
	}{
		{"create", "on_create"},
		{"update", "on_update"},
		{"close", "on_close"},
		{"unknown", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := eventToHook(tc.event); got != tc.want {
			t.Errorf("eventToHook(%q) = %q, want %q", tc.event, got, tc.want)
		}
	}
}

// BenchmarkModuleInstantiation measures WASM module load + instantiate time.
// SLO target: WASM module instantiation memory under 5MB per instance.
func BenchmarkModuleInstantiation(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()

	rt, err := NewRuntime(ctx, dir)
	if err != nil {
		b.Fatal(err)
	}
	defer rt.Close(ctx) //nolint:errcheck

	wasmBytes := buildHookModuleBench(b)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		name := fmt.Sprintf("bench-%d", i)
		mod, err := rt.LoadModule(ctx, name, wasmBytes)
		if err != nil {
			b.Fatal(err)
		}
		mod.Close(ctx) //nolint:errcheck
	}
}

// BenchmarkHookExecution measures hook call latency.
func BenchmarkHookExecution(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()

	rt, err := NewRuntime(ctx, dir)
	if err != nil {
		b.Fatal(err)
	}
	defer rt.Close(ctx) //nolint:errcheck

	wasmBytes := buildHookModuleBench(b)
	mod, err := rt.LoadModule(ctx, "bench", wasmBytes)
	if err != nil {
		b.Fatal(err)
	}
	defer mod.Close(ctx) //nolint:errcheck

	issue := &types.Issue{ID: "bench-1", Title: "Benchmark"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := mod.CallHook(ctx, "on_create", issue)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMemoryFootprint measures the memory cost of WASM instances.
// SLO target: under 5MB per instance.
func BenchmarkMemoryFootprint(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()

	rt, err := NewRuntime(ctx, dir)
	if err != nil {
		b.Fatal(err)
	}
	defer rt.Close(ctx) //nolint:errcheck

	wasmBytes := buildHookModuleBench(b)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	const numInstances = 10
	modules := make([]*Module, numInstances)
	for i := 0; i < numInstances; i++ {
		name := fmt.Sprintf("mem-%d", i)
		m, err := rt.LoadModule(ctx, name, wasmBytes)
		if err != nil {
			b.Fatal(err)
		}
		modules[i] = m
	}

	runtime.GC()
	runtime.ReadMemStats(&after)

	perInstance := (after.TotalAlloc - before.TotalAlloc) / numInstances
	b.ReportMetric(float64(perInstance), "bytes/instance")

	// SLO: under 5MB per instance.
	if perInstance > 5*1024*1024 {
		b.Errorf("memory per instance = %d bytes, exceeds 5MB SLO", perInstance)
	}

	for _, m := range modules {
		m.Close(ctx) //nolint:errcheck
	}
}

// buildHookModuleBench is the benchmark-friendly version of buildHookModule.
func buildHookModuleBench(b *testing.B) []byte {
	b.Helper()
	return buildHookModuleBytes()
}

// buildHookModuleBytes builds the WASM module without a testing.T/B dependency.
func buildHookModuleBytes() []byte {
	// Same construction as buildHookModule but without t.Helper().
	typeSection := []byte{0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f}
	funcSection := []byte{0x03, 0x04, 0x03, 0x00, 0x00, 0x00}
	memSection := []byte{0x05, 0x03, 0x01, 0x00, 0x01}

	exportNames := []string{"on_create", "on_update", "on_close"}
	var exportData []byte
	exportData = append(exportData, byte(len(exportNames)+1))
	for i, name := range exportNames {
		exportData = append(exportData, byte(len(name)))
		exportData = append(exportData, []byte(name)...)
		exportData = append(exportData, 0x00, byte(i))
	}
	exportData = append(exportData, 0x06)
	exportData = append(exportData, []byte("memory")...)
	exportData = append(exportData, 0x02, 0x00)
	exportSection := append([]byte{0x07, byte(len(exportData))}, exportData...)

	funcBody := []byte{0x04, 0x00, 0x41, 0x00, 0x0b}
	codeData := []byte{0x03}
	for i := 0; i < 3; i++ {
		codeData = append(codeData, funcBody...)
	}
	codeSection := append([]byte{0x0a, byte(len(codeData))}, codeData...)

	var module []byte
	module = append(module, 0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00)
	module = append(module, typeSection...)
	module = append(module, funcSection...)
	module = append(module, memSection...)
	module = append(module, exportSection...)
	module = append(module, codeSection...)
	return module
}

// TestIssueDataAvailable verifies that issue JSON is available to hooks.
func TestIssueDataAvailable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	rt, err := NewRuntime(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx) //nolint:errcheck

	wasmBytes := buildHookModule(t)
	mod, err := rt.LoadModule(ctx, "test", wasmBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(ctx) //nolint:errcheck

	issue := &types.Issue{
		ID:       "data-test",
		Title:    "Data Available Test",
		Priority: 1,
		Status:   types.StatusOpen,
	}

	// After CallHook, the inputBuf should contain serialized issue.
	_, err = mod.CallHook(ctx, "on_create", issue)
	if err != nil {
		t.Fatal(err)
	}

	if len(mod.inputBuf) == 0 {
		t.Error("inputBuf is empty — issue data not set")
	}

	var decoded types.Issue
	if err := json.Unmarshal(mod.inputBuf, &decoded); err != nil {
		t.Fatalf("unmarshal inputBuf: %v", err)
	}
	if decoded.ID != "data-test" {
		t.Errorf("decoded ID = %q, want data-test", decoded.ID)
	}
}

func TestDefaultLogger(t *testing.T) {
	// Just verify it doesn't panic.
	l := defaultLogger{}
	l.Log("test-plugin", "info", "hello world")
}

// TestKVStoreEmptyDir verifies KV store creates its directory.
func TestKVStoreEmptyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "path")
	kv, err := NewKVStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Set("k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	// Verify file was created.
	if _, err := os.Stat(filepath.Join(dir, "kv.json")); err != nil {
		t.Errorf("kv.json not created: %v", err)
	}
}
