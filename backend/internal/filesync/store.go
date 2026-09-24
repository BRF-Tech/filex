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
//	held/<pair-id>.json            a hold: local items kept for a decision (hold.go)
//	trash/<pair-id>/<stamp>/...    files this engine deleted locally
type Store struct{ Dir string }

// DefaultStoreDir is $FILEX_SYNC_DIR when set, otherwise ~/.filex/sync (beside
// the CLI's own config, which honours $FILEX_CLI_CONFIG the same way).
//
// ⚠ The override is not a convenience. This directory holds the pairs, the
// per-pair baselines and the local trash — which contains real copies of files
// the engine deleted. The desktop app's PORTABLE build runs from a USB stick on
// a machine that is not the user's, and everything it keeps has to live in its
// own folder so that deleting that folder leaves nothing behind. Without this,
// a portable copy would seed somebody else's home directory with another
// person's deleted files.
func DefaultStoreDir() (string, error) {
	if dir := os.Getenv("FILEX_SYNC_DIR"); dir != "" {
		return dir, nil
	}
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
	// The hold is folded in from its own file (see holdFile); what pairs.json
	// says about it is ignored. An unreadable hold file reads as holding.
	for i := range pairs {
		h, err := s.loadHold(pairs[i].ID)
		pairs[i].HoldNew = h.Holding || err != nil
		pairs[i].Held = len(h.Items)
	}
	return pairs, nil
}

func (s *Store) SavePairs(pairs []Pair) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	// Never the hold: it lives in held/<pair-id>.json (see holdFile).
	out := make([]Pair, len(pairs))
	for i, p := range pairs {
		p.HoldNew, p.Held = false, 0
		out[i] = p
	}
	raw, err := json.MarshalIndent(out, "", "  ")
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
	// The new path must already hold the content. Repointing a pair at a
	// path that is not there is the one thing this command must never do:
	// the next run would create it empty, and an empty mirror under a
	// surviving baseline reads as "every file deleted here" — which the
	// planner dutifully carries to the server.
	info, err := os.Stat(abs)
	if err != nil {
		return Pair{}, fmt.Errorf("new path %s: %w (move the folder there first; the pair keeps pointing at its current location)", abs, err)
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
	if pairs[idx].File && !info.Mode().IsRegular() {
		return Pair{}, fmt.Errorf("%s is a file pair but %s is not a regular file", id, abs)
	}
	if !pairs[idx].File && !info.IsDir() {
		return Pair{}, fmt.Errorf("%s is a folder pair but %s is not a folder", id, abs)
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
	if err := os.Remove(s.path("held", id+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ─────────────────────────── held items ───────────────────────────
//
// A hold (see hold.go) lives in held/<pair-id>.json and NOWHERE else.
//
// ⚠ Not in pairs.json. The watcher is a long-running process and pairs.json
// is written by other processes while it runs — the desktop app adding,
// pausing and removing pairs, `filex sync confirm`. A watcher that rewrote
// pairs.json to record a hold (load, change one pair, save) could put back a
// pair that had just been removed, or drop one that had just been added, and
// on Windows a sharing violation on the rename failed the write outright.
// LoadPairs folds the hold into Pair.HoldNew / Pair.Held so every reader —
// `sync list --json`, the desktop app — still sees it on the pair; SavePairs
// never writes them.

// holdFile is held/<pair-id>.json.
type holdFile struct {
	Holding bool              `json:"hold_new"`
	Items   map[string]string `json:"held"` // rel → local signature when held ("dir" for a folder)
}

// loadHold reads a pair's hold. No file is "not holding".
func (s *Store) loadHold(pairID string) (holdFile, error) {
	h := holdFile{Items: map[string]string{}}
	raw, err := os.ReadFile(s.path("held", pairID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return h, fmt.Errorf("read held items of %s: %w", pairID, err)
	}
	if _, ok := probe["hold_new"]; !ok {
		// The first shape of this file (never released): a bare rel →
		// signature map, written only while the pair held.
		if err := json.Unmarshal(raw, &h.Items); err != nil {
			return holdFile{Items: map[string]string{}}, fmt.Errorf("read held items of %s: %w", pairID, err)
		}
		h.Holding = true
		return h, nil
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return holdFile{Items: map[string]string{}}, fmt.Errorf("read held items of %s: %w", pairID, err)
	}
	if h.Items == nil {
		h.Items = map[string]string{}
	}
	return h, nil
}

// saveHold writes a pair's hold; a hold that has ended leaves no file.
func (s *Store) saveHold(pairID string, h holdFile) error {
	if !h.Holding {
		err := os.Remove(s.path("held", pairID+".json"))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(s.path("held"), 0o700); err != nil {
		return err
	}
	if h.Items == nil {
		h.Items = map[string]string{}
	}
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return writeAtomic(s.path("held", pairID+".json"), raw)
}

// Holding reports whether the pair is holding its unknown local items for a
// decision.
//
// ⚠ An unreadable hold file reads as HOLDING. The hold exists to stop a stale
// mirror being uploaded over a cleaned-up server; failing open would do exactly
// that. `filex sync confirm` or `discard` rewrite the file either way.
func (s *Store) Holding(pairID string) (bool, error) {
	h, err := s.loadHold(pairID)
	if err != nil {
		return true, err
	}
	return h.Holding, nil
}

// LoadHeld returns the items the pair holds for a decision, keyed by
// pair-relative path, with the local signature each had then ("dir" for a
// folder). Nothing held is an empty map.
func (s *Store) LoadHeld(pairID string) (map[string]string, error) {
	h, err := s.loadHold(pairID)
	return h.Items, err
}

// recordHold stores what a pass held. triggered means this pass starts the
// hold (a first run); otherwise the hold is only refreshed while it is still
// on — a confirm or discard made while the pass was busy wins. replace swaps
// the whole list (a full pass saw everything); otherwise the items are added
// to it (a targeted pass saw one folder, a conflict turned out to differ).
func (s *Store) recordHold(pairID string, triggered, replace bool, held map[string]string) error {
	h, err := s.loadHold(pairID)
	if err != nil && !triggered {
		return err
	}
	if !triggered && !h.Holding {
		return nil
	}
	if replace || triggered || h.Items == nil {
		h.Items = map[string]string{}
	}
	for rel, sig := range held {
		h.Items[rel] = sig
	}
	h.Holding = true
	return s.saveHold(pairID, h)
}

// ConfirmHeld ends a hold so the next run sends the held items to the
// server. It returns how many the pair held.
func (s *Store) ConfirmHeld(pairID string) (int, error) {
	h, err := s.loadHold(pairID)
	if err != nil {
		return 0, err
	}
	return len(h.Items), s.saveHold(pairID, holdFile{})
}

// DiscardHeld ends a hold by moving the held files into the pair's local sync
// trash (recoverable for TrashRetentionDays), so the next run makes this side
// match the server: files that are only here are gone, and a file that
// differs comes down from the server. A folder that held only such files is
// removed once empty. A file whose signature changed since it was held is
// not what the person decided about: it is left in place and counted in kept.
//
// ⚠⚠ So is a file that has a baseline row by now. A row means the engine has
// since recorded it as in step on BOTH sides — an identical copy turned up on
// the server and was adopted, say — and trashing it here would make the next
// run carry the deletion to the server.
func (s *Store) DiscardHeld(p Pair, now time.Time) (moved, kept int, err error) {
	h, err := s.loadHold(p.ID)
	if err != nil {
		return 0, 0, err
	}
	base, _, err := s.LoadBaseline(p.ID)
	if err != nil {
		return 0, 0, err
	}
	var files, dirs []string
	for rel, sig := range h.Items {
		if sig == "dir" {
			dirs = append(dirs, rel)
		} else {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	for _, rel := range files {
		lp, err := localPathOf(p.Local, rel)
		if err != nil {
			return moved, kept, err
		}
		info, err := os.Lstat(lp)
		if errors.Is(err, os.ErrNotExist) {
			continue // already gone: nothing to decide
		}
		if err != nil {
			return moved, kept, err
		}
		cur := Node{Rel: rel, Size: info.Size(), ModMillis: info.ModTime().UnixMilli()}
		if _, agreed := base[rel]; agreed || !info.Mode().IsRegular() || cur.Signature() != h.Items[rel] {
			kept++
			continue
		}
		if err := s.TrashLocal(p.ID, p.Local, rel, now); err != nil {
			return moved, kept, err
		}
		moved++
	}
	// Deepest first, and only when empty: a folder that still holds anything
	// keeps it.
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], "/") > strings.Count(dirs[j], "/")
	})
	for _, rel := range dirs {
		if _, agreed := base[rel]; agreed {
			continue
		}
		if lp, err := localPathOf(p.Local, rel); err == nil {
			_ = os.Remove(lp)
		}
	}
	return moved, kept, s.saveHold(p.ID, holdFile{})
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
