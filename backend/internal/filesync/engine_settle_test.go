package filesync

import (
	"testing"
	"time"
)

// ─────────────────────────── a change DURING a run ───────────────────────────

// The baseline used to be rebuilt from the post-run snapshots, so an edit made
// on the server while a run was busy (a nine-hour first sync is a long window)
// was recorded as already in step and never downloaded.
func TestAServerEditDuringARunIsNotSwallowed(t *testing.T) {
	r := newRig(t)
	r.writeLocal("a.txt", "a1")
	r.writeLocal("b.txt", "b1")
	r.run()

	r.writeLocal("a.txt", "a2") // gives the next run a transfer to happen during
	done := false
	r.srv.afterTransfer = func() {
		if done {
			return
		}
		done = true
		r.srv.files["b.txt"] = []byte("b2, from a colleague")
		r.srv.clock += 1000
		r.srv.mod["b.txt"] = r.srv.clock
	}
	r.run()
	r.srv.afterTransfer = nil
	r.run()

	if got := r.readLocal("b.txt"); got != "b2, from a colleague" {
		t.Fatalf("an edit made on the server during a run never arrived: %q", got)
	}
}

func TestALocalEditDuringARunIsNotSwallowed(t *testing.T) {
	r := newRig(t)
	r.writeLocal("a.txt", "a1")
	r.writeLocal("c.txt", "c1")
	r.run()

	r.writeLocal("a.txt", "a2")
	done := false
	r.srv.afterTransfer = func() {
		if done {
			return
		}
		done = true
		r.touchLocal("c.txt", "c2, typed while syncing", r.clock.Add(30*time.Second))
	}
	r.run()
	r.srv.afterTransfer = nil
	r.run()

	if got := string(r.srv.files["c.txt"]); got != "c2, typed while syncing" {
		t.Fatalf("an edit made here during a run never reached the server: %q", got)
	}
}
