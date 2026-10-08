package handlers

import (
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/identity"
)

// Suggest answers what the Add user form fills in while an address is typed:
// the username identity.Suggest derives from it - the same one an account
// made by its first SSO sign-in gets - and a display name.
//
//	GET /api/admin/users/suggest?email=<address>
//	→ {"username": "gozluk", "name": "Gözlük"}
//
// ⚠ Here and not in the browser (filex #211, audit B4): the form's own copy
// turned the letters a name actually contains into dots ("gözlük" →
// "g.zl.k") and had no length limit, so one address got one username from
// the admin form and another from a first sign-in. It claims nothing and
// reads no account: uniqueness is the create's to check, as it always was.
func (h *Users) Suggest(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	// Half an address ("ja", while it is typed) is not one yet: Suggest
	// would pad it to its minimum length ("jax"), a name nobody meant.
	if !strings.Contains(email, "@") {
		writeJSON(w, http.StatusOK, map[string]string{"username": "", "name": ""})
		return
	}
	lang := requestLang(r, r.URL.Query().Get("lang"))
	writeJSON(w, http.StatusOK, map[string]string{
		"username": identity.Suggest(email),
		"name":     identity.SuggestName(email, lang),
	})
}
