package filesync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TrashRetentionDays is how long a file deleted by sync is kept on this
// machine before it is really gone.
//
// Sync deletes files because the OTHER side lost them, which is exactly when a
// mistake is least visible: a folder emptied on a phone quietly empties the
// laptop too. The local copy is the last line of defence, so it is kept for a
// month rather than a few days.
const TrashRetentionDays = 30

// Store holds the on-disk state for every pair: the baseline that makes change
// detection possible, and the trash that makes deletes recoverable.
//
// Layout under Dir:
//
//	pairs.json                     the configured pairs
//	baseline/<pair-id>.json        last agreed state
//	trash/<pair-id>/<stamp>/...    files this engine deleted locally
type Store struct{ Dir string }

// DefaultStoreDir is ~/.filex/sync (beside the CLI's own config).
func DefaultStoreDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".filex", "sync"), nil
}

func (s *Store) path(parts ...string) string {
	return filepath.Join(append([]string{s.Dir}, parts...)...)
}

// ─────────────────────────── pairs ───────────────────────────

// LoadPairs reads the configured folder pairs. A missing file is not an error:
// it means nothing has been paired yet.
func (s *Store) LoadPairs() ([]Pair, error) {
	raw, err := os.ReadFile(s.path("pairs.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pairs []Pair
	if err := json.Unmarshal(raw, &pairs); err != nil {
		return nil, fmt.Errorf("read pairs.json: %w", err)
	}
	return pairs, nil
}

func (s *Store) SavePairs(pairs []Pair) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(pairs, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.path("pairs.json"), raw)
}

// AddPair validates and stores a new mapping.
//
// ⚠ Overlapping local roots are refused. Two pairs sharing a directory each
// see the other's writes as user edits and push them back and forth forever.
func (s *Store) AddPair(p Pair) (Pair, error) {
	abs, err := filepath.Abs(p.Local)
	if err != nil {
		return p, err
	}
	p.Local = abs
	if !strings.Contains(p.Remote, "://") {
		return p, fmt.Errorf("remote must look like adapter://path, got %q", p.Remote)
	}
	// ⚠ A `..` segment is refused rather than cleaned. The remote is a WIRE
	// path that callers derive local directory names from — the desktop app
	// mirrors `adapter://a/b` at `<root>/adapter/a/b` — and the listing it
	// comes from is the server's answer, not the user's typing. A remote of
	// `docs://../../Documents` would name a folder outside the mirror root,
	// where a first run (which merges both sides) would happily upload
	// whatever it found. Nothing legitimate needs it: the server resolves
	// paths from its storage root, so `..` cannot address anything real.
	for _, seg := range strings.Split(p.Remote[strings.Index(p.Remote, "://")+3:], "/") {
		if seg == ".." {
			return p, fmt.Errorf("remote path may not contain a %q segment: %q", "..", p.Remote)
		}
	}
	if p.File {
		rel := strings.Trim(p.Remote[strings.Index(p.Remote, "://")+3:], "/")
		if rel == "" {
			return p, fmt.Errorf("a file pair needs a file path, not a storage root")
		}
		// The engine keys both sides by ONE basename; a rename across the
		// pair would silently sync the wrong entry.
		if base := rel[strings.LastIndex(rel, "/")+1:]; base != filepath.Base(p.Local) {
			return p, fmt.Errorf("a file pair keeps its name: local %q vs remote %q", filepath.Base(p.Local), base)
		}
	}

	pairs, err := s.LoadPairs()
	if err != nil {
		return p, err
	}
	for _, ex := range pairs {
		if overlaps(ex.Local, p.Local) {
			return p, fmt.Errorf("folder overlaps the existing pair %s (%s)", ex.ID, ex.Local)
		}
		if ex.Remote == p.Remote {
			return p, fmt.Errorf("%s is already synced to %s", p.Remote, ex.Local)
		}
	}
	if p.ID == "" {
		p.ID = newPairID(pairs)
	}
	return p, s.SavePairs(append(pairs, p))
}

// MovePairLocal repoints a pair at a new local path, keeping its identity —
// and, crucially, its BASELINE: baselines store relative paths only, so a
// mirror that physically moved keeps its whole history and the next run is an
// ordinary incremental pass. The alternative (remove + re-add) throws the
// baseline away, and the first-run merge that follows conflicts every file
// whose two sides carry different mtimes — which is every file this machine
// ever uploaded.
func (s *Store) MovePairLocal(id, newLocal string) (Pair, error) {
	abs, err := filepath.Abs(newLocal)
	if err != nil {
		return Pair{}, err
	}
	pairs, err := s.LoadPairs()
	if err != nil {
		return Pair{}, err
	}
	idx := -1
	for i, p := range pairs {
		if p.ID == id {
			idx = i
			continue
		}
		if overlaps(p.Local, abs) {
			return Pair{}, fmt.Errorf("new path overlaps the existing pair %s (%s)", p.ID, p.Local)
		}
	}
	if idx < 0 {
		return Pair{}, fmt.Errorf("no such pair: %s", id)
	}
	if pairs[idx].File && filepath.Base(abs) != filepath.Base(pairs[idx].Local) {
		return Pair{}, fmt.Errorf("a file pair keeps its name: %q vs %q",
			filepath.Base(pairs[idx].Local), filepath.Base(abs))
	}
	pairs[idx].Local = abs
	return pairs[idx], s.SavePairs(pairs)
}

// RemovePair forgets a mapping. Files on either side are left exactly where
// they are — unpairing is not deleting, and a user who wanted the files gone
// would have deleted them.
func (s *Store) RemovePair(id string) error {
	pairs, err := s.LoadPairs()
	if err != nil {
		return err
	}
	out := pairs[:0]
	found := false
	for _, p := range pairs {
		if p.ID == id {
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		return fmt.Errorf("no such pair: %s", id)
	}
	if err := s.SavePairs(out); err != nil {
		return err
	}
	// The baseline only appears once a pair's first run has COMPLETED. A pair
	// removed before that — typically a large first sync the user cancels by
	// unpairing — has no file here, and that must not read as failure: the
	// pair is already gone from pairs.json by this line, and callers treat an
	// error as "the remove failed" and skip their own follow-up.
	if err := os.Remove(s.path("baseline", id+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func overlaps(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if strings.EqualFold(a, b) {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(b, a+sep) || strings.HasPrefix(a, b+sep)
}

func newPairID(existing []Pair) string {
	used := map[string]bool{}
	for _, p := range existing {
		used[p.ID] = true
	}
	for i := 1; ; i++ {
		id := fmt.Sprintf("pair-%d", i)
		if !used[id] {
			return id
		}
	}
}

// ─────────────────────────── baseline ───────────────────────────

// LoadBaseline returns the state carried from the previous run. The bool
// reports whether a baseline existed at all: the first run of a pair must not
// delete anything, and this is how the engine knows.
func (s *Store) LoadBaseline(pairID string) (Baseline, bool, error) {
	raw, err := os.ReadFile(s.path("baseline", pairID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return Baseline{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var b Baseline
	if err := json.Unmarshal(raw, &b); err != nil {
		// A corrupt baseline must not be treated as "everything was deleted".
		// Falling back to a first run costs one extra merge pass and cannot
		// destroy anything.
		return Baseline{}, false, nil
	}
	return b, true, nil
}

func (s *Store) SaveBaseline(pairID string, b Baseline) error {
	if err := os.MkdirAll(s.path("baseline"), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return writeAtomic(s.path("baseline", pairID+".json"), raw)
}

// ─────────────────────────── trash ───────────────────────────

// TrashLocal moves a path out of the sync folder instead of deleting it.
//
// The engine NEVER calls os.Remove on user content: everything it removes from
// disk lands here first, under a per-run timestamp, and is only really gone
// once PruneTrash finds it older than the retention window.
func (s *Store) TrashLocal(pairID, root, rel string, now time.Time) error {
	src, err := localPathOf(root, rel)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(src); errors.Is(err, os.ErrNotExist) {
		return nil // already gone; nothing to preserve
	}
	dest := s.path("trash", pairID, now.UTC().Format("20060102-150405"), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	// Rename fails across filesystems (the trash may be on a different drive to
	// the sync folder). Copy, then remove — in that order, so a failure leaves
	// the original in place.
	if err := copyTree(src, dest); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// PruneTrash drops trashed items older than days. Returns how many were
// removed.
func (s *Store) PruneTrash(pairID string, days int, now time.Time) (int, error) {
	dir := s.path("trash", pairID)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cutoff := now.UTC().AddDate(0, 0, -days)
	removed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		stamp, err := time.Parse("20060102-150405", e.Name())
		if err != nil {
			continue // not ours; leave it alone
		}
		if stamp.Before(cutoff) {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

// TrashItem is one recoverable deletion.
type TrashItem struct {
	PairID  string
	Deleted time.Time
	Rel     string
	Path    string
	Size    int64
}

// ListTrash reports what can still be recovered, newest first.
func (s *Store) ListTrash(pairID string) ([]TrashItem, error) {
	dir := s.path("trash", pairID)
	stamps, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []TrashItem
	for _, st := range stamps {
		when, err := time.Parse("20060102-150405", st.Name())
		if err != nil {
			continue
		}
		root := filepath.Join(dir, st.Name())
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			out = append(out, TrashItem{
				PairID:  pairID,
				Deleted: when,
				Rel:     filepath.ToSlash(rel),
				Path:    p,
				Size:    info.Size(),
			})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Deleted.After(out[j].Deleted) })
	return out, nil
}

// ─────────────────────────── helpers ───────────────────────────

// writeAtomic writes via a temporary file and renames, so a crash mid-write
// cannot leave a half-written baseline that the next run would read as "these
// files were deleted".
func writeAtomic(dest string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, dest)
}

func copyTree(src, dest string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dest)
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dest, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
