package onlyoffice

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// etagLocal is the local driver reporting an md5 etag, as S3 does.
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

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// The callback has always read the saved document back with Stat and put the
// backend's etag on the row before announcing the save — the one write surface
// that got this right. The shared write gate it announces through
// (protocolsync.WriteRows) now reads what landed as well; it must agree with
// the callback, not blank the etag the callback just stored. Pinned for a
// syncer wired without a way to reach the backend, and for no syncer at all.
func TestCallback_SaveKeepsTheLandedEtag(t *testing.T) {
	for _, tc := range []struct {
		name   string
		unwire bool
	}{
		{"syncer attached without a resolver", false},
		{"no syncer attached", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			base := &local.Driver{}
			require.NoError(t, base.Init(context.Background(), map[string]any{"path": h.root}))
			h.svc.StorageResolver = func(int64) (storage.Driver, error) { return etagLocal{base}, nil }
			if tc.unwire {
				h.svc.AttachSync(nil)
			}

			resp := h.save(t, StatusReadyForSaving, "EDITED IN THE OFFICE SUITE")
			require.Equal(t, 0, resp["error"])
			// The write event is emitted on a goroutine; wait for it, as the
			// other callback tests do, so the harness's cleanup (which resets
			// the process-wide sink) cannot race it.
			h.sink.wait(t)

			row, err := h.svc.Store.GetNode(context.Background(), h.node.ID)
			require.NoError(t, err)
			assert.Equal(t, md5hex("EDITED IN THE OFFICE SUITE"), row.Etag)
			assert.EqualValues(t, len("EDITED IN THE OFFICE SUITE"), row.Size)
		})
	}
}
