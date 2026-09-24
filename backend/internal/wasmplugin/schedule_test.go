package wasmplugin

// Tests for the hourly wake-up and the work it schedules (schedule.go).
//
// Every test drives the pass with a SYNTHETIC clock — RunDueSchedule takes
// `now` — so nothing here depends on what hour it happens to be. A test that
// scheduled work "two minutes from now" would pass all day and fail at :58,
// when two minutes lands past the window boundary.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// tickAt is the synthetic wall clock every pass below runs at: the top of an
// hour, so the window is the whole hour after it and an item ten minutes out
// is unambiguously inside it.
var tickAt = time.Date(2030, 1, 2, 10, 0, 0, 0, time.UTC)

// ── fake queue ─────────────────────────────────────────────────────────

type queued struct {
	Kind      string
	StorageID int64
	Sources   []string
	Dest      string // the app_plugin_jobs id
}

type fakeQueue struct {
	mu   sync.Mutex
	subs []queued
	err  error
	next int64
}

func (q *fakeQueue) SubmitTo(_ context.Context, kind string, storageID, _ int64, sources []string, dest string) (*ops.Op, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return nil, q.err
	}
	q.next++
	q.subs = append(q.subs, queued{Kind: kind, StorageID: storageID, Sources: sources, Dest: dest})
	return &ops.Op{ID: q.next, Kind: kind, StorageID: storageID, Sources: sources, Dest: dest}, nil
}

func (q *fakeQueue) all() []queued {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]queued(nil), q.subs...)
}

// ── harness helpers ────────────────────────────────────────────────────

// scheduleSettings are the three fields the echo fixture's tick reads. They
// are added to the manifest per test rather than shipped in it, because the
// base fixture must stay an app that was NEVER granted a wake-up.
func scheduleManifest(m map[string]any) {
	perms, _ := m["permissions"].([]any)
	m["permissions"] = append(perms, "schedule")
	m["settings"] = []map[string]any{
		{"key": "tick_mode", "type": "string", "label": "Tick mode"},
		{"key": "tick_path", "type": "string", "label": "Tick path"},
		{"key": "tick_due_in_s", "type": "string", "label": "Due in seconds"},
	}
}

// installScheduled installs echo as an app that HAS the schedule permission,
// wires a fake ops queue, and sets the settings that steer its tick.
func (h *harness) installScheduled(t *testing.T, settings map[string]string, extra func(map[string]any)) (*Installed, *fakeQueue) {
	t.Helper()
	st, _, err := h.reg.Install(context.Background(), echoInput(t, func(m map[string]any) {
		scheduleManifest(m)
		if extra != nil {
			extra(m)
		}
	}))
	require.NoError(t, err)
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)
	require.NoError(t, h.reg.PutSettings(context.Background(), st.ID, settings))
	q := &fakeQueue{}
	h.reg.SetJobQueue(q)
	return p, q
}

// row returns one schedule row of a plugin, or nil.
func (h *harness) row(t *testing.T, p *Installed, key string) *model.AppPluginScheduleItem {
	t.Helper()
	rows, err := h.reg.ScheduleOf(context.Background(), p.Row.ID)
	require.NoError(t, err)
	for _, it := range rows {
		if it.Key == key {
			return it
		}
	}
	return nil
}

func (h *harness) rows(t *testing.T, p *Installed) []*model.AppPluginScheduleItem {
	t.Helper()
	rows, err := h.reg.ScheduleOf(context.Background(), p.Row.ID)
	require.NoError(t, err)
	return rows
}

// wake runs one pass at the synthetic clock and returns how many rows ran.
func (h *harness) pass(t *testing.T, at time.Time) int {
	t.Helper()
	n, err := h.reg.RunDueSchedule(context.Background(), at)
	require.NoError(t, err)
	return n
}

// ── who gets woken ─────────────────────────────────────────────────────

func TestSchedule_AnAppThatAsksIsWoken(t *testing.T) {
	h := newHarness(t, nil)
	p, _ := h.installScheduled(t, map[string]string{"tick_mode": "quiet"}, nil)

	// Installing armed the wake-up, and nothing else.
	rows := h.rows(t, p)
	require.Len(t, rows, 1)
	assert.Equal(t, model.AppPluginScheduleWakeupKey, rows[0].Key)
	assert.Equal(t, model.AppPluginScheduleDue, rows[0].Status)
	assert.True(t, h.reg.StatusOf(p).Scheduled, "the admin list says this app is woken")

	// The wake-up runs, the app says it has nothing to do, and the row is
	// re-armed onto the next hour rather than firing again.
	assert.Equal(t, 1, h.pass(t, tickAt))
	wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
	require.NotNil(t, wake)
	assert.Equal(t, model.AppPluginScheduleDue, wake.Status)
	assert.Equal(t, tickAt.Add(time.Hour), wake.DueAt.UTC(), "re-armed onto the hour, not an hour from now")
	assert.Contains(t, LocalizeNote(wake.Error, "en"), "nothing to do", "the app's own note is kept where an admin can read it")
	assert.Contains(t, LocalizeNote(wake.Error, "en"), "0 scheduled")
	// ...in the reader's language: the app said it in Turkish too, and the
	// host's tally follows it (2026-09-21: the Turkish admin panel read
	// "nothing due … — 0 scheduled").
	assert.Equal(t, "nothing to do — 0 scheduled", LocalizeNote(wake.Error, "en"))
	assert.Equal(t, "yapacak iş yok — 0 planlandı", LocalizeNote(wake.Error, "tr"))
	assert.Equal(t, "the plugin trapped", LocalizeNote("the plugin trapped", "tr"), "a plain note (an error) is read as it is")
	assert.Empty(t, wake.ClaimedBy, "re-arming releases the lease, or no other process could ever take it")

	// A second pass at the same moment does nothing: the row is not due again.
	assert.Equal(t, 0, h.pass(t, tickAt))
}

func TestSchedule_AnAppThatDoesNotAskIsNeverWoken(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t) // the plain fixture: no `schedule` permission

	assert.Empty(t, h.rows(t, p), "no wake-up was armed, so there is nothing to come due")
	assert.False(t, h.reg.StatusOf(p).Scheduled)
	assert.Equal(t, 0, h.pass(t, tickAt))

	// And the door is shut from the other side too: asking the registry to
	// tick it directly is refused, so a future caller cannot route round the
	// grant by accident.
	_, err := h.reg.Tick(context.Background(), p, tickAt, tickAt.Add(time.Hour))
	require.Error(t, err)
	assert.True(t, IsCode(err, CodeRefused), err)
	assert.Contains(t, err.Error(), "schedule permission")
}

// ── the point of the whole thing: to the minute, exactly once ──────────

func TestSchedule_DueItemRunsAtItsTimeAndExactlyOnce(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, q := h.installScheduled(t, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, nil)

	h.pass(t, tickAt)
	item := h.row(t, p, "due")
	require.NotNil(t, item, "the wake-up scheduled one piece of work")
	assert.Equal(t, model.AppPluginScheduleDue, item.Status)
	assert.Equal(t, tickAt.Add(10*time.Minute), item.DueAt.UTC(), "to the minute the app named")
	assert.Equal(t, "expire", item.ActionID)
	assert.Equal(t, h.st.ID, item.StorageID, "the adapter-qualified path was resolved to a storage")
	assert.JSONEq(t, `["docs/contract.txt"]`, item.PathsJSON)

	// NOT EARLY: a pass nine minutes in leaves it alone.
	assert.Equal(t, 0, h.pass(t, tickAt.Add(9*time.Minute)))
	assert.Empty(t, q.all())

	// On time: it becomes an ordinary plugin job on the ops queue.
	assert.Equal(t, 1, h.pass(t, tickAt.Add(10*time.Minute)))
	subs := q.all()
	require.Len(t, subs, 1)
	assert.Equal(t, ops.OpPluginAction, subs[0].Kind)
	assert.Equal(t, []string{"docs/contract.txt"}, subs[0].Sources)

	item = h.row(t, p, "due")
	require.NotNil(t, item)
	assert.Equal(t, model.AppPluginScheduleQueued, item.Status)
	assert.Equal(t, subs[0].Dest, item.JobID)
	assert.Equal(t, 1, item.Attempts)
	assert.Equal(t, h.reg.InstanceID(), item.ClaimedBy, "which node ran it is on the row")

	// EXACTLY ONCE: later passes find nothing, however often they run.
	for _, at := range []time.Duration{11 * time.Minute, 30 * time.Minute, 59 * time.Minute} {
		assert.Equal(t, 0, h.pass(t, tickAt.Add(at)))
	}
	assert.Len(t, q.all(), 1)

	// The job row the queue was handed is a real one, and it belongs to
	// nobody: unattended work is SYSTEM, not somebody's name.
	job, err := h.store.GetAppPluginJob(context.Background(), subs[0].Dest)
	require.NoError(t, err)
	assert.Equal(t, "expire", job.ActionID)
	assert.Equal(t, model.AppPluginJobPending, job.Status)
	assert.Nil(t, job.ActorID, "a scheduled job has no actor")
	assert.Equal(t, "Expire (scheduled)", job.Label)

	// And running it does the work: the same runner a person's job uses.
	require.NoError(t, h.reg.RunPluginAction(context.Background(),
		&ops.Op{ID: 1, Kind: ops.OpPluginAction, StorageID: h.st.ID, Sources: subs[0].Sources, Dest: job.ID}, nil))
	job, err = h.store.GetAppPluginJob(context.Background(), job.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AppPluginJobOK, job.Status)
	assert.Contains(t, job.Message, "expired contract.txt")
	assert.Contains(t, job.Message, "|due|", "the item's params reached the action")
	assert.Contains(t, job.Message, "actor=0")
}

func TestSchedule_SurvivesARestart_AndTwoProcessesRunItOnce(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, q1 := h.installScheduled(t, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, nil)
	h.pass(t, tickAt)
	require.NotNil(t, h.row(t, p, "due"))
	// From here the app has nothing new to ask for, so the wake-up the
	// second process arms at boot cannot move the item this test is about.
	require.NoError(t, h.reg.PutSettings(context.Background(), p.Row.ID, map[string]string{"tick_mode": "quiet"}))

	// The process goes away BEFORE the item is due — the 03:00 case.
	h.reg.Close(context.Background())

	// A second process comes up on the same database and module directory.
	reg2, err := New(h.reg.opts)
	require.NoError(t, err)
	defer reg2.Close(context.Background())
	require.NoError(t, reg2.Load(context.Background()))
	q2 := &fakeQueue{}
	reg2.SetJobQueue(q2)
	reg2.SetOutputSink(h.sink)
	p2, ok := reg2.ByName("echo")
	require.True(t, ok)

	// The work it never saw scheduled is still there, and still due.
	rows, err := reg2.ScheduleOf(context.Background(), p2.Row.ID)
	require.NoError(t, err)
	var found *model.AppPluginScheduleItem
	for _, it := range rows {
		if it.Key == "due" {
			found = it
		}
	}
	require.NotNil(t, found, "the schedule survived the restart")
	assert.Equal(t, model.AppPluginScheduleDue, found.Status)

	// Late rather than never: the new process runs it when it comes back,
	// an hour after it was due.
	n, err := reg2.RunDueSchedule(context.Background(), tickAt.Add(70*time.Minute))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, 1)
	assert.Len(t, q2.all(), 1, "and runs it once")

	// The lease is what stops the second run: claiming an already-claimed
	// row answers false, whoever asks.
	again, err := h.store.ClaimAppPluginScheduleItem(context.Background(), p2.Row.ID, "due", "someone-else", tickAt.Add(90*time.Minute))
	require.NoError(t, err)
	assert.False(t, again, "a row that is not `due` cannot be claimed twice")
	assert.Empty(t, q1.all(), "the process that scheduled it never ran it")
}

// A later wake-up MOVES an item it names again rather than adding a second
// one. That is what makes the key an idempotency key, and it is also how an
// app changes its mind: a signature request extended by an hour is the same
// envelope, closing later, not two closures.
func TestSchedule_NamingAKeyAgainMovesTheItemRatherThanAddingASecond(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, q := h.installScheduled(t, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, nil)

	ctx := context.Background()
	h.pass(t, tickAt)
	require.Equal(t, tickAt.Add(10*time.Minute), h.row(t, p, "due").DueAt.UTC())

	// A SECOND wake-up while that item is still pending — which is what a
	// restart five minutes in causes, since a load arms one. This time the
	// app wants the same work half an hour out instead of ten minutes.
	require.NoError(t, h.reg.PutSettings(ctx, p.Row.ID, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "1800",
	}))
	require.NoError(t, h.store.PutAppPluginScheduleItem(ctx, &model.AppPluginScheduleItem{
		PluginID: p.Row.ID, Key: model.AppPluginScheduleWakeupKey,
		DueAt: tickAt.Add(5 * time.Minute), Status: model.AppPluginScheduleDue,
	}))
	assert.Equal(t, 1, h.pass(t, tickAt.Add(5*time.Minute)))

	assert.Len(t, h.rows(t, p), 2, "the wake-up and ONE item, not two items")
	assert.Equal(t, tickAt.Add(35*time.Minute), h.row(t, p, "due").DueAt.UTC(), "moved, not duplicated")

	// The old time is gone: nothing fires at 10:10 any more.
	assert.Equal(t, 0, h.pass(t, tickAt.Add(10*time.Minute)))
	assert.Empty(t, q.all())

	assert.Equal(t, 1, h.pass(t, tickAt.Add(35*time.Minute)))
	assert.Len(t, q.all(), 1, "and it runs once, at the time that replaced the first")
}

func TestSchedule_TwoProcessesRacingRunADueItemOnce(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, q1 := h.installScheduled(t, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, nil)
	h.pass(t, tickAt)
	require.NotNil(t, h.row(t, p, "due"))
	require.NoError(t, h.reg.PutSettings(context.Background(), p.Row.ID, map[string]string{"tick_mode": "quiet"}))

	reg2, err := New(h.reg.opts)
	require.NoError(t, err)
	defer reg2.Close(context.Background())
	require.NoError(t, reg2.Load(context.Background()))
	q2 := &fakeQueue{}
	reg2.SetJobQueue(q2)
	assert.NotEqual(t, h.reg.InstanceID(), reg2.InstanceID(), "two processes, two names")

	at := tickAt.Add(10 * time.Minute)
	var wg sync.WaitGroup
	for _, reg := range []*Registry{h.reg, reg2} {
		wg.Add(1)
		go func(reg *Registry) {
			defer wg.Done()
			_, _ = reg.RunDueSchedule(context.Background(), at)
		}(reg)
	}
	wg.Wait()

	assert.Len(t, append(q1.all(), q2.all()...), 1,
		"two filex processes on one database queue the same due item exactly once")
}

// ── containment ────────────────────────────────────────────────────────

func TestSchedule_ATrappingTickIsContainedAndNotRetriedInsideTheHour(t *testing.T) {
	h := newHarness(t, nil)
	p, q := h.installScheduled(t, map[string]string{"tick_mode": "crash"}, nil)

	assert.Equal(t, 1, h.pass(t, tickAt), "the pass completes; the trap does not take it down")
	wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
	require.NotNil(t, wake)
	assert.Equal(t, model.AppPluginScheduleDue, wake.Status, "re-armed, not left stuck at running")
	assert.Equal(t, tickAt.Add(time.Hour), wake.DueAt.UTC())
	assert.NotEmpty(t, wake.Error, "the failure is written where an admin looks")
	assert.NotContains(t, LocalizeNote(wake.Error, "en"), "wazero", "no runtime internals on an admin's screen")
	assert.Empty(t, q.all())

	// NOT RETRIED inside the hour: passes a minute and half an hour later
	// find nothing to do. The next wake-up is the retry.
	assert.Equal(t, 0, h.pass(t, tickAt.Add(time.Minute)))
	assert.Equal(t, 0, h.pass(t, tickAt.Add(30*time.Minute)))

	// The plugin is still running, and still answers other calls.
	state, serr := p.State()
	assert.Equal(t, StateRunning, state, serr)

	// An hour on it is asked again — once.
	assert.Equal(t, 1, h.pass(t, tickAt.Add(time.Hour)))
	wake = h.row(t, p, model.AppPluginScheduleWakeupKey)
	assert.Equal(t, 2, wake.Attempts)
	assert.Equal(t, tickAt.Add(2*time.Hour), wake.DueAt.UTC())

	// An app that returns an error rather than trapping gets its OWN words
	// onto the row: "the plugin crashed" tells an administrator nothing.
	require.NoError(t, h.reg.PutSettings(context.Background(), p.Row.ID, map[string]string{"tick_mode": "boom"}))
	assert.Equal(t, 1, h.pass(t, tickAt.Add(2*time.Hour)))
	assert.Contains(t, h.row(t, p, model.AppPluginScheduleWakeupKey).Error, "the plugin said no")

	// And it heals by itself: a fixed app is simply woken again next hour.
	require.NoError(t, h.reg.PutSettings(context.Background(), p.Row.ID, map[string]string{"tick_mode": "quiet"}))
	assert.Equal(t, 1, h.pass(t, tickAt.Add(3*time.Hour)))
	assert.Contains(t, h.row(t, p, model.AppPluginScheduleWakeupKey).Error, "nothing to do")
}

func TestSchedule_ASlowTickIsCutOffByItsBudget(t *testing.T) {
	h := newHarness(t, nil)
	// A one-second ceiling, so the test does not sit through the default.
	p, q := h.installScheduled(t, map[string]string{"tick_mode": "slow"}, func(m map[string]any) {
		m["limits"] = map[string]any{"call_timeout_s": 1}
	})
	assert.Equal(t, 1, TickTimeout(p.Manifest))

	started := time.Now()
	assert.Equal(t, 1, h.pass(t, tickAt))
	assert.Less(t, time.Since(started), 30*time.Second, "the budget ended it, not the test's patience")

	wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
	require.NotNil(t, wake)
	assert.Equal(t, model.AppPluginScheduleDue, wake.Status, "re-armed, not stuck at running")
	assert.Contains(t, LocalizeNote(wake.Error, "en"), "out of time")
	assert.Empty(t, q.all())
}

func TestSchedule_TickTimeoutIsCappedTighterThanAScreen(t *testing.T) {
	h := newHarness(t, nil)
	p, _ := h.installScheduled(t, map[string]string{"tick_mode": "quiet"}, func(m map[string]any) {
		m["limits"] = map[string]any{"call_timeout_s": MaxCallTimeout}
	})
	assert.Equal(t, MaxCallTimeout, p.Manifest.CallTimeout(), "a screen may ask for the full ceiling")
	assert.Equal(t, MaxTickTimeout, TickTimeout(p.Manifest), "an unattended wake-up may not")
}

// ── bounds ─────────────────────────────────────────────────────────────

func TestSchedule_BoundsOnWhatOneWakeUpMayAskFor(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, _ := h.installScheduled(t, map[string]string{
		"tick_mode": "flood", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, nil)

	h.pass(t, tickAt)
	rows := h.rows(t, p)
	assert.Len(t, rows, MaxScheduleItems+1, "the cap, plus the wake-up row itself")
	wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
	assert.Contains(t, LocalizeNote(wake.Error, "en"), fmt.Sprintf("%d scheduled", MaxScheduleItems))
	assert.Contains(t, LocalizeNote(wake.Error, "en"), "refused")
	assert.Contains(t, LocalizeNote(wake.Error, "en"), "at most 64 items")
}

func TestSchedule_WorkBeyondTheWindowIsNotScheduledAndIsNotAnError(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, _ := h.installScheduled(t, map[string]string{
		"tick_mode": "far", "tick_path": "main://docs/contract.txt",
	}, nil)

	h.pass(t, tickAt)
	assert.Nil(t, h.row(t, p, "far"), "a week out is not this hour's business")
	wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
	assert.Contains(t, LocalizeNote(wake.Error, "en"), "1 beyond this window")
	assert.NotContains(t, LocalizeNote(wake.Error, "en"), "refused", "looking further ahead is not a mistake")
	assert.Equal(t, model.AppPluginScheduleDue, wake.Status)
}

func TestSchedule_MalformedItemsAreRefusedNamedAndDropped(t *testing.T) {
	for _, tc := range []struct {
		mode, key, want string
	}{
		{"badkey", "no spaces allowed", "key must be 1 to 64 characters"},
		{"badaction", "ghost", "has no action no-such-action"},
		{"nopaths", "empty", "must name at least one file"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			h := newHarness(t, nil)
			h.writeFile(t, "docs/contract.txt", "hello")
			p, q := h.installScheduled(t, map[string]string{
				"tick_mode": tc.mode, "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
			}, nil)

			h.pass(t, tickAt)
			assert.Nil(t, h.row(t, p, tc.key))
			assert.Len(t, h.rows(t, p), 1, "only the wake-up row")
			wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
			assert.Contains(t, LocalizeNote(wake.Error, "en"), "1 refused")
			assert.Contains(t, LocalizeNote(wake.Error, "en"), tc.want)
			assert.Equal(t, 0, h.pass(t, tickAt.Add(30*time.Minute)))
			assert.Empty(t, q.all())
		})
	}
}

func TestSchedule_PathsMustNameOneRealStorage(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()

	id, rels, why := h.reg.resolveSchedulePaths(ctx, []string{"main://docs/a.txt"})
	assert.Empty(t, why)
	assert.Equal(t, h.st.ID, id)
	assert.Equal(t, []string{"docs/a.txt"}, rels)

	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"none", nil, "at least one file"},
		{"unqualified", []string{"docs/a.txt"}, "must be adapter-qualified"},
		{"unknown adapter", []string{"nope://a.txt"}, "no storage named nope"},
		{"folder only", []string{"main://"}, "names no file"},
		{"too many", make([]string, MaxSchedulePaths+1), "at most 16 files"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, why := h.reg.resolveSchedulePaths(ctx, tc.paths)
			assert.Contains(t, why, tc.want)
		})
	}

	// Two storages in one item: an ops row has one storage, so this is the
	// kind of ambiguity that has to be refused rather than picked from.
	other, err := h.store.CreateStorage(ctx, &model.Storage{Name: "other", Driver: "local", MountPath: "/other", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"."}`)})
	require.NoError(t, err)
	_, _, why = h.reg.resolveSchedulePaths(ctx, []string{"main://a.txt", "other://b.txt"})
	assert.Contains(t, why, "must all live on one storage")

	// A depo the operator switched off says so now, not an hour later as a
	// driver error nobody is watching.
	other.Enabled = false
	require.NoError(t, h.store.UpdateStorage(ctx, other))
	_, _, why = h.reg.resolveSchedulePaths(ctx, []string{"other://b.txt"})
	assert.Contains(t, why, "switched off")
}

func TestSchedule_KeysAreBoundedAndCannotBeTheHostsWakeUpRow(t *testing.T) {
	assert.False(t, ValidScheduleKey(model.AppPluginScheduleWakeupKey), "an app can never address the wake-up row")
	assert.False(t, ValidScheduleKey("with space"))
	assert.False(t, ValidScheduleKey("-leading"))
	assert.False(t, ValidScheduleKey(string(make([]byte, 65))))
	assert.True(t, ValidScheduleKey("expire:7f3a-9c.1@env"))
	assert.True(t, ValidScheduleKey("a"))
}

// ── the scope a wake-up runs in ────────────────────────────────────────

func TestSchedule_TickMayNotWrite(t *testing.T) {
	h := newHarness(t, nil)
	p, _ := h.installScheduled(t, map[string]string{"tick_mode": "write"}, nil)

	h.pass(t, tickAt)
	wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
	require.NotNil(t, wake)
	assert.Contains(t, LocalizeNote(wake.Error, "en"), "may not write state",
		"a wake-up decides; the job it schedules is what writes")
}

func TestSchedule_TickReadsItsOwnStateToDecide(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/a.txt", "hello")
	p, q := h.installScheduled(t, map[string]string{"tick_mode": "state", "tick_due_in_s": "600"}, nil)

	// A person runs the app on a file, which leaves this app's own state on
	// it — the record a signing app keeps of "waiting for a signature".
	_, err := h.runJob(t, p, "upper", []string{"docs/a.txt"}, "en")
	require.NoError(t, err)

	h.pass(t, tickAt)
	item := h.row(t, p, "state-0")
	require.NotNil(t, item, "the wake-up found its own file through state_list and scheduled it")
	assert.Equal(t, "expire", item.ActionID)
	assert.JSONEq(t, `["docs/a.txt"]`, item.PathsJSON)
	wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
	assert.Contains(t, LocalizeNote(wake.Error, "en"), "from state", "the app's note")

	assert.Equal(t, 1, h.pass(t, tickAt.Add(10*time.Minute)))
	require.Len(t, q.all(), 1)
}

// ── the switches that must turn it off ─────────────────────────────────

func TestSchedule_DemoAndDisabledGuardsHold(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, q := h.installScheduled(t, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, nil)

	// Demo mode: an instance that hands an admin account to strangers must
	// not also run their code with nobody watching.
	h.reg.opts.Demo = true
	assert.Equal(t, 0, h.pass(t, tickAt), "no row is even looked at")
	assert.Equal(t, model.AppPluginScheduleDue, h.row(t, p, model.AppPluginScheduleWakeupKey).Status)
	assert.Empty(t, q.all())

	_, err := h.reg.Tick(context.Background(), p, tickAt, tickAt.Add(time.Hour))
	require.Error(t, err)
	assert.True(t, IsCode(err, CodeRefused), err)

	// StartScheduler starts nothing, so there is no goroutine to race.
	h.reg.StartScheduler(context.Background())
	assert.Equal(t, 0, h.pass(t, tickAt.Add(time.Hour)))

	// Arming is off too: a demo never acquires a wake-up in the first place.
	require.NoError(t, h.store.FinishAppPluginScheduleItem(context.Background(), p.Row.ID,
		model.AppPluginScheduleWakeupKey, model.AppPluginScheduleSkipped, "", "", nil))
	h.reg.armWakeup(context.Background(), p)
	assert.Equal(t, model.AppPluginScheduleSkipped, h.row(t, p, model.AppPluginScheduleWakeupKey).Status)

	// FILEX_APP_PLUGINS_DISABLED leaves the server with no registry at all.
	// That is this shape, and every entry point has to survive it.
	var off *Registry
	assert.NotPanics(t, func() { off.StartScheduler(context.Background()) })
	n, err := off.RunDueSchedule(context.Background(), tickAt)
	assert.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Zero(t, off.SweepSchedule(context.Background()))
	assert.Empty(t, off.InstanceID())
}

// ── an app that cannot run its work at its time ────────────────────────

func TestSchedule_AStoppedAppDropsItsWorkAndKeepsItsWakeUp(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, q := h.installScheduled(t, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, nil)
	h.pass(t, tickAt)
	require.NotNil(t, h.row(t, p, "due"))

	_, err := h.reg.SetEnabled(context.Background(), p.Row.ID, false)
	require.NoError(t, err)

	assert.Equal(t, 1, h.pass(t, tickAt.Add(10*time.Minute)))
	item := h.row(t, p, "due")
	require.NotNil(t, item)
	assert.Equal(t, model.AppPluginScheduleSkipped, item.Status)
	assert.Contains(t, item.Error, "not running")
	assert.Empty(t, q.all(), "work is dropped rather than run hours late by a version that may have changed")

	// The wake-up survives, so switching the app back on costs an hour at
	// most rather than an uninstall.
	assert.Equal(t, 1, h.pass(t, tickAt.Add(time.Hour)))
	wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
	assert.Equal(t, model.AppPluginScheduleDue, wake.Status)
	assert.Contains(t, LocalizeNote(wake.Error, "en"), "not running")
}

// unhideExpire makes the fixture's scheduled `expire` a MENU action for one
// test. A hidden action cannot be switched off at all (hiddenIgnoresOverride),
// so the run-time switch check is measured on a visible one. describe does
// not compare actions, so the module installs unchanged.
func unhideExpire(m map[string]any) {
	for _, raw := range m["actions"].([]any) {
		if a := raw.(map[string]any); a["id"] == "expire" {
			delete(a, "hidden")
		}
	}
}

func TestSchedule_AnActionTheAdminSwitchedOffDoesNotRun(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, q := h.installScheduled(t, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, unhideExpire)
	h.pass(t, tickAt)
	require.NotNil(t, h.row(t, p, "due"))

	// Switched off AFTER it was scheduled: the check has to be at run time
	// too, not only when the wake-up answered.
	require.NoError(t, h.reg.PutOverrides(context.Background(), p.Row.ID, []OverrideRow{{ID: "expire", Enabled: false}}))
	assert.Equal(t, 1, h.pass(t, tickAt.Add(10*time.Minute)))
	item := h.row(t, p, "due")
	assert.Equal(t, model.AppPluginScheduleSkipped, item.Status)
	assert.Contains(t, item.Error, "switched expire off")
	assert.Empty(t, q.all())

	// And the next wake-up will not schedule it again while it is off.
	h.pass(t, tickAt.Add(time.Hour))
	wake := h.row(t, p, model.AppPluginScheduleWakeupKey)
	assert.Contains(t, LocalizeNote(wake.Error, "en"), "switched expire off")
}

// The same switch sent for a HIDDEN scheduled action changes nothing: the
// app's own machinery runs until the app itself is switched off (the test
// above this one's neighbour, AStoppedAppDropsItsWork, is that switch).
func TestSchedule_AHiddenScheduledActionCannotBeSwitchedOffAlone(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, q := h.installScheduled(t, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, nil)
	h.pass(t, tickAt)
	require.NotNil(t, h.row(t, p, "due"))
	require.NoError(t, h.reg.PutOverrides(context.Background(), p.Row.ID, []OverrideRow{{ID: "expire", Enabled: false}}))
	assert.Equal(t, 1, h.pass(t, tickAt.Add(10*time.Minute)))
	item := h.row(t, p, "due")
	require.NotNil(t, item)
	assert.Equal(t, model.AppPluginScheduleQueued, item.Status, "a hidden action is not a person's to switch off")
	assert.Len(t, q.all(), 1)
}

func TestSchedule_AnUninstalledAppLeavesNoSchedule(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, _ := h.installScheduled(t, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, nil)
	h.pass(t, tickAt)
	require.Len(t, h.rows(t, p), 2)

	require.NoError(t, h.reg.Remove(context.Background(), p.Row.ID))
	rows, err := h.reg.ScheduleOf(context.Background(), p.Row.ID)
	require.NoError(t, err)
	assert.Empty(t, rows, "uninstalling takes the schedule with it")
	assert.Equal(t, 0, h.pass(t, tickAt.Add(10*time.Minute)))
}

func TestSchedule_AFailingQueueFailsLoudlyAndIsNotRetried(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, q := h.installScheduled(t, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, nil)
	h.pass(t, tickAt)
	q.err = assert.AnError

	assert.Equal(t, 1, h.pass(t, tickAt.Add(10*time.Minute)))
	item := h.row(t, p, "due")
	require.NotNil(t, item)
	assert.Equal(t, model.AppPluginScheduleFailed, item.Status)
	assert.Contains(t, item.Error, "queue:")

	// No retry loop: it stays failed until the app asks for it again.
	q.err = nil
	assert.Equal(t, 0, h.pass(t, tickAt.Add(11*time.Minute)))
	assert.Empty(t, q.all())
}

// ── housekeeping and the loop's own arithmetic ─────────────────────────

func TestSchedule_FinishedRowsAreSweptAndDueOnesAreNot(t *testing.T) {
	h := newHarness(t, nil)
	h.writeFile(t, "docs/contract.txt", "hello")
	p, _ := h.installScheduled(t, map[string]string{
		"tick_mode": "due", "tick_path": "main://docs/contract.txt", "tick_due_in_s": "600",
	}, nil)
	ctx := context.Background()

	old := time.Now().UTC().Add(-scheduleRetention - time.Hour)
	require.NoError(t, h.store.PutAppPluginScheduleItem(ctx, &model.AppPluginScheduleItem{
		PluginID: p.Row.ID, Key: "ancient", DueAt: old, ActionID: "expire",
		Status: model.AppPluginScheduleQueued,
	}))
	require.NotNil(t, h.row(t, p, "ancient"))

	// Finished, but only just: an administrator looking at what ran last
	// night must still find it.
	require.NoError(t, h.store.PutAppPluginScheduleItem(ctx, &model.AppPluginScheduleItem{
		PluginID: p.Row.ID, Key: "recent", DueAt: time.Now().UTC().Add(-time.Hour), ActionID: "expire",
		Status: model.AppPluginScheduleQueued,
	}))

	// Old AND still due: a process that was down for a week comes back to
	// work it has not done. Sweeping that would turn "late" into "never",
	// which is the one outcome this whole mechanism exists to prevent.
	require.NoError(t, h.store.PutAppPluginScheduleItem(ctx, &model.AppPluginScheduleItem{
		PluginID: p.Row.ID, Key: "overdue", DueAt: old, ActionID: "expire",
		StorageID: h.st.ID, PathsJSON: `["docs/contract.txt"]`, Status: model.AppPluginScheduleDue,
	}))

	assert.Equal(t, int64(1), h.reg.SweepSchedule(ctx))
	assert.Nil(t, h.row(t, p, "ancient"))
	assert.NotNil(t, h.row(t, p, "recent"), "a run from an hour ago is still the record of what happened")
	assert.NotNil(t, h.row(t, p, "overdue"), "unfinished work is never swept, however late it is")
	assert.NotNil(t, h.row(t, p, model.AppPluginScheduleWakeupKey), "a live wake-up is never swept")

	// And it still runs — late rather than never. (The wake-up is due at
	// this synthetic clock too, so the pass covers more than one row.)
	assert.GreaterOrEqual(t, h.pass(t, tickAt), 1)
	assert.Equal(t, model.AppPluginScheduleQueued, h.row(t, p, "overdue").Status)
}

// The lease is not only about who RUNS a row; it also protects a running row
// from being rewritten underneath. Two processes, one ticking while the
// other runs an item: the wake-up must not move work that is already going.
func TestSchedule_ALaterWakeUpMayNotRewriteAnItemThatIsRunning(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	const pluginID = 42
	put := func(due time.Time) {
		require.NoError(t, store.PutAppPluginScheduleItem(ctx, &model.AppPluginScheduleItem{
			PluginID: pluginID, Key: "expire:7f3a", DueAt: due, ActionID: "expire",
			StorageID: 1, PathsJSON: `["a.pdf"]`, Status: model.AppPluginScheduleDue,
		}))
	}
	only := func() *model.AppPluginScheduleItem {
		rows, err := store.ListAppPluginScheduleItems(ctx, pluginID)
		require.NoError(t, err)
		require.Len(t, rows, 1, "one key is one row, however often it is named")
		return rows[0]
	}

	put(tickAt)
	claimed, err := store.ClaimAppPluginScheduleItem(ctx, pluginID, "expire:7f3a", "node-a", tickAt)
	require.NoError(t, err)
	require.True(t, claimed)

	// Node B's wake-up names the same key while node A is running it.
	put(tickAt.Add(time.Hour))
	row := only()
	assert.Equal(t, model.AppPluginScheduleRunning, row.Status, "the row node A holds is untouched")
	assert.Equal(t, tickAt, row.DueAt.UTC(), "and it was not moved out from under node A")
	assert.Equal(t, "node-a", row.ClaimedBy)

	// Once node A is finished the same call moves it, as it always did.
	require.NoError(t, store.FinishAppPluginScheduleItem(ctx, pluginID, "expire:7f3a",
		model.AppPluginScheduleQueued, "job-1", "", nil))
	put(tickAt.Add(time.Hour))
	row = only()
	assert.Equal(t, model.AppPluginScheduleDue, row.Status)
	assert.Equal(t, tickAt.Add(time.Hour), row.DueAt.UTC())
	assert.Empty(t, row.JobID, "a fresh item, not the finished one wearing a new date")

	// And the lease refuses a second claim of a row that is no longer due.
	claimed, err = store.ClaimAppPluginScheduleItem(ctx, pluginID, "expire:7f3a", "node-b", tickAt)
	require.NoError(t, err)
	assert.False(t, claimed, "not due yet: never early, whoever asks")
}

func TestSchedule_NextTickIsOnTheHourAndTheLoopSleepsUntilTheNextDueRow(t *testing.T) {
	assert.Equal(t, time.Date(2030, 1, 2, 4, 0, 0, 0, time.UTC),
		NextTickBoundary(time.Date(2030, 1, 2, 3, 37, 12, 0, time.UTC)),
		"a restart at :37 does not move every later wake-up to :37")
	assert.Equal(t, time.Date(2030, 1, 2, 4, 0, 0, 0, time.UTC),
		NextTickBoundary(time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)),
		"exactly on the hour goes to the next one, not to itself")

	h := newHarness(t, nil)
	ctx := context.Background()
	assert.Equal(t, schedulePollCeiling, h.reg.untilNextDue(ctx), "nothing due: sleep the ceiling")

	p, _ := h.installScheduled(t, map[string]string{"tick_mode": "quiet"}, nil)
	require.NoError(t, h.store.PutAppPluginScheduleItem(ctx, &model.AppPluginScheduleItem{
		PluginID: p.Row.ID, Key: "soon", DueAt: time.Now().UTC().Add(2 * time.Second),
		ActionID: "expire", Status: model.AppPluginScheduleDue,
	}))
	d := h.reg.untilNextDue(ctx)
	assert.Greater(t, d, schedulePollFloor)
	assert.Less(t, d, schedulePollCeiling, "it sleeps until that row is due, not for a fixed beat")
}

// ── an app that asks to be woken must have something to wake ───────────

func TestSchedule_AModuleWithoutATickExportIsRefused(t *testing.T) {
	h := newHarness(t, nil)
	p, _ := h.installScheduled(t, map[string]string{"tick_mode": "quiet"}, nil)
	ctx := context.Background()
	c, err := p.running()
	require.NoError(t, err)

	has, err := c.HasExport(ctx, "tick")
	require.NoError(t, err)
	assert.True(t, has, "the SDK compiles the export in")
	has, err = c.HasExport(ctx, "no_such_export")
	require.NoError(t, err)
	assert.False(t, has)

	// The rule itself: a granted app must export tick; an app that was not
	// granted it is not asked about it at all.
	assert.NoError(t, checkTickExport(ctx, NewGrants(nil), c))
	assert.NoError(t, checkTickExport(ctx, NewGrants([]Permission{PermSchedule}), c))

	err = checkTickExport(ctx, NewGrants([]Permission{PermSchedule}), noTickModule{})
	require.Error(t, err)
	assert.True(t, IsCode(err, CodeRefused), err)
	assert.Contains(t, err.Error(), "no tick export")
}

// noTickModule is a module that was granted a wake-up and cannot answer one
// — a hand-written or other-language plugin, or one built against an SDK
// older than the export.
type noTickModule struct{}

func (noTickModule) HasExport(context.Context, string) (bool, error) { return false, nil }

// ── the permission is legible to the person granting it ────────────────

func TestSchedule_ThePermissionSaysWhatItMeansInBothLanguages(t *testing.T) {
	assert.Contains(t, PermSchedule.Label("en"), "with nobody present")
	assert.Contains(t, PermSchedule.Label("tr"), "kimse başında değilken")

	h := newHarness(t, nil)
	in := echoInput(t, scheduleManifest)
	in.DryRun = true
	_, dry, err := h.reg.Install(context.Background(), in)
	require.NoError(t, err)
	var row *PermissionRow
	for i := range dry.Permissions {
		if dry.Permissions[i].ID == string(PermSchedule) {
			row = &dry.Permissions[i]
		}
	}
	require.NotNil(t, row, "the install review lists it, so it cannot be granted unseen")
	assert.Contains(t, row.Label, "once an hour")
}

// The wake-up line is composed in the READER's language when the row is read,
// from the tally's numbers and the app's own summary in that language — a
// language pack's Spanish reads Spanish, not the English half of an en/tr
// pair written at wake-up time. Rows written before (a {lang: …} object) still
// read, and an app that speaks many languages cannot overflow the 500-byte
// row into JSON nobody can parse.
func TestSchedule_WakeNoteIsComposedInTheReadersLanguage(t *testing.T) {
	srvtext.SetPacks(srvtext.StaticPacks{"es": {"server.app.wake.scheduled": "{count} programadas"}})
	t.Cleanup(func() { srvtext.SetPacks(nil) })

	note := wakeNote(wire.Text{"en": "nothing to do", "tr": "yapacak iş yok", "es": "nada que hacer"},
		tallied{Scheduled: 2, Refused: 1, Reason: "bad item"})
	assert.Equal(t, "nada que hacer — 2 programadas, 1 refused (bad item)", LocalizeNote(note, "es"),
		"the pack's words where it has them, English where it does not")
	assert.Equal(t, "yapacak iş yok — 2 planlandı, 1 reddedildi (bad item)", LocalizeNote(note, "tr"))
	assert.Equal(t, "nothing to do — 2 scheduled, 1 refused (bad item)", LocalizeNote(note, "en"))

	assert.Equal(t, "eski", LocalizeNote(`{"en":"old","tr":"eski"}`, "tr"), "a row from the previous release still reads")

	big := wire.Text{}
	for _, l := range []string{"en", "tr", "es", "de", "fr", "ar"} {
		big[l] = strings.Repeat("ş", 400)
	}
	n := wakeNote(big, tallied{})
	assert.LessOrEqual(t, len(n), 500, "the row keeps 500 bytes; a clipped JSON object reads as garbage")
	assert.True(t, utf8.ValidString(n), "clipped on a rune boundary")
	assert.NotEqual(t, n, LocalizeNote(n, "en"), "the record parses")
}
