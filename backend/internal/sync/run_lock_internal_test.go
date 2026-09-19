package sync

import (
	"strings"
	"testing"
)

// A tick that finds the previous run still walking is not a failed run: it
// must not move the storage towards the "reported to the tracker" threshold.
func TestNoteRun_ASkippedTickIsNotAFailure(t *testing.T) {
	s := newTestSyncer()
	out := capture(t, func() {
		s.noteRun(ErrRunInProgress)
		s.noteRun(ErrRunInProgress)
		s.noteRun(ErrRunInProgress)
	})
	if s.failures != 0 {
		t.Fatalf("skipped ticks counted as failures: %d", s.failures)
	}
	if strings.Contains(out, "level=WARN") {
		t.Fatalf("a skipped tick reached the error tracker:\n%s", out)
	}
	if !strings.Contains(out, "still in progress") {
		t.Fatalf("the skip has to be visible in the log:\n%s", out)
	}
}
