package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/filesync"
)

// syncEnv points every piece of state the sync commands touch at a temp dir,
// so a test never reads or writes the developer's own ~/.filex.
func syncEnv(t *testing.T) (dir string, st *filesync.Store) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("FILEX_SYNC_DIR", filepath.Join(dir, "state"))
	t.Setenv("FILEX_CLI_CONFIG", filepath.Join(dir, "cli.yaml"))
	t.Setenv("FILEX_UPLOAD_STATE", filepath.Join(dir, "uploads"))
	t.Setenv("FILEX_URL", "")
	t.Setenv("FILEX_TOKEN", "")
	return dir, &filesync.Store{Dir: filepath.Join(dir, "state")}
}

// runSync executes `filex sync <args…>` in-process and returns its error and
// what it printed.
func runSync(t *testing.T, ctx context.Context, args ...string) (error, string, string) {
	t.Helper()
	cmd := syncCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	return err, out.String(), errOut.String()
}

// A token revoked on the server used to leave the watcher retrying every
// 30 s forever — and the desktop app, which only saw stderr lines, showed an
// error screen whose only button retried the same dead token. The watcher now
// stops, and says why with an exit status a supervisor can act on.
func TestSyncRun_ARevokedTokenStopsTheWatcherWithStatus3(t *testing.T) {
	dir, st := syncEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(srv.Close)
	_, err := st.AddPair(filesync.Pair{Local: filepath.Join(dir, "mirror"), Remote: "docs://work"})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err, _, _ = runSync(t, ctx, "run", "--url", srv.URL, "--token", "revoked", "--watch", "20ms")

	require.Error(t, err, "a watcher must not keep going on a token the server rejects")
	require.NoError(t, ctx.Err(), "it must stop at once, not retry until the test gives up")
	var ee *exitError
	require.True(t, errors.As(err, &ee), "want an exit status, got %T: %v", err, err)
	require.Equal(t, exitSignedOut, ee.code)
	require.Equal(t, "signed out: the server no longer accepts this token (HTTP 401)", err.Error())
}

func TestExitCodeFollowsTheErrorChain(t *testing.T) {
	require.Equal(t, 0, exitCode(nil))
	require.Equal(t, 1, exitCode(errors.New("anything")))
	require.Equal(t, exitSignedOut, exitCode(&exitError{code: exitSignedOut, err: errSignedOut}))
	wrapped := fmt.Errorf("pair-1: %w", &exitError{code: exitSignedOut, err: errSignedOut})
	require.Equal(t, exitSignedOut, exitCode(wrapped))
}

// holdingPair stores a pair that a first run left holding one local file.
func holdingPair(t *testing.T) (st *filesync.Store, p filesync.Pair, file string) {
	t.Helper()
	dir, st := syncEnv(t)
	local := filepath.Join(dir, "mirror")
	file = filepath.Join(local, "stale.txt")
	require.NoError(t, os.MkdirAll(local, 0o755))
	require.NoError(t, os.WriteFile(file, []byte("cleaned up on the server"), 0o644))
	info, err := os.Stat(file)
	require.NoError(t, err)
	p = filesync.Pair{ID: "pair-1", Local: local, Remote: "docs://work", HoldNew: true, Held: 1}
	require.NoError(t, st.SavePairs([]filesync.Pair{p}))
	sig := (filesync.Node{Size: info.Size(), ModMillis: info.ModTime().UnixMilli()}).Signature()
	require.NoError(t, os.MkdirAll(filepath.Join(st.Dir, "held"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(st.Dir, "held", "pair-1.json"), []byte(`{"stale.txt":"`+sig+`"}`), 0o600))
	return st, p, file
}

func TestSyncConfirm_EndsTheHold(t *testing.T) {
	st, _, file := holdingPair(t)

	err, out, _ := runSync(t, context.Background(), "confirm", "pair-1")
	require.NoError(t, err)
	require.Equal(t, "pair-1: 1 held item(s) go to the server on the next run.\n", out)
	pairs, err := st.LoadPairs()
	require.NoError(t, err)
	require.False(t, pairs[0].HoldNew)
	require.Zero(t, pairs[0].Held)
	require.FileExists(t, file, "confirm touches no file")
}

func TestSyncDiscard_MovesTheHeldFilesToTheLocalTrash(t *testing.T) {
	st, _, file := holdingPair(t)

	err, out, _ := runSync(t, context.Background(), "discard", "pair-1")
	require.NoError(t, err)
	require.Equal(t, "pair-1: moved 1 held file(s) to the local sync trash.\n", out)
	require.NoFileExists(t, file)
	items, err := st.ListTrash("pair-1")
	require.NoError(t, err)
	require.Len(t, items, 1)
	pairs, _ := st.LoadPairs()
	require.False(t, pairs[0].HoldNew)
}

func TestSyncList_JSONCarriesTheHold(t *testing.T) {
	holdingPair(t)

	err, out, _ := runSync(t, context.Background(), "list", "--json")
	require.NoError(t, err)
	require.Contains(t, out, `"hold_new": true`)
	require.Contains(t, out, `"held": 1`)
}

// countingServer answers every request 200 with an empty listing and counts them.
func countingServer(t *testing.T) (*httptest.Server, *int64) {
	t.Helper()
	var n int64
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"adapter":"docs","files":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// outsideWindow moves the watcher's clock to noon for a night-time window.
func outsideWindow(t *testing.T) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local) }
	t.Cleanup(func() { nowFunc = old })
}

func TestSyncRun_OutsideTheWindowDoesNothing(t *testing.T) {
	dir, st := syncEnv(t)
	srv, hits := countingServer(t)
	_, err := st.AddPair(filesync.Pair{Local: filepath.Join(dir, "mirror"), Remote: "docs://work"})
	require.NoError(t, err)
	outsideWindow(t)

	err, out, _ := runSync(t, context.Background(), "run", "--url", srv.URL, "--token", "t", "--window", "22:00-07:00")
	require.NoError(t, err)
	require.Contains(t, out, "outside the sync window 22:00-07:00")
	require.Zero(t, *hits, "not one request outside the window")
}

func TestSyncRun_TheWatcherWaitsForTheWindow(t *testing.T) {
	dir, st := syncEnv(t)
	srv, hits := countingServer(t)
	_, err := st.AddPair(filesync.Pair{Local: filepath.Join(dir, "mirror"), Remote: "docs://work"})
	require.NoError(t, err)
	outsideWindow(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err, out, _ := runSync(t, ctx, "run", "--url", srv.URL, "--token", "t", "--window", "22:00-07:00", "--watch", "20ms")
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(out, "sync: waiting for the sync window 22:00-07:00"), "said once, not every tick:\n%s", out)
	require.Zero(t, *hits)
}

func TestSyncRun_RejectsABadWindowOrLimit(t *testing.T) {
	syncEnv(t)
	err, _, _ := runSync(t, context.Background(), "run", "--url", "http://x", "--token", "t", "--window", "22-07")
	require.ErrorContains(t, err, "--window")
	err, _, _ = runSync(t, context.Background(), "run", "--url", "http://x", "--token", "t", "--limit-up", "-1")
	require.ErrorContains(t, err, "cannot be negative")
}

// feedServer is a tiny server with one empty folder, docs://work, that
// counts listings and change-log questions. withFeed=false answers `changes`
// the way every server before it does: 501.
func feedServer(t *testing.T, withFeed bool) (srv *httptest.Server, index, changes *int64) {
	t.Helper()
	var mu sync.Mutex
	index, changes = new(int64), new(int64)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		switch q.Get("action") {
		case "index":
			*index++
			_, _ = w.Write([]byte(`{"adapter":"docs","files":[]}`))
		case "changes":
			*changes++
			if !withFeed {
				w.WriteHeader(http.StatusNotImplemented)
				_, _ = w.Write([]byte(`{"error":"action not implemented: changes"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"cursor":"e.1","changed":%v}`, q.Get("since") != "e.1")
		default:
			w.WriteHeader(http.StatusNotImplemented)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, index, changes
}

// H10: an idle watcher no longer re-lists the tree every tick.
func TestSyncWatch_AQuietPairIsNotWalkedAgain(t *testing.T) {
	dir, st := syncEnv(t)
	srv, index, changes := feedServer(t, true)
	_, err := st.AddPair(filesync.Pair{Local: filepath.Join(dir, "mirror"), Remote: "docs://work"})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err, _, _ = runSync(t, ctx, "run", "--url", srv.URL, "--token", "t", "--watch", "20ms")
	require.NoError(t, err)

	require.LessOrEqual(t, *index, int64(2), "one run: the inventory walk and the settle walk, then no listing at all")
	require.Greater(t, *changes, int64(5), "every tick asks the change log instead")
}

// Against an older server the watcher still walks, backing off while nothing
// happens.
func TestSyncWatch_WithoutAChangeLogItBacksOff(t *testing.T) {
	dir, st := syncEnv(t)
	srv, index, _ := feedServer(t, false)
	_, err := st.AddPair(filesync.Pair{Local: filepath.Join(dir, "mirror"), Remote: "docs://work"})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err, _, _ = runSync(t, ctx, "run", "--url", srv.URL, "--token", "t", "--watch", "20ms")
	require.NoError(t, err)

	// ~25 ticks; the old watcher walked on every one of them (2 listings each).
	require.Greater(t, *index, int64(2), "it still walks")
	require.Less(t, *index, int64(14), "but not on every tick: got %d listings", *index)
}
