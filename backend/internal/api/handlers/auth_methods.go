package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// ssoMethod is one SSO button of the sign-in page.
type ssoMethod struct {
	// ID goes back in GET /api/auth/oidc/start?instance=: an instance's slug,
	// or "tenant" for the tenant's own OIDC.
	ID string `json:"id"`
	// Label is what the button says; "" = the page's own words (or the
	// branding's sso_label).
	Label string `json:"label"`
}

// Methods answers GET /api/auth/methods?realm=: how a sign-in for the tenant
// the address and the realm name may go (docs/TENANT-ADMIN.md):
//
//	{"password": bool, "recovery": bool, "sso": [{"id", "label"}]}
//
// The sign-in page asks before it draws its buttons, and again when the Realm
// field changes.
//
// ⚠ No realm oracle. For a realm that names a tenant, `password` is whether
// the instance answers a password form at all (never whether THAT tenant has
// a directory), and a realm nobody has answers exactly like a tenant with no
// SSO. A realm with SSO shows its buttons: starting its sign-in would send the
// browser to its identity provider anyway. `recovery` is the platform's own
// tenant's only.
func (h *Auth) Methods(w http.ResponseWriter, r *http.Request) {
	out, err := h.methodsFor(r, r.URL.Query().Get("realm"))
	if err != nil {
		slog.Error("auth methods: could not tell which tenant the realm names", slog.String("err", err.Error()))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "sign-in methods could not be read"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// SSOFor is the SSO buttons of the sign-in page as it first loads: the
// tenant the address names, else the platform's own (/api/capabilities
// `auth_sso`). The page asks Methods only when a realm is typed.
func (h *Auth) SSOFor(r *http.Request) []ssoMethod {
	out, err := h.methodsFor(r, "")
	if err != nil {
		return []ssoMethod{}
	}
	list, _ := out["sso"].([]ssoMethod)
	if list == nil {
		list = []ssoMethod{}
	}
	return list
}

func (h *Auth) methodsFor(r *http.Request, realm string) (map[string]any, error) {
	out := map[string]any{"password": available(h.LocalAuth), "recovery": false, "sso": []ssoMethod{}}
	if h.Live == nil {
		if available(h.OIDCAuth) {
			out["sso"] = []ssoMethod{{ID: "", Label: ""}}
		}
		return out, nil
	}
	ctx := r.Context()
	set := h.Live.Current()
	host := requestHost(r)
	sso := func(p *model.Provider, tid int64) []ssoMethod {
		list := []ssoMethod{}
		for _, c := range h.Live.SSOChoices(ctx, p, tid, host) {
			list = append(list, ssoMethod{ID: c.Key, Label: c.Label})
		}
		return list
	}
	if !h.MultiTenant || h.Store == nil {
		v := set.For(0)
		out["password"], out["recovery"], out["sso"] = v.PasswordLogin(), v.Recovery(), sso(nil, 0)
		return out, nil
	}
	typed := tenant.NormalizeRealm(realm)
	lr, err := auth.ResolveLoginRealm(ctx, h.Store, typed, typed != "", host, h.EmailToken)
	if err != nil {
		if !errors.Is(err, auth.ErrUnknownRealm) && !errors.Is(err, auth.ErrRealmConflict) {
			return nil, err
		}
		// A realm nobody has: the answer a tenant with no SSO gets.
		out["password"] = set.PasswordLogin()
		return out, nil
	}
	if lr.Tenant != nil && !lr.Tenant.IsSupertenant {
		out["password"] = set.PasswordLogin()
		out["sso"] = sso(lr.Tenant, lr.Tenant.ID)
		return out, nil
	}
	v := set.For(0)
	out["password"], out["recovery"], out["sso"] = v.PasswordLogin(), v.Recovery(), sso(lr.Tenant, 0)
	return out, nil
}
