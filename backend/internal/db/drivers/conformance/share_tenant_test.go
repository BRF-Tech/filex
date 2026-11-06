package conformance_test

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// A link is stamped with the tenant its storage is in (CreateShare) and is
// served only while that tenant still has the storage (GetShareByToken), on
// every engine. The row stays: the storage coming back brings the link back.
// A link made on a storage in no tenant is not held to one.
func TestShareTenant_ALinkIsServedWhileItsTenantHasItsStorage(t *testing.T) {
	each(t, shareTenantStands)
}

func shareTenantStands(t *testing.T, sqlDB *sql.DB, store db.Store, _ string) {
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	provider := func(slug string) int64 {
		p, err := store.CreateProvider(ctx, &model.Provider{Slug: slug + "-" + suffix, Name: slug, AuthType: "local", Enabled: true})
		require.NoError(t, err)
		return p.ID
	}
	alpha, bravo := provider("alpha"), provider("bravo")
	node := func(storage string) *model.Node {
		st, err := store.CreateStorage(ctx, &model.Storage{Name: storage + "-" + suffix, Driver: "local", MountPath: "/s", Enabled: true, ConfigJSON: []byte(`{}`)})
		require.NoError(t, err)
		n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, Name: "doc.txt", Path: "/doc.txt", PathHash: pathkey.Hash(st.ID, "/doc.txt"), Type: model.NodeTypeFile})
		require.NoError(t, err)
		return n
	}
	links := 0
	link := func(n *model.Node) *model.Share {
		links++
		token := fmt.Sprintf("t%d%030d", links, time.Now().UnixNano())[:32]
		sh, err := store.CreateShare(ctx, &model.Share{NodeID: n.ID, Token: token})
		require.NoError(t, err)
		return sh
	}
	served := func(sh *model.Share) bool {
		got, err := store.GetShareByToken(ctx, sh.Token)
		if err != nil || got == nil {
			return false
		}
		require.Equal(t, sh.ID, got.ID)
		return true
	}

	doc := node("tenant-doc")
	require.NoError(t, store.LinkProviderStorage(ctx, alpha, doc.StorageID))
	sh := link(doc)
	require.Equal(t, alpha, sh.TenantID, "stamped with the storage's tenant")
	require.True(t, served(sh))
	read, err := store.GetShareByToken(ctx, sh.Token)
	require.NoError(t, err)
	require.Equal(t, alpha, read.TenantID, "and read back")

	require.NoError(t, store.UnlinkProviderStorage(ctx, alpha, doc.StorageID))
	require.NoError(t, store.LinkProviderStorage(ctx, bravo, doc.StorageID))
	require.False(t, served(sh), "the storage is bravo's now")
	_, err = store.GetShareByToken(ctx, sh.Token)
	require.ErrorIs(t, err, sql.ErrNoRows, "answered like a token that does not exist")
	kept, err := store.GetShareByID(ctx, sh.ID)
	require.NoError(t, err)
	require.NotNil(t, kept, "the row is kept")

	require.NoError(t, store.LinkProviderStorage(ctx, alpha, doc.StorageID))
	require.True(t, served(sh), "alpha has it again (shared with bravo): served")

	platform := node("platform-doc")
	open := link(platform)
	require.Zero(t, open.TenantID, "a storage in no tenant stamps none")
	require.NoError(t, store.LinkProviderStorage(ctx, bravo, platform.StorageID))
	require.True(t, served(open), "the platform's storage handed to a tenant keeps its links")

	// The migration's backfill, on this engine: an existing link gets the
	// tenant its storage is in now.
	_, err = sqlDB.ExecContext(ctx, `UPDATE shares SET tenant_id = NULL`)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, backfillStatement(t, store))
	require.NoError(t, err)
	back, err := store.GetShareByID(ctx, sh.ID)
	require.NoError(t, err)
	require.Equal(t, alpha, back.TenantID, "the lowest tenant id that has the storage")
	backOpen, err := store.GetShareByID(ctx, open.ID)
	require.NoError(t, err)
	require.Equal(t, bravo, backOpen.TenantID)
}

// backfillStatement is migration 00135's UPDATE. The three engines' files
// carry the same statement, which this checks before handing it out.
func backfillStatement(t *testing.T, _ db.Store) string {
	t.Helper()
	var stmt string
	for _, engine := range []string{"sqlite", "postgres", "mysql"} {
		raw, err := fs.ReadFile(db.MustGet(engine).MigrationsFS(), "00135_share_tenant.sql")
		require.NoError(t, err, engine)
		s := string(raw)
		start := strings.Index(s, "UPDATE shares")
		require.GreaterOrEqual(t, start, 0, engine)
		end := strings.Index(s[start:], ";")
		require.Greater(t, end, 0, engine)
		got := s[start : start+end]
		if stmt == "" {
			stmt = got
		}
		require.Equal(t, stmt, got, "%s backfills differently", engine)
	}
	return stmt
}
