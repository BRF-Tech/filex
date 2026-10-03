package loginguard

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ── Accounts the per-ACCOUNT limit never applies to ────────────────────────
//
// ⚠⚠ One case, and only one: the shared account of a PUBLIC DEMO
// (FILEX_DEMO_MODE). Its credentials are printed on the sign-in page, so a
// per-account lock on it is a switch any stranger can flip for every other
// visitor: five wrong passwords and the next person typing the right one
// reads "this account is locked" (measured 2026-10-01). The per-ADDRESS limit
// still applies to every attempt on it - one address guessing is stopped
// exactly as before - and so does the allow-list. Every other account,
// administrators included, is limited as the package doc says.
//
// The guard is told WHICH counters through a function (ExemptAccounts),
// asked once on first use and again at every Tick, so an account seeded
// after the start, or renamed, is followed within a minute. The answer is
// account counter keys as Subject / SubjectIn makes them - the realm is part
// of the key, so on a multi-tenant install the exemption is the account's
// counter in ITS realm (`acme/demo`), and `beta/demo` or the platform's
// `demo` - other people - are limited as ever.

// ExemptFunc answers the account counter keys (SubjectIn form) the per-account
// limit does not apply to. An error keeps the last answer in force; the first
// time, what came back with it is used.
type ExemptFunc func(ctx context.Context) ([]string, error)

// exemption is the installed ExemptFunc and its last answer.
type exemption struct {
	resolve ExemptFunc
	mu      sync.Mutex
	// set is the answer in force; nil until the first one.
	set atomic.Pointer[map[string]struct{}]
}

// ExemptAccounts installs fn (nil removes the exemption). Safe to call while
// the guard is running.
func (g *Guard) ExemptAccounts(fn ExemptFunc) {
	if fn == nil {
		g.exempt.Store(nil)
		return
	}
	g.exempt.Store(&exemption{resolve: fn})
}

// accountExempt reports whether the attempt's ACCOUNT counter is exempt. The
// first call resolves the answer (a database read, outside every lock of the
// guard's memory); after that it reads memory.
func (g *Guard) accountExempt(ctx context.Context, a Attempt) bool {
	ex := g.exempt.Load()
	if ex == nil {
		return false
	}
	set := ex.set.Load()
	if set == nil {
		g.refreshExempt(ctx, false)
		if set = ex.set.Load(); set == nil {
			return false
		}
	}
	_, ok := (*set)[a.subject()]
	return ok
}

// refreshExempt asks the installed ExemptFunc again - at every Tick (force),
// and at the first use - puts the answer in force and lets go of the account
// counters it names: a lock such an account earned before (a restored copy, a
// demo switched on later) is no lock any more, so neither the decision nor
// the admin list may keep it. The table is told too (persist: the row is
// deleted, or the delete is owed), so a restart does not bring it back.
func (g *Guard) refreshExempt(ctx context.Context, force bool) {
	ex := g.exempt.Load()
	if ex == nil {
		return
	}
	ex.mu.Lock()
	prev := ex.set.Load()
	if prev != nil && !force {
		// Resolved by a concurrent first use while this one waited.
		ex.mu.Unlock()
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	subjects, err := ex.resolve(rctx)
	cancel()
	if err != nil {
		slog.Warn("loginguard: could not read which account is exempt from the account limit; keeping the last answer", slog.Any("err", err))
		if prev != nil {
			ex.mu.Unlock()
			return
		}
	}
	set := make(map[string]struct{}, len(subjects))
	for _, s := range subjects {
		if s = strings.TrimSpace(s); s != "" {
			set[s] = struct{}{}
		}
	}
	ex.set.Store(&set)
	ex.mu.Unlock()

	keys := make([]counterKey, 0, len(set))
	g.mu.Lock()
	g.initMemory()
	for s := range set {
		k := counterKey{model.LoginThrottleAccount, s}
		delete(g.counters, k)
		keys = append(keys, k)
	}
	g.mu.Unlock()
	g.persist(ctx, keys...)
}

// DemoAccount is the ExemptFunc of a public demo: the account identifier
// (FILEX_DEMO_USER - the name the demo page's button signs in with) names,
// looked up as a sign-in looks it up (identity.Resolve), exempt by its e-mail,
// its username, the login name a made-up address was derived from (emailToken
// is the installation's FILEX_OS_LOGIN_EMAIL_TOKEN) and the identifier as
// configured - each in the account's own realm (its tenant's; "" for the
// platform's). An identifier that names no account yet is exempt as written,
// in the platform's realm, until the account exists. A blank one exempts
// nobody.
func DemoAccount(store db.Store, identifier, emailToken string) ExemptFunc {
	return func(ctx context.Context) ([]string, error) {
		id := strings.TrimSpace(identifier)
		if id == "" || store == nil {
			return nil, nil
		}
		u, err := identity.Resolve(ctx, store, id)
		if errors.Is(err, identity.ErrNotFound) {
			return []string{Subject(id)}, nil
		}
		if err != nil {
			return []string{Subject(id)}, err
		}
		realm := ""
		if u.ProviderID != nil && *u.ProviderID != 0 {
			p, perr := store.GetProvider(ctx, *u.ProviderID)
			if perr != nil {
				return []string{Subject(id)}, perr
			}
			realm = p.LoginRealm()
		}
		out := []string{SubjectIn(realm, u.Email), SubjectIn(realm, id)}
		if u.Username != "" {
			out = append(out, SubjectIn(realm, u.Username))
		}
		if name, ok := identity.LoginNameOf(u.Email, realm, emailToken); ok {
			out = append(out, SubjectIn(realm, name))
		}
		return out, nil
	}
}
