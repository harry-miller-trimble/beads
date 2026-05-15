package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
)

// Lockfile manages the plugin lockfile (lock.json).
// Thread-safe for concurrent reads; writes must be serialized externally.
type Lockfile struct {
	path    string
	mu      sync.RWMutex
	entries map[string]*LockEntry // keyed by plugin name
}

// lockfileJSON is the on-disk format.
type lockfileJSON struct {
	Version int          `json:"version"`
	Plugins []*LockEntry `json:"plugins"`
}

const lockfileVersion = 1

// OpenLockfile opens or creates a lockfile at the given path.
func OpenLockfile(path string) (*Lockfile, error) {
	lf := &Lockfile{
		path:    path,
		entries: make(map[string]*LockEntry),
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is from trusted config, not user input
	if errors.Is(err, os.ErrNotExist) {
		return lf, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lockfile: %w", err)
	}
	if len(data) == 0 {
		return lf, nil
	}

	var doc lockfileJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse lockfile %s: %w", path, err)
	}
	if doc.Version != lockfileVersion {
		return nil, fmt.Errorf("unsupported lockfile version %d (expected %d)", doc.Version, lockfileVersion)
	}
	for _, e := range doc.Plugins {
		if err := e.Validate(); err != nil {
			return nil, fmt.Errorf("lockfile %s: %w", path, err)
		}
		lf.entries[e.Name] = e
	}
	return lf, nil
}

// Get returns a lock entry by plugin name, or nil if not found.
func (lf *Lockfile) Get(name string) *LockEntry {
	lf.mu.RLock()
	defer lf.mu.RUnlock()
	return lf.entries[name]
}

// List returns all lock entries sorted by name.
func (lf *Lockfile) List() []*LockEntry {
	lf.mu.RLock()
	defer lf.mu.RUnlock()
	result := make([]*LockEntry, 0, len(lf.entries))
	for _, e := range lf.entries {
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Put adds or updates a lock entry. The entry is validated before insertion.
func (lf *Lockfile) Put(entry *LockEntry) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	lf.mu.Lock()
	defer lf.mu.Unlock()
	lf.entries[entry.Name] = entry
	return nil
}

// Remove deletes a lock entry by name. Returns true if the entry existed.
func (lf *Lockfile) Remove(name string) bool {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	_, existed := lf.entries[name]
	delete(lf.entries, name)
	return existed
}

// Len returns the number of entries.
func (lf *Lockfile) Len() int {
	lf.mu.RLock()
	defer lf.mu.RUnlock()
	return len(lf.entries)
}

// Save writes the lockfile to disk atomically.
func (lf *Lockfile) Save() error {
	lf.mu.RLock()
	plugins := make([]*LockEntry, 0, len(lf.entries))
	for _, e := range lf.entries {
		plugins = append(plugins, e)
	}
	lf.mu.RUnlock()

	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Name < plugins[j].Name
	})

	doc := lockfileJSON{
		Version: lockfileVersion,
		Plugins: plugins,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lockfile: %w", err)
	}
	data = append(data, '\n')

	// Write to temp file then rename for atomicity.
	tmp := lf.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write lockfile: %w", err)
	}
	if err := os.Rename(tmp, lf.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename lockfile: %w", err)
	}
	return nil
}
