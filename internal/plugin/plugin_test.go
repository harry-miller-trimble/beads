package plugin

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockEntry_Validate(t *testing.T) {
	tests := []struct {
		name    string
		entry   LockEntry
		wantErr bool
	}{
		{
			name: "valid entry",
			entry: LockEntry{
				Name:    "test-plugin",
				Version: "1.0.0",
				Tier:    TierProvider,
				Digest:  "sha256:abc123",
				Source:  SourceLocal,
			},
		},
		{
			name:    "missing name",
			entry:   LockEntry{Version: "1.0.0", Tier: TierProvider, Digest: "sha256:abc"},
			wantErr: true,
		},
		{
			name:    "missing version",
			entry:   LockEntry{Name: "x", Tier: TierProvider, Digest: "sha256:abc"},
			wantErr: true,
		},
		{
			name:    "invalid tier",
			entry:   LockEntry{Name: "x", Version: "1.0", Tier: "bad", Digest: "sha256:abc"},
			wantErr: true,
		},
		{
			name:    "missing digest",
			entry:   LockEntry{Name: "x", Version: "1.0", Tier: TierProvider},
			wantErr: true,
		},
		{
			name:    "bad digest prefix",
			entry:   LockEntry{Name: "x", Version: "1.0", Tier: TierProvider, Digest: "md5:abc"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.entry.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestManifest_Validate(t *testing.T) {
	tests := []struct {
		name     string
		manifest Manifest
		wantErr  bool
	}{
		{
			name: "valid provider",
			manifest: Manifest{
				Name: "bd-jira", Version: "1.0.0", Tier: TierProvider,
				Entrypoint: "bd-jira", Capabilities: []Capability{CapTrackerRead},
			},
		},
		{
			name: "valid automation",
			manifest: Manifest{
				Name: "my-hook", Version: "0.1.0", Tier: TierAutomation,
				Entrypoint: "hook.wasm",
			},
		},
		{
			name:     "missing name",
			manifest: Manifest{Version: "1.0", Tier: TierProvider, Entrypoint: "x"},
			wantErr:  true,
		},
		{
			name:     "invalid name",
			manifest: Manifest{Name: "Bad-Name", Version: "1.0", Tier: TierProvider, Entrypoint: "x"},
			wantErr:  true,
		},
		{
			name:     "missing entrypoint",
			manifest: Manifest{Name: "test", Version: "1.0", Tier: TierProvider},
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.manifest.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLockfile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.json")

	lf, err := OpenLockfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lf.Len() != 0 {
		t.Fatal("expected empty lockfile")
	}

	entry := &LockEntry{
		Name:        "test-plugin",
		Version:     "1.0.0",
		Tier:        TierProvider,
		Digest:      "sha256:abcdef1234567890",
		Source:      SourceLocal,
		SourceURI:   "/tmp/test",
		InstalledAt: time.Now().UTC(),
	}
	if err := lf.Put(entry); err != nil {
		t.Fatal(err)
	}
	if err := lf.Save(); err != nil {
		t.Fatal(err)
	}

	// Re-read from disk.
	lf2, err := OpenLockfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lf2.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", lf2.Len())
	}
	got := lf2.Get("test-plugin")
	if got == nil {
		t.Fatal("entry not found after reload")
	}
	if got.Digest != entry.Digest {
		t.Errorf("digest = %q, want %q", got.Digest, entry.Digest)
	}

	// Remove.
	if !lf2.Remove("test-plugin") {
		t.Fatal("Remove returned false")
	}
	if lf2.Len() != 0 {
		t.Fatal("expected 0 entries after remove")
	}
}

func TestGrantStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grants.json")

	gs, err := OpenGrantStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if gs.HasGrant("test", CapTrackerRead) {
		t.Fatal("unexpected grant")
	}

	if !gs.AddGrant("test", CapTrackerRead, "user") {
		t.Fatal("AddGrant returned false on first add")
	}
	if gs.AddGrant("test", CapTrackerRead, "user") {
		t.Fatal("AddGrant returned true on duplicate")
	}
	if err := gs.Save(); err != nil {
		t.Fatal(err)
	}

	// Re-read.
	gs2, err := OpenGrantStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !gs2.HasGrant("test", CapTrackerRead) {
		t.Fatal("grant not found after reload")
	}

	grants := gs2.GrantsFor("test")
	if len(grants) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(grants))
	}

	// Revoke.
	if !gs2.RevokeGrant("test", CapTrackerRead) {
		t.Fatal("RevokeGrant returned false")
	}
	if gs2.HasGrant("test", CapTrackerRead) {
		t.Fatal("grant still present after revoke")
	}
}

func TestAuditLog_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	al := OpenAuditLog(path)

	if err := al.LogInstall("test", "1.0", "sha256:abc", "/tmp/test"); err != nil {
		t.Fatal(err)
	}
	if err := al.LogGrant("test", CapTrackerRead, "user"); err != nil {
		t.Fatal(err)
	}

	events, err := al.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Kind != AuditInstall {
		t.Errorf("event[0].Kind = %q, want %q", events[0].Kind, AuditInstall)
	}
	if events[1].Kind != AuditGrant {
		t.Errorf("event[1].Kind = %q, want %q", events[1].Kind, AuditGrant)
	}

	// Filter by plugin.
	filtered, err := al.ReadForPlugin("test")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered events, got %d", len(filtered))
	}

	// Non-existent plugin.
	empty, err := al.ReadForPlugin("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected 0 events for nonexistent, got %d", len(empty))
	}
}

func TestPaths(t *testing.T) {
	dir := t.TempDir()
	p := PathsFromRoot(dir)

	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	// Verify directories exist.
	for _, d := range []string{p.PluginsDir, p.CacheDir} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatalf("dir %s: %v", d, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", d)
		}
	}
}

func TestManager_InstallLocal(t *testing.T) {
	root := t.TempDir()
	paths := PathsFromRoot(root)
	mgr, err := NewManager(paths)
	if err != nil {
		t.Fatal(err)
	}

	// Create a test plugin.
	pluginDir := filepath.Join(t.TempDir(), "my-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteManifest(filepath.Join(pluginDir, "manifest.json"), &Manifest{
		Name:         "my-plugin",
		Version:      "0.1.0",
		Tier:         TierProvider,
		Entrypoint:   "my-plugin",
		Capabilities: []Capability{CapTrackerRead},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "my-plugin"), []byte("#!/bin/sh\necho hello"), 0755); err != nil {
		t.Fatal(err)
	}

	// Install.
	entry, err := mgr.InstallLocal(pluginDir)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "my-plugin" {
		t.Errorf("name = %q, want %q", entry.Name, "my-plugin")
	}
	if entry.Digest == "" {
		t.Fatal("expected non-empty digest")
	}

	// Verify lockfile.
	if mgr.Lockfile.Len() != 1 {
		t.Fatalf("lockfile len = %d, want 1", mgr.Lockfile.Len())
	}

	// Verify cached files.
	cached := filepath.Join(paths.PluginCacheDir("my-plugin"), "my-plugin")
	if _, err := os.Stat(cached); err != nil {
		t.Fatalf("cached entrypoint missing: %v", err)
	}

	// Duplicate install fails.
	_, err = mgr.InstallLocal(pluginDir)
	if err == nil {
		t.Fatal("expected error on duplicate install")
	}

	// Verify passes.
	problems := mgr.Verify()
	if len(problems) != 0 {
		t.Fatalf("Verify found problems: %v", problems)
	}

	// Remove.
	if err := mgr.Remove("my-plugin"); err != nil {
		t.Fatal(err)
	}
	if mgr.Lockfile.Len() != 0 {
		t.Fatal("expected 0 entries after remove")
	}

	// Audit log has events.
	events, err := mgr.Audit.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 {
		t.Fatalf("expected at least 2 audit events, got %d", len(events))
	}
}

func TestManager_Verify_DetectsTampering(t *testing.T) {
	root := t.TempDir()
	paths := PathsFromRoot(root)
	mgr, err := NewManager(paths)
	if err != nil {
		t.Fatal(err)
	}

	// Create and install a plugin.
	pluginDir := filepath.Join(t.TempDir(), "tamper-test")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteManifest(filepath.Join(pluginDir, "manifest.json"), &Manifest{
		Name: "tamper-test", Version: "1.0", Tier: TierAutomation, Entrypoint: "hook.wasm",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "hook.wasm"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.InstallLocal(pluginDir); err != nil {
		t.Fatal(err)
	}

	// Tamper with the cached file.
	cachedFile := filepath.Join(paths.PluginCacheDir("tamper-test"), "hook.wasm")
	if err := os.WriteFile(cachedFile, []byte("tampered!"), 0644); err != nil {
		t.Fatal(err)
	}

	problems := mgr.Verify()
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d: %v", len(problems), problems)
	}
	if got := problems[0]; got == "" {
		t.Fatal("expected non-empty problem description")
	}
}

func TestCapability_Domain(t *testing.T) {
	tests := []struct {
		cap  Capability
		want string
	}{
		{CapTrackerRead, "tracker"},
		{Capability("network:jira.example.com"), "network"},
		{Capability("env:JIRA_TOKEN"), "env"},
		{Capability("fs.read:/tmp"), "fs"},
		{Capability("simple"), "simple"},
	}
	for _, tt := range tests {
		t.Run(string(tt.cap), func(t *testing.T) {
			if got := tt.cap.Domain(); got != tt.want {
				t.Errorf("Domain() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseSource(t *testing.T) {
	tests := []struct {
		input    string
		wantKind SourceKind
	}{
		{"./my-plugin", SourceLocal},
		{"/tmp/plugin", SourceLocal},
		{"oci://ghcr.io/owner/plugin:v1", SourceOCI},
		{"gh:owner/repo", SourceGH},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			kind, _ := ParseSource(tt.input)
			if kind != tt.wantKind {
				t.Errorf("ParseSource(%q) kind = %q, want %q", tt.input, kind, tt.wantKind)
			}
		})
	}
}
