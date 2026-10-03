package perm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Saved roles and a catalogue that grows.
//
// ⚠ Every list an administrator saves — the built-in User role's
// (model.SettingPermissionDefaults), the Viewer role's, a custom role's own
// list and its "different in some folders" part — stores only what it
// ALLOWS. A key the list was saved without reads as "not allowed", and a key
// that did not exist yet when the list was saved is exactly such a key. For a
// permission carved out of one the list already allowed (files.encrypt out of
// files.create), that silently takes away what the role could do the day
// before the upgrade — the one thing an upgrade must not do
// (docs/PERMISSIONS.md).
//
// So a key carved out of an older one names it in inheritOnAdd, and the first
// start that finds the key missing from the catalogue the install last
// recorded (model.SettingPermissionCatalogue) gives it to every saved list
// that allows the older key — and to every person's own exceptions that
// allow it: an exception-only files.create, or a preset pinned on a person
// before the key existed, would lose the key there too. Once: afterwards it
// is a permission like any other, and an administrator who takes it away has
// taken it away.

// inheritOnAdd is, for each key added after v0.49.0 that is carved out of an
// older one, that older key. "Carved out" means every action the added key
// allows needs the older key as well — which is why a Deny of the older key
// needs no twin (inheritAllows). A key added with no entry here is new
// ground: recorded as known, and given to no saved list.
var inheritOnAdd = map[Perm]Perm{
	// Encrypting is creating the folder's marker file or a .fxe, which needs
	// files.create too. A rename, a move or a copy onto those names needs
	// files.encrypt beside its own permission instead: a list that allows
	// renaming or moving but not files.create does not follow, and loses that
	// (CHANGELOG, the upgrade note).
	FilesEncrypt: FilesCreate,
}

// v049Catalogue is the catalogue of v0.49.0, the release that introduced
// saved roles: what an install knew before it recorded a catalogue of its
// own. ⚠ Frozen history — never edit it to follow the catalogue.
var v049Catalogue = []Perm{
	"files.download", "files.create", "files.modify", "files.rename", "files.move", "files.delete", "files.purge", "files.tag",
	"share.links", "share.upload_links", "share.users", "comments.write", "ai.use", "plugins.run",
	"access.webdav", "access.sftp", "access.ftp", "access.s3", "access.nfs", "access.api", "access.desktop",
	"account.edit",
	"admin.users", "admin.grants", "admin.shares", "admin.audit", "admin.monitor", "admin.full",
}

// listable is every key a saved list may hold: all but the role-only ones.
var listable = filter(func(d Def) bool { return !d.RoleOnly })

// builtinLists are the settings holding a built-in role's saved list, each
// with what that role can ever hold.
var builtinLists = []struct {
	key  string
	room Set
}{
	{model.SettingPermissionDefaults, listable},
	{model.SettingPermissionViewerDefaults, viewerCeiling},
}

// UpgradeReport is what one UpgradeCatalogue changed.
type UpgradeReport struct {
	// Added are the catalogue's keys the install had not known, in
	// catalogue order — with or without an older key to follow.
	Added []Perm
	// RolesChanged is how many custom roles were given an added key.
	RolesChanged int
	// DefaultsChanged: a built-in role's saved list was given one.
	DefaultsChanged bool
	// ExceptionsChanged is how many people's own exceptions were given one.
	ExceptionsChanged int
}

// UpgradeCatalogue gives every saved role, and every person's own exceptions,
// the keys this version added that they held through an older key
// (inheritOnAdd), and records the catalogue.
// Call it at start, after the migrations and before serving; a start that
// finds the catalogue it recorded changes nothing.
//
// All or nothing, in one transaction: the catalogue is recorded only together
// with every change it implies, so a start that fails part-way leaves the
// database as it found it and the next start does the whole of it again.
// ⚠ The server runs on after such a failure, and a list an administrator
// edits before the next start looks like one saved before the key existed:
// the retry gives it the key as well.
func UpgradeCatalogue(ctx context.Context, store db.Store) (UpgradeReport, error) {
	return upgradeCatalogue(ctx, store, inheritOnAdd)
}

// inheritance is one added key and the older key it follows.
type inheritance struct{ key, from Perm }

func upgradeCatalogue(ctx context.Context, store db.Store, inherit map[Perm]Perm) (UpgradeReport, error) {
	var rep UpgradeReport
	err := store.WithTx(ctx, func(ctx context.Context) error {
		known, recorded, err := knownCatalogue(ctx, store)
		if err != nil {
			return err
		}
		var follow []inheritance
		for _, d := range catalogue {
			if known[d.Key] {
				continue
			}
			rep.Added = append(rep.Added, d.Key)
			if from, ok := inherit[d.Key]; ok {
				follow = append(follow, inheritance{key: d.Key, from: from})
			}
		}
		if recorded && len(rep.Added) == 0 {
			// The catalogue the install already knows — or a later version's,
			// recorded before a downgrade: writing this one's over it would
			// forget the keys only the later version knows (recordCatalogue).
			return nil
		}
		if len(follow) > 0 {
			if rep.DefaultsChanged, err = upgradeBuiltinRoles(ctx, store, follow); err != nil {
				return err
			}
			if rep.RolesChanged, err = upgradeCustomRoles(ctx, store, follow); err != nil {
				return err
			}
			if rep.ExceptionsChanged, err = upgradeExceptions(ctx, store, follow); err != nil {
				return err
			}
		}
		return recordCatalogue(ctx, store, known)
	})
	if err != nil {
		return UpgradeReport{}, fmt.Errorf("perm: catalogue upgrade: %w", err)
	}
	if rep.DefaultsChanged || rep.RolesChanged > 0 || rep.ExceptionsChanged > 0 {
		// Every Loader (one per acl.Resolver) may hold the lists from
		// before; they must not answer from them for SnapshotTTL more.
		Invalidate()
	}
	return rep, nil
}

// knownCatalogue is the catalogue the install last recorded, and whether it
// recorded one at all; before it did, v0.49.0's.
func knownCatalogue(ctx context.Context, store db.Store) (map[Perm]bool, bool, error) {
	raw, err := store.GetSetting(ctx, model.SettingPermissionCatalogue)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && raw == "") {
		known := make(map[Perm]bool, len(v049Catalogue))
		for _, k := range v049Catalogue {
			known[k] = true
		}
		return known, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", model.SettingPermissionCatalogue, err)
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, false, fmt.Errorf("%s: %w", model.SettingPermissionCatalogue, err)
	}
	known := make(map[Perm]bool, len(keys))
	for _, k := range keys {
		known[Perm(k)] = true
	}
	return known, true, nil
}

// recordCatalogue stores the catalogue this start knows together with every
// key known before (known): this version's keys in catalogue order, then the
// others, sorted.
//
// ⚠ Only ever grows. A key known before and missing here is a later
// version's, recorded before a downgrade. Dropped, it would read as new when
// that version starts again, and be handed once more to every list that holds
// the key it follows: a permission an administrator took away in between
// would come back.
func recordCatalogue(ctx context.Context, store db.Store, known map[Perm]bool) error {
	keys := make([]string, 0, len(catalogue)+len(known))
	ours := make(map[Perm]bool, len(catalogue))
	for _, d := range catalogue {
		keys = append(keys, string(d.Key))
		ours[d.Key] = true
	}
	var later []string
	for k := range known {
		if !ours[k] {
			later = append(later, string(k))
		}
	}
	sort.Strings(later)
	keys = append(keys, later...)
	b, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return store.UpsertSetting(ctx, model.SettingPermissionCatalogue, string(b))
}

// upgradeBuiltinRoles gives the built-in roles' saved lists the keys they
// follow. A list never saved stays unsaved: the role reads its preset, which
// the catalogue builds with the added keys already in it.
func upgradeBuiltinRoles(ctx context.Context, store db.Store, follow []inheritance) (bool, error) {
	changed := false
	for _, b := range builtinLists {
		raw, err := store.GetSetting(ctx, b.key)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && raw == "") {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("%s: %w", b.key, err)
		}
		var list []string
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			return false, fmt.Errorf("%s: %w", b.key, err)
		}
		list, grew := inheritInto(list, follow, b.room)
		if !grew {
			continue
		}
		out, err := json.Marshal(list)
		if err != nil {
			return false, err
		}
		if err := store.UpsertSetting(ctx, b.key, string(out)); err != nil {
			return false, fmt.Errorf("%s: %w", b.key, err)
		}
		changed = true
	}
	return changed, nil
}

// upgradeCustomRoles gives every custom role — switched off or not, of any
// tenant — the keys it follows, in its own list and in its folder part.
func upgradeCustomRoles(ctx context.Context, store db.Store, follow []inheritance) (int, error) {
	rules, err := store.ListPermissionRules(ctx)
	if err != nil {
		return 0, fmt.Errorf("roles: %w", err)
	}
	n := 0
	for _, r := range rules {
		list, grewList := inheritInto(r.Permissions, follow, listable)
		grewPart := !r.Conditions.Empty() && inheritAllows(r.Effects, follow, conditionable)
		if !grewList && !grewPart {
			continue
		}
		r.Permissions = list
		if err := store.UpdatePermissionRule(ctx, r); err != nil {
			return 0, fmt.Errorf("role %d: %w", r.ID, err)
		}
		n++
	}
	return n, nil
}

// inheritInto returns list with every added key whose older key it allows —
// in order, so a key that follows another added key sees it — within room,
// and whether it grew. Keys the catalogue no longer knows stay as they were;
// they are ignored on read.
func inheritInto(list []string, follow []inheritance, room Set) ([]string, bool) {
	out := append([]string(nil), list...)
	has := make(map[string]bool, len(out))
	for _, k := range out {
		has[k] = true
	}
	grew := false
	for _, f := range follow {
		if has[string(f.from)] && !has[string(f.key)] && room.Has(f.key) {
			out = append(out, string(f.key))
			has[string(f.key)] = true
			grew = true
		}
	}
	return out, grew
}

// upgradeExceptions gives every person's own exceptions the keys they
// follow. The whole map is written back — app permission keys included — and
// updated_by is left empty: the change is the server's, not an
// administrator's.
func upgradeExceptions(ctx context.Context, store db.Store, follow []inheritance) (int, error) {
	all, err := store.ListUserPermissionOverrides(ctx)
	if err != nil {
		return 0, fmt.Errorf("exceptions: %w", err)
	}
	n := 0
	for userID, overrides := range all {
		if !inheritAllows(overrides, follow, listable) {
			continue
		}
		if err := store.SetUserPermissionOverrides(ctx, userID, overrides, nil); err != nil {
			return 0, fmt.Errorf("exceptions of user %d: %w", userID, err)
		}
		n++
	}
	return n, nil
}

// inheritAllows gives an Allow / Deny map — a role's folder part (its
// Effects, where its Conditions match) or a person's exceptions — an Allow of
// each added key whose older key it allows, within room (what the map may
// name at all), unless it already decides the added key. A Deny of the older
// key needs no twin: the added key's actions need the older key too, and it
// is denied there.
func inheritAllows(m map[string]string, follow []inheritance, room Set) bool {
	grew := false
	for _, f := range follow {
		if m[string(f.from)] != model.PermAllow || !room.Has(f.key) {
			continue
		}
		if _, decided := m[string(f.key)]; decided {
			continue
		}
		m[string(f.key)] = model.PermAllow
		grew = true
	}
	return grew
}
