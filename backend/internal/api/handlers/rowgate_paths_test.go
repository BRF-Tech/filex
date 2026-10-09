package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/gatetest"
)

// The row gate on the agent's cross-storage move and on a draft's discard
// (issue #201), the paths rowgate_surfaces_test.go leaves out. Same shape:
// the verb stops right after its bytes changed (gatetest.Driver) and a
// storage scan asks for the gate (gatetest.HeldHalfWay). The gate is
// process-wide: no t.Parallel here.

// The agent's move to another storage (POST /api/ai/move, MCP file_move,
// aiOps.moveAcross) carries the bytes over, then drops the source's rows and
// deletes its bytes, all under the source storage's gate: a scan between the
// two would find the source's bytes with no rows and catalogue them again, as
// new files about to vanish.
//
// Break: take rowgate.Move out of aiOps.moveAcross (as up to 0.54) - the
// scan's judgement gets the source's gate while its delete stands half way.
func TestRowGate_AIMoveAcrossStoragesHoldsTheSourceGateUntilItsRowsAreGone(t *testing.T) {
	g := newGateRig(t)
	ctx := context.Background()
	coldDir := t.TempDir()
	coldDrv := &local.Driver{}
	require.NoError(t, coldDrv.Init(ctx, map[string]any{"root": coldDir}))
	cold, err := g.store.CreateStorage(ctx, &model.Storage{
		Name: "cold", Driver: "local", MountPath: "/cold", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(coldDir) + `"}`),
	})
	require.NoError(t, err)
	resolve := func(id int64) (storage.Driver, error) {
		if id == cold.ID {
			return coldDrv, nil
		}
		return g.resolve(id)
	}
	g.dir(t, "Leon")
	g.file(t, "Leon/a.txt", "a")

	g.drv.Arm(true)
	done := gateServe(t, ctx, handlers.NewAI(g.store, resolve, nil, "").Move,
		"/api/ai/move", map[string]any{"src": "main://Leon", "dst": "cold://Leo"})
	gatetest.HeldHalfWay(t, g.st.ID, g.drv, func() bool {
		return g.rowAt("/Leon") == nil && g.rowAt("/Leon/a.txt") == nil
	})

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, err = os.Stat(filepath.Join(coldDir, "Leo", "a.txt"))
	require.NoError(t, err, "the bytes did not reach the other storage")
}

// Discarding a draft (DELETE /api/files/drafts/{key}) moves its file into the
// trash and holds the gate until the draft's row is retagged there: a scan
// half way would confirm the live row gone by a Stat of its old path and drop
// it, and with it the trash entry that brings the draft back.
//
// Break: take rowgate.Move out of Manager.discardDraftGated (DiscardDraft up
// to 0.54).
func TestRowGate_DraftDiscardHoldsTheGateUntilTheDraftIsInTheTrash(t *testing.T) {
	g := newGateRig(t)
	doc, key, reqCtx := g.draftOf(t)

	g.drv.Arm(true)
	done := gateServe(t, reqCtx, g.mh.DiscardDraft, "/api/files/drafts/"+key, map[string]any{})
	gatetest.HeldHalfWay(t, g.st.ID, g.drv, func() bool { return g.inTrash(doc.ID) })

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"trashed":true`)
}

// On a storage that cannot keep what it deletes (no Move, no Copy) the
// discard is for good: the draft's bytes go, then its record and its
// folder's rows, and the gate is held from the first to the last, as the
// explorer's delete holds it on such a storage (deleteItem).
//
// Break: delete the bytes and drop the rows after the gate is released (as
// DiscardDraft did in 0.55 before this test: only the trash branch was
// gated) or with no gate at all (0.54).
func TestRowGate_DraftDiscardWithoutATrashHoldsTheGateUntilItsRowsAreGone(t *testing.T) {
	g := newGateRig(t)
	g.withoutTrash()
	doc, key, reqCtx := g.draftOf(t)

	g.drv.Arm(true)
	done := gateServe(t, reqCtx, g.mh.DiscardDraft, "/api/files/drafts/"+key, map[string]any{})
	gatetest.HeldHalfWay(t, g.st.ID, g.drv, func() bool {
		n, err := g.store.GetNode(context.Background(), doc.ID)
		return err != nil || n == nil
	})

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"trashed":false`)
}
