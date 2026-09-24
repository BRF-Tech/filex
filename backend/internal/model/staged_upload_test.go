package model

import (
	"testing"
	"time"
)

func TestTransferLanded(t *testing.T) {
	commit := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	overwrite := &Node{Size: 100, BackendMtime: &commit, DBMtime: commit.Add(-30 * 24 * time.Hour)}
	newFile := &Node{Size: 100, DBMtime: commit}

	cases := []struct {
		name  string
		n     *Node
		size  int64
		mtime time.Time
		want  bool
	}{
		{"the committed bytes, written after the commit", overwrite, 100, commit.Add(5 * time.Second), true},
		{"within the clock skew allowance", overwrite, 100, commit.Add(-TransferLandedSkew), true},
		{"the version being replaced: older than the commit", overwrite, 100, commit.Add(-time.Hour), false},
		{"just past the skew allowance", overwrite, 100, commit.Add(-TransferLandedSkew - time.Millisecond), false},
		{"not the committed size", overwrite, 99, commit.Add(time.Minute), false},
		{"a time that cannot be compared", overwrite, 100, time.Time{}, false},
		// A brand-new file's row was created at the commit (db_mtime), so the
		// same rule reads the row's creation time; its OWN old db_mtime is
		// never used for an overwrite, where backend_mtime says when.
		{"a new file, written after its row", newFile, 100, commit.Add(time.Second), true},
		{"a new file's key held something older", newFile, 100, commit.Add(-time.Hour), false},
		{"no node", nil, 100, commit, false},
	}
	for _, c := range cases {
		if got := TransferLanded(c.n, c.size, c.mtime); got != c.want {
			t.Errorf("%s: TransferLanded = %v, want %v", c.name, got, c.want)
		}
	}
}
