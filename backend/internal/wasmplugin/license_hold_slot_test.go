package wasmplugin

// A hold that arrives while a job or a screen event waits for one of the
// app's slots stops it once the slot is free: running() is asked again with
// the slot held (store review, second round, Y1; lesson #1069).

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// A job passes running() and then waits for one of the app's job slots
// (other jobs of the app are running). The hold arrives while it waits.
// Does it still run once a slot frees?
func TestLicenseHold_AJobWaitingForASlotDoesNotRunAfterTheHold(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "docs/a.txt", "hello")
	filled := 0
	for len(p.sem) < cap(p.sem) {
		p.sem <- struct{}{}
		filled++
	}
	pj, _ := json.Marshal([]string{"docs/a.txt"})
	job := &model.AppPluginJob{ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: "upper",
		StorageID: h.st.ID, PathsJSON: string(pj), ParamsJSON: "{}", Locale: "en", Label: "x", Status: model.AppPluginJobPending}
	require.NoError(t, h.store.CreateAppPluginJob(context.Background(), job))
	done := make(chan error, 1)
	go func() {
		done <- h.reg.RunPluginAction(context.Background(), &ops.Op{ID: 1, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: []string{"docs/a.txt"}, Dest: job.ID}, nil)
	}()
	time.Sleep(300 * time.Millisecond)
	h.reg.SetLicenseHold("echo", "license: revoked")
	st, _ := p.State()
	require.Equal(t, StateUnlicensed, st)
	<-p.sem
	err := <-done
	for i := 1; i < filled; i++ {
		<-p.sem
	}
	got, _ := h.store.GetAppPluginJob(context.Background(), job.ID)
	t.Logf("slots=%d err=%v status=%s", filled, err, got.Status)
	if err == nil {
		t.Errorf("a job that waited for a slot ran AFTER the hold (status=%s)", got.Status)
	}
}

// The same for a screen event waiting (at most callQueueWait) for a call slot.
func TestLicenseHold_AScreenWaitingForACallSlotDoesNotRunAfterTheHold(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	if p.calls == nil || cap(p.calls) == 0 {
		t.Skip("no call slots")
	}
	filled := 0
	for len(p.calls) < cap(p.calls) {
		p.calls <- struct{}{}
		filled++
	}
	type res struct {
		s   *wire.Surface
		err error
	}
	done := make(chan res, 1)
	go func() {
		s, err := h.reg.ViewEvent(context.Background(), "echo", "hello", 0, nil, nil, "en", wire.ViewEventInput{Event: "open"})
		done <- res{s, err}
	}()
	time.Sleep(300 * time.Millisecond)
	h.reg.SetLicenseHold("echo", "license: revoked")
	<-p.calls
	r := <-done
	for i := 1; i < filled; i++ {
		<-p.calls
	}
	t.Logf("call slots=%d err=%v", filled, r.err)
	if r.err == nil {
		t.Errorf("a screen event that waited for a call slot ran AFTER the hold")
	}
}

// put (a new entry: an install, an upgrade) and SetLicenseHold racing: the
// entry ends with the hold the registry has. put used to read the hold,
// publish the entry and set the hold after unlocking, so a hold set in
// between was overwritten with the old one (store review, second round, Y5).
func TestLicenseHold_APutRacingAHoldKeepsTheHold(t *testing.T) {
	lost := 0
	for i := 0; i < 50000; i++ {
		r := &Registry{byID: map[int64]*Installed{}, byName: map[string]*Installed{}, holds: map[string]string{}}
		p := &Installed{Row: &model.AppPlugin{ID: 1, Name: "paid"}, logs: &logRing{}}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; r.put(p) }()
		go func() { defer wg.Done(); <-start; r.SetLicenseHold("paid", "license: revoked") }()
		close(start)
		wg.Wait()
		if p.holdReason() != r.LicenseHold("paid") {
			lost++
		}
	}
	if lost > 0 {
		t.Fatalf("in %d of 50000 races the entry's hold differs from the registry's", lost)
	}
}
