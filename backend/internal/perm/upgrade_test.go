package perm_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// v049Standard is the built-in User role as v0.49.0 saved it: the Standard
// preset of the day, before files.encrypt existed.
func v049Standard() perm.Set { return perm.Standard.Without(perm.FilesEncrypt) }

// v049UploadOnly is the Upload-only preset as v0.49.0 saved it: files.create
// and account.edit, before files.encrypt existed.
func v049UploadOnly() perm.Set { return perm.Of(perm.FilesCreate, perm.AccountEdit) }

// v049Pin is what pinning a person to a preset wrote under v0.49.0: an
// exception for every permission an exception can name, allowing what the
// preset allows and denying the rest. files.encrypt is not among them — it did
// not exist.
func v049Pin(allowed perm.Set) map[string]string {
	pin := map[string]string{}
	for _, d := range perm.All() {
		if d.RoleOnly || d.Key == perm.FilesEncrypt {
			continue
		}
		effect := model.PermDeny
		if allowed.Has(d.Key) {
			effect = model.PermAllow
		}
		pin[string(d.Key)] = effect
	}
	return pin
}

// catalogueKeys is this version's catalogue, as UpgradeCatalogue records it.
func catalogueKeys() []string {
	var out []string
	for _, d := range perm.All() {
		out = append(out, string(d.Key))
	}
	return out
}

// recordCatalogueWithout stores a recorded catalogue that lacks keys, as if
// a version before them had written it.
func recordCatalogueWithout(t *testing.T, store db.Store, missing ...perm.Perm) {
	t.Helper()
	skip := map[string]bool{}
	for _, k := range missing {
		skip[string(k)] = true
	}
	var keys []string
	for _, k := range catalogueKeys() {
		if !skip[k] {
			keys = append(keys, k)
		}
	}
	b, err := json.Marshal(keys)
	require.NoError(t, err)
	require.NoError(t, store.UpsertSetting(context.Background(), model.SettingPermissionCatalogue, string(b)))
}

// recordedCatalogue reads the catalogue UpgradeCatalogue recorded.
func recordedCatalogue(t *testing.T, store db.Store) []string {
	t.Helper()
	raw, err := store.GetSetting(context.Background(), model.SettingPermissionCatalogue)
	require.NoError(t, err)
	var keys []string
	require.NoError(t, json.Unmarshal([]byte(raw), &keys))
	return keys
}

func mustRole(t *testing.T, store db.Store, r *model.PermissionRule) *model.PermissionRule {
	t.Helper()
	out, err := store.CreatePermissionRule(context.Background(), r)
	require.NoError(t, err)
	return out
}

func roleByID(t *testing.T, store db.Store, id int64) *model.PermissionRule {
	t.Helper()
	r, err := store.GetPermissionRule(context.Background(), id)
	require.NoError(t, err)
	return r
}

// A fresh install — or one that never saved a role — has nothing to carry
// over: the User role reads the Standard preset, which holds files.encrypt
// already. The start only records the catalogue.
func TestUpgradeCatalogue_NothingSavedChangesNothing(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	rep, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.Equal(t, []perm.Perm{perm.FilesEncrypt}, rep.Added, "v0.49.0's catalogue is the baseline when none is recorded")
	require.Zero(t, rep.RolesChanged)
	require.False(t, rep.DefaultsChanged)
	require.Zero(t, rep.ExceptionsChanged)

	_, err = store.GetSetting(ctx, model.SettingPermissionDefaults)
	require.ErrorIs(t, err, sql.ErrNoRows, "a User role nobody saved stays unsaved: it follows the preset")
	defaults, err := perm.LoadDefaults(ctx, store)
	require.NoError(t, err)
	require.Equal(t, perm.Standard, defaults)
	require.True(t, defaults.Has(perm.FilesEncrypt))
	require.Equal(t, catalogueKeys(), recordedCatalogue(t, store))
}

// The case the upgrade exists for: a User role saved under v0.49.0 allows
// files.create and would read files.encrypt — which did not exist then — as
// "not allowed". It is given files.encrypt, and nothing else changes.
func TestUpgradeCatalogue_SavedUserRoleWithCreateGainsEncrypt(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	saved := v049Standard().Without(perm.AccessSFTP) // narrowed by an administrator
	require.NoError(t, perm.SaveDefaults(ctx, store, saved))

	rep, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.True(t, rep.DefaultsChanged)
	require.Zero(t, rep.RolesChanged)

	defaults, err := perm.LoadDefaults(ctx, store)
	require.NoError(t, err)
	require.Equal(t, saved.With(perm.FilesEncrypt), defaults, "files.encrypt added; the administrator's narrowing kept")
}

// A User role that could not add files could not encrypt either, and still
// cannot.
func TestUpgradeCatalogue_SavedUserRoleWithoutCreateStaysWithout(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	saved := perm.Of(perm.FilesDownload, perm.AccountEdit)
	require.NoError(t, perm.SaveDefaults(ctx, store, saved))

	rep, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.False(t, rep.DefaultsChanged)

	defaults, err := perm.LoadDefaults(ctx, store)
	require.NoError(t, err)
	require.Equal(t, saved, defaults)
}

// Custom roles: the own list and the folder part each get files.encrypt where
// they allow files.create — in the folder part only as an Allow, and never
// over a decision the part already makes.
func TestUpgradeCatalogue_CustomRolesFollowCreate(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	editors := mustRole(t, store, &model.PermissionRule{
		Name: "Editors", Enabled: true, Names: map[string]string{"tr": "Düzenleyiciler"},
		Permissions: v049Standard().Without(perm.FilesDelete).Strings(),
		Targets:     []model.PermRuleTarget{{Kind: model.PermTargetSSOGroup, Value: "editors"}},
	})
	readers := mustRole(t, store, &model.PermissionRule{
		Name: "Readers", Enabled: true, Permissions: []string{"files.download", "account.edit"},
	})
	dropBox := mustRole(t, store, &model.PermissionRule{
		Name: "Drop box", Enabled: false, Permissions: []string{"files.download"},
		Effects:    map[string]string{"files.create": model.PermAllow},
		Conditions: model.PermRuleConditions{Paths: []string{"Drop"}},
	})
	archive := mustRole(t, store, &model.PermissionRule{
		Name: "Archive keepers", Enabled: true, Permissions: v049Standard().Strings(),
		Effects:    map[string]string{"files.create": model.PermDeny},
		Conditions: model.PermRuleConditions{Paths: []string{"Archive"}},
	})
	// Written by an administrator between a failed start and the next.
	decided := mustRole(t, store, &model.PermissionRule{
		Name: "No encryption in Clients", Enabled: true, Permissions: []string{"files.download"},
		Effects:    map[string]string{"files.create": model.PermAllow, "files.encrypt": model.PermDeny},
		Conditions: model.PermRuleConditions{Paths: []string{"Clients"}},
	})

	rep, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.Equal(t, 3, rep.RolesChanged, "editors, the drop box (switched off, still saved) and the archive keepers")
	require.False(t, rep.DefaultsChanged)

	e := roleByID(t, store, editors.ID)
	require.Equal(t, v049Standard().Without(perm.FilesDelete).With(perm.FilesEncrypt), perm.RoleSet(e))
	require.Equal(t, map[string]string{"tr": "Düzenleyiciler"}, e.Names, "the rest of the role is written back as it was")
	require.Equal(t, []model.PermRuleTarget{{Kind: model.PermTargetSSOGroup, Value: "editors"}}, e.Targets)

	require.Equal(t, perm.Of(perm.FilesDownload, perm.AccountEdit), perm.RoleSet(roleByID(t, store, readers.ID)), "never held files.create")

	d := roleByID(t, store, dropBox.ID)
	require.Equal(t, perm.Of(perm.FilesDownload), perm.RoleSet(d), "its list adds nothing; its folder part does")
	require.Equal(t, map[string]string{"files.create": model.PermAllow, "files.encrypt": model.PermAllow}, d.Effects)
	require.Equal(t, []string{"Drop"}, d.Conditions.Paths)

	a := roleByID(t, store, archive.ID)
	require.True(t, perm.RoleSet(a).Has(perm.FilesEncrypt))
	require.Equal(t, map[string]string{"files.create": model.PermDeny}, a.Effects,
		"a Deny of files.create needs no twin: encrypting there is creating there")

	require.Equal(t, map[string]string{"files.create": model.PermAllow, "files.encrypt": model.PermDeny},
		roleByID(t, store, decided.ID).Effects, "a part that already decides files.encrypt keeps its decision")
}

// Once: a start that finds the catalogue it recorded changes nothing — not
// even to give back a files.encrypt an administrator took away in between.
func TestUpgradeCatalogue_RunsOnce(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, perm.SaveDefaults(ctx, store, v049Standard()))
	role := mustRole(t, store, &model.PermissionRule{Name: "Editors", Enabled: true, Permissions: v049Standard().Strings()})
	ada, err := store.CreateUser(ctx, "ada@example.test", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	onlyCreate := map[string]string{"files.create": model.PermAllow}
	require.NoError(t, store.SetUserPermissionOverrides(ctx, ada.ID, onlyCreate, nil))

	first, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.True(t, first.DefaultsChanged)
	require.Equal(t, 1, first.RolesChanged)
	require.Equal(t, 1, first.ExceptionsChanged)

	// Encryption is not for them, the administrator decides.
	r := roleByID(t, store, role.ID)
	r.Permissions = perm.RoleSet(r).Without(perm.FilesEncrypt).Strings()
	require.NoError(t, store.UpdatePermissionRule(ctx, r))
	require.NoError(t, perm.SaveDefaults(ctx, store, perm.Standard.Without(perm.FilesEncrypt)))
	require.NoError(t, store.SetUserPermissionOverrides(ctx, ada.ID, onlyCreate, nil))

	second, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.Equal(t, perm.UpgradeReport{}, second)

	require.False(t, perm.RoleSet(roleByID(t, store, role.ID)).Has(perm.FilesEncrypt), "taken away stays taken away")
	defaults, err := perm.LoadDefaults(ctx, store)
	require.NoError(t, err)
	require.False(t, defaults.Has(perm.FilesEncrypt))
	own, err := store.GetUserPermissionOverrides(ctx, ada.ID)
	require.NoError(t, err)
	require.Equal(t, onlyCreate, own)
}

// A later version may have recorded a catalogue this one only partly knows,
// and a downgrade runs this start against it. Nothing is added, so nothing is
// written: the recorded set stays the later version's. Recording only what
// this version knows would make the later version, upgraded to again, take
// its own key for new and hand it once more to every list holding the key it
// follows — undoing an administrator who had taken it away in between. A key
// this start does add is recorded beside what was there, never instead of it.
func TestUpgradeCatalogue_TheRecordedCatalogueOnlyGrows(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	later := append(catalogueKeys(), "files.later")
	b, err := json.Marshal(later)
	require.NoError(t, err)
	require.NoError(t, store.UpsertSetting(ctx, model.SettingPermissionCatalogue, string(b)))
	// files.create without files.encrypt: taken away by an administrator.
	role := mustRole(t, store, &model.PermissionRule{Name: "Editors", Enabled: true, Permissions: v049Standard().Strings()})

	rep, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.Equal(t, perm.UpgradeReport{}, rep)
	require.Equal(t, later, recordedCatalogue(t, store), "the recorded catalogue shrank")
	require.False(t, perm.RoleSet(roleByID(t, store, role.ID)).Has(perm.FilesEncrypt), "an inheritance ran again")

	var withoutTag []string
	for _, k := range later {
		if k != string(perm.FilesTag) {
			withoutTag = append(withoutTag, k)
		}
	}
	b, err = json.Marshal(withoutTag)
	require.NoError(t, err)
	require.NoError(t, store.UpsertSetting(ctx, model.SettingPermissionCatalogue, string(b)))
	rep, err = perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.Equal(t, []perm.Perm{perm.FilesTag}, rep.Added)
	require.ElementsMatch(t, later, recordedCatalogue(t, store), "the key the later version knows was dropped")
}

// A key added with no older key to follow is new ground: recorded as known,
// given to nobody. files.tag stands in for it — the recorded catalogue lacks
// it, as if this start were the first to know it.
func TestUpgradeCatalogue_AKeyWithNothingToFollowIsRecordedOnly(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	recordCatalogueWithout(t, store, perm.FilesTag)
	saved := perm.Of(perm.FilesDownload, perm.FilesCreate, perm.FilesEncrypt)
	require.NoError(t, perm.SaveDefaults(ctx, store, saved))
	role := mustRole(t, store, &model.PermissionRule{Name: "Makers", Enabled: true, Permissions: saved.Strings()})

	rep, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.Equal(t, perm.UpgradeReport{Added: []perm.Perm{perm.FilesTag}}, rep)

	defaults, err := perm.LoadDefaults(ctx, store)
	require.NoError(t, err)
	require.Equal(t, saved, defaults)
	require.Equal(t, saved, perm.RoleSet(roleByID(t, store, role.ID)))
	require.Equal(t, catalogueKeys(), recordedCatalogue(t, store))
}

// A person's own exceptions follow the same rule. An exception that allows
// files.create gains an Allow of files.encrypt, so encryption survives for
// someone whose files.create comes from their exception over a role without
// it — and for someone pinned to a preset before files.encrypt existed. A
// Deny of files.create needs no twin, and an exception that already decides
// files.encrypt keeps its decision.
func TestUpgradeCatalogue_ExceptionsFollowCreate(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	readers := mustRole(t, store, &model.PermissionRule{Name: "Readers", Enabled: true, Permissions: []string{"files.download"}})
	person := func(email string, own map[string]string) *model.User {
		t.Helper()
		u, err := store.CreateUser(ctx, email, "x", model.RoleUser, "en", "UTC")
		require.NoError(t, err)
		require.NoError(t, store.SetUserCustomRole(ctx, u.ID, readers.ID))
		require.NoError(t, store.SetUserPermissionOverrides(ctx, u.ID, own, nil))
		return u
	}
	// Adds files through her own exception; her app decision rides along.
	ada := person("ada@example.test", map[string]string{"files.create": model.PermAllow, "app.sign.request": model.PermDeny})
	// May not add files: nothing to carry over.
	bob := person("bob@example.test", map[string]string{"files.create": model.PermDeny})
	// An administrator already decided encryption for her.
	cem := person("cem@example.test", map[string]string{"files.create": model.PermAllow, "files.encrypt": model.PermDeny})
	// Pinned to the Standard preset under v0.49.0: every permission an exception.
	dee := person("dee@example.test", v049Pin(v049Standard()))

	rep, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.Equal(t, 2, rep.ExceptionsChanged, "ada and dee")
	require.Zero(t, rep.RolesChanged, "Readers never held files.create")

	own := func(u *model.User) map[string]string {
		t.Helper()
		got, err := store.GetUserPermissionOverrides(ctx, u.ID)
		require.NoError(t, err)
		return got
	}
	can := func(u *model.User, p perm.Perm) bool {
		t.Helper()
		res, err := perm.NewLoader(store).Load(ctx, u)
		require.NoError(t, err)
		return res.Can(p)
	}
	require.Equal(t, map[string]string{"files.create": model.PermAllow, "files.encrypt": model.PermAllow, "app.sign.request": model.PermDeny}, own(ada))
	require.True(t, can(ada, perm.FilesEncrypt), "her role lacks files.create; her exception carries encryption over")
	require.Equal(t, map[string]string{"files.create": model.PermDeny}, own(bob))
	require.False(t, can(bob, perm.FilesEncrypt))
	require.Equal(t, map[string]string{"files.create": model.PermAllow, "files.encrypt": model.PermDeny}, own(cem))
	require.False(t, can(cem, perm.FilesEncrypt))
	require.Equal(t, model.PermAllow, own(dee)["files.encrypt"], "a pinned Standard is today's Standard")
	require.True(t, can(dee, perm.FilesEncrypt))
}

// v0.49.0's Upload-only could encrypt — files.create was all it took — and the
// upgrade keeps that: the access, and the label. The User role, a custom role
// and a person's pin that were Upload-only before are Upload-only after, not
// "custom".
func TestUpgradeCatalogue_AnUploadOnlyListKeepsItsPreset(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, perm.SaveDefaults(ctx, store, v049UploadOnly()))
	dropBox := mustRole(t, store, &model.PermissionRule{Name: "Drop box", Enabled: true, Permissions: v049UploadOnly().Strings()})
	// Pinned over a role that never allowed files.create, so only her own
	// exception can carry encryption over.
	readers := mustRole(t, store, &model.PermissionRule{Name: "Readers", Enabled: true, Permissions: []string{"files.download"}})
	ada, err := store.CreateUser(ctx, "ada@example.test", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, store.SetUserCustomRole(ctx, ada.ID, readers.ID))
	require.NoError(t, store.SetUserPermissionOverrides(ctx, ada.ID, v049Pin(v049UploadOnly()), nil))

	rep, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.True(t, rep.DefaultsChanged, "the User role")
	require.Equal(t, 1, rep.RolesChanged, "the drop box; Readers never held files.create")
	require.Equal(t, 1, rep.ExceptionsChanged, "ada's pin")

	defaults, err := perm.LoadDefaults(ctx, store)
	require.NoError(t, err)
	require.Equal(t, perm.PresetUploadOnly, perm.MatchPreset(defaults), "the User role, saved as Upload-only")
	require.Equal(t, perm.PresetUploadOnly, perm.MatchPreset(perm.RoleSet(roleByID(t, store, dropBox.ID))), "a custom role with the same list")
	res, err := perm.NewLoader(store).Load(ctx, ada)
	require.NoError(t, err)
	require.Equal(t, perm.PresetUploadOnly, perm.MatchPreset(res.Allowed), "a person pinned to it")
}

// failingRoleWrites is a store whose role writes fail — a full disk halfway
// through the upgrade.
type failingRoleWrites struct{ db.Store }

func (failingRoleWrites) UpdatePermissionRule(context.Context, *model.PermissionRule) error {
	return errors.New("disk full")
}

// ⚠ All or nothing: a failure part-way leaves no list changed and no
// catalogue recorded — the User role's change, made before the failing role
// write, is rolled back with it — so the next start does the whole upgrade.
func TestUpgradeCatalogue_AFailureChangesNothingAndIsRetried(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, perm.SaveDefaults(ctx, store, v049Standard()))
	mustRole(t, store, &model.PermissionRule{Name: "Editors", Enabled: true, Permissions: v049Standard().Strings()})

	_, err := perm.UpgradeCatalogue(ctx, failingRoleWrites{store})
	require.ErrorContains(t, err, "disk full")

	_, err = store.GetSetting(ctx, model.SettingPermissionCatalogue)
	require.ErrorIs(t, err, sql.ErrNoRows, "not recorded: the next start retries")
	defaults, err := perm.LoadDefaults(ctx, store)
	require.NoError(t, err)
	require.False(t, defaults.Has(perm.FilesEncrypt), "rolled back with the rest")

	rep, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.True(t, rep.DefaultsChanged)
	require.Equal(t, 1, rep.RolesChanged)
}

// A Loader that cached the saved lists before the upgrade — every
// acl.Resolver holds one — answers with files.encrypt at once, not
// SnapshotTTL later.
func TestUpgradeCatalogue_DropsCachedRoles(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, perm.SaveDefaults(ctx, store, v049Standard()))
	ada, err := store.CreateUser(ctx, "ada@example.test", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	l := perm.NewLoader(store)
	res, err := l.Load(ctx, ada)
	require.NoError(t, err)
	require.False(t, res.Can(perm.FilesEncrypt))

	_, err = perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)

	res, err = l.Load(ctx, ada)
	require.NoError(t, err)
	require.True(t, res.Can(perm.FilesEncrypt))
}

// The mechanism is not files.encrypt's alone. files.tag stands in for a key
// a later version adds, carved out of files.download: both built-in roles
// that allow files.download are given it — the Viewer role too, since a
// viewer may hold it. A key a viewer can never hold is not written into the
// Viewer role (files.encrypt, following files.download here only for the
// test).
func TestUpgradeCatalogue_BuiltinRolesWithinWhatTheyCanHold(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	recordCatalogueWithout(t, store, perm.FilesEncrypt, perm.FilesTag)
	require.NoError(t, perm.SaveDefaults(ctx, store, perm.Of(perm.FilesDownload)))
	require.NoError(t, perm.SaveRoleBase(ctx, store, model.RoleViewer, perm.Of(perm.FilesDownload)))

	rep, err := perm.UpgradeCatalogueWith(ctx, store, map[perm.Perm]perm.Perm{
		perm.FilesTag:     perm.FilesDownload,
		perm.FilesEncrypt: perm.FilesDownload,
	})
	require.NoError(t, err)
	require.Equal(t, []perm.Perm{perm.FilesEncrypt, perm.FilesTag}, rep.Added, "catalogue order")
	require.True(t, rep.DefaultsChanged)

	user, err := perm.LoadDefaults(ctx, store)
	require.NoError(t, err)
	require.Equal(t, perm.Of(perm.FilesDownload, perm.FilesEncrypt, perm.FilesTag), user)
	viewer, err := perm.LoadRoleBase(ctx, store, model.RoleViewer)
	require.NoError(t, err)
	require.Equal(t, perm.Of(perm.FilesDownload, perm.FilesTag), viewer)
	raw, err := store.GetSetting(ctx, model.SettingPermissionViewerDefaults)
	require.NoError(t, err)
	require.NotContains(t, raw, string(perm.FilesEncrypt), "never written where a viewer cannot hold it")
}
