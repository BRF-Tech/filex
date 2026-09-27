package acl

import (
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

// A drafts area is one person's (issue #71): the owner edits their drafts,
// and nobody else reaches them — not an administrator, not a grant on the
// storage root, not the role base of an RBAC-off storage.

func TestDrafts_OnlyTheOwnerReachesThem(t *testing.T) {
	own := ".filex-drafts/1/0123456789abcdef/notes.txt" // mkSet's user is id 1
	other := ".filex-drafts/2/0123456789abcdef/notes.txt"

	for _, role := range []string{model.RoleAdmin, model.RoleUser} {
		for _, rbac := range []bool{false, true} {
			s := mkSet(role, rbac, grant("", model.GrantOwner))
			if got := s.Effective(own); got != LevelEditor {
				t.Errorf("%s rbac=%v on own draft: got %v, want editor", role, rbac, got)
			}
			if got := s.Effective(other); got != LevelNone {
				t.Errorf("%s rbac=%v on somebody else's draft: got %v, want none", role, rbac, got)
			}
			if got := s.Effective(".filex-drafts"); got != LevelNone {
				t.Errorf("%s rbac=%v on the drafts area's root: got %v, want none", role, rbac, got)
			}
			// …and nothing else moved.
			if got := s.Effective("Documents/notes.txt"); got < LevelEditor {
				t.Errorf("%s rbac=%v on an ordinary file: got %v", role, rbac, got)
			}
		}
	}
}

func TestDrafts_AViewerCannotWriteEvenOwnDrafts(t *testing.T) {
	// The role ceiling still applies: a viewer account makes no documents,
	// and a draft is a document on its way to being one.
	s := mkSet(model.RoleViewer, false)
	if got := s.Effective(".filex-drafts/1/0123456789abcdef/notes.txt"); got > LevelViewer {
		t.Errorf("viewer on own draft: got %v", got)
	}
}
