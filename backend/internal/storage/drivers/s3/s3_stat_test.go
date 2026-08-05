package s3

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// listXML renders a minimal ListObjectsV2 response holding the given keys.
func listXML(prefix string, keys ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>`+
		`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+
		`<Name>b</Name><Prefix>%s</Prefix><KeyCount>%d</KeyCount>`+
		`<MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>`, prefix, len(keys))
	for _, k := range keys {
		fmt.Fprintf(&b, `<Contents><Key>%s</Key>`+
			`<LastModified>2026-08-05T00:00:00.000Z</LastModified>`+
			`<ETag>&quot;d41d8cd98f00b204e9800998ecf8427e&quot;</ETag>`+
			`<Size>0</Size><StorageClass>STANDARD</StorageClass></Contents>`, k)
	}
	b.WriteString(`</ListBucketResult>`)
	return b.String()
}

// objectStoreStub answers HeadObject 404 for every key (nothing is a real
// object) and ListObjectsV2 with whatever `contents` holds for the requested
// prefix — an object store where "deneme/" exists only as a prefix carrying
// a .empty marker, which is exactly how filex represents an empty folder.
func statStub(t *testing.T, contents map[string][]string) *Driver {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") == "2" {
			prefix := r.URL.Query().Get("prefix")
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(listXML(prefix, contents[prefix]...)))
			return
		}
		http.Error(w, "", http.StatusNotFound) // HeadObject / GetObject
	}))
	t.Cleanup(srv.Close)

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("fake", "fake", ""),
		),
	)
	require.NoError(t, err)

	d := &Driver{bucket: "b", region: "auto", endpoint: srv.URL, pathStyle: true}
	d.client = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.UsePathStyle = true
		o.HTTPClient = srv.Client()
	})
	return d
}

// TestStat_FolderIsADirectory — Stat used to HeadObject the bare key and
// stop there. S3 has no directories: a folder is a prefix, and filex marks
// an empty one with a hidden .empty object *inside* it. So Stat answered
// ErrNotFound for every folder.
//
// WebDAV is where that surfaced: OpenFile stats the parent before a PUT and
// maps a miss to 409, and PROPFIND on the folder 404'd. olivov could write
// to a storage root but not into any subfolder (H3, 2026-08-05).
func TestStat_FolderIsADirectory(t *testing.T) {
	d := statStub(t, map[string][]string{
		"deneme/": {"deneme/.empty"},
	})

	obj, err := d.Stat(context.Background(), "/deneme")
	require.NoError(t, err, "a prefix with contents must stat as a directory")
	assert.Equal(t, storage.KindDirectory, obj.Kind)
	assert.Equal(t, "deneme", obj.Name)
	assert.Equal(t, "/deneme", obj.Path)
}

// TestStat_FolderWithRealFiles — a folder holding actual files, not just the
// marker.
func TestStat_FolderWithRealFiles(t *testing.T) {
	d := statStub(t, map[string][]string{
		"projeler/": {"projeler/a.txt", "projeler/b.txt"},
	})

	obj, err := d.Stat(context.Background(), "projeler")
	require.NoError(t, err)
	assert.Equal(t, storage.KindDirectory, obj.Kind)
}

// TestStat_MissingIsStillNotFound — an empty prefix is not a directory, and
// the not-found mapping must survive the fix so callers keep getting clean
// 404s instead of a phantom folder.
func TestStat_MissingIsStillNotFound(t *testing.T) {
	d := statStub(t, map[string][]string{})

	_, err := d.Stat(context.Background(), "/yok")
	require.ErrorIs(t, err, storage.ErrNotFound)
}

// TestIsDir_StillWorks — isDir shares the prefix probe with Stat; guard
// against the two recursing into each other.
func TestIsDir_StillWorks(t *testing.T) {
	d := statStub(t, map[string][]string{
		"deneme/": {"deneme/.empty"},
	})

	assert.True(t, d.isDir(context.Background(), "/deneme"))
	assert.False(t, d.isDir(context.Background(), "/yok"))
}
