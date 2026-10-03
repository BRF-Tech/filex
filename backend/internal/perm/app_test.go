package perm

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestAppAllowed_Layers — an app permission is decided by, highest first: the
// administrator role, the person's exception, their custom role, the built-in
// role's decision, and the app's own default.
func TestAppAllowed_Layers(t *testing.T) {
	const key = "app.sign.request"

	user := Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard})
	viewer := Resolve(Input{UserID: 2, Role: model.RoleViewer})
	admin := Resolve(Input{UserID: 3, Role: model.RoleAdmin})

	// The app's default.
	ok, src := user.AppAllowed(key, AppDefaultUser)
	assert.True(t, ok)
	assert.Equal(t, SourceAppDefault, src.Kind)
	ok, _ = viewer.AppAllowed(key, AppDefaultUser)
	assert.False(t, ok, "a viewer does not hold a user-default app permission")
	ok, _ = viewer.AppAllowed(key, AppDefaultViewer)
	assert.True(t, ok)
	ok, _ = user.AppAllowed(key, AppDefaultAdmin)
	assert.False(t, ok, "an admin-default permission waits to be granted")
	ok, _ = admin.AppAllowed(key, AppDefaultAdmin)
	assert.True(t, ok, "an administrator always may")

	// The built-in role's decision beats the default.
	denied := Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, AppDecisions: map[string]string{key: model.PermDeny}})
	ok, src = denied.AppAllowed(key, AppDefaultUser)
	assert.False(t, ok)
	assert.Equal(t, SourceBase, src.Kind)

	// The custom role beats the built-in role.
	role := &model.PermissionRule{ID: 7, Name: "İmza masası", Enabled: true, Permissions: Standard.Strings(),
		Settings: model.PermRuleSettings{Apps: map[string]string{key: model.PermAllow}}}
	held := Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 7, Rules: []*model.PermissionRule{role},
		AppDecisions: map[string]string{key: model.PermDeny}})
	ok, src = held.AppAllowed(key, AppDefaultUser)
	assert.True(t, ok)
	assert.Equal(t, SourceRule, src.Kind)
	assert.Equal(t, int64(7), src.RuleID)

	// A switched-off role says nothing about apps.
	off := *role
	off.Enabled = false
	offRes := Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 7, Rules: []*model.PermissionRule{&off}})
	_, src = offRes.AppAllowed(key, AppDefaultUser)
	assert.Equal(t, SourceAppDefault, src.Kind)

	// The person's exception beats everything but the administrator role.
	mine := Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 7, Rules: []*model.PermissionRule{role},
		Overrides: map[string]string{key: model.PermDeny}})
	ok, src = mine.AppAllowed(key, AppDefaultUser)
	assert.False(t, ok)
	assert.Equal(t, SourceOverride, src.Kind)

	// …and what the account would get without it is its role's answer —
	// what "Default" stands for on the person's exceptions editor.
	ok, src = mine.AppInherited(key, AppDefaultUser)
	assert.True(t, ok)
	assert.Equal(t, SourceRule, src.Kind)
	ok, src = Resolve(Input{UserID: 2, Role: model.RoleViewer, Overrides: map[string]string{key: model.PermAllow}}).AppInherited(key, AppDefaultUser)
	assert.False(t, ok, "a viewer's inherited answer is the app default's")
	assert.Equal(t, SourceAppDefault, src.Kind)
	ok, _ = admin.AppInherited(key, AppDefaultAdmin)
	assert.True(t, ok)
}

// TestAppDefaultFor — what the catalogue tells the role editors an app
// default comes to on each built-in role (apps[].default_for). It is the
// last layer of AppAllowed, so an account on that role with nothing decided
// anywhere gets exactly this; a built-in role's own decision is a layer
// above it and is not in it.
func TestAppDefaultFor(t *testing.T) {
	want := map[AppDefault]map[string]bool{
		AppDefaultViewer: {model.RoleViewer: true, model.RoleUser: true, model.RoleAdmin: true},
		AppDefaultUser:   {model.RoleViewer: false, model.RoleUser: true, model.RoleAdmin: true},
		AppDefaultAdmin:  {model.RoleViewer: false, model.RoleUser: false, model.RoleAdmin: true},
		// A default nobody knows is nobody's but an administrator's.
		AppDefault("everyone"): {model.RoleViewer: false, model.RoleUser: false, model.RoleAdmin: true},
	}
	for def, roles := range want {
		assert.Equal(t, roles, AppDefaultFor(def), "default %q", def)
	}

	// The same answer an account on each role gets from AppAllowed, whatever
	// the key and whatever else the account holds.
	const key = "app.sign.request"
	for _, def := range []AppDefault{AppDefaultViewer, AppDefaultUser, AppDefaultAdmin} {
		table := AppDefaultFor(def)
		for role, res := range map[string]*Result{
			model.RoleViewer: Resolve(Input{UserID: 2, Role: model.RoleViewer}),
			model.RoleUser:   Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard}),
			model.RoleAdmin:  Resolve(Input{UserID: 3, Role: model.RoleAdmin}),
		} {
			ok, src := res.AppAllowed(key, def)
			assert.Equal(t, table[role], ok, "%s on %s", def, role)
			if role != model.RoleAdmin {
				assert.Equal(t, SourceAppDefault, src.Kind)
			}
		}
	}

	// A built-in role's decision is not the app's default: the table stays.
	denied := Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, AppDecisions: map[string]string{key: model.PermDeny}})
	ok, _ := denied.AppAllowed(key, AppDefaultUser)
	assert.False(t, ok)
	assert.True(t, AppDefaultFor(AppDefaultUser)[model.RoleUser])
}

func TestAppKeys_Validation(t *testing.T) {
	assert.True(t, IsAppKey("app.sign.request"))
	assert.True(t, IsAppKey(AppKey("filex-convert", "run")))
	for _, bad := range []string{"app.sign", "sign.request", "app.Sign.request", "app..x", "app.sign.request.more", "files.delete"} {
		assert.False(t, IsAppKey(bad), bad)
	}
	// Overrides and rule settings take app keys, allow/deny only.
	assert.NoError(t, ValidateEffects(map[string]string{"app.sign.request": model.PermDeny, "files.delete": model.PermDeny}))
	assert.Error(t, ValidateEffects(map[string]string{"app.sign.request": "maybe"}))
	r := &model.PermissionRule{Name: "x", Settings: model.PermRuleSettings{Apps: map[string]string{"app.sign.request": model.PermAllow}}}
	assert.NoError(t, NormalizeRule(r))
	r = &model.PermissionRule{Name: "x", Settings: model.PermRuleSettings{Apps: map[string]string{"files.delete": model.PermAllow}}}
	assert.Error(t, NormalizeRule(r), "a catalogue key is not an app decision")
	// A role's own list stays catalogue-only: app decisions live in settings.
	r = &model.PermissionRule{Name: "x", Permissions: []string{"app.sign.request"}}
	assert.Error(t, NormalizeRule(r))
}
