package filesync

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer is an in-memory filex. It is deliberately a real implementation
// rather than a stub returning canned answers: the engine's job is to make two
// trees agree, and only a server that actually stores what it is told can prove
// that happened.
type fakeServer struct {
	mu    sync.Mutex
	root  string // adapter://root prefix this fake answers for
	files map[string][]byte
	dirs  map[string]bool
	mod   map[string]int64
	clock int64
	fail  map[string]error // path → error to return once
}

func newFake(root string) *fakeServer {
	return &fakeServer{
		root:  root,
		files: map[string][]byte{},
		dirs:  map[string]bool{"": true},
		mod:   map[string]int64{},
		clock: 1_700_000_000_000,
		fail:  map[string]error{},
	}
}

// rel strips the adapter://root prefix the engine sends.
func (f *fakeServer) rel(remote string) (string, error) {
	if remote == f.root {
		return "", nil
	}
	if !strings.HasPrefix(remote, f.root+"/") {
		return "", fmt.Errorf("path outside the pair root: %q", remote)
	}
	return strings.TrimPrefix(remote, f.root+"/"), nil
}

func (f *fakeServer) List(_ context.Context, remote string) (*Listing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rel, err := f.rel(remote)
	if err != nil {
		return nil, err
	}
	if rel != "" && !f.dirs[rel] {
		return nil, fmt.Errorf("not a directory: %s", rel)
	}
	var out Listing
	seen := map[string]bool{}
	add := func(name string, isDir bool, size, mod int64) {
		if seen[name] {
			return
		}
		seen[name] = true
		out.Files = append(out.Files, ListedFile{Basename: name, IsDir: isDir, Size: size, LastModified: mod})
	}
	for p := range f.dirs {
		if p != "" && path.Dir(p) == dirOf(rel) {
			add(path.Base(p), true, 0, 0)
		}
	}
	for p, b := range f.files {
		if path.Dir(p) == dirOf(rel) {
			add(path.Base(p), false, int64(len(b)), f.mod[p])
		}
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Basename < out.Files[j].Basename })
	return &out, nil
}

// dirOf normalises "" (the root) to the "." that path.Dir returns for a
// top-level entry.
func dirOf(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

func (f *fakeServer) Download(_ context.Context, remote string, w io.Writer) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rel, err := f.rel(remote)
	if err != nil {
		return 0, err
	}
	if err := f.fail[rel]; err != nil {
		delete(f.fail, rel)
		return 0, err
	}
	b, ok := f.files[rel]
	if !ok {
		return 0, fmt.Errorf("no such file: %s", rel)
	}
	n, err := w.Write(b)
	return int64(n), err
}

func (f *fakeServer) Upload(_ context.Context, localPath, remote string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rel, err := f.rel(remote)
	if err != nil {
		return err
	}
	if err := f.fail[rel]; err != nil {
		delete(f.fail, rel)
		return err
	}
	b, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	if f.dirs[rel] {
		return fmt.Errorf("refusing to write a file onto the folder %s", rel)
	}
	f.files[rel] = b
	f.clock += 1000
	f.mod[rel] = f.clock
	return nil
}

func (f *fakeServer) Mkdir(_ context.Context, remote string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rel, err := f.rel(remote)
	if err != nil {
		return err
	}
	if rel == "" || rel == "." {
		return nil
	}
	if _, isFile := f.files[rel]; isFile {
		return fmt.Errorf("refusing to write a folder onto the file %s", rel)
	}
	f.dirs[rel] = true
	return nil
}

func (f *fakeServer) Remove(_ context.Context, remote string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rel, err := f.rel(remote)
	if err != nil {
		return err
	}
	if err := f.fail[rel]; err != nil {
		delete(f.fail, rel)
		return err
	}
	if _, ok := f.files[rel]; ok {
		delete(f.files, rel)
		delete(f.mod, rel)
		return nil
	}
	if f.dirs[rel] {
		for p := range f.files {
			if strings.HasPrefix(p, rel+"/") {
				return fmt.Errorf("directory not empty: %s", rel)
			}
		}
		delete(f.dirs, rel)
		return nil
	}
	return fmt.Errorf("no such path: %s", rel)
}

// ─────────────────────────── harness ───────────────────────────

type rig struct {
	t      *testing.T
	dir    string
	srv    *fakeServer
	engine *Engine
	clock  time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	base := t.TempDir()
	localDir := filepath.Join(base, "folder")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := newFake("docs://work")
	r := &rig{t: t, dir: localDir, srv: srv, clock: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)}
	r.engine = &Engine{
		Pair:  Pair{ID: "p1", Local: localDir, Remote: "docs://work"},
		API:   srv,
		Store: &Store{Dir: filepath.Join(base, "state")},
		Now:   func() time.Time { return r.clock },
	}
	return r
}

func (r *rig) run() Result {
	r.t.Helper()
	res, err := r.engine.Run(context.Background())
	if err != nil {
		r.t.Fatalf("run: %v", err)
	}
	if len(res.Errors) > 0 {
		r.t.Logf("run reported errors: %v", res.Errors)
	}
	return res
}

func (r *rig) writeLocal(rel, body string) {
	r.t.Helper()
	p := filepath.Join(r.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
	// Local mtimes come from the filesystem, whose resolution is coarse enough
	// that two writes in the same test tick can look identical. Step them.
	r.clock = r.clock.Add(time.Minute)
	_ = os.Chtimes(p, r.clock, r.clock)
}

func (r *rig) readLocal(rel string) string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(rel)))
	if err != nil {
		r.t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func (r *rig) localExists(rel string) bool {
	_, err := os.Lstat(filepath.Join(r.dir, filepath.FromSlash(rel)))
	return err == nil
}

func (r *rig) writeRemote(rel, body string) {
	r.srv.files[rel] = []byte(body)
	r.srv.clock += 1000
	r.srv.mod[rel] = r.srv.clock
	// A real server has every ancestor, not just the immediate parent.
	for d := path.Dir(rel); d != "." && d != "/"; d = path.Dir(d) {
		r.srv.dirs[d] = true
	}
}

// ─────────────────────────── tests ───────────────────────────

func TestEngineMirrorsBothDirections(t *testing.T) {
	r := newRig(t)
	r.writeLocal("notes.txt", "from the laptop")
	r.writeRemote("shared/plan.md", "from the server")

	r.run()

	if got := string(r.srv.files["notes.txt"]); got != "from the laptop" {
		t.Errorf("local file did not reach the server: %q", got)
	}
	if got := r.readLocal("shared/plan.md"); got != "from the server" {
		t.Errorf("remote file did not reach the disk: %q", got)
	}
}

func TestEngineIsIdempotent(t *testing.T) {
	r := newRig(t)
	r.writeLocal("a.txt", "x")
	r.writeRemote("b.txt", "y")
	r.run()

	second := r.run()

	if second.Planned != 0 {
		t.Fatalf("a settled pair must plan no work, got %d actions", second.Planned)
	}
}

func TestEngineDeletesLocallyWhenTheServerLostAFile(t *testing.T) {
	r := newRig(t)
	r.writeLocal("gone.txt", "content")
	r.run() // now in the baseline

	delete(r.srv.files, "gone.txt")
	res := r.run()

	if r.localExists("gone.txt") {
		t.Error("file should have been removed from the sync folder")
	}
	if res.DeletedLocal != 1 {
		t.Errorf("DeletedLocal = %d, want 1", res.DeletedLocal)
	}
}

// ⚠ The engine must never os.Remove user content outright.
func TestLocalDeletesAreRecoverableFromTrash(t *testing.T) {
	r := newRig(t)
	r.writeLocal("payslip.pdf", "important")
	r.run()
	delete(r.srv.files, "payslip.pdf")
	r.run()

	items, err := r.engine.Store.ListTrash("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 recoverable item, got %d", len(items))
	}
	if items[0].Rel != "payslip.pdf" {
		t.Errorf("trashed path = %q", items[0].Rel)
	}
	b, err := os.ReadFile(items[0].Path)
	if err != nil || string(b) != "important" {
		t.Errorf("the trashed copy must still hold the content, got %q (%v)", b, err)
	}
}

func TestTrashIsPrunedAfterTheRetentionWindow(t *testing.T) {
	r := newRig(t)
	r.writeLocal("old.txt", "x")
	r.run()
	delete(r.srv.files, "old.txt")
	r.run()

	// Nothing expires before the window is up.
	n, err := r.engine.Store.PruneTrash("p1", TrashRetentionDays, r.clock.AddDate(0, 0, TrashRetentionDays-1))
	if err != nil || n != 0 {
		t.Fatalf("pruned too early: n=%d err=%v", n, err)
	}
	n, err = r.engine.Store.PruneTrash("p1", TrashRetentionDays, r.clock.AddDate(0, 0, TrashRetentionDays+1))
	if err != nil || n != 1 {
		t.Fatalf("expired item was not pruned: n=%d err=%v", n, err)
	}
	items, _ := r.engine.Store.ListTrash("p1")
	if len(items) != 0 {
		t.Errorf("trash should be empty, got %d", len(items))
	}
}

func TestEngineDeletesRemotelyWhenTheFolderLostAFile(t *testing.T) {
	r := newRig(t)
	r.writeLocal("bye.txt", "content")
	r.run()

	if err := os.Remove(filepath.Join(r.dir, "bye.txt")); err != nil {
		t.Fatal(err)
	}
	r.run()

	if _, ok := r.srv.files["bye.txt"]; ok {
		t.Error("file should have been removed from the server")
	}
}

// The whole reason a first run merges instead of syncing.
func TestFirstRunAdoptsExistingFoldersWithoutDeleting(t *testing.T) {
	r := newRig(t)
	r.writeLocal("mine.txt", "a")
	r.writeRemote("theirs.txt", "b")

	res := r.run()

	if !res.FirstRun {
		t.Fatal("expected the run to know it was the first")
	}
	if res.DeletedLocal != 0 || res.DeletedRemot != 0 {
		t.Fatalf("a first run deleted something: %+v", res)
	}
	if !r.localExists("theirs.txt") {
		t.Error("remote file should have been adopted")
	}
	if _, ok := r.srv.files["mine.txt"]; !ok {
		t.Error("local file should have been adopted")
	}
}

func TestConflictKeepsBothVersions(t *testing.T) {
	r := newRig(t)
	r.writeLocal("report.txt", "v1")
	r.run()

	r.writeLocal("report.txt", "my edit")
	r.writeRemote("report.txt", "their edit")
	res := r.run()

	if res.Conflicts != 1 {
		t.Fatalf("want 1 conflict, got %d", res.Conflicts)
	}
	if got := r.readLocal("report.txt"); got != "my edit" {
		t.Errorf("the user's own file must keep its name and content, got %q", got)
	}
	side := "report (server copy 2026-08-07 12-02).txt"
	found := false
	entries, _ := os.ReadDir(r.dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "report (server copy") {
			found = true
			if b, _ := os.ReadFile(filepath.Join(r.dir, e.Name())); string(b) != "their edit" {
				t.Errorf("conflict copy holds %q", b)
			}
		}
	}
	if !found {
		t.Errorf("no conflict copy beside the original (expected something like %q); dir=%v", side, names(entries))
	}
	if got := string(r.srv.files["report.txt"]); got != "my edit" {
		t.Errorf("the local edit should also have reached the server, got %q", got)
	}
}

func names(entries []os.DirEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// One failing transfer must not stop the rest, and must not be recorded as
// settled — otherwise a single broken file wedges the folder forever.
func TestAFailedTransferIsRetriedNextRun(t *testing.T) {
	r := newRig(t)
	r.writeRemote("good.txt", "fine")
	r.writeRemote("bad.txt", "also fine, eventually")
	r.srv.fail["bad.txt"] = fmt.Errorf("storage hiccup")

	first := r.run()

	if !r.localExists("good.txt") {
		t.Error("the healthy file should have downloaded")
	}
	if r.localExists("bad.txt") {
		t.Error("the failed file must not exist locally")
	}
	if len(first.Errors) != 1 {
		t.Fatalf("the failure must be reported, got %v", first.Errors)
	}

	second := r.run()

	if !r.localExists("bad.txt") {
		t.Error("the failed file should be retried on the next run")
	}
	if got := r.readLocal("bad.txt"); got != "also fine, eventually" {
		t.Errorf("retried content = %q", got)
	}
	_ = second
}

// An interrupted download must never be mistaken for a complete file.
func TestAnInterruptedDownloadLeavesNothingBehind(t *testing.T) {
	r := newRig(t)
	r.writeRemote("big.bin", "payload")
	r.srv.fail["big.bin"] = fmt.Errorf("connection reset")

	r.run()

	entries, _ := os.ReadDir(r.dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".filex-part-") {
			t.Fatalf("a partial file was left in the sync folder: %s", e.Name())
		}
		if e.Name() == "big.bin" {
			t.Fatal("a failed download must not produce the destination file")
		}
	}
}

func TestNestedTreesSyncBothWays(t *testing.T) {
	r := newRig(t)
	r.writeLocal("a/b/c/deep.txt", "down here")
	r.writeRemote("x/y/other.txt", "over there")

	r.run()

	if got := string(r.srv.files["a/b/c/deep.txt"]); got != "down here" {
		t.Errorf("nested local file did not reach the server: %q", got)
	}
	if got := r.readLocal("x/y/other.txt"); got != "over there" {
		t.Errorf("nested remote file did not reach the disk: %q", got)
	}
	if second := r.run(); second.Planned != 0 {
		t.Errorf("nested trees should settle in one pass, second run planned %d", second.Planned)
	}
}

// ⚠ Names come from the server. A listing carrying `..` must not be able to
// write outside the sync folder.
func TestRemotePathsCannotEscapeTheSyncFolder(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"../evil.txt", "a/../../evil.txt", "/etc/passwd", "a//b"} {
		if _, err := localPathOf(root, bad); err == nil {
			t.Errorf("localPathOf accepted %q", bad)
		}
	}
	if _, err := localPathOf(root, "fine/name.txt"); err != nil {
		t.Errorf("localPathOf rejected a legitimate path: %v", err)
	}
}

func TestOverlappingPairsAreRefused(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: filepath.Join(dir, "state")}
	parent := filepath.Join(dir, "docs")
	child := filepath.Join(dir, "docs", "2026")

	if _, err := s.AddPair(Pair{Local: parent, Remote: "docs://a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPair(Pair{Local: child, Remote: "docs://b"}); err == nil {
		t.Error("a pair inside an existing pair must be refused")
	}
	if _, err := s.AddPair(Pair{Local: filepath.Join(dir, "other"), Remote: "docs://a"}); err == nil {
		t.Error("two folders syncing to the same remote must be refused")
	}
}

// A remote path is the server's answer, not the user's typing, and callers
// build local directory names out of it — so a `..` in it is a way to name a
// folder outside the mirror root, which a first run would then merge INTO the
// server. Refused at the door.
func TestARemoteThatClimbsOutIsRefused(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: filepath.Join(dir, "state")}

	for _, remote := range []string{"docs://../secrets", "docs://a/../../secrets", "docs://.."} {
		if _, err := s.AddPair(Pair{Local: filepath.Join(dir, "x"), Remote: remote}); err == nil {
			t.Errorf("%q must be refused", remote)
		}
	}
	// A folder that merely CONTAINS dots in its name is legitimate.
	if _, err := s.AddPair(Pair{Local: filepath.Join(dir, "ok"), Remote: "docs://a..b/c"}); err != nil {
		t.Errorf("a dotted folder name is not a traversal: %v", err)
	}
}

func TestRemovingAPairLeavesTheFilesAlone(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: filepath.Join(dir, "state")}
	local := filepath.Join(dir, "docs")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(local, "keep.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := s.AddPair(Pair{Local: local, Remote: "docs://a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveBaseline(p.ID, Baseline{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemovePair(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("unpairing must not touch the user's files: %v", err)
	}
}

// The baseline file only exists after a first run has COMPLETED. Unpairing
// before that (a long first sync the user cancels by removing the pair) used
// to fail with the raw ENOENT, which the desktop surfaced as an error dialog —
// and then skipped its own watcher cleanup because the remove "failed".
func TestRemovingAPairBeforeItsFirstRunSucceeds(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: filepath.Join(dir, "state")}
	p, err := s.AddPair(Pair{Local: filepath.Join(dir, "docs"), Remote: "docs://a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemovePair(p.ID); err != nil {
		t.Fatalf("removing a never-run pair must not fail: %v", err)
	}
	pairs, err := s.LoadPairs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 0 {
		t.Errorf("pair still present after remove: %v", pairs)
	}
}

// Progress must speak even when Log is silent: the desktop runs the engine
// with --quiet and shows only the last stdout line, and the inventory phase
// of a big first sync otherwise prints nothing for minutes.
func TestProgressReportsInventoryAndTransfer(t *testing.T) {
	r := newRig(t)
	r.srv.files["a.txt"] = []byte("one")
	r.srv.files["sub/b.txt"] = []byte("two")
	r.srv.dirs["sub"] = true

	var lines []string
	r.engine.Progress = func(s string) { lines = append(lines, s) }
	r.run()

	joined := strings.Join(lines, "\n")
	for _, want := range []string{"inventory:", "plan:", "transfer:", "settling:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("progress lines missing %q:\n%s", want, joined)
		}
	}
}

// fileRig wires a single-FILE pair against the same fake server.
func fileRig(t *testing.T) (*rig, *Engine) {
	t.Helper()
	r := newRig(t)
	eng := &Engine{
		Pair: Pair{
			ID:     "f1",
			Local:  filepath.Join(filepath.Dir(r.dir), "mirror", "a.txt"),
			Remote: "docs://work/a.txt",
			File:   true,
		},
		API:   r.srv,
		Store: r.engine.Store,
		Now:   func() time.Time { return r.clock },
	}
	return r, eng
}

func TestFilePairFirstRunDownloads(t *testing.T) {
	r, eng := fileRig(t)
	r.srv.files["a.txt"] = []byte("from the server")
	r.srv.mod["a.txt"] = r.srv.clock

	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Downloaded != 1 {
		t.Fatalf("want 1 download, got %+v", res)
	}
	b, err := os.ReadFile(eng.Pair.Local)
	if err != nil || string(b) != "from the server" {
		t.Fatalf("local copy wrong: %q %v", b, err)
	}
}

func TestFilePairUploadsALocalEdit(t *testing.T) {
	r, eng := fileRig(t)
	r.srv.files["a.txt"] = []byte("v1")
	r.srv.mod["a.txt"] = r.srv.clock
	if _, err := eng.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	r.clock = r.clock.Add(time.Hour)
	if err := os.WriteFile(eng.Pair.Local, []byte("v2 edited here"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(eng.Pair.Local, future, future); err != nil {
		t.Fatal(err)
	}

	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Uploaded != 1 {
		t.Fatalf("want 1 upload, got %+v", res)
	}
	if string(r.srv.files["a.txt"]) != "v2 edited here" {
		t.Fatalf("server copy wrong: %q", r.srv.files["a.txt"])
	}
}

func TestFilePairRemoteDeleteTrashesTheLocalCopy(t *testing.T) {
	r, eng := fileRig(t)
	r.srv.files["a.txt"] = []byte("short-lived")
	r.srv.mod["a.txt"] = r.srv.clock
	if _, err := eng.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	delete(r.srv.files, "a.txt")
	delete(r.srv.mod, "a.txt")
	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.DeletedLocal != 1 {
		t.Fatalf("want the local copy trashed, got %+v", res)
	}
	if _, err := os.Stat(eng.Pair.Local); !os.IsNotExist(err) {
		t.Fatalf("local copy should be gone: %v", err)
	}
	items, err := eng.Store.ListTrash("f1")
	if err != nil || len(items) != 1 {
		t.Fatalf("want 1 trashed item, got %v %v", items, err)
	}
}

// The exact disaster the adopt rule prevents: a first run interrupted after
// most of the tree came down. On resume there is no baseline, both sides hold
// the same files — and every one of them used to become a "changed in both
// places" conflict pair. Thousands of "(remote copy)" duplicates from one
// restart, measured almost-live on a 7,800-file tree.
func TestInterruptedFirstRunResumesWithoutConflicts(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 12; i++ {
		rel := fmt.Sprintf("docs/f%02d.txt", i)
		r.srv.files[rel] = []byte(strings.Repeat("x", i+1))
		r.srv.mod[rel] = r.srv.clock + int64(i)*1000
	}
	r.srv.dirs["docs"] = true

	res := r.run()
	if res.Downloaded != 12 {
		t.Fatalf("seed run: want 12 downloads, got %+v", res)
	}

	// Simulate the interruption: the work is on disk, the baseline is not.
	if err := os.Remove(filepath.Join(r.engine.Store.Dir, "baseline", "p1.json")); err != nil {
		t.Fatal(err)
	}

	res = r.run()
	if res.Planned != 0 {
		t.Fatalf("resume must adopt settled files, planned %d: %+v", res.Planned, res)
	}
	copies, _ := filepath.Glob(filepath.Join(r.dir, "docs", "* copy*"))
	if len(copies) != 0 {
		t.Fatalf("resume made conflict copies: %v", copies)
	}
}

// The transfer pool must produce exactly the serial results — run under
// -race, with the fake server shared by every worker.
func TestParallelTransfersDownloadEverything(t *testing.T) {
	r := newRig(t)
	want := 0
	for d := 0; d < 4; d++ {
		dir := fmt.Sprintf("d%d", d)
		r.srv.dirs[dir] = true
		for i := 0; i < 10; i++ {
			rel := fmt.Sprintf("%s/f%02d.bin", dir, i)
			r.srv.files[rel] = []byte(strings.Repeat("y", 100+i))
			r.srv.mod[rel] = r.srv.clock + int64(want)*500
			want++
		}
	}
	r.engine.Transfers = 6

	res := r.run()
	if res.Downloaded != want || len(res.Errors) != 0 {
		t.Fatalf("want %d downloads and no errors, got %+v", want, res)
	}
	local, _, err := WalkLocal(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, n := range local {
		if !n.IsDir {
			files++
		}
	}
	if files != want {
		t.Fatalf("want %d files on disk, got %d", want, files)
	}
}

// Moving a mirror keeps its baseline, so the next run is an ordinary
// incremental pass — not a first-run merge.
func TestMovePairLocalKeepsBaseline(t *testing.T) {
	r := newRig(t)
	// The rig drives the engine directly; MovePairLocal reads pairs.json, so
	// the pair has to exist on disk the way a real one would.
	if _, err := r.engine.Store.AddPair(Pair{ID: "p1", Local: r.dir, Remote: "docs://work"}); err != nil {
		t.Fatal(err)
	}
	r.srv.files["a.txt"] = []byte("hello")
	r.srv.mod["a.txt"] = r.srv.clock
	r.writeLocal("b.txt", "local born")
	r.run()

	newLocal := filepath.Join(filepath.Dir(r.dir), "moved-folder")
	if err := os.Rename(r.dir, newLocal); err != nil {
		t.Fatal(err)
	}
	moved, err := r.engine.Store.MovePairLocal("p1", newLocal)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Local != newLocal {
		t.Fatalf("pair not repointed: %+v", moved)
	}

	r.engine.Pair.Local = newLocal
	res := r.run()
	if res.Planned != 0 {
		t.Fatalf("moved mirror must settle with zero work, planned %d: %+v", res.Planned, res)
	}
}

// A corrupt baseline must degrade to a merge, never to "everything was deleted".
func TestACorruptBaselineFallsBackToAFirstRun(t *testing.T) {
	r := newRig(t)
	r.writeLocal("a.txt", "keep me")
	r.run()

	bad := filepath.Join(r.engine.Store.Dir, "baseline", "p1.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	delete(r.srv.files, "a.txt") // looks like a remote delete

	res := r.run()

	if !res.FirstRun {
		t.Error("a corrupt baseline should be treated as no baseline")
	}
	if !r.localExists("a.txt") {
		t.Error("a corrupt baseline must not cause a deletion")
	}
}

func TestSyncStateDirectoryIsNeverSynced(t *testing.T) {
	r := newRig(t)
	r.writeLocal(".filex-sync/state.json", "internal")
	r.writeLocal("real.txt", "user file")

	r.run()

	if _, ok := r.srv.files[".filex-sync/state.json"]; ok {
		t.Error("the engine uploaded its own bookkeeping")
	}
	if _, ok := r.srv.files["real.txt"]; !ok {
		t.Error("a real file next to it should still have synced")
	}
}

// Pairing to a server folder that does not exist yet must work. Measured
// against a live server first: the walk 404s on a missing root and the whole
// pair failed on every run, which no amount of retrying fixes.
func TestPairingToAMissingRemoteFolderCreatesIt(t *testing.T) {
	r := newRig(t)
	r.engine.Pair.Remote = "docs://work/brand-new"
	r.srv.root = "docs://work/brand-new"
	delete(r.srv.dirs, "") // the fake now knows of no such folder at all
	r.writeLocal("a.txt", "content")

	res, err := r.engine.Run(context.Background())
	if err != nil {
		t.Fatalf("run against a missing remote folder: %v", err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("run reported errors: %v", res.Errors)
	}
	if got := string(r.srv.files["a.txt"]); got != "content" {
		t.Errorf("file did not reach the newly created folder, got %q", got)
	}
}

func TestResultCountsAreMeasurements(t *testing.T) {
	r := newRig(t)
	r.writeLocal("up.txt", "a")
	r.writeRemote("down.txt", "b")

	res := r.run()

	if res.Uploaded != 1 || res.Downloaded != 1 {
		t.Errorf("Uploaded=%d Downloaded=%d, want 1/1", res.Uploaded, res.Downloaded)
	}
	if res.Applied != res.Planned {
		t.Errorf("Applied=%d Planned=%d — a clean run should complete everything", res.Applied, res.Planned)
	}
}

var _ = bytes.MinRead
