package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// Grants is the per-file/per-folder permission-management API backing the
// explorer's right-side "İzinler" (Permissions) panel. It is mounted under
// /api/files/permissions inside the authenticated group (session OR token),
// so confine.Middleware still applies to any path fields.
//
// Authorization for every endpoint: the caller must be an admin OR hold
// owner-level (acl.LevelOwner) on the target path. Viewer/editor accounts and
// non-owning users get 403 and never see the panel.
type Grants struct {
	Store     db.Store
	ACL       *acl.Resolver
	Share     *share.Service // optional — nil disables the share fallback in Invite
	Mailer    *mailer.Service
	PublicURL string
	// Tenants resolves which origin the invite e-mails link to. ⚠ The
	// account-created mail carries a temporary password next to a login URL —
	// on the operator's host that login cannot succeed, and the operator's
	// hostname leaks to every tenant that adds a user.
	Tenants tenanturl.Resolver

	// mailRate holds each account to shareMailHourlyRecipients addresses an
	// hour (share_mail.go), made on first use so a Grants built as a literal
	// is held to it too.
	mailRateOnce sync.Once
	mailRate     *ipLimiter
}

// AttachTenants wires the shared per-request origin resolver (internal/tenanturl).
func (h *Grants) AttachTenants(rv tenanturl.Resolver) { h.Tenants = rv }

// NewGrants constructs the permissions handler.
func NewGrants(store db.Store, resolver *acl.Resolver) *Grants {
	return &Grants{Store: store, ACL: resolver}
}

// AttachInvite wires the share service + mailer + public URL used by the
// email-invite flow (existing user → grant, admin → create user, else share).
func (h *Grants) AttachInvite(sh *share.Service, m *mailer.Service, publicURL string) {
	h.Share = sh
	h.Mailer = m
	h.PublicURL = strings.TrimRight(publicURL, "/")
	h.Tenants = tenanturl.New(h.Store, publicURL, h.Tenants.MultiTenant)
}

// tryMail sends best-effort; returns true iff the mail actually went out (SMTP
// configured + verified). A false result tells the caller to surface the link /
// temp password on-screen instead.
func (h *Grants) tryMail(ctx context.Context, to, subject, body string) bool {
	if h.Mailer == nil {
		return false
	}
	return h.Mailer.Send(ctx, to, subject, body) == nil
}

// grantView is the enriched grant row returned to the panel.
type grantView struct {
	*model.FileGrant
	// Kind is "user" (a person's grant) or "group" (a group's — GroupID and
	// GroupName are set, the user fields empty). The two have their own ids:
	// PATCH/DELETE a group's at /permissions/groups/{id}.
	Kind            string `json:"kind"`
	UserEmail       string `json:"user_email"`
	UserDisplayName string `json:"user_display_name"`
	Inherited       bool   `json:"inherited"`
}

// resolvePath splits an adapter://rel path and loads the storage row. Returns
// the storage, the cleaned rel, or an error already written to w.
func (h *Grants) resolvePath(w http.ResponseWriter, r *http.Request, raw string) (*model.Storage, string, bool) {
	if raw == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path"})
		return nil, "", false
	}
	adapter, rel := splitAdapterPath(raw)
	// Elsewhere in the API an unqualified path falls back to storages[0], but
	// a grant is durable authorization state and guessing its storage is not
	// recoverable by looking again. A multi-tenant deployment posted {"path":"/deneme"} and
	// watched the grant land on a different tenant's storage; adding a
	// `storage` / `storage_id` field to the body changed nothing, because no
	// such field is read (H6, 2026-08-05). Say which storage.
	if adapter == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": `path must name a storage, e.g. "Dosyalar://klasor"`,
		})
		return nil, "", false
	}
	st, err := h.Store.GetStorageByName(r.Context(), adapter)
	// GetStorageByName is not one of the methods tenantstore confines, so the
	// tenant gate is applied here. Out-of-tenant reads as unknown — same
	// answer as a name that doesn't exist, so this isn't an existence oracle.
	if err != nil || st == nil || !scopeOf(r.Context()).CanAccessStorage(st.ID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown adapter: " + adapter})
		return nil, "", false
	}
	rel = acl.CleanRel(rel)
	// The token's `root:`. Every path this file takes - a grant's, an
	// invitation's (which can mint a public link), a share mail's - is
	// resolved here, and up to 0.52 confine.Middleware rewrote a body's
	// `path` only when the body was labelled JSON: the same body as text/plain
	// granted, invited to and linked files outside the root
	// (GHSA-8gvc-6w52-6c7j).
	if !rootAllowsIn(r.Context(), st, rel) {
		refuseOutsideRoot(w)
		return nil, "", false
	}
	return st, rel, true
}

// scopeOf returns the request's tenant scope; a nil scope means "unscoped"
// (single-tenant mode) and reaches everything, per tenant.Scope's contract.
func scopeOf(ctx context.Context) *tenant.Scope {
	s, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}
	return s
}

// requireEditor reports whether the caller may write/share at (st, rel):
// admin, or acl.LevelEditor effective there. Used by the share-by-email action
// (same capability that created the link). Writes 403 + returns false if not.
func (h *Grants) requireEditor(w http.ResponseWriter, r *http.Request, st *model.Storage, rel string, p perm.Perm) bool {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	if u.IsAdmin() {
		return true
	}
	if h.ACL == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return false
	}
	set, err := h.ACL.LoadSet(r.Context(), u, st)
	if err != nil || set == nil || set.Effective(rel) < acl.LevelEditor {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return false
	}
	if p != "" && !set.AllowsAt(rel, p) {
		writePermDeniedSource(w, r, p, set.WhyAt(rel, p))
		return false
	}
	return true
}

// requireOwner reports whether the caller may manage permissions on (st, rel):
// admin, or acl.LevelOwner effective there — plus, when p is set, the
// caller's per-user permission p (share.users for every change to who has
// access; "" for merely reading the panel). Writes 403 + returns false if not.
func (h *Grants) requireOwner(w http.ResponseWriter, r *http.Request, st *model.Storage, rel string, p perm.Perm) bool {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	if u.IsAdmin() {
		return true
	}
	if h.ACL == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return false
	}
	set, err := h.ACL.LoadSet(r.Context(), u, st)
	if err != nil || set == nil || set.Effective(rel) < acl.LevelOwner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an owner can manage permissions here"})
		return false
	}
	if p != "" && !set.AllowsAt(rel, p) {
		writePermDeniedSource(w, r, p, set.WhyAt(rel, p))
		return false
	}
	return true
}

// List returns the direct + inherited grants for a path so the panel can show
// who has access (including permissions cascading from parent folders).
//
//	GET /api/files/permissions?path=<adapter://rel>
func (h *Grants) List(w http.ResponseWriter, r *http.Request) {
	st, rel, ok := h.resolvePath(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	if !h.requireOwner(w, r, st, rel, "") {
		return
	}
	all, err := h.Store.ListFileGrantsByStorage(r.Context(), st.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	groupGrants, err := h.Store.ListGroupFileGrantsByStorage(r.Context(), st.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	all = append(all, groupGrants...)
	direct := []grantView{}
	inherited := []grantView{}
	for _, g := range all {
		gp := acl.CleanRel(g.PathPrefix)
		gv := grantView{FileGrant: g, Kind: "user"}
		if g.GroupID != 0 {
			gv.Kind = "group"
			if gr, gerr := h.Store.GetGroup(r.Context(), g.GroupID); gerr == nil && gr != nil {
				gv.GroupName = gr.Name
			}
		} else if u, uerr := h.Store.GetUser(r.Context(), g.UserID); uerr == nil && u != nil {
			gv.UserEmail = u.Email
			gv.UserDisplayName = u.DisplayName
			gv.UserName = u.Label()
		}
		switch {
		case gp == rel:
			direct = append(direct, gv)
		case gp == "" || strings.HasPrefix(rel, gp+"/"):
			// Ancestor folder grant → inherited onto this path.
			gv.Inherited = true
			inherited = append(inherited, gv)
		}
	}
	effective := ""
	if u := auth.UserFrom(r.Context()); u != nil && h.ACL != nil {
		if set, _ := h.ACL.LoadSet(r.Context(), u, st); set != nil {
			effective = set.Effective(rel).String()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":         st.Name + "://" + rel,
		"storage_rbac": st.RBACEnabled,
		"direct":       direct,
		"inherited":    inherited,
		"effective":    effective,
	})
}

// grantCreateReq is what a grant request carries. ⚠ No `is_dir`: whether a
// grant is a folder's is the item's fact, read from the catalogue
// (grantIsDir), and a client that still sends the field is not asked.
type grantCreateReq struct {
	Path   string `json:"path"`
	UserID int64  `json:"user_id"`
	// GroupID grants to a group (every member) instead of one person. One
	// of the two.
	GroupID int64  `json:"group_id"`
	Level   string `json:"level"`
}

// grantIsDir says whether a grant on (st, rel) is a folder's — from the
// catalogue, never from the request. The storage root is a folder; a path the
// catalogue has not seen is recorded as one too, the default a grant always
// had when nobody said otherwise.
func (h *Grants) grantIsDir(ctx context.Context, st *model.Storage, rel string) bool {
	if rel == "" {
		return true
	}
	n, err := h.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, normalizeDBPath(rel)))
	if err != nil || n == nil {
		return true
	}
	return n.Type == model.NodeTypeDirectory
}

// Create (upsert) a grant for a user — or a group — on a path.
//
//	POST /api/files/permissions {path, user_id | group_id, level}
func (h *Grants) Create(w http.ResponseWriter, r *http.Request) {
	var req grantCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	st, rel, ok := h.resolvePath(w, r, req.Path)
	if !ok {
		return
	}
	if gate(w, r, h.ACL, st.ID, writegate.Names(rel)) {
		return
	}
	if !st.RBACEnabled {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "enable RBAC on this storage before granting per-item access"})
		return
	}
	if !h.requireOwner(w, r, st, rel, perm.ShareUsers) {
		return
	}
	if !model.ValidGrantLevel(req.Level) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid level"})
		return
	}
	if req.GroupID > 0 && req.UserID > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "give user_id or group_id, not both"})
		return
	}
	if req.GroupID > 0 {
		h.createGroupGrant(w, r, st, rel, req)
		return
	}
	if req.UserID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing user_id"})
		return
	}
	target, err := h.Store.GetUser(r.Context(), req.UserID)
	// The PATH was gated by resolvePath; the RECIPIENT was not. Granting a
	// foreign tenant's user into your own storage is a smaller harm than
	// reading theirs, but it is still a cross-tenant write to their access
	// graph, and the 404-vs-200 split made it a user-id existence oracle.
	if err != nil || target == nil || !userInTenant(r.Context(), target) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	// Account-role ceiling: a viewer account may only ever hold viewer grants.
	if target.IsViewer() && req.Level != model.GrantViewer {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a viewer account can only be granted viewer access"})
		return
	}
	var createdBy *int64
	if u := auth.UserFrom(r.Context()); u != nil {
		id := u.ID
		createdBy = &id
	}
	g, err := h.Store.CreateFileGrant(r.Context(), &model.FileGrant{
		StorageID:  st.ID,
		PathPrefix: rel,
		IsDir:      h.grantIsDir(r.Context(), st, rel),
		UserID:     req.UserID,
		Level:      req.Level,
		CreatedBy:  createdBy,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// #196 - the person's open explorers ask their menu answers again.
	emitAccessChanged(req.UserID)
	writeJSON(w, http.StatusOK, g)
}

// createGroupGrant is Create for a group. The group must be one the
// caller's tenant can see. Its members' account-role ceilings still apply —
// a viewer account in a group granted editor reaches the folder as a viewer —
// so, unlike a person's grant, any level is accepted.
func (h *Grants) createGroupGrant(w http.ResponseWriter, r *http.Request, st *model.Storage, rel string, req grantCreateReq) {
	g, err := h.Store.GetGroup(r.Context(), req.GroupID)
	if err != nil || g == nil || !groupForStorage(r.Context(), g) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
		return
	}
	var createdBy *int64
	if u := auth.UserFrom(r.Context()); u != nil {
		id := u.ID
		createdBy = &id
	}
	created, err := h.Store.CreateGroupFileGrant(r.Context(), &model.FileGrant{
		StorageID: st.ID, PathPrefix: rel, IsDir: h.grantIsDir(r.Context(), st, rel), GroupID: g.ID, Level: req.Level, CreatedBy: createdBy,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	created.GroupName = g.Name
	emitGroupAccessChanged(r.Context(), h.Store, g.ID) // #196
	writeJSON(w, http.StatusOK, created)
}

// groupForStorage reports whether g may be given a grant by a caller of the
// request's tenant: a tenant's own group, or — on an install without
// tenants, or for the supertenant — any. An install-wide group in a
// multi-tenant install can hold people of every tenant, so a confined caller
// never grants to one.
func groupForStorage(ctx context.Context, g *model.Group) bool {
	scope, confined := confinedScope(ctx)
	if !confined {
		return true
	}
	return g.ProviderID != nil && *g.ProviderID == scope.ProviderID
}

type grantPatchReq struct {
	Level string `json:"level"`
}

// Update changes a grant's level.
//
//	PATCH /api/files/permissions/{id} {level}
func (h *Grants) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	g, err := h.Store.GetFileGrant(r.Context(), id)
	if err != nil || g == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "grant not found"})
		return
	}
	var req grantPatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if !model.ValidGrantLevel(req.Level) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid level"})
		return
	}
	st, ok := h.authorizeGrant(w, r, g)
	if !ok {
		return
	}
	_ = st
	if target, uerr := h.Store.GetUser(r.Context(), g.UserID); uerr == nil && target != nil {
		if target.IsViewer() && req.Level != model.GrantViewer {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a viewer account can only be granted viewer access"})
			return
		}
	}
	if err := h.Store.UpdateFileGrantLevel(r.Context(), id, req.Level); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	emitAccessChanged(g.UserID) // #196
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete revokes a grant.
//
//	DELETE /api/files/permissions/{id}
func (h *Grants) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	g, err := h.Store.GetFileGrant(r.Context(), id)
	if err != nil || g == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "grant not found"})
		return
	}
	if _, ok := h.authorizeGrant(w, r, g); !ok {
		return
	}
	if err := h.Store.DeleteFileGrant(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	emitAccessChanged(g.UserID) // #196
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// groupGrantByID loads a group's grant named by {id}, answering 404.
func (h *Grants) groupGrantByID(w http.ResponseWriter, r *http.Request) *model.FileGrant {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return nil
	}
	g, err := h.Store.GetGroupFileGrant(r.Context(), id)
	if err != nil || g == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "grant not found"})
		return nil
	}
	return g
}

// UpdateGroup changes a group grant's level.
//
//	PATCH /api/files/permissions/groups/{id} {level}
func (h *Grants) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	g := h.groupGrantByID(w, r)
	if g == nil {
		return
	}
	var req grantPatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if !model.ValidGrantLevel(req.Level) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid level"})
		return
	}
	if _, ok := h.authorizeGrant(w, r, g); !ok {
		return
	}
	if err := h.Store.UpdateGroupFileGrantLevel(r.Context(), g.ID, req.Level); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	emitGroupAccessChanged(r.Context(), h.Store, g.GroupID) // #196
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DeleteGroup revokes a group grant.
//
//	DELETE /api/files/permissions/groups/{id}
func (h *Grants) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	g := h.groupGrantByID(w, r)
	if g == nil {
		return
	}
	if _, ok := h.authorizeGrant(w, r, g); !ok {
		return
	}
	if err := h.Store.DeleteGroupFileGrant(r.Context(), g.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	emitGroupAccessChanged(r.Context(), h.Store, g.GroupID) // #196
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// SearchGroups returns the groups matching q (by name) that the caller could
// grant to, so the permissions panel can offer them beside people. Like
// SearchUsers, any authenticated user may call it; it carries names only.
//
//	GET /api/files/permissions/groups?q=<substr>
func (h *Grants) SearchGroups(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	groups, err := h.Store.ListGroups(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, 10)
	for _, g := range groups {
		if !groupForStorage(r.Context(), g) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(g.Name), q) {
			continue
		}
		out = append(out, map[string]any{"id": g.ID, "name": g.Name, "description": g.Description})
		if len(out) >= 10 {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

// authorizeGrant loads the grant's storage and verifies the caller may manage
// it (owner of the grant's path, or admin).
func (h *Grants) authorizeGrant(w http.ResponseWriter, r *http.Request, g *model.FileGrant) (*model.Storage, bool) {
	st, err := h.Store.GetStorage(r.Context(), g.StorageID)
	if err != nil || st == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "storage not found"})
		return nil, false
	}
	// ⚠⚠ Tenancy, and it has to be HERE rather than in requireOwner below.
	// requireOwner short-circuits on u.IsAdmin(), and under multi-tenancy that
	// is true for the admin of every tenant — so the ACL layer, which is
	// tenant-blind by design, was the only thing standing between a tenant
	// admin and another tenant's permission graph. GetStorage above is the
	// unconfined lookup (tenantstore wraps only the three list queries), so
	// the grant's storage resolves perfectly well for a foreign id.
	if !ownsStorage(w, r, st.ID, "grant") {
		return nil, false
	}
	// A token's `root:` too: these routes name the grant by id, which
	// confine.Middleware cannot rewrite (confine_guard.go), and requireOwner
	// asks only about the ACCOUNT — which may own folders its token was
	// never meant to reach.
	if !rootAllows(r.Context(), h.Store, st.ID, acl.CleanRel(g.PathPrefix)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": confine.ErrOutOfRoot.Error()})
		return nil, false
	}
	if !h.requireOwner(w, r, st, acl.CleanRel(g.PathPrefix), perm.ShareUsers) {
		return nil, false
	}
	return st, true
}

// AdminList returns every grant across all storages, enriched with storage
// name + user email, for the admin panel's global "İzinler" overview (who has
// what, where). Admin-only via the /api/admin route group.
//
//	GET /api/admin/grants → {grants:[{id, storage_name, path_prefix, user_email, level, …}]}
func (h *Grants) AdminList(w http.ResponseWriter, r *http.Request) {
	all, err := h.Store.ListAllFileGrants(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Multi-tenant: a tenant-admin only sees grants on its own storages
	// (docs/MULTI-TENANCY.md §9).
	scope, scoped := tenant.FromContext(r.Context())
	storageName := map[int64]string{}
	userEmail := map[int64]string{}
	userName := map[int64]string{}
	out := make([]map[string]any, 0, len(all))
	for _, g := range all {
		if scoped && !scope.IsSupertenant && !scope.CanAccessStorage(g.StorageID) {
			continue
		}
		if _, ok := storageName[g.StorageID]; !ok {
			if st, e := h.Store.GetStorage(r.Context(), g.StorageID); e == nil && st != nil {
				storageName[g.StorageID] = st.Name
			}
		}
		if _, ok := userEmail[g.UserID]; !ok {
			if u, e := h.Store.GetUser(r.Context(), g.UserID); e == nil && u != nil {
				userEmail[g.UserID] = u.Email
				userName[g.UserID] = u.Label()
			}
		}
		out = append(out, map[string]any{
			"id":           g.ID,
			"storage_id":   g.StorageID,
			"storage_name": storageName[g.StorageID],
			"path":         storageName[g.StorageID] + "://" + g.PathPrefix,
			"path_prefix":  g.PathPrefix,
			"is_dir":       g.IsDir,
			"user_id":      g.UserID,
			"user_email":   userEmail[g.UserID],
			"user_name":    userName[g.UserID],
			"level":        g.Level,
			"created_at":   g.CreatedAt,
			"kind":         "user",
		})
	}
	// Groups' grants, in the same shape, with the group in place of the person.
	groupGrants, err := h.Store.ListAllGroupFileGrants(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	groupName := map[int64]string{}
	for _, g := range groupGrants {
		if scoped && !scope.IsSupertenant && !scope.CanAccessStorage(g.StorageID) {
			continue
		}
		if _, ok := storageName[g.StorageID]; !ok {
			if st, e := h.Store.GetStorage(r.Context(), g.StorageID); e == nil && st != nil {
				storageName[g.StorageID] = st.Name
			}
		}
		if _, ok := groupName[g.GroupID]; !ok {
			if gr, e := h.Store.GetGroup(r.Context(), g.GroupID); e == nil && gr != nil {
				groupName[g.GroupID] = gr.Name
			}
		}
		out = append(out, map[string]any{
			"id":           g.ID,
			"storage_id":   g.StorageID,
			"storage_name": storageName[g.StorageID],
			"path":         storageName[g.StorageID] + "://" + g.PathPrefix,
			"path_prefix":  g.PathPrefix,
			"is_dir":       g.IsDir,
			"group_id":     g.GroupID,
			"group_name":   groupName[g.GroupID],
			"level":        g.Level,
			"created_at":   g.CreatedAt,
			"kind":         "group",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": out})
}

// AdminDeleteGroup revokes any group's grant (admin override).
//
//	DELETE /api/admin/grants/groups/{id}
func (h *Grants) AdminDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	// The same unconditional existence check as AdminDelete, for the same
	// reason: a 404 only for a foreign id would say the row exists.
	g, gerr := h.Store.GetGroupFileGrant(r.Context(), id)
	if gerr != nil || g == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "grant not found"})
		return
	}
	if !ownsStorage(w, r, g.StorageID, "grant") {
		return
	}
	if err := h.Store.DeleteGroupFileGrant(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	emitGroupAccessChanged(r.Context(), h.Store, g.GroupID) // #196
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// AdminDelete revokes any grant (admin override).
//
//	DELETE /api/admin/grants/{id}
func (h *Grants) AdminDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	// AdminList beside this DOES filter by storage (see :389), so the grants
	// a tenant admin could not see were exactly the ones they could still
	// revoke by id.
	//
	// ⚠ An earlier draft of this comment claimed the user-facing sibling
	// (Delete, above) was "already safe because authorizeGrant calls
	// CanAccessStorage". That was wrong, and worth recording rather than
	// quietly correcting: authorizeGrant resolves the storage with the
	// UNCONFINED GetStorage and then defers to requireOwner, which returns
	// true for any u.IsAdmin() — and in multi-tenant mode a tenant admin IS
	// an admin. The confined lookup lives in resolvePath (:101-107), which is
	// a different entry point serving the PATH-shaped routes. So the
	// by-id routes were open too; authorizeGrant now carries the check.
	// ⚠ The existence check is unconditional, not just for confined callers.
	// DeleteFileGrant answers {"ok":true} for an id that names nothing, so if
	// only a tenant's foreign id produced a 404 the status code would announce
	// that the row exists. Both answers have to agree. (storages.Delete makes
	// the same argument for the same reason.)
	g, gerr := h.Store.GetFileGrant(r.Context(), id)
	if gerr != nil || g == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "grant not found"})
		return
	}
	if !ownsStorage(w, r, g.StorageID, "grant") {
		return
	}
	if err := h.Store.DeleteFileGrant(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	emitAccessChanged(g.UserID) // #196
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// SearchUsers returns existing accounts matching q (email or display name) so
// the permissions panel can autocomplete as the owner types. Any authenticated
// user may call it (the panel itself is owner-gated); results are capped and
// carry no secrets.
//
//	GET /api/files/permissions/users?q=<substr>
func (h *Grants) SearchUsers(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, 10)
	for _, u := range users {
		if q != "" && !strings.Contains(strings.ToLower(u.Email), q) &&
			!strings.Contains(strings.ToLower(u.DisplayName), q) &&
			!strings.Contains(strings.ToLower(u.Username), q) {
			continue
		}
		out = append(out, map[string]any{
			"id":           u.ID,
			"email":        u.Email,
			"display_name": u.DisplayName,
			"username":     u.Username,
			"role":         u.Role,
		})
		if len(out) >= 10 {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

// Resolve looks up a user by email so the panel can decide between a direct
// grant (existing account) and the invite flow (no account).
//
//	GET /api/files/permissions/resolve?email=<addr>
func (h *Grants) Resolve(w http.ResponseWriter, r *http.Request) {
	email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
	if email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing email"})
		return
	}
	u, err := h.Store.GetUserByEmail(r.Context(), email)
	// ⚠ GetUserByEmail is not one of the three methods tenantstore confines,
	// so this endpoint punched straight through the ListUsers directory gate
	// that every picker beside it relies on: one query per address turned it
	// into a membership oracle over the whole platform. Because e-mail is
	// still globally unique (docs/MULTI-TENANCY.md §4), "this address exists"
	// is exactly the question a competitor would ask.
	//
	// A foreign account answers `found:false` — the same shape as an address
	// nobody has registered — rather than an error, so the refusal itself
	// carries no signal.
	if err != nil || u == nil || !userInTenant(r.Context(), u) {
		writeJSON(w, http.StatusOK, map[string]any{"found": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found": true,
		"user": map[string]any{
			"id":           u.ID,
			"email":        u.Email,
			"display_name": u.DisplayName,
			"username":     u.Username,
			"role":         u.Role,
		},
	})
}

type inviteReq struct {
	Path       string `json:"path"`
	Email      string `json:"email"`
	Level      string `json:"level"`
	CreateUser bool   `json:"create_user,omitempty"`
	Role       string `json:"role,omitempty"`   // new-user role when CreateUser (default "user")
	Locale     string `json:"locale,omitempty"` // the language PICKED for the recipient (an account opened here starts in it); absent: the server's
	// No `is_dir`: the item says what it is (grantIsDir).
}

// Invite grants access to an email address. Three outcomes (owner/admin only):
//   - existing account → a direct ACL grant (mode "granted")
//   - no account + caller is admin + create_user → new account + grant, temp
//     password mailed (or returned for on-screen display) (mode "user_created")
//   - otherwise → a public share link, mailed or returned (mode "shared")
//
// Mail is sent only when SMTP is configured AND verified; otherwise the link /
// temp password comes back in the response for the UI to show.
//
//	POST /api/files/permissions/invite {path, email, level, create_user?, role?}
func (h *Grants) Invite(w http.ResponseWriter, r *http.Request) {
	var req inviteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	st, rel, ok := h.resolvePath(w, r, req.Path)
	if !ok {
		return
	}
	if !h.requireOwner(w, r, st, rel, perm.ShareUsers) {
		return
	}
	if !model.ValidGrantLevel(req.Level) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid level"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || !strings.Contains(email, "@") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid email required"})
		return
	}
	caller := auth.UserFrom(r.Context())
	var createdBy *int64
	if caller != nil {
		id := caller.ID
		createdBy = &id
	}
	isDir := h.grantIsDir(r.Context(), st, rel)

	// ── Existing account → direct grant. ──
	if u, err := h.Store.GetUserByEmail(r.Context(), email); err == nil && u != nil && userInTenant(r.Context(), u) {
		if !st.RBACEnabled {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "enable RBAC on this storage first"})
			return
		}
		if u.IsViewer() && req.Level != model.GrantViewer {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a viewer account can only be granted viewer access"})
			return
		}
		if _, gerr := h.Store.CreateFileGrant(r.Context(), &model.FileGrant{
			StorageID: st.ID, PathPrefix: rel, IsDir: isDir, UserID: u.ID, Level: req.Level, CreatedBy: createdBy,
		}); gerr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": gerr.Error()})
			return
		}
		emitAccessChanged(u.ID) // #196
		// The recipient's own language: an account reads its owner's choice
		// (translated at the last stop, never in the sender's).
		loc := srvtext.Pick(u.Locale)
		subject, body := itemGrantText(loc, st.Name+"://"+rel, h.Tenants.FromRequest(r)+"/admin/explore")
		emailed := h.tryMail(mailer.WithLanguage(r.Context(), loc), email, subject, body)
		writeJSON(w, http.StatusOK, map[string]any{"mode": "granted", "user_id": u.ID, "emailed": emailed})
		return
	}

	// ── No account + admin + create_user → make the account + grant. ──
	if req.CreateUser {
		if caller == nil || !caller.IsAdmin() {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an admin can create new users"})
			return
		}
		if !st.RBACEnabled {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "enable RBAC on this storage first"})
			return
		}
		role := strings.TrimSpace(req.Role)
		if role == "" {
			role = model.RoleUser
		}
		if !model.ValidRole(role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid role"})
			return
		}
		if role == model.RoleViewer && req.Level != model.GrantViewer {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a viewer account can only be granted viewer access"})
			return
		}
		tempPw := randomPIN(12)
		hash, herr := local.HashPassword(tempPw)
		if herr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": herr.Error()})
			return
		}
		// The new account starts in the language PICKED for it where it was
		// invited (req.Locale) - any language this server speaks, a pack's
		// included - else the instance's (FILEX_DEFAULT_LOCALE); never the
		// composer's own screen language (#191: the form sends a language only
		// when somebody picked one). It is also the language of the welcome
		// mail below.
		loc := srvtext.Pick(req.Locale)
		newU, cerr := h.Store.CreateUser(r.Context(), email, hash, role, loc, model.TimezoneUnset)
		if cerr != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "could not create user: " + cerr.Error()})
			return
		}
		// ⚠⚠ Home the account in the CALLER's tenant, or this invite is a
		// privilege escalation rather than a convenience.
		//
		// db.Store.CreateUser hard-codes provider_id to the `default`
		// provider, and `default` is seeded IS_SUPERTENANT. A supertenant
		// scope is confine-exempt — CanAccessStorage returns true for every
		// storage — so a tenant admin inviting one address would have minted
		// an account that reads every other customer's files. The account
		// creation is gated on caller.IsAdmin(), and under multi-tenancy that
		// is the admin of any tenant. Same defect the POST /api/admin/users
		// path was fixed for (handlers/users.go, a production report, 2026-08-05); this caller was
		// missed because it does not look like user administration.
		if scope, confined := confinedScope(r.Context()); confined {
			if perr := h.Store.SetUserProvider(r.Context(), newU.ID, scope.ProviderID, ""); perr != nil {
				// Do not leave a half-created supertenant account behind.
				_ = h.Store.DeleteUser(r.Context(), newU.ID)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not home the new account in this tenant"})
				return
			}
		}
		if _, gerr := h.Store.CreateFileGrant(r.Context(), &model.FileGrant{
			StorageID: st.ID, PathPrefix: rel, IsDir: isDir, UserID: newU.ID, Level: req.Level, CreatedBy: createdBy,
		}); gerr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": gerr.Error()})
			return
		}
		loginURL := h.Tenants.FromRequest(r) + "/admin/"
		subject, body := accountCreatedText(loc, loginURL, email, tempPw)
		emailed := h.tryMail(mailer.WithLanguage(r.Context(), loc), email, subject, body)
		resp := map[string]any{"mode": "user_created", "user_id": newU.ID, "emailed": emailed}
		if !emailed {
			resp["temp_password"] = tempPw // show once so the admin can relay it
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// ── No account, no create → public share link. ──
	if h.Share == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "sharing is not enabled"})
		return
	}
	hash := pathkey.Hash(st.ID, normalizeDBPath(rel))
	node, nerr := h.Store.GetNodeByPath(r.Context(), st.ID, hash)
	if nerr != nil || node == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "item not indexed yet - open it once, then retry"})
		return
	}
	// This branch mints a PUBLIC link, which is share.links — the
	// share.users that got the caller this far is about people with accounts.
	if v := aclCanID(r.Context(), h.ACL, h.Store, st.ID, rel, perm.ShareLinks); !v.ok {
		if !v.WritePerm(w, r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		}
		return
	}
	opts := share.CreateOpts{NodeID: node.ID, CreatedBy: createdBy}
	pin := applyLinkPolicy(&opts, linkSettings(r.Context(), h.ACL), time.Now())
	sh, serr := h.Share.Create(r.Context(), opts)
	if serr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": serr.Error()})
		return
	}
	url := publicLinkOf(h.Tenants.FromRequest(r), sh)
	// Translated at the last stop: the recipient's language (recipientLang),
	// never the sender's.
	lang := recipientLang(r.Context(), h.Store, email, req.Locale)
	// A PIN the rules required goes in the mail with the link; without it the
	// recipient could not open what they were sent. ⚠ This is the one mail
	// that carries a PIN: the server made it a moment ago and nobody else has
	// it — the inviter is never shown it. Share-mail (share_mail.go) mails a
	// link that already exists and never carries one.
	size := int64(0)
	if node.Type != model.NodeTypeDirectory {
		size = node.Size
	}
	subject, body := shareMailText(lang, h.siteName(r.Context()), baseName(rel), node.Type == model.NodeTypeDirectory,
		size, url, pin, false, linkDaysLeft(sh.ExpiresAt, time.Now()))
	emailed := h.tryMail(mailer.WithLanguage(r.Context(), lang), email, subject, body)
	writeJSON(w, http.StatusOK, map[string]any{"mode": "shared", "url": url, "emailed": emailed})
}

// baseName returns the last path segment (the file/folder name).
func baseName(rel string) string {
	rel = strings.Trim(rel, "/")
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[i+1:]
	}
	return rel
}

// siteName reads the operator's configured site name (used to brand emails).
func (h *Grants) siteName(ctx context.Context) string {
	v, _ := h.Store.GetSetting(ctx, "site_name")
	return strings.TrimSpace(v)
}

// recipientLang is the language an email to one address is written in -
// translated at the last stop, for the one who reads it (#191): the language
// of the account that address belongs to (its owner chose it; an account that
// chose none reads the instance's), else the language the form that composed
// the mail sent for it (chosen), else the instance's (FILEX_DEFAULT_LOCALE,
// else English). An account's own setting always outranks the form.
func recipientLang(ctx context.Context, store interface {
	GetUserByEmail(context.Context, string) (*model.User, error)
}, email, chosen string) string {
	if store != nil {
		if u, err := store.GetUserByEmail(ctx, strings.TrimSpace(email)); err == nil && u != nil {
			return srvtext.Pick(u.Locale)
		}
	}
	return srvtext.Pick(chosen)
}
