package authsetup

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ── Instances: sign-in providers as rows, bound to tenants ──────────────────
//
// docs/TENANT-ADMIN.md. Until 0.50 a provider was its driver: `auth.ldap.*`
// meant "the LDAP". Now a configured provider is a row of auth_instances, and
// provider_auth_instances says which tenant signs in through which.
//
// Where an instance's configuration lives:
//
//   - the ENVIRONMENT's providers (FILEX_AUTH_DRIVERS, the config file): in the
//     environment, as before; the row carries only the bindings;
//   - the page's FIRST instance of a driver (slug == driver name): in the
//     `auth.<driver>.*` settings rows, exactly where it has always been, so
//     nothing is copied, no secret moves, and a binary of the previous version
//     still finds it after a rollback;
//   - every further instance, and every tenant's own: on its row
//     (auth_instances.config_json, secrets sealed).
//
// `local` (filex's own passwords) is not an instance: password sign-in is the
// environment's to switch on, for every tenant.

const (
	// InstancesSetting records that the one-time upgrade below has run.
	InstancesSetting = "auth.instances.upgraded"
	// ReviewSetting asks the operator to review the bindings the upgrade made
	// (every provider bound to every tenant), until they dismiss it.
	ReviewSetting = "auth.instances.review_pending"
)

// PrimarySlug reports whether a slug names the first instance of a driver,
// whose configuration lives in the `auth.<driver>.*` settings rows.
func PrimarySlug(slug, driver string) bool {
	return Canonical(slug) == Canonical(driver)
}

// envPin is the 0.50 `provider` pin an environment provider carries
// (FILEX_LDAP_PROVIDER, FILEX_HEADER_PROVIDER): a tenant slug, or "".
func envPin(e Entry) string {
	if e.config == nil {
		return ""
	}
	s, _ := e.config["provider"].(string)
	return strings.TrimSpace(s)
}

// tenantsAtHand is the platform's own tenant and every other tenant now.
type tenantsAtHand struct {
	main   int64
	others []int64
	bySlug map[string]int64
}

func readTenants(ctx context.Context, store db.Store) (tenantsAtHand, error) {
	ps, err := store.ListProviders(ctx)
	if err != nil {
		return tenantsAtHand{}, err
	}
	out := tenantsAtHand{bySlug: map[string]int64{}}
	for _, p := range ps {
		if p == nil {
			continue
		}
		out.bySlug[p.Slug] = p.ID
		if p.IsSupertenant && out.main == 0 {
			out.main = p.ID
			continue
		}
		out.others = append(out.others, p.ID)
	}
	return out, nil
}

// wantedAnchors lists the rows the existing providers are known by: every
// environment provider but `local`, and the page's first instance of every
// managed driver that has settings rows and that the environment does not
// define (the environment's wins; the page's rows under its name stay
// shadowed, as they always were).
func wantedAnchors(env []Entry, stored map[string]*Stored) []anchorWant {
	var out []anchorWant
	seen := map[string]bool{}
	for _, e := range env {
		if e.Name == "local" || e.Name == "" || seen[e.Name] {
			continue
		}
		seen[e.Name] = true
		out = append(out, anchorWant{slug: e.Name, driver: e.Name, origin: model.AuthOriginEnvironment, pin: envPin(e)})
	}
	for _, name := range Managed {
		if seen[name] {
			continue
		}
		if s := stored[name]; s != nil && s.Exists {
			seen[name] = true
			out = append(out, anchorWant{slug: name, driver: name, origin: model.AuthOriginPage,
				wasOn: s.Enabled && IsOS(name)})
		}
	}
	return out
}

type anchorWant struct {
	slug, driver, origin, pin string
	// wasOn: an operating-system provider the page had already switched on,
	// with a passed test that promoted its account in the platform's tenant.
	// Its row starts with that scope promoted, so a later test there promotes
	// nobody (the owner's decision of 2026-10-01).
	wasOn bool
}

// EnsureAnchors makes sure every existing provider has its row, and binds a
// row it had to create:
//
//   - before the one-time upgrade (InstancesSetting unset): to the platform's
//     own tenant AND to every tenant that exists now (source `upgrade`) - what
//     happened before 0.50, where one LDAP served every tenant - and it asks
//     the operator to review the bindings (ReviewSetting) when there are
//     tenants to review;
//   - after it: to the platform's own tenant only (a provider added later
//     serves nobody else until somebody binds it).
//
// A pin (FILEX_LDAP_PROVIDER, FILEX_HEADER_PROVIDER) is an implicit binding to
// the pinned tenant (source `pin`), whenever it is read. Idempotent: run at
// every Start and Reload.
func EnsureAnchors(ctx context.Context, store db.Store, env []Entry, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	stored, err := LoadStored(ctx, store)
	if err != nil {
		return err
	}
	ts, err := readTenants(ctx, store)
	if err != nil {
		return err
	}
	upgraded := false
	if v, err := store.GetSetting(ctx, InstancesSetting); err == nil && v == "1" {
		upgraded = true
	}
	for _, w := range wantedAnchors(env, stored) {
		inst, err := store.GetAuthInstanceBySlug(ctx, w.slug)
		if err != nil {
			return err
		}
		if inst == nil {
			row := &model.AuthInstance{Slug: w.slug, Driver: w.driver, Origin: w.origin}
			if w.wasOn {
				row.Promoted = []int64{0}
			}
			inst, err = store.CreateAuthInstance(ctx, row)
			if err != nil {
				return fmt.Errorf("auth: the row of provider %q: %w", w.slug, err)
			}
			targets := []int64{}
			source := model.AuthBindExplicit
			if !upgraded {
				targets = append(targets, ts.others...)
				source = model.AuthBindUpgrade
			}
			if ts.main != 0 {
				targets = append([]int64{ts.main}, targets...)
			}
			for _, pid := range targets {
				if err := store.BindAuthInstance(ctx, pid, inst.ID, source); err != nil {
					return err
				}
			}
		}
		if w.pin != "" {
			if pid, ok := ts.bySlug[w.pin]; ok {
				if err := store.BindAuthInstance(ctx, pid, inst.ID, model.AuthBindPin); err != nil {
					return err
				}
			} else {
				log.Warn("auth: a provider pin names no tenant; nothing is bound by it",
					slog.String("provider", w.slug), slog.String("pin", w.pin))
			}
		}
	}
	if !upgraded {
		if len(ts.others) > 0 {
			if err := store.UpsertSetting(ctx, ReviewSetting, "1"); err != nil {
				return err
			}
			log.Warn("auth: sign-in providers are now bound to tenants; every provider that existed was bound to every tenant that existed, as it served them before - review the bindings on Admin → Identity providers and remove the tenants a provider should not serve",
				slog.Int("tenants", len(ts.others)))
		}
		if err := store.UpsertSetting(ctx, InstancesSetting, "1"); err != nil {
			return err
		}
	}
	return nil
}

// ── Bindings, read for one reload ───────────────────────────────────────────

// Bindings is which tenant signs in through which instance, as one reload
// read it.
type Bindings struct {
	// Main is the platform's own tenant (the supertenant), 0 when the install
	// has none.
	Main int64
	// byInstance: instance id → the tenants bound to it.
	byInstance map[int64]map[int64]bool
	// tenants: every tenant id (the platform's own included).
	tenants []int64
	// insecure: tenants whose own providers may reach internal addresses.
	insecure map[int64]bool
}

// ReadBindings reads the bindings and the tenants.
func ReadBindings(ctx context.Context, store db.Store) (*Bindings, error) {
	b := &Bindings{byInstance: map[int64]map[int64]bool{}, insecure: map[int64]bool{}}
	ps, err := store.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p == nil {
			continue
		}
		if p.IsSupertenant && b.Main == 0 {
			b.Main = p.ID
		}
		b.tenants = append(b.tenants, p.ID)
		if p.AllowInsecureAuth {
			b.insecure[p.ID] = true
		}
	}
	rows, err := store.ListAuthBindings(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if b.byInstance[r.InstanceID] == nil {
			b.byInstance[r.InstanceID] = map[int64]bool{}
		}
		b.byInstance[r.InstanceID][r.ProviderID] = true
	}
	return b, nil
}

// Bound reports whether a tenant signs in through an instance. An entry with
// no row (instance 0: `local`, or a provider built before its row existed)
// serves everybody, as every provider did before 0.50.
func (b *Bindings) Bound(instanceID, tenantID int64) bool {
	if instanceID == 0 || b == nil {
		return true
	}
	return b.byInstance[instanceID][tenantID]
}

// TenantsOf lists the tenants bound to an instance, sorted.
func (b *Bindings) TenantsOf(instanceID int64) []int64 {
	if b == nil {
		return nil
	}
	var out []int64
	for pid := range b.byInstance[instanceID] {
		out = append(out, pid)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Tenants lists every tenant the reload saw.
func (b *Bindings) Tenants() []int64 {
	if b == nil {
		return nil
	}
	return append([]int64(nil), b.tenants...)
}

// Insecure reports whether a tenant's own providers may reach internal
// addresses and plain ldap:// (providers.allow_insecure_auth).
func (b *Bindings) Insecure(tenantID int64) bool { return b != nil && b.insecure[tenantID] }

// ── A row instance's configuration ──────────────────────────────────────────

// StoredOfRow reads a row instance's configuration (config_json: field →
// value, secrets sealed) as the Stored the page's primary instance reads from
// the settings rows, so DriverConfig builds both the same way.
func StoredOfRow(a *model.AuthInstance) (*Stored, error) {
	s := &Stored{Name: Canonical(a.Driver), Enabled: a.Enabled, Legacy: a.Legacy, Exists: true, Values: map[string]string{}}
	if strings.TrimSpace(a.ConfigJSON) == "" {
		return s, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(a.ConfigJSON), &raw); err != nil {
		return nil, fmt.Errorf("instance %q: configuration: %w", a.Slug, err)
	}
	for k, v := range raw {
		if _, known := FieldOf(s.Name, k); known {
			s.Values[k] = stringify(v)
		}
	}
	return s, nil
}

// RowConfigJSON is the inverse of StoredOfRow: what a row instance stores.
func RowConfigJSON(s *Stored) string {
	keys := make([]string, 0, len(s.Values))
	for k := range s.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		m[k] = s.Values[k]
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// TenantGuard is what a tenant's own instance is handed on top of its
// configuration (docs/TENANT-ADMIN.md, the owner's decision of 2026-10-01):
// `guarded` (the connection goes through internal/netguard, and an LDAP must
// be encrypted) unless the platform operator allowed the tenant insecure and
// internal-network providers, and never a file on the server (`ca_file`: a
// tenant pastes its CA as `ca_pem`).
func TenantGuard(insecureAllowed bool) func(map[string]any) {
	return func(cfg map[string]any) {
		delete(cfg, "ca_file")
		if !insecureAllowed {
			cfg["guarded"] = true
		}
	}
}

// TenantFields are the fields a tenant's administrator may set on their own
// OIDC or LDAP: the schema's, without the ones that name something on the
// server (`ca_file`).
func TenantFields(driver string) []Field {
	var out []Field
	for _, f := range Schema[Canonical(driver)] {
		if f.Key == "ca_file" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// TenantDrivers are the kinds a tenant's administrator may add for their own
// tenant: an OIDC and an LDAP. The operating-system providers and the header
// proxy are the platform operator's alone.
var TenantDrivers = []string{"oidc", "ldap"}
