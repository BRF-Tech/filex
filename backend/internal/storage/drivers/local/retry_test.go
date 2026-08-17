package local

import (
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"
)

// The retry exists for one measured failure: on Windows, uploading a file and
// deleting it straight away returned 500 about three times in two hundred,
// because the rename into the trash hit a handle somebody else still had open
// — including, thanks to Go opening files without FILE_SHARE_DELETE, filex's
// own thumbnailer and content indexer.
//
// These tests pin the SHAPE of the fix on every platform: what is retried, what
// is not, and that success is not delayed. The end-to-end proof is on Windows
// and is in the commit message, because only Windows can produce the condition.

func TestRetryReturnsSuccessImmediately(t *testing.T) {
	calls := 0
	start := time.Now()
	err := retryWhileLocked(func() error { calls++; return nil })
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("op ran %d times, want 1 — a success must not be retried", calls)
	}
	// ⚠ A sleep before the first attempt would put a delay on EVERY delete and
	// rename in the product, for a condition almost none of them hit.
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("a successful op took %s — the retry is delaying the happy path", d)
	}
}

// ⚠ The important negative: an error that is NOT a sharing violation must come
// back at once. Retrying everything would turn "no such file" — an answer —
// into a second of dead time before the same answer.
func TestRetryDoesNotRetryOrdinaryErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"not exist", fs.ErrNotExist},
		{"path error", &fs.PathError{Op: "rename", Path: "x", Err: fs.ErrInvalid}},
		{"plain", errors.New("disk on fire")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			start := time.Now()
			err := retryWhileLocked(func() error { calls++; return tc.err })
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if calls != 1 {
				t.Fatalf("op ran %d times, want 1 — %s is not transient", calls, tc.name)
			}
			if d := time.Since(start); d > 100*time.Millisecond {
				t.Fatalf("%s took %s — it was retried", tc.name, d)
			}
		})
	}
}

// A lock that clears is the case the whole thing exists for: the op must be
// tried again and its eventual success returned.
//
// ⚠ Skipped off Windows rather than faked: `sharingViolation` is false
// everywhere else by design (a rename succeeds on Unix while the file is open),
// so there is no error value that could make this meaningful there. A test that
// stubbed it out would assert the stub.
func TestRetrySurvivesALockThatClears(t *testing.T) {
	if !sharingViolation(errSharingViolationForTest()) {
		t.Skip("sharing violations do not exist on this platform — see locked_other.go")
	}
	calls := 0
	err := retryWhileLocked(func() error {
		calls++
		if calls < 3 {
			return errSharingViolationForTest()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil once the lock cleared", err)
	}
	if calls != 3 {
		t.Fatalf("op ran %d times, want 3", calls)
	}
}

// A lock that never clears must still end, with the original error, inside the
// budget — a delete that hangs forever is worse than one that fails.
func TestRetryGivesUpWithTheRealError(t *testing.T) {
	if !sharingViolation(errSharingViolationForTest()) {
		t.Skip("sharing violations do not exist on this platform")
	}
	want := errSharingViolationForTest()
	start := time.Now()
	err := retryWhileLocked(func() error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want the original %v", err, want)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("gave up after %s — the budget is supposed to be about a second", d)
	}
}

// Move and Delete go through the retry. Checked by behaviour on a real
// directory rather than by reading the source, so a future edit that drops the
// wrapper is caught.
func TestMoveAndDeleteStillWorkNormally(t *testing.T) {
	dir := t.TempDir()
	d := &Driver{}
	if err := d.Init(t.Context(), map[string]any{"path": dir}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := os.WriteFile(dir+"/a.txt", []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.Move(t.Context(), "a.txt", "sub/b.txt"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if _, err := os.Stat(dir + "/sub/b.txt"); err != nil {
		t.Fatalf("moved file is not there: %v", err)
	}
	if err := d.Delete(t.Context(), "sub/b.txt"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(dir + "/sub/b.txt"); !os.IsNotExist(err) {
		t.Fatalf("deleted file is still there: %v", err)
	}
	// ⚠ A missing source is ErrNotFound, not a retried second of nothing —
	// internal/trash.Put branches on exactly this to tell "already gone" from
	// "the rename failed".
	start := time.Now()
	if err := d.Move(t.Context(), "nope.txt", "x.txt"); err == nil {
		t.Fatal("moving a missing file should fail")
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("a missing source took %s — it was retried", d)
	}
}
