package sync

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

// What reaches the error tracker, and what does not.
//
// A poll run reads the backend's listing; when an object store answers 504
// under load and the retry budget is spent, the run gives up and the catalogue
// is refreshed on the next tick instead. Nothing is lost. Measured on
// fm.example.com: fifteen such failures in six weeks, every one followed by a
// successful run — fifteen reports that meant "the internet had a hiccup".
//
// So the rule is: note the hiccup, report the outage.
func capture(t *testing.T, f func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	f()
	return buf.String()
}

func newTestSyncer() *storageSyncer {
	return &storageSyncer{storage: &model.Storage{Name: "s3-test"}}
}

func TestSingleFailedRunIsNotReported(t *testing.T) {
	s := newTestSyncer()
	boom := errors.New("s3: list: ListObjectsV2, exceeded maximum number of attempts, 6, 504")

	out := capture(t, func() { s.noteRun(boom) })
	if strings.Contains(out, "level=WARN") {
		t.Fatalf("one failed run is a hiccup, not an error to report:\n%s", out)
	}
	if !strings.Contains(out, "level=INFO") || !strings.Contains(out, "s3-test") {
		t.Fatalf("it still has to be visible in the log:\n%s", out)
	}
}

func TestThirdConsecutiveFailureIsReported(t *testing.T) {
	s := newTestSyncer()
	boom := errors.New("504 again")

	out := capture(t, func() {
		s.noteRun(boom)
		s.noteRun(boom)
	})
	if strings.Contains(out, "level=WARN") {
		t.Fatalf("two in a row is still under the threshold:\n%s", out)
	}

	out = capture(t, func() { s.noteRun(boom) })
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("three in a row is a storage that is genuinely not answering:\n%s", out)
	}
	if !strings.Contains(out, "consecutive=3") {
		t.Fatalf("the report should say how long it has been failing:\n%s", out)
	}

	// And it keeps reporting: the tracker groups by message, so a rising count
	// on one issue is exactly the signal an operator wants from an outage.
	out = capture(t, func() { s.noteRun(boom) })
	if !strings.Contains(out, "consecutive=4") {
		t.Fatalf("a continuing outage must keep counting:\n%s", out)
	}
}

func TestSuccessResetsTheStreak(t *testing.T) {
	s := newTestSyncer()
	boom := errors.New("hiccup")

	capture(t, func() {
		s.noteRun(boom)
		s.noteRun(boom)
		s.noteRun(nil)
	})
	if s.failures != 0 {
		t.Fatalf("a successful run clears the streak, got %d", s.failures)
	}

	// Two more failures must NOT be reported: the streak restarted.
	out := capture(t, func() {
		s.noteRun(boom)
		s.noteRun(boom)
	})
	if strings.Contains(out, "level=WARN") {
		t.Fatalf("failures either side of a success are not consecutive:\n%s", out)
	}
}

func TestRecoveryAfterAReportedOutageIsLogged(t *testing.T) {
	s := newTestSyncer()
	boom := errors.New("outage")
	capture(t, func() {
		for i := 0; i < FailureReportThreshold; i++ {
			s.noteRun(boom)
		}
	})
	out := capture(t, func() { s.noteRun(nil) })
	if !strings.Contains(out, "sync: recovered") {
		t.Fatalf("an outage that was reported should have a visible end:\n%s", out)
	}
}
