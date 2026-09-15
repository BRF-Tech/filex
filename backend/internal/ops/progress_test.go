package ops

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

// The S3 driver rewinds a seekable body to retry a signed request. A rewind
// must neither hide the Seeker (the driver would then buffer or split the
// upload) nor count the replayed bytes twice.
func TestCountBytes_SeekableStaysSeekableAndRewindIsNotCountedTwice(t *testing.T) {
	var reported int64
	rc := countBytes(nopSeekCloser{bytes.NewReader([]byte("0123456789"))}, func(n int64) { reported += n })

	sk, ok := rc.(io.Seeker)
	require.True(t, ok, "a seekable source must stay seekable")

	buf := make([]byte, 6)
	_, err := io.ReadFull(rc, buf)
	require.NoError(t, err)
	require.EqualValues(t, 6, reported)

	_, err = sk.Seek(0, io.SeekStart)
	require.NoError(t, err)
	rest, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "0123456789", string(rest))
	require.EqualValues(t, 10, reported, "the replayed first six bytes are not counted again")
}

func TestCountBytes_NonSeekableStaysNonSeekable(t *testing.T) {
	rc := countBytes(io.NopCloser(bytes.NewReader([]byte("abc"))), func(int64) {})
	_, ok := rc.(io.Seeker)
	require.False(t, ok, "wrapping must not invent a Seek the source cannot do")
}

func TestMeasureSources_SumsTreesAndSkipsBookkeeping(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, n int) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, bytes.Repeat([]byte("x"), n), 0o644))
	}
	write("klasor/a.bin", 100)
	write("klasor/alt/b.bin", 250)
	write("klasor/.thumbs/t.jpg", 9999)
	write("tek.bin", 30)

	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": root}))

	total, ok := measureSources(context.Background(), drv, []string{"klasor", "tek.bin"})
	require.True(t, ok)
	require.EqualValues(t, 380, total, ".thumbs is not transferred, so it is not counted")
}
