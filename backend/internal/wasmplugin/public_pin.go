package wasmplugin

// share_pin — the one door an app plugin has to the PIN of a link it minted.
//
// # Why this exists
//
// An app that mints a PIN-protected link is handed the PIN once, at
// `share_create`, and told to pass it on by hand. The e-Signature app tells
// the requester exactly that ("the PIN is shown to you and never mailed") —
// and then had nowhere to show it again, because an app is not allowed to
// keep a secret of its own (`ShareFacts`: "never the token's owner, the PIN
// or who else it went to"). The owner found the promise broken, 2026-09-23:
// "imzalama pinlerini sadece siz görürsünüz dedin ama o pinleri
// göstermiyorsun bir yerde".
//
// The answer is NOT a second PIN store inside the app. The platform already
// keeps one recoverable copy (migration 00049, `share.RevealPIN`) behind one
// audited endpoint (`GET /api/shares/{id}/pin`, "My shares"). This host
// function is the same door, opened for an app, with the same three rules:
//
//   - the link must be one this plugin itself minted;
//   - the ASKER must be the person who created it, or an administrator —
//     an app never reads a PIN "as the app", because the secret belongs to
//     the person, not to the module;
//   - every read writes the platform's own audit row, success or not, with
//     the same action name the "My shares" door writes.
//
// What comes back is a PIN or a REASON, never a blank: "no_pin",
// "no_secret_key", "not_recoverable" are the three honest answers, and the
// screen that asked can say which one it got.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// hfSharePIN implements share_pin.
func hfSharePIN(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	token := strings.ToLower(strings.TrimSpace(req.Token))
	if token == "" {
		return nil, hostErr(wire.ErrInvalid, "token is required")
	}
	sh, err := s.reg.opts.Store.GetShareByToken(ctx, token)
	if err != nil || sh == nil || sh.PluginID != s.plugin.Row.ID {
		return nil, hostErr(wire.ErrNotFound, "no such link")
	}
	// ⚠⚠ WHO is asking, not WHAT. A page event has a visitor, not an actor,
	// and the hourly wake-up has nobody at all: neither may read a PIN. Only
	// a call a signed-in person made — a screen or an action of theirs —
	// carries one, and it still has to be the right person.
	if s.actor == nil {
		return nil, hostErr(wire.ErrPermissionDenied, "a link's PIN is read by a person, not by an app")
	}
	owner := sh.CreatedBy != nil && *sh.CreatedBy == s.actor.ID
	if !owner && !s.actor.IsAdmin() {
		return nil, hostErr(wire.ErrPermissionDenied, "only the person who made this link, or an administrator, may read its PIN")
	}

	pin, reason := "", ""
	switch revealed, rerr := revealPIN(s, sh); {
	case rerr == nil:
		pin = revealed
	case errors.Is(rerr, share.ErrNoPIN):
		reason = "no_pin"
	case errors.Is(rerr, share.ErrNoSecretKey):
		reason = "no_secret_key"
	default:
		reason = "not_recoverable"
	}
	auditPinReveal(ctx, s, sh, owner, pin != "", reason)

	out := map[string]any{"pin": pin}
	if reason != "" {
		out["reason"] = reason
	}
	return out, nil
}

// revealPIN is the nil-safe call into the share service. No service (an
// instance built without one) is "cannot be shown", which is true.
func revealPIN(s *Scope, sh *model.Share) (string, error) {
	if s.reg.opts.Share == nil {
		return "", share.ErrPinNotRecoverable
	}
	return s.reg.opts.Share.RevealPIN(sh)
}

// auditPinReveal writes the trail row for one PIN read — the SAME action the
// "My shares" door writes, so an operator has one spelling to grep for and
// one place to read every reveal on the instance.
//
// ⚠ Neither the PIN nor the TOKEN is in it: a live public link sitting in an
// audit table is a live public link for everybody who can read audit rows.
func auditPinReveal(ctx context.Context, s *Scope, sh *model.Share, owner, revealed bool, reason string) {
	if s.reg.opts.Store == nil || s.actor == nil {
		return
	}
	uid := s.actor.ID
	meta := map[string]any{
		"revealed": revealed,
		"as_admin": !owner,
		"kind":     sh.Kind,
		// Which app asked, because "the requester read a signing PIN" and
		// "somebody read a PIN on the Shares screen" are different events.
		"plugin": s.plugin.Row.Name,
	}
	if reason != "" {
		meta["reason"] = reason
	}
	_ = s.reg.opts.Store.InsertAuditEntry(ctx, &model.AuditEntry{
		UserID:     &uid,
		Action:     share.AuditActionPinReveal,
		TargetType: "share",
		TargetID:   strconv.FormatInt(sh.ID, 10),
		Metadata:   meta,
		CreatedAt:  time.Now(),
	})
}
