// Package e2epolicy — audit.go
//
// The policy's own rows in the audit log.
//
// The routes that change the policy, a tenant's ceiling or a request are
// skipped by the audit middleware (auth.ActionForPath: /api/admin/e2e), and
// the rows are written here instead — the arrangement of internal/pluginreq —
// for two reasons. The middleware reads only the URL, so a policy change would
// be a bare `e2e.update` with no tenant and no before/after, and a request's
// approval would read as a "create". And one of the events has no route at
// all: an approval is spent at a create door (CheckCreate), whichever door
// that is. Written here, every event is one row that names what it acted on.
package e2epolicy

import (
	"context"
	"log/slog"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// The actions. Constants, so the admin panel's audit labels are held to them
// (web/tests/lib/auditLabel.test.ts reads `AuditAction… = "…"`).
const (
	AuditActionPolicyUpdate   = "e2e_policy.update"
	AuditActionTenantUpdate   = "e2e_tenant.update"
	AuditActionRequestCreate  = "e2e_request.create"
	AuditActionRequestApprove = "e2e_request.approve"
	AuditActionRequestReject  = "e2e_request.reject"
	AuditActionRequestExpire  = "e2e_request.expire"
	AuditActionRequestUse     = "e2e_request.use"
)

// The target types those rows name: a policy (the tenant's id, or none for
// the instance's), a tenant (its provider id), a request (its id).
const (
	AuditTargetPolicy  = "e2e_policy"
	AuditTargetTenant  = "providers"
	AuditTargetRequest = "e2e_request"
)

// Actor is who did what a row records. The zero value is filex itself — a
// request that expired because nobody decided it.
type Actor struct {
	UserID *int64
	Name   string
	// TokenID: the API key the call came through, when it came through one.
	TokenID *int64
	IP      string
}

// AuditRow is one row of the policy's log.
type AuditRow struct {
	Action     string
	TargetType string
	TargetID   string
	// TargetName is what a reader wants once the thing has changed or gone:
	// a tenant's name, a request's place ("Depo://a/b"). Stored as meta
	// `target_name`, the key the Audit page reads (auth.SetAuditTarget).
	TargetName string
	Who        Actor
	Meta       map[string]any
}

// Audit writes one row. Best-effort, like every audit writer here: a row the
// store refuses is logged, never a reason to undo what already happened.
//
// ⚠ Names and flags only in Meta: the audit log is read by every
// administrator and exported (auth.AuditDetail says the same).
func Audit(ctx context.Context, store db.Store, row AuditRow) {
	if store == nil {
		return
	}
	meta := make(map[string]any, len(row.Meta)+2)
	for k, v := range row.Meta {
		meta[k] = v
	}
	if row.TargetName != "" {
		meta["target_name"] = row.TargetName
	}
	if row.Who.TokenID != nil {
		meta["token_id"] = *row.Who.TokenID
	}
	entry := &model.AuditEntry{
		UserID: row.Who.UserID, Action: row.Action, TargetType: row.TargetType, TargetID: row.TargetID,
		Metadata: meta, IP: row.Who.IP, CreatedAt: time.Now().UTC(),
	}
	// Detached: a caller whose request ended must not lose the row with it.
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := store.InsertAuditEntry(actx, entry); err != nil {
		slog.Warn("e2epolicy: audit row not written", slog.String("action", row.Action), slog.Any("err", err))
	}
}
