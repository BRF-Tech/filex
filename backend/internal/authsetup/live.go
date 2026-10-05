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
	authldap "github.com/brf-tech/filex/backend/internal/auth/drivers/ldap"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/multioidc"
	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
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
	// LoginEmailToken is the installation's e-mail token (FILEX_OS_LOGIN_EMAIL_TOKEN):
	// what follows the `@` of an account a provider knows only by login name.
	// Environment only — never a page field: it is part of every such account's
	// address, and changing it later makes a second account of the same person
	// (identity.EmailToken).
	LoginEmailToken string
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
	// Name is the DRIVER (oidc, ldap, pam, windows, proxy-header, local).
	Name string
	// Slug names the instance (auth_instances.slug): the driver's name for
	// its first instance, so "the ldap" is still addressed as `ldap`.
	Slug string
	// InstanceID is the instance's row (auth_instances.id); 0 for `local`,
	// which is not an instance, and for a provider built before its row
	// existed. An entry with no row serves every tenant (Bindings.Bound).
	InstanceID int64
	// Label is what the sign-in page calls it ("" = the driver's words).
	Label string
	// Owner is the tenant a tenant's own instance belongs to (0 = none).
	Owner  int64
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
	// SetByUpgrade names the fields whose value the upgrade to 0.50 chose
	// for an environment provider (trust_email, UpgradeOIDCTrust), for the
	// page to say so.
	SetByUpgrade []string
	// config is what the environment gave the driver, secrets included — in
	// memory only, for "Test now" on an environment provider.
	config map[string]any
}

// NewEnvEntry records a provider the environment/config file defines. cfg is
// what Build was given; the page is shown a copy with every secret removed.
func NewEnvEntry(name, from string, cfg map[string]any, d auth.Driver, err error, directory bool) Entry {
	e := Entry{Name: Canonical(name), Slug: Canonical(name), Origin: OriginEnvironment, From: from, Driver: d, Directory: directory, config: cfg, Display: map[string]any{}}
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

	// Multi-tenant only (docs/TENANT-ADMIN.md): which tenant signs in through
	// which instance, and each tenant's own arrangement of the providers bound
	// to it, built on first use (a tenant made after the reload included).
	multiTenant bool
	binds       *Bindings
	// recoveryOn is Options.RecoveryLogin: the platform's own tenant gets the
	// bootstrap administrator's recovery sign-in, no other tenant does.
	recoveryOn bool
	store      db.Store
	viewMu     sync.Mutex
	views      map[int64]*View
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

// Entry returns a provider by its slug: the driver's name for the first
// instance of a driver, so `ldap` is still "the LDAP".
func (s *Set) Entry(name string) (Entry, bool) {
	name = Canonical(name)
	for _, e := range s.Entries {
		if e.Slug == name || (e.Slug == "" && e.Name == name) {
			return e, true
		}
	}
	return Entry{}, false
}

// Bindings is which tenant signs in through which instance, as the reload
// that made this set read it (nil on a set built before any reload).
func (s *Set) Bindings() *Bindings { return s.binds }

// MultiTenant reports whether the set arranges providers per tenant.
func (s *Set) MultiTenant() bool { return s.multiTenant }

// View is one tenant's arrangement of the providers bound to it: what a
// sign-in for that tenant is judged by.
type View struct {
	Tenant    int64
	login     auth.LoginDriver
	directory Directory
	recovery  bool
	// SSO are the running OIDC instances bound to the tenant, in the set's
	// order (the environment's first).
	SSO   []Entry
	names []string
}

// PasswordLogin reports whether a password sign-in for the tenant is
// answered at all.
func (v *View) PasswordLogin() bool { return v != nil && v.login != nil }

// Recovery reports whether the tenant has the recovery sign-in (the
// platform's own tenant only).
func (v *View) Recovery() bool { return v != nil && v.recovery }

// Names are the providers the tenant signs in through.
func (v *View) Names() []string {
	if v == nil {
		return nil
	}
	return append([]string(nil), v.names...)
}

// For is a tenant's view. On a single-tenant install every tenant's view is
// the whole set. 0 is the platform's own tenant.
func (s *Set) For(tenantID int64) *View {
	if !s.multiTenant || s.binds == nil {
		return s.wholeView()
	}
	if tenantID == 0 {
		tenantID = s.binds.Main
	}
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	if s.views == nil {
		s.views = map[int64]*View{}
	}
	if v := s.views[tenantID]; v != nil {
		return v
	}
	v := s.buildView(tenantID)
	s.views[tenantID] = v
	return v
}

// Serves reports whether an entry signs people in to a tenant (0: the
// platform's own): always on an install with one tenant; on a multi-tenant
// one when it is bound to that tenant and, a tenant's own instance, it is
// that tenant's - the rule buildView arranges a tenant's sign-in by.
func (s *Set) Serves(e Entry, tenantID int64) bool {
	if !s.multiTenant || s.binds == nil {
		return true
	}
	if tenantID == 0 {
		tenantID = s.binds.Main
	}
	return s.binds.Bound(e.InstanceID, tenantID) && (e.Owner == 0 || e.Owner == tenantID)
}

// wholeView is the single-tenant answer: the set itself.
func (s *Set) wholeView() *View {
	v := &View{login: s.login, directory: s.directory, recovery: s.recovery, names: s.Names()}
	for _, e := range s.Entries {
		if e.Driver != nil && e.Name == "oidc" {
			v.SSO = append(v.SSO, e)
		}
	}
	return v
}

// buildView arranges the providers bound to one tenant the way assemble
// arranges the whole set: the environment's `local` first wherever the
// environment put it, the tenant's directories after it, recovery last and
// for the platform's own tenant only. The caller holds viewMu.
func (s *Set) buildView(tenantID int64) *View {
	v := &View{Tenant: tenantID}
	var logins []auth.LoginDriver
	var dirs []Directory
	for _, e := range s.Entries {
		if !s.binds.Bound(e.InstanceID, tenantID) || (e.Owner != 0 && e.Owner != tenantID) {
			continue
		}
		if e.Origin == OriginEnvironment || e.Driver != nil {
			v.names = appendOnce(v.names, e.Name)
		}
		if e.Driver == nil {
			continue
		}
		switch e.Name {
		case "local", "ldap", "windows", "pam":
			if ld, ok := e.Driver.(auth.LoginDriver); ok {
				logins = append(logins, ld)
			}
		case "oidc":
			v.SSO = append(v.SSO, e)
		}
		if dd, ok := e.Driver.(Directory); ok && e.Directory {
			dirs = append(dirs, dd)
		}
	}
	v.directory = directoryOf(dirs)
	logins, v.recovery = WithRecoveryLogin(logins, s.recoveryOn && tenantID == s.binds.Main, s.store)
	switch len(logins) {
	case 0:
	case 1:
		v.login = logins[0]
	default:
		v.login = auth.NewLoginChain(logins...)
	}
	return v
}

func appendOnce(list []string, name string) []string {
	for _, n := range list {
		if n == name {
			return list
		}
	}
	return append(list, name)
}

// viewOf is the view a sign-in is judged by: the tenant its realm names
// (auth.LoginRealmFrom, resolved before any provider runs), else the
// platform's own. A single-tenant install has one view, the whole set.
func (s *Set) viewOf(ctx context.Context) *View {
	if !s.multiTenant {
		return s.wholeView()
	}
	if lr := auth.LoginRealmFrom(ctx); lr != nil && lr.Tenant != nil {
		return s.For(lr.Tenant.ID)
	}
	return s.For(0)
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

	// alarm tells the operator that an operating-system provider they switched
	// on cannot start (SetProviderAlarm); alarmed is the reason last told per
	// provider, so a retry loop does not repeat it.
	alarm   func(name, reason string)
	alarmed map[string]string

	// flows are the OIDC sign-ins in flight (oidcflow.go): which instance and
	// which tenant each was started for.
	flows *flowBook

	// Directory sync (dirsync.go): the LDAP instances whose sync is running.
	syncMu  sync.Mutex
	syncing map[string]bool
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
	l := &Live{opts: opts, env: env, apiTok: at, built: map[string]*built{}, alarmed: map[string]string{},
		flows: &flowBook{rows: multioidc.New(opts.Store, nil)}}
	// Until the first reload has read the bindings, nothing is bound: a
	// tenant signs in with filex's own passwords only, never through every
	// provider (Bindings.Bound answers true for `local` alone).
	l.swap(l.assemble(append([]Entry(nil), env...), nil))
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
//
// It also brings the providers' rows up to date (EnsureAnchors: a provider
// saved since the last reload gets its row, bound to the platform's own
// tenant) and reads which tenant signs in through which (ReadBindings).
func (l *Live) Reload(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := EnsureAnchors(ctx, l.opts.Store, l.env, l.opts.Log); err != nil {
		l.opts.Log.Warn("auth: the sign-in providers' rows could not be brought up to date", slog.Any("err", err))
	}
	stored, err := LoadStored(ctx, l.opts.Store)
	if err != nil {
		return err
	}
	rows, err := l.opts.Store.ListAuthInstances(ctx)
	if err != nil {
		l.opts.Log.Warn("auth: the sign-in provider rows could not be read; only the environment's and the page's first providers run", slog.Any("err", err))
		rows = nil
	}
	bySlug := map[string]*model.AuthInstance{}
	for _, r := range rows {
		bySlug[r.Slug] = r
	}
	binds, berr := ReadBindings(ctx, l.opts.Store)
	if berr != nil {
		// ⚠ Closed, not open: with no bindings a tenant signs in through
		// nothing but `local` (and the platform's own tenant through recovery),
		// never through every provider of every tenant.
		l.opts.Log.Warn("auth: which tenant signs in through which provider could not be read; tenants sign in with filex's own passwords only until it can", slog.Any("err", berr))
		binds = &Bindings{byInstance: map[int64]map[int64]bool{}, insecure: map[int64]bool{}}
	}
	anchor := func(e *Entry) {
		if r := bySlug[e.Slug]; r != nil {
			e.InstanceID, e.Label = r.ID, r.Label
		}
	}
	entries := make([]Entry, 0, len(l.env)+len(Managed))
	for _, e := range l.env {
		if e.Name != "local" {
			anchor(&e)
		}
		entries = append(entries, e)
	}
	keep := map[string]bool{}
	for _, name := range Managed {
		if _, envHas := l.EnvDefines(name); envHas {
			continue
		}
		s := stored[name]
		if s == nil || !s.Enabled {
			continue
		}
		e := l.buildOne(name, name, s, Entry{Name: name, Slug: name, Origin: OriginPage, From: "Admin → Identity providers"}, nil)
		anchor(&e)
		keep[name] = true
		entries = append(entries, e)
	}
	// Every further instance, and every tenant's own: configured on its row.
	for _, r := range rows {
		if r.Origin == model.AuthOriginEnvironment || PrimarySlug(r.Slug, r.Driver) || !r.Enabled {
			continue
		}
		e := Entry{Name: Canonical(r.Driver), Slug: r.Slug, InstanceID: r.ID, Label: r.Label, Origin: OriginPage, From: "Admin → Identity providers"}
		var tweak func(map[string]any)
		if r.Origin == model.AuthOriginTenant {
			e.Origin, e.From = OriginTenant, "tenant"
			if r.OwnerProviderID != nil {
				e.Owner = *r.OwnerProviderID
			}
			tweak = tenantOwned(e.Name, e.Owner, TenantGuard(binds.Insecure(e.Owner)))
		}
		st, serr := StoredOfRow(r)
		if serr != nil {
			e.Err = serr.Error()
			entries = append(entries, e)
			continue
		}
		e = l.buildOne(r.Slug, Canonical(r.Driver), st, e, tweak)
		keep[r.Slug] = true
		entries = append(entries, e)
	}
	for k := range l.built {
		if !keep[k] {
			delete(l.built, k)
		}
	}
	l.swap(l.assemble(entries, binds))
	l.raiseAlarms(entries)
	return nil
}

// tenantOwned is the configuration tweak of a tenant's own instance: the
// guard, and for an OIDC the tenant it is pinned to from the moment it is
// built (authoidc.TenantConfigKey) - it signs people in to its tenant and no
// other, whatever a callback carries (docs/SSO.md, "Which account an SSO
// sign-in opens").
func tenantOwned(driver string, owner int64, guard func(map[string]any)) func(map[string]any) {
	return func(cfg map[string]any) {
		guard(cfg)
		if driver == "oidc" && owner != 0 {
			cfg[authoidc.TenantConfigKey] = owner
		}
		// A tenant's own LDAP: its directory sync opens groups in its tenant
		// and reaches that tenant's accounts only (authldap.OwnerTenantKey).
		if driver == "ldap" && owner != 0 {
			cfg[authldap.OwnerTenantKey] = owner
		}
	}
}

// buildOne builds one page or row instance (cached while its configuration
// does not change). key is the cache key: the instance's slug. tweak (nil for
// none) adjusts the configuration the driver is handed: a tenant's own
// instance is guarded (TenantGuard).
func (l *Live) buildOne(key, driver string, s *Stored, e Entry, tweak func(map[string]any)) Entry {
	cfg, directory, err := DriverConfig(s, l.opts.Box, l.opts)
	if err != nil {
		e.Err = err.Error()
		l.built[key] = &built{entry: e}
		return e
	}
	if tweak != nil {
		tweak(cfg)
	}
	if driver == "ldap" {
		// Each LDAP instance knows its own slug: the accounts it makes are
		// its own (users.auth_directory), and so are its people's permanent
		// ids and its directory sync (docs/LDAP.md → Several directories).
		cfg["directory"] = key
	}
	e.Directory = directory
	fp := fingerprint(cfg)
	if b := l.built[key]; b != nil && b.fingerprint == fp && b.entry.Driver != nil {
		d := b.entry.Driver
		e.Driver = d
		return e
	}
	bctx, cancel := context.WithTimeout(context.Background(), l.opts.BuildTimeout)
	d, berr := Build(bctx, l.opts.Store, driver, cfg)
	cancel()
	if berr != nil {
		e.Err = berr.Error()
		// ⚠ The error, never the configuration: the map holds the opened
		// secret.
		l.opts.Log.Warn("auth: an identity provider from the admin page could not start; it stays off until it can",
			slog.String("provider", key), slog.String("reason", berr.Error()))
	} else {
		e.Driver = d
	}
	l.built[key] = &built{fingerprint: fp, entry: e}
	return e
}

// SetProviderAlarm registers what happens when an operating-system provider
// the admin page switched on cannot start: the provider is left out of the
// running set (every other way in keeps working) and the operator hears about
// it, once per distinct reason. A failure that happened before the alarm was
// registered (the boot, which builds providers before notifications exist) is
// raised now.
//
// The reason is the driver's error text, which never holds a secret (Build's
// errors are about the configuration and the machine, not the values).
func (l *Live) SetProviderAlarm(f func(name, reason string)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.alarm = f
	l.raiseAlarms(l.cur.Load().Entries)
}

// raiseAlarms fires the alarm for each page-enabled provider that needs a
// passing test to be on (auth.StrictProber, the operating-system ones) and is
// not running, once per reason; a provider that runs again is forgotten, so its
// next failure is told afresh. The caller holds l.mu.
func (l *Live) raiseAlarms(entries []Entry) {
	for _, e := range entries {
		if e.Origin != OriginPage {
			continue
		}
		if e.Driver != nil {
			delete(l.alarmed, e.Slug)
			continue
		}
		drv, err := auth.Get(e.Name)
		if err != nil {
			continue
		}
		if sp, ok := drv.(auth.StrictProber); !ok || !sp.StrictProbe() {
			continue
		}
		if l.alarm == nil || e.Err == "" || l.alarmed[e.Slug] == e.Err {
			continue
		}
		l.alarmed[e.Slug] = e.Err
		go l.alarm(e.Slug, e.Err)
	}
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
// contract. binds (nil before the first reload) is which tenant signs in
// through which; on a multi-tenant install each tenant's view is built from
// it on first use (Set.For).
func (l *Live) assemble(entries []Entry, binds *Bindings) *Set {
	s := &Set{Entries: entries, multiTenant: l.opts.MultiTenant, binds: binds, recoveryOn: l.opts.RecoveryLogin, store: l.opts.Store}
	if s.multiTenant && s.binds == nil {
		s.binds = &Bindings{byInstance: map[int64]map[int64]bool{}, insecure: map[int64]bool{}}
	}
	var logins []auth.LoginDriver
	var dirs []Directory
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
		case "local", "ldap", "windows", "pam":
			if ld, ok := e.Driver.(auth.LoginDriver); ok {
				logins = append(logins, ld)
			}
		}
		if od, ok := e.Driver.(auth.OIDCDriver); ok && e.Name == "oidc" && s.oidc == nil {
			s.oidc = od
		}
		if s.multiTenant && e.Name == "proxy-header" && len(s.enabled) > 0 {
			// A header proxy answers only on the addresses of the tenants it is
			// bound to (the platform's address: the platform's own tenant).
			s.enabled[len(s.enabled)-1] = &hostBound{Driver: e.Driver, instance: e.InstanceID, set: s}
		}
		if dd, ok := e.Driver.(Directory); ok && e.Directory {
			dirs = append(dirs, dd)
		}
	}
	s.directory = directoryOf(dirs)
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
	// OIDC is started by instance, for the tenant the address or the realm
	// names (oidcflow.go); a tenant's own OIDC on its provider row is one of
	// its choices (multioidc). A single-tenant install with one OIDC keeps
	// starting the one it has.
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

// Login judges a password with the providers of the tenant the sign-in is for
// (Set.viewOf): on a multi-tenant install, only the directories bound to that
// tenant are asked, so a person in the operator's directory gets no account in
// a tenant the directory does not serve.
func (p loginProxy) Login(ctx context.Context, identifier, password string) (*model.User, string, error) {
	v := p.l.cur.Load().viewOf(ctx)
	if v == nil || v.login == nil {
		return nil, "", ErrPasswordSignInOff
	}
	return v.login.Login(ctx, identifier, password)
}

func (p loginProxy) Logout(ctx context.Context, token string) error {
	if d := p.l.cur.Load().login; d != nil {
		return d.Logout(ctx, token)
	}
	return nil
}

type oidcProxy struct{ l *Live }

func (p oidcProxy) Available() bool { return p.l.cur.Load().oidc != nil }

// StartFlow starts an OIDC sign-in for the tenant the address or the realm
// names, through the instance `?instance=` names or the tenant's first
// (oidcflow.go).
func (p oidcProxy) StartFlow(w http.ResponseWriter, r *http.Request) error {
	return p.l.startFlow(w, r)
}

// HandleCallback finishes it, through the instance and for the tenant it was
// started with.
func (p oidcProxy) HandleCallback(w http.ResponseWriter, r *http.Request) (*model.User, string, error) {
	return p.l.handleCallback(w, r)
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
	// The file protocols resolve the sign-in's realm before they ask
	// (protocolauth.realmOf), so this is the tenant's directory chain.
	v := p.l.cur.Load().viewOf(ctx)
	if v == nil || v.directory == nil {
		// Same answer as "no directory configured": the resolver moves on.
		return nil, auth.ErrUnauthorized
	}
	return v.directory.VerifyPassword(ctx, identifier, password)
}

// hostBound is a header proxy on a multi-tenant install: it answers only for a
// request whose address belongs to a tenant it is bound to (the platform's
// address, or one no tenant claims: the platform's own tenant).
type hostBound struct {
	auth.Driver
	instance int64
	set      *Set
}

func (h *hostBound) Authenticate(r *http.Request) (*model.User, error) {
	if !h.serves(r) {
		return nil, auth.ErrUnauthorized
	}
	return h.Driver.Authenticate(r)
}

func (h *hostBound) serves(r *http.Request) bool {
	tid := h.set.binds.Main
	if host := multioidc.RequestHost(r); host != "" && h.set.store != nil {
		if p, err := h.set.store.GetProviderByHost(r.Context(), host); err == nil && p != nil && !p.IsSupertenant {
			tid = p.ID
		}
	}
	return h.set.binds.Bound(h.instance, tid)
}
