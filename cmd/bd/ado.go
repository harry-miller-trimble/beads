// Package main provides the bd CLI commands.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/ado"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// ADOConfig holds Azure DevOps connection configuration.
type ADOConfig struct {
	PAT     string // Personal access token
	Org     string // Organization name
	Project string // Project name
	URL     string // Custom base URL (for on-prem)
}

// adoCmd is the root command for Azure DevOps operations.
var adoCmd = &cobra.Command{
	Use:   "ado",
	Short: "Azure DevOps integration commands",
	Long: `Commands for syncing issues between beads and Azure DevOps.

Configuration can be set via 'bd config' or environment variables:
  ado.org / AZURE_DEVOPS_ORG         - Organization name
  ado.project / AZURE_DEVOPS_PROJECT - Project name
  ado.pat / AZURE_DEVOPS_PAT         - Personal access token
  ado.url / AZURE_DEVOPS_URL         - Custom base URL (on-prem)`,
}

// adoSyncCmd synchronizes issues between beads and Azure DevOps.
var adoSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync issues with Azure DevOps",
	Long: `Synchronize issues between beads and Azure DevOps.

By default, performs bidirectional sync:
- Pulls new/updated work items from Azure DevOps to beads
- Pushes local beads issues to Azure DevOps

Use --pull-only or --push-only to limit direction.`,
	RunE: runADOSync,
}

// adoStatusCmd displays Azure DevOps configuration and sync status.
var adoStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Azure DevOps sync status",
	Long:  `Display current Azure DevOps configuration and sync status.`,
	RunE:  runADOStatus,
}

// adoProjectsCmd lists accessible Azure DevOps projects.
var adoProjectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List accessible Azure DevOps projects",
	Long:  `List Azure DevOps projects that the configured token has access to.`,
	RunE:  runADOProjects,
}

var (
	adoSyncDryRun     bool
	adoSyncPullOnly   bool
	adoSyncPushOnly   bool
	adoPreferLocal    bool
	adoPreferADO      bool
	adoPreferNewer    bool
	adoBootstrapMatch bool
	adoNoCreate       bool
	adoReconcile      bool
)

// ADOConflictStrategy defines how to resolve conflicts between local and ADO versions.
type ADOConflictStrategy string

const (
	// ADOConflictPreferNewer uses the most recently updated version (default).
	ADOConflictPreferNewer ADOConflictStrategy = "prefer-newer"
	// ADOConflictPreferLocal always keeps the local beads version.
	ADOConflictPreferLocal ADOConflictStrategy = "prefer-local"
	// ADOConflictPreferADO always uses the Azure DevOps version.
	ADOConflictPreferADO ADOConflictStrategy = "prefer-ado"
)

// getADOConflictStrategy determines the conflict strategy from flag values.
// Returns error if multiple conflicting flags are set.
func getADOConflictStrategy(preferLocal, preferADO, preferNewer bool) (ADOConflictStrategy, error) {
	flagsSet := 0
	if preferLocal {
		flagsSet++
	}
	if preferADO {
		flagsSet++
	}
	if preferNewer {
		flagsSet++
	}
	if flagsSet > 1 {
		return "", fmt.Errorf("cannot use multiple conflict resolution flags")
	}

	if preferLocal {
		return ADOConflictPreferLocal, nil
	}
	if preferADO {
		return ADOConflictPreferADO, nil
	}
	return ADOConflictPreferNewer, nil
}

func init() {
	// Add subcommands to ado
	adoCmd.AddCommand(adoSyncCmd)
	adoCmd.AddCommand(adoStatusCmd)
	adoCmd.AddCommand(adoProjectsCmd)

	// Add flags to sync command
	adoSyncCmd.Flags().BoolVar(&adoSyncDryRun, "dry-run", false, "Show what would be synced without making changes")
	adoSyncCmd.Flags().BoolVar(&adoSyncPullOnly, "pull-only", false, "Only pull issues from Azure DevOps")
	adoSyncCmd.Flags().BoolVar(&adoSyncPushOnly, "push-only", false, "Only push issues to Azure DevOps")

	// Conflict resolution flags (mutually exclusive)
	adoSyncCmd.Flags().BoolVar(&adoPreferLocal, "prefer-local", false, "On conflict, keep local beads version")
	adoSyncCmd.Flags().BoolVar(&adoPreferADO, "prefer-ado", false, "On conflict, use Azure DevOps version")
	adoSyncCmd.Flags().BoolVar(&adoPreferNewer, "prefer-newer", false, "On conflict, use most recent version (default)")

	// Additional sync options
	adoSyncCmd.Flags().BoolVar(&adoBootstrapMatch, "bootstrap-match", false, "Enable heuristic matching for first sync")
	adoSyncCmd.Flags().BoolVar(&adoNoCreate, "no-create", false, "Pull-only mode: never create issues in ADO")
	adoSyncCmd.Flags().BoolVar(&adoReconcile, "reconcile", false, "Force reconciliation scan for deleted items")

	// Register ado command with root
	rootCmd.AddCommand(adoCmd)
}

// getADOConfig returns Azure DevOps configuration from bd config or environment.
func getADOConfig() ADOConfig {
	ctx := context.Background()
	cfg := ADOConfig{}

	cfg.PAT = getADOConfigValue(ctx, "ado.pat")
	cfg.Org = getADOConfigValue(ctx, "ado.org")
	cfg.Project = getADOConfigValue(ctx, "ado.project")
	cfg.URL = getADOConfigValue(ctx, "ado.url")

	return cfg
}

// getADOConfigValue reads an Azure DevOps configuration value from store or environment.
func getADOConfigValue(ctx context.Context, key string) string {
	// Try to read from store (works in direct mode)
	if store != nil {
		value, _ := store.GetConfig(ctx, key)
		if value != "" {
			return value
		}
	} else if dbPath != "" {
		tempStore, err := dolt.New(ctx, &dolt.Config{Path: dbPath})
		if err == nil {
			defer func() { _ = tempStore.Close() }()
			value, _ := tempStore.GetConfig(ctx, key)
			if value != "" {
				return value
			}
		}
	}

	// Fall back to environment variable
	envKey := adoConfigToEnvVar(key)
	if envKey != "" {
		if value := os.Getenv(envKey); value != "" {
			return value
		}
	}

	return ""
}

// adoConfigToEnvVar maps Azure DevOps config keys to their environment variable names.
func adoConfigToEnvVar(key string) string {
	switch key {
	case "ado.pat":
		return "AZURE_DEVOPS_PAT"
	case "ado.org":
		return "AZURE_DEVOPS_ORG"
	case "ado.project":
		return "AZURE_DEVOPS_PROJECT"
	case "ado.url":
		return "AZURE_DEVOPS_URL"
	default:
		return ""
	}
}

// validateADOConfig checks that required configuration is present.
func validateADOConfig(cfg ADOConfig) error {
	if cfg.PAT == "" {
		return fmt.Errorf("ado.pat is not configured. Set via 'bd config ado.pat <token>' or AZURE_DEVOPS_PAT environment variable")
	}
	if cfg.Org == "" && cfg.URL == "" {
		return fmt.Errorf("ado.org is not configured. Set via 'bd config ado.org <org>' or AZURE_DEVOPS_ORG environment variable")
	}
	if cfg.Project == "" {
		return fmt.Errorf("ado.project is not configured. Set via 'bd config ado.project <project>' or AZURE_DEVOPS_PROJECT environment variable")
	}
	return nil
}

// maskADOToken masks a token for safe display.
// Shows only the first 4 characters to aid identification without
// revealing enough to reduce brute-force entropy.
func maskADOToken(token string) string {
	if token == "" {
		return "(not set)"
	}
	if len(token) <= 4 {
		return "****"
	}
	return token[:4] + "****"
}

// getADOClient creates an Azure DevOps client from the current configuration.
func getADOClient(cfg ADOConfig) *ado.Client {
	client := ado.NewClient(ado.NewSecretString(cfg.PAT), cfg.Org, cfg.Project)
	if cfg.URL != "" {
		client = client.WithBaseURL(cfg.URL)
	}
	return client
}

// runADOStatus implements the ado status command.
func runADOStatus(cmd *cobra.Command, _ []string) error {
	cfg := getADOConfig()

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out, "Azure DevOps Configuration")
	_, _ = fmt.Fprintln(out, "==========================")
	_, _ = fmt.Fprintf(out, "Organization: %s\n", cfg.Org)
	_, _ = fmt.Fprintf(out, "Project:      %s\n", cfg.Project)
	_, _ = fmt.Fprintf(out, "PAT:          %s\n", maskADOToken(cfg.PAT))
	if cfg.URL != "" {
		_, _ = fmt.Fprintf(out, "Base URL:     %s\n", cfg.URL)
	}

	// Validate configuration
	if err := validateADOConfig(cfg); err != nil {
		_, _ = fmt.Fprintf(out, "\nStatus: ❌ Not configured\n")
		_, _ = fmt.Fprintf(out, "Error: %v\n", err)
		return nil
	}

	_, _ = fmt.Fprintf(out, "\nStatus: ✓ Configured\n")
	return nil
}

// runADOProjects implements the ado projects command.
func runADOProjects(cmd *cobra.Command, _ []string) error {
	cfg := getADOConfig()
	if cfg.PAT == "" {
		return fmt.Errorf("ado.pat is not configured. Set via 'bd config ado.pat <token>' or AZURE_DEVOPS_PAT environment variable")
	}
	if cfg.Org == "" && cfg.URL == "" {
		return fmt.Errorf("ado.org is not configured. Set via 'bd config ado.org <org>' or AZURE_DEVOPS_ORG environment variable")
	}

	out := cmd.OutOrStdout()
	client := getADOClient(cfg)
	ctx := context.Background()

	projects, err := client.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("failed to list projects: %w", err)
	}

	if jsonOutput {
		outputJSON(projects)
		return nil
	}

	_, _ = fmt.Fprintln(out, "Azure DevOps Projects")
	_, _ = fmt.Fprintln(out, "=====================")
	for _, p := range projects {
		_, _ = fmt.Fprintf(out, "  %s\n", p.Name)
		if p.Description != "" {
			_, _ = fmt.Fprintf(out, "    %s\n", p.Description)
		}
	}

	if len(projects) == 0 {
		_, _ = fmt.Fprintln(out, "No projects found")
	}

	return nil
}

// runADOSync implements the ado sync command.
// Uses the tracker.Engine for all sync operations.
func runADOSync(cmd *cobra.Command, _ []string) error {
	cfg := getADOConfig()
	if err := validateADOConfig(cfg); err != nil {
		return err
	}

	if !adoSyncDryRun {
		CheckReadonly("ado sync")
	}

	if adoSyncPullOnly && adoSyncPushOnly {
		return fmt.Errorf("cannot use both --pull-only and --push-only")
	}

	// Validate conflict flags
	conflictStrategy, err := getADOConflictStrategy(adoPreferLocal, adoPreferADO, adoPreferNewer)
	if err != nil {
		return fmt.Errorf("%w (--prefer-local, --prefer-ado, --prefer-newer)", err)
	}

	if err := ensureStoreActive(); err != nil {
		return fmt.Errorf("database not available: %w", err)
	}

	out := cmd.OutOrStdout()
	ctx := context.Background()

	// Create and initialize the ADO tracker
	at := &ado.Tracker{}
	if err := at.Init(ctx, store); err != nil {
		return fmt.Errorf("initializing Azure DevOps tracker: %w", err)
	}

	// Create the sync engine
	engine := tracker.NewEngine(at, store, actor)
	engine.OnMessage = func(msg string) { _, _ = fmt.Fprintln(out, "  "+msg) }
	engine.OnWarning = func(msg string) { _, _ = fmt.Fprintf(os.Stderr, "Warning: %s\n", msg) }

	// Set up ADO-specific pull hooks
	engine.PullHooks = buildADOPullHooks(ctx)

	// Build sync options from CLI flags
	pull := !adoSyncPushOnly
	push := !adoSyncPullOnly

	opts := tracker.SyncOptions{
		Pull:   pull,
		Push:   push,
		DryRun: adoSyncDryRun,
	}

	// Map conflict resolution
	switch conflictStrategy {
	case ADOConflictPreferLocal:
		opts.ConflictResolution = tracker.ConflictLocal
	case ADOConflictPreferADO:
		opts.ConflictResolution = tracker.ConflictExternal
	default:
		opts.ConflictResolution = tracker.ConflictTimestamp
	}

	if adoSyncDryRun {
		_, _ = fmt.Fprintln(out, "Dry run mode - no changes will be made")
		_, _ = fmt.Fprintln(out)
	}

	// Run sync
	result, err := engine.Sync(ctx, opts)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	// Output results
	if !adoSyncDryRun {
		if result.Stats.Pulled > 0 {
			_, _ = fmt.Fprintf(out, "✓ Pulled %d issues (%d created, %d updated)\n",
				result.Stats.Pulled, result.Stats.Created, result.Stats.Updated)
		}
		if result.Stats.Pushed > 0 {
			_, _ = fmt.Fprintf(out, "✓ Pushed %d issues\n", result.Stats.Pushed)
		}
		if result.Stats.Conflicts > 0 {
			_, _ = fmt.Fprintf(out, "→ Resolved %d conflicts\n", result.Stats.Conflicts)
		}
	}

	if adoSyncDryRun {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "Run without --dry-run to apply changes")
	}

	return nil
}

// buildADOPullHooks creates PullHooks for ADO-specific pull behavior.
func buildADOPullHooks(ctx context.Context) *tracker.PullHooks {
	prefix := "bd"
	// YAML config takes precedence — in shared-server mode the DB
	// may belong to a different project (GH#2469).
	if p := config.GetString("issue-prefix"); p != "" {
		prefix = p
	} else if store != nil {
		if p, err := store.GetConfig(ctx, "issue_prefix"); err == nil && p != "" {
			prefix = p
		}
	}

	return &tracker.PullHooks{
		GenerateID: func(_ context.Context, issue *types.Issue) error {
			if issue.ID == "" {
				issue.ID = generateIssueID(prefix)
			}
			return nil
		},
	}
}
