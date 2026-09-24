// Package handlers — demo_redact.go
//
// What a public demo shows in the read-only admin pages it deliberately keeps
// visible. The demo guard (api/demo_guard.go) stops a visitor CHANGING the
// instance; this file stops the pages a visitor may still READ from handing
// out other people's data and the operator's secrets.
package handlers

import (
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// demoMaskedIP replaces a client address in demo mode.
//
// Not an empty string: the Audit page has an IP column, and blanking it reads
// as "filex did not record one" — which would be a lie about the product on
// the very page a visitor is evaluating it from.
const demoMaskedIP = "hidden on the demo"

// maskAuditIP hides the client address on one audit row.
//
// ⚠ On a public demo the audit log is a visitor-readable page (that is the
// point — the operator surfaces are what a demo is for), and the addresses in
// it belong to the OTHER visitors. Before the launch post it showed the
// operator's own address; after it, every reader's, to every other reader.
//
// On a normal install the operator is entitled to every one of these: nothing
// here runs unless Demo.Mode is on.
func maskAuditIP(e *model.AuditEntry) {
	if e == nil {
		return
	}
	if e.IP != "" {
		e.IP = demoMaskedIP
	}
}

// maskAuditEntries masks the joined rows served by GET /api/admin/audit.
func maskAuditEntries(rows []*db.AuditEntryWithUser) {
	for _, row := range rows {
		if row != nil {
			maskAuditIP(row.Entry)
		}
	}
}

// maskAuditRecent masks the `recent_activity` block of GET
// /api/admin/dashboard.
//
// ⚠ The dashboard carries the same rows as the audit page. Masking one and
// not the other leaves the addresses on the landing page of the admin panel,
// which is the first thing a visitor sees.
func maskAuditRecent(rows []*model.AuditEntry) {
	for _, e := range rows {
		maskAuditIP(e)
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
