package handlers_test

// Integration tests for the RBAC permissions + self-service token endpoints,
// exercised through the real router (auth middleware + confine + acl included).

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// doReq is a tiny helper: method + JSON body → (status, raw response bytes).
func doReq(t *testing.T, client *http.Client, method, url string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// freshClient returns a new cookie-jar client so we can hold several logged-in
// principals at once.
func freshClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Jar: jar}
}

// createUser makes an account of the given role via the admin API and returns
// its id.
func createUser(t *testing.T, url string, admin *http.Client, email, pw, role string) int64 {
	t.Helper()
	st, raw := doReq(t, admin, http.MethodPost, url+"/api/admin/users",
		map[string]any{"email": email, "password": pw, "role": role})
	require.Equal(t, http.StatusOK, st, "create user %s: %s", email, raw)
	var u struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(raw, &u))
	require.NotZero(t, u.ID)
	return u.ID
}

func TestRBAC_Grants_And_SelfTokens(t *testing.T) {
	srv, adminClient, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, adminClient, email, pw)

	// RBAC-enabled storage.
	st, raw := doReq(t, adminClient, http.MethodPost, srv.URL+"/api/admin/storages", model.Storage{
		Name:          "s1",
		Driver:        "local",
		MountPath:     "/data",
		ConfigJSON:    json.RawMessage(`{"root":"/tmp/filex-grants-test"}`),
		SyncMode:      model.SyncModePoll,
		SyncIntervalS: 900,
		Enabled:       true,
		RBACEnabled:   true,
	})
	require.Equal(t, http.StatusOK, st, "create storage: %s", raw)

	uid := createUser(t, srv.URL, adminClient, "u@test.local", "UserPass1!", model.RoleUser)
	vid := createUser(t, srv.URL, adminClient, "v@test.local", "ViewPass1!", model.RoleViewer)

	// Admin (owner-exempt) grants.
	st, raw = doReq(t, adminClient, http.MethodPost, srv.URL+"/api/files/permissions",
		map[string]any{"path": "s1://alfa", "user_id": uid, "level": "owner"})
	require.Equal(t, http.StatusOK, st, "grant owner: %s", raw)
	st, _ = doReq(t, adminClient, http.MethodPost, srv.URL+"/api/files/permissions",
		map[string]any{"path": "s1://beta", "user_id": vid, "level": "viewer"})
	require.Equal(t, http.StatusOK, st)

	// Viewer-account ceiling: cannot grant a viewer editor.
	st, _ = doReq(t, adminClient, http.MethodPost, srv.URL+"/api/files/permissions",
		map[string]any{"path": "s1://beta", "user_id": vid, "level": "editor"})
	assert.Equal(t, http.StatusBadRequest, st, "viewer account cannot get editor grant")

	// Admin global grants overview lists both.
	st, raw = doReq(t, adminClient, http.MethodGet, srv.URL+"/api/admin/grants", nil)
	require.Equal(t, http.StatusOK, st)
	var gl struct {
		Grants []map[string]any `json:"grants"`
	}
	require.NoError(t, json.Unmarshal(raw, &gl))
	assert.GreaterOrEqual(t, len(gl.Grants), 2, "admin sees all grants")

	// Permissions panel GET: owner-only. Admin (exempt) → 200.
	st, _ = doReq(t, adminClient, http.MethodGet, srv.URL+"/api/files/permissions?path=s1://alfa", nil)
	assert.Equal(t, http.StatusOK, st)

	// The 'user' account is OWNER on alfa → can read the panel there…
	userClient := freshClient(t)
	testutil.LoginAs(t, srv, userClient, "u@test.local", "UserPass1!")
	st, _ = doReq(t, userClient, http.MethodGet, srv.URL+"/api/files/permissions?path=s1://alfa", nil)
	assert.Equal(t, http.StatusOK, st, "owner reads its own permissions panel")
	// …but NOT on beta (no grant there).
	st, _ = doReq(t, userClient, http.MethodGet, srv.URL+"/api/files/permissions?path=s1://beta", nil)
	assert.Equal(t, http.StatusForbidden, st, "non-owner cannot read another path's panel")

	// Self-service tokens: user may mint read/write, never admin.
	st, _ = doReq(t, userClient, http.MethodPost, srv.URL+"/api/tokens",
		map[string]any{"label": "u", "scopes": "read,write"})
	assert.Equal(t, http.StatusCreated, st, "user read/write token ok")
	st, _ = doReq(t, userClient, http.MethodPost, srv.URL+"/api/tokens",
		map[string]any{"label": "esc", "scopes": "admin"})
	assert.Equal(t, http.StatusForbidden, st, "admin-scope self token rejected")

	// Viewer: read-only tokens only, and cannot read the permissions panel.
	viewClient := freshClient(t)
	testutil.LoginAs(t, srv, viewClient, "v@test.local", "ViewPass1!")
	st, _ = doReq(t, viewClient, http.MethodPost, srv.URL+"/api/tokens",
		map[string]any{"label": "vw", "scopes": "read,write"})
	assert.Equal(t, http.StatusForbidden, st, "viewer write token rejected")
	st, _ = doReq(t, viewClient, http.MethodPost, srv.URL+"/api/tokens",
		map[string]any{"label": "vr", "scopes": "read"})
	assert.Equal(t, http.StatusCreated, st, "viewer read token ok")
	st, _ = doReq(t, viewClient, http.MethodGet, srv.URL+"/api/files/permissions?path=s1://beta", nil)
	assert.Equal(t, http.StatusForbidden, st, "viewer cannot manage permissions")

	// Non-admin cannot hit the admin grants overview.
	st, _ = doReq(t, userClient, http.MethodGet, srv.URL+"/api/admin/grants", nil)
	assert.Equal(t, http.StatusForbidden, st)

	// share-mail names a LINK (share_mail.go): the admin may mail one on
	// beta (503 here since no SMTP is configured), the viewer, who manages no
	// link there, is refused.
	ctx := context.Background()
	s1, err := store.GetStorageByName(ctx, "s1")
	require.NoError(t, err)
	admin, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)
	betaDoc, err := store.CreateNode(ctx, &model.Node{
		StorageID: s1.ID, Name: "b.txt", Path: "/beta/b.txt", PathHash: mutTestPathHash(s1.ID, "/beta/b.txt"),
		Type: model.NodeTypeFile, Size: 1,
	})
	require.NoError(t, err)
	link, err := share.NewService(store).Create(ctx, share.CreateOpts{NodeID: betaDoc.ID, CreatedBy: &admin.ID})
	require.NoError(t, err)
	st, _ = doReq(t, adminClient, http.MethodPost, srv.URL+"/api/files/permissions/share-mail",
		map[string]any{"share": link.Token, "email": "x@test.local"})
	assert.Equal(t, http.StatusServiceUnavailable, st, "admin share-mail: no SMTP → 503, not 403")
	st, _ = doReq(t, viewClient, http.MethodPost, srv.URL+"/api/files/permissions/share-mail",
		map[string]any{"share": link.Token, "email": "x@test.local"})
	assert.Equal(t, http.StatusForbidden, st, "viewer cannot share-mail")

	// is_dir is the ITEM's, read from the catalogue — not the request's
	// (C11a). A catalogued file asked for as a folder is a file grant; a
	// catalogued folder asked for as a file is a folder grant.
	_, err = store.CreateNode(context.Background(), &model.Node{
		StorageID: s1.ID, Name: "doc.txt", Path: "/alfa/doc.txt", PathHash: mutTestPathHash(s1.ID, "/alfa/doc.txt"),
		Type: model.NodeTypeFile, Size: 3,
	})
	require.NoError(t, err)
	_, err = store.CreateNode(context.Background(), &model.Node{
		StorageID: s1.ID, Name: "alt", Path: "/alfa/alt", PathHash: mutTestPathHash(s1.ID, "/alfa/alt"),
		Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	var fg struct {
		IsDir bool `json:"is_dir"`
	}
	st, raw = doReq(t, adminClient, http.MethodPost, srv.URL+"/api/files/permissions",
		map[string]any{"path": "s1://alfa/doc.txt", "user_id": uid, "level": "viewer", "is_dir": true})
	require.Equal(t, http.StatusOK, st, "file grant: %s", raw)
	require.NoError(t, json.Unmarshal(raw, &fg))
	assert.False(t, fg.IsDir, "a file's grant is a file grant whatever the request says")
	st, raw = doReq(t, adminClient, http.MethodPost, srv.URL+"/api/files/permissions",
		map[string]any{"path": "s1://alfa/alt", "user_id": uid, "level": "viewer", "is_dir": false})
	require.Equal(t, http.StatusOK, st, "folder grant: %s", raw)
	require.NoError(t, json.Unmarshal(raw, &fg))
	assert.True(t, fg.IsDir, "a folder's grant is a folder grant whatever the request says")
}
