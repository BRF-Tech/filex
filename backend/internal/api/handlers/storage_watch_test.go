package handlers_test

// filex-watch's two halves on the server (handlers/storage_watch.go,
// docs/FILEX-WATCH.md): the storage page's keys, and the door the agent on
// the machine that owns the disk reports changed folders to.
//
// What these hold:
//   - a key is shown once, in the answer that made it, and listed without it;
//   - a key opens its own door and NO other - not /api/files, not /api/ai,
//     not the admin API - and only for its own storage;
//   - the door refuses a folder that climbs, takes nothing from filex's own
//     trees, and asks of the key's owner what every key's owner is asked;
//   - a report makes the catalogue look at the folder it names;
//   - making and revoking a key are audited as what they are, not as the
//     storage's own deletion.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/watchproto"

	// The drivers the card is asked about (the server links them all).
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/s3"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/sftp"
)

// watchKey makes a filex-watch key for the fixture's storage through the
// storage page's own route, and answers the whole answer.
func (f *rescanFixture) watchKey(t *testing.T, name string) (string, map[string]any) {
	t.Helper()
	code, body := doJSON(t, f.client, http.MethodPost, f.srv+"/api/admin/storages/"+itoa64(f.st.ID)+"/watch/keys", map[string]any{"name": name})
	require.Equal(t, http.StatusCreated, code, "%v", body)
	key, _ := body["key"].(string)
	require.True(t, strings.HasPrefix(key, watchproto.KeyPrefix), "a filex-watch key says what it is: %q", key)
	return key, body
}

// report sends one report to storage id's door with key (no session).
func report(t *testing.T, base string, storageID int64, key string, rep any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(rep)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, base+watchproto.EventsPath(storageID), bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func heartbeat(seq uint64) watchproto.Report {
	return watchproto.Report{Protocol: watchproto.Protocol, Seq: seq, Agent: watchproto.Agent{ID: "t", Version: watchproto.AgentVersion, Host: "nas01"}}
}

func TestStorageWatch_AKeyIsShownOnceAndListedWithout(t *testing.T) {
	f := newRescanFixture(t, "local")
	key, made := f.watchKey(t, "nas01")
	setup, _ := made["setup"].(map[string]any)
	require.NotNil(t, setup)
	assert.Contains(t, setup["linux"], key, "the commands made with the key carry it")
	assert.Contains(t, setup["docker"], "ghcr.io/brf-tech/filex-watch:"+watchproto.AgentVersion)
	assert.EqualValues(t, f.st.ID, setup["storage_id"])

	code, body := doJSON(t, f.client, http.MethodGet, f.srv+"/api/admin/storages/"+itoa64(f.st.ID)+"/watch", nil)
	require.Equal(t, http.StatusOK, code, "%v", body)
	raw, _ := json.Marshal(body)
	assert.NotContains(t, string(raw), key, "never listed again")
	keys, _ := body["keys"].([]any)
	require.Len(t, keys, 1)
	row := keys[0].(map[string]any)
	assert.Equal(t, "nas01", row["name"])
	assert.Nil(t, row["agent"], "nothing heard from it yet")
	assert.Contains(t, body["setup"].(map[string]any)["linux"], "PASTE-THE-KEY-HERE")
	assert.Equal(t, watchproto.AgentVersion, body["release"])

	// It is an API key row of its own kind: listed among the API keys, and
	// holding no verb.
	toks, err := f.store.ListAPITokens(context.Background())
	require.NoError(t, err)
	var mine *model.APIToken
	for _, tk := range toks {
		if tk.Source == model.TokenSourceWatch {
			mine = tk
		}
	}
	require.NotNil(t, mine)
	assert.Equal(t, model.WatchScopePrefix+itoa64(f.st.ID), mine.Scopes)
	assert.True(t, mine.DoorOnly())
}

// A report makes the catalogue look at the folder it names, and the key's
// card says what the agent said about itself.
func TestStorageWatch_AReportRescansTheFolder(t *testing.T) {
	f := newRescanFixture(t, "local")
	f.write(t, "docs/a.txt", "a")
	require.NoError(t, f.worker.Trigger(context.Background(), f.st.ID))
	key, _ := f.watchKey(t, "nas01")

	f.write(t, "docs/b.txt", "b")
	rep := heartbeat(1)
	rep.Folders = []string{"/docs"}
	code, ans := report(t, f.srv, f.st.ID, key, rep)
	require.Equal(t, http.StatusAccepted, code, "%v", ans)
	assert.Equal(t, true, ans["accepted"])
	assert.EqualValues(t, 1, ans["queued"])
	assert.Equal(t, "arsiv", ans["storage"])
	require.Eventually(t, func() bool { return f.row(t, "/docs/b.txt") != nil }, 20*time.Second, 100*time.Millisecond,
		"the reported folder was looked at")

	code, body := doJSON(t, f.client, http.MethodGet, f.srv+"/api/admin/storages/"+itoa64(f.st.ID)+"/watch", nil)
	require.Equal(t, http.StatusOK, code)
	row := body["keys"].([]any)[0].(map[string]any)
	agent, _ := row["agent"].(map[string]any)
	require.NotNil(t, agent, "the agent's own word, since filex started")
	assert.Equal(t, "nas01", agent["agent"].(map[string]any)["host"])
	assert.NotEmpty(t, row["last_used_at"], "the key's last use is the API key row's")
}

// The same batch again (the agent did not hear the first answer) is
// acknowledged and not queued twice.
func TestStorageWatch_ARetriedBatchIsNotQueuedTwice(t *testing.T) {
	f := newRescanFixture(t, "local")
	key, _ := f.watchKey(t, "")
	rep := heartbeat(5)
	rep.Folders = []string{"/x"}
	code, ans := report(t, f.srv, f.st.ID, key, rep)
	require.Equal(t, http.StatusAccepted, code, "%v", ans)
	assert.EqualValues(t, 1, ans["queued"])
	first, _ := ans["instance"].(string)
	assert.NotEmpty(t, first, "the answer names the filex process that took it")
	code, ans = report(t, f.srv, f.st.ID, key, rep)
	require.Equal(t, http.StatusAccepted, code, "%v", ans)
	assert.EqualValues(t, 0, ans["queued"])
	assert.Equal(t, first, ans["instance"], "the same process, the same name: the agent asks for no resync")
}

// An object store has no folders on a disk for an agent to watch: the page's
// descriptor says so (watch_agent), its card says so (supported), and a key
// for one is refused.
func TestStorageWatch_NoKeyForAnObjectStore(t *testing.T) {
	f := newRescanFixture(t, "local")
	code, body := doJSON(t, f.client, http.MethodGet, f.srv+"/api/admin/storages/"+itoa64(f.st.ID)+"/watch", nil)
	require.Equal(t, http.StatusOK, code, "%v", body)
	assert.Equal(t, true, body["supported"])

	bucket, err := f.store.CreateStorage(context.Background(), &model.Storage{
		Name: "kova", Driver: "s3", MountPath: "/kova",
		ConfigJSON: []byte(`{"bucket":"b","region":"us-east-1","endpoint":"http://127.0.0.1:1"}`),
		SyncMode:   model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	code, body = doJSON(t, f.client, http.MethodPost, f.srv+"/api/admin/storages/"+itoa64(bucket.ID)+"/watch/keys", map[string]any{"name": "x"})
	assert.Equal(t, http.StatusConflict, code, "%v", body)
	assert.Equal(t, "watch_unsupported_driver", body["error"])
	assert.NotEmpty(t, body["message"])
	code, body = doJSON(t, f.client, http.MethodGet, f.srv+"/api/admin/storages/"+itoa64(bucket.ID)+"/watch", nil)
	require.Equal(t, http.StatusOK, code, "%v", body)
	assert.Equal(t, false, body["supported"])

	// The storage page reads the same answer from the driver descriptors.
	res, err := f.client.Get(f.srv + "/api/admin/storage-drivers")
	require.NoError(t, err)
	defer res.Body.Close()
	var descs []map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&descs))
	byName := map[string]bool{}
	for _, d := range descs {
		on, _ := d["watch_agent"].(bool)
		byName[d["driver"].(string)] = on
	}
	assert.True(t, byName["local"], "local: real folders")
	assert.True(t, byName["sftp"], "sftp: real folders on the server")
	assert.False(t, byName["s3"], "s3: an object store")
}

// ⚠⚠ The key opens its own door and no other: every other door's lookup
// cannot even find it (its hash has a domain of its own), and a door that
// reached the row another way refuses it (DoorOnly).
func TestStorageWatch_AKeyOpensNoOtherDoor(t *testing.T) {
	f := newRescanFixture(t, "local")
	key, _ := f.watchKey(t, "nas01")
	for _, door := range []struct{ method, path string }{
		{http.MethodGet, "/api/files/manager?path=arsiv://"},
		{http.MethodGet, "/api/ai/root"},
		{http.MethodPost, "/api/ai/mcp"},
		{http.MethodGet, "/api/admin/storages"},
		{http.MethodGet, "/api/auth/me"},
		{http.MethodGet, "/api/tokens"},
	} {
		req, err := http.NewRequest(door.method, f.srv+door.path, strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = res.Body.Close()
		assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, res.StatusCode, "%s %s answered %d to a filex-watch key", door.method, door.path, res.StatusCode)
	}
	// And an ordinary API key - even an administrator's - is not a key for
	// this door.
	admin, err := f.store.GetUserByEmail(context.Background(), "admin@test.local")
	require.NoError(t, err)
	tok := testutil.NewAPIToken(t, f.store, admin.ID, "read,write,delete,mcp,admin")
	code, body := report(t, f.srv, f.st.ID, tok, heartbeat(1))
	assert.Equal(t, http.StatusUnauthorized, code, "%v", body)
	assert.Equal(t, "watch_key_unknown", body["error"])
}

func TestStorageWatch_DoorRefusals(t *testing.T) {
	f := newRescanFixture(t, "local")
	key, made := f.watchKey(t, "nas01")

	code, body := report(t, f.srv, f.st.ID, "", heartbeat(1))
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "watch_key_missing", body["error"])
	assert.NotEmpty(t, body["message"], "a sentence the agent logs")

	code, body = report(t, f.srv, f.st.ID, watchproto.KeyPrefix+"not-a-real-key", heartbeat(1))
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "watch_key_unknown", body["error"])

	// Another storage's door.
	other, err := f.store.CreateStorage(context.Background(), &model.Storage{
		Name: "baska", Driver: "local", MountPath: "/baska", ConfigJSON: []byte(`{"root":"` + strings.ReplaceAll(t.TempDir(), `\`, `/`) + `"}`),
		SyncMode: model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	code, body = report(t, f.srv, other.ID, key, heartbeat(1))
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, "watch_key_other_storage", body["error"])

	// A folder that climbs is refused; one in filex's own trees is ignored.
	climb := heartbeat(2)
	climb.Folders = []string{"/docs/../../etc"}
	code, body = report(t, f.srv, f.st.ID, key, climb)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "watch_report_invalid", body["error"])
	own := heartbeat(3)
	own.Folders = []string{"/.versions/12", "/.filex-trash"}
	code, body = report(t, f.srv, f.st.ID, key, own)
	require.Equal(t, http.StatusAccepted, code, "%v", body)
	assert.EqualValues(t, 0, body["queued"])
	assert.EqualValues(t, 2, body["ignored"])

	// A newer protocol than this server speaks is said, not misread.
	newer := heartbeat(4)
	newer.Protocol = watchproto.Protocol + 1
	code, body = report(t, f.srv, f.st.ID, key, newer)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "watch_report_invalid", body["error"])

	// A body past the limit.
	huge := heartbeat(5)
	huge.Agent.Root = strings.Repeat("x", watchproto.MaxBodyBytes)
	code, body = report(t, f.srv, f.st.ID, key, huge)
	assert.Equal(t, http.StatusRequestEntityTooLarge, code, "%v", body)
	assert.Equal(t, "watch_report_too_large", body["error"])

	// A revoked key is refused from its next report.
	row := made["row"].(map[string]any)
	code, _ = doJSON(t, f.client, http.MethodDelete, f.srv+"/api/admin/storages/"+itoa64(f.st.ID)+"/watch/keys/"+itoa64(int64(row["id"].(float64))), nil)
	require.Equal(t, http.StatusOK, code)
	code, body = report(t, f.srv, f.st.ID, key, heartbeat(6))
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "watch_key_unknown", body["error"])
}

// The key's owner is asked, as they are now, what every key's owner is
// asked: an owner who is no longer an administrator sends no more reports.
func TestStorageWatch_TheOwnerMustStillBeAnAdministrator(t *testing.T) {
	f := newRescanFixture(t, "local")
	key, _ := f.watchKey(t, "nas01")
	admin, err := f.store.GetUserByEmail(context.Background(), "admin@test.local")
	require.NoError(t, err)
	// A second administrator, so the first may be demoted.
	testutil.SeedAdminUser(t, f.store)
	require.NoError(t, f.store.UpdateUserRole(context.Background(), admin.ID, model.RoleUser))
	code, body := report(t, f.srv, f.st.ID, key, heartbeat(1))
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, "watch_owner_not_admin", body["error"])
}

// One key's report rate is bounded.
func TestStorageWatch_RateLimited(t *testing.T) {
	f := newRescanFixture(t, "local")
	key, _ := f.watchKey(t, "nas01")
	limited := false
	for i := 1; i <= 130 && !limited; i++ {
		code, body := report(t, f.srv, f.st.ID, key, heartbeat(uint64(i)))
		if code == http.StatusTooManyRequests {
			assert.Equal(t, "watch_rate_limited", body["error"])
			limited = true
			break
		}
		require.Equal(t, http.StatusAccepted, code, "%v", body)
	}
	assert.True(t, limited, "past 120 reports a minute the key is held back")
}

// The page's routes are an administrator's; making and revoking a key are
// audited as what they are - a revoke under /storages/{id} must not read as
// the storage's deletion.
func TestStorageWatch_AuditedAsKeysNotAsTheStorage(t *testing.T) {
	f := newRescanFixture(t, "local")
	_, made := f.watchKey(t, "nas01")
	row := made["row"].(map[string]any)
	code, _ := doJSON(t, f.client, http.MethodDelete, f.srv+"/api/admin/storages/"+itoa64(f.st.ID)+"/watch/keys/"+itoa64(int64(row["id"].(float64))), nil)
	require.Equal(t, http.StatusOK, code)

	entries, err := f.store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	assert.Contains(t, actions, "storage.watch_key_create")
	assert.Contains(t, actions, "storage.watch_key_delete")
	assert.NotContains(t, actions, "storage.delete")

	// Another storage's key id is answered like one that does not exist.
	code, body := doJSON(t, f.client, http.MethodDelete, f.srv+"/api/admin/storages/"+itoa64(f.st.ID)+"/watch/keys/999999", nil)
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "watch_key_not_found", body["error"])
}

// The admin MCP tools reach the same card.
func TestStorageWatch_AdminTools(t *testing.T) {
	f := newRescanFixture(t, "local")
	admin, err := f.store.GetUserByEmail(context.Background(), "admin@test.local")
	require.NoError(t, err)
	tok := testutil.NewAPIToken(t, f.store, admin.ID, "mcp,admin")

	tl, status, res := adminTool(t, f.srv, tok, "admin_storages_watch_key_create", map[string]any{"id": f.st.ID, "body": map[string]any{"name": "nas02"}})
	require.False(t, tl.IsError, tl.Text)
	assert.Equal(t, http.StatusCreated, status, "%v", res)
	assert.True(t, strings.HasPrefix(res["key"].(string), watchproto.KeyPrefix))

	tl, status, res = adminTool(t, f.srv, tok, "admin_storages_watch", map[string]any{"id": f.st.ID})
	require.False(t, tl.IsError, tl.Text)
	assert.Equal(t, http.StatusOK, status)
	keys := res["keys"].([]any)
	require.Len(t, keys, 1)
	id := int64(keys[0].(map[string]any)["id"].(float64))

	tl, status, _ = adminTool(t, f.srv, tok, "admin_storages_watch_key_delete", map[string]any{"id": f.st.ID, "key": id})
	require.False(t, tl.IsError, tl.Text)
	assert.Equal(t, http.StatusOK, status)
}
