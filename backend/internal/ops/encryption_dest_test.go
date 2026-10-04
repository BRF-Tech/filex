package ops_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
)

// Who may encrypt, when the queue runs a rename, a move or a copy onto a
// literal destination named like an encrypted folder's key file or a `.fxe`.
// The handler that queued it asked the rule about every source that was a
// FILE and lets a FOLDER through under any name; it tells the queue which
// sources it settled (ops.WithEncryptionSettled). A folder replaced by a file
// before the worker reaches the row would land as the name nobody was asked
// about, so the worker fails it: a source it was not told about that is a
// file by then. A restart forgets what it was told; the worker then asks the
// names half of the rule itself, runs what the names alone free and fails the
// rest the same way.

// runSubmitted runs the queue until op has finished, and returns it.
func runSubmitted(t *testing.T, f *opsFixture, op *ops.Op) *ops.Op {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.svc.Run(ctx)
	deadline := time.Now().Add(30 * time.Second)
	for {
		cur, err := f.svc.Get(context.Background(), op.ID)
		require.NoError(t, err)
		if cur.Status == ops.StatusOK || cur.Status == ops.StatusFailed || cur.Status == ops.StatusPartial {
			f.svc.Stop()
			return cur
		}
		if time.Now().After(deadline) {
			f.svc.Stop()
			t.Fatalf("op %d (%s) did not finish; last status=%s err=%s", op.ID, op.Kind, cur.Status, cur.Error)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

// replaceWithFile swaps the folder at rel for a plain file of that name, the
// way a person would between the queueing and the run: a delete and an
// upload.
func replaceWithFile(t *testing.T, f *opsFixture, rel string) {
	t.Helper()
	p := filepath.Join(f.drv.Root(), filepath.FromSlash(rel))
	require.NoError(t, os.RemoveAll(p))
	require.NoError(t, os.WriteFile(p, []byte(`{"v":2}`), 0o644))
}

func TestOpsWorker_AFolderTurnedFileIsNotGivenAnEncryptionName(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		kind, dest string
	}{
		{ops.OpRename, "x.fxe"},
		{ops.OpMove, "Hedef/.filex-e2e.json"},
		{ops.OpCopy, "kopya.fxe"},
	} {
		t.Run(c.kind, func(t *testing.T) {
			f := newOpsFixture(t)
			f.seedDir(t, "F")
			f.seedDir(t, "Hedef")
			// Queued while F was a folder: nothing settled.
			op, err := f.svc.Submit(ctx, c.kind, f.st.ID, []string{"F"}, c.dest)
			require.NoError(t, err)
			replaceWithFile(t, f, "F")

			got := runSubmitted(t, f, op)
			assert.Equal(t, ops.StatusFailed, got.Status, "a file landed on %s unasked: %s", c.dest, got.Error)
			assert.Contains(t, got.Error, "queue it again")
			_, err = os.Stat(filepath.Join(f.drv.Root(), filepath.FromSlash(c.dest)))
			assert.True(t, os.IsNotExist(err), "%s landed", c.dest)
			assert.FileExists(t, filepath.Join(f.drv.Root(), "F"), "the file left its place")
		})
	}
}

// What the handler settled runs as it always did — a file it asked the rule
// about, a `.fxe` that stays one — and so does a folder that is still a
// folder, and a source dropped INTO a folder under its own name.
func TestOpsWorker_WhatTheRuleSettledStillRuns(t *testing.T) {
	ctx := context.Background()
	f := newOpsFixture(t)
	f.seedFile(t, "rapor.bin", "plain")
	f.seedFile(t, "a.fxe", "cipher")
	f.seedFile(t, "b.fxe", "cipher")
	f.seedDir(t, "G")
	f.seedDir(t, "Alt")

	for _, c := range []struct {
		why, kind, src, dest string
		settled              bool
	}{
		{"a file the rule was asked about", ops.OpMove, "rapor.bin", "rapor.bin.fxe", true},
		{"a .fxe that stays a .fxe", ops.OpCopy, "a.fxe", "a2.fxe", true},
		{"a folder that is still a folder", ops.OpRename, "G", "g.fxe", false},
		{"a .fxe dropped into a folder under its own name", ops.OpMove, "b.fxe", "Alt/", false},
	} {
		qctx := ctx
		if c.settled {
			qctx = ops.WithEncryptionSettled(ctx, []string{c.src})
		}
		op, err := f.svc.Submit(qctx, c.kind, f.st.ID, []string{c.src}, c.dest)
		require.NoError(t, err, c.why)
		got := runSubmitted(t, f, op)
		require.Equal(t, ops.StatusOK, got.Status, "%s: %s", c.why, got.Error)
	}
	for _, rel := range []string{"rapor.bin.fxe", "a2.fxe", "g.fxe", "Alt/b.fxe"} {
		_, err := os.Stat(filepath.Join(f.drv.Root(), filepath.FromSlash(rel)))
		assert.NoError(t, err, "%s did not arrive", rel)
	}
}

// A restart forgets what the queue was told (the record lives in memory), and
// a row it carries on has been told nothing. Its names can still free it, and
// then it runs as it would have before the restart: a `.fxe` that stays a
// `.fxe` under any verb, a key file that stays its own folder's. Nothing is
// told here, which is what a restarted server's row looks like.
func TestOpsWorker_ARelocationTheNamesFreeRunsAfterARestart(t *testing.T) {
	ctx := context.Background()
	f := newOpsFixture(t)
	f.seedFile(t, "a.fxe", "cipher")
	f.seedFile(t, "b.fxe", "cipher")
	f.seedFile(t, "c.fxe", "cipher")
	f.seedDir(t, "Kasa")
	f.seedFile(t, "Kasa/.filex-e2e.json", `{"v":1}`)

	for _, c := range []struct{ why, kind, src, dest string }{
		{"a .fxe copied to a .fxe", ops.OpCopy, "a.fxe", "a2.fxe"},
		{"a .fxe moved to a .fxe", ops.OpMove, "b.fxe", "b2.fxe"},
		{"a .fxe renamed to a .fxe", ops.OpRename, "c.fxe", "c2.fxe"},
		{"a key file that stays its own folder's", ops.OpRename, "Kasa/.filex-e2e.json", "Kasa/.FILEX-E2E.JSON"},
	} {
		op, err := f.svc.Submit(ctx, c.kind, f.st.ID, []string{c.src}, c.dest)
		require.NoError(t, err, c.why)
		got := runSubmitted(t, f, op)
		require.Equal(t, ops.StatusOK, got.Status, "%s: %s", c.why, got.Error)
	}
	for _, rel := range []string{"a.fxe", "a2.fxe", "b2.fxe", "c2.fxe"} {
		_, err := os.Stat(filepath.Join(f.drv.Root(), filepath.FromSlash(rel)))
		assert.NoError(t, err, "%s is not there", rel)
	}
	for _, rel := range []string{"b.fxe", "c.fxe"} {
		_, err := os.Stat(filepath.Join(f.drv.Root(), filepath.FromSlash(rel)))
		assert.True(t, os.IsNotExist(err), "%s was moved, and is still there", rel)
	}
}

// The names half of the rule reads the storages too: a `.fxe` stays a `.fxe`
// on another storage, but a key file that lands on its own folder's path
// there is a new encryption, and a row nobody settled for it fails.
func TestOpsWorker_ACrossStorageRelocationAfterARestart(t *testing.T) {
	t.Run("a .fxe that stays a .fxe", func(t *testing.T) {
		f := newCrossFixture(t, nil)
		writeA(t, f, "a.fxe", "cipher")
		got := f.run(t, ops.OpCopy, []string{"a.fxe"}, "a.fxe")
		require.Equal(t, ops.StatusOK, got.Status, got.Error)
		assert.Equal(t, "cipher", readB(t, f, "a.fxe"))
	})
	t.Run("a key file onto its folder's path on another storage", func(t *testing.T) {
		f := newCrossFixture(t, nil)
		writeA(t, f, "Kasa/.filex-e2e.json", `{"v":1}`)
		require.NoError(t, os.MkdirAll(filepath.Join(f.rootB, "Kasa"), 0o755))
		got := f.run(t, ops.OpCopy, []string{"Kasa/.filex-e2e.json"}, "Kasa/.filex-e2e.json")
		assert.Equal(t, ops.StatusFailed, got.Status, "a key file landed on another storage unasked: %s", got.Error)
		assert.Contains(t, got.Error, "queue it again")
		_, err := os.Stat(filepath.Join(f.rootB, "Kasa", ".filex-e2e.json"))
		assert.True(t, os.IsNotExist(err), "the key file landed")
	})
}
