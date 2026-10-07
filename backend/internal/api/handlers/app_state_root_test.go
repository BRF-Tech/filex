package handlers_test

// An app's own bookkeeping is held to a `root:` token's folder (filex Roadmap
// #154, found in the GHSA-8gvc-6w52-6c7j review, 2026-10-04).
//
// state_list answers an app "which files do I keep this key on?" with each
// file's adapter-qualified path, its name and the app's value for it. Every
// row went through the asking PERSON's ACL and never through the root of the
// token the call was made with, so a token confined to main://docs that opened
// an app's screen - or queued one of its jobs - was told about every file the
// app keeps state on anywhere on the storage. And a job could NAME such a file
// by path ("a file this app keeps state on" is what a lock, a notice or a page
// link may name besides the job's inputs): the token's job froze a file
// outside its folder, and lifted the app's lock on one.
//
// Every probe binds the token to an ADMIN account (rootConfinedApp): an
// admin's ACL clears every path, so nothing but the token's root can refuse.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// seedRuns has the administrator's own session run `upper` on a file inside
// the token's folder and on one outside it: the app now keeps its `runs`
// counter on both.
func seedRuns(t *testing.T, f *appFixture, paths ...string) {
	t.Helper()
	for _, p := range paths {
		op := f.runAndDrain(t, f.admin, "upper", map[string]any{"paths": []string{p}})
		require.Equal(t, "ok", op["status"], "%s: %v", p, op)
	}
}

// runAsToken queues an echo action with the token and waits for its job; the
// job's message is what the app said.
func runAsToken(t *testing.T, f *appFixture, tok, action string, body map[string]any) string {
	t.Helper()
	code, raw := appAs(t, f, tok, "/api/files/plugins/actions/echo/"+action+"/run", body)
	require.Equal(t, http.StatusAccepted, code, raw)
	var ans struct {
		Op struct {
			ID int64 `json:"id"`
		} `json:"op"`
		JobID string `json:"job_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &ans))
	op := f.drain(t, ans.Op.ID)
	require.Equal(t, "ok", op["status"], "%v", op)
	job, err := f.store.GetAppPluginJob(context.Background(), ans.JobID)
	require.NoError(t, err)
	require.NotNil(t, job)
	return job.Message
}

// viewText opens echo's `hello` screen at a section and reads what it drew.
func viewText(t *testing.T, f *appFixture, client *http.Client, tok, section string) string {
	t.Helper()
	u := f.srv.URL + "/api/files/plugins/views/echo/hello?storage_id=" + strconv.FormatInt(f.st.ID, 10) + "&section=" + url.QueryEscape(section)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	require.NoError(t, err)
	if tok != "" {
		req.Header.Set("X-Filex-Token", tok)
	}
	res, err := client.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	require.Equal(t, http.StatusOK, res.StatusCode, string(raw))
	return string(raw)
}

func TestAppState_RootToken_AScreenListsOnlyInsideTheRoot(t *testing.T) {
	f, tok := rootConfinedApp(t)
	seedRuns(t, f, "main://docs/a.txt", "main://secret/s.txt")

	// Unconfined, the screen is told about both: the row outside is there to
	// be told about.
	all := viewText(t, f, f.admin, "", "state_list")
	require.Contains(t, all, "main://docs/a.txt")
	require.Contains(t, all, "main://secret/s.txt")

	got := viewText(t, f, &http.Client{}, tok, "state_list")
	assert.Contains(t, got, "main://docs/a.txt", "a file inside the root is listed")
	assert.NotContains(t, got, "secret", "a file outside the token's root is not named to its screen")
}

func TestAppState_RootToken_AJobListsOnlyInsideTheRoot(t *testing.T) {
	f, tok := rootConfinedApp(t)
	seedRuns(t, f, "main://docs/a.txt", "main://secret/s.txt")

	msg := runAsToken(t, f, tok, "listing", map[string]any{"paths": []string{"main://docs/b.txt"}, "params": map[string]any{}})
	assert.Contains(t, msg, "main://docs/a.txt=1", "a file inside the root is listed")
	assert.NotContains(t, msg, "secret", "a job the token queued is not told about a file outside its root")
}

func TestAppState_RootToken_AJobNamesNoFileOutsideTheRoot(t *testing.T) {
	f, tok := rootConfinedApp(t)
	seedRuns(t, f, "main://docs/a.txt", "main://secret/s.txt")
	ctx := context.Background()
	lockOn := func(rel string) bool {
		l, err := f.store.GetAppPluginLock(ctx, f.st.ID, pathkey.Hash(f.st.ID, "/"+rel))
		require.NoError(t, err)
		return l != nil
	}

	// A lock by path on a file the app keeps state on, outside the root.
	msg := runAsToken(t, f, tok, "listing", map[string]any{"paths": []string{"main://docs/b.txt"}, "params": map[string]any{"lock": "main://secret/s.txt"}})
	assert.Contains(t, msg, "lock=permission_denied", msg)
	assert.False(t, lockOn("secret/s.txt"), "the token's job froze a file outside its folder")

	// The same, inside the root: a.txt is not this job's input, but the app
	// keeps state on it.
	msg = runAsToken(t, f, tok, "listing", map[string]any{"paths": []string{"main://docs/b.txt"}, "params": map[string]any{"lock": "main://docs/a.txt"}})
	assert.Contains(t, msg, "lock=ok", msg)
	assert.True(t, lockOn("docs/a.txt"), "inside the root a file the app keeps state on may still be named")

	// The app's OWN lock outside the root, taken by an unconfined session, is
	// not lifted through the token's job either.
	op := f.runAndDrain(t, f.admin, "listing", map[string]any{"paths": []string{"main://docs/b.txt"}, "params": map[string]any{"lock": "main://secret/s.txt"}})
	require.Equal(t, "ok", op["status"], "%v", op)
	require.True(t, lockOn("secret/s.txt"), "unconfined, the app locks a file it keeps state on")
	msg = runAsToken(t, f, tok, "listing", map[string]any{"paths": []string{"main://docs/b.txt"}, "params": map[string]any{"unlock": "main://secret/s.txt"}})
	assert.Contains(t, msg, "unlock=permission_denied", msg)
	assert.True(t, lockOn("secret/s.txt"), "the token's job lifted a lock outside its folder")
}

// A screen may send its person to a file; the link is kept only for a file the
// person could open themselves, and a `root:` token opens nothing outside its
// folder.
func TestAppState_RootToken_AScreenSendsNobodyOutsideTheRoot(t *testing.T) {
	f, tok := rootConfinedApp(t)
	open := func(p string) any {
		code, raw := appAs(t, f, tok, "/api/files/plugins/views/echo/hello/event", map[string]any{
			"event": "change", "storage_id": f.st.ID, "data": map[string]any{"open": p},
		})
		require.Equal(t, http.StatusOK, code, raw)
		var ans struct {
			Surface map[string]any `json:"surface"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &ans))
		return ans.Surface["open"]
	}
	assert.NotNil(t, open("main://docs/a.txt"), "a file inside the root keeps its link")
	assert.Nil(t, open("main://secret/s.txt"), "a file outside the token's root loses its link")
}

// A screen opened on no file (a home page) still names a storage, and its call
// is told that storage's name and reads through its driver. A `root:` token
// opens one only on its own root's storage; any other answers what every app
// door answers outside the root, whether or not the storage exists.
func TestAppState_RootToken_AScreenOnAStorageOutsideTheRootDoesNotOpen(t *testing.T) {
	f, tok := rootConfinedApp(t)
	yan, _ := f.addStorage(t, "yan")
	open := func(id int64) (int, string) {
		u := f.srv.URL + "/api/files/plugins/views/echo/hello?storage_id=" + strconv.FormatInt(id, 10)
		req, err := http.NewRequest(http.MethodGet, u, nil)
		require.NoError(t, err)
		req.Header.Set("X-Filex-Token", tok)
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(raw)
	}
	event := func(id int64) (int, string) {
		return appAs(t, f, tok, "/api/files/plugins/views/echo/hello/event", map[string]any{"event": "change", "storage_id": id})
	}

	code, body := open(f.st.ID)
	require.Equal(t, http.StatusOK, code, "a home page on the root's own storage opens: %s", body)
	code, body = event(f.st.ID)
	require.Equal(t, http.StatusOK, code, body)

	// What an app door answers a storage outside the root named by id, one
	// that is not there. (A storage named in a path is answered before any
	// door, by confine.Middleware, whatever the body's Content-Type.)
	code, want := event(yan.ID + 1000)
	require.Equal(t, http.StatusForbidden, code, want)
	for _, id := range []int64{yan.ID, yan.ID + 1000} {
		code, body = open(id)
		assert.Equal(t, http.StatusForbidden, code, "storage %d, opened: %s", id, body)
		assert.Equal(t, want, body, "storage %d, opened", id)
		code, body = event(id)
		assert.Equal(t, http.StatusForbidden, code, "storage %d, an event: %s", id, body)
		assert.Equal(t, want, body, "storage %d, an event", id)
	}
}
