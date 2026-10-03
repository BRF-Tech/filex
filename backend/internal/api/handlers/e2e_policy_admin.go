// Package handlers — e2e_policy_admin.go
//
// Who may encrypt (internal/e2epolicy), the administrator's half:
//
//	GET   /api/admin/e2e               — the policy the caller administers
//	PATCH /api/admin/e2e               — SESSION ONLY: {"policy": "off|admins|permitted|approval"}
//	GET   /api/admin/e2e/tenants       — SUPERTENANT: every tenant's ceiling and policy
//	PATCH /api/admin/e2e/tenants/{id}  — SUPERTENANT, SESSION ONLY: {"e2e_allowed": bool}
//	GET   /api/admin/e2e/requests[?status=pending|approved|rejected|expired|used|all]
//	POST  /api/admin/e2e/requests/{id}/approve — SESSION ONLY: {"note"?: "…"}
//	POST  /api/admin/e2e/requests/{id}/reject  — SESSION ONLY: {"reason"?: "…"}
//
// A tenant administrator lists and decides their own tenant's requests only;
// another tenant's id answers 404, like an id nobody has (tenantown.go).
//
// Whose policy "the caller administers" is decided by the tenant scope
// auth.TenantResolver attached, never by the request: a tenant administrator
// holds their own tenant's row, the supertenant's administrator the
// supertenant's own row — another tenant's policy is that tenant's decision;
// the operator's lever is the ceiling — and a single-tenant install's
// administrator the instance setting `e2e.policy`.
//
// ⚠ A change is a person's (sessionOnly): an admin-scoped API key may read
// the policy, not change it. The policy decides who may make data readable
// only by its key holders, and the ceiling is the operator's.
//
// ⚠ requireSupertenant and sessionOnly are called in the handler, never as a
// route middleware — the reasoning of supertenant.go.
//
// The audit rows are written here through e2epolicy.Audit, not by the audit
// middleware (auth.ActionForPath skips /api/admin/e2e): one change is one row
// that names the tenant and says what the value was and what it became.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// E2EPolicyAdmin is the handler set.
type E2EPolicyAdmin struct {
	Store       db.Store
	Policy      *e2epolicy.Service
	MultiTenant bool
	// Requests decides the approval policy's requests (e2e_policy_files.go
	// leaves them).
	Requests *e2epolicy.Requests
}

// NewE2EPolicyAdmin constructs the handler.
func NewE2EPolicyAdmin(store db.Store, policy *e2epolicy.Service, multiTenant bool) *E2EPolicyAdmin {
	return &E2EPolicyAdmin{Store: store, Policy: policy, MultiTenant: multiTenant}
}

// e2ePolicyView is the answer of GET and PATCH /api/admin/e2e.
type e2ePolicyView struct {
	// Available is the tenant's ceiling (always true on a single-tenant
	// install): false means nobody there may start encrypting, whatever the
	// policy says.
	Available bool   `json:"available"`
	Policy    string `json:"policy"`
	// Scope: "tenant" (a tenant's row, the supertenant's included) or
	// "instance" (the setting of a single-tenant install).
	Scope  string        `json:"scope"`
	Tenant *e2eTenantRef `json:"tenant"`
	// Pending counts the requests waiting for this administrator: their own
	// tenant's, or everybody's for the supertenant and a single-tenant install.
	Pending int `json:"pending"`
}

type e2eTenantRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// e2eTenantRow is one row of GET /api/admin/e2e/tenants, and PATCH's answer.
type e2eTenantRow struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	IsSupertenant bool   `json:"is_supertenant"`
	E2EAllowed    bool   `json:"e2e_allowed"`
	E2EPolicy     string `json:"e2e_policy"`
}

// appliedPolicy is the policy the rule applies for a stored value: an unknown
// or missing one reads as `permitted` there (e2epolicy.Decide), so the page
// shows that rather than a value no choice matches.
func appliedPolicy(p string) string {
	if model.ValidE2EPolicy(p) {
		return p
	}
	return model.E2EPolicyPermitted
}

// writeInvalidE2EPolicy is the refusal for a policy the rule does not know —
// PATCH /api/admin/e2e and the generic settings API (settings.go) answer it
// alike.
func writeInvalidE2EPolicy(w http.ResponseWriter) {
	writeJSON(w, http.StatusBadRequest, map[string]string{
		"error":   "invalid_policy",
		"message": "policy must be one of off, admins, permitted, approval",
	})
}

// e2eSessionOnly refuses an API key a change to who may encrypt.
func e2eSessionOnly(w http.ResponseWriter, r *http.Request, what string) bool {
	return sessionOnly(w, r, what+" needs an administrator signed in to the admin panel; an API key cannot do it.", nil)
}

// e2eActorOf is who is changing or deciding, for the audit row.
func e2eActorOf(r *http.Request) e2epolicy.Actor {
	a := e2epolicy.Actor{IP: clientIP(r)}
	if u := auth.UserFrom(r.Context()); u != nil && u.ID > 0 {
		id := u.ID
		a.UserID, a.Name = &id, u.Label()
	}
	if tok := auth.TokenFrom(r.Context()); tok != nil {
		id := tok.ID
		a.TokenID = &id
	}
	return a
}

// e2eRequestTenant is whose encryption requests the caller administers: their
// own tenant's for a tenant administrator, everybody's (nil) for the
// supertenant and on a single-tenant install.
func e2eRequestTenant(ctx context.Context) *int64 {
	if s, confined := confinedScope(ctx); confined {
		id := s.ProviderID
		return &id
	}
	return nil
}

// adminTenant says whose policy this administrator holds: (nil, true) for the
// instance's (no tenant scope — single-tenant mode), (&id, true) for the
// tenant the scope names — their own, or the supertenant's own row — and
// (nil, false), with a 403 written, for a scope that names no tenant
// (tenant.DenyAll: a broken tenancy record reaches nothing, here too).
func (h *E2EPolicyAdmin) adminTenant(w http.ResponseWriter, r *http.Request) (*int64, bool) {
	scope, ok := tenant.FromContext(r.Context())
	if !ok {
		return nil, true
	}
	if scope == nil || scope.ProviderID <= 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "no_tenant", "message": "this account belongs to no tenant",
		})
		return nil, false
	}
	id := scope.ProviderID
	return &id, true
}

// pending counts the requests waiting for this administrator. A request past
// its expiry is not counted, whether or not the hourly sweep has closed it.
func (h *E2EPolicyAdmin) pending(ctx context.Context) (int, error) {
	rows, err := h.Store.ListE2ERequests(ctx, model.E2ERequestFilter{
		ProviderID: e2eRequestTenant(ctx), Status: model.E2ERequestPending, Limit: 500,
	})
	if err != nil {
		return 0, err
	}
	now, n := time.Now(), 0
	for _, row := range rows {
		if row.ExpiresAt.After(now) {
			n++
		}
	}
	return n, nil
}

// view is the policy this administrator holds, as GET and PATCH answer it.
func (h *E2EPolicyAdmin) view(ctx context.Context, providerID *int64) (e2ePolicyView, error) {
	pe, err := h.Policy.PolicyFor(ctx, providerID)
	if err != nil {
		return e2ePolicyView{}, err
	}
	// PolicyFor reads an unknown stored policy as the default already.
	v := e2ePolicyView{Available: pe.Allowed, Policy: pe.Policy, Scope: "instance"}
	if providerID != nil {
		p, err := h.Store.GetProvider(ctx, *providerID)
		if err != nil {
			return e2ePolicyView{}, err
		}
		v.Scope = "tenant"
		v.Tenant = &e2eTenantRef{ID: p.ID, Name: p.Name}
	}
	if v.Pending, err = h.pending(ctx); err != nil {
		return e2ePolicyView{}, err
	}
	return v, nil
}

// Get answers the policy this administrator holds: GET /api/admin/e2e.
func (h *E2EPolicyAdmin) Get(w http.ResponseWriter, r *http.Request) {
	pid, ok := h.adminTenant(w, r)
	if !ok {
		return
	}
	v, err := h.view(r.Context(), pid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// Patch sets the policy this administrator holds: PATCH /api/admin/e2e
// {"policy": "…"}. Session only. A tenant whose ceiling is off may still
// choose one: it applies the day the operator switches the ceiling on.
func (h *E2EPolicyAdmin) Patch(w http.ResponseWriter, r *http.Request) {
	if !e2eSessionOnly(w, r, "changing who may encrypt") {
		return
	}
	pid, ok := h.adminTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		Policy *string `json:"policy"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || body.Policy == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "bad_request", "message": `send {"policy": "off|admins|permitted|approval"}`,
		})
		return
	}
	policy := strings.TrimSpace(*body.Policy)
	if !model.ValidE2EPolicy(policy) {
		writeInvalidE2EPolicy(w)
		return
	}
	// The tenant, and the name its audit row carries, is read before anything
	// is written: a tenant that is gone is a 404 with nothing stored.
	var tenant *e2eTenantRef
	if pid != nil {
		p, ok := h.lookupTenant(w, r, *pid)
		if !ok {
			return
		}
		tenant = &e2eTenantRef{ID: p.ID, Name: p.Name}
	}
	if err := setE2EPolicy(r, h.Store, tenant, policy); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed", "message": err.Error()})
		return
	}
	// Stored and recorded: a read back that fails now answers 500, and the
	// change it could not show is in the audit log all the same.
	v, err := h.view(r.Context(), pid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// setE2EPolicy stores the policy an administrator holds (the tenant's the
// reference names, the instance's for nil) and records the change: one
// e2e_policy.update row naming the tenant (none for the instance's policy)
// with the value before and after, none when the value did not change, and
// then nothing is written either (writeE2EPolicy).
// PATCH /api/admin/e2e and the settings API's e2e.policy (settings.go) both
// write it here, so a change leaves the same row whichever door it came in by.
//
// The row is written as soon as the value is stored, before anything is read
// back, so no failure after the write leaves the change unaudited; and
// e2epolicy.Audit writes it on a context the request's end does not cancel.
func setE2EPolicy(r *http.Request, store db.Store, tenant *e2eTenantRef, policy string) error {
	var pid *int64
	if tenant != nil {
		id := tenant.ID
		pid = &id
	}
	before, err := writeE2EPolicy(r.Context(), store, pid, policy)
	if err != nil {
		return err
	}
	if before == policy {
		return nil
	}
	row := e2epolicy.AuditRow{
		Action: e2epolicy.AuditActionPolicyUpdate, TargetType: e2epolicy.AuditTargetPolicy,
		Who: e2eActorOf(r), Meta: map[string]any{"before": before, "after": policy},
	}
	if tenant != nil {
		row.TargetID, row.TargetName = strconv.FormatInt(tenant.ID, 10), tenant.Name
	}
	e2epolicy.Audit(r.Context(), store, row)
	return nil
}

// writeE2EPolicy stores the policy and answers the one it replaced (as
// applied). Only the policy column is written: a tenant administrator does not
// hold the ceiling beside it, and a write that named both would put back a
// ceiling the operator switched off, on another instance, since it was read.
//
// The policy already in force is not written. setE2EPolicy records a change
// only when the value differs from the one read here, so a write that changes
// nothing would be one no audit row accounts for. With several filex instances
// on one database it could also be a stale save of the value it read, putting
// that value back over another instance's change of the same column, unseen.
// (A stored value nobody can choose applies as `permitted`, so saving
// `permitted` over it is no change either.)
func writeE2EPolicy(ctx context.Context, store db.Store, providerID *int64, policy string) (string, error) {
	if providerID == nil {
		// A missing setting is the default (`permitted`), as PolicyFor reads
		// it. Any other failure to read it is an error: taken for the default,
		// it would store an `off` → `permitted` change as no change at all,
		// with no audit row.
		stored, err := store.GetSetting(ctx, model.SettingE2EPolicy)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		before := appliedPolicy(stored)
		if before == policy {
			return before, nil
		}
		return before, store.UpsertSetting(ctx, model.SettingE2EPolicy, policy)
	}
	cur, err := store.GetProviderE2E(ctx, *providerID)
	if err != nil {
		return "", err
	}
	before := appliedPolicy(cur.Policy)
	if before == policy {
		return before, nil
	}
	return before, store.SetProviderE2EPolicy(ctx, *providerID, policy)
}

// lookupTenant reads the tenant a change is about, before anything is
// written. A tenant that is not there answers 404. One the database could not
// read answers 500 and is logged: a 404 would say the tenant is gone when it
// was the read that failed.
func (h *E2EPolicyAdmin) lookupTenant(w http.ResponseWriter, r *http.Request, id int64) (*model.Provider, bool) {
	p, err := h.Store.GetProvider(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && p == nil) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "no such tenant"})
		return nil, false
	}
	if err != nil {
		slog.Error("e2e policy: could not read a tenant", slog.Int64("tenant_id", id), slog.String("err", err.Error()))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed", "message": err.Error()})
		return nil, false
	}
	return p, true
}

// tenantRow is one tenant with its ceiling and policy.
func (h *E2EPolicyAdmin) tenantRow(ctx context.Context, p *model.Provider) (e2eTenantRow, error) {
	pe, err := h.Store.GetProviderE2E(ctx, p.ID)
	if err != nil {
		return e2eTenantRow{}, err
	}
	return e2eTenantRow{
		ID: p.ID, Slug: p.Slug, Name: p.Name, IsSupertenant: p.IsSupertenant,
		E2EAllowed: pe.Allowed, E2EPolicy: appliedPolicy(pe.Policy),
	}, nil
}

// e2eCeilingOperator is requireSupertenant's sentence for the ceiling.
const e2eCeilingOperator = "whether a tenant may use encryption at all is the platform operator's decision"

// Tenants answers every tenant's ceiling and policy:
// GET /api/admin/e2e/tenants. Supertenant only.
func (h *E2EPolicyAdmin) Tenants(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, e2eCeilingOperator) {
		return
	}
	list, err := h.Store.ListProviders(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed", "message": err.Error()})
		return
	}
	rows := make([]e2eTenantRow, 0, len(list))
	for _, p := range list {
		row, err := h.tenantRow(r.Context(), p)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed", "message": err.Error()})
			return
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": rows, "multi_tenant": h.MultiTenant})
}

// PatchTenant switches a tenant's ceiling: PATCH /api/admin/e2e/tenants/{id}
// {"e2e_allowed": false}. Supertenant only, session only, multi-tenant only.
// Off, nobody in that tenant may start encrypting — its administrators
// included; what is already encrypted keeps working.
func (h *E2EPolicyAdmin) PatchTenant(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, e2eCeilingOperator) || !e2eSessionOnly(w, r, "changing a tenant's encryption ceiling") {
		return
	}
	// The rule reads a tenant's ceiling in multi-tenant mode only. On a
	// single-tenant install a stored `false` would do nothing today, then
	// switch encryption off for the supertenant the day multi-tenant mode is
	// turned on.
	if !h.MultiTenant {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "single_tenant", "message": srvtext.Text(langOf(r), "server.e2e.ceiling.single_tenant", nil),
		})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "bad id"})
		return
	}
	p, ok := h.lookupTenant(w, r, id)
	if !ok {
		return
	}
	var body struct {
		Allowed *bool `json:"e2e_allowed"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || body.Allowed == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": `send {"e2e_allowed": true|false}`})
		return
	}
	before, err := h.writeCeiling(r.Context(), id, *body.Allowed)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed", "message": err.Error()})
		return
	}
	if before != *body.Allowed {
		e2epolicy.Audit(r.Context(), h.Store, e2epolicy.AuditRow{
			Action: e2epolicy.AuditActionTenantUpdate, TargetType: e2epolicy.AuditTargetTenant,
			TargetID: strconv.FormatInt(id, 10), TargetName: p.Name, Who: e2eActorOf(r),
			Meta: map[string]any{"before": before, "after": *body.Allowed},
		})
	}
	row, err := h.tenantRow(r.Context(), p)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// writeCeiling stores a tenant's ceiling and answers the one it replaced. Only
// the ceiling column is written: the policy beside it is the tenant's, so this
// neither puts back a policy the tenant changed since it was read nor refuses
// a stored policy it does not know.
//
// A ceiling that already is what was asked for is not written, for the reason
// writeE2EPolicy gives: PatchTenant records a change only when the value
// differs from the one read here, and a stale save of the value it read would
// put it back over another instance's switch, unseen.
func (h *E2EPolicyAdmin) writeCeiling(ctx context.Context, providerID int64, allowed bool) (bool, error) {
	cur, err := h.Store.GetProviderE2E(ctx, providerID)
	if err != nil {
		return false, err
	}
	if cur.Allowed == allowed {
		return cur.Allowed, nil
	}
	return cur.Allowed, h.Store.SetProviderE2EAllowed(ctx, providerID, allowed)
}

// ListRequests answers the requests this administrator decides:
// GET /api/admin/e2e/requests[?status=…]. Pending by default.
func (h *E2EPolicyAdmin) ListRequests(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Requests.List(r.Context(), e2eRequestTenant(r.Context()), strings.TrimSpace(r.URL.Query().Get("status")))
	if err != nil {
		writeE2ERequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"requests": e2eRequestViews(r.Context(), h.Store, rows, nil),
		"ttl_days": e2epolicy.TTLDays(),
	})
}

// ApproveRequest opens one encryption for the requester at the place asked:
// POST /api/admin/e2e/requests/{id}/approve. Session only.
func (h *E2EPolicyAdmin) ApproveRequest(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, true)
}

// RejectRequest closes a request: POST /api/admin/e2e/requests/{id}/reject.
// Session only.
func (h *E2EPolicyAdmin) RejectRequest(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, false)
}

func (h *E2EPolicyAdmin) decide(w http.ResponseWriter, r *http.Request, approve bool) {
	what := "rejecting an encryption request"
	if approve {
		what = "approving an encryption request"
	}
	if !e2eSessionOnly(w, r, what) {
		return
	}
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	// An approval carries a note, a rejection its reason; either key is read
	// for either, so a client that sends the other one is not ignored.
	var body struct {
		Note   string `json:"note"`
		Reason string `json:"reason"`
	}
	if !decodeOptionalJSON(w, r, &body) {
		return
	}
	note := body.Note
	if note == "" {
		note = body.Reason
	}
	req, err := h.Requests.Decide(r.Context(), e2epolicy.Decision{
		ID: id, Approve: approve, Tenant: e2eRequestTenant(r.Context()), Note: note, Who: e2eActorOf(r),
	})
	if err != nil {
		writeE2ERequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"request": e2eRequestViews(r.Context(), h.Store, []*model.E2ERequest{req}, nil)[0],
	})
}
