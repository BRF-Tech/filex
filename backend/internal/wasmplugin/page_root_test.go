package wasmplugin

// A visitor's screen on an app's page is held to the root of the job that
// opened the link (filex Roadmap #161, after #154).
//
// 0.52.0 held the job a visitor's submit queues to that root (the link records
// it, model.Share.AppRoot, and the door stamps it: StampJobRoot). The page
// event that draws the visitor's screen was not held to it. The screen has no
// person behind it, so on a server the ACL already tells it about no file in
// state_list - but a notice's `target.path` is judged by the app's own state,
// not by the ACL, and a page could point a notice at any file the app keeps
// state on, outside the root included. These drive the page through PageEvent
// with an ACL that lets every row through, so that what holds the screen is
// the root and nothing else.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// inviteStamped opens echo's signing link on docs/contract.txt from a job
// whose door stamped `root` (StampJobRoot; "" stamps nothing), and loads it.
func (h *harness) inviteStamped(t *testing.T, p *Installed, root string) *model.Share {
	t.Helper()
	params := map[string]any{}
	StampJobRoot(params, root)
	job := h.runJobWith(t, p, "invite", []string{"docs/contract.txt"}, params)
	url, _, _ := strings.Cut(job.Message, "|")
	sh, _, err := h.reg.LoadPage(context.Background(), url[strings.LastIndex(url, "/")+1:])
	require.NoError(t, err)
	return sh
}

// pageProbe is echo's page asked with the event "probe": what state_list told
// it, and what notify_send answered for a notice on `path` and on the page's
// own file by ref.
func (h *harness) pageProbe(t *testing.T, sh *model.Share, path string) string {
	t.Helper()
	_, ins, err := h.reg.LoadPage(context.Background(), sh.Token)
	require.NoError(t, err)
	s, err := h.reg.PageEvent(context.Background(), sh, ins, wire.ViewEventInput{Event: "probe",
		Data: map[string]any{"path": path}, Context: wire.CallContext{Locale: "en"}}, "203.0.113.9")
	require.NoError(t, err)
	require.NotEmpty(t, s.Nodes)
	return s.Nodes[0].Props["text"].(map[string]any)["en"].(string)
}

// noticeOn reports whether a notice points at rel.
func noticeOn(nf *fakeNotify, rel string) bool {
	for _, e := range nf.events {
		if e.Node != nil && e.Node.Path == "/"+rel {
			return true
		}
	}
	return false
}

func TestPageEvent_AVisitorsScreenIsHeldToTheRootOfTheJobThatOpenedTheLink(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	nf := &fakeNotify{}
	h.reg.SetNotify(nf)
	h.reg.SetVisibility(func(context.Context, *model.User, int64, string) bool { return true })
	h.writeCatalogued(t, "docs/contract.txt", "the terms")
	h.seedRunsOn(t, p, "docs/a.txt", "secret/s.txt")

	sh := h.inviteStamped(t, p, "main://docs")
	require.Equal(t, "main://docs", sh.AppRoot, "the link records the root of the job that opened it")

	got := h.pageProbe(t, sh, "secret/s.txt")
	assert.Contains(t, got, "listed=main://docs/a.txt", "a file inside the root is listed")
	assert.NotContains(t, got, "secret", "the visitor's screen is told nothing outside the root")
	assert.Contains(t, got, "path=permission_denied", "a file outside the root is not one the screen may name")
	assert.Contains(t, got, "ref=ok", "the page's own file, by ref, is unchanged")
	assert.False(t, noticeOn(nf, "secret/s.txt"), "no notice points outside the root")

	// Inside the root a file the app keeps state on is named as before.
	got = h.pageProbe(t, sh, "docs/a.txt")
	assert.Contains(t, got, "path=ok")
	assert.True(t, noticeOn(nf, "docs/a.txt"))

	// A recorded root that does not read as one confines the screen to
	// nothing, never to everything: the host wrote it, so it is a broken row.
	broken := *sh
	broken.AppRoot = "no storage named"
	got = h.pageProbe(t, &broken, "docs/a.txt")
	assert.Contains(t, got, "listed= ", "a broken root lists nothing")
	assert.Contains(t, got, "path=permission_denied", "...and names nothing by path")
	// ...nor the page's own file by ref: a ref is judged against the root on
	// every use, and nothing is inside a broken one (filex #185).
	assert.Contains(t, got, "ref=not_found", "...nor by ref")

	// A link opened with no root: the visitor's screen is what it was.
	open := h.inviteStamped(t, p, "")
	require.Empty(t, open.AppRoot)
	got = h.pageProbe(t, open, "secret/s.txt")
	assert.Contains(t, got, "main://secret/s.txt", "a link with no root behind it is held to none")
	assert.Contains(t, got, "path=ok")
}

func TestLinkRoot_ReadsTheRootALinkRecorded(t *testing.T) {
	_, ok := linkRoot("")
	assert.False(t, ok, "a link opened with no root")

	got, ok := linkRoot("main://projeler/acme")
	assert.True(t, ok)
	assert.Equal(t, "main://projeler/acme", got.String())

	got, ok = linkRoot("no storage named")
	assert.True(t, ok, "a recorded root that does not parse still confines")
	assert.False(t, got.Within("main", "no storage named"), "...to nothing")
	assert.False(t, got.Within("main", ""))
}
