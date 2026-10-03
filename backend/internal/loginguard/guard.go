// Package loginguard limits how many wrong sign-in attempts an account and an
// address may make, for every door a password can be typed at: the web form,
// WebDAV, FTPS, SFTP.
//
// # What is counted, and where
//
// Two counters, held in MEMORY and written through to the database
// (login_throttle, migration 00072) on every change:
//
//   - per ACCOUNT IDENTIFIER: the normalized text the caller typed
//     (identity.Normalize), whether or not an account by that name exists. The
//     answer to "how many tries are left" is therefore the same for a real
//     name and a made-up one — the limit is not an account-enumeration oracle;
//   - per ADDRESS: the client address (internal/clientip).
//
// Every decision is read from memory; the table is what a restart or a deploy
// comes back to (Load fills the memory from it at start), so neither hands an
// attacker a fresh set of guesses. A write the table refuses (full disk, lost
// connection) does NOT change the decision — the limit goes on from memory,
// neither failing open nor locking everyone out — and the counter is written
// again at the next write that succeeds, or at the next tick.
//
// The settings (the switch, the numbers, the allow-list, the trusted proxies)
// are held the same way: read from the table at start, written to the table
// AND put in force in memory on every save (SaveSetting), so a change applies
// to the next attempt — and read from the table again every minute (Tick), so
// a change saved on another instance is in force here within a minute. A
// settings read the table refuses keeps the last value known (the default
// when there was none).
//
// The housekeeping runs on its own clock, not on the traffic: Run (started
// with the server) ticks every minute — the settings are read again, locks
// that ran out are released and audited, idle counters leave memory, owed
// writes are retried and the table is pruned.
//
// ⚠ The COUNTERS are per PROCESS. Instances sharing one database each count in
// their own memory (the table is read once, at Load): with N instances a
// guesser spread over all of them gets up to N times the limit per account, a
// lock placed by one is not seen by the others until they restart, and the
// table holds whichever instance wrote a counter last. The settings are
// shared (above). Run the limit on one instance, or in front of all of them,
// if that matters.
//
// The guard sits ABOVE the driver chain. Local, LDAP, recovery and any
// provider added later are judged behind it; none of them has to know it
// exists, and none of them can forget it.
//
// # The rules
//
//   - A subject that reaches its limit inside the window is LOCKED. The first
//     lock lasts login.lock_base_seconds; each further lock in a row doubles it
//     up to login.lock_max_seconds. The doubling forgets after a quiet spell
//     and after a success.
//   - While a lock is in force even the RIGHT password is refused, and the
//     refused attempt is not counted. A lock the correct answer lifts is no
//     lock at all against somebody working through the space.
//   - An address on login.ip_allowlist is exempt from the per-address limit,
//     and may sign in to any locked account (with the right password): the lock
//     is not applied to that address. Its wrong attempts are audited but
//     counted against nobody. NO account is privileged — the bootstrap
//     administrator is limited like everyone else, and the way back in is an
//     allow-listed address. The one exception is a public demo's SHARED
//     account (FILEX_DEMO_MODE): its password is published, so the
//     per-account lock on it would be a switch any stranger flips for every
//     visitor; it is counted per address only (exempt.go).
//   - A success resets the ACCOUNT's counter. It does NOT reset the address's:
//     otherwise one valid login between guesses would keep a spraying address
//     under its limit for ever.
//
// # Known limits
//
// Locking an account by typing its name wrongly is possible by design (that is
// what a lock is); the allow-list is the remedy, and the escalating lock keeps
// the cost of the attack on the attacker's side of the door. The memory holds
// at most MaxCounters counters: past that, the counters a sweep would drop go
// first, then the oldest — a counter with no lock in force before one with —
// so a flood of made-up names or addresses cannot grow it without bound.
package loginguard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
)

// The doors a password can be typed at, as the audit trail names them.
const (
	ProtoWeb  = "web"
	ProtoDAV  = "dav"
	ProtoFTP  = "ftp"
	ProtoSFTP = "sftp"
	ProtoS3   = "s3"
)

// Audit actions the guard writes. One spelling each, so an operator grepping
// the Audit page finds every event ("login." is the filter prefix).
const (
	ActionFailed        = "login.failed"
	ActionLocked        = "login.locked"
	ActionUnlocked      = "login.unlocked"
	ActionAllowlistPass = "login.allowlist_pass"
)

// Why a wrong attempt was wrong — audit metadata, never shown to the caller.
const (
	ReasonCredentials = "invalid_credentials"
	ReasonTOTP        = "invalid_totp"
)

const (
	// sweepEvery is how often Run ticks: the settings are read again, and the
	// guard looks for locks that ran out (to audit their release), for
	// counters nobody has touched for a day, and for writes the table refused.
	sweepEvery = time.Minute
	// pruneAfter is how long a counter with no lock may sit unused.
	pruneAfter = 24 * time.Hour
	// maxSubject is the longest identifier stored as typed; a longer one is
	// cut and given a hash so two long names do not share a counter.
	maxSubject = 190
	// DefaultMaxCounters is the memory's size when Guard.MaxCounters is 0.
	DefaultMaxCounters = 100_000
	// writeTimeout bounds one write-through, so a hung database never holds a
	// sign-in answer.
	writeTimeout = 5 * time.Second
)

// counterKey names one counter.
type counterKey struct{ scope, subject string }

func keyOf(t *model.LoginThrottle) counterKey { return counterKey{t.Scope, t.Subject} }

// Guard is the sign-in limiter.
type Guard struct {
	Store db.Store
	// Now is the clock; nil = time.Now. Tests move it.
	Now func() time.Time
	// MaxCounters bounds the counters held in memory; 0 = DefaultMaxCounters.
	MaxCounters int
	// Every is how often Run ticks; 0 = a minute.
	Every time.Duration
	// OnTick, when set, runs at the end of every Tick. The server hangs the
	// re-resolution of the automatic trusted-proxy set on it
	// (clientip.RefreshAuto): the list the guard's settings name may say
	// `auto`, and a container can join a network at run time.
	OnTick func(context.Context)

	// loadMu serialises loading; loaded says it has happened (or was tried —
	// counters the table could not give are read at the next sweep, settings
	// at the next tick).
	loadMu sync.Mutex
	loaded atomic.Bool

	// settings is the settings in force (settings.go); setMu serialises the
	// writes and reloads that replace it. Read without a lock: every request
	// asks it for the trusted proxies.
	settings atomic.Pointer[snapshot]
	setMu    sync.Mutex

	// mu guards the memory: the counters, the writes the table owes, and
	// whether the table still has to be read.
	mu       sync.Mutex
	counters map[counterKey]*model.LoginThrottle
	pending  map[counterKey]struct{}
	needLoad bool

	// dbMu: one write-through at a time, each of the NEWEST state of its
	// counter, so two writes can never land in the wrong order.
	dbMu sync.Mutex

	// exempt names the account counters the per-account limit does not apply
	// to - a public demo's shared account (exempt.go). nil: none.
	exempt atomic.Pointer[exemption]
}

// New builds a Guard over store. It reads the table the first time it is
// used; call Load at start to do that before anything is served.
func New(store db.Store) *Guard { return &Guard{Store: store} }

func (g *Guard) now() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

func (g *Guard) maxCounters() int {
	if g.MaxCounters > 0 {
		return g.MaxCounters
	}
	return DefaultMaxCounters
}

// Load reads the settings and the counters from the table into memory. It is
// called once at start; a table that cannot be read is reported (and logged),
// the guard runs on the last known settings (the defaults) and an empty
// memory, and the counters are read again at the next sweep — merged under
// what the memory has counted since.
func (g *Guard) Load(ctx context.Context) error {
	g.loadMu.Lock()
	defer g.loadMu.Unlock()
	err := g.load(ctx)
	g.loaded.Store(true)
	return err
}

func (g *Guard) ensureLoaded(ctx context.Context) {
	if g.loaded.Load() {
		return
	}
	g.loadMu.Lock()
	defer g.loadMu.Unlock()
	if g.loaded.Load() {
		return
	}
	_ = g.load(ctx)
	g.loaded.Store(true)
}

func (g *Guard) load(ctx context.Context) error {
	serr := g.reloadSettings(ctx)
	cerr := g.loadCounters(ctx)
	if serr != nil {
		return serr
	}
	return cerr
}

// loadCounters fills the memory from the table. A counter the memory already
// holds — or owes the table a write for — is newer than the table's row and is
// kept as it is.
func (g *Guard) loadCounters(ctx context.Context) error {
	var (
		rows []*model.LoginThrottle
		err  error
	)
	if g.Store != nil {
		rows, err = g.Store.LoadLoginThrottles(ctx, g.now().Add(-pruneAfter), g.maxCounters())
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initMemory()
	if err != nil {
		g.needLoad = true
		slog.Warn("loginguard: could not read the counters; counting in memory, the table is read again at the next sweep", slog.Any("err", err))
		return err
	}
	g.needLoad = false
	for _, r := range rows {
		k := keyOf(r)
		if _, ok := g.counters[k]; ok {
			continue
		}
		if _, owed := g.pending[k]; owed {
			continue
		}
		if len(g.counters) >= g.maxCounters() {
			break
		}
		cp := *r
		g.counters[k] = &cp
	}
	return nil
}

// initMemory makes the maps; caller holds g.mu.
func (g *Guard) initMemory() {
	if g.counters == nil {
		g.counters = map[counterKey]*model.LoginThrottle{}
	}
	if g.pending == nil {
		g.pending = map[counterKey]struct{}{}
	}
}

// Attempt is one try at signing in.
type Attempt struct {
	// Identifier is what the caller typed as the account name (e-mail or
	// username), exactly as typed.
	Identifier string
	// Realm is the tenant realm the attempt was for on a multi-tenant install
	// (typed, or named by the address — auth.LoginRealm.CounterRealm); "" for
	// the platform's own realm and on a single-tenant install. It is part of
	// the account counter: `acme/alex`, `beta/alex` and `alex` are three
	// people, and one of them guessing wrong must not lock the others.
	Realm string
	// IP is the client address; "" when it could not be told.
	IP string
	// Protocol is one of the Proto* doors.
	Protocol string
}

// Subject is the counter key for an identifier: normalized, and shortened with
// a hash when long. Exported because the admin surface names counters by it.
//
// An operating-system account can be typed several ways (`alex`, `.\alex`,
// `CORP\alex`); they are ONE account, so they share one counter — otherwise
// somebody guessing a password could take a fresh allowance with every spelling.
// Only the `DOMAIN\` / `.\` prefix is dropped. The `name@host` form is left as
// it is, exactly as identity.Resolve reads it: an e-mail address and an
// operating-system name can collide, and folding the local part would let one
// person's failures lock another's account.
//
// A realm (multi-tenant) leads the key: `acme/alex`. It is read from a
// `realm/` prefix too, so the key an administrator copies from the locks list
// unlocks exactly that counter, and the domain prefix is dropped AFTER it
// (`acme/CORP\alex` is `acme/alex`, not `alex`).
func Subject(identifier string) string {
	return SubjectIn("", identifier)
}

// SubjectIn is Subject for an attempt in a realm: `<realm>/<subject>`; with no
// realm, the identifier's own `realm/` prefix, if it has one (see Subject).
func SubjectIn(realm, identifier string) string {
	s := identity.Normalize(identifier)
	r := strings.ToLower(strings.TrimSpace(realm))
	if r == "" {
		if i := strings.IndexByte(s, '/'); i > 0 && !strings.ContainsAny(s[:i], "@\\") {
			r, s = s[:i], s[i+1:]
		}
	}
	s = stripDomainPrefix(strings.TrimSpace(s))
	if r != "" && s != "" {
		s = r + "/" + s
	}
	if s == "" {
		return "(empty)"
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if s == "" {
		return "(empty)"
	}
	if len([]rune(s)) > maxSubject-20 {
		sum := sha256.Sum256([]byte(s))
		r := []rune(s)
		return string(r[:maxSubject-20-17]) + "~" + hex.EncodeToString(sum[:8])
	}
	return s
}

// subject is the attempt's account counter key.
func (a Attempt) subject() string { return SubjectIn(a.Realm, a.Identifier) }

// stripDomainPrefix turns `corp\alex` and `.\alex` into `alex`. A backslash
// cannot be part of a filex username or an e-mail address, so a name that has
// one, and no `@`, is an operating-system spelling. Nothing after the last
// backslash (`corp\`) leaves the identifier as typed.
func stripDomainPrefix(s string) string {
	if strings.Contains(s, "@") {
		return s
	}
	i := strings.LastIndex(s, "\\")
	if i < 0 || strings.TrimSpace(s[i+1:]) == "" {
		return s
	}
	return strings.TrimSpace(s[i+1:])
}

func addrSubject(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "(unknown)"
	}
	return ip
}

// Verdict is Check's answer: may this attempt be judged at all?
type Verdict struct {
	// Blocked: a lock is in force for this attempt. The password must not be
	// checked, and the attempt is not counted.
	Blocked bool
	// Scope names the lock that blocks: model.LoginThrottleAccount or
	// model.LoginThrottleIP.
	Scope string
	// RetryAfter is how long until it lifts.
	RetryAfter time.Duration
	// Allowlisted: the address is on the allow-list.
	Allowlisted bool
	// Disabled: the guard is switched off; nothing is counted.
	Disabled bool
}

// Check decides, BEFORE any password is compared, whether the attempt may go
// on. A blocked attempt is refused whatever it carries. It reads memory only.
func (g *Guard) Check(ctx context.Context, a Attempt) Verdict {
	cfg := g.Config(ctx)
	if !cfg.Enabled {
		return Verdict{Disabled: true}
	}
	v := Verdict{Allowlisted: cfg.Allow.ContainsString(a.IP)}
	if v.Allowlisted {
		return v
	}
	keys := []counterKey{{model.LoginThrottleIP, addrSubject(a.IP)}}
	if !g.accountExempt(ctx, a) {
		keys = append(keys, counterKey{model.LoginThrottleAccount, a.subject()})
	}
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	var worst time.Duration
	for _, k := range keys {
		if row := g.counters[k]; row.Locked(now) {
			if d := row.LockedUntil.Sub(now); d > worst {
				worst, v.Blocked, v.Scope, v.RetryAfter = d, true, k.scope, d
			}
		}
	}
	return v
}

// Outcome is Failed's answer: what the caller is told about the attempt that
// just failed.
type Outcome struct {
	// Locked: this failure (or a lock already running) shuts the door now.
	Locked bool
	// Scope is the counter the message speaks about: the one with fewer tries
	// left (the account on a tie), or the one that locked.
	Scope string
	// Remaining is the tries left before that counter locks (0 when Locked).
	Remaining int
	// Limit is the failure number at which it locks.
	Limit int
	// RetryAfter is how long the lock lasts, when Locked.
	RetryAfter time.Duration
	// Unlimited: nothing is counted for this attempt (guard off, or an
	// allow-listed address), so there is no count to report.
	Unlimited bool
}

// event is an audit row to write once the memory's lock is released.
type event struct {
	action string
	a      Attempt
	meta   map[string]any
}

// Failed records a wrong attempt and answers what to tell the caller. reason
// is one of the Reason* constants (audit only).
//
// The same code runs for an identifier that names no account: the counter is
// keyed by the text, not by a user row.
func (g *Guard) Failed(ctx context.Context, a Attempt, reason string) Outcome {
	cfg := g.Config(ctx)
	if !cfg.Enabled {
		return Outcome{Unlimited: true}
	}
	if cfg.Allow.ContainsString(a.IP) {
		g.audit(ctx, ActionFailed, a, map[string]any{"reason": reason, "allowlisted": true})
		return Outcome{Unlimited: true}
	}

	// The shared account of a public demo is not counted per account: only its
	// address is (exempt.go). The reply then speaks about the address alone -
	// never of an account lock that will not come.
	exempt := g.accountExempt(ctx, a)

	var events []event
	g.mu.Lock()
	now := g.now()
	acctKey := counterKey{model.LoginThrottleAccount, a.subject()}
	ipKey := counterKey{model.LoginThrottleIP, addrSubject(a.IP)}
	var (
		acct    model.LoginThrottle
		aLocked bool
	)
	if !exempt {
		var acctRow *model.LoginThrottle
		acctRow, aLocked = g.bump(cfg, acctKey, cfg.AccountMax, a, now, &events)
		acct = *acctRow
	}
	ipRow, iLocked := g.bump(cfg, ipKey, cfg.IPMax, a, now, &events)
	ip := *ipRow
	g.mu.Unlock()

	if exempt {
		g.persist(ctx, ipKey)
	} else {
		g.persist(ctx, acctKey, ipKey)
	}
	g.emit(ctx, events)

	out := Outcome{}
	switch {
	case exempt && iLocked:
		out.Locked, out.Scope, out.RetryAfter, out.Limit = true, model.LoginThrottleIP, lockLeft(&ip, now), cfg.IPMax
	case exempt:
		out.Scope, out.Limit, out.Remaining = model.LoginThrottleIP, cfg.IPMax, cfg.IPMax-ip.Fails
	case aLocked || iLocked:
		out.Locked = true
		// The message names the lock that runs longer.
		out.Scope, out.RetryAfter = model.LoginThrottleAccount, lockLeft(&acct, now)
		if d := lockLeft(&ip, now); iLocked && (!aLocked || d > out.RetryAfter) {
			out.Scope, out.RetryAfter = model.LoginThrottleIP, d
		}
		out.Limit = limitFor(out.Scope, cfg)
	default:
		out.Scope, out.Limit = model.LoginThrottleAccount, cfg.AccountMax
		out.Remaining = cfg.AccountMax - acct.Fails
		if r := cfg.IPMax - ip.Fails; r < out.Remaining {
			out.Scope, out.Limit, out.Remaining = model.LoginThrottleIP, cfg.IPMax, r
		}
	}
	meta := map[string]any{"reason": reason}
	if out.Locked {
		meta["locked"] = true
	} else {
		meta["remaining"] = out.Remaining
	}
	if exempt {
		meta["account_exempt"] = true
	}
	g.audit(ctx, ActionFailed, a, meta)
	return out
}

func limitFor(scope string, cfg Config) int {
	if scope == model.LoginThrottleIP {
		return cfg.IPMax
	}
	return cfg.AccountMax
}

func lockLeft(t *model.LoginThrottle, now time.Time) time.Duration {
	if t == nil || t.LockedUntil == nil {
		return 0
	}
	return t.LockedUntil.Sub(now)
}

// bump adds one failure to a counter in memory, locks it when the limit is
// reached, and returns the row plus whether THIS failure locked it. The audit
// rows it causes are appended to events. Caller holds g.mu.
func (g *Guard) bump(cfg Config, k counterKey, max int, a Attempt, now time.Time, events *[]event) (*model.LoginThrottle, bool) {
	g.initMemory()
	row := g.counters[k]
	if row == nil {
		g.makeRoom(now)
		row = &model.LoginThrottle{Scope: k.scope, Subject: k.subject, WindowStart: now}
		g.counters[k] = row
	}
	if ev, ok := expiredLock(row, now); ok {
		*events = append(*events, ev)
	}
	// A quiet spell forgets both the count and the escalation.
	if now.Sub(row.WindowStart) > cfg.Window {
		row.Fails, row.WindowStart = 0, now
	}
	if row.LockLevel > 0 && row.LockedUntil == nil && now.Sub(row.LastFailAt) > cfg.LockMax+cfg.Window {
		row.LockLevel = 0
	}
	row.Fails++
	row.LastFailAt = now
	row.LastIP, row.LastProtocol = a.IP, a.Protocol
	locked := false
	if row.Fails >= max {
		row.LockLevel++
		d := lockDuration(cfg, row.LockLevel)
		until := now.Add(d)
		row.LockedUntil = &until
		row.Fails, row.WindowStart = 0, now
		locked = true
		*events = append(*events, lockEvent(row, a))
	}
	return row, locked
}

// lockDuration is base * 2^(level-1), never above the ceiling.
func lockDuration(cfg Config, level int) time.Duration {
	d := cfg.LockBase
	for i := 1; i < level && d < cfg.LockMax; i++ {
		d *= 2
	}
	if d > cfg.LockMax {
		d = cfg.LockMax
	}
	return d
}

// Succeeded records a correct sign-in. A normal success resets the ACCOUNT's
// counter (never the address's). A success from an allow-listed address that
// only got in because of the allow-list — the account or address was locked —
// is audited as such, and changes no counter: the lock is not applied to that
// address, it is not lifted for everybody else.
func (g *Guard) Succeeded(ctx context.Context, a Attempt) {
	cfg := g.Config(ctx)
	if !cfg.Enabled {
		return
	}
	now := g.now()
	exempt := g.accountExempt(ctx, a)
	if cfg.Allow.ContainsString(a.IP) {
		var pass *event
		keys := []counterKey{{model.LoginThrottleIP, addrSubject(a.IP)}}
		if !exempt {
			keys = append([]counterKey{{model.LoginThrottleAccount, a.subject()}}, keys...)
		}
		g.mu.Lock()
		for _, k := range keys {
			if row := g.counters[k]; row.Locked(now) {
				pass = &event{ActionAllowlistPass, a, map[string]any{"lock_scope": k.scope, "locked_until": row.LockedUntil.UTC().Format(time.RFC3339)}}
				break
			}
		}
		g.mu.Unlock()
		if pass != nil {
			g.audit(ctx, pass.action, pass.a, pass.meta)
		}
		return
	}
	if exempt {
		return
	}
	k := counterKey{model.LoginThrottleAccount, a.subject()}
	var events []event
	g.mu.Lock()
	row := g.counters[k]
	if row == nil || (row.Fails == 0 && row.LockLevel == 0 && row.LockedUntil == nil) {
		g.mu.Unlock()
		return
	}
	if ev, ok := expiredLock(row, now); ok {
		events = append(events, ev)
	}
	delete(g.counters, k)
	g.mu.Unlock()
	g.persist(ctx, k)
	g.emit(ctx, events)
}

// Unlock lifts a lock and clears the counter (an administrator's action; the
// caller writes the audit row through the request's audit detail). It reports
// whether there was a counter — in memory, or in the table. The lock is lifted
// in memory whatever the table answers; a delete the table refuses is retried.
func (g *Guard) Unlock(ctx context.Context, scope, subject string) (bool, error) {
	g.ensureLoaded(ctx)
	k := counterKey{scope, subject}
	g.mu.Lock()
	g.initMemory()
	_, had := g.counters[k]
	delete(g.counters, k)
	g.mu.Unlock()
	if g.Store == nil {
		return had, nil
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	g.dbMu.Lock()
	defer g.dbMu.Unlock()
	inTable, err := g.Store.DeleteLoginThrottle(wctx, scope, subject)
	g.mu.Lock()
	if err != nil {
		g.pending[k] = struct{}{}
	} else {
		delete(g.pending, k)
	}
	g.mu.Unlock()
	if err != nil {
		slog.Warn("loginguard: could not delete a counter; lifted in memory, the delete is retried", slog.String("scope", scope), slog.Any("err", err))
		if !had {
			return false, err
		}
	}
	return had || inTable, nil
}

// UnlockAll lifts every lock in force — in memory, and any the table holds
// that the memory does not (a counter this process let go of) — and answers
// how many.
func (g *Guard) UnlockAll(ctx context.Context) int {
	g.ensureLoaded(ctx)
	now := g.now()
	var keys []counterKey
	g.mu.Lock()
	for k, row := range g.counters {
		if row.Locked(now) {
			keys = append(keys, k)
			delete(g.counters, k)
		}
	}
	g.mu.Unlock()
	g.persist(ctx, keys...)
	n := len(keys)
	if g.Store != nil {
		if rows, err := g.Store.ListLoginThrottles(ctx, "", &now, 1000); err == nil {
			for _, r := range rows {
				if ok, _ := g.Unlock(ctx, r.Scope, r.Subject); ok {
					n++
				}
			}
		}
	}
	return n
}

// List answers the counters in memory — the ones deciding right now — the
// most recently active first: one scope ("" = both), only the locks in force
// at lockedAt when it is non-nil, at most limit (<= 0: 200; never more than
// 1000). The rows are copies.
func (g *Guard) List(ctx context.Context, scope string, lockedAt *time.Time, limit int) []*model.LoginThrottle {
	g.ensureLoaded(ctx)
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	g.mu.Lock()
	out := make([]*model.LoginThrottle, 0, len(g.counters))
	for _, row := range g.counters {
		if scope != "" && row.Scope != scope {
			continue
		}
		if lockedAt != nil && !row.Locked(*lockedAt) {
			continue
		}
		cp := *row
		out = append(out, &cp)
	}
	g.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastFailAt.Equal(out[j].LastFailAt) {
			return out[i].LastFailAt.After(out[j].LastFailAt)
		}
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Subject < out[j].Subject
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// expiredLock clears the row's locked_until when the lock has run out (the
// escalation level stays) and answers the audit row of its release. A lock has
// no timer of its own — nothing runs when it ends — so the release is noticed
// the next time the row is touched, or by the periodic sweep; the audit row
// carries the instant it really ended.
func expiredLock(row *model.LoginThrottle, now time.Time) (event, bool) {
	if row == nil || row.LockedUntil == nil || row.LockedUntil.After(now) {
		return event{}, false
	}
	ended := *row.LockedUntil
	row.LockedUntil = nil
	return event{ActionUnlocked, Attempt{Identifier: subjectLabel(row), IP: ipLabel(row)}, map[string]any{
		"scope": row.Scope, "reason": "expired", "ended_at": ended.UTC().Format(time.RFC3339),
	}}, true
}

func lockEvent(row *model.LoginThrottle, a Attempt) event {
	meta := map[string]any{
		"scope": row.Scope, "level": row.LockLevel,
		"locked_until": row.LockedUntil.UTC().Format(time.RFC3339),
	}
	if row.Scope == model.LoginThrottleIP {
		return event{ActionLocked, Attempt{IP: row.Subject, Protocol: a.Protocol}, meta}
	}
	return event{ActionLocked, a, meta}
}

func subjectLabel(row *model.LoginThrottle) string {
	if row.Scope == model.LoginThrottleAccount {
		return row.Subject
	}
	return ""
}

func ipLabel(row *model.LoginThrottle) string {
	if row.Scope == model.LoginThrottleIP {
		return row.Subject
	}
	return row.LastIP
}

// idle: a counter with nothing left to say at now — no failure for a day and
// no lock running — which the sweep drops (the table's PruneLoginThrottles
// rule).
func idle(row *model.LoginThrottle, now time.Time) bool {
	before := now.Add(-pruneAfter)
	return row.LastFailAt.Before(before) && (row.LockedUntil == nil || row.LockedUntil.Before(before))
}

// makeRoom keeps the memory under its size before a new counter is added:
// first the idle counters go, then — still full — the oldest, a counter with
// no lock in force before one with, a tenth of the memory at a time (so a
// flood does not sort on every attempt). A counter let go of stays in the
// table until the table's own prune. Caller holds g.mu.
func (g *Guard) makeRoom(now time.Time) {
	max := g.maxCounters()
	if len(g.counters) < max {
		return
	}
	for k, row := range g.counters {
		if idle(row, now) {
			g.forget(k)
		}
	}
	if len(g.counters) < max {
		return
	}
	type cand struct {
		k      counterKey
		locked bool
		last   time.Time
	}
	cands := make([]cand, 0, len(g.counters))
	for k, row := range g.counters {
		cands = append(cands, cand{k, row.Locked(now), row.LastFailAt})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].locked != cands[j].locked {
			return !cands[i].locked
		}
		return cands[i].last.Before(cands[j].last)
	})
	drop := len(g.counters) - max + 1 + max/10
	for i := 0; i < drop && i < len(cands); i++ {
		g.forget(cands[i].k)
	}
}

// forget lets a counter go from memory without touching the table. Caller
// holds g.mu.
func (g *Guard) forget(k counterKey) {
	delete(g.counters, k)
	delete(g.pending, k)
}

// persist writes the newest state of each counter to the table: the row when
// the memory holds one, a delete when it does not. A write the table refuses
// is logged and owed (pending); the decision is the memory's either way. After
// a write that succeeded the owed ones are tried again.
func (g *Guard) persist(ctx context.Context, keys ...counterKey) {
	if len(keys) == 0 || g.Store == nil {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	g.dbMu.Lock()
	defer g.dbMu.Unlock()
	if g.writeLocked(wctx, keys) {
		g.flushLocked(wctx)
	}
}

// flush writes every counter the table is owed. Called by the sweep.
func (g *Guard) flush(ctx context.Context) {
	if g.Store == nil {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	g.dbMu.Lock()
	defer g.dbMu.Unlock()
	g.flushLocked(wctx)
}

// flushLocked: caller holds g.dbMu.
func (g *Guard) flushLocked(ctx context.Context) {
	g.mu.Lock()
	keys := make([]counterKey, 0, len(g.pending))
	for k := range g.pending {
		keys = append(keys, k)
	}
	g.mu.Unlock()
	if len(keys) > 0 {
		g.writeLocked(ctx, keys)
	}
}

// writeLocked writes each key's newest state and reports whether the table
// took them all. Caller holds g.dbMu (not g.mu).
func (g *Guard) writeLocked(ctx context.Context, keys []counterKey) bool {
	all := true
	for _, k := range keys {
		g.mu.Lock()
		g.initMemory()
		row, inMemory := g.counters[k]
		var cp model.LoginThrottle
		if inMemory {
			cp = *row
		}
		g.mu.Unlock()
		var err error
		if inMemory {
			err = g.Store.SaveLoginThrottle(ctx, &cp)
		} else {
			_, err = g.Store.DeleteLoginThrottle(ctx, k.scope, k.subject)
		}
		g.mu.Lock()
		if err != nil {
			g.pending[k] = struct{}{}
		} else {
			delete(g.pending, k)
		}
		g.mu.Unlock()
		if err != nil {
			all = false
			slog.Warn("loginguard: could not write a counter; the limit goes on from memory, the write is retried",
				slog.String("scope", k.scope), slog.Any("err", err))
		}
	}
	return all
}

// Run is the housekeeping loop: every Every (a minute) it runs Tick, until ctx
// ends. The server starts it once, at start (server.go). No sign-in decision
// waits on it, and no sign-in attempt runs it: a quiet instance is swept as
// often as a busy one.
func (g *Guard) Run(ctx context.Context) {
	every := g.Every
	if every <= 0 {
		every = sweepEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.Tick(ctx)
		}
	}
}

// Tick is one turn of the loop. The SETTINGS are read from the table again —
// a change saved on another instance sharing the database is in force here
// from this tick on, so a change reaches every instance within a minute; a
// read the table refuses keeps the last value known. Then Sweep. The COUNTERS
// are not read again: they are this instance's own (Load reads them once).
// Last, OnTick (the automatic trusted-proxy set is worked out again).
func (g *Guard) Tick(ctx context.Context) {
	g.ensureLoaded(ctx)
	_ = g.reloadSettings(ctx)
	g.refreshExempt(ctx, true)
	g.Sweep(ctx)
	if g.OnTick != nil {
		g.OnTick(ctx)
	}
}

// Sweep does the housekeeping now: it releases the locks that ran out
// (auditing each), drops from memory the counters idle for a day, reads the
// table's counters if they could not be read at Load, writes what the table
// is owed, and prunes the table. Tick runs it every minute; the admin lock
// list runs it too, so what it shows and the audit trail agree.
func (g *Guard) Sweep(ctx context.Context) {
	g.ensureLoaded(ctx)
	g.mu.Lock()
	again := g.needLoad
	g.mu.Unlock()
	if again {
		_ = g.loadCounters(ctx)
	}
	now := g.now()
	var (
		events   []event
		released []counterKey
	)
	g.mu.Lock()
	g.initMemory()
	for k, row := range g.counters {
		if ev, ok := expiredLock(row, now); ok {
			events = append(events, ev)
			released = append(released, k)
		}
		if idle(row, now) {
			g.forget(k)
		}
	}
	// A counter that went idle needs no write of its release: the table's
	// prune below drops its row.
	keep := released[:0]
	for _, k := range released {
		if _, ok := g.counters[k]; ok {
			keep = append(keep, k)
		}
	}
	g.mu.Unlock()
	g.persist(ctx, keep...)
	g.emit(ctx, events)
	g.flush(ctx)
	if g.Store != nil {
		if _, err := g.Store.PruneLoginThrottles(ctx, now.Add(-pruneAfter)); err != nil {
			slog.Warn("loginguard: could not prune counters", slog.Any("err", err))
		}
	}
}

func (g *Guard) emit(ctx context.Context, events []event) {
	for _, ev := range events {
		g.audit(ctx, ev.action, ev.a, ev.meta)
	}
}

// audit writes one audit row. The identifier and the address are visible; a
// password never reaches this package. An attempt with no identifier (an
// address-only event) is filed under its address.
func (g *Guard) audit(ctx context.Context, action string, a Attempt, meta map[string]any) {
	if g.Store == nil {
		return
	}
	if meta == nil {
		meta = map[string]any{}
	}
	target := a.IP
	if a.Identifier != "" {
		target = a.subject()
		meta["identifier"] = target
	}
	if a.Protocol != "" {
		meta["protocol"] = a.Protocol
	}
	e := &model.AuditEntry{
		Action:     action,
		TargetType: "login",
		TargetID:   target,
		Metadata:   meta,
		IP:         a.IP,
		CreatedAt:  g.now(),
	}
	// The request may already be gone (a protocol's context.Background) or
	// cancelled; the row is best-effort either way.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	if err := g.Store.InsertAuditEntry(wctx, e); err != nil {
		slog.Warn("loginguard: audit insert failed", slog.String("action", action), slog.Any("err", err))
	}
}
