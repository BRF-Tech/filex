package sqlite_test

// External _test package so we exercise the driver via the public
// db.Driver interface, the same way callers do.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// ---------- Storages ----------

func TestStore_Storages_CRUD(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	cfg, _ := json.Marshal(map[string]any{"root": "/tmp"})
	st := &model.Storage{
		Name:          "main",
		Driver:        "local",
		MountPath:     "/",
		ConfigJSON:    cfg,
		SyncMode:      model.SyncModePoll,
		SyncIntervalS: 900,
		Enabled:       true,
	}
	created, err := store.CreateStorage(ctx, st)
	require.NoError(t, err)
	require.NotZero(t, created.ID)
	assert.Equal(t, "main", created.Name)

	// Get / GetByName
	got, err := store.GetStorage(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "main", got.Name)
	gotByName, err := store.GetStorageByName(ctx, "main")
	require.NoError(t, err)
	assert.Equal(t, created.ID, gotByName.ID)

	// List
	all, err := store.ListStorages(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)

	// ListEnabled
	enabled, err := store.ListEnabledStorages(ctx)
	require.NoError(t, err)
	require.Len(t, enabled, 1)

	// Update
	created.MountPath = "/data"
	require.NoError(t, store.UpdateStorage(ctx, created))
	got2, _ := store.GetStorage(ctx, created.ID)
	assert.Equal(t, "/data", got2.MountPath)

	// UpdateStorageSyncCursor
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, store.UpdateStorageSyncCursor(ctx, created.ID, now, "tok"))
	got3, _ := store.GetStorage(ctx, created.ID)
	assert.Equal(t, "tok", got3.LastSyncToken)
	require.NotNil(t, got3.LastSyncAt)

	// Delete
	require.NoError(t, store.DeleteStorage(ctx, created.ID))
	all2, _ := store.ListStorages(ctx)
	assert.Empty(t, all2)
}

// ---------- Users ----------

func TestStore_Users_CRUD(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	u, err := store.CreateUser(ctx, "alice@example.com", "$2a$bogus", model.RoleAdmin, "tr", "Europe/Istanbul")
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", u.Email)

	got, err := store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", got.Email)
	assert.Equal(t, model.RoleAdmin, got.Role)

	gotByEmail, err := store.GetUserByEmail(ctx, "alice@example.com")
	require.NoError(t, err)
	assert.Equal(t, u.ID, gotByEmail.ID)

	users, err := store.ListUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)

	cnt, err := store.CountUsers(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, cnt)

	// UpdateUserPassword
	require.NoError(t, store.UpdateUserPassword(ctx, u.ID, "$2a$new"))
	got2, _ := store.GetUser(ctx, u.ID)
	assert.Equal(t, "$2a$new", got2.PasswordHash)

	// UpdateUserEmail
	require.NoError(t, store.UpdateUserEmail(ctx, u.ID, "alice2@example.com"))
	got3, _ := store.GetUser(ctx, u.ID)
	assert.Equal(t, "alice2@example.com", got3.Email)

	// UpdateUserLocale
	require.NoError(t, store.UpdateUserLocale(ctx, u.ID, "en", "UTC"))
	got4, _ := store.GetUser(ctx, u.ID)
	assert.Equal(t, "en", got4.Locale)
	assert.Equal(t, "UTC", got4.Timezone)

	// UpdateUserRole
	require.NoError(t, store.UpdateUserRole(ctx, u.ID, model.RoleUser))
	got5, _ := store.GetUser(ctx, u.ID)
	assert.Equal(t, model.RoleUser, got5.Role)

	// TouchLastLogin
	require.NoError(t, store.TouchLastLogin(ctx, u.ID))
	got6, _ := store.GetUser(ctx, u.ID)
	require.NotNil(t, got6.LastLoginAt)

	// Delete
	require.NoError(t, store.DeleteUser(ctx, u.ID))
	cnt2, _ := store.CountUsers(ctx)
	assert.EqualValues(t, 0, cnt2)
}

// ---------- Sessions ----------

func TestStore_Sessions_Lifecycle(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	u, err := store.CreateUser(ctx, "u@test.local", "h", model.RoleUser, "en", "UTC")
	require.NoError(t, err)

	expires := time.Now().Add(time.Hour).UTC()
	sess, err := store.CreateSession(ctx, u.ID, "tok-aaaa-aaaa", expires, "127.0.0.1", "ua")
	require.NoError(t, err)
	require.NotZero(t, sess.ID)

	got, err := store.GetSessionByToken(ctx, "tok-aaaa-aaaa")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.UserID)

	// CountActiveSessions
	cnt, err := store.CountActiveSessions(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, cnt)

	// DeleteSession
	require.NoError(t, store.DeleteSession(ctx, "tok-aaaa-aaaa"))
	_, err = store.GetSessionByToken(ctx, "tok-aaaa-aaaa")
	require.Error(t, err)
}

func TestStore_Sessions_DeleteAllForUser_KeepCurrent(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	u, err := store.CreateUser(ctx, "u@test.local", "h", model.RoleUser, "en", "UTC")
	require.NoError(t, err)

	exp := time.Now().Add(time.Hour).UTC()
	for _, tok := range []string{"a-aaaa-aaaa", "b-bbbb-bbbb", "c-cccc-cccc"} {
		_, err := store.CreateSession(ctx, u.ID, tok, exp, "", "")
		require.NoError(t, err)
	}

	// Keep "a", revoke the other two.
	require.NoError(t, store.DeleteSessionsForUser(ctx, u.ID, "a-aaaa-aaaa"))

	_, err = store.GetSessionByToken(ctx, "a-aaaa-aaaa")
	require.NoError(t, err)
	_, err = store.GetSessionByToken(ctx, "b-bbbb-bbbb")
	require.Error(t, err)
	_, err = store.GetSessionByToken(ctx, "c-cccc-cccc")
	require.Error(t, err)
}

// ---------- Shares ----------

func TestStore_Shares_CRUD(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	u, _ := store.CreateUser(ctx, "u@test.local", "h", model.RoleUser, "en", "UTC")

	// Need a node — create the storage row + node row first.
	stg, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	n, err := store.CreateNode(ctx, &model.Node{
		StorageID: stg.ID, Name: "f.txt", Path: "/f.txt", PathHash: "h0001", Type: model.NodeTypeFile,
		SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)

	exp := time.Now().Add(time.Hour).UTC()
	maxDL := 5
	sh, err := store.CreateShare(ctx, &model.Share{
		NodeID:       n.ID,
		Token:        "tok-share-001",
		PinHash:      "$2a$bogus",
		ExpiresAt:    &exp,
		MaxDownloads: &maxDL,
		CreatedBy:    &u.ID,
	})
	require.NoError(t, err)
	require.NotZero(t, sh.ID)
	assert.True(t, sh.HasPin)

	got, err := store.GetShareByToken(ctx, "tok-share-001")
	require.NoError(t, err)
	assert.Equal(t, sh.ID, got.ID)

	gotByID, err := store.GetShareByID(ctx, sh.ID)
	require.NoError(t, err)
	assert.Equal(t, sh.ID, gotByID.ID)

	// IncrementShareDownload
	require.NoError(t, store.IncrementShareDownload(ctx, sh.ID))
	got2, _ := store.GetShareByID(ctx, sh.ID)
	assert.Equal(t, 1, got2.DownloadCount)

	// ListSharesByNode
	list, err := store.ListSharesByNode(ctx, n.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)

	// RevokeShare — sets ExpiresAt to NOW so IsExpired = true.
	require.NoError(t, store.RevokeShare(ctx, sh.ID))
	got3, _ := store.GetShareByID(ctx, sh.ID)
	require.NotNil(t, got3.ExpiresAt)

	// DeleteShare
	require.NoError(t, store.DeleteShare(ctx, sh.ID))
	_, err = store.GetShareByID(ctx, sh.ID)
	require.Error(t, err)
}

// ---------- Nodes ----------

func TestStore_Nodes_TreeAndMove(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	stg, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "n", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})

	root, err := store.CreateNode(ctx, &model.Node{
		StorageID: stg.ID, Name: "rootdir", Path: "/rootdir", PathHash: "p_root",
		Type: model.NodeTypeDirectory, SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)

	child, err := store.CreateNode(ctx, &model.Node{
		StorageID: stg.ID, ParentID: &root.ID, Name: "a.txt", Path: "/rootdir/a.txt", PathHash: "p_child",
		Type: model.NodeTypeFile, SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)

	rootChildren, err := store.ListNodesByParent(ctx, stg.ID, &root.ID)
	require.NoError(t, err)
	require.Len(t, rootChildren, 1)
	assert.Equal(t, child.ID, rootChildren[0].ID)

	// Top-level (parent IS NULL) should return rootdir.
	tops, err := store.ListNodesByParent(ctx, stg.ID, nil)
	require.NoError(t, err)
	require.Len(t, tops, 1)
	assert.Equal(t, root.ID, tops[0].ID)

	// MoveNode
	require.NoError(t, store.MoveNode(ctx, child.ID, &root.ID, "a-renamed.txt", "/rootdir/a-renamed.txt", "p_child2"))
	moved, err := store.GetNode(ctx, child.ID)
	require.NoError(t, err)
	assert.Equal(t, "a-renamed.txt", moved.Name)

	// CountNodesByStorage
	count, _ := store.CountNodesByStorage(ctx, stg.ID)
	assert.EqualValues(t, 2, count)

	// SoftDeleteNode
	require.NoError(t, store.SoftDeleteNode(ctx, child.ID))
	count2, _ := store.CountNodesByStorage(ctx, stg.ID)
	assert.EqualValues(t, 1, count2, "soft-deleted node should drop from active count")

	// HardDeleteNode
	require.NoError(t, store.HardDeleteNode(ctx, root.ID))
}

func TestStore_Nodes_Search(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	stg, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	for i, name := range []string{"holiday-photo.jpg", "holiday-video.mp4", "report.pdf"} {
		_, err := store.CreateNode(ctx, &model.Node{
			StorageID: stg.ID, Name: name, Path: "/" + name, PathHash: hashOf(i),
			Type: model.NodeTypeFile, SyncState: model.SyncStateSynced,
		})
		require.NoError(t, err)
	}
	results, err := store.SearchNodes(ctx, stg.ID, "holiday%", 50)
	require.NoError(t, err)
	require.Len(t, results, 2)
}

// ---------- Audit ----------

func TestStore_Audit_InsertAndList(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	u, _ := store.CreateUser(ctx, "u@test.local", "h", model.RoleAdmin, "en", "UTC")

	for i := 0; i < 3; i++ {
		require.NoError(t, store.InsertAuditEntry(ctx, &model.AuditEntry{
			UserID:     &u.ID,
			Action:     "test.action",
			TargetType: "node",
			TargetID:   "1",
			Metadata:   map[string]interface{}{"i": i},
			IP:         "127.0.0.1",
		}))
	}
	recent, err := store.ListAuditRecent(ctx, 10)
	require.NoError(t, err)
	require.Len(t, recent, 3)
	assert.Equal(t, "test.action", recent[0].Action)

	// Filtered list
	filtered, total, err := store.ListAuditFiltered(ctx, &u.ID, "test.action", nil, nil, 10, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	assert.Len(t, filtered, 3)
}

// ---------- TOTP ----------

func TestStore_TOTP_Lifecycle(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	u, _ := store.CreateUser(ctx, "u@test.local", "h", model.RoleUser, "en", "UTC")

	// Set pending
	require.NoError(t, store.SetTotpPendingSecret(ctx, u.ID, "BASE32SECRET", []string{"AAAA-1111", "BBBB-2222"}))
	got, _ := store.GetUser(ctx, u.ID)
	assert.Equal(t, "BASE32SECRET", got.TOTPPendingSecret)
	assert.False(t, got.TOTPEnabled)

	// Activate
	require.NoError(t, store.ActivateTotp(ctx, u.ID))
	got2, _ := store.GetUser(ctx, u.ID)
	assert.True(t, got2.TOTPEnabled)
	assert.Equal(t, "BASE32SECRET", got2.TOTPSecret)
	assert.Empty(t, got2.TOTPPendingSecret)

	// Clear
	require.NoError(t, store.ClearTotp(ctx, u.ID))
	got3, _ := store.GetUser(ctx, u.ID)
	assert.False(t, got3.TOTPEnabled)
	assert.Empty(t, got3.TOTPSecret)
}

// ---------- Sync runs ----------

func TestStore_SyncRuns(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	stg, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})

	run, err := store.CreateSyncRun(ctx, stg.ID, "")
	require.NoError(t, err)
	require.NotZero(t, run.ID)

	require.NoError(t, store.FinishSyncRun(ctx, run.ID, "tok-after", 12, 5, 4, 3, "ok", ""))

	last, err := store.GetLastSyncRun(ctx, stg.ID)
	require.NoError(t, err)
	assert.Equal(t, run.ID, last.ID)
	assert.Equal(t, "ok", last.Status)
	assert.Equal(t, 12, last.SeenCount)
	assert.Equal(t, 5, last.Added)

	// ListSyncRunsAcrossAll
	all, total, err := store.ListSyncRunsAcrossAll(ctx, 0, "", 10, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, all, 1)

	// Filter by status
	okList, okTotal, err := store.ListSyncRunsAcrossAll(ctx, 0, "ok", 10, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, okTotal)
	assert.Len(t, okList, 1)
	failedList, failedTotal, err := store.ListSyncRunsAcrossAll(ctx, 0, "failed", 10, 0)
	require.NoError(t, err)
	assert.Zero(t, failedTotal)
	assert.Empty(t, failedList)
}

// ---------- Settings ----------

func TestStore_Settings(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertSetting(ctx, "instance.name", "filex-prod"))
	got, err := store.GetSetting(ctx, "instance.name")
	require.NoError(t, err)
	assert.Equal(t, "filex-prod", got)

	// Update existing
	require.NoError(t, store.UpsertSetting(ctx, "instance.name", "filex-staging"))
	got2, _ := store.GetSetting(ctx, "instance.name")
	assert.Equal(t, "filex-staging", got2)

	// Missing key returns sql.ErrNoRows wrapped via the store error.
	_, err = store.GetSetting(ctx, "definitely-missing")
	require.Error(t, err)
}

// ---------- ChunkedUploads ----------

func TestStore_ChunkedUploads(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	stg, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	cu := &model.ChunkedUpload{
		ID:         "abc1234567890abcdef1234567890abcd",
		StorageID:  stg.ID,
		StorageKey: "/foo/big.bin",
		UploadID:   "upload-id-123",
		TotalSize:  1024 * 1024 * 100,
		Parts:      []model.UploadPart{{PartNumber: 1, Etag: "e1", Size: 1024}},
		ExpiresAt:  time.Now().Add(time.Hour).UTC(),
	}
	require.NoError(t, store.CreateChunkedUpload(ctx, cu))

	got, err := store.GetChunkedUpload(ctx, cu.ID)
	require.NoError(t, err)
	assert.Equal(t, cu.UploadID, got.UploadID)
	require.Len(t, got.Parts, 1)
	assert.Equal(t, "e1", got.Parts[0].Etag)

	// Update parts
	require.NoError(t, store.UpdateChunkedUploadParts(ctx, cu.ID, []model.UploadPart{
		{PartNumber: 1, Etag: "e1", Size: 1024},
		{PartNumber: 2, Etag: "e2", Size: 2048},
	}))
	got2, _ := store.GetChunkedUpload(ctx, cu.ID)
	require.Len(t, got2.Parts, 2)

	// Delete
	require.NoError(t, store.DeleteChunkedUpload(ctx, cu.ID))
	_, err = store.GetChunkedUpload(ctx, cu.ID)
	require.Error(t, err)
}

// ---------- ErrNoRows ----------

func TestStore_GetUserByEmail_NotFound(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	_, err := store.GetUserByEmail(context.Background(), "ghost@nowhere")
	require.Error(t, err)
	// Should propagate sql.ErrNoRows.
	assert.True(t, errors.Is(err, db.ErrNoRows), "expected ErrNoRows, got %v", err)
}

// ---------- helpers ----------

// hashOf returns a unique 32-char hex hash suitable for path_hash columns.
// The path_hash column is CHAR(32) with a unique index per storage, so each
// generated value must differ.
func hashOf(i int) string {
	// Pad the index into a 32-char hex string. Use sprintf for safety.
	s := strings.Repeat("0", 28)
	return s + sprintf04x(i)
}

// sprintf04x is a tiny helper to avoid importing fmt in the helpers section.
func sprintf04x(i int) string {
	const hex = "0123456789abcdef"
	out := []byte{
		hex[(i>>12)&0xF],
		hex[(i>>8)&0xF],
		hex[(i>>4)&0xF],
		hex[i&0xF],
	}
	return string(out)
}

// ---------- Tags: cross-storage listing + tagged-node lookup ----------

func TestStore_Tags_AllAndByTag(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	stgA, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "a", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	stgB, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "b", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})

	mk := func(stgID int64, name, hash string) int64 {
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: stgID, Name: name, Path: "/" + name, PathHash: hash,
			Type: model.NodeTypeFile, SyncState: model.SyncStateSynced,
		})
		require.NoError(t, err)
		return n.ID
	}

	n1 := mk(stgA.ID, "f1.txt", "h-tag-1") // tags: report, draft
	n2 := mk(stgB.ID, "f2.txt", "h-tag-2") // tags: report   (other storage)
	n3 := mk(stgA.ID, "f3.txt", "h-tag-3") // tags: archive
	nDel := mk(stgA.ID, "del.txt", "h-tag-4")

	require.NoError(t, store.SetNodeTags(ctx, n1, []string{"report", "draft"}))
	require.NoError(t, store.SetNodeTags(ctx, n2, []string{"report"}))
	require.NoError(t, store.SetNodeTags(ctx, n3, []string{"archive"}))
	require.NoError(t, store.SetNodeTags(ctx, nDel, []string{"report"}))

	// Soft-deleted nodes/tags must not surface.
	require.NoError(t, store.SoftDeleteNode(ctx, nDel))

	// ListAllTags → distinct across both storages, alphabetical, no dupes,
	// excludes the soft-deleted node's tags (here "report" still exists via
	// live nodes, so it stays; the deleted-only case is covered below).
	all, err := store.ListAllTags(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"archive", "draft", "report"}, all)

	// ListNodesByTag("report") → n1 (storage A) + n2 (storage B), not nDel.
	rep, err := store.ListNodesByTag(ctx, "report", 100)
	require.NoError(t, err)
	require.Len(t, rep, 2)
	ids := map[int64]bool{}
	for _, n := range rep {
		ids[n.ID] = true
	}
	assert.True(t, ids[n1] && ids[n2])
	assert.False(t, ids[nDel], "soft-deleted node must be excluded")

	// A tag that exists only on a soft-deleted node disappears from ListAllTags.
	require.NoError(t, store.SetNodeTags(ctx, n3, []string{}))
	all2, err := store.ListAllTags(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"draft", "report"}, all2)

	// Unknown tag → empty slice, no error.
	none, err := store.ListNodesByTag(ctx, "no-such-tag", 100)
	require.NoError(t, err)
	assert.Empty(t, none)
}
