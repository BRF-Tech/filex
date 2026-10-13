package cliclient

import (
	"context"
	"net/url"
)

// Space is the room at a remote path, in bytes: the server's internal/space
// figures, the ones `df` shows over SFTP, NFS and /dav too.
type Space struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Free  uint64 `json:"free"`
	// Known is false when the server could measure nothing there and the
	// account has no quota.
	Known bool `json:"known"`
}

// Space asks how much room remote has (GET /api/files/manager?action=space);
// "" asks for the storage list, the storages added up. A server older than the
// question answers 501, an *APIError like any other refusal.
func (c *Client) Space(ctx context.Context, remote string) (*Space, error) {
	q := url.Values{}
	q.Set("action", "space")
	if remote != "" {
		rp, err := ParseRemotePath(remote)
		if err != nil {
			return nil, err
		}
		q.Set("path", rp.String())
	}
	var sp Space
	if _, err := c.getJSONInto(ctx, managerPath, q, "space", &sp); err != nil {
		return nil, err
	}
	return &sp, nil
}
