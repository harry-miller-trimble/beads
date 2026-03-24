package main

import (
	"context"
	"fmt"
	"os"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/ui"
)

// ResolveCustomTypes returns the effective custom issue types by checking
// the database first and falling back to config.yaml when the DB returns
// nothing. This is the single source of truth for custom type resolution
// across all commands (create, update, list).
//
// Precedence: DB takes strict priority. YAML is used only when DB returns
// an empty list, never merged/unioned.
func ResolveCustomTypes(ctx context.Context, s storage.DoltStorage) []string {
	var customTypes []string
	if s != nil {
		ct, err := s.GetCustomTypes(ctx)
		if err != nil {
			if !jsonOutput {
				fmt.Fprintf(os.Stderr, "%s Failed to get custom types from DB: %v (falling back to config.yaml)\n",
					ui.RenderWarn("!"), err)
			}
		} else {
			customTypes = ct
		}
	}
	// Fallback to config.yaml when DB returns no custom types.
	if len(customTypes) == 0 {
		customTypes = config.GetCustomTypesFromYAML()
	}
	return customTypes
}

// ResolveCustomStatuses returns the effective custom statuses by checking
// the database first and falling back to config.yaml when the DB returns
// nothing.
//
// Precedence: DB takes strict priority. YAML is used only when DB returns
// an empty list, never merged/unioned.
func ResolveCustomStatuses(ctx context.Context, s storage.DoltStorage) []string {
	var customStatuses []string
	if s != nil {
		cs, err := s.GetCustomStatuses(ctx)
		if err != nil {
			if !jsonOutput {
				fmt.Fprintf(os.Stderr, "%s Failed to get custom statuses from DB: %v (falling back to config.yaml)\n",
					ui.RenderWarn("!"), err)
			}
		} else {
			customStatuses = cs
		}
	}
	// Fallback to config.yaml when DB returns no custom statuses.
	if len(customStatuses) == 0 {
		customStatuses = config.GetCustomStatusesFromYAML()
	}
	return customStatuses
}
