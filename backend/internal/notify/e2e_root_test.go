package notify_test

// A row about an item inside an end-to-end encrypted folder carries
// meta.e2e_root, so the bell can say "🔒 Encrypted item" instead of printing
// the ciphertext name a level-2 folder stores (docs/E2E-ENCRYPTION.md).

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestSend_MarksRowsInsideAnEncryptedFolder(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st := newStorage(t, store, "yerel")
	ctx := context.Background()
	mk := func(name, p string, typ model.NodeType, parent *int64) *model.Node {
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, ParentID: parent, Name: name, Path: p,
			PathHash: pathkey.Hash(st.ID, p), Type: typ, Size: 4, Etag: "e-" + name,
		})
		require.NoError(t, err)
		return n
	}
	kasa := mk("Kasa", "/Kasa", model.NodeTypeDirectory, nil)
	mk(".filex-e2e.json", "/Kasa/.filex-e2e.json", model.NodeTypeFile, &kasa.ID)
	const stored = "cnCYVvOrMoH0uQKjxUUeYr9h7KREShFsI3Y"

	svc := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
	defer svc.Stop()

	metaOf := func(id int64) map[string]any {
		rows, _, err := svc.List(ctx, nil, notify.AdminBell, false, 20, 0)
		require.NoError(t, err)
		for _, r := range rows {
			if r.ID == id {
				var m map[string]any
				require.NoError(t, json.Unmarshal(r.MetaJSON, &m))
				return m
			}
		}
		t.Fatalf("row %d not listed", id)
		return nil
	}
	send := func(p string) int64 {
		id, err := svc.Send(ctx, notify.Event{
			Event:    notify.EventFileUploaded,
			Severity: notify.SeverityInfo,
			Node:     &notify.NodeRef{StorageID: st.ID, Path: p, Name: path.Base(p)},
			Target:   notify.FileTarget(strings.TrimPrefix(p, "/")),
		})
		require.NoError(t, err)
		return id
	}

	inside := metaOf(send("/Kasa/" + stored))
	require.Equal(t, "yerel://Kasa", inside["e2e_root"])

	outside := metaOf(send("/Açık/rapor.txt"))
	_, marked := outside["e2e_root"]
	require.False(t, marked, "a row outside every encrypted folder is not marked")

	// The encrypted folder itself: its name is plaintext, it is not inside itself.
	self := metaOf(send("/Kasa"))
	_, marked = self["e2e_root"]
	require.False(t, marked)
}
