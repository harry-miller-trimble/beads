package tracker

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/steveyegge/beads/internal/plugin"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// RegisterMCPPlugins discovers provider plugins from the lockfile and registers
// them as tracker factories in the global registry. Only plugins with
// tracker.read grants are registered.
//
// This should be called during CLI initialization, after the plugin trust layer
// is loaded. It is a no-op if no plugins are installed.
func RegisterMCPPlugins(paths *plugin.Paths) error {
	mgr, err := plugin.NewManager(paths)
	if err != nil {
		// Plugin infrastructure not set up — that's fine, no plugins to load.
		return nil
	}

	for _, entry := range mgr.Lockfile.List() {
		if entry.Tier != plugin.TierProvider {
			continue
		}

		// Check that the plugin has tracker.read grant.
		if !mgr.Grants.HasGrant(entry.Name, plugin.CapTrackerRead) {
			continue
		}

		// Verify digest before registering.
		problems := verifyEntry(mgr, entry)
		if len(problems) > 0 {
			fmt.Printf("warning: skipping plugin %q: %s\n", entry.Name, problems[0])
			continue
		}

		// Read manifest for entrypoint and env allowlist.
		cacheDir := paths.PluginCacheDir(entry.Name)
		manifest, err := plugin.ReadManifest(filepath.Join(cacheDir, "manifest.json"))
		if err != nil {
			fmt.Printf("warning: skipping plugin %q: %v\n", entry.Name, err)
			continue
		}

		// Capture loop variables for the closure.
		pluginName := entry.Name
		command := filepath.Join(cacheDir, manifest.Entrypoint)
		envAllowlist := manifest.EnvAllowlist
		manifestCopy := manifest

		Register(pluginName, func() IssueTracker {
			adapter, err := NewMCPAdapter(context.Background(), MCPAdapterConfig{
				PluginName:   pluginName,
				Command:      command,
				EnvAllowlist: envAllowlist,
				Manifest:     manifestCopy,
			})
			if err != nil {
				// Factory cannot return errors, so return a broken adapter
				// that will fail on Init/Validate.
				return &brokenAdapter{name: pluginName, err: err}
			}
			return adapter
		})
	}

	return nil
}

// verifyEntry checks a single lockfile entry's digest.
func verifyEntry(mgr *plugin.Manager, entry *plugin.LockEntry) []string {
	// Use Manager.Verify which checks all — filter for just this entry.
	all := mgr.Verify()
	var relevant []string
	for _, p := range all {
		if len(p) > len(entry.Name) && p[:len(entry.Name)] == entry.Name {
			relevant = append(relevant, p)
		}
	}
	return relevant
}

// brokenAdapter is returned when plugin startup fails. It satisfies the
// IssueTracker interface but fails on Init/Validate with the original error.
type brokenAdapter struct {
	name string
	err  error
}

func (b *brokenAdapter) Name() string                                    { return b.name }
func (b *brokenAdapter) DisplayName() string                             { return b.name }
func (b *brokenAdapter) ConfigPrefix() string                            { return b.name }
func (b *brokenAdapter) Init(_ context.Context, _ storage.Storage) error { return b.err }
func (b *brokenAdapter) Validate() error                                 { return b.err }
func (b *brokenAdapter) Close() error                                    { return nil }
func (b *brokenAdapter) FetchIssues(_ context.Context, _ FetchOptions) ([]TrackerIssue, error) {
	return nil, b.err
}
func (b *brokenAdapter) FetchIssue(_ context.Context, _ string) (*TrackerIssue, error) {
	return nil, b.err
}
func (b *brokenAdapter) CreateIssue(_ context.Context, _ *types.Issue) (*TrackerIssue, error) {
	return nil, b.err
}
func (b *brokenAdapter) UpdateIssue(_ context.Context, _ string, _ *types.Issue) (*TrackerIssue, error) {
	return nil, b.err
}
func (b *brokenAdapter) FieldMapper() FieldMapper                { return nil }
func (b *brokenAdapter) IsExternalRef(_ string) bool             { return false }
func (b *brokenAdapter) ExtractIdentifier(ref string) string     { return ref }
func (b *brokenAdapter) BuildExternalRef(_ *TrackerIssue) string { return "" }
