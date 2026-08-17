package sharezip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// ---------------------------------------------------------------- helpers

// fakeDriver serves a fixed tree from memory. Read hands back a reader that
// yields `chunk` bytes at a time with a small pause, so a build can be caught
// in the middle of one file rather than only between files.
type fakeDriver struct {
	root   string
	files  map[string][]byte // full path -> content
	chunk  int
	pause  time.Duration
	onRead func(path string)
	served atomic.Int64
}

func (d *fakeDriver) Init(context.Context, map[string]any) error { return nil }
func (d *fakeDriver) Name() string                               { return "fake" }
func (d *fakeDriver) Capabilities() storage.Capabilities         { return storage.Capabilities{Read: true} }

func (d *fakeDriver) List(_ context.Context, path string) ([]storage.Object, error) {
	if path != d.root {
		return nil, nil
	}
	names := make([]string, 0, len(d.files))
	for p := range d.files {
		names = append(names, p)
	}
	// Deterministic order: the tests name their files a/b/c on purpose.
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	out := make([]storage.Object, 0, len(names))
	for _, p := range names {
		out = append(out, storage.Object{
			Path:  p,
			Name:  filepath.Base(p),
			Size:  int64(len(d.files[p])),
			Kind:  storage.KindFile,
			Mtime: time.Unix(1000, 0),
		})
	}
	return out, nil
}

func (d *fakeDriver) Stat(_ context.Context, path string) (storage.Object, error) {
	b, ok := d.files[path]
	if !ok {
		return storage.Object{}, storage.ErrNotFound
	}
	return storage.Object{Path: path, Name: filepath.Base(path), Size: int64(len(b)), Kind: storage.KindFile}, nil
}

func (d *fakeDriver) Read(_ context.Context, path string) (io.ReadCloser, error) {
	b, ok := d.files[path]
	if !ok {
		return nil, storage.ErrNotFound
	}
	if d.onRead != nil {
		d.onRead(path)
	}
	chunk := d.chunk
	if chunk <= 0 {
		chunk = len(b) + 1
	}
	return &chunkReader{b: b, chunk: chunk, pause: d.pause, served: &d.served}, nil
}

type chunkReader struct {
	b      []byte
	off    int
	chunk  int
	pause  time.Duration
	served *atomic.Int64
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	if r.pause > 0 {
		time.Sleep(r.pause)
	}
	n := r.chunk
	if n > len(p) {
		n = len(p)
	}
	if r.off+n > len(r.b) {
		n = len(r.b) - r.off
	}
	copy(p, r.b[r.off:r.off+n])
	r.off += n
	r.served.Add(int64(n))
	return n, nil
}

func (r *chunkReader) Close() error { return nil }

// shareList is a controllable stand-in for the store query the warmer uses.
type shareList struct {
	shares atomic.Value // []DirShare
	err    atomic.Value // error
}

func newShareList(s ...DirShare) *shareList {
	l := &shareList{}
	l.shares.Store(s)
	return l
}

func (l *shareList) set(s ...DirShare) { l.shares.Store(s) }
func (l *shareList) fail(err error)    { l.err.Store(err) }

func (l *shareList) list(context.Context) ([]DirShare, error) {
	if e, ok := l.err.Load().(error); ok && e != nil {
		return nil, e
	}
	return l.shares.Load().([]DirShare), nil
}

func writeFile(t *testing.T, dir, name string, size int, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if age > 0 {
		old := time.Now().Add(-age)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}
	return p
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func has(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

const sig = "0123456789abcdef"

// ---------------------------------------------------------------- sweeper

// The incident this whole change exists for: an archive whose share is gone
// used to live forever, because the only cleanup (pruneOld) removes older
// SIGNATURES of a node that is still shared.
func TestSweep_RemovesArchivesWithNoLiveShareAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	live := fmt.Sprintf("7-%s.zip", sig)
	dead := fmt.Sprintf("3748-%s.zip", sig)
	otherSigSameLiveNode := fmt.Sprintf("7-%s.zip", "fedcba9876543210")
	writeFile(t, dir, live, 10, 0)
	writeFile(t, dir, dead, 15, 0)
	writeFile(t, dir, otherSigSameLiveNode, 10, 0)
	writeFile(t, dir, ".tmp-stale.zip", 20, 2*time.Hour)
	writeFile(t, dir, ".tmp-fresh.zip", 20, 0)
	writeFile(t, dir, "notes.txt", 5, 0)
	writeFile(t, dir, "12-nothexadecimal.zip", 5, 0)

	list := newShareList(DirShare{StorageID: 1, Path: "/shared", NodeID: 7})
	active := NewActiveShares(list.list)
	if _, err := active.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	c.Track(active)

	removed, freed := c.Sweep()
	left := names(t, dir)

	if removed != 2 || freed != 35 {
		t.Fatalf("swept %d files / %d bytes, want 2 / 35 (dead zip + stale tmp); left = %v", removed, freed, left)
	}
	if has(left, dead) {
		t.Errorf("archive of a node with no live share survived: %v", left)
	}
	if has(left, ".tmp-stale.zip") {
		t.Errorf("abandoned temp file survived: %v", left)
	}
	for _, keep := range []string{live, otherSigSameLiveNode, ".tmp-fresh.zip", "notes.txt", "12-nothexadecimal.zip"} {
		if !has(left, keep) {
			t.Errorf("sweeper deleted %s, which it must not touch; left = %v", keep, left)
		}
	}
}

// A cache that has never seen a successful share listing must delete nothing:
// an empty set would otherwise read as "no share exists anywhere".
func TestSweep_DeletesNothingWithoutASuccessfulListing(t *testing.T) {
	dir := t.TempDir()
	dead := fmt.Sprintf("3748-%s.zip", sig)
	writeFile(t, dir, dead, 15, 0)

	// (a) no tracker at all
	c := New(dir)
	if removed, _ := c.Sweep(); removed != 0 {
		t.Fatalf("untracked cache swept %d files, want 0", removed)
	}

	// (b) tracker whose only listing failed
	list := newShareList()
	list.fail(errors.New("database is away"))
	active := NewActiveShares(list.list)
	if _, err := active.Refresh(context.Background()); err == nil {
		t.Fatal("expected the listing to fail")
	}
	c.Track(active)
	if removed, _ := c.Sweep(); removed != 0 {
		t.Fatalf("swept %d files off a failed listing, want 0", removed)
	}
	if !has(names(t, dir), dead) {
		t.Fatal("swept an archive without knowing which shares are live")
	}
}

// A failed refresh must keep the previous answer rather than replace it with
// "nothing is active" — that snapshot is what deletes archives and kills
// builds.
func TestActiveShares_KeepsThePreviousSnapshotOnError(t *testing.T) {
	list := newShareList(DirShare{NodeID: 7})
	active := NewActiveShares(list.list)
	if _, err := active.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	before, at, ok := active.Snapshot()
	if !ok || len(before) != 1 {
		t.Fatalf("snapshot = %v (ok=%v), want one node", before, ok)
	}

	list.fail(errors.New("boom"))
	if _, err := active.Refresh(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	after, at2, ok := active.Snapshot()
	if !ok || len(after) != 1 {
		t.Fatalf("snapshot after a failed refresh = %v (ok=%v), want the previous one", after, ok)
	}
	if !at2.Equal(at) {
		t.Error("a failed refresh moved the snapshot timestamp")
	}
}

// A build in flight owns its temp file whatever its age; the sweeper must not
// pull it out from under itself.
func TestSweep_KeepsATempFileALiveBuildOwns(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	held := writeFile(t, dir, ".tmp-inflight.zip", 20, 2*time.Hour)
	c.holdTmp(held)

	list := newShareList()
	active := NewActiveShares(list.list)
	if _, err := active.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	c.Track(active)

	if removed, _ := c.Sweep(); removed != 0 {
		t.Fatalf("swept %d files, want 0 — a running build owns that temp file", removed)
	}
	if !has(names(t, dir), ".tmp-inflight.zip") {
		t.Fatal("sweeper deleted the temp file of a running build")
	}
}

// ---------------------------------------------------- abandon on expiry

// Between files: the share dies mid-build and the build stops instead of
// reading the rest of the folder for a link nobody can use any more.
func TestRun_AbandonsWhenTheShareDiesMidBuild(t *testing.T) {
	// Zero: check at every opportunity. These files are read in microseconds,
	// so any throttle at all would make "did it notice at file c?" a question
	// about the clock rather than about the code.
	restore := activeCheckInterval
	activeCheckInterval = 0
	defer func() { activeCheckInterval = restore }()

	dir := t.TempDir()
	c := New(dir)

	reached := make(chan struct{})
	release := make(chan struct{})
	drv := &fakeDriver{
		root: "/shared",
		files: map[string][]byte{
			"/shared/a.bin": make([]byte, 1024),
			"/shared/b.bin": make([]byte, 1024),
			"/shared/c.bin": make([]byte, 1024),
		},
		onRead: func(path string) {
			if path == "/shared/b.bin" {
				close(reached)
				<-release
			}
		},
	}

	list := newShareList(DirShare{StorageID: 1, Path: "/shared", NodeID: 7})
	active := NewActiveShares(list.list)
	c.Track(active)

	ctx := context.Background()
	cachePath, files, err := c.Plan(ctx, drv, "/shared", 7)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("collected %d files, want 3", len(files))
	}
	g := c.StartOrGet(cachePath, files, 7, drv)

	<-reached
	list.set() // the share expires
	if _, err := active.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	close(release)

	if err := g.Wait(ctx); !errors.Is(err, ErrShareGone) {
		t.Fatalf("build error = %v, want ErrShareGone", err)
	}
	if _, ok := c.Cached(cachePath); ok {
		t.Fatal("an abandoned build published its archive")
	}
	// With no size ceiling in front of it, a leaked partial is unbounded.
	if left := names(t, dir); len(left) != 0 {
		t.Fatalf("abandoned build left files behind: %v", left)
	}
}

// Inside one file: a folder can be a single 15 GB object, so a check that only
// runs between files is no bound at all. The build must stop mid-copy, and the
// partial must not survive.
func TestRun_AbandonsPartWayThroughOneLongFile(t *testing.T) {
	restore := activeCheckInterval
	activeCheckInterval = 5 * time.Millisecond
	defer func() { activeCheckInterval = restore }()

	dir := t.TempDir()
	c := New(dir)

	const total = 512 * 1024
	reached := make(chan struct{})
	drv := &fakeDriver{
		root:   "/shared",
		files:  map[string][]byte{"/shared/big.bin": make([]byte, total)},
		chunk:  4096,
		pause:  time.Millisecond,
		onRead: func(string) { close(reached) },
	}

	list := newShareList(DirShare{StorageID: 1, Path: "/shared", NodeID: 7})
	active := NewActiveShares(list.list)
	c.Track(active)

	ctx := context.Background()
	cachePath, files, err := c.Plan(ctx, drv, "/shared", 7)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	g := c.StartOrGet(cachePath, files, 7, drv)

	<-reached
	list.set()
	if _, err := active.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if err := g.Wait(ctx); !errors.Is(err, ErrShareGone) {
		t.Fatalf("build error = %v, want ErrShareGone", err)
	}
	if served := drv.served.Load(); served >= total {
		t.Fatalf("read %d of %d bytes: the build ran to the end of the file instead of stopping inside it", served, total)
	}
	if left := names(t, dir); len(left) != 0 {
		t.Fatalf("abandoned build left files behind: %v", left)
	}
	if _, ok := c.Cached(cachePath); ok {
		t.Fatal("an abandoned build published its archive")
	}
}

// A build started on demand seconds after a link is minted must not be killed
// by a view of the shares that predates the link. Only a snapshot taken AFTER
// the build started can say "that share is gone".
func TestRun_SurvivesAShareViewOlderThanItself(t *testing.T) {
	restore := activeCheckInterval
	activeCheckInterval = 0
	defer func() { activeCheckInterval = restore }()

	dir := t.TempDir()
	c := New(dir)
	drv := &fakeDriver{
		root:  "/shared",
		files: map[string][]byte{"/shared/a.bin": make([]byte, 8192)},
		chunk: 512,
	}

	// The world as it was BEFORE the share existed: node 7 is not in it.
	list := newShareList()
	active := NewActiveShares(list.list)
	if _, err := active.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	c.Track(active)

	// ...and now the share is created and its zip is built on demand.
	list.set(DirShare{StorageID: 1, Path: "/shared", NodeID: 7})
	ctx := context.Background()
	cachePath, files, err := c.Plan(ctx, drv, "/shared", 7)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := c.StartOrGet(cachePath, files, 7, drv).Wait(ctx); err != nil {
		t.Fatalf("build error = %v, want nil — the share view predates this build", err)
	}
	if _, ok := c.Cached(cachePath); !ok {
		t.Fatal("a live share's archive was not published")
	}
}

// A live share is built to the end, however long it takes: the expiry check
// refuses nothing and must never grow into a size limit.
func TestRun_BuildsALiveShareToCompletion(t *testing.T) {
	restore := activeCheckInterval
	activeCheckInterval = time.Millisecond
	defer func() { activeCheckInterval = restore }()

	dir := t.TempDir()
	c := New(dir)
	drv := &fakeDriver{
		root:  "/shared",
		files: map[string][]byte{"/shared/a.bin": make([]byte, 4096), "/shared/b.bin": make([]byte, 4096)},
		chunk: 512,
		pause: time.Millisecond,
	}
	list := newShareList(DirShare{StorageID: 1, Path: "/shared", NodeID: 7})
	active := NewActiveShares(list.list)
	c.Track(active)

	ctx := context.Background()
	did, err := c.Warm(ctx, drv, "/shared", 7)
	if err != nil {
		t.Fatalf("warm: %v", err)
	}
	if !did {
		t.Fatal("warm reported nothing to do on a cold cache")
	}
	// Refreshing mid-life must not disturb a live build's successor either.
	if _, err := active.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	left := names(t, dir)
	if len(left) != 1 || !strings.HasPrefix(left[0], "7-") {
		t.Fatalf("cache contains %v, want one published 7-*.zip", left)
	}
	if again, err := c.Warm(ctx, drv, "/shared", 7); err != nil || again {
		t.Fatalf("second warm did=%v err=%v, want false/nil (cache is fresh)", again, err)
	}
}
