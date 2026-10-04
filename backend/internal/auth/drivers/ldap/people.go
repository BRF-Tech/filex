package ldap

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/go-ldap/ldap/v3"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

// People (migration 00082, docs/LDAP.md → Who is who):
//
//   - A person is found by their PERMANENT id in the directory (entryUUID, or
//     objectGUID on Active Directory) before their e-mail. Someone whose
//     address changes there keeps their account here, and its e-mail follows;
//     an address the directory hands to somebody new does not hand them the
//     previous owner's account and files.
//   - A person the directory has switched off (Active Directory's "account
//     disabled", 389-ds's nsAccountLock, an OpenLDAP ppolicy lock with no end)
//     is switched off here by directory sync, so their sessions, API keys and
//     SFTP keys stop too — their password already stopped at the directory.
//     Sync switches them back on when the directory does.
//
// A directory with no permanent ids (none of those attributes readable) works
// as before: by e-mail alone.

// personAttrs are what a person's entry is read with, besides the e-mail and
// group attributes: the permanent id and the switched-off flags.
var personAttrs = []string{"entryUUID", "objectGUID", "userAccountControl", "nsAccountLock", "pwdAccountLockedTime"}

// personID is a person's permanent id in this directory: the prefix (its
// provider name and ":") and entryUUID, else objectGUID in hex. "" when the
// entry has neither — unlike a group, a person is never keyed by their DN,
// which changes when they are renamed, so a rename would look like a
// different person taking over the address.
func personID(prefix string, e *ldap.Entry) string {
	if v := strings.TrimSpace(e.GetAttributeValue("entryUUID")); v != "" {
		return prefix + strings.ToLower(v)
	}
	if b := e.GetRawAttributeValue("objectGUID"); len(b) == 16 {
		return prefix + hex.EncodeToString(b)
	}
	return ""
}

// adAccountDisabled is userAccountControl's ACCOUNTDISABLE bit.
const adAccountDisabled = 0x2

// ppolicyLockedForever is the pwdAccountLockedTime an OpenLDAP administrator
// sets to lock an account until it is unlocked by hand; any other value is a
// lockout after failed passwords, which ends on its own.
const ppolicyLockedForever = "000001010000Z"

// switchedOff says why the directory has switched this person off, or "".
func switchedOff(e *ldap.Entry) string {
	if v := strings.TrimSpace(e.GetAttributeValue("userAccountControl")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n&adAccountDisabled != 0 {
			return "disabled in Active Directory"
		}
	}
	if strings.EqualFold(strings.TrimSpace(e.GetAttributeValue("nsAccountLock")), "true") {
		return "locked (nsAccountLock)"
	}
	if strings.TrimSpace(e.GetAttributeValue("pwdAccountLockedTime")) == ppolicyLockedForever {
		return "locked by the directory's password policy"
	}
	return ""
}

// errNotMine: the account the address names is not this directory's to sign
// in (notMine), or belongs to a different directory entry.
type errNotMine struct{ why string }

func (e errNotMine) Error() string { return e.why }

// errNoAccount: there is no account, and none was to be made.
var errNoAccount = errors.New("no account")

// account finds — or, with create, opens — the account of the person whose
// entry this is: name is what they were found by, em their address (already
// normalised, in this directory's domains), groups what group_attr lists
// (upstream's first sign-in rule reads them). created reports a new account.
// rep, during directory sync, counts what changed and collects problems; nil
// at a sign-in, where they are only logged.
//
// In order: by their permanent id (an e-mail changed in the directory is
// followed); by their address (an account this directory may sign in, not
// the previous owner's of a reused address); an account an older build
// opened under the bare name, adopted (auth.AdoptAccount); else a new one,
// if the first sign-in rule opens it.
func (d *Driver) account(ctx context.Context, e *ldap.Entry, name, em string, groups []string, create bool, rep *auth.DirectorySyncReport) (*model.User, bool, error) {
	id := personID(d.idPrefix(), e)
	if id != "" {
		if u, err := d.store.GetUserByDirectoryID(ctx, id); err == nil && u != nil {
			if u.Email != em {
				d.followEmail(ctx, u, em, rep)
			}
			return u, false, nil
		}
	}
	u, err := d.store.GetUserByEmail(ctx, em)
	if err == nil && u != nil {
		if why := d.notMine(u); why != "" {
			return nil, false, errNotMine{why}
		}
		switch {
		case id == "" || u.DirectoryID == id:
		case u.DirectoryID != "":
			// The directory gave this address to someone new. The account,
			// its files and shares are the previous person's.
			return nil, false, errNotMine{"the address now belongs to a different person in the directory; " +
				"their account is the previous owner's - rename or delete it to let the new person in"}
		default:
			d.recordID(ctx, u, id, em, rep)
		}
		return u, false, nil
	}
	if !create {
		return nil, false, errNoAccount
	}
	adopted, err := auth.AdoptAccount(ctx, d.store, auth.Adoption{
		Driver: "ldap", LoginName: name, Email: em, Homing: d.homing,
	})
	if err != nil {
		return nil, false, err
	}
	if adopted != nil {
		d.recordID(ctx, adopted, id, em, rep)
		return adopted, false, nil
	}
	if rep != nil && d.firstLogin.Decide(groups) != "" {
		// Directory sync opens only the accounts a first sign-in would — and
		// says nothing about the rest: refusing a whole directory one audit
		// row at a time is noise.
		return nil, false, errNoAccount
	}
	// ⚠⚠ NOT store.CreateUser directly. That call hard-codes provider_id to
	// `default`, which is seeded is_supertenant = 1 and therefore
	// confine-EXEMPT: on a multi-tenant install every directory user it created
	// could reach every storage on the box. auth.ProvisionUser homes the account
	// in the tenant this login arrived for — the request Host, which
	// handlers.Auth.Login stamps onto the context (the same signal multioidc uses
	// to pick a realm), or an operator's pinned `provider` slug.
	//
	// ⚠ A protocol login (SFTP/FTPS/NFS — internal/protocolauth) has no Host at
	// all — nor has directory sync — so on a multi-tenant install with no pin
	// this REFUSES rather than falling back to the supertenant. The account
	// still works over those protocols the moment it exists; what it cannot do
	// is come into existence there.
	//
	// ⚠ The first-login rule (auth.ProvisionFirstLogin) sits in front of that:
	// auto_create off, or allowed_groups with no match, refuses. The person is
	// answered exactly as for a wrong password (no account/group oracle) unless
	// show_refusal_reason is on (auth.RefusedAfterPassword); the reason is in
	// the log and the audit row either way.
	u, err = auth.ProvisionFirstLogin(ctx, d.store, auth.FirstLogin{
		Driver: "ldap", Identifier: name, Email: em, Role: model.RoleUser,
		Groups: groups, Policy: d.firstLogin, Homing: d.homing,
	})
	if err != nil {
		if errors.Is(err, auth.ErrFirstLoginRefused) {
			// After the bind: the password was right. The answer a wrong
			// password gets, unless the operator chose to tell why.
			return nil, false, auth.RefusedAfterPassword(d.tellRefusal, err)
		}
		return nil, false, err
	}
	slog.Info("ldap: provisioned a directory account",
		slog.String("email", em), slog.String("dn", e.DN), slog.String("directory", d.Directory()))
	if rep != nil {
		rep.Created++
	}
	d.recordID(ctx, u, id, em, rep)
	return u, true, nil
}

// recordID records a person's permanent id on their account (none: nothing
// to record).
func (d *Driver) recordID(ctx context.Context, u *model.User, id, em string, rep *auth.DirectorySyncReport) {
	if id == "" || u.DirectoryID == id {
		return
	}
	if err := d.store.SetUserDirectoryID(ctx, u.ID, id); err != nil {
		d.problem(rep, em, fmt.Errorf("record the directory id: %w", err))
		return
	}
	u.DirectoryID = id
}

// followEmail gives the account the e-mail the directory now has for the
// person — unless another account already has it, which an administrator
// has to settle.
func (d *Driver) followEmail(ctx context.Context, u *model.User, em string, rep *auth.DirectorySyncReport) {
	if other, err := d.store.GetUserByEmail(ctx, em); err == nil && other != nil && other.ID != u.ID {
		d.problem(rep, em, fmt.Errorf("the directory changed %s's e-mail to an address another account has; the account keeps %s", u.Email, u.Email))
		return
	}
	if err := d.store.UpdateUserEmail(ctx, u.ID, em); err != nil {
		d.problem(rep, em, fmt.Errorf("follow the directory's new e-mail: %w", err))
		return
	}
	slog.Info("ldap: the directory changed a person's e-mail; the account follows",
		slog.Int64("user_id", u.ID), slog.String("from", u.Email), slog.String("to", em), slog.String("directory", d.Directory()))
	u.Email = em
	if rep != nil {
		rep.EmailsChanged++
	}
}

// problem records a problem with one person: in the sync report, or in the
// log at a sign-in.
func (d *Driver) problem(rep *auth.DirectorySyncReport, em string, err error) {
	if rep != nil {
		rep.Problem(em, err)
		return
	}
	slog.Warn("ldap: "+err.Error(), slog.String("email", em), slog.String("directory", d.Directory()))
}
