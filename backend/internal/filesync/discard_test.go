package filesync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"
)

// ── discard deletes only what the server still has (task #259) ──────────
//
// The owner's decision of 2026-10-15: a held file is deleted from this
// computer only when the server holds an identical copy of it (the same
// bytes) - in the person's trash, or elsewhere - so the deletion never loses
// the last copy of anything. Everything else stays where it is, stays held,
// and is reported with its reason.

// fakeCopies is a CopyFinder over the bytes the server "holds" somewhere:
// sha256 → where.
type fakeCopies struct {
	at    map[string]string
	err   error
	asked []string
	// during runs inside FindCopy: a file written while the server is asked.
	during func(c CopyCheck)
}

func (f *fakeCopies) FindCopy(_ context.Context, c CopyCheck) (string, error) {
	f.asked = append(f.asked, c.Rel)
	if f.during != nil {
		f.during(c)
	}
	if f.err != nil {
		return "", f.err
	}
	return f.at[c.SHA256], nil
}

func hexSum(body string) string {
	s := sha256.Sum256([]byte(body))
	return hex.EncodeToString(s[:])
}

// trashHolding is a server whose trash holds these bodies.
func trashHolding(bodies ...string) *fakeCopies {
	f := &fakeCopies{at: map[string]string{}}
	for i, b := range bodies {
		f.at[hexSum(b)] = fmt.Sprintf("trash: docs://work/%d", i)
	}
	return f
}

// trashOfStaleMirror holds the stale mirror's extra files, all but skip.
func trashOfStaleMirror(extras int, skip ...int) *fakeCopies {
	var bodies []string
	for i := 0; i < extras; i++ {
		if len(skip) > 0 && skip[0] == i {
			continue
		}
		bodies = append(bodies, fmt.Sprintf("tidied away on the server %d", i))
	}
	return trashHolding(bodies...)
}

func reasonOf(res DiscardResult, rel string) string {
	for _, it := range res.Kept {
		if it.Rel == rel {
			return it.Reason
		}
	}
	return ""
}

// ⚠⚠ The point of the rule: the server has no copy of one held file. It is
// not deleted, it is still held - the next run does not upload it - and the
// result says why. Its folder stays, held with it.
func TestDiscardKeepsAndStillHoldsAFileTheServerHasNoCopyOf(t *testing.T) {
	const extras = MassUploadThreshold + 1
	r := staleMirror(t, extras)
	r.registerPair()
	r.runStored()

	res, err := r.engine.Store.DiscardHeld(context.Background(), r.pair(), trashOfStaleMirror(extras, 7))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != extras-1 || len(res.Kept) != 1 {
		t.Fatalf("deleted %d, kept %d", len(res.Deleted), len(res.Kept))
	}
	if got := reasonOf(res, "copies/c007.txt"); got != KeptNoCopy {
		t.Fatalf("reason = %q, want %q", got, KeptNoCopy)
	}
	if got := r.readLocal("copies/c007.txt"); got != "tidied away on the server 7" {
		t.Fatalf("the file with no copy was touched: %q", got)
	}
	if r.localExists("copies/c000.txt") {
		t.Fatal("a file the server's trash holds must be deleted")
	}
	p := r.pair()
	if !p.HoldNew || p.Held != 2 || res.StillHeld != 2 { // the file and its folder
		t.Fatalf("still held: %+v, StillHeld %d", p, res.StillHeld)
	}
	if held, _ := r.engine.Store.LoadHeld(p.ID); held["copies/c007.txt"] == "" || held["copies"] != "dir" {
		t.Fatalf("the hold's list: %v", held)
	}
	if res := r.runStored(); res.Uploaded != 0 {
		t.Fatalf("the next run uploaded the kept file: %+v", res)
	}
	if _, ok := r.srv.files["copies/c007.txt"]; ok {
		t.Fatal("a file kept here went to the server")
	}
}

// A copy is the same BYTES: a server file with other bytes - of the same
// name and length, here - is no copy.
func TestDiscardDoesNotTakeAFileOfTheSameSizeForACopy(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+1)
	r.registerPair()
	r.runStored()
	// Same length as "tidied away on the server 0", other bytes.
	find := trashHolding("tidied away on the server X")

	res, err := r.engine.Store.DiscardHeld(context.Background(), r.pair(), find)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 || !r.localExists("copies/c000.txt") {
		t.Fatalf("deleted %v", res.Deleted)
	}
	if got := reasonOf(res, "copies/c000.txt"); got != KeptNoCopy {
		t.Fatalf("reason = %q", got)
	}
}

// The server could not be asked: nothing can be said, nothing is deleted,
// and the reason says so - not "no copy".
func TestDiscardKeepsEverythingTheServerCouldNotBeAskedAbout(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+1)
	r.registerPair()
	r.runStored()

	find := &fakeCopies{err: errors.New("HTTP 503: the search is off")}
	res, err := r.engine.Store.DiscardHeld(context.Background(), r.pair(), find)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 || len(res.Kept) != MassUploadThreshold+1 {
		t.Fatalf("deleted %d kept %d", len(res.Deleted), len(res.Kept))
	}
	for _, it := range res.Kept {
		if it.Reason != KeptUnchecked || it.Err == "" {
			t.Fatalf("%+v", it)
		}
	}
	if !r.pair().HoldNew {
		t.Fatal("the hold must stay on")
	}
}

// No server to ask (find nil): nothing is deleted.
func TestDiscardWithNoServerDeletesNothing(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+1)
	r.registerPair()
	r.runStored()

	res, err := r.engine.Store.DiscardHeld(context.Background(), r.pair(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 || !r.localExists("copies/c000.txt") {
		t.Fatalf("deleted %v", res.Deleted)
	}
}

// ⚠ A file written WHILE the server was being asked is not the file that was
// compared: it stays, and is reported as changed.
func TestDiscardKeepsAFileWrittenWhileTheServerWasAsked(t *testing.T) {
	const extras = MassUploadThreshold + 1
	r := staleMirror(t, extras)
	r.registerPair()
	r.runStored()

	find := trashOfStaleMirror(extras)
	find.during = func(c CopyCheck) {
		if c.Rel == "copies/c003.txt" {
			r.touchLocal("copies/c003.txt", "written while it was compared", r.clock.Add(time.Hour))
		}
	}
	res, err := r.engine.Store.DiscardHeld(context.Background(), r.pair(), find)
	if err != nil {
		t.Fatal(err)
	}
	if got := reasonOf(res, "copies/c003.txt"); got != KeptChanged {
		t.Fatalf("reason = %q", got)
	}
	if got := r.readLocal("copies/c003.txt"); got != "written while it was compared" {
		t.Fatalf("the file was touched: %q", got)
	}
}

// The person's files are hashed, never just their names and sizes compared:
// every file the server was asked about carried the sha256 of its bytes.
func TestDiscardAsksAboutTheBytes(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+1)
	r.registerPair()
	r.runStored()

	var got []CopyCheck
	find := &fakeCopies{at: map[string]string{}}
	find.during = func(c CopyCheck) { got = append(got, c) }
	if _, err := r.engine.Store.DiscardHeld(context.Background(), r.pair(), find); err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.Rel == "copies/c004.txt" {
			body := "tidied away on the server 4"
			if c.SHA256 != hexSum(body) || c.Size != int64(len(body)) || c.Name != "c004.txt" {
				t.Fatalf("%+v", c)
			}
			return
		}
	}
	t.Fatal("copies/c004.txt was not asked about")
}
