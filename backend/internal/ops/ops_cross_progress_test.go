package ops_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// gatedWriter reads the first half of every body, then waits for the test to
// release it — a transfer caught in the middle, the moment the tray is looked at.
type gatedWriter struct {
	storage.Driver
	halfway chan struct{}
	release chan struct{}
}

func (g gatedWriter) Write(ctx context.Context, p string, r io.Reader, size int64) error {
	w, ok := g.Driver.(storage.Writer)
	if !ok {
		return storage.ErrUnsupported
	}
	head := make([]byte, size/2)
	if _, err := io.ReadFull(r, head); err != nil {
		return err
	}
	select {
	case g.halfway <- struct{}{}:
	default:
	}
	select {
	case <-g.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return w.Write(ctx, p, io.MultiReader(bytes.NewReader(head), r), size)
}

// Issue #27: "during move status icon is like frozen and does not reflect
// actual status". A single-file cross-storage move is `0 of 1` sources until it
// ends; while it streams, the op must report how many BYTES have moved and how
// many there are in total.
func TestCross_RunningMove_ReportsBytes(t *testing.T) {
	g := gatedWriter{halfway: make(chan struct{}, 1), release: make(chan struct{})}
	f := newCrossFixture(t, func(d storage.Driver) storage.Driver { g.Driver = d; return g })

	const size = 4 << 20
	body := bytes.Repeat([]byte("filex-27 "), size/9+1)[:size]
	abs := filepath.Join(f.rootA, "buyuk.bin")
	require.NoError(t, os.WriteFile(abs, body, 0o644))

	ctx := context.Background()
	op, err := f.svc.SubmitTo(ctx, ops.OpMove, f.stA.ID, f.stB.ID, []string{"buyuk.bin"}, "/")
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go f.svc.Run(runCtx)
	defer f.svc.Stop()
	// Registered AFTER Stop so it runs BEFORE it: on a failed assertion the
	// worker is still parked in the gated write, and Stop would wait forever.
	release := sync.OnceFunc(func() { close(g.release) })
	defer release()

	select {
	case <-g.halfway:
	case <-time.After(10 * time.Second):
		t.Fatal("the transfer never reached the destination")
	}

	var mid *ops.Op
	deadline := time.Now().Add(5 * time.Second)
	for {
		mid, err = f.svc.Get(ctx, op.ID)
		require.NoError(t, err)
		if mid.BytesDone >= size/2 && mid.BytesTotal == size {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mid-transfer op reported bytes_done=%d bytes_total=%d, want >= %d of %d", mid.BytesDone, mid.BytesTotal, size/2, size)
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Equal(t, 0, mid.Done, "the source-count stays 0 of 1 — which is why bytes are needed")
	require.Less(t, mid.BytesDone, int64(size), "half the body is still waiting")

	listed, err := f.svc.List(ctx, "")
	require.NoError(t, err)
	var inList *ops.Op
	for _, o := range listed {
		if o.ID == op.ID {
			inList = o
		}
	}
	require.NotNil(t, inList)
	require.Equal(t, int64(size), inList.BytesTotal, "the tray polls List, so List carries the bytes too")

	release()
	deadline = time.Now().Add(10 * time.Second)
	for {
		cur, err := f.svc.Get(ctx, op.ID)
		require.NoError(t, err)
		if cur.Status == ops.StatusOK {
			require.Zero(t, cur.BytesTotal, "a finished op no longer carries live counters")
			break
		}
		require.NotEqual(t, ops.StatusFailed, cur.Status, cur.Error)
		if time.Now().After(deadline) {
			t.Fatalf("op never finished: %s", cur.Status)
		}
		time.Sleep(15 * time.Millisecond)
	}
	got, err := os.ReadFile(filepath.Join(f.rootB, "buyuk.bin"))
	require.NoError(t, err)
	require.True(t, bytes.Equal(body, got), "the counting reader must not change a byte")
}
