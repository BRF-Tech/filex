// Package handlers — ai_tags.go
//
// Tags for agents: GET/POST /api/ai/tags and the MCP `file_tags` tool.
//
// Until v0.43.0 an agent could FILTER by tag (`tag:` in file_search) but could
// neither read a file's tags nor set them — and there was one kind. With two
// kinds the agent has to be able to say which one it means, so this surface
// speaks the same {name, kind} items as the explorer's routes and runs the
// very same rules (tagOps, tags.go): personal = only the token's user sees it;
// team = the tenant, and needs edit permission on the file.
//
// ⚠ It never has a default kind. The explorer's legacy `tags: [names]` shape
// defaults new names to personal for old clients; an agent is new code, so it
// must name the kind of every tag it writes — an agent quietly publishing a
// label to the whole tenant, or hiding one the user meant to share, is exactly
// the kind of silent audience change the finding was about.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
)

// aiTagsResult is a file's tags as the token's user sees them.
type aiTagsResult struct {
	Path string    `json:"path"`
	Tags []tagItem `json:"tags"`
	// CanEditTeam: whether this user may add or remove TEAM tags here.
	CanEditTeam bool `json:"can_edit_team"`
}

// Tags reads a file's tags (set == nil) or makes the visible set exactly
// `set`. The path is resolved like every AI op — token root, tenant, ≥viewer —
// and the catalogue row is created on demand (catalogueOnDemand), because an
// agent addresses files by path and a file just written may not be indexed yet.
func (a *aiOps) Tags(ctx context.Context, p string, set *[]tagItem) (*aiTagsResult, error) {
	c, ok := tagCallerOf(ctx)
	if !ok {
		return nil, errAIForbidden
	}
	s, rel, err := a.resolveStorage(ctx, p)
	if err != nil {
		return nil, err
	}
	if rel == "" {
		return nil, errors.New("a storage root carries no tags — name a file or folder")
	}
	n, err := catalogueOnDemand(ctx, a.store, a.resolver, a.sync(), s.ID, rel)
	if err != nil {
		return nil, err
	}
	ops := tagOps{store: a.store, acl: a.acl}
	level := ops.level(ctx, n)
	var items []tagItem
	if set != nil {
		items, err = ops.set(ctx, c, n, level, tagsSetReq{NodeID: n.ID, Items: set})
		if errors.Is(err, errTeamNeedsEdit) {
			return nil, denied(errAIForbidden, "%s", err.Error())
		}
	} else {
		var rows = []*model.Tag{}
		rows, err = ops.visibleOnNode(ctx, c, n.ID)
		items = itemsOf(rows)
	}
	if err != nil {
		return nil, err
	}
	return &aiTagsResult{
		Path:        joinAdapterPath(s.Name, rel),
		Tags:        items,
		CanEditTeam: level >= acl.LevelEditor,
	}, nil
}

// aiTagsBody is the body of POST /api/ai/tags.
type aiTagsBody struct {
	Path string    `json:"path"`
	Tags []tagItem `json:"tags"`
}

// TagsGet → GET /api/ai/tags?path=
func (h *AI) TagsGet(w http.ResponseWriter, r *http.Request) {
	res, err := h.ops.Tags(r.Context(), r.URL.Query().Get("path"), nil)
	h.writeTags(w, res, err)
}

// TagsSet → POST /api/ai/tags {path, tags:[{name, kind}]}. `tags` is the whole
// set the user can see afterwards; [] clears it.
func (h *AI) TagsSet(w http.ResponseWriter, r *http.Request) {
	var body aiTagsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if body.Tags == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tags required: the whole list, [] to clear"})
		return
	}
	res, err := h.ops.Tags(r.Context(), body.Path, &body.Tags)
	h.writeTags(w, res, err)
}

func (h *AI) writeTags(w http.ResponseWriter, res *aiTagsResult, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, res)
	case badTagInput(err):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
	}
}
