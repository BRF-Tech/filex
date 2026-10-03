package notify

import "github.com/brf-tech/filex/backend/internal/srvtext"

// The operator's notice about an older directory account in another tenant
// (EventLDAPLegacyAccountElsewhere). Its words are the server catalogue's
// (srvtext), never an `if lang == "tr"`: the row's Title/Body are in the
// instance's default language (the webhook and the admin history read them),
// and meta carries the sentence in every built-in language so each reader's
// bell shows their own (packages/core notificationText.ts, `{notice_title}`).
const (
	legacyElsewhereTitle = "server.auth_provider.legacy_elsewhere_title"
	legacyElsewhereBody  = "server.auth_provider.legacy_elsewhere_body"
)

// LegacyAccountElsewhere is the notice itself. It is a BROADCAST (no user) of
// an operator event: only a platform administrator's bell carries it — never a
// tenant administrator's (TenantAdminBell reads only broadcasts that name a
// file) and never a member's (operatorEvents). Names only: the account's login
// name and the two tenants' short names; no password, no token.
func LegacyAccountElsewhere(driver, account, accountTenant, loginTenant string) Event {
	vars := srvtext.Vars{"account": account, "account_tenant": accountTenant, "login_tenant": loginTenant}
	meta := map[string]any{
		"provider": driver, "account": account,
		"account_tenant": accountTenant, "login_tenant": loginTenant,
	}
	for _, lang := range srvtext.BuiltinLanguages() {
		meta["title_"+lang] = srvtext.Text(lang, legacyElsewhereTitle, vars)
		meta["body_"+lang] = srvtext.Text(lang, legacyElsewhereBody, vars)
	}
	lang := srvtext.Pick()
	return Event{
		Event:    EventLDAPLegacyAccountElsewhere,
		Severity: SeverityWarning,
		Title:    srvtext.Text(lang, legacyElsewhereTitle, vars),
		Body:     srvtext.Text(lang, legacyElsewhereBody, vars),
		Meta:     meta,
	}
}
