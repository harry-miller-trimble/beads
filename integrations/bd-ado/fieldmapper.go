package main

import (
	"strings"
)

// adoFieldMapper implements FieldMapper for Azure DevOps.
type adoFieldMapper struct {
	stateMap map[string]string // beads status → ADO state (from ado.state_map.* config)
	typeMap  map[string]string // beads type → ADO type (from ado.type_map.* config)
}

// Compile-time interface check.
var _ FieldMapper = (*adoFieldMapper)(nil)

// NewFieldMapper creates a new ADO field mapper with optional custom mappings.
// stateMap overrides default status mapping (beads status → ADO state).
// typeMap overrides default type mapping (beads type → ADO type).
// Pass nil for either to use defaults only.
func NewFieldMapper(stateMap, typeMap map[string]string) FieldMapper {
	sm := make(map[string]string)
	for k, v := range stateMap {
		sm[k] = v
	}
	tm := make(map[string]string)
	for k, v := range typeMap {
		tm[k] = v
	}
	return &adoFieldMapper{stateMap: sm, typeMap: tm}
}

// PriorityToBeads converts an ADO priority (float64 from JSON: 1-4) to beads (0-4).
// ADO 1→0, 2→1, 3→2, 4→3. Unknown values default to 2 (medium).
func (m *adoFieldMapper) PriorityToBeads(trackerPriority interface{}) int {
	p, ok := trackerPriority.(float64)
	if !ok {
		return 2
	}
	switch int(p) {
	case 1:
		return 0
	case 2:
		return 1
	case 3:
		return 2
	case 4:
		return 3
	default:
		return 2
	}
}

// PriorityToTracker converts a beads priority (0-4) to ADO priority (1-4).
// Beads 0→1, 1→2, 2→3, 3→4, 4→4 (lossy: backlog collapses to low).
func (m *adoFieldMapper) PriorityToTracker(beadsPriority int) interface{} {
	switch beadsPriority {
	case 0:
		return 1
	case 1:
		return 2
	case 2:
		return 3
	case 3:
		return 4
	case 4:
		return 4
	default:
		return 3
	}
}

// StatusToBeads converts an ADO state string to a beads Status.
// Checks custom stateMap first (inverted lookup), then falls back to Agile defaults.
func (m *adoFieldMapper) StatusToBeads(trackerState interface{}) string {
	state, ok := trackerState.(string)
	if !ok {
		return StatusOpen
	}

	// Check custom map first (inverted: ADO state → beads status).
	for beadsStatus, adoState := range m.stateMap {
		if strings.EqualFold(state, adoState) {
			return beadsStatus
		}
	}

	// Agile defaults.
	switch state {
	case "New":
		return StatusOpen
	case "Active":
		return StatusInProgress
	case "Resolved":
		return StatusClosed
	case "Closed":
		return StatusClosed
	case "Removed":
		return StatusDeferred
	default:
		return StatusOpen
	}
}

// StatusToTracker converts a beads Status to an ADO state string.
// Checks custom stateMap first, then falls back to Agile defaults.
func (m *adoFieldMapper) StatusToTracker(beadsStatus string) interface{} {
	if name, ok := m.stateMap[beadsStatus]; ok {
		return name
	}
	switch beadsStatus {
	case StatusOpen:
		return "New"
	case StatusInProgress:
		return "Active"
	case StatusBlocked:
		return "Active"
	case StatusDeferred:
		return "Removed"
	case StatusClosed:
		return "Closed"
	default:
		return "New"
	}
}

// TypeToBeads converts an ADO work item type string to a beads IssueType.
// Uses case-insensitive matching. Checks custom typeMap first (inverted), then defaults.
func (m *adoFieldMapper) TypeToBeads(trackerType interface{}) string {
	t, ok := trackerType.(string)
	if !ok {
		return TypeTask
	}

	// Check custom map first (inverted: ADO type → beads type).
	for beadsType, adoType := range m.typeMap {
		if strings.EqualFold(t, adoType) {
			return beadsType
		}
	}

	// Agile defaults (case-insensitive).
	lower := strings.ToLower(t)
	switch lower {
	case "bug":
		return TypeBug
	case "user story":
		return TypeFeature
	case "product backlog item":
		return TypeFeature
	case "task":
		return TypeTask
	case "epic":
		return TypeEpic
	default:
		return TypeTask
	}
}

// SeverityForBug maps a beads priority (0-4) to an ADO Severity string.
// ADO Bug work items require a Severity field with values like "1 - Critical".
// Beads 0→"1 - Critical", 1→"2 - High", 2→"3 - Medium", 3/4→"4 - Low".
// Returns "3 - Medium" for unknown values.
func (m *adoFieldMapper) SeverityForBug(beadsPriority int) string {
	switch beadsPriority {
	case 0:
		return "1 - Critical"
	case 1:
		return "2 - High"
	case 2:
		return "3 - Medium"
	case 3:
		return "4 - Low"
	case 4:
		return "4 - Low"
	default:
		return "3 - Medium"
	}
}

// TypeToTracker converts a beads IssueType to an ADO work item type string.
// Checks custom typeMap first, then falls back to Agile defaults.
func (m *adoFieldMapper) TypeToTracker(beadsType string) interface{} {
	if name, ok := m.typeMap[beadsType]; ok {
		return name
	}
	switch beadsType {
	case TypeBug:
		return "Bug"
	case TypeFeature:
		return "User Story"
	case TypeEpic:
		return "Epic"
	case TypeTask:
		return "Task"
	case TypeChore:
		return "Task"
	default:
		return "Task"
	}
}
