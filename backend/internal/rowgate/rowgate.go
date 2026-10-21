// Package rowgate keeps a storage's catalogue judgements apart from the
// changes filex makes to that storage in two steps.
//
// # The two steps
//
// A rename, a move, a delete into the trash and a restore from it change a
// storage first and the catalogue after: the bytes move (Mover.Move,
// trash.Put), then the rows follow (protocolsync.MoveRows, SoftDeleteAndRetag,
// RestoreNodeAt). Between the two, the catalogue says one thing and the
// storage another, and nothing on either side can tell that apart from a
// change made outside filex.
//
// # The judgements
//
// The storage sync reads exactly that difference. A walk lists a directory
// and turns what it sees into rows; the tombstone pass takes a row the walk
// did not see, asks the storage for its object, and drops the row when the
// answer is "not found". Run inside the gap, both are wrong about a row that
// is fine:
//
//   - a folder listed in its parent just before the rename, then listed
//     itself just after it, looked empty, so everything below it looked gone
//     (issue #192: a queued folder rename opened empty in the explorer, e2e
//     159 and 172 under load);
//   - a row whose object had already moved, but which still named the old
//     path, was confirmed gone by a Stat of that path and dropped - with its
//     shares, versions and comments - a moment before the rename re-homed it;
//   - a listing taken after the bytes moved but before the rows did would
//     find an object with no row and catalogue it as a NEW file, which would
//     then hold the name the rename's own row is moving to.
//
// # The gate
//
// So each storage has one gate. A two-step change holds it SHARED (Move,
// MoveCtx) from the first byte it moves to the last row it writes; any number
// of them run at once. A judgement holds it ALONE (JudgeCtx): the walk while
// it lists one directory and applies that listing, the tombstone pass while it
// confirms and drops one batch of rows, the lazy catalogue while it reconciles
// one folder. A judgement therefore sees a storage either before a change or
// after it, never half way.
//
// # Changes first, judgements step back
//
// The gate favours the changes. A judgement never queues ahead of them: it
// takes the gate only while no change holds it, and while one does it steps
// back and waits for the gate to come free (on its context, and within the
// budget its caller gives it: JudgeWithin), without keeping any change out.
// A change waits only while a judgement actually HOLDS the gate - one
// directory, one batch of rows, one folder - and, through MoveCtx, on the
// context of whoever asked, so a client that gives up while it waits takes
// nothing. Up to 0.54 the gate was a sync.RWMutex, which favours the writer:
// one judgement waiting behind a long change (a folder moved or deleted object
// by object over WebDAV or SFTP) kept every later change on that storage out,
// and the operations queue's one worker with them, until the long change
// finished. Now a long change only postpones the storage's scan, and says so
// (Snapshot, the filex_rowgate_* metrics, a warning once it has held the gate
// for LongHold).
//
// The other side of that choice: a storage under a steady stream of
// overlapping changes is not judged until the stream pauses. Its scan is
// deferred, never wrong.
//
// # A change that waits is first too
//
// ⚠⚠ "Changes first" holds for a change that WAITS, not only for one that holds
// the gate (issue #127). A scan lets go of the gate after one directory and
// asks for it again microseconds later for the next one; a change that only
// looked whether the gate was free, or woke when it opened, lost that race
// nearly every time. On a local storage of tens of thousands of entries the
// operations queue's delete sat "Queued" for the whole scan, minutes, and ran
// two seconds after it ended. So the gate counts the changes waiting for it:
// those waiting in MoveCtx, FenceCtx or Await while a judgement holds it, and
// those announced with Want (the operations queue never waits in the gate - its
// one worker would stand there - and announces the job it put back instead). A
// judgement steps back for a waiting change as for a holding one: the scan
// finishes the directory or batch in hand and takes the next only after the
// change had its turn. A Want stands for a short while only (WantFor, well
// under the scan's own budget), so a job that is not run after all - its lane
// busy elsewhere, its row cancelled - holds the scan back that long, no longer.
//
// # Fences
//
// A folder moved or trashed object by object (an object store) is the long
// change that matters, and since 0.55 it does not hold the gate at all: it
// fences the folder's prefixes (FenceCtx, HoldCtx; fence.go says why that is
// enough and how the two are ordered). Judgements read the fence set once they
// hold the gate (Fences) and leave those prefixes alone; the rest of the
// storage is scanned as usual.
//
// ⚠ Never hold Move and ask for Judge on the same goroutine: the judgement
// waits for every change to let go, its own caller's included. The same goes
// for a Want: a judgement asked under one waits until it runs out. Take the gate
// at the outermost step of the change (the queue's job, the request handler, a
// protocol server's verb through protocolsync's Relocate, Discard and Purge),
// not inside the shared helpers it calls, and never twice for one change.
// Every surface that changes a storage in two steps holds it since 0.55 (issue
// #201; docs/ARCHITECTURE.md "The row gate" lists them).
// Take it before opening a database transaction, never inside one: on SQLite
// a transaction holds the only connection, and the other side may be waiting
// for that connection while it holds the gate.
//
// The gate is per process. Two filex processes over one database (a replica
// beside the main instance) do not see each other's gate; what narrows the gap
// there is that the tombstone pass reads a row again before it judges it and
// again before it drops it (internal/sync stillAsListed).
package rowgate

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ErrBusy is a judgement that stepped back for as long as its caller allowed
// (JudgeWithin) and found the gate held by a change all that time. The caller
// defers what it was going to judge; nothing was judged.
var ErrBusy = errors.New("rowgate: the storage is being changed; its judgement is deferred")

// LongHold is how long a change may hold the gate before it is reported: a
// warning when it lets go, and filex_rowgate_long_changes_total. The gate is
// not taken from it - a change finishes what it started - but the storage's
// scan has waited that long.
var LongHold = time.Minute

type gate struct {
	mu sync.Mutex
	// changes holding the gate now, by token, with when each took it.
	changes map[uint64]time.Time
	next    uint64
	// judged: a judgement holds the gate alone.
	judged bool
	// waiting counts the changes waiting for the gate now (MoveCtx, FenceCtx,
	// Await while a judgement holds it); wants the ones announced without
	// waiting (Want) that still stand. A judgement does not take the gate
	// while either is above zero (open).
	waiting, wants int
	// eased is closed, and replaced, every time a holder lets go, a waiting
	// change stops waiting or a Want ends: whoever waits for the gate tries
	// again then.
	eased chan struct{}

	// moves counts the two-step changes that have finished on the storage
	// since the process started (Moves).
	moves atomic.Uint64
	// long counts changes that held the gate for LongHold or more; deferred
	// counts judgements that stopped waiting for it (ErrBusy, or their
	// context ended); lapsed counts Wants that ran out before their change
	// called them off.
	long, deferred, lapsed atomic.Uint64

	// fences holds the long changes fencing prefixes of the storage now
	// (FenceCtx), by token; fenced is the set they make, rebuilt under mu
	// whenever one comes or goes and read without a lock (Fences).
	fences map[uint64]fenceHold
	fenced atomic.Pointer[FenceSet]
}

// gates holds one gate per storage id. A gate is never removed: its move
// count is what a walk compares (Moves), and a gate dropped and created again
// would start counting from zero and could answer "unchanged" for a storage
// that changed. Storages are few, and their ids are never reused.
var gates sync.Map // int64 -> *gate

func of(storageID int64) *gate {
	if g, ok := gates.Load(storageID); ok {
		return g.(*gate)
	}
	g, _ := gates.LoadOrStore(storageID, &gate{changes: map[uint64]time.Time{}, fences: map[uint64]fenceHold{}, eased: make(chan struct{})})
	return g.(*gate)
}

// ease wakes whoever waits for the gate. Called with g.mu held.
func (g *gate) ease() {
	close(g.eased)
	g.eased = make(chan struct{})
}

// open reports whether a judgement may take the gate now: nothing holds it,
// and no change waits for it or has announced itself (Want). Called with g.mu
// held.
func (g *gate) open() bool {
	return !g.judged && len(g.changes) == 0 && g.waiting == 0 && g.wants == 0
}

// whenFree waits on ctx until no judgement holds the gate and then runs take
// with g.mu held, in the same instant: what a change takes (MoveCtx's place
// among the holders, FenceCtx's fence, nothing for Await). While it waits it
// is counted among the changes waiting for the gate, and a judgement steps
// back for it (open). On ctx's end it takes nothing and answers ctx's error,
// also when the gate is free (a request already gone starts nothing).
func (g *gate) whenFree(ctx context.Context, take func()) error {
	waits := false
	for {
		if err := ctx.Err(); err != nil {
			g.stopWaiting(waits)
			return err
		}
		g.mu.Lock()
		if !g.judged {
			take()
			if waits {
				g.waiting--
				g.ease()
			}
			g.mu.Unlock()
			return nil
		}
		if !waits {
			g.waiting++
			waits = true
		}
		wait := g.eased
		g.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			g.stopWaiting(waits)
			return ctx.Err()
		}
	}
}

// stopWaiting takes a change that gave up out of the waiting count, and wakes
// the judgements stepping back for it.
func (g *gate) stopWaiting(waits bool) {
	if !waits {
		return
	}
	g.mu.Lock()
	g.waiting--
	g.ease()
	g.mu.Unlock()
}

// enter records one change holding the gate. Called with g.mu held.
func (g *gate) enter() uint64 {
	g.next++
	g.changes[g.next] = time.Now()
	return g.next
}

// leaver is the release of change tok on storageID's gate.
func (g *gate) leaver(storageID int64, tok uint64) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			// Counted before the gate opens, so the judgement that gets the
			// gate next sees the change it waited for (Moves).
			g.moves.Add(1)
			g.mu.Lock()
			since := g.changes[tok]
			delete(g.changes, tok)
			g.ease()
			g.mu.Unlock()
			if held := time.Since(since); held >= LongHold {
				g.long.Add(1)
				slog.Warn("rowgate: a change held the storage's row gate for a long time; its scan waited that long",
					slog.Int64("storage", storageID),
					slog.Duration("held", held))
			}
		})
	}
}

func noop() {}

// MoveCtx holds storageID's gate for one two-step change: call it before the
// first byte moves and call release once the last row has followed (release
// is safe to call more than once; only the first call counts). Any number of
// changes hold the gate at once, and a judgement waiting for it keeps none of
// them out; a change waits only while a judgement holds the gate.
//
// It waits on ctx - the request's own context, before the change detaches
// from the client (storage.DetachMutation): a client that has gone, or goes
// while the change waits, takes nothing, and the change is not made. It then
// answers ctx's error and a release that does nothing.
//
// While it waits it is counted among the changes waiting for the gate: the
// judgement holding the gate finishes its directory or batch, and no new one
// starts before this change has the gate (issue #127).
func MoveCtx(ctx context.Context, storageID int64) (release func(), err error) {
	g := of(storageID)
	var tok uint64
	if err := g.whenFree(ctx, func() { tok = g.enter() }); err != nil {
		return noop, err
	}
	return g.leaver(storageID, tok), nil
}

// Move is MoveCtx for a change nobody can call off while it waits (the
// operations queue's job, which has no client): it waits for as long as a
// judgement holds the gate.
func Move(storageID int64) (release func()) {
	release, _ = MoveCtx(context.Background(), storageID)
	return release
}

// TryMove holds storageID's gate for one change if no judgement holds it now,
// and reports whether it did. It never waits.
func TryMove(storageID int64) (release func(), ok bool) {
	g := of(storageID)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.judged {
		return noop, false
	}
	return g.leaver(storageID, g.enter()), true
}

// Judged reports whether a judgement holds storageID's gate right now: a
// change asking for it now would wait. The operations queue asks it before it
// starts a job, and puts the job back rather than wait - and announces it
// (Want), or the next judgement takes the gate before the job asks again.
func Judged(storageID int64) bool {
	g := of(storageID)
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.judged
}

// Await waits on ctx until no judgement holds storageID's gate, and takes
// nothing. A change whose first step runs outside the gate - a copy to another
// storage before the source is deleted under it - asks it before that step,
// so a request that goes while a judgement holds the gate starts nothing.
// While it waits it is counted among the changes waiting for the gate, as
// MoveCtx is: the judgement holding the gate is the last one before it.
func Await(ctx context.Context, storageID int64) error {
	return of(storageID).whenFree(ctx, func() {})
}

// WantFor is how long a Want stands when its caller names no time of its own.
// Well under the storage sync's GateWait (30 s): a judgement steps back for a
// Want, and one that stepped back for all of GateWait defers the whole pass.
var WantFor = 10 * time.Second

// Want announces a change that will ask for storageID's gate soon, without
// waiting for it: from now on, for at most ttl (WantFor when ttl <= 0), no new
// judgement takes the gate, as if the change were waiting in MoveCtx. The
// judgement holding the gate now finishes what it holds (Await says when).
// cancel ends the Want - once the change holds the gate, or will not run; it
// is safe to call more than once, and after the Want ran out.
//
// The operations queue announces each job it puts back for a judgement
// (internal/ops gate_lane.go): its one worker never waits in the gate, so
// without this the job asked again only at random moments, and a scan that
// lets go of the gate for microseconds between two directories was nearly
// always holding it again (issue #127).
//
// ⚠ Never hold a Want and ask for a judgement of the same storage on the same
// goroutine: the judgement waits for the Want, which then ends only by
// running out.
func Want(storageID int64, ttl time.Duration) (cancel func()) {
	if ttl <= 0 {
		ttl = WantFor
	}
	g := of(storageID)
	g.mu.Lock()
	g.wants++
	g.mu.Unlock()
	var once sync.Once
	end := func(lapsed bool) {
		once.Do(func() {
			if lapsed {
				g.lapsed.Add(1)
			}
			g.mu.Lock()
			g.wants--
			g.ease()
			g.mu.Unlock()
		})
	}
	timer := time.AfterFunc(ttl, func() { end(true) })
	return func() {
		timer.Stop()
		end(false)
	}
}

// Change runs one two-step change under storageID's gate (Move): move, the
// storage side, and then follow, the catalogue side, which runs only when
// move succeeded. It answers move's error.
func Change(storageID int64, move func() error, follow func()) error {
	return ChangeCtx(context.Background(), storageID, move, follow)
}

// ChangeCtx is Change waiting for the gate on ctx (MoveCtx): when ctx ends
// first, neither move nor follow runs and it answers ctx's error.
func ChangeCtx(ctx context.Context, storageID int64, move func() error, follow func()) error {
	release, err := MoveCtx(ctx, storageID)
	if err != nil {
		return err
	}
	defer release()
	if err := move(); err != nil {
		return err
	}
	follow()
	return nil
}

// JudgeCtx holds storageID's gate alone for one judgement of the catalogue
// against the storage. It tries the gate; while a change holds it, waits for
// it or has announced itself (Want), the judgement steps back until the gate
// is free - without keeping any change out in the meantime - or ctx ends,
// which it answers (and a release that does nothing). release is safe to call
// more than once.
func JudgeCtx(ctx context.Context, storageID int64) (release func(), err error) {
	g := of(storageID)
	for {
		g.mu.Lock()
		if g.open() {
			g.judged = true
			g.mu.Unlock()
			return g.judgeLeaver(), nil
		}
		wait := g.eased
		g.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			g.deferred.Add(1)
			return noop, ctx.Err()
		}
	}
}

// JudgeWithin is JudgeCtx for at most wait: a judgement still stepped back
// when wait has passed gives up with ErrBusy (ctx's own end is answered as
// ctx's error). The storage sync judges this way, and defers what it could not
// judge to its next pass.
func JudgeWithin(ctx context.Context, storageID int64, wait time.Duration) (release func(), err error) {
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	release, err = JudgeCtx(wctx, storageID)
	if err != nil && ctx.Err() == nil {
		return noop, ErrBusy
	}
	return release, err
}

// TryJudge holds storageID's gate alone if nothing holds it now and no change
// waits for it, and reports whether it did. It never waits.
func TryJudge(storageID int64) (release func(), ok bool) {
	g := of(storageID)
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.open() {
		return noop, false
	}
	g.judged = true
	return g.judgeLeaver(), true
}

// judgeLeaver is the release of the judgement holding g.
func (g *gate) judgeLeaver() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.judged = false
			g.ease()
			g.mu.Unlock()
		})
	}
}

// Judge is JudgeCtx with no end: it waits until the gate is free. Tests use
// it; the storage sync judges within a budget (JudgeWithin).
func Judge(storageID int64) (release func()) {
	release, _ = JudgeCtx(context.Background(), storageID)
	return release
}

// Moves is how many two-step changes (Move) have finished on storageID since
// the process started. A walk that read the whole storage up front (an object
// store's one-pass listing) compares it with the count it read BEFORE that
// listing: once it differs, the listing in memory may predate a change whose
// rows have already moved, and the walk asks the storage again instead.
func Moves(storageID int64) uint64 {
	return of(storageID).moves.Load()
}

// State is one storage's gate as Snapshot reads it.
type State struct {
	StorageID int64
	// Changes is how many two-step changes hold the gate now; OldestChange
	// is how long the one that took it first has held it (0 with none).
	Changes      int
	OldestChange time.Duration
	// Judged: a judgement holds the gate now.
	Judged bool
	// Waiting is how many changes wait for the gate now: in MoveCtx, FenceCtx
	// or Await while a judgement holds it, or announced (Want). No new
	// judgement starts while it is above zero.
	Waiting int
	// Fences is how many prefixes long changes fence now (FenceCtx), and
	// OldestFence how long the fence that came first has stood (0 with
	// none).
	Fences      int
	OldestFence time.Duration
	// LongChanges counts the changes that held the gate for LongHold or
	// more; DeferredJudgements the judgements that stopped waiting for it;
	// LapsedWants the Wants that ran out before their change called them off
	// (its job was not run in time). All since the process started.
	LongChanges        uint64
	DeferredJudgements uint64
	LapsedWants        uint64
}

// Snapshot reads every storage's gate, ordered by storage id (the metrics
// publish it at scrape time: internal/metrics).
func Snapshot() []State {
	now := time.Now()
	var out []State
	gates.Range(func(k, v any) bool {
		g := v.(*gate)
		s := State{StorageID: k.(int64), LongChanges: g.long.Load(), DeferredJudgements: g.deferred.Load(), LapsedWants: g.lapsed.Load()}
		g.mu.Lock()
		s.Changes, s.Judged, s.Waiting = len(g.changes), g.judged, g.waiting+g.wants
		for _, t := range g.changes {
			if d := now.Sub(t); d > s.OldestChange {
				s.OldestChange = d
			}
		}
		for _, f := range g.fences {
			if d := now.Sub(f.since); d > s.OldestFence {
				s.OldestFence = d
			}
		}
		s.Fences = len(g.fenced.Load().prefixes())
		g.mu.Unlock()
		out = append(out, s)
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].StorageID < out[j].StorageID })
	return out
}
