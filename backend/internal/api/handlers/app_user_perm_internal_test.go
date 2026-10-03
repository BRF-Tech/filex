package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// An app's own user permission ("Request signatures", manifest
// `user_permissions` + `requires`) is asked of the caller on every door that
// starts the app's work (appUserPermOK), decided by perm.Result.AppAllowed.
// The maintainer 2026-09-28: "imza isteme bir yetki arkasında olmalı".

func appPermFixture(t *testing.T) (db.Store, *AppPlugins, *wasmplugin.Installed) {
	t.Helper()
	drv := db.MustGet("sqlite")
	conn, err := drv.Open(context.Background(), "file:app_user_perm_"+t.Name()+"?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, db.Migrate(context.Background(), drv, conn))
	store := drv.NewStore(conn)
	t.Cleanup(perm.Invalidate)

	p := &wasmplugin.Installed{
		Row: &model.AppPlugin{Name: "sign"},
		Manifest: &wasmplugin.Manifest{Manifest: wire.Manifest{
			Name:  "sign",
			Label: wire.Text{"en": "Sign", "tr": "İmza"},
			UserPermissions: []wire.UserPermission{{
				ID:      "request",
				Label:   wire.Text{"en": "Request signatures", "tr": "İmza isteme"},
				Default: "user",
			}},
		}},
	}
	return store, &AppPlugins{Store: store, ACL: acl.New(store)}, p
}

func askAppPerm(t *testing.T, h *AppPlugins, p *wasmplugin.Installed, u *model.User, lang string) (bool, int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/files/plugins/actions/sign/request/run", nil)
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	req = req.WithContext(auth.WithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	ok := h.appUserPermOK(rec, req, p, "request")
	var body map[string]any
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	}
	return ok, rec.Code, body
}

func TestAppUserPerm_DefaultRoleExceptionAndBuiltIn(t *testing.T) {
	store, h, p := appPermFixture(t)
	ctx := context.Background()
	user, err := store.CreateUser(ctx, "user@test.local", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	viewer, err := store.CreateUser(ctx, "viewer@test.local", "x", model.RoleViewer, "en", "UTC")
	require.NoError(t, err)
	admin, err := store.CreateUser(ctx, "admin@test.local", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)

	// The app's default ("user"): a user may, a viewer may not.
	ok, _, _ := askAppPerm(t, h, p, user, "")
	assert.True(t, ok, "a user holds a user-default app permission")
	ok, code, body := askAppPerm(t, h, p, viewer, "")
	assert.False(t, ok)
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, "permission_denied", body["error"])
	assert.Equal(t, "app.sign.request", body["permission"])
	assert.Contains(t, body["message"], "Request signatures", "the refusal names the app's own label")

	// …in the reader's language (the account's own, as every refusal).
	trViewer, err := store.CreateUser(ctx, "okur@test.local", "x", model.RoleViewer, "tr", "UTC")
	require.NoError(t, err)
	_, _, body = askAppPerm(t, h, p, trViewer, "")
	assert.Contains(t, body["message"], "İmza isteme")

	// The built-in User role's decision takes it away from every user…
	require.NoError(t, perm.SaveAppDecisions(ctx, store, model.RoleUser, map[string]string{"app.sign.request": model.PermDeny}))
	ok, _, body = askAppPerm(t, h, p, user, "")
	assert.False(t, ok, "the built-in role's deny beats the app default")
	assert.Equal(t, "base", body["source"].(map[string]any)["kind"])

	// …a person's exception gives it back to one…
	require.NoError(t, store.SetUserPermissionOverrides(ctx, user.ID, map[string]string{"app.sign.request": model.PermAllow}, nil))
	perm.Invalidate()
	ok, _, _ = askAppPerm(t, h, p, user, "")
	assert.True(t, ok, "the person's exception beats the built-in role")

	// …and an administrator always may.
	ok, _, _ = askAppPerm(t, h, p, admin, "")
	assert.True(t, ok)

	// An action that requires nothing is never asked.
	req := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(auth.WithUser(context.Background(), viewer))
	assert.True(t, h.appUserPermOK(httptest.NewRecorder(), req, p, ""))
}

// What an app is told the person holds (wire.Actor.Permissions, heldAppPerms)
// is the door's own answer (appUserPermOK), permission by permission, through
// every layer that decides it: the app default, the built-in role, the
// person's exception, the administrator. One question, asked once
// (appPermHeld) — an app is never told "you may" by a list the door
// disagrees with.
func TestAppUserPerm_TheAppIsToldWhatTheDoorDecides(t *testing.T) {
	store, h, p := appPermFixture(t)
	p.Manifest.UserPermissions = append(p.Manifest.UserPermissions,
		wire.UserPermission{ID: "audit", Label: wire.Text{"en": "Read the audit trail", "tr": "Denetim izini okuma"}, Default: "admin"},
		wire.UserPermission{ID: "verify", Label: wire.Text{"en": "Verify", "tr": "Doğrulama"}, Default: "viewer"})
	ctx := context.Background()
	user, err := store.CreateUser(ctx, "user@test.local", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	viewer, err := store.CreateUser(ctx, "viewer@test.local", "x", model.RoleViewer, "en", "UTC")
	require.NoError(t, err)
	admin, err := store.CreateUser(ctx, "admin@test.local", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)

	told := func(u *model.User) []string {
		t.Helper()
		held := h.heldAppPerms(ctx, u, p)
		for _, up := range p.Manifest.UserPermissions {
			req := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(auth.WithUser(ctx, u))
			door := h.appUserPermOK(httptest.NewRecorder(), req, p, up.ID)
			assert.Equal(t, door, slices.Contains(held, up.ID), "%s / %s: the app is told %v, the door says %v", u.Email, up.ID, held, door)
		}
		return held
	}

	assert.Equal(t, []string{"request", "verify"}, told(user), "the app's defaults: user, admin, viewer")
	assert.Equal(t, []string{"verify"}, told(viewer))
	assert.Equal(t, []string{"request", "audit", "verify"}, told(admin), "an administrator holds every one")

	require.NoError(t, perm.SaveAppDecisions(ctx, store, model.RoleUser, map[string]string{"app.sign.request": model.PermDeny}))
	assert.Equal(t, []string{"verify"}, told(user), "the built-in role took it away")

	require.NoError(t, store.SetUserPermissionOverrides(ctx, user.ID,
		map[string]string{"app.sign.request": model.PermAllow, "app.sign.audit": model.PermAllow}, nil))
	perm.Invalidate()
	assert.Equal(t, []string{"request", "audit", "verify"}, told(user), "the person's exceptions")

	// Nobody, and an app with nothing to hold, are told nothing.
	assert.Nil(t, h.heldAppPerms(ctx, nil, p))
	bare := &wasmplugin.Installed{Row: &model.AppPlugin{Name: "echo"}, Manifest: &wasmplugin.Manifest{Manifest: wire.Manifest{Name: "echo"}}}
	assert.Nil(t, h.heldAppPerms(ctx, user, bare))
}

// The exceptions answer an administrator reads (GET/PUT
// /api/admin/users/{id}/exceptions) carries every installed app permission:
// the account's answer with its source, and the answer WITHOUT the person's
// own exception — what the editor's "Default" choice stands for.
func TestAppUserPerm_ExceptionsAnswerCarriesApps(t *testing.T) {
	store, h, _ := appPermFixture(t)
	ctx := context.Background()
	user, err := store.CreateUser(ctx, "user@test.local", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, store.SetUserPermissionOverrides(ctx, user.ID, map[string]string{"app.sign.request": model.PermDeny}, nil))
	perm.Invalidate()

	type row struct {
		Key       string      `json:"key"`
		Allowed   bool        `json:"allowed"`
		Source    perm.Source `json:"source"`
		Inherited struct {
			Allowed bool        `json:"allowed"`
			Source  perm.Source `json:"source"`
		} `json:"inherited"`
	}
	answer := func(ph *PermissionsAdmin) []row {
		rec := httptest.NewRecorder()
		ph.writeUserPermissions(rec, httptest.NewRequest(http.MethodGet, "/", nil), user)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body struct {
			Effective struct {
				Apps []row `json:"apps"`
			} `json:"effective"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return body.Effective.Apps
	}

	ph := &PermissionsAdmin{Store: store, ACL: h.ACL, AppPermissions: func() []wasmplugin.UserPermRow {
		return []wasmplugin.UserPermRow{{Key: "app.sign.request", App: "sign", ID: "request", Default: "user"}}
	}}
	apps := answer(ph)
	require.Len(t, apps, 1)
	assert.Equal(t, "app.sign.request", apps[0].Key)
	assert.False(t, apps[0].Allowed, "the person's deny")
	assert.Equal(t, perm.SourceOverride, apps[0].Source.Kind)
	assert.True(t, apps[0].Inherited.Allowed, "without it the app's user default holds")
	assert.Equal(t, perm.SourceAppDefault, apps[0].Inherited.Source.Kind)

	// Apps off: no apps key at all, the rest of the answer as before.
	assert.Empty(t, answer(&PermissionsAdmin{Store: store, ACL: h.ACL}))
}

// The catalogue (GET /api/admin/roles/catalogue) says, for each installed app
// permission, what its default comes to on each built-in role —
// apps[].default_for — so the role editors label "Default (allowed / not
// allowed)" from the server's answer and keep no copy of the rule. What it
// says must be what a real account on that role, with nothing decided, is
// answered on the doors (ACL.Perms → AppAllowed).
func TestAppUserPerm_CatalogueSaysWhatDefaultComesTo(t *testing.T) {
	store, h, _ := appPermFixture(t)
	ctx := context.Background()
	rows := []wasmplugin.UserPermRow{
		{Key: "app.sign.verify", App: "sign", ID: "verify", Default: "viewer"},
		{Key: "app.sign.request", App: "sign", ID: "request", Default: "user"},
		{Key: "app.sign.audit", App: "sign", ID: "audit", Default: "admin"},
	}
	ph := &PermissionsAdmin{Store: store, ACL: h.ACL, AppPermissions: func() []wasmplugin.UserPermRow { return rows }}

	rec := httptest.NewRecorder()
	ph.Catalogue(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Apps []struct {
			Key        string          `json:"key"`
			Default    string          `json:"default"`
			DefaultFor map[string]bool `json:"default_for"`
		} `json:"apps"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Apps, len(rows))

	accounts := map[string]*model.User{}
	for _, role := range []string{model.RoleViewer, model.RoleUser, model.RoleAdmin} {
		u, err := store.CreateUser(ctx, role+"@test.local", "x", role, "en", "UTC")
		require.NoError(t, err)
		accounts[role] = u
	}
	for i, app := range body.Apps {
		assert.Equal(t, rows[i].Key, app.Key)
		assert.Equal(t, rows[i].Default, app.Default, "the manifest's default is still there")
		require.Len(t, app.DefaultFor, 3, "%s: one answer per built-in role", app.Key)
		for role, u := range accounts {
			res, err := h.ACL.Perms(ctx, u)
			require.NoError(t, err)
			held, _ := res.AppAllowed(app.Key, perm.AppDefault(app.Default))
			assert.Equal(t, held, app.DefaultFor[role], "%s on %s: the catalogue says what the doors answer", app.Key, role)
		}
	}
	assert.Equal(t, map[string]bool{"viewer": false, "user": true, "admin": true}, body.Apps[1].DefaultFor)

	// Apps off: an empty list, not null.
	rec = httptest.NewRecorder()
	(&PermissionsAdmin{Store: store, ACL: h.ACL}).Catalogue(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Contains(t, rec.Body.String(), `"apps":[]`)
}
