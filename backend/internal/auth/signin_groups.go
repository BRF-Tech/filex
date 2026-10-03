package auth

import (
	"context"
	"log/slog"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/group"
	"github.com/brf-tech/filex/backend/internal/model"
)

// RecordSignInGroups is what a sign-in does with the groups its identity
// provider reported. Every provider that reads groups calls it at the point
// it records them — OIDC (`role_claim`), LDAP (`group_attr`), the operating
// system (pam, windows) and the header proxy (`header_roles`) — so there is
// ONE rule, whichever way a person signs in:
//
//  1. the groups are stored, replacing the last sign-in's (user_sso_groups):
//     what a role's SSO-group target and a changed group link read;
//  2. a NEW account that is not an administrator starts with the custom role
//     they name (ApplyStartingRole) — once, at creation;
//  3. the account joins every filex group of its tenant linked to one of them
//     and leaves every group it was in only through such a link
//     (group.SyncLinked): the directory is the authority, a member added by
//     hand stays;
//  4. its built-in level follows the role its groups now give it
//     (group.SyncLevels) — also when no membership moved, since an admin
//     mapping may just have demoted it.
//
// Nothing here is fatal to the sign-in: a failure is logged, and the next
// sign-in (or a change to a group's links) puts it right. It returns the
// account as stored afterwards — its level may have moved.
func RecordSignInGroups(ctx context.Context, store db.Store, driver string, user *model.User, groups []string, created bool) *model.User {
	if user == nil {
		return nil
	}
	if err := store.SetUserSSOGroups(ctx, user.ID, groups); err != nil {
		// Loud: a role or a group that should follow this account's groups
		// will not until the next successful write.
		slog.Warn(driver+": could not record the sign-in's groups",
			slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
	}
	if created && !user.IsAdmin() && len(groups) > 0 {
		ApplyStartingRole(ctx, store, driver, user, groups)
	}
	if _, err := group.SyncLinked(ctx, store, user, model.GroupLinkSSO, groups); err != nil {
		slog.Warn(driver+": could not update the account's linked groups",
			slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
	}
	if err := group.SyncLevels(ctx, store, []int64{user.ID}); err != nil {
		slog.Warn(driver+": could not bring the account's level in step with its groups",
			slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
	}
	if u, err := store.GetUser(ctx, user.ID); err == nil && u != nil {
		return u
	}
	return user
}
