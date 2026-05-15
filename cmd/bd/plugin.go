package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/plugin"
)

var pluginCmd = &cobra.Command{
	Use:     "plugin",
	GroupID: "advanced",
	Short:   "Manage plugins",
	Long: `Manage bd plugins: install, remove, inspect, and verify.

Plugins extend bd with external tracker integrations (Providers) and
lifecycle hooks/formatters (Automations). All plugins are governed by
a trust layer: content-addressed lockfile, explicit capability grants,
and an append-only audit log.

Use 'bd plugin install' to add a plugin, 'bd plugin list' to see what's
installed, and 'bd plugin doctor' to verify integrity.`,
}

var pluginInstallCmd = &cobra.Command{
	Use:   "install <source>",
	Short: "Install a plugin",
	Long: `Install a plugin from a local folder, OCI registry, or GitHub.

Sources:
  ./path/to/plugin     Local folder containing manifest.json
  oci://ghcr.io/...    OCI registry artifact (not yet implemented)
  gh:owner/repo        GitHub repository (not yet implemented)

The plugin's manifest.json must declare name, version, tier, entrypoint,
and capabilities. A SHA-256 digest is computed and pinned in the lockfile.
No digest = no execution.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := pluginManager()
		if err != nil {
			return err
		}

		source := args[0]
		kind, uri := plugin.ParseSource(source)

		var entry *plugin.LockEntry
		switch kind {
		case plugin.SourceLocal:
			entry, err = mgr.InstallLocal(uri)
		case plugin.SourceOCI:
			entry, err = mgr.InstallOCI(uri)
		case plugin.SourceGH:
			entry, err = mgr.InstallGH(uri)
		}
		if err != nil {
			return err
		}

		if jsonOutput {
			outputJSON(entry)
			return nil
		}

		fmt.Printf("✓ Installed plugin %s (v%s, %s)\n", entry.Name, entry.Version, entry.Tier)
		fmt.Printf("  Digest: %s\n", entry.Digest)
		fmt.Printf("  Source: %s\n", entry.SourceURI)
		fmt.Println()
		fmt.Println("Grant capabilities with: bd plugin trust", entry.Name)
		return nil
	},
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed plugins",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := pluginManager()
		if err != nil {
			return err
		}

		entries := mgr.Lockfile.List()

		if jsonOutput {
			outputJSON(entries)
			return nil
		}

		if len(entries) == 0 {
			fmt.Println("No plugins installed.")
			fmt.Println("Install one with: bd plugin install <source>")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tVERSION\tTIER\tSOURCE\tDIGEST")
		for _, e := range entries {
			shortDigest := e.Digest
			if len(shortDigest) > 19 { // "sha256:" + first 12 hex
				shortDigest = shortDigest[:19] + "…"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", e.Name, e.Version, e.Tier, e.Source, shortDigest)
		}
		return w.Flush()
	},
}

var pluginRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an installed plugin",
	Long: `Remove a plugin by name. This revokes all grants, removes the
lockfile entry, and cleans the cached plugin files.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := pluginManager()
		if err != nil {
			return err
		}

		name := args[0]
		entry := mgr.Lockfile.Get(name)
		if entry == nil {
			return fmt.Errorf("plugin %q is not installed", name)
		}

		if err := mgr.Remove(name); err != nil {
			return err
		}

		if jsonOutput {
			outputJSON(map[string]any{
				"removed": name,
				"version": entry.Version,
			})
			return nil
		}

		fmt.Printf("✓ Removed plugin %s (v%s)\n", name, entry.Version)
		return nil
	},
}

var pluginTrustCmd = &cobra.Command{
	Use:   "trust <name> [capability...]",
	Short: "View or manage plugin capability grants",
	Long: `View or manage capability grants for a plugin.

With no capabilities specified, shows existing grants. With one or more
capabilities, grants them to the plugin.

Use --revoke to revoke specific capabilities, or --revoke-all to revoke
all grants for a plugin.

Examples:
  bd plugin trust bd-jira                           # Show grants
  bd plugin trust bd-jira tracker.read tracker.write # Grant capabilities
  bd plugin trust bd-jira --revoke tracker.write     # Revoke one
  bd plugin trust bd-jira --revoke-all               # Revoke all`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := pluginManager()
		if err != nil {
			return err
		}

		name := args[0]
		if mgr.Lockfile.Get(name) == nil {
			return fmt.Errorf("plugin %q is not installed", name)
		}

		revokeAll, _ := cmd.Flags().GetBool("revoke-all")
		revokeCaps, _ := cmd.Flags().GetStringSlice("revoke")

		// Revoke all grants.
		if revokeAll {
			count := mgr.Grants.RevokeAll(name)
			if count > 0 {
				if err := mgr.Grants.Save(); err != nil {
					return err
				}
				for i := 0; i < count; i++ {
					_ = mgr.Audit.LogRevoke(name, "all")
				}
			}
			if jsonOutput {
				outputJSON(map[string]any{"plugin": name, "revoked_count": count})
				return nil
			}
			fmt.Printf("Revoked %d grant(s) for %s\n", count, name)
			return nil
		}

		// Revoke specific capabilities.
		if len(revokeCaps) > 0 {
			var revoked []string
			for _, c := range revokeCaps {
				cap := plugin.Capability(c)
				if mgr.Grants.RevokeGrant(name, cap) {
					_ = mgr.Audit.LogRevoke(name, cap)
					revoked = append(revoked, c)
				}
			}
			if len(revoked) > 0 {
				if err := mgr.Grants.Save(); err != nil {
					return err
				}
			}
			if jsonOutput {
				outputJSON(map[string]any{"plugin": name, "revoked": revoked})
				return nil
			}
			if len(revoked) > 0 {
				fmt.Printf("Revoked: %s\n", strings.Join(revoked, ", "))
			} else {
				fmt.Println("No matching grants to revoke.")
			}
			return nil
		}

		// Grant new capabilities.
		capabilities := args[1:]
		if len(capabilities) > 0 {
			var granted []string
			for _, c := range capabilities {
				cap := plugin.Capability(c)
				if mgr.Grants.AddGrant(name, cap, "user") {
					_ = mgr.Audit.LogGrant(name, cap, "user")
					granted = append(granted, c)
				}
			}
			if len(granted) > 0 {
				if err := mgr.Grants.Save(); err != nil {
					return err
				}
			}
			if jsonOutput {
				outputJSON(map[string]any{"plugin": name, "granted": granted})
				return nil
			}
			if len(granted) > 0 {
				fmt.Printf("✓ Granted to %s: %s\n", name, strings.Join(granted, ", "))
			} else {
				fmt.Println("All requested capabilities were already granted.")
			}
			return nil
		}

		// Show existing grants.
		grants := mgr.Grants.GrantsFor(name)
		if jsonOutput {
			outputJSON(grants)
			return nil
		}
		if len(grants) == 0 {
			fmt.Printf("No capabilities granted to %s.\n", name)
			return nil
		}
		fmt.Printf("Grants for %s:\n", name)
		for _, g := range grants {
			fmt.Printf("  %s  (granted %s by %s)\n", g.Capability, g.GrantedAt.Format("2006-01-02"), g.GrantedBy)
		}
		return nil
	},
}

var pluginAuditCmd = &cobra.Command{
	Use:   "audit [name]",
	Short: "View plugin audit log",
	Long: `View the append-only plugin audit log. Shows install, remove,
grant, revoke, and execute events.

With no arguments, shows all events. With a plugin name, filters to
events for that plugin.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := pluginManager()
		if err != nil {
			return err
		}

		var events []plugin.AuditEvent
		if len(args) > 0 {
			events, err = mgr.Audit.ReadForPlugin(args[0])
		} else {
			events, err = mgr.Audit.Read()
		}
		if err != nil {
			return err
		}

		if jsonOutput {
			outputJSON(events)
			return nil
		}

		if len(events) == 0 {
			fmt.Println("No audit events.")
			return nil
		}

		for _, e := range events {
			ts := e.Timestamp.Format("2006-01-02 15:04:05")
			detail := ""
			if d, err := json.Marshal(e.Details); err == nil && string(d) != "null" {
				detail = " " + string(d)
			}
			fmt.Printf("[%s] %-8s %s %s%s\n", ts, e.Kind, e.Plugin, e.Version, detail)
		}
		return nil
	},
}

var pluginDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Verify plugin integrity",
	Long: `Verify lockfile integrity: check that every installed plugin's
cached files match their pinned SHA-256 digest. Also checks for
plugins with no capability grants (installed but unused).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := pluginManager()
		if err != nil {
			return err
		}

		problems := mgr.Verify()

		// Check for plugins with no grants.
		for _, entry := range mgr.Lockfile.List() {
			grants := mgr.Grants.GrantsFor(entry.Name)
			if len(grants) == 0 {
				problems = append(problems, fmt.Sprintf("%s: no capabilities granted (plugin cannot execute)", entry.Name))
			}
		}

		if jsonOutput {
			result := map[string]any{
				"healthy":       len(problems) == 0,
				"plugin_count":  mgr.Lockfile.Len(),
				"problem_count": len(problems),
				"problems":      problems,
			}
			outputJSON(result)
			return nil
		}

		count := mgr.Lockfile.Len()
		if count == 0 {
			fmt.Println("No plugins installed.")
			return nil
		}

		if len(problems) == 0 {
			fmt.Printf("✓ All %d plugin(s) healthy.\n", count)
			return nil
		}

		fmt.Printf("Found %d problem(s) across %d plugin(s):\n\n", len(problems), count)
		for _, p := range problems {
			fmt.Printf("  ✗ %s\n", p)
		}
		fmt.Println()
		fmt.Println("Run 'bd plugin remove <name>' and reinstall to fix digest mismatches.")
		return fmt.Errorf("%d problem(s) found", len(problems))
	},
}

// pluginManager creates a Manager with default paths.
func pluginManager() (*plugin.Manager, error) {
	paths, err := plugin.DefaultPaths()
	if err != nil {
		return nil, err
	}
	return plugin.NewManager(paths)
}

func registerPluginCmds(root *cobra.Command) {
	pluginTrustCmd.Flags().StringSlice("revoke", nil, "Revoke specific capabilities")
	pluginTrustCmd.Flags().Bool("revoke-all", false, "Revoke all capabilities")

	pluginCmd.AddCommand(pluginInstallCmd)
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginRemoveCmd)
	pluginCmd.AddCommand(pluginTrustCmd)
	pluginCmd.AddCommand(pluginAuditCmd)
	pluginCmd.AddCommand(pluginDoctorCmd)
	root.AddCommand(pluginCmd)
}
