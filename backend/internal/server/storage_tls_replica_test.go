package server

// A replication target is a connection of its own (#298): its certificate
// switch reaches the target's driver through the resolver, and neither side
// hears the other's. The target here is a real WebDAV server on real TLS with a
// certificate nothing trusts.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/webdav"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// selfSignedTarget points the fixture's target at a self-signed WebDAV server.
func selfSignedTarget(t *testing.T, f *cacheFixture, skip *bool) webdav.FileSystem {
	t.Helper()
	fs := webdav.NewMemFS()
	srv := httptest.NewTLSServer(&webdav.Handler{FileSystem: fs, LockSystem: webdav.NewMemLS()})
	t.Cleanup(srv.Close)
	cfg := map[string]any{"url": srv.URL, "user": "u", "password": "p", "root": "backup", "max_attempts": 1}
	if skip != nil {
		cfg[storage.SkipTLSVerifyKey] = *skip
	}
	require.NoError(t, fs.Mkdir(context.Background(), "/backup", 0o755))
	// The storage's folder there: this server refuses a PUT into one that is
	// missing, and that is not what is measured here.
	require.NoError(t, fs.Mkdir(context.Background(), "/backup/"+f.st.Name, 0o755))
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	f.target.Driver = "webdav"
	f.target.ConfigJSON = raw
	require.NoError(t, f.store.UpdateReplicationTarget(context.Background(), f.target))
	return fs
}

func (f *cacheFixture) writeThroughResolver(t *testing.T, name, body string) {
	t.Helper()
	drv, err := f.cache.resolve(f.st.ID)
	require.NoError(t, err)
	_, wrapped := storage.AsReplicated(drv)
	require.True(t, wrapped)
	require.NoError(t, drv.(storage.Writer).Write(context.Background(), name, strings.NewReader(body), int64(len(body))))
}

func TestReplicationTarget_TheSwitchReachesItsDriver(t *testing.T) {
	f := newCacheFixture(t, true)
	on := true
	fs := selfSignedTarget(t, f, &on)

	f.writeThroughResolver(t, "a.txt", "over self-signed TLS")

	require.Eventually(t, func() bool {
		st, err := fs.Stat(context.Background(), "/backup/"+f.folder()+"/a.txt")
		return err == nil && st.Size() == int64(len("over self-signed TLS"))
	}, 10*time.Second, 10*time.Millisecond, "the file never reached the target")
}

// The primary's own configuration says nothing about the target's connection:
// without the target's switch the change is refused and recorded, and the
// primary is untouched.
func TestReplicationTarget_AnswersForItselfNotForThePrimary(t *testing.T) {
	f := newCacheFixture(t, true)
	raw, err := json.Marshal(map[string]any{"path": f.primaryDir, storage.SkipTLSVerifyKey: true})
	require.NoError(t, err)
	f.st.ConfigJSON = raw
	require.NoError(t, f.store.UpdateStorage(context.Background(), f.st))
	selfSignedTarget(t, f, nil)

	f.writeThroughResolver(t, "a.txt", "x")

	_, err = os.Stat(f.primaryDir + "/a.txt")
	require.NoError(t, err, "the primary has the file either way")
	require.Eventually(t, func() bool {
		rows, _, err := f.store.ListReplicaFailures(context.Background(), true, 10, 0)
		return err == nil && len(rows) == 1
	}, 10*time.Second, 20*time.Millisecond, "a target that refused the certificate left no trace")
	rows, _, err := f.store.ListReplicaFailures(context.Background(), true, 10, 0)
	require.NoError(t, err)
	assert.Contains(t, rows[0].ErrorMsg, "certificate")
}

// The switch survives the trip through the masked admin surface: a target's
// configuration is shown and saved back, and a bool is not a secret.
func TestReplicationTarget_TheSwitchIsNotACredential(t *testing.T) {
	raw := json.RawMessage(`{"url":"https://dav.example.com/","user":"u","password":"p","root":"b","insecure_skip_tls_verify":true}`)

	shown := storage.MaskSecrets("webdav", raw)

	assert.Contains(t, string(shown), `"insecure_skip_tls_verify":true`)
	kept, err := storage.KeepSecrets("webdav", shown, "webdav", raw)
	require.NoError(t, err)
	assert.JSONEq(t, string(raw), string(kept))
}
