package ops_test

// Issue #127: a job put back for a scan of its storage says why it waits - the
// tray showed only "Queued" - in the reader's language, and its announcement
// to the storage's gate (rowgate.Want) ends as soon as it will not be needed:
// when the job takes the gate, when it is cancelled, and when it runs out.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// heldBack waits until op reads as put back for a scan (WaitReason) and
// returns the row as a reader of lang reads it.
func heldBack(t *testing.T, svc *ops.Service, id int64, lang string) *ops.Op {
	t.Helper()
	ctx := srvtext.WithReader(context.Background(), lang)
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := svc.Get(ctx, id)
		require.NoError(t, err)
		if op.WaitReason != "" {
			return op
		}
		require.Contains(t, []string{ops.StatusPending, ops.StatusRunning}, op.Status, "the job ended while its storage was being judged")
		require.False(t, time.Now().After(deadline), "the job never said it waits for the scan (status %s)", op.Status)
		time.Sleep(5 * time.Millisecond)
	}
}

// waitingOn is how many changes wait for storageID's gate now (Snapshot).
func waitingOn(storageID int64) int {
	for _, s := range rowgate.Snapshot() {
		if s.StorageID == storageID {
			return s.Waiting
		}
	}
	return 0
}

// Break: drop attachWait from Get - the row reads as a bare pending one.
func TestOpsWorker_AJobHeldBackByAScanSaysWhyInTheReadersWords(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "Klasor")
	ctx := context.Background()
	directory := rowgate.Judge(f.st.ID) // one long directory of a scan
	let := false
	letGo := func() {
		if !let {
			let = true
			directory()
		}
	}
	t.Cleanup(letGo)

	op, err := f.svc.Submit(ctx, ops.OpDelete, f.st.ID, []string{"Klasor"}, "")
	require.NoError(t, err)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)

	tr := heldBack(t, f.svc, op.ID, "tr")
	assert.Equal(t, ops.StatusPending, tr.Status)
	assert.Equal(t, ops.WaitStorageScan, tr.WaitReason)
	assert.Equal(t, "Sırasını bekliyor: depo taranıyor", tr.WaitText)
	require.NotNil(t, tr.WaitingSince, "the row does not say since when it waits")
	assert.WithinDuration(t, time.Now(), *tr.WaitingSince, 10*time.Second)
	en := heldBack(t, f.svc, op.ID, "en")
	assert.Equal(t, "Waiting for its turn: the storage is being scanned", en.WaitText)
	assert.Equal(t, ops.SayWait("en", ops.WaitStorageScan), en.WaitText)

	// The lane does not claim the row again while the scan holds the gate
	// (#282), so the list reads it queued, with its reason, at any moment.
	rows, err := f.svc.List(srvtext.WithReader(ctx, "tr"), "")
	require.NoError(t, err)
	var listed *ops.Op
	for _, r := range rows {
		if r.ID == op.ID {
			listed = r
		}
	}
	require.NotNil(t, listed, "the job is not in the list")
	assert.Equal(t, ops.StatusPending, listed.Status)
	assert.Equal(t, "Sırasını bekliyor: depo taranıyor", listed.WaitText, "the list (the tray's poll) does not say why the job waits")

	// The directory ends: the job runs at once, not at the worker's next
	// tick, and the row no longer says it waits.
	letGo()
	done := waitForOpWithin(t, f.svc, op.ID, 3*time.Second)
	require.Equal(t, ops.StatusOK, done.Status, "the delete: %s", done.Error)
	assert.Empty(t, done.WaitReason)
	assert.Empty(t, done.WaitText)
	assert.Nil(t, done.WaitingSince)
	assert.Zero(t, waitingOn(f.st.ID), "the job's announcement outlived the job")
	f.svc.Stop()
}

// Break: drop forgetHeld from Cancel - the cancelled job keeps the storage's
// scan back until its announcement runs out.
func TestOpsWorker_ACancelledJobCallsItsAnnouncementOff(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "Klasor")
	ctx := context.Background()
	directory := rowgate.Judge(f.st.ID)
	t.Cleanup(directory)

	op, err := f.svc.Submit(ctx, ops.OpDelete, f.st.ID, []string{"Klasor"}, "")
	require.NoError(t, err)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	heldBack(t, f.svc, op.ID, "en")
	require.NotZero(t, waitingOn(f.st.ID), "the job put back was not announced to its storage's gate")

	// One ask is enough: a job its lane is claiming at this instant is
	// cancelled as well (#282, cancel_claim_test.go).
	ok, err := f.svc.Cancel(ctx, op.ID)
	require.NoError(t, err)
	require.True(t, ok, "the job held back for the scan could not be cancelled")
	deadline := time.Now().Add(2 * time.Second)
	for waitingOn(f.st.ID) != 0 {
		require.False(t, time.Now().After(deadline), "a cancelled job still keeps the storage's scan back")
		time.Sleep(5 * time.Millisecond)
	}
	cur, err := f.svc.Get(ctx, op.ID)
	require.NoError(t, err)
	assert.Equal(t, ops.StatusCancelled, cur.Status)
	assert.Empty(t, cur.WaitReason, "a cancelled job still says it waits")
	f.svc.Stop()
}

// A job whose storage stays judged longer than its announcement lasts (one
// huge directory) stops keeping the scan back when the announcement runs out,
// and still runs as soon as that judgement lets go.
//
// Break: wait for the gate in announce's watcher only as long as the
// announcement lasts - the job is left to the 5 s tick.
func TestOpsWorker_AnAnnouncementRunsOutAndTheJobStillRunsWhenTheScanLetsGo(t *testing.T) {
	defer ops.SetTurnFor(200 * time.Millisecond)()
	f := newOpsFixture(t)
	f.seedDir(t, "Klasor")
	ctx := context.Background()
	directory := rowgate.Judge(f.st.ID)
	let := false
	letGo := func() {
		if !let {
			let = true
			directory()
		}
	}
	t.Cleanup(letGo)
	lapsed := func() uint64 {
		for _, s := range rowgate.Snapshot() {
			if s.StorageID == f.st.ID {
				return s.LapsedWants
			}
		}
		return 0
	}
	before := lapsed()

	op, err := f.svc.Submit(ctx, ops.OpDelete, f.st.ID, []string{"Klasor"}, "")
	require.NoError(t, err)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	heldBack(t, f.svc, op.ID, "en")
	deadline := time.Now().Add(3 * time.Second)
	for lapsed() == before {
		require.False(t, time.Now().After(deadline), "the job's announcement never ran out")
		time.Sleep(5 * time.Millisecond)
	}

	letGo()
	done := waitForOpWithin(t, f.svc, op.ID, 3*time.Second)
	require.Equal(t, ops.StatusOK, done.Status, "the delete: %s", done.Error)
	f.svc.Stop()
}
