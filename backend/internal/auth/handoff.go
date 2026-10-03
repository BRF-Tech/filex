package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ── Handoff tickets: a session opened on another address ────────────────────
//
// A session cookie belongs to the host it was set on. Whenever filex decides
// on ONE address that a session should be opened on ANOTHER — today: a realm
// typed on the platform's sign-in page for a tenant that has an address of its
// own (docs/MULTI-TENANCY.md, Realms) — it cannot set that cookie itself. It
// issues a handoff ticket instead, the browser carries it to the other address
// (in a URL fragment, which no server sees), and that address redeems it.
//
// The ticket is a general primitive on purpose (the owner's decision): the
// same shape will carry an administrator's "sign in as this account"
// (impersonation) and a platform operator's "switch to this tenant" — not
// written yet, and each will add its OWN checks at redeem. What every purpose
// shares is here:
//
//   - a PURPOSE, checked at redeem: a ticket issued for one use is never
//     accepted for another;
//   - an ACTOR (who caused it) and a SUBJECT (whom the session is for), kept
//     apart — equal for a sign-in handoff, different for impersonation;
//   - one target HOST, and the tenant that host must name;
//   - a short life and a SINGLE use: the first redeem spends it, whatever it
//     answers — a ticket shown on the wrong host is gone too;
//   - an AUDIT trail: issued, used, refused (never the code);
//   - a re-check at the target (CheckHandoffTarget): the accounts still exist,
//     may still sign in, and the subject is still the host's tenant's.
//
// ⚠ Held in memory, like every short-lived ticket here (WebSocket, download
// links): a restart forgets the tickets in flight, and behind several
// replicas the redeem needs the same sticky routing. The CODE is never held —
// only its SHA-256 — so a memory dump or a debug listing cannot replay one.

// HandoffPurpose names what a ticket may be redeemed for.
type HandoffPurpose string

const (
	// HandoffLogin finishes, on a tenant's own address, a sign-in made on the
	// platform's address. Actor and subject are the same account.
	HandoffLogin HandoffPurpose = "login_handoff"
)

// Audit actions of a handoff. One spelling each, so the audit page's `auth.`
// filter finds all three.
const (
	AuditHandoffIssued  = "auth.handoff_issued"
	AuditHandoffUsed    = "auth.handoff_used"
	AuditHandoffRefused = "auth.handoff_refused"
)

// Why a ticket is refused. Stable strings: they are stored in audit rows.
const (
	HandoffRefuseUnknown     = "unknown" // no such ticket, or already spent
	HandoffRefuseExpired     = "expired" // outlived its life
	HandoffRefusePurpose     = "wrong_purpose"
	HandoffRefuseHost        = "wrong_host"   // shown on another address
	HandoffRefuseActor       = "actor_gone"   // the actor may no longer act
	HandoffRefuseSubject     = "subject_gone" // the subject may no longer sign in
	HandoffRefuseTenant      = "wrong_tenant" // the subject is not the host's tenant's
	HandoffRefuseActorPolicy = "actor_is_not_subject"
)

// ErrHandoffRefused is matched (errors.Is) by every refusal; the reason is in
// the *HandoffRefusal.
var ErrHandoffRefused = errors.New("auth: handoff refused")

// HandoffRefusal is the error a refused ticket answers with.
type HandoffRefusal struct{ Reason string }

func (e *HandoffRefusal) Error() string { return "auth: handoff refused (" + e.Reason + ")" }

// Is makes errors.Is(err, ErrHandoffRefused) true.
func (e *HandoffRefusal) Is(target error) bool { return target == ErrHandoffRefused }

// HandoffTicket is one grant to open a session on one host.
type HandoffTicket struct {
	Purpose HandoffPurpose
	// ActorID is the account that caused the ticket; SubjectID the account
	// the session is for. Equal for HandoffLogin.
	ActorID   int64
	SubjectID int64
	// Host is the only request host the ticket is redeemed on (lower case, no
	// port); ProviderID the tenant that host must name.
	Host       string
	ProviderID int64
	IssuedAt   time.Time
	ExpiresAt  time.Time
}

// HandoffStore holds the tickets in flight.
type HandoffStore struct {
	// Now is the clock; nil = time.Now. Tests move it.
	Now func() time.Time

	mu sync.Mutex
	m  map[[32]byte]*HandoffTicket
}

// NewHandoffStore builds an empty store.
func NewHandoffStore() *HandoffStore {
	return &HandoffStore{m: map[[32]byte]*HandoffTicket{}}
}

func (s *HandoffStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func handoffKey(code string) [32]byte { return sha256.Sum256([]byte(code)) }

// Issue stores t for ttl and returns its code: 32 random bytes, URL-safe. The
// code is returned once and never kept.
func (s *HandoffStore) Issue(t HandoffTicket, ttl time.Duration) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(raw[:])
	now := s.now()
	t.Host = strings.ToLower(strings.TrimSpace(t.Host))
	t.IssuedAt, t.ExpiresAt = now, now.Add(ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[[32]byte]*HandoffTicket{}
	}
	for k, v := range s.m {
		if now.After(v.ExpiresAt) {
			delete(s.m, k)
		}
	}
	s.m[handoffKey(code)] = &t
	return code, nil
}

// Redeem spends the ticket behind code and returns it when it may be used for
// purpose on host. A ticket that exists is spent by this call whatever it
// answers; the ticket is returned with a refusal too (nil only for an unknown
// code), so the caller can audit whose ticket was refused.
func (s *HandoffStore) Redeem(code string, purpose HandoffPurpose, host string) (*HandoffTicket, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, &HandoffRefusal{Reason: HandoffRefuseUnknown}
	}
	k := handoffKey(code)
	s.mu.Lock()
	t := s.m[k]
	delete(s.m, k)
	s.mu.Unlock()
	switch {
	case t == nil:
		return nil, &HandoffRefusal{Reason: HandoffRefuseUnknown}
	case s.now().After(t.ExpiresAt):
		return t, &HandoffRefusal{Reason: HandoffRefuseExpired}
	case t.Purpose != purpose:
		return t, &HandoffRefusal{Reason: HandoffRefusePurpose}
	case t.Host == "" || !strings.EqualFold(t.Host, strings.TrimSpace(host)):
		return t, &HandoffRefusal{Reason: HandoffRefuseHost}
	}
	return t, nil
}

// CheckHandoffTarget is the re-check every purpose makes at the target, after
// Redeem: the actor still exists and is enabled; the subject too, and may
// still sign in (auth.LoginAllowed — a suspended tenant, maintenance mode);
// and the subject is the tenant's the host names, which is the tenant the
// ticket was issued for. It answers the subject, or a refusal. Purpose-specific
// rules come on top: for HandoffLogin the actor must be the subject.
func CheckHandoffTarget(ctx context.Context, store db.Store, multiTenant bool, t *HandoffTicket, host string) (*model.User, error) {
	if t == nil {
		return nil, &HandoffRefusal{Reason: HandoffRefuseUnknown}
	}
	if t.Purpose == HandoffLogin && t.ActorID != t.SubjectID {
		return nil, &HandoffRefusal{Reason: HandoffRefuseActorPolicy}
	}
	actor, err := store.GetUser(ctx, t.ActorID)
	if err != nil || actor == nil || !actor.Enabled {
		return nil, &HandoffRefusal{Reason: HandoffRefuseActor}
	}
	subject := actor
	if t.SubjectID != t.ActorID {
		if subject, err = store.GetUser(ctx, t.SubjectID); err != nil || subject == nil {
			return nil, &HandoffRefusal{Reason: HandoffRefuseSubject}
		}
	}
	if !subject.Enabled || !LoginAllowed(ctx, store, multiTenant, subject) {
		return nil, &HandoffRefusal{Reason: HandoffRefuseSubject}
	}
	p, err := store.GetProviderByHost(ctx, strings.ToLower(strings.TrimSpace(host)))
	if err != nil || p == nil || p.ID != t.ProviderID || subject.ProviderID == nil || *subject.ProviderID != p.ID {
		return nil, &HandoffRefusal{Reason: HandoffRefuseTenant}
	}
	return subject, nil
}

// handoffAuditWriter is the slice of db.Store the audit rows need.
type handoffAuditWriter interface {
	InsertAuditEntry(ctx context.Context, e *model.AuditEntry) error
}

// AuditHandoff writes one row of a ticket's life: issued, used or refused
// (reason). Never the code. The row is filed under the SUBJECT (target) and,
// as its user, the ACTOR — the account that caused it — so an impersonation
// will read as "who did it to whom". A refusal with no ticket (an unknown or
// already spent code) is not audited: anybody can send one, and an anonymous
// endpoint must not be a way to fill the audit log; it is logged at debug.
func AuditHandoff(ctx context.Context, store any, action string, t *HandoffTicket, host, reason string) {
	if t == nil {
		slog.Debug("auth: handoff refused", slog.String("reason", reason), slog.String("host", host))
		return
	}
	w, ok := store.(handoffAuditWriter)
	if !ok {
		return
	}
	meta := map[string]any{
		"purpose": string(t.Purpose), "actor_id": t.ActorID, "subject_id": t.SubjectID,
		"host": t.Host, "provider_id": t.ProviderID,
	}
	if host != "" && !strings.EqualFold(host, t.Host) {
		meta["presented_on"] = host
	}
	if reason != "" {
		meta["reason"] = reason
	}
	actor := t.ActorID
	e := &model.AuditEntry{
		UserID: &actor, Action: action, TargetType: "user",
		TargetID: strconv.FormatInt(t.SubjectID, 10), Metadata: meta,
	}
	err := w.InsertAuditEntry(ctx, e)
	if err != nil {
		// An actor that no longer exists cannot be the row's user (the column
		// references users); the row is written without one — the metadata
		// still names it — rather than lost, because a refusal for exactly that
		// reason is the row an operator most needs.
		e.UserID = nil
		err = w.InsertAuditEntry(ctx, e)
	}
	if err != nil {
		slog.Warn("auth: could not write a handoff audit row", slog.String("action", action), slog.String("err", err.Error()))
	}
}
