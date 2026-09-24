package wasmplugin

// A plugin's public page is a share. From here a plugin may also open a share
// that is ONLY a share: no page behind it, no surface, no exposed copies —
// the ordinary download link an administrator already sees and revokes in
// **Shares**. It is how an app hands a finished file to somebody with no
// account (the signed document at the end of a signature round).
//
// These tests drive `share_create` through its host-function entry point with
// a hand-built call scope, which is what an action_run call gives it: the
// wasm fixture is not involved, so what is asserted here is the host's own
// contract rather than one fixture's use of it.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
)

// jobScope is the ticket an action_run call is handed: writable, on the
// harness's storage, with the named files as its inputs.
func (h *harness) jobScope(t *testing.T, p *Installed, actor *model.User, inputs ...string) *Scope {
	t.Helper()
	s, err := newScope(p, h.reg, NewJobID(), h.st.ID, h.drv, actor, "en", true)
	require.NoError(t, err)
	t.Cleanup(s.Close)
	for _, rel := range inputs {
		s.AddInput(rel, 0, "text/plain")
	}
	return s
}

// beyondStoreT is the rest of db.Store these tests need. The harness's own
// `storeT` is a deliberately narrow interface, so the extra calls are asked
// for here rather than widening it for everybody.
type beyondStoreT interface {
	CreateUser(ctx context.Context, email, passwordHash, role, locale, tz string) (*model.User, error)
	ListAuditRecent(ctx context.Context, limit int) ([]*model.AuditEntry, error)
	UpsertSetting(ctx context.Context, key, value string) error
}

func (h *harness) wide(t *testing.T) beyondStoreT {
	t.Helper()
	s, ok := h.store.(beyondStoreT)
	require.True(t, ok, "the test store is not a full db.Store")
	return s
}

// actor is a real user row, because a share carries created_by and the link's
// follow-up job runs under that person.
func (h *harness) actor(t *testing.T) *model.User {
	t.Helper()
	u, err := h.wide(t).CreateUser(context.Background(), "signer@example.com", "x", "user", "en", "UTC")
	require.NoError(t, err)
	return u
}

func shareCreate(t *testing.T, s *Scope, req map[string]any) (map[string]any, error) {
	t.Helper()
	b, err := json.Marshal(req)
	require.NoError(t, err)
	out, err := hfShareCreate(context.Background(), s, b)
	if err != nil {
		return nil, err
	}
	m, ok := out.(map[string]any)
	require.True(t, ok, "share_create answered %T", out)
	return m, nil
}

// TestShareCreate_PageLessIsAnOrdinaryShare is the delivery link: a plugin
// asks for a share with no page_id and gets a plain download link of the
// document — a row the Shares list, the expiry sweep and the administrator's
// Revoke treat exactly like any other, and which the app-page lookup refuses
// to answer as a surface because there is no surface.
func TestShareCreate_PageLessIsAnOrdinaryShare(t *testing.T) {
	h := newHarness(t, nil)
	h.reg.SetPublicURL("https://files.example.com")
	p := h.install(t)
	node := h.writeCatalogued(t, "docs/contract-signed.txt", "signed")
	u := h.actor(t)
	s := h.jobScope(t, p, u, "docs/contract-signed.txt")

	out, err := shareCreate(t, s, map[string]any{
		"subject": "contract-signed.txt", "pin": "auto", "ttl_days": 5,
	})
	require.NoError(t, err)
	token, _ := out["token"].(string)
	require.Len(t, token, 32, "a share token")
	assert.Equal(t, "https://files.example.com/s/"+token, out["url"])
	pin, _ := out["pin"].(string)
	assert.Len(t, pin, 6, "auto PIN")

	sh, err := h.store.GetShareByToken(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, node.ID, sh.NodeID, "the link points at the file it delivers")
	assert.Equal(t, model.ShareKindDownload, sh.Kind)
	assert.Equal(t, p.Row.ID, sh.PluginID, "the app that opened it is on the row")
	assert.Empty(t, sh.PageID, "no page behind it")
	assert.False(t, sh.IsApp(), "⭐ it is a share, not an app surface")
	assert.Equal(t, "echo", sh.CreatedVia)
	require.NotNil(t, sh.CreatedBy)
	assert.Equal(t, u.ID, *sh.CreatedBy, "minted as the person whose job it was")
	assert.Equal(t, "contract-signed.txt", sh.Subject)
	assert.Empty(t, pageFiles(sh), "no copies were staged for a link that cannot show them")
	assert.NotEmpty(t, sh.PinHash)
	require.NoError(t, h.reg.CheckPIN(context.Background(), sh, pin), "the PIN gate is the share's own")

	// The app-page lookup must NOT answer it: there is nothing to render.
	_, _, err = h.reg.LoadPage(context.Background(), token)
	assert.ErrorIs(t, err, ErrPageNotFound)

	// Revoke is the share's revoke, and the row stays with its history.
	require.NoError(t, h.store.RevokeShare(context.Background(), sh.ID))
	after, err := h.store.GetShareByToken(context.Background(), token)
	require.NoError(t, err)
	assert.True(t, after.IsExpired(time.Now()))

	// ⚠ Audited: an administrator can see that an app opened a link, without
	// the audit row BEING the link.
	rows, err := h.wide(t).ListAuditRecent(context.Background(), 20)
	require.NoError(t, err)
	var found *model.AuditEntry
	for _, e := range rows {
		if e.Action == "app_plugin.share_create" {
			found = e
		}
	}
	require.NotNil(t, found, "no audit row for the link")
	assert.Equal(t, strconv.FormatInt(sh.ID, 10), found.TargetID)
	assert.Equal(t, "echo", found.Metadata["plugin"])
	assert.NotContains(t, found.TargetID, token)
	for _, v := range found.Metadata {
		assert.NotEqual(t, token, v, "the token must not be in an audit row")
	}
}

// TestShareCreate_UnknownPageIsStillRefused guards the half of the check that
// was right: a page_id the manifest does not declare is a plugin asking for a
// screen nobody approved at install.
func TestShareCreate_UnknownPageIsStillRefused(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	s := h.jobScope(t, p, h.actor(t), "docs/contract.txt")

	_, err := shareCreate(t, s, map[string]any{"page_id": "not-in-the-manifest"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "declares no public page")

	// And the one it does declare still works, copies and all.
	out, err := shareCreate(t, s, map[string]any{
		"page_id": "signer", "subject": "Please sign",
		"files": []map[string]any{{"ref": "in:0", "name": "contract.txt"}},
	})
	require.NoError(t, err)
	sh, err := h.store.GetShareByToken(context.Background(), out["token"].(string))
	require.NoError(t, err)
	assert.True(t, sh.IsApp())
	require.Len(t, pageFiles(sh), 1)
}

// TestShareCreate_PageLessRefusesCopiesItCannotShow is the failure that would
// have been worst: a link the plugin believes carries the file it just made,
// which in fact hands the visitor the DOCUMENT. A page-less link streams its
// node, so a copy is refused rather than dropped — unless the copy simply
// names a file this call knows, which is the document said twice.
func TestShareCreate_PageLessRefusesCopiesItCannotShow(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	node := h.writeCatalogued(t, "docs/contract.txt", "the terms")
	s := h.jobScope(t, p, h.actor(t), "docs/contract.txt")
	s.outputMode = "sibling"

	// ONE copy naming a file this job is WRITING is the document said twice
	// as well, and is promised rather than refused (public_promised.go).
	_, ref, err := s.create("contract-signed.txt")
	require.NoError(t, err)
	out, err := shareCreate(t, s, map[string]any{
		"files": []map[string]any{{"ref": ref, "name": "contract-signed.txt"}},
	})
	require.NoError(t, err)
	assert.Len(t, out["token"], 32)
	assert.True(t, s.hasPromises(), "it waits for the output rather than being refused")

	// Two copies have nowhere to go, whatever they name.
	_, err = shareCreate(t, s, map[string]any{
		"files": []map[string]any{{"ref": "in:0"}, {"ref": ref}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hands over no copies")
	assert.Contains(t, err.Error(), "outputs it is writing")

	// One copy naming an input IS the document: honoured, not refused, and
	// still no copies staged.
	out, err = shareCreate(t, s, map[string]any{
		"files": []map[string]any{{"ref": "in:0", "name": "contract.txt"}},
	})
	require.NoError(t, err)
	sh, err := h.store.GetShareByToken(context.Background(), out["token"].(string))
	require.NoError(t, err)
	assert.Equal(t, node.ID, sh.NodeID)
	assert.Empty(t, pageFiles(sh))
}

// TestShareCreate_PageLessObeysTheOrdinaryRules: the ACL, the instance's
// expiry ceiling, the PIN bounds and the visit ceiling all hold on the new
// path, because a link with no app behind it is judged by exactly the rules
// every other link is.
func TestShareCreate_PageLessObeysTheOrdinaryRules(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	h.writeCatalogued(t, "gizli/payroll.txt", "not yours")
	u := h.actor(t)
	s := h.jobScope(t, p, u, "docs/contract.txt")

	// ⭐ ACL: a path the job was never given is not the plugin's to publish.
	_, err := shareCreate(t, s, map[string]any{"path": "gizli/payroll.txt"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not one of its inputs")
	var he *hostError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, "permission_denied", he.Code)

	// A page still may (that check is older than this path and unchanged).
	_, err = shareCreate(t, s, map[string]any{"page_id": "signer", "path": "gizli/payroll.txt"})
	require.NoError(t, err)

	// ⭐ Expiry ceiling: the administrator's share.max_ttl_days wins over the
	// plugin's ask, exactly as it does over a person's.
	require.NoError(t, h.wide(t).UpsertSetting(context.Background(), share.SettingKeyMaxTTLDays, "2"))
	out, err := shareCreate(t, s, map[string]any{"ttl_days": 300})
	require.NoError(t, err)
	sh, err := h.store.GetShareByToken(context.Background(), out["token"].(string))
	require.NoError(t, err)
	require.NotNil(t, sh.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(2*24*time.Hour), *sh.ExpiresAt, time.Minute,
		"the instance ceiling clamped the plugin's 300 days")

	// ⭐ PIN bounds.
	_, err = shareCreate(t, s, map[string]any{"pin": "12"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "4–12")

	// ⭐ Visit ceiling: max_visits is the share's download cap, so a spent
	// link is dead by the same rule every other link is judged by.
	out, err = shareCreate(t, s, map[string]any{"max_visits": 1})
	require.NoError(t, err)
	capped, err := h.store.GetShareByToken(context.Background(), out["token"].(string))
	require.NoError(t, err)
	require.NotNil(t, capped.MaxDownloads)
	assert.Equal(t, 1, *capped.MaxDownloads)
	capped.DownloadCount = 1
	assert.True(t, capped.IsExpired(time.Now()))

	// ⭐ And a page-less link is still jobs-only: a page call may not mint one.
	ro, err := newScope(p, h.reg, "", h.st.ID, h.drv, u, "en", false)
	require.NoError(t, err)
	defer ro.Close()
	ro.AddInput("docs/contract.txt", 0, "text/plain")
	_, err = shareCreate(t, ro, map[string]any{})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "action jobs only"), err.Error())
}
