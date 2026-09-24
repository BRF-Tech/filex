package filesync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The review of PR #35 (2026-09-22) found these by probing the engine; each
// test was red on the code it describes before the fix beside it.

// A first run that starts a hold and is then cut short — Ctrl-C, a closed
// laptop, the desktop quitting — used to leave the pair with a baseline (the
// ledger checkpoints while a pass runs) and NO hold, because the hold was
// written only when the run finished. The next run was not a first run any
// more, so nothing held it: the whole stale mirror went up.
func TestAHoldSurvivesAFirstRunCutShort(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+50)
	r.registerPair()
	for i := 0; i < 3; i++ {
		r.writeRemote(fmt.Sprintf("new-on-server/s%d.txt", i), "from the server")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.engine.CheckpointEvery = 1
	r.engine.Transfers = 1
	r.srv.afterTransfer = cancel // pull the plug after the first download
	if _, err := r.engine.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("first run: %v", err)
	}
	r.srv.afterTransfer = nil
	if _, had, _ := r.engine.Store.LoadBaseline(r.engine.Pair.ID); !had {
		t.Fatal("the interrupted run left no baseline; this test needs one to mean anything")
	}

	res := r.runStored()
	if res.Uploaded != 0 {
		t.Fatalf("the run after an interrupted first run uploaded %d stale file(s) — the hold was lost", res.Uploaded)
	}
	if p := r.pair(); !p.HoldNew || p.Held == 0 {
		t.Fatalf("the pair is not holding after the interruption: %+v", p)
	}
}

// A conflict made its copy of the server's version, then the upload of the
// local version failed — a lock (423), a size limit, a full quota. Nothing was
// recorded, so every later pass saw the same two versions, made ANOTHER copy,
// and failed the same way: a copy per pass, for ever.
func TestAConflictWhoseUploadKeepsFailingMakesOneCopy(t *testing.T) {
	r := newRig(t)
	r.writeLocal("doc.txt", "v1")
	r.run()
	r.writeLocal("doc.txt", "edited here")
	r.writeRemote("doc.txt", "edited there")
	locked := errors.New("HTTP 423: locked by the signing app")
	r.srv.uploadErr = func(rel string) error {
		if rel == "doc.txt" {
			return locked
		}
		return nil
	}
	for i := 0; i < 4; i++ {
		res, err := r.engine.Run(context.Background())
		if err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		if len(res.Errors) == 0 {
			t.Fatalf("pass %d: the locked upload must be reported: %+v", i, res)
		}
	}
	copies := 0
	for rel := range r.srv.files {
		if strings.Contains(rel, "(server copy") {
			copies++
		}
	}
	entries, _ := os.ReadDir(r.dir)
	local := 0
	for _, e := range entries {
		if strings.Contains(e.Name(), "(server copy") {
			local++
		}
	}
	if copies != 1 || local != 1 {
		t.Fatalf("four passes against a locked file made %d server copies and %d local ones; want one each", copies, local)
	}
	// The cause goes away: the local version goes up, and still no new copy.
	r.srv.uploadErr = nil
	res := r.run()
	if got := string(r.srv.files["doc.txt"]); got != "edited here" || res.Conflicts != 0 {
		t.Fatalf("after the lock: server has %q, %d conflict(s)", got, res.Conflicts)
	}
}

// The watcher decides whether this side changed by comparing a fresh local
// walk with the fingerprint the last pass reported. That fingerprint used to
// come from a walk AFTER the pass — which already contained an edit made while
// the pass ran, so the edit never looked like a change and waited for the
// 30-minute safety net.
func TestAnEditDuringAPassStillChangesTheFingerprint(t *testing.T) {
	r := newRig(t)
	r.writeLocal("a.txt", "a")
	r.writeLocal("b.txt", "b")
	r.run()
	r.writeRemote("from-server.txt", "downloaded in the next pass")
	edited := false
	r.srv.afterTransfer = func() {
		if !edited {
			edited = true
			r.writeLocal("b.txt", "saved while the pass was busy")
		}
	}
	res := r.run()
	r.srv.afterTransfer = nil
	now, err := LocalFingerprint(r.engine.Pair)
	if err != nil {
		t.Fatal(err)
	}
	if now == res.LocalFingerprint {
		t.Fatal("the pass's fingerprint already contains the edit made while it ran; a watcher would never see it")
	}
	// ...and a pass that nothing interrupted reports exactly what a walk sees,
	// the download it made included.
	res = r.run()
	if now, _ = LocalFingerprint(r.engine.Pair); now != res.LocalFingerprint {
		t.Fatal("an undisturbed pass must report the tree it left")
	}
}

// A token revoked while a pass is busy: every remaining transfer was still
// attempted — a refused request per file — before the pass gave up.
func TestARefusedTokenEndsThePassAtOnce(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 6; i++ {
		r.writeRemote(fmt.Sprintf("f%d.txt", i), "x")
	}
	refused := errors.New("HTTP 401: unauthorized")
	for i := 0; i < 6; i++ {
		r.srv.fail[fmt.Sprintf("f%d.txt", i)] = refused
	}
	r.engine.Transfers = 1
	r.engine.StopOn = func(err error) bool { return errors.Is(err, refused) }
	res, err := r.engine.Run(context.Background())
	if !errors.Is(err, refused) {
		t.Fatalf("the pass must end with the refusal, got %v", err)
	}
	if r.srv.downloads > 0 || res.Downloaded > 0 {
		t.Fatalf("%d more download(s) were attempted after the token was refused", r.srv.downloads)
	}
	for _, e := range res.Errors {
		if strings.HasPrefix(e, "stopped:") {
			t.Fatalf("a refused token is not a stop request: %v", res.Errors)
		}
	}
}

// The hold used to be recorded by rewriting pairs.json from the long-running
// watcher, while the desktop app writes the same file to add, pause and remove
// pairs: a lost update could bring back a removed pair or drop a new one.
func TestAHoldNeverRewritesPairsJSON(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+5)
	r.registerPair()
	before, err := os.ReadFile(filepath.Join(r.engine.Store.Dir, "pairs.json"))
	if err != nil {
		t.Fatal(err)
	}
	res := r.runStored()
	if res.Held == 0 {
		t.Fatalf("the run did not hold: %+v", res)
	}
	after, _ := os.ReadFile(filepath.Join(r.engine.Store.Dir, "pairs.json"))
	if !bytes.Equal(before, after) {
		t.Fatalf("the engine rewrote pairs.json:\nbefore %s\nafter  %s", before, after)
	}
	if p := r.pair(); !p.HoldNew || p.Held != res.Held {
		t.Fatalf("the pair list does not carry the hold: %+v", p)
	}
	// A save of the pair list (the desktop pausing a pair) must not lose it.
	pairs, _ := r.engine.Store.LoadPairs()
	pairs[0].Paused = true
	if err := r.engine.Store.SavePairs(pairs); err != nil {
		t.Fatal(err)
	}
	if p := r.pair(); !p.HoldNew {
		t.Fatal("saving the pair list dropped the hold")
	}
}

// Discard trashes held files. One that the engine has since recorded as in
// step on BOTH sides — an identical copy turned up on the server and was
// adopted — is not stale any more: trashing it here made the next pass carry
// the deletion to the server.
func TestDiscardLeavesAFileTheBaselineKnowsAlone(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+5)
	r.registerPair()
	if res := r.runStored(); res.Held == 0 {
		t.Fatalf("no hold: %+v", res)
	}
	// Someone uploads the very same file on the server; the live pass the
	// announcement triggers (a targeted pass, which adds to the hold's list
	// rather than replacing it) adopts the twin.
	body := r.readLocal("copies/c000.txt")
	r.writeRemote("copies/c000.txt", body)
	info, _ := os.Stat(filepath.Join(r.dir, "copies", "c000.txt"))
	r.srv.mod["copies/c000.txt"] = info.ModTime().UnixMilli()
	r.engine.Pair = r.pair()
	if _, err := r.engine.RunDirs(context.Background(), []string{"copies"}); err != nil {
		t.Fatal(err)
	}
	if held, _ := r.engine.Store.LoadHeld(r.engine.Pair.ID); held["copies/c000.txt"] == "" {
		t.Fatal("setup: the file must still be on the hold's list")
	}
	base, _, _ := r.engine.Store.LoadBaseline(r.engine.Pair.ID)
	if _, ok := base["copies/c000.txt"]; !ok {
		t.Fatal("setup: the twin was not adopted")
	}

	if _, _, err := r.engine.Store.DiscardHeld(r.pair(), r.clock); err != nil {
		t.Fatal(err)
	}
	if !r.localExists("copies/c000.txt") {
		t.Fatal("discard trashed a file the baseline records as in step")
	}
	r.runStored()
	if _, ok := r.srv.files["copies/c000.txt"]; !ok {
		t.Fatal("the server's copy was deleted")
	}
}

// A conflict copy goes to the server create-only: two machines resolving the
// same conflict in the same minute pick the same name from the same listing,
// and the second used to write its copy over the first one's.
func TestAConflictCopyNeverReplacesOneOnTheServer(t *testing.T) {
	r := newRig(t)
	r.writeLocal("doc.txt", "v1")
	r.run()
	r.writeLocal("doc.txt", "edited here")
	r.writeRemote("doc.txt", "edited there")
	var otherCopy string
	r.srv.beforeUpload = func(rel string) {
		if strings.Contains(rel, "(server copy") && otherCopy == "" {
			// Another machine got there first, under the same name.
			otherCopy = rel
			r.srv.files[rel] = []byte("the other machine's copy")
			r.srv.clock += 1000
			r.srv.mod[rel] = r.srv.clock
		}
	}
	r.run()
	if otherCopy == "" {
		t.Fatal("setup: no copy was uploaded")
	}
	if got := string(r.srv.files[otherCopy]); got != "the other machine's copy" {
		t.Fatalf("the copy another machine put on the server was replaced: %q", got)
	}
}
