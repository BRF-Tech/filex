package cliclient

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The caller's own public links ("My shares", GET /api/shares) and revoking
// one (DELETE /api/files/share/{id}).

// MyShare is one link the caller created.
type MyShare struct {
	Share struct {
		ID            int64      `json:"id"`
		Token         string     `json:"token"`
		Kind          string     `json:"kind"` // download | drop
		HasPin        bool       `json:"has_pin"`
		ExpiresAt     *time.Time `json:"expires_at,omitempty"`
		MaxDownloads  *int       `json:"max_downloads,omitempty"`
		DownloadCount int        `json:"download_count"`
		CreatedAt     time.Time  `json:"created_at"`
	} `json:"share"`
	NodePath    string `json:"node_path,omitempty"`
	StorageName string `json:"storage_name,omitempty"`
	URL         string `json:"url,omitempty"`
}

// Location is the shared item's `adapter://path`.
func (s MyShare) Location() string {
	if s.StorageName == "" {
		return s.NodePath
	}
	return RemotePath{Adapter: s.StorageName, Rel: strings.Trim(s.NodePath, "/")}.String()
}

// SharePage is one page of the caller's links, newest first.
type SharePage struct {
	Items []MyShare `json:"items"`
	Total int64     `json:"total"`
	Raw   []byte    `json:"-"`
}

// MyShares lists the links the caller created. activeOnly leaves out expired
// and used-up ones; limit 0 is the server's page (50, at most 500).
func (c *Client) MyShares(ctx context.Context, activeOnly bool, limit, offset int) (*SharePage, error) {
	q := pageQuery(limit, offset)
	if activeOnly {
		q.Set("active", "true")
	}
	var page SharePage
	raw, err := c.getJSONInto(ctx, "/api/shares", q, "shares", &page)
	if err != nil {
		return nil, err
	}
	page.Raw = raw
	return &page, nil
}

// Unshare revokes the link with id: its URL stops working at once.
func (c *Client) Unshare(ctx context.Context, id int64) ([]byte, error) {
	if id <= 0 {
		return nil, fmt.Errorf("bad share id %d", id)
	}
	req, err := c.newRequest(ctx, http.MethodDelete, "/api/files/share/"+strconv.FormatInt(id, 10), nil, nil)
	if err != nil {
		return nil, err
	}
	return c.doJSON(req)
}
