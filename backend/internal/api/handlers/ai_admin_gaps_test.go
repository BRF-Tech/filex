package handlers_test

// The admin MCP tools' gaps since 0.43 (task #115), measured 2026-09-28 on
// v0.48.1: admin_storages_sync refused `path` ("unexpected additional
// properties"), admin_trash_restore / admin_trash_purge could not be queued,
// webhook targets, the replica fixes, a storage's sync runs and drift, the
// storage order, app locks and the protection / archive settings had no tool.
// Each test drives the tool and asserts what it DID; on the old code the tool
// or its argument does not exist and the call is a JSON-RPC error.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// adminTool calls one admin_* tool and unpacks its {status, result}.
func adminTool(t *testing.T, base, tok, name string, args any) (e2eAITool, int, map[string]any) {
	t.Helper()
	tl := doorCall(t, base, tok, name, args)
	status, res := doorResult(t, tl)
	return tl, status, res
}

// TestAIAdminGaps_StorageSyncRescansOneFolder - admin_storages_sync takes the
// folder REST's `?path=` takes: only that folder is rescanned.
func TestAIAdminGaps_StorageSyncRescansOneFolder(t *testing.T) {
	f := newRescanFixture(t, "local")
	f.write(t, "docs/a.txt", "a")
	f.write(t, "other/x.txt", "x")
	require.NoError(t, f.worker.Trigger(context.Background(), f.st.ID))
	f.write(t, "docs/new.txt", "new")
	f.write(t, "other/new.txt", "new")

	admin, err := f.store.GetUserByEmail(context.Background(), "admin@test.local")
	require.NoError(t, err)
	tok := testutil.NewAPIToken(t, f.store, admin.ID, "mcp,admin")

	tl, status, res := adminTool(t, f.srv, tok, "admin_storages_sync", map[string]any{"id": f.st.ID, "path": "docs"})
	require.False(t, tl.IsError, tl.Text)
	assert.Equal(t, http.StatusOK, status, "%v", res)
	assert.Equal(t, "/docs", res["path"], "the folder asked for was rescanned, not the storage")
	assert.NotNil(t, f.row(t, "/docs/new.txt"))
	assert.Nil(t, f.row(t, "/other/new.txt"), "only the folder asked for is rescanned")

	// sync_runs and drift: the storage's own, REST already had them.
	tl, status, _ = adminTool(t, f.srv, tok, "admin_storages_sync_runs", map[string]any{"id": f.st.ID})
	require.False(t, tl.IsError, tl.Text)
	assert.Equal(t, http.StatusOK, status)
	tl, status, _ = adminTool(t, f.srv, tok, "admin_storages_drift", map[string]any{"id": f.st.ID})
	require.False(t, tl.IsError, tl.Text)
	assert.Equal(t, http.StatusOK, status)
}

// TestAIAdminGaps_TrashRestoreAndPurgeQueue - with queued the admin trash
// tools run as jobs of the operations queue, as the panel's Trash page asks.
func TestAIAdminGaps_TrashRestoreAndPurgeQueue(t *testing.T) {
	f := newDoorFix(t)
	adm := testutil.NewAPIToken(t, f.Store, f.AdminID, "read,write,mcp,admin")
	f.put(t, f.Tok, "main://docs/geri.txt", "geri")
	f.put(t, f.Tok, "main://docs/yok.txt", "yok")
	require.False(t, f.tool(t, f.Tok, "file_delete", map[string]any{"path": "main://docs/geri.txt"}).IsError)
	require.False(t, f.tool(t, f.Tok, "file_delete", map[string]any{"path": "main://docs/yok.txt"}).IsError)

	ids := map[string]float64{}
	tl, _, res := adminTool(t, f.URL, adm, "admin_trash_list", map[string]any{})
	require.False(t, tl.IsError, tl.Text)
	for _, e := range res["entries"].([]any) {
		m := e.(map[string]any)
		ids[m["name"].(string)], _ = m["id"].(float64)
	}
	require.NotZero(t, ids["geri.txt"], "%v", res)
	require.NotZero(t, ids["yok.txt"], "%v", res)

	tl, status, res := adminTool(t, f.URL, adm, "admin_trash_restore", map[string]any{
		"body": map[string]any{"node_ids": []float64{ids["geri.txt"]}}, "queued": true,
	})
	require.False(t, tl.IsError, tl.Text)
	require.Equal(t, http.StatusAccepted, status, "queued: a job, not a synchronous restore: %v", res)
	queued := res["ops"].([]any)
	require.Len(t, queued, 1)
	op := f.waitOp(t, adm, int64(queued[0].(map[string]any)["id"].(float64)))
	require.Equal(t, "ok", op["status"], "%v", op)
	got, ok := f.read(t, f.RootMain, "docs/geri.txt")
	require.True(t, ok)
	assert.Equal(t, "geri", got)

	tl, status, res = adminTool(t, f.URL, adm, "admin_trash_purge", map[string]any{"id": ids["yok.txt"], "queued": true})
	require.False(t, tl.IsError, tl.Text)
	require.Equal(t, http.StatusAccepted, status, "%v", res)
	op = f.waitOp(t, adm, opIDOf(t, res))
	require.Equal(t, "ok", op["status"], "%v", op)
	tl, _, _ = adminTool(t, f.URL, adm, "admin_trash_list", map[string]any{})
	assert.NotContains(t, string(tl.Structured), "yok.txt", "the purged entry is gone from the trash")
}

// TestAIAdminGaps_WebhookTargets - the v2 webhook targets: list, add, change,
// test and delete, through the panel's handler; a secret is never read back.
func TestAIAdminGaps_WebhookTargets(t *testing.T) {
	f := newDoorFix(t)
	adm := testutil.NewAPIToken(t, f.Store, f.AdminID, "mcp,admin")

	tl, status, res := adminTool(t, f.URL, adm, "admin_webhooks_create", map[string]any{"body": map[string]any{
		"name": "ops", "url": "https://hooks.example.test/filex", "secret": "s3cr3t-do-not-echo", "events": []string{"file.uploaded"},
	}})
	require.False(t, tl.IsError, tl.Text)
	assert.Less(t, status, 300)
	id, _ := res["id"].(float64)
	require.NotZero(t, id, "%v", res)
	assert.NotContains(t, tl.Raw, "s3cr3t-do-not-echo", "a webhook secret is never answered")

	tl, _, _ = adminTool(t, f.URL, adm, "admin_webhooks_list", map[string]any{})
	require.False(t, tl.IsError, tl.Text)
	assert.Contains(t, string(tl.Structured), "https://hooks.example.test/filex")
	assert.NotContains(t, tl.Raw, "s3cr3t-do-not-echo")

	tl, _, res = adminTool(t, f.URL, adm, "admin_webhooks_update", map[string]any{"id": id, "body": map[string]any{"enabled": false}})
	require.False(t, tl.IsError, tl.Text)
	assert.Equal(t, false, res["enabled"], "%v", res)

	tl, status, _ = adminTool(t, f.URL, adm, "admin_webhooks_delete", map[string]any{"id": id})
	require.False(t, tl.IsError, tl.Text)
	assert.Less(t, status, 300)
	tl, _, _ = adminTool(t, f.URL, adm, "admin_webhooks_list", map[string]any{})
	assert.NotContains(t, string(tl.Structured), "hooks.example.test", "deleted")

	// The REST twin is there too, for an admin key.
	code, _ := f.rest(t, adm, http.MethodGet, "/api/ai/admin/webhooks", nil)
	assert.Equal(t, http.StatusOK, code)
}

// TestAIAdminGaps_ProtectionIsValidated - admin_protection_update refuses a
// value out of bounds, which admin_settings_set's raw key would have stored.
func TestAIAdminGaps_ProtectionIsValidated(t *testing.T) {
	f := newDoorFix(t)
	adm := testutil.NewAPIToken(t, f.Store, f.AdminID, "mcp,admin")

	tl := doorCall(t, f.URL, adm, "admin_protection_update", map[string]any{"body": map[string]any{"trash_retention_days": -5}})
	require.True(t, tl.IsError, "out of bounds is refused: %s", tl.Raw)
	assert.Contains(t, tl.Text, "HTTP 400")
	assert.Contains(t, tl.Text, "trash_retention_days")

	tl, _, _ = adminTool(t, f.URL, adm, "admin_protection_update", map[string]any{"body": map[string]any{"trash_retention_days": 7}})
	require.False(t, tl.IsError, tl.Text)
	tl, _, res := adminTool(t, f.URL, adm, "admin_protection_get", map[string]any{})
	require.False(t, tl.IsError, tl.Text)
	assert.EqualValues(t, 7, res["trash_retention_days"], "%v", res)

	// The archive settings answer through the panel's handler too.
	tl, status, _ := adminTool(t, f.URL, adm, "admin_archives_get", map[string]any{})
	require.False(t, tl.IsError, tl.Text)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(tl.Structured), "allowed_formats")
}

// TestAIAdminGaps_ReplicaAndStorageOrder - the replica count and fixes, and
// the storage order, are tools now (REST had the replica routes already).
func TestAIAdminGaps_ReplicaAndStorageOrder(t *testing.T) {
	f := newDoorFix(t)
	adm := testutil.NewAPIToken(t, f.Store, f.AdminID, "mcp,admin")

	tl, status, res := adminTool(t, f.URL, adm, "admin_replica_failures_count", map[string]any{})
	require.False(t, tl.IsError, tl.Text)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, res, "count")
	// No replica service in this fixture: the tool exists and says so.
	tl = doorCall(t, f.URL, adm, "admin_replica_fix", map[string]any{})
	require.True(t, tl.IsError, tl.Raw)
	assert.Contains(t, tl.Text, "503")

	tl, status, res = adminTool(t, f.URL, adm, "admin_storages_order", map[string]any{"ids": []int64{f.Yan.ID, f.Main.ID}})
	require.False(t, tl.IsError, tl.Text)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, []any{float64(f.Yan.ID), float64(f.Main.ID)}, res["ids"], "the order was taken: %v", res)
	// An id that is not a storage is refused by the panel's own check.
	tl = doorCall(t, f.URL, adm, "admin_storages_order", map[string]any{"ids": []int64{f.Main.ID, 999999}})
	require.True(t, tl.IsError, tl.Raw)
	assert.True(t, strings.Contains(tl.Text, "unknown storage id"), tl.Text)
}

// TestAIAdminGaps_AppLocksAndOneUnlockRow - an admin key lists the files apps
// froze and lifts one, and the unlock is ONE audit row named for what it did:
// app_plugin.unlock (through the AI door, ai.app_plugin.unlock). The panel's
// unlock wrote two - its generic app-plugins.delete and the hook's row.
func TestAIAdminGaps_AppLocksAndOneUnlockRow(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/nda.txt", "draft")
	f.writeFile(t, "docs/sozlesme.txt", "draft")
	op := f.runAndDrain(t, f.admin, "lock", map[string]any{"paths": []string{"main://docs/nda.txt"}, "params": map[string]any{}})
	require.Equal(t, "ok", op["status"], op)
	op = f.runAndDrain(t, f.admin, "lock", map[string]any{"paths": []string{"main://docs/sozlesme.txt"}, "params": map[string]any{}})
	require.Equal(t, "ok", op["status"], op)
	tok := testutil.NewAPIToken(t, f.store, f.adminID(t), "mcp,admin")

	tl, _, _ := adminTool(t, f.srv.URL, tok, "admin_app_locks_list", map[string]any{})
	require.False(t, tl.IsError, tl.Text)
	assert.Contains(t, string(tl.Structured), "docs/nda.txt")

	before := len(mcpAuditRows(t, f.store))
	tl, _, _ = adminTool(t, f.srv.URL, tok, "admin_app_unlock", map[string]any{"storage_id": f.st.ID, "path": "docs/nda.txt"})
	require.False(t, tl.IsError, tl.Text)
	rows := newRows(t, f.store, before)
	require.Len(t, rows, 1, "one unlock, one row: %+v", rows)
	assert.Equal(t, "ai.app_plugin.unlock", rows[0].Action)
	assert.Equal(t, "docs/nda.txt", rows[0].TargetID)
	assert.Equal(t, "mcp", rows[0].Metadata["via"])

	// The panel's own unlock: one row too, no generic app-plugins.delete.
	before = len(mcpAuditRows(t, f.store))
	status, raw := doReq(t, f.admin, http.MethodDelete, f.srv.URL+"/api/admin/app-plugins/locks", map[string]any{"storage_id": f.st.ID, "path": "docs/sozlesme.txt"})
	require.Equal(t, http.StatusOK, status, string(raw))
	rows = newRows(t, f.store, before)
	require.Len(t, rows, 1, "%+v", rows)
	assert.Equal(t, "app_plugin.unlock", rows[0].Action)
	assert.Equal(t, "docs/sozlesme.txt", rows[0].TargetID)
}
