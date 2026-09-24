//go:build unix

package sync_test

// Issue #38, end to end: a storage scan over a local root that holds a named
// pipe and a socket has to FINISH. Before the fix it did not fail — it never
// returned: the run stayed `running` with seen_count 0, and every later run
// queued behind it. Everything here runs under a deadline for that reason.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestSync_ALocalStorageHoldingAPipeAndASocketFinishes(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)

	// The reporter's shape: Docker overlay directories under the storage root,
	// `.cinit_cmd` pipes and X11/VNC sockets among ordinary files.
	root, err := os.MkdirTemp("", "fx38s")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	writeUnder(t, root, "rapor.txt", "gercek icerik")
	writeUnder(t, root, ".liveos/docker/overlay2/abc/merged/tmp/keep.txt", "komsu")
	overlay := filepath.Join(root, ".liveos", "docker", "overlay2", "abc", "merged", "tmp")
	if err := syscall.Mkfifo(filepath.Join(overlay, ".cinit_cmd"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	l, err := net.Listen("unix", filepath.Join(root, "vnc.sock"))
	if err != nil {
		t.Skipf("unix socket: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "overlay", Driver: "local", MountPath: "/overlay", ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	w := filexsync.New(store)
	w.AttachIndex(idx)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)

	done := make(chan error, 1)
	go func() { done <- w.Trigger(ctx, st.ID) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the storage scan did not finish in 30s — it is waiting on the named pipe (issue #38)")
	}

	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.Equal(t, "ok", run.Status, "the run never closed cleanly")
	assert.Empty(t, run.Error)
	assert.NotNil(t, run.FinishedAt)
	assert.Positive(t, run.SeenCount)

	for _, p := range []string{"/rapor.txt", "/.liveos/docker/overlay2/abc/merged/tmp/keep.txt"} {
		n := node(t, store, st.ID, p)
		require.NotNil(t, n, "%s must be catalogued", p)
		assert.Equal(t, model.NodeTypeFile, n.Type)
	}
	for _, p := range []string{"/.liveos/docker/overlay2/abc/merged/tmp/.cinit_cmd", "/vnc.sock"} {
		assert.Nil(t, node(t, store, st.ID, p), "%s is not a file and must not be catalogued as one", p)
	}

	// A second scan finishes too — the first no longer leaves anything behind
	// for it to queue on.
	go func() { done <- w.Trigger(ctx, st.ID) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the second scan did not finish")
	}
}
