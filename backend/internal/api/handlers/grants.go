package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
)

// Grants is the per-file/per-folder permission-management API backing the
// explorer's right-side "İzinler" panel. It is mounted under
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
}

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
	if adapter == "" {
		storages, err := h.Store.ListEnabledStorages(r.Context())
		if err != nil || len(storages) == 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no storages"})
			return nil, "", false
		}
		adapter = storages[0].Name
	}
	st, err := h.Store.GetStorageByName(r.Context(), adapter)
	if err != nil || st == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown adapter: " + adapter})
		return nil, "", false
	}
	return st, acl.CleanRel(rel), true
}

// requireEditor reports whether the caller may write/share at (st, rel):
// admin, or acl.LevelEditor effective there. Used by the share-by-email action
// (same capability that created the link). Writes 403 + returns false if not.
func (h *Grants) requireEditor(w http.ResponseWriter, r *http.Request, st *model.Storage, rel string) bool {
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
	return true
}

// requireOwner reports whether the caller may manage permissions on (st, rel):
// admin, or acl.LevelOwner effective there. Writes 403 + returns false if not.
func (h *Grants) requireOwner(w http.ResponseWriter, r *http.Request, st *model.Storage, rel string) bool {
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
	if !h.requireOwner(w, r, st, rel) {
		return
	}
	all, err := h.Store.ListFileGrantsByStorage(r.Context(), st.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	direct := []grantView{}
	inherited := []grantView{}
	for _, g := range all {
		gp := acl.CleanRel(g.PathPrefix)
		gv := grantView{FileGrant: g}
		if u, uerr := h.Store.GetUser(r.Context(), g.UserID); uerr == nil && u != nil {
			gv.UserEmail = u.Email
			gv.UserDisplayName = u.DisplayName
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

type grantCreateReq struct {
	Path   string `json:"path"`
	UserID int64  `json:"user_id"`
	Level  string `json:"level"`
	IsDir  *bool  `json:"is_dir,omitempty"`
}

// Create (upsert) a grant for a user on a path.
//
//	POST /api/files/permissions {path, user_id, level, is_dir?}
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
	if !st.RBACEnabled {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "enable RBAC on this storage before granting per-item access"})
		return
	}
	if !h.requireOwner(w, r, st, rel) {
		return
	}
	if !model.ValidGrantLevel(req.Level) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid level"})
		return
	}
	if req.UserID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing user_id"})
		return
	}
	target, err := h.Store.GetUser(r.Context(), req.UserID)
	if err != nil || target == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	// Account-role ceiling: a viewer account may only ever hold viewer grants.
	if target.IsViewer() && req.Level != model.GrantViewer {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a viewer account can only be granted viewer access"})
		return
	}
	isDir := true
	if req.IsDir != nil {
		isDir = *req.IsDir
	}
	var createdBy *int64
	if u := auth.UserFrom(r.Context()); u != nil {
		id := u.ID
		createdBy = &id
	}
	g, err := h.Store.CreateFileGrant(r.Context(), &model.FileGrant{
		StorageID:  st.ID,
		PathPrefix: rel,
		IsDir:      isDir,
		UserID:     req.UserID,
		Level:      req.Level,
		CreatedBy:  createdBy,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, g)
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// authorizeGrant loads the grant's storage and verifies the caller may manage
// it (owner of the grant's path, or admin).
func (h *Grants) authorizeGrant(w http.ResponseWriter, r *http.Request, g *model.FileGrant) (*model.Storage, bool) {
	st, err := h.Store.GetStorage(r.Context(), g.StorageID)
	if err != nil || st == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "storage not found"})
		return nil, false
	}
	if !h.requireOwner(w, r, st, acl.CleanRel(g.PathPrefix)) {
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
	storageName := map[int64]string{}
	userEmail := map[int64]string{}
	out := make([]map[string]any, 0, len(all))
	for _, g := range all {
		if _, ok := storageName[g.StorageID]; !ok {
			if st, e := h.Store.GetStorage(r.Context(), g.StorageID); e == nil && st != nil {
				storageName[g.StorageID] = st.Name
			}
		}
		if _, ok := userEmail[g.UserID]; !ok {
			if u, e := h.Store.GetUser(r.Context(), g.UserID); e == nil && u != nil {
				userEmail[g.UserID] = u.Email
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
			"level":        g.Level,
			"created_at":   g.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": out})
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
	if err := h.Store.DeleteFileGrant(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
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
			!strings.Contains(strings.ToLower(u.DisplayName), q) {
			continue
		}
		out = append(out, map[string]any{
			"id":           u.ID,
			"email":        u.Email,
			"display_name": u.DisplayName,
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
	if err != nil || u == nil {
		writeJSON(w, http.StatusOK, map[string]any{"found": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found": true,
		"user": map[string]any{
			"id":           u.ID,
			"email":        u.Email,
			"display_name": u.DisplayName,
			"role":         u.Role,
		},
	})
}

type inviteReq struct {
	Path       string `json:"path"`
	Email      string `json:"email"`
	Level      string `json:"level"`
	CreateUser bool   `json:"create_user,omitempty"`
	Role       string `json:"role,omitempty"` // new-user role when CreateUser (default "user")
	IsDir      *bool  `json:"is_dir,omitempty"`
	Locale     string `json:"locale,omitempty"` // composer UI locale (mail language fallback)
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
	if !h.requireOwner(w, r, st, rel) {
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
	isDir := true
	if req.IsDir != nil {
		isDir = *req.IsDir
	}

	// ── Existing account → direct grant. ──
	if u, err := h.Store.GetUserByEmail(r.Context(), email); err == nil && u != nil {
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
		// Prefer the recipient's own language; fall back to the composer's.
		loc := u.Locale
		if loc == "" {
			loc = req.Locale
		}
		subject, body := itemGrantText(loc, st.Name+"://"+rel, h.PublicURL+"/admin/explore")
		emailed := h.tryMail(r.Context(), email, subject, body)
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
		// Normalize the new account's locale to tr/en from the composer's UI
		// locale (empty → en default).
		loc := "en"
		if req.Locale != "" && !mailLangEN(req.Locale) {
			loc = "tr"
		}
		newU, cerr := h.Store.CreateUser(r.Context(), email, hash, role, loc, "UTC")
		if cerr != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "could not create user: " + cerr.Error()})
			return
		}
		if _, gerr := h.Store.CreateFileGrant(r.Context(), &model.FileGrant{
			StorageID: st.ID, PathPrefix: rel, IsDir: isDir, UserID: newU.ID, Level: req.Level, CreatedBy: createdBy,
		}); gerr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": gerr.Error()})
			return
		}
		loginURL := h.PublicURL + "/admin/"
		subject, body := accountCreatedText(loc, loginURL, email, tempPw)
		emailed := h.tryMail(r.Context(), email, subject, body)
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
	hash := managerPathHash(st.ID, normalizeDBPath(rel))
	node, nerr := h.Store.GetNodeByPath(r.Context(), st.ID, hash)
	if nerr != nil || node == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "item not indexed yet — open it once, then retry"})
		return
	}
	sh, serr := h.Share.Create(r.Context(), share.CreateOpts{NodeID: node.ID, CreatedBy: createdBy})
	if serr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": serr.Error()})
		return
	}
	url := h.PublicURL + "/s/" + sh.Token
	subject, body := shareMailText(req.Locale, h.siteName(r.Context()), baseName(rel), isDir, 0, url, "", 0)
	emailed := h.tryMail(r.Context(), email, subject, body)
	writeJSON(w, http.StatusOK, map[string]any{"mode": "shared", "url": url, "emailed": emailed})
}

type shareMailReq struct {
	Path        string   `json:"path"`
	Email       string   `json:"email"`            // single recipient (back-compat)
	Emails      []string `json:"emails,omitempty"` // multiple recipients
	URL         string   `json:"url"`
	Pin         string   `json:"pin,omitempty"`
	ExpiresDays int      `json:"expires_days,omitempty"`
	Locale      string   `json:"locale,omitempty"`
	IsDir       bool     `json:"is_dir,omitempty"`
	Size        int64    `json:"size,omitempty"`
	Mode        string   `json:"mode,omitempty"` // "download" (default) | "drop"
}

// parseRecipients merges the single `email` + `emails[]` inputs, splitting each
// on commas/semicolons/whitespace/newlines, lowercasing, validating (@) and
// deduping — so one textarea of addresses or a chips array both work.
func parseRecipients(single string, list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, chunk := range append([]string{single}, list...) {
		for _, part := range strings.FieldsFunc(chunk, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
		}) {
			e := strings.ToLower(strings.TrimSpace(part))
			if e == "" || !strings.Contains(e, "@") || seen[e] {
				continue
			}
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
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

// ShareMail emails an already-created public share link to an address. It does
// NOT create a share — it delivers a link the caller just made (with their
// chosen expiry/PIN) in the share tab. Gated editor+ on the path, the same
// capability that created the link. Best-effort: returns {emailed:false} when
// SMTP isn't verified so the UI keeps showing the link for manual delivery.
//
//	POST /api/files/permissions/share-mail {path, email, url}
func (h *Grants) ShareMail(w http.ResponseWriter, r *http.Request) {
	var req shareMailReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	st, rel, ok := h.resolvePath(w, r, req.Path)
	if !ok {
		return
	}
	if !h.requireEditor(w, r, st, rel) {
		return
	}
	recipients := parseRecipients(req.Email, req.Emails)
	if len(recipients) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid email required"})
		return
	}
	link := strings.TrimSpace(req.URL)
	if link == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing url"})
		return
	}
	if h.Mailer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"emailed": false, "error": "not_configured"})
		return
	}
	// Use the composer's selected UI language (req.Locale). We intentionally do
	// NOT override with the recipient's stored locale here: a link often goes to
	// people outside the system, and the sender picks the language. A drop link
	// ("mode":"drop") is an upload invite, so it uses the upload-worded body.
	var subject, body string
	if req.Mode == model.ShareKindDrop {
		// Look the drop link's configured limits back up from the token so the
		// invite spells them out (X files, Y MB per file, allowed types).
		var maxFiles, maxSizeMB int
		var allowedExt []string
		if tok := dropTokenFromURL(link); tok != "" {
			if sh, err := h.Store.GetShareByToken(r.Context(), tok); err == nil && sh != nil && sh.IsDrop() {
				ds := parseDropSettings(sh.DropSettings)
				maxFiles, maxSizeMB, allowedExt = ds.MaxFiles, ds.MaxFileSizeMB, ds.AllowedExt
			}
		}
		subject, body = dropInviteMailText(req.Locale, h.siteName(r.Context()), baseName(rel), link, req.Pin, req.ExpiresDays, maxFiles, maxSizeMB, allowedExt)
	} else {
		subject, body = shareMailText(req.Locale, h.siteName(r.Context()), baseName(rel), req.IsDir, req.Size, link, req.Pin, req.ExpiresDays)
	}
	var sent, failed []string
	reason := ""
	for _, email := range recipients {
		if err := h.Mailer.Send(r.Context(), email, subject, body); err != nil {
			// Distinguish "SMTP not set up / not verified" (show the link) from a
			// transient send failure (worth retrying) so the UI can say which.
			reason = "send_failed"
			if errors.Is(err, mailer.ErrNotConfigured) || errors.Is(err, mailer.ErrNotVerified) {
				reason = "not_configured"
			}
			failed = append(failed, email)
			continue
		}
		sent = append(sent, email)
	}
	if len(sent) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"emailed": false, "error": reason, "failed": failed})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"emailed": true, "sent": sent, "failed": failed})
}
