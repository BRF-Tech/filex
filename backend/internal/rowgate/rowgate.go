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
// So each storage has one gate. A two-step change holds it SHARED (Move) from
// the first byte it moves to the last row it writes; any number of them run at
// once. A judgement holds it ALONE (Judge): the walk while it lists one
// directory and applies that listing, the tombstone pass while it confirms and
// drops, the lazy catalogue while it reconciles one folder. A judgement
// therefore sees a storage either before a change or after it, never half way.
//
// ⚠ Never hold Move and ask for Judge on the same goroutine (or the other way
// round), and never take Move twice on one goroutine: a judgement waiting for
// the gate keeps new holders out, so the second Move waits for a Judge that
// waits for the first. Take the gate at the outermost step of the change (the
// queue's job, the request handler), not inside the shared helpers it calls.
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
	"sync"
	"sync/atomic"
)

type gate struct {
	mu sync.RWMutex
	// moves counts the two-step changes that have finished on the storage
	// since the process started (Moves).
	moves atomic.Uint64
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
	g, _ := gates.LoadOrStore(storageID, &gate{})
	return g.(*gate)
}

// Move holds storageID's gate for one two-step change: call it before the
// first byte moves and call release once the last row has followed (release
// is safe to call more than once; only the first call counts). Any number of
// changes hold the gate at once; a judgement waits until they have all let
// go, and a change that arrives while a judgement holds the gate waits for it.
func Move(storageID int64) (release func()) {
	g := of(storageID)
	g.mu.RLock()
	var once sync.Once
	return func() {
		once.Do(func() {
			// Counted before the gate opens, so the judgement that gets the
			// gate next sees the change it waited for (Moves).
			g.moves.Add(1)
			g.mu.RUnlock()
		})
	}
}

// Change runs one two-step change under storageID's gate (Move): move, the
// storage side, and then follow, the catalogue side, which runs only when
// move succeeded. It answers move's error.
func Change(storageID int64, move func() error, follow func()) error {
	release := Move(storageID)
	defer release()
	if err := move(); err != nil {
		return err
	}
	follow()
	return nil
}

// Judge holds storageID's gate alone for one judgement of the catalogue
// against the storage. release is safe to call more than once.
func Judge(storageID int64) (release func()) {
	g := of(storageID)
	g.mu.Lock()
	var once sync.Once
	return func() { once.Do(g.mu.Unlock) }
}

// Moves is how many two-step changes (Move) have finished on storageID since
// the process started. A walk that read the whole storage up front (an object
// store's one-pass listing) compares it with the count it read BEFORE that
// listing: once it differs, the listing in memory may predate a change whose
// rows have already moved, and the walk asks the storage again instead.
func Moves(storageID int64) uint64 {
	return of(storageID).moves.Load()
}
