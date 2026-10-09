package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// sec055: the explorer's rename waits for the storage's row gate on its
// request, and asks whether the name is free again once it holds the gate.
// Up to 0.55's first cut it detached first, waited with no context, and moved
// on the answer it got before the wait: a file that landed on the name while
// a storage scan held the gate was replaced (every driver's Move replaces what
// holds the name), and a request whose client had gone renamed all the same.
//
// Break: in vfRename, drop the destinationTaken check inside the gated move
// (the late rename replaces the new file), or wait with rowgate.Change (the
// cancelled rename moves once the scan lets go).

func TestRowGate_ALateRenameDoesNotReplaceAFileThatTookItsName(t *testing.T) {
	g := newGateRig(t)
	g.file(t, "a.txt", "old")
	judged := rowgate.Judge(g.st.ID) // a storage scan judging the storage
	let := false
	letGo := func() {
		if !let {
			let = true
			judged()
		}
	}
	t.Cleanup(letGo)

	done := gateServe(t, context.Background(), g.mh.Mutate, "/api/files/manager?action=rename",
		map[string]any{"path": "main://", "item": "main://a.txt", "name": "b.txt"})
	// The rename found b.txt free and waits for the gate; a file lands on
	// the name meanwhile, bytes and row, as an upload does.
	time.Sleep(300 * time.Millisecond)
	g.file(t, "b.txt", "new")
	letGo()

	rec := gateAnswer(t, done)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.True(t, strings.Contains(rec.Body.String(), "name_taken"), rec.Body.String())
	require.Equal(t, "new", g.read(t, "b.txt"), "the late rename replaced the file that took its name")
	require.Equal(t, "old", g.read(t, "a.txt"), "the refused rename moved its item")
}

func TestRowGate_ARenameWhoseClientWentWhileItWaitedChangesNothing(t *testing.T) {
	g := newGateRig(t)
	row := g.file(t, "a.txt", "old")
	judged := rowgate.Judge(g.st.ID)
	let := false
	letGo := func() {
		if !let {
			let = true
			judged()
		}
	}
	t.Cleanup(letGo)

	ctx, cancel := context.WithCancel(context.Background())
	done := gateServe(t, ctx, g.mh.Mutate, "/api/files/manager?action=rename",
		map[string]any{"path": "main://", "item": "main://a.txt", "name": "b.txt"})
	time.Sleep(300 * time.Millisecond)
	cancel() // the tab is closed while the rename waits for the gate

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		letGo()
		t.Fatal("a rename whose client had gone kept waiting for the row gate")
	}
	letGo()
	// The gate is free, and nothing moved.
	release, ok := rowgate.TryJudge(g.st.ID)
	require.True(t, ok, "the abandoned rename holds the row gate")
	release()
	require.Equal(t, "old", g.read(t, "a.txt"), "the abandoned rename moved the file")
	n := g.rowAt("/a.txt")
	require.NotNil(t, n, "the abandoned rename moved the row")
	require.Equal(t, row.ID, n.ID)
}

// sec055: the agent's move to another storage (aiOps.moveAcross) does not
// start its copy while a judgement holds the source storage's gate, and waits
// for it on the caller's own context: a caller gone in the meantime copies
// and deletes nothing. It used to copy at once and then wait for the gate
// with no context at all, so the request stood in the source's gate for as
// long as the judgement held it, whether anyone was still waiting or not.
//
// Break: drop the rowgate.Await before the copy in moveAcross - the copy
// lands on the other storage and the request waits in rowgate.Move.
func TestRowGate_AnAIMoveAcrossStoragesWhoseCallerWentCopiesNothing(t *testing.T) {
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
	row := g.file(t, "Leon/a.txt", "a")

	judged := rowgate.Judge(g.st.ID) // a scan judging the source storage
	let := false
	letGo := func() {
		if !let {
			let = true
			judged()
		}
	}
	t.Cleanup(letGo)

	cctx, cancel := context.WithCancel(ctx)
	done := gateServe(t, cctx, handlers.NewAI(g.store, resolve, nil, "").Move,
		"/api/ai/move", map[string]any{"src": "main://Leon", "dst": "cold://Leo"})
	time.Sleep(300 * time.Millisecond)
	cancel() // the agent gives up while the source is being judged

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		letGo()
		t.Fatal("a move whose caller had gone kept waiting for the source's row gate")
	}
	letGo()
	_, err = os.Stat(filepath.Join(coldDir, "Leo", "a.txt"))
	require.True(t, os.IsNotExist(err), "the abandoned move copied to the other storage (%v)", err)
	require.Equal(t, "a", g.read(t, "Leon/a.txt"), "the abandoned move touched its source")
	n := g.rowAt("/Leon/a.txt")
	require.NotNil(t, n, "the abandoned move dropped the source's row")
	require.Equal(t, row.ID, n.ID)
}
