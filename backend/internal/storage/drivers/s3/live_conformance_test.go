package s3_test

// What a provider has to do before filex can call it supported.
//
// The S3 "standard" is a family of dialects: providers differ on multipart
// details, on copy semantics, on what a listing returns for a prefix, on
// whether a presigned URL survives their edge. Reading our own driver proves
// none of it. This suite drives the real driver against a real endpoint and
// exercises exactly the operations filex performs on a storage — so pointing
// it at a provider answers "does filex work on this" with a measurement
// instead of an opinion.
//
//	FILEX_TEST_S3_ENDPOINT=https://s3.eu-central-003.backblazeb2.com \
//	FILEX_TEST_S3_BUCKET=… FILEX_TEST_S3_ACCESS_KEY=… FILEX_TEST_S3_SECRET_KEY=… \
//	FILEX_TEST_S3_REGION=eu-central-003 \
//	  go test ./internal/storage/drivers/s3/ -run TestLiveProviderConformance -v
//
// It writes under a prefix of its own and deletes what it wrote.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/s3"
)

func liveDriver(t *testing.T) (storage.Driver, string) {
	t.Helper()
	bucket := os.Getenv("FILEX_TEST_S3_BUCKET")
	access := os.Getenv("FILEX_TEST_S3_ACCESS_KEY")
	secret := os.Getenv("FILEX_TEST_S3_SECRET_KEY")
	if bucket == "" || access == "" || secret == "" {
		t.Skip("set FILEX_TEST_S3_* to measure a real provider")
	}
	cfg := map[string]any{
		"bucket":     bucket,
		"endpoint":   os.Getenv("FILEX_TEST_S3_ENDPOINT"),
		"access_key": access,
		"secret_key": secret,
	}
	if r := os.Getenv("FILEX_TEST_S3_REGION"); r != "" {
		cfg["region"] = r
	}
	// Path style is what a local MinIO/Garage needs; a real provider is happy
	// either way and B2 accepts both.
	if os.Getenv("FILEX_TEST_S3_PATH_STYLE") != "0" {
		cfg["path_style"] = true
	}
	d, err := storage.Get("s3")
	require.NoError(t, err)
	require.NoError(t, d.Init(context.Background(), cfg))
	return d, fmt.Sprintf("filex-conformance/%d", time.Now().UnixNano())
}

func TestLiveProviderConformance(t *testing.T) {
	drv, prefix := liveDriver(t)
	ctx := context.Background()

	w, ok := drv.(storage.Writer)
	require.True(t, ok, "the driver must be able to write")
	del, ok := drv.(storage.Deleter)
	require.True(t, ok, "the driver must be able to delete")

	var written []string
	put := func(key string, body []byte) {
		require.NoError(t, w.Write(ctx, key, bytes.NewReader(body), int64(len(body))))
		written = append(written, key)
	}
	t.Cleanup(func() {
		for _, k := range written {
			_ = del.Delete(context.Background(), k)
		}
	})

	small := []byte("filex conformance — small object\n")
	smallKey := prefix + "/notes/small.txt"

	t.Run("write, stat and read back", func(t *testing.T) {
		put(smallKey, small)

		st, err := drv.Stat(ctx, smallKey)
		require.NoError(t, err)
		require.Equal(t, int64(len(small)), st.Size)

		rc, err := drv.Read(ctx, smallKey)
		require.NoError(t, err)
		defer rc.Close()
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.Equal(t, small, got)
	})

	t.Run("a listing shows the object and its folder", func(t *testing.T) {
		objs, err := drv.List(ctx, prefix)
		require.NoError(t, err)
		var names []string
		for _, o := range objs {
			names = append(names, o.Name)
		}
		require.Contains(t, names, "notes",
			"a prefix must come back as a directory entry — this is what filex lists")

		inner, err := drv.List(ctx, prefix+"/notes")
		require.NoError(t, err)
		require.NotEmpty(t, inner)
		require.Equal(t, "small.txt", inner[0].Name)
	})

	t.Run("a ranged read returns the window", func(t *testing.T) {
		rr, ok := drv.(storage.RangeReader)
		if !ok {
			t.Skip("driver does not advertise ranged reads")
		}
		rc, err := rr.ReadRange(ctx, smallKey, 6, 11)
		require.NoError(t, err)
		defer rc.Close()
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.Equal(t, "conformance", string(got))
	})

	t.Run("a large object goes up in parts and comes back whole", func(t *testing.T) {
		if testing.Short() {
			t.Skip("short mode")
		}
		// Above every provider's 5 MiB minimum part size, so the driver's
		// multipart path is the one under test rather than a single PUT.
		size := 12 << 20
		body := make([]byte, size)
		for i := range body {
			body[i] = byte('a' + i%26)
		}
		key := prefix + "/big.bin"
		put(key, body)

		st, err := drv.Stat(ctx, key)
		require.NoError(t, err)
		require.Equal(t, int64(size), st.Size, "the provider stored a different length")

		rc, err := drv.Read(ctx, key)
		require.NoError(t, err)
		defer rc.Close()
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.Equal(t, size, len(got))
		require.True(t, bytes.Equal(body, got), "the bytes came back different")
	})

	t.Run("a rename moves the object", func(t *testing.T) {
		mv, ok := drv.(storage.Mover)
		if !ok {
			t.Skip("driver does not advertise move")
		}
		dst := prefix + "/notes/renamed.txt"
		require.NoError(t, mv.Move(ctx, smallKey, dst))
		written = append(written, dst)

		_, err := drv.Stat(ctx, dst)
		require.NoError(t, err, "the object is not at the new key")
		_, err = drv.Stat(ctx, smallKey)
		require.Error(t, err, "the object is still at the old key")

		// Put it back so the rest of the suite reads a stable key.
		require.NoError(t, mv.Move(ctx, dst, smallKey))
	})

	t.Run("a presigned URL is fetchable by a browser", func(t *testing.T) {
		p, ok := drv.(storage.Presigner)
		if !ok {
			t.Skip("driver does not advertise presigning")
		}
		url, err := p.PresignDownload(ctx, smallKey, 5*time.Minute)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(url, "http"), "not a URL: %q", url)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, "the signed URL did not resolve")
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode,
			"the provider refused its own signed URL")
		got, _ := io.ReadAll(resp.Body)
		require.Equal(t, small, got)
	})

	t.Run("delete removes it, and the absence is reported as absence", func(t *testing.T) {
		require.NoError(t, del.Delete(ctx, smallKey))
		_, err := drv.Stat(ctx, smallKey)
		require.ErrorIs(t, err, storage.ErrNotFound,
			"a missing object must report storage.ErrNotFound, not a provider-specific error")
	})
}
