package acl

import (
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// App-plugin locks bind everyone: a locked path is viewer for the owner and
// for administrators alike, folder mutations that would carry the file are
// refused through LockWithin, and only the locking plugin's own jobs see the
// level without the cap.

func lockSet(role string, rels ...string) *Set {
	s := mkSet(role, false)
	until := time.Now().Add(time.Hour)
	s.locks = map[string]*model.AppPluginLock{}
	for _, rel := range rels {
		s.locks[CleanRel(rel)] = &model.AppPluginLock{StorageID: 7, Rel: rel, PluginID: 3, PluginName: "sign", Until: &until}
	}
	return s
}

func TestLock_CapsEveryoneAtViewer(t *testing.T) {
	for _, role := range []string{model.RoleAdmin, model.RoleUser} {
		s := lockSet(role, "contracts/nda.pdf")
		if got := s.Effective("contracts/nda.pdf"); got != LevelViewer {
			t.Errorf("%s on locked file: got %v want viewer", role, got)
		}
		if got := s.Effective("contracts/other.pdf"); got != LevelOwner && got != LevelEditor {
			t.Errorf("%s on sibling: got %v, the lock must not spread", role, got)
		}
		if got := s.Effective("contracts"); got < LevelEditor {
			t.Errorf("%s on the folder: got %v, uploads into it must stay open", role, got)
		}
		if got := s.EffectiveIgnoringLocks("contracts/nda.pdf"); got < LevelEditor {
			t.Errorf("%s ignoring locks: got %v, the locking plugin's own job needs the real level", role, got)
		}
	}
}

func TestLock_ViewerStaysViewer(t *testing.T) {
	s := lockSet(model.RoleViewer, "a.txt")
	if got := s.Effective("a.txt"); got != LevelViewer {
		t.Errorf("got %v want viewer", got)
	}
}

func TestLockWithin_CoversAncestorsNotSiblings(t *testing.T) {
	s := lockSet(model.RoleAdmin, "contracts/2026/nda.pdf")
	if s.LockWithin("contracts") == nil || s.LockWithin("contracts/2026") == nil || s.LockWithin("contracts/2026/nda.pdf") == nil {
		t.Error("the folder chain above a locked file must report the lock")
	}
	if s.LockWithin("contracts/2025") != nil || s.LockWithin("contracts/2026/other.pdf") != nil || s.LockWithin("contractsX") != nil {
		t.Error("siblings and look-alike prefixes must not report it")
	}
	if s.LockWithin("") == nil {
		t.Error("the storage root contains every lock")
	}
	if lockSet(model.RoleAdmin).LockWithin("contracts") != nil {
		t.Error("no locks, no answer")
	}
}

func TestLock_ExpiredIsIgnoredByLoadSet(t *testing.T) {
	// Live() is what LoadSet filters on; an expired lock never enters the set.
	past := time.Now().Add(-time.Minute)
	l := &model.AppPluginLock{Until: &past}
	if l.Live(time.Now()) {
		t.Error("an expired lock must not be live")
	}
	if !(&model.AppPluginLock{}).Live(time.Now()) {
		t.Error("a lock without until holds until lifted")
	}
}
