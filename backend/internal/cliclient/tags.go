package cliclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Tags (docs/SEARCH.md → Tags): a PERSONAL tag is its owner's alone, like a
// star; a TEAM tag is seen by everyone in the tenant who can see the file, and
// changing one needs edit permission on it.

// Tag kinds.
const (
	TagPersonal = "personal"
	TagTeam     = "team"
)

// TagItem is one tag as the caller sees it.
type TagItem struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// FileTags is a file's tags as the caller can see them.
type FileTags struct {
	NodeID      int64     `json:"node_id"`
	Items       []TagItem `json:"items"`
	CanEditTeam bool      `json:"can_edit_team"`
	Raw         []byte    `json:"-"`
}

// TagsOf reads the tags on one file (GET /api/files/manager/tags?node_id=).
func (c *Client) TagsOf(ctx context.Context, nodeID int64) (*FileTags, error) {
	q := url.Values{}
	q.Set("node_id", strconv.FormatInt(nodeID, 10))
	req, err := c.newRequest(ctx, http.MethodGet, "/api/files/manager/tags", q, nil)
	if err != nil {
		return nil, err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	var out FileTags
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse tags: %w", err)
	}
	out.Raw = raw
	return &out, nil
}

// SetTags makes the tags the caller can see on the file exactly items
// (POST /api/files/manager/tags with `items`). Tags the caller cannot see -
// another person's personal ones - are never touched.
func (c *Client) SetTags(ctx context.Context, nodeID int64, items []TagItem) (*FileTags, error) {
	if items == nil {
		items = []TagItem{}
	}
	raw, err := c.postJSON(ctx, "/api/files/manager/tags", map[string]any{"node_id": nodeID, "items": items})
	if err != nil {
		return nil, err
	}
	var out FileTags
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse tags: %w", err)
	}
	out.Raw = raw
	return &out, nil
}

// TagEdit is one change to a file's tags: names to add (with the kind a new
// one gets) and names to remove.
type TagEdit struct {
	Add    []string
	Remove []string
	// Kind is the kind an added name gets: TagPersonal (default) or TagTeam.
	// A removal with Kind set removes only that kind; "" removes the name in
	// either kind.
	Kind string
}

// EditTags applies edit to the file's current tags and saves the result: the
// read-modify-write the explorer's tag picker does. Names compare without
// regard to case, as the server's do.
func (c *Client) EditTags(ctx context.Context, nodeID int64, edit TagEdit) (*FileTags, error) {
	kind := edit.Kind
	if kind != "" && kind != TagPersonal && kind != TagTeam {
		return nil, fmt.Errorf("bad tag kind %q: want personal or team", kind)
	}
	cur, err := c.TagsOf(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	same := func(it TagItem, name, kind string) bool {
		return strings.EqualFold(strings.TrimSpace(it.Name), strings.TrimSpace(name)) && (kind == "" || it.Kind == kind)
	}
	items := make([]TagItem, 0, len(cur.Items)+len(edit.Add))
	for _, it := range cur.Items {
		drop := false
		for _, name := range edit.Remove {
			if same(it, name, edit.Kind) {
				drop = true
				break
			}
		}
		if !drop {
			items = append(items, it)
		}
	}
	addKind := kind
	if addKind == "" {
		addKind = TagPersonal
	}
	for _, name := range edit.Add {
		if strings.TrimSpace(name) == "" {
			continue
		}
		have := false
		for _, it := range items {
			if same(it, name, addKind) {
				have = true
				break
			}
		}
		if !have {
			items = append(items, TagItem{Name: strings.TrimSpace(name), Kind: addKind})
		}
	}
	return c.SetTags(ctx, nodeID, items)
}

// AllTags lists every tag the caller can see, both kinds
// (GET /api/files/manager/tags/all).
func (c *Client) AllTags(ctx context.Context) ([]TagItem, []byte, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/files/manager/tags/all", nil, nil)
	if err != nil {
		return nil, nil, err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, nil, err
	}
	var out struct {
		Items []TagItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("parse tags: %w", err)
	}
	return out.Items, raw, nil
}

// TaggedFile is one file carrying a tag.
type TaggedFile struct {
	ID      int64  `json:"id"`
	Storage string `json:"storage"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
}

// Location is the file's `adapter://path`.
func (f TaggedFile) Location() string {
	if f.Storage == "" {
		return f.Path
	}
	return RemotePath{Adapter: f.Storage, Rel: strings.Trim(f.Path, "/")}.String()
}

// TaggedFiles lists the files carrying tag, newest first
// (GET /api/files/manager/tagged). kind "" is both kinds.
func (c *Client) TaggedFiles(ctx context.Context, tag, kind string, limit int) ([]TaggedFile, []byte, error) {
	q := url.Values{}
	q.Set("tag", tag)
	if kind != "" {
		q.Set("kind", kind)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/api/files/manager/tagged", q, nil)
	if err != nil {
		return nil, nil, err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, nil, err
	}
	var out struct {
		Nodes []TaggedFile `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, fmt.Errorf("parse tagged files: %w", err)
	}
	return out.Nodes, raw, nil
}
