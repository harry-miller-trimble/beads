package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/steveyegge/beads/internal/plugin"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// MCPAdapter implements IssueTracker by delegating to an external tracker driver
// process over MCP (JSON-RPC 2.0 over stdio). This is the tracker driver protocol:
// bd starts the driver, sends IssueTracker method calls as tool invocations,
// and receives results back — like git credential helpers or Docker storage drivers.
type MCPAdapter struct {
	pluginName  string
	client      *plugin.MCPClient
	manifest    *plugin.Manifest
	trackerInfo *mcpTrackerInfo
	fieldMapper FieldMapper
	callTimeout time.Duration
}

type mcpTrackerInfo struct {
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	ConfigPrefix string `json:"config_prefix"`
}

// MCPAdapterConfig configures how the MCP adapter connects to a plugin.
type MCPAdapterConfig struct {
	// PluginName is the plugin identifier from the lockfile.
	PluginName string
	// Command is the path to the plugin binary.
	Command string
	// Args are additional arguments.
	Args []string
	// EnvAllowlist from the plugin manifest.
	EnvAllowlist []string
	// Manifest is the plugin's manifest (for metadata).
	Manifest *plugin.Manifest
	// CallTimeout for individual MCP tool calls.
	CallTimeout time.Duration
}

// NewMCPAdapter creates an MCP-backed IssueTracker. It starts the plugin
// subprocess, performs the MCP handshake, and fetches tracker metadata.
func NewMCPAdapter(ctx context.Context, cfg MCPAdapterConfig) (*MCPAdapter, error) {
	if cfg.CallTimeout == 0 {
		cfg.CallTimeout = 30 * time.Second
	}

	client, err := plugin.StartMCP(ctx, plugin.MCPClientConfig{
		Command:        cfg.Command,
		Args:           cfg.Args,
		EnvAllowlist:   cfg.EnvAllowlist,
		StartupTimeout: 2 * time.Second,
		CallTimeout:    cfg.CallTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("mcp adapter %q: %w", cfg.PluginName, err)
	}

	adapter := &MCPAdapter{
		pluginName:  cfg.PluginName,
		client:      client,
		manifest:    cfg.Manifest,
		callTimeout: cfg.CallTimeout,
	}

	// Fetch tracker info if the tool is available.
	if client.HasTool("tracker_info") {
		callCtx, cancel := context.WithTimeout(ctx, cfg.CallTimeout)
		defer cancel()
		raw, err := client.CallTool(callCtx, "tracker_info", nil)
		if err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("mcp adapter %q: tracker_info: %w", cfg.PluginName, err)
		}
		var info mcpTrackerInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("mcp adapter %q: parse tracker_info: %w", cfg.PluginName, err)
		}
		adapter.trackerInfo = &info
	} else {
		// Fallback to manifest metadata.
		adapter.trackerInfo = &mcpTrackerInfo{
			Name:         cfg.PluginName,
			DisplayName:  cfg.PluginName,
			ConfigPrefix: cfg.PluginName,
		}
	}

	adapter.fieldMapper = &mcpFieldMapper{client: client, timeout: cfg.CallTimeout}

	return adapter, nil
}

// --- IssueTracker interface ---

func (a *MCPAdapter) Name() string         { return a.trackerInfo.Name }
func (a *MCPAdapter) DisplayName() string  { return a.trackerInfo.DisplayName }
func (a *MCPAdapter) ConfigPrefix() string { return a.trackerInfo.ConfigPrefix }

func (a *MCPAdapter) Init(_ context.Context, _ storage.Storage) error {
	// MCP plugins handle their own configuration via env vars or config files.
	return nil
}

func (a *MCPAdapter) Validate() error {
	if !a.client.HasTool("validate") {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.callTimeout)
	defer cancel()
	raw, err := a.client.CallTool(ctx, "validate", nil)
	if err != nil {
		return fmt.Errorf("plugin %q validation failed: %w", a.pluginName, err)
	}
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("plugin %q: parse validate response: %w", a.pluginName, err)
	}
	if !result.OK {
		return fmt.Errorf("plugin %q: %s", a.pluginName, result.Error)
	}
	return nil
}

func (a *MCPAdapter) Close() error {
	return a.client.Close()
}

func (a *MCPAdapter) FetchIssues(ctx context.Context, opts FetchOptions) ([]TrackerIssue, error) {
	args := map[string]interface{}{}
	if opts.State != "" {
		args["state"] = opts.State
	}
	if opts.Since != nil {
		args["since"] = opts.Since.Format(time.RFC3339)
	}
	if opts.Limit > 0 {
		args["limit"] = opts.Limit
	}

	callCtx, cancel := context.WithTimeout(ctx, a.callTimeout)
	defer cancel()
	raw, err := a.client.CallTool(callCtx, "fetch_issues", args)
	if err != nil {
		return nil, a.wrapError("fetch_issues", err)
	}

	var issues []TrackerIssue
	if err := json.Unmarshal(raw, &issues); err != nil {
		return nil, fmt.Errorf("plugin %q: parse fetch_issues: %w", a.pluginName, err)
	}
	return issues, nil
}

func (a *MCPAdapter) FetchIssue(ctx context.Context, identifier string) (*TrackerIssue, error) {
	callCtx, cancel := context.WithTimeout(ctx, a.callTimeout)
	defer cancel()
	raw, err := a.client.CallTool(callCtx, "fetch_issue", map[string]interface{}{
		"identifier": identifier,
	})
	if err != nil {
		return nil, a.wrapError("fetch_issue", err)
	}

	// null/empty means not found.
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var issue TrackerIssue
	if err := json.Unmarshal(raw, &issue); err != nil {
		return nil, fmt.Errorf("plugin %q: parse fetch_issue: %w", a.pluginName, err)
	}
	return &issue, nil
}

func (a *MCPAdapter) CreateIssue(ctx context.Context, issue *types.Issue) (*TrackerIssue, error) {
	callCtx, cancel := context.WithTimeout(ctx, a.callTimeout)
	defer cancel()
	raw, err := a.client.CallTool(callCtx, "create_issue", map[string]interface{}{
		"issue": issue,
	})
	if err != nil {
		return nil, a.wrapError("create_issue", err)
	}

	var result TrackerIssue
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("plugin %q: parse create_issue: %w", a.pluginName, err)
	}
	return &result, nil
}

func (a *MCPAdapter) UpdateIssue(ctx context.Context, externalID string, issue *types.Issue) (*TrackerIssue, error) {
	callCtx, cancel := context.WithTimeout(ctx, a.callTimeout)
	defer cancel()
	raw, err := a.client.CallTool(callCtx, "update_issue", map[string]interface{}{
		"external_id": externalID,
		"issue":       issue,
	})
	if err != nil {
		return nil, a.wrapError("update_issue", err)
	}

	var result TrackerIssue
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("plugin %q: parse update_issue: %w", a.pluginName, err)
	}
	return &result, nil
}

func (a *MCPAdapter) FieldMapper() FieldMapper {
	return a.fieldMapper
}

func (a *MCPAdapter) IsExternalRef(ref string) bool {
	if !a.client.HasTool("is_external_ref") {
		// Default: check if ref starts with the tracker name.
		return len(ref) > len(a.trackerInfo.Name)+1 &&
			ref[:len(a.trackerInfo.Name)+1] == a.trackerInfo.Name+"-"
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.callTimeout)
	defer cancel()
	raw, err := a.client.CallTool(ctx, "is_external_ref", map[string]interface{}{"ref": ref})
	if err != nil {
		return false
	}
	var result bool
	_ = json.Unmarshal(raw, &result)
	return result
}

func (a *MCPAdapter) ExtractIdentifier(ref string) string {
	if !a.client.HasTool("extract_identifier") {
		return ref // passthrough
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.callTimeout)
	defer cancel()
	raw, err := a.client.CallTool(ctx, "extract_identifier", map[string]interface{}{"ref": ref})
	if err != nil {
		return ref
	}
	var result string
	_ = json.Unmarshal(raw, &result)
	return result
}

func (a *MCPAdapter) BuildExternalRef(issue *TrackerIssue) string {
	if !a.client.HasTool("build_external_ref") {
		return fmt.Sprintf("%s-%s", a.trackerInfo.Name, issue.Identifier)
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.callTimeout)
	defer cancel()
	raw, err := a.client.CallTool(ctx, "build_external_ref", map[string]interface{}{
		"id":         issue.ID,
		"identifier": issue.Identifier,
		"url":        issue.URL,
	})
	if err != nil {
		return fmt.Sprintf("%s-%s", a.trackerInfo.Name, issue.Identifier)
	}
	var result string
	_ = json.Unmarshal(raw, &result)
	return result
}

func (a *MCPAdapter) wrapError(tool string, err error) error {
	return fmt.Errorf("plugin %q: %s: %w", a.pluginName, tool, err)
}

// --- FieldMapper via MCP ---

type mcpFieldMapper struct {
	client  *plugin.MCPClient
	timeout time.Duration
}

func (m *mcpFieldMapper) PriorityToBeads(trackerPriority interface{}) int {
	return m.callMapper("priority_to_beads", trackerPriority, 2).(int)
}

func (m *mcpFieldMapper) PriorityToTracker(beadsPriority int) interface{} {
	return m.callMapperRaw("priority_to_tracker", beadsPriority)
}

func (m *mcpFieldMapper) StatusToBeads(trackerState interface{}) types.Status {
	result := m.callMapper("status_to_beads", trackerState, string(types.StatusOpen))
	if s, ok := result.(string); ok {
		return types.Status(s)
	}
	return types.StatusOpen
}

func (m *mcpFieldMapper) StatusToTracker(beadsStatus types.Status) interface{} {
	return m.callMapperRaw("status_to_tracker", string(beadsStatus))
}

func (m *mcpFieldMapper) TypeToBeads(trackerType interface{}) types.IssueType {
	result := m.callMapper("type_to_beads", trackerType, string(types.TypeTask))
	if s, ok := result.(string); ok {
		return types.IssueType(s)
	}
	return types.TypeTask
}

func (m *mcpFieldMapper) TypeToTracker(beadsType types.IssueType) interface{} {
	return m.callMapperRaw("type_to_tracker", string(beadsType))
}

func (m *mcpFieldMapper) IssueToBeads(trackerIssue *TrackerIssue) *IssueConversion {
	if !m.client.HasTool("issue_to_beads") {
		// Default conversion.
		return &IssueConversion{
			Issue: &types.Issue{
				Title:       trackerIssue.Title,
				Description: trackerIssue.Description,
				Priority:    trackerIssue.Priority,
				Labels:      trackerIssue.Labels,
				Assignee:    trackerIssue.Assignee,
				CreatedAt:   trackerIssue.CreatedAt,
				UpdatedAt:   trackerIssue.UpdatedAt,
			},
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	raw, err := m.client.CallTool(ctx, "issue_to_beads", map[string]interface{}{
		"issue": trackerIssue,
	})
	if err != nil {
		// Fallback to default.
		return &IssueConversion{
			Issue: &types.Issue{
				Title:       trackerIssue.Title,
				Description: trackerIssue.Description,
			},
		}
	}

	var result IssueConversion
	if err := json.Unmarshal(raw, &result); err != nil {
		return &IssueConversion{
			Issue: &types.Issue{
				Title:       trackerIssue.Title,
				Description: trackerIssue.Description,
			},
		}
	}
	return &result
}

func (m *mcpFieldMapper) IssueToTracker(issue *types.Issue) map[string]interface{} {
	if !m.client.HasTool("issue_to_tracker") {
		return map[string]interface{}{
			"title":       issue.Title,
			"description": issue.Description,
			"priority":    issue.Priority,
			"status":      string(issue.Status),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	raw, err := m.client.CallTool(ctx, "issue_to_tracker", map[string]interface{}{
		"issue": issue,
	})
	if err != nil {
		return map[string]interface{}{"title": issue.Title}
	}

	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return map[string]interface{}{"title": issue.Title}
	}
	return result
}

func (m *mcpFieldMapper) callMapper(tool string, input interface{}, fallback interface{}) interface{} {
	if !m.client.HasTool(tool) {
		return fallback
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	raw, err := m.client.CallTool(ctx, tool, map[string]interface{}{"value": input})
	if err != nil {
		return fallback
	}
	var result interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fallback
	}
	return result
}

func (m *mcpFieldMapper) callMapperRaw(tool string, input interface{}) interface{} {
	return m.callMapper(tool, input, input)
}

// Provenance wraps plugin output with source attribution for AI agents.
type Provenance struct {
	Source string      `json:"source"`
	Trust  string      `json:"trust"`
	Data   interface{} `json:"data"`
}

// WrapProvenance wraps data in a provenance envelope.
func WrapProvenance(pluginName string, data interface{}) *Provenance {
	return &Provenance{
		Source: "plugin:" + pluginName,
		Trust:  "external",
		Data:   data,
	}
}
