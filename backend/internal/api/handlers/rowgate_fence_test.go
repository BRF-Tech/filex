package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/testutil/gatetest"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// sec055 follow-up: a folder moved or trashed on an object store moves one
// object at a time, for as long as the folder is large. The explorer's and the
// agent's moves and deletes fence the folder's prefixes (storage.FenceAt,
// rowgate.FencedChangeCtx / HoldCtx) instead of holding the storage's row gate:
// a storage scan half way gets the gate at once, scans the rest of the storage
// and leaves the fenced prefixes alone (gatetest.FencedHalfWay). A file, and
// every change on a storage with real folders, holds the gate as before (the
// rowgate_surfaces tests).
//
// Break: hold rowgate.MoveCtx whatever FenceAt says (vfRename, moveFenced,
// deleteItem, aiOps.Move, aiOps.Delete) - the scan does not get the gate half
// way.

// onObjectStore puts the rig's storage on drv's object-store view.
func (g *gateRig) onObjectStore() { g.served = g.drv.ObjectStore() }

func TestRowGate_ExplorerRenameOfAFolderOnAnObjectStoreFencesItsPrefixes(t *testing.T) {
	g := newGateRig(t)
	g.onObjectStore()
	g.dir(t, "Leon")
	g.file(t, "Leon/a.txt", "a")
	g.dir(t, "Baska")

	g.drv.Arm(true)
	done := gateServe(t, context.Background(), g.mh.Mutate, "/api/files/manager?action=rename",
		map[string]any{"path": "main://", "item": "main://Leon", "name": "Leo"})
	gatetest.FencedHalfWay(t, g.st.ID, g.drv, []string{"/Leon", "/Leon/a.txt", "/Leo", "/Leo/a.txt"}, "/Baska",
		func() bool { return g.rowAt("/Leo/a.txt") != nil && g.rowAt("/Leon/a.txt") == nil })

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, rowgate.Fences(g.st.ID).Empty())
}

func TestRowGate_ExplorerMoveOfAFolderOnAnObjectStoreFencesItsPrefixes(t *testing.T) {
	g := newGateRig(t)
	g.onObjectStore()
	g.dir(t, "Leon")
	g.file(t, "Leon/a.txt", "a")
	g.dir(t, "Arsiv")
	g.dir(t, "Baska")

	g.drv.Arm(true)
	done := gateServe(t, context.Background(), g.mh.Mutate, "/api/files/manager?action=move",
		map[string]any{"path": "main://Arsiv", "items": []map[string]string{{"path": "main://Leon"}}})
	gatetest.FencedHalfWay(t, g.st.ID, g.drv, []string{"/Leon", "/Arsiv/Leon", "/Arsiv/Leon/a.txt"}, "/Baska",
		func() bool { return g.rowAt("/Arsiv/Leon/a.txt") != nil })

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, rowgate.Fences(g.st.ID).Empty())
}

func TestRowGate_ExplorerDeleteOfAFolderOnAnObjectStoreFencesIt(t *testing.T) {
	g := newGateRig(t)
	g.onObjectStore()
	dir := g.dir(t, "Leon")
	g.file(t, "Leon/a.txt", "a")
	g.dir(t, "Baska")

	g.drv.Arm(true)
	done := gateServe(t, context.Background(), g.mh.Mutate, "/api/files/manager?action=delete",
		map[string]any{"path": "main://", "items": []map[string]string{{"path": "main://Leon"}}})
	gatetest.FencedHalfWay(t, g.st.ID, g.drv, []string{"/Leon", "/Leon/a.txt"}, "/Baska",
		func() bool { return g.inTrash(dir.ID) })

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, rowgate.Fences(g.st.ID).Empty())
}

func TestRowGate_AIMoveOfAFolderOnAnObjectStoreFencesItsPrefixes(t *testing.T) {
	g := newGateRig(t)
	g.onObjectStore()
	g.dir(t, "Leon")
	g.file(t, "Leon/a.txt", "a")
	g.dir(t, "Baska")

	g.drv.Arm(true)
	done := gateServe(t, context.Background(), handlers.NewAI(g.store, g.resolve, nil, "").Move,
		"/api/ai/move", map[string]any{"src": "main://Leon", "dst": "main://Leo"})
	gatetest.FencedHalfWay(t, g.st.ID, g.drv, []string{"/Leon", "/Leo"}, "/Baska",
		func() bool { return g.rowAt("/Leo/a.txt") != nil })

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, rowgate.Fences(g.st.ID).Empty())
}

func TestRowGate_AIDeleteOfAFolderOnAnObjectStoreFencesIt(t *testing.T) {
	g := newGateRig(t)
	g.onObjectStore()
	dir := g.dir(t, "Leon")
	g.file(t, "Leon/a.txt", "a")
	g.dir(t, "Baska")

	g.drv.Arm(true)
	done := gateServe(t, context.Background(), handlers.NewAI(g.store, g.resolve, nil, "").Delete,
		"/api/ai/delete", map[string]any{"path": "main://Leon"})
	gatetest.FencedHalfWay(t, g.st.ID, g.drv, []string{"/Leon", "/Leon/a.txt"}, "/Baska",
		func() bool { return g.inTrash(dir.ID) })

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, rowgate.Fences(g.st.ID).Empty())
}

// A file on an object store is one object: it holds the gate as before.
func TestRowGate_ExplorerDeleteOfAFileOnAnObjectStoreStillHoldsTheGate(t *testing.T) {
	g := newGateRig(t)
	g.onObjectStore()
	a := g.file(t, "a.txt", "a")

	g.drv.Arm(true)
	done := gateServe(t, context.Background(), g.mh.Mutate, "/api/files/manager?action=delete",
		map[string]any{"path": "main://", "items": []map[string]string{{"path": "main://a.txt"}}})
	gatetest.HeldHalfWay(t, g.st.ID, g.drv, func() bool { return g.inTrash(a.ID) })

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, rowgate.Fences(g.st.ID).Empty(), "a file delete set a fence")
}

// A folder restored from the trash on an object store comes back one object
// at a time (trash.TakeBack's walk): the request's restore fences the folder's
// trash key and its original path instead of holding the storage's gate.
//
// Break: hold rowgate.MoveCtx in Trash.restoreGated whatever RestoreFence
// says - the scan does not get the gate half way.
func TestRowGate_TrashRestoreOfAFolderOnAnObjectStoreFencesIt(t *testing.T) {
	g := newGateRig(t)
	g.onObjectStore()
	dir := g.dir(t, "Leon")
	g.file(t, "Leon/a.txt", "a")
	g.dir(t, "Baska")
	rec := callMutate(t, g.mh, "delete", map[string]any{
		"path": "main://", "items": []map[string]string{{"path": "main://Leon"}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, g.inTrash(dir.ID), "the folder did not go to the trash")

	th := handlers.NewTrash(trash.New(g.store, g.resolve, nil), g.store)
	g.drv.Arm(true)
	done := gateServe(t, context.Background(), th.Restore, "/api/files/manager/restore",
		map[string]any{"node_id": dir.ID})
	gatetest.FencedHalfWay(t, g.st.ID, g.drv, []string{"/Leon", "/Leon/a.txt"}, "/Baska", func() bool {
		n, err := g.store.GetNode(context.Background(), dir.ID)
		return err == nil && n != nil && n.DeletedAt == nil
	})

	rec = gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, rowgate.Fences(g.st.ID).Empty())
}
