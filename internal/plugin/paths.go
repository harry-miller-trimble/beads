package plugin

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths holds resolved filesystem paths for plugin infrastructure.
type Paths struct {
	// PluginsDir is ~/.beads/plugins/
	PluginsDir string
	// LockfilePath is ~/.beads/plugins/lock.json
	LockfilePath string
	// GrantsPath is ~/.beads/plugins/grants.json
	GrantsPath string
	// CacheDir is ~/.beads/plugins/cache/
	CacheDir string
	// AuditLogPath is ~/.beads/audit.log
	AuditLogPath string
}

// DefaultPaths returns plugin paths rooted at ~/.beads/.
func DefaultPaths() (*Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	return PathsFromRoot(filepath.Join(home, ".beads")), nil
}

// PathsFromRoot returns plugin paths rooted at the given directory.
// Useful for testing with t.TempDir().
func PathsFromRoot(root string) *Paths {
	plugins := filepath.Join(root, "plugins")
	return &Paths{
		PluginsDir:   plugins,
		LockfilePath: filepath.Join(plugins, "lock.json"),
		GrantsPath:   filepath.Join(plugins, "grants.json"),
		CacheDir:     filepath.Join(plugins, "cache"),
		AuditLogPath: filepath.Join(root, "audit.log"),
	}
}

// PluginCacheDir returns the cache directory for a specific plugin.
func (p *Paths) PluginCacheDir(name string) string {
	return filepath.Join(p.CacheDir, name)
}

// EnsureDirs creates the plugins directory structure if it doesn't exist.
func (p *Paths) EnsureDirs() error {
	for _, d := range []string{p.PluginsDir, p.CacheDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", d, err)
		}
	}
	return nil
}

// ProjectLockfilePath returns the project-scoped lockfile path.
// The project root is the directory containing .beads/.
func ProjectLockfilePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".beads", "plugins.lock")
}
