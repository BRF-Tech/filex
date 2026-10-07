package onlyoffice

// The record of each editing session's version lives in the database
// (office_sessions, migration 00092) with the in-process cache in front of it
// (internal/memcache): a restart and a second filex instance behind the same
// database both still know which version a running session stands on, and a
// save is judged against the DATABASE, never against a cache another
// instance's answer has not reached.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// another is a second filex process on the harness's database and storage:
// nothing of the first one's memory.
func (h *csvHarness) another() *Service {
	return New(h.store, func(int64) (storage.Driver, error) { return h.drv, nil },
		h.ds.URL, "shh", "https://filex.example", time.Hour)
}

func TestSessionBase_ARestartStillKeepsAnOlderSessionsSaveOffTheNewVersion(t *testing.T) {
	h := newDocHarness(t, "rapor.docx", docxMime, "V1 - opened in the editor")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada) // the process before the restart records the session's version
	outside(t, h, "V2 - written while filex restarted")

	h.svc = h.another() // the restart: a new process, the same database
	resp := h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "V2 - written while filex restarted", h.disk(t),
		"after a restart the old session's save went over the newer version")
	assert.Len(t, h.besideFiles(t), 1, "the save is kept beside it")
}

func TestSessionBase_TwoInstancesOnOneDatabaseSeeEachOthersRecord(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada) // instance A hands out the editing config
	b := h.another()

	stale, known := b.SessionState(ctx, h.node, h.sessionKey)
	assert.True(t, known, "B knows the session A recorded")
	assert.False(t, stale)

	outside(t, h, "V2 from outside")
	stale, known = b.SessionState(ctx, h.node, h.sessionKey)
	assert.True(t, known)
	assert.True(t, stale)

	// The person answers through B ("write mine"); the save arrives at A,
	// whose cache still holds the version the session opened on. The
	// decision is read from the database, so A sees B's answer.
	require.NoError(t, b.RebaseSession(ctx, h.node, h.sessionKey))
	cached, ok := h.svc.bases().Cache().Get(h.sessionKey)
	require.True(t, ok, "the rig: A's cache holds the first version")
	require.NotEqual(t, int64(len("V2 from outside")), cached.size, "the rig: and it is stale")

	resp := h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, docxBytes, h.disk(t), "A decided on its stale cache instead of the database")
	assert.Empty(t, h.besideFiles(t))
}

func TestSessionBase_AnotherInstancesDropIsHonoured(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)
	outside(t, h, "V2 from outside")

	require.NoError(t, h.another().DropSession(ctx, h.node, h.sessionKey))
	resp := h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "V2 from outside", h.disk(t))
	assert.Empty(t, h.besideFiles(t), "the person dropped those edits on the other instance")
}

func TestSessionBase_TheSessionsEndRemovesItsRow(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)
	row, err := h.store.GetOfficeSession(ctx, h.sessionKey)
	require.NoError(t, err)
	require.NotNil(t, row, "the editing config recorded the session")

	resp := h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"])
	row, err = h.store.GetOfficeSession(ctx, h.sessionKey)
	require.NoError(t, err)
	assert.Nil(t, row, "the last save ended the session")
	_, ok := h.svc.bases().Cache().Get(h.sessionKey)
	assert.False(t, ok, "and the cache forgot it too")
}

func TestSessionBase_TheSweepRemovesRowsWhoseSessionNeverEnded(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	require.NoError(t, h.store.PutOfficeSession(ctx, &model.OfficeSession{
		DocKey: "abandoned", NodeID: h.node.ID, ExpiresUnix: time.Now().Add(-time.Minute).Unix(),
	}))
	require.NoError(t, h.store.PutOfficeSession(ctx, &model.OfficeSession{
		DocKey: "running", NodeID: h.node.ID, ExpiresUnix: time.Now().Add(time.Hour).Unix(),
	}))

	h.svc.PruneSessions(ctx)
	gone, err := h.store.GetOfficeSession(ctx, "abandoned")
	require.NoError(t, err)
	assert.Nil(t, gone)
	kept, err := h.store.GetOfficeSession(ctx, "running")
	require.NoError(t, err)
	assert.NotNil(t, kept)

	// At most once an hour, however often the maintenance tick calls it.
	require.NoError(t, h.store.PutOfficeSession(ctx, &model.OfficeSession{
		DocKey: "abandoned-later", NodeID: h.node.ID, ExpiresUnix: time.Now().Add(-time.Minute).Unix(),
	}))
	h.svc.PruneSessions(ctx)
	later, err := h.store.GetOfficeSession(ctx, "abandoned-later")
	require.NoError(t, err)
	assert.NotNil(t, later, "the second call within the hour did not sweep again")
}

func TestSessionBase_AnExpiredRowIsNoRecord(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	require.NoError(t, h.store.PutOfficeSession(ctx, &model.OfficeSession{
		DocKey: "long-gone", NodeID: h.node.ID, Size: 1, ExpiresUnix: time.Now().Add(-time.Minute).Unix(),
	}))
	// A session whose record expired (two days with no end): judged by the
	// key the document would get now - an older key is stale.
	stale, known := h.svc.SessionState(ctx, h.node, "long-gone")
	assert.False(t, known)
	assert.True(t, stale)
}
