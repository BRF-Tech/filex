//go:build unix

package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/brf-tech/filex/backend/internal/storage"
)

func TestList_SkipsNamedPipe(t *testing.T) {
	d := newDriver(t)
	require.NoError(t, os.WriteFile(filepath.Join(d.Root(), "regular.txt"), []byte("hello"), 0o600))

	fifo := filepath.Join(d.Root(), "blocked.fifo")
	require.NoError(t, unix.Mkfifo(fifo, 0o600))

	type listResult struct {
		objects []storage.Object
		err     error
	}
	result := make(chan listResult, 1)
	go func() {
		objects, err := d.List(context.Background(), "/")
		result <- listResult{objects: objects, err: err}
	}()

	select {
	case got := <-result:
		require.NoError(t, got.err)
		require.Len(t, got.objects, 1)
		assert.Equal(t, "regular.txt", got.objects[0].Name)
		assert.Equal(t, storage.KindFile, got.objects[0].Kind)
	case <-time.After(time.Second):
		// Let a broken implementation that opened the FIFO finish before the
		// test exits, avoiding a leaked goroutine while still reporting the hang.
		go func() {
			writer, err := os.OpenFile(fifo, os.O_WRONLY, 0)
			if err == nil {
				_, _ = writer.Write([]byte("x"))
				_ = writer.Close()
			}
		}()
		<-result
		t.Fatal("List blocked while inspecting a named pipe")
	}
}
