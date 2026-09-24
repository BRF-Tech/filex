package handlers

/* ===== tema:v1 — serving the operator stylesheet =====

`GET /api/me/custom-css`, behind auth.

⚠⚠ THE AUTH IS THE FEATURE, not boilerplate. The whole point of moving this
off `/api/branding` is that the login page and every anonymous share visitor
must never receive the sheet: they cannot be allowed to wear it (a stylesheet
that repaints the sign-in form is the lock-out this round exists to prevent),
and they should not even be able to download it. A route behind
`auth.Middleware` answers both at once.

⚠ It is `/api/me/...` and not `/api/admin/...` because EVERY signed-in person
wears the sheet, not just admins — it is the instance's look. Only writing it
is an operator's privilege, and that is enforced where the write happens
(settings.go `allowSettingWrite`). */

import (
	"net/http"

	"github.com/brf-tech/filex/backend/internal/db"
)

// CustomCSS serves the operator stylesheet to signed-in browsers.
type CustomCSS struct {
	Store db.Store
}

// NewCustomCSS constructs the handler.
func NewCustomCSS(store db.Store) *CustomCSS { return &CustomCSS{Store: store} }

// customCSSResponse is the wire shape.
type customCSSResponse struct {
	// CSS is already sanitised AND already wrapped in its `@scope` rule — the
	// browser assigns it to a style element's textContent and does nothing
	// else to it. Keeping the wrapper server-side means the guard cannot be
	// dropped by a client that forgot to apply it.
	CSS string `json:"css"`
	// Enabled says whether the switch is on, so the admin screen can tell
	// "off" from "on but empty" without a second call.
	Enabled bool `json:"enabled"`
}

// Get returns the effective stylesheet for this installation.
//
// ⚠ `Cache-Control: no-store`. This is per-installation rather than per-person,
// so a cache would be correct — but it is also the thing an operator turns off
// in a hurry, and a sheet still sitting in a shared cache after the switch was
// flipped is the exact failure the switch exists to end.
func (h *CustomCSS) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	m, err := h.Store.ListSettings(r.Context())
	if err != nil {
		// An unstyled panel is the right failure, and it is the same one an
		// installation with no custom CSS already sees.
		writeJSON(w, http.StatusOK, customCSSResponse{})
		return
	}
	writeJSON(w, http.StatusOK, customCSSResponse{
		CSS:     customCSSFromSettings(m),
		Enabled: customCSSEnabled(m),
	})
}

/* ===== /tema:v1 ===== */
