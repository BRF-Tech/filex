package handlers_test

// The ops queue inside ONE tenant (or a single-tenant install): whose rows a
// person is shown.
//
// A queue row carries its sources and its destination — file and folder paths.
// The listing was scoped to the storages the caller's tenant reaches and to
// nothing finer, so every member of a tenant read every other member's rows,
// whatever folders their grants cover: on a production install (2026-09-26)
// the queue held a colleague's move into a folder of client records, and the
// listing handed it to every member of the tenant. `GET /ops/{id}` answered the
// full row for any id in the tenant, and the ids are sequential.

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// queuedOp reads the id of the op a submit answered.
func queuedOp(t *testing.T, body map[string]any) string {
	t.Helper()
	op, ok := body["op"].(map[string]any)
	require.True(t, ok, "no op in %v", body)
	return strconv.FormatInt(int64(op["id"].(float64)), 10)
}

// secondMember is another plain (role=user) member of alpha — or, with the
// mode off, of the one provider everybody lives in.
func secondMember(t *testing.T, f *mtFix) *http.Client {
	t.Helper()
	provider := f.ProvA
	if !f.MultiTenant {
		super, err := f.Store.GetSupertenant(context.Background())
		require.NoError(t, err)
		provider = super.ID
	}
	seedUserIn(t, f.Store, provider, "second@alpha.test")
	return mtLogin(t, f.Srv, "second@alpha.test", mtUserPass)
}

func TestOpsList_AMemberSeesOnlyTheOpsTheyQueued(t *testing.T) {
	for _, multi := range []bool{true, false} {
		name := "single-tenant"
		if multi {
			name = "multi-tenant"
		}
		t.Run(name, func(t *testing.T) {
			f := newMTFix(t, multi)
			second := secondMember(t, f)
			f.seedFile(t, f.StA, f.RootA, "danisan-kayitlari.txt", "the first member's file")
			f.seedFile(t, f.StA, f.RootA, "ortak.txt", "the second member's file")

			status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/delete", map[string]any{
				"source": []string{"alpha://danisan-kayitlari.txt"},
			})
			require.Equal(t, http.StatusAccepted, status, "%v", body)
			firstOp := queuedOp(t, body)

			status, body = doJSON(t, second, http.MethodPost, f.URL+"/api/files/copy", map[string]any{
				"source": []string{"alpha://ortak.txt"}, "target": "alpha://",
			})
			require.Equal(t, http.StatusAccepted, status, "%v", body)
			secondOp := queuedOp(t, body)

			// The second member's tray: their own row, nobody else's.
			status, raw := mtGet(t, second, f.URL+"/api/files/ops")
			require.Equal(t, http.StatusOK, status)
			require.NotContains(t, raw, "danisan-kayitlari.txt", "another member's row must not be in the listing")
			require.Contains(t, raw, "ortak.txt", "a member keeps their own row")

			// Nor by id, nor by cancelling it — and in the words a missing id
			// gets, so walking the sequential ids counts nothing.
			status, raw = mtGet(t, second, f.URL+"/api/files/ops/"+firstOp)
			require.Equal(t, http.StatusNotFound, status, raw)
			status, body = doJSON(t, second, http.MethodPost, f.URL+"/api/files/ops/"+firstOp+"/cancel", nil)
			require.Equal(t, http.StatusNotFound, status, "%v", body)

			status, raw = mtGet(t, second, f.URL+"/api/files/ops/"+secondOp)
			require.Equal(t, http.StatusOK, status, raw)
			status, raw = mtGet(t, f.A, f.URL+"/api/files/ops/"+firstOp)
			require.Equal(t, http.StatusOK, status, "the person who queued it still follows it: %s", raw)

			// An administrator keeps the whole view of what they administer.
			status, raw = mtGet(t, f.AdminA, f.URL+"/api/files/ops")
			require.Equal(t, http.StatusOK, status)
			require.Contains(t, raw, "danisan-kayitlari.txt")
			require.Contains(t, raw, "ortak.txt")
			status, raw = mtGet(t, f.AdminA, f.URL+"/api/files/ops/"+firstOp)
			require.Equal(t, http.StatusOK, status, raw)
		})
	}
}

// A row that names nobody — queued before actor_id existed, or by something
// that is not a person (a scheduled app job) — is nobody's to follow but an
// administrator's. It used to be everybody's, cancellable by anyone who saw it.
func TestOpsList_ARowNobodyQueuedIsAnAdministratorsOnly(t *testing.T) {
	f := newMTFix(t, true)
	res, err := f.SQL.ExecContext(context.Background(),
		`INSERT INTO pending_ops (kind, storage_id, dest_storage_id, sources_json, dest, total, status)
		 VALUES ('delete', ?, ?, '["sistem-isi.txt"]', '', 1, 'pending')`, f.StA.ID, f.StA.ID)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	row := strconv.FormatInt(id, 10)

	status, raw := mtGet(t, f.A, f.URL+"/api/files/ops")
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, raw, "sistem-isi.txt")
	status, raw = mtGet(t, f.A, f.URL+"/api/files/ops/"+row)
	require.Equal(t, http.StatusNotFound, status, raw)
	status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/ops/"+row+"/cancel", nil)
	require.Equal(t, http.StatusNotFound, status, "%v", body)

	status, raw = mtGet(t, f.AdminA, f.URL+"/api/files/ops")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, raw, "sistem-isi.txt")
	status, body = doJSON(t, f.AdminA, http.MethodPost, f.URL+"/api/files/ops/"+row+"/cancel", nil)
	require.Equal(t, http.StatusOK, status, "an administrator can still stop it: %v", body)
}
