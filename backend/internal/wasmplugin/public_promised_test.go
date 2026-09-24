package wasmplugin

// A plugin may share a file its own job is still writing.
//
// The signing app's last step is the case these tests are about: every
// signature is in, the signed PDF has been produced inside `action_run`, and
// the app wants to mail ONE ordinary filex link of it to each signer. Until
// now it could not — a share points at a node, and a job's output has no node
// until `runJob` commits it, which happens after the plugin has returned. The
// link is now PROMISED at the ask (token, PIN, expiry decided and handed back
// at once, so the mail being composed carries the real address) and the ROW is
// written when the output lands.
//
// What is asserted here, in order: the promise is kept and the link is an
// ordinary share of the committed file; a promise the job breaks leaves
// nothing a visitor can open; the ordinary rules hold on the new path; both
// output modes the wizard offers work; the "share a file that already exists"
// path did not move; and a ref that is neither an input nor an output is said
// plainly.

import (
	"context"
	"encoding/json"
	"io"
	"os"
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

// ── helpers ────────────────────────────────────────────────────────────

// deliver runs the fixture's `deliver` action: it reads the document, writes a
// signed sibling, and asks for ONE link of that sibling — all before the host
// has committed anything. params steer which branch it takes.
func (h *harness) deliver(t *testing.T, p *Installed, u *model.User, rel string, params map[string]any) *model.AppPluginJob {
	t.Helper()
	pj, _ := json.Marshal([]string{rel})
	if params == nil {
		params = map[string]any{}
	}
	pb, _ := json.Marshal(params)
	job := &model.AppPluginJob{ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: "deliver",
		StorageID: h.st.ID, PathsJSON: string(pj), ParamsJSON: string(pb), Locale: "en", Label: "x",
		Status: model.AppPluginJobPending}
	if u != nil {
		id := u.ID
		job.ActorID = &id
	}
	require.NoError(t, h.store.CreateAppPluginJob(context.Background(), job))
	_ = h.reg.RunPluginAction(context.Background(),
		&ops.Op{ID: 11, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: []string{rel}, Dest: job.ID}, nil)
	got, err := h.store.GetAppPluginJob(context.Background(), job.ID)
	require.NoError(t, err)
	return got
}

// linkIn pulls the /s/<token> address out of whatever line the fixture wrote,
// and returns the token with it. A promised link is reported by the plugin
// exactly like an immediate one — that is the point of promising.
func linkIn(t *testing.T, line string) (string, string) {
	t.Helper()
	i := strings.Index(line, "/s/")
	require.GreaterOrEqual(t, i, 0, "no link in %q", line)
	tok := line[i+3:]
	if j := strings.IndexAny(tok, "| "); j >= 0 {
		tok = tok[:j]
	}
	require.Len(t, tok, 32, "a share token")
	return line[:i+3] + tok, tok
}

// bytesOf reads what the share's node actually holds — the chain the download
// route walks: token → share → node → storage.
func (h *harness) bytesOf(t *testing.T, sh *model.Share) string {
	t.Helper()
	node, err := h.store.GetNode(context.Background(), sh.NodeID)
	require.NoError(t, err)
	require.NotNil(t, node)
	rc, err := h.drv.Read(context.Background(), strings.TrimPrefix(node.Path, "/"))
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(b)
}

// promiseKept asks for a link against an output and then does exactly what
// runJob does when that output is committed. It asserts the shape of the whole
// mechanism on the way through: a promise leaves NO row, and keeping it makes
// one with the token the plugin was already given.
func (h *harness) promiseKept(t *testing.T, p *Installed, u *model.User, req map[string]any) (map[string]any, *model.Share) {
	t.Helper()
	ctx := context.Background()
	s := h.jobScope(t, p, u, "docs/contract.txt")
	s.outputMode = "sibling"
	_, ref, err := s.create("contract-signed.txt")
	require.NoError(t, err)
	req["ref"] = ref

	out, err := shareCreate(t, s, req)
	require.NoError(t, err)
	tok, _ := out["token"].(string)
	require.Len(t, tok, 32)
	require.True(t, s.hasPromises(), "the row is waiting for the output")
	_, missing := h.store.GetShareByToken(ctx, tok)
	require.Error(t, missing, "nothing exists while the output is unwritten")

	h.reg.keepPromisedShares(ctx, s, p, h.st.ID, ref, "docs/contract-signed.txt")
	sh, err := h.store.GetShareByToken(ctx, tok)
	require.NoError(t, err, "the promise was kept once the output had a node")
	return out, sh
}

// ── the link the signing app could not open ────────────────────────────

// TestShareOfOutput_OpensWhenTheJobCommitsIt is the defect, end to end: the
// plugin makes a file and asks for a share of THAT file, inside the same call,
// and the link it is handed resolves — after the job — to the committed
// document and serves its bytes.
func TestShareOfOutput_OpensWhenTheJobCommitsIt(t *testing.T) {
	h := newHarness(t, nil)
	h.reg.SetPublicURL("https://files.example.com")
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	u := h.actor(t)
	ctx := context.Background()

	job := h.deliver(t, p, u, "docs/contract.txt", nil)
	require.Equal(t, model.AppPluginJobOK, job.Status, job.Error)
	url, tok := linkIn(t, job.Message)
	assert.Equal(t, "https://files.example.com/s/"+tok, url, "the address the mail would carry")
	_, pin, _ := strings.Cut(job.Message, "|")
	require.Len(t, pin, 6, "the PIN came back with the link, at the ask")

	// The signed file is a file filex knows, and the link points at THAT one.
	signed, err := h.store.GetNodeByPath(ctx, h.st.ID, pathkey.Hash(h.st.ID, "/docs/contract-signed.txt"))
	require.NoError(t, err)
	require.NotNil(t, signed, "the output was committed")

	// ⭐ Resolved through the ordinary share service, with the ordinary PIN
	// gate, because it is an ordinary share.
	sh, err := h.share.Resolve(ctx, tok, pin)
	require.NoError(t, err)
	assert.Equal(t, signed.ID, sh.NodeID, "the link names the file the recipient gets")
	assert.Equal(t, "SIGNED the terms", h.bytesOf(t, sh), "and it serves the signed bytes")
	assert.False(t, sh.IsApp(), "no surface: it is a download link")
	assert.Empty(t, sh.PageID)
	assert.Equal(t, p.Row.ID, sh.PluginID, "the app that opened it is on the row")
	assert.Equal(t, "echo", sh.CreatedVia)
	require.NotNil(t, sh.CreatedBy)
	assert.Equal(t, u.ID, *sh.CreatedBy, "minted as the person whose job it was")
	assert.Equal(t, model.ShareKindDownload, sh.Kind)
	assert.Equal(t, "contract.txt", sh.Subject)
	assert.Empty(t, pageFiles(sh), "a page-less link stages no copies")

	// The PIN is the share's own bcrypt gate, and a wrong one is refused.
	assert.NotEmpty(t, sh.PinHash)
	_, err = h.share.Resolve(ctx, tok, "000000")
	assert.ErrorIs(t, err, share.ErrBadPIN)

	// ⭐ An ordinary share in every list that matters: the file's own, and the
	// administrator's revoke, which ends it like any other link.
	byNode, err := h.store.ListSharesByNode(ctx, signed.ID)
	require.NoError(t, err)
	require.Len(t, byNode, 1)
	assert.Equal(t, sh.ID, byNode[0].ID)
	require.NoError(t, h.store.RevokeShare(ctx, sh.ID))
	_, err = h.share.Resolve(ctx, tok, pin)
	assert.ErrorIs(t, err, share.ErrExpired)
}

// TestShareOfOutput_APromiseTheJobBreaksLeavesNothing: the other half of the
// guarantee. A link may only exist once the file behind it does, so a job that
// fails — or that simply never keeps the output it promised a link of —
// leaves a token nothing answers, and no staged copies on disk either.
func TestShareOfOutput_APromiseTheJobBreaksLeavesNothing(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	u := h.actor(t)
	ctx := context.Background()

	// The job fails AFTER the plugin has the link in its hand.
	failed := h.deliver(t, p, u, "docs/contract.txt", map[string]any{"fail": true, "page": "signer"})
	require.Equal(t, model.AppPluginJobFailed, failed.Status)
	_, tok := linkIn(t, failed.Error)
	_, err := h.store.GetShareByToken(ctx, tok)
	assert.Error(t, err, "the promised link was never opened")
	_, _, err = h.reg.LoadPage(ctx, tok)
	assert.ErrorIs(t, err, ErrPageNotFound, "and there is no app surface behind it either")

	// The output was never named in `outputs`, so it was never committed.
	dropped := h.deliver(t, p, u, "docs/contract.txt", map[string]any{"drop": true})
	require.Equal(t, model.AppPluginJobOK, dropped.Status, dropped.Error)
	_, tok2 := linkIn(t, dropped.Message)
	_, err = h.store.GetShareByToken(ctx, tok2)
	assert.Error(t, err, "a link of a file nobody kept is a link of nothing")
	assert.Empty(t, h.sink.siblings, "nothing was committed")

	// ⚠ And the copies staged for those links are gone, not left for the
	// sweeper: a promise is abandoned on every exit path the scope has.
	entries, err := os.ReadDir(h.reg.publicRoot())
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), "new-"), "staging directory %s survived", e.Name())
	}
}

// TestShareOfOutput_ObeysTheOrdinaryRules: the instance's expiry ceiling, the
// PIN bounds, the visit cap, created_by and the audit row all hold on the new
// path, because a promised link is judged by exactly the rules every other
// link is — and the clamped expiry is what the PLUGIN IS TOLD, not something
// discovered later, because the mail has already gone out by then.
func TestShareOfOutput_ObeysTheOrdinaryRules(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	h.writeCatalogued(t, "docs/contract-signed.txt", "SIGNED the terms")
	u := h.actor(t)
	ctx := context.Background()

	// ⭐ Expiry ceiling: the administrator's share.max_ttl_days wins over the
	// plugin's ask — and the answer says so at once.
	require.NoError(t, h.wide(t).UpsertSetting(ctx, share.SettingKeyMaxTTLDays, "2"))
	out, sh := h.promiseKept(t, p, u, map[string]any{"ttl_days": 300, "pin": "auto", "max_visits": 3})
	told, ok := out["expires_at"].(*time.Time)
	require.True(t, ok, "expires_at was %T", out["expires_at"])
	require.NotNil(t, sh.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(2*24*time.Hour), *sh.ExpiresAt, time.Minute,
		"the instance ceiling clamped the plugin's 300 days")
	assert.WithinDuration(t, *told, *sh.ExpiresAt, time.Second,
		"⭐ the date the plugin was told is the date the row carries")

	// ⭐ Visit ceiling and creator.
	require.NotNil(t, sh.MaxDownloads)
	assert.Equal(t, 3, *sh.MaxDownloads)
	require.NotNil(t, sh.CreatedBy)
	assert.Equal(t, u.ID, *sh.CreatedBy)

	// ⭐ The PIN handed back at the ask is the one the finished row's bcrypt
	// gate answers to.
	pin, _ := out["pin"].(string)
	require.Len(t, pin, 6)
	require.NoError(t, h.reg.CheckPIN(ctx, sh, pin))
	assert.ErrorIs(t, h.reg.CheckPIN(ctx, sh, "000000"), share.ErrBadPIN)

	// ⭐ PIN bounds are checked at the ASK, before any link is handed out.
	s := h.jobScope(t, p, u, "docs/contract.txt")
	s.outputMode = "sibling"
	_, ref, err := s.create("contract-signed.txt")
	require.NoError(t, err)
	_, err = shareCreate(t, s, map[string]any{"ref": ref, "pin": "12"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "4–12")
	assert.False(t, s.hasPromises(), "a refused ask promises nothing")

	// ⭐ Audited, when the link becomes real, and still without the token.
	rows, err := h.wide(t).ListAuditRecent(ctx, 20)
	require.NoError(t, err)
	var found *model.AuditEntry
	for _, e := range rows {
		if e.Action == "app_plugin.share_create" {
			found = e
		}
	}
	require.NotNil(t, found, "no audit row for the promised link")
	assert.Equal(t, "echo", found.Metadata["plugin"])
	assert.NotEmpty(t, found.Metadata["output"], "the row says the link is of a file the job made")
	for _, v := range found.Metadata {
		assert.NotEqual(t, sh.Token, v, "the token must not be in an audit row")
	}
	assert.NotContains(t, found.TargetID, sh.Token)
}

// TestShareOfOutput_BothOutputModesOfTheWizard: a new file beside the original
// and a new version OF the original both produce a link of the file the
// recipient actually gets. `version` is the one that would have looked free to
// bind early — the node already exists — and is exactly the one where an early
// row would hand the visitor the unsigned document if the job fell over.
func TestShareOfOutput_BothOutputModesOfTheWizard(t *testing.T) {
	ctx := context.Background()

	t.Run("sibling", func(t *testing.T) {
		h := newHarness(t, nil)
		p := h.install(t)
		original := h.writeCatalogued(t, "docs/contract.txt", "the terms")
		job := h.deliver(t, p, h.actor(t), "docs/contract.txt", nil)
		require.Equal(t, model.AppPluginJobOK, job.Status, job.Error)
		_, tok := linkIn(t, job.Message)
		sh, err := h.store.GetShareByToken(ctx, tok)
		require.NoError(t, err)
		assert.NotEqual(t, original.ID, sh.NodeID, "a new file beside the original")
		assert.Equal(t, "SIGNED the terms", h.bytesOf(t, sh))
	})

	t.Run("version", func(t *testing.T) {
		h := newHarness(t, nil)
		p := h.install(t)
		original := h.writeCatalogued(t, "docs/contract.txt", "the terms")
		params := map[string]any{}
		SetOutputOverride(params, &wire.Output{Mode: "version"})
		job := h.deliver(t, p, h.actor(t), "docs/contract.txt", params)
		require.Equal(t, model.AppPluginJobOK, job.Status, job.Error)
		assert.Equal(t, []string{"docs/contract.txt"}, h.sink.versions)
		_, tok := linkIn(t, job.Message)
		sh, err := h.store.GetShareByToken(ctx, tok)
		require.NoError(t, err)
		assert.Equal(t, original.ID, sh.NodeID, "the same file, a new version of it")
		assert.Equal(t, "SIGNED the terms", h.bytesOf(t, sh), "the link serves the NEW version")
	})
}

// TestShareOfOutput_WithAPageKeepsItsCopies: a promised link may have one of
// the manifest's pages behind it. The copies it exposes are staged at the ask
// like any other page's and move into the share's own directory when the row
// is written — so the surface a visitor opens has them.
func TestShareOfOutput_WithAPageKeepsItsCopies(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	ctx := context.Background()

	job := h.deliver(t, p, h.actor(t), "docs/contract.txt", map[string]any{"page": "signer"})
	require.Equal(t, model.AppPluginJobOK, job.Status, job.Error)
	_, tok := linkIn(t, job.Message)

	sh, _, err := h.reg.LoadPage(ctx, tok)
	require.NoError(t, err)
	assert.True(t, sh.IsApp())
	assert.Equal(t, "signer", sh.PageID)
	signed, err := h.store.GetNodeByPath(ctx, h.st.ID, pathkey.Hash(h.st.ID, "/docs/contract-signed.txt"))
	require.NoError(t, err)
	require.NotNil(t, signed)
	assert.Equal(t, signed.ID, sh.NodeID, "the page is about the file the job wrote")

	files := pageFiles(sh)
	require.Len(t, files, 1, "the exposed copy survived the wait")
	pf, fh, err := h.reg.PageFile(sh, files[0].Ref)
	require.NoError(t, err)
	defer fh.Close()
	assert.Equal(t, "contract-signed.txt", pf.Name)
	b, err := io.ReadAll(fh)
	require.NoError(t, err)
	assert.Equal(t, "SIGNED the terms", string(b))
}

// TestShareOfOutput_TheExistingPathDidNotMove: a link of a file that already
// exists is still written THERE AND THEN, promising nothing. This is the whole
// of the old behaviour, asserted from the one angle the other tests cannot see
// — that no wait was introduced where there was none.
func TestShareOfOutput_TheExistingPathDidNotMove(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	node := h.writeCatalogued(t, "docs/contract.txt", "the terms")
	u := h.actor(t)
	ctx := context.Background()

	s := h.jobScope(t, p, u, "docs/contract.txt")
	s.outputMode = "sibling"
	// The job has an output too — the choice is made by the REF, not by the
	// job having one.
	_, _, err := s.create("contract-signed.txt")
	require.NoError(t, err)

	out, err := shareCreate(t, s, map[string]any{"ref": "in:0", "subject": "contract.txt"})
	require.NoError(t, err)
	assert.False(t, s.hasPromises(), "an existing file waits for nothing")
	sh, err := h.store.GetShareByToken(ctx, out["token"].(string))
	require.NoError(t, err, "the row is there the moment share_create answers")
	assert.Equal(t, node.ID, sh.NodeID)
}

// TestShareCreate_RefThatIsNeitherIsSaidPlainly. The old message told a plugin
// that a file its job created "is not one filex knows until the job has
// finished", which is no longer true; what is left has to be true instead. A
// ref nobody has is named as such rather than quietly becoming the first
// input, and an action that keeps NO outputs is told so at the ask — the
// alternative is a token that never answers and nobody ever learns why.
func TestShareCreate_RefThatIsNeitherIsSaidPlainly(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	u := h.actor(t)

	s := h.jobScope(t, p, u, "docs/contract.txt")
	s.outputMode = "sibling"

	// ⭐ A ref that is neither an input nor an output. ⚠ Silently delivering
	// the first input instead is how a plugin's bug becomes a data leak.
	_, err := shareCreate(t, s, map[string]any{"ref": "out:99"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such file ref out:99")
	assert.Contains(t, err.Error(), "outputs it is writing")
	var he *hostError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, "not_found", he.Code)

	// The same, said through `files`.
	_, err = shareCreate(t, s, map[string]any{"files": []map[string]any{{"ref": "out:99"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outputs it is writing")

	// ⭐ A ref and a path that name two different documents. `path` used to
	// win silently, which against an output would promise a link of one file
	// and deliver another.
	_, made, err := s.create("contract-signed.txt")
	require.NoError(t, err)
	_, err = shareCreate(t, s, map[string]any{"ref": made, "path": "docs/contract.txt"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a link is of ONE file")
	assert.False(t, s.hasPromises())

	// ⭐ An action that keeps no outputs: the ref is real, the file never will
	// be. Said at the ask, before a link is handed to anybody.
	no := h.jobScope(t, p, u, "docs/contract.txt")
	no.outputMode = "none"
	_, ref, err := no.create("contract-signed.txt")
	require.NoError(t, err)
	_, err = shareCreate(t, no, map[string]any{"ref": ref})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keeps no outputs")
	assert.False(t, no.hasPromises())

	// …and the plugin gets those words, not "something went wrong": the
	// fixture hands the host's message straight to the job row.
	params := map[string]any{}
	SetOutputOverride(params, &wire.Output{Mode: "none"})
	job := h.deliver(t, p, u, "docs/contract.txt", params)
	require.Equal(t, model.AppPluginJobFailed, job.Status)
	assert.Contains(t, job.Error, "share_create:")
	assert.Contains(t, job.Error, "keeps no outputs")
}
