//go:build embeddeddolt

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreateCustomTypeYAMLFallback verifies that bd create accepts custom
// issue types configured in config.yaml even when the database config table
// has no types.custom entry. This is the primary regression test for GH#2793.
func TestCreateCustomTypeYAMLFallback(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "ct")

	// Write config.yaml with a custom type (YAML-only, not in DB).
	configPath := filepath.Join(beadsDir, "config.yaml")
	configContent := "types:\n  custom:\n    - verification\n    - spike\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config.yaml: %v", err)
	}

	tests := []struct {
		name      string
		typeName  string
		wantType  string
		wantError bool
		errSubstr string
	}{
		{
			name:     "yaml_only_custom_type_accepted",
			typeName: "verification",
			wantType: "verification",
		},
		{
			name:     "second_yaml_custom_type_accepted",
			typeName: "spike",
			wantType: "spike",
		},
		{
			name:     "builtin_type_still_works",
			typeName: "bug",
			wantType: "bug",
		},
		{
			name:     "alias_still_normalizes",
			typeName: "enhancement",
			wantType: "feature",
		},
		{
			name:      "truly_invalid_type_rejected",
			typeName:  "nonexistent",
			wantError: true,
			errSubstr: "invalid issue type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantError {
				out := bdCreateFail(t, bd, dir, "Test "+tt.typeName, "-t", tt.typeName, "--description", "test")
				if !strings.Contains(out, tt.errSubstr) {
					t.Errorf("expected error containing %q, got: %s", tt.errSubstr, out)
				}
				return
			}

			issue := bdCreate(t, bd, dir, "Test "+tt.typeName, "-t", tt.typeName, "--description", "test")
			if string(issue.IssueType) != tt.wantType {
				t.Errorf("issue type = %q, want %q", issue.IssueType, tt.wantType)
			}
		})
	}
}

// TestCreateCustomTypeDBPrecedence verifies that when both DB and YAML have
// custom types configured, the DB list takes precedence (YAML is only used
// as fallback when DB returns empty).
func TestCreateCustomTypeDBPrecedence(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "pr")

	// Write YAML config with one custom type.
	configPath := filepath.Join(beadsDir, "config.yaml")
	configContent := "types:\n  custom:\n    - yaml_only_type\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config.yaml: %v", err)
	}

	// Set a different custom type in the DB via bd config.
	runBD(t, bd, dir, "config", "set", "types.custom", "db_type")

	// DB type should be accepted (DB takes precedence).
	issue := bdCreate(t, bd, dir, "DB type issue", "-t", "db_type", "--description", "test")
	if string(issue.IssueType) != "db_type" {
		t.Errorf("issue type = %q, want %q", issue.IssueType, "db_type")
	}

	// YAML-only type should be rejected when DB has custom types
	// (DB is non-empty so YAML fallback is NOT used).
	out := bdCreateFail(t, bd, dir, "YAML type issue", "-t", "yaml_only_type", "--description", "test")
	if !strings.Contains(out, "invalid issue type") {
		t.Errorf("expected 'invalid issue type' error for YAML-only type when DB has types, got: %s", out)
	}
}

// TestCreateUpdateCustomTypeParity verifies that create and update accept
// the same custom types — both should use the shared resolver.
func TestCreateUpdateCustomTypeParity(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "pa")

	// Configure custom type only in YAML.
	configPath := filepath.Join(beadsDir, "config.yaml")
	configContent := "types:\n  custom:\n    - verification\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config.yaml: %v", err)
	}

	// Create with the custom type should work (GH#2793 fix).
	issue := bdCreate(t, bd, dir, "Parity test", "-t", "verification", "--description", "test")
	if string(issue.IssueType) != "verification" {
		t.Errorf("create: issue type = %q, want %q", issue.IssueType, "verification")
	}

	// Update to the same custom type should also work.
	bdUpdate(t, bd, dir, issue.ID, "--type", "verification", "--json")

	// Verify via show that the type is still correct.
	shown := bdShow(t, bd, dir, issue.ID)
	if string(shown.IssueType) != "verification" {
		t.Errorf("show after update: issue type = %q, want %q", shown.IssueType, "verification")
	}
}

// runBD runs an arbitrary bd subcommand. Fatals on failure.
func runBD(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
