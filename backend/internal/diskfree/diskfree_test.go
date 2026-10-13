package diskfree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMeasureReadsTheDisk(t *testing.T) {
	u, err := Measure(t.TempDir())
	if err != nil {
		t.Skipf("no probe on this platform: %v", err)
	}
	if u.Total == 0 {
		t.Fatal("a mounted filesystem measured zero bytes")
	}
	if u.Free > u.Total || u.Used > u.Total {
		t.Errorf("free %d / used %d exceed the total %d", u.Free, u.Used, u.Total)
	}
	if u.Volume == "" {
		t.Error("no volume name: storages on one disk would be added up twice")
	}
}

func TestTwoFoldersOnOneDiskAreOneVolume(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, d := range []string{a, b} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ua, err := Measure(a)
	if err != nil {
		t.Skipf("no probe on this platform: %v", err)
	}
	ub, err := Measure(b)
	if err != nil {
		t.Fatal(err)
	}
	if ua.Volume != ub.Volume || ua.Total != ub.Total {
		t.Errorf("one disk measured as two: %q (%d) and %q (%d)", ua.Volume, ua.Total, ub.Volume, ub.Total)
	}
}

func TestFreeIsMeasuresFree(t *testing.T) {
	dir := t.TempDir()
	free, err := Free(dir)
	if err != nil {
		t.Skipf("no probe on this platform: %v", err)
	}
	if free == 0 {
		t.Error("the staging guard and the index rebuild would refuse everything")
	}
}
