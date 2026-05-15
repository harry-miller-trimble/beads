package main

// Wire types for the tracker driver protocol.
// These replace the beads-internal types (internal/tracker, internal/types)
// so bd-ado has zero beads imports.

import "time"

// TrackerIssue is the wire format for issues returned over the driver protocol.
// Field names must match the Go struct field names in internal/tracker.TrackerIssue
// since the MCP adapter deserializes JSON directly into that struct.
type TrackerIssue struct {
	ID         string `json:"ID"`
	Identifier string `json:"Identifier"`
	URL        string `json:"URL"`

	Title       string `json:"Title"`
	Description string `json:"Description"`

	Priority int         `json:"Priority"`
	State    interface{} `json:"State"`
	Type     interface{} `json:"Type"`
	Labels   []string    `json:"Labels,omitempty"`

	Assignee      string `json:"Assignee,omitempty"`
	AssigneeID    string `json:"AssigneeID,omitempty"`
	AssigneeEmail string `json:"AssigneeEmail,omitempty"`

	CreatedAt   time.Time  `json:"CreatedAt"`
	UpdatedAt   time.Time  `json:"UpdatedAt"`
	CompletedAt *time.Time `json:"CompletedAt,omitempty"`

	ParentID         string `json:"ParentID,omitempty"`
	ParentInternalID string `json:"ParentInternalID,omitempty"`

	Raw      interface{}            `json:"Raw,omitempty"`
	Metadata map[string]interface{} `json:"Metadata,omitempty"`
}

// FetchOptions mirrors tracker.FetchOptions.
type FetchOptions struct {
	State string     `json:"state,omitempty"`
	Since *time.Time `json:"since,omitempty"`
	Limit int        `json:"limit,omitempty"`
}

// DependencyInfo mirrors tracker.DependencyInfo.
type DependencyInfo struct {
	FromExternalID string `json:"from_external_id"`
	ToExternalID   string `json:"to_external_id"`
	Type           string `json:"type"`
	TargetID       string `json:"target_id,omitempty"`
	TargetURL      string `json:"target_url,omitempty"`
	TargetRef      string `json:"target_ref,omitempty"`
	TargetName     string `json:"target_name,omitempty"`
}

// IssueConversion holds a converted issue + discovered dependencies.
type IssueConversion struct {
	Issue        *BeadsIssue      `json:"issue"`
	Dependencies []DependencyInfo `json:"dependencies,omitempty"`
}

// BeadsIssue is a minimal representation of internal/types.Issue for
// the driver wire format. Only the fields needed for create/update.
type BeadsIssue struct {
	ID           string                 `json:"id,omitempty"`
	Title        string                 `json:"title"`
	Description  string                 `json:"description,omitempty"`
	Status       string                 `json:"status,omitempty"`
	Priority     int                    `json:"priority"`
	IssueType    string                 `json:"issue_type,omitempty"`
	Assignee     string                 `json:"assignee,omitempty"`
	Labels       []string               `json:"labels,omitempty"`
	ExternalRef  string                 `json:"external_ref,omitempty"`
	ExternalID   string                 `json:"external_id,omitempty"`
	SourceSystem string                 `json:"source_system,omitempty"`
	CreatedAt    time.Time              `json:"created_at,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// FieldMapper maps between ADO fields and beads fields.
// This is the local equivalent of tracker.FieldMapper.
type FieldMapper interface {
	StatusToBeads(externalStatus interface{}) string
	StatusToTracker(beadsStatus string) interface{}
	PriorityToBeads(externalPriority interface{}) int
	PriorityToTracker(beadsPriority int) interface{}
	TypeToBeads(externalType interface{}) string
	TypeToTracker(beadsType string) interface{}
	IssueToBeads(trackerIssue *TrackerIssue) *IssueConversion
	IssueToTracker(issue *BeadsIssue) map[string]interface{}
}

// Status constants matching internal/types.
const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusBlocked    = "blocked"
	StatusDeferred   = "deferred"
	StatusClosed     = "closed"
)

// IssueType constants matching internal/types.
const (
	TypeTask    = "task"
	TypeBug     = "bug"
	TypeFeature = "feature"
	TypeEpic    = "epic"
	TypeChore   = "chore"
)
