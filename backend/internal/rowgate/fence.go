package rowgate

import (
	"context"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// # Fences: a long change holds its prefixes, not the storage
//
// A folder moved or put in the trash on an object store is not one call but a
// copy and a delete for every object under it: minutes, or hours, for a large
// folder. Held with MoveCtx, the gate is held for all of it, and the storage's
// whole scan - every directory, every tombstone batch, every lazy folder - is
// deferred until it ends, although only the folder being moved is half way.
//
// FenceCtx holds prefixes instead: the folder's path and its destination's. A
// judgement takes the gate as if no change held it (a fence is not a change in
// the gate's count), and then asks the fence set (Fences) what it may not look
// at: a directory at or below a fenced prefix is not listed this pass, and a
// fenced child is left out of its parent's listing, so neither its rows nor
// its objects are judged. The rest of the storage is scanned as usual.
//
// Why that is enough: everything the change makes half way lives under the two
// prefixes. Below the source, objects are gone whose rows are still there;
// below the destination, objects have come whose rows are not there yet; the
// folder's own entries in its old and new parent are the fenced children. A
// judgement that looks at none of it sees the storage as it was before the
// change or as it will be after it.
//
// The order the two need:
//
//   - A fence is set only while no judgement holds the gate (it waits for one
//     that does, on ctx, like MoveCtx), so a judgement either started before
//     the fence - and finished before the first byte moved - or asks the fence
//     set after it, under the gate, and sees it.
//   - A fence is let go only after the change's rows have followed its bytes,
//     and it counts as a finished change (Moves) before it opens, so a walk
//     reading a tree it fetched before the change asks the storage again.
//   - A judgement never waits for a fence and never takes one; a change holding
//     a fence never needs the gate. So there is no order between the two to
//     get wrong: HoldCtx takes ONE of them for a change - the fence for a long
//     change, the gate for any other - and nothing takes both.
//
// A fence on the storage's root covers everything: it is the gate (HoldCtx and
// FenceCtx take MoveCtx for it).

// fenceHold is one long change's fence.
type fenceHold struct {
	prefixes []string
	since    time.Time
}

// FenceSet is the prefixes fenced on one storage at one moment. It is never
// changed once built: Fences hands out the current one. A nil set fences
// nothing.
type FenceSet struct {
	// list is the fenced prefixes, cleaned ("/a/b"), each once; under is the
	// same with a trailing slash, for Covers.
	list, under []string
	// kids maps a directory to its fenced children (cleaned full paths).
	kids map[string][]string
}

func (f *FenceSet) prefixes() []string {
	if f == nil {
		return nil
	}
	return f.list
}

// Empty reports whether nothing is fenced.
func (f *FenceSet) Empty() bool { return f == nil || len(f.list) == 0 }

// Covers reports whether p is a fenced prefix or below one. With nothing
// fenced it costs nothing; else one comparison per fenced prefix (a long
// change fences two).
func (f *FenceSet) Covers(p string) bool {
	if f.Empty() {
		return false
	}
	c := Clean(p)
	for i, pre := range f.list {
		if c == pre || strings.HasPrefix(c, f.under[i]) {
			return true
		}
	}
	return false
}

// Kids answers dir's fenced children, as cleaned full paths (one map lookup):
// what a listing of dir must leave out.
func (f *FenceSet) Kids(dir string) []string {
	if f.Empty() {
		return nil
	}
	return f.kids[Clean(dir)]
}

// Clean is the form fences are kept and compared in: rooted, no trailing
// slash, no "." or ".." ("a/b/" and "/a/b" are one prefix).
func Clean(p string) string { return path.Clean("/" + p) }

// Fences answers storageID's fence set as it stands: a lock-free load. A
// judgement asks it once it holds the gate (JudgeCtx), so a fence it does not
// see was not set before the judgement began, and cannot be until it ends.
func Fences(storageID int64) *FenceSet {
	return of(storageID).fenced.Load()
}

// FenceCtx fences prefixes of storageID for one long change: call it before the
// first object moves and call release once the last row has followed (release
// is safe to call more than once; only the first call counts, and it counts as
// a finished change, Moves). It waits, on ctx, only while a judgement holds
// the gate; a ctx that ends first takes nothing and answers its error and a
// release that does nothing. While the fence stands, judgements take the gate
// and leave the prefixes alone (Fences); no change waits for it.
//
// Empty prefixes are ignored; with none left, or with the storage's root among
// them, it is MoveCtx.
func FenceCtx(ctx context.Context, storageID int64, prefixes ...string) (release func(), err error) {
	ps, whole := cleanPrefixes(prefixes)
	if whole || len(ps) == 0 {
		return MoveCtx(ctx, storageID)
	}
	g := of(storageID)
	for {
		if err := ctx.Err(); err != nil {
			return noop, err
		}
		g.mu.Lock()
		if !g.judged {
			g.next++
			tok := g.next
			g.fences[tok] = fenceHold{prefixes: ps, since: time.Now()}
			g.refence()
			g.mu.Unlock()
			return g.fenceLeaver(tok), nil
		}
		wait := g.eased
		g.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return noop, ctx.Err()
		}
	}
}

// HoldCtx holds what one two-step change on storageID needs, and is where the
// choice between the gate and a fence is made: with fence empty (a short
// change - one file, a storage with real folders) it is MoveCtx, the gate; with
// fence (a long change - a folder moved or trashed object by object: its path
// and its destination's) it is FenceCtx, those prefixes only. It never takes
// both.
func HoldCtx(ctx context.Context, storageID int64, fence ...string) (release func(), err error) {
	if len(fence) == 0 {
		return MoveCtx(ctx, storageID)
	}
	return FenceCtx(ctx, storageID, fence...)
}

// FencedChangeCtx is ChangeCtx for a change that holds HoldCtx(ctx, storageID,
// fence...): move, then follow when move succeeded, and the hold let go
// however they end - a panic included.
func FencedChangeCtx(ctx context.Context, storageID int64, fence []string, move func() error, follow func()) error {
	release, err := HoldCtx(ctx, storageID, fence...)
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

// fenceLeaver is the release of fence tok.
func (g *gate) fenceLeaver(tok uint64) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			// Counted before the fence opens: a walk that fetched the tree
			// before the change asks the storage again from here on.
			g.moves.Add(1)
			g.mu.Lock()
			delete(g.fences, tok)
			g.refence()
			g.ease()
			g.mu.Unlock()
		})
	}
}

// refence builds the fence set from g.fences. Called with g.mu held.
func (g *gate) refence() {
	if len(g.fences) == 0 {
		g.fenced.Store(nil)
		return
	}
	seen := map[string]bool{}
	f := &FenceSet{kids: map[string][]string{}}
	for _, h := range g.fences {
		for _, p := range h.prefixes {
			if seen[p] {
				continue
			}
			seen[p] = true
			f.list = append(f.list, p)
		}
	}
	sort.Strings(f.list)
	for _, p := range f.list {
		f.under = append(f.under, p+"/")
		parent := path.Dir(p)
		f.kids[parent] = append(f.kids[parent], p)
	}
	g.fenced.Store(f)
}

// cleanPrefixes cleans prefixes and drops the empty ones; whole reports the
// storage's root among them.
func cleanPrefixes(prefixes []string) (out []string, whole bool) {
	for _, p := range prefixes {
		if strings.TrimSpace(p) == "" {
			continue
		}
		c := Clean(p)
		if c == "/" {
			return nil, true
		}
		out = append(out, c)
	}
	return out, false
}
