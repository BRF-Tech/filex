package authsetup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/multioidc"
	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// ── Starting an OIDC sign-in for a tenant (docs/TENANT-ADMIN.md) ────────────
//
// An OIDC instance may be bound to several tenants, and a tenant may have
// several (its own, on its provider row, and shared ones). So a flow is
// started for ONE tenant through ONE instance:
//
//	GET /api/auth/oidc/start?instance=<slug>&realm=<realm>
//
// The tenant is the one the address names, else the one the realm names,
// else the platform's own (the same rule as a password sign-in,
// auth.ResolveLoginRealm). The instance must be one of that tenant's; without
// `instance` the tenant's first is started (its own OIDC first), which is
// what a client that never learned about instances gets.
//
// What was decided is remembered on the server (flows), under a random id in a
// cookie, until the identity provider sends the browser back: the callback
// finds its instance and its tenant there, never by the address, so a tenant
// with no address of its own signs in on the platform's address and its
// account is made in the right tenant.
//
// ⚠ Held in memory, like the handoff tickets: a restart forgets flows in
// flight (the person starts again), and behind several replicas the callback
// needs sticky routing. A single-tenant install starting its one OIDC with no
// `instance` keeps no flow at all: nothing changes for it.

const (
	flowCookie = "filex_oidc_flow"
	flowTTL    = 10 * time.Minute
	// OwnSSOKey names a tenant's own OIDC (its provider row) among its choices.
	OwnSSOKey = "tenant"
)

// ErrNoSSOForTenant answers a start whose tenant has no such OIDC, or a realm
// nobody has. One error for both, as the sign-in form answers one way.
var ErrNoSSOForTenant = errors.New("oidc: no such sign-in for this realm")

type pendingFlow struct {
	key      string
	tenant   int64
	redirect string
	expires  time.Time
}

type flowBook struct {
	mu    sync.Mutex
	flows map[string]*pendingFlow
	rows  *multioidc.Dispatcher
}

func (b *flowBook) put(f *pendingFlow) (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(raw[:])
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.flows == nil {
		b.flows = map[string]*pendingFlow{}
	}
	for k, v := range b.flows {
		if now.After(v.expires) {
			delete(b.flows, k)
		}
	}
	// A start costs nobody a sign-in, so a stranger could fill the book;
	// bounded, it refuses new starts until flows expire instead of growing.
	if len(b.flows) >= maxFlows {
		return "", errTooManyFlows
	}
	b.flows[id] = f
	return id, nil
}

// maxFlows bounds the sign-ins in flight held in memory.
const maxFlows = 20000

// The sign-in page says so (auth.SSOReasonBusy) rather than "SSO failed".
var errTooManyFlows = auth.SSORefused(auth.SSOReasonBusy, errors.New("oidc: too many sign-ins in flight; try again in a few minutes"))

// take spends a flow: it is answered once.
func (b *flowBook) take(id string) *pendingFlow {
	if id == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	f := b.flows[id]
	delete(b.flows, id)
	if f == nil || time.Now().After(f.expires) {
		return nil
	}
	return f
}

// SSOChoice is one OIDC a tenant may sign in through.
type SSOChoice struct {
	// Key names it in /api/auth/oidc/start?instance=: the instance's slug, or
	// OwnSSOKey for the tenant's own OIDC on its provider row.
	Key   string
	Label string
	drv   auth.OIDCDriver
	// own: the tenant's provider row (its driver carries its redirect URI).
	own bool
}

// flowTenant is the tenant a flow is for: the address's, else the realm's,
// else the platform's own (0 on a single-tenant install). A realm nobody has,
// or one that is not the address's tenant, is ErrNoSSOForTenant.
func (s *Set) flowTenant(ctx context.Context, r *http.Request) (*model.Provider, int64, error) {
	if !s.multiTenant || s.store == nil {
		return nil, 0, nil
	}
	typed := tenant.NormalizeRealm(r.URL.Query().Get("realm"))
	lr, err := auth.ResolveLoginRealm(ctx, s.store, typed, typed != "", multioidc.RequestHost(r), "")
	if err != nil {
		return nil, 0, ErrNoSSOForTenant
	}
	if lr.Tenant == nil || lr.Tenant.IsSupertenant {
		return lr.Tenant, s.binds.Main, nil
	}
	return lr.Tenant, lr.Tenant.ID, nil
}

// SSOChoices lists the OIDC a tenant may sign in through, in order: its own
// (provider row) first, then the instances bound to it.
func (l *Live) SSOChoices(ctx context.Context, p *model.Provider, tenantID int64, host string) []SSOChoice {
	s := l.cur.Load()
	var out []SSOChoice
	if s.multiTenant && p != nil && !p.IsSupertenant && multioidc.HasOwnOIDC(p) {
		if d, err := l.flows.rows.TenantDriver(ctx, p, host); err == nil {
			out = append(out, SSOChoice{Key: OwnSSOKey, Label: p.Name, drv: d, own: true})
		}
	}
	for _, e := range s.For(tenantID).SSO {
		if d, ok := e.Driver.(auth.OIDCDriver); ok {
			out = append(out, SSOChoice{Key: e.Slug, Label: e.Label, drv: d})
		}
	}
	return out
}

func pick(choices []SSOChoice, key string) (SSOChoice, bool) {
	if len(choices) == 0 {
		return SSOChoice{}, false
	}
	if key == "" {
		return choices[0], true
	}
	for _, c := range choices {
		if c.Key == key {
			return c, true
		}
	}
	return SSOChoice{}, false
}

// callbackOn is the redirect URI of the address a flow started on, when that
// address is a tenant's own (the identity provider must send the browser back
// there: the state cookie is on it). "" keeps the instance's own.
func (s *Set) callbackOn(ctx context.Context, r *http.Request) string {
	host := multioidc.RequestHost(r)
	if !s.multiTenant || host == "" || s.store == nil {
		return ""
	}
	p, err := s.store.GetProviderByHost(ctx, host)
	if err != nil || p == nil || p.IsSupertenant {
		return ""
	}
	scheme := "https"
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "http") {
		scheme = "http"
	}
	return scheme + "://" + host + basepath.Path(ctx, "/api/auth/oidc/callback")
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// startFlow is oidcProxy.StartFlow.
func (l *Live) startFlow(w http.ResponseWriter, r *http.Request) error {
	s := l.cur.Load()
	key := strings.TrimSpace(r.URL.Query().Get("instance"))
	// A single-tenant install starting the one OIDC it has, as always.
	if !s.multiTenant && key == "" {
		if s.oidc == nil {
			return ErrOIDCOff
		}
		return s.oidc.StartFlow(w, r)
	}
	ctx := r.Context()
	p, tid, err := s.flowTenant(ctx, r)
	if err != nil {
		return err
	}
	choice, ok := pick(l.SSOChoices(ctx, p, tid, multioidc.RequestHost(r)), key)
	if !ok {
		if key == "" && !s.multiTenant {
			return ErrOIDCOff
		}
		return ErrNoSSOForTenant
	}
	f := &pendingFlow{key: choice.Key, tenant: tid, expires: time.Now().Add(flowTTL)}
	if !choice.own {
		f.redirect = s.callbackOn(ctx, r)
	}
	id, err := l.flows.put(f)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: flowCookie, Value: id, Path: basepath.CookiePath(ctx), HttpOnly: true,
		Secure: secureRequest(r), SameSite: http.SameSiteLaxMode, MaxAge: int(flowTTL / time.Second),
	})
	return choice.drv.StartFlow(w, r.WithContext(authoidc.WithFlow(ctx, l.flowOf(s, f))))
}

// flowOf is what the driver is told for a flow: the redirect URI it started
// with, and the tenant its account belongs to - ALWAYS a tenant: the
// platform's own one (Main) for a flow started there or on a single-tenant
// install, never "any tenant" (docs/SSO.md, "Which account an SSO sign-in
// opens").
func (l *Live) flowOf(s *Set, f *pendingFlow) authoidc.Flow {
	out := authoidc.Flow{RedirectURL: f.redirect}
	switch {
	case !s.multiTenant:
		out.Tenant = authoidc.MainTenant()
	case f.tenant == 0 || (s.binds != nil && f.tenant == s.binds.Main):
		out.Tenant = authoidc.Tenant{ProviderID: f.tenant, Main: true}
	default:
		out.Tenant = authoidc.Tenant{ProviderID: f.tenant}
	}
	return out
}

// errNoFlow refuses an SSO callback that carries no sign-in this server
// started, on a multi-tenant install. The page says "expired": it is what
// the person can act on (start again), and it says nothing about any tenant.
var errNoFlow = auth.SSORefused(auth.SSOReasonExpired, errors.New(
	"oidc: the callback carries no sign-in this server started (no flow cookie, or one it does not know); a multi-tenant install signs nobody in without knowing the tenant"))

// handleCallback is oidcProxy.HandleCallback.
func (l *Live) handleCallback(w http.ResponseWriter, r *http.Request) (*model.User, string, error) {
	s := l.cur.Load()
	c, _ := r.Cookie(flowCookie)
	var f *pendingFlow
	if c != nil {
		f = l.flows.take(c.Value)
		http.SetCookie(w, &http.Cookie{Name: flowCookie, Value: "", Path: basepath.CookiePath(r.Context()), HttpOnly: true, MaxAge: -1})
	}
	if f == nil {
		// ⚠⚠ Closed on a multi-tenant install: without its flow the callback
		// does not know which tenant the sign-in is for, and the set's first
		// OIDC (s.oidc, possibly a tenant's own) used to answer it - an
		// identity at one tenant's provider was signed in to an account of
		// another tenant, the platform administrator's among them
		// (2026-10-02 review, scenario 2).
		if s.multiTenant {
			return nil, "", errNoFlow
		}
		// A single-tenant install started its one OIDC without a flow: its
		// driver signs people in to the platform's own tenant, as always.
		if s.oidc == nil {
			return nil, "", ErrOIDCOff
		}
		return s.oidc.HandleCallback(w, r)
	}
	ctx := r.Context()
	var p *model.Provider
	if s.multiTenant && s.store != nil && f.tenant != 0 {
		p, _ = s.store.GetProvider(ctx, f.tenant)
	}
	choice, ok := pick(l.SSOChoices(ctx, p, f.tenant, multioidc.RequestHost(r)), f.key)
	if !ok || choice.Key != f.key {
		return nil, "", ErrNoSSOForTenant
	}
	flow := l.flowOf(s, f)
	u, token, err := choice.drv.HandleCallback(w, r.WithContext(authoidc.WithFlow(ctx, flow)))
	if err != nil {
		return nil, "", err
	}
	// The last word, here too: the driver looked the account up inside the
	// flow's tenant; whatever it returned, an account of another tenant does
	// not get the session. No reason code (the page answers it as any
	// failure); the log says why.
	owner := flow.Tenant
	if owner.Main && owner.ProviderID == 0 && s.store != nil {
		if st, serr := s.store.GetSupertenant(ctx); serr == nil && st != nil {
			owner.ProviderID = st.ID
		}
	}
	if !flowOwns(owner, u) {
		if s.store != nil && token != "" {
			_ = s.store.DeleteSession(ctx, token)
		}
		return nil, "", fmt.Errorf("oidc: account %d is not in tenant %d, the one the sign-in was started for", u.ID, f.tenant)
	}
	return u, token, nil
}

// flowOwns reports whether an account belongs to a flow's tenant (the rule of
// identity.Tenant.Owns: an account with no tenant is the platform's own).
func flowOwns(t authoidc.Tenant, u *model.User) bool {
	if u == nil {
		return false
	}
	if u.ProviderID == nil || *u.ProviderID == 0 {
		return t.Main
	}
	return t.ProviderID != 0 && *u.ProviderID == t.ProviderID
}
