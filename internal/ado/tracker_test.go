package ado

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/tracker"
)

// mockStore implements the config methods of storage.Storage for testing.
// All other methods panic if called (via the embedded nil interface).
type mockStore struct {
	storage.Storage
	config map[string]string
}

func newMockStore(config map[string]string) *mockStore {
	if config == nil {
		config = make(map[string]string)
	}
	return &mockStore{config: config}
}

func (m *mockStore) GetConfig(_ context.Context, key string) (string, error) {
	if v, ok := m.config[key]; ok {
		return v, nil
	}
	return "", fmt.Errorf("key not found: %s", key)
}

func (m *mockStore) SetConfig(_ context.Context, key, value string) error {
	m.config[key] = value
	return nil
}

func TestTracker_Name(t *testing.T) {
	tr := &Tracker{}
	if got := tr.Name(); got != "ado" {
		t.Errorf("Name() = %q, want %q", got, "ado")
	}
	if got := tr.DisplayName(); got != "Azure DevOps" {
		t.Errorf("DisplayName() = %q, want %q", got, "Azure DevOps")
	}
	if got := tr.ConfigPrefix(); got != "ado" {
		t.Errorf("ConfigPrefix() = %q, want %q", got, "ado")
	}
}

func TestTracker_InitFromEnv(t *testing.T) {
	t.Setenv("AZURE_DEVOPS_PAT", "test-pat-value")
	t.Setenv("AZURE_DEVOPS_ORG", "myorg")
	t.Setenv("AZURE_DEVOPS_PROJECT", "myproject")

	tr := &Tracker{}
	err := tr.Init(context.Background(), newMockStore(nil))
	if err != nil {
		t.Fatalf("Init() unexpected error: %v", err)
	}
	if tr.client == nil {
		t.Fatal("Init() did not create client")
	}
	if tr.org != "myorg" {
		t.Errorf("org = %q, want %q", tr.org, "myorg")
	}
	if tr.project != "myproject" {
		t.Errorf("project = %q, want %q", tr.project, "myproject")
	}
	if tr.mapper == nil {
		t.Fatal("Init() did not create field mapper")
	}
}

func TestTracker_InitFromConfig(t *testing.T) {
	tr := &Tracker{}
	store := newMockStore(map[string]string{
		"ado.pat":     "config-pat",
		"ado.org":     "configorg",
		"ado.project": "configproject",
	})
	err := tr.Init(context.Background(), store)
	if err != nil {
		t.Fatalf("Init() unexpected error: %v", err)
	}
	if tr.org != "configorg" {
		t.Errorf("org = %q, want %q", tr.org, "configorg")
	}
	if tr.project != "configproject" {
		t.Errorf("project = %q, want %q", tr.project, "configproject")
	}
}

func TestTracker_InitWithCustomURL(t *testing.T) {
	tr := &Tracker{}
	store := newMockStore(map[string]string{
		"ado.pat":     "config-pat",
		"ado.project": "myproject",
		"ado.url":     "https://tfs.corp.com/DefaultCollection",
	})
	err := tr.Init(context.Background(), store)
	if err != nil {
		t.Fatalf("Init() unexpected error: %v", err)
	}
	if tr.client.BaseURL != "https://tfs.corp.com/DefaultCollection" {
		t.Errorf("BaseURL = %q, want %q", tr.client.BaseURL, "https://tfs.corp.com/DefaultCollection")
	}
	if tr.baseURL != "https://tfs.corp.com/DefaultCollection" {
		t.Errorf("baseURL = %q, want %q", tr.baseURL, "https://tfs.corp.com/DefaultCollection")
	}
}

func TestTracker_InitMissingPAT(t *testing.T) {
	tr := &Tracker{}
	err := tr.Init(context.Background(), newMockStore(nil))
	if err == nil {
		t.Fatal("Init() expected error for missing PAT")
	}
	if got := err.Error(); !contains(got, "PAT") {
		t.Errorf("error = %q, want mention of PAT", got)
	}
}

func TestTracker_InitMissingOrg(t *testing.T) {
	tr := &Tracker{}
	store := newMockStore(map[string]string{
		"ado.pat":     "some-pat",
		"ado.project": "proj",
	})
	err := tr.Init(context.Background(), store)
	if err == nil {
		t.Fatal("Init() expected error for missing org")
	}
	if got := err.Error(); !contains(got, "organization") {
		t.Errorf("error = %q, want mention of organization", got)
	}
}

func TestTracker_InitMissingProject(t *testing.T) {
	tr := &Tracker{}
	store := newMockStore(map[string]string{
		"ado.pat": "some-pat",
		"ado.org": "myorg",
	})
	err := tr.Init(context.Background(), store)
	if err == nil {
		t.Fatal("Init() expected error for missing project")
	}
	if got := err.Error(); !contains(got, "project") {
		t.Errorf("error = %q, want mention of project", got)
	}
}

func TestTracker_InitWithStateMappings(t *testing.T) {
	tr := &Tracker{}
	store := newMockStore(map[string]string{
		"ado.pat":              "some-pat",
		"ado.org":              "myorg",
		"ado.project":          "myproject",
		"ado.state_map.open":   "To Do",
		"ado.state_map.closed": "Done",
		"ado.type_map.bug":     "Defect",
		"ado.type_map.feature": "Story",
	})
	err := tr.Init(context.Background(), store)
	if err != nil {
		t.Fatalf("Init() unexpected error: %v", err)
	}
	if tr.mapper == nil {
		t.Fatal("mapper not created")
	}
}

func TestTracker_ValidateUninitialized(t *testing.T) {
	tr := &Tracker{}
	err := tr.Validate()
	if err == nil {
		t.Fatal("Validate() expected error when not initialized")
	}
}

func TestTracker_Close(t *testing.T) {
	tr := &Tracker{}
	if err := tr.Close(); err != nil {
		t.Errorf("Close() unexpected error: %v", err)
	}
}

func TestTracker_IsExternalRef(t *testing.T) {
	tr := &Tracker{
		baseURL: "https://tfs.corp.com/DefaultCollection",
	}
	tests := []struct {
		name string
		ref  string
		want bool
	}{
		{
			name: "cloud URL",
			ref:  "https://dev.azure.com/myorg/myproject/_workitems/edit/42",
			want: true,
		},
		{
			name: "visualstudio URL",
			ref:  "https://myorg.visualstudio.com/myproject/_workitems/edit/42",
			want: true,
		},
		{
			name: "on-prem with matching baseURL",
			ref:  "https://tfs.corp.com/DefaultCollection/myproject/_workitems/edit/99",
			want: true,
		},
		{
			name: "GitHub issue",
			ref:  "https://github.com/owner/repo/issues/42",
			want: false,
		},
		{
			name: "ADO URL without workitems path",
			ref:  "https://dev.azure.com/myorg/myproject/other/path",
			want: false,
		},
		{
			name: "empty string",
			ref:  "",
			want: false,
		},
		{
			name: "unknown on-prem without matching baseURL",
			ref:  "https://other-tfs.example.com/proj/_workitems/edit/10",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tr.IsExternalRef(tt.ref); got != tt.want {
				t.Errorf("IsExternalRef(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestTracker_ExtractIdentifier(t *testing.T) {
	tr := &Tracker{}
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{
			name: "cloud URL",
			ref:  "https://dev.azure.com/org/proj/_workitems/edit/123",
			want: "123",
		},
		{
			name: "visualstudio URL",
			ref:  "https://org.visualstudio.com/proj/_workitems/edit/456",
			want: "456",
		},
		{
			name: "on-prem URL",
			ref:  "https://tfs.corp.com/DefaultCollection/proj/_workitems/edit/789",
			want: "789",
		},
		{
			name: "invalid URL",
			ref:  "invalid-url",
			want: "",
		},
		{
			name: "empty string",
			ref:  "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tr.ExtractIdentifier(tt.ref); got != tt.want {
				t.Errorf("ExtractIdentifier(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestTracker_BuildExternalRef(t *testing.T) {
	tests := []struct {
		name    string
		tracker *Tracker
		issue   *tracker.TrackerIssue
		want    string
	}{
		{
			name:    "uses issue URL if set",
			tracker: &Tracker{org: "myorg", project: "myproj"},
			issue: &tracker.TrackerIssue{
				Identifier: "42",
				URL:        "https://dev.azure.com/myorg/myproj/_workitems/edit/42",
			},
			want: "https://dev.azure.com/myorg/myproj/_workitems/edit/42",
		},
		{
			name:    "constructs cloud URL from org and project",
			tracker: &Tracker{org: "myorg", project: "myproj"},
			issue:   &tracker.TrackerIssue{Identifier: "99"},
			want:    "https://dev.azure.com/myorg/myproj/_workitems/edit/99",
		},
		{
			name:    "constructs on-prem URL from baseURL",
			tracker: &Tracker{baseURL: "https://tfs.corp.com/DefaultCollection", project: "proj"},
			issue:   &tracker.TrackerIssue{Identifier: "55"},
			want:    "https://tfs.corp.com/DefaultCollection/proj/_workitems/edit/55",
		},
		{
			name:    "fallback to ado: prefix",
			tracker: &Tracker{},
			issue:   &tracker.TrackerIssue{Identifier: "77"},
			want:    "ado:77",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tracker.BuildExternalRef(tt.issue); got != tt.want {
				t.Errorf("BuildExternalRef() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAdoWorkItemToTrackerIssue(t *testing.T) {
	wi := &WorkItem{
		ID:  42,
		Rev: 5,
		URL: "https://dev.azure.com/org/proj/_apis/wit/workItems/42",
		Fields: map[string]interface{}{
			FieldTitle:         "Test Work Item",
			FieldDescription:   "<p>Description HTML</p>",
			FieldState:         "Active",
			FieldWorkItemType:  "Bug",
			FieldPriority:      float64(2),
			FieldTags:          "tag1; tag2; tag3",
			FieldCreatedDate:   "2024-01-15T10:30:00.000Z",
			FieldChangedDate:   "2024-06-20T14:45:00.000Z",
			FieldAreaPath:      "proj\\Team1",
			FieldIterationPath: "proj\\Sprint 5",
			FieldStoryPoints:   float64(8),
			FieldAssignedTo: map[string]interface{}{
				"displayName": "Jane Doe",
				"uniqueName":  "jane@example.com",
			},
		},
	}

	ti := adoWorkItemToTrackerIssue(wi)

	if ti.ID != "42" {
		t.Errorf("ID = %q, want %q", ti.ID, "42")
	}
	if ti.Identifier != "42" {
		t.Errorf("Identifier = %q, want %q", ti.Identifier, "42")
	}
	if ti.Title != "Test Work Item" {
		t.Errorf("Title = %q, want %q", ti.Title, "Test Work Item")
	}
	if ti.Description != "<p>Description HTML</p>" {
		t.Errorf("Description = %q, want %q", ti.Description, "<p>Description HTML</p>")
	}
	if ti.State != "Active" {
		t.Errorf("State = %v, want %q", ti.State, "Active")
	}
	if ti.Type != "Bug" {
		t.Errorf("Type = %v, want %q", ti.Type, "Bug")
	}
	if ti.Priority != 2 {
		t.Errorf("Priority = %d, want %d", ti.Priority, 2)
	}
	if len(ti.Labels) != 3 || ti.Labels[0] != "tag1" || ti.Labels[1] != "tag2" || ti.Labels[2] != "tag3" {
		t.Errorf("Labels = %v, want [tag1, tag2, tag3]", ti.Labels)
	}
	if ti.Assignee != "Jane Doe" {
		t.Errorf("Assignee = %q, want %q", ti.Assignee, "Jane Doe")
	}
	if ti.AssigneeEmail != "jane@example.com" {
		t.Errorf("AssigneeEmail = %q, want %q", ti.AssigneeEmail, "jane@example.com")
	}

	wantCreated, _ := time.Parse(time.RFC3339Nano, "2024-01-15T10:30:00.000Z")
	if !ti.CreatedAt.Equal(wantCreated) {
		t.Errorf("CreatedAt = %v, want %v", ti.CreatedAt, wantCreated)
	}
	wantUpdated, _ := time.Parse(time.RFC3339Nano, "2024-06-20T14:45:00.000Z")
	if !ti.UpdatedAt.Equal(wantUpdated) {
		t.Errorf("UpdatedAt = %v, want %v", ti.UpdatedAt, wantUpdated)
	}

	if ti.Raw != wi {
		t.Error("Raw should reference the original WorkItem")
	}

	// Check metadata
	if ti.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
	if rev, ok := ti.Metadata["ado.rev"]; !ok || rev != 5 {
		t.Errorf("Metadata[ado.rev] = %v, want 5", rev)
	}
	if ap, ok := ti.Metadata["ado.area_path"]; !ok || ap != "proj\\Team1" {
		t.Errorf("Metadata[ado.area_path] = %v, want %q", ap, "proj\\Team1")
	}
	if ip, ok := ti.Metadata["ado.iteration_path"]; !ok || ip != "proj\\Sprint 5" {
		t.Errorf("Metadata[ado.iteration_path] = %v, want %q", ip, "proj\\Sprint 5")
	}
	if sp, ok := ti.Metadata["ado.story_points"]; !ok || sp != float64(8) {
		t.Errorf("Metadata[ado.story_points] = %v, want 8", sp)
	}

	// Verify URL was constructed from API URL
	wantURL := "https://dev.azure.com/org/proj/_workitems/edit/42"
	if ti.URL != wantURL {
		t.Errorf("URL = %q, want %q", ti.URL, wantURL)
	}
}

func TestAdoWorkItemToTrackerIssue_AssigneeVariants(t *testing.T) {
	tests := []struct {
		name         string
		assignedTo   interface{}
		wantAssignee string
		wantEmail    string
	}{
		{
			name:         "string assignee",
			assignedTo:   "John Smith",
			wantAssignee: "John Smith",
			wantEmail:    "",
		},
		{
			name: "identity map",
			assignedTo: map[string]interface{}{
				"displayName": "Jane Doe",
				"uniqueName":  "jane@corp.com",
			},
			wantAssignee: "Jane Doe",
			wantEmail:    "jane@corp.com",
		},
		{
			name:         "nil assignee",
			assignedTo:   nil,
			wantAssignee: "",
			wantEmail:    "",
		},
		{
			name: "identity map without uniqueName",
			assignedTo: map[string]interface{}{
				"displayName": "Bob",
			},
			wantAssignee: "Bob",
			wantEmail:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := map[string]interface{}{
				FieldTitle: "test",
			}
			if tt.assignedTo != nil {
				fields[FieldAssignedTo] = tt.assignedTo
			}
			wi := &WorkItem{
				ID:     1,
				Fields: fields,
			}
			ti := adoWorkItemToTrackerIssue(wi)
			if ti.Assignee != tt.wantAssignee {
				t.Errorf("Assignee = %q, want %q", ti.Assignee, tt.wantAssignee)
			}
			if ti.AssigneeEmail != tt.wantEmail {
				t.Errorf("AssigneeEmail = %q, want %q", ti.AssigneeEmail, tt.wantEmail)
			}
		})
	}
}

func TestAdoWorkItemToTrackerIssue_EmptyFields(t *testing.T) {
	wi := &WorkItem{
		ID:     1,
		Fields: map[string]interface{}{},
	}
	ti := adoWorkItemToTrackerIssue(wi)
	if ti.ID != "1" {
		t.Errorf("ID = %q, want %q", ti.ID, "1")
	}
	if ti.Title != "" {
		t.Errorf("Title = %q, want empty", ti.Title)
	}
	if ti.Labels != nil {
		t.Errorf("Labels = %v, want nil", ti.Labels)
	}
	if ti.CreatedAt.IsZero() != true {
		t.Error("CreatedAt should be zero for missing field")
	}
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		name string
		pat  string
		want string
	}{
		{name: "normal token", pat: "abcdefghij", want: "abcd******"},
		{name: "short token", pat: "abc", want: "****"},
		{name: "exactly 4 chars", pat: "abcd", want: "****"},
		{name: "5 chars", pat: "abcde", want: "abcd*"},
		{name: "empty", pat: "", want: "****"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskToken(tt.pat); got != tt.want {
				t.Errorf("maskToken(%q) = %q, want %q", tt.pat, got, tt.want)
			}
		})
	}
}

func TestTracker_Registration(t *testing.T) {
	factory := tracker.Get("ado")
	if factory == nil {
		t.Fatal("tracker 'ado' not registered")
	}
	tr := factory()
	if tr == nil {
		t.Fatal("factory returned nil")
	}
	if tr.Name() != "ado" {
		t.Errorf("Name() = %q, want %q", tr.Name(), "ado")
	}
}

func TestTracker_FieldMapper(t *testing.T) {
	tr := &Tracker{
		mapper: NewFieldMapper(nil, nil),
	}
	fm := tr.FieldMapper()
	if fm == nil {
		t.Fatal("FieldMapper() returned nil")
	}
}

func TestTracker_GetConfig_Precedence(t *testing.T) {
	// Config store value takes precedence over env var.
	t.Setenv("AZURE_DEVOPS_PAT", "env-pat")
	store := newMockStore(map[string]string{
		"ado.pat": "config-pat",
	})
	tr := &Tracker{store: store}
	got := tr.getConfig(context.Background(), "ado.pat", "AZURE_DEVOPS_PAT")
	if got != "config-pat" {
		t.Errorf("getConfig() = %q, want %q (config should win)", got, "config-pat")
	}
}

func TestTracker_GetConfig_EnvFallback(t *testing.T) {
	t.Setenv("AZURE_DEVOPS_PAT", "env-pat")
	store := newMockStore(nil)
	tr := &Tracker{store: store}
	got := tr.getConfig(context.Background(), "ado.pat", "AZURE_DEVOPS_PAT")
	if got != "env-pat" {
		t.Errorf("getConfig() = %q, want %q (env fallback)", got, "env-pat")
	}
}

func TestTracker_GetConfig_NotFound(t *testing.T) {
	store := newMockStore(nil)
	tr := &Tracker{store: store}
	got := tr.getConfig(context.Background(), "ado.pat", "NONEXISTENT_ENV_VAR_FOR_TEST")
	if got != "" {
		t.Errorf("getConfig() = %q, want empty", got)
	}
}

func TestAdoWorkItemToTrackerIssue_URLConstruction(t *testing.T) {
	tests := []struct {
		name    string
		apiURL  string
		id      int
		wantURL string
	}{
		{
			name:    "standard API URL",
			apiURL:  "https://dev.azure.com/org/proj/_apis/wit/workItems/42",
			id:      42,
			wantURL: "https://dev.azure.com/org/proj/_workitems/edit/42",
		},
		{
			name:    "empty API URL",
			apiURL:  "",
			id:      10,
			wantURL: "",
		},
		{
			name:    "non-standard URL without _apis",
			apiURL:  "https://custom-tfs.com/item/42",
			id:      42,
			wantURL: "https://custom-tfs.com/item/42",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wi := &WorkItem{
				ID:     tt.id,
				URL:    tt.apiURL,
				Fields: map[string]interface{}{},
			}
			ti := adoWorkItemToTrackerIssue(wi)
			if ti.URL != tt.wantURL {
				t.Errorf("URL = %q, want %q", ti.URL, tt.wantURL)
			}
			if ti.Identifier != strconv.Itoa(tt.id) {
				t.Errorf("Identifier = %q, want %q", ti.Identifier, strconv.Itoa(tt.id))
			}
		})
	}
}

// contains is a test helper for substring matching.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
