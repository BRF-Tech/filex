package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/filesync"
)

// One engine per pair on this computer, as the CLI and the desktop app see it
// (synclock.go). The desktop app parses the `lock:` lines
// (desktop/src/syncstatus.ts); the exit status 4 is for scripts.

// fakeLocker plays other processes: a pair in busy cannot be taken.
type fakeLocker struct {
	mu   sync.Mutex
	busy map[string]bool
	held map[string]bool
}

func newFakeLocker(busy ...string) *fakeLocker {
	f := &fakeLocker{busy: map[string]bool{}, held: map[string]bool{}}
	for _, id := range busy {
		f.busy[id] = true
	}
	return f
}

func (f *fakeLocker) take(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy[id] {
		return &filesync.BusyError{Pair: id, Holder: filesync.LockHolder{PID: 4242, Exe: `C:\Store\filex.exe`}}
	}
	f.held[id] = true
	return nil
}

func (f *fakeLocker) retain(keep map[string]bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.held {
		if !keep[id] {
			delete(f.held, id)
		}
	}
}

func (f *fakeLocker) releaseAll() { f.retain(nil) }

func (f *fakeLocker) free(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.busy, id)
}

func (f *fakeLocker) holds(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[id]
}

var otherPair = filesync.Pair{ID: "p2", Local: "/tmp/y", Remote: "docs://other"}

// A watcher with two pairs, one of which another process is syncing: the
// other pair syncs as if nothing were wrong, the busy one gets no pass, no
// stream root — and is reported once, after the grace, not on every try.
// When the other process lets go the watcher takes the pair over with a full
// pass, without being restarted.
func TestLiveBusyPairWaitsAndTheOthersSync(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair, otherPair}, time.Hour)
	lk := newFakeLocker("p1")
	r.loop.locker = lk
	r.loop.errOut = r.out
	r.loop.lockGrace = 120 * time.Millisecond
	r.loop.lockRetryFast = 10 * time.Millisecond
	r.loop.lockRetry = 30 * time.Millisecond
	r.start()

	if p := r.next(time.Second); p.pair != "p2" || p.dirs != nil {
		t.Fatalf("the free pair's startup pass: %+v", p)
	}
	r.none(250 * time.Millisecond)
	stream := r.loop.stream.(*fakeStream)
	stream.mu.Lock()
	roots := strings.Join(stream.roots, ",")
	stream.mu.Unlock()
	if roots != "docs://other" {
		t.Fatalf("a busy pair must not be watched on the server: roots %q", roots)
	}
	want := "p1: lock: busy — another filex on this computer is syncing this pair (process 4242, C:\\Store\\filex.exe)\n"
	if got := strings.Count(r.out.String(), "p1: lock: busy"); got != 1 || !strings.Contains(r.out.String(), want) {
		t.Fatalf("want the busy line exactly once:\n%s", r.out.String())
	}

	lk.free("p1")
	if p := r.next(time.Second); p.pair != "p1" || p.dirs != nil {
		t.Fatalf("taking the pair over must start with a full pass: %+v", p)
	}
	if !strings.Contains(r.out.String(), "p1: lock: acquired\n") {
		t.Fatalf("the takeover must be said: %q", r.out.String())
	}
	if !lk.holds("p1") || !lk.holds("p2") {
		t.Fatal("the watcher must hold both pairs now")
	}

	// A pair that is removed gives its lock back.
	r.set(func() { r.pairs = []filesync.Pair{otherPair} })
	r.loop.notePairsFile()
	waitUntil(t, time.Second, "p1's lock to be given back", func() bool { return !lk.holds("p1") })
}

// Within the grace a busy pair is not reported: the desktop app's restart of
// its own watcher meets the old one's lock for a moment, and that is not news.
func TestLiveBusyPairIsNotReportedWithinTheGrace(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, time.Hour)
	lk := newFakeLocker("p1")
	r.loop.locker = lk
	r.loop.lockGrace = time.Hour
	r.loop.lockRetryFast = 10 * time.Millisecond
	r.start()
	r.none(150 * time.Millisecond)
	lk.free("p1")
	r.next(time.Second)
	if strings.Contains(r.out.String(), "lock:") {
		t.Fatalf("a busy spell inside the grace must stay quiet: %q", r.out.String())
	}
}

func TestReporter_ABusyPairIsItsOwnLineNotAFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	r := newPassReporter(&out, &errOut)
	busy := &filesync.BusyError{Pair: "pair-1", Holder: filesync.LockHolder{PID: 7}}
	r.pass(p1, false, filesync.Result{}, fmt.Errorf("wrapped: %w", busy))
	require.Equal(t, "pair-1: lock: busy — another filex on this computer is syncing this pair (process 7)\n", errOut.String())
	require.Empty(t, out.String())
	require.False(t, r.failing["pair-1"], "busy is not a failed pass")
}

func TestExitCodeForBusyPairs(t *testing.T) {
	err := &exitError{code: exitPairBusy, err: errPairsBusy([]string{"pair-1", "pair-3"})}
	require.Equal(t, 4, exitCode(err))
	require.Equal(t, "2 pairs were not synced (pair-1, pair-3): another filex on this computer is syncing them — "+
		"run again once that one has stopped (in the desktop app: Pause sync, or quit it)", err.Error())
}

// ── end to end: two real engines, one of them in another process ──────────

// TestSyncEngineHelperProcess is `filex sync …` in a process of its own — the
// "other filex" of the test below. Its arguments come from the environment.
func TestSyncEngineHelperProcess(t *testing.T) {
	args := os.Getenv("FILEX_SYNC_HELPER_ARGS")
	if args == "" {
		t.Skip("only run as a helper process")
	}
	cmd := syncCmd()
	cmd.SetArgs(strings.Fields(args))
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	err := cmd.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, "filex: "+err.Error())
	}
	os.Exit(exitCode(err))
}

// The situation the lock exists for: the desktop app (or a second copy of it
// — the Microsoft Store one) is syncing a folder and another engine starts on
// the same pairs. The second engine leaves that pair alone and syncs the
// rest; a one-shot run says so with exit status 4; a watcher waits, and takes
// the pair over the moment the first process is KILLED — no graceful
// shutdown, no release, nothing left behind to clean up.
func TestSyncRun_TwoEnginesOnOnePair(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	ls := newLiveServer(t)
	for _, f := range []string{"proj", "other"} {
		ls.do(t, http.MethodPost, "/api/files/manager?action=newfolder", map[string]string{"path": "main://", "name": f})
	}
	ls.saveText(t, "main://proj/note.txt", "seed\n")
	ls.saveText(t, "main://other/o.txt", "other\n")

	home := t.TempDir()
	t.Setenv("FILEX_SYNC_DIR", filepath.Join(home, "sync"))
	t.Setenv("FILEX_CLI_CONFIG", filepath.Join(home, "cli.yaml"))
	t.Setenv("FILEX_UPLOAD_STATE", filepath.Join(home, "uploads"))
	t.Setenv("FILEX_URL", ls.srv.URL)
	t.Setenv("FILEX_TOKEN", ls.token)
	st := &filesync.Store{Dir: filepath.Join(home, "sync")}
	mirror1, mirror2 := filepath.Join(home, "proj"), filepath.Join(home, "other")
	p1, err := st.AddPair(filesync.Pair{Local: mirror1, Remote: "main://proj", Account: "t"})
	require.NoError(t, err)
	_, err = st.AddPair(filesync.Pair{Local: mirror2, Remote: "main://other", Account: "t"})
	require.NoError(t, err)
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }

	// ── engine A: another process, syncing pair-1 ──
	a := exec.Command(os.Args[0], "-test.run=^TestSyncEngineHelperProcess$")
	a.Env = append(os.Environ(), "FILEX_SYNC_HELPER_ARGS=run --pair "+p1.ID+" --watch 1h --quiet")
	aOut := &syncBuffer{}
	a.Stdout, a.Stderr = aOut, aOut
	require.NoError(t, a.Start())
	aDone := make(chan struct{})
	go func() { _ = a.Wait(); close(aDone) }()
	t.Cleanup(func() {
		_ = a.Process.Kill()
		<-aDone
		if t.Failed() {
			t.Logf("engine A said:\n%s", aOut.String())
		}
	})
	waitUntil(t, 20*time.Second, "engine A's first pass", func() bool {
		return read(filepath.Join(mirror1, "note.txt")) == "seed\n"
	})

	// ── engine B, once: pair-1 is left alone, pair-2 syncs, status 4 ──
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err, out, errOut := runSync(t, ctx, "run", "--quiet")
	var ee *exitError
	require.True(t, errors.As(err, &ee), "want an exit status, got %v\nstdout: %s\nstderr: %s", err, out, errOut)
	require.Equal(t, exitPairBusy, ee.code)
	require.Contains(t, errOut, fmt.Sprintf("pair-1: lock: busy — another filex on this computer is syncing this pair (process %d, ", a.Process.Pid))
	require.NotContains(t, out, "pair-1:", "no pass may have run for the busy pair: %s", out)
	require.Contains(t, out, "pair-2:", "the other pair must have synced")
	require.Equal(t, "other\n", read(filepath.Join(mirror2, "o.txt")))
	require.Contains(t, err.Error(), "1 pair was not synced (pair-1)")

	// ── engine B, watching: waits for pair-1… ──
	restore := []time.Duration{lockGrace, lockRetryFast, lockRetry}
	lockGrace, lockRetryFast, lockRetry = 300*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { lockGrace, lockRetryFast, lockRetry = restore[0], restore[1], restore[2] })
	bOut := &syncBuffer{}
	b := syncCmd()
	b.SetArgs([]string{"run", "--account", "t", "--watch", "1h", "--quiet"})
	b.SetOut(bOut)
	b.SetErr(bOut)
	bctx, bcancel := context.WithCancel(context.Background())
	bDone := make(chan error, 1)
	go func() { bDone <- b.ExecuteContext(bctx) }()
	t.Cleanup(func() {
		bcancel()
		<-bDone
		if t.Failed() {
			t.Logf("engine B said:\n%s", bOut.String())
		}
	})
	waitUntil(t, 10*time.Second, "engine B to report pair-1 busy", func() bool {
		return strings.Contains(bOut.String(), "pair-1: lock: busy — ")
	})
	ls.saveText(t, "main://proj/note.txt", "while A still holds it\n")
	waitUntil(t, 10*time.Second, "engine A to sync its own pair", func() bool {
		return read(filepath.Join(mirror1, "note.txt")) == "while A still holds it\n"
	})
	require.NotContains(t, bOut.String(), "pair-1: already", "engine B ran a pass of a pair it does not hold")
	require.NotContains(t, bOut.String(), "pair-1: 1/", "engine B ran a pass of a pair it does not hold")

	// ── …and takes it over when A is killed ──
	// ⚠ Not the moment the file lands: A writes the file first and records the
	// pass in its state after ("settling"). Killed between the two, it leaves
	// a pass with no record, and B — rightly — keeps both versions instead of
	// trusting either, which is not what this test is about. Under load that
	// window was wide enough to hit (the v0.45.0 export run, 2026-09-25): A's
	// log ended at "settling", B's at "1 kept as both versions".
	waitUntil(t, 10*time.Second, "engine A to record its second pass", func() bool {
		return strings.Count(aOut.String(), "pair-1: 1/1 done") >= 2
	})
	require.NoError(t, a.Process.Kill())
	<-aDone
	waitUntil(t, 10*time.Second, "engine B to take pair-1 over", func() bool {
		return strings.Contains(bOut.String(), "pair-1: lock: acquired")
	})
	ls.saveText(t, "main://proj/note.txt", "after the takeover\n")
	waitUntil(t, 10*time.Second, "engine B to sync pair-1", func() bool {
		return read(filepath.Join(mirror1, "note.txt")) == "after the takeover\n"
	})
	require.NotContains(t, bOut.String(), "server copy", "a takeover must not conflict anything: %s", bOut.String())
}
