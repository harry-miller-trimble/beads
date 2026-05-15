package tracker

import (
	"testing"

	"github.com/steveyegge/beads/internal/plugin"
)

func TestWrapProvenance(t *testing.T) {
	p := WrapProvenance("bd-jira", map[string]string{"key": "PROJ-123"})
	if p.Source != "plugin:bd-jira" {
		t.Errorf("Source = %q, want %q", p.Source, "plugin:bd-jira")
	}
	if p.Trust != "external" {
		t.Errorf("Trust = %q, want %q", p.Trust, "external")
	}
	data, ok := p.Data.(map[string]string)
	if !ok {
		t.Fatal("Data type assertion failed")
	}
	if data["key"] != "PROJ-123" {
		t.Errorf("Data[key] = %q, want %q", data["key"], "PROJ-123")
	}
}

func TestBrokenAdapter(t *testing.T) {
	b := &brokenAdapter{name: "test-broken", err: ErrBroken("test failure")}

	if b.Name() != "test-broken" {
		t.Errorf("Name() = %q, want %q", b.Name(), "test-broken")
	}
	if b.DisplayName() != "test-broken" {
		t.Errorf("DisplayName() = %q, want %q", b.DisplayName(), "test-broken")
	}
	if err := b.Init(nil, nil); err == nil {
		t.Error("Init() should return error")
	}
	if err := b.Validate(); err == nil {
		t.Error("Validate() should return error")
	}
	if _, err := b.FetchIssues(nil, FetchOptions{}); err == nil {
		t.Error("FetchIssues() should return error")
	}
	if _, err := b.FetchIssue(nil, "x"); err == nil {
		t.Error("FetchIssue() should return error")
	}
	if b.IsExternalRef("x") {
		t.Error("IsExternalRef() should return false")
	}
	if b.FieldMapper() != nil {
		t.Error("FieldMapper() should return nil")
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close() should not error: %v", err)
	}
}

type ErrBroken string

func (e ErrBroken) Error() string { return string(e) }

func TestMCPDiscovery_NoPaths(t *testing.T) {
	// RegisterMCPPlugins should be a no-op when plugins dir doesn't exist.
	nonexistent := t.TempDir() + "/nonexistent"
	paths := plugin.PathsFromRoot(nonexistent)
	// Should not panic or error — just a no-op.
	err := RegisterMCPPlugins(paths)
	if err != nil {
		t.Errorf("RegisterMCPPlugins should be no-op for missing dir, got: %v", err)
	}
}
