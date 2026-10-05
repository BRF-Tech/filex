package ldap

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/brf-tech/filex/backend/internal/group"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Which directory an account belongs to, and the LDAP links (docs/LDAP.md →
// Several directories, → Groups). Every LDAP instance is a directory of its
// own, named by its slug: it signs in only the accounts it made (or, for the
// first, the ones made here before there were several), and only addresses
// in its e-mail domains when it names some.

// domainAllowed reports whether this directory may sign in or make an
// account for the address: any, unless email_domains names some.
func (d *Driver) domainAllowed(email string) bool {
	if len(d.emailDomains) == 0 {
		return true
	}
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	dom := email[at+1:]
	for _, want := range d.emailDomains {
		if dom == want {
			return true
		}
	}
	return false
}

// ClaimsEmail reports whether this directory owns an address: it names
// e-mail domains (email_domains) and the address is in one. A directory
// that names none claims nothing — it signs in any address, but owns none.
func (d *Driver) ClaimsEmail(email string) bool {
	return len(d.emailDomains) > 0 && d.domainAllowed(strings.ToLower(strings.TrimSpace(email)))
}

// notMine says why an existing account is not this directory's to sign in,
// or "" when it is: another directory made it, or it was made here (a local,
// SSO or proxy account) — which only the main directory may sign in, as
// before there were several.
//
// ⚠ An account from before migration 00084 with no password here and no SSO
// identity has no label (an empty auth_source): which directory made it is not
// known, and in 0.51 every directory signed in any account at its address.
// The first directory that signs it in now takes it (claimDirectory) - the
// sign-in's realm still decides (outOfReach). Labelling it 'local' instead
// would lock every person of a second directory, or of a tenant's own, out
// of their account at the upgrade.
func (d *Driver) notMine(u *model.User) string {
	switch owner := u.DirectoryOwner(); {
	case owner == d.Directory():
		return ""
	case owner != "":
		return "made by the directory " + owner
	case u.AuthSource == "" && u.PasswordHash == "":
		return ""
	case d.Directory() != model.MainDirectory:
		return "made here, not by a directory"
	}
	return ""
}

// OwnerTenantKey is the configuration key authsetup sets on a tenant's own
// LDAP instance: the id of the tenant it belongs to.
const OwnerTenantKey = "owner_tenant"

// syncTenant is the tenant directory sync works in: the one a tenant's own
// instance belongs to, else the one the instance is pinned to (provider), else
// 0 - the platform's directory on an install with one tenant, or one that
// signs people in to several by their realm.
func (d *Driver) syncTenant(ctx context.Context) int64 {
	if d.ownerTenant != 0 {
		return d.ownerTenant
	}
	if d.homing.MultiTenant && d.homing.Pin != "" {
		if p, err := d.store.GetProviderBySlug(ctx, d.homing.Pin); err == nil && p != nil {
			return p.ID
		}
	}
	return 0
}

// claimDirectory records this directory on an LDAP account that does not
// name one yet (one it just made, or one from before users.auth_directory).
func (d *Driver) claimDirectory(ctx context.Context, u *model.User) {
	if u == nil || u.AuthSource != model.AuthSourceLDAP || u.AuthDirectory != "" {
		return
	}
	if err := d.store.SetUserAuthDirectory(ctx, u.ID, d.Directory()); err != nil {
		slog.Warn("ldap: could not record which directory made an account",
			slog.Int64("user_id", u.ID), slog.String("err", err.Error()))
		return
	}
	u.AuthDirectory = d.Directory()
}

// minSyncInterval bounds how often directory sync runs on its own: each run
// reads the whole directory.
const minSyncInterval = 5 * time.Minute

// readsLinks reports whether this directory reads people's groups for the
// LDAP links: group_attr names the attribute listing them (the page fills
// in memberOf), or group_filter finds them by a search. Neither: groups are
// not read, and nobody's LDAP-linked memberships move.
func (d *Driver) readsLinks() bool {
	return d.groupAttr != "" || d.groupFilter != ""
}

// syncLinkGroups records the LDAP groups of a browser sign-in and makes the
// account's memberships of filex groups linked to LDAP groups match them
// (group.SyncLinked) — every sign-in, the directory being the authority.
// Members added by hand stay. A group can give a role, so the level
// underneath may move too. None of it refuses the sign-in: the directory
// accepted the password, and a failed read or write here is logged and
// retried at the next one — a directory hiccup never takes anybody out of
// their groups.
func (d *Driver) syncLinkGroups(ctx context.Context, c conn, user *model.User, entry *ldap.Entry, name string) *model.User {
	if !d.readsLinks() {
		return user
	}
	groups, err := d.linkGroupsOf(c, entry, name)
	if err != nil {
		slog.Warn("ldap: could not read the account's LDAP groups; its group memberships are left as they are",
			slog.String("dn", entry.DN), slog.String("err", err.Error()))
		return user
	}
	if _, err := d.applyGroups(ctx, user, groups); err != nil {
		slog.Warn("ldap: could not bring the account's LDAP groups in step",
			slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
	}
	if err := group.SyncLevels(ctx, d.store, []int64{user.ID}); err != nil {
		slog.Warn("ldap: could not bring the account's level in step with its groups",
			slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
	}
	if u2, err := d.store.GetUser(ctx, user.ID); err == nil && u2 != nil {
		return u2
	}
	return user
}

// linkGroupsOf returns the LDAP groups of the person whose entry this is, in
// group.LDAPValue's form: each group's DN and its common name, so a filex
// group may name either. Read from the entry's group attribute (group_attr),
// or — with group_filter set — found by a search, run as the service
// account: the person's own bind may not be allowed to read groups.
func (d *Driver) linkGroupsOf(c conn, entry *ldap.Entry, identifier string) ([]string, error) {
	var dns []string
	if d.groupAttr != "" {
		dns = entry.GetAttributeValues(d.groupAttr)
	}
	if d.groupFilter != "" {
		if d.bindDN != "" {
			if err := c.Bind(d.bindDN, d.bindPass); err != nil {
				return nil, fmt.Errorf("service bind for the group search: %w", err)
			}
		}
		base := d.groupBaseDN
		if base == "" {
			base = d.baseDN
		}
		f := strings.ReplaceAll(d.groupFilter, "%s", ldap.EscapeFilter(entry.DN))
		f = strings.ReplaceAll(f, "%u", ldap.EscapeFilter(strings.ToLower(strings.TrimSpace(identifier))))
		res, err := c.Search(ldap.NewSearchRequest(
			base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, maxGroups, 0, false,
			f, []string{"dn"}, nil,
		))
		if err != nil && (!ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) || res == nil) {
			return nil, fmt.Errorf("group search: %w", err)
		}
		dns = nil
		if res != nil {
			for _, e := range res.Entries {
				dns = append(dns, e.DN)
			}
		}
	}
	return groupValues(dns), nil
}

// maxGroups bounds the groups one sign-in reads.
const maxGroups = 1000

// maxGroupName is user_ldap_groups.group_name's size on MySQL.
const maxGroupName = 512

// groupValues turns group DNs into the names a filex group's LDAP link is
// compared with: each DN and its common name (the value of its first part),
// in group.LDAPValue's form. A name too long to store is dropped.
func groupValues(dns []string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(v string) {
		v = group.LDAPValue(v)
		if v != "" && len(v) <= maxGroupName && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, dn := range dns {
		add(dn)
		if parsed, err := ldap.ParseDN(dn); err == nil && len(parsed.RDNs) > 0 && len(parsed.RDNs[0].Attributes) > 0 {
			add(parsed.RDNs[0].Attributes[0].Value)
		}
	}
	return out
}
