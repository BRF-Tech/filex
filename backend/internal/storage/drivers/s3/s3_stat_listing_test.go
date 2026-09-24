package s3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// listingStub answers every HeadObject 404 (a folder has no object of its
// own) and every ListObjectsV2 with `list` — which may fail.
func listingStub(t *testing.T, list func(w http.ResponseWriter, r *http.Request)) *Driver {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") == "2" {
			list(w, r)
			return
		}
		http.Error(w, "", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("fake", "fake", "")),
		awsconfig.WithRetryMaxAttempts(1),
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

// ⚠⚠ Stat of a folder is a HEAD (404 — a folder is only a prefix) and a
// listing. A listing that FAILED used to be read as "no children", so Stat
// answered ErrNotFound — and "not found" is what makes a name look free:
// the rename guard let a rename onto the folder's name through and the
// driver's copy-then-delete merged the renamed folder into it, replacing
// every object with the same name. A failed listing is an error, never a
// verdict.
func TestStat_AFailedListingIsNotNotFound(t *testing.T) {
	d := listingStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`<Error><Code>SlowDown</Code><Message>Please reduce your request rate.</Message></Error>`))
	})
	_, err := d.Stat(context.Background(), "projeler")
	require.Error(t, err)
	assert.False(t, errors.Is(err, storage.ErrNotFound), "a listing that failed said the folder is not there: %v", err)
	assert.True(t, storage.Exists(context.Background(), d, "projeler"),
		"a name the store cannot vouch for must count as taken, never as free")
}

// One key answers "does anything live under this prefix?": the probe used to
// page through every object under it, so a Stat of a folder of 100,000 files
// listed all of them.
func TestStat_AFolderProbeAsksForOneKey(t *testing.T) {
	var mu sync.Mutex
	var maxKeys []string
	d := listingStub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		maxKeys = append(maxKeys, r.URL.Query().Get("max-keys"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(listXML(r.URL.Query().Get("prefix"), "projeler/a.txt")))
	})
	obj, err := d.Stat(context.Background(), "projeler")
	require.NoError(t, err)
	assert.Equal(t, storage.KindDirectory, obj.Kind)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"1"}, maxKeys)
}
