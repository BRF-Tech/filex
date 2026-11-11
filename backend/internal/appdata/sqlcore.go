package appdata

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
)

// ── The SQL both drivers share ─────────────────────────────────────────────
//
// SQLite keeps one file per app (no app column); PostgreSQL keeps every app
// in one table keyed by app. dialect writes the difference once.

type dialect struct {
	pg bool
}

var (
	sqliteDialect = dialect{}
	pgDialect     = dialect{pg: true}
)

// q numbers the placeholders for PostgreSQL ($1, $2 ...) and leaves SQLite's
// `?` as they are.
func (d dialect) q(s string) string {
	if !d.pg {
		return s
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// where is the row's key: (scope, k), with app first on PostgreSQL.
func (d dialect) where() string {
	if d.pg {
		return "app = ? AND scope = ? AND k = ?"
	}
	return "scope = ? AND k = ?"
}

func (d dialect) keyArgs(app, scope, key string) []any {
	if d.pg {
		return []any{app, scope, key}
	}
	return []any{scope, key}
}

func (d dialect) kvTable() string {
	if d.pg {
		return "filex_app_data"
	}
	return "kv"
}

// usageWhere is the app's usage row.
func (d dialect) usage() (table, where string, args func(app string) []any) {
	if d.pg {
		return "filex_app_usage", "app = ?", func(app string) []any { return []any{app} }
	}
	return "usage", "id = 1", func(string) []any { return nil }
}

// putRule is Put's step for write: a counter is not overwritten, and
// IfAbsent / IfRev hold or the write is ErrConflict.
func putRule(value string, c Cond) func(cur *Entry) (string, bool, error) {
	return func(cur *Entry) (string, bool, error) {
		if cur != nil && cur.Counter {
			return "", false, ErrCounter
		}
		if c.IfAbsent && cur != nil {
			return "", false, ErrConflict
		}
		if c.IfRev != 0 && (cur == nil || cur.Rev != c.IfRev) {
			return "", false, ErrConflict
		}
		return value, false, nil
	}
}

// incrRule is Incr's step for write: a counter (or an absent key) moves by
// by, and *n is its new value; a key Put wrote is not a counter.
func incrRule(by int64, n *int64) func(cur *Entry) (string, bool, error) {
	return func(cur *Entry) (string, bool, error) {
		*n = by
		if cur != nil {
			if !cur.Counter {
				return "", false, ErrCounter
			}
			v, _ := strconv.ParseInt(cur.Value, 10, 64)
			*n = v + by
		}
		return strconv.FormatInt(*n, 10), true, nil
	}
}

// checkList is List's argument check, the same on every engine.
func checkList(app, scope, prefix, after string) error {
	if !appRe.MatchString(app) {
		return ErrApp
	}
	if !scopeRe.MatchString(scope) {
		return ErrScope
	}
	if err := checkPrefix(prefix); err != nil {
		return err
	}
	return checkPrefix(after)
}

// write is one key's read-modify-write in a transaction: next is handed the
// current entry (nil when absent) and answers the new value and whether the
// key is a counter, or an error that leaves everything as it was. The app's
// usage moves with it, and a write that would take the app past maxBytes is
// ErrQuota.
func write(ctx context.Context, db *sql.DB, d dialect, maxBytes int64, app, scope, key string,
	next func(cur *Entry) (string, bool, error)) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	lock := ""
	if d.pg {
		lock = " FOR UPDATE"
	}
	ut, uw, uargs := d.usage()
	if d.pg {
		// The usage row is the app's write lock on PostgreSQL: every writer
		// of one app passes through it, so the quota is checked against a
		// figure nobody else is moving.
		if _, err := tx.ExecContext(ctx, d.q(`INSERT INTO filex_app_usage (app, keys, bytes) VALUES (?, 0, 0) ON CONFLICT (app) DO NOTHING`), app); err != nil {
			return 0, err
		}
	}
	var keys, bytes int64
	if err := tx.QueryRowContext(ctx, d.q(`SELECT keys, bytes FROM `+ut+` WHERE `+uw+lock), uargs(app)...).Scan(&keys, &bytes); err != nil {
		return 0, err
	}
	var cur *Entry
	var e Entry
	var counter int
	var counterB bool
	row := tx.QueryRowContext(ctx, d.q(`SELECT v, rev, counter FROM `+d.kvTable()+` WHERE `+d.where()+lock), d.keyArgs(app, scope, key)...)
	if d.pg {
		err = row.Scan(&e.Value, &e.Rev, &counterB)
		e.Counter = counterB
	} else {
		err = row.Scan(&e.Value, &e.Rev, &counter)
		e.Counter = counter != 0
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return 0, err
	default:
		e.Key = key
		cur = &e
	}
	value, isCounter, err := next(cur)
	if err != nil {
		return 0, err
	}
	delta := int64(len(key) + len(value))
	dkeys := int64(1)
	rev := int64(1)
	if cur != nil {
		delta -= int64(len(key) + len(cur.Value))
		dkeys = 0
		rev = cur.Rev + 1
	}
	if delta > 0 && bytes+delta > maxBytes {
		return 0, ErrQuota
	}
	cnt := any(0)
	if d.pg {
		cnt = isCounter
	} else if isCounter {
		cnt = 1
	}
	if cur == nil {
		cols, ph := "scope, k, v, rev, counter", "?, ?, ?, ?, ?"
		args := []any{scope, key, value, rev, cnt}
		if d.pg {
			cols, ph = "app, "+cols, "?, "+ph
			args = append([]any{app}, args...)
		}
		res, err := tx.ExecContext(ctx, d.q(`INSERT INTO `+d.kvTable()+` (`+cols+`) VALUES (`+ph+`) ON CONFLICT DO NOTHING`), args...)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// Somebody wrote the key between our read and our insert.
			return 0, ErrConflict
		}
	} else {
		args := append([]any{value, rev, cnt}, d.keyArgs(app, scope, key)...)
		if _, err := tx.ExecContext(ctx, d.q(`UPDATE `+d.kvTable()+` SET v = ?, rev = ?, counter = ? WHERE `+d.where()), args...); err != nil {
			return 0, err
		}
	}
	uarg := append([]any{dkeys, delta}, uargs(app)...)
	if _, err := tx.ExecContext(ctx, d.q(`UPDATE `+ut+` SET keys = keys + ?, bytes = bytes + ? WHERE `+uw), uarg...); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return rev, nil
}

func remove(ctx context.Context, db *sql.DB, d dialect, app, scope, key string, ifRev int64) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	lock := ""
	if d.pg {
		lock = " FOR UPDATE"
	}
	var v string
	var rev int64
	err = tx.QueryRowContext(ctx, d.q(`SELECT v, rev FROM `+d.kvTable()+` WHERE `+d.where()+lock), d.keyArgs(app, scope, key)...).Scan(&v, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ifRev != 0 && rev != ifRev {
		return false, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, d.q(`DELETE FROM `+d.kvTable()+` WHERE `+d.where()), d.keyArgs(app, scope, key)...); err != nil {
		return false, err
	}
	ut, uw, uargs := d.usage()
	uarg := append([]any{int64(len(key) + len(v))}, uargs(app)...)
	if _, err := tx.ExecContext(ctx, d.q(`UPDATE `+ut+` SET keys = keys - 1, bytes = bytes - ? WHERE `+uw), uarg...); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func list(ctx context.Context, db *sql.DB, d dialect, app, scope, prefix, after string, limit int) ([]Entry, string, error) {
	lo := prefix
	cmp := ">="
	if after > prefix {
		lo, cmp = after, ">"
	}
	args := []any{scope, lo}
	where := "scope = ? AND k " + cmp + " ?"
	if d.pg {
		where = "app = ? AND " + where
		args = append([]any{app}, args...)
	}
	if prefix != "" {
		where += " AND k < ?"
		args = append(args, upper(prefix))
	}
	args = append(args, limit+1)
	rows, err := db.QueryContext(ctx, d.q(`SELECT k, v, rev, counter FROM `+d.kvTable()+` WHERE `+where+` ORDER BY k LIMIT ?`), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if d.pg {
			if err := rows.Scan(&e.Key, &e.Value, &e.Rev, &e.Counter); err != nil {
				return nil, "", err
			}
		} else {
			var c int
			if err := rows.Scan(&e.Key, &e.Value, &e.Rev, &c); err != nil {
				return nil, "", err
			}
			e.Counter = c != 0
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].Key
	}
	return out, next, nil
}
