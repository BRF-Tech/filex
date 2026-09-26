package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

// A member's trash listing counts, and pages through, the entries the member
// may see — not the store's rows around them.
//
// RED PROOF (lesson #413, 2026-09-26): the rows were paged in SQL and judged
// afterwards, and `total` was set to the page's length after the judging. A
// member with a grant on Ekip, whose 30 deletes are interleaved with 30 in
// Gizli, got 12 entries and total 12 on the first page of 25 — no pager — and
// the second page (offset 25) started 25 rows into the STORE's order: most of
// Ekip's entries were never reachable.
func TestTrashList_AMembersPagesReachEveryEntryTheyMaySee(t *testing.T) {
	q := newQueueRig(t)
	q.th.AttachACL(acl.New(q.store))
	q.dir(t, "Ekip")
	q.dir(t, "Gizli")
	var rels []string
	for i := 0; i < 30; i++ {
		for _, dir := range []string{"Ekip", "Gizli"} {
			rel := fmt.Sprintf("%s/%02d.txt", dir, i)
			q.file(t, rel, "x")
			rels = append(rels, rel)
		}
	}
	q.trashed(t, rels...)
	member := seedSharedUser(t, q.store, "uye@filex.test", "TestUserPass!1")
	grant(t, q.store, q.st, member, "Ekip", model.GrantEditor, true)

	page := func(offset int) (ids []float64, total int) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/files/manager/trash?limit=25&offset=%d", offset), nil)
		rec := httptest.NewRecorder()
		q.th.List(rec, req.WithContext(auth.WithUser(context.Background(), member)))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body struct {
			Entries []map[string]any `json:"entries"`
			Total   int              `json:"total"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		for _, e := range body.Entries {
			assert.Contains(t, e["path"], "/Ekip/", "an entry the member may not see was listed")
			ids = append(ids, e["id"].(float64))
		}
		return ids, body.Total
	}

	first, total1 := page(0)
	second, total2 := page(25)
	assert.Equal(t, 30, total1, "the count must be every entry the member may see")
	assert.Equal(t, 30, total2)
	assert.Len(t, first, 25)
	assert.Len(t, second, 5)
	seen := map[float64]int{}
	for _, id := range append(first, second...) {
		seen[id]++
	}
	assert.Len(t, seen, 30, "every entry once across the two pages")
	for id, n := range seen {
		assert.Equal(t, 1, n, "entry %v came twice", id)
	}
}
