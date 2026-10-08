package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/permgap"
)

// Roles that allow adding files but not encrypting (perm/gaps.go).
//
// A save on a version without files.encrypt takes it away without a word, and
// a list left that way looks like one an administrator took it away from on
// purpose. Nothing is given back by itself: Admin -> Roles shows each such
// list with one click to give the permission back and one to say it was on
// purpose. Who sees and acts on a gap is who may edit the list: the built-in
// User role is the platform operator's (PutDefaults), a custom role its
// tenant's administrators' and the operator's (visibleRule).

// gapVisible reports whether the caller may see and act on g.
func gapVisible(ctx context.Context, g perm.Gap) bool {
	scope, confined := confinedScope(ctx)
	if !confined {
		return true
	}
	if g.Builtin() {
		return false
	}
	return g.ProviderID != nil && *g.ProviderID == scope.ProviderID
}

// visibleGaps are the open gaps the caller may see.
func (h *PermissionsAdmin) visibleGaps(ctx context.Context) ([]perm.Gap, error) {
	open, err := perm.OpenGaps(ctx, h.Store)
	if err != nil {
		return nil, err
	}
	out := []perm.Gap{}
	for _, g := range open {
		if gapVisible(ctx, g) {
			out = append(out, g)
		}
	}
	return out, nil
}

// announceGaps tells administrators about a gap a save has opened that
// nobody has been told of (internal/permgap). Best effort.
func (h *PermissionsAdmin) announceGaps(ctx context.Context) {
	permgap.Announce(ctx, h.Store, h.Notify)
}

// noteGapsSaved records what a save on this version did to one list's gaps
// (perm.NoteGapsSaved) and tells administrators about a new one. Best
// effort: the save itself has happened.
func (h *PermissionsAdmin) noteGapsSaved(ctx context.Context, before, after []perm.Gap, shown []string) {
	if err := perm.NoteGapsSaved(ctx, h.Store, before, after, shown); err == nil {
		h.announceGaps(ctx)
	}
}

// ListGaps returns the roles the caller may edit that allow adding files but
// not encrypting, and nobody said were on purpose.
//
//	GET /api/admin/roles/gaps → {"gaps": [{id, key, from, role | rule_id, rule_name}]}
func (h *PermissionsAdmin) ListGaps(w http.ResponseWriter, r *http.Request) {
	gaps, err := h.visibleGaps(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gaps": gaps})
}

type gapRequest struct {
	ID string `json:"id"`
}

// gapFromRequest reads {"id"} and finds the gap the caller may act on,
// answering otherwise.
func (h *PermissionsAdmin) gapFromRequest(w http.ResponseWriter, r *http.Request) (perm.Gap, bool) {
	var req gapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json: want {\"id\":\"…\"}"})
		return perm.Gap{}, false
	}
	g, ok, err := perm.FindGap(r.Context(), h.Store, req.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return perm.Gap{}, false
	}
	if ok && g.Builtin() && !requireSupertenant(w, r, builtinRolesAreInstanceWide) {
		return perm.Gap{}, false
	}
	if !ok || !gapVisible(r.Context(), g) {
		// Saved since, deleted, or another tenant's: there is nothing here
		// for the caller to act on.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "gap_gone", "message": "this role no longer lacks the permission"})
		return perm.Gap{}, false
	}
	return g, true
}

// auditGap names the gap on the audit row.
func auditGap(ctx context.Context, g perm.Gap) {
	auth.AddAuditDetail(ctx, "key", g.Key)
	auth.AddAuditDetail(ctx, "from", g.From)
	name := g.RuleName
	if g.Builtin() {
		auth.AddAuditDetail(ctx, "role", g.Role)
		name = g.Role
	} else {
		auth.AddAuditDetail(ctx, "rule_id", g.RuleID)
	}
	auth.SetAuditTarget(ctx, g.ID, name+" · "+string(g.Key))
}

// RestoreGap gives a role the permission it lacks: the built-in role's list
// the key, a custom role's folder part an Allow of it. Nothing else changes.
//
//	POST /api/admin/roles/gaps/restore {"id": "builtin:user:files.encrypt"} → {"gaps": […]}
func (h *PermissionsAdmin) RestoreGap(w http.ResponseWriter, r *http.Request) {
	g, ok := h.gapFromRequest(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	auditGap(ctx, g)
	if g.Builtin() {
		before, after, err := perm.RestoreBuiltinGap(ctx, h.Store, g)
		if err != nil {
			writePermInvalid(w, err)
			return
		}
		auth.AddAuditDetail(ctx, "before", before)
		auth.AddAuditDetail(ctx, "after", after)
	} else {
		rule, err := h.Store.GetPermissionRule(ctx, g.RuleID)
		if err != nil || rule == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "gap_gone", "message": "this role no longer lacks the permission"})
			return
		}
		auditRule(ctx, "before", rule)
		next := *rule
		next.Effects = make(map[string]string, len(rule.Effects)+1)
		for k, v := range rule.Effects {
			next.Effects[k] = v
		}
		next.Effects[string(g.Key)] = model.PermAllow
		if err := h.Store.UpdatePermissionRule(ctx, &next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		auditRule(ctx, "after", &next)
		if err := h.syncHolders(ctx, &next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		perm.InvalidateFor(r.Context())
	}
	h.noteGapsSaved(ctx, []perm.Gap{g}, nil, nil)
	h.ListGaps(w, r)
}

// DismissGap records that a role lacks the permission on purpose: it is not
// shown again while it stays that way.
//
//	POST /api/admin/roles/gaps/dismiss {"id": "role:12:files.encrypt"} → {"gaps": […]}
func (h *PermissionsAdmin) DismissGap(w http.ResponseWriter, r *http.Request) {
	g, ok := h.gapFromRequest(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	auditGap(ctx, g)
	if err := perm.DismissGap(ctx, h.Store, g.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.announceGaps(ctx)
	h.ListGaps(w, r)
}

// ruleGapsOf is perm.RuleGaps for a rule that may be nil.
func ruleGapsOf(r *model.PermissionRule) []perm.Gap {
	if r == nil {
		return nil
	}
	return perm.RuleGaps(r)
}
