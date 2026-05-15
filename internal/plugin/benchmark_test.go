package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkLockfileScan100 measures the time to open and parse a lockfile
// with 100 plugin entries.
// SLO target: under 150ms.
func BenchmarkLockfileScan100(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "lock.json")

	// Create a lockfile with 100 entries.
	lf, err := OpenLockfile(path)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := lf.Put(&LockEntry{
			Name:        fmt.Sprintf("plugin-%03d", i),
			Version:     "1.0.0",
			Tier:        TierProvider,
			Digest:      fmt.Sprintf("sha256:%064x", i),
			Source:      SourceOCI,
			SourceURI:   fmt.Sprintf("oci://ghcr.io/test/plugin-%03d:v1", i),
			InstalledAt: time.Now().UTC(),
		}); err != nil {
			b.Fatal(err)
		}
	}
	if err := lf.Save(); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lf2, err := OpenLockfile(path)
		if err != nil {
			b.Fatal(err)
		}
		if lf2.Len() != 100 {
			b.Fatalf("expected 100 entries, got %d", lf2.Len())
		}
	}
}

// BenchmarkLockfileScan100_WithVerify measures lockfile parse + digest
// verification for 100 plugins (with cached files on disk).
// SLO target: under 150ms.
func BenchmarkLockfileScan100_WithVerify(b *testing.B) {
	root := b.TempDir()
	paths := PathsFromRoot(root)
	if err := paths.EnsureDirs(); err != nil {
		b.Fatal(err)
	}

	lf, err := OpenLockfile(paths.LockfilePath)
	if err != nil {
		b.Fatal(err)
	}

	// Create 100 plugins with cached files.
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("plugin-%03d", i)
		cacheDir := paths.PluginCacheDir(name)
		if err := os.MkdirAll(cacheDir, 0700); err != nil {
			b.Fatal(err)
		}

		content := []byte(fmt.Sprintf("plugin binary content %d", i))
		entrypoint := filepath.Join(cacheDir, name)
		if err := os.WriteFile(entrypoint, content, 0600); err != nil {
			b.Fatal(err)
		}
		if err := WriteManifest(filepath.Join(cacheDir, "manifest.json"), &Manifest{
			Name: name, Version: "1.0.0", Tier: TierProvider, Entrypoint: name,
		}); err != nil {
			b.Fatal(err)
		}

		digest, err := hashFile(entrypoint)
		if err != nil {
			b.Fatal(err)
		}
		if err := lf.Put(&LockEntry{
			Name: name, Version: "1.0.0", Tier: TierProvider,
			Digest: digest, Source: SourceLocal, InstalledAt: time.Now().UTC(),
		}); err != nil {
			b.Fatal(err)
		}
	}
	if err := lf.Save(); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mgr, err := NewManager(paths)
		if err != nil {
			b.Fatal(err)
		}
		problems := mgr.Verify()
		if len(problems) != 0 {
			b.Fatalf("unexpected problems: %v", problems)
		}
	}
}

// BenchmarkPluginInstallLocal measures local plugin installation time.
func BenchmarkPluginInstallLocal(b *testing.B) {
	// Create a source plugin once.
	srcDir := filepath.Join(b.TempDir(), "source")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		b.Fatal(err)
	}
	if err := WriteManifest(filepath.Join(srcDir, "manifest.json"), &Manifest{
		Name: "bench-plugin", Version: "1.0.0", Tier: TierProvider,
		Entrypoint: "bench-plugin", Capabilities: []Capability{CapTrackerRead},
	}); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "bench-plugin"), []byte("#!/bin/sh\necho ok"), 0755); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Fresh root each iteration.
		root := filepath.Join(b.TempDir(), fmt.Sprintf("root-%d", i))
		paths := PathsFromRoot(root)
		mgr, err := NewManager(paths)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := mgr.InstallLocal(srcDir); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGrantStore measures grant lookup performance.
func BenchmarkGrantStore_HasGrant(b *testing.B) {
	dir := b.TempDir()
	gs, err := OpenGrantStore(filepath.Join(dir, "grants.json"))
	if err != nil {
		b.Fatal(err)
	}
	// Add 50 plugins with 5 capabilities each.
	for i := 0; i < 50; i++ {
		for j := 0; j < 5; j++ {
			gs.AddGrant(
				fmt.Sprintf("plugin-%d", i),
				Capability(fmt.Sprintf("cap.%d", j)),
				"user",
			)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = gs.HasGrant("plugin-25", Capability("cap.3"))
	}
}
