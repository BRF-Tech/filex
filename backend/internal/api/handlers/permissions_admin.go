package handlers

// The admin API for per-user permissions (internal/perm): the catalogue, the
// install defaults, permission rules, and one account's overrides beside the
// effective answer — with where each answer came from.
//
// Who may call what:
//
//   - the catalogue and one account's permissions: admin.users (delegated),
//     under the same no-escalation line as the rest of the users area
//     (refuseAdminTarget), plus one more: a delegated administrator can only
//     ALLOW what they hold themselves;
//   - the defaults and the rules: administrators only. Both reach many
//     accounts at once — a rule targeting "everyone" is a change to every
//     account — so a delegated administrator could write one that grants
//     themselves anything.
//
// Every write calls perm.Invalidate, so this process answers from the new
// state at once; another filex process on the same database follows within
// perm.SnapshotTTL.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// PermissionsAdmin serves /api/admin/roles* and
// /api/admin/users/{id}/{exceptions,roles}.
type PermissionsAdmin struct {
	Store db.Store
	ACL   *acl.Resolver
}

// NewPermissionsAdmin constructs the handler.
func NewPermissionsAdmin(store db.Store, resolver *acl.Resolver) *PermissionsAdmin {
	return &PermissionsAdmin{Store: store, ACL: resolver}
}

// ── wire shapes ────────────────────────────────────────────────────────────

type permDefWire struct {
	Key          perm.Perm  `json:"key"`
	Group        perm.Group `json:"group"`
	RoleOnly     bool       `json:"role_only,omitempty"`
	ViewerCapped bool       `json:"viewer_capped,omitempty"`
}

type permPresetWire struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type permEffectiveWire struct {
	Key     perm.Perm   `json:"key"`
	Allowed bool        `json:"allowed"`
	Source  perm.Source `json:"source"`
}

// effectiveWire is one account's resolved permissions, in catalogue order.
type effectiveWire struct {
	Permissions []permEffectiveWire    `json:"permissions"`
	Allowed     []string               `json:"allowed"`
	Preset      string                 `json:"preset"`
	Settings    model.PermRuleSettings `json:"settings"`
	Rules       []int64                `json:"rules"`
	// ConditionalRules apply only on some storages or paths, so they are not
	// in the answers above; the page lists them beside the grid.
	ConditionalRules []int64 `json:"conditional_rules"`
	// AllowedInFolders is what the role allows only in some folders.
	AllowedInFolders []string `json:"allowed_in_folders"`
}

func effectiveOf(res *perm.Result) effectiveWire {
	out := effectiveWire{Allowed: res.Allowed.Strings(), Preset: perm.MatchPreset(res.Allowed), Settings: res.Settings, Rules: res.Rules,
		ConditionalRules: res.ConditionalRules(), AllowedInFolders: res.AllowedInFolders().Strings()}
	if out.Rules == nil {
		out.Rules = []int64{}
	}
	for _, d := range perm.All() {
		out.Permissions = append(out.Permissions, permEffectiveWire{Key: d.Key, Allowed: res.Can(d.Key), Source: res.Why(d.Key)})
	}
	return out
}

// ── catalogue ──────────────────────────────────────────────────────────────

// Catalogue lists every permission and preset.
//
//	GET /api/admin/roles/catalogue
func (h *PermissionsAdmin) Catalogue(w http.ResponseWriter, _ *http.Request) {
	defs := []permDefWire{}
	for _, d := range perm.All() {
		defs = append(defs, permDefWire{Key: d.Key, Group: d.Group, RoleOnly: d.RoleOnly, ViewerCapped: d.ViewerCapped})
	}
	presets := []permPresetWire{}
	for _, p := range perm.Presets() {
		presets = append(presets, permPresetWire{Name: p.Name, Permissions: p.Set.Strings()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": defs, "presets": presets})
}

// ── defaults ───────────────────────────────────────────────────────────────

type permDefaultsWire struct {
	Permissions []string `json:"permissions"`
}

// builtinRole reads ?role= — the built-in role whose permissions a defaults
// request is about: "user" (the default) or "viewer".
func builtinRole(r *http.Request) string {
	if r.URL.Query().Get("role") == model.RoleViewer {
		return model.RoleViewer
	}
	return model.RoleUser
}

// GetDefaults returns what a built-in role's accounts start from — the User
// role's, or with ?role=viewer the Viewer role's.
//
//	GET /api/admin/roles/builtin[?role=viewer]
func (h *PermissionsAdmin) GetDefaults(w http.ResponseWriter, r *http.Request) {
	s, err := perm.LoadRoleBase(r.Context(), h.Store, builtinRole(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": s.Strings(), "preset": perm.MatchPreset(s)})
}

// PutDefaults replaces the defaults.
//
//	PUT /api/admin/roles/builtin {"permissions":["files.download",…]}
func (h *PermissionsAdmin) PutDefaults(w http.ResponseWriter, r *http.Request) {
	var req permDefaultsWire
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Permissions == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json: want {\"permissions\":[…]}"})
		return
	}
	for _, k := range req.Permissions {
		if !perm.Known(perm.Perm(k)) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown permission " + strconv.Quote(k)})
			return
		}
	}
	s := perm.FromStrings(req.Permissions)
	role := builtinRole(r)
	if before, err := perm.LoadRoleBase(r.Context(), h.Store, role); err == nil {
		auth.AddAuditDetail(r.Context(), "before", before.Strings())
	}
	auth.AddAuditDetail(r.Context(), "role", role)
	auth.AddAuditDetail(r.Context(), "after", s.Strings())
	if err := perm.SaveRoleBase(r.Context(), h.Store, role, s); err != nil {
		writePermInvalid(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": s.Strings(), "preset": perm.MatchPreset(s)})
}

// ── rules ──────────────────────────────────────────────────────────────────

// visibleRule reports whether the caller's tenant may see and edit r: a
// tenant administrator sees its own tenant's rules; the supertenant (and an
// install without tenancy) sees all of them.
func visibleRule(ctx context.Context, r *model.PermissionRule) bool {
	scope, confined := confinedScope(ctx)
	if !confined {
		return true
	}
	return r.ProviderID != nil && *r.ProviderID == scope.ProviderID
}

// ListRules returns the rules the caller's tenant may see, in id order.
//
//	GET /api/admin/roles
func (h *PermissionsAdmin) ListRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.Store.ListPermissionRules(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := []*model.PermissionRule{}
	visible := map[int64]bool{}
	for _, rule := range rules {
		if visibleRule(r.Context(), rule) {
			out = append(out, rule)
			visible[rule.ID] = true
		}
	}
	members, err := h.Store.ListUserCustomRoles(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// assignments: user id → the custom role they hold, for the roles the
	// caller can see — the Users list names each person's role from it.
	assignments := map[string]int64{}
	for uid, rid := range members {
		if visible[rid] {
			assignments[strconv.FormatInt(uid, 10)] = rid
		}
	}
	// builtin_members: how many people are on each built-in role itself —
	// not on a custom role — so the Roles page need not fetch every user to
	// count them. An administrator is one whatever else they hold.
	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	builtin := map[string]int{model.RoleAdmin: 0, model.RoleUser: 0, model.RoleViewer: 0}
	for _, u := range users {
		if _, custom := assignments[strconv.FormatInt(u.ID, 10)]; custom && u.Role != model.RoleAdmin {
			continue
		}
		if _, ok := builtin[u.Role]; ok {
			builtin[u.Role]++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out, "assignments": assignments, "builtin_members": builtin})
}

// ruleFromRequest decodes, normalises and checks a rule body against the
// caller's tenant. It writes the refusal and returns nil when invalid.
func (h *PermissionsAdmin) ruleFromRequest(w http.ResponseWriter, r *http.Request) *model.PermissionRule {
	var rule model.PermissionRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return nil
	}
	if err := perm.NormalizeRule(&rule); err != nil {
		writePermInvalid(w, err)
		return nil
	}
	ctx := r.Context()
	// A tenant administrator's rule is its tenant's, whatever the body says.
	if scope, confined := confinedScope(ctx); confined {
		pid := scope.ProviderID
		rule.ProviderID = &pid
	} else if rule.ProviderID != nil {
		if p, err := h.Store.GetProvider(ctx, *rule.ProviderID); err != nil || p == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown provider_id"})
			return nil
		}
	}
	// A storage condition must name a storage the caller's tenant has.
	for _, id := range rule.Conditions.StorageIDs {
		st, err := h.Store.GetStorage(ctx, id)
		if err != nil || st == nil || !scopeOf(ctx).CanAccessStorage(id) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown storage in conditions: " + strconv.FormatInt(id, 10)})
			return nil
		}
	}
	return &rule
}

// CreateRule adds a rule.
//
//	POST /api/admin/roles {name, description, enabled, targets, effects, settings}
func (h *PermissionsAdmin) CreateRule(w http.ResponseWriter, r *http.Request) {
	rule := h.ruleFromRequest(w, r)
	if rule == nil {
		return
	}
	if u := auth.UserFrom(r.Context()); u != nil {
		id := u.ID
		rule.CreatedBy = &id
	}
	created, err := h.Store.CreatePermissionRule(r.Context(), rule)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	perm.Invalidate()
	auditRule(r.Context(), "rule", created)
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(created.ID, 10), created.Name)
	writeJSON(w, http.StatusCreated, created)
}

// ruleByID loads a rule the caller may see, answering 404 otherwise.
func (h *PermissionsAdmin) ruleByID(w http.ResponseWriter, r *http.Request) *model.PermissionRule {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return nil
	}
	rule, err := h.Store.GetPermissionRule(r.Context(), id)
	if err != nil || rule == nil || !visibleRule(r.Context(), rule) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found"})
		return nil
	}
	return rule
}

// UpdateRule replaces a rule's fields.
//
//	PUT /api/admin/roles/{id}
func (h *PermissionsAdmin) UpdateRule(w http.ResponseWriter, r *http.Request) {
	existing := h.ruleByID(w, r)
	if existing == nil {
		return
	}
	rule := h.ruleFromRequest(w, r)
	if rule == nil {
		return
	}
	rule.ID = existing.ID
	auditRule(r.Context(), "before", existing)
	auditRule(r.Context(), "after", rule)
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(rule.ID, 10), rule.Name)
	if err := h.Store.UpdatePermissionRule(r.Context(), rule); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// What the role allows decides its people's built-in role (User when it
	// can change or share anything, Viewer otherwise): keep them in step.
	if err := h.syncHolders(r.Context(), rule); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	perm.Invalidate()
	updated, err := h.Store.GetPermissionRule(r.Context(), rule.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// DeleteRule removes a role. A role people hold is deleted only when the
// caller says what those people become — ?to=user, ?to=viewer or ?to=<role
// id> — so deleting a role that takes things away never quietly hands its
// people the whole built-in User role. Without it: 409 with the count.
//
//	DELETE /api/admin/roles/{id}[?to=user|viewer|<id>]
func (h *PermissionsAdmin) DeleteRule(w http.ResponseWriter, r *http.Request) {
	rule := h.ruleByID(w, r)
	if rule == nil {
		return
	}
	ctx := r.Context()
	members, err := h.Store.ListUserCustomRoles(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var holders []int64
	for uid, rid := range members {
		if rid == rule.ID {
			holders = append(holders, uid)
		}
	}
	if len(holders) > 0 {
		to := r.URL.Query().Get("to")
		if to == "" {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "people hold this role: say what they become (?to=user, ?to=viewer or ?to=<role id>)",
				"holders": len(holders),
			})
			return
		}
		if err := h.moveHolders(ctx, rule, holders, to); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		auth.AddAuditDetail(ctx, "moved_to", to)
		auth.AddAuditDetail(ctx, "holders", len(holders))
	}
	auditRule(r.Context(), "rule", rule)
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(rule.ID, 10), rule.Name)
	if err := h.Store.DeletePermissionRule(r.Context(), rule.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	perm.Invalidate()
	w.WriteHeader(http.StatusNoContent)
}

// ── one account ────────────────────────────────────────────────────────────

// userForPermissions resolves {id} within the caller's tenant.
func (h *PermissionsAdmin) userForPermissions(w http.ResponseWriter, r *http.Request) *model.User {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return nil
	}
	if !ownsUser(w, r, h.Store, id, "user") {
		return nil
	}
	u, err := h.Store.GetUser(r.Context(), id)
	if err != nil || u == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return nil
	}
	return u
}

func (h *PermissionsAdmin) writeUserPermissions(w http.ResponseWriter, r *http.Request, u *model.User) {
	overrides, err := h.Store.GetUserPermissionOverrides(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	res, err := h.ACL.Perms(r.Context(), u)
	if err != nil || res == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "permissions could not be resolved"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":   u.ID,
		"role":      u.Role,
		"overrides": overrides,
		"effective": effectiveOf(res),
	})
}

// GetUserPermissions returns an account's overrides and effective permissions.
//
//	GET /api/admin/users/{id}/exceptions
func (h *PermissionsAdmin) GetUserPermissions(w http.ResponseWriter, r *http.Request) {
	u := h.userForPermissions(w, r)
	if u == nil {
		return
	}
	h.writeUserPermissions(w, r, u)
}

type userPermissionsReq struct {
	Overrides map[string]string `json:"overrides"`
}

// PutUserPermissions replaces an account's overrides ({} clears them).
//
//	PUT /api/admin/users/{id}/exceptions {"overrides":{"files.delete":"deny"}}
func (h *PermissionsAdmin) PutUserPermissions(w http.ResponseWriter, r *http.Request) {
	target := h.userForPermissions(w, r)
	if target == nil {
		return
	}
	var req userPermissionsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Overrides == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json: want {\"overrides\":{…}}"})
		return
	}
	if err := perm.ValidateEffects(req.Overrides); err != nil {
		writePermInvalid(w, err)
		return
	}
	if refuseAdminTarget(w, r, target, "") {
		return
	}
	// A delegated administrator can take anything away but only hand out
	// what they hold: otherwise admin.users alone is every permission, by
	// allowing it to a second account they control.
	if !callerIsFullAdmin(r.Context()) {
		caller := auth.UserFrom(r.Context())
		mine, err := h.ACL.Perms(r.Context(), caller)
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		for k, eff := range req.Overrides {
			if eff == model.PermAllow && mine != nil && !mine.Can(perm.Perm(k)) {
				writeJSON(w, http.StatusForbidden, map[string]string{
					"error":      "you cannot allow a permission you do not hold",
					"permission": k,
				})
				return
			}
		}
	}
	var by *int64
	if u := auth.UserFrom(r.Context()); u != nil {
		id := u.ID
		by = &id
	}
	// The audit row says exactly what changed on whom.
	if before, err := h.Store.GetUserPermissionOverrides(r.Context(), target.ID); err == nil {
		auth.AddAuditDetail(r.Context(), "before", before)
	}
	auth.AddAuditDetail(r.Context(), "after", req.Overrides)
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(target.ID, 10), target.Email)
	if err := h.Store.SetUserPermissionOverrides(r.Context(), target.ID, req.Overrides, by); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	perm.Invalidate()
	h.writeUserPermissions(w, r, target)
}

// writePermInvalid answers a perm.ErrInvalid (or anything else) as a 400.
func writePermInvalid(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

// ── the caller's own ───────────────────────────────────────────────────────

// MyPermissions returns the caller's own effective permissions — what the
// file manager reads to hide the buttons that would only answer 403.
//
//	GET /api/auth/me/permissions
func (h *PermissionsAdmin) MyPermissions(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	res, err := h.ACL.Perms(r.Context(), u)
	if err != nil || res == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "permissions could not be resolved"})
		return
	}
	writeJSON(w, http.StatusOK, effectiveOf(res))
}

// ListOverrides returns every account that has overrides, keyed by user id,
// so the Users list can mark "Custom" rows without a request per row. A
// tenant administrator sees its own tenant's accounts only.
//
//	GET /api/admin/roles/exceptions → {"overrides":{"12":{"files.delete":"deny"}}}
func (h *PermissionsAdmin) ListOverrides(w http.ResponseWriter, r *http.Request) {
	all, err := h.Store.ListUserPermissionOverrides(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	scope, confined := confinedScope(r.Context())
	out := map[string]map[string]string{}
	for id, m := range all {
		if confined {
			u, err := h.Store.GetUser(r.Context(), id)
			if err != nil || u == nil || u.ProviderID == nil || *u.ProviderID != scope.ProviderID {
				continue
			}
		}
		out[strconv.FormatInt(id, 10)] = m
	}
	writeJSON(w, http.StatusOK, map[string]any{"overrides": out})
}

// auditRule attaches a rule's substance to the request's audit row under key
// — enough to read later what it did, without the timestamps.
func auditRule(ctx context.Context, key string, r *model.PermissionRule) {
	auth.AddAuditDetail(ctx, key, map[string]any{
		"name":       r.Name,
		"enabled":    r.Enabled,
		"targets":    r.Targets,
		"effects":    r.Effects,
		"settings":   r.Settings,
		"conditions": r.Conditions,
	})
}

// ── one account's custom roles ─────────────────────────────────────────────
//
// A custom role is a permission rule; an account holds it when the rule
// names the account among its targets. These two routes are that membership
// seen from the account — what the Users page edits — so assigning a role
// never needs the role editor.

// syncHolders sets every holder of rule to the built-in role it implies
// (perm.HolderRole). Administrators never hold a custom role (PutUserRoles
// refuses them), but one promoted since keeps admin: a role never demotes
// anybody.
func (h *PermissionsAdmin) syncHolders(ctx context.Context, rule *model.PermissionRule) error {
	want := perm.RoleHolder(rule)
	members, err := h.Store.ListUserCustomRoles(ctx)
	if err != nil {
		return err
	}
	for uid, rid := range members {
		if rid != rule.ID {
			continue
		}
		u, err := h.Store.GetUser(ctx, uid)
		if err != nil || u == nil || u.IsAdmin() || u.Role == want {
			continue
		}
		if err := h.Store.UpdateUserRole(ctx, uid, want); err != nil {
			return err
		}
	}
	return nil
}

// GetUserRoles returns the one custom role the account holds, or null.
//
//	GET /api/admin/users/{id}/roles → {"role_id": 3} | {"role_id": null}
func (h *PermissionsAdmin) GetUserRoles(w http.ResponseWriter, r *http.Request) {
	u := h.userForPermissions(w, r)
	if u == nil {
		return
	}
	id, err := h.Store.GetUserCustomRole(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if id != 0 {
		if rule, err := h.Store.GetPermissionRule(r.Context(), id); err != nil || rule == nil || !visibleRule(r.Context(), rule) {
			id = 0
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"role_id": nullableID(id)})
}

func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

type userRoleReq struct {
	// RoleID gives a custom role (null takes it away)…
	RoleID *int64 `json:"role_id"`
	// …or Role picks a built-in one ("admin" | "user" | "viewer"), which ends
	// the custom role. One of the two.
	Role string `json:"role"`
}

// PutUserRoles sets the account's ONE role, in one call: a custom role
// (role_id), no custom role (role_id null), or a built-in role (role). A
// custom role also sets the built-in level it implies (perm.HolderRole: User
// when it can change or share anything, Viewer otherwise); a built-in role
// ends the custom one. An administrator given a custom role stops being one
// — never the last administrator. A delegated administrator never touches an
// administrator, never lifts an account above their own role, and gives only
// roles that allow nothing they do not hold (refuseAdminTarget and below).
//
//	PUT /api/admin/users/{id}/roles {"role_id": 3} | {"role_id": null} | {"role": "viewer"}
func (h *PermissionsAdmin) PutUserRoles(w http.ResponseWriter, r *http.Request) {
	target := h.userForPermissions(w, r)
	if target == nil {
		return
	}
	var req userRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Role != "" && req.RoleID != nil) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `bad json: want {"role_id": n | null} or {"role": "admin" | "user" | "viewer"}`})
		return
	}
	ctx := r.Context()
	var rule *model.PermissionRule
	var roleSet perm.Set
	newRole := ""
	switch {
	case req.Role != "":
		if !model.ValidRole(req.Role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid role: " + req.Role})
			return
		}
		newRole = req.Role
	case req.RoleID != nil:
		got, err := h.Store.GetPermissionRule(ctx, *req.RoleID)
		if err != nil || got == nil || !visibleRule(ctx, got) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown role: " + strconv.FormatInt(*req.RoleID, 10)})
			return
		}
		roleSet = perm.RoleSet(got)
		rule, newRole = got, perm.HolderRole(got, roleSet)
	}
	if refuseAdminTarget(w, r, target, newRole) {
		return
	}
	if target.IsAdmin() && newRole != "" && newRole != model.RoleAdmin {
		if last, err := isLastAdmin(ctx, h.Store, target.ID); err == nil && last {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot demote the last admin"})
			return
		}
	}
	if rule != nil && !callerIsFullAdmin(ctx) {
		mine, err := h.ACL.Perms(ctx, auth.UserFrom(ctx))
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		// Everything the role allows — its list, and its folder part's
		// allows — must be something the caller holds.
		allowed := roleSet.Keys()
		for k, eff := range rule.Effects {
			if eff == model.PermAllow {
				allowed = append(allowed, perm.Perm(k))
			}
		}
		for _, p := range allowed {
			if !mine.Can(p) {
				writeJSON(w, http.StatusForbidden, map[string]string{
					"error":      "you cannot give a role that allows a permission you do not hold",
					"role":       rule.Name,
					"permission": string(p),
				})
				return
			}
		}
	}

	before, err := h.Store.GetUserCustomRole(ctx, target.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var after int64
	if rule != nil {
		after = rule.ID
	}
	if newRole != "" && target.Role != newRole {
		if err := h.Store.UpdateUserRole(ctx, target.ID, newRole); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := h.Store.SetUserCustomRole(ctx, target.ID, after); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	finalRole := target.Role
	if newRole != "" {
		finalRole = newRole
	}
	auth.AddAuditDetail(ctx, "before", map[string]any{"role": target.Role, "role_id": nullableID(before)})
	auth.AddAuditDetail(ctx, "after", map[string]any{"role": finalRole, "role_id": nullableID(after)})
	auth.SetAuditTarget(ctx, strconv.FormatInt(target.ID, 10), target.Email)
	perm.Invalidate()
	writeJSON(w, http.StatusOK, map[string]any{"role_id": nullableID(after), "role": finalRole})
}

// moveHolders gives every holder of a role about to be deleted the role
// "to" names: a built-in one ("user" | "viewer") or another custom role (its
// id). Administrators never hold a custom role and are left alone.
func (h *PermissionsAdmin) moveHolders(ctx context.Context, from *model.PermissionRule, holders []int64, to string) error {
	var next *model.PermissionRule
	level := to
	if to != model.RoleUser && to != model.RoleViewer {
		id, err := strconv.ParseInt(to, 10, 64)
		if err != nil || id == from.ID {
			return errors.New(`"to" must be user, viewer or another role's id`)
		}
		got, err := h.Store.GetPermissionRule(ctx, id)
		if err != nil || got == nil || !visibleRule(ctx, got) {
			return errors.New("unknown role: " + to)
		}
		level = perm.RoleHolder(got)
		next = got
	}
	for _, uid := range holders {
		u, err := h.Store.GetUser(ctx, uid)
		if err != nil || u == nil || u.IsAdmin() {
			continue
		}
		if u.Role != level {
			if err := h.Store.UpdateUserRole(ctx, uid, level); err != nil {
				return err
			}
		}
		var id int64
		if next != nil {
			id = next.ID
		}
		if err := h.Store.SetUserCustomRole(ctx, uid, id); err != nil {
			return err
		}
	}
	return nil
}
