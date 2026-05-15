package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Manager coordinates plugin operations using the trust layer components.
type Manager struct {
	Paths    *Paths
	Lockfile *Lockfile
	Grants   *GrantStore
	Audit    *AuditLog
}

// NewManager creates a Manager with all trust layer components loaded.
func NewManager(paths *Paths) (*Manager, error) {
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}
	lf, err := OpenLockfile(paths.LockfilePath)
	if err != nil {
		return nil, fmt.Errorf("open lockfile: %w", err)
	}
	gs, err := OpenGrantStore(paths.GrantsPath)
	if err != nil {
		return nil, fmt.Errorf("open grants: %w", err)
	}
	return &Manager{
		Paths:    paths,
		Lockfile: lf,
		Grants:   gs,
		Audit:    OpenAuditLog(paths.AuditLogPath),
	}, nil
}

// InstallLocal installs a plugin from a local folder containing manifest.json
// and the plugin entrypoint.
func (m *Manager) InstallLocal(sourceDir string) (*LockEntry, error) {
	manifestPath := filepath.Join(sourceDir, "manifest.json")
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("install: %w", err)
	}

	// Check if already installed.
	if existing := m.Lockfile.Get(manifest.Name); existing != nil {
		return nil, fmt.Errorf("plugin %q is already installed (version %s); remove it first", manifest.Name, existing.Version)
	}

	// Resolve and hash the entrypoint.
	entrypointSrc := filepath.Join(sourceDir, manifest.Entrypoint)
	digest, err := hashFile(entrypointSrc)
	if err != nil {
		return nil, fmt.Errorf("install %q: hash entrypoint: %w", manifest.Name, err)
	}

	// Copy to cache.
	cacheDir := m.Paths.PluginCacheDir(manifest.Name)
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, fmt.Errorf("install %q: create cache dir: %w", manifest.Name, err)
	}
	if err := WriteManifest(filepath.Join(cacheDir, "manifest.json"), manifest); err != nil {
		return nil, fmt.Errorf("install %q: write manifest: %w", manifest.Name, err)
	}
	entrypointDst := filepath.Join(cacheDir, manifest.Entrypoint)
	if err := copyFile(entrypointSrc, entrypointDst); err != nil {
		return nil, fmt.Errorf("install %q: copy entrypoint: %w", manifest.Name, err)
	}
	// Ensure entrypoint is executable for provider plugins.
	if manifest.Tier == TierProvider {
		if err := os.Chmod(entrypointDst, 0700); err != nil { //nolint:gosec // provider plugins must be executable
			return nil, fmt.Errorf("install %q: chmod entrypoint: %w", manifest.Name, err)
		}
	}

	entry := &LockEntry{
		Name:        manifest.Name,
		Version:     manifest.Version,
		Tier:        manifest.Tier,
		Digest:      digest,
		Source:      SourceLocal,
		SourceURI:   sourceDir,
		InstalledAt: time.Now().UTC(),
	}
	if err := m.Lockfile.Put(entry); err != nil {
		return nil, fmt.Errorf("install %q: lockfile: %w", manifest.Name, err)
	}
	if err := m.Lockfile.Save(); err != nil {
		return nil, fmt.Errorf("install %q: save lockfile: %w", manifest.Name, err)
	}

	_ = m.Audit.LogInstall(manifest.Name, manifest.Version, digest, sourceDir)

	return entry, nil
}

// InstallOCI is a placeholder for OCI registry installation.
func (m *Manager) InstallOCI(_ string) (*LockEntry, error) {
	return nil, fmt.Errorf("OCI plugin installation is not yet implemented; use a local folder for now")
}

// InstallGH is a placeholder for GitHub source installation.
func (m *Manager) InstallGH(_ string) (*LockEntry, error) {
	return nil, fmt.Errorf("GitHub plugin installation (gh:) is not yet implemented; use a local folder for now")
}

// Remove uninstalls a plugin: removes from lockfile, revokes grants, cleans cache.
func (m *Manager) Remove(name string) error {
	entry := m.Lockfile.Get(name)
	if entry == nil {
		return fmt.Errorf("plugin %q is not installed", name)
	}

	revokedCount := m.Grants.RevokeAll(name)
	if revokedCount > 0 {
		if err := m.Grants.Save(); err != nil {
			return fmt.Errorf("remove %q: save grants: %w", name, err)
		}
	}

	m.Lockfile.Remove(name)
	if err := m.Lockfile.Save(); err != nil {
		return fmt.Errorf("remove %q: save lockfile: %w", name, err)
	}

	// Clean cache directory.
	cacheDir := m.Paths.PluginCacheDir(name)
	if err := os.RemoveAll(cacheDir); err != nil {
		// Non-fatal — lockfile is already updated.
		fmt.Fprintf(os.Stderr, "warning: failed to clean cache for %q: %v\n", name, err)
	}

	_ = m.Audit.LogRemove(name, entry.Version)

	return nil
}

// Verify checks that all lockfile entries have matching digests in the cache.
// Returns a list of problems found. An empty list means everything is healthy.
func (m *Manager) Verify() []string {
	var problems []string
	for _, entry := range m.Lockfile.List() {
		cacheDir := m.Paths.PluginCacheDir(entry.Name)
		manifestPath := filepath.Join(cacheDir, "manifest.json")
		manifest, err := ReadManifest(manifestPath)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: cannot read cached manifest: %v", entry.Name, err))
			continue
		}

		entrypoint := filepath.Join(cacheDir, manifest.Entrypoint)
		digest, err := hashFile(entrypoint)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: cannot hash entrypoint: %v", entry.Name, err))
			continue
		}

		if digest != entry.Digest {
			problems = append(problems, fmt.Sprintf("%s: digest mismatch (lockfile=%s, actual=%s)", entry.Name, entry.Digest, digest))
		}
	}
	return problems
}

// hashFile computes "sha256:<hex>" for a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is resolved from cache dir, not arbitrary user input
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// copyFile copies src to dst, preserving permissions.
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src is from plugin cache, not arbitrary input
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode()) //nolint:gosec // dst is cache path
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, in)
	return err
}

// ParseSource determines the source kind from a user-provided string.
// Returns the kind and the cleaned URI.
func ParseSource(source string) (SourceKind, string) {
	if strings.HasPrefix(source, "oci://") {
		return SourceOCI, source
	}
	if strings.HasPrefix(source, "gh:") {
		return SourceGH, source
	}
	return SourceLocal, source
}
