package ldap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/group"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Directory sync (docs/LDAP.md → Directory sync): without anybody signing
// in, read every person the directory lists and
//
//   - make an account for each one filex does not have yet, as their first
//     sign-in would (auth.ProvisionUser — the same tenant homing);
//   - record their LDAP groups and bring their memberships of filex groups
//     linked to LDAP groups in step (what every sign-in does);
//   - switch off the accounts of people the directory has switched off, and
//     back on when it lets them back in (people.go);
//   - for accounts the directory made that it no longer lists: end their
//     LDAP memberships and, with sync_disable_missing, switch them off —
//     back on if they return.
//
// Sign-in keeps doing its part: sync only means the directory's answer
// reaches filex before the person does.

// syncPageSize is the page size of the directory search (RFC 2696). Active
// Directory answers at most 1000 entries to a search without paging.
const syncPageSize = 500

// SyncInterval implements auth.DirectorySyncer.
func (d *Driver) SyncInterval() time.Duration { return d.syncInterval }

// syncFilter is the search that lists every person: sync_filter when set,
// else user_filter with the sign-in name replaced by "*" — (mail=%s) becomes
// (mail=*), so the people sync finds are the people who can sign in.
func (d *Driver) syncFilter() string {
	if d.syncFilterRaw != "" {
		return d.syncFilterRaw
	}
	f := strings.ReplaceAll(d.userFilter, "%[1]s", "%s")
	return strings.ReplaceAll(f, "%s", "*")
}

// SyncDirectory implements auth.DirectorySyncer.
func (d *Driver) SyncDirectory(ctx context.Context) (*auth.DirectorySyncReport, error) {
	rep := &auth.DirectorySyncReport{Provider: d.Directory(), StartedAt: time.Now().UTC()}
	fail := func(err error) (*auth.DirectorySyncReport, error) {
		rep.Error = err.Error()
		rep.FinishedAt = time.Now().UTC()
		return rep, err
	}
	c, err := d.connect(ctx)
	if err != nil {
		return fail(err)
	}
	defer c.Close()
	if d.startTLS {
		tc, err := d.startTLSConfig()
		if err != nil {
			return fail(fmt.Errorf("ldap: ca_file: %w", err))
		}
		if err := c.StartTLS(tc); err != nil {
			return fail(fmt.Errorf("ldap: starttls: %w", err))
		}
	}
	if d.bindDN != "" {
		if err := c.Bind(d.bindDN, d.bindPass); err != nil {
			return fail(fmt.Errorf("ldap: service bind: %w", err))
		}
	}
	// Groups first, so the people below join the groups made for them.
	if d.importGroupsOn {
		listed, err := d.listGroups(c)
		if err != nil {
			rep.Problem("groups", fmt.Errorf("group search: %w", err))
		} else if err := d.importGroups(ctx, listed, rep); err != nil {
			rep.Problem("groups", err)
		}
	}

	attrs := d.searchAttrs()
	res, err := c.SearchWithPaging(ldap.NewSearchRequest(
		d.baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		d.syncFilter(), attrs, nil,
	), syncPageSize)
	if err != nil {
		return fail(fmt.Errorf("ldap: directory search: %w", err))
	}

	seen := map[int64]bool{}
	// unsure: a person the directory listed whose account could not be
	// looked up (a store error, not "none" or "not this directory's"). Their
	// account may be one of this directory's, so this run counts nobody as
	// no longer listed: a partial answer switches nobody off.
	unsure := 0
	// links: group_filter names the person by the name they sign in with
	// (%u), which sync does not know - their LDAP-linked memberships are
	// brought in step at their sign-in instead, and left as they are here.
	links := d.readsLinks() && !strings.Contains(d.groupFilter, "%u")
	for _, e := range res.Entries {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		// Only a person with an address: an entry that names none is signed
		// in to as `<name>@<token>` by its first sign-in, which sync cannot
		// tell (it does not know what they will type).
		em := identity.Normalize(e.GetAttributeValue(d.emailAttr))
		if !identity.LooksLikeEmail(em) || !d.domainAllowed(em) {
			rep.Skipped++
			// Still listed, though: an account this directory knows by the
			// person's permanent id is not one it stopped listing.
			if id := personID(d.idPrefix(), e); id != "" {
				if u, err := d.store.GetUserByDirectoryID(ctx, id); err == nil && u != nil {
					seen[u.ID] = true
				}
			}
			continue
		}
		rep.Found++
		off := switchedOff(e)
		user, _, err := d.account(ctx, e, em, em, d.groupsOf(e), off == "", rep)
		if err != nil {
			rep.Skipped++
			var lookup errLookup
			switch {
			case errors.Is(err, errNoAccount): // switched off there, and no account here: nothing to do
			case errors.As(err, &lookup):
				unsure++
				rep.Problem(em, err)
			default:
				rep.Problem(em, err)
			}
			continue
		}
		seen[user.ID] = true
		auth.ClaimSource(ctx, d.store, user, model.AuthSourceLDAP)
		d.claimDirectory(ctx, user)
		switch {
		case off != "" && user.Enabled && user.DirectoryOwner() != d.Directory():
			// An account with a password of its own here (made here, not by
			// the directory): the directory switching its entry off is not
			// the administrator switching it off.
			rep.Problem(em, fmt.Errorf("%s, but the account has a password of its own here: left on", off))
		case off != "" && user.Enabled:
			// Administrators too: the directory says so in as many words,
			// unlike a person who is merely no longer listed — but never the
			// last one, which would leave nobody to administer filex.
			if user.IsAdmin() {
				if last, err := group.LastAdmin(ctx, d.store, user); err != nil || last {
					rep.Problem(em, fmt.Errorf("%s, but the last administrator: left on - make another administrator, then sync again", off))
					break
				}
			}
			if err := d.store.SetUserEnabledByDirectory(ctx, user.ID, false); err != nil {
				rep.Problem(em, err)
			} else {
				rep.Disabled++
			}
		case off == "" && user.DisabledByDirectory():
			if err := d.store.SetUserEnabledByDirectory(ctx, user.ID, true); err != nil {
				rep.Problem(em, err)
			} else {
				rep.Enabled++
			}
		}
		if !links {
			continue // groups are not read: memberships stay as they are
		}
		groups, err := d.linkGroupsOf(c, e, em)
		if err != nil {
			// Left as they are, as at a sign-in that cannot read groups.
			rep.Problem(em, err)
			continue
		}
		changed, err := d.applyGroups(ctx, user, groups)
		if err != nil {
			rep.Problem(em, err)
			continue
		}
		if changed {
			rep.Updated++
		}
	}

	// ⚠ A search that found nobody is far likelier a wrong filter or base
	// than a directory that emptied: it must not end everybody's
	// memberships, nor switch everybody off.
	if rep.Found == 0 {
		return fail(errors.New("ldap: the directory search found nobody with an e-mail; no account was changed - check base_dn and sync_filter"))
	}
	if d.readsLinks() && !links {
		rep.Problem("groups", errors.New("group_filter names people by their sign-in name (%u), which directory sync does not know; their LDAP-linked groups follow at their sign-in"))
	}
	if unsure > 0 {
		rep.Problem("accounts", fmt.Errorf("%d listed people could not be looked up; nobody was counted as no longer listed this time", unsure))
		rep.FinishedAt = time.Now().UTC()
		return rep, nil
	}
	users, err := d.store.ListUsers(ctx)
	if err != nil {
		return fail(err)
	}
	for _, u := range users {
		if u.DirectoryOwner() != d.Directory() || seen[u.ID] {
			continue
		}
		rep.Missing++
		if d.readsLinks() {
			if _, err := d.applyGroups(ctx, u, nil); err != nil {
				rep.Problem(u.Email, err)
			}
		}
		if d.syncDisableMissing && u.Enabled && !u.IsAdmin() {
			if err := d.store.SetUserEnabledByDirectory(ctx, u.ID, false); err != nil {
				rep.Problem(u.Email, err)
				continue
			}
			rep.Disabled++
		}
	}
	rep.FinishedAt = time.Now().UTC()
	return rep, nil
}

// applyGroups records a person's LDAP groups and brings their memberships of
// filex groups linked to LDAP groups in step; it reports whether a
// membership changed. Shared by sign-in (syncGroups) and directory sync.
func (d *Driver) applyGroups(ctx context.Context, user *model.User, groups []string) (bool, error) {
	if err := d.store.SetUserLDAPGroups(ctx, user.ID, groups); err != nil {
		return false, fmt.Errorf("record LDAP groups: %w", err)
	}
	changed, err := group.SyncLinked(ctx, d.store, user, model.GroupLinkLDAP, groups)
	if err != nil {
		return changed, fmt.Errorf("update LDAP-linked groups: %w", err)
	}
	return changed, nil
}
