package contain

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/search/extract/extracttest"
)

// The children these tests start are this test binary, started again: the
// package's init answers as a child before any test runs.

// stalled is a source that never sends a byte, so the child waits on its
// stdin until something ends it.
func stalled(t *testing.T) io.Reader {
	t.Helper()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	return pr
}

// short makes the time limit and the wait for the source short for one test.
func short(t *testing.T, timeout time.Duration) {
	t.Helper()
	oldTimeout, oldWait := Timeout, waitDelay
	Timeout, waitDelay = timeout, 100*time.Millisecond
	t.Cleanup(func() { Timeout, waitDelay = oldTimeout, oldWait })
}

func TestExtract_APDFsTextComesBackFromItsOwnProcess(t *testing.T) {
	pdf := extracttest.MinimalPDF("Hello filex content search")
	got, err := Extract(context.Background(), "application/pdf", "pdf", bytes.NewReader(pdf), 0, 256<<20)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "filex") {
		t.Fatalf("extracted %q, want it to contain %q", got, "filex")
	}
}

// The case of GitHub #123: a parse that outgrows its ceiling ends its own
// process, and the caller - the server - is told so and goes on.
func TestExtract_OverItsCeilingTheChildStopsAndTheCallerGoesOn(t *testing.T) {
	short(t, time.Minute)
	// 1 MiB is under what any Go process holds at its start, so the watch
	// ends the child at its first look, while it waits for the file.
	_, err := Extract(context.Background(), "application/pdf", "pdf", stalled(t), 0, 1<<20)
	if !errors.Is(err, ErrStopped) || !strings.Contains(err.Error(), "memory ceiling") {
		t.Fatalf("err = %v, want ErrStopped over the memory ceiling", err)
	}
	pdf := extracttest.MinimalPDF("still searchable")
	got, err := Extract(context.Background(), "application/pdf", "pdf", bytes.NewReader(pdf), 0, 256<<20)
	if err != nil || !strings.Contains(got, "searchable") {
		t.Fatalf("the next file: (%q, %v), want its text", got, err)
	}
}

// The real thing, with the real parser: a 400 KB PDF of nested arrays sends
// ledongthuc/pdf far past 64 MB (about 900 bytes a level), and the child is
// stopped at its ceiling instead of taking the caller with it.
func TestExtract_APDFThatOutgrowsItsCeilingIsStopped(t *testing.T) {
	short(t, time.Minute)
	pdf := extracttest.NestedArraysPDF(200_000)
	_, err := Extract(context.Background(), "application/pdf", "pdf", bytes.NewReader(pdf), 0, 64<<20)
	if !errors.Is(err, ErrStopped) || !strings.Contains(err.Error(), "memory ceiling") {
		t.Fatalf("err = %v, want the parse stopped over its 64 MB ceiling", err)
	}
}

func TestExtract_PastTheTimeLimitTheChildIsKilled(t *testing.T) {
	short(t, 300*time.Millisecond)
	start := time.Now()
	_, err := Extract(context.Background(), "application/pdf", "pdf", stalled(t), 0, 256<<20)
	if !errors.Is(err, ErrStopped) || !strings.Contains(err.Error(), "no text after") {
		t.Fatalf("err = %v, want ErrStopped past the time limit", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("took %s, want the time limit to end it", took)
	}
}

// A job that is cancelled (filex stopping) is the job's error, not a file
// without text: the caller must not record the file as empty.
func TestExtract_ACancelledJobAnswersTheJobsError(t *testing.T) {
	short(t, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	_, err := Extract(ctx, "application/pdf", "pdf", stalled(t), 0, 256<<20)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrStopped) {
		t.Fatalf("err = %v: a cancelled job is not a stopped extraction", err)
	}
}

func TestExtract_TheTextIsCappedAtTheLimit(t *testing.T) {
	long := strings.Repeat("filex ", 200)
	got, err := Extract(context.Background(), "text/plain", "txt", strings.NewReader(long), 64, 256<<20)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) > 64 || !strings.HasPrefix(got, "filex") {
		t.Fatalf("got %d bytes (%q), want at most 64 from the start", len(got), got)
	}
}

// A source that fails is the caller's error, as it is in filex itself: the
// queue may retry a storage that did not answer.
func TestExtract_ASourceThatFailsIsTheCallersError(t *testing.T) {
	broken := io.MultiReader(strings.NewReader("%PDF-1.4 "), iotestErrReader{})
	_, err := Extract(context.Background(), "application/pdf", "pdf", broken, 0, 256<<20)
	if err == nil || errors.Is(err, ErrStopped) || !strings.Contains(err.Error(), "storage went away") {
		t.Fatalf("err = %v, want the source's own error", err)
	}
}

type iotestErrReader struct{}

func (iotestErrReader) Read([]byte) (int, error) { return 0, errors.New("storage went away") }
