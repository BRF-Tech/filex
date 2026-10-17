package acl

import (
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

// A grant on the storage ROOT is how an administrator hands a whole storage
// to a person or a group (#117): its path prefix is "", the form
// model.FileGrant has always documented, so it needs no table and no second
// resolver of its own. These tests pin what the admin screen and every
// protocol may rely on, because nothing but a grant row stands behind the
// feature.
func TestStorageRootGrant_ReachesEveryPathInTheStorage(t *testing.T) {
	s := mkSet(model.RoleUser, true, grant("", model.GrantEditor))

	for _, rel := range []string{"", "file.txt", "Team", "Team/deep/report.pdf", "a/b/c/d/e"} {
		if got := s.Effective(rel); got != LevelEditor {
			t.Errorf("Effective(%q) = %v, want editor: a root grant covers the whole storage", rel, got)
		}
		if !s.CanSee(rel) {
			t.Errorf("CanSee(%q) = false, want true", rel)
		}
	}
}

// Without a grant the storage is not in the person's drive list; the root
// grant alone puts it there. This is what makes "assign the whole storage"
// a complete answer on an RBAC-on storage.
func TestStorageRootGrant_MakesTheStorageVisible(t *testing.T) {
	if mkSet(model.RoleUser, true).StorageVisible() {
		t.Error("no grant on an RBAC-on storage: the storage must not be listed")
	}
	if !mkSet(model.RoleUser, true, grant("", model.GrantViewer)).StorageVisible() {
		t.Error("a root grant must put the storage in the list")
	}
}

// Folder grants and the root grant live side by side, and the HIGHEST
// covering grant wins (acl's rule since the first version). A narrower
// folder grant therefore RAISES access inside that folder; it cannot lower
// it. The admin screen says so in words, and 0.57 does not change the rule:
// narrowing (deny) would touch every protocol.
func TestStorageRootGrant_HighestCoveringGrantStillWins(t *testing.T) {
	s := mkSet(model.RoleUser, true,
		grant("", model.GrantViewer),
		grant("Team", model.GrantEditor),
	)
	if got := s.Effective("Team/notes.md"); got != LevelEditor {
		t.Errorf("inside the folder grant: %v, want editor", got)
	}
	if got := s.Effective("Elsewhere/notes.md"); got != LevelViewer {
		t.Errorf("outside it the root grant still answers: %v, want viewer", got)
	}

	// The other way round: a root grant does not pull a wider folder grant
	// down, and it does not pull itself down either.
	wide := mkSet(model.RoleUser, true,
		grant("", model.GrantEditor),
		grant("Team", model.GrantViewer),
	)
	if got := wide.Effective("Team/notes.md"); got != LevelEditor {
		t.Errorf("a narrower viewer grant must not narrow the root's editor: %v", got)
	}
}

// Revoking the storage assignment removes one row: every folder grant the
// storage carried is still there and still answers. (The handler deletes by
// id; this is the ACL half of that promise.)
func TestStorageRootGrant_RevokingItLeavesFolderGrantsAlone(t *testing.T) {
	folder := grant("Team", model.GrantEditor)
	before := mkSet(model.RoleUser, true, grant("", model.GrantViewer), folder)
	if got := before.Effective("Private/x"); got != LevelViewer {
		t.Fatalf("before the revoke: %v, want viewer", got)
	}

	after := mkSet(model.RoleUser, true, folder)
	if got := after.Effective("Private/x"); got != LevelNone {
		t.Errorf("after the revoke the storage-wide access is gone: %v, want none", got)
	}
	if got := after.Effective("Team/notes.md"); got != LevelEditor {
		t.Errorf("the folder grant must survive the revoke: %v, want editor", got)
	}
	if !after.StorageVisible() {
		t.Error("the folder grant still makes the storage visible")
	}
}

// The account role is still the ceiling: a viewer account handed the whole
// storage as owner reads it and nothing more.
func TestStorageRootGrant_ViewerAccountStaysReadOnly(t *testing.T) {
	s := mkSet(model.RoleViewer, true, grant("", model.GrantOwner))
	for _, rel := range []string{"", "Team/deep/report.pdf"} {
		if got := s.Effective(rel); got != LevelViewer {
			t.Errorf("Effective(%q) = %v, want viewer: the account role caps the grant", rel, got)
		}
	}
}
