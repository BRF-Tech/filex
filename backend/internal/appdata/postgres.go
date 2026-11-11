package appdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// ── A PostgreSQL database of the apps' own ─────────────────────────────────
//
// FILEX_APP_DATA_DSN: every replica of a filex reads the same apps' data.
// One table for every app, keyed by app; `k` is COLLATE "C" so the database
// orders keys byte for byte, as SQLite does (a locale collation would put
// `a-b` and `ab` in another order and break paging by `after`).

const pgSchema = `
CREATE TABLE IF NOT EXISTS filex_app_data (
    app     TEXT    NOT NULL,
    scope   TEXT    NOT NULL,
    k       TEXT    COLLATE "C" NOT NULL,
    v       TEXT    NOT NULL,
    rev     BIGINT  NOT NULL,
    counter BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (app, scope, k)
);
CREATE TABLE IF NOT EXISTS filex_app_usage (
    app   TEXT   PRIMARY KEY,
    keys  BIGINT NOT NULL,
    bytes BIGINT NOT NULL
);
`

// Postgres keeps every app's data in a database of the apps' own.
type Postgres struct {
	db       *sql.DB
	maxBytes int64
}

// ErrSameDatabase: FILEX_APP_DATA_DSN names filex's own database.
var ErrSameDatabase = errors.New("appdata: FILEX_APP_DATA_DSN names filex's own database; give apps a database of their own")

// OpenPostgres connects and creates the two tables. filexDSN is filex's own
// database's DSN when it is PostgreSQL ("" otherwise): naming the same
// database (host, port and database name) is refused.
func OpenPostgres(ctx context.Context, dsn, filexDSN string, maxBytes int64) (*Postgres, error) {
	if sameDatabase(dsn, filexDSN) {
		return nil, ErrSameDatabase
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("appdata: %w", err)
	}
	db.SetMaxOpenConns(16)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("appdata: %w", err)
	}
	if _, err := db.ExecContext(ctx, pgSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("appdata: %w", err)
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Postgres{db: db, maxBytes: maxBytes}, nil
}

// sameDatabase compares the host, port and database of two PostgreSQL
// DSNs (URL or key=value form).
func sameDatabase(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	ka, kb := pgTarget(a), pgTarget(b)
	return ka != "" && ka == kb
}

func pgTarget(dsn string) string {
	host, port, name := "", "5432", ""
	if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		host = u.Hostname()
		if p := u.Port(); p != "" {
			port = p
		}
		name = strings.TrimPrefix(u.Path, "/")
	} else {
		for _, f := range strings.Fields(dsn) {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			switch k {
			case "host":
				host = v
			case "port":
				port = v
			case "dbname":
				name = v
			}
		}
	}
	if host == "" || name == "" {
		return ""
	}
	return strings.ToLower(host) + ":" + port + "/" + name
}

func (p *Postgres) Driver() string  { return "postgres" }
func (p *Postgres) NodeLocal() bool { return false }

func (p *Postgres) Get(ctx context.Context, app, scope, key string) (Entry, bool, error) {
	if err := check(app, scope, key); err != nil {
		return Entry{}, false, err
	}
	e := Entry{Key: key}
	err := p.db.QueryRowContext(ctx, `SELECT v, rev, counter FROM filex_app_data WHERE app = $1 AND scope = $2 AND k = $3`, app, scope, key).Scan(&e.Value, &e.Rev, &e.Counter)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	return e, true, nil
}

func (p *Postgres) Put(ctx context.Context, app, scope, key, value string, c Cond) (int64, error) {
	if err := check(app, scope, key); err != nil {
		return 0, err
	}
	if !validValue(value) {
		return 0, ErrValue
	}
	return write(ctx, p.db, pgDialect, p.maxBytes, app, scope, key, putRule(value, c))
}

func (p *Postgres) Incr(ctx context.Context, app, scope, key string, by int64) (int64, error) {
	if err := check(app, scope, key); err != nil {
		return 0, err
	}
	var n int64
	_, err := write(ctx, p.db, pgDialect, p.maxBytes, app, scope, key, incrRule(by, &n))
	return n, err
}

func (p *Postgres) Delete(ctx context.Context, app, scope, key string, ifRev int64) (bool, error) {
	if err := check(app, scope, key); err != nil {
		return false, err
	}
	return remove(ctx, p.db, pgDialect, app, scope, key, ifRev)
}

func (p *Postgres) List(ctx context.Context, app, scope, prefix, after string, limit int) ([]Entry, string, error) {
	if err := checkList(app, scope, prefix, after); err != nil {
		return nil, "", err
	}
	return list(ctx, p.db, pgDialect, app, scope, prefix, after, clampLimit(limit))
}

func (p *Postgres) Usage(ctx context.Context, app string) (int64, int64, error) {
	if !appRe.MatchString(app) {
		return 0, 0, ErrApp
	}
	var keys, bytes int64
	err := p.db.QueryRowContext(ctx, `SELECT keys, bytes FROM filex_app_usage WHERE app = $1`, app).Scan(&keys, &bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return keys, bytes, err
}

func (p *Postgres) Drop(ctx context.Context, app string) error {
	if !appRe.MatchString(app) {
		return ErrApp
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM filex_app_data WHERE app = $1`, app); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM filex_app_usage WHERE app = $1`, app); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) Close() error { return p.db.Close() }
