// Package watchin is filex's side of filex-watch once a report has been let
// in (handlers/storage_watch.go checks the key, the storage and the report):
// it gathers the folders the agents report per storage, waits for a burst to
// go quiet, and hands them to the catalogue (sync.Worker.ExternalChanges /
// ExternalResync) - and it remembers what each key's agent last said about
// itself, for the storage page (agents.go).
//
// Nothing here is stored: a report is a hint that makes the catalogue look
// sooner, never the catalogue itself. What a restart loses, the agent's next
// report makes up for: the first report a key sends after filex starts is
// taken as a resync (Agents.Note), and an agent that lost its connection says
// resync itself.
package watchin

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Rescanner is the catalogue's half. sync.Worker implements it; each method
// answers the folders it could not look at yet (a scan holds the storage),
// which the inbox hands back after RetryAfter.
type Rescanner interface {
	ExternalChanges(ctx context.Context, storageID int64, dirs []string) ([]string, error)
	ExternalResync(ctx context.Context, storageID int64, dirs []string) ([]string, error)
}

// Defaults of an Inbox. The agent already gathers a burst for a second before
// it sends; Quiet is the server's own pause, so reports from several agents of
// one storage, or several batches of one long copy, become one pass.
const (
	DefaultQuiet      = 2 * time.Second
	DefaultMaxWait    = 15 * time.Second
	DefaultRetryAfter = 5 * time.Second
	// DefaultMaxPending is how many folders one storage may have waiting.
	// Past it the folders are dropped and the storage is resynced whole:
	// one pass over everything instead of thousands of single ones.
	DefaultMaxPending = 4096
)

// Inbox gathers reported folders per storage and drains each storage on a
// goroutine of its own, so one storage's long rescan never holds another's
// back. Build it with New; the timing fields may be changed before first use.
type Inbox struct {
	r          Rescanner
	Quiet      time.Duration
	MaxWait    time.Duration
	RetryAfter time.Duration
	MaxPending int
	// Gone reports an error that means nothing will ever look at the storage
	// (the server: sync.ErrNoSyncer - deleted or switched off). Its folders
	// are dropped instead of retried. Nil: no error is.
	Gone func(error) bool

	mu    sync.Mutex
	boxes map[int64]*box
	// now is the clock; nil = time.Now.
	now func() time.Time
}

type box struct {
	changed map[string]struct{}
	resync  map[string]struct{}
	// first and last are when the current burst started and last grew.
	first, last time.Time
	// notBefore holds the next pass back after a scan held the storage.
	notBefore time.Time
	draining  bool
}

// New is an inbox that hands its folders to r.
func New(r Rescanner) *Inbox {
	return &Inbox{
		r:          r,
		Quiet:      DefaultQuiet,
		MaxWait:    DefaultMaxWait,
		RetryAfter: DefaultRetryAfter,
		MaxPending: DefaultMaxPending,
		boxes:      map[int64]*box{},
	}
}

func (in *Inbox) clock() time.Time {
	if in.now != nil {
		return in.now()
	}
	return time.Now()
}

// Add queues folders of storageID: changed (their direct contents changed)
// and resync (to be brought up to date whole; "/" is the storage). Paths are
// canonical (watchproto.CleanFolder). It never waits for the catalogue.
func (in *Inbox) Add(storageID int64, changed, resync []string) {
	if len(changed) == 0 && len(resync) == 0 {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	b := in.boxes[storageID]
	if b == nil {
		b = &box{changed: map[string]struct{}{}, resync: map[string]struct{}{}}
		in.boxes[storageID] = b
	}
	now := in.clock()
	if len(b.changed) == 0 && len(b.resync) == 0 {
		b.first = now
	}
	b.last = now
	for _, d := range resync {
		b.resync[d] = struct{}{}
	}
	for _, d := range changed {
		b.changed[d] = struct{}{}
	}
	b.collapse(in.MaxPending)
	if !b.draining {
		b.draining = true
		go in.drain(storageID)
	}
}

// collapse keeps a box within limit folders: past it, the storage is resynced
// whole and the single folders are dropped. A resync of the whole storage
// also makes every other resync moot.
func (b *box) collapse(limit int) {
	if limit > 0 && len(b.changed)+len(b.resync) > limit {
		b.changed = map[string]struct{}{}
		b.resync = map[string]struct{}{"/": {}}
		return
	}
	if _, whole := b.resync["/"]; whole && len(b.resync) > 1 {
		b.resync = map[string]struct{}{"/": {}}
	}
}

// due is how long the box still waits before its pass, 0 when it is due: the
// burst has been quiet for Quiet, or has lasted MaxWait - and in either case
// not before notBefore.
func (in *Inbox) due(b *box, now time.Time) time.Duration {
	wait := max(b.last.Add(in.Quiet).Sub(now), 0)
	if m := max(b.first.Add(in.MaxWait).Sub(now), 0); m < wait {
		wait = m
	}
	if nb := b.notBefore.Sub(now); nb > wait {
		wait = nb
	}
	return wait
}

// drain runs the passes of one storage until its box is empty.
func (in *Inbox) drain(storageID int64) {
	for {
		in.mu.Lock()
		b := in.boxes[storageID]
		if b == nil || len(b.changed)+len(b.resync) == 0 {
			delete(in.boxes, storageID)
			in.mu.Unlock()
			return
		}
		if wait := in.due(b, in.clock()); wait > 0 {
			in.mu.Unlock()
			time.Sleep(wait)
			continue
		}
		changed, resync := keys(b.changed), keys(b.resync)
		b.changed, b.resync = map[string]struct{}{}, map[string]struct{}{}
		in.mu.Unlock()

		laterR, laterC, gone := in.pass(storageID, changed, resync)

		in.mu.Lock()
		if gone {
			// Nothing will look at this storage, now or after a retry; what
			// came in meanwhile goes the same way.
			delete(in.boxes, storageID)
			in.mu.Unlock()
			return
		}
		if len(laterR)+len(laterC) > 0 {
			for _, d := range laterR {
				b.resync[d] = struct{}{}
			}
			for _, d := range laterC {
				b.changed[d] = struct{}{}
			}
			b.collapse(in.MaxPending)
			b.notBefore = in.clock().Add(in.RetryAfter)
		}
		in.mu.Unlock()
	}
}

// pass hands one gathered set to the catalogue. gone: nothing will ever look
// at the storage.
func (in *Inbox) pass(storageID int64, changed, resync []string) (laterR, laterC []string, gone bool) {
	ctx := context.Background()
	var err error
	if len(resync) > 0 {
		laterR, err = in.r.ExternalResync(ctx, storageID, resync)
		if in.isGone(err) {
			return nil, nil, true
		}
		logPass(storageID, "resync", err)
	}
	if len(changed) > 0 {
		laterC, err = in.r.ExternalChanges(ctx, storageID, changed)
		if in.isGone(err) {
			return nil, nil, true
		}
		logPass(storageID, "changes", err)
	}
	return laterR, laterC, false
}

func (in *Inbox) isGone(err error) bool {
	return err != nil && in.Gone != nil && in.Gone(err)
}

func logPass(storageID int64, what string, err error) {
	if err != nil {
		slog.Warn("watch: a reported change could not be handed to the catalogue",
			slog.Int64("storage", storageID), slog.String("what", what), slog.String("err", err.Error()))
	}
}

// Pending reports how many folders storageID has waiting.
func (in *Inbox) Pending(storageID int64) int {
	in.mu.Lock()
	defer in.mu.Unlock()
	if b := in.boxes[storageID]; b != nil {
		return len(b.changed) + len(b.resync)
	}
	return 0
}

func keys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
