package handlers_test

// A visitor's job on an app's page is held to the root of the token whose job
// opened the link (found in the GHSA-8gvc-6w52-6c7j review, 2026-10-04).
//
// A job a `root:` token queues carries that token's root (`__root`), so the
// app is told nothing outside the folder and may name nothing there. A link
// such a job opens is answered later, by a stranger, and the job the
// stranger's submit queues runs as the link's creator - with no request of the
// token behind it. It carried no root, so the app's follow-up work on that
// link listed (and could name) every file the app keeps state on, as the
// creator's account sees them. The share row now carries the root of the job
// that opened it, and the visitor's job is stamped with it.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// openSigningLinkWithToken is openSigningLinkAs for a token: the token's job
// opens the link, and the new link's token is returned.
func openSigningLinkWithToken(t *testing.T, f *appFixture, tok string, pluginID int64, rel string) string {
	t.Helper()
	ctx := context.Background()
	before, _, err := f.store.ListAppPluginShares(ctx, pluginID, false, 50, 0)
	require.NoError(t, err)
	runAsToken(t, f, tok, "invite", map[string]any{"paths": []string{"main://" + rel}, "params": map[string]any{"pin": "-"}})
	after, _, err := f.store.ListAppPluginShares(ctx, pluginID, false, 50, 0)
	require.NoError(t, err)
	seen := map[int64]bool{}
	for _, s := range before {
		seen[s.Share.ID] = true
	}
	for _, s := range after {
		if !seen[s.Share.ID] {
			return s.Share.Token
		}
	}
	t.Fatal("the token's job opened no link")
	return ""
}

// visitorRuns is a stranger's submit on the link asking for `action`, carried
// out; it answers what the app said.
func visitorRuns(t *testing.T, f *appFixture, token, action string) string {
	t.Helper()
	code, raw := f.visitorSubmit(t, token, map[string]any{"run": action})
	require.Equal(t, http.StatusAccepted, code, string(raw))
	var ans struct {
		JobID string `json:"job_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))
	job, err := f.store.GetAppPluginJob(context.Background(), ans.JobID)
	require.NoError(t, err)
	require.NotNil(t, job.OpID, "the visitor's job has no queue row")
	op := f.drain(t, *job.OpID)
	require.Equal(t, "ok", op["status"], "%v", op)
	job, err = f.store.GetAppPluginJob(context.Background(), ans.JobID)
	require.NoError(t, err)
	return job.Message
}

func TestAppPage_AVisitorsJobIsHeldToTheRootOfTheJobThatOpenedTheLink(t *testing.T) {
	f, tok := rootConfinedApp(t)
	seedRuns(t, f, "main://docs/a.txt", "main://secret/s.txt")
	p, ok := f.reg.ByName("echo")
	require.True(t, ok)

	link := openSigningLinkWithToken(t, f, tok, p.Row.ID, "docs/a.txt")
	msg := visitorRuns(t, f, link, "listing")
	assert.Contains(t, msg, "main://docs/a.txt=1", "a file inside the root is listed")
	assert.NotContains(t, msg, "secret", "a visitor's job on a link a root token's job opened is told nothing outside the root")

	// A link an unconfined session opened: its visitor's job is what it was.
	link = f.openSigningLink(t, p.Row.ID, "docs/b.txt")
	msg = visitorRuns(t, f, link, "listing")
	assert.Contains(t, msg, "main://secret/s.txt=1", "a link with no root behind it is held to none")
}

// pageNotices keeps the notices an app sends.
type pageNotices struct{ events []notify.Event }

func (n *pageNotices) Send(_ context.Context, e notify.Event) (int64, error) {
	n.events = append(n.events, e)
	return int64(len(n.events)), nil
}

// on reports whether a notice points at rel.
func (n *pageNotices) on(rel string) bool {
	for _, e := range n.events {
		if e.Node != nil && e.Node.Path == "/"+rel {
			return true
		}
	}
	return false
}

// visitorProbe is a stranger asking the link's page for echo's "probe" screen:
// what state_list told the screen, and what a notice on `path` (and on the
// page's own file, by ref) was answered.
func (f *appFixture) visitorProbe(t *testing.T, token, path string) string {
	t.Helper()
	code, raw := doReq(t, freshClient(t), http.MethodPost, f.srv.URL+"/api/public/s/"+token+"/event",
		map[string]any{"event": "probe", "data": map[string]any{"path": path}})
	require.Equal(t, http.StatusOK, code, string(raw))
	var ans struct {
		Surface struct {
			Nodes []struct {
				Props struct {
					Text map[string]string `json:"text"`
				} `json:"props"`
			} `json:"nodes"`
		} `json:"surface"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))
	require.NotEmpty(t, ans.Surface.Nodes, string(raw))
	return ans.Surface.Nodes[0].Props.Text["en"]
}

// The visitor's SCREEN on such a link (the page event), not only the job its
// submit queues, is held to the root of the job that opened the link (filex
// Roadmap #161). The screen has no person behind it, so the server's ACL tells
// it about no file in state_list; a notice's target.path is judged by the
// app's own state instead, and named a file outside the root.
func TestAppPage_AVisitorsScreenIsHeldToTheRootOfTheJobThatOpenedTheLink(t *testing.T) {
	f, tok := rootConfinedApp(t)
	// The server's rule (server.go): a person sees what their ACL gives, and
	// nobody - the visitor - sees nothing. Here every person sees the whole
	// storage: the link creator's ACL is wide, the link's root is narrow.
	f.reg.SetVisibility(func(_ context.Context, u *model.User, _ int64, _ string) bool { return u != nil })
	sent := &pageNotices{}
	f.reg.SetNotify(sent)
	seedRuns(t, f, "main://docs/a.txt", "main://secret/s.txt")
	p, ok := f.reg.ByName("echo")
	require.True(t, ok)

	link := openSigningLinkWithToken(t, f, tok, p.Row.ID, "docs/a.txt")
	got := f.visitorProbe(t, link, "secret/s.txt")
	assert.NotContains(t, got, "secret", "the visitor's screen is told nothing outside the root")
	assert.Contains(t, got, "path=permission_denied", "a file outside the root is not one the visitor's screen may name")
	assert.Contains(t, got, "ref=ok", "the page's own file, by ref, is unchanged")
	assert.False(t, sent.on("secret/s.txt"), "no notice points outside the root")

	// Inside the root, a file the app keeps state on is named as before.
	got = f.visitorProbe(t, link, "docs/a.txt")
	assert.Contains(t, got, "path=ok", got)

	// A link an unconfined session opened: its visitor's screen is what it was.
	link = f.openSigningLink(t, p.Row.ID, "docs/b.txt")
	got = f.visitorProbe(t, link, "secret/s.txt")
	assert.Contains(t, got, "path=ok", "a link with no root behind it is held to none")
	assert.True(t, sent.on("secret/s.txt"))
}
