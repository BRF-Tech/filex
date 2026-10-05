package db

import (
	"context"
	"database/sql"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// NodesUnderSQL implements what a Store answers about the rows at and below a
// folder - ListNodesUnder, ListStaleNodesUnder, CountLiveNodesUnder and
// HasLiveNodesUnder - written ONCE for every engine. Each driver embeds a
// *NodesUnderSQL in its Store, so the methods are promoted and there is no
// per-driver copy to drift (the arrangement of VanishedSQL).
//
// "Below dir" is a BYTE RANGE of nodes.path (subtreeRange), one per spelling a
// stored path can have ("/a/b/…" and "a/b/…"), and the index of migration
// 00083 answers it. Until then it was SUBSTR(path,1,n)=?, which no index
// answers: the listing and the count read every row of the storage, and the
// stale rows every row of it last seen before the cut-off - in a rescan, every
// row outside the folder.
//
// ⚠ One statement arm per spelling, never an OR between the two. With an OR
// SQLite - which has no statistics here, nothing runs ANALYZE - plans the
// listing and the stale rows as before: nodes entered by storage_id alone, or
// by (storage_id, seen_at), never by the folder.
// TestSubtreeQuestionsAreAnsweredFromThePathIndexOnEveryEngine reads the plan
// of every statement below, on every engine.
//
// ⚠ Not every subtree match is here. Trashing and restoring a folder and
// naming who deleted it (retagTrashedSubtree, restoreTrashedSubtree,
// SetNodeDeletedBy) still match with SUBSTR and still read the storage's rows.
type NodesUnderSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
	// Time binds a timestamp the way the engine compares nodes.seen_at:
	// CatalogueTime where it is compared as text, PlainTime on PostgreSQL.
	Time func(*time.Time) any
	// Columns is the driver's node column list and Scan reads one row of it.
	Columns string
	Scan    func(RowScanner) (*model.Node, error)
	// Path is nodes.path as this engine compares it BYTE FOR BYTE. Empty means
	// `path`: the column is byte-ordered itself on SQLite, and on MySQL since
	// 00041 (utf8mb4_0900_bin). PostgreSQL passes `path COLLATE "C"`: its
	// column has the database's collation, under which a range of names is not
	// a range of bytes: "/müşteri/x" sorts inside "/Müşteri/"…"/Müşteri0"
	// under ICU en-US, and "/Rapor/a.txt" outside "/Rapor/"…"/Rapor0" under
	// glibc en_US.utf8.
	Path string
	// Key wraps an SQL operand - the path column or a bound of the range - in
	// the expression the engine's index holds instead of the path: its first
	// characters, byte ordered. Nil where the index is on the column: SQLite
	// holds the whole path, and MySQL enters its path(512) index with a
	// comparison on path. PostgreSQL has neither. A B-tree row there stops at
	// 2704 bytes, so an index on the whole path would make filex refuse a path
	// the column takes, and one on left(path, 512) is entered only by a
	// comparison on that expression. The statements then ask Key("path") for
	// the range cut to the key's length - the bounds cut by the same
	// expression, on the server - and path for the range itself.
	Key func(of string) string
}

// subtreeStatement is one statement about a folder and what it is run with.
type subtreeStatement struct {
	query string
	args  []any
}

// treeSpellings returns the two spellings a row's path can carry for dir -
// "/a/b" and "a/b" - or ok=false for the storage root, which is never a
// subtree.
func treeSpellings(dir string) (slashed, bare string, ok bool) {
	bare = strings.Trim(path.Clean("/"+strings.Trim(dir, "/")), "/")
	if bare == "" {
		return "", "", false
	}
	return "/" + bare, bare, true
}

// subtreeRange is the byte range holding every path strictly below the folder
// spelled p: lo <= path < hi. '0' is the byte after '/', so wherever path is
// compared byte for byte the range is exactly "starts with p/": for "/a/b",
// "/a/b.txt", "/a/b-old/x", "/a/b0" and "/A/b/x" are all outside it, and a `%`
// or `_` in p is a character like any other - which LIKE could not promise,
// and why the match was SUBSTR before it was this.
//
// ⚠ Not CatalogueSubtreeRange: that one is for catalogue_folders, whose paths
// have one spelling, and its lower bound is exclusive (path > lo).
func subtreeRange(p string) (lo, hi string) { return p + "/", p + "0" }

// path is nodes.path as this engine compares it byte for byte (Path).
func (o *NodesUnderSQL) path() string {
	if o.Path == "" {
		return "path"
	}
	return o.Path
}

// below is "this storage's rows strictly below the folder spelled p", and what
// it is run with. Storage and range are one conjunction: together they are
// what the index is entered with.
//
// Where the index holds a key cut from the path, the key is asked first, for
// the range with both ends cut the same way and both INCLUDED: cutting the two
// sides of a byte comparison at the same character keeps their order or makes
// them equal, so a path inside lo…hi has a key inside Key(lo)…Key(hi).
func (o *NodesUnderSQL) below(storageID int64, p string) (string, []any) {
	lo, hi := subtreeRange(p)
	if o.Key == nil {
		return `storage_id=? AND ` + o.path() + `>=? AND ` + o.path() + `<?`, []any{storageID, lo, hi}
	}
	key := o.Key("path")
	return `storage_id=? AND ` + key + `>=` + o.Key("?") + ` AND ` + key + `<=` + o.Key("?") +
			` AND ` + o.path() + `>=? AND ` + o.path() + `<?`,
		[]any{storageID, lo, hi, lo, hi}
}

// at is "this storage's rows at the folder itself", in both spellings.
func (o *NodesUnderSQL) at(storageID int64, slashed, bare string) (string, []any) {
	if o.Key == nil {
		return `storage_id=? AND ` + o.path() + ` IN (?,?)`, []any{storageID, slashed, bare}
	}
	return `storage_id=? AND ` + o.Key("path") + ` IN (` + o.Key("?") + `,` + o.Key("?") + `) AND ` + o.path() + ` IN (?,?)`,
		[]any{storageID, slashed, bare, slashed, bare}
}

// statement binds query, written with `?`, to what its arms are run with, in
// the order they stand in it.
func (o *NodesUnderSQL) statement(query string, args ...[]any) subtreeStatement {
	st := subtreeStatement{query: rebind(o.Placeholders, query)}
	for _, a := range args {
		st.args = append(st.args, a...)
	}
	return st
}

func (o *NodesUnderSQL) listStatement(storageID int64, dir string, includeDeleted bool) (subtreeStatement, bool) {
	slashed, bare, ok := treeSpellings(dir)
	if !ok {
		return subtreeStatement{}, false
	}
	self, selfArgs := o.at(storageID, slashed, bare)
	a, aArgs := o.below(storageID, slashed)
	b, bArgs := o.below(storageID, bare)
	from, live := `SELECT `+o.Columns+` FROM nodes WHERE `, ``
	if !includeDeleted {
		live = ` AND deleted_at IS NULL`
	}
	// No ORDER BY: over a UNION it is a sort per arm and a merge (SQLite) or a
	// temporary table (MySQL), which for a folder that is most of a storage
	// cost more than the scan this replaced. ListNodesUnder sorts the rows.
	return o.statement(from+self+live+` UNION ALL `+from+a+live+` UNION ALL `+from+b+live,
		selfArgs, aArgs, bArgs), true
}

func (o *NodesUnderSQL) staleStatement(storageID int64, dir string, before time.Time) (subtreeStatement, bool) {
	slashed, bare, ok := treeSpellings(dir)
	if !ok {
		return subtreeStatement{}, false
	}
	a, aArgs := o.below(storageID, slashed)
	b, bArgs := o.below(storageID, bare)
	stale, seen := `SELECT `+o.Columns+` FROM nodes WHERE seen_at < ? AND deleted_at IS NULL AND `, []any{o.Time(&before)}
	return o.statement(stale+a+` UNION ALL `+stale+b, seen, aArgs, seen, bArgs), true
}

func (o *NodesUnderSQL) countLiveStatement(storageID int64, dir string) (subtreeStatement, bool) {
	slashed, bare, ok := treeSpellings(dir)
	if !ok {
		return subtreeStatement{}, false
	}
	a, aArgs := o.below(storageID, slashed)
	b, bArgs := o.below(storageID, bare)
	// The two spellings are two ranges with no row in both, so their counts
	// add up.
	const live = `(SELECT COUNT(*) FROM nodes WHERE deleted_at IS NULL AND `
	return o.statement(`SELECT `+live+a+`) + `+live+b+`)`, aArgs, bArgs), true
}

func (o *NodesUnderSQL) hasLiveStatement(storageID int64, dir string) (subtreeStatement, bool) {
	slashed, bare, ok := treeSpellings(dir)
	if !ok {
		return subtreeStatement{}, false
	}
	a, aArgs := o.below(storageID, slashed)
	b, bArgs := o.below(storageID, bare)
	const live = `EXISTS(SELECT 1 FROM nodes WHERE deleted_at IS NULL AND `
	return o.statement(`SELECT `+live+a+`) OR `+live+b+`)`, aArgs, bArgs), true
}

func (o *NodesUnderSQL) nodes(ctx context.Context, st subtreeStatement) ([]*model.Node, error) {
	rows, err := Conn(ctx, o.Pool).QueryContext(ctx, st.query, st.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Node
	for rows.Next() {
		n, err := o.Scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ListNodesUnder implements Store.ListNodesUnder.
func (o *NodesUnderSQL) ListNodesUnder(ctx context.Context, storageID int64, dir string, includeDeleted bool) ([]*model.Node, error) {
	st, ok := o.listStatement(storageID, dir, includeDeleted)
	if !ok {
		return nil, nil
	}
	out, err := o.nodes(ctx, st)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ListStaleNodesUnder implements Store.ListStaleNodesUnder.
func (o *NodesUnderSQL) ListStaleNodesUnder(ctx context.Context, storageID int64, dir string, before time.Time) ([]*model.Node, error) {
	st, ok := o.staleStatement(storageID, dir, before)
	if !ok {
		return nil, nil
	}
	return o.nodes(ctx, st)
}

// CountLiveNodesUnder implements Store.CountLiveNodesUnder.
func (o *NodesUnderSQL) CountLiveNodesUnder(ctx context.Context, storageID int64, dir string) (int64, error) {
	st, ok := o.countLiveStatement(storageID, dir)
	if !ok {
		return 0, nil
	}
	var n int64
	err := Conn(ctx, o.Pool).QueryRowContext(ctx, st.query, st.args...).Scan(&n)
	return n, err
}

// HasLiveNodesUnder implements Store.HasLiveNodesUnder.
func (o *NodesUnderSQL) HasLiveNodesUnder(ctx context.Context, storageID int64, dir string) (bool, error) {
	st, ok := o.hasLiveStatement(storageID, dir)
	if !ok {
		return false, nil
	}
	var has bool
	err := Conn(ctx, o.Pool).QueryRowContext(ctx, st.query, st.args...).Scan(&has)
	return has, err
}
