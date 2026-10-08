package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// POST /api/auth/account/check - "would this e-mail address / username be
// accepted?", asked WHILE a person types (the profile form, an administrator
// adding a user), answered by the same rules and in the same words the save
// refuses with (account_rules.go).
//
// ⚠⚠ Why (0.54 audit B15 + A12): the browser kept its own copy of these rules
// (core lib/accountRules.ts) and its own copy of their sentences, in two
// catalogues (the explorer's and the admin panel's) beside the server's
// `server.account.*`: three copies of every sentence, and the e-mail rule had
// already drifted - the browser let `a,b@x` and `ada.@x` through, the save
// refused them. Now there is one rule and one sentence, the server's.
//
// Request: {"email"?: "...", "username"?: "...", "for"?: "self"|"new"}. Only
// the fields sent are checked. `for` is whose account the values are meant
// for: the caller's own (the default) - where keeping the address or the
// name the account already holds is no change and never a refusal - or an
// account an administrator is about to create.
//
// Answer: 200 {"email"?: {error, message}, "username"?: {error, message}} - a
// field is present only when it would be refused; the code is the save's
// (email_invalid, username_invalid, email_taken…) and the message the
// reader's language. It never writes anything.
//
// Who learns what: the format of a value is no secret, so any signed-in
// account asks. Whether an address or a name is TAKEN is told only to whoever
// the save would tell it to: the caller about their own account when they may
// edit it (perm.AccountEdit, as PATCH /api/auth/profile), and a full
// administrator about a new one.
func (h *AuthSelf) CheckAccount(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req struct {
		Email    *string `json:"email"`
		Username *string `json:"username"`
		For      string  `json:"for"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "bad json"})
		return
	}
	forNew := req.For == "new"
	var self int64
	var taken bool
	if forNew {
		taken = auth.CallerMayAdminister(r.Context())
	} else {
		self = u.ID
		taken = h.ACL == nil || callerCan(r.Context(), h.ACL, perm.AccountEdit)
	}
	lang := langOf(r)
	out := map[string]any{}
	if req.Email != nil {
		email := strings.ToLower(strings.TrimSpace(*req.Email))
		// The profile's own rule: an account with no address may keep none,
		// and its own address is no change.
		keep := !forNew && (email == u.Email || (email == "" && u.Email == ""))
		if !keep {
			var p *accountProblem
			if taken {
				p = emailProblem(r.Context(), h.Store, email, self)
			} else {
				p = emailFormatProblem(email)
			}
			if p != nil {
				out["email"] = p.said(lang)
			}
		}
	}
	if req.Username != nil {
		name := identity.Normalize(*req.Username)
		// Keeping the name the account holds is not claiming it (the first
		// administrator holds the reserved "admin", auth_self.go).
		if forNew || name != u.Username {
			var p *accountProblem
			if taken {
				p = usernameProblem(r.Context(), h.Store, name, self)
			} else if ip := identity.Check(name); ip != nil {
				p = usernameRefusal(ip, name)
			}
			if p != nil {
				out["username"] = p.said(lang)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// said is a problem as the check answers it: its code and its sentence.
func (p *accountProblem) said(lang string) map[string]string {
	return map[string]string{"error": p.code, "message": p.message(lang)}
}
