package acl

import (
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

func mkSet(role string, rbac bool, grants ...*model.FileGrant) *Set {
	return &Set{
		user:    &model.User{ID: 1, Role: role},
		storage: &model.Storage{ID: 7, RBACEnabled: rbac},
		grants:  grants,
		ceiling: RoleCeiling(role),
	}
}

func grant(prefix, level string) *model.FileGrant {
	return &model.FileGrant{StorageID: 7, UserID: 1, PathPrefix: prefix, Level: level, IsDir: true}
}

func TestParseLevelRoundTrip(t *testing.T) {
	for _, s := range []string{model.GrantViewer, model.GrantEditor, model.GrantOwner} {
		if got := ParseLevel(s).String(); got != s {
			t.Errorf("round trip %q -> %q", s, got)
		}
	}
	if ParseLevel("bogus") != LevelNone {
		t.Error("unknown level should be LevelNone")
	}
}

func TestRoleCeiling(t *testing.T) {
	cases := map[string]Level{
		model.RoleAdmin:  LevelOwner,
		model.RoleUser:   LevelOwner,
		model.RoleViewer: LevelViewer,
		"weird":          LevelNone,
	}
	for role, want := range cases {
		if got := RoleCeiling(role); got != want {
			t.Errorf("RoleCeiling(%q)=%v want %v", role, got, want)
		}
	}
}

func TestCleanRel(t *testing.T) {
	cases := map[string]string{
		"":               "",
		"/":              "",
		".":              "",
		"a/b/":           "a/b",
		"/a/b":           "a/b",
		"a/../b":         "b",
		"a/./b":          "a/b",
		"projeler/acme/": "projeler/acme",
	}
	for in, want := range cases {
		if got := CleanRel(in); got != want {
			t.Errorf("CleanRel(%q)=%q want %q", in, got, want)
		}
	}
}

func TestPrefixContains(t *testing.T) {
	if !prefixContains("", "anything/here") {
		t.Error("root prefix should contain all")
	}
	if !prefixContains("a/b", "a/b") {
		t.Error("equal should match")
	}
	if !prefixContains("a/b", "a/b/c") {
		t.Error("descendant should match")
	}
	if prefixContains("a/b", "a/bc") {
		t.Error("sibling-with-shared-prefix must NOT match")
	}
	if prefixContains("a/b", "a") {
		t.Error("ancestor must NOT be contained by descendant prefix")
	}
}

func TestEffectiveAdmin(t *testing.T) {
	s := mkSet(model.RoleAdmin, true) // admin, RBAC on, no grants
	if got := s.Effective("any/path"); got != LevelOwner {
		t.Errorf("admin should be Owner everywhere, got %v", got)
	}
}

func TestEffectiveRBACOff(t *testing.T) {
	if got := mkSet(model.RoleUser, false).Effective("x"); got != LevelEditor {
		t.Errorf("user on RBAC-off should be Editor, got %v", got)
	}
	if got := mkSet(model.RoleViewer, false).Effective("x"); got != LevelViewer {
		t.Errorf("viewer on RBAC-off should be Viewer, got %v", got)
	}
}

func TestEffectiveRBACOnGrants(t *testing.T) {
	s := mkSet(model.RoleUser, true, grant("projeler/acme", model.GrantEditor))
	if got := s.Effective("projeler/acme"); got != LevelEditor {
		t.Errorf("direct grant: got %v want editor", got)
	}
	if got := s.Effective("projeler/acme/sub/file.txt"); got != LevelEditor {
		t.Errorf("inherited from folder grant: got %v want editor", got)
	}
	if got := s.Effective("projeler/other"); got != LevelNone {
		t.Errorf("ungranted sibling: got %v want none", got)
	}
	if got := s.Effective("projeler"); got != LevelNone {
		t.Errorf("ancestor of grant has no capability itself: got %v want none", got)
	}
}

func TestEffectiveHighestGrantWins(t *testing.T) {
	s := mkSet(model.RoleUser, true,
		grant("projeler", model.GrantViewer),
		grant("projeler/acme", model.GrantOwner),
	)
	if got := s.Effective("projeler/acme/x"); got != LevelOwner {
		t.Errorf("highest covering grant should win: got %v want owner", got)
	}
	if got := s.Effective("projeler/readme"); got != LevelViewer {
		t.Errorf("only the broad viewer grant covers here: got %v want viewer", got)
	}
}

func TestViewerAccountCeilingCapsGrant(t *testing.T) {
	// A viewer ACCOUNT handed an editor item-grant stays read-only.
	s := mkSet(model.RoleViewer, true, grant("projeler/acme", model.GrantEditor))
	if got := s.Effective("projeler/acme/x"); got != LevelViewer {
		t.Errorf("viewer ceiling must cap editor grant to viewer, got %v", got)
	}
}

func TestCanSeeAncestorTraversal(t *testing.T) {
	s := mkSet(model.RoleUser, true, grant("projeler/acme", model.GrantEditor))
	if !s.CanSee("") {
		t.Error("root should be visible (ancestor of a grant)")
	}
	if !s.CanSee("projeler") {
		t.Error("ancestor folder should be visible as traversal node")
	}
	if !s.CanSee("projeler/acme") {
		t.Error("granted folder should be visible")
	}
	if !s.CanSee("projeler/acme/deep/file") {
		t.Error("descendant of grant should be visible")
	}
	if s.CanSee("projeler/other") {
		t.Error("ungranted sibling must be hidden")
	}
	if s.CanSee("baska") {
		t.Error("unrelated top-level must be hidden")
	}
}

func TestStorageVisible(t *testing.T) {
	if !mkSet(model.RoleAdmin, true).StorageVisible() {
		t.Error("admin sees all storages")
	}
	if !mkSet(model.RoleUser, false).StorageVisible() {
		t.Error("RBAC-off storage visible to everyone")
	}
	if mkSet(model.RoleUser, true).StorageVisible() {
		t.Error("RBAC-on storage with no grant must be hidden")
	}
	if !mkSet(model.RoleUser, true, grant("x", model.GrantViewer)).StorageVisible() {
		t.Error("RBAC-on storage with a grant must be visible")
	}
}

func TestNilSafety(t *testing.T) {
	var s *Set
	if s.Effective("x") != LevelNone || s.CanSee("x") || s.StorageVisible() {
		t.Error("nil set must be inert")
	}
	anon := &Set{} // no user
	if anon.Effective("x") != LevelNone || anon.CanSee("x") || anon.StorageVisible() {
		t.Error("userless set must be inert")
	}
}
