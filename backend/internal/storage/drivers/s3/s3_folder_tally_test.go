package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
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

// A folder copy, move or delete on an object store is ONE driver call that
// lists the folder and then works through its objects one at a time. Nothing
// outside the call could see how far it had got: the operations centre read
// "0%" until the whole folder was done. The call now counts on the context's
// tally: the total as soon as the listing is in, one more per object.

// folderStore is an object store of keys that records, at every copy and
// every delete, how far the tally said the call had got.
type folderStore struct {
	mu    sync.Mutex
	keys  map[string]bool
	tally *storage.Tally
	// at[i] is {done, total} as the i-th copy or delete arrived.
	at [][2]int64
}

func folderStub(t *testing.T, keys ...string) (*Driver, *folderStore) {
	t.Helper()
	fs := &folderStore{keys: map[string]bool{}}
	for _, k := range keys {
		fs.keys[k] = true
	}
	note := func() {
		done, total := fs.tally.Load()
		fs.at = append(fs.at, [2]int64{done, total})
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		key := strings.TrimPrefix(r.URL.Path, "/b/")
		switch {
		case r.URL.Query().Get("list-type") == "2":
			prefix := r.URL.Query().Get("prefix")
			var under []string
			for k := range fs.keys {
				if strings.HasPrefix(k, prefix) {
					under = append(under, k)
				}
			}
			sort.Strings(under)
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(listXML(prefix, under...)))
		case r.Method == http.MethodHead:
			if !fs.keys[key] {
				http.Error(w, "", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
			note()
			fs.keys[key] = true
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<CopyObjectResult><ETag>"abc"</ETag>` +
				`<LastModified>2026-09-01T10:00:00.000Z</LastModified></CopyObjectResult>`))
		case r.Method == http.MethodDelete:
			note()
			delete(fs.keys, key)
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
	return d, fs
}

func (fs *folderStore) counting() context.Context {
	fs.tally = &storage.Tally{}
	return storage.WithTally(context.Background(), fs.tally)
}

var folder = []string{"dir/a.txt", "dir/b.txt", "dir/sub/c.txt"}

func TestFolderMove_CountsItsObjects(t *testing.T) {
	d, fs := folderStub(t, folder...)
	require.NoError(t, d.Move(fs.counting(), "dir", "moved"))

	done, total := fs.tally.Load()
	assert.EqualValues(t, 3, total)
	assert.EqualValues(t, 3, done)
	// A move is a copy and a delete per object; the total is known before the
	// first, and each object counts once, after its delete.
	require.Len(t, fs.at, 6)
	assert.Equal(t, [][2]int64{{0, 3}, {0, 3}, {1, 3}, {1, 3}, {2, 3}, {2, 3}}, fs.at)
}

func TestFolderCopy_CountsItsObjects(t *testing.T) {
	d, fs := folderStub(t, folder...)
	require.NoError(t, d.Copy(fs.counting(), "dir", "copied"))

	done, total := fs.tally.Load()
	assert.EqualValues(t, 3, total)
	assert.EqualValues(t, 3, done)
	assert.Equal(t, [][2]int64{{0, 3}, {1, 3}, {2, 3}}, fs.at)
}

func TestFolderDelete_CountsItsObjects(t *testing.T) {
	d, fs := folderStub(t, folder...)
	require.NoError(t, d.Delete(fs.counting(), "dir"))

	done, total := fs.tally.Load()
	assert.EqualValues(t, 3, total)
	assert.EqualValues(t, 3, done)
	assert.Equal(t, [][2]int64{{0, 3}, {1, 3}, {2, 3}}, fs.at)
}

// A file is one object, and it is counted as one.
func TestFileMove_IsOneObject(t *testing.T) {
	d, fs := folderStub(t, "dir/a.txt")
	require.NoError(t, d.Move(fs.counting(), "dir/a.txt", "a.txt"))
	done, total := fs.tally.Load()
	assert.EqualValues(t, 1, total)
	assert.EqualValues(t, 1, done)
}

// With no tally on the context nothing is counted and nothing breaks.
func TestFolderMove_WithoutATallyIsUnchanged(t *testing.T) {
	d, fs := folderStub(t, folder...)
	fs.tally = nil
	require.NoError(t, d.Move(context.Background(), "dir", "moved"))
	assert.True(t, fs.keys["moved/sub/c.txt"])
}
