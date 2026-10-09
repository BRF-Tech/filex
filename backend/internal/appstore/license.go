package appstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// License results a store signs (`POST <store>/v1/licenses/verify`).
const (
	ResultValid          = "valid"
	ResultInvalid        = "invalid"
	ResultRevoked        = "revoked"
	ResultExpired        = "expired"
	ResultSeatsExhausted = "seats_exhausted"
	ResultWrongApp       = "wrong_app"
)

// License statuses filex reports (the store's results, plus what filex
// decides by itself).
const (
	StatusFree         = "free"          // not a paid app
	StatusValid        = "valid"         // the store said valid at its last check
	StatusGrace        = "grace"         // valid, but the store could not be asked since; holds until grace_until
	StatusGraceExpired = "grace_expired" // the store could not be asked, and grace_until has passed
	StatusMissing      = "missing"       // a paid app with no key
	StatusUnverified   = "unverified"    // a key, never confirmed by the store
)

// Answer is a license check's signed payload.
type Answer struct {
	Result       string     `json:"result"`
	App          string     `json:"app"`
	Licensee     string     `json:"licensee,omitempty"`
	Seats        *int       `json:"seats,omitempty"`
	SeatsUsed    *int       `json:"seats_used,omitempty"`
	ValidUntil   *time.Time `json:"valid_until,omitempty"`
	UpdatesUntil *time.Time `json:"updates_until,omitempty"`
	InstanceID   string     `json:"instance_id"`
	CheckedAt    time.Time  `json:"checked_at"`
	NextCheckBy  time.Time  `json:"next_check_by"`
	GraceUntil   time.Time  `json:"grace_until"`
}

// License is a paid app's license as filex keeps it (app_store_state
// `license:<app>`): the key sealed, its prefix for the screens, and the
// store's last signed answers - the evidence a status is judged from.
type License struct {
	// App is the row's id: the app's name, or "storage:<name>" for a storage
	// plugin (LicenseID).
	App string `json:"app"`
	// StoreApp is what the store calls the entry when it is not App (a
	// storage plugin's name, without the prefix): what a check names.
	StoreApp  string `json:"store_app,omitempty"`
	Store     string `json:"store"`
	KeySealed string `json:"key_sealed,omitempty"`
	KeyPrefix string `json:"key_prefix,omitempty"`
	// Last is the store's last signed answer (any result); LastValid the
	// last one that said valid. Kept whole: the signature travels with them.
	Last      *Envelope `json:"last,omitempty"`
	LastValid *Envelope `json:"last_valid,omitempty"`
	// LastAttemptAt / LastError: the last check, when it did not end in a
	// signed answer (the store unreachable, a bad answer). Cleared by one
	// that did.
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt time.Time `json:"last_success_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	LastErrorCode string    `json:"last_error_code,omitempty"`
	// Status is the last status decided, for telling a change (audit).
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// storeName is the entry's name at its store: what a check asks about and
// what the store's answer must name.
func (l *License) storeName() string {
	if l.StoreApp != "" {
		return l.StoreApp
	}
	return l.App
}

// kind is the kind of entry the license is for (KindStorage, or "" for an
// app).
func (l *License) kind() string {
	if strings.HasPrefix(l.App, StoragePrefix) {
		return KindStorage
	}
	return ""
}

func (l *License) answer(env *Envelope) *Answer {
	if env == nil {
		return nil
	}
	var a Answer
	if json.Unmarshal(env.Payload, &a) != nil {
		return nil
	}
	a.bound()
	return &a
}

// Bounds on what a store's answer may promise, counted from its checked_at:
// at most maxGrace of grace and maxNextCheck until the next check. A store
// that signs more is CLIPPED, not refused - its answer still says what it
// says (valid), filex only applies no more than this - so a store cannot make
// a license outlive filex's asking by signing a far deadline.
const (
	maxGrace     = 30 * 24 * time.Hour
	maxNextCheck = 2 * 24 * time.Hour
)

// answerSkew is how far a store's checked_at may be from this server's wall
// clock and still be taken (license answers, the proven time): a day either
// way. Further off, the answer is refused - the store's clock or this
// server's is wrong - and moves nothing.
const answerSkew = 24 * time.Hour

// bound clips grace_until and next_check_by to maxGrace and maxNextCheck
// after checked_at.
func (a *Answer) bound() {
	if a.CheckedAt.IsZero() {
		return
	}
	if lim := a.CheckedAt.Add(maxGrace); a.GraceUntil.After(lim) {
		a.GraceUntil = lim
	}
	if lim := a.CheckedAt.Add(maxNextCheck); a.NextCheckBy.After(lim) {
		a.NextCheckBy = lim
	}
}

// View is a license as the administrator's panel shows it. The key itself
// is never in it - its prefix only.
type View struct {
	// App is the license's id (LicenseID): an app's name, or
	// "storage:<name>"; Name is the entry's own name and Kind "storage" for a
	// storage plugin ("" for an app), so a screen never reads the id.
	App           string     `json:"app"`
	Name          string     `json:"name,omitempty"`
	Kind          string     `json:"kind,omitempty"`
	Required      bool       `json:"required"`
	Status        string     `json:"status"`
	Held          bool       `json:"held"`
	Store         string     `json:"store,omitempty"`
	KeyPrefix     string     `json:"key_prefix,omitempty"`
	Licensee      string     `json:"licensee,omitempty"`
	Seats         *int       `json:"seats,omitempty"`
	SeatsUsed     *int       `json:"seats_used,omitempty"`
	ValidUntil    *time.Time `json:"valid_until,omitempty"`
	UpdatesUntil  *time.Time `json:"updates_until,omitempty"`
	CheckedAt     *time.Time `json:"checked_at,omitempty"`
	NextCheckBy   *time.Time `json:"next_check_by,omitempty"`
	GraceUntil    *time.Time `json:"grace_until,omitempty"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastErrorCode string     `json:"last_error_code,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	// StoreTrusted: false when the store that issued the license is no
	// longer trusted here (it cannot be asked any more).
	StoreTrusted bool `json:"store_trusted"`
}

// AppView is a license as the app itself reads it (fx.license.get()): the
// status and its dates - nothing about the key, the store or who holds it.
// ⚠ Every user who may run apps reads it, a tenant's included, and any token
// with that right: the licensee (a person's or a company's name) is the
// administrator's to see (View), not theirs.
type AppView struct {
	Status       string     `json:"status"`
	ValidUntil   *time.Time `json:"valid_until,omitempty"`
	UpdatesUntil *time.Time `json:"updates_until,omitempty"`
}

var keyRe = regexp.MustCompile(`^[\x21-\x7e]{4,512}$`)

// ValidKey reports whether s may be a license key: 4-512 printable ASCII
// characters, no spaces.
func ValidKey(s string) bool { return keyRe.MatchString(s) }

// Prefix is what of a key the screens, the API and the audit log show: the
// first six characters at most (a third of a short key), then an ellipsis.
func Prefix(key string) string {
	n := 6
	if len(key)/3 < n {
		n = len(key) / 3
	}
	if n < 1 {
		return "…"
	}
	return key[:n] + "…"
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SHA256Hex is the lower-hex sha256 of b (what a link's pins are written in).
func SHA256Hex(b []byte) string { return sha256Hex(b) }

// judge decides a license's status at now (the clock's latest time) and
// whether the app is held: it runs while the status is free, valid or grace.
func judge(l *License, now time.Time) (status string, held bool) {
	if l == nil {
		return StatusFree, false
	}
	if l.KeySealed == "" {
		return StatusMissing, true
	}
	last := l.answer(l.Last)
	if last == nil {
		return StatusUnverified, true
	}
	if last.Result != ResultValid {
		return last.Result, true
	}
	if last.ValidUntil != nil && !now.Before(*last.ValidUntil) {
		return ResultExpired, true
	}
	if !now.Before(last.GraceUntil) {
		return StatusGraceExpired, true
	}
	if l.LastError != "" || !now.Before(last.NextCheckBy) {
		return StatusGrace, false
	}
	return StatusValid, false
}

// ── Reading and writing licenses ───────────────────────────────────────

func (s *Service) license(ctx context.Context, app string) (*License, error) {
	var l License
	ok, err := s.getJSON(ctx, keyLicensePrefix+app, &l)
	if err != nil || !ok {
		return nil, err
	}
	return &l, nil
}

func (s *Service) saveLicense(ctx context.Context, l *License) error {
	l.UpdatedAt = time.Now().UTC()
	if l.CreatedAt.IsZero() {
		l.CreatedAt = l.UpdatedAt
	}
	return s.putJSON(ctx, keyLicensePrefix+l.App, l)
}

// Require makes app a paid app licensed by store, with key ("" = none yet).
// Called at a paid install BEFORE the app is installed: the hold is in place
// before the app could run, and lifted by a valid answer.
func (s *Service) Require(ctx context.Context, app, store, key string, actorID *int64) error {
	_, err := s.RequireUndoable(ctx, app, store, key, actorID)
	return err
}

// Undo is what Require changed, for an install that then failed (Service.Undo).
type Undo struct {
	app string
	// created is the CreatedAt of the row Require created; zero when the
	// row was there before.
	created time.Time
	// prev is the row as it was before Require changed it (nil: there was
	// none) - a paid upgrade's new key or store, put back if it fails.
	prev *License
}

// RequireUndoable is Require, answering what to Undo when the install it
// prepared fails.
func (s *Service) RequireUndoable(ctx context.Context, app, store, key string, actorID *int64) (*Undo, error) {
	return s.RequireUndoableAs(ctx, app, app, store, key, actorID)
}

// RequireUndoableAs is RequireUndoable for the license row id (LicenseID)
// of an entry the store calls storeApp: a storage plugin's row is
// "storage:<name>", its checks name <name>.
func (s *Service) RequireUndoableAs(ctx context.Context, app, storeApp, store, key string, actorID *int64) (*Undo, error) {
	s.licMu.Lock()
	defer s.licMu.Unlock()
	l, err := s.license(ctx, app)
	if err != nil {
		return nil, err
	}
	u := &Undo{app: app}
	created := l == nil
	if created {
		l = &License{App: app}
		if storeApp != app {
			l.StoreApp = storeApp
		}
	} else {
		prev := *l
		u.prev = &prev
	}
	if l.Store != "" && l.Store != store {
		// ⚠ Another store's license: nothing of the first store's carries
		// over - not its key (that store's secret, which would otherwise be
		// sent to this one at the next check), not its answers (that store's
		// word, not this one's).
		l.KeySealed, l.KeyPrefix = "", ""
		l.Last, l.LastValid, l.LastError, l.LastErrorCode = nil, nil, "", ""
		l.LastSuccessAt, l.LastAttemptAt = time.Time{}, time.Time{}
	}
	l.Store = store
	if key != "" {
		if err := s.setKeyLocked(l, key); err != nil {
			return nil, err
		}
	}
	if err := s.saveLicense(ctx, l); err != nil {
		return nil, err
	}
	if created {
		u.created = l.CreatedAt
	}
	s.applyLocked(ctx, l, actorID)
	return u, nil
}

// Undo takes back what RequireUndoable did for an install that failed: the
// row it CREATED is removed (and its hold lifted) - that row only, never one
// another install created: of two installs of one app, the loser must not
// forget the winner's license - and a row it CHANGED (a paid upgrade's new
// key) is put back as it was, key, answers and hold.
func (s *Service) Undo(ctx context.Context, u *Undo) {
	if u == nil {
		return
	}
	s.licMu.Lock()
	defer s.licMu.Unlock()
	l, err := s.license(ctx, u.app)
	if err != nil || l == nil {
		return
	}
	switch {
	case !u.created.IsZero() && l.CreatedAt.Equal(u.created):
		if ok, err := s.opts.Store.DeleteAppStoreState(ctx, keyLicensePrefix+u.app); err == nil && ok && s.opts.Holder != nil {
			s.opts.Holder.SetLicenseHold(u.app, "")
		}
	case u.prev != nil && l.CreatedAt.Equal(u.prev.CreatedAt):
		prev := *u.prev
		if err := s.saveLicense(ctx, &prev); err != nil {
			s.log.Warn("app-store: the license was not put back after a failed install", slog.String("app", u.app), slog.Any("err", err))
			return
		}
		s.applyLocked(ctx, &prev, nil)
	}
}

// ErrNoSecretKey: a license key is kept sealed, which needs FILEX_SECRET_KEY.
var ErrNoSecretKey = errors.New("a license key is kept encrypted, and this installation has no FILEX_SECRET_KEY")

// keyAAD binds a sealed license key to its app's row: a sealed value copied
// into another row (another app's license, by somebody who can write the
// database) does not open there, so it is never sent to that app's store.
func keyAAD(app string) string { return "appstore:license:" + app }

// openKey opens l's sealed key; a value that is not sealed, or sealed for
// another app, does not open.
func (s *Service) openKey(l *License) (string, error) {
	if !secretbox.IsSealed(l.KeySealed) {
		return "", secretbox.ErrCorrupt
	}
	key, err := s.opts.Box.OpenFor(l.KeySealed, keyAAD(l.App))
	if err != nil || secretbox.IsSealed(key) {
		return "", secretbox.ErrCorrupt
	}
	return key, nil
}

func (s *Service) setKeyLocked(l *License, key string) error {
	key = strings.TrimSpace(key)
	if !ValidKey(key) {
		return errf(CodeLicenseKey, "a license key is 4-512 printable characters, without spaces")
	}
	if s.opts.Box == nil || !s.opts.Box.Enabled() {
		return &Error{Code: CodeLicenseKey, Message: ErrNoSecretKey.Error()}
	}
	sealed, err := s.opts.Box.SealFor(key, keyAAD(l.App))
	if err != nil {
		return err
	}
	// A new key starts from nothing: what the store said about the old one
	// is not about this one.
	if l.KeySealed != "" {
		if old, err := s.openKey(l); err != nil || old != key {
			l.Last, l.LastValid, l.LastError, l.LastErrorCode = nil, nil, "", ""
			l.LastSuccessAt, l.LastAttemptAt = time.Time{}, time.Time{}
		}
	}
	l.KeySealed, l.KeyPrefix = sealed, Prefix(key)
	return nil
}

// SetKey replaces a paid app's key and asks the store about it at once.
func (s *Service) SetKey(ctx context.Context, app, key string, actorID *int64) (*View, error) {
	s.licMu.Lock()
	l, err := s.license(ctx, app)
	if err != nil {
		s.licMu.Unlock()
		return nil, err
	}
	if l == nil {
		s.licMu.Unlock()
		return nil, errf(CodeLicenseKey, "%s is not a paid app here", app)
	}
	if err := s.setKeyLocked(l, key); err != nil {
		s.licMu.Unlock()
		return nil, err
	}
	if err := s.saveLicense(ctx, l); err != nil {
		s.licMu.Unlock()
		return nil, err
	}
	s.audit(ctx, actorID, "app_plugin.license_set", "app_plugin", app, map[string]any{"app": app, "key_prefix": l.KeyPrefix, "store": l.Store})
	s.licMu.Unlock()
	return s.Check(ctx, app, actorID)
}

// Forget removes what filex keeps about an app installed from a store (the
// app was removed): its license - and the hold goes with it - and where it
// came from.
func (s *Service) Forget(ctx context.Context, app string) {
	s.licMu.Lock()
	defer s.licMu.Unlock()
	_, _ = s.opts.Store.DeleteAppStoreState(ctx, keySourcePrefix+app)
	if ok, err := s.opts.Store.DeleteAppStoreState(ctx, keyLicensePrefix+app); err == nil && ok && s.opts.Holder != nil {
		s.opts.Holder.SetLicenseHold(app, "")
	}
}

// Check asks the store about app's license now and answers the result.
// actorID is the administrator who pressed "Verify now" (nil: the daily
// check).
func (s *Service) Check(ctx context.Context, app string, actorID *int64) (*View, error) {
	s.licMu.Lock()
	defer s.licMu.Unlock()
	l, err := s.license(ctx, app)
	if err != nil {
		return nil, err
	}
	if l == nil {
		return &View{App: app, Status: StatusFree}, nil
	}
	s.checkLocked(ctx, l)
	if err := s.saveLicense(ctx, l); err != nil {
		return nil, err
	}
	s.applyLocked(ctx, l, actorID)
	return s.viewOf(ctx, l), nil
}

// checkLocked asks the store and folds the answer into l. A failure that is
// not a signed answer (unreachable, unreadable, a key this store was not
// trusted with) is recorded as such and leaves the last answers standing:
// the grace decides.
func (s *Service) checkLocked(ctx context.Context, l *License) {
	fail := func(code, msg string) {
		l.LastAttemptAt = s.opts.Now().UTC()
		l.LastError, l.LastErrorCode = msg, code
	}
	if l.KeySealed == "" {
		return
	}
	key, err := s.openKey(l)
	if err != nil {
		fail(CodeLicenseKey, "the stored license key cannot be opened (was FILEX_SECRET_KEY changed, or the row edited?)")
		return
	}
	id, err := s.InstanceID(ctx)
	if err != nil {
		fail(CodeBadAnswer, err.Error())
		return
	}
	// ⚠⚠ The trust BEFORE the key leaves: a store that is not trusted - never,
	// or no longer - is not sent the key at all (asking afterwards would have
	// refused its answer, with the key already gone).
	if _, err := s.trustFor(ctx, l.Store, true); err != nil {
		e, _ := AsError(err)
		code := CodeTrustRequired
		if e != nil {
			code = e.Code
		}
		fail(code, redact(err.Error(), key))
		return
	}
	env, err := s.opts.Client.VerifyLicense(ctx, l.Store, LicenseRequest{Key: key, App: l.storeName(), InstanceID: id, FilexVersion: s.opts.FilexVersion})
	if err != nil {
		e, _ := AsError(err)
		code := CodeUnreachable
		if e != nil {
			code = e.Code
		}
		fail(code, redact(err.Error(), key))
		return
	}
	if _, err := s.verifyWith(ctx, l.Store, env, UseLicense, false); err != nil {
		e, _ := AsError(err)
		code := CodeSignature
		if e != nil {
			code = e.Code
		}
		fail(code, redact(err.Error(), key))
		return
	}
	var a Answer
	dec := json.NewDecoder(bytes.NewReader(env.Payload))
	if err := dec.Decode(&a); err != nil {
		fail(CodeBadAnswer, "the store's license answer does not read")
		return
	}
	if msg := s.acceptable(l, &a, id); msg != "" {
		fail(CodeBadAnswer, msg)
		return
	}
	l.Last = env
	l.LastError, l.LastErrorCode = "", ""
	l.LastAttemptAt = s.opts.Now().UTC()
	l.LastSuccessAt = l.LastAttemptAt
	if a.Result == ResultValid {
		l.LastValid = env
	}
	// The store's signed time is the clock's proof for ITS licenses from now
	// on (clock.go).
	s.clk.Prove(l.Store, a.CheckedAt)
	s.saveClock(ctx)
}

// acceptable answers why a signed license answer is not taken ("" = it is):
// it must be about this installation and this app, a result filex knows, and
// no older than the last answer taken - a replayed old "valid" moves nothing.
func (s *Service) acceptable(l *License, a *Answer, instanceID string) string {
	switch a.Result {
	case ResultValid, ResultInvalid, ResultRevoked, ResultExpired, ResultSeatsExhausted, ResultWrongApp:
	default:
		return "the store answered an unknown license result " + a.Result
	}
	if a.InstanceID != instanceID {
		return "the store's license answer is about another installation"
	}
	if a.App != l.storeName() && a.Result != ResultWrongApp {
		return "the store's license answer is about another app (" + a.App + ")"
	}
	if a.CheckedAt.IsZero() {
		return "the store's license answer has no checked_at"
	}
	// ⚠ Within a day of this server's clock, or not at all: a checked_at far
	// ahead would push the store's proven time ahead (holding its apps) and,
	// once taken, make every later true answer "older than the last one" -
	// a revocation would never land.
	if d := a.CheckedAt.Sub(s.opts.Now().UTC()); d > answerSkew || d < -answerSkew {
		return "the store's license answer is dated " + a.CheckedAt.UTC().Format(time.RFC3339) +
			", more than a day from this server's clock (" + s.opts.Now().UTC().Format(time.RFC3339) + "); one of the two clocks is wrong"
	}
	if prev := l.answer(l.Last); prev != nil && a.CheckedAt.Before(prev.CheckedAt) {
		return "the store's license answer is older than the last one taken (checked_at " + a.CheckedAt.UTC().Format(time.RFC3339) + ")"
	}
	if a.Result == ResultValid && (a.GraceUntil.Before(a.CheckedAt) || a.NextCheckBy.IsZero()) {
		return "the store's valid answer has no next_check_by, or a grace_until before its checked_at"
	}
	return ""
}

// redact keeps a key out of a message that may quote a request.
func redact(msg, key string) string {
	if key == "" {
		return msg
	}
	return strings.ReplaceAll(msg, key, Prefix(key))
}

// applyLocked decides l's status, holds or releases the app, and writes an
// audit row when the app's fate changed.
func (s *Service) applyLocked(ctx context.Context, l *License, actorID *int64) {
	status, held := judge(l, s.clk.NowFor(l.Store))
	prevHeld := l.Status != "" && heldStatus(l.Status)
	firstTime := l.Status == ""
	if s.opts.Holder != nil {
		reason := ""
		if held {
			reason = "license: " + status
		}
		s.opts.Holder.SetLicenseHold(l.App, reason)
	}
	if status != l.Status {
		l.Status = status
		_ = s.saveLicense(ctx, l)
	}
	switch {
	case held && (!prevHeld || firstTime):
		s.audit(ctx, actorID, "app_plugin.license_held", "app_plugin", l.App, map[string]any{"app": l.App, "status": status, "store": l.Store, "key_prefix": l.KeyPrefix})
	case !held && prevHeld:
		s.audit(ctx, actorID, "app_plugin.license_released", "app_plugin", l.App, map[string]any{"app": l.App, "status": status, "store": l.Store, "key_prefix": l.KeyPrefix})
	}
}

// HoldReasonStoreOff is why a storage plugin under a store's license is
// held on a server whose app store is off: nobody can check the license, so
// the plugin does not run (server.go holds it before any plugin starts).
const HoldReasonStoreOff = "license: not checked - the app store is off on this server"

// StorageLicensed answers the storage plugins a store's license names - the
// `license:storage:<name>` rows, whatever their status - read straight from
// the rows, so it answers with the app store off too.
func StorageLicensed(ctx context.Context, st StateStore) ([]string, error) {
	if st == nil {
		return nil, nil
	}
	rows, err := st.ListAppStoreState(ctx, keyLicensePrefix+StoragePrefix)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for key := range rows {
		if name := strings.TrimPrefix(key, keyLicensePrefix+StoragePrefix); name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func heldStatus(status string) bool {
	switch status {
	case StatusFree, StatusValid, StatusGrace:
		return false
	}
	return true
}

// clockState is what the loop keeps of the clock (app_store_state `clock`):
// every store's proven time, the part of it that is restart allowance, and
// whether this keeping was a clean shutdown's (clock.go).
type clockState struct {
	Stores map[string]time.Time     `json:"stores"`
	Debt   map[string]time.Duration `json:"debt,omitempty"`
	// Signed is the latest checked_at taken from each store: only a later
	// one takes debt back (clock.go Prove).
	Signed map[string]time.Time `json:"signed,omitempty"`
	Clean  bool                 `json:"clean,omitempty"`
}

func (s *Service) saveClock(ctx context.Context) { s.keepClock(ctx, false) }

// keepClock writes the clock's state; clean only from a clean shutdown.
func (s *Service) keepClock(ctx context.Context, clean bool) {
	p, debt, signed := s.clk.Proven()
	if len(p) == 0 {
		return
	}
	if err := s.putJSON(ctx, keyClock, clockState{Stores: p, Debt: debt, Signed: signed, Clean: clean}); err != nil {
		s.log.Warn("app-store: the proven time was not kept", slog.Any("err", err))
	}
}

// ApplyHolds re-judges every license (time moves; a grace ends) and keeps
// the clock's reading.
func (s *Service) ApplyHolds(ctx context.Context) {
	s.licMu.Lock()
	defer s.licMu.Unlock()
	for _, l := range s.licenses(ctx) {
		s.applyLocked(ctx, l, nil)
	}
	s.saveClock(ctx)
}

func (s *Service) licenses(ctx context.Context) []*License {
	rows, err := s.opts.Store.ListAppStoreState(ctx, keyLicensePrefix)
	if err != nil {
		s.log.Warn("app-store: licenses unreadable", slog.Any("err", err))
		return nil
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []*License{}
	for _, k := range keys {
		var l License
		if unmarshal(rows[k], &l) == nil && l.App != "" {
			out = append(out, &l)
		}
	}
	return out
}

// due reports whether l is to be asked now: never asked, its last attempt
// failed an hour or more ago, or the store's next_check_by (at most a day
// after the last answer) has come.
func (s *Service) due(l *License, now time.Time) bool {
	if l.KeySealed == "" {
		return false
	}
	if l.LastError != "" {
		return now.Sub(l.LastAttemptAt) >= time.Hour || now.Before(l.LastAttemptAt)
	}
	last := l.answer(l.Last)
	if last == nil {
		return true
	}
	next := l.LastSuccessAt.Add(24 * time.Hour)
	if !last.NextCheckBy.IsZero() && last.NextCheckBy.Before(next) {
		next = last.NextCheckBy
	}
	return !now.Before(next)
}

// Tick is one round of the license loop: ask the store about every license
// that is due, then re-judge them all.
func (s *Service) Tick(ctx context.Context) {
	for _, l := range s.licenses(ctx) {
		if s.due(l, s.clk.NowFor(l.Store)) {
			if _, err := s.Check(ctx, l.App, nil); err != nil {
				s.log.Warn("app-store: license check failed", slog.String("app", l.App), slog.Any("err", err))
			}
		}
	}
	s.ApplyHolds(ctx)
}

// Run is the license loop: a round a minute after start, then hourly. On
// the way out (ctx done: a clean shutdown) it keeps the proven time once
// more, marked clean, so the next start owes no restart allowance.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.licMu.Lock()
			s.keepClock(context.WithoutCancel(ctx), true)
			s.licMu.Unlock()
			return
		case <-t.C:
			s.Tick(ctx)
			t.Reset(time.Hour)
		}
	}
}

// LicenseOf is app's license for the panel; nil when app is not a paid app.
func (s *Service) LicenseOf(ctx context.Context, app string) (*View, error) {
	l, err := s.license(ctx, app)
	if err != nil || l == nil {
		return nil, err
	}
	return s.viewOf(ctx, l), nil
}

// Licenses is every paid app's license, for the panel's warning band.
func (s *Service) Licenses(ctx context.Context) []*View {
	out := []*View{}
	for _, l := range s.licenses(ctx) {
		out = append(out, s.viewOf(ctx, l))
	}
	return out
}

// AppLicense is what the app reads about itself (fx.license.get()).
func (s *Service) AppLicense(ctx context.Context, app string) *AppView {
	l, err := s.license(ctx, app)
	if err != nil || l == nil {
		return &AppView{Status: StatusFree}
	}
	status, _ := judge(l, s.clk.NowFor(l.Store))
	v := &AppView{Status: status}
	if a := l.answer(l.Last); a != nil && a.Result == ResultValid {
		v.ValidUntil, v.UpdatesUntil = a.ValidUntil, a.UpdatesUntil
	}
	return v
}

func (s *Service) viewOf(ctx context.Context, l *License) *View {
	status, held := judge(l, s.clk.NowFor(l.Store))
	v := &View{App: l.App, Name: l.storeName(), Kind: l.kind(), Required: true, Status: status, Held: held, Store: l.Store, KeyPrefix: l.KeyPrefix,
		LastErrorCode: l.LastErrorCode, LastError: l.LastError, StoreTrusted: s.TrustStatus(ctx, l.Store) != ""}
	if !l.LastAttemptAt.IsZero() {
		t := l.LastAttemptAt
		v.LastAttemptAt = &t
	}
	if a := l.answer(l.Last); a != nil {
		v.Licensee, v.Seats, v.SeatsUsed = a.Licensee, a.Seats, a.SeatsUsed
		v.ValidUntil, v.UpdatesUntil = a.ValidUntil, a.UpdatesUntil
		ca, nc, gu := a.CheckedAt, a.NextCheckBy, a.GraceUntil
		v.CheckedAt = &ca
		if !nc.IsZero() {
			v.NextCheckBy = &nc
		}
		if !gu.IsZero() {
			v.GraceUntil = &gu
		}
	}
	return v
}
