package handlers_test

// Admin → Tools → Thumbnail repair, for an administrator's session narrowed to
// a folder by X-Filex-Root (filex Roadmap #185). Starting a repair was already
// held to the folder (confine.Middleware, mounted on /tools); what the tab
// READS was not: the list of files without a thumbnail named every problem
// file in the caller's reach (and its `truncated` said so of the rows outside
// the folder too), the latest run - and the run a busy answer carries - was
// told with its path, storage, counts and refusals whatever folder it was of,
// and the generators were counted over every storage.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestThumbRepair_ProblemsOfASessionNarrowedToAFolderStayInIt(t *testing.T) {
	ctx := context.Background()
	rep := &recordingRepairer{}
	srv, client, store := repairServer(t, false, rep)
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)
	mainSt := plainStorage(t, store, "main")
	yan := plainStorage(t, store, "yan")
	mark := func(storageID int64, p string, at time.Time) {
		n := seedNodeIn(t, store, storageID, p)
		require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{NodeID: n.ID, State: "skipped", Error: "svg_too_large:5242880", AttemptedAt: &at}))
	}
	// The problem inside the folder is the OLDEST: more problems outside it
	// than the list shows came after, so a list cut before the folder's
	// filter would not even reach it.
	old := time.Now().Add(-time.Hour)
	mark(mainSt.ID, "/docs/harita.svg", old)
	now := time.Now()
	for i := 0; i < 505; i++ {
		mark(mainSt.ID, fmt.Sprintf("/secret/s-%03d.svg", i), now)
	}
	mark(yan.ID, "/docs/yan.svg", now)

	narrowed := narrowedTo(client, "main://docs")

	status, body := doJSON(t, narrowed, http.MethodGet, srv.URL+"/api/admin/tools/thumbnails/problems", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	var paths []string
	for _, raw := range body["items"].([]any) {
		paths = append(paths, raw.(map[string]any)["path"].(string))
	}
	assert.Equal(t, []string{"main://docs/harita.svg"}, paths, "only the problems inside main://docs")
	assert.Equal(t, false, body["truncated"], "the problems outside the folder do not make its list a cut one")

	// The session itself, not narrowed: every storage, cut at the list's 500.
	status, body = doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/tools/thumbnails/problems", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Len(t, body["items"].([]any), 500)
	assert.Equal(t, true, body["truncated"])
}

// narrowedTo is the session's client with every request narrowed to root by
// X-Filex-Root, as a host app's proxy sets it.
func narrowedTo(client *http.Client, root string) *http.Client {
	narrowed := *client
	next := narrowed.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	narrowed.Transport = withHeader{next: next, key: "X-Filex-Root", val: root}
	return &narrowed
}

// runFacts are the keys that say WHICH run it is and how it went: a session
// narrowed to a folder is told none of them about a run outside it.
var runFacts = []string{"op_id", "status", "mode", "path", "storage_id", "total", "processed", "ok", "failed", "skipped", "refused", "error", "started_at", "finished_at"}

func TestThumbRepair_ASessionNarrowedToAFolderIsToldOnlyThatAnotherRunIsGoing(t *testing.T) {
	rep := &recordingRepairer{hold: make(chan struct{})}
	srv, client, store := repairServer(t, false, rep)
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)
	mainSt := plainStorage(t, store, "main")
	plainStorage(t, store, "yan")
	seedNodeIn(t, store, mainSt.ID, "/docs")
	narrowed := narrowedTo(client, "main://docs")

	// The session itself repairs another storage, and the run is held going.
	status, body := doJSON(t, client, http.MethodPost, srv.URL+repairURL, map[string]any{"path": "yan://", "mode": "fix"})
	require.Equal(t, http.StatusAccepted, status, "%v", body)

	// Narrowed to main://docs: that a run is going, and nothing about it.
	status, body = doJSON(t, narrowed, http.MethodGet, srv.URL+repairURL, nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, true, body["running"])
	for _, k := range runFacts {
		assert.NotContains(t, body, k, "the latest run is of another folder: %v", body)
	}

	// A repair of its own folder meets the same busy answer, and the run it
	// carries is told the same way.
	status, body = doJSON(t, narrowed, http.MethodPost, srv.URL+repairURL, map[string]any{"path": "", "mode": "fix"})
	require.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "BUSY", body["code"])
	job, ok := body["job"].(map[string]any)
	require.True(t, ok, "the busy answer still carries a run to follow: %v", body)
	assert.Equal(t, true, job["running"])
	for _, k := range runFacts {
		assert.NotContains(t, job, k, "the run going is of another folder: %v", job)
	}

	// Ended, it is nothing to the narrowed session - as if none had been
	// asked for - and everything to the session itself.
	close(rep.hold)
	st := waitRepairIdle(t, client, srv.URL)
	assert.Equal(t, "yan://", st["path"])
	status, body = doJSON(t, narrowed, http.MethodGet, srv.URL+repairURL, nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, map[string]any{"running": false}, body)

	// A run of its own folder is told to it in full.
	status, body = doJSON(t, narrowed, http.MethodPost, srv.URL+repairURL, map[string]any{"path": "", "mode": "fix"})
	require.Contains(t, []int{http.StatusOK, http.StatusAccepted}, status, "%v", body)
	assert.Equal(t, "main://docs", body["path"])
	st = waitRepairIdle(t, narrowed, srv.URL)
	assert.Equal(t, "main://docs", st["path"])
	assert.Equal(t, float64(4), st["processed"])
}

func TestThumbRepair_GeneratorsOfASessionNarrowedToAFolderAreItsStoragesOnly(t *testing.T) {
	ctx := context.Background()
	rep := &recordingRepairer{}
	srv, client, store := repairServer(t, false, rep)
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)
	mainSt := plainStorage(t, store, "main")
	yan := plainStorage(t, store, "yan")
	drawn := func(storageID int64, p, generator string) {
		n := seedNodeIn(t, store, storageID, p)
		require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{NodeID: n.ID, State: "ready", Generator: generator}))
	}
	drawn(mainSt.ID, "/docs/a.png", "builtin")
	drawn(yan.ID, "/b.png", "app:cizer@1.0.0")
	drawn(yan.ID, "/c.png", "builtin")

	counts := func(c *http.Client) map[string]float64 {
		t.Helper()
		status, body := doJSON(t, c, http.MethodGet, srv.URL+"/api/admin/tools/thumbnails/generators", nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
		out := map[string]float64{}
		for _, raw := range body["generators"].([]any) {
			g := raw.(map[string]any)
			out[g["generator"].(string)] = g["count"].(float64)
		}
		return out
	}

	assert.Equal(t, map[string]float64{"builtin": 1}, counts(narrowedTo(client, "main://docs")),
		"narrowed to main://docs: the root's own storage is counted, the other is not")
	assert.Equal(t, map[string]float64{}, counts(narrowedTo(client, "yok://docs")),
		"a root on a storage that does not resolve counts nothing")
	assert.Equal(t, map[string]float64{"builtin": 2, "app:cizer@1.0.0": 1}, counts(client), "the session itself: every storage")
}
