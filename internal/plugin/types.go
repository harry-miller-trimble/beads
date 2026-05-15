// Package plugin implements the trust layer for the beads plugin system.
//
// The trust layer governs both Provider (MCP-stdio) and Automation (WASM)
// plugins via a unified model: manifest + lockfile + capability grants + audit.
// No plugin executes without an explicit user grant and a verified content hash.
package plugin

import (
	"fmt"
	"strings"
	"time"
)

// Tier distinguishes the two plugin paradigms.
type Tier string

const (
	TierProvider   Tier = "provider"
	TierAutomation Tier = "automation"
)

func (t Tier) Valid() bool {
	return t == TierProvider || t == TierAutomation
}

// Capability represents a single permission a plugin may request.
// Format: "domain.action" or "domain:scope" (e.g. "tracker.read",
// "network:jira.example.com", "env:JIRA_TOKEN", "fs.read:/tmp").
type Capability string

// Well-known capability prefixes.
const (
	CapTrackerRead  Capability = "tracker.read"
	CapTrackerWrite Capability = "tracker.write"
	CapHookExecute  Capability = "hooks.execute"
)

// Domain returns the part before the first dot or colon.
func (c Capability) Domain() string {
	s := string(c)
	for i, ch := range s {
		if ch == '.' || ch == ':' {
			return s[:i]
		}
	}
	return s
}

func (c Capability) String() string { return string(c) }

// SourceKind identifies how a plugin was installed.
type SourceKind string

const (
	SourceLocal SourceKind = "local"
	SourceOCI   SourceKind = "oci"
	SourceGH    SourceKind = "gh"
)

// LockEntry is a single plugin record in the lockfile.
type LockEntry struct {
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Tier        Tier       `json:"tier"`
	Digest      string     `json:"digest"` // "sha256:<hex>"
	Source      SourceKind `json:"source"`
	SourceURI   string     `json:"source_uri"`
	InstalledAt time.Time  `json:"installed_at"`
}

// Validate checks that a LockEntry has all required fields.
func (e *LockEntry) Validate() error {
	if e.Name == "" {
		return fmt.Errorf("plugin name is required")
	}
	if e.Version == "" {
		return fmt.Errorf("plugin %q: version is required", e.Name)
	}
	if !e.Tier.Valid() {
		return fmt.Errorf("plugin %q: invalid tier %q", e.Name, e.Tier)
	}
	if e.Digest == "" {
		return fmt.Errorf("plugin %q: digest is required (no digest = no execution)", e.Name)
	}
	if !strings.HasPrefix(e.Digest, "sha256:") {
		return fmt.Errorf("plugin %q: digest must start with \"sha256:\"", e.Name)
	}
	return nil
}

// Grant records user consent for a plugin to use a specific capability.
type Grant struct {
	Plugin     string     `json:"plugin"`
	Capability Capability `json:"capability"`
	GrantedAt  time.Time  `json:"granted_at"`
	GrantedBy  string     `json:"granted_by,omitempty"` // "user" or "auto"
}

// AuditEvent records a plugin lifecycle event in the append-only audit log.
type AuditEvent struct {
	Timestamp time.Time      `json:"timestamp"`
	Kind      AuditKind      `json:"kind"`
	Plugin    string         `json:"plugin,omitempty"`
	Version   string         `json:"version,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// AuditKind identifies the type of audit event.
type AuditKind string

const (
	AuditInstall AuditKind = "install"
	AuditUpdate  AuditKind = "update"
	AuditRemove  AuditKind = "remove"
	AuditGrant   AuditKind = "grant"
	AuditRevoke  AuditKind = "revoke"
	AuditExecute AuditKind = "execute"
)
