package authsetup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	authapitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/multioidc"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// Options is what Live needs from the server.
type Options struct {
	Store         db.Store
	Box           *secretbox.Box
	MultiTenant   bool
	RecoveryLogin bool
	PublicURL     string
	// BuildTimeout bounds one page provider's construction (an OIDC
	// discovery that hangs must not hold a save, or the boot, hostage).
	BuildTimeout time.Duration
	// RetryBackoff re-tries, after boot, a page provider that could not
	// start — its IdP may be booting alongside filex.
	RetryBackoff []time.Duration
	Log          *slog.Logger
}

// Entry is one provider the instance knows of.
type Entry struct {
	Name   string
	Origin string
	// From says where an environment provider is defined
	// (FILEX_AUTH_DRIVERS, the config file, the built-in default).
	From string
	// Driver is nil when the provider could not start; Err says why.
	Driver auth.Driver
	Err    string
	// Directory: an LDAP provider that also judges passwords on the file
	// protocols (WebDAV, SFTP, FTPS, S3, NFS).
	Directory bool
	// Display is the configuration shown on the page for an environment
	// provider — secrets left out (Secrets says which are set).
	Display map[string]any
	// Secrets names the secret fields an environment provider has set.
	Secrets []string
	// config is what the environment gave the driver, secrets included — in
	// memory only, for "Test now" on an environment provider.
	config map[string]any
}

// NewEnvEntry records a provider the environment/config file defines. cfg is
// what Build was given; the page is shown a copy with every secret removed.
func NewEnvEntry(name, from string, cfg map[string]any, d auth.Driver, err error, directory bool) Entry {
	e := Entry{Name: Canonical(name), Origin: OriginEnvironment, From: from, Driver: d, Directory: directory, config: cfg, Display: map[string]any{}}
	if err != nil {
		e.Err = err.Error()
		e.Driver = nil
	}
	for k, v := range cfg {
		if f, ok := FieldOf(e.Name, k); ok && f.Kind == FieldSecret {
			if s, _ := v.(string); s != "" {
				e.Secrets = append(e.Secrets, k)
			}
			continue
		}
		if _, ok := FieldOf(e.Name, k); ok {
			e.Display[k] = v
		}
	}
	return e
}

// Config is the configuration the environment gave a provider (secrets
// included; in memory only).
func (e Entry) Config() map[string]any { return e.config }

// Live reports whether the provider is running.
func (e Entry) Live() bool { return e.Driver != nil }

// Directory is an external password authority (protocolauth.Directory).
type Directory interface {
	VerifyPassword(ctx context.Context, identifier, password string) (*model.User, error)
}

// Set is one immutable arrangement of the providers. Live swaps whole sets,
// so a request never sees half of a reload.
type Set struct {
	Entries   []Entry
	enabled   []auth.Driver
	login     auth.LoginDriver
	oidc      auth.OIDCDriver
	directory Directory
	recovery  bool
	names     []string
}

// Names are the sign-in methods the login page offers: every provider the
// environment lists (as before) and every page provider that is running.
func (s *Set) Names() []string { return append([]string(nil), s.names...) }

// Recovery reports whether the bootstrap administrator's recovery sign-in is on.
func (s *Set) Recovery() bool { return s.recovery }

// PasswordLogin reports whether the password form is answered at all.
func (s *Set) PasswordLogin() bool { return s.login != nil }

// HasDirectory reports whether a directory judges passwords on the file
// protocols.
func (s *Set) HasDirectory() bool { return s.directory != nil }

// Entry returns a provider by name.
func (s *Set) Entry(name string) (Entry, bool) {
	name = Canonical(name)
	for _, e := range s.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Live owns the running providers and swaps them when the page saves.
type Live struct {
	opts   Options
	env    []Entry
	apiTok auth.Driver

	mu     sync.Mutex // one reload at a time
	built  map[string]*built
	cur    atomic.Pointer[Set]
	onSwap []func(*Set)
}

type built struct {
	fingerprint string
	entry       Entry
}

// New prepares a Live over the environment's providers (already built by the
// caller through Build) and installs a first set holding only those, so the
// password sign-in the environment configured works before anything the page
// saved has been looked at.
func New(ctx context.Context, opts Options, env []Entry) (*Live, error) {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.BuildTimeout <= 0 {
		opts.BuildTimeout = 15 * time.Second
	}
	at := authapitoken.New(opts.Store)
	if err := at.Init(ctx, nil); err != nil {
		return nil, err
	}
	l := &Live{opts: opts, env: env, apiTok: at, built: map[string]*built{}}
	l.swap(l.assemble(append([]Entry(nil), env...)))
	return l, nil
}

// Start imports what an earlier version saved (switched off), builds the
// page's enabled providers and, when one could not start, keeps re-trying it
// in the background.
func (l *Live) Start(ctx context.Context) {
	if err := UpgradeLegacy(ctx, l.opts.Store, l.opts.Box, l.opts.Log); err != nil {
		l.opts.Log.Warn("auth: identity provider settings could not be upgraded; the page's providers stay off", slog.Any("err", err))
		return
	}
	if err := l.Reload(ctx); err != nil {
		l.opts.Log.Warn("auth: identity providers from the admin page could not be loaded", slog.Any("err", err))
	}
	if len(l.opts.RetryBackoff) > 0 && l.pageFailing() {
		go l.retry(ctx)
	}
}

func (l *Live) pageFailing() bool {
	for _, e := range l.cur.Load().Entries {
		if e.Origin == OriginPage && e.Driver == nil {
			return true
		}
	}
	return false
}

func (l *Live) retry(ctx context.Context) {
	for _, d := range l.opts.RetryBackoff {
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
		if err := l.Reload(ctx); err != nil {
			l.opts.Log.Warn("auth: identity providers from the admin page could not be reloaded", slog.Any("err", err))
		}
		if !l.pageFailing() {
			return
		}
	}
}

// OnSwap registers a callback run with every new set (and once, now).
func (l *Live) OnSwap(f func(*Set)) {
	l.mu.Lock()
	l.onSwap = append(l.onSwap, f)
	l.mu.Unlock()
	f(l.cur.Load())
}

// Current is the running set.
func (l *Live) Current() *Set { return l.cur.Load() }

// Environment is the environment's providers, in the order it listed them.
func (l *Live) Environment() []Entry { return append([]Entry(nil), l.env...) }

// EnvDefines reports whether the environment lists a provider — in which case
// it is the effective one, and the page may not change it.
func (l *Live) EnvDefines(name string) (Entry, bool) {
	name = Canonical(name)
	for _, e := range l.env {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Options returns the options Live runs with.
func (l *Live) Options() Options { return l.opts }

// Reload reads the page's providers and swaps in the set they make.
//
// ⚠ A provider that did not change is not rebuilt (an OIDC discovery per
// save of an unrelated LDAP field would be a network round trip for
// nothing), and a provider that fails to start is left out with its reason —
// the environment's providers and every other page provider stay exactly as
// they were. There is no path through here that drops `local`.
func (l *Live) Reload(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	stored, err := LoadStored(ctx, l.opts.Store)
	if err != nil {
		return err
	}
	entries := append([]Entry(nil), l.env...)
	for _, name := range Managed {
		if _, envHas := l.EnvDefines(name); envHas {
			delete(l.built, name)
			continue
		}
		s := stored[name]
		if s == nil || !s.Enabled {
			delete(l.built, name)
			continue
		}
		cfg, directory, err := DriverConfig(s, l.opts.Box, l.opts)
		if err != nil {
			e := Entry{Name: name, Origin: OriginPage, From: "Admin → Identity providers", Err: err.Error()}
			l.built[name] = &built{entry: e}
			entries = append(entries, e)
			continue
		}
		fp := fingerprint(cfg)
		if b := l.built[name]; b != nil && b.fingerprint == fp && b.entry.Driver != nil {
			entries = append(entries, b.entry)
			continue
		}
		bctx, cancel := context.WithTimeout(context.Background(), l.opts.BuildTimeout)
		d, berr := Build(bctx, l.opts.Store, name, cfg)
		cancel()
		e := Entry{Name: name, Origin: OriginPage, From: "Admin → Identity providers", Directory: directory}
		if berr != nil {
			e.Err = berr.Error()
			// ⚠ The error, never the configuration: the map holds the opened
			// secret.
			l.opts.Log.Warn("auth: an identity provider from the admin page could not start; it stays off until it can",
				slog.String("provider", name), slog.String("reason", berr.Error()))
		} else {
			e.Driver = d
		}
		l.built[name] = &built{fingerprint: fp, entry: e}
		entries = append(entries, e)
	}
	l.swap(l.assemble(entries))
	return nil
}

// fingerprint identifies a configuration without keeping it: a hash of the
// opened map, in memory only.
func fingerprint(cfg map[string]any) string {
	b, _ := json.Marshal(cfg)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// assemble arranges entries into a set: the environment's first, in its
// order, then the page's. See the package comment for why the order is the
// contract.
func (l *Live) assemble(entries []Entry) *Set {
	s := &Set{Entries: entries}
	var logins []auth.LoginDriver
	for _, e := range entries {
		if e.Origin == OriginEnvironment {
			s.names = append(s.names, e.Name)
		} else if e.Driver != nil {
			s.names = append(s.names, e.Name)
		}
		if e.Driver == nil {
			continue
		}
		s.enabled = append(s.enabled, e.Driver)
		switch e.Name {
		case "local", "ldap":
			if ld, ok := e.Driver.(auth.LoginDriver); ok {
				logins = append(logins, ld)
			}
		}
		if od, ok := e.Driver.(auth.OIDCDriver); ok && e.Name == "oidc" && s.oidc == nil {
			s.oidc = od
		}
		if dd, ok := e.Driver.(Directory); ok && e.Directory && s.directory == nil {
			s.directory = dd
		}
	}
	s.enabled = WithSessionAuthenticator(s.enabled, l.opts.Store)
	s.enabled = append(s.enabled, l.apiTok)
	logins, s.recovery = WithRecoveryLogin(logins, l.opts.RecoveryLogin, l.opts.Store)
	switch len(logins) {
	case 0:
	case 1:
		s.login = logins[0]
	default:
		s.login = auth.NewLoginChain(logins...)
	}
	// Multi-tenant mode dispatches OIDC per tenant realm; hosts with no
	// tenant configuration fall back to the instance's provider.
	if l.opts.MultiTenant {
		s.oidc = multioidc.New(l.opts.Store, s.oidc)
	}
	return s
}

func (l *Live) swap(s *Set) {
	l.cur.Store(s)
	auth.SetEnabled(s.enabled)
	for _, f := range l.onSwap {
		f(s)
	}
}

// ErrPasswordSignInOff answers a password sign-in when no driver takes one.
var ErrPasswordSignInOff = errors.New("password sign-in is off")

// ErrOIDCOff answers an OIDC flow when no OIDC provider is running.
var ErrOIDCOff = errors.New("OIDC is not configured")

// Login is the password sign-in the handlers hold. It always asks the
// running set, so a reload changes what it does without re-wiring anything.
func (l *Live) Login() auth.LoginDriver { return loginProxy{l} }

// OIDC is the OIDC flow the handlers hold.
func (l *Live) OIDC() auth.OIDCDriver { return oidcProxy{l} }

// LoginName names the password chain, for the boot line.
func (l *Live) LoginName() string { return loginProxy{l}.Name() }

// Dir is the directory the file protocols consult.
func (l *Live) Dir() Directory { return dirProxy{l} }

type loginProxy struct{ l *Live }

func (p loginProxy) Available() bool { return p.l.cur.Load().login != nil }

func (p loginProxy) Name() string {
	if d := p.l.cur.Load().login; d != nil {
		if n, ok := d.(interface{ Name() string }); ok {
			return n.Name()
		}
	}
	return "none"
}

func (p loginProxy) Login(ctx context.Context, identifier, password string) (*model.User, string, error) {
	d := p.l.cur.Load().login
	if d == nil {
		return nil, "", ErrPasswordSignInOff
	}
	return d.Login(ctx, identifier, password)
}

func (p loginProxy) Logout(ctx context.Context, token string) error {
	if d := p.l.cur.Load().login; d != nil {
		return d.Logout(ctx, token)
	}
	return nil
}

type oidcProxy struct{ l *Live }

func (p oidcProxy) Available() bool { return p.l.cur.Load().oidc != nil }

func (p oidcProxy) StartFlow(w http.ResponseWriter, r *http.Request) error {
	d := p.l.cur.Load().oidc
	if d == nil {
		return ErrOIDCOff
	}
	return d.StartFlow(w, r)
}

func (p oidcProxy) HandleCallback(w http.ResponseWriter, r *http.Request) (*model.User, string, error) {
	d := p.l.cur.Load().oidc
	if d == nil {
		return nil, "", ErrOIDCOff
	}
	return d.HandleCallback(w, r)
}

// EndSessionURL is RP-initiated logout (auth.OIDCLogoutDriver, PR #40) on the
// RUNNING provider — the one the page runs now, not the one that was running
// when the handlers were wired.
//
// ⚠ Without it sign-out stayed local on every real server: the handlers hold
// this proxy, not a driver, and a type assertion on the proxy found no
// EndSessionURL, so in SSO-first mode "Sign out" signed the same account
// straight back in — the report PR #40 fixed, silently undone by the wiring.
// "" (local sign-out) when no OIDC provider runs or it cannot end sessions; a
// session signed in through a provider that has since been changed carries a
// token of another issuer, and the driver refuses to hand that to the new one.
func (p oidcProxy) EndSessionURL(r *http.Request, idToken, postLogoutRedirect string) string {
	lo, ok := p.l.cur.Load().oidc.(auth.OIDCLogoutDriver)
	if !ok {
		return ""
	}
	return lo.EndSessionURL(r, idToken, postLogoutRedirect)
}

var _ auth.OIDCLogoutDriver = oidcProxy{}

type dirProxy struct{ l *Live }

func (p dirProxy) VerifyPassword(ctx context.Context, identifier, password string) (*model.User, error) {
	d := p.l.cur.Load().directory
	if d == nil {
		// Same answer as "no directory configured": the resolver moves on.
		return nil, auth.ErrUnauthorized
	}
	return d.VerifyPassword(ctx, identifier, password)
}
