package coedit

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Kind names what an entry is. The first three are written by members and
// carry a sealed payload the relay cannot read; the last three are the
// relay's own and carry plain metadata.
type Kind string

const (
	// KindChanges is one chunk of an editor's changes.
	KindChanges Kind = "changes"
	// KindLock is an editor's request for locks.
	KindLock Kind = "lock"
	// KindRelease releases an editor's locks.
	KindRelease Kind = "release"
	// KindJoin records a member joining, with the indexUser it was given.
	KindJoin Kind = "join"
	// KindLeave records a member leaving (or dropped).
	KindLeave Kind = "leave"
	// KindSaved records a save: the file now holds the log up to Through.
	KindSaved Kind = "saved"
)

// MemberKind reports whether a member may append k.
func MemberKind(k Kind) bool {
	return k == KindChanges || k == KindLock || k == KindRelease
}

// Key modes (OpenRequest.KeyMode).
const (
	// KeyServer: the server makes the session key and keeps it sealed with
	// its secret key - an ordinary folder.
	KeyServer = "server"
	// KeyFolder: the members' browsers seal the key with the folder key - an
	// encrypted folder. The server cannot open it.
	KeyFolder = "folder"
)

// BaseBlob is the name of the sealed base document every member opens.
const BaseBlob = "base"

// KeyBytes is the size of a session key.
const KeyBytes = 32

var blobNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// ValidBlobName reports whether name may name a blob.
func ValidBlobName(name string) bool { return blobNameRE.MatchString(name) }

// Member is one connection in a session, as the members are told.
type Member struct {
	Client string `json:"client"`
	// User is an opaque id of the person in THIS session (the same for each
	// of their connections in it), never an account id.
	User string `json:"user"`
	Name string `json:"name"`
	// IndexUser is the editor's per-session user index: given once, never
	// twice in a session, not even after its member left.
	IndexUser int  `json:"index_user"`
	CanEdit   bool `json:"can_edit"`
}

// Entry is one place in a session's log, as the members receive it.
type Entry struct {
	Seq    uint64 `json:"seq"`
	Kind   Kind   `json:"kind"`
	Client string `json:"client"`
	// At is the relay's clock when the entry landed, Unix milliseconds - the
	// same for every member, where a Document Server would use its own.
	At int64 `json:"at"`
	// Ctr is the writer's counter, bound into the sealed entry.
	Ctr uint64 `json:"ctr,omitempty"`
	// CT is a member's sealed payload (base64 on the wire).
	CT []byte `json:"ct,omitempty"`
	// Member is who joined (KindJoin).
	Member *Member `json:"member,omitempty"`
	// Through is the last entry a save holds (KindSaved).
	Through uint64 `json:"through,omitempty"`
}

// Eph is a frame that is not kept: a cursor, sealed, for the other members.
type Eph struct {
	Client string `json:"client"`
	Ctr    uint64 `json:"ctr"`
	CT     []byte `json:"ct"`
	// At is when the relay took it (Unix ms) - for the sockets of other
	// processes, which read the latest cursors from the database
	// (cursors.go).
	At int64 `json:"-"`
}

// KeyBox seals and opens the keys the server makes (internal/secretbox.Box).
type KeyBox interface {
	Enabled() bool
	SealFor(plaintext, aad string) (string, error)
	OpenFor(stored, aad string) (string, error)
}

// Departure is a member that left, for the audit trail.
type Departure struct {
	SID       string
	StorageID int64
	Path      string
	App       string
	Member    model.CoEditMember
	// Reason: "left" (it said so), "timeout" (its page went away), "closed"
	// (the session ended).
	Reason string
}

// Options tune a Relay. The zero value of each duration and size takes its
// default.
type Options struct {
	Store Store
	// BlobDir is where the sealed blobs are kept, one folder per session
	// (<data_dir>/coedit). Several processes on one database must share it,
	// as they share the upload staging directory.
	BlobDir string
	// Keys seals the keys of KeyServer sessions. nil or not enabled: an
	// ordinary folder is not edited together (ErrNoServerKey).
	Keys KeyBox
	// LeaseTTL is how long the changes lease lasts without a renewal (the
	// Document Server's save lock expires after 60 s).
	LeaseTTL time.Duration
	// MemberTTL is how long a member whose page gives no sign of life stays.
	MemberTTL time.Duration
	// MaxEntryBytes caps one sealed payload.
	MaxEntryBytes int
	// MaxLogBytes caps a session's log: past it a member has to save, and
	// the next opening starts a new session.
	MaxLogBytes int64
	// MaxBlobBytes caps a session's blobs together.
	MaxBlobBytes int64
	// MaxMembers caps the members of one session.
	MaxMembers int
	// SavedIdle is how long an empty session whose log is all saved is kept.
	SavedIdle time.Duration
	// UnsavedRetention is how long an empty session with unsaved changes is
	// kept, for the next person to open the file to see and save them.
	UnsavedRetention time.Duration
	// NotReadyTTL is how long a session may wait for its key and its base.
	NotReadyTTL time.Duration
	// PollEvery is how often a member's socket reads the log without a
	// notice from this process (another process wrote the entry).
	PollEvery time.Duration
	// OnDeparture is told about every member that left, after it is recorded.
	OnDeparture func(ctx context.Context, d Departure)
	// Now is the clock (tests).
	Now func() time.Time
}

// Defaults.
const (
	DefaultLeaseTTL         = 60 * time.Second
	DefaultMemberTTL        = 45 * time.Second
	DefaultMaxEntryBytes    = 2 << 20
	DefaultMaxLogBytes      = 64 << 20
	DefaultMaxBlobBytes     = 256 << 20
	DefaultMaxMembers       = 20
	DefaultSavedIdle        = time.Hour
	DefaultUnsavedRetention = 30 * 24 * time.Hour
	DefaultNotReadyTTL      = 10 * time.Minute
	DefaultPollEvery        = 2 * time.Second
	// MaxEphBytes caps one sealed cursor frame.
	MaxEphBytes = 64 << 10
	// MaxSealedKeyBytes caps a sealed key a member hands over (KeyFolder).
	MaxSealedKeyBytes = 4096
)

// Store is what the relay keeps its state in: db.Store's editing-together
// half (db.CoEditStore) and its transactions.
type Store interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
	db.CoEditStore
}

// Errors. The HTTP layer turns each into a code and a sentence of its own.
var (
	ErrNoSession   = errors.New("coedit: no such session")
	ErrNotMember   = errors.New("coedit: not a member of this session")
	ErrReadOnly    = errors.New("coedit: this member may not write")
	ErrKind        = errors.New("coedit: a member may not append this kind")
	ErrNoLease     = errors.New("coedit: changes need the lease")
	ErrEmpty       = errors.New("coedit: an entry needs a sealed payload")
	ErrTooLarge    = errors.New("coedit: too large")
	ErrLogFull     = errors.New("coedit: the session log is full; save, and the next opening starts a new session")
	ErrCounter     = errors.New("coedit: the counter must grow")
	ErrBuild       = errors.New("coedit: the session runs another build of the app")
	ErrFileChanged = errors.New("coedit: the file changed outside the session")
	ErrKeySet      = errors.New("coedit: the session key is already set")
	ErrBlobSet     = errors.New("coedit: that blob is already stored")
	ErrBlobName    = errors.New("coedit: bad blob name")
	ErrNoBlob      = errors.New("coedit: no such blob")
	ErrNotReady    = errors.New("coedit: the session has no key or no base yet")
	ErrSaveBehind  = errors.New("coedit: a save of a later point is already recorded")
	ErrSaveAhead   = errors.New("coedit: a save cannot hold entries the log does not have")
	ErrBadRequest  = errors.New("coedit: bad request")
	ErrNoIdentity  = errors.New("coedit: who is joining is not known")
	ErrFull        = errors.New("coedit: the session has as many members as it may")
	ErrNoServerKey = errors.New("coedit: no secret key to keep a session key with (FILEX_SECRET_KEY)")
	ErrBusy        = errors.New("coedit: the session kept changing under this request")
	// ErrNothingToWatch: a person who may only read the file opened it while
	// no session is there to join. A watcher never starts one - its starter
	// stores the base document every member builds on, which a person who
	// may not change the file must not choose.
	ErrNothingToWatch = errors.New("coedit: nobody is editing this file; a watcher starts no session")
)

// errLost says another process wrote the session first: read it again.
var errLost = errors.New("coedit: lost the compare-and-set")

// ConflictError says the entry was written against another head than the
// log's. The writer catches up to Head, seals again and retries.
type ConflictError struct {
	Head uint64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("coedit: the log's head is %d", e.Head)
}

// Relay is the editing-together relay. One per process; safe for concurrent
// use, and for several processes on one database.
type Relay struct {
	opts  Options
	store Store
	hub   *hub
	// stripes serialise one process's changes to one session, so its own
	// requests do not lose compare-and-sets to each other (another process's
	// still can, and are retried).
	stripes [64]sync.Mutex
	// cursors throttles what each member's cursor costs the database
	// (cursors.go).
	cursors cursorWriter
}

// New builds a relay over opts.Store.
func New(opts Options) *Relay {
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = DefaultLeaseTTL
	}
	if opts.MemberTTL <= 0 {
		opts.MemberTTL = DefaultMemberTTL
	}
	if opts.MaxEntryBytes <= 0 {
		opts.MaxEntryBytes = DefaultMaxEntryBytes
	}
	if opts.MaxLogBytes <= 0 {
		opts.MaxLogBytes = DefaultMaxLogBytes
	}
	if opts.MaxBlobBytes <= 0 {
		opts.MaxBlobBytes = DefaultMaxBlobBytes
	}
	if opts.MaxMembers <= 0 {
		opts.MaxMembers = DefaultMaxMembers
	}
	if opts.SavedIdle <= 0 {
		opts.SavedIdle = DefaultSavedIdle
	}
	if opts.UnsavedRetention <= 0 {
		opts.UnsavedRetention = DefaultUnsavedRetention
	}
	if opts.NotReadyTTL <= 0 {
		opts.NotReadyTTL = DefaultNotReadyTTL
	}
	if opts.PollEvery <= 0 {
		opts.PollEvery = DefaultPollEvery
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Relay{opts: opts, store: opts.Store, hub: newHub()}
}

// Options returns the relay's options with their defaults (the HTTP layer
// reads the limits).
func (r *Relay) Options() Options { return r.opts }

// ServerKeys reports whether an ordinary folder can be edited together here.
func (r *Relay) ServerKeys() bool { return r.opts.Keys != nil && r.opts.Keys.Enabled() }

func (r *Relay) nowMs() int64 { return r.opts.Now().UnixMilli() }

func (r *Relay) stripe(sid string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(sid))
	return &r.stripes[h.Sum32()%uint32(len(r.stripes))]
}

// change runs fn on the session's current row inside one transaction and
// writes the row back by compare-and-set, retrying when another process
// wrote it in between. fn returns whether the session row changed (false:
// nothing to write back, the transaction still commits what fn wrote).
func (r *Relay) change(ctx context.Context, sid string, fn func(ctx context.Context, s *model.CoEditSession) (bool, error)) error {
	mu := r.stripe(sid)
	mu.Lock()
	defer mu.Unlock()
	for attempt := 0; attempt < 8; attempt++ {
		err := r.store.WithTx(ctx, func(ctx context.Context) error {
			s, err := r.store.GetCoEditSession(ctx, sid)
			if err != nil {
				return err
			}
			if s == nil {
				return ErrNoSession
			}
			rev := s.Rev
			write, err := fn(ctx, s)
			if err != nil {
				return err
			}
			if !write {
				return nil
			}
			ok, err := r.store.UpdateCoEditSession(ctx, s, rev)
			if err != nil {
				return err
			}
			if !ok {
				return errLost
			}
			return nil
		})
		if errors.Is(err, errLost) {
			continue
		}
		return err
	}
	return ErrBusy
}

// ---------------------------------------------------------------------------
// Opening a session
// ---------------------------------------------------------------------------

// OpenRequest asks for the session of one file.
type OpenRequest struct {
	// Doc is the file's key (DocKey: a digest of the storage and the path).
	Doc       string
	StorageID int64
	Path      string
	// App and Build: the app and the build its members run. Members of one
	// session run one build: another build may write entries this one cannot
	// read.
	App   string
	Build string
	// FileSig is the file as the opener's server read it (size:mtime, etag).
	// A session goes on from the file it started from or from its last save;
	// a file that changed outside it is not the document its log applies to.
	FileSig string
	// KeyMode is KeyServer (an ordinary folder) or KeyFolder (an encrypted
	// one).
	KeyMode string
}

// Identity is who a joining member is, as the SERVER knows it: the account
// signed in on the request and the file's rules - never anything the caller
// sent.
type Identity struct {
	User int64
	// Person is the person's presence identity (the account, and the end
	// user a shared proxy token names): two of them behind one proxy token
	// are two people. Empty = the account alone.
	Person  string
	Name    string
	CanEdit bool
	// TokenHash is hex SHA-256 of the token the member is handed: the
	// requests that name the member carry it (MemberToken checks it).
	TokenHash string
}

// Hello is what a new member needs to start.
type Hello struct {
	SID string
	// Created: this member started the session and must store the base
	// document (and, KeyFolder, the sealed key) before anybody else joins.
	Created      bool
	Me           Member
	Head         uint64
	ChangesHead  uint64
	SavedThrough uint64
	KeyMode      string
	// Key is the raw session key (KeyServer).
	Key []byte
	// SealedKey is the sealed session key (KeyFolder), "" until set.
	SealedKey string
}

// DocKey is the session key of a file: one session per storage and path.
func DocKey(storageID int64, path string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("coedit-doc:%d\x00%s", storageID, path)))
	return hex.EncodeToString(h[:])
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func keyAAD(sid string) string { return "filex-coedit-sk:" + sid }

// userRef is the opaque id the members see for a person.
func userRef(salt, person string) string {
	m := hmac.New(sha256.New, []byte(salt))
	_, _ = m.Write([]byte(person))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

func ready(s *model.CoEditSession) bool {
	return s.HasBase && s.SealedKey != ""
}

// Open joins the caller to the file's session, starting it when there is
// none (or the one there makes way: nobody is in it and everything in it is
// saved). A session nobody may join yet - its creator is still storing the
// base document - answers ErrNotReady; ask again in a moment.
func (r *Relay) Open(ctx context.Context, req OpenRequest, who Identity) (Hello, error) {
	if req.Doc == "" || req.App == "" || req.Build == "" || req.FileSig == "" {
		return Hello{}, ErrBadRequest
	}
	if req.KeyMode != KeyServer && req.KeyMode != KeyFolder {
		return Hello{}, ErrBadRequest
	}
	if who.User <= 0 {
		return Hello{}, ErrNoIdentity
	}
	if req.KeyMode == KeyServer && !r.ServerKeys() {
		return Hello{}, ErrNoServerKey
	}
	person := who.Person
	if person == "" {
		person = fmt.Sprintf("%d", who.User)
	}
	for attempt := 0; attempt < 8; attempt++ {
		var (
			hello    Hello
			replaced *model.CoEditSession
			lost     bool
		)
		err := r.store.WithTx(ctx, func(ctx context.Context) error {
			now := r.nowMs()
			s, err := r.store.GetCoEditSessionByDoc(ctx, req.Doc)
			if err != nil {
				return err
			}
			if s != nil {
				replace, err := r.reusable(s, req, now)
				if err != nil {
					return err
				}
				if replace && !who.CanEdit {
					return ErrNothingToWatch
				}
				if replace {
					// Claimed by compare-and-set before it goes, so a joiner
					// on another process cannot be left in a deleted session.
					ok, err := r.store.UpdateCoEditSession(ctx, s, s.Rev)
					if err != nil {
						return err
					}
					if !ok {
						lost = true
						return errLost
					}
					if err := r.store.DeleteCoEditSession(ctx, s.SID); err != nil {
						return err
					}
					replaced = s
					s = nil
				}
			}
			created := false
			if s == nil && !who.CanEdit {
				return ErrNothingToWatch
			}
			if s == nil {
				s, err = r.newSession(req, now)
				if err != nil {
					return err
				}
				ok, err := r.store.InsertCoEditSession(ctx, s)
				if err != nil {
					return err
				}
				if !ok {
					lost = true
					return errLost
				}
				created = true
			} else if !ready(s) {
				return ErrNotReady
			}
			if s.Members >= r.opts.MaxMembers {
				return ErrFull
			}
			rev := s.Rev
			m, err := r.addMember(ctx, s, who, person, now)
			if err != nil {
				return err
			}
			ok, err := r.store.UpdateCoEditSession(ctx, s, rev)
			if err != nil {
				return err
			}
			if !ok {
				lost = true
				return errLost
			}
			hello, err = r.hello(s, m, created)
			return err
		})
		if lost || errors.Is(err, errLost) {
			continue
		}
		if err != nil {
			return Hello{}, err
		}
		if replaced != nil {
			r.dropBlobs(replaced.SID)
			r.hub.end(replaced.SID, "", "closed")
			r.cursors.forget(replaced.SID, "")
		}
		r.hub.publish(hello.SID)
		return hello, nil
	}
	return Hello{}, ErrBusy
}

// reusable decides whether an opener joins s (false, nil), s makes way for a
// new session (true, nil), or the opener is refused.
func (r *Relay) reusable(s *model.CoEditSession, req OpenRequest, now int64) (bool, error) {
	idle := s.Members == 0
	if !ready(s) {
		// Its creator never stored the base and nobody is in it: nobody ever
		// will. With its creator still in it, the opener waits.
		return idle, nil
	}
	clean := s.ChangesHead <= s.SavedThrough
	// The file as the session holds it: its base, or its last save over it.
	// A session whose saves go elsewhere (SavedTo: beside it in another
	// format, a conflict copy) never wrote the file: it is still the base.
	current := s.BaseSig
	if s.SavedSig != "" && s.SavedTo == "" {
		current = s.SavedSig
	}
	switch {
	case s.App != req.App || s.Build != req.Build:
		if idle && clean {
			return true, nil
		}
		return false, ErrBuild
	case s.SavedTo != "" && idle && clean:
		// Its edits are all in the file beside: with nobody in it, the file
		// opens as it is, in a new session - as a Document Server's next
		// opening after a save beside the document.
		return true, nil
	case s.KeyMode != req.KeyMode || current != req.FileSig:
		if idle && clean {
			return true, nil
		}
		return false, ErrFileChanged
	}
	return false, nil
}

func (r *Relay) newSession(req OpenRequest, now int64) (*model.CoEditSession, error) {
	sid, err := randHex(16)
	if err != nil {
		return nil, err
	}
	salt, err := randHex(16)
	if err != nil {
		return nil, err
	}
	s := &model.CoEditSession{
		SID: sid, DocKey: req.Doc, StorageID: req.StorageID, Path: req.Path, App: req.App, Build: req.Build,
		BaseSig: req.FileSig, KeyMode: req.KeyMode, UserSalt: salt, CreatedMs: now, ActiveMs: now,
	}
	if req.KeyMode == KeyServer {
		raw := make([]byte, KeyBytes)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		sealed, err := r.opts.Keys.SealFor(base64.StdEncoding.EncodeToString(raw), keyAAD(sid))
		for i := range raw {
			raw[i] = 0
		}
		if err != nil {
			return nil, err
		}
		s.SealedKey = sealed
	}
	return s, nil
}

// addMember records who joining s, with s read in this transaction.
func (r *Relay) addMember(ctx context.Context, s *model.CoEditSession, who Identity, person string, now int64) (*model.CoEditMember, error) {
	s.NextClient++
	s.NextIndex++
	seq := s.Head + 1
	m := &model.CoEditMember{
		SID: s.SID, Client: fmt.Sprintf("c%d", s.NextClient), TokenHash: who.TokenHash, UserID: who.User, UserRef: userRef(s.UserSalt, person),
		Name: who.Name, IndexUser: s.NextIndex, CanEdit: who.CanEdit, JoinSeq: seq, SeenMs: now,
	}
	if err := r.store.InsertCoEditMember(ctx, m); err != nil {
		return nil, err
	}
	body, err := json.Marshal(wireMember(m))
	if err != nil {
		return nil, err
	}
	if err := r.store.InsertCoEditEntry(ctx, &model.CoEditEntry{
		SID: s.SID, Seq: seq, Kind: string(KindJoin), Client: m.Client, AtMs: now, Member: string(body),
	}); err != nil {
		return nil, err
	}
	s.Head = seq
	s.Members++
	s.ActiveMs = now
	return m, nil
}

func wireMember(m *model.CoEditMember) Member {
	return Member{Client: m.Client, User: m.UserRef, Name: m.Name, IndexUser: int(m.IndexUser), CanEdit: m.CanEdit}
}

func (r *Relay) hello(s *model.CoEditSession, m *model.CoEditMember, created bool) (Hello, error) {
	h := Hello{
		SID: s.SID, Created: created, Me: wireMember(m),
		Head: uint64(s.Head), ChangesHead: uint64(s.ChangesHead), SavedThrough: uint64(s.SavedThrough),
		KeyMode: s.KeyMode,
	}
	switch s.KeyMode {
	case KeyServer:
		key, err := r.serverKey(s)
		if err != nil {
			return Hello{}, err
		}
		h.Key = key
	case KeyFolder:
		h.SealedKey = s.SealedKey
	}
	return h, nil
}

func (r *Relay) serverKey(s *model.CoEditSession) ([]byte, error) {
	if !r.ServerKeys() {
		return nil, ErrNoServerKey
	}
	b64, err := r.opts.Keys.OpenFor(s.SealedKey, keyAAD(s.SID))
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != KeyBytes {
		return nil, fmt.Errorf("coedit: the session key does not open")
	}
	return raw, nil
}

// SetKey stores the sealed session key of a KeyFolder session, once - by a
// member, before anybody else may join.
func (r *Relay) SetKey(ctx context.Context, sid, client, sealed string) error {
	if sealed == "" || len(sealed) > MaxSealedKeyBytes {
		return ErrBadRequest
	}
	return r.change(ctx, sid, func(ctx context.Context, s *model.CoEditSession) (bool, error) {
		if _, err := r.member(ctx, sid, client); err != nil {
			return false, err
		}
		if s.KeyMode != KeyFolder || s.SealedKey != "" {
			return false, ErrKeySet
		}
		s.SealedKey = sealed
		s.ActiveMs = r.nowMs()
		return true, nil
	})
}

// ---------------------------------------------------------------------------
// Members
// ---------------------------------------------------------------------------

func (r *Relay) member(ctx context.Context, sid, client string) (*model.CoEditMember, error) {
	m, err := r.store.GetCoEditMember(ctx, sid, client)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrNotMember
	}
	return m, nil
}

func (r *Relay) writer(ctx context.Context, sid, client string) (*model.CoEditMember, error) {
	m, err := r.member(ctx, sid, client)
	if err != nil {
		return nil, err
	}
	if !m.CanEdit {
		return nil, ErrReadOnly
	}
	return m, nil
}

// Member returns a member of a session (ErrNotMember when it is not one).
// The HTTP layer checks with it that a request naming a member comes from
// that member's account.
func (r *Relay) Member(ctx context.Context, sid, client string) (*model.CoEditMember, error) {
	return r.member(ctx, sid, client)
}

// TokenHash is what a member's token is kept as.
func TokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// NewToken makes a member's token (handed to its page once, at the join).
func NewToken() (string, error) { return randHex(32) }

// Authorise returns the member a request names, when the request is that
// member's: the same account, and the member's token. Anything else is
// ErrNotMember - a request that names somebody else's connection learns
// nothing about it.
func (r *Relay) Authorise(ctx context.Context, sid, client, token string, userID int64) (*model.CoEditMember, error) {
	if !ValidSID(sid) || client == "" || token == "" || userID <= 0 {
		return nil, ErrNotMember
	}
	m, err := r.member(ctx, sid, client)
	if err != nil {
		return nil, err
	}
	if m.UserID != userID || m.TokenHash == "" ||
		!hmac.Equal([]byte(TokenHash(token)), []byte(m.TokenHash)) {
		return nil, ErrNotMember
	}
	return m, nil
}

// Session returns a session's row (ErrNoSession when there is none).
func (r *Relay) Session(ctx context.Context, sid string) (*model.CoEditSession, error) {
	s, err := r.store.GetCoEditSession(ctx, sid)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrNoSession
	}
	return s, nil
}

// Leave removes a member: its lease goes, its socket is told, and the log
// records it (every bridge then drops its locks). reason is "left" or
// "timeout".
func (r *Relay) Leave(ctx context.Context, sid, client, reason string) error {
	var (
		gone *model.CoEditMember
		sess model.CoEditSession
	)
	err := r.change(ctx, sid, func(ctx context.Context, s *model.CoEditSession) (bool, error) {
		m, err := r.member(ctx, sid, client)
		if err != nil {
			return false, err
		}
		if _, err := r.store.DeleteCoEditMember(ctx, sid, client); err != nil {
			return false, err
		}
		now := r.nowMs()
		if s.LeaseHolder == client {
			s.LeaseHolder, s.LeaseUntilMs = "", 0
		}
		seq := s.Head + 1
		if err := r.store.InsertCoEditEntry(ctx, &model.CoEditEntry{SID: sid, Seq: seq, Kind: string(KindLeave), Client: client, AtMs: now}); err != nil {
			return false, err
		}
		s.Head = seq
		if s.Members > 0 {
			s.Members--
		}
		s.ActiveMs = now
		gone = m
		sess = *s
		return true, nil
	})
	if err != nil {
		return err
	}
	r.hub.end(sid, client, reason)
	r.hub.publish(sid)
	r.cursors.forget(sid, client)
	if r.opts.OnDeparture != nil && gone != nil {
		r.opts.OnDeparture(ctx, Departure{SID: sid, StorageID: sess.StorageID, Path: sess.Path, App: sess.App, Member: *gone, Reason: reason})
	}
	return nil
}

// Touch records that a member's page is still there; ErrNotMember once it
// is no longer a member (it was dropped, or the session ended).
func (r *Relay) Touch(ctx context.Context, sid, client string) error {
	ok, err := r.store.TouchCoEditMember(ctx, sid, client, r.nowMs())
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotMember
	}
	return nil
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
func (r *Relay) Append(ctx context.Context, sid, client string, req AppendRequest) (uint64, error) {
	if !MemberKind(req.Kind) {
		return 0, ErrKind
	}
	if len(req.CT) == 0 {
		return 0, ErrEmpty
	}
	if len(req.CT) > r.opts.MaxEntryBytes {
		return 0, ErrTooLarge
	}
	var seq uint64
	err := r.change(ctx, sid, func(ctx context.Context, s *model.CoEditSession) (bool, error) {
		m, err := r.writer(ctx, sid, client)
		if err != nil {
			return false, err
		}
		if int64(req.Ctr) <= m.LastCtr {
			return false, ErrCounter
		}
		now := r.nowMs()
		if req.Kind == KindChanges && !(s.LeaseHolder == client && now < s.LeaseUntilMs) {
			return false, ErrNoLease
		}
		if req.Base != uint64(s.Head) {
			return false, &ConflictError{Head: uint64(s.Head)}
		}
		if s.LogBytes+int64(len(req.CT)) > r.opts.MaxLogBytes {
			return false, ErrLogFull
		}
		next := s.Head + 1
		if err := r.store.InsertCoEditEntry(ctx, &model.CoEditEntry{
			SID: sid, Seq: next, Kind: string(req.Kind), Client: client, AtMs: now, Ctr: int64(req.Ctr), CT: req.CT,
		}); err != nil {
			return false, err
		}
		if err := r.store.SetCoEditMemberCounter(ctx, sid, client, int64(req.Ctr)); err != nil {
			return false, err
		}
		s.Head = next
		s.LogBytes += int64(len(req.CT))
		if req.Kind == KindChanges {
			s.ChangesHead = next
			s.LeaseUntilMs = now + r.opts.LeaseTTL.Milliseconds()
		}
		s.ActiveMs = now
		seq = uint64(next)
		return true, nil
	})
	if err != nil {
		return 0, err
	}
	r.hub.publish(sid)
	return seq, nil
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
func (r *Relay) Lease(ctx context.Context, sid, client string, op LeaseOp, changesSeen uint64) (bool, error) {
	if op != LeaseAcquire && op != LeaseRelease {
		return false, ErrBadRequest
	}
	granted := false
	err := r.change(ctx, sid, func(ctx context.Context, s *model.CoEditSession) (bool, error) {
		if _, err := r.writer(ctx, sid, client); err != nil {
			return false, err
		}
		now := r.nowMs()
		if op == LeaseRelease {
			if s.LeaseHolder != client {
				return false, nil
			}
			s.LeaseHolder, s.LeaseUntilMs = "", 0
			return true, nil
		}
		if s.LeaseHolder != "" && s.LeaseHolder != client && now < s.LeaseUntilMs {
			return false, nil
		}
		if changesSeen != uint64(s.ChangesHead) {
			return false, nil
		}
		s.LeaseHolder = client
		s.LeaseUntilMs = now + r.opts.LeaseTTL.Milliseconds()
		granted = true
		return true, nil
	})
	return granted, err
}

// Saved records that a member saved the file with the log up to `through` in
// it, and the file's signature after the write - the server's own reading of
// the file, never the member's word. Saves only move forward.
func (r *Relay) Saved(ctx context.Context, sid, client string, through uint64, sig string) (uint64, error) {
	return r.SavedTo(ctx, sid, client, through, sig, "")
}

// SavedTo is Saved for a save written to another file than the session's
// own: `to`, beside it in another format or a conflict copy (filex 0.57).
// From then on the session's saves go there (CoEditSession.SavedTo), and
// sig is that file's. "" is the session's own file; a session that saved
// elsewhere once never goes back to it.
func (r *Relay) SavedTo(ctx context.Context, sid, client string, through uint64, sig, to string) (uint64, error) {
	if sig == "" {
		return 0, ErrBadRequest
	}
	var seq uint64
	err := r.change(ctx, sid, func(ctx context.Context, s *model.CoEditSession) (bool, error) {
		if _, err := r.writer(ctx, sid, client); err != nil {
			return false, err
		}
		if through > uint64(s.Head) {
			return false, ErrSaveAhead
		}
		if int64(through) < s.SavedThrough {
			return false, ErrSaveBehind
		}
		now := r.nowMs()
		next := s.Head + 1
		if err := r.store.InsertCoEditEntry(ctx, &model.CoEditEntry{
			SID: sid, Seq: next, Kind: string(KindSaved), Client: client, AtMs: now, Through: int64(through), Sig: sig,
		}); err != nil {
			return false, err
		}
		s.Head = next
		s.SavedThrough = int64(through)
		s.SavedSig = sig
		if to != "" {
			s.SavedTo = to
		}
		s.ActiveMs = now
		seq = uint64(next)
		return true, nil
	})
	if err != nil {
		return 0, err
	}
	r.hub.publish(sid)
	return seq, nil
}

// SendEph passes a sealed cursor frame to the other members connected to
// this process. It is not kept. The caller checked the member may write.
func (r *Relay) SendEph(sid string, e Eph) error {
	if len(e.CT) == 0 || len(e.CT) > MaxEphBytes {
		return ErrTooLarge
	}
	e.At = r.nowMs()
	r.hub.eph(sid, e)
	r.cursors.put(r, sid, e)
	return nil
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// Entries returns up to limit entries after `after`, in order. The caller
// checked the reader is a member.
func (r *Relay) Entries(ctx context.Context, sid string, after uint64, limit int) ([]Entry, error) {
	rows, err := r.store.ListCoEditEntries(ctx, sid, int64(after), limit)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, row := range rows {
		e := Entry{Seq: uint64(row.Seq), Kind: Kind(row.Kind), Client: row.Client, At: row.AtMs, Ctr: uint64(row.Ctr), CT: row.CT, Through: uint64(row.Through)}
		if row.Member != "" {
			var m Member
			if err := json.Unmarshal([]byte(row.Member), &m); err == nil {
				e.Member = &m
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// Watch registers a member's socket in this process: it is told when an
// entry lands here (Notify), gets the cursors of the others (Eph) and is told
// when its membership ends (Ended). Close it when the socket goes.
func (r *Relay) Watch(sid, client string) *Watcher { return r.hub.watch(sid, client) }

// PollEvery is how often a socket reads the log without a notice.
func (r *Relay) PollEvery() time.Duration { return r.opts.PollEvery }

// MemberTTL is how long a member's page may be silent.
func (r *Relay) MemberTTL() time.Duration { return r.opts.MemberTTL }
