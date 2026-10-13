package smb

import (
	"context"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/storage"
)

var _ storage.SpaceReporter = (*Driver)(nil)

// The share's volume, as the server reports it (live: see liveDriver).
func TestLive_SpaceIsTheSharesVolume(t *testing.T) {
	d := liveDriver(t)
	ctx := context.Background()
	if err := d.Mkdir(ctx, ""); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	sp, err := d.Space(ctx, "belgeler/henuz-yok.txt")
	if err != nil {
		t.Fatalf("space: %v", err)
	}
	if sp.Total == 0 || sp.Free == 0 {
		t.Fatalf("a mounted share measured empty or full: %+v", sp)
	}
	if sp.Free > sp.Total || sp.Used > sp.Total {
		t.Errorf("free %d / used %d exceed the total %d", sp.Free, sp.Used, sp.Total)
	}
	if !strings.HasPrefix(sp.Volume, "smb://") {
		t.Errorf("volume %q", sp.Volume)
	}
}
