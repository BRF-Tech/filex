package handlers_test

// Office thumbnails drawn by OnlyOffice (0.50, thumb/office.go) on the
// Thumbnail repair tab: the two settings (size, slots) beside the SVG limits,
// and the document server's answers as reasons the tab says in words, with
// the marker the explorer shows on the file. And on the listing: the marker
// itself (`thumb_note`), only for a reason that is the file's.

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestThumbRepair_OfficeSettings(t *testing.T) {
	rep := &recordingRepairer{}
	srv, client, store := repairServer(t, false, rep)
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)

	status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/tools/thumbnails/settings", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, float64(25), body["office_max_mb"], "25 MB by default")
	assert.Equal(t, float64(1), body["office_slots"], "one document at a time by default")
	assert.Equal(t, float64(4), body["office_slots_max"])

	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/tools/thumbnails/settings",
		map[string]any{"office_max_mb": 40, "office_slots": 2})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, float64(40), body["office_max_mb"])
	assert.Equal(t, float64(2), body["office_slots"])

	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/tools/thumbnails/settings",
		map[string]any{"office_slots": 5})
	assert.Equal(t, http.StatusBadRequest, status, "at most 4")
	assert.Equal(t, "OUT_OF_RANGE", body["code"])
	_, body = doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/tools/thumbnails/settings", nil)
	assert.Equal(t, float64(2), body["office_slots"], "a refused value changes nothing")
	assert.Equal(t, float64(5), body["svg_max_mb"], "the SVG limits are left alone")
}

func TestThumbRepair_OfficeReasons(t *testing.T) {
	ctx := context.Background()
	rep := &recordingRepairer{}
	srv, client, store := repairServer(t, true, rep)
	mine := seedFullTenant(t, store, "globex")
	testutil.LoginAs(t, srv, client, mine.adminEmail, mine.adminPass)
	at := time.Now()
	mark := func(p, state, reason string) {
		n := seedNodeIn(t, store, mine.storage.ID, p)
		require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{NodeID: n.ID, State: state, Error: reason, AttemptedAt: &at}))
	}
	mark("/bozuk.docx", "failed", "oo_corrupt:ds-3")
	mark("/parolali.xlsx", "skipped", "oo_password")
	mark("/buyuk.pptx", "skipped", "oo_too_large:26214400")
	mark("/sinirsiz.pptx", "skipped", "oo_too_large:0")
	mark("/bekleyen.docx", "failed", "oo_retry:3:ds-4")
	mark("/yok.docx", "skipped", "no_tool:office")

	status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/tools/thumbnails/problems", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	byPath := map[string]map[string]any{}
	for _, raw := range body["items"].([]any) {
		it := raw.(map[string]any)
		byPath[it["path"].(string)] = it
	}
	q := func(p string) map[string]any {
		it := byPath[mine.storage.Name+"://"+p]
		require.NotNil(t, it, "%s in %v", p, byPath)
		return it
	}
	assert.Equal(t, "oo_corrupt", q("bozuk.docx")["code"])
	assert.Equal(t, "ds-3", q("bozuk.docx")["detail"])
	assert.Equal(t, "corrupt", q("bozuk.docx")["note"])
	assert.Equal(t, "oo_password", q("parolali.xlsx")["code"])
	assert.Equal(t, "encrypted", q("parolali.xlsx")["note"])
	assert.Equal(t, "oo_too_large", q("buyuk.pptx")["code"])
	assert.Equal(t, float64(26214400), q("buyuk.pptx")["limit"])
	assert.Equal(t, "too_large", q("buyuk.pptx")["note"])
	assert.Nil(t, q("sinirsiz.pptx")["limit"], "the document server's own limit: 0, not shown")
	assert.Equal(t, "oo_retry", q("bekleyen.docx")["code"])
	assert.Equal(t, float64(3), q("bekleyen.docx")["tries"])
	assert.Equal(t, "ds-4", q("bekleyen.docx")["detail"])
	assert.Nil(t, q("bekleyen.docx")["note"], "a failure that may pass is not marked")
	assert.Equal(t, "no_tool", q("yok.docx")["code"])
	assert.Equal(t, "office", q("yok.docx")["tool"])
	assert.Nil(t, q("yok.docx")["note"])
}
