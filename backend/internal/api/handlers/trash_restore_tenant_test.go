package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A trash entry is its tenant's to restore, one at a time or as a queued
// batch — the way Purge has always asked.
//
// RED PROOF (v0.46.0 and before, 2026-09-26): a member of `alpha` posting
// bravo's entry id to POST /api/files/manager/restore got 200 and bravo's
// file was back out of the trash; with `queued=1` and a list of ids the
// batch was queued (202) and the worker restored them. mayRestore asked
// about locks, the root and the ACL — which is tenant-blind, and answers
// editor for anybody on a storage without access control — and never whose
// storage the entry was in.
func TestTrashRestore_AnotherTenantsEntryIsNotThere(t *testing.T) {
	for _, queued := range []bool{false, true} {
		name := "one entry"
		if queued {
			name = "a queued batch"
		}
		t.Run(name, func(t *testing.T) {
			f := newMTFix(t, true)
			ctx := context.Background()
			n := f.seedFile(t, f.StB, f.RootB, "gizli.txt", "bravo's private file")
			status, body := doJSON(t, f.B, http.MethodPost, f.URL+"/api/files/manager?action=delete", map[string]any{
				"path": "bravo://", "items": []map[string]string{{"path": "bravo://gizli.txt"}},
			})
			require.Equal(t, http.StatusOK, status, "%v", body)

			url, req := f.URL+"/api/files/manager/restore", map[string]any{"node_id": n.ID}
			if queued {
				url, req = url+"?queued=1", map[string]any{"node_ids": []int64{n.ID}}
			}
			status, body = doJSON(t, f.A, http.MethodPost, url, req)
			assert.Equal(t, http.StatusNotFound, status, "another tenant's entry was restorable: %v", body)
			assert.Equal(t, "trash entry not found", body["error"], "the refusal must read like a missing entry")

			f.drainOps(t)
			row, err := f.Store.GetNode(ctx, n.ID)
			require.NoError(t, err)
			assert.NotNil(t, row.DeletedAt, "bravo's file is out of the trash")

			// Its own tenant still restores it.
			status, body = doJSON(t, f.B, http.MethodPost, f.URL+"/api/files/manager/restore", map[string]any{"node_id": n.ID})
			require.Equal(t, http.StatusOK, status, "%v", body)
		})
	}
}
