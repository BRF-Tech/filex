package thumb

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// panicDriver panics on every read, the way a generator does on a bug or a
// hostile file.
type panicDriver struct {
	storage.Driver
}

func (panicDriver) Read(context.Context, string) (io.ReadCloser, error) {
	panic("a generator tripped")
}

// A panic while drawing one file is that file's failure, never the
// process's. GenerateThumb runs on goroutines nothing else recovers (the
// upload's dispatchThumb, the backfill and repair walkers), so this test
// calls it the same way: had the panic escaped, the test binary itself would
// have died here, as the server did in the 2026-10-01 e2e run.
func TestGenerateThumb_PanicIsTheFilesFailure(t *testing.T) {
	f := newArchiveFixture(t, func(d storage.Driver) storage.Driver { return panicDriver{Driver: d} })
	n := f.put("notlar.txt", []byte("bir\niki\n"))
	n.Mime = "text/plain"

	done := make(chan error, 1)
	go func() { done <- f.p.GenerateThumb(context.Background(), n) }()
	var err error
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("GenerateThumb did not return")
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "a generator tripped")

	row := f.row(n.ID)
	require.NotNil(t, row, "the failure is recorded")
	require.Equal(t, "failed", row.State)
	require.Contains(t, row.Error, "a generator tripped")
	require.NotNil(t, row.AttemptedAt, "the attempt is stamped")
	require.Equal(t, SourceSig(n), row.SourceSig)
	// The same content is not drawn (and the panic not hit) again on the
	// next listing, long after the retry guard: only a change brings it back.
	require.Equal(t, Leave, f.p.Assess(n, row, time.Now().Add(24*time.Hour)))
}
