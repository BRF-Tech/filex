package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// ── discard: only what the server still has (task #259) ──────────────────
//
// The owner's decision of 2026-10-15: "Delete from this computer" deletes a
// held file only when the server holds an identical copy of it (sha256) - in
// the person's trash, or elsewhere in the pair's storage. A held file is held
// BECAUSE the server does not have it at that path; without the copy the
// deletion would be the last copy of it gone.

const staleBody = "cleaned up on the server"

// copyServer is the server a discard asks: the trash listing, the trash's
// own comparison (POST /trash/same), the search, and the download.
type copyServer struct {
	*httptest.Server
	trash        map[int64]string  // trash entry id -> its bytes (name stale.txt)
	live         map[string]string // storage-relative path -> bytes, in docs://
	searchStatus int               // non-zero: the search answers this
	same         atomic.Int64      // POST /trash/same requests
	downloads    atomic.Int64
}

func newCopyServer(t *testing.T) *copyServer {
	t.Helper()
	cs := &copyServer{trash: map[int64]string{}, live: map[string]string{}}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/files/manager/trash":
			var entries []map[string]any
			for id, b := range cs.trash {
				entries = append(entries, map[string]any{"id": id, "storage_id": 1, "storage_name": "docs",
					"path": "/work/stale.txt", "name": "stale.txt", "size": len(b)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries, "total": len(entries), "limit": 500, "offset": 0})
		case r.Method == http.MethodPost && r.URL.Path == "/api/files/manager/trash/same":
			cs.same.Add(1)
			var req struct {
				Items []struct {
					ID     int64  `json:"id"`
					SHA256 string `json:"sha256"`
				} `json:"items"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			var out []map[string]any
			for _, it := range req.Items {
				b, ok := cs.trash[it.ID]
				out = append(out, map[string]any{"id": it.ID, "same": ok && sumOf([]byte(b)) == it.SHA256, "checked": true})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": out})
		case r.URL.Path == "/api/files/search":
			if cs.searchStatus != 0 {
				w.WriteHeader(cs.searchStatus)
				_, _ = w.Write([]byte(`{"error":"search_disabled"}`))
				return
			}
			q := r.URL.Query()
			var hits []map[string]any
			for rel, b := range cs.live {
				if path.Base(rel) == q.Get("q") && strconv.Itoa(len(b)) == q.Get("min_size") {
					hits = append(hits, map[string]any{"id": 9, "storage_id": 1, "name": path.Base(rel), "path": "/" + rel, "type": "file", "size": len(b)})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": hits, "total": len(hits)})
		case r.URL.Path == "/api/files/manager" && r.URL.Query().Get("action") == "download":
			cs.downloads.Add(1)
			b, ok := cs.live[strings.TrimPrefix(r.URL.Query().Get("path"), "docs://")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.Itoa(len(b)))
			_, _ = w.Write([]byte(b))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not_found"}`))
		}
	}))
	t.Cleanup(cs.Close)
	return cs
}

func (cs *copyServer) discard(t *testing.T, extra ...string) (error, string) {
	t.Helper()
	err, out, _ := runSync(t, context.Background(), append([]string{"discard", "pair-1", "--url", cs.URL, "--token", "t"}, extra...)...)
	return err, out
}

func stillHeld(t *testing.T, st *filesync.Store) filesync.Pair {
	t.Helper()
	pairs, err := st.LoadPairs()
	require.NoError(t, err)
	return pairs[0]
}

// Its identical copy is in the person's trash on the server: deleted here.
func TestSyncDiscard_DeletesAHeldFileTheServersTrashHolds(t *testing.T) {
	st, _, file := holdingPair(t)
	cs := newCopyServer(t)
	cs.trash[7] = staleBody

	err, out := cs.discard(t)
	require.NoError(t, err)
	require.Equal(t, "pair-1: deleted 1 held file(s) from this computer - the server holds an identical copy of each.\n", out)
	require.NoFileExists(t, file)
	// Deleted, not moved into a local trash (task #259).
	require.NoDirExists(t, filepath.Join(st.Dir, "trash"))
	require.False(t, stillHeld(t, st).HoldNew, "nothing is left to decide about")
}

// Its identical copy is a live file elsewhere in the pair's storage: deleted
// here too, after its bytes were downloaded and compared.
func TestSyncDiscard_DeletesAHeldFileTheStorageHoldsElsewhere(t *testing.T) {
	st, _, file := holdingPair(t)
	cs := newCopyServer(t)
	cs.live["archive/2025/stale.txt"] = staleBody

	err, out := cs.discard(t, "--json", "--lang", "en")
	require.NoError(t, err)
	require.NoFileExists(t, file)
	evs := streamEvents(t, out)
	require.Equal(t, "hello", evs[0]["code"])
	require.Equal(t, "discard.deleted", evs[1]["code"])
	require.Equal(t, "stale.txt", param(evs[1], "path"))
	require.Equal(t, "docs://archive/2025/stale.txt", param(evs[1], "where"))
	require.Equal(t, "discard.done", evs[len(evs)-1]["code"])
	require.EqualValues(t, 1, param(evs[len(evs)-1], "deleted"))
	require.False(t, stillHeld(t, st).HoldNew)
}

// ⚠⚠ The point of the rule. The trash has a file of the same name and size,
// with other bytes, and the storage has nothing: the server holds no copy, so
// the file stays - and stays HELD (released, the next run would upload it) -
// and the person is told why.
func TestSyncDiscard_KeepsAHeldFileTheServerHasNoCopyOf(t *testing.T) {
	st, _, file := holdingPair(t)
	cs := newCopyServer(t)
	cs.trash[7] = strings.Repeat("x", len(staleBody)) // same size, other bytes

	err, out := cs.discard(t)
	require.NoError(t, err)
	require.FileExists(t, file)
	require.Contains(t, out, "  stale.txt - the server has no identical copy of it\n")
	require.Contains(t, out, "pair-1: deleted 0 held file(s) from this computer")
	require.Contains(t, out, "1 item(s) stay here and are still held")
	p := stillHeld(t, st)
	require.True(t, p.HoldNew, "a file kept here must stay held, or the next run uploads it")
	require.Equal(t, 1, p.Held)
	require.EqualValues(t, 1, cs.same.Load(), "the trash entry of its size was compared on the server")
}

// A file that changed after it was held is not what the person decided
// about: it stays, held, and the server is not even asked.
func TestSyncDiscard_KeepsAFileThatChangedAfterItWasHeld(t *testing.T) {
	st, _, file := holdingPair(t)
	cs := newCopyServer(t)
	cs.trash[7] = staleBody
	require.NoError(t, os.WriteFile(file, []byte("written here since, much longer than before"), 0o644))

	err, out := cs.discard(t, "--json", "--lang", "tr")
	require.NoError(t, err)
	require.FileExists(t, file)
	evs := streamEvents(t, out)
	var kept map[string]any
	for _, ev := range evs {
		if ev["code"] == "discard.kept_changed" {
			kept = ev
		}
	}
	require.NotNil(t, kept, out)
	require.Equal(t, "stale.txt: burada kalıyor; bekletildikten sonra değişti", kept["message"])
	require.Zero(t, cs.same.Load(), "a changed file is not compared")
	require.True(t, stillHeld(t, st).HoldNew)
}

// The server could not search its storage (search off) and the trash does
// not have it: "no copy" cannot be said, so the file stays, and the reason
// says the server could not be asked - not that it has no copy.
func TestSyncDiscard_KeepsAFileWhenTheServerCannotSearch(t *testing.T) {
	st, _, file := holdingPair(t)
	cs := newCopyServer(t)
	cs.searchStatus = http.StatusServiceUnavailable

	err, out := cs.discard(t)
	require.NoError(t, err)
	require.FileExists(t, file)
	require.Contains(t, out, "  stale.txt - the server could not be asked whether it has a copy: ")
	require.True(t, stillHeld(t, st).HoldNew)
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
// the way every server before it does: 501. The counters are atomic because
// a watcher test reads them while the watcher runs (watchFor).
func feedServer(t *testing.T, withFeed bool) (srv *httptest.Server, index, changes *atomic.Int64) {
	t.Helper()
	var mu sync.Mutex
	index, changes = new(atomic.Int64), new(atomic.Int64)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		switch q.Get("action") {
		case "index":
			index.Add(1)
			_, _ = w.Write([]byte(`{"adapter":"docs","files":[]}`))
		case "changes":
			changes.Add(1)
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

// watchFor runs `sync run --watch 20ms` for `window` after the watcher's
// first listing, and on until `seen` holds (30 s at most in all).
//
// ⚠⚠ Not a wall-clock deadline on the whole command. It was 500 ms from the
// start, and on a loaded machine starting up and the first run took most of
// it: in the 0.50 full Go run (six packages at once, on a disk whose fsync
// took hundreds of milliseconds) the watcher made its first run and nothing
// else in time, two listings, and "it still walks" failed on a watcher that
// was fine. 500 ms counted from the first listing still lost one run in
// eight under load. So what a test has to SEE is waited for, and the window
// only bounds how much more may happen: a slow machine can only make fewer
// walks, which keeps the upper bounds honest.
func watchFor(t *testing.T, url string, index *atomic.Int64, window time.Duration, seen func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		defer cancel()
		giveUp := time.Now().Add(30 * time.Second)
		for index.Load() == 0 && ctx.Err() == nil && time.Now().Before(giveUp) {
			time.Sleep(5 * time.Millisecond)
		}
		end := time.Now().Add(window)
		for ctx.Err() == nil && time.Now().Before(giveUp) && (time.Now().Before(end) || !seen()) {
			time.Sleep(5 * time.Millisecond)
		}
	}()
	err, _, _ := runSync(t, ctx, "run", "--url", url, "--token", "t", "--watch", "20ms")
	require.NoError(t, err)
}

// H10: an idle watcher no longer re-lists the tree every tick.
func TestSyncWatch_AQuietPairIsNotWalkedAgain(t *testing.T) {
	dir, st := syncEnv(t)
	srv, index, changes := feedServer(t, true)
	_, err := st.AddPair(filesync.Pair{Local: filepath.Join(dir, "mirror"), Remote: "docs://work"})
	require.NoError(t, err)

	watchFor(t, srv.URL, index, 500*time.Millisecond, func() bool { return changes.Load() > 5 })

	require.LessOrEqual(t, index.Load(), int64(2), "one run: the inventory walk and the settle walk, then no listing at all")
	require.Greater(t, changes.Load(), int64(5), "every tick asks the change log instead")
}

// Against an older server the watcher still walks, backing off while nothing
// happens.
func TestSyncWatch_WithoutAChangeLogItBacksOff(t *testing.T) {
	dir, st := syncEnv(t)
	srv, index, _ := feedServer(t, false)
	_, err := st.AddPair(filesync.Pair{Local: filepath.Join(dir, "mirror"), Remote: "docs://work"})
	require.NoError(t, err)

	watchFor(t, srv.URL, index, 500*time.Millisecond, func() bool { return index.Load() > 2 })

	// ~25 ticks in the window; the old watcher walked on every one of them
	// (2 listings each).
	got := index.Load()
	require.Greater(t, got, int64(2), "it still walks")
	require.Less(t, got, int64(14), "but not on every tick: got %d listings", got)
}
