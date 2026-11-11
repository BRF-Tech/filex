package api_test

// Ç3 (#314, #321): a link's page wears the branding of the tenant the link
// was made in, at whatever address it is opened - the JavaScript page's
// identity answer (/api/public/{s,d}/{token}/branding) and the document it
// is drawn in (its <title>, theme colour, icon) alike. A token that names no
// live link answers what the address answers, so the "not available" page
// says nothing about whose link it was.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type linkBrandEnv struct {
	url   string
	store db.Store
	file  *model.Share // a download link made in alpha
	drop  *model.Share // a file request made in alpha
	gone  *model.Share // a withdrawn link made in alpha
}

func newLinkBrandEnv(t *testing.T) *linkBrandEnv {
	t.Helper()
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.MultiTenant = true
		c.PublicURL = "http://platform.test"
	}, func(d *api.Deps) {
		d.Embed = brandBuild()
	})
	ctx := context.Background()
	mk := func(slug, host string) int64 {
		p, err := store.CreateProvider(ctx, &model.Provider{
			Slug: slug, Realm: slug, Name: slug, AuthType: model.AuthTypeLocal, Enabled: true, Host: host,
		})
		require.NoError(t, err)
		return p.ID
	}
	alpha, bravo := mk("alpha", "alpha.test"), mk("bravo", "bravo.test")
	for k, v := range map[string]string{
		"branding.name": "Platform",
		fmt.Sprintf("tenant.%d.branding.name", alpha):   "Alpha Studio",
		fmt.Sprintf("tenant.%d.branding.accent", alpha): "#aa2200",
		fmt.Sprintf("tenant.%d.branding.name", bravo):   "Bravo Works",
		fmt.Sprintf("tenant.%d.branding.accent", bravo): "#0055aa",
	} {
		require.NoError(t, store.UpsertSetting(ctx, k, v))
	}

	root := t.TempDir()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "alpha-files", Driver: "local", MountPath: "/alpha-files", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + strings.ReplaceAll(root, `\`, `\\`) + `"}`),
	})
	require.NoError(t, err)
	require.NoError(t, store.LinkProviderStorage(ctx, alpha, st.ID))
	node := func(name string, typ model.NodeType) *model.Node {
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: name, Path: "/" + name, PathHash: pathkey.Hash(st.ID, "/"+name), Type: typ,
		})
		require.NoError(t, err)
		return n
	}
	svc := share.NewService(store)
	file, err := svc.Create(ctx, share.CreateOpts{NodeID: node("price-list.pdf", model.NodeTypeFile).ID, Kind: model.ShareKindDownload})
	require.NoError(t, err)
	drop, err := svc.Create(ctx, share.CreateOpts{NodeID: node("inbox", model.NodeTypeDirectory).ID, Kind: model.ShareKindDrop})
	require.NoError(t, err)
	gone, err := svc.Create(ctx, share.CreateOpts{NodeID: node("old.pdf", model.NodeTypeFile).ID, Kind: model.ShareKindDownload})
	require.NoError(t, err)
	require.NoError(t, store.RevokeShare(ctx, gone.ID))
	require.Equal(t, alpha, file.TenantID, "the fixture's link must be alpha's")
	return &linkBrandEnv{url: srv.URL, store: store, file: file, drop: drop, gone: gone}
}

// at GETs path with Host host and returns the body.
func (e *linkBrandEnv) at(t *testing.T, host, path string, hdr ...string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.url+path, nil)
	require.NoError(t, err)
	req.Host = host
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *linkBrandEnv) brandName(t *testing.T, host, path string) string {
	t.Helper()
	code, body := e.at(t, host, path)
	require.Equal(t, http.StatusOK, code, "%s %s: %s", host, path, body)
	var b struct {
		Name   string `json:"name"`
		Accent string `json:"accent"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &b), body)
	return b.Name + " " + b.Accent
}

func TestLinkBrand_TheLinkPageWearsItsTenantAtAnyAddress(t *testing.T) {
	e := newLinkBrandEnv(t)
	// The control: bravo's address is bravo's.
	require.Equal(t, "Bravo Works #0055aa", e.brandName(t, "bravo.test", "/api/public/branding"))

	// Alpha's link, opened at bravo's address and at the platform's: alpha's.
	for _, host := range []string{"bravo.test", "platform.test", "alpha.test"} {
		assert.Equal(t, "Alpha Studio #aa2200", e.brandName(t, host, "/api/public/s/"+e.file.Token+"/branding"), host)
		assert.Equal(t, "Alpha Studio #aa2200", e.brandName(t, host, "/api/public/d/"+e.drop.Token+"/branding"), host)
	}
}

func TestLinkBrand_ADeadTokenAnswersWhatTheAddressAnswers(t *testing.T) {
	e := newLinkBrandEnv(t)
	for _, c := range []struct{ what, path string }{
		{"unknown", "/api/public/s/0123456789abcdef0123456789abcdef/branding"},
		{"withdrawn", "/api/public/s/" + e.gone.Token + "/branding"},
		{"a file request asked as a share", "/api/public/s/" + e.drop.Token + "/branding"},
		{"a share asked as a file request", "/api/public/d/" + e.file.Token + "/branding"},
	} {
		assert.Equal(t, "Bravo Works #0055aa", e.brandName(t, "bravo.test", c.path), c.what)
		assert.Equal(t, "Platform ", e.brandName(t, "platform.test", c.path), c.what)
	}
}

func TestLinkBrand_TheDocumentWearsTheLinksTenantFromTheFirstPaint(t *testing.T) {
	e := newLinkBrandEnv(t)
	code, body := e.at(t, "bravo.test", "/s/"+e.file.Token, "Accept", "text/html")
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "<title>Alpha Studio</title>")
	assert.Contains(t, body, `<meta name="theme-color" content="#aa2200"`)

	code, body = e.at(t, "bravo.test", "/d/"+e.drop.Token, "Accept", "text/html")
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "<title>Alpha Studio</title>")

	// A dead token's document is the address's.
	code, body = e.at(t, "bravo.test", "/s/"+e.gone.Token, "Accept", "text/html")
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "<title>Bravo Works</title>")
	// And the admin panel at bravo's address is bravo's, link or no link.
	_, body = e.at(t, "bravo.test", "/admin/", "Accept", "text/html")
	assert.Contains(t, body, "<title>Bravo Works</title>")
}

func TestLinkBrand_IsNotKeptByASharedCache(t *testing.T) {
	e := newLinkBrandEnv(t)
	req, err := http.NewRequest(http.MethodGet, e.url+"/api/public/s/"+e.file.Token+"/branding", nil)
	require.NoError(t, err)
	req.Host = "bravo.test"
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	// The answer is the link's and changes the moment the link dies: no
	// shared cache may hold it the way it holds /api/public/branding.
	assert.Contains(t, resp.Header.Get("Cache-Control"), "no-store")
	assert.NotContains(t, resp.Header.Get("Cache-Control"), "public")
}
