package db

import (
	"database/sql"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ─────────────────── Lazy catalogue folders: the dialect-free half ─────────
//
// Migration 00059 (docs/LAZY-CATALOGUE.md). The two drivers spell the SQL
// (drivers/sqlite — which MySQL shares — and drivers/postgres); the row, the
// column list and the path arithmetic have no dialect and live here once, the
// same split customtheme_scan.go makes.

// CatalogueFolderColumns is the select list both drivers use, in the order
// ScanCatalogueFolder reads them.
const CatalogueFolderColumns = `storage_id, path_hash, path, depth, state, reconciled_at, visited_at, watched_at, reconcile_on_open, entries, held_back`

// RowScanner is the one method ScanCatalogueFolder needs from *sql.Row or
// *sql.Rows.
type RowScanner interface {
	Scan(dst ...any) error
}

// ScanCatalogueFolder reads one row in CatalogueFolderColumns order.
func ScanCatalogueFolder(r RowScanner) (*model.CatalogueFolder, error) {
	var (
		f                           model.CatalogueFolder
		state                       string
		reconciled, visited, watchd sql.NullTime
	)
	if err := r.Scan(&f.StorageID, &f.PathHash, &f.Path, &f.Depth, &state,
		&reconciled, &visited, &watchd, &f.ReconcileOnOpen, &f.Entries, &f.HeldBack); err != nil {
		return nil, err
	}
	f.State = model.CatalogueFolderState(state)
	f.ReconciledAt = nullTimePtr(reconciled)
	f.VisitedAt = nullTimePtr(visited)
	f.WatchedAt = nullTimePtr(watchd)
	return &f, nil
}

func nullTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

// CatalogueFolderPath is the canonical spelling of a catalogue folder: "/" for
// the storage root, "/a/b" below it — the spelling the walk stores in nodes.
func CatalogueFolderPath(dir string) string {
	clean := path.Clean("/" + strings.Trim(strings.ReplaceAll(dir, `\`, "/"), "/"))
	if clean == "." {
		return "/"
	}
	return clean
}

// CatalogueFolderDepth is how many segments deep a canonical folder path is
// (the root is 0).
func CatalogueFolderDepth(p string) int {
	p = strings.Trim(CatalogueFolderPath(p), "/")
	if p == "" {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// CatalogueSubtreeRange is the byte range holding every canonical path
// strictly below dir: (lo, hi) exclusive. The migration makes `path` a
// byte-ordered column on every engine, and '0' is the byte after '/', so
// `path > lo AND path < hi` is exactly "starts with dir/" — "/a/b.txt" and
// "/a/bc" fall outside, and the index answers it. ok is false for the root,
// where every other row is below.
func CatalogueSubtreeRange(dir string) (lo, hi string, ok bool) {
	p := CatalogueFolderPath(dir)
	if p == "/" {
		return "", "", false
	}
	return p + "/", p + "0", true
}

// CatalogueTime is how the SQLite/MySQL driver binds a catalogue timestamp:
// UTC, second precision, in CURRENT_TIMESTAMP's own spelling — so a bound
// value and a stored one compare as the instants they are (see
// ListStaleNodes for what an RFC 3339 binding did to that comparison).
func CatalogueTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}
