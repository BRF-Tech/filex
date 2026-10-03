package cliclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The trash and the version history: what `rm` and an overwrite leave behind,
// and the way back (docs/TRASH-VERSIONING.md).

// ───────────────────────── trash ─────────────────────────

// TrashEntry is one item in the trash (GET /api/files/manager/trash). ID is
// the id a restore names.
type TrashEntry struct {
	ID            int64     `json:"id"`
	StorageID     int64     `json:"storage_id"`
	StorageName   string    `json:"storage_name,omitempty"`
	Path          string    `json:"path"`
	Name          string    `json:"name"`
	Size          int64     `json:"size"`
	DeletedAt     time.Time `json:"deleted_at"`
	DeletedByName string    `json:"deleted_by_name,omitempty"`
	DeletedBySelf bool      `json:"deleted_by_self,omitempty"`
	Draft         bool      `json:"draft,omitempty"`
	// E2eRoot is the end-to-end encrypted folder the item was deleted from;
	// its name may then be ciphertext.
	E2eRoot string `json:"e2e_root,omitempty"`
}

// Location is the entry's original place as `adapter://path`.
func (e TrashEntry) Location() string {
	if e.StorageName == "" {
		return e.Path
	}
	return RemotePath{Adapter: e.StorageName, Rel: strings.Trim(e.Path, "/")}.String()
}

// TrashPage is one page of the caller's trash.
type TrashPage struct {
	Entries []TrashEntry `json:"entries"`
	Total   int          `json:"total"`
	Limit   int          `json:"limit"`
	Offset  int          `json:"offset"`
	Raw     []byte       `json:"-"`
}

// Trash lists what the caller may see in the trash, newest first. storageID 0
// is every storage; limit 0 is the server's default page (50, at most 500).
func (c *Client) Trash(ctx context.Context, storageID int64, limit, offset int) (*TrashPage, error) {
	q := pageQuery(limit, offset)
	if storageID > 0 {
		q.Set("storage_id", strconv.FormatInt(storageID, 10))
	}
	var page TrashPage
	raw, err := c.getJSONInto(ctx, "/api/files/manager/trash", q, "trash", &page)
	if err != nil {
		return nil, err
	}
	page.Raw = raw
	return &page, nil
}

// RestoreTrash puts one trash entry back where it was deleted from
// (POST /api/files/manager/restore). A path that is taken again answers
// 409 EXISTS and nothing moves.
func (c *Client) RestoreTrash(ctx context.Context, id int64) ([]byte, error) {
	if id <= 0 {
		return nil, fmt.Errorf("bad trash entry id %d", id)
	}
	return c.postJSON(ctx, "/api/files/manager/restore", map[string]any{"node_id": id})
}

// ───────────────────────── versions ─────────────────────────

// Version is one recorded version of a file (GET /api/files/versions).
type Version struct {
	ID        int64     `json:"id"`
	NodeID    int64     `json:"node_id"`
	VersionN  int       `json:"version_n"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// VersionList is a file's version timeline.
type VersionList struct {
	NodeID   int64     `json:"node_id"`
	Versions []Version `json:"versions"`
	Raw      []byte    `json:"-"`
}

// Versions lists the recorded versions of the file with catalogue id nodeID
// (NodeID resolves a path).
func (c *Client) Versions(ctx context.Context, nodeID int64) (*VersionList, error) {
	q := url.Values{}
	q.Set("node_id", strconv.FormatInt(nodeID, 10))
	req, err := c.newRequest(ctx, http.MethodGet, "/api/files/versions", q, nil)
	if err != nil {
		return nil, err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	var out VersionList
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse versions: %w", err)
	}
	out.Raw = raw
	return &out, nil
}

// RestoreVersion makes version versionID the file's live content
// (POST /api/files/versions/restore). The server records the content it
// replaces as a version first, so a restore can itself be undone.
func (c *Client) RestoreVersion(ctx context.Context, nodeID, versionID int64) ([]byte, error) {
	if nodeID <= 0 || versionID <= 0 {
		return nil, fmt.Errorf("bad file id %d or version id %d", nodeID, versionID)
	}
	return c.postJSON(ctx, "/api/files/versions/restore", map[string]any{
		"node_id":          nodeID,
		"version_id":       versionID,
		"snapshot_current": true,
	})
}
