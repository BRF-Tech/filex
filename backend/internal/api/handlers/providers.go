package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// Providers handles /api/admin/providers — the tenant lifecycle API
// (docs/MULTI-TENANCY.md §8, §11): create/suspend/delete tenants, link
// storages, transfer the supertenant flag.
//
// Guards enforced here (not scattered):
//   - only a supertenant admin may manage providers in multi-tenant mode;
//   - at most ONE supertenant — setting the flag on another provider is a
//     TRANSFER (the old supertenant becomes a regular tenant atomically);
//   - the supertenant cannot be deleted, disabled, or directly un-flagged;
//   - deleting a tenant with users requires ?force=1 and then cascades its
//     users and storage LINKS. Storage rows and file data are never touched —
//     unlinking is reversible, deleting files is not;
//   - a tenant's REALM is given when it is created (the slug by default) and
//     never changes: an update that names another realm is refused
//     (realm_immutable), and the store never writes the column again.
type Providers struct {
	Store       db.Store
	MultiTenant bool
	// DemoMode marks a public playground: every write is refused here too.
	// api.DemoGuard already refuses writes under /api/admin and /api/ai/admin,
	// but the admin MCP tools call this handler in-process and never pass that
	// middleware, so the handler holds its own lock.
	DemoMode bool
}

// NewProviders constructs the handler.
func NewProviders(store db.Store, multiTenant bool) *Providers {
	return &Providers{Store: store, MultiTenant: multiTenant}
}

// requireSupertenant gates management: in multi-tenant mode only the
// supertenant's admins pass (the route group already requires admin); in
// single-tenant mode every admin passes (no scope is set).
//
// The decision itself lives in handlers/supertenant.go — one predicate for
// every instance-wide admin surface, so there is a single place to read what
// "instance-wide" is gated on and a single place to get it wrong.
func (h *Providers) requireSupertenant(w http.ResponseWriter, r *http.Request) bool {
	return requireSupertenant(w, r, "tenants are managed by the platform operator")
}

// mayWrite is requireSupertenant for a change: it also refuses every write on
// a public demo (see DemoMode).
func (h *Providers) mayWrite(w http.ResponseWriter, r *http.Request) bool {
	if !h.requireSupertenant(w, r) {
		return false
	}
	if h.DemoMode {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "demo_read_only"})
		return false
	}
	return true
}

type providerReq struct {
	Slug *string `json:"slug"`
	// Realm is read on CREATE only (default: the slug). On an update it may be
	// sent back unchanged — the panel round-trips the object — and anything
	// else is refused: see realmChange.
	Realm            *string `json:"realm"`
	Name             *string `json:"name"`
	Host             *string `json:"host"`
	AuthType         *string `json:"auth_type"`
	OIDCIssuer       *string `json:"oidc_issuer"`
	OIDCClientID     *string `json:"oidc_client_id"`
	OIDCClientSecret *string `json:"oidc_client_secret"`
	OIDCRedirectURL  *string `json:"oidc_redirect_url"`
	RoleClaim        *string `json:"role_claim"`
	AdminGroup       *string `json:"admin_group"`
	CookieDomain     *string `json:"cookie_domain"`
	IsSupertenant    *bool   `json:"is_supertenant"`
	Enabled          *bool   `json:"enabled"`
	// OIDCTrustEmail is the tenant's own OIDC's "trust this provider's email
	// addresses" (docs/SSO.md): written by its own statement
	// (setTrustEmail), never by UpdateProvider.
	OIDCTrustEmail *bool `json:"oidc_trust_email"`
}

// setTrustEmail writes the trust setting when the request carries it, and
// records it in the request's audit row; a value saved by somebody is no
// longer the upgrade's (authsetup.ForgetTrustUpgradeTenant).
func (h *Providers) setTrustEmail(r *http.Request, p *model.Provider, req *providerReq) error {
	if req.OIDCTrustEmail == nil {
		return nil
	}
	if err := h.Store.SetProviderOIDCTrustEmail(r.Context(), p.ID, *req.OIDCTrustEmail); err != nil {
		return err
	}
	authsetup.ForgetTrustUpgradeTenant(r.Context(), h.Store, p.ID)
	if *req.OIDCTrustEmail != p.OIDCTrustEmail {
		auth.AddAuditDetail(r.Context(), "oidc_trust_email_before", p.OIDCTrustEmail)
		auth.AddAuditDetail(r.Context(), "oidc_trust_email_after", *req.OIDCTrustEmail)
	}
	return nil
}

func (req *providerReq) apply(p *model.Provider) {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = strings.TrimSpace(*src)
		}
	}
	set(&p.Slug, req.Slug)
	set(&p.Name, req.Name)
	set(&p.Host, req.Host)
	set(&p.AuthType, req.AuthType)
	set(&p.OIDCIssuer, req.OIDCIssuer)
	set(&p.OIDCClientID, req.OIDCClientID)
	set(&p.OIDCRedirectURL, req.OIDCRedirectURL)
	set(&p.RoleClaim, req.RoleClaim)
	set(&p.AdminGroup, req.AdminGroup)
	set(&p.CookieDomain, req.CookieDomain)
	if req.OIDCClientSecret != nil && *req.OIDCClientSecret != "" {
		// Empty secret in the payload means "keep the stored one" so the UI can
		// round-trip the (never-serialized) secret without re-entering it.
		p.OIDCClientSecret = *req.OIDCClientSecret
	}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
}

type providerOut struct {
	*model.Provider
	StorageIDs []int64 `json:"storage_ids"`
	UserCount  int     `json:"user_count"`
	// OIDCClientSecretSet says whether the tenant's OIDC client secret is
	// stored. The secret itself is never sent (model.Provider has it as `-`),
	// so a form can say "set - type a new one to replace it".
	OIDCClientSecretSet bool `json:"oidc_client_secret_set"`
}

func (h *Providers) out(r *http.Request, p *model.Provider) providerOut {
	o := providerOut{Provider: p, OIDCClientSecretSet: p.OIDCClientSecret != ""}
	p.OIDCTrustEmailByUpgrade = p.OIDCTrustEmail && authsetup.ReadTrustUpgrade(r.Context(), h.Store).Tenant(p.ID)
	o.StorageIDs, _ = h.Store.ListProviderStorageIDs(r.Context(), p.ID)
	if users, err := h.Store.ListUsersByProvider(r.Context(), p.ID); err == nil {
		o.UserCount = len(users)
	}
	return o
}

// List returns every provider (tenant) with its storage links + user count.
func (h *Providers) List(w http.ResponseWriter, r *http.Request) {
	if !h.requireSupertenant(w, r) {
		return
	}
	list, err := h.Store.ListProviders(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]providerOut, 0, len(list))
	for _, p := range list {
		out = append(out, h.out(r, p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out, "multi_tenant": h.MultiTenant})
}

// Get returns one provider (tenant) with its storage links and user count.
func (h *Providers) Get(w http.ResponseWriter, r *http.Request) {
	if !h.requireSupertenant(w, r) {
		return
	}
	p := h.load(w, r)
	if p == nil {
		return
	}
	writeJSON(w, http.StatusOK, h.out(r, p))
}

// realmSuggestionTries bounds how many `-2`, `-3`, … variants RealmSuggestion
// looks at before it gives up on a free realm.
const realmSuggestionTries = 20

// RealmSuggestion answers GET /api/admin/providers/realm-suggestion?slug= with
// the realm the tenant screen offers for a tenant being created
// (tenant.SuggestRealm), the first variant of it that is valid, not reserved
// and not taken: {realm, base, available}. `realm` is "" when the slug holds
// nothing realm-shaped. Nothing is reserved by asking: the realm is settled
// by POST, and a second tenant created in between is refused there
// (`realm_taken`).
//
// ⚠ One rule for every client: the screen and the MCP tool ask here rather
// than each carrying its own transliteration.
func (h *Providers) RealmSuggestion(w http.ResponseWriter, r *http.Request) {
	if !h.requireSupertenant(w, r) {
		return
	}
	slug := r.URL.Query().Get("slug")
	base := tenant.SuggestRealm(slug)
	out := map[string]any{"realm": "", "base": base, "available": false}
	for _, c := range tenant.RealmCandidates(slug, realmSuggestionTries) {
		if tenant.CheckRealm(c) != nil {
			continue
		}
		taken, err := h.Store.GetProviderByRealm(r.Context(), c)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if taken == nil {
			out["realm"], out["available"] = c, true
			break
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// Create provisions a tenant.
func (h *Providers) Create(w http.ResponseWriter, r *http.Request) {
	if !h.mayWrite(w, r) {
		return
	}
	var req providerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	p := &model.Provider{AuthType: model.AuthTypeOIDC, Enabled: true}
	req.apply(p)
	if p.Slug == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "slug required", "field": "slug"})
		return
	}
	if !h.freeSlugAndHost(w, r, p) {
		return
	}
	if req.IsSupertenant != nil && *req.IsSupertenant {
		// The platform's own tenant has no realm: an empty realm is how a
		// person signs in to it. Created flagged, so the store leaves the
		// column empty; transferSupertenant below un-flags the previous one.
		p.IsSupertenant = true
	} else if !h.newRealm(w, r, &req, p) {
		return
	}
	created, err := h.Store.CreateProvider(r.Context(), p)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(created.ID, 10), created.Slug)
	if err := h.setTrustEmail(r, created, &req); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if req.IsSupertenant != nil && *req.IsSupertenant {
		if err := h.transferSupertenant(r, created); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	fresh, _ := h.Store.GetProvider(r.Context(), created.ID)
	if fresh == nil {
		fresh = created
	}
	writeJSON(w, http.StatusCreated, h.out(r, fresh))
}

// Update edits a tenant; flag changes go through the transfer guard.
func (h *Providers) Update(w http.ResponseWriter, r *http.Request) {
	if !h.mayWrite(w, r) {
		return
	}
	p := h.load(w, r)
	if p == nil {
		return
	}
	var req providerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	auth.SetAuditTarget(r.Context(), "", p.Slug)
	if p.IsSupertenant {
		if req.IsSupertenant != nil && !*req.IsSupertenant {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot un-flag the supertenant - transfer it by setting is_supertenant on another provider"})
			return
		}
		if req.Enabled != nil && !*req.Enabled {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot disable the supertenant"})
			return
		}
	}
	if realmChange(&req, p) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "realm_immutable",
			"field":   "realm",
			"message": "a tenant's realm is chosen when it is created and never changes: it is part of its accounts' addresses and of the user names its people saved in their clients",
		})
		return
	}
	req.apply(p)
	if p.Slug == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "slug required", "field": "slug"})
		return
	}
	if !h.freeSlugAndHost(w, r, p) {
		return
	}
	if err := h.Store.UpdateProvider(r.Context(), p); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err := h.setTrustEmail(r, p, &req); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if req.IsSupertenant != nil && *req.IsSupertenant && !p.IsSupertenant {
		if err := h.transferSupertenant(r, p); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	fresh, _ := h.Store.GetProvider(r.Context(), p.ID)
	if fresh == nil {
		fresh = p
	}
	writeJSON(w, http.StatusOK, h.out(r, fresh))
}

// freeSlugAndHost refuses a slug or a host another provider already has
// (409 slug_taken / host_taken, with the field). p is the row about to be
// written; its own id is skipped, so an update that keeps them passes.
//
// ⚠ The host is the one that matters: providers.host carries no unique index
// (migration 00014), and GetProviderByHost answers ONE row, so a second tenant
// on the same address would make sign-ins, cookies and minted links of one
// tenant land in the other, depending on which row the database returns
// first. A disabled tenant's host counts too: resuming it must not create the
// clash.
func (h *Providers) freeSlugAndHost(w http.ResponseWriter, r *http.Request, p *model.Provider) bool {
	all, err := h.Store.ListProviders(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return false
	}
	host := strings.ToLower(strings.TrimSpace(p.Host))
	for _, o := range all {
		if o == nil || o.ID == p.ID {
			continue
		}
		if strings.EqualFold(o.Slug, p.Slug) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "slug_taken", "field": "slug", "message": "another tenant already has this slug"})
			return false
		}
		if host != "" && strings.EqualFold(strings.TrimSpace(o.Host), host) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "host_taken", "field": "host", "message": "another tenant already has this address"})
			return false
		}
	}
	return true
}

// newRealm settles the realm of a tenant about to be created: the one asked
// for, else the slug, validated (tenant.CheckRealm) and free. It writes the
// refusal and answers false when there is one.
func (h *Providers) newRealm(w http.ResponseWriter, r *http.Request, req *providerReq, p *model.Provider) bool {
	realm := p.Slug
	if req.Realm != nil && strings.TrimSpace(*req.Realm) != "" {
		realm = *req.Realm
	}
	realm = tenant.NormalizeRealm(realm)
	if err := tenant.CheckRealm(realm); err != nil {
		reason := "realm_invalid"
		switch {
		case errors.Is(err, tenant.ErrRealmReserved):
			reason = "realm_reserved"
		case errors.Is(err, tenant.ErrRealmEmpty):
			reason = "realm_empty"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": reason, "field": "realm", "message": err.Error()})
		return false
	}
	if taken, err := h.Store.GetProviderByRealm(r.Context(), realm); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return false
	} else if taken != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "realm_taken", "field": "realm", "message": "another tenant already has this realm"})
		return false
	}
	p.Realm = realm
	return true
}

// realmChange reports whether an update asks for a realm other than the
// tenant's own. Sending the same realm back (the panel round-trips the whole
// object) is not a change.
func realmChange(req *providerReq, p *model.Provider) bool {
	if req.Realm == nil {
		return false
	}
	return tenant.NormalizeRealm(*req.Realm) != tenant.NormalizeRealm(p.Realm)
}

// transferSupertenant moves the platform flag to p, un-flagging the previous
// holder — the only way the flag moves, preserving the at-most-one invariant.
func (h *Providers) transferSupertenant(r *http.Request, p *model.Provider) error {
	prev, err := h.Store.GetSupertenant(r.Context())
	if err != nil {
		return err
	}
	if prev != nil && prev.ID != p.ID {
		prev.IsSupertenant = false
		if err := h.Store.UpdateProvider(r.Context(), prev); err != nil {
			return err
		}
	}
	p.IsSupertenant = true
	return h.Store.UpdateProvider(r.Context(), p)
}

// Delete removes a tenant. Users require ?force=1 (then cascade); storage
// LINKS are removed but storage rows + file data are never touched.
func (h *Providers) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.mayWrite(w, r) {
		return
	}
	p := h.load(w, r)
	if p == nil {
		return
	}
	// The row is gone by the time anybody reads the log: its name goes with it.
	auth.SetAuditTarget(r.Context(), "", p.Slug)
	if p.IsSupertenant {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot delete the supertenant"})
		return
	}
	users, err := h.Store.ListUsersByProvider(r.Context(), p.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(users) > 0 && r.URL.Query().Get("force") != "1" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "tenant still has users - pass ?force=1 to delete them too",
			"user_count": len(users),
		})
		return
	}
	// Each account's own public links go with it (db.Store.DeleteUser); the
	// audit row says how many were still open.
	var linksClosed int64
	for _, u := range users {
		closed, err := h.Store.DeleteUserWithLinks(r.Context(), u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		linksClosed += closed
	}
	auth.AddAuditDetail(r.Context(), "links_closed", linksClosed)
	ids, _ := h.Store.ListProviderStorageIDs(r.Context(), p.ID)
	for _, sid := range ids {
		_ = h.Store.UnlinkProviderStorage(r.Context(), p.ID, sid)
	}
	if err := h.Store.DeleteProvider(r.Context(), p.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_users": len(users)})
}

// LinkStorage links a storage to the tenant (POST {storage_id}).
func (h *Providers) LinkStorage(w http.ResponseWriter, r *http.Request) {
	if !h.mayWrite(w, r) {
		return
	}
	p := h.load(w, r)
	if p == nil {
		return
	}
	var req struct {
		StorageID int64 `json:"storage_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.StorageID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "storage_id required"})
		return
	}
	if _, err := h.Store.GetStorage(r.Context(), req.StorageID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown storage"})
		return
	}
	if err := h.Store.LinkProviderStorage(r.Context(), p.ID, req.StorageID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	auth.SetAuditTarget(r.Context(), "", p.Slug)
	auth.AddAuditDetail(r.Context(), "storage_id", req.StorageID)
	writeJSON(w, http.StatusOK, h.out(r, p))
}

// UnlinkStorage removes a storage link (never the storage itself).
func (h *Providers) UnlinkStorage(w http.ResponseWriter, r *http.Request) {
	if !h.mayWrite(w, r) {
		return
	}
	p := h.load(w, r)
	if p == nil {
		return
	}
	sid, err := strconv.ParseInt(chi.URLParam(r, "storageID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage id"})
		return
	}
	if err := h.Store.UnlinkProviderStorage(r.Context(), p.ID, sid); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	auth.SetAuditTarget(r.Context(), "", p.Slug)
	auth.AddAuditDetail(r.Context(), "storage_id", sid)
	writeJSON(w, http.StatusOK, h.out(r, p))
}

// load parses {id} and fetches the provider (writing the error response).
func (h *Providers) load(w http.ResponseWriter, r *http.Request) *model.Provider {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return nil
	}
	p, err := h.Store.GetProvider(r.Context(), id)
	if err != nil || p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "provider not found"})
		return nil
	}
	return p
}
