package ado

import (
	"encoding/json"
	"testing"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

func TestPriorityToBeads(t *testing.T) {
	m := NewFieldMapper(nil, nil)

	tests := []struct {
		name  string
		input interface{}
		want  int
	}{
		{"ADO 1 → beads 0", float64(1), 0},
		{"ADO 2 → beads 1", float64(2), 1},
		{"ADO 3 → beads 2", float64(3), 2},
		{"ADO 4 → beads 3", float64(4), 3},
		{"ADO 0 invalid → default 2", float64(0), 2},
		{"ADO 5 invalid → default 2", float64(5), 2},
		{"nil → default 2", nil, 2},
		{"string wrong type → default 2", "2", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.PriorityToBeads(tt.input)
			if got != tt.want {
				t.Errorf("PriorityToBeads(%v) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestPriorityToTracker(t *testing.T) {
	m := NewFieldMapper(nil, nil)

	tests := []struct {
		name  string
		input int
		want  int
	}{
		{"beads 0 → ADO 1", 0, 1},
		{"beads 1 → ADO 2", 1, 2},
		{"beads 2 → ADO 3", 2, 3},
		{"beads 3 → ADO 4", 3, 4},
		{"beads 4 → ADO 4 lossy", 4, 4},
		{"beads -1 → ADO 3 default", -1, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.PriorityToTracker(tt.input)
			if got != tt.want {
				t.Errorf("PriorityToTracker(%d) = %v, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestStatusToBeads_Defaults(t *testing.T) {
	m := NewFieldMapper(nil, nil)

	tests := []struct {
		name  string
		input interface{}
		want  types.Status
	}{
		{"New → open", "New", types.StatusOpen},
		{"Active → in_progress", "Active", types.StatusInProgress},
		{"Removed → deferred", "Removed", types.StatusDeferred},
		{"Closed → closed", "Closed", types.StatusClosed},
		{"Resolved → closed", "Resolved", types.StatusClosed},
		{"empty → open", "", types.StatusOpen},
		{"unknown → open", "CustomState", types.StatusOpen},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.StatusToBeads(tt.input)
			if got != tt.want {
				t.Errorf("StatusToBeads(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStatusToBeads_CustomMap(t *testing.T) {
	m := NewFieldMapper(
		map[string]string{"in_progress": "Doing", "closed": "Finished"},
		nil,
	)

	tests := []struct {
		name  string
		input string
		want  types.Status
	}{
		{"custom Doing → in_progress", "Doing", types.StatusInProgress},
		{"custom Finished → closed", "Finished", types.StatusClosed},
		{"fallthrough New → open", "New", types.StatusOpen},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.StatusToBeads(tt.input)
			if got != tt.want {
				t.Errorf("StatusToBeads(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStatusToTracker_Defaults(t *testing.T) {
	m := NewFieldMapper(nil, nil)

	tests := []struct {
		name  string
		input types.Status
		want  string
	}{
		{"open → New", types.StatusOpen, "New"},
		{"in_progress → Active", types.StatusInProgress, "Active"},
		{"blocked → Active", types.StatusBlocked, "Active"},
		{"deferred → Removed", types.StatusDeferred, "Removed"},
		{"closed → Closed", types.StatusClosed, "Closed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.StatusToTracker(tt.input)
			if got != tt.want {
				t.Errorf("StatusToTracker(%q) = %v, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStatusToTracker_CustomMap(t *testing.T) {
	m := NewFieldMapper(
		map[string]string{"in_progress": "Doing", "closed": "Finished"},
		nil,
	)

	tests := []struct {
		name  string
		input types.Status
		want  string
	}{
		{"custom in_progress → Doing", types.StatusInProgress, "Doing"},
		{"custom closed → Finished", types.StatusClosed, "Finished"},
		{"fallthrough open → New", types.StatusOpen, "New"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.StatusToTracker(tt.input)
			if got != tt.want {
				t.Errorf("StatusToTracker(%q) = %v, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTypeToBeads_Defaults(t *testing.T) {
	m := NewFieldMapper(nil, nil)

	tests := []struct {
		name  string
		input interface{}
		want  types.IssueType
	}{
		{"Bug → bug", "Bug", types.TypeBug},
		{"User Story → feature", "User Story", types.TypeFeature},
		{"Task → task", "Task", types.TypeTask},
		{"Epic → epic", "Epic", types.TypeEpic},
		{"bug lowercase → bug", "bug", types.TypeBug},
		{"user story lowercase → feature", "user story", types.TypeFeature},
		{"empty → task", "", types.TypeTask},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.TypeToBeads(tt.input)
			if got != tt.want {
				t.Errorf("TypeToBeads(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTypeToBeads_Scrum(t *testing.T) {
	m := NewFieldMapper(nil, nil)

	got := m.TypeToBeads("Product Backlog Item")
	if got != types.TypeFeature {
		t.Errorf("TypeToBeads(Product Backlog Item) = %q, want %q", got, types.TypeFeature)
	}
}

func TestTypeToTracker_Defaults(t *testing.T) {
	m := NewFieldMapper(nil, nil)

	tests := []struct {
		name  string
		input types.IssueType
		want  string
	}{
		{"bug → Bug", types.TypeBug, "Bug"},
		{"feature → User Story", types.TypeFeature, "User Story"},
		{"task → Task", types.TypeTask, "Task"},
		{"epic → Epic", types.TypeEpic, "Epic"},
		{"chore → Task", types.TypeChore, "Task"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.TypeToTracker(tt.input)
			if got != tt.want {
				t.Errorf("TypeToTracker(%q) = %v, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTypeToTracker_CustomMap(t *testing.T) {
	m := NewFieldMapper(nil, map[string]string{"feature": "Product Backlog Item"})

	got := m.TypeToTracker(types.TypeFeature)
	if got != "Product Backlog Item" {
		t.Errorf("TypeToTracker(feature) = %v, want %q", got, "Product Backlog Item")
	}

	// Non-overridden types still use defaults.
	got = m.TypeToTracker(types.TypeBug)
	if got != "Bug" {
		t.Errorf("TypeToTracker(bug) = %v, want %q", got, "Bug")
	}
}

func TestIssueToBeads(t *testing.T) {
	m := NewFieldMapper(nil, nil)

	wi := &WorkItem{
		ID:  42,
		Rev: 7,
		URL: "https://dev.azure.com/myorg/myproject/_apis/wit/workItems/42",
		Fields: map[string]interface{}{
			FieldTitle:         "Fix login bug",
			FieldDescription:   "<p>Users cannot log in</p>",
			FieldPriority:      float64(1),
			FieldState:         "Active",
			FieldWorkItemType:  "Bug",
			FieldTags:          "urgent; beads:blocked; frontend",
			FieldAreaPath:      "MyProject\\Team1",
			FieldIterationPath: "MyProject\\Sprint 5",
			FieldStoryPoints:   float64(3),
			FieldRemainingWork: float64(2),
			FieldAssignedTo: map[string]interface{}{
				"displayName": "Alice Smith",
				"uniqueName":  "alice@example.com",
			},
		},
	}

	ti := &tracker.TrackerIssue{
		ID:  "42",
		Raw: wi,
	}

	conv := m.IssueToBeads(ti)
	if conv == nil {
		t.Fatal("IssueToBeads returned nil")
	}

	issue := conv.Issue
	if issue.Title != "Fix login bug" {
		t.Errorf("Title = %q, want %q", issue.Title, "Fix login bug")
	}
	if issue.Description != "Users cannot log in" {
		t.Errorf("Description = %q, want %q", issue.Description, "Users cannot log in")
	}
	if issue.Priority != 0 {
		t.Errorf("Priority = %d, want 0", issue.Priority)
	}
	if issue.Status != types.StatusInProgress {
		t.Errorf("Status = %q, want %q", issue.Status, types.StatusInProgress)
	}
	if issue.IssueType != types.TypeBug {
		t.Errorf("IssueType = %q, want %q", issue.IssueType, types.TypeBug)
	}
	if issue.Owner != "Alice Smith" {
		t.Errorf("Owner = %q, want %q", issue.Owner, "Alice Smith")
	}

	// Labels should exclude beads:* tags.
	wantLabels := []string{"urgent", "frontend"}
	if len(issue.Labels) != len(wantLabels) {
		t.Fatalf("Labels = %v, want %v", issue.Labels, wantLabels)
	}
	for i, l := range issue.Labels {
		if l != wantLabels[i] {
			t.Errorf("Labels[%d] = %q, want %q", i, l, wantLabels[i])
		}
	}

	// External ref should be the web URL.
	wantRef := "https://dev.azure.com/myorg/myproject/_workitems/edit/42"
	if issue.ExternalRef == nil || *issue.ExternalRef != wantRef {
		got := "<nil>"
		if issue.ExternalRef != nil {
			got = *issue.ExternalRef
		}
		t.Errorf("ExternalRef = %s, want %s", got, wantRef)
	}

	// Verify metadata preservation.
	if len(issue.Metadata) == 0 {
		t.Fatal("Metadata is empty")
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(issue.Metadata, &meta); err != nil {
		t.Fatalf("failed to unmarshal metadata: %v", err)
	}
	if meta["ado.area_path"] != "MyProject\\Team1" {
		t.Errorf("metadata ado.area_path = %v, want %q", meta["ado.area_path"], "MyProject\\Team1")
	}
	if meta["ado.iteration_path"] != "MyProject\\Sprint 5" {
		t.Errorf("metadata ado.iteration_path = %v, want %q", meta["ado.iteration_path"], "MyProject\\Sprint 5")
	}
	if meta["ado.story_points"] != float64(3) {
		t.Errorf("metadata ado.story_points = %v, want 3", meta["ado.story_points"])
	}
	if meta["ado.rev"] != float64(7) {
		t.Errorf("metadata ado.rev = %v, want 7", meta["ado.rev"])
	}
}

func TestIssueToBeads_NilRaw(t *testing.T) {
	m := NewFieldMapper(nil, nil)

	// nil TrackerIssue.
	if conv := m.IssueToBeads(nil); conv != nil {
		t.Error("IssueToBeads(nil) should return nil")
	}

	// Wrong Raw type.
	ti := &tracker.TrackerIssue{Raw: "not a WorkItem"}
	if conv := m.IssueToBeads(ti); conv != nil {
		t.Error("IssueToBeads(wrong type) should return nil")
	}

	// Nil Raw.
	ti = &tracker.TrackerIssue{Raw: (*WorkItem)(nil)}
	if conv := m.IssueToBeads(ti); conv != nil {
		t.Error("IssueToBeads(nil WorkItem) should return nil")
	}
}

func TestIssueToTracker(t *testing.T) {
	m := NewFieldMapper(nil, nil)

	meta, _ := json.Marshal(map[string]interface{}{
		"ado.area_path":      "Project\\TeamA",
		"ado.iteration_path": "Project\\Sprint 3",
		"ado.story_points":   float64(5),
	})

	issue := &types.Issue{
		Title:       "Implement login",
		Description: "Add OAuth2 support",
		Status:      types.StatusInProgress,
		Priority:    1,
		IssueType:   types.TypeFeature,
		Labels:      []string{"auth", "backend"},
		Metadata:    json.RawMessage(meta),
	}

	fields := m.IssueToTracker(issue)

	if fields[FieldTitle] != "Implement login" {
		t.Errorf("Title = %v, want %q", fields[FieldTitle], "Implement login")
	}
	if fields[FieldState] != "Active" {
		t.Errorf("State = %v, want %q", fields[FieldState], "Active")
	}
	if fields[FieldPriority] != 2 {
		t.Errorf("Priority = %v, want 2", fields[FieldPriority])
	}
	if fields[FieldTags] != "auth; backend" {
		t.Errorf("Tags = %v, want %q", fields[FieldTags], "auth; backend")
	}

	// Description should be HTML.
	desc, ok := fields[FieldDescription].(string)
	if !ok || desc == "" {
		t.Error("Description should be non-empty HTML string")
	}

	// Metadata fields should be restored.
	if fields[FieldAreaPath] != "Project\\TeamA" {
		t.Errorf("AreaPath = %v, want %q", fields[FieldAreaPath], "Project\\TeamA")
	}
	if fields[FieldIterationPath] != "Project\\Sprint 3" {
		t.Errorf("IterationPath = %v, want %q", fields[FieldIterationPath], "Project\\Sprint 3")
	}
	if fields[FieldStoryPoints] != float64(5) {
		t.Errorf("StoryPoints = %v, want 5", fields[FieldStoryPoints])
	}
}

func TestParseTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"semicolon separated", "tag1; tag2; tag3", []string{"tag1", "tag2", "tag3"}},
		{"no spaces", "tag1;tag2;tag3", []string{"tag1", "tag2", "tag3"}},
		{"empty string", "", nil},
		{"whitespace only", "  ", nil},
		{"single tag", "solo", []string{"solo"}},
		{"trailing semicolons", "a; b; ", []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTags(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("parseTags(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseTags(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestBuildTagString(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  string
	}{
		{"multiple tags", []string{"tag1", "tag2"}, "tag1; tag2"},
		{"single tag", []string{"solo"}, "solo"},
		{"empty slice", []string{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildTagString(tt.input)
			if got != tt.want {
				t.Errorf("buildTagString(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFilterBeadsTags(t *testing.T) {
	input := []string{"bug", "beads:blocked", "urgent"}
	got := filterBeadsTags(input)
	want := []string{"bug", "urgent"}

	if len(got) != len(want) {
		t.Fatalf("filterBeadsTags(%v) = %v, want %v", input, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("filterBeadsTags[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHasBeadsTag(t *testing.T) {
	tests := []struct {
		name   string
		tagStr string
		tag    string
		want   bool
	}{
		{"present", "urgent; beads:blocked; frontend", "beads:blocked", true},
		{"absent", "urgent; frontend", "beads:blocked", false},
		{"empty string", "", "beads:blocked", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasBeadsTag(tt.tagStr, tt.tag)
			if got != tt.want {
				t.Errorf("hasBeadsTag(%q, %q) = %v, want %v", tt.tagStr, tt.tag, got, tt.want)
			}
		})
	}
}
