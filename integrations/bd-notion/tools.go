package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// toolDefinitions returns the MCP tool list for this provider plugin.
func toolDefinitions() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "tracker_info",
			"description": "Returns tracker metadata (name, display name, config prefix).",
		},
		{
			"name":        "validate",
			"description": "Validates the Notion connection and configuration.",
		},
		{
			"name":        "fetch_issues",
			"description": "Fetches issues from the configured Notion database.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": {
					"state":  {"type": "string", "description": "Filter by state (open, closed, all)"},
					"since":  {"type": "string", "description": "ISO 8601 timestamp, fetch issues updated after this time"},
					"limit":  {"type": "integer", "description": "Maximum number of issues to return"}
				}
			}`),
		},
		{
			"name":        "fetch_issue",
			"description": "Fetches a single issue by its Notion page identifier.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": {
					"identifier": {"type": "string", "description": "Notion page ID or URL"}
				},
				"required": ["identifier"]
			}`),
		},
		{
			"name":        "create_issue",
			"description": "Creates a new issue in the Notion database.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": {
					"issue": {"type": "object", "description": "Issue data with title, description, priority, status, etc."}
				},
				"required": ["issue"]
			}`),
		},
		{
			"name":        "update_issue",
			"description": "Updates an existing issue in the Notion database.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": {
					"external_id": {"type": "string", "description": "Notion page ID"},
					"issue":       {"type": "object", "description": "Updated issue data"}
				},
				"required": ["external_id", "issue"]
			}`),
		},
		{
			"name":        "is_external_ref",
			"description": "Checks whether a reference string is a valid Notion page reference.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": {
					"ref": {"type": "string"}
				},
				"required": ["ref"]
			}`),
		},
		{
			"name":        "extract_identifier",
			"description": "Extracts a Notion page identifier from a URL or reference string.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": {
					"ref": {"type": "string"}
				},
				"required": ["ref"]
			}`),
		},
		{
			"name":        "build_external_ref",
			"description": "Builds a canonical Notion external reference URL.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": {
					"id":         {"type": "string"},
					"identifier": {"type": "string"},
					"url":        {"type": "string"}
				}
			}`),
		},
		{
			"name":        "priority_to_beads",
			"description": "Converts a Notion priority string to a beads priority integer.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": { "value": {} },
				"required": ["value"]
			}`),
		},
		{
			"name":        "priority_to_tracker",
			"description": "Converts a beads priority integer to a Notion priority string.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": { "value": {} },
				"required": ["value"]
			}`),
		},
		{
			"name":        "status_to_beads",
			"description": "Converts a Notion status string to a beads status string.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": { "value": {} },
				"required": ["value"]
			}`),
		},
		{
			"name":        "status_to_tracker",
			"description": "Converts a beads status string to a Notion status string.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": { "value": {} },
				"required": ["value"]
			}`),
		},
		{
			"name":        "type_to_beads",
			"description": "Converts a Notion type string to a beads issue type string.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": { "value": {} },
				"required": ["value"]
			}`),
		},
		{
			"name":        "type_to_tracker",
			"description": "Converts a beads issue type string to a Notion type string.",
			"inputSchema": json.RawMessage(`{
				"type": "object",
				"properties": { "value": {} },
				"required": ["value"]
			}`),
		},
	}
}

// dispatchTool routes a tool call to its handler.
func (s *server) dispatchTool(name string, args map[string]interface{}) (interface{}, error) {
	switch name {
	case "tracker_info":
		return s.toolTrackerInfo()
	case "validate":
		return s.toolValidate()
	case "fetch_issues":
		return s.toolFetchIssues(args)
	case "fetch_issue":
		return s.toolFetchIssue(args)
	case "create_issue":
		return s.toolCreateIssue(args)
	case "update_issue":
		return s.toolUpdateIssue(args)
	case "is_external_ref":
		return s.toolIsExternalRef(args)
	case "extract_identifier":
		return s.toolExtractIdentifier(args)
	case "build_external_ref":
		return s.toolBuildExternalRef(args)
	case "priority_to_beads":
		return s.toolPriorityToBeads(args)
	case "priority_to_tracker":
		return s.toolPriorityToTracker(args)
	case "status_to_beads":
		return s.toolStatusToBeads(args)
	case "status_to_tracker":
		return s.toolStatusToTracker(args)
	case "type_to_beads":
		return s.toolTypeToBeads(args)
	case "type_to_tracker":
		return s.toolTypeToTracker(args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// --- Tool implementations ---

func (s *server) toolTrackerInfo() (interface{}, error) {
	return map[string]interface{}{
		"name":          "notion",
		"display_name":  "Notion",
		"config_prefix": "notion",
	}, nil
}

func (s *server) toolValidate() (interface{}, error) {
	if s.client == nil {
		return map[string]interface{}{
			"ok":    false,
			"error": "NOTION_TOKEN not set",
		}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := s.client.GetCurrentUser(ctx)
	if err != nil {
		return map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		}, nil
	}
	return map[string]interface{}{"ok": true}, nil
}

func (s *server) toolFetchIssues(args map[string]interface{}) (interface{}, error) {
	if s.client == nil {
		return nil, fmt.Errorf("NOTION_TOKEN not set")
	}
	dsID, _ := args["data_source_id"].(string)
	if dsID == "" {
		return nil, fmt.Errorf("data_source_id required (set via NOTION_DATA_SOURCE_ID or pass in args)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pages, err := s.client.QueryDataSource(ctx, dsID)
	if err != nil {
		return nil, fmt.Errorf("query data source: %w", err)
	}

	limit := 0
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	issues := make([]wireTrackerIssue, 0, len(pages))
	for i, page := range pages {
		if limit > 0 && i >= limit {
			break
		}
		issues = append(issues, trackerIssueFromPage(page))
	}
	return issues, nil
}

func (s *server) toolFetchIssue(args map[string]interface{}) (interface{}, error) {
	if s.client == nil {
		return nil, fmt.Errorf("NOTION_TOKEN not set")
	}
	identifier, _ := args["identifier"].(string)
	if identifier == "" {
		return nil, fmt.Errorf("identifier required")
	}

	// Normalize to page ID.
	pageID := ExtractNotionIdentifier(identifier)
	if pageID == "" {
		pageID = identifier
	}

	// Notion doesn't have a direct "get page by ID" in our client,
	// so we'd need to add one. For the pilot, return not-found.
	return nil, nil
}

func (s *server) toolCreateIssue(args map[string]interface{}) (interface{}, error) {
	if s.client == nil {
		return nil, fmt.Errorf("NOTION_TOKEN not set")
	}
	dsID, _ := args["data_source_id"].(string)
	if dsID == "" {
		return nil, fmt.Errorf("data_source_id required")
	}

	issueData, ok := args["issue"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("issue object required")
	}

	// Convert issue data to Notion page properties.
	props := buildPagePropertiesFromIssue(issueData)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	page, err := s.client.CreatePage(ctx, dsID, props)
	if err != nil {
		return nil, fmt.Errorf("create page: %w", err)
	}

	return trackerIssueFromPage(*page), nil
}

func (s *server) toolUpdateIssue(args map[string]interface{}) (interface{}, error) {
	if s.client == nil {
		return nil, fmt.Errorf("NOTION_TOKEN not set")
	}
	externalID, _ := args["external_id"].(string)
	if externalID == "" {
		return nil, fmt.Errorf("external_id required")
	}
	issueData, ok := args["issue"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("issue object required")
	}

	props := buildPagePropertiesFromIssue(issueData)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	page, err := s.client.UpdatePage(ctx, externalID, props)
	if err != nil {
		return nil, fmt.Errorf("update page: %w", err)
	}

	return trackerIssueFromPage(*page), nil
}

func (s *server) toolIsExternalRef(args map[string]interface{}) (interface{}, error) {
	ref, _ := args["ref"].(string)
	return IsNotionExternalRef(ref), nil
}

func (s *server) toolExtractIdentifier(args map[string]interface{}) (interface{}, error) {
	ref, _ := args["ref"].(string)
	return ExtractNotionIdentifier(ref), nil
}

func (s *server) toolBuildExternalRef(args map[string]interface{}) (interface{}, error) {
	id, _ := args["id"].(string)
	identifier, _ := args["identifier"].(string)
	url, _ := args["url"].(string)

	// Try URL first, then identifier, then id.
	for _, candidate := range []string{url, identifier, id} {
		if pageID := ExtractNotionIdentifier(candidate); pageID != "" {
			return notionPageURL(pageID), nil
		}
	}
	return "", nil
}

// --- Field mapper tools ---

func (s *server) toolPriorityToBeads(args map[string]interface{}) (interface{}, error) {
	value, _ := args["value"].(string)
	return priorityToBeads(value), nil
}

func (s *server) toolPriorityToTracker(args map[string]interface{}) (interface{}, error) {
	value, _ := args["value"].(float64)
	return priorityToNotion(int(value)), nil
}

func (s *server) toolStatusToBeads(args map[string]interface{}) (interface{}, error) {
	value, _ := args["value"].(string)
	return statusToBeads(value), nil
}

func (s *server) toolStatusToTracker(args map[string]interface{}) (interface{}, error) {
	value, _ := args["value"].(string)
	return statusToNotion(value), nil
}

func (s *server) toolTypeToBeads(args map[string]interface{}) (interface{}, error) {
	value, _ := args["value"].(string)
	return typeToBeads(value), nil
}

func (s *server) toolTypeToTracker(args map[string]interface{}) (interface{}, error) {
	value, _ := args["value"].(string)
	return typeToNotion(value), nil
}

// --- Helpers ---

// wireTrackerIssue is the JSON shape that beads' MCPAdapter deserializes.
type wireTrackerIssue struct {
	ID          string    `json:"id"`
	Identifier  string    `json:"identifier"`
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Priority    int       `json:"priority"`
	State       string    `json:"state"`
	Type        string    `json:"type"`
	Labels      []string  `json:"labels"`
	Assignee    string    `json:"assignee"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// trackerIssueFromPage converts a Notion Page to the wire TrackerIssue format.
func trackerIssueFromPage(page Page) wireTrackerIssue {
	title := DataSourceTitle(page.Properties[PropertyTitle].Title)
	description := DataSourceTitle(page.Properties[PropertyDescription].RichText)
	status := pagePropertySelect(page.Properties[PropertyStatus])
	priority := pagePropertySelect(page.Properties[PropertyPriority])
	issueType := pagePropertySelect(page.Properties[PropertyType])
	assignee := DataSourceTitle(page.Properties[PropertyAssignee].RichText)
	labels := pagePropertyMultiSelect(page.Properties[PropertyLabels])

	pageID := ExtractNotionIdentifier(page.ID)
	if pageID == "" {
		pageID = page.ID
	}

	return wireTrackerIssue{
		ID:          page.ID,
		Identifier:  pageID,
		URL:         notionPageURL(pageID),
		Title:       strings.TrimSpace(title),
		Description: strings.TrimSpace(description),
		Priority:    priorityToBeads(priority),
		State:       statusToBeads(status),
		Type:        typeToBeads(issueType),
		Labels:      labels,
		Assignee:    strings.TrimSpace(assignee),
		CreatedAt:   page.CreatedTime,
		UpdatedAt:   page.LastEditedTime,
	}
}

// buildPagePropertiesFromIssue builds Notion page properties from a generic issue map.
func buildPagePropertiesFromIssue(issue map[string]interface{}) map[string]interface{} {
	props := map[string]interface{}{}

	if title, ok := issue["title"].(string); ok && title != "" {
		props[PropertyTitle] = map[string]interface{}{"title": richTextRequest(title)}
	}
	if desc, ok := issue["description"].(string); ok {
		props[PropertyDescription] = map[string]interface{}{"rich_text": richTextRequest(desc)}
	}
	if id, ok := issue["id"].(string); ok && id != "" {
		props[PropertyBeadsID] = map[string]interface{}{"rich_text": richTextRequest(id)}
	}
	if status, ok := issue["status"].(string); ok && status != "" {
		notionStatus := statusToNotion(status)
		props[PropertyStatus] = map[string]interface{}{"select": map[string]interface{}{"name": notionStatus}}
	}
	if priority, ok := issue["priority"].(float64); ok {
		notionPriority := priorityToNotion(int(priority))
		props[PropertyPriority] = map[string]interface{}{"select": map[string]interface{}{"name": notionPriority}}
	}
	if issueType, ok := issue["issue_type"].(string); ok && issueType != "" {
		notionType := typeToNotion(issueType)
		props[PropertyType] = map[string]interface{}{"select": map[string]interface{}{"name": notionType}}
	}
	if assignee, ok := issue["assignee"].(string); ok {
		props[PropertyAssignee] = map[string]interface{}{"rich_text": richTextRequest(assignee)}
	}
	if labelsRaw, ok := issue["labels"].([]interface{}); ok {
		labels := make([]map[string]interface{}, 0, len(labelsRaw))
		for _, l := range labelsRaw {
			if name, ok := l.(string); ok && name != "" {
				labels = append(labels, map[string]interface{}{"name": name})
			}
		}
		props[PropertyLabels] = map[string]interface{}{"multi_select": labels}
	}

	return props
}
