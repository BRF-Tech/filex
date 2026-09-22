package realtime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
)

// ChangeLog remembers which folders of each storage changed recently, so a
// sync client can ask "has anything under this folder changed since I last
// looked?" with ONE request instead of listing the whole tree again.
//
// The desktop app re-walked every paired tree every 30 seconds, one listing
// per folder: a single Mac with 7,048 synced folders sent 100–150 thousand
// listings an hour, around the clock, and two clients together kept a small
// server at ~60 requests a second doing nothing but answering "still the
// same".
//
// It is fed by the same emitter chain that already refreshes open explorers
// and folder sizes after every change, from every surface (explorer, WebDAV,
// S3, SFTP, FTPS, NFS, trash and version restores, the ops queue), so it sees
// what they see. It lives in memory: a restart starts a new epoch, every old
// cursor then reads as "changed", and each client walks once. Changes the
// catalogue learns from a storage scan (bytes written straight into the
// backend) do not pass through here — clients keep a slower full walk as the
// safety net for those.
type ChangeLog struct {
	epoch    string
	capacity int

	mu   sync.Mutex
	logs map[int64]*storageLog
}

type storageLog struct {
	seq     uint64        // last sequence number handed out
	entries []changeEntry // ring, oldest at head
	head    int
}

type changeEntry struct {
	seq           uint64
	dir           string // the folder whose contents changed ("" = the root)
	name, newName string // the child it was about, when the event said
}

// DefaultChangeLogCapacity is how many changes per storage the log keeps. A
// cursor older than that reads as "changed", which only costs one walk.
const DefaultChangeLogCapacity = 4096

// NewChangeLog returns an empty log with a fresh epoch.
func NewChangeLog(capacity int) *ChangeLog {
	if capacity <= 0 {
		capacity = DefaultChangeLogCapacity
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return &ChangeLog{epoch: hex.EncodeToString(b[:]), capacity: capacity, logs: map[int64]*storageLog{}}
}

func cleanChangeDir(dir string) string {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if dir == "" || dir == "." {
		return ""
	}
	return path.Clean(dir)
}

// EmitChange records one change. It is the ChangeEmitter method, so the log
// can also be used as the last link of a chain.
func (l *ChangeLog) EmitChange(storageID int64, dir string, ev ChangeEvent) {
	if l == nil || storageID <= 0 {
		return
	}
	e := changeEntry{dir: cleanChangeDir(dir), name: ev.Name, newName: ev.NewName}
	l.mu.Lock()
	defer l.mu.Unlock()
	sl := l.logs[storageID]
	if sl == nil {
		sl = &storageLog{}
		l.logs[storageID] = sl
	}
	sl.seq++
	e.seq = sl.seq
	if len(sl.entries) < l.capacity {
		sl.entries = append(sl.entries, e)
		return
	}
	sl.entries[sl.head] = e
	sl.head = (sl.head + 1) % len(sl.entries)
}

// Emitter is what the log wraps: the hub, or the next link of the chain.
type Emitter interface {
	EmitChange(storageID int64, dir string, ev ChangeEvent)
}

// Wrap puts the log in front of inner: every event is recorded, then passed on.
func (l *ChangeLog) Wrap(inner Emitter) Emitter {
	return changeLogEmitter{log: l, inner: inner}
}

type changeLogEmitter struct {
	log   *ChangeLog
	inner Emitter
}

func (e changeLogEmitter) EmitChange(storageID int64, dir string, ev ChangeEvent) {
	e.log.EmitChange(storageID, dir, ev)
	if e.inner != nil {
		e.inner.EmitChange(storageID, dir, ev)
	}
}

func (l *ChangeLog) cursor(sl *storageLog) string {
	var seq uint64
	if sl != nil {
		seq = sl.seq
	}
	return l.epoch + "." + strconv.FormatUint(seq, 10)
}

// ChangedSince answers whether anything under rel (a folder of storageID) may
// have changed after cursor, and returns the cursor to ask with next time.
//
// ⚠ Every doubt is "changed": no cursor, a cursor from another boot or
// garbled, a cursor older than the ring still holds, an event on the way to
// rel that does not say which child it touched. A false "changed" costs one
// walk; a false "unchanged" leaves a file unsynced until the next full walk.
func (l *ChangeLog) ChangedSince(storageID int64, rel, cursor string) (bool, string) {
	return l.ChangedSinceFor(storageID, rel, cursor, nil)
}

// ChangedSinceFor is ChangedSince for a caller who may not see everything:
// a change counts only when visible reports that the caller can see the item
// it touched (the folder itself when the event names no item). Otherwise a
// change inside a subfolder somebody has no grant on would still tell them
// "something happened there". A nil visible sees everything.
func (l *ChangeLog) ChangedSinceFor(storageID int64, rel, cursor string, visible func(path string) bool) (bool, string) {
	rel = cleanChangeDir(rel)
	l.mu.Lock()
	defer l.mu.Unlock()
	sl := l.logs[storageID]
	cur := l.cursor(sl)

	epoch, seqStr, ok := strings.Cut(cursor, ".")
	if !ok || epoch != l.epoch {
		return true, cur
	}
	seq, err := strconv.ParseUint(seqStr, 10, 64)
	if err != nil {
		return true, cur
	}
	if sl == nil {
		return seq != 0, cur
	}
	if seq > sl.seq {
		return true, cur
	}
	if seq == sl.seq {
		return false, cur
	}
	n := len(sl.entries)
	oldest := sl.entries[sl.head].seq
	if seq+1 < oldest {
		return true, cur // the ring no longer holds everything after this cursor
	}
	for i := 0; i < n; i++ {
		e := sl.entries[(sl.head+i)%n]
		if e.seq > seq && touches(e, rel) && (visible == nil || e.visibleTo(visible)) {
			return true, cur
		}
	}
	return false, cur
}

// visibleTo reports whether the caller can see what this change touched.
func (e changeEntry) visibleTo(visible func(string) bool) bool {
	child := func(name string) string {
		if e.dir == "" {
			return name
		}
		return e.dir + "/" + name
	}
	if e.name == "" && e.newName == "" {
		return visible(e.dir)
	}
	return (e.name != "" && visible(child(e.name))) || (e.newName != "" && visible(child(e.newName)))
}

// touches reports whether a change in e.dir can affect the tree at rel.
func touches(e changeEntry, rel string) bool {
	switch {
	case rel == "" || e.dir == rel:
		return true
	case strings.HasPrefix(e.dir, rel+"/"):
		return true // inside the tree
	case e.dir == "" || strings.HasPrefix(rel, e.dir+"/"):
		// On the way to rel: it matters when it touched the child the path
		// goes through (a rename, move or delete of an ancestor), or did not
		// say what it touched.
		below := strings.TrimPrefix(rel, e.dir)
		below = strings.TrimPrefix(below, "/")
		next, _, _ := strings.Cut(below, "/")
		return e.name == "" || e.name == next || e.newName == next
	}
	return false
}

// String is for logs.
func (l *ChangeLog) String() string {
	return fmt.Sprintf("changelog(epoch=%s)", l.epoch)
}
