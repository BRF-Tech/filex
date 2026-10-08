package handlers_test

// The trash says its own numbers and its own words (0.54, findings D1, D2,
// A4, A15).
//
// RED PROOF, D1: the explorer's "Empty the trash?" confirmation counted the
// rows it had loaded - GET /api/files/manager/trash answers 50 by default -
// and the purge then took every entry the caller reaches. "This permanently
// deletes 50 items (2 KB)" stood over a delete of 70. The listing now carries
// `total_bytes` (every entry, not the page) and the confirmation asks
// GET /api/admin/trash/empty/preview, which counts with the purge's own tally:
// the number it names is the number the purge deletes.
//
// RED PROOF, D2: the virtual `.trash` row of a storage asked
// `?storage=<name>`, which the handler did not read - it summed the newest 50
// of EVERY storage. An unknown name now lists nothing.
//
// RED PROOF, A15: a restore or a permanent delete of a selection was one
// request per entry, tallied and worded in the browser. One request now
// answers `{done, failed, reason_code, summary}`.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
)

// bigTrash is n files of 42 bytes each on one local storage ("main"),
// deleted through the manager into the real trash (`.filex-trash/`), with the
// trash handler and the queue wired the way routes.go wires them.
func bigTrash(t *testing.T, n int) (*handlers.Trash, db.Store, *model.Storage) {
	t.Helper()
	q := newQueueRig(t)
	q.svc.SetTrashEmptier(q.th.Service)
	q.th.EmptyWait = 10 * time.Second
	body := strings.Repeat("x", 42)
	rels := make([]string, 0, n)
	for i := 0; i < n; i++ {
		rel := fmt.Sprintf("copy-%03d.bin", i)
		q.file(t, rel, body)
		rels = append(rels, rel)
	}
	q.trashed(t, rels...)
	return q.th, q.store, q.st
}

func trashGetJSON(t *testing.T, h http.HandlerFunc, target string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, target, nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func postBody(t *testing.T, h http.HandlerFunc, target string, body any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return rec.Code, out
}

// The listing's totals are every entry's, not the page's: 70 entries of 42
// bytes, asked for 25 of them.
func TestTrashList_TotalsCountEveryEntryNotThePage(t *testing.T) {
	h, _, st := bigTrash(t, 70)

	body := trashGetJSON(t, h.List, "/api/files/manager/trash?limit=25&lang=en")
	assert.Len(t, body["entries"], 25)
	assert.Equal(t, float64(70), body["total"])
	assert.Equal(t, float64(70*42), body["total_bytes"], "the size of the whole trash, not of the 25 rows on the page")
	assert.NotNil(t, body["newest_deleted_at"])
	assert.Equal(t, "70 items in the trash, 2.94 KB in all", body["summary"])

	storages, ok := body["storages"].([]any)
	require.True(t, ok, "the per-storage summary is missing: %v", body["storages"])
	require.Len(t, storages, 1)
	one := storages[0].(map[string]any)
	assert.Equal(t, float64(st.ID), one["storage_id"])
	assert.Equal(t, float64(70), one["count"])
	assert.Equal(t, float64(70*42), one["bytes"])
	assert.NotNil(t, one["newest_deleted_at"])

	tr := trashGetJSON(t, h.List, "/api/files/manager/trash?limit=1&lang=tr")
	assert.Equal(t, "Çöp kutusunda 70 öğe var, toplam 2,94 KB", tr["summary"])
}

// `?storage=<name>` is read (the virtual `.trash` row of a storage asks it):
// its own storage's entries, and nothing at all for a name no storage has.
func TestTrashList_StorageByNameIsThatStorageOnly(t *testing.T) {
	h, _, _ := bigTrash(t, 3)

	mine := trashGetJSON(t, h.List, "/api/files/manager/trash?storage=main")
	assert.Equal(t, float64(3), mine["total"])
	assert.Equal(t, float64(3*42), mine["total_bytes"])

	none := trashGetJSON(t, h.List, "/api/files/manager/trash?storage=nowhere")
	assert.Equal(t, float64(0), none["total"], "an unknown storage listed every storage's trash")
	assert.Empty(t, none["entries"])
	assert.Equal(t, float64(0), none["total_bytes"])
}

// The dry run counts what the purge then deletes - with the purge's own tally
// over the caller's own reach - and deletes nothing.
func TestTrashEmptyPreview_CountsWhatTheEmptyThenDeletes(t *testing.T) {
	h, store, _ := bigTrash(t, 70)

	preview := trashGetJSON(t, h.EmptyPreview, "/api/admin/trash/empty/preview?lang=en")
	assert.Equal(t, true, preview["dry_run"])
	assert.Equal(t, float64(70), preview["count"], "the confirmation must name every entry the purge takes, not a page")
	assert.Equal(t, float64(70*42), preview["bytes"])
	assert.Equal(t, "This permanently deletes 70 items (2.94 KB). It cannot be undone.", preview["summary"])

	// Nothing was deleted by asking.
	_, stored, err := store.ListTrashed(context.Background(), nil, 1, 0)
	require.NoError(t, err)
	assert.Equal(t, 70, stored, "the dry run deleted something")

	code, done := postBody(t, h.AdminEmpty, "/api/admin/trash/empty?lang=en", map[string]any{})
	require.Equal(t, http.StatusOK, code, "%v", done)
	assert.Equal(t, preview["count"], done["purged"], "the dry run's count is what the purge deleted")
	assert.Equal(t, preview["count"], done["total"])
	assert.Equal(t, float64(0), done["failed"])
	assert.NotContains(t, done["summary"], "server log")
	assert.Contains(t, done["summary"], "70 items deleted for good")

	_, stored, err = store.ListTrashed(context.Background(), nil, 1, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, stored)

	after := trashGetJSON(t, h.EmptyPreview, "/api/admin/trash/empty/preview?lang=tr")
	assert.Equal(t, float64(0), after["count"])
	assert.Equal(t, "Silinecek bir şey yok: çöp kutusu boş.", after["summary"])
}

// The preview reads its narrowing like the empty does: what it cannot read is
// refused, never widened to "everything".
func TestTrashEmptyPreview_ABadNarrowingIsRefused(t *testing.T) {
	h, _, _ := bigTrash(t, 1)
	rec := httptest.NewRecorder()
	h.EmptyPreview(rec, httptest.NewRequest(http.MethodGet, "/api/admin/trash/empty/preview?older_than_days=-1", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// One restore request for a selection says how many came back, how many did
// not and why, in the reader's language.
func TestRestoreBatch_SaysWhatCameBackAndWhyTheRestDidNot(t *testing.T) {
	f := newRestoreFixture(t, false)
	a := f.seedFile(t, "a.txt", "a")
	b := f.seedFile(t, "b.txt", "b")
	c := f.seedFile(t, "c.txt", "c")
	trashNow(t, f, a)
	trashNow(t, f, b)
	trashNow(t, f, c)
	// Somebody wrote a new b.txt in the meantime.
	f.seedFile(t, "b.txt", "the b that is there now")

	rec := f.postJSON(t, f.trashH.Restore, map[string]any{"node_ids": []int64{a.ID, b.ID, c.ID}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Done       int      `json:"done"`
		Failed     int      `json:"failed"`
		ReasonCode string   `json:"reason_code"`
		Taken      []string `json:"taken"`
		Summary    string   `json:"summary"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 2, body.Done)
	assert.Equal(t, 1, body.Failed)
	assert.Equal(t, ops.ReasonExists, body.ReasonCode)
	assert.Equal(t, []string{"b.txt"}, body.Taken)
	assert.Equal(t, "2 items restored - 1 item was not restored: something already has the name “b.txt”", body.Summary)
	assert.Equal(t, "the b that is there now", f.read(t, "b.txt"), "the occupant was overwritten")
	f.stillTrashed(t, b.ID, "/b.txt")
	assert.Equal(t, "a", f.read(t, "a.txt"))
	assert.Equal(t, "c", f.read(t, "c.txt"))
}

// An id that is not in the trash is counted, not fatal, and the batch says
// why in Turkish when the screen is Turkish.
func TestRestoreBatch_AMissingEntryIsCountedInTheReadersLanguage(t *testing.T) {
	f := newRestoreFixture(t, false)
	a := f.seedFile(t, "a.txt", "a")
	trashNow(t, f, a)

	raw, err := json.Marshal(map[string]any{"node_ids": []int64{a.ID, 987654}})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/files/manager/restore?lang=tr", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	f.trashH.Restore(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, float64(1), body["done"])
	assert.Equal(t, float64(1), body["failed"])
	assert.Equal(t, ops.ReasonNotFound, body["reason_code"])
	assert.Equal(t, "1 öğe geri getirildi - 1 öğe geri getirilmedi: artık çöp kutusunda değil", body["summary"])
}

// A permanent delete of a selection is one request, with the count and the
// reason said by the server; queued, one job for the storage.
func TestPurgeBatch_OneRequestSaysTheCount(t *testing.T) {
	h, store, _ := bigTrash(t, 3)
	ctx := context.Background()
	rows, _, err := store.ListTrashed(ctx, nil, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	ids := []int64{rows[0].ID, rows[1].ID, 987654}

	code, body := postBody(t, h.PurgeBatch, "/api/admin/trash/purge?lang=en", map[string]any{"node_ids": ids})
	require.Equal(t, http.StatusOK, code, "%v", body)
	assert.Equal(t, float64(2), body["done"])
	assert.Equal(t, float64(1), body["failed"])
	assert.Equal(t, ops.ReasonNotFound, body["reason_code"])
	assert.Equal(t, "2 items deleted permanently - 1 item was not deleted: it is no longer in the trash", body["summary"])
	_, stored, err := store.ListTrashed(ctx, nil, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, stored)

	code, body = postBody(t, h.PurgeBatch, "/api/admin/trash/purge?queued=1&lang=en", map[string]any{"node_ids": []int64{rows[2].ID}})
	require.Equal(t, http.StatusAccepted, code, "%v", body)
	assert.Len(t, body["ops"], 1, "one job for the storage")
	assert.Equal(t, float64(1), body["done"])
	assert.Equal(t, "Deleting 1 item permanently…", body["summary"])
}

// The status of a finished empty is said, and never sends a person to the
// server log.
func TestTrashEmptyStatus_CarriesTheServersSentence(t *testing.T) {
	h, _, _ := bigTrash(t, 2)
	code, done := postBody(t, h.AdminEmpty, "/api/admin/trash/empty", map[string]any{})
	require.Equal(t, http.StatusOK, code, "%v", done)

	st := trashGetJSON(t, h.EmptyStatus, "/api/admin/trash/empty?lang=tr")
	assert.Equal(t, false, st["running"])
	summary, _ := st["summary"].(string)
	assert.True(t, strings.HasPrefix(summary, "Çöp kutusu boşaltıldı: 2 öğe kalıcı olarak silindi"), summary)
	assert.NotContains(t, strings.ToLower(summary), "log")
}
