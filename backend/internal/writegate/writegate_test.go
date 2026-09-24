package writegate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// table is a Locks over a plain map, the way acl.Set answers.
type table map[string]*model.AppPluginLock

func (m table) Lock(rel string) *model.AppPluginLock { return m[rel] }
func (m table) LockWithin(rel string) *model.AppPluginLock {
	if l := m[rel]; l != nil {
		return l
	}
	for k, l := range m {
		if rel == "" || strings.HasPrefix(k, rel+"/") {
			return l
		}
	}
	return nil
}

func TestCheck(t *testing.T) {
	sign := &model.AppPluginLock{Rel: "imza/NDA.docx", PluginID: 7, PluginName: "sign"}
	locks := table{"imza/NDA.docx": sign}

	cases := []struct {
		name    string
		app     int64
		targets []Target
		want    error
	}{
		{"an ordinary write", 0, []Target{Writes("docs/a.txt")}, nil},
		{"the frozen file", 0, []Target{Writes("imza/NDA.docx")}, ErrLocked},
		{"the folder around it", 0, []Target{Writes("imza")}, ErrLocked},
		{"the whole storage", 0, []Target{Writes("")}, ErrLocked},
		{"named, not changed: the folder a file goes INTO", 0, []Target{Names("imza")}, nil},
		{"named, not changed: a copy's source", 0, []Target{Names("imza/NDA.docx")}, nil},
		{"a sibling next to it", 0, []Target{Writes("imza/NDA-imzali.pdf")}, nil},
		{"the holder app", 7, []Target{Writes("imza/NDA.docx")}, nil},
		{"another app", 8, []Target{Writes("imza/NDA.docx")}, ErrLocked},
		{"a reserved name, even named", 0, []Target{Names(".filex-trash")}, syspath.ErrReserved},
		{"reserved is judged before locks", 0, []Target{Writes("imza"), Writes(".versions/1")}, syspath.ErrReserved},
		{"the desktop's claim", 0, []Target{Writes(".filex-open/a1b2c3d4e5f6-x.docx").As(syspath.PutWorkCopy)}, nil},
		{"a protocol's keep marker", 0, []Target{Writes("docs/.keepdir").As(syspath.Mounted)}, nil},
	}
	for _, c := range cases {
		err := Check(locks, c.app, c.targets...)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: Check = %v, want %v", c.name, err, c.want)
		}
	}

	var le *LockedError
	if err := Check(locks, 0, Writes("imza")); !errors.As(err, &le) || le.Lock != sign || le.Rel != "imza" {
		t.Fatalf("the refusal does not carry the lock and the path: %#v", err)
	}
	if err := Check(nil, 0, Writes("imza/NDA.docx")); err != nil {
		t.Fatalf("no lock table must judge names only: %v", err)
	}
}

func TestAppContext(t *testing.T) {
	ctx := context.Background()
	if AppFrom(ctx) != 0 {
		t.Fatal("a plain context names an app")
	}
	if AppFrom(WithApp(ctx, 7)) != 7 {
		t.Fatal("WithApp did not carry the app")
	}
}
