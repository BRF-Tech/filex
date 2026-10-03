package server

// Issue #104, item 6: what the server does with an entry the storage could not
// answer for. Every answer goes to the log (a storage that is not a plugin has
// no page; its lines reach the server log alone, seldom); only a CHANGE goes
// to the audit trail - the sync asks on every pass, and every answer is the
// same.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestEntryStateReporter_AuditsAChangeNotARepeat(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st := &model.Storage{ID: 7, Name: "arsiv", Driver: "local"}
	report := entryStateReporter(store, nil)

	report(ctx, syncpkg.EntryState{Storage: st, NodeID: 1, Path: "/Proje", Kind: model.NodeTypeDirectory,
		Reason: "permission denied", Changed: true})
	report(ctx, syncpkg.EntryState{Storage: st, NodeID: 1, Path: "/Proje", Kind: model.NodeTypeDirectory,
		Reason: "permission denied", Changed: false})
	report(ctx, syncpkg.EntryState{Storage: st, NodeID: 1, Path: "/Proje", Kind: model.NodeTypeDirectory,
		Changed: true})

	rows, err := store.ListAuditRecent(ctx, 50)
	require.NoError(t, err)
	var actions []string
	for _, r := range rows {
		if r.TargetType == "storage" && r.TargetID == "7" {
			actions = append(actions, r.Action)
			assert.Equal(t, "/Proje", r.Metadata["path"])
			if r.Action == AuditActionEntryUnavailable {
				assert.Equal(t, "permission denied", r.Metadata["reason"])
			}
		}
	}
	assert.ElementsMatch(t, []string{AuditActionEntryUnavailable, AuditActionEntryAvailable}, actions,
		"one row per change, none for the repeat")
}
