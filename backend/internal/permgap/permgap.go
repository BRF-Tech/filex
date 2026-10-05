// Package permgap tells administrators about the saved roles that allow a
// permission but not the one carved out of it (perm/gaps.go): adding files but
// not encrypting, as a save on a version without files.encrypt leaves them.
//
// Once per new gap, never at every start: the gaps administrators were told of
// are recorded (perm.AnnounceGaps), and the bell rings again only when one
// they were not told of opens - after a start that finds a role an older
// version saved, or after a save through the API that never showed the key.
// A gap given back or dismissed leaves the record, so it is told again should
// it ever open again.
//
// Who is told is who can act on Admin -> Roles: the administrators nobody
// confines read every gap (one broadcast), and a tenant's administrators are
// told about their tenant's own custom roles (one notification each). The
// built-in roles are the platform operator's alone.
package permgap

import (
	"context"
	"log/slog"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// Announce tells administrators about the gaps they have not been told of.
// Best effort: a failure is logged and changes nothing a person relies on.
// With notifications off (n nil) nothing is recorded either, so the gaps are
// told once they are on.
func Announce(ctx context.Context, store db.Store, n notify.Service) {
	if n == nil || store == nil {
		return
	}
	fresh, open, err := perm.AnnounceGaps(ctx, store)
	if err != nil {
		slog.Warn("perm: could not check the roles for a missing permission", slog.Any("err", err))
		return
	}
	if len(fresh) == 0 {
		return
	}
	slog.Warn("perm: saved roles allow adding files but not encrypting; Admin -> Roles lists them",
		slog.Int("open", len(open)), slog.Int("new", len(fresh)))
	ctx = context.WithoutCancel(ctx)
	if _, err := n.Send(ctx, notify.PermissionGaps(nil, len(open))); err != nil {
		slog.Warn("perm: could not tell the administrators about the roles", slog.Any("err", err))
	}
	for _, pid := range tenantsWithNew(fresh) {
		p, err := store.GetProvider(ctx, pid)
		if err != nil || p == nil || p.IsSupertenant {
			// The supertenant's administrators read the broadcast.
			continue
		}
		count := 0
		for _, g := range open {
			if g.ProviderID != nil && *g.ProviderID == pid {
				count++
			}
		}
		users, err := store.ListUsersByProvider(ctx, pid)
		if err != nil {
			slog.Warn("perm: could not tell a tenant's administrators about its roles", slog.Int64("tenant", pid), slog.Any("err", err))
			continue
		}
		for _, u := range users {
			if u.Role != model.RoleAdmin || !u.Enabled {
				continue
			}
			uid := u.ID
			_, _ = n.Send(ctx, notify.PermissionGaps(&uid, count))
		}
	}
}

// tenantsWithNew are the tenants whose own roles have a new gap, in the
// order the gaps came.
func tenantsWithNew(fresh []perm.Gap) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, g := range fresh {
		if g.ProviderID == nil || seen[*g.ProviderID] {
			continue
		}
		seen[*g.ProviderID] = true
		out = append(out, *g.ProviderID)
	}
	return out
}
