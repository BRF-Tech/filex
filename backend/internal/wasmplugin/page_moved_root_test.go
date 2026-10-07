package wasmplugin

// A link's document moved out of the root after the link was opened (filex
// Roadmap #185, what #161 left).
//
// A link a `root:` token's job opens records that root (model.Share.AppRoot),
// and the visitor's screen (PageEvent) and the job the visitor's submit queues
// are held to it. Both are handed the link's document where it lies NOW - the
// share points at a node, and the node follows a move - and the document was
// judged against the root once, when the link was opened. Moved out of the
// root afterwards, it was still handed to the page, its ref still reached it
// (a notice pointed at it), and the visitor's job read it and wrote beside it.
// The ref is judged against the link's root on every use now, and the job
// again when it runs.

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// moveCatalogued moves a catalogued file on the storage and in the catalogue,
// as somebody tidying the folders after the link was mailed would.
func (h *harness) moveCatalogued(t *testing.T, from, to string) {
	t.Helper()
	ctx := context.Background()
	mover, ok := h.store.(interface {
		MoveNode(ctx context.Context, id int64, parentID *int64, name, path, pathHash string) error
	})
	require.True(t, ok, "the test store cannot move a node")
	n, err := h.store.GetNodeByPath(ctx, h.st.ID, pathkey.Hash(h.st.ID, "/"+from))
	require.NoError(t, err)
	require.NotNil(t, n, "%s is not catalogued", from)
	require.NoError(t, mover.MoveNode(ctx, n.ID, n.ParentID, path.Base(to), "/"+to, pathkey.Hash(h.st.ID, "/"+to)))
	dst := filepath.Join(h.root, filepath.FromSlash(to))
	require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
	require.NoError(t, os.Rename(filepath.Join(h.root, filepath.FromSlash(from)), dst))
}

// runStampedJob is harness.runJob with the params the job row carries, the
// outcome as it is: a refused job is an answer here, not a failed test.
func (h *harness) runStampedJob(t *testing.T, p *Installed, action string, paths []string, params map[string]any) (*model.AppPluginJob, error) {
	t.Helper()
	pj, _ := json.Marshal(paths)
	pb, _ := json.Marshal(params)
	job := &model.AppPluginJob{ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: action,
		StorageID: h.st.ID, PathsJSON: string(pj), ParamsJSON: string(pb), Locale: "en", Label: "x", Status: model.AppPluginJobPending}
	require.NoError(t, h.store.CreateAppPluginJob(context.Background(), job))
	err := h.reg.RunPluginAction(context.Background(), &ops.Op{ID: 1, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: paths, Dest: job.ID}, nil)
	got, gerr := h.store.GetAppPluginJob(context.Background(), job.ID)
	require.NoError(t, gerr)
	return got, err
}

// pageOpen is the visitor opening the page: echo's opening surface says how
// many files the page was handed (`inputs=`) - the link's document and the
// copy it exposed.
func (h *harness) pageOpen(t *testing.T, sh *model.Share) string {
	t.Helper()
	_, ins, err := h.reg.LoadPage(context.Background(), sh.Token)
	require.NoError(t, err)
	s, err := h.reg.PageEvent(context.Background(), sh, ins, wire.ViewEventInput{Event: "open",
		Context: wire.CallContext{Locale: "en"}}, "203.0.113.9")
	require.NoError(t, err)
	require.NotEmpty(t, s.Nodes)
	return s.Nodes[0].Props["text"].(map[string]any)["en"].(string)
}

func TestPage_ALinksDocumentMovedOutOfTheRootIsNoLongerReachedThroughIt(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	nf := &fakeNotify{}
	h.reg.SetNotify(nf)
	h.reg.SetVisibility(func(context.Context, *model.User, int64, string) bool { return true })
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	h.seedRunsOn(t, p, "docs/a.txt")

	sh := h.inviteStamped(t, p, "main://docs")
	require.Equal(t, "main://docs", sh.AppRoot)
	// Opened on the same document by a job with no root behind it.
	open := h.inviteStamped(t, p, "")
	require.Empty(t, open.AppRoot)

	// Inside the root the page is handed its document, and its ref reaches it.
	assert.Contains(t, h.pageOpen(t, sh), "inputs=2", "the document and the copy the link exposed")
	assert.Contains(t, h.pageProbe(t, sh, "docs/a.txt"), "ref=ok")

	h.moveCatalogued(t, "docs/contract.txt", "secret/contract.txt")
	_, rel := h.reg.PageAnchor(context.Background(), sh)
	require.Equal(t, "secret/contract.txt", rel, "the link follows its document")
	nf.events = nil

	// The screen is not handed the document any more, and the ref that named
	// it is no ref: no notice points at it.
	assert.Contains(t, h.pageOpen(t, sh), "inputs=1", "only the copy the link exposed")
	got := h.pageProbe(t, sh, "docs/a.txt")
	assert.Contains(t, got, "ref=not_found", "the page's own ref no longer reaches a document outside the root")
	assert.NotContains(t, got, "secret")
	assert.False(t, noticeOn(nf, "secret/contract.txt"), "no notice points at the document outside the root")
	assert.Contains(t, got, "path=ok", "a file inside the root is named as before")

	// The visitor's job, as the page door queues it - on the document where
	// it lies now, stamped with the link's root - is refused when it runs,
	// before it reads or writes anything.
	params := map[string]any{"from_page": true}
	StampJobRoot(params, sh.AppRoot)
	job, err := h.runStampedJob(t, p, "upper", []string{rel}, params)
	require.Error(t, err)
	assert.Equal(t, model.AppPluginJobFailed, job.Status)
	assert.Contains(t, job.Error, "not inside the folder this job is held to")
	assert.NotContains(t, h.sink.siblings, "secret/contract-upper.txt", "nothing was written beside it")
	_, serr := os.Stat(filepath.Join(h.root, "secret", "contract-upper.txt"))
	assert.True(t, os.IsNotExist(serr), "nothing was written beside it: %v", serr)
	_, found, err := h.store.GetAppPluginState(context.Background(), p.Row.ID, h.st.ID, pathkey.Hash(h.st.ID, "/secret/contract.txt"), "runs")
	require.NoError(t, err)
	assert.False(t, found, "no state was kept on it")

	// A recorded root that does not read as one: not even inside.
	broken := *sh
	broken.AppRoot = "no storage named"
	assert.Contains(t, h.pageOpen(t, &broken), "inputs=1")

	// A link opened with no root follows its document anywhere, as before.
	assert.Contains(t, h.pageOpen(t, open), "inputs=2")
	got = h.pageProbe(t, open, "docs/a.txt")
	assert.Contains(t, got, "ref=ok")
	assert.True(t, noticeOn(nf, "secret/contract.txt"), "a link with no root behind it is held to none")
}
