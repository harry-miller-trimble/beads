package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// adoWorkItemPattern matches ADO work item URLs containing /_workitems/edit/{digits}.
var adoWorkItemPattern = regexp.MustCompile(`/_workitems/edit/(\d+)`)

// adoShorthandPattern matches the "ado:{digits}" shorthand produced by BuildExternalRef
// when a full URL cannot be constructed (e.g., missing org/project config).
var adoShorthandPattern = regexp.MustCompile(`^ado:([1-9]\d*)$`)

// Tracker implements tracker.IssueTracker for Azure DevOps. It is registered
// under the name "ado" and supports bidirectional sync of work items between
// ADO and the local beads database.
type Tracker struct {
	client   *Client
	mapper   FieldMapper
	baseURL  string // Resolved base URL for external ref matching
	org      string
	projects []string     // one or more project names (first is primary)
	filters  *PullFilters // Optional pull filters for WIQL queries
}

// SetProjects sets project names before Init(). When set, Init() uses these
// instead of reading from config. This supports the --project CLI flag.
func (t *Tracker) SetProjects(projects []string) {
	t.projects = projects
}

// Projects returns the list of configured project names.
func (t *Tracker) Projects() []string {
	return t.projects
}

// PrimaryProject returns the first configured project name.
func (t *Tracker) PrimaryProject() string {
	if len(t.projects) == 0 {
		return ""
	}
	return t.projects[0]
}

// Name returns the lowercase identifier for this tracker.
func (t *Tracker) Name() string { return "ado" }

// DisplayName returns the human-readable name for this tracker.
func (t *Tracker) DisplayName() string { return "Azure DevOps" }

// ConfigPrefix returns the config key prefix for this tracker.
func (t *Tracker) ConfigPrefix() string { return "ado" }

// Init initializes the tracker from environment variables.
// No network calls are made during initialization.
func (t *Tracker) Init(ctx context.Context) error {
	pat := os.Getenv("AZURE_DEVOPS_PAT")
	if pat == "" {
		return fmt.Errorf("Azure DevOps PAT not configured (set AZURE_DEVOPS_PAT)")
	}

	t.org = os.Getenv("AZURE_DEVOPS_ORG")
	customURL := os.Getenv("AZURE_DEVOPS_URL")

	if t.org == "" && customURL == "" {
		return fmt.Errorf("Azure DevOps organization not configured (set AZURE_DEVOPS_ORG)")
	}

	// Resolve projects: use pre-set projects (from CLI), or fall back to env vars.
	if len(t.projects) == 0 {
		pluralVal := os.Getenv("AZURE_DEVOPS_PROJECTS")
		singularVal := os.Getenv("AZURE_DEVOPS_PROJECT")
		t.projects = resolveProjectIDs(pluralVal, singularVal)
	}
	if len(t.projects) == 0 {
		return fmt.Errorf("Azure DevOps project not configured (set AZURE_DEVOPS_PROJECT or AZURE_DEVOPS_PROJECTS)")
	}

	if t.org != "" {
		if err := ValidateOrg(t.org); err != nil {
			return fmt.Errorf("invalid Azure DevOps organization: %w", err)
		}
	}
	for _, p := range t.projects {
		if err := ValidateProject(p); err != nil {
			return fmt.Errorf("invalid Azure DevOps project %q: %w", p, err)
		}
	}

	// State/type mappings can be added via env vars in the future; for now, use defaults.
	t.mapper = NewFieldMapper(nil, nil)

	// Create client with primary project for API URL construction.
	t.client = NewClient(NewSecretString(pat), t.org, t.PrimaryProject())
	if customURL != "" {
		var err error
		t.client, err = t.client.WithBaseURL(customURL)
		if err != nil {
			return fmt.Errorf("invalid Azure DevOps URL: %w", err)
		}
		t.baseURL = strings.TrimSuffix(customURL, "/")
	} else if t.org != "" {
		t.baseURL = DefaultBaseURL + "/" + t.org
	}

	return nil
}

// resolveProjectIDs resolves effective project names from config values.
func resolveProjectIDs(pluralVal, singularVal string) []string {
	if pluralVal != "" {
		parts := strings.Split(pluralVal, ",")
		var result []string
		seen := make(map[string]bool)
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" && !seen[p] {
				seen[p] = true
				result = append(result, p)
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	if singularVal != "" {
		return []string{singularVal}
	}
	return nil
}

// Validate checks that the tracker is properly configured and can connect
// to the Azure DevOps API.
func (t *Tracker) Validate() error {
	if t.client == nil {
		return fmt.Errorf("Azure DevOps tracker not initialized")
	}
	ctx := context.Background()
	_, err := t.client.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("Azure DevOps validation failed: %w", err)
	}
	return nil
}

// Close releases any resources held by the tracker.
func (t *Tracker) Close() error { return nil }

// ADOClient returns the underlying ADO API client.
// Callers use this for operations like link sync that need direct API access.
func (t *Tracker) ADOClient() *Client { return t.client }

// SetFilters configures pull filters for WIQL queries.
// When set, FetchIssues will only return work items matching these filters.
func (t *Tracker) SetFilters(f *PullFilters) { t.filters = f }

// FetchIssues retrieves work items from Azure DevOps. If opts.Since is set,
// only work items changed after that time are fetched (incremental sync);
// otherwise all matching work items in the project are returned (full sync).
func (t *Tracker) FetchIssues(ctx context.Context, opts FetchOptions) ([]TrackerIssue, error) {
	var items []WorkItem
	var err error

	if opts.Since != nil {
		items, err = t.client.FetchWorkItemsSinceMulti(ctx, *opts.Since, t.projects, t.filters)
	} else {
		items, err = t.client.FetchAllWorkItemsMulti(ctx, t.projects, t.filters)
	}
	if err != nil {
		return nil, err
	}

	result := make([]TrackerIssue, 0, len(items))
	for i := range items {
		result = append(result, adoWorkItemToTrackerIssue(&items[i]))
	}
	return result, nil
}

// FetchIssue retrieves a single work item by its numeric ID.
// Returns nil, nil if the work item doesn't exist.
func (t *Tracker) FetchIssue(ctx context.Context, identifier string) (*TrackerIssue, error) {
	id, err := strconv.Atoi(identifier)
	if err != nil {
		return nil, fmt.Errorf("invalid ADO work item ID %q: %w", identifier, err)
	}
	if id <= 0 {
		return nil, fmt.Errorf("invalid ADO work item ID: must be positive, got %d", id)
	}

	items, err := t.client.FetchWorkItems(ctx, []int{id})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}

	ti := adoWorkItemToTrackerIssue(&items[0])
	return &ti, nil
}

// CreateIssue creates a new work item in Azure DevOps. If the target state
// is not a valid initial state (e.g., "Closed"), the work item is created
// without a state (ADO assigns its default) and then transitioned through
// intermediate states to reach the target.
func (t *Tracker) CreateIssue(ctx context.Context, issue *BeadsIssue) (*TrackerIssue, error) {
	fields := t.mapper.IssueToTracker(issue)
	typeName, _ := t.mapper.TypeToTracker(issue.IssueType).(string)
	if typeName == "" {
		typeName = "Task"
	}

	// Extract and remove the target state from creation fields when it is not
	// a valid initial state. ADO rejects creating items directly in states
	// like "Closed" — they must be created in an initial state and transitioned.
	var targetState string
	if s, ok := fields[FieldState].(string); ok && !isInitialState(s) {
		targetState = s
		delete(fields, FieldState)
	}

	wi, err := t.client.CreateWorkItem(ctx, typeName, fields)
	if err != nil {
		return nil, err
	}

	// Transition to the target state if it differs from the created state.
	if targetState != "" {
		createdState := wi.GetStringField(FieldState)
		if createdState != targetState {
			transitioned, err := t.client.transitionWorkItem(ctx, wi.ID, typeName, createdState, targetState)
			if err != nil {
				// Return the created item even if transition fails — the item
				// exists in ADO but may be in the wrong state.
				ti := adoWorkItemToTrackerIssue(wi)
				return &ti, fmt.Errorf("created work item %d but failed to transition from %q to %q: %w",
					wi.ID, createdState, targetState, err)
			}
			if transitioned != nil {
				wi = transitioned
			}
		}
	}

	ti := adoWorkItemToTrackerIssue(wi)
	return &ti, nil
}

// UpdateIssue updates an existing work item in Azure DevOps.
func (t *Tracker) UpdateIssue(ctx context.Context, externalID string, issue *BeadsIssue) (*TrackerIssue, error) {
	id, err := strconv.Atoi(externalID)
	if err != nil {
		return nil, fmt.Errorf("invalid ADO work item ID %q: %w", externalID, err)
	}
	if id <= 0 {
		return nil, fmt.Errorf("invalid ADO work item ID: must be positive, got %d", id)
	}

	fields := t.mapper.IssueToTracker(issue)
	wi, err := t.client.UpdateWorkItem(ctx, id, fields)
	if err != nil {
		return nil, err
	}

	ti := adoWorkItemToTrackerIssue(wi)
	return &ti, nil
}

// FieldMapper returns the bidirectional field mapper used to convert priorities,
// statuses, types, and issue data between ADO and beads representations.
func (t *Tracker) FieldMapper() FieldMapper {
	return t.mapper
}

// IsExternalRef checks if a URL belongs to this Azure DevOps tracker.
// It recognizes both full ADO URLs and the "ado:{id}" shorthand format
// produced by BuildExternalRef when org/project config is unavailable.
func (t *Tracker) IsExternalRef(ref string) bool {
	if adoShorthandPattern.MatchString(ref) {
		return true
	}
	if !adoWorkItemPattern.MatchString(ref) {
		return false
	}
	if t.baseURL != "" && strings.HasPrefix(ref, t.baseURL) {
		return true
	}
	return strings.Contains(ref, "dev.azure.com") || strings.Contains(ref, "visualstudio.com")
}

// ExtractIdentifier extracts the work item ID from an ADO URL or shorthand ref.
func (t *Tracker) ExtractIdentifier(ref string) string {
	if m := adoShorthandPattern.FindStringSubmatch(ref); len(m) >= 2 {
		return m[1]
	}
	matches := adoWorkItemPattern.FindStringSubmatch(ref)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// BuildExternalRef constructs an Azure DevOps web URL for the given tracker issue.
// It prefers the issue's existing URL, then falls back to constructing one from
// the configured org/project or base URL. Returns an "ado:{id}" URI as a last resort.
func (t *Tracker) BuildExternalRef(issue *TrackerIssue) string {
	if issue.URL != "" {
		return issue.URL
	}
	project := t.PrimaryProject()
	if t.org != "" && project != "" {
		return fmt.Sprintf("%s/%s/%s/_workitems/edit/%s",
			DefaultBaseURL, url.PathEscape(t.org), url.PathEscape(project), issue.Identifier)
	}
	if t.baseURL != "" && project != "" {
		return fmt.Sprintf("%s/%s/_workitems/edit/%s",
			t.baseURL, url.PathEscape(project), issue.Identifier)
	}
	return fmt.Sprintf("ado:%s", issue.Identifier)
}

// adoWorkItemToTrackerIssue converts a WorkItem to a TrackerIssue.
func adoWorkItemToTrackerIssue(wi *WorkItem) TrackerIssue {
	ti := TrackerIssue{
		ID:          strconv.Itoa(wi.ID),
		Identifier:  strconv.Itoa(wi.ID),
		URL:         buildExternalRef(wi),
		Title:       wi.GetStringField(FieldTitle),
		Description: wi.GetStringField(FieldDescription),
		State:       wi.GetStringField(FieldState),
		Type:        wi.GetStringField(FieldWorkItemType),
		Labels:      parseTags(wi.GetStringField(FieldTags)),
		Raw:         wi,
	}

	ti.Priority = wi.GetIntField(FieldPriority)

	if created := wi.GetStringField(FieldCreatedDate); created != "" {
		if ts, err := time.Parse(time.RFC3339Nano, created); err == nil {
			ti.CreatedAt = ts
		}
	}
	if updated := wi.GetStringField(FieldChangedDate); updated != "" {
		if ts, err := time.Parse(time.RFC3339Nano, updated); err == nil {
			ti.UpdatedAt = ts
		}
	}

	// AssignedTo can be a string or identity object.
	switch v := wi.GetField(FieldAssignedTo).(type) {
	case string:
		ti.Assignee = v
	case map[string]interface{}:
		if name, ok := v["displayName"].(string); ok {
			ti.Assignee = name
		}
		if uid, ok := v["uniqueName"].(string); ok {
			ti.AssigneeEmail = uid
		}
	}

	ti.Metadata = map[string]interface{}{
		"ado.rev": wi.Rev,
	}
	if ap := wi.GetStringField(FieldAreaPath); ap != "" {
		ti.Metadata["ado.area_path"] = ap
	}
	if ip := wi.GetStringField(FieldIterationPath); ip != "" {
		ti.Metadata["ado.iteration_path"] = ip
	}
	if sp := wi.GetField(FieldStoryPoints); sp != nil {
		ti.Metadata["ado.story_points"] = sp
	}

	return ti
}
