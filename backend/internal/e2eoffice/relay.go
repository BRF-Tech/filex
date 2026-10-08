package e2eoffice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"
)

// Kind names what an entry is. The first three are written by members and
// carry a sealed payload the relay cannot read; the last three are the
// relay's own and carry plain metadata.
type Kind string

const (
	// KindChanges is one chunk of an editor's changes (its saveChanges).
	KindChanges Kind = "changes"
	// KindLock is an editor's request for locks (its getLock).
	KindLock Kind = "lock"
	// KindRelease releases an editor's locks (its unLockDocument).
	KindRelease Kind = "release"
	// KindJoin records a member joining, with the indexUser it was given.
	KindJoin Kind = "join"
	// KindLeave records a member leaving.
	KindLeave Kind = "leave"
	// KindSaved records a save: the file now holds the log up to Through.
	KindSaved Kind = "saved"
)

// memberKind reports whether a member may append k.
func memberKind(k Kind) bool {
	return k == KindChanges || k == KindLock || k == KindRelease
}

// Entry is one place in a session's log.
type Entry struct {
	Seq    uint64 `json:"seq"`
	Kind   Kind   `json:"kind"`
	Client string `json:"client"`
	User   int64  `json:"user"`
	// At is the relay's clock when the entry landed, in Unix milliseconds.
	// Every bridge uses it where the Document Server would use its own clock
	// (a lock's time), so all of them derive the same state.
	At int64 `json:"at"`
	// Ctr is the writer's counter, bound into the sealed entry; the relay
	// only checks that it grows.
	Ctr uint64 `json:"ctr,omitempty"`
	// CT is the sealed payload of a member's entry.
	CT []byte `json:"ct,omitempty"`
	// Member is who joined (KindJoin).
	Member *Member `json:"member,omitempty"`
	// Through and Sig describe a save (KindSaved): the last entry the saved
	// file holds and the file's new signature.
	Through uint64 `json:"through,omitempty"`
	Sig     string `json:"sig,omitempty"`
}

// Member is one connection in a session.
type Member struct {
	Client string `json:"client"`
	User   int64  `json:"user"`
	Name   string `json:"name"`
	// IndexUser is the editor's per-session user index. The editor builds its
	// user id from it and its object ids from the user id, so two members
	// must never share one, not even one that left: it only grows.
	IndexUser int  `json:"index_user"`
	CanEdit   bool `json:"can_edit"`
}

// Eph is a frame that is not kept: a cursor. It is sealed like an entry and
// goes to the other members only.
type Eph struct {
	Client string `json:"client"`
	CT     []byte `json:"ct"`
}

// Frame is what a subscriber receives: an entry or an ephemeral frame.
type Frame struct {
	Entry *Entry
	Eph   *Eph
}

// Options tune a Relay. The zero value of each field takes its default.
type Options struct {
	// LeaseTTL is how long the changes lease lasts without a renewal (the
	// Document Server's save lock expires after 60 s).
	LeaseTTL time.Duration
	// MaxEntryBytes caps one sealed payload. The editor cuts its changes at
	// 1.5 MB per message (websocketMaxPayloadSize); the rest is sealing.
	MaxEntryBytes int
	// MaxLogBytes caps a session's log. Past it a member has to save and the
	// next opening starts a new session.
	MaxLogBytes int64
	// MaxBlobBytes caps a session's blobs (the base document and its images).
	MaxBlobBytes int64
	// SubscriberBuffer is how many frames a subscriber may fall behind before
	// it is cut off (and has to subscribe again from the last entry it has).
	SubscriberBuffer int
	// SavedIdle is how long an empty session whose log is all saved is kept.
	SavedIdle time.Duration
	// UnsavedRetention is how long an empty session with unsaved changes is
	// kept for somebody to open and save it.
	UnsavedRetention time.Duration
	// NotReadyTTL is how long a session may wait for its key and its base.
	NotReadyTTL time.Duration
	// Now is the clock (tests).
	Now func() time.Time
	// Who answers, for a caller joining the session of doc, who they are and
	// whether they may write - from the server's own records: the account
	// signed in on ctx and filex's rules for the file (an editor role and
	// files.modify), never from anything the caller sent. nil lets nobody
	// join (ErrNoIdentity); an error from it refuses the join.
	Who func(ctx context.Context, doc string) (Identity, error)
}

// Identity is who a joining member is, as the server knows it (Options.Who).
type Identity struct {
	User    int64
	Name    string
	CanEdit bool
}

const (
	defaultLeaseTTL         = 60 * time.Second
	defaultMaxEntryBytes    = 2 << 20
	defaultMaxLogBytes      = 64 << 20
	defaultMaxBlobBytes     = 256 << 20
	defaultSubscriberBuffer = 256
	defaultSavedIdle        = time.Hour
	// DefaultUnsavedRetention is how long unsaved changes wait for somebody
	// to save them: 30 days.
	DefaultUnsavedRetention = 30 * 24 * time.Hour
	defaultNotReadyTTL      = 10 * time.Minute
)

// BaseBlob is the name of the sealed base document every member opens.
const BaseBlob = "base"

var blobNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

var (
	ErrNoSession   = errors.New("e2eoffice: no such session")
	ErrNotMember   = errors.New("e2eoffice: not a member of this session")
	ErrReadOnly    = errors.New("e2eoffice: this member may not write")
	ErrKind        = errors.New("e2eoffice: a member may not append this kind")
	ErrNoLease     = errors.New("e2eoffice: changes need the lease")
	ErrEmpty       = errors.New("e2eoffice: an entry needs a sealed payload")
	ErrTooLarge    = errors.New("e2eoffice: too large")
	ErrLogFull     = errors.New("e2eoffice: the session log is full; save, and the next opening starts a new session")
	ErrCounter     = errors.New("e2eoffice: the counter must grow")
	ErrBuild       = errors.New("e2eoffice: the session runs another editor build")
	ErrFileChanged = errors.New("e2eoffice: the file changed outside the session")
	ErrKeySet      = errors.New("e2eoffice: the session key is already set")
	ErrBlobSet     = errors.New("e2eoffice: that blob is already stored")
	ErrBlobName    = errors.New("e2eoffice: bad blob name")
	ErrNoBlob      = errors.New("e2eoffice: no such blob")
	ErrNotReady    = errors.New("e2eoffice: the session has no key or no base yet")
	ErrSaveBehind  = errors.New("e2eoffice: a save of a later point is already recorded")
	ErrSaveAhead   = errors.New("e2eoffice: a save cannot hold entries the log does not have")
	ErrBadRequest  = errors.New("e2eoffice: bad request")
	ErrNoIdentity  = errors.New("e2eoffice: who is joining is not known")
)

// ConflictError says the entry was written against another head than the
// log's. The writer catches up to Head, seals again and retries.
type ConflictError struct {
	Head uint64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("e2eoffice: the log's head is %d", e.Head)
}

// Relay holds the sessions.
type Relay struct {
	opts  Options
	mu    sync.Mutex
	byID  map[string]*session
	byDoc map[string]*session
}

type session struct {
	mu      sync.Mutex
	gone    bool
	id      string
	doc     string
	build   string
	baseSig string
	key     []byte

	created    time.Time
	lastActive time.Time

	log          []Entry
	logBytes     int64
	changesHead  uint64
	savedThrough uint64
	savedSig     string

	blobs     map[string][]byte
	blobBytes int64

	members    map[string]*memberState
	nextClient int
	nextIndex  int

	leaseHolder string
	leaseUntil  time.Time

	subs map[string]chan Frame
}

type memberState struct {
	m       Member
	lastCtr uint64
}

// New builds an empty relay.
func New(opts Options) *Relay {
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = defaultLeaseTTL
	}
	if opts.MaxEntryBytes <= 0 {
		opts.MaxEntryBytes = defaultMaxEntryBytes
	}
	if opts.MaxLogBytes <= 0 {
		opts.MaxLogBytes = defaultMaxLogBytes
	}
	if opts.MaxBlobBytes <= 0 {
		opts.MaxBlobBytes = defaultMaxBlobBytes
	}
	if opts.SubscriberBuffer <= 0 {
		opts.SubscriberBuffer = defaultSubscriberBuffer
	}
	if opts.SavedIdle <= 0 {
		opts.SavedIdle = defaultSavedIdle
	}
	if opts.UnsavedRetention <= 0 {
		opts.UnsavedRetention = DefaultUnsavedRetention
	}
	if opts.NotReadyTTL <= 0 {
		opts.NotReadyTTL = defaultNotReadyTTL
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Relay{opts: opts, byID: map[string]*session{}, byDoc: map[string]*session{}}
}

// ---------------------------------------------------------------------------
// Opening a session
// ---------------------------------------------------------------------------

// OpenRequest asks for the session of one file.
type OpenRequest struct {
	// Doc is the caller's key for the file (tenant, storage and path). The
	// relay only compares it.
	Doc string
	// Build is the editor build the opener runs. Members of one session run
	// one build: another build may write changes this one cannot read.
	Build string
	// FileSig is the file as the opener read it (size:mtime). A session goes
	// on from the file it started from or from its last save; a file that
	// changed outside it is not the document its log applies to.
	FileSig string
}

// OpenResult is the session to join.
type OpenResult struct {
	SID string
	// Created: this opener started the session and must set its key and
	// store its base before anybody can join.
	Created bool
	// Key is the sealed session key, nil until the creator set it.
	Key []byte
}

// Open finds the file's session or starts one. An empty session whose log is
// all saved does not hold anybody back: a new build or a changed file simply
// replaces it.
func (r *Relay) Open(req OpenRequest) (OpenResult, error) {
	if req.Doc == "" || req.Build == "" || req.FileSig == "" {
		return OpenResult{}, ErrBadRequest
	}
	now := r.opts.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.byDoc[req.Doc]; s != nil {
		s.mu.Lock()
		replace, err := r.reusable(s, req, now)
		if err == nil && !replace {
			// append onto nil keeps a key that is not set yet nil.
			res := OpenResult{SID: s.id, Key: append([]byte(nil), s.key...)}
			s.mu.Unlock()
			return res, nil
		}
		if err != nil {
			s.mu.Unlock()
			return OpenResult{}, err
		}
		s.gone = true
		r.closeSubsLocked(s)
		s.mu.Unlock()
		delete(r.byID, s.id)
		delete(r.byDoc, s.doc)
	}
	id, err := newID()
	if err != nil {
		return OpenResult{}, err
	}
	s := &session{
		id:         id,
		doc:        req.Doc,
		build:      req.Build,
		baseSig:    req.FileSig,
		created:    now,
		lastActive: now,
		blobs:      map[string][]byte{},
		members:    map[string]*memberState{},
		subs:       map[string]chan Frame{},
	}
	r.byID[id] = s
	r.byDoc[req.Doc] = s
	return OpenResult{SID: id, Created: true}, nil
}

// reusable decides, with s locked, whether an opener joins s (false, nil), s
// is replaced by a new session (true, nil), or the opener is refused.
func (r *Relay) reusable(s *session, req OpenRequest, now time.Time) (bool, error) {
	idle := len(s.members) == 0
	if !s.ready() {
		// A creator that never finished: after a while somebody else may start
		// over.
		return idle && now.Sub(s.created) >= r.opts.NotReadyTTL, nil
	}
	clean := s.changesHead <= s.savedThrough
	current := s.baseSig
	if s.savedSig != "" {
		current = s.savedSig
	}
	switch {
	case s.build != req.Build:
		if idle && clean {
			return true, nil
		}
		return false, ErrBuild
	case current != req.FileSig:
		if idle && clean {
			return true, nil
		}
		return false, ErrFileChanged
	}
	return false, nil
}

func (s *session) ready() bool {
	_, base := s.blobs[BaseBlob]
	return len(s.key) > 0 && base
}

// SetKey stores the sealed session key, once.
func (r *Relay) SetKey(sid string, sealed []byte) error {
	if len(sealed) == 0 || len(sealed) > 4096 {
		return ErrBadRequest
	}
	return r.with(sid, func(s *session) error {
		if len(s.key) > 0 {
			return ErrKeySet
		}
		s.key = append([]byte(nil), sealed...)
		return nil
	})
}

// PutBlob stores a sealed blob once: the base document, or an image.
func (r *Relay) PutBlob(sid, name string, sealed []byte) error {
	if !blobNameRE.MatchString(name) || len(sealed) == 0 {
		return ErrBlobName
	}
	return r.with(sid, func(s *session) error {
		if _, ok := s.blobs[name]; ok {
			return ErrBlobSet
		}
		if s.blobBytes+int64(len(sealed)) > r.opts.MaxBlobBytes {
			return ErrTooLarge
		}
		s.blobs[name] = append([]byte(nil), sealed...)
		s.blobBytes += int64(len(sealed))
		s.lastActive = r.opts.Now()
		return nil
	})
}

// Blob returns a stored sealed blob.
func (r *Relay) Blob(sid, name string) ([]byte, error) {
	var out []byte
	err := r.with(sid, func(s *session) error {
		b, ok := s.blobs[name]
		if !ok {
			return ErrNoBlob
		}
		out = append([]byte(nil), b...)
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------------------
// Members
// ---------------------------------------------------------------------------

// A joining connection names the session and nothing else. Who it is and
// whether it may write come from Options.Who - the signed-in account and
// filex's own rules for the file (an editor role and files.modify) - never
// from the request: a member that could say its own user or its own right to
// write could write as somebody else, or write where it may only read. A
// member without the right reads along and writes nothing.

// Hello is what a new member needs to start.
type Hello struct {
	Client       string
	IndexUser    int
	Head         uint64
	ChangesHead  uint64
	SavedThrough uint64
	Key          []byte
}

// Join adds the caller ctx carries as a member and records it in the log.
func (r *Relay) Join(ctx context.Context, sid string) (Hello, error) {
	r.mu.Lock()
	s := r.byID[sid]
	r.mu.Unlock()
	if s == nil {
		return Hello{}, ErrNoSession
	}
	// The session's doc never changes, and Who is asked outside every lock:
	// it reads the database.
	if r.opts.Who == nil {
		return Hello{}, ErrNoIdentity
	}
	who, err := r.opts.Who(ctx, s.doc)
	if err != nil {
		return Hello{}, err
	}
	if who.User <= 0 {
		return Hello{}, ErrNoIdentity
	}
	var h Hello
	err = r.with(sid, func(s *session) error {
		if !s.ready() {
			return ErrNotReady
		}
		s.nextClient++
		s.nextIndex++
		m := Member{
			Client:    fmt.Sprintf("c%d", s.nextClient),
			User:      who.User,
			Name:      who.Name,
			IndexUser: s.nextIndex,
			CanEdit:   who.CanEdit,
		}
		s.members[m.Client] = &memberState{m: m}
		joined := m
		r.addLocked(s, Entry{Kind: KindJoin, Client: m.Client, User: m.User, Member: &joined}, 0)
		h = Hello{
			Client:       m.Client,
			IndexUser:    m.IndexUser,
			Head:         uint64(len(s.log)),
			ChangesHead:  s.changesHead,
			SavedThrough: s.savedThrough,
			Key:          append([]byte(nil), s.key...),
		}
		return nil
	})
	return h, err
}

// Leave removes a member: its lease goes, its subscription closes, and the
// log records it (every bridge then drops its locks).
func (r *Relay) Leave(sid, client string) error {
	return r.with(sid, func(s *session) error {
		ms, ok := s.members[client]
		if !ok {
			return ErrNotMember
		}
		delete(s.members, client)
		if s.leaseHolder == client {
			s.leaseHolder = ""
		}
		if ch, ok := s.subs[client]; ok {
			close(ch)
			delete(s.subs, client)
		}
		r.addLocked(s, Entry{Kind: KindLeave, Client: client, User: ms.m.User}, 0)
		return nil
	})
}

// Subscribe returns the entries after `from` and a channel for what comes
// next, with nothing lost or repeated between the two. A subscriber that
// falls SubscriberBuffer frames behind is cut off (the channel closes; the
// member stays) and subscribes again from the last entry it has: an entry is
// never skipped. Cursor frames are the exception: a full buffer drops them.
func (r *Relay) Subscribe(sid, client string, from uint64) ([]Entry, <-chan Frame, error) {
	var backlog []Entry
	var ch chan Frame
	err := r.with(sid, func(s *session) error {
		if _, ok := s.members[client]; !ok {
			return ErrNotMember
		}
		if from > uint64(len(s.log)) {
			return ErrBadRequest
		}
		backlog = append([]Entry(nil), s.log[from:]...)
		if old, ok := s.subs[client]; ok {
			close(old)
		}
		ch = make(chan Frame, r.opts.SubscriberBuffer)
		s.subs[client] = ch
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return backlog, ch, nil
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

// AppendRequest is a member's sealed entry.
type AppendRequest struct {
	Kind Kind
	// Base is the head the writer has seen; the entry lands at Base+1 or not
	// at all.
	Base uint64
	Ctr  uint64
	CT   []byte
}

// Append puts a member's entry in the log and returns its seq.
func (r *Relay) Append(sid, client string, req AppendRequest) (uint64, error) {
	var seq uint64
	err := r.with(sid, func(s *session) error {
		ms, err := s.writer(client)
		if err != nil {
			return err
		}
		if !memberKind(req.Kind) {
			return ErrKind
		}
		if len(req.CT) == 0 {
			return ErrEmpty
		}
		if len(req.CT) > r.opts.MaxEntryBytes {
			return ErrTooLarge
		}
		if req.Ctr <= ms.lastCtr {
			return ErrCounter
		}
		now := r.opts.Now()
		if req.Kind == KindChanges && !s.holds(client, now) {
			return ErrNoLease
		}
		if req.Base != uint64(len(s.log)) {
			return &ConflictError{Head: uint64(len(s.log))}
		}
		if s.logBytes+int64(len(req.CT)) > r.opts.MaxLogBytes {
			return ErrLogFull
		}
		ms.lastCtr = req.Ctr
		seq = r.addLocked(s, Entry{
			Kind:   req.Kind,
			Client: client,
			User:   ms.m.User,
			Ctr:    req.Ctr,
			CT:     append([]byte(nil), req.CT...),
		}, int64(len(req.CT)))
		if req.Kind == KindChanges {
			s.changesHead = seq
			s.leaseUntil = now.Add(r.opts.LeaseTTL)
		}
		return nil
	})
	return seq, err
}

// LeaseOp is what a member does with the changes lease.
type LeaseOp string

const (
	LeaseAcquire LeaseOp = "acquire"
	LeaseRelease LeaseOp = "release"
)

// Lease acquires (or renews) the changes lease, or releases it. It is given
// only to a writer that has seen every change in the log (changesSeen is the
// seq of the last changes entry it applied) while nobody else holds it: the
// Document Server's isSaveLock, which refuses an editor that is behind and
// one whose turn it is not.
func (r *Relay) Lease(sid, client string, op LeaseOp, changesSeen uint64) (bool, error) {
	granted := false
	err := r.with(sid, func(s *session) error {
		if _, err := s.writer(client); err != nil {
			return err
		}
		now := r.opts.Now()
		switch op {
		case LeaseRelease:
			if s.leaseHolder == client {
				s.leaseHolder = ""
			}
			return nil
		case LeaseAcquire:
			if s.leaseHolder != "" && s.leaseHolder != client && now.Before(s.leaseUntil) {
				return nil
			}
			if changesSeen != s.changesHead {
				return nil
			}
			s.leaseHolder = client
			s.leaseUntil = now.Add(r.opts.LeaseTTL)
			granted = true
			return nil
		}
		return ErrBadRequest
	})
	return granted, err
}

// Saved records that a member saved the file with the log up to `through`
// in it, and the file's new signature. Saves only move forward.
func (r *Relay) Saved(sid, client string, through uint64, sig string) (uint64, error) {
	if sig == "" {
		return 0, ErrBadRequest
	}
	var seq uint64
	err := r.with(sid, func(s *session) error {
		ms, err := s.writer(client)
		if err != nil {
			return err
		}
		if through > uint64(len(s.log)) {
			return ErrSaveAhead
		}
		if through < s.savedThrough {
			return ErrSaveBehind
		}
		seq = r.addLocked(s, Entry{Kind: KindSaved, Client: client, User: ms.m.User, Through: through, Sig: sig}, 0)
		s.savedThrough = through
		s.savedSig = sig
		return nil
	})
	return seq, err
}

// SendEph passes a sealed cursor frame to the other members. It is not kept.
func (r *Relay) SendEph(sid, client string, sealed []byte) error {
	if len(sealed) == 0 || len(sealed) > 64<<10 {
		return ErrTooLarge
	}
	return r.with(sid, func(s *session) error {
		if _, err := s.writer(client); err != nil {
			return err
		}
		f := Frame{Eph: &Eph{Client: client, CT: append([]byte(nil), sealed...)}}
		for c, ch := range s.subs {
			if c == client {
				continue
			}
			select {
			case ch <- f:
			default:
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// State and housekeeping
// ---------------------------------------------------------------------------

// State is what the server may say about a session without reading it.
type State struct {
	SID          string
	Doc          string
	Head         uint64
	ChangesHead  uint64
	SavedThrough uint64
	Members      int
	// Dirty: the log holds changes no save has (the explorer can say "unsaved
	// changes from a co-editing session").
	Dirty      bool
	LastActive time.Time
}

// State reports on one session.
func (r *Relay) State(sid string) (State, error) {
	var st State
	err := r.with(sid, func(s *session) error {
		st = s.state()
		return nil
	})
	return st, err
}

// StateOf reports on a file's session, if it has one.
func (r *Relay) StateOf(doc string) (State, bool) {
	r.mu.Lock()
	s := r.byDoc[doc]
	r.mu.Unlock()
	if s == nil {
		return State{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gone {
		return State{}, false
	}
	return s.state(), true
}

func (s *session) state() State {
	return State{
		SID:          s.id,
		Doc:          s.doc,
		Head:         uint64(len(s.log)),
		ChangesHead:  s.changesHead,
		SavedThrough: s.savedThrough,
		Members:      len(s.members),
		Dirty:        s.changesHead > s.savedThrough,
		LastActive:   s.lastActive,
	}
}

// Sweep forgets the sessions nobody is in any more: a saved one after
// SavedIdle, one with unsaved changes after UnsavedRetention (30 days), one
// that never got its key or base after NotReadyTTL. It returns their ids, for
// the caller to delete what it keeps beside them.
func (r *Relay) Sweep() []string {
	now := r.opts.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for id, s := range r.byID {
		s.mu.Lock()
		drop := false
		if len(s.members) == 0 {
			idle := now.Sub(s.lastActive)
			switch {
			case !s.ready():
				drop = now.Sub(s.created) >= r.opts.NotReadyTTL
			case s.changesHead > s.savedThrough:
				drop = idle >= r.opts.UnsavedRetention
			default:
				drop = idle >= r.opts.SavedIdle
			}
		}
		if drop {
			s.gone = true
			r.closeSubsLocked(s)
		}
		s.mu.Unlock()
		if drop {
			delete(r.byID, id)
			if r.byDoc[s.doc] == s {
				delete(r.byDoc, s.doc)
			}
			out = append(out, id)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------------

// with runs fn with the session locked; a swept session is gone.
func (r *Relay) with(sid string, fn func(*session) error) error {
	r.mu.Lock()
	s := r.byID[sid]
	r.mu.Unlock()
	if s == nil {
		return ErrNoSession
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gone {
		return ErrNoSession
	}
	return fn(s)
}

// writer returns a member that may write.
func (s *session) writer(client string) (*memberState, error) {
	ms, ok := s.members[client]
	if !ok {
		return nil, ErrNotMember
	}
	if !ms.m.CanEdit {
		return nil, ErrReadOnly
	}
	return ms, nil
}

// holds reports whether client holds an unexpired lease.
func (s *session) holds(client string, now time.Time) bool {
	return s.leaseHolder == client && now.Before(s.leaseUntil)
}

// addLocked appends e at the next seq, stamps it and hands it to every
// subscriber. A subscriber whose buffer is full is cut off rather than
// skipped. With s locked.
func (r *Relay) addLocked(s *session, e Entry, bytes int64) uint64 {
	now := r.opts.Now()
	e.Seq = uint64(len(s.log)) + 1
	e.At = now.UnixMilli()
	s.log = append(s.log, e)
	s.logBytes += bytes
	s.lastActive = now
	out := e
	for c, ch := range s.subs {
		select {
		case ch <- Frame{Entry: &out}:
		default:
			close(ch)
			delete(s.subs, c)
		}
	}
	return e.Seq
}

func (r *Relay) closeSubsLocked(s *session) {
	for c, ch := range s.subs {
		close(ch)
		delete(s.subs, c)
	}
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
