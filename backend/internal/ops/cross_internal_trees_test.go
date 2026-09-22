package ops_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
)

// A cross-storage copy of a storage ROOT walks every entry at the top level,
// and filex keeps its own trees there. The walk skipped the trash and the
// thumbnails but not `.versions/`, so the destination received the source's
// version history: snapshots keyed by node ids that mean nothing on the other
// storage, which its next scan then catalogued. A user folder that happens to
// be called `.versions`, deeper down, is still the user's and still travels.
func TestCross_CopyOfAStorageRootLeavesFilexsOwnTreesBehind(t *testing.T) {
	f := newCrossFixture(t, nil)
	writeA(t, f, "rapor.txt", "gercek")
	writeA(t, f, ".versions/7/1", "eski surum")
	writeA(t, f, ".thumbs/k.jpg", "jpeg")
	writeA(t, f, ".filex-trash/1700000000-abc123__cop.txt", "cop")
	writeA(t, f, "proje/.versions/notlar.txt", "kullanicinin")

	require.NoError(t, ops.Transfer(context.Background(), f.drvA, f.drvB, "/", "kopya", ops.TransferHooks{}))

	require.Equal(t, "gercek", readB(t, f, "kopya/rapor.txt"))
	require.Equal(t, "kullanicinin", readB(t, f, "kopya/proje/.versions/notlar.txt"),
		"a user's own .versions folder below the root is content and must be copied")
	for _, rel := range []string{"kopya/.versions", "kopya/.thumbs", "kopya/.filex-trash"} {
		_, err := os.Stat(filepath.Join(f.rootB, filepath.FromSlash(rel)))
		assert.True(t, os.IsNotExist(err), "filex's own tree %s must not be copied to another storage", rel)
	}
}
