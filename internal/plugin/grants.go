package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"
)

// GrantStore manages per-plugin, per-capability user consent.
type GrantStore struct {
	path   string
	grants map[string][]Grant // keyed by plugin name
}

// grantsJSON is the on-disk format.
type grantsJSON struct {
	Version int     `json:"version"`
	Grants  []Grant `json:"grants"`
}

const grantsVersion = 1

// OpenGrantStore opens or creates a grant store at the given path.
func OpenGrantStore(path string) (*GrantStore, error) {
	gs := &GrantStore{
		path:   path,
		grants: make(map[string][]Grant),
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is from trusted config, not user input
	if errors.Is(err, os.ErrNotExist) {
		return gs, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read grants: %w", err)
	}
	if len(data) == 0 {
		return gs, nil
	}

	var doc grantsJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse grants %s: %w", path, err)
	}
	if doc.Version != grantsVersion {
		return nil, fmt.Errorf("unsupported grants version %d (expected %d)", doc.Version, grantsVersion)
	}
	for _, g := range doc.Grants {
		gs.grants[g.Plugin] = append(gs.grants[g.Plugin], g)
	}
	return gs, nil
}

// HasGrant checks whether a plugin has been granted a specific capability.
func (gs *GrantStore) HasGrant(plugin string, cap Capability) bool {
	for _, g := range gs.grants[plugin] {
		if g.Capability == cap {
			return true
		}
	}
	return false
}

// GrantsFor returns all grants for a given plugin.
func (gs *GrantStore) GrantsFor(plugin string) []Grant {
	result := make([]Grant, len(gs.grants[plugin]))
	copy(result, gs.grants[plugin])
	return result
}

// AllGrants returns all grants sorted by plugin then capability.
func (gs *GrantStore) AllGrants() []Grant {
	var all []Grant
	for _, grants := range gs.grants {
		all = append(all, grants...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Plugin != all[j].Plugin {
			return all[i].Plugin < all[j].Plugin
		}
		return all[i].Capability < all[j].Capability
	})
	return all
}

// AddGrant records consent for a plugin to use a capability.
// Returns false if the grant already exists.
func (gs *GrantStore) AddGrant(plugin string, cap Capability, grantedBy string) bool {
	if gs.HasGrant(plugin, cap) {
		return false
	}
	gs.grants[plugin] = append(gs.grants[plugin], Grant{
		Plugin:     plugin,
		Capability: cap,
		GrantedAt:  time.Now().UTC(),
		GrantedBy:  grantedBy,
	})
	return true
}

// RevokeGrant removes a specific capability grant for a plugin.
// Returns true if the grant existed.
func (gs *GrantStore) RevokeGrant(plugin string, cap Capability) bool {
	grants := gs.grants[plugin]
	for i, g := range grants {
		if g.Capability == cap {
			gs.grants[plugin] = append(grants[:i], grants[i+1:]...)
			return true
		}
	}
	return false
}

// RevokeAll removes all grants for a plugin. Returns the count revoked.
func (gs *GrantStore) RevokeAll(plugin string) int {
	count := len(gs.grants[plugin])
	delete(gs.grants, plugin)
	return count
}

// Save writes the grant store to disk atomically.
func (gs *GrantStore) Save() error {
	doc := grantsJSON{
		Version: grantsVersion,
		Grants:  gs.AllGrants(),
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal grants: %w", err)
	}
	data = append(data, '\n')

	tmp := gs.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write grants: %w", err)
	}
	if err := os.Rename(tmp, gs.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename grants: %w", err)
	}
	return nil
}
