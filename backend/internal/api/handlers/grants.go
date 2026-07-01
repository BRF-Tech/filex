package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"gitlab.com/brftech/filemanager/backend/internal/acl"
	"gitlab.com/brftech/filemanager/backend/internal/auth"
	"gitlab.com/brftech/filemanager/backend/internal/auth/drivers/local"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/mailer"
	"gitlab.com/brftech/filemanager/backend/internal/model"
	"gitlab.com/brftech/filemanager/backend/internal/share"
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
			StorageID: st.ID, PathPrefix: rel, IsDir: true, UserID: u.ID, Level: req.Level, CreatedBy: createdBy,
		}); gerr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": gerr.Error()})
			return
		}
		emailed := h.tryMail(r.Context(), email, "Bir öğe sizinle paylaşıldı",
			"Merhaba,\n\nfilex üzerinde bir klasör/dosya sizinle paylaşıldı: "+st.Name+"://"+rel+"\n\n"+h.PublicURL+"/admin/explore")
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
		newU, cerr := h.Store.CreateUser(r.Context(), email, hash, role, "en", "UTC")
		if cerr != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "could not create user: " + cerr.Error()})
			return
		}
		if _, gerr := h.Store.CreateFileGrant(r.Context(), &model.FileGrant{
			StorageID: st.ID, PathPrefix: rel, IsDir: true, UserID: newU.ID, Level: req.Level, CreatedBy: createdBy,
		}); gerr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": gerr.Error()})
			return
		}
		loginURL := h.PublicURL + "/admin/"
		emailed := h.tryMail(r.Context(), email, "filex hesabınız oluşturuldu",
			"Merhaba,\n\nSizin için bir filex hesabı oluşturuldu.\n\nGiriş: "+loginURL+"\nE-posta: "+email+"\nGeçici parola: "+tempPw+"\n\nLütfen giriş yaptıktan sonra parolanızı değiştirin.")
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
	emailed := h.tryMail(r.Context(), email, "Bir dosya sizinle paylaşıldı",
		"Merhaba,\n\nSizinle bir dosya paylaşıldı. İndirmek için:\n\n"+url)
	writeJSON(w, http.StatusOK, map[string]any{"mode": "shared", "url": url, "emailed": emailed})
}
