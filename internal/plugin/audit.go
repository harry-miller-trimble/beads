package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// AuditLog manages the append-only plugin audit log.
type AuditLog struct {
	path string
}

// OpenAuditLog opens or creates an audit log at the given path.
func OpenAuditLog(path string) *AuditLog {
	return &AuditLog{path: path}
}

// Log appends an event to the audit log.
func (al *AuditLog) Log(event *AuditEvent) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}

	f, err := os.OpenFile(al.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Single write for append atomicity under concurrent O_APPEND.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(event); err != nil {
		return fmt.Errorf("marshal audit event: %w", err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}

// LogInstall records a plugin installation event.
func (al *AuditLog) LogInstall(name, version, digest, sourceURI string) error {
	return al.Log(&AuditEvent{
		Kind:    AuditInstall,
		Plugin:  name,
		Version: version,
		Details: map[string]any{
			"digest":     digest,
			"source_uri": sourceURI,
		},
	})
}

// LogRemove records a plugin removal event.
func (al *AuditLog) LogRemove(name, version string) error {
	return al.Log(&AuditEvent{
		Kind:    AuditRemove,
		Plugin:  name,
		Version: version,
	})
}

// LogGrant records a capability grant event.
func (al *AuditLog) LogGrant(name string, cap Capability, grantedBy string) error {
	return al.Log(&AuditEvent{
		Kind:   AuditGrant,
		Plugin: name,
		Details: map[string]any{
			"capability": string(cap),
			"granted_by": grantedBy,
		},
	})
}

// LogRevoke records a capability revoke event.
func (al *AuditLog) LogRevoke(name string, cap Capability) error {
	return al.Log(&AuditEvent{
		Kind:   AuditRevoke,
		Plugin: name,
		Details: map[string]any{
			"capability": string(cap),
		},
	})
}

// Read returns all audit events from the log. Returns an empty slice if the
// file does not exist.
func (al *AuditLog) Read() ([]AuditEvent, error) {
	data, err := os.ReadFile(al.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}

	var events []AuditEvent
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var e AuditEvent
		if err := dec.Decode(&e); err != nil {
			return events, fmt.Errorf("parse audit event: %w", err)
		}
		events = append(events, e)
	}
	return events, nil
}

// ReadForPlugin returns audit events for a specific plugin.
func (al *AuditLog) ReadForPlugin(name string) ([]AuditEvent, error) {
	all, err := al.Read()
	if err != nil {
		return nil, err
	}
	var filtered []AuditEvent
	for _, e := range all {
		if e.Plugin == name {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}
