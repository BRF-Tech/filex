package dav

// Per-user permissions (internal/perm) over WebDAV: access.webdav at every
// request's authentication, and each method on the action it performs.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

const (
	permUser = "perm@test.local"
	permPass = "PermPass!1"
)

// permHarness is a storage holding report.txt and a plain user denied ps.
func permHarness(t *testing.T, ps ...perm.Perm) (*harness, string) {
	t.Helper()
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	var cfg map[string]string
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	root := cfg["path"]
	require.NoError(t, os.WriteFile(filepath.Join(root, "report.txt"), []byte("q3 numbers"), 0o644))

	dbtest.SeedRegularUser(t, ha.store, permUser, permPass)
	u, err := ha.store.GetUserByEmail(context.Background(), permUser)
	require.NoError(t, err)
	m := map[string]string{}
	for _, p := range ps {
		m[string(p)] = model.PermDeny
	}
	require.NoError(t, ha.store.SetUserPermissionOverrides(context.Background(), u.ID, m, nil))
	perm.Invalidate()
	t.Cleanup(perm.Invalidate)
	return ha, root
}

func TestPerm_AccessWebDAVGatesEveryRequest(t *testing.T) {
	ha, _ := permHarness(t, perm.AccessWebDAV)
	resp := ha.req(t, "PROPFIND", "/dav/depo/", permUser, permPass, "", map[string]string{"Depth": "1"})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "an account without access.webdav was let in")
}

func TestPerm_WebDAVMethods(t *testing.T) {
	cases := []struct {
		denied        perm.Perm
		method, path  string
		body          string
		hdr           map[string]string
		stillThere    bool   // report.txt must survive
		unchanged     bool   // report.txt keeps its bytes
		neighbour     string // "METHOD path" that must still succeed
		neighbourBody string
		neighbourHdr  map[string]string
	}{
		{denied: perm.FilesDownload, method: http.MethodGet, path: "/dav/depo/report.txt", neighbour: "PROPFIND /dav/depo/report.txt"},
		{denied: perm.FilesDelete, method: http.MethodDelete, path: "/dav/depo/report.txt", stillThere: true, neighbour: "MKCOL /dav/depo/newdir"},
		{denied: perm.FilesCreate, method: http.MethodPut, path: "/dav/depo/new.txt", body: "x", neighbour: "PUT /dav/depo/report.txt", neighbourBody: "revised"},
		{denied: perm.FilesModify, method: http.MethodPut, path: "/dav/depo/report.txt", body: "tampered", unchanged: true, neighbour: "PUT /dav/depo/fresh.txt", neighbourBody: "new"},
		{denied: perm.FilesRename, method: "MOVE", path: "/dav/depo/report.txt", hdr: map[string]string{"Destination": "/dav/depo/moved.txt"}, stillThere: true, neighbour: "DELETE /dav/depo/report.txt"},
		{denied: perm.FilesMove, method: "MOVE", path: "/dav/depo/report.txt", hdr: map[string]string{"Destination": "/dav/depo/sub/report.txt"}, stillThere: true, neighbour: "MOVE /dav/depo/report.txt", neighbourHdr: map[string]string{"Destination": "/dav/depo/renamed.txt"}},
		{denied: perm.FilesCreate, method: "MKCOL", path: "/dav/depo/nope", neighbour: "GET /dav/depo/report.txt"},
	}
	for _, c := range cases {
		t.Run(string(c.denied)+"/"+c.method, func(t *testing.T) {
			ha, root := permHarness(t, c.denied)
			resp := ha.req(t, c.method, c.path, permUser, permPass, c.body, c.hdr)
			body := bodyString(t, resp)
			require.Equal(t, http.StatusForbidden, resp.StatusCode, "%s %s without %s: %s", c.method, c.path, c.denied, body)
			assert.Contains(t, body, string(c.denied), "the refusal names the permission")

			if c.stillThere || c.unchanged {
				got, err := os.ReadFile(filepath.Join(root, "report.txt"))
				require.NoError(t, err, "report.txt is gone")
				if c.unchanged {
					assert.Equal(t, "q3 numbers", string(got))
				}
			}

			method, path := splitNeighbour(c.neighbour)
			hdr := map[string]string{}
			for k, v := range c.neighbourHdr {
				hdr[k] = v
			}
			if method == "PROPFIND" {
				hdr["Depth"] = "0"
			}
			n := ha.req(t, method, path, permUser, permPass, c.neighbourBody, hdr)
			assert.Less(t, n.StatusCode, 300, "denying %s also stopped %s: %s", c.denied, c.neighbour, bodyString(t, n))
		})
	}
}

func splitNeighbour(s string) (string, string) {
	for i := range s {
		if s[i] == ' ' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}
