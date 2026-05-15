package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL       = "https://api.notion.com/v1"
	defaultNotionVersion = "2026-03-11"
	defaultTimeout       = 30 * time.Second
	maxResponseBytes     = 20 * 1024 * 1024
	maxQueryPages        = 50
	maxPageSize          = 100
)

// NotionClient is a minimal Notion API client.
type NotionClient struct {
	Token         string
	BaseURL       string
	NotionVersion string
	HTTPClient    *http.Client
}

func NewNotionClient(token string) *NotionClient {
	return &NotionClient{
		Token:         token,
		BaseURL:       defaultBaseURL,
		NotionVersion: defaultNotionVersion,
		HTTPClient:    &http.Client{Timeout: defaultTimeout},
	}
}

func (c *NotionClient) GetCurrentUser(ctx context.Context) (*User, error) {
	body, err := c.doRequest(ctx, http.MethodGet, "/users/me", nil)
	if err != nil {
		return nil, err
	}
	var user User
	if err := json.Unmarshal(body, &user); err != nil {
		return nil, fmt.Errorf("parse user: %w", err)
	}
	return &user, nil
}

func (c *NotionClient) QueryDataSource(ctx context.Context, dataSourceID string) ([]Page, error) {
	var pages []Page
	var cursor string
	for pageNum := 0; pageNum < maxQueryPages; pageNum++ {
		request := map[string]interface{}{
			"page_size":   maxPageSize,
			"result_type": "page",
			"in_trash":    false,
		}
		if cursor != "" {
			request["start_cursor"] = cursor
		}

		body, err := c.doRequest(ctx, http.MethodPost, "/data_sources/"+url.PathEscape(dataSourceID)+"/query", request)
		if err != nil {
			return nil, err
		}
		var resp QueryDataSourceResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parse query response: %w", err)
		}
		pages = append(pages, resp.Results...)
		if !resp.HasMore || resp.NextCursor == "" {
			return pages, nil
		}
		cursor = resp.NextCursor
	}
	return nil, fmt.Errorf("query pagination exceeded %d pages", maxQueryPages)
}

func (c *NotionClient) CreatePage(ctx context.Context, dataSourceID string, properties map[string]interface{}) (*Page, error) {
	request := map[string]interface{}{
		"parent": map[string]interface{}{
			"type":           "data_source_id",
			"data_source_id": dataSourceID,
		},
		"properties": properties,
	}
	body, err := c.doRequest(ctx, http.MethodPost, "/pages", request)
	if err != nil {
		return nil, err
	}
	var page Page
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("parse page: %w", err)
	}
	return &page, nil
}

func (c *NotionClient) UpdatePage(ctx context.Context, pageID string, properties map[string]interface{}) (*Page, error) {
	request := map[string]interface{}{"properties": properties}
	body, err := c.doRequest(ctx, http.MethodPatch, "/pages/"+url.PathEscape(pageID), request)
	if err != nil {
		return nil, err
	}
	var page Page
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("parse page: %w", err)
	}
	return &page, nil
}

func (c *NotionClient) doRequest(ctx context.Context, method, path string, requestBody interface{}) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("notion client is nil")
	}
	if strings.TrimSpace(c.Token) == "" {
		return nil, fmt.Errorf("Notion token not configured")
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	var bodyReader io.Reader
	if requestBody != nil {
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}

	requestURL := path
	if !strings.HasPrefix(requestURL, "http://") && !strings.HasPrefix(requestURL, "https://") {
		requestURL = strings.TrimSuffix(c.BaseURL, "/") + path
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Notion-Version", c.NotionVersion)
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return body, nil
	}

	var apiErr struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Message != "" {
		return nil, fmt.Errorf("Notion API error %s (%d): %s", apiErr.Code, resp.StatusCode, apiErr.Message)
	}
	return nil, fmt.Errorf("Notion API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
