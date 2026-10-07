// Package handlers — notification_digest.go
//
// The notification digest's HTTP half (internal/notify digest.go):
//
//	GET   /api/notifications/settings       — the person's own urgent choices beside their mutes (notifications.go)
//	PATCH /api/notifications/settings       — {urgent_overrides: {"<event>": bool}}; absent keeps them
//	GET   /api/admin/notifications/digest   — the defaults the caller administers
//	PATCH /api/admin/notifications/digest   — {window_minutes: 1-15, urgent_events: [...] | null}
//
// and the View the digest's background pass judges a person's rows through
// (DigestViewer): the bell this handler gives them, built without a request.
//
// Whose defaults an administrator holds is decided by the tenant scope
// auth.TenantResolver attached, never by the request — the rule of Admin →
// Encryption (e2e_policy_admin.go): a tenant's administrator holds their own
// tenant's, the supertenant's administrator the supertenant's, a
// single-tenant install's administrator the instance's (scope 0).
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// digests is the service's digest half, nil when it has none (the digest is
// off, or a test double stands in for the service).
func (h *Notifications) digests() notify.Digests {
	if d, ok := h.Service.(notify.Digests); ok {
		return d
	}
	return nil
}

// personView is the person's whole bell as the digest reads it: their bell's
// broadcasts and the per-row pass — never a folder-confined token's part of
// it (a digest is the person's, and one made from a part would tell the rest
// as nothing).
func (h *Notifications) personView(ctx context.Context, user *model.User) notify.View {
	bell := bellFor(ctx, user)
	if bell.Unconfined() {
		return notify.View{Bell: bell}
	}
	j := h.judge(ctx, user, bell)
	j.rooted = false
	return notify.View{Bell: bell, Keep: j.keep}
}

// DigestViewer is the notify.Viewer for the digest's background pass: the
// person's account, their tenant on the context (UserScope, multi-tenant),
// and the bell this handler would give them.
func (h *Notifications) DigestViewer(ctx context.Context, userID int64) (notify.View, error) {
	if h.Store == nil {
		return notify.View{}, errors.New("notifications: no store")
	}
	u, err := h.Store.GetUser(ctx, userID)
	if err != nil {
		return notify.View{}, err
	}
	if u == nil {
		return notify.View{}, sql.ErrNoRows
	}
	if h.UserScope != nil {
		ctx = h.UserScope(ctx, u)
	}
	return h.personView(ctx, u), nil
}

// settingsAnswer is GET and PATCH /api/notifications/settings: the stored
// preference, and — when the digest is on — what the settings pane draws of
// it (`digest`: the window, the kinds told at once, the administrator's
// defaults and the catalogue a choice is made from). `digest` is null when the
// digest is off.
func (h *Notifications) settingsAnswer(ctx context.Context, userID int64, st *model.NotificationSettings) map[string]any {
	overrides := st.UrgentOverrides()
	if overrides == nil {
		overrides = map[string]bool{}
	}
	out := map[string]any{
		"user_id":          st.UserID,
		"in_app_enabled":   st.InAppEnabled,
		"muted_events":     st.MutedEventsRaw,
		"urgent_overrides": overrides,
		"digest":           nil,
	}
	if d := h.digests(); d != nil {
		if v, err := d.PersonDigest(ctx, userID); err == nil {
			out["digest"] = v
		}
	}
	return out
}

// cleanOverrides keeps the choices the catalogue knows: a kind a later
// version removed, or a typo, is not stored.
func cleanOverrides(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for e, on := range in {
		if notify.KnownDigestEvent(e) {
			out[e] = on
		}
	}
	return out
}

// digestPolicyAnswer is GET and PATCH /api/admin/notifications/digest.
type digestPolicyAnswer struct {
	// WindowMinutes and Urgent are the defaults as they apply.
	WindowMinutes int      `json:"window_minutes"`
	Urgent        []string `json:"urgent_events"`
	// Saved: an administrator chose them; otherwise they are the built-in ones.
	Saved bool `json:"saved"`
	// Scope: "instance" (a single-tenant install) or "tenant" (a tenant's
	// own, the supertenant's included).
	Scope  string        `json:"scope"`
	Tenant *e2eTenantRef `json:"tenant"`
	// Defaults is the built-in choice, what "Restore defaults" brings back.
	Defaults struct {
		WindowMinutes int      `json:"window_minutes"`
		Urgent        []string `json:"urgent_events"`
	} `json:"defaults"`
	// Events is every kind a choice can be made about, AdminEvents the
	// administrator alerts among them.
	Events      []string `json:"events"`
	AdminEvents []string `json:"admin_events"`
	WindowMin   int      `json:"window_min"`
	WindowMax   int      `json:"window_max"`
}

// digestScope is whose defaults this administrator holds: 0 (the instance) on
// a single-tenant install, the scope's tenant otherwise — and false, with a
// 403 written, for a scope that names no tenant (tenant.DenyAll).
func digestScope(w http.ResponseWriter, r *http.Request) (int64, bool, bool) {
	scope, ok := tenant.FromContext(r.Context())
	if !ok {
		return 0, false, true
	}
	if scope == nil || scope.ProviderID <= 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "no_tenant", "message": "this account belongs to no tenant",
		})
		return 0, false, false
	}
	return scope.ProviderID, true, true
}

func (h *Notifications) digestPolicyAnswer(ctx context.Context, scope int64, isTenant bool, d notify.Digests) (*digestPolicyAnswer, error) {
	pol, saved, err := d.DigestPolicy(ctx, scope)
	if err != nil {
		return nil, err
	}
	out := &digestPolicyAnswer{
		WindowMinutes: pol.WindowMinutes,
		Urgent:        pol.Urgent,
		Saved:         saved,
		Scope:         "instance",
		Events:        notify.DigestEvents(),
		AdminEvents:   notify.AdminAlertEvents(),
		WindowMin:     notify.DigestWindowMin,
		WindowMax:     notify.DigestWindowMax,
	}
	out.Defaults.WindowMinutes = notify.DigestWindowDefault
	out.Defaults.Urgent = notify.DefaultUrgentEvents()
	if isTenant {
		out.Scope = "tenant"
		ref := &e2eTenantRef{ID: scope}
		if h.Store != nil {
			if p, err := h.Store.GetProvider(ctx, scope); err == nil && p != nil {
				ref.Name = p.Name
			}
		}
		out.Tenant = ref
	}
	return out, nil
}

// AdminDigest answers the digest defaults the caller administers.
//
//	GET /api/admin/notifications/digest
func (h *Notifications) AdminDigest(w http.ResponseWriter, r *http.Request) {
	scope, isTenant, ok := digestScope(w, r)
	if !ok {
		return
	}
	d := h.digests()
	if h.Service == nil || d == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	out, err := h.digestPolicyAnswer(r.Context(), scope, isTenant, d)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// AdminUpdateDigest changes the digest defaults the caller administers. A
// field left out keeps its value; `urgent_events: null` restores the built-in
// list. The window must be within 1-15 minutes (400 invalid_window); unknown
// event ids are dropped.
//
//	PATCH /api/admin/notifications/digest
//	body: {window_minutes?: 1-15, urgent_events?: ["file.infected", …] | null}
func (h *Notifications) AdminUpdateDigest(w http.ResponseWriter, r *http.Request) {
	scope, isTenant, ok := digestScope(w, r)
	if !ok {
		return
	}
	d := h.digests()
	if h.Service == nil || d == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	var body struct {
		WindowMinutes *int            `json:"window_minutes"`
		Urgent        json.RawMessage `json:"urgent_events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	cur, _, err := d.DigestPolicy(r.Context(), scope)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	next := model.DigestPolicy{Scope: scope, WindowMinutes: cur.WindowMinutes}
	// An urgent list nobody chose stays "none chosen" (nil) when only the
	// window changes, so a later release's built-in list still reaches it.
	if h.digestUrgentSaved(r.Context(), scope) {
		next.Urgent = cur.Urgent
	}
	if body.WindowMinutes != nil {
		if *body.WindowMinutes < notify.DigestWindowMin || *body.WindowMinutes > notify.DigestWindowMax {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   "invalid_window",
				"message": "window_minutes must be between 1 and 15",
				"min":     notify.DigestWindowMin,
				"max":     notify.DigestWindowMax,
			})
			return
		}
		next.WindowMinutes = *body.WindowMinutes
	}
	if len(body.Urgent) > 0 {
		if string(body.Urgent) == "null" {
			next.Urgent = nil
		} else {
			var list []string
			if err := json.Unmarshal(body.Urgent, &list); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "urgent_events must be a list of event ids, or null"})
				return
			}
			if list == nil {
				list = []string{}
			}
			next.Urgent = list
		}
	}
	if _, err := d.SaveDigestPolicy(r.Context(), next); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out, err := h.digestPolicyAnswer(r.Context(), scope, isTenant, d)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// digestUrgentSaved reports whether the stored defaults of scope carry an
// urgent list of their own (not the built-in one).
func (h *Notifications) digestUrgentSaved(ctx context.Context, scope int64) bool {
	if h.Store == nil {
		return false
	}
	p, err := h.Store.GetDigestPolicy(ctx, scope)
	return err == nil && p != nil && p.Urgent != nil
}
