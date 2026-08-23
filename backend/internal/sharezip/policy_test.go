package sharezip

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// ---------------------------------------------------------------- warm ceiling

// The warmer must not pre-build a folder bigger than WarmMaxBytes. It is a
// limit on SPECULATIVE work only: a visitor who clicks "download all" on that
// folder still gets the on-demand build, exactly as before.
func TestWarm_SkipsAFolderOverTheCeilingButOnDemandStillBuildsIt(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	c.WarmMaxBytes = 10

	drv := &fakeDriver{root: "/big", files: map[string][]byte{
		"/big/a.bin": make([]byte, 8),
		"/big/b.bin": make([]byte, 8), // 16 bytes in total, over the 10-byte ceiling
	}}

	did, err := c.Warm(context.Background(), drv, "/big", 42)
	if !errors.Is(err, ErrTooLargeToWarm) {
		t.Fatalf("Warm over the ceiling: did=%v err=%v, want ErrTooLargeToWarm", did, err)
	}
	if did {
		t.Fatal("Warm reported a build for a folder it must not pre-build")
	}
	if left := names(t, dir); len(left) != 0 {
		t.Fatalf("warm over the ceiling left files behind: %v", left)
	}

	// On demand — the path the download handler takes — the same folder
	// builds: the ceiling refuses nothing a visitor asked for.
	cachePath, files, err := c.Plan(context.Background(), drv, "/big", 42)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := c.StartOrGet(cachePath, files, 42, drv).Wait(context.Background()); err != nil {
		t.Fatalf("on-demand build: %v", err)
	}
	if _, ok := c.Cached(cachePath); !ok {
		t.Fatal("on-demand build of an over-ceiling folder produced no archive")
	}

	// And a folder under the ceiling is still warmed.
	small := &fakeDriver{root: "/small", files: map[string][]byte{"/small/a.bin": make([]byte, 4)}}
	did, err = c.Warm(context.Background(), small, "/small", 43)
	if err != nil || !did {
		t.Fatalf("Warm under the ceiling: did=%v err=%v, want a build", did, err)
	}
}

// Zero means no ceiling — the pre-v0.25 behaviour, for operators who want it.
func TestWarm_ZeroCeilingMeansUnlimited(t *testing.T) {
	c := New(t.TempDir())
	c.WarmMaxBytes = 0
	drv := &fakeDriver{root: "/big", files: map[string][]byte{"/big/a.bin": make([]byte, 4096)}}
	did, err := c.Warm(context.Background(), drv, "/big", 1)
	if err != nil || !did {
		t.Fatalf("unlimited Warm: did=%v err=%v", did, err)
	}
}

// The warmer logs an over-ceiling folder once, not every five minutes forever.
func TestWarmer_LogsAnOverCeilingFolderOnce(t *testing.T) {
	c := New(t.TempDir())
	c.WarmMaxBytes = 1
	drv := &fakeDriver{root: "/big", files: map[string][]byte{"/big/a.bin": make([]byte, 2)}}
	var logs []string
	w := NewWarmer(c,
		newShareList(DirShare{StorageID: 1, Path: "/big", NodeID: 9}).list,
		func(int64) (storage.Driver, error) { return drv, nil },
		time.Hour,
		func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	w.runOnce(context.Background())
	w.runOnce(context.Background())
	w.runOnce(context.Background())
	n := 0
	for _, l := range logs {
		if strings.Contains(l, "over the warm ceiling") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("over-ceiling folder logged %d times over three passes, want exactly once; logs=%v", n, logs)
	}
}

// ---------------------------------------------------------------- max age

// An archive older than MaxAge is swept even though its share is alive: it is
// regenerable, and a week-old file nobody has asked for is disk, not cache.
// The warmer's next pass rebuilds it if the folder is still worth warming.
func TestSweep_AgesOutTheArchiveOfALiveShare(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	c.MaxAge = 7 * 24 * time.Hour

	old := fmt.Sprintf("7-%s.zip", sig)
	fresh := fmt.Sprintf("7-%s.zip", "fedcba9876543210")
	writeFile(t, dir, old, 30, 8*24*time.Hour)
	writeFile(t, dir, fresh, 10, 24*time.Hour)

	active := NewActiveShares(newShareList(DirShare{StorageID: 1, Path: "/shared", NodeID: 7}).list)
	if _, err := active.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	c.Track(active)

	removed, freed := c.Sweep()
	left := names(t, dir)
	if removed != 1 || freed != 30 {
		t.Fatalf("swept %d / %d bytes, want 1 / 30 (the 8-day-old archive); left=%v", removed, freed, left)
	}
	if has(left, old) {
		t.Errorf("an archive older than MaxAge survived: %v", left)
	}
	if !has(left, fresh) {
		t.Errorf("a fresh archive of a live share was swept: %v", left)
	}
}

// MaxAge zero keeps archives for as long as their share lives (old behaviour).
func TestSweep_ZeroMaxAgeNeverAgesOut(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	c.MaxAge = 0
	old := fmt.Sprintf("7-%s.zip", sig)
	writeFile(t, dir, old, 30, 400*24*time.Hour)
	active := NewActiveShares(newShareList(DirShare{StorageID: 1, Path: "/shared", NodeID: 7}).list)
	if _, err := active.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	c.Track(active)
	if removed, _ := c.Sweep(); removed != 0 {
		t.Fatalf("swept %d with MaxAge=0, want 0", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, old)); err != nil {
		t.Fatalf("archive gone: %v", err)
	}
}
