package wasmplugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// writeCatalogued writes the bytes AND the node row. share_create resolves the
// document a link is about through the catalogue — a share points at a node —
// so a file that exists only on the driver cannot be shared, by design.
func (h *harness) writeCatalogued(t *testing.T, rel, content string) *model.Node {
	t.Helper()
	h.writeFile(t, rel, content)
	n, err := h.store.CreateNode(context.Background(), &model.Node{
		StorageID: h.st.ID, Name: filepath.Base(rel), Path: "/" + rel,
		PathHash: pathkey.Hash(h.st.ID, "/"+rel), Type: model.NodeTypeFile,
		Size: int64(len(content)), Mime: "text/plain", SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)
	return n
}

// invite runs the fixture's link-creating action and returns the token + PIN.
func (h *harness) invite(t *testing.T, p *Installed, pinParam string) (string, string, *model.Share) {
	t.Helper()
	pj, _ := json.Marshal([]string{"docs/contract.txt"})
	params := "{}"
	if pinParam != "" {
		params = `{"pin":"` + pinParam + `"}`
	}
	job := &model.AppPluginJob{ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: "invite",
		StorageID: h.st.ID, PathsJSON: string(pj), ParamsJSON: params, Locale: "en", Label: "x", Status: model.AppPluginJobPending}
	require.NoError(t, h.store.CreateAppPluginJob(context.Background(), job))
	err := h.reg.RunPluginAction(context.Background(), &ops.Op{ID: 7, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: []string{"docs/contract.txt"}, Dest: job.ID}, nil)
	require.NoError(t, err)
	got, _ := h.store.GetAppPluginJob(context.Background(), job.ID)
	require.Equal(t, model.AppPluginJobOK, got.Status, got.Error)
	url, pin, _ := strings.Cut(got.Message, "|")
	token := url[strings.LastIndex(url, "/")+1:]
	sh, _, err := h.reg.LoadPage(context.Background(), token)
	require.NoError(t, err)
	return token, pin, sh
}

func TestPublicPage_IsARealShare(t *testing.T) {
	h := newHarness(t, nil)
	h.reg.SetPublicURL("https://files.example.com")
	p := h.install(t)
	node := h.writeCatalogued(t, "docs/contract.txt", "the terms")

	token, pin, sh := h.invite(t, p, "")

	// ⭐ What this round is about: the link the plugin opened is a row in
	// `shares`, at /s/<token>, and the administrator's Shares list has it. It
	// is not a second kind of public link with its own everything.
	assert.Len(t, token, 32, "a share token, not a 64-hex page token")
	assert.Equal(t, token, sh.Token)
	same, err := h.store.GetShareByToken(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, node.ID, same.NodeID, "the share points at the document")
	assert.Equal(t, p.Row.ID, same.PluginID)
	assert.Equal(t, "signer", same.PageID)
	assert.Equal(t, "Please sign contract.txt", same.Subject)
	assert.Equal(t, "echo", same.CreatedVia, "the app is named as the creating caller")
	assert.True(t, same.IsApp())

	rows, total, err := h.store.ListAppPluginShares(context.Background(), p.Row.ID, false, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, "echo", rows[0].PluginName, "the admin list can draw a plugin column")

	assert.Len(t, pin, 6, "auto PIN")
	assert.NotNil(t, sh.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(3*24*time.Hour), *sh.ExpiresAt, time.Minute, "ttl 3 days from the plugin, under the 30-day ceiling")

	files := pageFiles(sh)
	require.Len(t, files, 1)
	assert.Equal(t, "pub:0", files[0].Ref)
	assert.Equal(t, "0-contract.txt", files[0].File, "a basename, not an absolute path")
	assert.FileExists(t, filepath.Join(h.reg.pageDir(sh.ID), files[0].File))

	// The per-file state the action wrote survived.
	v, found, _ := h.store.GetAppPluginState(context.Background(), p.Row.ID, h.st.ID, pathkey.Hash(h.st.ID, "/docs/contract.txt"), "envelope")
	assert.True(t, found)
	assert.Equal(t, token, v)
}

func TestPublicPage_PINGateIsTheShares(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	token, pin, sh := h.invite(t, p, "")

	// Wrong ×5 locks; the RIGHT PIN during the lock is refused too — a lock
	// the correct answer lifts is no lock at all against somebody walking the
	// space, because they would simply find it.
	for i := 0; i < 4; i++ {
		assert.ErrorIs(t, h.reg.CheckPIN(context.Background(), sh, "000000"), share.ErrBadPIN)
	}
	assert.ErrorIs(t, h.reg.CheckPIN(context.Background(), sh, "000000"), share.ErrLocked)
	assert.ErrorIs(t, h.reg.CheckPIN(context.Background(), sh, pin), share.ErrLocked)

	// ⭐ The strikes are ON THE SHARE ROW, so a fresh process — or the second
	// instance behind the same address — sees the same lock. The v2 counter
	// lived on a table only the app pages had.
	reread, err := h.store.GetShareByToken(context.Background(), token)
	require.NoError(t, err)
	assert.True(t, reread.PinLocked(time.Now()))

	// Once it lifts, the right PIN passes and the counter resets.
	require.NoError(t, h.store.UpdateSharePinLock(context.Background(), sh.ID, 0, nil))
	sh.LockedUntil, sh.PinFails = nil, 0
	require.NoError(t, h.reg.CheckPIN(context.Background(), sh, pin))
	assert.Equal(t, 0, sh.PinFails)

	// The unlock cookie round-trips and is bound to THIS link.
	other, _, _ := h.invite(t, p, "")
	c := h.reg.MintUnlock(token)
	assert.True(t, h.reg.VerifyUnlock(token, c))
	assert.False(t, h.reg.VerifyUnlock(other, c), "a cookie for one link does not open another")
	assert.False(t, h.reg.VerifyUnlock(token, c+"x"))
}

func TestPublicPage_OpenSubmitRevoke(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	token, _, sh := h.invite(t, p, "")

	// Open: the page reads its state and the exposed copy, never the storage.
	_, ins, err := h.reg.LoadPage(context.Background(), token)
	require.NoError(t, err)
	s, err := h.reg.PageEvent(context.Background(), sh, ins, wire.ViewEventInput{Event: "open", Context: wire.CallContext{Locale: "en"}}, "203.0.113.9")
	require.NoError(t, err)
	require.Len(t, s.Nodes, 3)
	text := s.Nodes[0].Props["text"].(map[string]any)["en"].(string)
	assert.Equal(t, "step=sent doc=the terms inputs=2", text, "context file (unreadable anchor) + one exposed copy")
	// The page says which event drew it, because a visitor-facing screen has
	// to be re-askable without starting over (a language switch re-asks with
	// `change`), and it carries a field whose value must survive that.
	assert.Equal(t, "event=open note=", s.Nodes[1].Props["text"].(map[string]any)["en"].(string))

	// ⭐ The visit was counted on the share's VISIT counter, which is what an
	// app page's ceiling is measured against — and NOT as a download: a
	// signing link somebody had only opened twice said "İndirme 2" in My
	// shares (2026-09-21; the owner: page views are not downloads).
	assert.Equal(t, 1, sh.VisitCount)
	reread, _ := h.store.GetShareByToken(context.Background(), token)
	assert.Equal(t, 1, reread.VisitCount)
	assert.Equal(t, 0, reread.DownloadCount, "opening the page downloads nothing")

	// Submit: state flips, per-file state written from the page, a job asked for.
	s, err = h.reg.PageEvent(context.Background(), sh, ins, wire.ViewEventInput{Event: "submit", Context: wire.CallContext{Locale: "en"}}, "203.0.113.9")
	require.NoError(t, err)
	require.NotNil(t, s.Job)
	assert.Equal(t, "upper", s.Job.ActionID)
	after, _ := h.store.GetShareByToken(context.Background(), token)
	assert.JSONEq(t, `{"step":"signed"}`, after.StateJSON)
	v, _, _ := h.store.GetAppPluginState(context.Background(), p.Row.ID, h.st.ID, pathkey.Hash(h.st.ID, "/docs/contract.txt"), "signed_by")
	assert.Equal(t, "visitor", v)

	// The exposed copy is served by ref; other refs are not.
	pf, fh, err := h.reg.PageFile(sh, "pub:0")
	require.NoError(t, err)
	fh.Close()
	assert.Equal(t, "contract.txt", pf.Name)
	_, _, err = h.reg.PageFile(sh, "in:0")
	assert.Error(t, err)

	// Revoke is the SHARE's revoke: the row stays (with its history), the
	// link stops.
	require.NoError(t, h.store.RevokeShare(context.Background(), sh.ID))
	_, _, err = h.reg.LoadPage(context.Background(), token)
	assert.ErrorIs(t, err, ErrPageGone)
}

// TestPublicPage_SweepTakesDeadDirectories covers the half a dead row cannot:
// the sweeper walks the DIRECTORIES, so copies belonging to a share that was
// hard-deleted (the administrator's Delete, not Revoke) are collected too —
// there is no row left to join against.
func TestPublicPage_SweepTakesDeadDirectories(t *testing.T) {
	h := newHarness(t, nil)
	node := h.writeCatalogued(t, "docs/contract.txt", "the terms")
	svc := h.share

	live, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID, PluginID: 1, PageID: "signer", FilesJSON: "[]"})
	require.NoError(t, err)
	long := time.Now().Add(-30 * 24 * time.Hour)
	lapsed, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID, PluginID: 1, PageID: "signer", FilesJSON: "[]", ExpiresAt: &long})
	require.NoError(t, err)
	deleted, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID, PluginID: 1, PageID: "signer", FilesJSON: "[]"})
	require.NoError(t, err)
	require.NoError(t, h.store.DeleteShare(context.Background(), deleted.ID))

	for _, id := range []int64{live.ID, lapsed.ID, deleted.ID} {
		require.NoError(t, os.MkdirAll(h.reg.pageDir(id), 0o700))
	}
	assert.Equal(t, 2, h.reg.SweepPages(context.Background()))
	assert.DirExists(t, h.reg.pageDir(live.ID))
	for _, id := range []int64{lapsed.ID, deleted.ID} {
		_, statErr := os.Stat(h.reg.pageDir(id))
		assert.True(t, os.IsNotExist(statErr), "dead link %d still has its copies", id)
	}
}

func TestPublicPage_NoPIN_MaxVisits_Expiry(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "x")
	token, pin, sh := h.invite(t, p, "-")
	assert.Empty(t, pin, "manifest pin=optional and the plugin asked for none")
	require.NoError(t, h.reg.CheckPIN(context.Background(), sh, ""), "no PIN → always unlocked")

	// The visit ceiling lives in the share's one cap column, measured against
	// the VISITS of an app page (model.Share.CappedCount) — so a spent link
	// is dead by exactly the rule every other public link is judged by, and
	// the downloads of what it exposed never spend it.
	one := 1
	sh.MaxDownloads, sh.DownloadCount = &one, 5
	assert.False(t, sh.IsExpired(time.Now()), "downloads do not spend an app page's visits")
	sh.VisitCount = 1
	assert.True(t, sh.IsExpired(time.Now()))

	require.NoError(t, h.store.RevokeShare(context.Background(), sh.ID))
	_, _, err := h.reg.LoadPage(context.Background(), token)
	assert.ErrorIs(t, err, ErrPageGone)

	_, _, err = h.reg.LoadPage(context.Background(), "short")
	assert.ErrorIs(t, err, ErrPageNotFound)
}

// TestPublicPage_OrdinaryShareIsNotAnAppPage guards the lookup: a plain
// download link must not be answerable as an app surface, and the refusal is
// "no such page" rather than anything that describes the row.
func TestPublicPage_OrdinaryShareIsNotAnAppPage(t *testing.T) {
	h := newHarness(t, nil)
	node := h.writeCatalogued(t, "docs/plain.txt", "hello")
	sh, err := h.share.Create(context.Background(), share.CreateOpts{NodeID: node.ID})
	require.NoError(t, err)
	_, _, err = h.reg.LoadPage(context.Background(), sh.Token)
	assert.ErrorIs(t, err, ErrPageNotFound)
}
