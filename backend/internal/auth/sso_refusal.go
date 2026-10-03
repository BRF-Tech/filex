package auth

import "errors"

// ── What the sign-in page may say about a refused SSO sign-in ───────────────
//
// An SSO sign-in that does not end in a session sends the browser back to the
// sign-in page (`/admin/login?error=oidc`). Until 0.50 that was ALL it said:
// "SSO sign-in failed", whether the identity provider had refused the person,
// the install opens no account at a first sign-in, or the person is in no
// group that may sign in. The reason was in the server log only, so the person
// could not tell "try again" from "ask your administrator".
//
// Now the redirect carries a REASON CODE (`&reason=auto_create_off`), one of
// the constants below, and the page says what happened and what to do in the
// reader's language (web/src/lib/ssoRefusal.ts, `login.ssoRefused.*`). Never a
// sentence and never the identity provider's own error text: the code is all
// that crosses, so nothing the IdP or a crafted callback URL says reaches the
// screen.
//
// ⚠⚠ ONE answer stays generic, on purpose (owner, 2026-10-02): an e-mail
// address that already has an account in ANOTHER tenant. Saying so would tell
// whoever holds an identity at a shared IdP which other tenants exist and
// that this address is a member of one. That refusal, and every failure that
// is not classified here (a realm nobody has, an instance not bound to the
// realm, a database error), carries NO code: the redirect is byte for byte the
// one a plain failure gets. A code reaches the page only when it is wrapped in
// an *SSORefusalError or is a first-login refusal SSOReason lets through, so
// an unclassified error can never leak one by accident.
//
// The server log keeps the full reason in every case (handlers.OIDCCallback).
//
// The same codes reach the page two other ways:
//   - the header proxy's first-login refusals, in the 401 body of every
//     request the proxy names the person in (RefusalToTell, Middleware): the
//     proxy has said who they are, there is no password to confirm;
//   - a password provider's (LDAP, PAM, Windows) refusals AFTER a right
//     password, only when its operator switched show_refusal_reason on
//     (RefusedAfterPassword, handlers.Auth.Login answers 403 with the code).
//     Off - the default - they are the answer a wrong password gets.
const (
	// SSOReasonAutoCreateOff: no account yet, and the provider opens none at a
	// first sign-in (auto_create off). Same string as ReasonAutoCreateOff.
	SSOReasonAutoCreateOff = ReasonAutoCreateOff
	// SSOReasonGroupNotAllowed: no account yet, and none of the person's
	// groups is in the provider's allowed_groups.
	SSOReasonGroupNotAllowed = ReasonGroupNotAllowed
	// SSOReasonNoEmail: the identity provider's answer carried no e-mail
	// address, which is what an account is found by.
	SSOReasonNoEmail = "no_email"
	// SSOReasonIdPDenied: the identity provider sent the browser back with an
	// error instead of a sign-in (the person cancelled, or the IdP refused).
	SSOReasonIdPDenied = "idp_denied"
	// SSOReasonIdPError: the sign-in could not be completed with the identity
	// provider (the code exchange or the id_token check failed).
	SSOReasonIdPError = "idp_error"
	// SSOReasonExpired: the browser came back without the sign-in this server
	// started (no state cookie, or another one): too late, another tab, or
	// cookies blocked.
	SSOReasonExpired = "expired"
	// SSOReasonAccountDisabled: the account exists and is disabled.
	SSOReasonAccountDisabled = "account_disabled"
	// SSOReasonTenantSuspended: the account's tenant is suspended.
	SSOReasonTenantSuspended = "tenant_suspended"
	// SSOReasonMaintenance: multi-tenant mode is off on an install that has
	// tenants, so only the platform's own accounts may sign in.
	SSOReasonMaintenance = "maintenance"
	// SSOReasonBusy: too many SSO sign-ins are in flight on this server to
	// start another one (authsetup's bounded flow book); try again shortly.
	SSOReasonBusy = "busy"
	// SSOReasonForbiddenAccount: the operating system said, only after the
	// password, that the account is a system or built-in administrator account
	// (Windows reads it from the token's SID). Same string as
	// ReasonForbiddenAccount; written out so the web gate can read it.
	SSOReasonForbiddenAccount = "forbidden_account"
	// SSOReasonEmailUnverified: the account with this address (in the tenant
	// the sign-in is for) is bound to no SSO identity yet, and the identity
	// provider did not say the address is verified, so it is not signed in to
	// by the address alone (docs/SSO.md, "Which account an SSO sign-in
	// opens"). An administrator can trust the provider's addresses.
	SSOReasonEmailUnverified = "email_unverified"
	// SSOReasonAccountPending: the sign-in opened a new account switched off,
	// because the identity provider did not say the address is verified; an
	// administrator switches it on (model.DisabledPendingApproval). Also the
	// answer for that account's later sign-ins until then.
	SSOReasonAccountPending = "account_pending"
	// SSOReasonIdentityMismatch: the account with this address (in the
	// tenant the sign-in is for) is bound to ANOTHER SSO identity (issuer,
	// sub): the same address, someone else, or the same person after the
	// identity provider gave them a new identity. An administrator can remove
	// the account's SSO bind (docs/SSO.md).
	SSOReasonIdentityMismatch = "identity_mismatch"
)

// ShowRefusalReasonKey is the setting of a password provider (LDAP, PAM,
// Windows; authsetup.Schema) that lets its sign-in form say why a person whose
// password was right gets no session. OFF by default: see RefusedAfterPassword.
const ShowRefusalReasonKey = "show_refusal_reason"

// TellsRefusal reads ShowRefusalReasonKey from a provider configuration.
func TellsRefusal(cfg map[string]any) bool {
	return CfgBoolDefault(cfg, ShowRefusalReasonKey, false)
}

// RefusalToTell is ErrUnauthorized carrying the reason of a refusal the
// person may be told (SSOReason): the first-login rule's two, or a forbidden
// account. Anything else is plain ErrUnauthorized. Every caller that asks
// errors.Is(err, ErrUnauthorized) - the login chain, the file protocols, the
// auth middleware - still reads it as a refusal.
func RefusalToTell(refusal error) error {
	var fl *FirstLoginRefusal
	if errors.As(refusal, &fl) {
		switch fl.Reason {
		case ReasonAutoCreateOff, ReasonGroupNotAllowed, ReasonForbiddenAccount:
			return &SSORefusalError{Reason: fl.Reason, Err: ErrUnauthorized}
		}
	}
	return ErrUnauthorized
}

// RefusedAfterPassword is how a password provider (LDAP, PAM, Windows)
// answers a person whose password was RIGHT but who may not sign in here:
// the first-login rule refused them, or the operating system named a system
// account only once the password was in.
//
// ⚠⚠ By default it is plain ErrUnauthorized - the answer a wrong password
// gets. A different answer would come only after the directory accepted the
// password, so it would tell anybody guessing that a password of that
// directory is right, and the same directory usually signs people in to
// other services too. Only a provider whose operator switched
// show_refusal_reason on (tell) answers with the reason code
// (RefusalToTell); the sign-in form then says why.
//
// A refusal that comes BEFORE the password is judged (a name that is a
// system account, an unknown account) and an account of another tenant are
// never passed here: they stay the one answer whatever the setting.
func RefusedAfterPassword(tell bool, refusal error) error {
	if !tell {
		return ErrUnauthorized
	}
	return RefusalToTell(refusal)
}

// SSORefusalError is a failed SSO sign-in whose reason the sign-in page may
// show. Error() is the wrapped error's, so the log line does not change.
type SSORefusalError struct {
	Reason string
	Err    error
}

func (e *SSORefusalError) Error() string { return e.Err.Error() }

// Unwrap keeps errors.Is / errors.As working on the wrapped error.
func (e *SSORefusalError) Unwrap() error { return e.Err }

// SSORefused wraps err with a reason code the sign-in page may show.
func SSORefused(reason string, err error) error {
	return &SSORefusalError{Reason: reason, Err: err}
}

// SSOReason is the code the sign-in page may show for a failed SSO sign-in,
// or "" for the one generic answer (see the block comment above): an error
// wrapped by SSORefused, or a first-login refusal for auto_create /
// allowed_groups. Anything else, including an account of another tenant, is
// "".
func SSOReason(err error) string {
	var r *SSORefusalError
	if errors.As(err, &r) {
		return r.Reason
	}
	var fl *FirstLoginRefusal
	if errors.As(err, &fl) {
		switch fl.Reason {
		case ReasonAutoCreateOff, ReasonGroupNotAllowed:
			return fl.Reason
		}
	}
	return ""
}
