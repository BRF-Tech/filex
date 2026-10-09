package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/drafts"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/testutil/gatetest"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// The row gate on the explorer's and the agent's own write paths (issue
// #201, after #192).
//
// A move, a delete into the trash and a restore from it change the storage
// first and the catalogue after. Between the two a storage scan judges a row
// whose bytes have already gone: it confirms it gone by a Stat of its old path
// and drops it, or catalogues the bytes at their new place as a NEW file. The
// queue and the explorer's rename and move have held the storage's row gate
// (internal/rowgate) since 0.54; the agent's move and delete, the explorer's
// delete, the drafts area's save and the trash's restore did their two steps
// bare. Each test below stops its verb right after the bytes changed
// (gatetest.Driver) and asks for the gate the way a scan does
// (gatetest.HeldHalfWay). The gate is process-wide: no t.Parallel here.

// gateRig is the mutate fixture with its driver caught half way.
type gateRig struct {
	*renameRig
	drv *gatetest.Driver
	// served is what the rig's resolver hands out for its storage: drv
	// itself, or drv's view of a storage with no trash (withoutTrash).
	served storage.Driver
}

func newGateRig(t *testing.T) *gateRig {
	t.Helper()
	_, store, base, st, root := newMutateFixture(t)
	drv := gatetest.Wrap(base)
	drv.Arm(false)
	t.Cleanup(drv.Finish)
	g := &gateRig{drv: drv, served: drv}
	g.renameRig = &renameRig{mh: handlers.NewManager(store, g.resolve), store: store, st: st, root: root}
	return g
}

func (g *gateRig) resolve(id int64) (storage.Driver, error) {
	if id != g.st.ID {
		return nil, fmt.Errorf("unknown storage %d", id)
	}
	return g.served, nil
}

// withoutTrash puts the rig's storage on a driver with no Move and no Copy
// (gatetest's NoTrash view): a delete there cannot be kept and is for good.
// Its Delete still stops half way, on g.drv.
func (g *gateRig) withoutTrash() { g.served = g.drv.NoTrash() }

// draftOf seeds a draft of doc.txt, meant for Docs, for a new account: its
// file in the account's drafts area, the rows a sync would have made for it,
// and the draft's record. It answers the file's row, the draft's key and a
// request context carrying the account and the key as the router would.
func (g *gateRig) draftOf(t *testing.T) (*model.Node, string, context.Context) {
	t.Helper()
	ctx := context.Background()
	u, err := g.store.CreateUser(ctx, "drafts@gate.test", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	key := drafts.NewKey()
	dir := syspath.DraftDir(u.ID, key)
	g.dir(t, syspath.Drafts)
	g.dir(t, syspath.Drafts+"/"+strconv.FormatInt(u.ID, 10))
	g.dir(t, dir)
	doc := g.file(t, dir+"/doc.txt", "draft")
	g.dir(t, "Docs")
	_, err = g.store.CreateDraft(ctx, &model.Draft{
		Key: key, UserID: u.ID, StorageID: g.st.ID, NodeID: doc.ID,
		TargetDir: "Docs", TargetName: "doc.txt", DocType: "txt",
	})
	require.NoError(t, err)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("key", key)
	return doc, key, context.WithValue(auth.WithUser(ctx, u), chi.RouteCtxKey, rctx)
}

func (g *gateRig) rowAt(p string) *model.Node {
	n, err := g.store.GetNodeByPath(context.Background(), g.st.ID, pathkey.Hash(g.st.ID, p))
	if err != nil {
		return nil
	}
	return n
}

// inTrash reports whether row id is a trash entry now.
func (g *gateRig) inTrash(id int64) bool {
	n, err := g.store.GetNode(context.Background(), id)
	return err == nil && n != nil && n.DeletedAt != nil
}

// gateServe runs one request in the background - it stops half way, so the
// test goroutine is free to ask for the gate - and hands its answer back.
func gateServe(t *testing.T, ctx context.Context, handle func(http.ResponseWriter, *http.Request), target string, body any) <-chan *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(raw)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		handle(rec, req)
		done <- rec
	}()
	return done
}

func gateAnswer(t *testing.T, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case rec := <-done:
		return rec
	case <-time.After(10 * time.Second):
		t.Fatal("the request never answered")
		return nil
	}
}

// The agent's move (POST /api/ai/move, MCP file_move) of a folder holds the
// gate until the folder's rows have followed its bytes: a scan half way would
// see the folder at its new name with its rows still at the old one.
//
// Break: take rowgate.Change out of aiOps.Move - the scan's judgement gets the
// gate while Leo/a.txt has no row yet.
func TestRowGate_AIMoveHoldsTheGateUntilTheRowsFollow(t *testing.T) {
	g := newGateRig(t)
	g.dir(t, "Leon")
	g.file(t, "Leon/a.txt", "a")

	g.drv.Arm(true)
	done := gateServe(t, context.Background(), handlers.NewAI(g.store, g.resolve, nil, "").Move,
		"/api/ai/move", map[string]any{"src": "main://Leon", "dst": "main://Leo"})
	gatetest.HeldHalfWay(t, g.st.ID, g.drv, func() bool { return g.rowAt("/Leo/a.txt") != nil })

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// The agent's delete (POST /api/ai/delete, MCP file_delete) holds the gate
// until the row is in the trash: a scan half way would confirm the live row
// gone and drop it, and the trash entry with it.
//
// Break: take rowgate.Move out of aiOps.Delete.
func TestRowGate_AIDeleteHoldsTheGateUntilTheRowIsInTheTrash(t *testing.T) {
	g := newGateRig(t)
	a := g.file(t, "a.txt", "a")

	g.drv.Arm(true)
	done := gateServe(t, context.Background(), handlers.NewAI(g.store, g.resolve, nil, "").Delete,
		"/api/ai/delete", map[string]any{"path": "main://a.txt"})
	gatetest.HeldHalfWay(t, g.st.ID, g.drv, func() bool { return g.inTrash(a.ID) })

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// The explorer's delete (?action=delete, vfDelete) holds the gate per item
// until the row is in the trash.
//
// Break: take rowgate.Move out of Manager.deleteItem.
func TestRowGate_ExplorerDeleteHoldsTheGateUntilTheRowIsInTheTrash(t *testing.T) {
	g := newGateRig(t)
	a := g.file(t, "a.txt", "a")

	g.drv.Arm(true)
	done := gateServe(t, context.Background(), g.mh.Mutate, "/api/files/manager?action=delete",
		map[string]any{"path": "main://", "items": []map[string]string{{"path": "main://a.txt"}}})
	gatetest.HeldHalfWay(t, g.st.ID, g.drv, func() bool { return g.inTrash(a.ID) })

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// Saving a draft (POST /api/files/drafts/{key}/save) moves its file out of the
// drafts area and holds the gate until the draft's own row has followed: a
// scan half way would catalogue the document at its place as a NEW file, and
// the draft's row - its id, the one an open editor saves by - would have to
// push that one out to land.
//
// Break: take rowgate.Change out of Manager.SaveDraft.
func TestRowGate_DraftSaveHoldsTheGateUntilTheDraftsRowFollows(t *testing.T) {
	g := newGateRig(t)
	doc, key, reqCtx := g.draftOf(t)

	g.drv.Arm(true)
	done := gateServe(t, reqCtx, g.mh.SaveDraft, "/api/files/drafts/"+key+"/save", map[string]any{})
	gatetest.HeldHalfWay(t, g.st.ID, g.drv, func() bool {
		n := g.rowAt("/Docs/doc.txt")
		return n != nil && n.ID == doc.ID
	})

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// A restore from the trash answered at once (POST /api/files/manager/restore
// without queued=1) holds the gate until the row is live again, as a queued
// one does (ops.runRestore): a scan half way would find the bytes back at
// their path with no live row and catalogue them as a NEW file.
//
// Break: take rowgate.Move out of Trash.restoreGated.
func TestRowGate_TrashRestoreHoldsTheGateUntilTheRowIsLive(t *testing.T) {
	g := newGateRig(t)
	a := g.file(t, "a.txt", "a")
	rec := callMutate(t, g.mh, "delete", map[string]any{
		"path": "main://", "items": []map[string]string{{"path": "main://a.txt"}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, g.inTrash(a.ID), "the file did not go to the trash")

	th := handlers.NewTrash(trash.New(g.store, g.resolve, nil), g.store)
	g.drv.Arm(true)
	done := gateServe(t, context.Background(), th.Restore, "/api/files/manager/restore",
		map[string]any{"node_id": a.ID})
	gatetest.HeldHalfWay(t, g.st.ID, g.drv, func() bool {
		n, err := g.store.GetNode(context.Background(), a.ID)
		return err == nil && n != nil && n.DeletedAt == nil
	})

	rec = gateAnswer(t, done)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
