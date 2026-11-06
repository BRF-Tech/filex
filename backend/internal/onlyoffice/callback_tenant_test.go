package onlyoffice

import (
	"context"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// tenantOf makes a customer tenant, homes uid in it and gives it the
// harness's storage.
func (h *csvHarness) tenantOf(t *testing.T, slug string, uid int64) int64 {
	t.Helper()
	ctx := context.Background()
	p, err := h.store.CreateProvider(ctx, &model.Provider{Slug: slug, Name: slug, AuthType: "local", Enabled: true})
	require.NoError(t, err)
	require.NoError(t, h.store.SetUserProvider(ctx, uid, p.ID, ""))
	require.NoError(t, h.store.LinkProviderStorage(ctx, p.ID, h.node.StorageID))
	return p.ID
}

// A save is written over the document only while one of its editors may
// still change it. The editor stays open for as long as the person keeps it
// open; the platform operator may move the storage to another tenant, or the
// account may be switched off, in the meantime.
func TestCallback_ASaveOverTheDocumentAsksItsEditorsAgain(t *testing.T) {
	cases := []struct {
		name    string
		change  func(t *testing.T, h *csvHarness, ada, tenant int64)
		written bool
	}{
		{"control: nothing changed", func(*testing.T, *csvHarness, int64, int64) {}, true},
		{"the storage moved to another tenant", func(t *testing.T, h *csvHarness, _, tenant int64) {
			ctx := context.Background()
			other, err := h.store.CreateProvider(ctx, &model.Provider{Slug: "initech", Name: "initech", AuthType: "local", Enabled: true})
			require.NoError(t, err)
			require.NoError(t, h.store.UnlinkProviderStorage(ctx, tenant, h.node.StorageID))
			require.NoError(t, h.store.LinkProviderStorage(ctx, other.ID, h.node.StorageID))
		}, false},
		{"the editor's account was switched off", func(t *testing.T, h *csvHarness, ada, _ int64) {
			require.NoError(t, h.store.SetUserEnabled(context.Background(), ada, false))
		}, false},
		// auth.LoginAllowed, the rule the tickets and the links follow.
		{"the editor's tenant was suspended", func(t *testing.T, h *csvHarness, _, tenant int64) {
			ctx := context.Background()
			p, err := h.store.GetProvider(ctx, tenant)
			require.NoError(t, err)
			p.Enabled = false
			require.NoError(t, h.store.UpdateProvider(ctx, p))
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newDocHarness(t, "rapor.docx", docxMime, "V1")
			h.svc.AttachMultiTenant(true)
			ada := h.user(t, "ada@example.com", model.RoleUser)
			tenant := h.tenantOf(t, "globex", ada)
			h.open(t, ada)
			h.serve(docxBytes)

			c.change(t, h, ada, tenant)

			p := dsCallback(h.sessionKey, StatusReadyForSaving, h.ds.URL+"/saved", idStrings(ada)...)
			resp, err := h.postCallback(t, h.node.ID, p, headerToken(t, p))
			require.NoError(t, err)
			if c.written {
				assert.Equal(t, 0, resp["error"], "%v", resp)
				assert.Equal(t, docxBytes, h.disk(t))
				return
			}
			assert.Equal(t, 1, resp["error"], "%v", resp)
			assert.Equal(t, "V1", h.disk(t), "nothing written over the document")
		})
	}
}

// The editor's fetch URL carries the tenant the document's storage was in
// when the editor was opened, signed with the rest; the fetch handler serves
// the document only while that tenant still has the storage (db.StampStands).
func TestFetchURL_CarriesTheDocumentsTenant(t *testing.T) {
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	tenant := h.tenantOf(t, "globex", ada)
	u, err := h.store.GetUser(context.Background(), ada)
	require.NoError(t, err)
	cfg, err := h.svc.BuildConfigForNode(context.Background(), h.node, u, "en", "edit")
	require.NoError(t, err)
	raw := cfg.Config["document"].(map[string]any)["url"].(string)
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	q := parsed.Query()
	require.Equal(t, strconv.FormatInt(tenant, 10), q.Get("ten"))
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, h.svc.VerifyFetchSignatureIn(ctx, h.node.ID, exp, tenant, q.Get("sig")))
	require.Error(t, h.svc.VerifyFetchSignatureIn(ctx, h.node.ID, exp, 0, q.Get("sig")), "the tenant cannot be stripped")
	require.True(t, db.StampStands(ctx, h.store, tenant, h.node.StorageID))

	require.NoError(t, h.store.UnlinkProviderStorage(ctx, tenant, h.node.StorageID))
	assert.False(t, db.StampStands(ctx, h.store, tenant, h.node.StorageID), "the storage left the tenant")
}
