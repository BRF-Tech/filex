package dav

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// etagLocal is the local driver reporting an md5 etag, as an S3 backend does.
type etagLocal struct{ *local.Driver }

func (d etagLocal) Stat(ctx context.Context, p string) (storage.Object, error) {
	o, err := d.Driver.Stat(ctx, p)
	if err != nil || o.Kind != storage.KindFile {
		return o, err
	}
	rc, rerr := d.Driver.Read(ctx, p)
	if rerr != nil {
		return o, nil
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	sum := md5.Sum(b)
	o.Etag = hex.EncodeToString(sum[:])
	return o, nil
}

// A PUT over an existing file used to leave the REPLACED file's etag on the
// row (protocolsync.WriteRows carried it forward). The same code path serves
// the S3 gateway, SFTP, FTPS and NFS; this pins it through WebDAV.
func TestPutOverwrite_RecordsTheNewEtag(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	email, pass := dbtest.SeedAdmin(t, store)
	root := t.TempDir()
	cfg, _ := json.Marshal(map[string]any{"path": root})
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "depo", Driver: "local", MountPath: "/depo", ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	base := &local.Driver{}
	require.NoError(t, base.Init(ctx, map[string]any{"path": root}))
	resolver := func(int64) (storage.Driver, error) { return etagLocal{base}, nil }

	h := NewHandler(Config{Enabled: true, Store: store, Resolver: resolver, ACL: acl.New(store)})
	mux := http.NewServeMux()
	mux.Handle(Prefix+"/", h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ha := &harness{store: store, h: h, srv: srv, resolver: resolver, adminEmail: email, adminPass: pass}

	resp := ha.req(t, http.MethodPut, "/dav/depo/report.txt", email, pass, "first", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	n := ha.nodeByPath(t, st.ID, "report.txt")
	require.NotNil(t, n)
	// What a storage scan leaves on the row: the backend's etag.
	sum := md5.Sum([]byte("first"))
	require.NoError(t, store.UpdateNodeMeta(ctx, n.ID, n.Size, n.Mime, hex.EncodeToString(sum[:]), time.Now()))

	resp = ha.req(t, http.MethodPut, "/dav/depo/report.txt", email, pass, "second, longer", nil)
	require.Less(t, resp.StatusCode, 300)

	after := ha.nodeByPath(t, st.ID, "report.txt")
	require.NotNil(t, after)
	sum = md5.Sum([]byte("second, longer"))
	assert.Equal(t, hex.EncodeToString(sum[:]), after.Etag, "the replaced file's etag was kept")
	assert.EqualValues(t, len("second, longer"), after.Size)
}
