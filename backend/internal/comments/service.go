// Package comments implements flat, chronological comment threads on
// file/folder nodes (v0.6 "Çalışma" wave, migration 00020).
//
// Access model (enforced by the caller/handler via acl.Resolver):
// anyone who can SEE a node (≥viewer) may read AND write comments on it;
// deleting a comment is author-or-admin (enforced HERE, since it depends
// on comment ownership, not node ACL). Rows are soft-deleted; hard
// removal rides the nodes FK CASCADE plus the trash purge hook.
package comments

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// MaxBodyLen is the maximum comment length in runes (spec: 1..5000).
const MaxBodyLen = 5000

// Sentinel errors the handler maps to HTTP statuses.
var (
	// ErrBadBody — empty (after trim) or over-length body → 400.
	ErrBadBody = errors.New("comments: body must be 1..5000 characters")
	// ErrNotFound — comment or node missing/soft-deleted → 404.
	ErrNotFound = errors.New("comments: not found")
	// ErrForbidden — delete by someone who is neither author nor admin → 403.
	ErrForbidden = errors.New("comments: forbidden")
)

// Service is the comment engine. Thin by design: validation + ownership
// rules here, SQL in db.Store, node-visibility ACL in the handler.
type Service struct {
	Store db.Store
}

// New constructs a Service.
func New(store db.Store) *Service { return &Service{Store: store} }

// List returns the live comments of a node, oldest first, author-joined.
// Never returns nil on success (empty slice instead) so the JSON encodes
// as [] rather than null.
func (s *Service) List(ctx context.Context, nodeID int64) ([]*model.NodeComment, error) {
	if s == nil || s.Store == nil {
		return nil, errors.New("comments: service not initialised")
	}
	rows, err := s.Store.ListNodeComments(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []*model.NodeComment{}
	}
	return rows, nil
}

// Add validates the body (1..5000 runes after trim) and inserts a comment
// by user on node. The caller must have verified node visibility already.
func (s *Service) Add(ctx context.Context, nodeID, userID int64, body string) (*model.NodeComment, error) {
	if s == nil || s.Store == nil {
		return nil, errors.New("comments: service not initialised")
	}
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > MaxBodyLen {
		return nil, ErrBadBody
	}
	return s.Store.CreateNodeComment(ctx, &model.NodeComment{
		NodeID: nodeID,
		UserID: userID,
		Body:   body,
	})
}

// Delete soft-deletes a comment. Only the comment's author or an admin
// account may delete; anyone else gets ErrForbidden. A missing (or
// already deleted) comment yields ErrNotFound.
func (s *Service) Delete(ctx context.Context, id int64, user *model.User) (*model.NodeComment, error) {
	if s == nil || s.Store == nil {
		return nil, errors.New("comments: service not initialised")
	}
	if user == nil {
		return nil, ErrForbidden
	}
	c, err := s.Store.GetNodeComment(ctx, id)
	if err != nil || c == nil {
		return nil, ErrNotFound
	}
	if !CanDelete(c, user) {
		return nil, ErrForbidden
	}
	if err := s.Store.SoftDeleteNodeComment(ctx, id); err != nil {
		return nil, ErrNotFound
	}
	return c, nil
}

// CanDelete reports whether user may delete comment c: the author or any
// admin account. Shared with the handler so list responses can stamp a
// per-row `can_delete` for the UI ("kendi yorumunda sil").
func CanDelete(c *model.NodeComment, user *model.User) bool {
	if c == nil || user == nil {
		return false
	}
	return c.UserID == user.ID || user.Role == model.RoleAdmin
}
