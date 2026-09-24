// Package handlers — account_rules.go
//
// What an account's e-mail and username may be, and how a refusal is said.
// Used by the self-service profile (auth_self.go) and by an administrator
// adding a user (users.go), so the two forms refuse the same things in the
// same words.
//
// ⚠⚠ Why this file exists (release-candidate sweep, 2026-09-21):
//
//   - the profile form saved "bu-bir-eposta-degil" as an e-mail address and
//     answered "Profil kaydedildi"; logging in with an e-mail then failed with
//     401, because the account no longer had one;
//   - changing to another account's address answered 200 with the success
//     message and changed nothing: UpdateUserEmail's unique-constraint error
//     was thrown away (`_ = h.Store.UpdateUserEmail(...)`);
//   - a username with "ş" in it came back as raw English —
//     `invalid username: 'ş' is not allowed (use a-z, 0-9, dot, dash,
//     underscore)` — in the Turkish panel.
//
// A refusal is `{"error": <code>, "message": <sentence in the reader's
// language>, "field": "email"|"username"}`: the code for code, the sentence
// for the person (extractError prefers `message`), the field so a form can
// put the sentence under the box it is about.
package handlers

import (
	"context"
	"net/http"
	"net/mail"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// accountProblem is one refused account field: its sentence is a
// `server.account.*` key of the server catalogue (srvtext), said in the
// reader's language when it is written — never an inline en/tr pair, which
// sent a third language (a language pack's) the English half.
type accountProblem struct {
	status int
	code   string
	field  string
	key    string
	vars   srvtext.Vars
	// count, when set, picks the plural form of key (srvtext.Plural).
	count *int
}

func (p *accountProblem) message(lang string) string {
	if p.count != nil {
		return srvtext.Plural(lang, p.key, *p.count, p.vars)
	}
	return srvtext.Text(lang, p.key, p.vars)
}

func (p *accountProblem) write(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, p.status, map[string]string{"error": p.code, "message": p.message(langOf(r)), "field": p.field})
}

// validEmailAddress is a bare address a mailbox can have: net/mail parses it
// and the parse IS the whole input (no "Name <addr>", no spaces). A dotless
// domain is allowed — `admin@local` is the address filex creates its first
// administrator with.
func validEmailAddress(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\r\n<>") {
		return false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return false
	}
	at := strings.LastIndexByte(s, '@')
	return at > 0 && at < len(s)-1
}

// emailProblem checks an e-mail address about to be written to account
// `self` (0 = a new account). The address is expected normalised (trimmed,
// lower-case).
func emailProblem(ctx context.Context, store db.Store, email string, self int64) *accountProblem {
	if email == "" {
		return &accountProblem{status: http.StatusBadRequest, code: "email_required", field: "email",
			key: "server.account.email_required"}
	}
	if !validEmailAddress(email) {
		return &accountProblem{status: http.StatusBadRequest, code: "email_invalid", field: "email",
			key: "server.account.email_invalid", vars: srvtext.Vars{"email": email}}
	}
	if other, err := store.GetUserByEmail(ctx, email); err == nil && other != nil && other.ID != self {
		return emailTaken(email)
	}
	return nil
}

func emailTaken(email string) *accountProblem {
	return &accountProblem{status: http.StatusConflict, code: "email_taken", field: "email",
		key: "server.account.email_taken", vars: srvtext.Vars{"email": email}}
}

// usernameProblem checks a (normalised) username about to be written to
// account `self`.
func usernameProblem(ctx context.Context, store db.Store, name string, self int64) *accountProblem {
	if p := identity.Check(name); p != nil {
		return usernameRefusal(p, name)
	}
	if other, err := store.GetUserByUsername(ctx, name); err == nil && other != nil && other.ID != self {
		return usernameTaken(name)
	}
	return nil
}

func usernameTaken(name string) *accountProblem {
	return &accountProblem{status: http.StatusConflict, code: "username_taken", field: "username",
		key: "server.account.username_taken", vars: srvtext.Vars{"name": name}}
}

// usernameRefusal says identity.Check's answer in the reader's language. The
// browser says the same things while the person types (web/src/lib/
// accountRules.ts); this is what reaches someone the browser did not stop.
func usernameRefusal(p *identity.Problem, name string) *accountProblem {
	bad := func(key string, vars srvtext.Vars) *accountProblem {
		return &accountProblem{status: http.StatusBadRequest, code: "username_invalid", field: "username",
			key: "server.account." + key, vars: vars}
	}
	counted := func(key string, n int) *accountProblem {
		out := bad(key, nil)
		out.count = &n
		return out
	}
	switch p.Kind {
	case identity.ProblemEmpty:
		return bad("username_empty", nil)
	case identity.ProblemAt:
		return bad("username_at", nil)
	case identity.ProblemShort:
		return counted("username_short", p.Limit)
	case identity.ProblemLong:
		return counted("username_long", p.Limit)
	case identity.ProblemUpper:
		return bad("username_upper", nil)
	case identity.ProblemDigit:
		return bad("username_digit", nil)
	case identity.ProblemChar:
		if p.Char == ' ' {
			return bad("username_space", nil)
		}
		return bad("username_char", srvtext.Vars{"char": string(p.Char)})
	}
	return bad("username_reserved", srvtext.Vars{"name": name})
}

// isUniqueViolation recognises the unique-constraint error of every driver
// the store has (SQLite, PostgreSQL, MySQL) — the race the pre-checks above
// cannot close, reported as the same "taken" instead of a 500.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique") || strings.Contains(s, "duplicate")
}
