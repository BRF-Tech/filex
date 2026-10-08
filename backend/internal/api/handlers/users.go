package handlers

import (
	"context"
	"encoding/json"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/group"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// Users handles /api/admin/users.
type Users struct {
	Store db.Store
	// ACL resolves per-user permissions, for the delegated administrator's
	// lines (refuseGain, refuseTakeover). Unset, a delegated administrator is
	// refused those changes; a full administrator never needs it.
	ACL *acl.Resolver
	// Mailer and Tenants send a new account its invitation (send_invite):
	// the address to sign in at and a first password. No mailer, or a send
	// that fails: the password comes back once for the administrator.
	Mailer  *mailer.Service
	Tenants tenanturl.Resolver
	// DirectoryFor names the LDAP directory that owns an address among the
	// ones that sign people in to a tenant (0: the platform's own;
	// authsetup.Live.DirectoryFor); nil on a harness with none.
	DirectoryFor func(email string, tenant int64) (name, label string, ok bool)
}

// NewUsers constructs a Users handler.
func NewUsers(store db.Store) *Users { return &Users{Store: store} }

// List returns all users.
func (h *Users) List(w http.ResponseWriter, r *http.Request) {
	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// ?q= narrows by e-mail, display name and username (labelMatches), on
	// the server: the page used to filter with the browser's own rules.
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		kept := users[:0]
		for _, u := range users {
			if u != nil && labelMatches(q, u.Email, u.DisplayName, u.Username) {
				kept = append(kept, u)
			}
		}
		users = kept
	}
	writeJSON(w, http.StatusOK, users)
}

// tenantGate resolves the target user and reports whether the caller may act
// on it, returning an HTTP status + message when it may not.
//
// Only LIST was tenant-confined: ListUsers goes through tenantstore, but
// tenantstore wraps exactly three methods (ListStorages, ListEnabledStorages,
// ListUsers) — GetUser and every mutation take a raw id. So a tenant admin
// could read, rename, re-password, disable and DELETE another tenant's users
// by id, including that tenant's last admin. Same class as the /dav leak
// (H4), and the reason this gate exists (a production report's follow-up, 2026-08-05).
//
// Out-of-tenant answers 404, not 403: a foreign id must be indistinguishable
// from one that does not exist, the same no-exists-oracle rule /dav and the
// grant path already follow.
func (h *Users) tenantGate(ctx context.Context, id int64) (*model.User, int, string) {
	u, err := h.Store.GetUser(ctx, id)
	if err != nil || u == nil {
		return nil, http.StatusNotFound, "not found"
	}
	scope, scoped := tenant.FromContext(ctx)
	if !scoped || scope.IsSupertenant {
		return u, 0, ""
	}
	// A user with no provider is a bootstrap/legacy account; it belongs to no
	// tenant, so a confined caller must not reach it either.
	if u.ProviderID == nil || *u.ProviderID != scope.ProviderID {
		return nil, http.StatusNotFound, "not found"
	}
	return u, 0, ""
}

// Get returns a single user by id. The admin UI's UserEdit page
// hits this when the row is clicked; without it chi returned 405
// (only PATCH/DELETE were wired).
func (h *Users) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	u, status, msg := h.tenantGate(r.Context(), id)
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type userCreateReq struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Locale      string `json:"locale"`
	Timezone    string `json:"timezone"`
	// ProviderID homes the new user in a tenant. Optional; when absent the
	// caller's own tenant is used.
	ProviderID *int64 `json:"provider_id,omitempty"`
	// Username is the login name (SFTP, FTP…); empty derives one from the
	// address, as every other account gets.
	Username string `json:"username,omitempty"`
	// SendInvite makes a first password and e-mails it with the address to
	// sign in at; Password is then ignored.
	SendInvite bool `json:"send_invite,omitempty"`
	// AllowDirectoryEmail makes the account although an LDAP directory owns
	// the address (it is refused with directory_email otherwise).
	AllowDirectoryEmail bool `json:"allow_directory_email,omitempty"`
}

// userCreated is a new account and, when it was invited, how: e-mailed, or
// its first password shown once because no mail could go out.
type userCreated struct {
	*model.User
	Invite *userInvite `json:"invite,omitempty"`
}

type userInvite struct {
	Emailed      bool   `json:"emailed"`
	TempPassword string `json:"temp_password,omitempty"`
}

// resolveProvider decides which provider a created/updated user belongs to
// and whether the caller may put them there. It returns the provider id to
// write (0 = leave the store's own default alone), or an HTTP status + message.
//
// Store.CreateUser defaults provider_id to 1, and provider 1 (`default`) is
// the SUPERTENANT — so "unspecified" used to mean "confine-exempt, sees every
// tenant's storages". A tenant admin's new users therefore default to that
// admin's own tenant, and only a supertenant caller may name an arbitrary one
// (a production report, 2026-08-05).
func (h *Users) resolveProvider(ctx context.Context, requested *int64) (int64, int, string) {
	scope, scoped := tenant.FromContext(ctx)
	confined := scoped && !scope.IsSupertenant

	var target int64
	if confined {
		target = scope.ProviderID
	}
	if requested != nil {
		if confined && *requested != scope.ProviderID {
			return 0, http.StatusForbidden, "cannot assign a user to another tenant"
		}
		target = *requested
	}
	if target == 0 {
		return 0, 0, "" // single-tenant / unscoped and nothing asked for
	}
	// provider_id carries no foreign key, so an unchecked value would strand
	// the user in a tenant that does not exist.
	if p, err := h.Store.GetProvider(ctx, target); err != nil || p == nil {
		return 0, http.StatusBadRequest, "unknown provider_id"
	}
	return target, 0, ""
}

// Create makes a new user.
func (h *Users) Create(w http.ResponseWriter, r *http.Request) {
	var req userCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	// The password is optional (issue #25): an account added ahead of its
	// first SSO sign-in has none, and an empty hash is refused by every
	// password check (local login, recovery login, /dav, SFTP, FTP).
	//
	// ⚠ The address is checked the way the profile checks it
	// (account_rules.go): missing, malformed and already taken are three
	// different sentences in the reader's language. "email required" in
	// English, behind the dialog's backdrop, was the whole answer before
	// (release-candidate sweep, 2026-09-21), and a taken address surfaced as
	// the database driver's unique-constraint text with a 500.
	if p := emailProblem(r.Context(), h.Store, req.Email, 0); p != nil {
		p.write(w, r)
		return
	}
	username := ""
	if strings.TrimSpace(req.Username) != "" {
		username = identity.Normalize(req.Username)
		if p := usernameProblem(r.Context(), h.Store, username, 0); p != nil {
			p.write(w, r)
			return
		}
	}
	if req.Role == "" {
		req.Role = model.RoleUser
	}
	if !model.ValidRole(req.Role) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid role: " + req.Role})
		return
	}
	if req.Role == model.RoleAdmin && !adminCredentialBySession(w, r, "Creating an administrator") {
		return
	}
	// A delegated administrator (admin.users) creates accounts at or below
	// their own role — never an administrator.
	if refuseAdminTarget(w, r, nil, req.Role) {
		return
	}
	// …and never one that holds more than they do: a new User account holds
	// the whole built-in User role (refuseGain).
	if refuseGain(w, r, h.ACL, nil, func(in *perm.Input) { in.Role = req.Role }) {
		return
	}
	if req.Locale == "" {
		req.Locale = "en"
	}
	// ⚠ No zone given stays NO zone (model.TimezoneUnset), never "UTC": an
	// operator creating an account is not choosing that person's clock, and
	// a stored "UTC" is read by every client as a deliberate choice.
	if strings.TrimSpace(req.Timezone) == "" {
		req.Timezone = model.TimezoneUnset
	}
	// Resolve the tenant BEFORE creating anything, so a rejected provider_id
	// doesn't leave a half-provisioned user behind.
	providerID, status, msg := h.resolveProvider(r.Context(), req.ProviderID)
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	// An address an LDAP directory owns (its email_domains): its people
	// arrive by sign-in and directory sync. A local account made here would
	// be one that directory could never sign in — said, not assumed. Asked
	// after the tenant is known: only that tenant's directories count.
	if h.DirectoryFor != nil && !req.AllowDirectoryEmail {
		if name, label, ok := h.DirectoryFor(req.Email, providerID); ok {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "directory_email", "directory": name, "label": label,
				"message": srvtext.Text(userLang(r), "server.account.directory_email", srvtext.Vars{"email": req.Email, "directory": label}),
			})
			return
		}
	}
	// A password the administrator typed passes the one rule
	// (local.CheckPassword); an invitation makes its own, and ignores this one.
	if !req.SendInvite && req.Password != "" {
		if p := passwordProblem(req.Password); p != nil {
			p.write(w, r)
			return
		}
	}
	hash := ""
	invitePw := ""
	if req.SendInvite {
		var err error
		if invitePw, err = generateRandomPassword(16); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		req.Password = invitePw
	}
	if req.Password != "" {
		var err error
		if hash, err = local.HashPassword(req.Password); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	u, err := h.Store.CreateUser(r.Context(), req.Email, hash, req.Role, req.Locale, req.Timezone)
	if err != nil {
		if isUniqueViolation(err) {
			emailTaken(req.Email).write(w, r)
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if providerID != 0 {
		if err := h.Store.SetUserProvider(r.Context(), u.ID, providerID, ""); err != nil {
			// CreateUser has already landed the row in provider 1 — the
			// SUPERTENANT. Returning 500 and leaving it there would mint
			// exactly the confine-exempt account G1 exists to prevent, so the
			// half-created user is removed before reporting the failure.
			if delErr := h.Store.DeleteUser(r.Context(), u.ID); delErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error": "could not home the user in its tenant (" + err.Error() +
						") and could not remove the half-created account (" + delErr.Error() +
						"); user id " + strconv.FormatInt(u.ID, 10) + " is in the supertenant and must be fixed by hand",
				})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		u.ProviderID = &providerID
	}
	if name := strings.TrimSpace(req.DisplayName); name != "" {
		if err := h.Store.UpdateUserDisplayName(r.Context(), u.ID, name); err == nil {
			u.DisplayName = name
		}
	}
	// The username asked for, over the one derived from the address. Taken
	// between the check and now: the derived one stays, and the page shows it.
	if username != "" && username != u.Username {
		if err := h.Store.SetUserUsername(r.Context(), u.ID, username); err == nil {
			u.Username = username
		}
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(u.ID, 10), u.Email)
	out := userCreated{User: u}
	if req.SendInvite {
		// The same letter a folder invitation that makes an account sends
		// (grants.go): where to sign in, the address, a first password.
		// The new account's own language (translated at the last stop).
		lang := srvtext.Pick(u.Locale)
		subject, body := accountCreatedText(lang, h.Tenants.FromRequest(r)+"/admin/", u.Email, invitePw)
		emailed := h.Mailer != nil && h.Mailer.Send(mailer.WithLanguage(r.Context(), lang), u.Email, subject, body) == nil
		out.Invite = &userInvite{Emailed: emailed}
		if !emailed {
			out.Invite.TempPassword = invitePw // shown once, for the administrator to pass on
		}
		auth.AddAuditDetail(r.Context(), "invited", true)
		auth.AddAuditDetail(r.Context(), "emailed", emailed)
	}
	writeJSON(w, http.StatusOK, out)
}

type userUpdateReq struct {
	Password    *string `json:"password,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Role        *string `json:"role,omitempty"`
	Locale      *string `json:"locale,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
	// ProviderID re-homes the user into another tenant. Supertenant only —
	// see Update.
	ProviderID *int64 `json:"provider_id,omitempty"`
	// Enabled gates whether the account may start a session.
	Enabled *bool `json:"enabled,omitempty"`
	// SSOUnlink removes the account's SSO bind (issuer + subject, migration
	// 00079): its next SSO sign-in is matched by its address again, as a
	// first sign-in is - the repair when the identity provider gave the person
	// a new identity or moved to a new issuer address (docs/SSO.md).
	SSOUnlink bool `json:"sso_unlink,omitempty"`
}

// Update modifies a user. Only fields present in the body are touched.
func (h *Users) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var req userUpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	// Before ANY mutation: a confined caller may only touch its own tenant's
	// users. Runs first so a refused request cannot have written half of the
	// body's fields already.
	target, status, msg := h.tenantGate(r.Context(), id)
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	// A delegated administrator never edits an administrator's account and
	// never raises a role above their own (refuseAdminTarget). Before the
	// role validation below so the refusal does not depend on it.
	newRole := ""
	if req.Role != nil {
		newRole = *req.Role
	}
	if refuseAdminTarget(w, r, target, newRole) {
		return
	}
	// An administrator's credential is handed out in a session only
	// (session_gate.go): promoting an account to administrator, changing an
	// administrator's role, setting an administrator's password. Asked before
	// anything is written. An unchanged role is not a role change.
	if target != nil {
		roleChange := req.Role != nil && *req.Role != target.Role
		switch {
		case roleChange && *req.Role == model.RoleAdmin:
			if !adminCredentialBySession(w, r, "Promoting an account to administrator") {
				return
			}
		case roleChange && target.IsAdmin():
			if !adminCredentialBySession(w, r, "Changing an administrator's role") {
				return
			}
		case req.Password != nil && target.IsAdmin():
			if !adminCredentialBySession(w, r, "Setting an administrator's password") {
				return
			}
		case req.SSOUnlink && target.IsAdmin():
			// Unbound, the account is matched by its address at the next SSO
			// sign-in: who may open an administrator's account changes.
			if !adminCredentialBySession(w, r, "Removing an administrator's SSO bind") {
				return
			}
		}
	}
	// Reject an unknown role up-front, and refuse to demote the last admin
	// out of the admin role (which would otherwise lock everyone out of the
	// admin surface just as effectively as deleting them).
	if req.Role != nil {
		if !model.ValidRole(*req.Role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid role: " + *req.Role})
			return
		}
		if *req.Role != model.RoleAdmin {
			if last, err := h.isLastAdmin(r.Context(), id); err == nil && last {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot demote the last admin"})
				return
			}
			// A group makes them one (migration 00086): it would at once
			// again. Who is in that group decides.
			if target != nil && target.AdminByGroup && *req.Role != target.Role {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "this person is an administrator through a group; take them out of the group (or its LDAP / SSO group) instead"})
				return
			}
		}
	}
	// A delegated administrator is judged by the result as well: a built-in
	// role ends the custom one (below), which can hand back what it took away
	// (refuseGain); and a password they set is an account they can sign in
	// as (refuseTakeover). Before any field is written.
	// A tenant move in the same request is judged together with the role
	// below (the move's check), on the state both leave: judged apart, each
	// check assumes the other change is not happening.
	moving := req.ProviderID != nil && target != nil && (target.ProviderID == nil || *target.ProviderID != *req.ProviderID)
	if req.Role != nil && target != nil && target.Role != *req.Role && !moving {
		if refuseGain(w, r, h.ACL, target, func(in *perm.Input) {
			in.Role = *req.Role
			in.CustomRoleID = 0
		}) {
			return
		}
	}
	if req.Password != nil && refuseTakeover(w, r, h.ACL, target) {
		return
	}
	// The one password rule (local.CheckPassword), as the person's own change
	// and an administrator's create ask it. Before any field is written.
	if req.Password != nil {
		if p := passwordProblem(*req.Password); p != nil {
			p.write(w, r)
			return
		}
	}
	// Disabling the final admin locks everyone out of the admin surface just
	// as surely as deleting or demoting them, both of which are already
	// refused above.
	if req.Enabled != nil && !*req.Enabled {
		if last, err := h.isLastAdmin(r.Context(), id); err == nil && last {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot disable the last admin"})
			return
		}
	}
	// Re-homing a user between tenants is a platform-operator action, so it is
	// restricted to an unscoped or supertenant caller. Letting a tenant admin
	// do it would be an escalation in the other direction: Update has no
	// ownership check on its target, so they could pull another tenant's user
	// into their own tenant. It exists to repair accounts stranded in
	// provider 1 by the create-side bug (G1).
	if req.ProviderID != nil {
		scope, scoped := tenant.FromContext(r.Context())
		if scoped && !scope.IsSupertenant {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the supertenant may move a user between tenants"})
			return
		}
		if p, err := h.Store.GetProvider(r.Context(), *req.ProviderID); err != nil || p == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown provider_id"})
			return
		}
		// Moving takes them out of the old tenant's groups (SetUserProvider),
		// and with them a role — possibly a restrictive one — and the level it
		// set: a delegated administrator is judged by what they would hold in
		// the new tenant, like any other change to an account.
		if moving {
			next := *req.ProviderID
			moved, err := groupsPreview(r.Context(), h.Store, target, &next, func(groups []*model.Group) []*model.Group {
				out := make([]*model.Group, 0, len(groups))
				for _, g := range groups {
					if g.ProviderID == nil || *g.ProviderID == next {
						out = append(out, g)
					}
				}
				return out
			})
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			change := moved
			if req.Role != nil && target.Role != *req.Role {
				// And the role picked in the same request: it ends their own
				// custom role and is their level, unless a group of the new
				// tenant gives them a role (Preview then sets its level).
				role := *req.Role
				change = func(in *perm.Input) {
					moved(in)
					in.Role, in.CustomRoleID = role, 0
				}
			}
			if refuseGain(w, r, h.ACL, target, change) {
				return
			}
		}
		if err := h.Store.SetUserProvider(r.Context(), id, *req.ProviderID, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		// SetUserProvider took them out of the old tenant's groups: the role
		// those gave them — and the level it set — go with them.
		if err := group.SyncLevels(r.Context(), h.Store, []int64{id}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		perm.InvalidateFor(r.Context(), id)
	}
	if req.SSOUnlink {
		had, err := h.Store.ClearUserOIDCIdentity(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		// The audit row of this request says it, and whether there was a bind.
		auth.AddAuditDetail(r.Context(), "sso_unlinked", had)
	}
	if req.Password != nil {
		hash, err := local.HashPassword(*req.Password)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		_ = h.Store.UpdateUserPassword(r.Context(), id, hash)
	}
	if req.DisplayName != nil {
		_ = h.Store.UpdateUserDisplayName(r.Context(), id, strings.TrimSpace(*req.DisplayName))
	}
	if req.Role != nil {
		_ = h.Store.UpdateUserRole(r.Context(), id, *req.Role)
		// A custom role is "its base role plus changes"; picking a built-in
		// role by hand is picking THAT role, so the custom one ends.
		// (PutUserRoles is how a custom role is given.)
		if target == nil || target.Role != *req.Role {
			_ = h.Store.SetUserCustomRole(r.Context(), id, 0)
			perm.InvalidateFor(r.Context(), id)
		}
		// As on the Role field (PutUserRoles): the level picked here is the
		// account's own now — forget the one kept from before a group's role,
		// or leaving the group would restore it over this choice — and a
		// group's role that still applies moves it again.
		// Only when the role really changes: an unchanged role sent along with
		// other fields is no choice, and forgetting the kept level then would
		// leave the account on the group role's level for good once it left.
		if target == nil || target.Role != *req.Role {
			if err := h.Store.DeleteUserGroupLevel(r.Context(), id); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		if err := group.SyncLevels(r.Context(), h.Store, []int64{id}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		perm.InvalidateFor(r.Context(), id)
	}
	if req.Enabled != nil {
		if err := h.Store.SetUserEnabled(r.Context(), id, *req.Enabled); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if req.Locale != nil || req.Timezone != nil {
		// Fetch current to fill in the missing field.
		cur, err := h.Store.GetUser(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		l := cur.Locale
		tz := cur.Timezone
		if req.Locale != nil {
			l = *req.Locale
		}
		if req.Timezone != nil {
			tz = *req.Timezone
		}
		_ = h.Store.UpdateUserLocale(r.Context(), id, l, tz)
		// One language per person, whichever surface reads it (#191).
		if req.Locale != nil {
			syncSurfaceLocales(r.Context(), h.Store, id, l)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete removes a user. The last remaining admin can never be deleted —
// not even by itself — so the instance can't be locked out of its own admin
// surface. A non-existent id is reported as 404 rather than a silent 200.
func (h *Users) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	target, status, msg := h.tenantGate(r.Context(), id)
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	if refuseAdminTarget(w, r, target, "") {
		return
	}
	if target.IsAdmin() {
		if last, err := h.isLastAdmin(r.Context(), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		} else if last {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot delete the last admin"})
			return
		}
	}
	// The account's own public links go with it (db.Store.DeleteUser), and
	// the audit row says how many were still open.
	closed, err := h.Store.DeleteUserWithLinks(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// The row is gone once the log is read: keep who it was.
	auth.SetAuditTarget(r.Context(), "", target.Email)
	auth.AddAuditDetail(r.Context(), "links_closed", closed)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// isLastAdmin reports whether userID is an admin and the only admin left.
// Used to block deleting/demoting the final admin. The user table is tiny
// (operator accounts), so a full ListUsers scan is cheaper than threading a
// dedicated COUNT through every Store implementation.
func (h *Users) isLastAdmin(ctx context.Context, userID int64) (bool, error) {
	return isLastAdmin(ctx, h.Store, userID)
}

// isLastAdmin reports whether userID is the only administrator the store
// lists (tenant-confined stores list the tenant's).
func isLastAdmin(ctx context.Context, store db.Store, userID int64) (bool, error) {
	users, err := store.ListUsers(ctx)
	if err != nil {
		return false, err
	}
	admins := 0
	targetIsAdmin := false
	for _, u := range users {
		if u.IsAdmin() {
			admins++
			if u.ID == userID {
				targetIsAdmin = true
			}
		}
	}
	return targetIsAdmin && admins <= 1, nil
}
