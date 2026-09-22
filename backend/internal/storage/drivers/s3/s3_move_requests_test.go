package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

// requestLog is an object store holding a set of keys, counting what it is
// asked: HEADs, copies (a PUT carrying x-amz-copy-source), deletes, listings.
type requestLog struct {
	mu     sync.Mutex
	keys   map[string]bool
	counts map[string]int
}

func (l *requestLog) count(kind string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.counts[kind]
}

func moveStub(t *testing.T, keys ...string) (*Driver, *requestLog) {
	t.Helper()
	log := &requestLog{keys: map[string]bool{}, counts: map[string]int{}}
	for _, k := range keys {
		log.keys[k] = true
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.mu.Lock()
		defer log.mu.Unlock()
		key := strings.TrimPrefix(r.URL.Path, "/b/")
		switch {
		case r.URL.Query().Get("list-type") == "2":
			log.counts["LIST"]++
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(listXML(r.URL.Query().Get("prefix"))))
		case r.Method == http.MethodHead:
			log.counts["HEAD"]++
			if !log.keys[key] {
				http.Error(w, "", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", "3")
			w.Header().Set("ETag", `"abc"`)
			w.Header().Set("Last-Modified", "Mon, 01 Sep 2026 10:00:00 GMT")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
			log.counts["COPY"]++
			src := strings.TrimPrefix(r.Header.Get("X-Amz-Copy-Source"), "b/")
			if !log.keys[src] {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code><Message>no</Message></Error>`))
				return
			}
			log.keys[key] = true
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<CopyObjectResult><ETag>"abc"</ETag>` +
				`<LastModified>2026-09-01T10:00:00.000Z</LastModified></CopyObjectResult>`))
		case r.Method == http.MethodDelete:
			log.counts["DELETE"]++
			delete(log.keys, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected "+r.Method, http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("fake", "fake", "")),
	)
	require.NoError(t, err)
	d := &Driver{bucket: "b", region: "auto", endpoint: srv.URL, pathStyle: true}
	d.client = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.UsePathStyle = true
		o.HTTPClient = srv.Client()
	})
	return d, log
}

// Moving a file — which is what putting it in the trash is — cost TWO HEADs:
// Move asked "is this a folder?" and then Copy asked the same question again
// about the same key. Across a bulk delete that is a quarter of every request
// sent. One is enough.
func TestMove_AFileIsOneHeadOneCopyOneDelete(t *testing.T) {
	d, log := moveStub(t, "docs/a.txt")
	require.NoError(t, d.Move(context.Background(), "docs/a.txt", ".filex-trash/1-abc__a.txt"))
	assert.Equal(t, 1, log.count("HEAD"))
	assert.Equal(t, 1, log.count("COPY"))
	assert.Equal(t, 1, log.count("DELETE"))
}

// Copy on its own still asks, because nobody asked for it.
func TestCopy_StillChecksForAFolderItself(t *testing.T) {
	d, log := moveStub(t, "docs/a.txt")
	require.NoError(t, d.Copy(context.Background(), "docs/a.txt", "docs/b.txt"))
	assert.Equal(t, 1, log.count("HEAD"))
	assert.Equal(t, 1, log.count("COPY"))
	assert.Zero(t, log.count("DELETE"))
}

// A source that is gone still reads as ErrNotFound, which is how trash.Put
// tells "already deleted" from "the move failed".
func TestMove_AMissingSourceIsNotFound(t *testing.T) {
	d, log := moveStub(t)
	err := d.Move(context.Background(), "gone.txt", ".filex-trash/1-abc__gone.txt")
	assert.ErrorIs(t, err, storage.ErrNotFound)
	assert.Zero(t, log.count("DELETE"), "nothing may be deleted when nothing was copied")
}
