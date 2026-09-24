// Package handlers — tags.go
//
// Personal and team tags (v0.43.0, migration 00055).
//
//	GET  /api/files/manager/tags?node_id=…     the tags on one file the caller can see
//	GET  /api/files/manager/tags?storage_id=…  the tags in use in one storage
//	GET  /api/files/manager/tags/all           every tag the caller can see
//	POST /api/files/manager/tags               set a file's tags
//	GET  /api/files/manager/tagged?tag=…[&kind=personal|team]
//
// # The finding this answers
//
// ⚠⚠ Tags used to be ONE shared label per file (node_meta `tag:<name>`), with
// no owner and no tenant, although routes.go introduced them as "Per-user
// metadata". Measured on v0.42 (tester, 2026-09-22, and reproduced here before
// the fix): user A tagged a file "Müşteri Teklifi"; user B's and the admin's
// `tags/all` answered `["müşteri teklifi"]`; B's `POST {tags:[]}` answered 200
// and the tag was gone for A; and with multi-tenancy on, tenant bravo's
// `tags/all` listed tenant alpha's tag. The name had also been lower-cased.
//
// # The rules (the owner's decision: "personal ve team olarak ikiye ayrılır,
// tenant based tagging yapılır")
//
//   - PERSONAL — one person's own label, like a star. Only its owner sees it,
//     adds it or removes it. Anyone who can see a file may personal-tag it.
//   - TEAM — shared with everyone in the TENANT who can see the file. Adding or
//     removing one needs EDIT permission on the file (the ACL level, `editor`
//     or `owner`); a viewer sees team tags and cannot change them.
//   - A team tag never crosses a tenant boundary. A confined caller (a tenant
//     in multi-tenant mode) sees its own tenant's team tags plus tenant-0 ones
//     ("the instance": made before a tenant existed on that storage); never
//     another tenant's. An unconfined caller — single-tenant mode, where the
//     tenant IS the instance, or the platform supertenant, which already
//     reaches every storage — sees every team tag on the files it can see.
//   - A tag is only ever named to someone who can see at least one file that
//     carries it: a tag NAME is information too ("Layoffs Q4").
//
// # What an old client gets
//
// A `POST {node_id, tags:[names]}` with no `kind` — every client before
// v0.43.0 — creates PERSONAL tags. Deliberately the narrow default:
//
//  1. A request that does not say who should see a label must not show it to
//     anyone else. The finding WAS an unintended audience; defaulting the old
//     shape to team would reproduce it for every stale tab and script.
//  2. It always works. Any caller who can see a file may personal-tag it; a
//     team default would turn a viewer's old client into a stream of 403s.
//  3. It is what the code always claimed ("per-user"), and what the tester
//     expected.
//
// Names in that list that match a tag the file ALREADY carries keep that tag
// as it is (team stays team) — an old client round-trips the list it read, and
// an untouched team tag in it is not a change. A team tag the old client
// leaves OUT is a removal, and needs edit permission like any other.
//
// # Case
//
// Names keep the case they were typed in; two names are one tag when
// internal/tagname.Key says so (case-folded, Turkish I/ı/İ/i merged, NFC). See
// that package for why strings.EqualFold is not the rule.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tagname"
)

// tagCaller is who is asking, reduced to what tag visibility depends on.
type tagCaller struct {
	userID int64
	// confined: a real tenant in multi-tenant mode (tenantown.confinedScope).
	// Single-tenant mode and the supertenant are NOT confined.
	confined bool
	tenant   int64
}

// tagCallerOf reads the caller from the request context. False when nobody is
// signed in — every tag surface is authenticated, so that is a 401.
func tagCallerOf(ctx context.Context) (tagCaller, bool) {
	u := auth.UserFrom(ctx)
	if u == nil {
		return tagCaller{}, false
	}
	c := tagCaller{userID: u.ID}
	if scope, confined := confinedScope(ctx); confined {
		c.confined = true
		c.tenant = scope.ProviderID
	}
	return c, true
}

// query is the part of the vocabulary this caller may even consider: their
// own personal tags, and the team tags of their tenant (+ tenant 0) — or of
// every tenant when unconfined.
//
// ⚠ This is half of the boundary; `sees` is the same rule applied to a row.
// Both exist because the store is asked with `query` (so another tenant's rows
// never leave the database) and rows that arrive by other paths — every tag on
// a node — are judged with `sees`. Change one, change the other.
func (c tagCaller) query() model.TagQuery {
	q := model.TagQuery{OwnerID: c.userID, Team: true}
	if c.confined {
		q.TeamTenants = []int64{c.tenant, 0}
	}
	return q
}

// sees reports whether a tag ROW is this caller's to see (the file is judged
// separately, by the ACL).
func (c tagCaller) sees(t *model.Tag) bool {
	switch t.Kind {
	case model.TagPersonal:
		return t.OwnerID != nil && *t.OwnerID == c.userID
	case model.TagTeam:
		if t.TenantID == nil {
			return false
		}
		return !c.confined || *t.TenantID == c.tenant || *t.TenantID == 0
	}
	return false
}

// tagItem is a tag on the wire: its name as typed and its kind. The id is
// deliberately not exposed — a client addresses tags by name, and ids would
// only let one tenant probe the size of another's vocabulary.
type tagItem struct {
	Name string `json:"name" jsonschema:"the tag as a person reads it; capitals are kept"`
	Kind string `json:"kind" jsonschema:"personal (only its owner sees it) or team (everyone in the tenant who can see the file)"`
}

// tagIdent is (kind, key): the identity a person means by "this tag". Two
// rows can share one — a tenant-0 team tag and the tenant's own of the same
// name, or the per-tenant copies the migration made — and they are treated as
// ONE tag everywhere a person sees them.
type tagIdent struct{ kind, key string }

func identOf(t *model.Tag) tagIdent { return tagIdent{t.Kind, tagname.Key(t.Name)} }

// itemsOf collapses rows to one item per identity (oldest row's spelling),
// sorted by name then kind — personal before team for the same name, which is
// the order the picker and the panel draw them in. Always non-nil.
func itemsOf(tags []*model.Tag) []tagItem {
	seen := map[tagIdent]bool{}
	sorted := append([]*model.Tag(nil), tags...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	out := []tagItem{}
	for _, t := range sorted {
		id := identOf(t)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, tagItem{Name: t.Name, Kind: t.Kind})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ki, kj := tagname.Key(out[i].Name), tagname.Key(out[j].Name)
		if ki != kj {
			return ki < kj
		}
		return out[i].Kind == model.TagPersonal && out[j].Kind != model.TagPersonal
	})
	return out
}

// namesOf is the legacy `tags: []string` view of items: one name per KEY, so
// an old client that shows a flat list never shows "rapor" twice because the
// file carries a personal and a team "rapor".
func namesOf(items []tagItem) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, it := range items {
		k := tagname.Key(it.Name)
		if !seen[k] {
			seen[k] = true
			out = append(out, it.Name)
		}
	}
	return out
}

// ─────────────────── the operations ───────────────────

// tagOps is every tag rule that is not HTTP: visibility, vocabulary, writes.
// ONE implementation for both doors into tags — the explorer's routes (Meta)
// and the AI/MCP `file_tags` tool (aiOps) — so an agent can never tag, or
// see, differently from the person whose token it holds.
type tagOps struct {
	store db.Store
	// acl is the RBAC resolver. Nil = nothing to enforce (a handler built by
	// hand in a test) and every caller counts as owner — the package-wide
	// convention for an unwired resolver; BuildRouter always wires it.
	acl *acl.Resolver
}

func (h *Meta) tagOps() tagOps { return tagOps{store: h.Store, acl: h.ACL} }

// ─────────────────── file visibility ───────────────────

// level is the caller's ACL level on a node (lock-aware, so a file an app
// holds for signature reads as viewer to everyone — its team tags freeze with
// it, consistently with the row `perm` the client gates on).
func (o tagOps) level(ctx context.Context, n *model.Node) acl.Level {
	if o.acl == nil {
		return acl.LevelOwner
	}
	st, err := o.store.GetStorage(ctx, n.StorageID)
	if err != nil || st == nil {
		return acl.LevelNone
	}
	set, err := o.acl.LoadSet(ctx, auth.UserFrom(ctx), st)
	if err != nil {
		return acl.LevelNone
	}
	return set.Effective(n.Path)
}

// tagNode resolves a node for a tag request: tenant, token confinement, then
// the ACL. A file the caller may not see answers exactly as a file that does
// not exist (404 "node not found") — the same shape ownsNode uses, so an id
// sweep learns nothing.
//
// ⚠ The ACL step is new in v0.43.0. Before it, tags?node_id= and the POST
// asked only the tenant and the token root, so on an RBAC storage a user with
// no grant on a file could read and rewrite its tags by id.
func (h *Meta) tagNode(w http.ResponseWriter, r *http.Request, nodeID int64) (*model.Node, acl.Level, bool) {
	if !ownsNode(w, r, h.Store, nodeID, "node") || !rootNodeAllowed(w, r, h.Store, nodeID) {
		return nil, acl.LevelNone, false
	}
	n, err := h.Store.GetNode(r.Context(), nodeID)
	if err != nil || n == nil {
		notFound(w, "node")
		return nil, acl.LevelNone, false
	}
	lv := h.tagOps().level(r.Context(), n)
	if lv < acl.LevelViewer {
		notFound(w, "node")
		return nil, acl.LevelNone, false
	}
	return n, lv, true
}

// visibleNodes keeps the nodes the caller can see: tenant, token root and the
// ACL, with one ACL set per storage. The tag view's rows come from a query
// with no path predicate, so without the ACL step an RBAC storage's file
// names reached anyone who knew — or guessed — a tag.
func (o tagOps) visibleNodes(ctx context.Context, in []*model.Node) []*model.Node {
	in = confineNodesToTenant(ctx, in)
	in = confineNodesToRoot(ctx, o.store, in)
	if o.acl == nil {
		return in
	}
	user := auth.UserFrom(ctx)
	sets := map[int64]*acl.Set{}
	out := in[:0]
	for _, n := range in {
		set, ok := sets[n.StorageID]
		if !ok {
			if st, err := o.store.GetStorage(ctx, n.StorageID); err == nil && st != nil {
				set, _ = o.acl.LoadSet(ctx, user, st)
			}
			sets[n.StorageID] = set
		}
		if set != nil && set.Effective(n.Path) >= acl.LevelViewer {
			out = append(out, n)
		}
	}
	return out
}

// visibleVocabulary returns the tags the caller may be TOLD about: rows they
// may see that sit on at least one live file they can see, optionally within
// one storage. Judged placement by placement, stopping at the first visible
// file of each tag.
func (o tagOps) visibleVocabulary(ctx context.Context, c tagCaller, onlyStorage int64) ([]*model.Tag, error) {
	q := c.query()
	vocab, err := o.store.ListTags(ctx, q)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*model.Tag{}
	for _, t := range vocab {
		if c.sees(t) {
			byID[t.ID] = t
		}
	}
	if len(byID) == 0 {
		return []*model.Tag{}, nil
	}
	places, err := o.store.ListTagPlacements(ctx, q)
	if err != nil {
		return nil, err
	}
	// ListEnabledStorages goes through tenantstore in production, but the
	// tenant check is repeated below so a hand-built handler cannot leak.
	storages := map[int64]*model.Storage{}
	if list, err := o.store.ListEnabledStorages(ctx); err == nil {
		for _, st := range list {
			storages[st.ID] = st
		}
	}
	scope, confined := confinedScope(ctx)
	root, rooted := confine.RootFrom(ctx)
	user := auth.UserFrom(ctx)
	sets := map[int64]*acl.Set{}
	shown := map[int64]bool{}
	for _, p := range places {
		if shown[p.TagID] || byID[p.TagID] == nil {
			continue
		}
		if onlyStorage > 0 && p.StorageID != onlyStorage {
			continue
		}
		st := storages[p.StorageID]
		if st == nil || (confined && !scope.CanAccessStorage(st.ID)) {
			continue
		}
		if rooted && !root.Within(st.Name, p.Path) {
			continue
		}
		if o.acl != nil {
			set, ok := sets[st.ID]
			if !ok {
				set, _ = o.acl.LoadSet(ctx, user, st)
				sets[st.ID] = set
			}
			if set == nil || set.Effective(p.Path) < acl.LevelViewer {
				continue
			}
		}
		shown[p.TagID] = true
	}
	out := make([]*model.Tag, 0, len(shown))
	for id := range shown {
		out = append(out, byID[id])
	}
	return out, nil
}

// visibleOnNode is the tags on one node this caller may see. The node itself
// has already passed tagNode.
func (o tagOps) visibleOnNode(ctx context.Context, c tagCaller, nodeID int64) ([]*model.Tag, error) {
	all, err := o.store.ListNodeTags(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	out := make([]*model.Tag, 0, len(all))
	for _, t := range all {
		if c.sees(t) {
			out = append(out, t)
		}
	}
	return out, nil
}

// resolveTagIDs is every vocabulary row the caller may see whose name is `name`
// (by Key) and whose kind is `kind` ("" = either). The rows the tag view and
// the `tag:` search filter read files through.
func resolveTagIDs(ctx context.Context, store db.Store, c tagCaller, name, kind string) ([]int64, string, error) {
	key := tagname.Key(name)
	vocab, err := store.ListTags(ctx, c.query())
	if err != nil {
		return nil, "", err
	}
	var ids []int64
	display := ""
	for _, t := range vocab {
		if !c.sees(t) || (kind != "" && t.Kind != kind) || tagname.Key(t.Name) != key {
			continue
		}
		if display == "" {
			display = t.Name
		}
		ids = append(ids, t.ID)
	}
	return ids, display, nil
}

// ─────────────────── writes ───────────────────

// teamTenantFor is the tenant a NEW team tag on n belongs to: the caller's own
// when confined; otherwise the storage's tenant (its lowest-id linked
// provider, GetProviderIDForStorage), else 0 — "the instance". So a platform
// operator tagging a customer's file makes a tag that customer sees, and a
// single-tenant install's tags follow the storage if tenants are introduced
// later.
func (o tagOps) teamTenantFor(ctx context.Context, c tagCaller, n *model.Node) int64 {
	if c.confined {
		return c.tenant
	}
	if id, ok, err := o.store.GetProviderIDForStorage(ctx, n.StorageID); err == nil && ok {
		return id
	}
	return 0
}

// ensureTag returns the caller's vocabulary row for (kind, name), creating it
// the first time. Matching is by tagname.Key recomputed from each row's NAME,
// never by the stored name_key alone: rows the 00055 migration wrote carry an
// approximated key (the Go fold cannot run in SQL), and recomputing makes them
// match exactly like rows written here.
//
// ⚠ A team tag is looked up in the target tenant ONLY — not in tenant 0 —
// so a tenant's new tag never extends an instance-level tag that a second
// tenant sharing the storage could also see.
func (o tagOps) ensureTag(ctx context.Context, c tagCaller, kind, name string, tenant int64) (*model.Tag, error) {
	key := tagname.Key(name)
	var q model.TagQuery
	if kind == model.TagPersonal {
		q.OwnerID = c.userID
	} else {
		q.Team = true
		q.TeamTenants = []int64{tenant}
	}
	find := func() (*model.Tag, error) {
		rows, err := o.store.ListTags(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, t := range rows {
			if t.Kind == kind && tagname.Key(t.Name) == key {
				return t, nil
			}
		}
		return nil, nil
	}
	if t, err := find(); err != nil || t != nil {
		return t, err
	}
	nt := &model.Tag{Kind: kind, Name: name, Key: key}
	if kind == model.TagPersonal {
		uid := c.userID
		nt.OwnerID = &uid
	} else {
		tid := tenant
		nt.TenantID = &tid
	}
	created, err := o.store.CreateTag(ctx, nt)
	if err != nil {
		// Most likely a concurrent create of the same name hit the UNIQUE
		// index first: that row is the answer.
		if t, ferr := find(); ferr == nil && t != nil {
			return t, nil
		}
		return nil, err
	}
	return created, nil
}

type tagsSetReq struct {
	NodeID int64 `json:"node_id"`
	// Tags is the pre-v0.43 shape: the file's whole list, as names.
	Tags []string `json:"tags"`
	// Kind is the kind a NEW name in Tags gets. Absent = personal (see the
	// file header on why).
	Kind string `json:"kind"`
	// Items is the v0.43 shape: the whole set the caller can see, with kinds.
	// When present it wins over Tags/Kind.
	Items *[]tagItem `json:"items"`
}

// The refusals `set` can give; the HTTP handler and the MCP tool each word
// them in their own shape (400 / 403, or a tool error).
var (
	errTeamNeedsEdit = errors.New("changing a team tag needs edit permission on this file")
	errBadTagKind    = errors.New("kind must be personal or team")
)

// badTagInput reports whether err is the caller's fault (a 400), as opposed to
// a permission refusal or a store failure.
func badTagInput(err error) bool {
	return errors.Is(err, errBadTagKind) || errors.Is(err, tagname.ErrEmpty) ||
		errors.Is(err, tagname.ErrTooLong) || errors.Is(err, tagname.ErrControl)
}

// set makes the tags the caller can see on n exactly `req`, and returns the
// set they can see afterwards. Tags the caller cannot see (another person's
// personal tags, another tenant's team tags) are never touched — so "clear
// every tag" from one account removes only what that account was shown.
//
// `level` is the caller's ACL level on n, already resolved by the door they
// came in through.
func (o tagOps) set(ctx context.Context, c tagCaller, n *model.Node, level acl.Level, req tagsSetReq) ([]tagItem, error) {
	defaultKind := model.TagPersonal
	if req.Kind != "" {
		if !model.ValidTagKind(req.Kind) {
			return nil, errBadTagKind
		}
		defaultKind = req.Kind
	}
	current, err := o.visibleOnNode(ctx, c, n.ID)
	if err != nil {
		return nil, err
	}

	// The desired set, as identity → display name, in request order.
	desired := map[tagIdent]string{}
	var order []tagIdent
	want := func(kind, raw string) error {
		name, err := tagname.Clean(raw)
		if err != nil {
			return err
		}
		id := tagIdent{kind, tagname.Key(name)}
		if _, dup := desired[id]; !dup {
			desired[id] = name
			order = append(order, id)
		}
		return nil
	}
	if req.Items != nil {
		for _, it := range *req.Items {
			if !model.ValidTagKind(it.Kind) {
				return nil, errBadTagKind
			}
			if err := want(it.Kind, it.Name); err != nil {
				return nil, err
			}
		}
	} else {
		// Legacy: a name that matches a tag already on the file keeps THAT
		// tag, whatever its kind; any other name is new and gets defaultKind.
		byKey := map[string][]*model.Tag{}
		for _, t := range current {
			k := tagname.Key(t.Name)
			byKey[k] = append(byKey[k], t)
		}
		for _, raw := range req.Tags {
			name, err := tagname.Clean(raw)
			if err != nil {
				return nil, err
			}
			if have := byKey[tagname.Key(name)]; len(have) > 0 {
				for _, t := range have {
					_ = want(t.Kind, t.Name)
				}
				continue
			}
			_ = want(defaultKind, name)
		}
	}

	// Diff against what is there.
	present := map[tagIdent]bool{}
	var remove []int64
	teamChange := false
	for _, t := range current {
		id := identOf(t)
		present[id] = true
		if _, keep := desired[id]; !keep {
			remove = append(remove, t.ID)
			teamChange = teamChange || t.Kind == model.TagTeam
		}
	}
	var adds []tagIdent
	for _, id := range order {
		if !present[id] {
			adds = append(adds, id)
			teamChange = teamChange || id.kind == model.TagTeam
		}
	}
	// ⚠⚠ The check the finding was missing: before v0.43.0 anyone who could
	// reach the file id could rewrite its (shared) tags. A team tag belongs to
	// everyone who can see the file, so changing one is editing the file's
	// shared description — editor or owner. Judged on the ACL LEVEL, not on
	// the storage's read_only flag: a tag is catalogue metadata, never a byte
	// on the storage, and tagging an archive is exactly what tags are for.
	if teamChange && level < acl.LevelEditor {
		return nil, errTeamNeedsEdit
	}
	var add []int64
	tenant := int64(-1)
	for _, id := range adds {
		if id.kind == model.TagTeam && tenant < 0 {
			tenant = o.teamTenantFor(ctx, c, n)
		}
		t, err := o.ensureTag(ctx, c, id.kind, desired[id], tenant)
		if err != nil {
			return nil, err
		}
		add = append(add, t.ID)
	}
	if len(add) > 0 || len(remove) > 0 {
		if err := o.store.LinkNodeTags(ctx, n.ID, add, remove); err != nil {
			return nil, err
		}
	}
	after, err := o.visibleOnNode(ctx, c, n.ID)
	if err != nil {
		return nil, err
	}
	return itemsOf(after), nil
}

// SetTags sets the tags on a file that the caller can see: their personal
// tags and their tenant's team tags (see tagOps.set).
func (h *Meta) SetTags(w http.ResponseWriter, r *http.Request) {
	c, ok := tagCallerOf(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var req tagsSetReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad node_id"})
		return
	}
	n, level, ok := h.tagNode(w, r, req.NodeID)
	if !ok {
		return
	}
	items, err := h.tagOps().set(r.Context(), c, n, level, req)
	switch {
	case err == nil:
	case badTagInput(err):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, errTeamNeedsEdit):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"node_id": n.ID,
		"tags":    namesOf(items),
		"items":   items,
	})
}

// ─────────────────── reads ───────────────────

// GetTags lists the tags for one node (?node_id=) or the tags in use in a
// storage (?storage_id=), as the caller can see them. Exactly one of the two.
//
// Both answers keep the pre-v0.43 `tags: []string` and add `items` (name +
// kind). The node answer also says `can_edit_team`, so the picker offers
// "team" only where saving it would succeed.
func (h *Meta) GetTags(w http.ResponseWriter, r *http.Request) {
	c, ok := tagCallerOf(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	q := r.URL.Query()
	if v := q.Get("node_id"); v != "" {
		nodeID, err := strconv.ParseInt(v, 10, 64)
		if err != nil || nodeID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad node_id"})
			return
		}
		n, level, ok := h.tagNode(w, r, nodeID)
		if !ok {
			return
		}
		tags, err := h.tagOps().visibleOnNode(r.Context(), c, n.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		items := itemsOf(tags)
		writeJSON(w, http.StatusOK, map[string]any{
			"node_id":       nodeID,
			"tags":          namesOf(items),
			"items":         items,
			"can_edit_team": level >= acl.LevelEditor,
		})
		return
	}
	if v := q.Get("storage_id"); v != "" {
		storageID, err := strconv.ParseInt(v, 10, 64)
		if err != nil || storageID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage_id"})
			return
		}
		if !ownsStorage(w, r, storageID, "storage") {
			return
		}
		tags, err := h.tagOps().visibleVocabulary(r.Context(), c, storageID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		items := itemsOf(tags)
		writeJSON(w, http.StatusOK, map[string]any{"storage_id": storageID, "tags": namesOf(items), "items": items})
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "node_id or storage_id required"})
}

// ListAllTags lists every tag the caller can see, both kinds, alphabetical.
// Powers the navigation panel's Tags section and the Tagged files page.
func (h *Meta) ListAllTags(w http.ResponseWriter, r *http.Request) {
	c, ok := tagCallerOf(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	tags, err := h.tagOps().visibleVocabulary(r.Context(), c, 0)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	items := itemsOf(tags)
	writeJSON(w, http.StatusOK, map[string]any{"tags": namesOf(items), "items": items})
}

// TaggedNodes lists the live files carrying a tag (?tag=…), newest-first,
// capped by ?limit=. ?kind=personal|team narrows to one kind; absent = both
// (a `#.tag~x` link from before v0.43 keeps working). Empty tag → 400.
func (h *Meta) TaggedNodes(w http.ResponseWriter, r *http.Request) {
	c, ok := tagCallerOf(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	if tag == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tag required"})
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind != "" && !model.ValidTagKind(kind) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errBadTagKind.Error()})
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 500, 1000)
	ids, display, err := resolveTagIDs(r.Context(), h.Store, c, tag, kind)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if display == "" {
		display = tag
	}
	nodes, err := h.Store.ListNodesByTagIDs(r.Context(), ids, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// ⚠⚠ THE TAG VIEW WAS EMPTY IN EVERY MULTI-STORAGE INSTALL WITHOUT `rows`
	// (2026-09-13: `?tag=test` answered 3 nodes, the view drew 0): a node row
	// carries only `storage_id`, and the client drops a row it cannot address.
	// And without visibleNodes the listing had no ACL step at all — see there.
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": h.rows(r.Context(), h.tagOps().visibleNodes(r.Context(), nodes)),
		"tag":   display,
		"kind":  kind,
	})
}
