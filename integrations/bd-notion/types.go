package main

import (
	"strings"
	"time"
)

// --- Notion API types (self-contained, no beads imports) ---

const (
	PropertyTitle       = "Name"
	PropertyBeadsID     = "Beads ID"
	PropertyStatus      = "Status"
	PropertyPriority    = "Priority"
	PropertyType        = "Type"
	PropertyDescription = "Description"
	PropertyAssignee    = "Assignee"
	PropertyLabels      = "Labels"
)

type User struct {
	Object string  `json:"object,omitempty"`
	ID     string  `json:"id,omitempty"`
	Name   string  `json:"name,omitempty"`
	Type   string  `json:"type,omitempty"`
	Person *Person `json:"person,omitempty"`
}

type Person struct {
	Email string `json:"email,omitempty"`
}

type DataSource struct {
	Object     string                        `json:"object,omitempty"`
	ID         string                        `json:"id,omitempty"`
	URL        string                        `json:"url,omitempty"`
	Title      []RichText                    `json:"title,omitempty"`
	Properties map[string]DataSourceProperty `json:"properties,omitempty"`
}

type DataSourceProperty struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type,omitempty"`
}

type QueryDataSourceResponse struct {
	Results    []Page `json:"results,omitempty"`
	HasMore    bool   `json:"has_more,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type Page struct {
	Object         string                  `json:"object,omitempty"`
	ID             string                  `json:"id,omitempty"`
	URL            string                  `json:"url,omitempty"`
	InTrash        bool                    `json:"in_trash,omitempty"`
	Archived       bool                    `json:"archived,omitempty"`
	CreatedTime    time.Time               `json:"created_time,omitempty"`
	LastEditedTime time.Time               `json:"last_edited_time,omitempty"`
	Properties     map[string]PageProperty `json:"properties,omitempty"`
}

type PageProperty struct {
	ID          string         `json:"id,omitempty"`
	Type        string         `json:"type,omitempty"`
	Title       []RichText     `json:"title,omitempty"`
	RichText    []RichText     `json:"rich_text,omitempty"`
	Select      *SelectOption  `json:"select,omitempty"`
	MultiSelect []SelectOption `json:"multi_select,omitempty"`
}

type RichText struct {
	Type      string      `json:"type,omitempty"`
	PlainText string      `json:"plain_text,omitempty"`
	Text      *TextObject `json:"text,omitempty"`
}

type TextObject struct {
	Content string `json:"content,omitempty"`
}

type SelectOption struct {
	Name string `json:"name,omitempty"`
}

// --- Helpers ---

func DataSourceTitle(items []RichText) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		switch {
		case item.PlainText != "":
			parts = append(parts, item.PlainText)
		case item.Text != nil && item.Text.Content != "":
			parts = append(parts, item.Text.Content)
		}
	}
	return strings.Join(parts, "")
}

func richTextRequest(content string) []map[string]interface{} {
	content = strings.TrimSpace(content)
	if content == "" {
		return []map[string]interface{}{}
	}
	return []map[string]interface{}{
		{
			"type": "text",
			"text": map[string]interface{}{
				"content": content,
			},
		},
	}
}

func pagePropertySelect(prop PageProperty) string {
	if prop.Select == nil {
		return ""
	}
	return prop.Select.Name
}

func pagePropertyMultiSelect(prop PageProperty) []string {
	labels := make([]string, 0, len(prop.MultiSelect))
	for _, item := range prop.MultiSelect {
		name := strings.TrimSpace(item.Name)
		if name != "" {
			labels = append(labels, name)
		}
	}
	return labels
}
