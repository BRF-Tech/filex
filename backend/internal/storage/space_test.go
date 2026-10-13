package storage

import (
	"context"
	"errors"
	"testing"
)

// roomDriver is a fakeDriver that knows how big its disk is.
type roomDriver struct {
	*fakeDriver
	room Space
	asks []string
}

func (r *roomDriver) Space(_ context.Context, p string) (Space, error) {
	r.asks = append(r.asks, p)
	return r.room, nil
}

func TestSpaceOfAsksTheDriver(t *testing.T) {
	d := &roomDriver{fakeDriver: newFakeDriver("disk"), room: Space{Total: 100, Used: 40, Free: 60, Volume: "dev:1"}}
	sp, err := SpaceOf(context.Background(), d, "docs/2026")
	if err != nil {
		t.Fatalf("SpaceOf: %v", err)
	}
	if sp != d.room {
		t.Errorf("got %+v, want %+v", sp, d.room)
	}
	if len(d.asks) != 1 || d.asks[0] != "docs/2026" {
		t.Errorf("the driver was asked about %v, want [docs/2026]", d.asks)
	}
}

func TestSpaceOfADriverThatCannotTell(t *testing.T) {
	if _, err := SpaceOf(context.Background(), newFakeDriver("bucket"), ""); !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}

// A replicated storage is handed out wrapped (and shaped). Its room is the
// primary's: the wrapper must not hide it, and the replica's disk is not the
// one a write fills first.
func TestSpaceOfAReplicatedStorageIsItsPrimary(t *testing.T) {
	primary := &roomDriver{fakeDriver: newFakeDriver("disk"), room: Space{Total: 500, Used: 100, Free: 400}}
	replica := &roomDriver{fakeDriver: newFakeDriver("target"), room: Space{Total: 9, Used: 9, Free: 0}}
	r := NewReplicated(primary, replica, DefaultRules(), nil, nil)
	t.Cleanup(func() { _ = r.Close() })

	sp, err := SpaceOf(context.Background(), r.Shaped(), "")
	if err != nil {
		t.Fatalf("SpaceOf: %v", err)
	}
	if sp != primary.room {
		t.Errorf("got %+v, want the primary's %+v", sp, primary.room)
	}
	if len(replica.asks) != 0 {
		t.Error("the replica was asked")
	}
}
