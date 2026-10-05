package handlers

// The admin API for groups of people (internal/group, docs/GROUPS.md):
// /api/admin/groups*, and one account's groups at /api/admin/users/{id}/groups.
//
// Who may call what: all of it is admin.users — a group is a way of managing
// people — under the users area's no-escalation line, plus three more for a
// delegated administrator:
//
//   - a role given to a group reaches every member, so it is held to the
//     same line as giving one to a person (refuseRoleBeyondCaller);
//   - they never change their OWN membership: a group can carry folder
//     grants and a role, and putting themselves in one would hand them both
//     — nor delete a group they are in, or change its role or priority,
//     which would change their own role just the same (refuseOwnGroup);
//   - they never change a group's links to an outside directory, which
//     decide membership at sign-in — for them too.
//
// Tenancy: a group belongs to one tenant (provider_id) or, made by the
// supertenant or on an install without tenants, to the whole install. A
// tenant administrator sees and changes only its own tenant's groups, and a
// group's members, role and grants must all be of its tenant.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/group"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// GroupsAdmin serves /api/admin/groups* and /api/admin/users/{id}/groups.
type GroupsAdmin struct {
	Store db.Store
	ACL   *acl.Resolver
}

// NewGroupsAdmin constructs the handler.
func NewGroupsAdmin(store db.Store, resolver *acl.Resolver) *GroupsAdmin {
	return &GroupsAdmin{Store: store, ACL: resolver}
}

// visibleGroup reports whether the caller's tenant may see and change g: a
// tenant administrator its own tenant's groups; the supertenant (and an
// install without tenancy) all of them.
func visibleGroup(ctx context.Context, g *model.Group) bool {
	scope, confined := confinedScope(ctx)
	if !confined {
		return true
	}
	return g.ProviderID != nil && *g.ProviderID == scope.ProviderID
}

// sameTenant reports whether an account may be in g: any account for an
// install-wide group, only the group's own tenant's otherwise.
func sameTenant(g *model.Group, u *model.User) bool {
	return perm.GroupInScope(g, u.ProviderID)
}

// ── wire shapes ────────────────────────────────────────────────────────────

type groupWire struct {
	*model.Group
	MemberCount int `json:"member_count"`
	GrantCount  int `json:"grant_count"`
}

type groupMemberWire struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Source string `json:"source"`
	// AuthSource is where the account comes from (model.AuthSource*).
	AuthSource string `json:"auth_source"`
	AddedAt    string `json:"added_at"`
}

type groupGrantWire struct {
	ID          int64  `json:"id"`
	StorageID   int64  `json:"storage_id"`
	StorageName string `json:"storage_name"`
	PathPrefix  string `json:"path_prefix"`
	Path        string `json:"path"`
	IsDir       bool   `json:"is_dir"`
	Level       string `json:"level"`
}

type groupReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	RoleID      *int64 `json:"role_id"`
	// GivesAdmin makes the members administrators instead of giving a role
	// (migration 00086). Absent keeps the group's (off for a new one).
	GivesAdmin *bool `json:"gives_admin"`
	// Priority decides whose role a member in several groups gets: the
	// highest first. Absent keeps the group's (0 for a new one).
	Priority *int              `json:"priority"`
	Links    []model.GroupLink `json:"links"`
	// ProviderID is read on create only, and only from a caller that is not
	// confined to a tenant — a tenant administrator's group is its tenant's.
	ProviderID *int64 `json:"provider_id,omitempty"`
}

// ── list / read ────────────────────────────────────────────────────────────

// List returns the groups the caller's tenant may see, in id order, each
// with how many members and folder grants it has.
//
//	GET /api/admin/groups
func (h *GroupsAdmin) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	groups, err := h.Store.ListGroups(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	members, err := h.Store.ListAllGroupMembers(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	grants, err := h.Store.ListAllGroupFileGrants(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	memberCount := map[int64]int{}
	for _, m := range members {
		memberCount[m.GroupID]++
	}
	grantCount := map[int64]int{}
	for _, g := range grants {
		grantCount[g.GroupID]++
	}
	out := []groupWire{}
	for _, g := range groups {
		if visibleGroup(ctx, g) {
			out = append(out, groupWire{Group: g, MemberCount: memberCount[g.ID], GrantCount: grantCount[g.ID]})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

// Memberships answers every person's groups the caller may see, keyed by
// user id — the Users list's Groups column in one call instead of one per
// row. Each carries the membership's source (added, SSO, LDAP).
func (h *GroupsAdmin) Memberships(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	groups, err := h.Store.ListGroups(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	members, err := h.Store.ListAllGroupMembers(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	grants, err := h.Store.ListAllGroupFileGrants(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	withFolders := map[int64]bool{}
	for _, gr := range grants {
		withFolders[gr.GroupID] = true
	}
	byID := map[int64]*model.Group{}
	for _, g := range groups {
		if visibleGroup(ctx, g) {
			byID[g.ID] = g
		}
	}
	// InUse: the group gives its members a role or folder access — the
	// groups that change what the person can do, listed first.
	type row struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Source string `json:"source"`
		InUse  bool   `json:"in_use,omitempty"`
	}
	out := map[string][]row{}
	for _, m := range members {
		if g := byID[m.GroupID]; g != nil {
			k := strconv.FormatInt(m.UserID, 10)
			out[k] = append(out[k], row{ID: g.ID, Name: g.Name, Source: m.Source, InUse: g.RoleID != nil || withFolders[g.ID]})
		}
	}
	for _, rows := range out {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].InUse != rows[j].InUse {
				return rows[i].InUse
			}
			return rows[i].Name < rows[j].Name
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"memberships": out})
}

// groupByID loads a group the caller may see, answering 404 otherwise.
func (h *GroupsAdmin) groupByID(w http.ResponseWriter, r *http.Request) *model.Group {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return nil
	}
	g, err := h.Store.GetGroup(r.Context(), id)
	if err != nil || g == nil || !visibleGroup(r.Context(), g) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
		return nil
	}
	return g
}

// Get returns one group with its members and its folder grants.
//
//	GET /api/admin/groups/{id}
func (h *GroupsAdmin) Get(w http.ResponseWriter, r *http.Request) {
	g := h.groupByID(w, r)
	if g == nil {
		return
	}
	h.writeGroup(w, r, http.StatusOK, g)
}

func (h *GroupsAdmin) writeGroup(w http.ResponseWriter, r *http.Request, status int, g *model.Group) {
	ctx := r.Context()
	ms, err := h.Store.ListGroupMembers(ctx, g.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	members := make([]groupMemberWire, 0, len(ms))
	for _, m := range ms {
		u, err := h.Store.GetUser(ctx, m.UserID)
		if err != nil || u == nil || !sameTenant(g, u) {
			continue
		}
		members = append(members, groupMemberWire{
			UserID: u.ID, Email: u.Email, Name: u.Label(), Role: u.Role,
			Source: m.Source, AuthSource: u.AuthSource, AddedAt: m.AddedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	sort.SliceStable(members, func(i, j int) bool { return members[i].Name < members[j].Name })
	gs, err := h.Store.ListGroupFileGrantsByGroup(ctx, g.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	grants := make([]groupGrantWire, 0, len(gs))
	names := map[int64]string{}
	for _, gr := range gs {
		// A group's grants are on its tenant's storages; the check is here
		// anyway so a stray row never names a foreign storage.
		if !ownsStorageQuiet(r, gr.StorageID) {
			continue
		}
		if _, ok := names[gr.StorageID]; !ok {
			if st, e := h.Store.GetStorage(ctx, gr.StorageID); e == nil && st != nil {
				names[gr.StorageID] = st.Name
			}
		}
		grants = append(grants, groupGrantWire{
			ID: gr.ID, StorageID: gr.StorageID, StorageName: names[gr.StorageID],
			PathPrefix: gr.PathPrefix, Path: names[gr.StorageID] + "://" + gr.PathPrefix,
			IsDir: gr.IsDir, Level: gr.Level,
		})
	}
	writeJSON(w, status, map[string]any{"group": g, "members": members, "grants": grants})
}

// ── create / update / delete ───────────────────────────────────────────────

// groupFromRequest decodes and checks a group body. existing is nil on
// create. It writes the refusal and returns nil when the body is refused.
func (h *GroupsAdmin) groupFromRequest(w http.ResponseWriter, r *http.Request, existing *model.Group) *model.Group {
	var req groupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return nil
	}
	ctx := r.Context()
	g := &model.Group{Name: req.Name, Description: req.Description, RoleID: req.RoleID, Links: req.Links}
	if req.Priority != nil {
		g.Priority = *req.Priority
	} else if existing != nil {
		g.Priority = existing.Priority
	}
	if req.GivesAdmin != nil {
		g.GivesAdmin = *req.GivesAdmin
	} else if existing != nil {
		g.GivesAdmin = existing.GivesAdmin
	}
	if g.GivesAdmin && g.RoleID != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a group that makes its members administrators gives no other role"})
		return nil
	}
	if g.GivesAdmin {
		// By name, any directory's group of that name would make
		// administrators (group.adminLinkCounts).
		for _, l := range g.Links {
			if l.Kind == model.GroupLinkLDAP && !group.IsLDAPDN(l.Value) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a group that makes its members administrators names its LDAP group by its full DN (cn=…,ou=…,dc=…), not by name: " + l.Value})
				return nil
			}
		}
	}
	if err := group.Normalize(g); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return nil
	}
	if existing != nil {
		g.ID, g.ProviderID, g.CreatedBy = existing.ID, existing.ProviderID, existing.CreatedBy
	} else if scope, confined := confinedScope(ctx); confined {
		// A tenant administrator's group is its tenant's, whatever the body says.
		pid := scope.ProviderID
		g.ProviderID = &pid
	} else if req.ProviderID != nil {
		if p, err := h.Store.GetProvider(ctx, *req.ProviderID); err != nil || p == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown provider_id"})
			return nil
		}
		g.ProviderID = req.ProviderID
	}

	// The same name twice in one tenant would be two groups nobody can tell
	// apart in a picker.
	all, err := h.Store.ListGroups(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return nil
	}
	for _, o := range all {
		if o.ID != g.ID && sameProvider(o.ProviderID, g.ProviderID) && o.Name == g.Name {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "a group with this name already exists"})
			return nil
		}
	}

	// The role: one the caller's tenant can see, of the group's own tenant —
	// an install-wide role, or the group's tenant's. Giving it to the group
	// gives it to every member, so the delegated line applies.
	roleChanged := !sameID(g.RoleID, roleOf(existing))
	if g.RoleID != nil {
		rule, err := h.Store.GetPermissionRule(ctx, *g.RoleID)
		// Checked only when the role CHANGES: a tenant's group may hold an
		// install-wide role the supertenant gave it, which its tenant's
		// admin cannot see (visibleRule) — keeping it must not stop them
		// renaming the group.
		if err != nil || rule == nil || (roleChanged && !visibleRule(ctx, rule)) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown role: " + strconv.FormatInt(*g.RoleID, 10)})
			return nil
		}
		if roleChanged && rule.ProviderID != nil && !sameProvider(rule.ProviderID, g.ProviderID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the role belongs to another tenant than the group"})
			return nil
		}
		if roleChanged && refuseRoleBeyondCaller(w, r, h.ACL, rule) {
			return nil
		}
		// A role with administration rights makes every member a delegated
		// administrator. Giving it to the group — or reaching more people
		// with it, through a new priority or SSO links — asks for a signed-in
		// session, as giving it to one person does (#120).
		reaches := roleChanged || g.Priority != priorityOf(existing) || !sameLinks(g.Links, linksOf(existing))
		if reaches && allowsAdministration(rule.Permissions, rule.Effects) &&
			!adminCredentialBySession(w, r, "Giving a group a role with administration rights") {
			return nil
		}
	}

	// Administrator through a group: only a full administrator makes a
	// group give it, or changes anything about a group that does — who it
	// reaches is who administers filex. Making it reach anyone new (turning
	// it on, new links) asks for a signed-in session, as promoting one
	// person does.
	if g.GivesAdmin || (existing != nil && existing.GivesAdmin) {
		if !callerIsFullAdmin(ctx) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an administrator can change a group that makes its members administrators"})
			return nil
		}
		reaches := existing == nil || !existing.GivesAdmin || !sameLinks(g.Links, existing.Links)
		if g.GivesAdmin && reaches && !adminCredentialBySession(w, r, "Making a group's members administrators") {
			return nil
		}
	}

	// Links decide who is a member at sign-in — the caller included — so
	// only a full administrator changes them.
	if !callerIsFullAdmin(ctx) && !sameLinks(g.Links, linksOf(existing)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an administrator can link a group to an outside directory"})
		return nil
	}
	// Priority decides WHICH of a member's groups gives them their role —
	// raising one hands its role to people in a weaker one, a role the
	// caller may not hold. Only a full administrator orders roles.
	if !callerIsFullAdmin(ctx) && g.Priority != priorityOf(existing) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an administrator can change a group's role priority"})
		return nil
	}
	return g
}

func priorityOf(g *model.Group) int {
	if g == nil {
		return 0
	}
	return g.Priority
}

// Create adds a group.
//
//	POST /api/admin/groups {name, description, role_id, links, provider_id?}
func (h *GroupsAdmin) Create(w http.ResponseWriter, r *http.Request) {
	g := h.groupFromRequest(w, r, nil)
	if g == nil {
		return
	}
	ctx := r.Context()
	if u := auth.UserFrom(ctx); u != nil {
		id := u.ID
		g.CreatedBy = &id
	}
	created, err := h.Store.CreateGroup(ctx, g)
	if err != nil {
		writeGroupStoreError(w, err)
		return
	}
	// Linked from the start: whoever's last sign-in carried a linked SSO
	// group is in it now, not at their next sign-in.
	if len(created.Links) > 0 && !h.syncLinks(w, r) {
		return
	}
	auditGroup(ctx, "group", created)
	auth.SetAuditTarget(ctx, strconv.FormatInt(created.ID, 10), created.Name)
	h.writeGroup(w, r, http.StatusCreated, created)
}

// Update replaces a group's name, description, role and links. Its tenant
// never changes.
//
//	PUT /api/admin/groups/{id}
func (h *GroupsAdmin) Update(w http.ResponseWriter, r *http.Request) {
	existing := h.groupByID(w, r)
	if existing == nil {
		return
	}
	g := h.groupFromRequest(w, r, existing)
	if g == nil {
		return
	}
	ctx := r.Context()
	// A group directory sync brought in: its LDAP link is the directory's
	// (sync keeps it on the directory group's current DN) — what the body
	// says about LDAP links is not taken.
	if existing.Synced() {
		links := []model.GroupLink{}
		for _, l := range g.Links {
			if l.Kind != model.GroupLinkLDAP {
				links = append(links, l)
			}
		}
		for _, l := range existing.Links {
			if l.Kind == model.GroupLinkLDAP {
				links = append(links, l)
			}
		}
		g.Links = links
	}
	// Which role the group gives — and so which of their groups decides —
	// is the caller's own role if they are in it.
	if !sameID(g.RoleID, existing.RoleID) || g.Priority != existing.Priority || g.GivesAdmin != existing.GivesAdmin {
		if h.refuseOwnGroup(w, r, existing) {
			return
		}
		ids, err := group.MemberIDs(ctx, h.Store, existing.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if h.refuseGroupGain(w, r, existing, ids, g) {
			return
		}
	}
	auditGroup(ctx, "before", existing)
	auditGroup(ctx, "after", g)
	auth.SetAuditTarget(ctx, strconv.FormatInt(g.ID, 10), g.Name)
	if err := h.Store.UpdateGroup(ctx, g); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
			return
		}
		writeGroupStoreError(w, err)
		return
	}
	if !sameLinks(g.Links, existing.Links) && !h.syncLinks(w, r) {
		return
	}
	if !sameID(g.RoleID, existing.RoleID) || g.Priority != existing.Priority || g.GivesAdmin != existing.GivesAdmin {
		// The members' role — and with it their level underneath — follows.
		if !h.syncMembers(w, r, g.ID) {
			return
		}
	}
	perm.Invalidate()
	updated, err := h.Store.GetGroup(ctx, g.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.writeGroup(w, r, http.StatusOK, updated)
}

// Delete removes a group, its memberships and its folder grants. Its
// members keep their level underneath; one with another group holding a
// role gets that group's role.
//
//	DELETE /api/admin/groups/{id}
func (h *GroupsAdmin) Delete(w http.ResponseWriter, r *http.Request) {
	g := h.groupByID(w, r)
	if g == nil {
		return
	}
	ctx := r.Context()
	if g.GivesAdmin && !callerIsFullAdmin(ctx) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an administrator can delete a group that makes its members administrators"})
		return
	}
	if h.refuseOwnGroup(w, r, g) {
		return
	}
	ids, err := group.MemberIDs(ctx, h.Store, g.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.refuseGroupGain(w, r, g, ids, nil) {
		return
	}
	auditGroup(ctx, "group", g)
	auth.AddAuditDetail(ctx, "members", len(ids))
	auth.SetAuditTarget(ctx, strconv.FormatInt(g.ID, 10), g.Name)
	if err := h.Store.DeleteGroup(ctx, g.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := group.SyncLevels(ctx, h.Store, ids); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	perm.Invalidate()
	w.WriteHeader(http.StatusNoContent)
}

// Detach keeps a group whose directory group is gone as an ordinary filex
// group: it is no longer the directory's, its dead LDAP link goes, and its
// folders, role and hand-added members stay. Only for a group flagged
// removed — one the directory still has is the directory's, and which
// directory groups exist is managed on the directory. An administrator's:
// links are.
//
//	POST /api/admin/groups/{id}/detach
func (h *GroupsAdmin) Detach(w http.ResponseWriter, r *http.Request) {
	g := h.groupByID(w, r)
	if g == nil {
		return
	}
	if !callerIsFullAdmin(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an administrator can change where a group comes from"})
		return
	}
	if !sessionOnly(w, r, "Keeping a removed directory group as a filex group needs an administrator signed in to the admin panel; an API key cannot do it.", nil) {
		return
	}
	if g.DirectoryID == "" || g.DirectoryState != model.GroupDirectoryRemoved {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "only a group whose directory group was removed can be kept as a filex group"})
		return
	}
	ctx := r.Context()
	auditGroup(ctx, "group", g)
	auth.SetAuditTarget(ctx, strconv.FormatInt(g.ID, 10), g.Name)
	links := []model.GroupLink{}
	for _, l := range g.Links {
		if l.Kind != model.GroupLinkLDAP {
			links = append(links, l)
		}
	}
	g.Links = links
	if err := h.Store.UpdateGroup(ctx, g); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := h.Store.SetGroupDirectory(ctx, g.ID, "", "", ""); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	fresh, err := h.Store.GetGroup(ctx, g.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.writeGroup(w, r, http.StatusOK, fresh)
}

// ── members ────────────────────────────────────────────────────────────────

type groupMembersReq struct {
	UserIDs []int64 `json:"user_ids"`
}

// AddMembers puts people in a group by hand. A person already in it through
// a link becomes a manual member — they stay whatever the directory says.
//
//	POST /api/admin/groups/{id}/members {"user_ids":[3,4]}
func (h *GroupsAdmin) AddMembers(w http.ResponseWriter, r *http.Request) {
	g := h.groupByID(w, r)
	if g == nil {
		return
	}
	var req groupMembersReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.UserIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `bad json: want {"user_ids":[…]}`})
		return
	}
	ctx := r.Context()
	for _, uid := range req.UserIDs {
		if !h.memberAllowed(w, r, g, uid) {
			return
		}
	}
	// Joining gives each of them the group's role (if they have none of
	// their own) — as good as giving it to them on their page.
	if g.GivesAdmin && !adminCredentialBySession(w, r, "Adding people to a group that makes its members administrators") {
		return
	}
	if g.RoleID != nil {
		rule, err := h.Store.GetPermissionRule(ctx, *g.RoleID)
		if err == nil && rule != nil && refuseRoleBeyondCaller(w, r, h.ACL, rule) {
			return
		}
		if err == nil && rule != nil && allowsAdministration(rule.Permissions, rule.Effects) &&
			!adminCredentialBySession(w, r, "Adding people to a group whose role has administration rights") {
			return
		}
	}
	// And by the result: joining can also END a stricter role they had
	// through a lower group.
	if h.refuseGroupGain(w, r, g, req.UserIDs, g) {
		return
	}
	for _, uid := range req.UserIDs {
		if err := h.Store.AddGroupMember(ctx, g.ID, uid); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := group.SyncLevels(ctx, h.Store, req.UserIDs); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	perm.Invalidate()
	auth.AddAuditDetail(ctx, "user_ids", req.UserIDs)
	auth.SetAuditTarget(ctx, strconv.FormatInt(g.ID, 10), g.Name)
	h.writeGroup(w, r, http.StatusOK, g)
}

// RemoveMember takes a person out of a group, however they came to be in it.
// One who is in it through a link joins again at their next sign-in if the
// directory still says so.
//
//	DELETE /api/admin/groups/{id}/members/{user_id}
func (h *GroupsAdmin) RemoveMember(w http.ResponseWriter, r *http.Request) {
	g := h.groupByID(w, r)
	if g == nil {
		return
	}
	uid, err := strconv.ParseInt(chi.URLParam(r, "user_id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad user id"})
		return
	}
	if !h.memberAllowed(w, r, g, uid) {
		return
	}
	// Leaving can end a restrictive role and hand them their built-in one.
	if h.refuseGroupGain(w, r, g, []int64{uid}, nil) {
		return
	}
	ctx := r.Context()
	if err := h.Store.RemoveGroupMember(ctx, g.ID, uid); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := group.SyncLevels(ctx, h.Store, []int64{uid}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	perm.Invalidate()
	auth.AddAuditDetail(ctx, "user_id", uid)
	auth.SetAuditTarget(ctx, strconv.FormatInt(g.ID, 10), g.Name)
	h.writeGroup(w, r, http.StatusOK, g)
}

// memberAllowed checks that the caller may change whether uid is in g: an
// account of the caller's tenant and of the group's, and — for a delegated
// administrator — not themselves.
func (h *GroupsAdmin) memberAllowed(w http.ResponseWriter, r *http.Request, g *model.Group, uid int64) bool {
	ctx := r.Context()
	u, err := h.Store.GetUser(ctx, uid)
	if err != nil || u == nil || !userInTenant(ctx, u) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found: " + strconv.FormatInt(uid, 10)})
		return false
	}
	if !sameTenant(g, u) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this person belongs to another tenant than the group"})
		return false
	}
	if callerIsFullAdmin(ctx) {
		return true
	}
	if g.GivesAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an administrator can change who is in a group that makes its members administrators"})
		return false
	}
	if caller := auth.UserFrom(ctx); caller != nil && caller.ID == uid {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "you cannot change which groups you are in"})
		return false
	}
	return true
}

// refuseOwnGroup is the delegated administrator's line for a group they are
// in: deleting it, or changing its role or priority, changes their OWN role
// and folder access — the same as changing their own membership, which they
// never do. It writes the 403 and reports true when refused. A full
// administrator is never refused.
func (h *GroupsAdmin) refuseOwnGroup(w http.ResponseWriter, r *http.Request, g *model.Group) bool {
	ctx := r.Context()
	if callerIsFullAdmin(ctx) {
		return false
	}
	caller := auth.UserFrom(ctx)
	if caller == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return true
	}
	ms, err := h.Store.ListUserGroupMemberships(ctx, caller.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return true
	}
	for _, m := range ms {
		if m.GroupID == g.ID {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "you cannot delete a group you are in, or change its role or priority"})
			return true
		}
	}
	return false
}

// refuseGroupGain is the delegated administrator's result check (refuseGain)
// for a change to a group: members joining, leaving, the group deleted, its
// role or priority changed. Each member is judged by what they would hold
// afterwards — the role their groups then give them (none: their built-in
// role, at the level kept from before a group's role moved it) — so taking
// someone out of a restrictive group, or clearing its role, cannot hand them
// the built-in User role's permissions the caller does not hold. after is the
// group as it will be (nil: gone, or they left it). It writes the 403 and
// reports true when refused. A full administrator is never refused.
func (h *GroupsAdmin) refuseGroupGain(w http.ResponseWriter, r *http.Request, g *model.Group, members []int64, after *model.Group) bool {
	ctx := r.Context()
	if callerIsFullAdmin(ctx) || len(members) == 0 {
		return false
	}
	for _, uid := range members {
		u, err := h.Store.GetUser(ctx, uid)
		if err != nil || u == nil {
			continue
		}
		change, err := groupsPreview(ctx, h.Store, u, u.ProviderID, func(groups []*model.Group) []*model.Group {
			out := make([]*model.Group, 0, len(groups)+1)
			for _, x := range groups {
				if x.ID != g.ID {
					out = append(out, x)
				}
			}
			if after != nil {
				out = append(out, after)
			}
			return out
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return true
		}
		if refuseGain(w, r, h.ACL, u, change) {
			return true
		}
	}
	return false
}

// groupsPreview is the perm.Loader.Preview change for an account whose groups
// change — pick maps the groups it is in now to the ones it will be in — and
// whose tenant becomes providerID: the role it then holds is picked from
// those groups, and its built-in level is the one group.SyncLevels will set
// (the group role's, or with none the level kept from before a group's role
// moved it). An account with a role of its own, or an administrator, keeps
// its level.
func groupsPreview(ctx context.Context, store db.Store, u *model.User, providerID *int64, pick func([]*model.Group) []*model.Group) (func(*perm.Input), error) {
	rules, err := store.ListPermissionRules(ctx)
	if err != nil {
		return nil, err
	}
	own, err := store.GetUserCustomRole(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	kept, hasKept, err := store.GetUserGroupLevel(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return func(in *perm.Input) {
		in.ProviderID = providerID
		in.Groups = pick(in.Groups)
		if own != 0 || u.IsAdmin() {
			return
		}
		roleID, _ := perm.EffectiveRole(0, in.Groups, rules, providerID)
		var groupRole *model.PermissionRule
		for _, rule := range rules {
			if rule.ID == roleID {
				groupRole = rule
				break
			}
		}
		if !hasKept {
			kept = ""
		}
		in.Role = perm.LevelUnderGroups(in.Role, groupRole, kept)
	}, nil
}

// syncMembers brings every member's level in step with the group's role.
func (h *GroupsAdmin) syncMembers(w http.ResponseWriter, r *http.Request, groupID int64) bool {
	ids, err := group.MemberIDs(r.Context(), h.Store, groupID)
	if err == nil {
		err = group.SyncLevels(r.Context(), h.Store, ids)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return false
	}
	return true
}

// syncLinks re-applies the SSO- and LDAP-linked memberships of every account the
// caller's tenant has, from the groups each one's last sign-in carried — so
// a changed link takes effect now (group.SyncStored).
func (h *GroupsAdmin) syncLinks(w http.ResponseWriter, r *http.Request) bool {
	users, err := h.Store.ListUsers(r.Context())
	if err == nil {
		err = group.SyncStored(r.Context(), h.Store, users)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return false
	}
	perm.Invalidate()
	return true
}

// writeGroupStoreError answers a failed group write: a name the tenant
// already has (the database's UNIQUE(tenant_key, name) — two saves at once
// that both passed the check above) is a 409 like the check's own.
func writeGroupStoreError(w http.ResponseWriter, err error) {
	if isUniqueViolation(err) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a group with this name already exists"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

// ── one account ────────────────────────────────────────────────────────────

// UserGroups returns the groups one account is in, and how.
//
//	GET /api/admin/users/{id}/groups → {"groups":[{id, name, role_id, source}]}
func (h *GroupsAdmin) UserGroups(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if !ownsUser(w, r, h.Store, id, "user") {
		return
	}
	ctx := r.Context()
	ms, err := h.Store.ListUserGroupMemberships(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := []map[string]any{}
	for _, m := range ms {
		g, err := h.Store.GetGroup(ctx, m.GroupID)
		if err != nil || g == nil || !visibleGroup(ctx, g) {
			continue
		}
		out = append(out, map[string]any{"id": g.ID, "name": g.Name, "role_id": g.RoleID, "source": m.Source})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

// ── helpers ────────────────────────────────────────────────────────────────

func auditGroup(ctx context.Context, key string, g *model.Group) {
	auth.AddAuditDetail(ctx, key, map[string]any{
		"name":        g.Name,
		"role_id":     g.RoleID,
		"gives_admin": g.GivesAdmin,
		"links":       g.Links,
	})
}

func sameProvider(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func sameID(a, b *int64) bool { return sameProvider(a, b) }

func roleOf(g *model.Group) *int64 {
	if g == nil {
		return nil
	}
	return g.RoleID
}

func linksOf(g *model.Group) []model.GroupLink {
	if g == nil {
		return nil
	}
	return g.Links
}

// sameLinks compares two link lists as sets.
func sameLinks(a, b []model.GroupLink) bool {
	if len(a) != len(b) {
		return false
	}
	in := map[model.GroupLink]bool{}
	for _, l := range a {
		in[l] = true
	}
	for _, l := range b {
		if !in[l] {
			return false
		}
	}
	return true
}
