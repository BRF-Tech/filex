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
	"github.com/brf-tech/filex/backend/internal/group"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// PermissionsAdmin serves /api/admin/roles* and
// /api/admin/users/{id}/{exceptions,roles}.
type PermissionsAdmin struct {
	Store db.Store
	ACL   *acl.Resolver
	// AppPermissions lists the installed apps' user permissions
	// (wasmplugin.Registry.UserPermissions); nil when apps are off.
	AppPermissions func() []wasmplugin.UserPermRow
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
	// Apps are the installed apps' user permissions for this account
	// (perm/app.go), on an administrator's exceptions answer only.
	Apps []appEffectiveWire `json:"apps,omitempty"`
}

// appEffectiveWire is one app permission's answer for one account, and the
// answer without their own exception (Inherited) — what the exceptions
// editor's "Default" choice stands for.
type appEffectiveWire struct {
	Key       string           `json:"key"`
	Allowed   bool             `json:"allowed"`
	Source    perm.Source      `json:"source"`
	Inherited appInheritedWire `json:"inherited"`
}

type appInheritedWire struct {
	Allowed bool        `json:"allowed"`
	Source  perm.Source `json:"source"`
}

// appEffectiveOf answers every installed app permission for res.
func (h *PermissionsAdmin) appEffectiveOf(res *perm.Result) []appEffectiveWire {
	if h.AppPermissions == nil {
		return nil
	}
	out := []appEffectiveWire{}
	for _, row := range h.AppPermissions() {
		def := perm.AppDefault(row.Default)
		ok, src := res.AppAllowed(row.Key, def)
		iok, isrc := res.AppInherited(row.Key, def)
		out = append(out, appEffectiveWire{Key: row.Key, Allowed: ok, Source: src, Inherited: appInheritedWire{Allowed: iok, Source: isrc}})
	}
	return out
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
	apps := []appPermDefWire{}
	if h.AppPermissions != nil {
		for _, row := range h.AppPermissions() {
			apps = append(apps, appPermDefWire{UserPermRow: row, DefaultFor: perm.AppDefaultFor(perm.AppDefault(row.Default))})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": defs, "presets": presets, "apps": apps})
}

// appPermDefWire is one installed app permission on the catalogue, with
// what its default comes to on each built-in role when nobody has decided it
// (perm.AppDefaultFor): {"viewer": false, "user": true, "admin": true}. The
// role editors label "Default" from it; the rule stays the server's.
type appPermDefWire struct {
	wasmplugin.UserPermRow
	DefaultFor map[string]bool `json:"default_for"`
}

// ── defaults ───────────────────────────────────────────────────────────────

type permDefaultsWire struct {
	Permissions []string `json:"permissions"`
	// Apps are the built-in role's decisions about app permissions
	// (app.<app>.<id> → allow | deny, perm/app.go). Absent leaves them as
	// they are; {} clears them back to each app's default.
	Apps map[string]string `json:"apps,omitempty"`
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
	apps := map[string]string{}
	if all, err := perm.LoadAppDecisions(r.Context(), h.Store); err == nil && all[builtinRole(r)] != nil {
		apps = all[builtinRole(r)]
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": s.Strings(), "preset": perm.MatchPreset(s), "apps": apps})
}

// builtinRolesAreInstanceWide is what a tenant admin reads when refused.
const builtinRolesAreInstanceWide = "the built-in User and Viewer roles apply to every tenant and are managed by the platform operator; a tenant's own roles are custom roles"

// PutDefaults replaces the defaults.
//
//	PUT /api/admin/roles/builtin {"permissions":["files.download",…]}
//
// ⚠ Supertenant-only in multi-tenant mode. Each built-in role is ONE
// instance-wide row (perm.SaveRoleBase), held by every account of every
// tenant that has no custom role — so a tenant admin writing it would narrow
// or widen every other tenant's people, up to making them all delegated
// administrators (admin.* with a session). A tenant's own roles are custom
// roles, which carry its provider_id (CreateRule). Reading stays open.
func (h *PermissionsAdmin) PutDefaults(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, builtinRolesAreInstanceWide) {
		return
	}
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
	// Both halves are checked before either is written: a bad app decision
	// must not leave the permission list saved and the answer a 400. And a
	// request that is wrong is wrong whoever sends it — 400 before the
	// session question, as CreateRule answers.
	if err := perm.ValidateRoleBase(role, s); err != nil {
		writePermInvalid(w, err)
		return
	}
	if req.Apps != nil {
		if err := perm.ValidateAppDecisions(req.Apps); err != nil {
			writePermInvalid(w, err)
			return
		}
	}
	// Putting an admin-area permission into a built-in role makes every
	// holder of that role a delegated administrator at once (#120).
	if allowsAdministration(req.Permissions, nil) && !adminCredentialBySession(w, r, "Giving a built-in role administration rights") {
		return
	}
	if before, err := perm.LoadRoleBase(r.Context(), h.Store, role); err == nil {
		auth.AddAuditDetail(r.Context(), "before", before.Strings())
	}
	auth.AddAuditDetail(r.Context(), "role", role)
	auth.AddAuditDetail(r.Context(), "after", s.Strings())
	if err := perm.SaveRoleBase(r.Context(), h.Store, role, s); err != nil {
		writePermInvalid(w, err)
		return
	}
	apps := map[string]string{}
	if req.Apps != nil {
		if err := perm.SaveAppDecisions(r.Context(), h.Store, role, req.Apps); err != nil {
			writePermInvalid(w, err)
			return
		}
		auth.AddAuditDetail(r.Context(), "apps", req.Apps)
		apps = req.Apps
	} else if all, err := perm.LoadAppDecisions(r.Context(), h.Store); err == nil && all[role] != nil {
		apps = all[role]
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": s.Strings(), "preset": perm.MatchPreset(s), "apps": apps})
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
	// group_assignments: user id → the role a group gives them, for people
	// with none of their own (group.EffectiveRoles). They hold that role, not
	// the built-in one, so they are not counted on it below.
	vias, err := group.EffectiveRoles(r.Context(), h.Store, users)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	groupAssignments := map[string]group.Via{}
	for uid, v := range vias {
		if visible[v.RoleID] {
			groupAssignments[strconv.FormatInt(uid, 10)] = v
		}
	}
	builtin := map[string]int{model.RoleAdmin: 0, model.RoleUser: 0, model.RoleViewer: 0}
	for _, u := range users {
		if _, custom := assignments[strconv.FormatInt(u.ID, 10)]; custom && u.Role != model.RoleAdmin {
			continue
		}
		if _, viaGroup := vias[u.ID]; viaGroup {
			continue
		}
		if _, ok := builtin[u.Role]; ok {
			builtin[u.Role]++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out, "assignments": assignments, "group_assignments": groupAssignments, "builtin_members": builtin})
}

// ruleFromRequest decodes, normalises and checks a rule body against the
// caller's tenant; prev is the role it replaces (nil for a new one). It
// writes the refusal and returns nil when invalid.
func (h *PermissionsAdmin) ruleFromRequest(w http.ResponseWriter, r *http.Request, prev *model.PermissionRule) *model.PermissionRule {
	var rule model.PermissionRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return nil
	}
	if err := perm.NormalizeRuleEdit(&rule, prev); err != nil {
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

// CreateRule adds a rule. names / descriptions are the role in other
// interface languages ({"tr": "…"}); name stays required and is the fallback.
//
//	POST /api/admin/roles {name, description, names, descriptions, enabled, permissions, targets, effects, settings, conditions}
func (h *PermissionsAdmin) CreateRule(w http.ResponseWriter, r *http.Request) {
	rule := h.ruleFromRequest(w, r, nil)
	if rule == nil {
		return
	}
	if allowsAdministration(rule.Permissions, rule.Effects) && !adminCredentialBySession(w, r, "A role with administration rights") {
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
	rule := h.ruleFromRequest(w, r, existing)
	if rule == nil {
		return
	}
	// Adding an admin-area permission to a role hands it to every holder at
	// once (#120); a role that already had it and keeps it is no new grant.
	if allowsAdministration(rule.Permissions, rule.Effects) && !allowsAdministration(existing.Permissions, existing.Effects) &&
		!adminCredentialBySession(w, r, "Giving a role administration rights") {
		return
	}
	rule.ID = existing.ID
	// A role moved to a tenant reaches only that tenant's people and groups
	// (perm ignores it elsewhere): holders of another tenant would drop to
	// their built-in role unseen — the quiet widening DeleteRule's "to"
	// refuses. Refuse the move while any hold it.
	if rule.ProviderID != nil && !sameProvider(rule.ProviderID, existing.ProviderID) {
		if msg, err := h.holdersOutside(r.Context(), existing.ID, *rule.ProviderID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		} else if msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
	}
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

// PreviewRole answers what a custom role being edited comes to before it is
// saved: the built-in role its people would be on, "user" or "viewer"
// (perm.HolderRole). The role editor asks it for each app permission's
// "Default", so the rule lives here alone. The body is a role body as the
// editor has it — permissions, effects, conditions; the rest is ignored —
// possibly unfinished: nothing is checked or stored.
//
//	POST /api/admin/roles/preview {permissions, effects, conditions} → {holder_role}
func (h *PermissionsAdmin) PreviewRole(w http.ResponseWriter, r *http.Request) {
	var rule model.PermissionRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"holder_role": perm.PreviewHolderRole(rule)})
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
	// Groups holding it count too: deleting it would leave their members
	// on whatever built-in role is underneath, the same quiet widening.
	groups, err := group.HoldingRole(ctx, h.Store, rule.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(holders) > 0 || len(groups) > 0 {
		to := r.URL.Query().Get("to")
		if to == "" {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "people or groups hold this role: say what they become (?to=user, ?to=viewer or ?to=<role id>)",
				"holders": len(holders),
				"groups":  len(groups),
			})
			return
		}
		// Before anything moves: a role of another tenant would reach none of
		// them (perm ignores a role out of its holder's tenant), leaving them
		// on the built-in User role — the quiet widening "to" exists to stop.
		if err := h.checkMoveTarget(ctx, to, holders, groups); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// Moving the holders into a custom role that allows an admin-area
		// permission GIVES them that role — the same act PutUserRoles asks a
		// session for, so it asks too (#120), or deleting a role would be
		// the key's way around it.
		if id, err := strconv.ParseInt(to, 10, 64); err == nil {
			next, err := h.Store.GetPermissionRule(ctx, id)
			if err == nil && next != nil && visibleRule(ctx, next) && allowsAdministration(next.Permissions, next.Effects) &&
				!adminCredentialBySession(w, r, "Moving people into a role with administration rights") {
				return
			}
		}
		if err := h.moveHolders(ctx, rule, holders, to); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := h.moveGroups(ctx, rule, groups, to); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		auth.AddAuditDetail(ctx, "moved_to", to)
		auth.AddAuditDetail(ctx, "holders", len(holders))
		auth.AddAuditDetail(ctx, "groups", len(groups))
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
	eff := effectiveOf(res)
	eff.Apps = h.appEffectiveOf(res)
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":   u.ID,
		"role":      u.Role,
		"overrides": overrides,
		"effective": eff,
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
	// Allowing an admin-area permission makes the account a delegated
	// administrator: a session only, like creating an administrator (#120).
	if allowsAdministration(nil, req.Overrides) && !adminCredentialBySession(w, r, "Granting administration rights to an account") {
		return
	}
	if refuseAdminTarget(w, r, target, "") {
		return
	}
	// App permissions (perm/app.go) are an administrator's to change: a
	// delegated administrator cannot tell what an app key would grant (its
	// default lives in the app), so "only what you hold" cannot be judged —
	// they may leave the account's app decisions exactly as they are.
	if !callerIsFullAdmin(r.Context()) {
		before, _ := h.Store.GetUserPermissionOverrides(r.Context(), target.ID)
		if !sameAppKeys(before, req.Overrides) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an administrator changes app permissions"})
			return
		}
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
	// …and, whatever the request names, may leave the account holding
	// nothing they do not hold: lifting a Deny hands the permission back
	// without naming it (refuseGain).
	if refuseGain(w, r, h.ACL, target, func(in *perm.Input) { in.Overrides = req.Overrides }) {
		return
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
// — enough to read later what it did, without the timestamps. The name is
// the role's own; its translations ride along only when it has some.
func auditRule(ctx context.Context, key string, r *model.PermissionRule) {
	detail := map[string]any{
		"name":       r.Name,
		"enabled":    r.Enabled,
		"targets":    r.Targets,
		"effects":    r.Effects,
		"settings":   r.Settings,
		"conditions": r.Conditions,
	}
	if len(r.Names) > 0 {
		detail["names"] = r.Names
	}
	if len(r.Descriptions) > 0 {
		detail["descriptions"] = r.Descriptions
	}
	auth.AddAuditDetail(ctx, key, detail)
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
	// And the people who hold it through a group (and none of their own).
	groups, err := group.HoldingRole(ctx, h.Store, rule.ID)
	if err != nil {
		return err
	}
	ids, err := group.MemberIDs(ctx, h.Store, groups...)
	if err != nil {
		return err
	}
	return group.SyncLevels(ctx, h.Store, ids)
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
	out := map[string]any{"role_id": nullableID(id), "group_role": nil}
	if id == 0 {
		out["group_role"] = h.groupRoleOf(r.Context(), u)
	}
	writeJSON(w, http.StatusOK, out)
}

// groupRoleOf is the role a person's groups give them (perm.EffectiveRole)
// — nil when none does, or it is not one the caller's tenant can see. The
// person's page shows it and says where it comes from.
func (h *PermissionsAdmin) groupRoleOf(ctx context.Context, u *model.User) *group.Via {
	vias, err := group.EffectiveRoles(ctx, h.Store, []*model.User{u})
	if err != nil {
		return nil
	}
	v, ok := vias[u.ID]
	if !ok {
		return nil
	}
	if rule, err := h.Store.GetPermissionRule(ctx, v.RoleID); err != nil || rule == nil || !visibleRule(ctx, rule) {
		return nil
	}
	return &v
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
		// A role binds its own tenant's accounts only (perm.Resolve treats a
		// held role of another tenant as switched off), so giving one across
		// tenants is refused rather than stored as a silent no-op.
		if got.ProviderID != nil && (target.ProviderID == nil || *target.ProviderID != *got.ProviderID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this role belongs to another tenant than the account"})
			return
		}
		roleSet = perm.RoleSet(got)
		rule, newRole = got, perm.HolderRole(got, roleSet)
	}
	// An administrator's credential is handed out in a session only (#120,
	// session_gate.go): making an account an administrator, changing an
	// administrator's role, or giving a custom role that allows an admin-area
	// permission. An unchanged role is not a role change.
	switch {
	case newRole == model.RoleAdmin && !target.IsAdmin():
		if !adminCredentialBySession(w, r, "Promoting an account to administrator") {
			return
		}
	case target.IsAdmin() && newRole != "" && newRole != model.RoleAdmin:
		if !adminCredentialBySession(w, r, "Changing an administrator's role") {
			return
		}
	case rule != nil && allowsAdministration(rule.Permissions, rule.Effects):
		if !adminCredentialBySession(w, r, "Giving a role with administration rights") {
			return
		}
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
	if rule != nil && refuseRoleBeyondCaller(w, r, h.ACL, rule) {
		return
	}

	// Judged by the result as well: a built-in role, or no custom role, can
	// hand out what a restrictive custom role took away (refuseGain).
	var nextRoleID int64
	if rule != nil {
		nextRoleID = rule.ID
	}
	if refuseGain(w, r, h.ACL, target, func(in *perm.Input) {
		if newRole != "" {
			in.Role = newRole
		}
		in.CustomRoleID = nextRoleID
	}) {
		return
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
	// Whatever was picked here — a role of their own, administrator, or a
	// built-in level — is the level the account has now, not one a group's
	// role moved: forget the one kept from before a group's role, or leaving
	// the group later would restore it over this choice. (If a group's role
	// still applies, SyncLevels below moves the level again and keeps THIS
	// one as the level to give back.) Only when the call changes something:
	// the same role sent again is no choice, and forgetting the kept level
	// then would leave the account on the group role's level for good.
	if before != after || (newRole != "" && newRole != target.Role) {
		if err := h.Store.DeleteUserGroupLevel(ctx, target.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	// With no custom role of their own now, a group may give them one — and
	// with it the level underneath.
	if after == 0 {
		if err := group.SyncLevels(ctx, h.Store, []int64{target.ID}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	finalRole := target.Role
	if newRole != "" {
		finalRole = newRole
	}
	if after == 0 {
		if u, err := h.Store.GetUser(ctx, target.ID); err == nil && u != nil {
			finalRole = u.Role
		}
	}
	auth.AddAuditDetail(ctx, "before", map[string]any{"role": target.Role, "role_id": nullableID(before)})
	auth.AddAuditDetail(ctx, "after", map[string]any{"role": finalRole, "role_id": nullableID(after)})
	auth.SetAuditTarget(ctx, strconv.FormatInt(target.ID, 10), target.Email)
	perm.Invalidate()
	// ⚠ With no role of their own, a group's role still decides — a built-in
	// role picked here does not override it, and its level wins. Say so, so
	// the page can tell the person instead of reporting what was asked.
	resp := map[string]any{"role_id": nullableID(after), "role": finalRole, "group_role": nil}
	if after == 0 {
		if u, err := h.Store.GetUser(ctx, target.ID); err == nil && u != nil {
			resp["group_role"] = h.groupRoleOf(ctx, u)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// refuseRoleBeyondCaller is the delegated administrator's line for giving a
// role — to a person, or to a group and so to its members: everything the
// role allows (its list, and its folder part's allows) must be something the
// caller holds. It writes the 403 and reports true when refused. A full
// administrator is never refused.
func refuseRoleBeyondCaller(w http.ResponseWriter, r *http.Request, resolver *acl.Resolver, rule *model.PermissionRule) bool {
	ctx := r.Context()
	if callerIsFullAdmin(ctx) {
		return false
	}
	mine, err := resolver.Perms(ctx, auth.UserFrom(ctx))
	if err != nil || mine == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return true
	}
	allowed := perm.RoleSet(rule).Keys()
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
			return true
		}
	}
	return false
}

// holdersOutside names why role roleID cannot become tenant providerID's: a
// group or a person of another tenant holds it. "" when none does.
func (h *PermissionsAdmin) holdersOutside(ctx context.Context, roleID, providerID int64) (string, error) {
	groups, err := group.HoldingRole(ctx, h.Store, roleID)
	if err != nil {
		return "", err
	}
	for _, gid := range groups {
		g, err := h.Store.GetGroup(ctx, gid)
		if err != nil {
			return "", err
		}
		if g.ProviderID == nil || *g.ProviderID != providerID {
			return "the group " + strconv.Quote(g.Name) + " of another tenant holds this role: give it another role first", nil
		}
	}
	members, err := h.Store.ListUserCustomRoles(ctx)
	if err != nil {
		return "", err
	}
	for uid, rid := range members {
		if rid != roleID {
			continue
		}
		u, err := h.Store.GetUser(ctx, uid)
		if err != nil || u == nil || u.IsAdmin() {
			continue
		}
		if u.ProviderID == nil || *u.ProviderID != providerID {
			return "people of another tenant hold this role: give them another role first", nil
		}
	}
	return "", nil
}

// checkMoveTarget refuses a "to" role that some holder of the deleted role —
// a person or a group — could not hold: a tenant's role reaches only that
// tenant's people and groups. A built-in "to" fits everyone.
func (h *PermissionsAdmin) checkMoveTarget(ctx context.Context, to string, holders, groups []int64) error {
	if to == model.RoleUser || to == model.RoleViewer {
		return nil
	}
	id, err := strconv.ParseInt(to, 10, 64)
	if err != nil {
		return errors.New(`"to" must be user, viewer or another role's id`)
	}
	next, err := h.Store.GetPermissionRule(ctx, id)
	if err != nil || next == nil || !visibleRule(ctx, next) {
		return errors.New("unknown role: " + to)
	}
	if next.ProviderID == nil {
		return nil
	}
	for _, gid := range groups {
		g, err := h.Store.GetGroup(ctx, gid)
		if err != nil {
			return err
		}
		if !sameProvider(g.ProviderID, next.ProviderID) {
			return errors.New("the role " + strconv.Quote(next.Name) + " belongs to another tenant than the group " + strconv.Quote(g.Name))
		}
	}
	for _, uid := range holders {
		u, err := h.Store.GetUser(ctx, uid)
		if err != nil || u == nil {
			continue
		}
		if !u.IsAdmin() && !sameProvider(u.ProviderID, next.ProviderID) {
			return errors.New("the role " + strconv.Quote(next.Name) + " belongs to another tenant than some of the people who hold this one")
		}
	}
	return nil
}

// moveGroups gives every group holding a role about to be deleted the role
// "to" names — another custom role (its id) — or none for a built-in one, and
// brings their members' levels along.
func (h *PermissionsAdmin) moveGroups(ctx context.Context, from *model.PermissionRule, groups []int64, to string) error {
	if len(groups) == 0 {
		return nil
	}
	var next int64
	if to != model.RoleUser && to != model.RoleViewer {
		// moveHolders has checked it names another visible role.
		next, _ = strconv.ParseInt(to, 10, 64)
	}
	ids, err := group.MemberIDs(ctx, h.Store, groups...)
	if err != nil {
		return err
	}
	// Whose role the deleted one really is — before it goes. A member a
	// higher-priority group gives another role to, or with a role of their
	// own, is not moved by this: their level and the one kept for them stay.
	members := make([]*model.User, 0, len(ids))
	for _, uid := range ids {
		if u, err := h.Store.GetUser(ctx, uid); err == nil && u != nil {
			members = append(members, u)
		}
	}
	vias, err := group.EffectiveRoles(ctx, h.Store, members)
	if err != nil {
		return err
	}
	if err := h.Store.ReassignGroupRole(ctx, from.ID, next); err != nil {
		return err
	}
	// A built-in "to" is the level those members are left on: the role was
	// what set it, and without one nothing else will. It is theirs now, so
	// the level kept from before a group's role is forgotten (SyncLevels
	// below would restore it over the administrator's choice; a member
	// another group still gives a role to is moved again, keeping this one
	// as the level to give back).
	if next == 0 {
		for _, u := range members {
			if v, ok := vias[u.ID]; !ok || v.RoleID != from.ID {
				continue
			}
			if u.Role != to {
				if err := h.Store.UpdateUserRole(ctx, u.ID, to); err != nil {
					return err
				}
			}
			if err := h.Store.DeleteUserGroupLevel(ctx, u.ID); err != nil {
				return err
			}
		}
	}
	return group.SyncLevels(ctx, h.Store, ids)
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
		// The level set here is theirs, not one a group's role moved.
		if err := h.Store.DeleteUserGroupLevel(ctx, uid); err != nil {
			return err
		}
	}
	// Moved to a built-in role, a holder with none of their own now gets a
	// group's role if one of their groups has one — and its level.
	if next == nil {
		return group.SyncLevels(ctx, h.Store, holders)
	}
	return nil
}

// sameAppKeys reports whether a and b say the same about every app permission
// key (perm.IsAppKey) — what a delegated administrator must leave unchanged.
func sameAppKeys(a, b map[string]string) bool {
	for k, v := range a {
		if perm.IsAppKey(k) && b[k] != v {
			return false
		}
	}
	for k, v := range b {
		if perm.IsAppKey(k) && a[k] != v {
			return false
		}
	}
	return true
}
