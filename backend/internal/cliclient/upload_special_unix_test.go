//go:build unix

package cliclient

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/regfile"
)

// `filex upload <dir>` over a folder holding a named pipe (issue #38's shape on
// the client side): the pipe is reported and skipped, everything else goes up,
// and the walk FINISHES — the unfixed walk opened the pipe and never returned.
func TestUploadTree_SkipsANamedPipe(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")
	root := makeTree(t)
	pipe := filepath.Join(root, "sub", ".cinit_cmd")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}

	type result struct {
		rep *TreeReport
		err error
	}
	done := make(chan result, 1)
	var special []string
	go func() {
		rep, err := api.UploadTree(context.Background(), root, "docs://inbox", func(ev TreeEvent) {
			if ev.Kind == TreeSpecial {
				special = append(special, ev.Local)
			}
		})
		done <- result{rep, err}
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the tree upload is waiting on a named pipe (issue #38)")
	}
	require.NoError(t, r.err)
	assert.Equal(t, 2, r.rep.Files, "the ordinary files still go up")
	assert.Empty(t, r.rep.Errors)
	assert.Equal(t, []string{pipe}, r.rep.Special)
	assert.Equal(t, []string{pipe}, special, "the progress stream says it too")
	for _, u := range fs.uploads {
		assert.NotEqual(t, ".cinit_cmd", u.Name)
	}
}

// A single `filex upload <pipe>` is refused at once rather than hanging.
func TestUpload_ANamedPipeIsRefusedAtOnce(t *testing.T) {
	_, srv := newFakeServer(t)
	api := testClient(srv, "good-token")
	pipe := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := api.uploadMultipart(context.Background(), RemotePath{Adapter: "docs", Rel: "inbox"}, "fifo", pipe, "")
		done <- err
	}()
	select {
	case err := <-done:
		assert.True(t, errors.Is(err, regfile.ErrNotRegular), "got %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("uploading a named pipe is waiting on it (issue #38)")
	}
}
