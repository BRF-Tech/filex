// Package handlers — auth_providers.go
//
// Admin → Identity providers: the sign-in providers this instance runs.
//
//	GET    /api/admin/auth-providers
//	POST   /api/admin/auth-providers                  (another instance of a driver)
//	PATCH  /api/admin/auth-providers/{name}
//	DELETE /api/admin/auth-providers/{name}           (an instance made with POST)
//	PUT    /api/admin/auth-providers/{name}/tenants   (which tenants sign in through it)
//	POST   /api/admin/auth-providers/{name}/test
//
// {name} is the provider's slug: the driver's name for its first instance
// (`ldap`, `oidc`, ...), so every client that addressed "the LDAP" still does
// (docs/TENANT-ADMIN.md).
//
// ⚠⚠ Until v0.43.0 this page wrote `auth.<name>.*` settings that NOTHING
// read — sign-in was built from the environment only, once, at boot — and
// answered "configuration saved; restart the server" to a restart that
// changed nothing (release-candidate sweep, 2026-09-21). The owner's
// decision: the page really manages sign-in, and it can never lock the
// instance out. The rules live in internal/authsetup; this file enforces
// them at the door:
//
//   - a provider the environment defines is read-only here, and says where it
//     comes from (409 environment_managed on a change);
//   - password sign-in and the recovery sign-in are the environment's — the
//     page has no switch for either (409 not_managed_here);
//   - a save runs the real test first; switching a provider ON while its test
//     fails needs `confirm_failed_test` (409 test_failed names the steps) -
//     except an operating-system provider (windows), whose only proof is a real
//     sign-in: its test must PASS, with `test_account` {username, password} sent
//     on that request (used once, never stored or logged), and the account that
//     passed becomes the super administrator (409 test_failed, no way round);
//   - switching off the last way an administrator can sign in is refused
//     (409 last_sign_in_method); on a multi-tenant install a tenant's last way
//     in is refused too unless the operator confirms it
//     (`confirm_tenant_lockout`): the operator can always bind something back;
//   - a secret is sealed (FILEX_SECRET_KEY) and never sent back;
//   - a change is applied at once (authsetup.Live.Reload) and audited with
//     the provider, the switch and the NAMES of the fields that changed.
//
// Instance-wide: only the platform operator (supertenant admin) sees or
// changes any of it. A tenant administrator gets 403 — the sign-in of the
// whole instance, and its secrets, are not a tenant's to read; a tenant's own
// sign-in lives on its provider row (/api/admin/providers).
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// authProvidersAreInstanceWide is the refusal a tenant admin reads.
const authProvidersAreInstanceWide = "authentication drivers apply to the whole instance and are managed by the platform operator"

// AuthProviders handles /api/admin/auth-providers.
type AuthProviders struct {
	Store db.Store
	// Live is the running set of providers; nil on a harness that has none,
	// where nothing can be changed.
	Live *authsetup.Live
	// DemoMode marks a public playground: nothing about sign-in changes there.
	DemoMode bool
}

// NewAuthProviders constructs the handler.
func NewAuthProviders(store db.Store, live *authsetup.Live) *AuthProviders {
	return &AuthProviders{Store: store, Live: live}
}

func (h *AuthProviders) multiTenant() bool {
	return h.Live != nil && h.Live.Options().MultiTenant
}

// providerView is one provider on the page.
type providerView struct {
	// Name is the provider's slug: the driver's name for its first instance.
	Name string `json:"name"`
	// Driver is what it is (oidc, ldap, pam, windows, proxy-header, local,
	// api-token).
	Driver       string            `json:"driver"`
	Capabilities auth.Capabilities `json:"capabilities"`
	// InstanceID is its row (auth_instances); 0 for `local` and api-token,
	// which are not instances.
	InstanceID int64 `json:"instance_id,omitempty"`
	// Label is what the sign-in page calls it ("" = the page's own words).
	Label string `json:"label,omitempty"`
	// OwnerProviderID is the tenant a tenant's own instance belongs to.
	OwnerProviderID *int64 `json:"owner_provider_id,omitempty"`
	// Tenants are the tenants that sign in through it (multi-tenant installs;
	// absent elsewhere, where every provider serves every sign-in).
	Tenants []int64 `json:"tenants,omitempty"`
	// Managed: the page may change it (a kind the page manages, not defined
	// by the environment).
	Managed bool `json:"managed"`
	// Origin: environment | page | tenant | builtin.
	Origin string `json:"origin"`
	From   string `json:"from,omitempty"`
	// Enabled: switched on (by the environment or on the page).
	Enabled bool `json:"enabled"`
	// State: running | failed | off.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// Legacy: saved on this page before v0.43.0 and never applied; imported
	// switched off for the operator to review.
	Legacy bool `json:"legacy,omitempty"`
	// Shadowed: the page holds a configuration under this name, but the
	// environment defines the provider, so the environment's is the one used.
	Shadowed bool `json:"shadowed,omitempty"`
	// ConfigRedacted is every non-secret value; a secret is never included.
	ConfigRedacted map[string]any `json:"config_redacted"`
	// SecretsSet names the secret fields that hold a value ("set — replace?").
	SecretsSet map[string]bool   `json:"secrets_set"`
	Fields     []authsetup.Field `json:"fields,omitempty"`
	Testable   bool              `json:"testable"`
	// TestAccountRequired: the test signs a real account in, so the test request
	// carries `test_account` {username, password} - and a failing test can never
	// be confirmed away (operating-system providers).
	TestAccountRequired bool `json:"test_account_required,omitempty"`
	// SetByUpgrade names the fields whose value the upgrade to 0.50 chose and
	// nobody has saved since: `trust_email` of an OIDC that existed before it
	// (authsetup.UpgradeOIDCTrust). The page says so beside the field.
	SetByUpgrade []string `json:"set_by_upgrade,omitempty"`
}

// tenantRef is a tenant as the binding control lists it.
type tenantRef struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Realm         string `json:"realm"`
	IsSupertenant bool   `json:"is_supertenant"`
}

// List returns every sign-in provider and how it stands.
func (h *AuthProviders) List(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, authProvidersAreInstanceWide) {
		return
	}
	out, err := h.views(r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "settings: " + err.Error()})
		return
	}
	body := map[string]any{"providers": out, "multi_tenant": h.multiTenant()}
	if h.Live != nil {
		set := h.Live.Current()
		body["password_sign_in"] = set.PasswordLogin()
		body["recovery_login"] = set.Recovery()
		box := h.Live.Options().Box
		body["secret_key"] = box != nil && box.Enabled()
	}
	if h.multiTenant() && h.Store != nil {
		// The bindings the upgrade made (every provider to every tenant) wait
		// for the operator's review until they dismiss the notice
		// (PUT /api/admin/settings/auth.instances.review_pending).
		v, _ := h.Store.GetSetting(r.Context(), authsetup.ReviewSetting)
		body["review_pending"] = v == "1"
		tenants := []tenantRef{}
		if ps, err := h.Store.ListProviders(r.Context()); err == nil {
			for _, p := range ps {
				tenants = append(tenants, tenantRef{ID: p.ID, Name: p.Name, Realm: p.LoginRealm(), IsSupertenant: p.IsSupertenant})
			}
		}
		body["tenants"] = tenants
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *AuthProviders) views(r *http.Request) ([]providerView, error) {
	ctx := r.Context()
	names := auth.Names()
	sort.Strings(names)
	var stored map[string]*authsetup.Stored
	var rows []*model.AuthInstance
	if h.Store != nil {
		var err error
		if stored, err = authsetup.LoadStored(ctx, h.Store); err != nil {
			return nil, err
		}
		if rows, err = h.Store.ListAuthInstances(ctx); err != nil {
			return nil, err
		}
	}
	bySlug := map[string]*model.AuthInstance{}
	for _, row := range rows {
		bySlug[row.Slug] = row
	}
	var set *authsetup.Set
	if h.Live != nil {
		set = h.Live.Current()
	}
	upgraded := authsetup.ReadTrustUpgrade(ctx, h.Store)
	// trustByUpgrade: the provider's trust_email is on and is the upgrade's.
	trustByUpgrade := func(v *providerView) {
		if on, _ := v.ConfigRedacted[authoidc.TrustEmailKey].(bool); on && v.Driver == "oidc" && upgraded.Instance(v.Name) {
			v.SetByUpgrade = []string{authoidc.TrustEmailKey}
		}
	}
	out := make([]providerView, 0, len(names)+len(rows))
	for _, n := range names {
		v := providerView{Name: n, Driver: n, State: "off", ConfigRedacted: map[string]any{}, SecretsSet: map[string]bool{}}
		h.driverTraits(&v, n)
		env, envHas := h.envEntry(n)
		switch {
		case n == "api-token":
			v.Origin, v.Enabled, v.State = "builtin", true, "running"
		case envHas:
			v.Origin, v.From, v.Enabled = authsetup.OriginEnvironment, authsetup.FromWords(env.From, langOf(r)), true
			v.State = stateOf(set, n)
			if e, ok := set.Entry(n); ok {
				v.Error = e.Err
			}
			for k, val := range env.Display {
				v.ConfigRedacted[k] = val
			}
			for _, k := range env.Secrets {
				v.SecretsSet[k] = true
			}
			v.SetByUpgrade = env.SetByUpgrade
			// The fields too — read-only here — so the page shows the
			// environment's settings in the same order and sections as a
			// page provider's, with what an unset one means.
			v.Fields = authsetup.Schema[authsetup.Canonical(n)]
			if s := stored[n]; s != nil && s.Exists {
				v.Shadowed = true
			}
		case !authsetup.IsManaged(n):
			// `local` when the environment does not list it: password sign-in
			// is off, and only the environment can turn it on.
			v.Origin, v.From = authsetup.OriginEnvironment, "FILEX_AUTH_DRIVERS"
		default:
			v.Origin, v.Managed = authsetup.OriginPage, h.Live != nil
			v.Fields = authsetup.Schema[n]
			if s := stored[n]; s != nil {
				v.Enabled, v.Legacy = s.Enabled, s.Legacy
				redact(&v, s)
			}
			trustByUpgrade(&v)
			if v.Enabled {
				v.State = stateOf(set, n)
				if e, ok := set.Entry(n); ok {
					v.Error = e.Err
				}
			}
		}
		h.instanceTraits(&v, bySlug[n], set)
		out = append(out, v)
	}
	// Every further instance, and every tenant's own: configured on its row.
	for _, row := range rows {
		if row.Origin == model.AuthOriginEnvironment || authsetup.PrimarySlug(row.Slug, row.Driver) {
			continue
		}
		// A tenant's own provider belongs to a tenant, and with multi-tenant
		// mode off nothing about tenants is shown (internal/tenancy): its
		// row stays, for when the mode is back on, and the page does not list
		// it. Its people cannot sign in meanwhile (maintenance mode).
		if row.Origin == model.AuthOriginTenant && !h.multiTenant() {
			continue
		}
		d := authsetup.Canonical(row.Driver)
		v := providerView{Name: row.Slug, Driver: d, State: "off", ConfigRedacted: map[string]any{}, SecretsSet: map[string]bool{},
			Origin: authsetup.OriginPage, Managed: h.Live != nil, Fields: authsetup.Schema[d], Enabled: row.Enabled, Legacy: row.Legacy}
		if row.Origin == model.AuthOriginTenant {
			// A tenant's own: the fields a tenant may set, never a file on
			// the server (the guard drops `ca_file` on every save anyway).
			v.Origin = authsetup.OriginTenant
			v.Fields = authsetup.TenantFields(d)
		}
		h.driverTraits(&v, d)
		if s, err := authsetup.StoredOfRow(row); err == nil {
			redact(&v, s)
		}
		if row.Origin == model.AuthOriginTenant {
			delete(v.ConfigRedacted, "ca_file")
		}
		trustByUpgrade(&v)
		if v.Enabled {
			v.State = stateOf(set, row.Slug)
			if e, ok := set.Entry(row.Slug); ok {
				v.Error = e.Err
			}
		}
		h.instanceTraits(&v, row, set)
		out = append(out, v)
	}
	return out, nil
}

func (h *AuthProviders) driverTraits(v *providerView, driver string) {
	if drv, err := auth.Get(driver); err == nil {
		v.Capabilities = drv.Capabilities()
		_, v.Testable = drv.(auth.Prober)
		if sp, ok := drv.(auth.StrictProber); ok && sp.StrictProbe() {
			v.TestAccountRequired = true
		}
	}
}

// instanceTraits adds what the row says: its id, label, owner and, on a
// multi-tenant install, the tenants bound to it.
func (h *AuthProviders) instanceTraits(v *providerView, row *model.AuthInstance, set *authsetup.Set) {
	if row == nil {
		return
	}
	v.InstanceID, v.Label = row.ID, row.Label
	if !h.multiTenant() {
		// No owner and no bindings: a single-tenant answer names no tenant.
		return
	}
	v.OwnerProviderID = row.OwnerProviderID
	if set != nil {
		v.Tenants = set.Bindings().TenantsOf(row.ID)
		if v.Tenants == nil {
			v.Tenants = []int64{}
		}
	}
}

// redact copies a stored configuration onto a view: every value but a
// secret, and which secrets are set.
func redact(v *providerView, s *authsetup.Stored) {
	for k, val := range s.Values {
		f, _ := authsetup.FieldOf(s.Name, k)
		switch f.Kind {
		case authsetup.FieldSecret:
			if val != "" {
				v.SecretsSet[k] = true
			}
		case authsetup.FieldBool:
			v.ConfigRedacted[k] = val == "true"
		default:
			v.ConfigRedacted[k] = val
		}
	}
}

func stateOf(set *authsetup.Set, name string) string {
	if set == nil {
		return "off"
	}
	e, ok := set.Entry(name)
	switch {
	case !ok:
		return "off"
	case e.Driver != nil:
		return "running"
	default:
		return "failed"
	}
}

// updateBody is a save. `config` holds the fields; the flat form (fields at
// the top level beside `enabled`) is what the page and the admin MCP tool
// sent before, and is still read.
type updateBody struct {
	Enabled              *bool
	Label                *string
	Config               map[string]any
	ConfirmFailedTest    bool
	ConfirmTenantLockout bool
	// TestRealm is the realm the operating-system test account signs in in
	// (`test_account.realm`): "" = the platform's own tenant, or the one tenant
	// the instance serves.
	TestRealm string
}

// providerTarget is the instance a save or a test addresses: the driver's
// first instance (its settings rows) or a row instance.
type providerTarget struct {
	slug   string
	driver string
	// row is the instance's row; for a first instance it may not exist yet
	// (made on the next reload, authsetup.EnsureAnchors).
	row *model.AuthInstance
	cur *authsetup.Stored
	// guard adjusts the configuration a test is run on, as Reload adjusts
	// what the driver is handed: a tenant's own instance is guarded
	// (authsetup.TenantGuard). Nil for none.
	guard func(map[string]any)
}

// guardOf is the guard a row instance is tested and built with: a tenant's
// own is held to authsetup.TenantGuard, as its tenant allows.
func (h *AuthProviders) guardOf(ctx context.Context, row *model.AuthInstance) func(map[string]any) {
	if row == nil || row.Origin != model.AuthOriginTenant || row.OwnerProviderID == nil {
		return nil
	}
	insecure := false
	if p, err := h.Store.GetProvider(ctx, *row.OwnerProviderID); err == nil && p != nil {
		insecure = p.AllowInsecureAuth
	}
	return authsetup.TenantGuard(insecure)
}

var errNoSuchProvider = errors.New("no such sign-in provider")

// target finds what {name} addresses. A first instance (slug == a managed
// driver's name) reads its settings rows; any other slug its row.
func (h *AuthProviders) target(ctx context.Context, name string) (*providerTarget, error) {
	row, err := h.Store.GetAuthInstanceBySlug(ctx, name)
	if err != nil {
		return nil, err
	}
	if authsetup.IsManaged(name) {
		stored, err := authsetup.LoadStored(ctx, h.Store)
		if err != nil {
			return nil, err
		}
		return &providerTarget{slug: name, driver: name, row: row, cur: stored[name]}, nil
	}
	if row == nil || row.Origin == model.AuthOriginEnvironment {
		return nil, errNoSuchProvider
	}
	cur, err := authsetup.StoredOfRow(row)
	if err != nil {
		return nil, err
	}
	return &providerTarget{slug: row.Slug, driver: cur.Name, row: row, cur: cur, guard: h.guardOf(ctx, row)}, nil
}

// save writes a target's next state: the settings rows of a first instance,
// the row of any other.
func (h *AuthProviders) save(ctx context.Context, t *providerTarget, next *authsetup.Stored, label *string) error {
	if t.row == nil || authsetup.PrimarySlug(t.slug, t.driver) {
		if err := authsetup.Save(ctx, h.Store, next); err != nil {
			return err
		}
		if t.row != nil && label != nil {
			t.row.Label = strings.TrimSpace(*label)
			return h.Store.UpdateAuthInstance(ctx, t.row)
		}
		return nil
	}
	t.row.ConfigJSON = authsetup.RowConfigJSON(next)
	t.row.Enabled = next.Enabled
	t.row.Legacy = false
	if label != nil {
		t.row.Label = strings.TrimSpace(*label)
	}
	return h.Store.UpdateAuthInstance(ctx, t.row)
}

// Update saves one provider, applies it at once, and audits it.
func (h *AuthProviders) Update(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, authProvidersAreInstanceWide) {
		return
	}
	name := authsetup.Canonical(chi.URLParam(r, "name"))
	raw := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	body := parseUpdate(raw)
	// ⚠ The test account (an operating-system provider's last test step) lives
	// on THIS request's context only: never stored, logged or audited but by
	// name.
	ctx := auth.WithProbeLang(r.Context(), langOf(r))
	ta, _ := auth.ParseTestAccount(raw)
	ctx = auth.WithTestAccount(ctx, ta)

	if !h.writable(w, r, name) {
		return
	}
	t, err := h.target(r.Context(), name)
	if errors.Is(err, errNoSuchProvider) {
		writeProviderRefusal(w, r, http.StatusConflict, "not_managed_here", nil, "server.auth_provider.not_managed_here", nil)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "settings: " + err.Error()})
		return
	}
	h.apply(w, r, ctx, t, body, ta, false)
}

// writable answers the refusals every change shares: a demo, no running set,
// a provider the environment defines.
func (h *AuthProviders) writable(w http.ResponseWriter, r *http.Request, name string) bool {
	if h.DemoMode {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "demo_read_only"})
		return false
	}
	if h.Live == nil {
		writeProviderRefusal(w, r, http.StatusServiceUnavailable, "not_managed", nil, "server.auth_provider.not_managed", nil)
		return false
	}
	if env, ok := h.Live.EnvDefines(name); ok {
		from := authsetup.FromWords(env.From, langOf(r))
		writeProviderRefusal(w, r, http.StatusConflict, "environment_managed", map[string]any{"from": from},
			"server.auth_provider.environment_managed", srvtext.Vars{"name": name, "from": from})
		return false
	}
	return true
}

// apply is the body of a save (Update, and Create once its row exists): the
// real test on exactly what is about to be saved, the refusals, the save, the
// reload, the promotion of an operating-system test account, the audit
// details and the answer.
func (h *AuthProviders) apply(w http.ResponseWriter, r *http.Request, ctx context.Context, t *providerTarget, body updateBody, ta *auth.TestAccount, created bool) {
	opts := h.Live.Options()
	next, changed, err := authsetup.Merge(t.cur, authsetup.Change{Enabled: body.Enabled, Values: body.Config}, opts.Box)
	if errors.Is(err, authsetup.ErrSecretKeyRequired) {
		writeProviderRefusal(w, r, http.StatusBadRequest, "secret_key_required", nil, "server.auth_provider.secret_key_required", nil)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Where an operating-system test account signs in, and whether it is the
	// first switch-on there (only that one promotes).
	scope, scopeErr := h.testScope(r.Context(), t, body.TestRealm)
	if scopeErr != "" {
		writeProviderRefusal(w, r, http.StatusConflict, scopeErr, nil, "server.auth_provider."+scopeErr, nil)
		return
	}
	if scope.tenant != nil && !scope.tenant.IsSupertenant {
		// The test signs the account in in the tenant's realm, so the address
		// it is given is the tenant's (alex@acme.local).
		ctx = auth.WithLoginRealm(ctx, &auth.LoginRealm{Tenant: scope.tenant, Named: true, Token: opts.LoginEmailToken})
	}

	// The real test, on exactly what is about to be saved.
	checks := []auth.ProbeCheck{}
	var probeCfg map[string]any
	strict := false
	var drv auth.Driver
	if cfg, _, cerr := authsetup.DriverConfig(next, opts.Box, opts); cerr != nil {
		checks = append(checks, auth.Check("secret", auth.ProbeFail))
	} else if d, gerr := auth.Get(t.driver); gerr == nil {
		if t.guard != nil {
			t.guard(cfg)
		}
		drv, probeCfg = d, cfg
		if sp, ok := d.(auth.StrictProber); ok {
			strict = sp.StrictProbe()
		}
		if p, ok := d.(auth.Prober); ok {
			if got := p.Probe(ctx, cfg, r); got != nil {
				checks = got
			}
		}
	}
	testOK := auth.ProbeOKAll(checks)
	var failed []string
	for _, c := range checks {
		if c.Status == auth.ProbeFail {
			failed = append(failed, c.ID)
		}
	}

	if next.Enabled && !testOK && (!body.ConfirmFailedTest || strict) {
		// Nothing is saved. The page asks again, naming the steps that failed;
		// a DISABLED provider saves with a failing test (it runs nothing).
		//
		// An operating-system provider (strict) is never switched on over a
		// failing test - there is no "switch on anyway" for it, so a
		// confirm_failed_test in the request changes nothing.
		key := "server.auth_provider.confirm_failed_test"
		if strict {
			key = "server.auth_provider.test_required"
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":           "test_failed",
			"message":         srvtext.Text(langOf(r), key, nil),
			"checks":          checks,
			"failed":          failed,
			"confirm_allowed": !strict,
			"strict":          strict,
		})
		return
	}
	// The account that proved the provider works becomes an administrator of
	// the scope it signed in in - the platform's super administrator, or the
	// tenant's administrator - the one deliberate exception to "auto_create is
	// off", so whoever sets it up is never locked out. Only the FIRST switch-on
	// in a scope promotes (the owner's decision, 2026-10-01): a later test is a
	// test, not a way to make administrators.
	//
	// ORDER (decided when the windows and pam providers were merged): the
	// promotion cannot be undone cleanly (an account created, a role raised, an
	// audit row written), so it is given AFTER the provider is saved and running
	// - never before, which could leave an administrator behind for a provider
	// that failed to save. What CAN be decided beforehand is checked before
	// anything is saved (a disabled account, or one that belongs to another
	// tenant, is not promoted and nothing is saved either). A promotion that
	// then fails is answered with a 500; the provider is saved, and the same
	// request may be repeated.
	granter, isGranter := drv.(auth.TestAccountGranter)
	firstHere := t.row == nil || !t.row.PromotedIn(scope.key)
	promote := next.Enabled && testOK && ta != nil && firstHere && (isGranter || (strict && ta.Email != ""))
	if promote && ta.Email != "" {
		if gerr := auth.CheckAdminCandidate(r.Context(), h.Store, ta.Email, scope.tenant); gerr != nil {
			key, code := "server.auth_provider.test_account_disabled", "test_account_disabled"
			switch {
			case errors.Is(gerr, auth.ErrNotSupertenant):
				key, code = "server.auth_provider.test_account_not_platform", "test_account_not_platform"
			case errors.Is(gerr, auth.ErrOtherTenant):
				key, code = "server.auth_provider.test_account_other_tenant", "test_account_other_tenant"
			}
			writeProviderRefusal(w, r, http.StatusConflict, code, nil, key, nil)
			return
		}
	}
	if t.cur.Enabled && !next.Enabled && stateOf(h.Live.Current(), t.slug) == "running" {
		if code, msgKey, vars := h.lockout(r.Context(), t.slug, t.row, nil, body.ConfirmTenantLockout); code != "" {
			writeProviderRefusal(w, r, http.StatusConflict, code, nil, msgKey, vars)
			return
		}
	}

	if err := h.save(r.Context(), t, next, body.Label); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "settings: " + err.Error()})
		return
	}
	// Saved with its trust setting in hand: the value is the operator's now,
	// not the upgrade's (authsetup.UpgradeOIDCTrust).
	if _, sent := body.Config[authoidc.TrustEmailKey]; sent {
		authsetup.ForgetTrustUpgradeInstance(r.Context(), h.Store, t.slug)
	}
	if err := h.Live.Reload(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload: " + err.Error()})
		return
	}

	superAdmin := false
	if promote {
		changedRole := true
		if scope.tenant != nil && !scope.tenant.IsSupertenant {
			var gerr error
			if changedRole, gerr = auth.GrantTenantAdmin(r.Context(), h.Store, ta.Email, scope.tenant); gerr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "tenant_admin: " + gerr.Error()})
				return
			}
			auth.AddAuditDetail(r.Context(), "tenant_admin_tenant", scope.tenant.ID)
		} else if isGranter {
			if gerr := granter.GrantTestAccount(r.Context(), h.Store, probeCfg, ta.Username); gerr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "super_admin: " + gerr.Error()})
				return
			}
			auth.AddAuditDetail(r.Context(), "test_account_user", ta.Username)
			superAdmin = true
		} else {
			var gerr error
			if changedRole, gerr = auth.GrantSuperAdmin(r.Context(), h.Store, ta.Email); gerr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "super_admin: " + gerr.Error()})
				return
			}
			superAdmin = true
		}
		h.notePromoted(r.Context(), t.slug, scope.key)
		acct := ta.Email
		if acct == "" {
			acct = ta.Username
		}
		auth.AddAuditDetail(r.Context(), "super_admin_account", acct)
		auth.AddAuditDetail(r.Context(), "super_admin_changed", changedRole)
		auth.AddAuditDetail(r.Context(), "promoted_scope", scope.key)
	}

	// ⚠ Names and flags only — never a value (auth.AuditDetail).
	auth.AddAuditDetail(r.Context(), "provider", t.slug)
	auth.AddAuditDetail(r.Context(), "enabled_before", t.cur.Enabled)
	auth.AddAuditDetail(r.Context(), "enabled_after", next.Enabled)
	if created {
		auth.AddAuditDetail(r.Context(), "created", true)
		auth.AddAuditDetail(r.Context(), "driver", t.driver)
	}
	if len(changed) > 0 {
		auth.AddAuditDetail(r.Context(), "changed_fields", changed)
	}
	if len(failed) > 0 {
		auth.AddAuditDetail(r.Context(), "test_failed", failed)
		auth.AddAuditDetail(r.Context(), "confirmed_failed_test", body.ConfirmFailedTest)
	}

	view := h.viewOf(r, t.slug)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{
		"ok":       true,
		"provider": view,
		"checks":   checks,
		"test_ok":  testOK,
		// super_admin: the account that passed the test is now a platform
		// administrator (operating-system providers).
		"super_admin":  superAdmin,
		"tenant_admin": promote && !superAdmin,
	})
}

func (h *AuthProviders) viewOf(r *http.Request, slug string) *providerView {
	views, _ := h.views(r)
	for i := range views {
		if views[i].Name == slug {
			return &views[i]
		}
	}
	return nil
}

// testScope is where an operating-system test account signs in: the realm the
// request names (`test_account.realm`), else the platform's own tenant when
// the instance serves it (or on a single-tenant install), else the one tenant
// it serves. key is the scope's number in the promoted list (0 = the
// platform's own). The refusal is a code (`test_realm_required`,
// `test_realm_unknown`) when there is no single answer.
type testScope struct {
	tenant *model.Provider
	key    int64
}

func (h *AuthProviders) testScope(ctx context.Context, t *providerTarget, typed string) (testScope, string) {
	if !h.multiTenant() {
		return testScope{}, ""
	}
	main, _ := h.Store.GetSupertenant(ctx)
	if typed = tenant.NormalizeRealm(typed); typed != "" {
		p, err := h.Store.GetProviderByRealm(ctx, typed)
		if err != nil || p == nil {
			return testScope{}, "test_realm_unknown"
		}
		return testScope{tenant: p, key: p.ID}, ""
	}
	if t.row == nil || main == nil {
		return testScope{tenant: main}, ""
	}
	bound := h.Live.Current().Bindings().TenantsOf(t.row.ID)
	for _, id := range bound {
		if id == main.ID {
			return testScope{tenant: main}, ""
		}
	}
	if len(bound) == 1 {
		p, err := h.Store.GetProvider(ctx, bound[0])
		if err == nil && p != nil {
			return testScope{tenant: p, key: p.ID}, ""
		}
	}
	if len(bound) == 0 {
		return testScope{tenant: main}, ""
	}
	return testScope{}, "test_realm_required"
}

// notePromoted records that the instance promoted its test account in a
// scope, on its row (made by the reload that just ran, for a first instance).
func (h *AuthProviders) notePromoted(ctx context.Context, slug string, scope int64) {
	row, err := h.Store.GetAuthInstanceBySlug(ctx, slug)
	if err != nil || row == nil || row.PromotedIn(scope) {
		return
	}
	row.Promoted = append(row.Promoted, scope)
	_ = h.Store.UpdateAuthInstance(ctx, row)
}

// lockout is the "last way in" rule for an instance about to stop serving:
// switched off or deleted (remove nil: every tenant it serves), or unbound
// from the tenants in remove. The platform's own tenant (and a single-tenant
// install) is refused outright (last_sign_in_method); a tenant is refused
// unless the operator confirmed it (tenant_lockout, with the tenant).
func (h *AuthProviders) lockout(ctx context.Context, slug string, row *model.AuthInstance, remove []int64, confirm bool) (string, string, srvtext.Vars) {
	set := h.Live.Current()
	if !set.MultiTenant() || set.Bindings() == nil {
		if paths := h.Live.AdminPaths(ctx, slug); len(paths) == 0 {
			return "last_sign_in_method", "server.auth_provider.last_sign_in_method", srvtext.Vars{"name": slug}
		}
		return "", "", nil
	}
	tenants := remove
	if tenants == nil {
		if row == nil {
			tenants = []int64{set.Bindings().Main}
		} else {
			tenants = set.Bindings().TenantsOf(row.ID)
		}
	}
	excl := func(e authsetup.Entry) bool { return e.Slug == slug }
	for _, tid := range tenants {
		if len(h.Live.AdminPathsIn(ctx, tid, excl)) > 0 {
			continue
		}
		if tid == set.Bindings().Main {
			return "last_sign_in_method", "server.auth_provider.last_sign_in_method", srvtext.Vars{"name": slug}
		}
		if !confirm {
			name := strconv.FormatInt(tid, 10)
			if p, err := h.Store.GetProvider(ctx, tid); err == nil && p != nil {
				name = p.Name
			}
			return "tenant_lockout", "server.auth_provider.tenant_lockout", srvtext.Vars{"name": slug, "tenant": name}
		}
	}
	return "", "", nil
}

func parseUpdate(raw map[string]any) updateBody {
	var b updateBody
	if v, ok := raw["enabled"].(bool); ok {
		b.Enabled = &v
	}
	if v, ok := raw["label"].(string); ok {
		b.Label = &v
	}
	if v, ok := raw["confirm_failed_test"].(bool); ok {
		b.ConfirmFailedTest = v
	}
	if v, ok := raw["confirm_tenant_lockout"].(bool); ok {
		b.ConfirmTenantLockout = v
	}
	if ta, ok := raw["test_account"].(map[string]any); ok {
		b.TestRealm, _ = ta["realm"].(string)
	}
	if c, ok := raw["config"].(map[string]any); ok {
		b.Config = c
		return b
	}
	b.Config = map[string]any{}
	for k, v := range raw {
		switch k {
		case "enabled", "confirm_failed_test", "confirm_tenant_lockout", "config", "test_account", "label", "driver", "slug", "tenants":
			continue
		}
		b.Config[k] = v
	}
	return b
}

// slugShape is what a provider's slug may be: the realm's alphabet (lower-case
// letters, digits, dashes, at most 63, a letter or a digit at both ends). It
// goes into a URL path and an audit row, never into an address.
func slugShape(s string) bool {
	if s == "" || len(s) > tenant.RealmMaxLen || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// Create makes another instance of a managed driver (a second LDAP, an OIDC
// for some tenants only): POST /api/admin/auth-providers
// {driver, slug?, label?, enabled?, config?, tenants?, confirm_failed_test?,
// test_account?}. It is saved like Update saves (the real test first), and
// serves the platform's own tenant unless `tenants` names others (a
// multi-tenant install).
func (h *AuthProviders) Create(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, authProvidersAreInstanceWide) {
		return
	}
	raw := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	driver, _ := raw["driver"].(string)
	driver = authsetup.Canonical(driver)
	if !authsetup.IsManaged(driver) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "driver_invalid", "field": "driver"})
		return
	}
	if h.DemoMode {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "demo_read_only"})
		return
	}
	if h.Live == nil {
		writeProviderRefusal(w, r, http.StatusServiceUnavailable, "not_managed", nil, "server.auth_provider.not_managed", nil)
		return
	}
	ctx := r.Context()
	slug, _ := raw["slug"].(string)
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		for i := 2; i < 100; i++ {
			c := fmt.Sprintf("%s-%d", driver, i)
			if got, err := h.Store.GetAuthInstanceBySlug(ctx, c); err == nil && got == nil {
				slug = c
				break
			}
		}
	}
	if !slugShape(slug) || authsetup.IsManaged(slug) || slug == "local" || slug == "api-token" || slug == authsetup.OwnSSOKey {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "slug_invalid", "field": "slug"})
		return
	}
	if got, err := h.Store.GetAuthInstanceBySlug(ctx, slug); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	} else if got != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "slug_taken", "field": "slug"})
		return
	}
	tenants, terr := h.tenantList(ctx, raw["tenants"])
	if terr != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": terr, "field": "tenants"})
		return
	}
	label, _ := raw["label"].(string)
	uid := callerRef(r)
	row, err := h.Store.CreateAuthInstance(ctx, &model.AuthInstance{
		Slug: slug, Driver: driver, Label: strings.TrimSpace(label), Origin: model.AuthOriginPage, CreatedBy: uid,
	})
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	auth.SetAuditTarget(ctx, strconv.FormatInt(row.ID, 10), slug)
	if len(tenants) == 0 {
		if main, err := h.Store.GetSupertenant(ctx); err == nil && main != nil {
			tenants = []int64{main.ID}
		}
	}
	for _, pid := range tenants {
		if err := h.Store.BindAuthInstance(ctx, pid, row.ID, model.AuthBindExplicit); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	body := parseUpdate(raw)
	pctx := auth.WithProbeLang(ctx, langOf(r))
	ta, _ := auth.ParseTestAccount(raw)
	pctx = auth.WithTestAccount(pctx, ta)
	t := &providerTarget{slug: slug, driver: driver, row: row, cur: &authsetup.Stored{Name: driver, Values: map[string]string{}}}
	rec := &statusRecorder{ResponseWriter: w}
	h.apply(rec, r, pctx, t, body, ta, true)
	if rec.status >= 300 {
		// Refused (a failing test, a missing key): the row it was to be does
		// not stay behind.
		_ = h.Store.DeleteAuthInstance(ctx, row.ID)
	}
}

// statusRecorder remembers the status apply answered with.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// tenantList reads a `tenants` array of tenant ids, each one a tenant that
// exists. "" or the refusal's code.
func (h *AuthProviders) tenantList(ctx context.Context, v any) ([]int64, string) {
	if v == nil {
		return nil, ""
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, "tenants_invalid"
	}
	var out []int64
	seen := map[int64]bool{}
	for _, x := range arr {
		f, ok := x.(float64)
		if !ok || f != float64(int64(f)) || f <= 0 {
			return nil, "tenants_invalid"
		}
		id := int64(f)
		if seen[id] {
			continue
		}
		p, err := h.Store.GetProvider(ctx, id)
		if err != nil || p == nil {
			return nil, "tenants_invalid"
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, ""
}

// callerRef is the signed-in caller's id for a created_by column, nil for
// none (callerID's 0 would be a dangling reference).
func callerRef(r *http.Request) *int64 {
	if u := auth.UserFrom(r.Context()); u != nil && u.ID > 0 {
		id := u.ID
		return &id
	}
	return nil
}

// Delete removes an instance made with Create (DELETE
// /api/admin/auth-providers/{name}). A driver's first instance is switched
// off, not deleted (its settings rows are the environment-independent record
// of it); an environment provider is the environment's.
func (h *AuthProviders) Delete(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, authProvidersAreInstanceWide) {
		return
	}
	name := authsetup.Canonical(chi.URLParam(r, "name"))
	if !h.writable(w, r, name) {
		return
	}
	if authsetup.IsManaged(name) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "first_instance", "message": "the first provider of a kind is switched off, not deleted"})
		return
	}
	ctx := r.Context()
	t, err := h.target(ctx, name)
	if errors.Is(err, errNoSuchProvider) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such provider"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	confirm := r.URL.Query().Get("confirm_tenant_lockout") == "1"
	if t.row.Enabled && stateOf(h.Live.Current(), t.slug) == "running" {
		if code, msgKey, vars := h.lockout(ctx, t.slug, t.row, nil, confirm); code != "" {
			writeProviderRefusal(w, r, http.StatusConflict, code, nil, msgKey, vars)
			return
		}
	}
	if err := h.Store.DeleteAuthInstance(ctx, t.row.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	auth.SetAuditTarget(ctx, strconv.FormatInt(t.row.ID, 10), t.slug)
	auth.AddAuditDetail(ctx, "driver", t.driver)
	// Its directory sync report goes with it. The accounts and groups it
	// made stay: nobody's files go with a provider.
	if t.driver == "ldap" {
		_ = h.Store.DeleteSettingsWithPrefix(ctx, authsetup.SyncKeyPrefix(t.slug))
	}
	if err := h.Live.Reload(ctx); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// SyncStatus answers an LDAP instance's directory sync: whether it can run
// (a running LDAP provider), whether it is running now, its interval and the
// last run's report (docs/LDAP.md → Directory sync).
//
//	GET /api/admin/auth-providers/{name}/sync
func (h *AuthProviders) SyncStatus(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, authProvidersAreInstanceWide) {
		return
	}
	name := authsetup.Canonical(chi.URLParam(r, "name"))
	out := map[string]any{"name": name, "available": false, "running": false, "interval_seconds": 0, "last": nil}
	if h.Live != nil {
		out["available"] = h.Live.CanSync(name)
		out["running"] = h.Live.Syncing(name)
		if iv := h.Live.SyncInterval(name); iv > 0 {
			out["interval_seconds"] = int(iv.Seconds())
		}
		if last, _ := h.Live.LastSync(r.Context(), name); last != nil {
			out["last"] = last
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// SyncStart starts an LDAP instance's directory sync and answers 202 at once
// — a directory of thousands takes longer than a request should; the page
// polls SyncStatus. 404 when the provider cannot sync, 409 while a run is
// going.
//
//	POST /api/admin/auth-providers/{name}/sync
func (h *AuthProviders) SyncStart(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, authProvidersAreInstanceWide) {
		return
	}
	// A run opens accounts, switches them off and moves group memberships -
	// through a group that gives Administrator, administrators too. A person
	// signed in to the panel starts it, not an API key.
	if !sessionOnly(w, r, "Starting a directory sync needs an administrator signed in to the admin panel; an API key cannot do it.", nil) {
		return
	}
	if h.DemoMode {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "demo_read_only"})
		return
	}
	name := authsetup.Canonical(chi.URLParam(r, "name"))
	if h.Live == nil || !h.Live.CanSync(name) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": authsetup.ErrNoDirectorySync.Error()})
		return
	}
	auth.SetAuditTarget(r.Context(), name, name)
	if err := h.Live.StartSync(r.Context(), name, "manual"); err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, authsetup.ErrSyncRunning):
			status = http.StatusConflict
		case errors.Is(err, authsetup.ErrNoDirectorySync):
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"name": name, "running": true})
}

// SetTenants says which tenants sign in through an instance (PUT
// /api/admin/auth-providers/{name}/tenants {tenants: [ids],
// confirm_tenant_lockout?}): bindings added and removed so the list is
// exactly this one. A multi-tenant install's; a tenant's own instance serves
// its tenant only, ever. Removing the last way in of a tenant is the lockout
// rule's.
func (h *AuthProviders) SetTenants(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, authProvidersAreInstanceWide) {
		return
	}
	name := authsetup.Canonical(chi.URLParam(r, "name"))
	if h.DemoMode {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "demo_read_only"})
		return
	}
	if h.Live == nil {
		writeProviderRefusal(w, r, http.StatusServiceUnavailable, "not_managed", nil, "server.auth_provider.not_managed", nil)
		return
	}
	if !h.multiTenant() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "not_multi_tenant", "message": "a single-tenant install has one tenant: every provider serves it"})
		return
	}
	raw := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	ctx := r.Context()
	want, terr := h.tenantList(ctx, raw["tenants"])
	if terr != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": terr, "field": "tenants"})
		return
	}
	confirm, _ := raw["confirm_tenant_lockout"].(bool)
	row, err := h.Store.GetAuthInstanceBySlug(ctx, name)
	if err == nil && row == nil {
		// A first instance whose row the next reload makes.
		if rerr := h.Live.Reload(ctx); rerr == nil {
			row, err = h.Store.GetAuthInstanceBySlug(ctx, name)
		}
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if row == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such provider"})
		return
	}
	if row.OwnerProviderID != nil {
		if len(want) != 1 || want[0] != *row.OwnerProviderID {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "tenant_owned", "message": "a tenant's own provider serves that tenant only"})
			return
		}
	}
	have := h.Live.Current().Bindings().TenantsOf(row.ID)
	wantSet := map[int64]bool{}
	for _, id := range want {
		wantSet[id] = true
	}
	var removed, added []int64
	for _, id := range have {
		if !wantSet[id] {
			removed = append(removed, id)
		}
	}
	haveSet := map[int64]bool{}
	for _, id := range have {
		haveSet[id] = true
	}
	for _, id := range want {
		if !haveSet[id] {
			added = append(added, id)
		}
	}
	if len(removed) > 0 && stateOf(h.Live.Current(), row.Slug) == "running" {
		if code, msgKey, vars := h.lockout(ctx, row.Slug, row, removed, confirm); code != "" {
			writeProviderRefusal(w, r, http.StatusConflict, code, nil, msgKey, vars)
			return
		}
	}
	for _, id := range added {
		if err := h.Store.BindAuthInstance(ctx, id, row.ID, model.AuthBindExplicit); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	for _, id := range removed {
		if err := h.Store.UnbindAuthInstance(ctx, id, row.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := h.Live.Reload(ctx); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload: " + err.Error()})
		return
	}
	auth.SetAuditTarget(ctx, strconv.FormatInt(row.ID, 10), row.Slug)
	auth.AddAuditDetail(ctx, "tenants_before", have)
	auth.AddAuditDetail(ctx, "tenants_after", want)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": h.viewOf(r, row.Slug)})
}

// writeProviderRefusal answers a refused change with a code and the
// catalogue's sentence (a `server.auth_provider.*` key) in the reader's
// language.
func writeProviderRefusal(w http.ResponseWriter, r *http.Request, status int, code string, extra map[string]any, key string, vars srvtext.Vars) {
	body := map[string]any{"error": code, "message": srvtext.Text(langOf(r), key, vars)}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

// Test checks a provider's configuration for real, step by step.
//
// ⚠⚠ It used to answer `{"ok": true}` for anything (release-candidate sweep,
// 2026-09-21). A test button that always passes is worse than none.
//
// A page provider is tested on the form as it stands (unsaved edits laid over
// what is stored; a secret left blank is the stored one, opened for the test
// and never returned). A provider the environment defines is tested on the
// environment's configuration — the form is read-only there, so a draft is
// ignored.
func (h *AuthProviders) Test(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, authProvidersAreInstanceWide) {
		return
	}
	name := authsetup.Canonical(chi.URLParam(r, "name"))
	var draft map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&draft)
	}
	ctx := auth.WithProbeLang(r.Context(), langOf(r))
	if ta, ok := auth.ParseTestAccount(draft); ok {
		ctx = auth.WithTestAccount(ctx, ta)
	}
	if c, ok := draft["config"].(map[string]any); ok {
		draft = c
	}
	var cfg map[string]any
	driver := name
	if env, isEnv := h.envEntry(name); isEnv {
		cfg = env.Config()
	} else {
		opts := authsetup.Options{}
		if h.Live != nil {
			opts = h.Live.Options()
		}
		cur := &authsetup.Stored{Name: name, Values: map[string]string{}}
		if h.Store != nil {
			if t, err := h.target(r.Context(), name); err == nil && t.cur != nil {
				cur, driver = t.cur, t.driver
			} else if !authsetup.IsManaged(name) {
				if _, gerr := auth.Get(name); gerr != nil {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such driver"})
					return
				}
			}
		}
		var err error
		if cfg, err = authsetup.DraftConfig(cur, draft, opts.Box, opts); err != nil {
			cfg = map[string]any{}
		}
		if h.Store != nil {
			if t, terr := h.target(r.Context(), name); terr == nil && t.guard != nil {
				t.guard(cfg)
			}
		}
	}
	drv, err := auth.Get(driver)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such driver"})
		return
	}
	prober, ok := drv.(auth.Prober)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "testable": false, "ok": false, "checks": []auth.ProbeCheck{}})
		return
	}
	checks := prober.Probe(ctx, cfg, r)
	if checks == nil {
		checks = []auth.ProbeCheck{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": name, "testable": true, "ok": auth.ProbeOKAll(checks), "checks": checks,
	})
}

func (h *AuthProviders) envEntry(name string) (authsetup.Entry, bool) {
	if h.Live == nil {
		return authsetup.Entry{}, false
	}
	return h.Live.EnvDefines(name)
}

func isSecretKey(k string) bool {
	switch strings.ToLower(k) {
	case "client_secret", "secret", "password", "bind_password", "token":
		return true
	}
	return false
}

// stringifyValue renders a JSON value as the settings table stores it.
// (settings.go writes arbitrary settings through it.)
func stringifyValue(v interface{}) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	default:
		b, err := json.Marshal(v)
		return string(b), err
	}
}
