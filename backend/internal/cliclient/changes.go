package cliclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// ErrChangesUnsupported means the server has no `action=changes` (anything
// before it answers 501): the caller has to walk the tree to find out.
var ErrChangesUnsupported = errors.New("the server cannot report changes")

// Changes asks whether anything under the folder remote changed since the
// cursor since (empty = never asked; always "changed"), and returns the
// cursor to send next time. One request, instead of listing the whole tree.
func (c *Client) Changes(ctx context.Context, remote, since string) (cursor string, changed bool, err error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return "", false, err
	}
	q := url.Values{}
	q.Set("action", "changes")
	q.Set("path", rp.String())
	if since != "" {
		q.Set("since", since)
	}
	req, err := c.newRequest(ctx, http.MethodGet, managerPath, q, nil)
	if err != nil {
		return "", false, err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotImplemented {
			return "", false, ErrChangesUnsupported
		}
		return "", false, err
	}
	var out struct {
		Cursor  string `json:"cursor"`
		Changed bool   `json:"changed"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", false, fmt.Errorf("parse changes: %w", err)
	}
	if out.Cursor == "" {
		// A server that answers 200 without a cursor is not one this client
		// understands; walking is always correct.
		return "", false, ErrChangesUnsupported
	}
	return out.Cursor, out.Changed, nil
}
