// Package handlers — audit_targets.go
//
// WHICH thing an audit row is about, in words a person reads.
//
// ⚠⚠ The Panel's Recent activity and the Audit page printed the KIND of thing
// and nothing else — "Kullanıcı: oluşturuldu — Kullanıcı", "Depo: senkron
// başlatıldı — Depo #1" (release-candidate sweep, 2026-09-21). A row stores a
// target TYPE and an id; the id of a user, a storage or a share is a number
// nobody can read, and a create has no id at all (its URL has none and the
// middleware never reads bodies).
//
// Two halves, one answer (`target_name` on the wire):
//
//   - write time: the handler names what it made or removed
//     (auth.SetAuditTarget → metadata.target_name). This is the only answer
//     for something that no longer exists — "which user was deleted?";
//   - read time: an id that still resolves is named from the live row, so the
//     rows written before this change read properly too.
//
// ⚠ Resolved through the SCOPED store the handler already holds: a tenant
// admin's list is filtered to its own users' rows (audit.go), and a name is
// only ever looked up for a row that survived that filter.
package handlers

import (
	"context"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// auditNamer looks names up once per request, lazily, per kind.
type auditNamer struct {
	ctx      context.Context
	store    db.Store
	users    map[int64]string
	storages map[int64]string
	nodes    map[int64]string
	shares   map[int64]string
}

func newAuditNamer(ctx context.Context, store db.Store) *auditNamer {
	return &auditNamer{ctx: ctx, store: store}
}

// name is the row's target in words, or "" when there is nothing better than
// what the client already shows (a kind, or an id that is itself a name).
func (n *auditNamer) name(e *model.AuditEntry) string {
	if e == nil {
		return ""
	}
	if v, ok := e.Metadata["target_name"].(string); ok && v != "" {
		return v
	}
	id, err := strconv.ParseInt(e.TargetID, 10, 64)
	if err != nil || id <= 0 {
		return ""
	}
	switch e.TargetType {
	case "user":
		return n.user(id)
	case "storage":
		return n.storage(id)
	case "node":
		return n.node(id)
	case "share":
		return n.share(id)
	}
	return ""
}

func (n *auditNamer) user(id int64) string {
	if n.users == nil {
		n.users = map[int64]string{}
		if list, err := n.store.ListUsers(n.ctx); err == nil {
			for _, u := range list {
				n.users[u.ID] = u.Email
			}
		}
	}
	return n.users[id]
}

func (n *auditNamer) storage(id int64) string {
	if n.storages == nil {
		n.storages = map[int64]string{}
		if list, err := n.store.ListStorages(n.ctx); err == nil {
			for _, s := range list {
				n.storages[s.ID] = s.Name
			}
		}
	}
	return n.storages[id]
}

// node is the file's path — the same thing a handler records at write time.
func (n *auditNamer) node(id int64) string {
	if n.nodes == nil {
		n.nodes = map[int64]string{}
	}
	if v, ok := n.nodes[id]; ok {
		return v
	}
	out := ""
	if nd, err := n.store.GetNode(n.ctx, id); err == nil && nd != nil {
		// A storage this caller cannot list (another tenant's) names nothing.
		if st := n.storage(nd.StorageID); st != "" {
			out = nd.Path
		}
	}
	n.nodes[id] = out
	return out
}

func (n *auditNamer) share(id int64) string {
	if n.shares == nil {
		n.shares = map[int64]string{}
	}
	if v, ok := n.shares[id]; ok {
		return v
	}
	out := ""
	if sh, err := n.store.GetShareByID(n.ctx, id); err == nil && sh != nil {
		out = n.node(sh.NodeID)
	}
	n.shares[id] = out
	return out
}
