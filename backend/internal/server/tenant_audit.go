package server

import (
	"context"
	"log/slog"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// auditSupertenantAccounts reports accounts homed in the confine-exempt
// supertenant on a multi-tenant install, once, at boot.
//
// ⚠⚠ It reports; it does not repair. Homing a directory account in the tenant
// its login arrived for is fixed going FORWARD (auth.ProvisionUser), but rows
// created before that fix are still where the old code put them, and a
// migration cannot safely move them:
//
//   - nothing records which driver created a row, so there is no column that
//     separates a mis-homed LDAP user from the platform operator;
//   - the break-glass admin (firstrun.go) is DELIBERATELY in the supertenant,
//     as is every legitimate platform operator, so a blanket re-home would take
//     the operator's own account away from them;
//   - re-homing changes which storages an account can reach. Guessing that from
//     an email domain is exactly the kind of heuristic that produces a support
//     ticket titled "all my users lost their files".
//
// So the operator gets the count and the two commands, and decides. Re-homing
// is `PATCH /api/admin/users/{id}` with `provider_id`, which only a supertenant
// admin may call.
//
// Non-admin only, because admins in the supertenant are the expected case and
// listing them every boot would train an operator to ignore the line.
func auditSupertenantAccounts(ctx context.Context, store db.Store, multiTenant bool, drivers []string) {
	if !multiTenant || store == nil {
		return
	}
	var directory []string
	for _, d := range drivers {
		switch normalizeDriverName(d) {
		case "ldap", "proxy-header":
			directory = append(directory, normalizeDriverName(d))
		}
	}
	if len(directory) == 0 {
		return
	}

	super, err := store.GetSupertenant(ctx)
	if err != nil || super == nil {
		return
	}
	users, err := store.ListUsersByProvider(ctx, super.ID)
	if err != nil {
		return
	}
	var stranded []string
	for _, u := range users {
		if u == nil || u.Role == model.RoleAdmin {
			continue
		}
		stranded = append(stranded, u.Email)
	}
	if len(stranded) == 0 {
		return
	}
	slog.Warn("tenancy: non-admin accounts are homed in the supertenant, which is confine-exempt (it can reach every storage)",
		slog.Int("count", len(stranded)),
		slog.String("accounts", strings.Join(stranded, ", ")),
		slog.String("drivers", strings.Join(directory, ",")),
		slog.String("cause", "before this release, LDAP and proxy-header logins provisioned into `default`; new logins now home in the tenant they arrive for"),
		slog.String("remedy", "re-home each with PATCH /api/admin/users/{id} {\"provider_id\": <tenant>} as a supertenant admin, or leave the ones that belong to the platform operator"))
}
