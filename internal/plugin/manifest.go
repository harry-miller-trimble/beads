package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

// Manifest describes a plugin's identity, tier, and requested capabilities.
// Baked into OCI artifacts and supplied alongside local plugins as manifest.json.
type Manifest struct {
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Tier         Tier         `json:"tier"`
	Description  string       `json:"description,omitempty"`
	Entrypoint   string       `json:"entrypoint,omitempty"` // binary name (provider) or .wasm file (automation)
	Capabilities []Capability `json:"capabilities"`
	EnvAllowlist []string     `json:"env_allowlist,omitempty"` // env vars the plugin may receive
}

var pluginNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Validate checks that a manifest has all required fields and valid values.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	if !pluginNameRe.MatchString(m.Name) {
		return fmt.Errorf("manifest: name %q must match [a-z][a-z0-9-]*", m.Name)
	}
	if m.Version == "" {
		return fmt.Errorf("manifest %q: version is required", m.Name)
	}
	if !m.Tier.Valid() {
		return fmt.Errorf("manifest %q: invalid tier %q (must be %q or %q)", m.Name, m.Tier, TierProvider, TierAutomation)
	}
	if m.Entrypoint == "" {
		return fmt.Errorf("manifest %q: entrypoint is required", m.Name)
	}
	return nil
}

// ReadManifest reads and validates a manifest from a JSON file.
func ReadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is from plugin source dir
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// WriteManifest writes a manifest to a JSON file.
func WriteManifest(path string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0600)
}
