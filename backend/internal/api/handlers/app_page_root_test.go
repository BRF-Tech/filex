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
