package main

import "strings"

// Field mapping between Notion and beads value spaces.
// These are self-contained — no dependency on beads types.

var priorityToBeadsMap = map[string]int{
	"critical": 0,
	"high":     1,
	"medium":   2,
	"low":      3,
	"backlog":  4,
}

var priorityToNotionMap = map[int]string{
	0: "Critical",
	1: "High",
	2: "Medium",
	3: "Low",
	4: "Backlog",
}

var statusToBeadsMap = map[string]string{
	"open":        "open",
	"in_progress": "in_progress",
	"blocked":     "blocked",
	"deferred":    "deferred",
	"closed":      "closed",
}

var statusToNotionMap = map[string]string{
	"open":        "Open",
	"in_progress": "In Progress",
	"blocked":     "Blocked",
	"deferred":    "Deferred",
	"closed":      "Closed",
}

var typeToBeadsMap = map[string]string{
	"bug":     "bug",
	"feature": "feature",
	"task":    "task",
	"epic":    "epic",
	"chore":   "chore",
}

var typeToNotionMap = map[string]string{
	"bug":     "Bug",
	"feature": "Feature",
	"task":    "Task",
	"epic":    "Epic",
	"chore":   "Chore",
}

func priorityToBeads(raw string) int {
	v, ok := priorityToBeadsMap[normalizeValue(raw)]
	if !ok {
		return 2
	}
	return v
}

func priorityToNotion(priority int) string {
	v, ok := priorityToNotionMap[priority]
	if !ok {
		return "Medium"
	}
	return v
}

func statusToBeads(raw string) string {
	v, ok := statusToBeadsMap[normalizeValue(raw)]
	if !ok {
		return "open"
	}
	return v
}

func statusToNotion(status string) string {
	v, ok := statusToNotionMap[normalizeValue(status)]
	if !ok {
		return "Open"
	}
	return v
}

func typeToBeads(raw string) string {
	v, ok := typeToBeadsMap[normalizeValue(raw)]
	if !ok {
		return "task"
	}
	return v
}

func typeToNotion(issueType string) string {
	trimmed := strings.TrimSpace(issueType)
	if trimmed == "" {
		return "Task"
	}
	v, ok := typeToNotionMap[normalizeValue(issueType)]
	if !ok {
		return "Task"
	}
	return v
}

func normalizeValue(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	return value
}
