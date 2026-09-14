package sync_test

// The scanner attributes NOTHING.
//
// A file the walk finds was not put there by anyone through filex, so its
// `owner_id` and `last_actor_id` are NULL — SYSTEM — and the Owner column says
// so. That is easy to believe of the background poller, which runs on a
// server-lifetime context with no user anywhere near it, and it was true by
// accident for exactly that reason.
//
// `Worker.Trigger` is the other door. The admin "Scan now" button hands its
// REQUEST context straight through, and a request context carries the admin.
// Before internal/sync/poll.go stripped the identity, one click on Scan stamped
// every object on the storage — thousands of files nobody uploaded — as that
// admin's, and billed the lot to their quota. Measured on a real instance
// through the real endpoint: a file written straight into the storage
// directory came back owned by the admin who pressed the button.
//
// This is that measurement, kept.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"

	_ "github.com/brf-tech/filex/backend/internal/queue/drivers/sqlite"
)

func TestScannerAttributesNothing_EvenWhenAnAdminTriggersIt(t *testing.T) {
	_, raw := dbtest.NewTestDB(t)
	// The accounting decorator is what stamps ownership in production; a test
	// against the bare store would prove nothing, because the bare store never
	// attributed anything in the first place.
	store := quotastore.New(raw)
	st, _, root := localStorage(t, store)

	require.NoError(t, os.WriteFile(filepath.Join(root, "was-already-here.txt"),
		[]byte("nobody put this here through filex"), 0o644))

	// A REAL account, because the second half of this test asks what the quota
	// says about them and a user that does not exist has no usage row to read.
	operator, err := raw.CreateUser(context.Background(), "ops@test.local", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)

	// Exactly what POST /api/admin/storages/{id}/sync does: the ADMIN's request
	// context, carried into the walk.
	admin := auth.WithUser(context.Background(), &model.User{ID: operator.ID})
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(admin, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(admin, st.ID))

	n, err := raw.GetNodeByPath(context.Background(), st.ID, pathkey.Hash(st.ID, "/was-already-here.txt"))
	require.NoError(t, err)
	require.NotNil(t, n, "precondition: the walk must have catalogued the file")

	assert.Nil(t, n.OwnerID,
		"finding a file is not putting it there — a scan must not make the operator its owner")
	assert.Nil(t, n.LastActorID,
		"…nor the last person to have touched it")
	assert.False(t, n.ExternalUpload)

	used, _, err := raw.GetUserUsage(context.Background(), operator.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 0, used,
		"and the operator is not billed for a bucket they only looked at")
}
