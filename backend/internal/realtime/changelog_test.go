package realtime

import (
	"strings"
	"testing"
)

func TestChangeLog_AFreshCursorIsUnchanged(t *testing.T) {
	l := NewChangeLog(16)
	changed, cur := l.ChangedSince(1, "docs", "")
	if !changed {
		t.Fatal("no cursor at all must read as changed: the caller has never looked")
	}
	changed, next := l.ChangedSince(1, "docs", cur)
	if changed || next != cur {
		t.Fatalf("nothing happened: changed=%v next=%q cur=%q", changed, next, cur)
	}
}

func TestChangeLog_ChangesUnderTheFolderCount(t *testing.T) {
	l := NewChangeLog(16)
	_, cur := l.ChangedSince(1, "04 Projeler", "")

	l.EmitChange(1, "04 Projeler/Ofis/Çizim", ChangeEvent{Action: "upload", Name: "a.psd"})
	if changed, _ := l.ChangedSince(1, "04 Projeler", cur); !changed {
		t.Fatal("a change in a descendant must count")
	}
	if changed, _ := l.ChangedSince(1, "04 Projeler/Ofis", cur); !changed {
		t.Fatal("…at every level above it")
	}
	if changed, _ := l.ChangedSince(1, "05 Başka", cur); changed {
		t.Fatal("a sibling tree did not change")
	}
	if changed, _ := l.ChangedSince(2, "04 Projeler", cur); changed {
		t.Fatal("another storage did not change")
	}
}

// A change in an ancestor folder matters only when it names the child on the
// way to the folder asked about: renaming or deleting it moves or removes the
// whole tree. An unrelated file in the ancestor is not a change here.
func TestChangeLog_AncestorChangesCountOnlyOnThePath(t *testing.T) {
	l := NewChangeLog(16)
	_, cur := l.ChangedSince(1, "a/b/c", "")

	l.EmitChange(1, "a", ChangeEvent{Action: "upload", Name: "unrelated.txt"})
	if changed, _ := l.ChangedSince(1, "a/b/c", cur); changed {
		t.Fatal("an unrelated file in an ancestor is not a change under a/b/c")
	}
	l.EmitChange(1, "a", ChangeEvent{Action: "rename", Name: "b", NewName: "b2"})
	if changed, _ := l.ChangedSince(1, "a/b/c", cur); !changed {
		t.Fatal("renaming a/b moves a/b/c")
	}

	_, cur = l.ChangedSince(1, "a/b/c", "")
	l.EmitChange(1, "", ChangeEvent{Action: "move", Name: ""})
	if changed, _ := l.ChangedSince(1, "a/b/c", cur); !changed {
		t.Fatal("an ancestor event that does not say what it touched must count")
	}
}

func TestChangeLog_TheRootSeesEverything(t *testing.T) {
	l := NewChangeLog(16)
	_, cur := l.ChangedSince(1, "", "")
	l.EmitChange(1, "/deep/down/", ChangeEvent{Action: "delete", Name: "x"})
	if changed, _ := l.ChangedSince(1, "", cur); !changed {
		t.Fatal("the storage root covers every folder")
	}
}

// A cursor older than what the ring still holds cannot be answered: it must
// read as changed, never as "nothing happened".
func TestChangeLog_AnOverflowedCursorIsChanged(t *testing.T) {
	l := NewChangeLog(4)
	_, cur := l.ChangedSince(1, "quiet", "")
	for i := 0; i < 10; i++ {
		l.EmitChange(1, "busy", ChangeEvent{Action: "upload", Name: "f"})
	}
	if changed, _ := l.ChangedSince(1, "quiet", cur); !changed {
		t.Fatal("the ring forgot changes after this cursor; it cannot promise nothing happened")
	}
}

// A cursor from another process (the server restarted) or a garbled one is
// unknown, and unknown is changed.
func TestChangeLog_ACursorFromAnotherBootIsChanged(t *testing.T) {
	a, b := NewChangeLog(16), NewChangeLog(16)
	_, cur := a.ChangedSince(1, "", "")
	if changed, _ := b.ChangedSince(1, "", cur); !changed {
		t.Fatal("another boot's cursor must read as changed")
	}
	for _, junk := range []string{"x", "x.y", strings.Repeat("9", 40)} {
		if changed, _ := a.ChangedSince(1, "", junk); !changed {
			t.Fatalf("garbled cursor %q must read as changed", junk)
		}
	}
}

// The log sits in front of the rest of the emitter chain and passes every
// event on.
func TestChangeLog_WrapPassesEventsOn(t *testing.T) {
	l := NewChangeLog(16)
	var got []string
	inner := emitterFunc(func(storageID int64, dir string, ev ChangeEvent) { got = append(got, dir+"/"+ev.Name) })
	e := l.Wrap(inner)
	_, cur := l.ChangedSince(7, "d", "")
	e.EmitChange(7, "d", ChangeEvent{Action: "upload", Name: "f"})
	if len(got) != 1 || got[0] != "d/f" {
		t.Fatalf("inner saw %v", got)
	}
	if changed, _ := l.ChangedSince(7, "d", cur); !changed {
		t.Fatal("the wrapped event must be recorded")
	}
}

type emitterFunc func(storageID int64, dir string, ev ChangeEvent)

func (f emitterFunc) EmitChange(storageID int64, dir string, ev ChangeEvent) { f(storageID, dir, ev) }

// A change to something the caller cannot see is not a change for them.
func TestChangeLog_InvisibleChangesDoNotCount(t *testing.T) {
	l := NewChangeLog(16)
	_, cur := l.ChangedSince(1, "shared", "")
	l.EmitChange(1, "shared/hr", ChangeEvent{Action: "upload", Name: "salaries.xlsx"})
	canSee := func(p string) bool { return !strings.HasPrefix(p, "shared/hr") }

	if changed, _ := l.ChangedSinceFor(1, "shared", cur, canSee); changed {
		t.Fatal("a change inside a folder the caller cannot see leaked as 'changed'")
	}
	l.EmitChange(1, "shared", ChangeEvent{Action: "rename", Name: "hr", NewName: "people"})
	if changed, _ := l.ChangedSinceFor(1, "shared", cur, canSee); !changed {
		t.Fatal("a rename the caller can see (the new name) must count")
	}
}
