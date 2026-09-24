package notify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// What an event about filex's own directories means to a person — measured at
// the one door every event goes through (Service.Send) and on the one read
// every surface uses (Service.List / UnreadCount). The rules are in
// personview.go; each case here is a row the owner actually saw on 2026-09-21.

// webhookSink records every body a webhook target receives.
type webhookSink struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (s *webhookSink) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		s.mu.Lock()
		s.bodies = append(s.bodies, m)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *webhookSink) all() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies...)
}

func newPersonViewService(t *testing.T) (notify.Service, *webhookSink, int64, interface {
	InsertNotification(context.Context, *model.NotificationInput) (int64, error)
}) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	st := newStorage(t, store, "docs")
	sink := &webhookSink{}
	srv := sink.server(t)
	svc := notify.New(store, notify.Config{WebhookURL: srv.URL, HTTPTimeout: 2 * time.Second, RetryBackoffs: []time.Duration{}})
	t.Cleanup(svc.Stop)
	return svc, sink, st.ID, store
}

func listAll(t *testing.T, svc notify.Service) []*model.Notification {
	t.Helper()
	rows, _, err := svc.List(context.Background(), nil, notify.AdminBell, false, 100, 0)
	require.NoError(t, err)
	return rows
}

// ⚠ The desktop's open-with round trip, event by event: placing the working
// copy, sweeping it, a version snapshot, anything inside the bin. None of it
// is something that happened to a person's file, so none of it is announced —
// not in the bell, not to a webhook.
func TestSend_BookkeepingInsideFilexDirectoriesIsNotAnnounced(t *testing.T) {
	svc, sink, sid, _ := newPersonViewService(t)
	ctx := context.Background()
	cases := []notify.Event{
		{Event: notify.EventFileUploaded, Body: "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx",
			Node: &notify.NodeRef{StorageID: sid, Path: "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx", Name: "a1b2c3d4e5f6-Bütçe Özeti.xlsx"}},
		{Event: notify.EventFileTrashed, Body: "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx",
			Meta:   map[string]any{"trash_path": ".filex-trash/1789-abc__a1b2c3d4e5f6-Bütçe Özeti.xlsx"},
			Node:   &notify.NodeRef{StorageID: sid, Path: "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx", Name: "a1b2c3d4e5f6-Bütçe Özeti.xlsx"},
			Target: notify.TrashTarget("/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx")},
		{Event: notify.EventFileUploaded, Body: ".versions/7/1",
			Node: &notify.NodeRef{StorageID: sid, Path: ".versions/7/1", Name: "1"}},
		{Event: notify.EventFileDeleted, Body: ".filex-trash/1789-abc__report.pdf",
			Node: &notify.NodeRef{StorageID: sid, Path: ".filex-trash/1789-abc__report.pdf", Name: "1789-abc__report.pdf"}},
		{Event: notify.EventFileUploaded, Body: "Photos/.keepdir",
			Node: &notify.NodeRef{StorageID: sid, Path: "Photos/.keepdir", Name: ".keepdir"}},
		// A folder the desktop never names like a working copy is still its.
		{Event: notify.EventFileUpdated, Body: ".filex-open/notes.txt",
			Node: &notify.NodeRef{StorageID: sid, Path: ".filex-open/notes.txt", Name: "notes.txt"}},
	}
	for _, e := range cases {
		e.Severity = notify.SeverityInfo
		id, err := svc.Send(ctx, e)
		require.NoError(t, err, "a silent drop is not an error for the caller")
		assert.Zero(t, id, "%s on %s was stored", e.Event, e.Node.Path)
	}
	svc.Wait()
	assert.Empty(t, listAll(t, svc), "no bell row for filex's own bookkeeping")
	assert.Empty(t, sink.all(), "no webhook delivery for filex's own bookkeeping")
}

// ⚠⚠ A save of the working copy IS news: it is the person's own document,
// edited. It is announced under the document's name, with no path (the
// original lives on their computer; no storage path names it) and nothing to
// click — and the same to a webhook.
func TestSend_OpenWithSaveNamesTheOriginalDocument(t *testing.T) {
	svc, sink, sid, _ := newPersonViewService(t)
	id, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventFileUpdated,
		Severity: notify.SeverityInfo,
		Body:     "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx",
		Meta:     map[string]any{"origin": "onlyoffice"},
		Node:     &notify.NodeRef{StorageID: sid, Path: "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx", Name: "a1b2c3d4e5f6-Bütçe Özeti.xlsx", Size: 42},
		Target:   notify.FileTarget("/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx"),
	})
	require.NoError(t, err)
	require.NotZero(t, id)
	svc.Wait()

	rows := listAll(t, svc)
	require.Len(t, rows, 1)
	row := rows[0]
	assert.Nil(t, row.Target, "nothing on the server is the original — the row must not be clickable")
	assert.Empty(t, row.Body, "the body was the working copy's path")
	var meta struct {
		Node     notify.NodeRef `json:"node"`
		OpenWith bool           `json:"open_with"`
		Origin   string         `json:"origin"`
	}
	require.NoError(t, json.Unmarshal(row.MetaJSON, &meta))
	assert.Equal(t, "Bütçe Özeti.xlsx", meta.Node.Name)
	assert.Empty(t, meta.Node.Path)
	assert.True(t, meta.OpenWith)
	assert.Equal(t, "onlyoffice", meta.Origin)
	assert.NotContains(t, string(row.MetaJSON), ".filex-open")

	hooks := sink.all()
	require.Len(t, hooks, 1)
	node, _ := hooks[0]["node"].(map[string]any)
	assert.Equal(t, "Bütçe Özeti.xlsx", node["name"])
	assert.Equal(t, "", node["path"], "webhook node path must be empty, not the working copy")
	assert.Equal(t, map[string]any{"kind": "none"}, hooks[0]["target"])
}

// An infected working copy is the person's own document carrying a virus: it
// is said, under the document's name, with nothing to open (the copy is in
// the bin, which is not a place to send anybody to).
func TestSend_InfectedWorkingCopyWarnsAboutTheOriginal(t *testing.T) {
	svc, _, sid, _ := newPersonViewService(t)
	_, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventFileInfected,
		Severity: notify.SeverityWarning,
		Title:    "Infected file detected",
		Body:     "/.filex-open/0123456789ab-Plan.docx: Eicar-Test-Signature",
		Meta:     map[string]any{"signature": "Eicar-Test-Signature", "quarantined": true, "trash_path": ".filex-trash/1-a__0123456789ab-Plan.docx"},
		Node:     &notify.NodeRef{StorageID: sid, Path: "/.filex-open/0123456789ab-Plan.docx", Name: "0123456789ab-Plan.docx"},
		Target:   notify.TrashTarget("/.filex-open/0123456789ab-Plan.docx"),
	})
	require.NoError(t, err)
	rows := listAll(t, svc)
	require.Len(t, rows, 1)
	assert.Nil(t, rows[0].Target)
	assert.NotContains(t, string(rows[0].MetaJSON), ".filex-")
	assert.NotContains(t, rows[0].Body, ".filex-")
	assert.Contains(t, string(rows[0].MetaJSON), `"name":"Plan.docx"`)
}

// Rows recorded BEFORE this release, read now: the old bookkeeping rows are
// hidden — from the list AND from the unread count, which is what the badge
// shows — and an old `file.trashed` row that targeted `.filex-trash/<key>`
// comes back addressed to the Trash view. Nothing in the table is changed.
func TestList_SanitisesRowsRecordedBeforeTheGate(t *testing.T) {
	svc, _, _, store := newPersonViewService(t)
	ctx := context.Background()
	insert := func(event, body, meta string) int64 {
		id, err := store.InsertNotification(ctx, &model.NotificationInput{
			Event: event, Severity: "info", Title: event, Body: body, MetaJSON: json.RawMessage(meta),
		})
		require.NoError(t, err)
		return id
	}
	// The exact shapes the baseline wrote (measured, repro 2026-09-21).
	insert("file.uploaded", "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx",
		`{"node":{"storage_id":1,"path":"/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx","name":"a1b2c3d4e5f6-Bütçe Özeti.xlsx"},"origin":"manager","target":{"kind":"file","storage":"docs","path":".filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx"}}`)
	insert("file.updated", "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx",
		`{"node":{"storage_id":1,"path":"/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx","name":"a1b2c3d4e5f6-Bütçe Özeti.xlsx"},"origin":"manager","target":{"kind":"file","storage":"docs","path":".filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx"}}`)
	insert("file.trashed", "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx",
		`{"node":{"storage_id":1,"path":"/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx","name":"a1b2c3d4e5f6-Bütçe Özeti.xlsx"},"origin":"manager","target":{"kind":"file","storage":"docs","path":".filex-trash/1789998390-28a4ce__a1b2c3d4e5f6-Bütçe Özeti.xlsx"},"trash_path":"/.filex-trash/1789998390-28a4ce__a1b2c3d4e5f6-Bütçe Özeti.xlsx"}`)
	trashed := insert("file.trashed", "/Documents/report.txt",
		`{"node":{"storage_id":1,"path":"/Documents/report.txt","name":"report.txt"},"origin":"manager","target":{"kind":"file","storage":"docs","path":".filex-trash/1789998389-6c7d18__report.txt"},"trash_path":"/.filex-trash/1789998389-6c7d18__report.txt"}`)
	uploaded := insert("file.uploaded", "/Documents/report.txt",
		`{"node":{"storage_id":1,"path":"/Documents/report.txt","name":"report.txt"},"origin":"manager","target":{"kind":"file","storage":"docs","path":"Documents/report.txt"}}`)

	rows, total, err := svc.List(ctx, nil, notify.AdminBell, false, 50, 0)
	require.NoError(t, err)
	ids := map[int64]*model.Notification{}
	for _, r := range rows {
		ids[r.ID] = r
		assert.NotContains(t, string(r.MetaJSON), ".filex-", "row %d still names an internal path", r.ID)
		assert.NotContains(t, r.Body, ".filex-")
	}
	assert.Len(t, rows, 2, "the three working-copy rows are hidden")
	assert.EqualValues(t, 2, total, "the total counts what is shown")
	require.Contains(t, ids, trashed)
	require.Contains(t, ids, uploaded)
	tgt := ids[trashed].Target
	require.NotNil(t, tgt)
	assert.Equal(t, model.TargetTrash, tgt.Kind)
	assert.Equal(t, "docs", tgt.Storage)
	assert.Equal(t, "Documents/report.txt", tgt.Path)

	unread, err := svc.UnreadCount(ctx, nil, notify.AdminBell)
	require.NoError(t, err)
	assert.EqualValues(t, 2, unread, "the badge counts what the bell shows")

	// History is untouched: the stored row still says what it said.
	raw, err := store.(interface {
		GetNotification(context.Context, int64) (*model.Notification, error)
	}).GetNotification(ctx, trashed)
	require.NoError(t, err)
	assert.Contains(t, string(raw.MetaJSON), ".filex-trash/1789998389-6c7d18__report.txt")
}

// The trash target on the wire — what every click surface and every webhook
// receiver switches on.
func TestSend_TrashedWebhookCarriesTheTrashTarget(t *testing.T) {
	svc, sink, sid, _ := newPersonViewService(t)
	_, err := svc.Send(context.Background(), notify.Event{
		Event: notify.EventFileTrashed, Severity: notify.SeverityInfo, Body: "Documents/report.txt",
		Meta:   map[string]any{"trash_path": ".filex-trash/1-a__report.txt"},
		Node:   &notify.NodeRef{StorageID: sid, Path: "Documents/report.txt", Name: "report.txt"},
		Target: notify.TrashTarget("Documents/report.txt"),
	})
	require.NoError(t, err)
	svc.Wait()
	hooks := sink.all()
	require.Len(t, hooks, 1)
	assert.Equal(t, map[string]any{"kind": "trash", "storage": "docs", "path": "Documents/report.txt"}, hooks[0]["target"])
	meta, _ := hooks[0]["meta"].(map[string]any)
	assert.Equal(t, ".filex-trash/1-a__report.txt", meta["trash_path"],
		"a webhook receiver keeps the documented trash_path; only the bell drops it")

	rows := listAll(t, svc)
	require.Len(t, rows, 1)
	assert.NotContains(t, string(rows[0].MetaJSON), "trash_path")
}
