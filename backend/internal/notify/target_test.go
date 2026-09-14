package notify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// The target is the field a click routes on. These tests pin the two things
// that can silently break it: the storage NAME has to appear (a client cannot
// turn a numeric id into one), and a target that cannot be completed has to
// come back as "none" rather than as a path with no storage.

func newStorage(t *testing.T, store interface {
	CreateStorage(context.Context, *model.Storage) (*model.Storage, error)
}, name string) *model.Storage {
	t.Helper()
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:      name,
		Driver:    "local",
		MountPath: t.TempDir(),
		Enabled:   true,
	})
	require.NoError(t, err)
	return st
}

func TestTarget_FileCarriesStorageName(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st := newStorage(t, store, "qldemo")

	svc := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
	defer svc.Stop()

	id, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventFileUploaded,
		Severity: notify.SeverityInfo,
		Node:     &notify.NodeRef{StorageID: st.ID, Path: "Documents/report.pdf", Name: "report.pdf"},
		Target:   notify.FileTarget("Documents/report.pdf"),
	})
	require.NoError(t, err)

	rows, _, err := svc.List(context.Background(), nil, false, 10, 0)
	require.NoError(t, err)
	var row *model.Notification
	for _, r := range rows {
		if r.ID == id {
			row = r
		}
	}
	require.NotNil(t, row, "the row we just sent is in the list")
	require.NotNil(t, row.Target, "List hydrates target out of meta")
	assert.Equal(t, model.TargetFile, row.Target.Kind)
	assert.Equal(t, "qldemo", row.Target.Storage, "the NAME, resolved centrally")
	assert.Equal(t, "Documents/report.pdf", row.Target.Path)
}

func TestTarget_UnresolvableStorageDowngradesToNone(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	svc := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
	defer svc.Stop()

	// storage id 4242 does not exist — half an address is worse than none:
	// the client would open whatever storage happened to be in front of it.
	id, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventFileUploaded,
		Severity: notify.SeverityInfo,
		Node:     &notify.NodeRef{StorageID: 4242, Path: "a/b.txt", Name: "b.txt"},
		Target:   notify.FileTarget("a/b.txt"),
	})
	require.NoError(t, err)

	row, err := store.GetNotification(context.Background(), id)
	require.NoError(t, err)
	assert.Nil(t, model.TargetFromMeta(row.MetaJSON), "no target persisted")
}

func TestTarget_ShareAndNoneAndWirePayload(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st := newStorage(t, store, "qldemo")

	bodies := make(chan []byte, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 8192)
		n, _ := r.Body.Read(b)
		bodies <- append([]byte(nil), b[:n]...)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	svc := notify.New(store, notify.Config{WebhookURL: srv.URL, HTTPTimeout: 2 * time.Second, RetryBackoffs: []time.Duration{}})
	defer svc.Stop()

	_, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventShareCreated,
		Severity: notify.SeverityInfo,
		Node:     &notify.NodeRef{StorageID: st.ID, Path: "Documents", Name: "Documents"},
		Share:    &notify.ShareRef{Token: "tok123", Path: "Documents"},
		Target:   notify.ShareTarget("tok123"),
	})
	require.NoError(t, err)
	svc.Wait()

	var got struct {
		Target *model.NotificationTarget `json:"target"`
	}
	require.NoError(t, json.Unmarshal(<-bodies, &got))
	require.NotNil(t, got.Target, "the webhook payload carries target too")
	assert.Equal(t, model.TargetShare, got.Target.Kind)
	assert.Equal(t, "tok123", got.Target.ID)
	assert.Empty(t, got.Target.Path, "a share target is a token, never a path")

	// An instance-wide alert has nothing to open and says so on the wire.
	_, err = svc.Send(context.Background(), notify.Event{
		Event:    notify.EventReplicaFail,
		Severity: notify.SeverityWarning,
		Meta:     map[string]any{"path": "fileman/x"},
	})
	require.NoError(t, err)
	svc.Wait()
	require.NoError(t, json.Unmarshal(<-bodies, &got))
	require.NotNil(t, got.Target)
	assert.Equal(t, model.TargetNone, got.Target.Kind)
}

// writehook is where five of the file events are born; these pin the two
// choices that are easy to get backwards.
func TestWritehookTargets(t *testing.T) {
	assert.Equal(t, model.TargetDir, notify.ParentDirTarget("a/b/c.txt").Kind)
	assert.Equal(t, "a/b", notify.ParentDirTarget("a/b/c.txt").Path)
	assert.Equal(t, "", notify.ParentDirTarget("c.txt").Path, "a file at the root has the root as its folder")
	assert.Equal(t, "x/y.txt", notify.FileTarget("qldemo://x/y.txt").Path, "a qualified path is not doubled up")
	assert.Equal(t, model.TargetNone, notify.ShareTarget("  ").Kind)
	// The gate is what decides uploaded vs updated; the target follows the
	// node either way.
	assert.Equal(t, notify.EventFileUpdated, writehook.Replaced.Event())
}
