// Package handlers — demo_redact.go
//
// What a public demo shows in the read-only admin pages it deliberately keeps
// visible. The demo guard (api/demo_guard.go) stops a visitor CHANGING the
// instance; this file stops the pages a visitor may still READ from handing
// out other people's data and the operator's secrets.
package handlers

import (
	"context"
	"net/netip"
	"regexp"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
)

// demoMaskedIP replaces a client address in demo mode.
//
// Not an empty string: the Audit page has an IP column, and blanking it reads
// as "filex did not record one" — which would be a lie about the product on
// the very page a visitor is evaluating it from.
const demoMaskedIP = "hidden on the demo"

// isAddressText reports whether s is one IP address - v4 or v6, bare, in
// brackets, with a port or a zone - the way a client address is written.
func isAddressText(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if _, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
		return true
	}
	_, err := netip.ParseAddrPort(s)
	return err == nil
}

// isNetworkText reports whether s is a CIDR network (an allow-list entry).
func isNetworkText(s string) bool {
	_, err := netip.ParsePrefix(strings.TrimSpace(s))
	return err == nil
}

// maskAddress hides s when it is one address; anything else is returned as it
// is.
func maskAddress(s string) string {
	if isAddressText(s) {
		return demoMaskedIP
	}
	return s
}

// maskAddressList answers a NEW list in which every address and every CIDR
// network is demoMaskedIP; the words of an address list (loopback, private,
// link-local, none…) stay - they say what kind of list it is, not where
// anybody is.
func maskAddressList(list []string) []string {
	if list == nil {
		return nil
	}
	out := make([]string, len(list))
	for i, e := range list {
		if isAddressText(e) || isNetworkText(e) {
			out[i] = demoMaskedIP
		} else {
			out[i] = e
		}
	}
	return out
}

// addrToken is one run of characters between the separators an address list
// is written with (comma, semicolon, white space).
var addrToken = regexp.MustCompile(`[^,;\s]+`)

// maskAddressesInText hides every address and network written anywhere in s
// - the whole string, or a word of it: an allow-list as stored ("192.0.2.7,
// 10.0.0.0/8"), a target id, an identifier somebody typed as an address. The
// rest of the text is left exactly as it was.
func maskAddressesInText(s string) string {
	if s == "" {
		return s
	}
	return addrToken.ReplaceAllStringFunc(s, func(tok string) string {
		core := strings.Trim(tok, `()<>"'`+"`")
		// "fe80::" is an address; "10.0.0.1:" or "10.0.0.1." is one followed
		// by punctuation. Try it whole first.
		for _, c := range []string{core, strings.TrimRight(core, ".:")} {
			if c != "" && (isAddressText(c) || isNetworkText(c)) {
				return strings.Replace(tok, c, demoMaskedIP, 1)
			}
		}
		return tok
	})
}

// maskAddressesInValue is maskAddressesInText through a metadata value:
// strings, lists and nested objects, copied - never changed in place.
func maskAddressesInValue(v any) any {
	switch x := v.(type) {
	case string:
		return maskAddressesInText(x)
	case []string:
		out := make([]string, len(x))
		for i, s := range x {
			out[i] = maskAddressesInText(s)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = maskAddressesInValue(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = maskAddressesInValue(e)
		}
		return out
	}
	return v
}

// demoNames is what a demo still shows of a name somebody TYPED at a sign-in
// door: the names of the instance's own accounts - their e-mails and
// usernames, normalised - so the demo account and the golden copy's accounts
// stay readable. Any other name is a visitor's: their own e-mail, typed
// instead of the demo's, is theirs, and a demo hides it like an address.
type demoNames map[string]struct{}

// loadDemoNames reads the accounts once per request. A store that cannot
// answer leaves the set empty, and every typed name is hidden - the failure
// that publishes nothing.
func loadDemoNames(ctx context.Context, store db.Store) demoNames {
	n := demoNames{}
	if store == nil {
		return n
	}
	users, err := store.ListUsers(ctx)
	if err != nil {
		return n
	}
	for _, u := range users {
		for _, name := range []string{u.Email, u.Username} {
			if k := identity.Normalize(name); k != "" {
				n[k] = struct{}{}
			}
		}
	}
	return n
}

// known reports whether a typed name - as the sign-in limit keys it, with a
// realm in front or a DOMAIN\ prefix (loginguard.Subject) - is one of the
// instance's accounts. The realm is dropped: `acme/known@example.com` names
// an account when `known@example.com` is one.
func (n demoNames) known(typed string) bool {
	s := loginguard.Subject(typed)
	if i := strings.IndexByte(s, '/'); i > 0 {
		s = s[i+1:]
	}
	_, ok := n[s]
	return ok
}

// mask hides a typed name that is no account of the instance.
func (n demoNames) mask(typed string) string {
	if typed == "" || typed == demoMaskedIP || n.known(typed) {
		return typed
	}
	return demoMaskedIP
}

// demoMask hides, on one request of a public demo, what the readable admin
// pages would hand a visitor of the OTHER visitors: their addresses, and the
// names they typed at a sign-in door that are no account of the instance.
// Built once per request (newDemoMask reads the accounts); nil masks nothing,
// which is what an ordinary install gets.
//
// ⚠ On a public demo the audit log, the dashboard and the Sign-in security
// page are visitor-readable (that is the point - the operator surfaces are
// what a demo is for). Before the launch post they showed the operator's own
// address; after it, every reader's, to every other reader. ⚠ The client
// address column alone was masked until 2026-10-01: an address lock's
// `login.locked` / `login.unlocked` rows carried the address as their target
// and in their metadata, the trail and the locks listed every address, and a
// visitor's own e-mail typed into the form was printed to everybody.
//
// On a normal install the operator is entitled to every one of these: nothing
// here runs unless Demo.Mode is on.
type demoMask struct{ names demoNames }

func newDemoMask(ctx context.Context, store db.Store) *demoMask {
	return &demoMask{names: loadDemoNames(ctx, store)}
}

// auditRow hides the addresses on one audit row - the client address, an
// address the row is ABOUT (target_id: an address lock, an address unlocked
// by an administrator), any address in its metadata (`subject`,
// `target_name`, the allow-list and the trusted proxies of a sign-in settings
// change) - and, on a sign-in row (target type `login`), the typed name it is
// about when that names no account (`target_id`, `identifier`, an account
// unlock's `subject` and `target_name`).
func (m *demoMask) auditRow(e *model.AuditEntry) {
	if m == nil || e == nil {
		return
	}
	if e.IP != "" {
		e.IP = demoMaskedIP
	}
	e.TargetID = maskAddressesInText(e.TargetID)
	if e.Metadata != nil {
		e.Metadata = maskAddressesInValue(e.Metadata).(map[string]any)
	}
	if e.TargetType != "login" {
		return
	}
	e.TargetID = m.names.mask(e.TargetID)
	for _, k := range []string{"identifier", "subject", "target_name"} {
		if v, ok := e.Metadata[k].(string); ok {
			e.Metadata[k] = m.names.mask(v)
		}
	}
}

// auditRows masks the joined rows served by GET /api/admin/audit - the row,
// and the name the handler worked out for its target.
func (m *demoMask) auditRows(rows []*db.AuditEntryWithUser) {
	if m == nil {
		return
	}
	for _, row := range rows {
		if row == nil || row.Entry == nil {
			continue
		}
		m.auditRow(row.Entry)
		row.TargetName = maskAddressesInText(row.TargetName)
		if row.Entry.TargetType == "login" {
			row.TargetName = m.names.mask(row.TargetName)
		}
	}
}

// recent masks the `recent_activity` block of GET /api/admin/dashboard.
//
// ⚠ The dashboard carries the same rows as the audit page. Masking one and
// not the other leaves the addresses on the landing page of the admin panel,
// which is the first thing a visitor sees.
func (m *demoMask) recent(rows []*model.AuditEntry) {
	for _, e := range rows {
		m.auditRow(e)
	}
}

// lock hides one row of the locks list: an address lock's subject, any
// counter's last address, and an account counter's subject when the name
// typed is no account of the instance. The row keeps its id (loginLockID),
// so two masked subjects are still two rows, and nothing can be done with the
// masked text: an unlock names an address or an account, and "hidden on the
// demo" is neither (and a demo refuses the unlock anyway).
func (m *demoMask) lock(it *loginLockItem) {
	if m == nil {
		return
	}
	it.Subject = maskAddress(it.Subject)
	if it.Scope == model.LoginThrottleAccount {
		it.Subject = m.names.mask(it.Subject)
	}
	if it.LastIP != "" {
		it.LastIP = demoMaskedIP
	}
}

// maskViewForDemo hides, on GET /api/admin/login-security, the addresses the
// operator saved: the allow-list (an address on it is exempt from the
// per-address limit - the most useful address on the page to somebody
// guessing passwords) and the trusted proxies. The class words stay, and so
// does `your_ip`: that is the reader's own address, shown to the reader.
//
// ⚠ One place for the demo masking of this answer: a field added to it that
// carries an address is masked HERE, behind the same DemoMode.
func (h *LoginSecurity) maskViewForDemo(resp *loginSecurityResponse) {
	resp.Settings.IPAllowlist = maskAddressList(resp.Settings.IPAllowlist)
	resp.Settings.TrustedProxies = maskAddressList(resp.Settings.TrustedProxies)
	resp.TrustedProxiesEffective = maskAddressList(resp.TrustedProxiesEffective)
	resp.TrustedAddresses = maskAddressList(resp.TrustedAddresses)
	maskProxyAutoForDemo(resp)
}

// maskSettingsForDemo hides the addresses in the sign-in security settings of
// GET /api/admin/settings (the allow-list, the trusted proxies), which the
// generic settings page lists beside every other key.
func maskSettingsForDemo(m map[string]string) {
	for _, k := range []string{loginguard.KeyIPAllowlist, loginguard.KeyTrustedProxy} {
		if v, ok := m[k]; ok {
			m[k] = maskAddressesInText(v)
		}
	}
}

// DemoRefusal is a public demo's answer to a state-changing request on an
// operator surface: api.DemoGuard writes it for the routes, and the admin MCP
// tools - which reach the same handlers in-process, past every route -
// answer it too (AIAdmin.invoke).
func DemoRefusal() map[string]string {
	return map[string]string{
		"error": "this is a public demo: the admin surface is read-only here, " +
			"and the shared demo account cannot be changed - run your own filex to try this",
		"demo": "read-only",
	}
}

// (maskNestedSecrets lived here: it masked, on a demo only, an auth-provider
// config blob that carried a secret. Since v0.43.0 the identity providers
// page never sends a secret back at all, on any install — secrets are sealed
// and listed only as "set" — so there is nothing left for a demo to mask.)

// newDemoAwareDashboard / newDemoAwareAudit / newDemoAwareAuthProviders build
// the /api/ai/admin copies of the three redacting handlers.
//
// ⚠ The AI admin surface constructs its OWN instances of the same handler
// types, so a field set in BuildRouter never reaches them. That is exactly how
// a demo rule ends up applying to the cookie route and not to the token route
// serving the same data.
func newDemoAwareDashboard(d AIAdminDeps) *Dashboard {
	h := NewDashboard(d.Store, d.Caps, d.Queue)
	h.DemoMode = d.DemoMode
	return h
}

func newDemoAwareAudit(d AIAdminDeps) *Audit {
	h := NewAudit(d.Store)
	h.DemoMode = d.DemoMode
	return h
}

// newDemoAwareLoginSecurity / newDemoAwareSettings: the sign-in security
// handler and the settings handler of /api/ai/admin and the admin_* MCP tools,
// masking what their /api/admin twins mask.
func newDemoAwareLoginSecurity(d AIAdminDeps) *LoginSecurity {
	h := NewLoginSecurity(d.Store, d.LoginGuard, d.EnvTrustedProxies)
	h.DemoMode = d.DemoMode
	return h
}

func newDemoAwareSettings(d AIAdminDeps) *Settings {
	h := newSettingsWithGuard(d.Store, d.LoginGuard)
	h.DemoMode = d.DemoMode
	return h
}

// newDemoAwareProviders is the tenant handler the admin tools drive: the
// in-process call never passes api.DemoGuard, so the handler refuses the demo's
// writes itself.
func newDemoAwareProviders(d AIAdminDeps) *Providers {
	h := NewProviders(d.Store, d.MultiTenant)
	h.DemoMode = d.DemoMode
	return h
}

func newDemoAwareAuthProviders(d AIAdminDeps) *AuthProviders {
	h := NewAuthProviders(d.Store, d.AuthLive)
	h.DemoMode = d.DemoMode
	return h
}

// newDemoAwareStorages carries denyOnDemo onto the token surface too. The
// middleware in api/demo_guard.go already refuses every write under
// /api/ai/admin, so this is the second lock on the same door — but the field
// being unset here is what made the first lock look complete.
func newDemoAwareStorages(d AIAdminDeps) *Storages {
	h := NewStorages(d.Store, d.Worker)
	h.DemoMode = d.DemoMode
	return h
}
