package automations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/beads/internal/plugin"
	"github.com/steveyegge/beads/internal/types"
)

// WASMHookRunner executes automation plugin hooks in response to lifecycle
// events. It replaces the executable-script mechanism from internal/hooks.
//
// For each event (create, update, close), the runner finds all installed
// automation plugins that export the corresponding hook function, verifies
// their digest, and executes them in the WASM sandbox.
type WASMHookRunner struct {
	runtime     *Runtime
	pluginPaths *plugin.Paths
	lockfile    *plugin.Lockfile
	grants      *plugin.GrantStore
}

// NewWASMHookRunner creates a hook runner backed by the WASM automation runtime.
func NewWASMHookRunner(rt *Runtime, paths *plugin.Paths) (*WASMHookRunner, error) {
	lf, err := plugin.OpenLockfile(paths.LockfilePath)
	if err != nil {
		return nil, fmt.Errorf("open lockfile: %w", err)
	}
	gs, err := plugin.OpenGrantStore(paths.GrantsPath)
	if err != nil {
		return nil, fmt.Errorf("open grants: %w", err)
	}

	return &WASMHookRunner{
		runtime:     rt,
		pluginPaths: paths,
		lockfile:    lf,
		grants:      gs,
	}, nil
}

// Run executes all automation hooks for the given event asynchronously.
func (r *WASMHookRunner) Run(event string, issue *types.Issue) {
	go func() {
		_ = r.RunSync(event, issue)
	}()
}

// RunSync executes all automation hooks for the given event synchronously.
func (r *WASMHookRunner) RunSync(event string, issue *types.Issue) error {
	hookName := eventToHook(event)
	if hookName == "" {
		return nil
	}

	ctx := context.Background()
	entries := r.lockfile.List()
	var lastErr error

	for _, entry := range entries {
		if entry.Tier != plugin.TierAutomation {
			continue
		}

		// Verify grant.
		if !r.grants.HasGrant(entry.Name, CapHookExecute) {
			continue
		}

		// Load and execute.
		if err := r.runPluginHook(ctx, entry, hookName, issue); err != nil {
			lastErr = err
			// Continue with other plugins — don't let one failure stop all.
		}
	}

	return lastErr
}

// HookExists checks if any automation plugin exports the given hook.
func (r *WASMHookRunner) HookExists(event string) bool {
	hookName := eventToHook(event)
	if hookName == "" {
		return false
	}

	entries := r.lockfile.List()
	for _, entry := range entries {
		if entry.Tier != plugin.TierAutomation {
			continue
		}
		// Check if the WASM file exists (don't load it just to check).
		wasmPath := filepath.Join(r.pluginPaths.PluginCacheDir(entry.Name), entry.Name+".wasm")
		if _, err := os.Stat(wasmPath); err == nil {
			return true
		}
	}
	return false
}

func (r *WASMHookRunner) runPluginHook(ctx context.Context, entry *plugin.LockEntry, hookName string, issue *types.Issue) error {
	cacheDir := r.pluginPaths.PluginCacheDir(entry.Name)
	wasmPath := filepath.Join(cacheDir, entry.Name+".wasm")

	wasmBytes, err := os.ReadFile(wasmPath) //nolint:gosec // path from trusted lockfile
	if err != nil {
		return fmt.Errorf("read wasm %s: %w", entry.Name, err)
	}

	mod, err := r.runtime.LoadModule(ctx, entry.Name, wasmBytes)
	if err != nil {
		return fmt.Errorf("load %s: %w", entry.Name, err)
	}
	defer mod.Close(ctx) //nolint:errcheck

	if !mod.HasHook(hookName) {
		return nil // Plugin doesn't handle this hook — that's fine.
	}

	rc, err := mod.CallHook(ctx, hookName, issue)
	if err != nil {
		return fmt.Errorf("hook %s.%s: %w", entry.Name, hookName, err)
	}
	if rc != 0 {
		return fmt.Errorf("hook %s.%s returned non-zero: %d", entry.Name, hookName, rc)
	}
	return nil
}

// CapHookExecute is the capability required for automation plugins to execute hooks.
const CapHookExecute plugin.Capability = "hooks.execute"

func eventToHook(event string) string {
	switch event {
	case "create":
		return "on_create"
	case "update":
		return "on_update"
	case "close":
		return "on_close"
	default:
		return ""
	}
}
