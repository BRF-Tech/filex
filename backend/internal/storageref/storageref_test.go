package storageref

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

type fakeStore struct {
	byName map[string]*model.Storage
	byUID  map[string]*model.Storage
	// calls records what was asked, so a test can prove a lookup did NOT
	// happen as well as that it did.
	calls []string
}

func (f *fakeStore) GetStorageByName(_ context.Context, name string) (*model.Storage, error) {
	f.calls = append(f.calls, "name:"+name)
	if st, ok := f.byName[name]; ok {
		return st, nil
	}
	return nil, sql.ErrNoRows
}

func (f *fakeStore) GetStorageByUID(_ context.Context, uid string) (*model.Storage, error) {
	f.calls = append(f.calls, "uid:"+uid)
	if st, ok := f.byUID[uid]; ok {
		return st, nil
	}
	return nil, sql.ErrNoRows
}

func TestResolve(t *testing.T) {
	ctx := context.Background()
	const uid = "7f3a1b2c-4d5e-4f60-8a1b-2c3d4e5f6071"
	st := &model.Storage{ID: 3, Name: "Garage S3", UID: uid}
	newStore := func() *fakeStore {
		return &fakeStore{
			byName: map[string]*model.Storage{"Garage S3": st},
			byUID:  map[string]*model.Storage{uid: st},
		}
	}

	t.Run("a name resolves without asking about uids", func(t *testing.T) {
		s := newStore()
		got, err := Resolve(ctx, s, "Garage S3")
		require.NoError(t, err)
		require.Equal(t, int64(3), got.ID)
		require.Equal(t, []string{"name:Garage S3"}, s.calls,
			"a ref that cannot be a uid must not cost a uid query")
	})

	t.Run("a uid resolves", func(t *testing.T) {
		s := newStore()
		got, err := Resolve(ctx, s, uid)
		require.NoError(t, err)
		require.Equal(t, int64(3), got.ID)
		require.Equal(t, []string{"uid:" + uid}, s.calls)
	})

	t.Run("a uid-shaped name falls back to the name lookup", func(t *testing.T) {
		const shaped = "11111111-2222-4333-8444-555555555555"
		s := newStore()
		s.byName[shaped] = &model.Storage{ID: 9, Name: shaped}
		got, err := Resolve(ctx, s, shaped)
		require.NoError(t, err)
		require.Equal(t, int64(9), got.ID,
			"a storage NAMED like a uuid became unreachable")
		require.Equal(t, []string{"uid:" + shaped, "name:" + shaped}, s.calls)
	})

	t.Run("an unknown ref reports the lookup's own error", func(t *testing.T) {
		s := newStore()
		_, err := Resolve(ctx, s, "nope")
		require.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("an empty ref never matches a row with an empty uid", func(t *testing.T) {
		s := newStore()
		s.byUID[""] = &model.Storage{ID: 99}
		_, err := Resolve(ctx, s, "")
		require.ErrorIs(t, err, sql.ErrNoRows,
			"an unfilled uid column must not become a wildcard")
	})
}

// Every protocol resolves the same way, and this is the gate that keeps it so.
//
// The rule these servers implement is identical — the first path segment names
// a storage — and it was five copies of one line. A fix applied to one of them
// produces a product that behaves differently depending on how you reach it,
// which is exactly what happened when only the HTTP manager was left off the
// shared subtree mover (issue #21).
func TestEveryProtocolGoesThroughResolve(t *testing.T) {
	files := []string{
		"../dav/fs.go",
		"../dav/dav.go",
		"../sftpsrv/handlers.go",
		"../ftpsrv/fs.go",
		"../nfssrv/fs.go",
		"../s3api/server.go",
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Clean(f))
		require.NoError(t, err)
		src := string(b)
		require.Contains(t, src, "storageref.Resolve(",
			"%s no longer resolves the storage segment through storageref", f)
		require.NotContains(t, src, ".GetStorageByName(",
			"%s resolves a storage by name directly, so a uid mount does not work there", f)
	}
}
