package wasmplugin

// ── The hourly wake-up, and work to the minute ─────────────────────────
//
// # What it is
//
// An app that holds the `schedule` permission is WOKEN once an hour and
// asked one question — what do you want done, and WHEN — through a `tick`
// export. It answers with items, each carrying a due time inside the window
// it was given, and filex runs each one AT that time. So the wake-up is
// hourly and the work is to the minute: not "some time this hour".
//
// # Why it exists
//
// A signing app cannot expire a request on its own. `opExpire` only runs
// when somebody opens the status screen and presses a button, so a request
// that lapses at 03:00 notifies nobody until a human happens to look. With a
// wake-up the app schedules the closure for 03:00 and filex runs it then,
// and both sides are told at the right moment.
//
// # How it reuses what is already here
//
// A scheduled item IS an ordinary plugin job: the app names one of its own
// actions (usually a `hidden` one), the host mints an app_plugin_jobs row and
// hands it to the ops queue as a `plugin-action` op. Everything downstream —
// the sandbox, the per-file input limits, output commit, progress, cancel,
// the ops tray row, DecorateOps — is the machinery that already runs a
// person's job. There is no second executor and no second retry policy.
//
// # Durability, and running it exactly once
//
// The schedule lives in the database (migration 00050), written when the
// wake-up ANSWERS rather than when the work runs, so a restart cannot lose
// it. A process that is down at 03:00 finds the row still `due` when it
// comes back and runs it then: once, late rather than never. Claiming a due
// row is a single conditional UPDATE from `due` to `running`, so two filex
// processes on one database cannot both run it — only the one whose UPDATE
// matched a row proceeds.
//
// # No retry loop
//
// An item that fails to queue is marked `failed` and is NOT retried. The
// next wake-up is the retry: if the app still wants that work it says so
// again, with a fresh view of the world. A wake-up that traps or times out
// is logged and re-armed for the next hour; nothing retries inside the hour,
// so a broken app costs one short call an hour and never a loop.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// Bounds. Everything a wake-up can ask for is capped here; a plugin is told
// the two it has to plan around (MaxScheduleItems, MaxSchedulePaths) in its
// TickInput, so it can choose what to drop rather than having it chosen.
const (
	// TickInterval is how often a scheduled app is woken. The wake-up is
	// re-armed to the next boundary of this interval, so an instance that
	// has been up for a week still wakes on the hour.
	TickInterval = time.Hour
	// MaxScheduleItems / MaxSchedulePaths are the wire contract's numbers,
	// not a second copy: the guest is told them in every TickInput and the
	// test kit checks an answer against them, so they are named once in
	// pkg/pluginkit/wire and enforced here.
	MaxScheduleItems = wire.ScheduleMaxItems
	MaxSchedulePaths = wire.ScheduleMaxPaths
	// maxScheduleParamsBytes caps one item's params, as the HTTP submit path
	// caps a job's (64 KiB there, tighter here: nobody typed these).
	maxScheduleParamsBytes = 16 << 10
	// maxDuePerPass bounds one scheduler pass, so a database full of due
	// rows cannot hold the goroutine for minutes.
	maxDuePerPass = 100
	// schedulePollCeiling is the longest the loop sleeps. It normally sleeps
	// until exactly the next due time; the ceiling is what makes it notice a
	// row ANOTHER process inserted in the meantime.
	schedulePollCeiling = 30 * time.Second
	// schedulePollFloor keeps the loop off a hot spin when a due row cannot
	// be claimed for a reason that does not change its status.
	schedulePollFloor = 250 * time.Millisecond
	// tickBootGrace is how long after a load the first wake-up fires, so the
	// schedule is rebuilt after a restart without racing the boot.
	tickBootGrace = 30 * time.Second
	// scheduleRetention is how long a finished row is kept, so an
	// administrator can still see what ran last night.
	scheduleRetention = 7 * 24 * time.Hour
	// scheduleSweepEvery is how often finished rows past the retention
	// window are dropped.
	scheduleSweepEvery = time.Hour
)

// validScheduleKey is what an app may call one of its items — the pattern
// the contract publishes, compiled once. It cannot match the empty string,
// which is what keeps an app off the host's own wake-up row
// (model.AppPluginScheduleWakeupKey).
var validScheduleKey = regexp.MustCompile(wire.ScheduleKeyPattern)

// ValidScheduleKey reports whether an app may use key for a scheduled item.
func ValidScheduleKey(key string) bool { return validScheduleKey.MatchString(key) }

// JobQueue is the ops queue, as the scheduler needs it: a way to hand an
// app_plugin_jobs row to the same worker a person's job goes through.
// *ops.Service satisfies it; the server injects it with SetJobQueue.
//
// Without it a due item fails loudly (status `failed`, the reason on the
// row) rather than disappearing.
type JobQueue interface {
	SubmitTo(ctx context.Context, kind string, storageID, destStorageID int64, sources []string, dest string) (*ops.Op, error)
}

// SetJobQueue wires the queue scheduled work is handed to.
func (r *Registry) SetJobQueue(q JobQueue) { r.queue = q }

// newInstanceID mints the name this process claims rows under, so an
// operator reading a row can tell WHICH node ran it.
func newInstanceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// InstanceID is what this process writes into claimed_by.
func (r *Registry) InstanceID() string {
	if r == nil {
		return ""
	}
	return r.instanceID
}

// scheduleOff answers whether the scheduler must not run at all.
//
// Two switches, both of which have to hold: the demo guard (a demo hands an
// admin account to strangers, and unattended execution is the last thing
// that should be on there) and the disabled flag — which the server serves
// by building no registry at all, so a nil receiver is the disabled case and
// every entry point here tolerates it.
func (r *Registry) scheduleOff() bool { return r == nil || r.opts.Demo }

// ── The loop ───────────────────────────────────────────────────────────

// StartScheduler runs the schedule until ctx ends. Call it once at boot,
// after the job queue is wired.
//
// It does not poll on a fixed beat: after each pass it sleeps until the next
// row is actually due, capped at schedulePollCeiling so a row another
// process wrote is picked up too. That is what makes "to the minute" true —
// an item due at 03:00:00 runs at 03:00:00, not at the next tick of a
// one-minute timer.
func (r *Registry) StartScheduler(ctx context.Context) {
	if r.scheduleOff() {
		if r != nil {
			r.log.Info("app-plugins: the schedule is off (demo mode)")
		}
		return
	}
	go func() {
		// Its own housekeeping rather than the public-link sweeper's loop:
		// the schedule owns its table, and one loop that can be read top to
		// bottom beats a second caller in another file.
		lastSweep := time.Now()
		for {
			if _, err := r.RunDueSchedule(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
				r.log.Warn("app-plugins: schedule pass failed", slog.String("err", err.Error()))
			}
			if time.Since(lastSweep) >= scheduleSweepEvery {
				lastSweep = time.Now()
				r.SweepSchedule(ctx)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(r.untilNextDue(ctx)):
			}
		}
	}()
}

// untilNextDue is how long the loop may sleep: until the earliest due row,
// never longer than the ceiling and never shorter than the floor.
func (r *Registry) untilNextDue(ctx context.Context) time.Duration {
	next, err := r.opts.Store.NextAppPluginScheduleDue(ctx)
	if err != nil || next == nil {
		return schedulePollCeiling
	}
	d := time.Until(*next)
	if d > schedulePollCeiling {
		return schedulePollCeiling
	}
	if d < schedulePollFloor {
		return schedulePollFloor
	}
	return d
}

// RunDueSchedule performs ONE pass: every row due at or before now, claimed
// and run. Exported so a test — and, later, an administrator's "run it now"
// — can drive the schedule without the loop.
//
// A plugin that traps, panics or hangs takes down its own row and nothing
// else: each row runs behind a recover, and the call itself is bounded by
// the wasm runtime's own per-call budget.
func (r *Registry) RunDueSchedule(ctx context.Context, now time.Time) (int, error) {
	if r.scheduleOff() {
		return 0, nil
	}
	rows, err := r.opts.Store.DueAppPluginScheduleItems(ctx, now, maxDuePerPass)
	if err != nil {
		return 0, err
	}
	ran := 0
	for _, row := range rows {
		if ctx.Err() != nil {
			return ran, ctx.Err()
		}
		claimed, err := r.opts.Store.ClaimAppPluginScheduleItem(ctx, row.PluginID, row.Key, r.instanceID, now)
		if err != nil {
			r.log.Warn("app-plugins: schedule claim failed", slog.Int64("plugin", row.PluginID), slog.String("err", err.Error()))
			continue
		}
		if !claimed {
			// Another filex process took it. Exactly the point of the lease.
			continue
		}
		ran++
		r.runClaimed(ctx, row, now)
	}
	return ran, nil
}

// runClaimed executes one row this process now holds the lease on. It always
// finishes the row: a claimed row that is left `running` would never come
// due again, which is the one failure mode a lease must not have.
func (r *Registry) runClaimed(ctx context.Context, row *model.AppPluginScheduleItem, now time.Time) {
	defer func() {
		if rec := recover(); rec != nil {
			msg := fmt.Sprintf("the schedule panicked: %v", rec)
			r.log.Error("app-plugins: schedule row panicked",
				slog.Int64("plugin", row.PluginID), slog.String("key", row.Key), slog.Any("panic", rec))
			r.finish(ctx, row, model.AppPluginScheduleFailed, "", clip(msg, 500), r.rearmFor(row, now))
		}
	}()

	p, ok := r.ByID(row.PluginID)
	switch {
	case !ok:
		r.finish(ctx, row, model.AppPluginScheduleSkipped, "", "the app is no longer installed", nil)
		return
	case !p.Grants.Has(PermSchedule):
		// An upgrade dropped the permission. The wake-up is NOT re-armed:
		// withdrawing the grant stops the app being woken, for good.
		p.log("info", "the schedule permission is gone; this app is no longer woken")
		r.finish(ctx, row, model.AppPluginScheduleSkipped, "", "the schedule permission was withdrawn", nil)
		return
	}
	if state, serr := p.State(); state != StateRunning {
		reason := "the app was not running at its time"
		if serr != "" {
			reason += ": " + serr
		}
		// A stopped app keeps its hourly wake-up (re-armed, so re-enabling
		// it costs at most an hour) but its pending WORK is dropped rather
		// than run hours late by a version that may since have changed.
		r.finish(ctx, row, model.AppPluginScheduleSkipped, "", reason, r.rearmFor(row, now))
		return
	}
	if row.Key == model.AppPluginScheduleWakeupKey {
		r.runWakeup(ctx, p, row, now)
		return
	}
	r.runScheduledItem(ctx, p, row)
}

// rearmFor is the next due time of a WAKE-UP row, or nil for a work item —
// work is never re-armed by the host, only by the app's next answer.
func (r *Registry) rearmFor(row *model.AppPluginScheduleItem, now time.Time) *time.Time {
	if row.Key != model.AppPluginScheduleWakeupKey {
		return nil
	}
	next := NextTickBoundary(now)
	return &next
}

// finish ends a claimed row. A non-nil rearmAt puts it back to `due` at that
// time instead, and `status` is then ignored — one call for both, because
// the two cases differ only in what happens to the row afterwards and a
// claimed row must be released down EVERY path.
func (r *Registry) finish(ctx context.Context, row *model.AppPluginScheduleItem, status, jobID, note string, rearmAt *time.Time) {
	// context.WithoutCancel: a row claimed on a context that ends mid-pass
	// must still be released, or it stays `running` for ever.
	if err := r.opts.Store.FinishAppPluginScheduleItem(context.WithoutCancel(ctx),
		row.PluginID, row.Key, status, jobID, clip(note, 500), rearmAt); err != nil {
		r.log.Warn("app-plugins: schedule row could not be finished",
			slog.Int64("plugin", row.PluginID), slog.String("key", row.Key), slog.String("err", err.Error()))
	}
}

// NextTickBoundary is when the wake-up after now falls: the next boundary of
// TickInterval, in UTC. Boundaries rather than "an hour from whenever it
// last ran" so a restart at 03:37 does not move every later wake-up to :37.
func NextTickBoundary(now time.Time) time.Time {
	return now.UTC().Truncate(TickInterval).Add(TickInterval)
}

// ── Arming ─────────────────────────────────────────────────────────────

// armWakeup gives a plugin its wake-up row. Called whenever a plugin becomes
// runnable (load, install, upgrade, enable), which means a restart rebuilds
// the schedule: the first wake-up comes tickBootGrace after the load and
// re-arms itself onto the hour from there.
//
// A wake-up another process is RUNNING is left alone by the store.
func (r *Registry) armWakeup(ctx context.Context, p *Installed) {
	r.setWakeup(ctx, p, tickBootGrace, "wake-up armed for")
}

// setWakeup puts an app's one wake-up row `after` from now and says so in the
// app's log ("<what> <time>").
//
// ⚠ The ONE writer of that row outside the scheduler's own re-arm. Arming at
// load and bringing the wake-up forward on a revoke (WakeSoon) differ only in
// how soon and in the words; the eligibility rule, the row and the failure
// handling are this function's, so the two cannot drift apart. (WakeSoon was
// first written as a copy of armWakeup's body and the duplication gate caught
// it — web/tests/quality/duplication.test.ts.)
func (r *Registry) setWakeup(ctx context.Context, p *Installed, after time.Duration, what string) {
	if r.scheduleOff() || p == nil || !p.Grants.Has(PermSchedule) {
		return
	}
	due := time.Now().UTC().Add(after)
	if err := r.opts.Store.PutAppPluginScheduleItem(ctx, &model.AppPluginScheduleItem{
		PluginID: p.Row.ID, Key: model.AppPluginScheduleWakeupKey, DueAt: due, Status: model.AppPluginScheduleDue,
	}); err != nil {
		r.log.Warn("app-plugins: wake-up could not be set", slog.String("plugin", p.Row.Name),
			slog.String("for", what), slog.String("err", err.Error()))
		return
	}
	p.log("info", what+" "+due.Format(time.RFC3339))
}

// tickNudgeGrace is how soon a wake-up brought forward by WakeSoon fires:
// long enough that a burst of revokes (an administrator clearing a page of
// Shares) lands in ONE wake-up, short enough that the person who pressed
// Revoke sees the request it belonged to close while they are still looking.
const tickNudgeGrace = 5 * time.Second

// WakeSoon brings an app's hourly wake-up forward to a few seconds from now.
//
// It is how the host tells an app that something it cannot see happened to
// one of its own things — today, a PERSON ended one of its links from the
// Shares screen (handlers: appLinkEnded). ⚠⚠ It carries no event and no
// payload, on purpose: the app is not told WHAT happened, it is woken, and
// its `tick` asks the host about its own links (`share_state` → the link's
// facts) exactly as it does every hour. One mechanism — the wake-up — and one
// way of learning — asking — rather than a revoke callback living beside
// them with its own delivery, retry and ordering rules. Without the nudge the
// same wake-up still notices within the hour; the nudge only makes it prompt.
//
// A no-op for an app without the `schedule` grant, a stopped app and a demo
// instance — each of those is simply not woken, which is what they already
// were.
func (r *Registry) WakeSoon(ctx context.Context, pluginID int64, why string) {
	if r.scheduleOff() || pluginID == 0 {
		return // nil-safe: the disabled registry is a nil one
	}
	p, ok := r.ByID(pluginID)
	if !ok {
		return
	}
	if state, _ := p.State(); state != StateRunning {
		return
	}
	r.setWakeup(ctx, p, tickNudgeGrace, "wake-up brought forward ("+why+") to")
}

// ── The wake-up itself ─────────────────────────────────────────────────

// Tick wakes one plugin and returns what it wants done. Exported so a test
// (and a future "run it now" button) can drive one wake-up on its own.
//
// The call gets a READ-ONLY scope with no storage and no actor: the same
// shape a screen gets. It may read settings and its own state, look people
// up, notify, mail and make granted HTTP calls; it may not write files,
// write state, take locks or sign. Those belong in the actions it schedules,
// which run as real jobs with a real scope.
func (r *Registry) Tick(ctx context.Context, p *Installed, now, windowEnd time.Time) (*wire.TickOutput, error) {
	if r.scheduleOff() {
		return nil, &CallError{Code: CodeRefused, Export: "tick", Message: "the schedule is off on this instance"}
	}
	if !p.Grants.Has(PermSchedule) {
		return nil, &CallError{Code: CodeRefused, Export: "tick", Message: "this app was not granted the schedule permission"}
	}
	c, err := p.running()
	if err != nil {
		return nil, err
	}
	scope, err := newScope(p, r, "", 0, nil, nil, "", false)
	if err != nil {
		return nil, err
	}
	// ⚠⚠ THE HOST STARTED THIS, NOT A PERSON. Without saying so, state_list
	// runs every row of the app's own state through the ACL of the actor —
	// and the actor is nil, so acl.CanSee refuses all of them and the wake-up
	// sees an empty world. Measured on a live instance: the signing app logged
	// "nothing due (0 open requests seen)" every hour while the Signatures
	// screen, asking for the very same rows with a person behind it, listed
	// the document. A tick has no inputs, so there is no state_get to fall
	// back on: an empty listing makes the whole feature inert.
	scope.system = true
	defer scope.Close()

	in := wire.TickInput{
		Now: now.UTC(), WindowStart: now.UTC(), WindowEnd: windowEnd.UTC(),
		MaxItems: MaxScheduleItems, MaxPaths: MaxSchedulePaths,
		Settings: r.publicSettings(ctx, p), Engines: r.enginesFor(p),
	}
	inb, _ := json.Marshal(in)
	budget := time.Duration(TickTimeout(p.Manifest)) * time.Second
	outb, err := c.Call(WithScope(ctx, scope), "tick", inb, budget)
	if err != nil {
		return nil, err
	}
	var out wire.TickOutput
	if err := json.Unmarshal(outb, &out); err != nil {
		return nil, &CallError{Code: CodePluginError, Export: "tick", Message: "tick returned malformed JSON"}
	}
	return &out, nil
}

// runWakeup calls tick, records what it asked for, and re-arms for the next
// hour whatever happened.
func (r *Registry) runWakeup(ctx context.Context, p *Installed, row *model.AppPluginScheduleItem, now time.Time) {
	windowEnd := NextTickBoundary(now)
	out, err := r.Tick(ctx, p, now, windowEnd)
	next := windowEnd
	if err != nil {
		// A trap, a timeout, an out-of-memory or a plugin error. It costs
		// this app its hour and nothing else: the next wake-up tries again,
		// nothing retries inside the hour, and no other app is delayed —
		// the runtime tore the instance down when the budget ran out.
		note := userMessage(err)
		p.log("warn", "wake-up failed: "+note)
		r.log.Warn("app-plugins: wake-up failed", slog.String("plugin", p.Row.Name), slog.String("err", note))
		r.finish(ctx, row, model.AppPluginScheduleDue, "", note, &next)
		return
	}
	res := r.storeItems(ctx, p, out.Items, now, windowEnd)
	note := wakeNote(out.Note, res)
	p.log("info", "wake-up: "+LocalizeNote(note, "en"))
	r.finish(ctx, row, model.AppPluginScheduleDue, "", note, &next)
}

// wakeNote is what one wake-up decided: the app's own summary (it answers in
// every language it speaks) and the host's tally as NUMBERS, stored as JSON in
// the row's note. LocalizeNote says it in the reader's language when the row
// is read — the tally through the server catalogue (`server.app.wake.*`), so a
// language pack's language reads it too.
//
// ⚠⚠ Why (2026-09-21, a tester): the admin panel's schedule line read raw
// English inside the Turkish UI — "nothing due (0 open request(s) seen) —
// 0 scheduled" — although the app HAD said it in Turkish too: the host kept
// only `Note.Get("en")` and glued its own English tally to it. The first fix
// wrote the line out in English and Turkish at wake-up time; a third language
// (a pack's Spanish) then read the English half, which is why the words are
// now composed at READ time from numbers.
//
// ⚠ The row keeps 500 characters (finish clips), and a clipped JSON object is
// unreadable, so the record is kept under that here: each of the app's
// languages is clipped on its own, on a rune boundary, and more tightly the
// more languages the app wrote.
func wakeNote(app wire.Text, t tallied) string {
	rec := wakeRecord{V: 2, N: [3]int{t.Scheduled, t.Beyond, t.Refused}, Why: clipRunes(t.Reason, 120)}
	langs := make([]string, 0, len(app))
	for l, v := range app {
		if strings.TrimSpace(v) != "" {
			langs = append(langs, l)
		}
	}
	sort.Strings(langs)
	per := 300
	if len(langs) > 0 {
		per = 300 / len(langs)
	}
	for {
		rec.App = wire.Text{}
		for _, l := range langs {
			rec.App[l] = clipRunes(strings.TrimSpace(app[l]), per)
		}
		b, _ := json.Marshal(rec)
		if len(b) <= 480 || per <= 8 {
			return string(b)
		}
		per /= 2
	}
}

// wakeRecord is a wake-up note as stored: V=2, the app's summary per
// language, the tally (scheduled, beyond this window, refused) and the first
// refusal's reason.
type wakeRecord struct {
	V   int       `json:"v"`
	App wire.Text `json:"app,omitempty"`
	N   [3]int    `json:"n"`
	Why string    `json:"why,omitempty"`
}

// LocalizeNote reads a schedule row's note in `lang`: a note written by
// wakeNote is composed in that language; a {lang: …} object (a row written
// before the record existed) is read the same way; anything else (an error)
// is returned as it is.
func LocalizeNote(note, lang string) string {
	if !strings.HasPrefix(note, "{") {
		return note
	}
	var rec wakeRecord
	if json.Unmarshal([]byte(note), &rec) == nil && rec.V == 2 {
		line := tallied{Scheduled: rec.N[0], Beyond: rec.N[1], Refused: rec.N[2], Reason: rec.Why}.In(lang)
		if a := strings.TrimSpace(rec.App.Get(lang)); a != "" {
			line = a + " — " + line
		}
		return line
	}
	var t wire.Text
	if json.Unmarshal([]byte(note), &t) != nil || t.Get("en") == "" {
		return note
	}
	return t.Get(lang)
}

// clipRunes shortens s to at most n runes (plus an ellipsis), never cutting a
// character in half — clip counts bytes, and half a "ş" is not valid UTF-8.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// tallied is what one wake-up's answer amounted to. It is written to the
// app's log and onto the wake-up row, because an app whose items are all
// being refused for the same reason every hour has to be able to SAY so
// somewhere an administrator looks.
type tallied struct {
	Scheduled int
	Beyond    int // due after the window: not an error, the next wake-up asks again
	Refused   int
	Reason    string // the first refusal, so the log names one concrete cause
}

func (t tallied) String() string { return t.In("en") }

// In is the tally in `lang`, from the server catalogue (`server.app.wake.*`).
// The refusal reason is the host's own validation message and stays as it
// was written.
func (t tallied) In(lang string) string {
	parts := []string{srvtext.Plural(lang, "server.app.wake.scheduled", t.Scheduled, nil)}
	if t.Beyond > 0 {
		parts = append(parts, srvtext.Plural(lang, "server.app.wake.beyond", t.Beyond, nil))
	}
	if t.Refused > 0 {
		parts = append(parts, srvtext.Plural(lang, "server.app.wake.refused", t.Refused, srvtext.Vars{"reason": t.Reason}))
	}
	return strings.Join(parts, srvtext.Text(lang, "server.list.separator", nil))
}

// storeItems validates and persists what a wake-up asked for.
//
// Validation is the whole safety story of an unattended run, so it is all
// here and it all fails CLOSED: a refused item is dropped and counted, never
// guessed at.
func (r *Registry) storeItems(ctx context.Context, p *Installed, items []wire.ScheduleItem, now, windowEnd time.Time) tallied {
	var t tallied
	refuse := func(key, why string) {
		t.Refused++
		if t.Reason == "" {
			t.Reason = why
		}
		p.log("warn", "schedule item "+key+" refused: "+why)
	}
	enabled, err := r.enabledActionIDs(ctx, p)
	if err != nil {
		r.log.Warn("app-plugins: overrides unreadable", slog.String("plugin", p.Row.Name), slog.String("err", err.Error()))
		enabled = nil
	}
	seen := map[string]bool{}
	for _, it := range items {
		if t.Scheduled >= MaxScheduleItems {
			refuse(it.Key, fmt.Sprintf("a wake-up may schedule at most %d items", MaxScheduleItems))
			continue
		}
		if !ValidScheduleKey(it.Key) {
			refuse(it.Key, "key must be 1 to 64 characters of letters, digits and _.:@-")
			continue
		}
		if seen[it.Key] {
			refuse(it.Key, "the same key was returned twice in one answer")
			continue
		}
		seen[it.Key] = true
		if _, ok := p.Manifest.Action(it.ActionID); !ok {
			refuse(it.Key, "this app has no action "+it.ActionID)
			continue
		}
		if enabled != nil && !enabled[it.ActionID] {
			refuse(it.Key, "the administrator switched "+it.ActionID+" off")
			continue
		}
		if it.DueAt.IsZero() {
			refuse(it.Key, "due_at is missing")
			continue
		}
		due := it.DueAt.UTC()
		if due.After(windowEnd) {
			// Not an error: the app is looking further ahead than this
			// window reaches. The wake-up whose window contains it will ask
			// again, and by then the app may have changed its mind.
			t.Beyond++
			continue
		}
		if due.Before(now) {
			// Already past — usually a wake-up that ran late. Run it now
			// rather than never; "no earlier than due" is the promise, not
			// "no later".
			due = now
		}
		storageID, rels, why := r.resolveSchedulePaths(ctx, it.Paths)
		if why != "" {
			refuse(it.Key, why)
			continue
		}
		params := it.Params
		if params == nil {
			params = map[string]any{}
		}
		pb, err := json.Marshal(params)
		if err != nil || len(pb) > maxScheduleParamsBytes {
			refuse(it.Key, fmt.Sprintf("params must be JSON of at most %d KiB", maxScheduleParamsBytes>>10))
			continue
		}
		if err := r.opts.Store.PutAppPluginScheduleItem(ctx, &model.AppPluginScheduleItem{
			PluginID: p.Row.ID, Key: it.Key, DueAt: due, ActionID: it.ActionID,
			StorageID: storageID, PathsJSON: jsonOf(rels), ParamsJSON: string(pb),
			Status: model.AppPluginScheduleDue,
		}); err != nil {
			refuse(it.Key, "could not be stored: "+err.Error())
			continue
		}
		t.Scheduled++
	}
	return t
}

// enabledActionIDs is the admin's on/off switch per action, as a set. The
// admin_only flag is not consulted: there is no caller to be an
// administrator, and an action only this app ever starts is exactly the
// `hidden` shape scheduled work takes.
func (r *Registry) enabledActionIDs(ctx context.Context, p *Installed) (map[string]bool, error) {
	effs, err := r.effectiveActions(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(effs))
	for _, e := range effs {
		out[e.Action.ID] = e.Enabled
	}
	return out, nil
}

// resolveSchedulePaths turns the adapter-qualified paths an app returned
// (`docs://reports/nda.pdf` — the spelling state_list answers with) into one
// storage id and storage-relative paths, or a reason it will not.
//
// Everything must name ONE storage, because an ops row has one. A storage
// that is disabled is refused rather than queued against: work scheduled
// onto a depo the operator switched off should say so now, not fail an hour
// later with a driver error.
func (r *Registry) resolveSchedulePaths(ctx context.Context, paths []string) (int64, []string, string) {
	if len(paths) == 0 {
		return 0, nil, "an item must name at least one file (a job runs on files)"
	}
	if len(paths) > MaxSchedulePaths {
		return 0, nil, fmt.Sprintf("an item may name at most %d files", MaxSchedulePaths)
	}
	var (
		storageID int64
		out       = make([]string, 0, len(paths))
	)
	for _, raw := range paths {
		adapter, rel, ok := splitQualified(raw)
		if !ok {
			return 0, nil, "paths must be adapter-qualified, as state_list answers them (docs://reports/nda.pdf): " + clip(raw, 80)
		}
		st, err := r.opts.Store.GetStorageByName(ctx, adapter)
		if err != nil || st == nil {
			return 0, nil, "no storage named " + adapter
		}
		if !st.Enabled {
			return 0, nil, "the storage " + adapter + " is switched off"
		}
		if storageID == 0 {
			storageID = st.ID
		} else if st.ID != storageID {
			return 0, nil, "one item's files must all live on one storage"
		}
		rel = strings.Trim(path.Clean("/"+rel), "/")
		if rel == "" || rel == "." {
			return 0, nil, "a path names no file: " + clip(raw, 80)
		}
		out = append(out, rel)
	}
	return storageID, out, ""
}

// splitQualified splits `name://rel`. A path without an adapter is refused
// rather than assumed: an unattended run has no "current storage".
func splitQualified(raw string) (string, string, bool) {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	i := strings.Index(raw, "://")
	if i <= 0 {
		return "", "", false
	}
	return raw[:i], raw[i+3:], true
}

// ── Running one due item ───────────────────────────────────────────────

// runScheduledItem hands one due item to the ops queue as an ordinary plugin
// job. From here it is indistinguishable from a job a person started, except
// that its actor is SYSTEM: the ops row, the job row, the progress, the
// outputs and the cancel button are the ones that already exist.
func (r *Registry) runScheduledItem(ctx context.Context, p *Installed, row *model.AppPluginScheduleItem) {
	if r.queue == nil {
		r.finish(ctx, row, model.AppPluginScheduleFailed, "", "no ops queue is wired on this instance", nil)
		return
	}
	action, ok := p.Manifest.Action(row.ActionID)
	if !ok {
		r.finish(ctx, row, model.AppPluginScheduleFailed, "", "the action "+row.ActionID+" no longer exists", nil)
		return
	}
	// Re-read the admin's switch AT RUN TIME, not only when it was
	// scheduled: an action turned off during the hour must not run.
	if enabled, err := r.enabledActionIDs(ctx, p); err == nil && !enabled[row.ActionID] {
		r.finish(ctx, row, model.AppPluginScheduleSkipped, "", "the administrator switched "+row.ActionID+" off", nil)
		return
	}
	var rels []string
	if err := json.Unmarshal([]byte(row.PathsJSON), &rels); err != nil || len(rels) == 0 {
		r.finish(ctx, row, model.AppPluginScheduleFailed, "", "the item names no files", nil)
		return
	}
	job := &model.AppPluginJob{
		ID: NewJobID(), PluginID: p.Row.ID, PluginName: p.Row.Name, ActionID: row.ActionID,
		StorageID: row.StorageID, PathsJSON: row.PathsJSON, ParamsJSON: row.ParamsJSON,
		// ActorID nil is SYSTEM, which is what the ops row means by a job
		// nobody asked for. There is no person to attribute this to and
		// pretending otherwise would put somebody's name on work they did
		// not start.
		ActorID: nil, Locale: "", Label: action.Label.Get(""), Status: model.AppPluginJobPending,
	}
	if err := r.opts.Store.CreateAppPluginJob(ctx, job); err != nil {
		r.finish(ctx, row, model.AppPluginScheduleFailed, "", "job row: "+err.Error(), nil)
		return
	}
	op, err := r.queue.SubmitTo(ctx, ops.OpPluginAction, row.StorageID, row.StorageID, rels, job.ID)
	if err != nil {
		r.finish(ctx, row, model.AppPluginScheduleFailed, job.ID, "queue: "+err.Error(), nil)
		return
	}
	// ⚠ One column, not the row: a short action can finish before this line
	// runs, and writing the whole row back would erase what the worker just
	// wrote (see db.Store.SetAppPluginJobOp).
	_ = r.opts.Store.SetAppPluginJobOp(ctx, job.ID, op.ID)
	p.log("info", fmt.Sprintf("scheduled %s ran on time (%s, job %s, op %d)", row.ActionID, row.Key, job.ID, op.ID))
	r.finish(ctx, row, model.AppPluginScheduleQueued, job.ID, "", nil)
}

// ── Housekeeping ───────────────────────────────────────────────────────

// SweepSchedule drops finished rows whose due time is past the retention
// window. Called once an hour from the schedule loop.
//
// It measures on due_at rather than updated_at on purpose: due_at is always
// written from Go and updated_at from the engine's CURRENT_TIMESTAMP, and
// comparing a Go time against an engine-formatted column is how a sweep
// silently stops matching anything.
func (r *Registry) SweepSchedule(ctx context.Context) int64 {
	if r == nil {
		return 0
	}
	n, err := r.opts.Store.ReapAppPluginScheduleItems(ctx, time.Now().UTC().Add(-scheduleRetention))
	if err != nil {
		r.log.Warn("app-plugins: schedule sweep failed", slog.String("err", err.Error()))
		return 0
	}
	if n > 0 {
		r.log.Info("app-plugins: swept finished schedule rows", slog.Int64("removed", n))
	}
	return n
}

// ScheduleOf returns one app's schedule rows, soonest first — what an admin
// screen would draw and what a test reads back.
func (r *Registry) ScheduleOf(ctx context.Context, id int64) ([]*model.AppPluginScheduleItem, error) {
	return r.opts.Store.ListAppPluginScheduleItems(ctx, id)
}
