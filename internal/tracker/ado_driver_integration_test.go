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

// TestADODriverEndToEnd exercises the full bd-ado tracker driver lifecycle:
//
//	build driver → install → grant → create adapter → call IssueTracker methods
//
// This proves the ADO tracker works end-to-end through the plugin trust layer
// and MCP adapter. It uses fake env vars so no live ADO instance is needed;
// only credential-free tools are exercised (tracker_info, ref matching, field
// mapping).
func TestADODriverEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Build the ADO driver binary.
	adoSrc := filepath.Join("..", "..", "integrations", "bd-ado")
	if _, err := os.Stat(filepath.Join(adoSrc, "main.go")); err != nil {
		t.Skipf("bd-ado source not found at %s: %v", adoSrc, err)
	}

	tmpBuild := t.TempDir()
	binaryName := "bd-ado"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(tmpBuild, binaryName)

	buildCmd := exec.Command("go", "build", "-o", binaryPath, ".")
	buildCmd.Dir = adoSrc
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build bd-ado: %v\n%s", err, out)
	}

	// Create plugin source directory with manifest + binary.
	pluginDir := filepath.Join(tmpBuild, "plugin-src")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}

	manifest := &plugin.Manifest{
		Name:         "bd-ado",
		Version:      "0.1.0",
		Tier:         plugin.TierProvider,
		Description:  "Azure DevOps tracker driver",
		Entrypoint:   binaryName,
		Capabilities: []plugin.Capability{plugin.CapTrackerRead, plugin.CapTrackerWrite},
		EnvAllowlist: []string{
			"AZURE_DEVOPS_PAT",
			"AZURE_DEVOPS_ORG",
			"AZURE_DEVOPS_URL",
			"AZURE_DEVOPS_PROJECT",
			"AZURE_DEVOPS_PROJECTS",
		},
	}
	if err := plugin.WriteManifest(filepath.Join(pluginDir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binaryPath) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, binaryName), data, 0755); err != nil { //nolint:gosec
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

	// Grant tracker capabilities.
	mgr.Grants.AddGrant("bd-ado", plugin.CapTrackerRead, "test")
	mgr.Grants.AddGrant("bd-ado", plugin.CapTrackerWrite, "test")
	if err := mgr.Grants.Save(); err != nil {
		t.Fatal(err)
	}

	// Verify integrity.
	problems := mgr.Verify()
	if len(problems) > 0 {
		t.Fatalf("verify: %v", problems)
	}

	// Set fake ADO env vars so the tracker can initialize.
	t.Setenv("AZURE_DEVOPS_PAT", "fake-pat-for-testing")
	t.Setenv("AZURE_DEVOPS_ORG", "testorg")
	t.Setenv("AZURE_DEVOPS_PROJECT", "testproject")

	// Create MCP adapter — starts the driver, does handshake, fetches tracker_info.
	ctx := context.Background()
	entrypointPath := filepath.Join(paths.PluginCacheDir("bd-ado"), binaryName)

	adapter, err := tracker.NewMCPAdapter(ctx, tracker.MCPAdapterConfig{
		PluginName:   "bd-ado",
		Command:      entrypointPath,
		Manifest:     manifest,
		EnvAllowlist: manifest.EnvAllowlist,
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	defer adapter.Close()

	// --- tracker_info ---
	t.Run("TrackerInfo", func(t *testing.T) {
		if got := adapter.Name(); got != "ado" {
			t.Errorf("Name() = %q, want %q", got, "ado")
		}
		if got := adapter.DisplayName(); got != "Azure DevOps" {
			t.Errorf("DisplayName() = %q, want %q", got, "Azure DevOps")
		}
		if got := adapter.ConfigPrefix(); got != "ado" {
			t.Errorf("ConfigPrefix() = %q, want %q", got, "ado")
		}
	})

	// --- IsExternalRef ---
	t.Run("IsExternalRef", func(t *testing.T) {
		tests := []struct {
			ref  string
			want bool
		}{
			{"https://dev.azure.com/testorg/testproject/_workitems/edit/42", true},
			{"https://dev.azure.com/otherorg/proj/_workitems/edit/99", true},
			{"ado:123", true},
			{"https://github.com/foo/bar/issues/1", false},
			{"jira://FOO-1", false},
			{"", false},
		}
		for _, tt := range tests {
			got := adapter.IsExternalRef(tt.ref)
			if got != tt.want {
				t.Errorf("IsExternalRef(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		}
	})

	// --- ExtractIdentifier ---
	t.Run("ExtractIdentifier", func(t *testing.T) {
		tests := []struct {
			ref  string
			want string
		}{
			{"https://dev.azure.com/testorg/testproject/_workitems/edit/42", "42"},
			{"ado:123", "123"},
		}
		for _, tt := range tests {
			got := adapter.ExtractIdentifier(tt.ref)
			if got != tt.want {
				t.Errorf("ExtractIdentifier(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		}
	})

	// --- BuildExternalRef ---
	t.Run("BuildExternalRef", func(t *testing.T) {
		ref := adapter.BuildExternalRef(&tracker.TrackerIssue{
			ID:         "42",
			Identifier: "42",
		})
		want := "https://dev.azure.com/testorg/testproject/_workitems/edit/42"
		if ref != want {
			t.Errorf("BuildExternalRef = %q, want %q", ref, want)
		}

		// With URL already set, should return it as-is.
		existing := "https://dev.azure.com/testorg/testproject/_workitems/edit/99"
		ref2 := adapter.BuildExternalRef(&tracker.TrackerIssue{
			ID:         "99",
			Identifier: "99",
			URL:        existing,
		})
		if ref2 != existing {
			t.Errorf("BuildExternalRef (with URL) = %q, want %q", ref2, existing)
		}
	})

	// --- Field mappers via MCP ---
	t.Run("FieldMapping", func(t *testing.T) {
		fm := adapter.FieldMapper()

		// Status mapping: ADO → beads
		if got := fm.StatusToBeads("Active"); string(got) != "in_progress" {
			t.Errorf("StatusToBeads(Active) = %q, want in_progress", got)
		}
		if got := fm.StatusToBeads("Closed"); string(got) != "closed" {
			t.Errorf("StatusToBeads(Closed) = %q, want closed", got)
		}
		if got := fm.StatusToBeads("New"); string(got) != "open" {
			t.Errorf("StatusToBeads(New) = %q, want open", got)
		}

		// Priority mapping: ADO (1-4) → beads (0-3)
		if got := fm.PriorityToBeads(float64(1)); got != 0 {
			t.Errorf("PriorityToBeads(1) = %d, want 0", got)
		}
		if got := fm.PriorityToBeads(float64(3)); got != 2 {
			t.Errorf("PriorityToBeads(3) = %d, want 2", got)
		}

		// Type mapping: ADO → beads
		if got := fm.TypeToBeads("Bug"); string(got) != "bug" {
			t.Errorf("TypeToBeads(Bug) = %q, want bug", got)
		}
		if got := fm.TypeToBeads("User Story"); string(got) != "feature" {
			t.Errorf("TypeToBeads(User Story) = %q, want feature", got)
		}
	})

	t.Log("✓ All ADO tracker driver methods work through the plugin → MCP → Tracker round-trip")
}
