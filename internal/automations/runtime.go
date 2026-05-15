// Package automations implements the WASM Automation runtime for beads plugins.
//
// Automation plugins are sandboxed WebAssembly modules executed in-process
// via wazero (pure Go, CGO-free). They replace the executable-script hook
// mechanism with a secure, portable alternative.
//
// Plugin shape: single .wasm file exporting hook entry points (on_create,
// on_update, on_close). Host functions provide structured access to issue
// data, key-value storage, logging, and event emission.
//
// Security model: WASI capabilities are default-deny. Only capabilities
// explicitly granted via the trust layer (internal/plugin) are available.
package automations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/steveyegge/beads/internal/types"
)

// Runtime manages WASM module instantiation and execution.
type Runtime struct {
	engine wazero.Runtime
	mu     sync.Mutex
	closed bool

	// KV is the key-value store backing host functions.
	KV *KVStore
	// Logger receives structured log output from plugins.
	Logger Logger

	// moduleState tracks per-module state for host function callbacks.
	moduleState map[string]*Module
}

// Logger receives log messages from WASM plugins.
type Logger interface {
	Log(pluginName, level, message string)
}

// defaultLogger writes to stderr.
type defaultLogger struct{}

func (defaultLogger) Log(plugin, level, msg string) {
	fmt.Fprintf(os.Stderr, "[wasm:%s] %s: %s\n", plugin, level, msg)
}

// NewRuntime creates a WASM automation runtime backed by wazero.
func NewRuntime(ctx context.Context, kvDir string) (*Runtime, error) {
	engine := wazero.NewRuntime(ctx)

	// Instantiate WASI for basic I/O (stdin/stdout/stderr).
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, engine); err != nil {
		_ = engine.Close(ctx)
		return nil, fmt.Errorf("wasi init: %w", err)
	}

	kv, err := NewKVStore(kvDir)
	if err != nil {
		_ = engine.Close(ctx)
		return nil, fmt.Errorf("kvstore init: %w", err)
	}

	rt := &Runtime{
		engine:      engine,
		KV:          kv,
		Logger:      defaultLogger{},
		moduleState: make(map[string]*Module),
	}

	// Register host functions once — they use the module registry for state lookup.
	if err := rt.registerHostFunctions(ctx); err != nil {
		_ = engine.Close(ctx)
		return nil, fmt.Errorf("host functions: %w", err)
	}

	return rt, nil
}

// Close shuts down the WASM runtime and releases resources.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.engine.Close(ctx)
}

// Module is a loaded WASM automation plugin ready for execution.
type Module struct {
	name     string
	mod      api.Module
	runtime  *Runtime
	hooks    map[string]api.Function
	inputBuf []byte // current input for host function reads
}

// LoadModule compiles and instantiates a WASM module from binary.
// The module's exported functions are discovered and mapped to hook points.
func (r *Runtime) LoadModule(ctx context.Context, name string, wasmBytes []byte) (*Module, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil, fmt.Errorf("runtime is closed")
	}

	compiled, err := r.engine.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", name, err)
	}

	m := &Module{
		name:    name,
		runtime: r,
		hooks:   make(map[string]api.Function),
	}

	// Register module state so host functions can find it.
	r.moduleState[name] = m

	// Instantiate with WASI config (minimal: no fs, no env, no args by default).
	config := wazero.NewModuleConfig().
		WithName(name).
		WithStartFunctions().  // Don't auto-run _start
		WithStdout(os.Stderr). // Plugin stdout goes to stderr (not the CLI pipe)
		WithStderr(os.Stderr)

	mod, err := r.engine.InstantiateModule(ctx, compiled, config)
	if err != nil {
		delete(r.moduleState, name)
		return nil, fmt.Errorf("instantiate %s: %w", name, err)
	}
	m.mod = mod

	// Discover exported hook functions.
	for _, hookName := range []string{"on_create", "on_update", "on_close"} {
		if fn := mod.ExportedFunction(hookName); fn != nil {
			m.hooks[hookName] = fn
		}
	}

	return m, nil
}

// HasHook reports whether the module exports a given hook function.
func (m *Module) HasHook(hookName string) bool {
	_, ok := m.hooks[hookName]
	return ok
}

// Hooks returns the names of all exported hook functions.
func (m *Module) Hooks() []string {
	names := make([]string, 0, len(m.hooks))
	for k := range m.hooks {
		names = append(names, k)
	}
	return names
}

// CallHook executes a hook function with the given issue as input.
// The issue is serialized to JSON and made available via the bd_get_issue
// host function. Returns the hook's return code (0 = success).
func (m *Module) CallHook(ctx context.Context, hookName string, issue *types.Issue) (uint32, error) {
	fn, ok := m.hooks[hookName]
	if !ok {
		return 0, fmt.Errorf("hook %q not exported by module %q", hookName, m.name)
	}

	// Serialize issue for host function access.
	data, err := json.Marshal(issue)
	if err != nil {
		return 0, fmt.Errorf("marshal issue: %w", err)
	}
	m.inputBuf = data

	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	results, err := fn.Call(callCtx)
	if err != nil {
		return 0, fmt.Errorf("call %s.%s: %w", m.name, hookName, err)
	}

	if len(results) > 0 {
		return uint32(results[0]), nil
	}
	return 0, nil
}

// Close releases the module resources.
func (m *Module) Close(ctx context.Context) error {
	if m.runtime != nil {
		m.runtime.mu.Lock()
		delete(m.runtime.moduleState, m.name)
		m.runtime.mu.Unlock()
	}
	if m.mod != nil {
		return m.mod.Close(ctx)
	}
	return nil
}
