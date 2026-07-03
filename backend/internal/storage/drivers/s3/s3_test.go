package s3

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// TestInit_BucketRequired returns an error when bucket is missing.
func TestInit_BucketRequired(t *testing.T) {
	d := &Driver{}
	err := d.Init(context.Background(), map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bucket")
}

// TestInit_Defaults_Region — when region is missing, driver defaults to
// "auto" so providers like Cloudflare R2 work without an explicit region.
func TestInit_Defaults_Region(t *testing.T) {
	d := &Driver{}
	err := d.Init(context.Background(), map[string]any{
		"bucket":     "test-bucket",
		"access_key": "fake",
		"secret_key": "fake",
	})
	require.NoError(t, err)
	assert.Equal(t, "auto", d.region)
}

// TestInit_PathStyle propagates the path_style flag. Hetzner Object
// Storage requires this.
func TestInit_PathStyle(t *testing.T) {
	d := &Driver{}
	err := d.Init(context.Background(), map[string]any{
		"bucket":     "hz",
		"region":     "nbg1",
		"endpoint":   "https://nbg1.your-objectstorage.com",
		"path_style": true,
		"access_key": "fake",
		"secret_key": "fake",
	})
	require.NoError(t, err)
	assert.True(t, d.pathStyle)
	assert.Equal(t, "nbg1", d.region)
}

func TestKeyAndUnkey_NoPrefix(t *testing.T) {
	d := &Driver{bucket: "b"}
	assert.Equal(t, "foo/bar.txt", d.key("/foo/bar.txt"))
	assert.Equal(t, "/foo/bar.txt", d.unkey("foo/bar.txt"))
}

func TestKeyAndUnkey_WithPrefix(t *testing.T) {
	d := &Driver{bucket: "b", prefix: "tenant1/"}
	assert.Equal(t, "tenant1/foo/bar.txt", d.key("/foo/bar.txt"))
	// unkey should strip the prefix back off.
	assert.Equal(t, "/foo/bar.txt", d.unkey("tenant1/foo/bar.txt"))
}

func TestCapabilities(t *testing.T) {
	d := &Driver{}
	c := d.Capabilities()
	assert.True(t, c.Read)
	assert.True(t, c.Write)
	assert.True(t, c.Move)
	assert.True(t, c.Copy)
	assert.True(t, c.Delete)
	assert.True(t, c.Mkdir)
	assert.True(t, c.Presign)
	assert.False(t, c.Watch, "S3 native watch needs SQS/EventBridge — skipped in v0.1")
}

func TestRegistration(t *testing.T) {
	// init() should have registered the driver under "s3".
	d, err := storage.Get("s3")
	require.NoError(t, err)
	assert.Equal(t, "s3", d.Name())
}

// TestInit_Integration only runs with INTEGRATION=1 — gated because it
// reaches out to a real (or local) S3 endpoint.
func TestInit_Integration(t *testing.T) {
	if testing.Short() || os.Getenv("INTEGRATION") != "1" {
		t.Skip("integration only — set INTEGRATION=1 to enable")
	}
	bucket := os.Getenv("FILEX_TEST_S3_BUCKET")
	endpoint := os.Getenv("FILEX_TEST_S3_ENDPOINT")
	access := os.Getenv("FILEX_TEST_S3_ACCESS_KEY")
	secret := os.Getenv("FILEX_TEST_S3_SECRET_KEY")
	if bucket == "" || access == "" || secret == "" {
		t.Skip("missing FILEX_TEST_S3_* env vars")
	}
	d := &Driver{}
	require.NoError(t, d.Init(context.Background(), map[string]any{
		"bucket":     bucket,
		"endpoint":   endpoint,
		"access_key": access,
		"secret_key": secret,
		"path_style": true,
	}))

	// Smoke-check List doesn't immediately fail.
	_, err := d.List(context.Background(), "/")
	require.NoError(t, err)
}
