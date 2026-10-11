package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestCoEditOnEveryEngine walks the editing-together relay's rows (migration
// 00107, #189) on every engine: one session per file (a second insert for the
// same document loses), a write names the revision it read and loses when
// another writer came first, members come back in the order they joined and
// are found stale by their last sign of life, the log refuses a second entry
// at the same place and hands back the sealed bytes exactly, a blob name is
// written once, and deleting a session takes everything kept for it.
func TestCoEditOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			const sid = "0123456789abcdef0123456789abcdef"
			const doc = "aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11"

			got, err := store.GetCoEditSession(ctx, sid)
			require.NoError(t, err)
			require.Nil(t, got, "no row: (nil, nil)")

			s := &model.CoEditSession{
				SID: sid, DocKey: doc, StorageID: 3, Path: "Raporlar/Çeyrek.docx", App: "office-editor",
				Build: "0.3.0+3f9a0c1e5b2d4f60", BaseSig: "100:1791000000", KeyMode: "server", SealedKey: "enc:v1:abc",
				UserSalt: "5a5a", CreatedMs: 1_791_000_000_123, ActiveMs: 1_791_000_000_123,
			}
			ok, err := store.InsertCoEditSession(ctx, s)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, int64(1), s.Rev)

			other := *s
			other.SID = "ffffffffffffffffffffffffffffffff"
			ok, err = store.InsertCoEditSession(ctx, &other)
			require.NoError(t, err)
			require.False(t, ok, "one file, one session")

			byDoc, err := store.GetCoEditSessionByDoc(ctx, doc)
			require.NoError(t, err)
			require.NotNil(t, byDoc)
			require.Equal(t, *s, *byDoc, "every column comes back as it went in")

			a, b := *byDoc, *byDoc
			a.Head, a.Members, a.NextClient, a.NextIndex, a.HasBase = 1, 1, 1, 1, true
			b.Head = 7
			ok, err = store.UpdateCoEditSession(ctx, &a, 1)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, int64(2), a.Rev)
			ok, err = store.UpdateCoEditSession(ctx, &b, 1)
			require.NoError(t, err)
			require.False(t, ok, "the second writer read rev 1 and loses")

			require.NoError(t, store.InsertCoEditMember(ctx, &model.CoEditMember{SID: sid, Client: "c2", UserID: 8, UserRef: "r8", Name: "Sam", IndexUser: 2, JoinSeq: 2, SeenMs: 200}))
			require.NoError(t, store.InsertCoEditMember(ctx, &model.CoEditMember{SID: sid, Client: "c1", UserID: 7, UserRef: "r7", Name: "Ayşe", IndexUser: 1, CanEdit: true, JoinSeq: 1, SeenMs: 100}))
			ms, err := store.ListCoEditMembers(ctx, sid)
			require.NoError(t, err)
			require.Len(t, ms, 2)
			require.Equal(t, "c1", ms[0].Client, "the first to join first")
			require.True(t, ms[0].CanEdit)
			require.Equal(t, "Ayşe", ms[0].Name)

			require.NoError(t, store.SetCoEditMemberCounter(ctx, sid, "c1", 5))
			m, err := store.GetCoEditMember(ctx, sid, "c1")
			require.NoError(t, err)
			require.Equal(t, int64(5), m.LastCtr)

			ok, err = store.TouchCoEditMember(ctx, sid, "c1", 300)
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = store.TouchCoEditMember(ctx, sid, "c9", 300)
			require.NoError(t, err)
			require.False(t, ok, "no such member")
			stale, err := store.ListStaleCoEditMembers(ctx, 250, 10)
			require.NoError(t, err)
			require.Len(t, stale, 1)
			require.Equal(t, "c2", stale[0].Client, "c1 was seen at 300, c2 at 200")

			sealed := []byte{0, 1, 2, 0xff, 'x'}
			require.NoError(t, store.InsertCoEditEntry(ctx, &model.CoEditEntry{SID: sid, Seq: 1, Kind: "join", Client: "c1", AtMs: 10, Member: `{"client":"c1"}`}))
			require.NoError(t, store.InsertCoEditEntry(ctx, &model.CoEditEntry{SID: sid, Seq: 2, Kind: "changes", Client: "c1", AtMs: 11, Ctr: 1, CT: sealed}))
			require.Error(t, store.InsertCoEditEntry(ctx, &model.CoEditEntry{SID: sid, Seq: 2, Kind: "lock", Client: "c2", AtMs: 12, Ctr: 1, CT: sealed}),
				"one entry per place")
			es, err := store.ListCoEditEntries(ctx, sid, 0, 10)
			require.NoError(t, err)
			require.Len(t, es, 2)
			require.Empty(t, es[0].CT, "a relay entry carries no payload")
			require.Equal(t, `{"client":"c1"}`, es[0].Member)
			require.Equal(t, sealed, es[1].CT, "the sealed bytes come back exactly")
			es, err = store.ListCoEditEntries(ctx, sid, 1, 10)
			require.NoError(t, err)
			require.Len(t, es, 1)
			require.Equal(t, int64(2), es[0].Seq)

			ok, err = store.InsertCoEditBlob(ctx, &model.CoEditBlob{SID: sid, Name: "base", Size: 42, SHA256: "ab", CreatedMs: 5})
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = store.InsertCoEditBlob(ctx, &model.CoEditBlob{SID: sid, Name: "base", Size: 1})
			require.NoError(t, err)
			require.False(t, ok, "a blob is written once")
			bl, err := store.GetCoEditBlob(ctx, sid, "base")
			require.NoError(t, err)
			require.Equal(t, int64(42), bl.Size)

			idle, err := store.ListIdleCoEditSessions(ctx, 10)
			require.NoError(t, err)
			require.Empty(t, idle, "the session has a member")

			// The members' latest cursors (00109): one row each, written
			// over in place, read by when they were written.
			require.NoError(t, store.PutCoEditCursor(ctx, &model.CoEditCursor{SID: sid, Client: "c1", Ctr: 1, CT: []byte{1}, AtMs: 1000}))
			require.NoError(t, store.PutCoEditCursor(ctx, &model.CoEditCursor{SID: sid, Client: "c1", Ctr: 2, CT: []byte{2}, AtMs: 1500}))
			require.NoError(t, store.PutCoEditCursor(ctx, &model.CoEditCursor{SID: sid, Client: "c2", Ctr: 7, CT: []byte{7}, AtMs: 1200}))
			cs, err := store.ListCoEditCursors(ctx, sid, 0)
			require.NoError(t, err)
			require.Len(t, cs, 2, "one row per member")
			require.Equal(t, "c2", cs[0].Client, "by when they were written")
			require.Equal(t, int64(2), cs[1].Ctr, "c1's latest")
			require.Equal(t, []byte{2}, cs[1].CT)
			cs, err = store.ListCoEditCursors(ctx, sid, 1200)
			require.NoError(t, err)
			require.Len(t, cs, 1)
			require.Equal(t, "c1", cs[0].Client)

			gone, err := store.DeleteCoEditMember(ctx, sid, "c2")
			require.NoError(t, err)
			require.True(t, gone)
			cs, err = store.ListCoEditCursors(ctx, sid, 0)
			require.NoError(t, err)
			require.Len(t, cs, 1, "a member's cursor goes with it")

			require.NoError(t, store.DeleteCoEditSession(ctx, sid))
			got, err = store.GetCoEditSession(ctx, sid)
			require.NoError(t, err)
			require.Nil(t, got)
			es, err = store.ListCoEditEntries(ctx, sid, 0, 10)
			require.NoError(t, err)
			require.Empty(t, es, "the log goes with the session")
			ms, err = store.ListCoEditMembers(ctx, sid)
			require.NoError(t, err)
			require.Empty(t, ms)
			bl, err = store.GetCoEditBlob(ctx, sid, "base")
			require.NoError(t, err)
			require.Nil(t, bl)
			cs, err = store.ListCoEditCursors(ctx, sid, 0)
			require.NoError(t, err)
			require.Empty(t, cs, "the cursors go with the session")
		})
	}
}
