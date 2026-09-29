package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Plugin install requests (docs/APP-PLUGINS.md → Install requests): what a
// key-holding client leaves instead of installing a plugin. An administrator
// approves or rejects it in the admin panel; there is no client call for that.

// PluginRequest is one request as the server answers it.
type PluginRequest struct {
	ID           int64     `json:"id"`
	Kind         string    `json:"kind"`
	Op           string    `json:"op"`
	Name         string    `json:"name"`
	Version      string    `json:"version"`
	FromVersion  string    `json:"from_version"`
	SHA256       string    `json:"sha256"`
	Permissions  []string  `json:"permissions"`
	Requester    string    `json:"requester"`
	TokenLabel   string    `json:"token_label"`
	Reason       string    `json:"reason"`
	Status       string    `json:"status"`
	DecisionNote string    `json:"decision_note"`
	ExpiresAt    time.Time `json:"expires_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// PluginRequestInput is a request to leave: the plugin's kind (app |
// storage), the operation (install | upgrade), where it comes from and why.
type PluginRequestInput struct {
	Kind        string `json:"kind"`
	Op          string `json:"op,omitempty"`
	Name        string `json:"name,omitempty"`
	PluginID    int64  `json:"plugin_id,omitempty"`
	GitHubRepo  string `json:"github_repo,omitempty"`
	Ref         string `json:"ref,omitempty"`
	ManifestURL string `json:"manifest_url,omitempty"`
	URL         string `json:"url,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Source      string `json:"source,omitempty"`
	Reason      string `json:"reason"`
}

// PluginRequestAnswer is POST /api/admin/plugin-requests' answer. Created is
// false when a request for the same source was already waiting (that one is
// answered).
type PluginRequestAnswer struct {
	Request PluginRequest `json:"request"`
	Created bool          `json:"created"`
	Message string        `json:"message"`
	Raw     []byte        `json:"-"`
}

// withServerMessage adds the server's own sentence ({"message": …}) to an
// API error: "HTTP 400: reason_required" says less than the sentence beside it.
func withServerMessage(err error) error {
	var ae *APIError
	if !errors.As(err, &ae) {
		return err
	}
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(ae.Body, &body) == nil && body.Message != "" {
		return fmt.Errorf("%w — %s", err, body.Message)
	}
	return err
}

// RequestPlugin leaves a plugin install (or upgrade) request.
func (c *Client) RequestPlugin(ctx context.Context, in PluginRequestInput) (*PluginRequestAnswer, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/admin/plugin-requests", nil, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, withServerMessage(err)
	}
	var out PluginRequestAnswer
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse plugin request response: %w", err)
	}
	out.Raw = raw
	return &out, nil
}

// PluginRequests lists the requests in one state: pending (""), approved,
// rejected, expired, superseded or all.
func (c *Client) PluginRequests(ctx context.Context, status string) ([]PluginRequest, []byte, error) {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/api/admin/plugin-requests", q, nil)
	if err != nil {
		return nil, nil, err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, nil, withServerMessage(err)
	}
	var out struct {
		Requests []PluginRequest `json:"requests"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("parse plugin requests: %w", err)
	}
	return out.Requests, raw, nil
}
