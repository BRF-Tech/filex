package filesync

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// One engine per pair on this computer (lock.go). Two engines planning from
// the same baseline at once is a silent race — nothing fails, files are
// uploaded twice, conflict copies appear, a delete travels the wrong way — so
// every test here is about the SECOND engine never getting as far as reading
// anything.

// The lock is an operating-system one: the process that holds it is asked
// about, and nothing but the OS decides whether it is free.
func TestLockPair_ASecondTakerIsRefusedUntilTheFirstLetsGo(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	first, err := st.LockPair("pair-1")
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if first.Unenforced() {
		t.Fatal("this file system must really lock (the test would prove nothing otherwise)")
	}

	_, err = st.LockPair("pair-1")
	var be *BusyError
	if !errors.As(err, &be) || !errors.Is(err, ErrPairBusy) {
		t.Fatalf("second lock: want a *BusyError (ErrPairBusy), got %v", err)
	}
	// ⚠ On Windows the holder's note is readable only because the locked byte
	// is beyond it (lock_windows.go): Windows locks are mandatory.
	if be.Holder.PID != os.Getpid() || be.Pair != "pair-1" {
		t.Fatalf("the refusal must name its holder: %+v", be)
	}

	other, err := st.LockPair("pair-2")
	if err != nil {
		t.Fatalf("another pair is not blocked by pair-1: %v", err)
	}
	other.Release()

	first.Release()
	first.Release() // twice is harmless
	again, err := st.LockPair("pair-1")
	if err != nil {
		t.Fatalf("after release the pair must be free: %v", err)
	}
	again.Release()
	if _, err := os.Stat(st.lockPath("pair-1")); err != nil {
		t.Fatalf("the lock FILE stays (deleting it would let two processes hold two files): %v", err)
	}
}

func TestLockPair_RefusesAnIdThatIsNotAFileName(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	for _, id := range []string{"", ".", "..", "../x", `a\b`} {
		if lk, err := st.LockPair(id); err == nil {
			lk.Release()
			t.Fatalf("%q: want a refusal", id)
		}
	}
}

// The case the lock exists for: a pass already running when a second engine
// (another process, here another Engine value) starts one on the same pair.
// The second must stand down before it reads or asks anything.
func TestTwoEnginesOnOnePair_TheSecondIsRefusedWhileTheFirstRuns(t *testing.T) {
	r := newRig(t)
	r.writeRemote("a.txt", "a1")
	r.writeRemote("b.txt", "b1")

	srvB := newFake("docs://work")
	srvB.files["c.txt"] = []byte("only the second engine would see this")
	srvB.mod["c.txt"] = 1000
	second := &Engine{Pair: r.engine.Pair, API: srvB, Store: &Store{Dir: r.engine.Store.Dir}, Now: r.engine.Now}

	var secondErr error
	ran := false
	r.srv.afterTransfer = func() {
		if ran {
			return
		}
		ran = true
		_, secondErr = second.Run(context.Background())
	}
	r.run()
	if !ran {
		t.Fatal("the first engine transferred nothing; the test did not overlap the two")
	}
	if !errors.Is(secondErr, ErrPairBusy) {
		t.Fatalf("the second engine must be refused while the first runs, got %v", secondErr)
	}
	if srvB.lists+srvB.downloads+srvB.uploads+srvB.mkdirs != 0 || r.localExists("c.txt") {
		t.Fatalf("the refused engine touched something: %d lists, %d downloads, %d uploads, %d mkdirs",
			srvB.lists, srvB.downloads, srvB.uploads, srvB.mkdirs)
	}

	// Once the first pass is over the pair is free again.
	if _, err := second.Run(context.Background()); err != nil {
		t.Fatalf("after the first engine finished: %v", err)
	}
	if !r.localExists("c.txt") {
		t.Fatal("the second engine did not run after the first let go")
	}
}

// A caller that holds the lock across passes (`sync run --watch`) hands it
// to the engine; the engine must not trip over it — nor accept one taken for
// a different pair.
func TestEngine_UsesTheCallersLock(t *testing.T) {
	r := newRig(t)
	r.writeRemote("a.txt", "a1")
	lk, err := r.engine.Store.LockPair("p1")
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()

	r.engine.Lock = lk
	r.run() // fails the test if the engine trips over its caller's lock
	if r.readLocal("a.txt") != "a1" {
		t.Fatal("the pass did not run")
	}
	if _, err := r.engine.RunDirs(context.Background(), []string{""}); err != nil {
		t.Fatalf("a targeted pass under the caller's lock: %v", err)
	}

	wrong, err := r.engine.Store.LockPair("p2")
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Release()
	r.engine.Lock = wrong
	if _, err := r.engine.Run(context.Background()); err == nil {
		t.Fatal("a lock taken for another pair must not cover this one")
	}

	// And an engine with no lock of its own, while the caller holds it,
	// is refused — a pass that forgot to take its caller's lock fails loudly.
	r.engine.Lock = nil
	if _, err := r.engine.Run(context.Background()); !errors.Is(err, ErrPairBusy) {
		t.Fatalf("want ErrPairBusy, got %v", err)
	}
}

// The other half of "the OS decides": a holder that is KILLED — not asked to
// stop, no deferred release, no cleanup — frees the pair. A PID file would
// stay behind here and block the pair until somebody deleted it.
func TestLockPair_AKilledHolderLetsGo(t *testing.T) {
	if os.Getenv("FILEX_LOCK_HELPER_DIR") != "" {
		t.Skip("helper process")
	}
	dir := t.TempDir()
	holder := exec.Command(os.Args[0], "-test.run=^TestLockHelperProcess$")
	holder.Env = append(os.Environ(), "FILEX_LOCK_HELPER_DIR="+dir)
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("helper did not take the lock: %q %v", line, err)
	}

	st := &Store{Dir: dir}
	_, err = st.LockPair("pair-1")
	var be *BusyError
	if !errors.As(err, &be) {
		t.Fatalf("while another process holds it: want *BusyError, got %v", err)
	}
	if be.Holder.PID != holder.Process.Pid {
		t.Fatalf("the refusal must name the other process (%d): %+v", holder.Process.Pid, be.Holder)
	}
	exe, _ := filepath.Abs(os.Args[0])
	if be.Holder.Exe == "" || filepath.Base(be.Holder.Exe) != filepath.Base(exe) {
		t.Fatalf("the refusal must name the other program (%s): %+v", exe, be.Holder)
	}

	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()
	// The OS lets go when the process is gone; on Windows handle teardown can
	// trail the process's exit by a moment.
	deadline := time.Now().Add(5 * time.Second)
	for {
		lk, err := st.LockPair("pair-1")
		if err == nil {
			lk.Release()
			return
		}
		if !errors.Is(err, ErrPairBusy) || time.Now().After(deadline) {
			t.Fatalf("after the holder was killed the pair must be free: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestLockHelperProcess is the other process of TestLockPair_AKilledHolderLetsGo:
// it takes pair-1's lock, says so, and waits to be killed.
func TestLockHelperProcess(t *testing.T) {
	dir := os.Getenv("FILEX_LOCK_HELPER_DIR")
	if dir == "" {
		t.Skip("only run as a helper process")
	}
	if _, err := (&Store{Dir: dir}).LockPair("pair-1"); err != nil {
		os.Stdout.WriteString("error: " + err.Error() + "\n")
		os.Exit(2)
	}
	os.Stdout.WriteString("locked\n")
	time.Sleep(5 * time.Minute)
	os.Exit(0)
}
