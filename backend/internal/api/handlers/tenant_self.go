// Package handlers: tenant_self.go
//
// A tenant runs itself (docs/TENANT-ADMIN.md):
//
//	GET    /api/admin/tenant                                  the tenant: addresses, sign-in, domains
//	POST   /api/admin/tenant/auth-providers                   its own OIDC or LDAP
//	PATCH  /api/admin/tenant/auth-providers/{name}
//	DELETE /api/admin/tenant/auth-providers/{name}
//	POST   /api/admin/tenant/auth-providers/{name}/test
//	POST   /api/admin/tenant/domains                          {domain}: an own domain, pending
//	POST   /api/admin/tenant/domains/{id}/check               look at its CNAME now
//	PUT    /api/admin/tenant/domains/{id}/certificate         {cert_pem, key_pem}: bring a certificate
//	DELETE /api/admin/tenant/domains/{id}/certificate
//	DELETE /api/admin/tenant/domains/{id}
//	PUT    /api/admin/tenant/insecure                         {allow}: the operator's switch
//
// WHOSE tenant: a tenant's administrator acts on their own and nothing else;
// the platform operator (an unconfined administrator) names one with
// `?tenant=<id>`. Another tenant's id from a tenant's administrator is 404, the
// same answer as a tenant that does not exist (handlers/tenantown.go).
//
// The rules the owner decided (2026-09-30, 2026-10-01):
//
//   - a tenant's administrator adds their OWN OIDC or LDAP only - the
//     operating-system providers and the header proxy are the operator's, and
//     binding a shared provider is the operator's too;
//   - a tenant's own provider is guarded (internal/netguard, LDAP only over
//     ldaps:// or StartTLS, the CA pasted, never a file on the server) unless
//     the operator ticked "allow insecure and internal-network providers" for
//     the tenant - which only the operator may change, audited;
//   - the last way a tenant's administrator can sign in is never switched off
//     by that administrator;
//   - an own domain is proven by a CNAME to the tenant's platform subdomain,
//     belongs to one tenant at a time, and may carry the tenant's own
//     certificate.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/tenantdomain"
)

// TenantSelf handles /api/admin/tenant.
type TenantSelf struct {
	Store       db.Store
	Live        *authsetup.Live
	Domains     *tenantdomain.Service
	MultiTenant bool
	DemoMode    bool
	// TLSMode is FILEX_TLS_MODE, told to the screen (who issues the
	// certificate of an own domain that brought none).
	TLSMode string
}

// NewTenantSelf constructs the handler.
func NewTenantSelf(store db.Store, live *authsetup.Live, domains *tenantdomain.Service, multiTenant bool) *TenantSelf {
	return &TenantSelf{Store: store, Live: live, Domains: domains, MultiTenant: multiTenant}
}

// target is the tenant a request acts on, and whether the caller is the
// platform operator. It writes the refusal and answers nil when there is one.
func (h *TenantSelf) target(w http.ResponseWriter, r *http.Request) (*model.Provider, bool) {
	if !h.MultiTenant {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "not_multi_tenant", "message": "a single-tenant install has one tenant, its own"})
		return nil, false
	}
	ctx := r.Context()
	asked := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if scope, confined := confinedScope(ctx); confined {
		if asked != "" && asked != strconv.FormatInt(scope.ProviderID, 10) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
			return nil, false
		}
		p, err := h.Store.GetProvider(ctx, scope.ProviderID)
		if err != nil || p == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
			return nil, false
		}
		return p, false
	}
	id, err := strconv.ParseInt(asked, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_required", "message": "name the tenant: ?tenant=<id>"})
		return nil, false
	}
	p, err := h.Store.GetProvider(ctx, id)
	if err != nil || p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return nil, false
	}
	if p.IsSupertenant {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "platform_tenant", "message": "the platform's own sign-in is on Admin → Identity providers"})
		return nil, false
	}
	return p, true
}

func (h *TenantSelf) refuseDemo(w http.ResponseWriter) bool {
	if h.DemoMode {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "demo_read_only"})
		return true
	}
	return false
}

// ownProvider is one of the tenant's own providers, as its screen shows it.
type ownProvider struct {
	providerView
}

// sharedProvider is a provider the operator bound to the tenant: its name and
// how it stands, never its configuration.
type sharedProvider struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Label  string `json:"label,omitempty"`
	State  string `json:"state"`
}

// domainView is an own domain as the screen shows it.
type domainView struct {
	*model.ProviderDomain
	// Target is what its CNAME must point at.
	Target string `json:"target"`
	// OwnCertificate: the tenant brought one (its key is never sent back).
	OwnCertificate bool `json:"own_certificate"`
	// ACME is what filex's own ACME last did for it (FILEX_TLS_MODE=acme, no
	// certificate of its own): obtained until when, not obtained and why, or
	// nothing yet. This process's memory only (docs/TENANT-ADMIN.md).
	ACME *tenantdomain.ACMEResult `json:"acme,omitempty"`
}

// Get answers the tenant's overview.
func (h *TenantSelf) Get(w http.ResponseWriter, r *http.Request) {
	p, _ := h.target(w, r)
	if p == nil {
		return
	}
	writeJSON(w, http.StatusOK, h.overview(r, p))
}

func (h *TenantSelf) overview(r *http.Request, p *model.Provider) map[string]any {
	ctx := r.Context()
	out := map[string]any{
		"tenant": map[string]any{
			"id": p.ID, "name": p.Name, "slug": p.Slug, "realm": p.LoginRealm(), "host": p.Host,
			"allow_insecure_auth": p.AllowInsecureAuth,
		},
		"platform_subdomain": db.PlatformSubdomain(p),
		"tenant_domain":      db.TenantDomain(),
		"tls_mode":           h.TLSMode,
		"drivers":            authsetup.TenantDrivers,
	}
	fields := map[string][]authsetup.Field{}
	for _, d := range authsetup.TenantDrivers {
		fields[d] = authsetup.TenantFields(d)
	}
	out["fields"] = fields
	own, shared := []ownProvider{}, []sharedProvider{}
	if h.Live != nil && h.Store != nil {
		ap := &AuthProviders{Store: h.Store, Live: h.Live}
		views, _ := ap.views(r)
		set := h.Live.Current()
		for _, v := range views {
			switch {
			case v.OwnerProviderID != nil && *v.OwnerProviderID == p.ID:
				v.Fields = authsetup.TenantFields(v.Driver)
				delete(v.ConfigRedacted, "ca_file")
				own = append(own, ownProvider{v})
			case v.InstanceID != 0 && v.OwnerProviderID == nil && set.Bindings().Bound(v.InstanceID, p.ID):
				shared = append(shared, sharedProvider{Name: v.Name, Driver: v.Driver, Label: v.Label, State: v.State})
			}
		}
	}
	out["providers"] = own
	out["shared_providers"] = shared
	domains := []domainView{}
	if rows, err := h.Store.ListProviderDomains(ctx, p.ID); err == nil {
		target := db.PlatformSubdomain(p)
		for _, d := range rows {
			v := domainView{ProviderDomain: d, Target: target, OwnCertificate: d.HasOwnCertificate()}
			if h.Domains != nil && h.Domains.ACME != nil && !v.OwnCertificate {
				r := h.Domains.ACME.For(d.Domain)
				v.ACME = &r
			}
			domains = append(domains, v)
		}
	}
	out["domains"] = domains
	return out
}

// ── the tenant's own OIDC and LDAP ──

// tenantAP is the identity-providers handler a tenant's own providers are
// saved through: the same apply (the real test first, the lockout rule, the
// audit), never a second save path.
func (h *TenantSelf) tenantAP() *AuthProviders {
	return &AuthProviders{Store: h.Store, Live: h.Live, DemoMode: h.DemoMode}
}

// ownRow finds one of the tenant's own providers by slug, or writes 404.
func (h *TenantSelf) ownRow(w http.ResponseWriter, r *http.Request, p *model.Provider) *model.AuthInstance {
	name := authsetup.Canonical(chi.URLParam(r, "name"))
	row, err := h.Store.GetAuthInstanceBySlug(r.Context(), name)
	if err != nil || row == nil || row.OwnerProviderID == nil || *row.OwnerProviderID != p.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such provider"})
		return nil
	}
	return row
}

// tenantConfig keeps the fields a tenant may set (TenantFields): `ca_file`,
// a file on the server, is never one.
func tenantConfig(driver string, cfg map[string]any) map[string]any {
	out := map[string]any{}
	for _, f := range authsetup.TenantFields(driver) {
		if v, ok := cfg[f.Key]; ok {
			out[f.Key] = v
		}
	}
	return out
}

func tenantDriverAllowed(d string) bool {
	for _, x := range authsetup.TenantDrivers {
		if x == d {
			return true
		}
	}
	return false
}

// CreateProvider adds the tenant's own OIDC or LDAP:
// {driver, label?, slug?, enabled?, config?, confirm_failed_test?}.
func (h *TenantSelf) CreateProvider(w http.ResponseWriter, r *http.Request) {
	p, operator := h.target(w, r)
	if p == nil || h.refuseDemo(w) {
		return
	}
	if h.Live == nil {
		writeProviderRefusal(w, r, http.StatusServiceUnavailable, "not_managed", nil, "server.auth_provider.not_managed", nil)
		return
	}
	raw := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	driver, _ := raw["driver"].(string)
	driver = authsetup.Canonical(driver)
	if !tenantDriverAllowed(driver) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "driver_invalid", "field": "driver",
			"message": "a tenant adds its own OIDC or LDAP; the other kinds are the platform operator's"})
		return
	}
	ctx := r.Context()
	slug, _ := raw["slug"].(string)
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		base := p.LoginRealm()
		if base == "" {
			base = strings.ToLower(p.Slug)
		}
		for i := 1; i < 100; i++ {
			c := fmt.Sprintf("%s-%s", base, driver)
			if i > 1 {
				c = fmt.Sprintf("%s-%d", c, i)
			}
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
	label, _ := raw["label"].(string)
	owner := p.ID
	row, err := h.Store.CreateAuthInstance(ctx, &model.AuthInstance{
		Slug: slug, Driver: driver, Label: strings.TrimSpace(label), Origin: model.AuthOriginTenant,
		OwnerProviderID: &owner, CreatedBy: callerRef(r),
	})
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err := h.Store.BindAuthInstance(ctx, p.ID, row.ID, model.AuthBindExplicit); err != nil {
		_ = h.Store.DeleteAuthInstance(ctx, row.ID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	auth.SetAuditTarget(ctx, strconv.FormatInt(row.ID, 10), slug)
	auth.AddAuditDetail(ctx, "tenant", p.ID)
	body := parseUpdate(raw)
	body.Config = tenantConfig(driver, body.Config)
	body.TestRealm = ""
	if !operator {
		body.ConfirmTenantLockout = false
	}
	t := &providerTarget{slug: slug, driver: driver, row: row, cur: &authsetup.Stored{Name: driver, Values: map[string]string{}},
		guard: authsetup.TenantGuard(p.AllowInsecureAuth)}
	rec := &statusRecorder{ResponseWriter: w}
	h.tenantAP().apply(rec, r, auth.WithProbeLang(ctx, langOf(r)), t, body, nil, true)
	if rec.status >= 300 {
		_ = h.Store.DeleteAuthInstance(ctx, row.ID)
	}
}

// UpdateProvider changes one of the tenant's own providers.
func (h *TenantSelf) UpdateProvider(w http.ResponseWriter, r *http.Request) {
	p, operator := h.target(w, r)
	if p == nil || h.refuseDemo(w) {
		return
	}
	if h.Live == nil {
		writeProviderRefusal(w, r, http.StatusServiceUnavailable, "not_managed", nil, "server.auth_provider.not_managed", nil)
		return
	}
	row := h.ownRow(w, r, p)
	if row == nil {
		return
	}
	raw := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	cur, err := authsetup.StoredOfRow(row)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	body := parseUpdate(raw)
	body.Config = tenantConfig(cur.Name, body.Config)
	body.TestRealm = ""
	if !operator {
		// A tenant's administrator never locks their own tenant out.
		body.ConfirmTenantLockout = false
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(row.ID, 10), row.Slug)
	auth.AddAuditDetail(r.Context(), "tenant", p.ID)
	t := &providerTarget{slug: row.Slug, driver: cur.Name, row: row, cur: cur, guard: authsetup.TenantGuard(p.AllowInsecureAuth)}
	h.tenantAP().apply(w, r, auth.WithProbeLang(r.Context(), langOf(r)), t, body, nil, false)
}

// DeleteProvider removes one of the tenant's own providers.
func (h *TenantSelf) DeleteProvider(w http.ResponseWriter, r *http.Request) {
	p, operator := h.target(w, r)
	if p == nil || h.refuseDemo(w) {
		return
	}
	if h.Live == nil {
		writeProviderRefusal(w, r, http.StatusServiceUnavailable, "not_managed", nil, "server.auth_provider.not_managed", nil)
		return
	}
	row := h.ownRow(w, r, p)
	if row == nil {
		return
	}
	ctx := r.Context()
	confirm := operator && r.URL.Query().Get("confirm_tenant_lockout") == "1"
	if row.Enabled && stateOf(h.Live.Current(), row.Slug) == "running" {
		if code, msgKey, vars := h.tenantAP().lockout(ctx, row.Slug, row, []int64{p.ID}, confirm); code != "" {
			writeProviderRefusal(w, r, http.StatusConflict, code, nil, msgKey, vars)
			return
		}
	}
	if err := h.Store.DeleteAuthInstance(ctx, row.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	auth.SetAuditTarget(ctx, strconv.FormatInt(row.ID, 10), row.Slug)
	auth.AddAuditDetail(ctx, "tenant", p.ID)
	if err := h.Live.Reload(ctx); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// TestProvider tests one of the tenant's own providers on the form as it
// stands, through the same guard as its sign-ins.
func (h *TenantSelf) TestProvider(w http.ResponseWriter, r *http.Request) {
	p, _ := h.target(w, r)
	if p == nil {
		return
	}
	row := h.ownRow(w, r, p)
	if row == nil {
		return
	}
	cur, err := authsetup.StoredOfRow(row)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var draft map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&draft)
	}
	if c, ok := draft["config"].(map[string]any); ok {
		draft = c
	}
	opts := authsetup.Options{}
	if h.Live != nil {
		opts = h.Live.Options()
	}
	cfg, err := authsetup.DraftConfig(cur, tenantConfig(cur.Name, draft), opts.Box, opts)
	if err != nil {
		cfg = map[string]any{}
	}
	authsetup.TenantGuard(p.AllowInsecureAuth)(cfg)
	drv, err := auth.Get(cur.Name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such driver"})
		return
	}
	prober, ok := drv.(auth.Prober)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"name": row.Slug, "testable": false, "ok": false, "checks": []auth.ProbeCheck{}})
		return
	}
	checks := prober.Probe(auth.WithProbeLang(r.Context(), langOf(r)), cfg, r)
	if checks == nil {
		checks = []auth.ProbeCheck{}
	}
	auth.SayChecks(readerLang(r), checks)
	writeJSON(w, http.StatusOK, map[string]any{"name": row.Slug, "testable": true, "ok": auth.ProbeOKAll(checks), "checks": checks})
}

// SetInsecure is the platform operator's per-tenant switch:
// {allow: bool}. A tenant's administrator is refused (403).
func (h *TenantSelf) SetInsecure(w http.ResponseWriter, r *http.Request) {
	if _, confined := confinedScope(r.Context()); confined {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "supertenant_only",
			"message": "whether a tenant's providers may reach internal addresses is the platform operator's decision"})
		return
	}
	p, _ := h.target(w, r)
	if p == nil || h.refuseDemo(w) {
		return
	}
	var req struct {
		Allow *bool `json:"allow"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Allow == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "allow required"})
		return
	}
	ctx := r.Context()
	if err := h.Store.SetProviderAllowInsecureAuth(ctx, p.ID, *req.Allow); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	auth.SetAuditTarget(ctx, strconv.FormatInt(p.ID, 10), p.Slug)
	auth.AddAuditDetail(ctx, "allow_before", p.AllowInsecureAuth)
	auth.AddAuditDetail(ctx, "allow_after", *req.Allow)
	if h.Live != nil {
		if err := h.Live.Reload(ctx); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload: " + err.Error()})
			return
		}
	}
	fresh, _ := h.Store.GetProvider(ctx, p.ID)
	if fresh == nil {
		fresh = p
	}
	writeJSON(w, http.StatusOK, h.overview(r, fresh))
}

// ── own domains ──

func (h *TenantSelf) domainsReady(w http.ResponseWriter) bool {
	if h.Domains == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "domains_unavailable"})
		return false
	}
	return true
}

// domainRefusal answers a refused domain with its code.
func domainRefusal(w http.ResponseWriter, err error) bool {
	for _, known := range []error{tenantdomain.ErrInvalid, tenantdomain.ErrReserved, tenantdomain.ErrNoTenantDomain,
		tenantdomain.ErrCertInvalid, tenantdomain.ErrCertMismatch, tenantdomain.ErrCertWrongName, tenantdomain.ErrCertExpired,
		tenantdomain.ErrCertNeedsKey} {
		if errors.Is(err, known) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": known.Error()})
			return true
		}
	}
	if errors.Is(err, tenantdomain.ErrTaken) {
		// Never says whose: one tenant at a time is all a stranger learns.
		writeJSON(w, http.StatusConflict, map[string]string{"error": tenantdomain.ErrTaken.Error()})
		return true
	}
	return false
}

// AddDomain records an own domain for the tenant ({domain}), pending until
// its CNAME is seen; it is looked at once at once.
func (h *TenantSelf) AddDomain(w http.ResponseWriter, r *http.Request) {
	p, _ := h.target(w, r)
	if p == nil || h.refuseDemo(w) || !h.domainsReady(w) {
		return
	}
	var req struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	ctx := r.Context()
	d, err := h.Domains.Add(ctx, p, req.Domain, callerRef(r))
	if err != nil {
		if !domainRefusal(w, err) {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	auth.SetAuditTarget(ctx, strconv.FormatInt(d.ID, 10), d.Domain)
	auth.AddAuditDetail(ctx, "tenant", p.ID)
	if checked, _, cerr := h.Domains.Check(ctx, d); cerr == nil {
		d = checked
	}
	writeJSON(w, http.StatusCreated, domainView{ProviderDomain: d, Target: db.PlatformSubdomain(p), OwnCertificate: d.HasOwnCertificate()})
}

// ownDomain finds one of the tenant's domains by id, or writes 404.
func (h *TenantSelf) ownDomain(w http.ResponseWriter, r *http.Request, p *model.Provider) *model.ProviderDomain {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return nil
	}
	d, err := h.Store.GetProviderDomainByID(r.Context(), id)
	if err != nil || d == nil || d.ProviderID != p.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "domain not found"})
		return nil
	}
	return d
}

// CheckDomain looks at a domain's CNAME now.
func (h *TenantSelf) CheckDomain(w http.ResponseWriter, r *http.Request) {
	p, _ := h.target(w, r)
	if p == nil || h.refuseDemo(w) || !h.domainsReady(w) {
		return
	}
	d := h.ownDomain(w, r, p)
	if d == nil {
		return
	}
	checked, changed, err := h.Domains.Check(r.Context(), d)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(d.ID, 10), d.Domain)
	auth.AddAuditDetail(r.Context(), "status", checked.Status)
	writeJSON(w, http.StatusOK, map[string]any{
		"domain":  domainView{ProviderDomain: checked, Target: db.PlatformSubdomain(p), OwnCertificate: checked.HasOwnCertificate()},
		"changed": changed,
	})
}

// DeleteDomain removes an own domain from the tenant.
func (h *TenantSelf) DeleteDomain(w http.ResponseWriter, r *http.Request) {
	p, _ := h.target(w, r)
	if p == nil || h.refuseDemo(w) {
		return
	}
	d := h.ownDomain(w, r, p)
	if d == nil {
		return
	}
	if err := h.Store.DeleteProviderDomain(r.Context(), d.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(d.ID, 10), d.Domain)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// SetCertificate stores the tenant's own certificate for a domain:
// {cert_pem, key_pem}. The key is sealed and never sent back.
func (h *TenantSelf) SetCertificate(w http.ResponseWriter, r *http.Request) {
	p, _ := h.target(w, r)
	if p == nil || h.refuseDemo(w) {
		return
	}
	d := h.ownDomain(w, r, p)
	if d == nil {
		return
	}
	var req struct {
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil || strings.TrimSpace(req.CertPEM) == "" || strings.TrimSpace(req.KeyPEM) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cert_pem and key_pem required"})
		return
	}
	h.setCert(w, r, p, d, req.CertPEM, req.KeyPEM)
}

// DeleteCertificate removes it: the installation's way issues one again.
func (h *TenantSelf) DeleteCertificate(w http.ResponseWriter, r *http.Request) {
	p, _ := h.target(w, r)
	if p == nil || h.refuseDemo(w) {
		return
	}
	d := h.ownDomain(w, r, p)
	if d == nil {
		return
	}
	h.setCert(w, r, p, d, "", "")
}

func (h *TenantSelf) setCert(w http.ResponseWriter, r *http.Request, p *model.Provider, d *model.ProviderDomain, certPEM, keyPEM string) {
	ctx := r.Context()
	var box *secretbox.Box
	if h.Live != nil {
		box = h.Live.Options().Box
	}
	if err := tenantdomain.SetCertificate(ctx, h.Store, box, d, certPEM, keyPEM, time.Now().UTC()); err != nil {
		if !domainRefusal(w, err) {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	auth.SetAuditTarget(ctx, strconv.FormatInt(d.ID, 10), d.Domain)
	auth.AddAuditDetail(ctx, "own_certificate", certPEM != "")
	fresh, _ := h.Store.GetProviderDomainByID(ctx, d.ID)
	if fresh == nil {
		fresh = d
	}
	writeJSON(w, http.StatusOK, domainView{ProviderDomain: fresh, Target: db.PlatformSubdomain(p), OwnCertificate: fresh.HasOwnCertificate()})
}
