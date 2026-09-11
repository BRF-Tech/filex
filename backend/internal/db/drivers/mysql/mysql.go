// Package mysql is the MySQL/MariaDB DB driver.
//
// MySQL placeholder syntax (?) matches SQLite, so this driver wraps the
// SQLite Store implementation and only swaps the migrations FS, dialect,
// and DSN handling.
package mysql

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/brf-tech/filex/backend/internal/db"
	sqlitedrv "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"

	mysql_migrations "github.com/brf-tech/filex/backend/db/migrations/mysql"
)

func init() {
	db.Register("mysql", func() db.Driver { return &Driver{} })
}

// Driver implements db.Driver for MySQL/MariaDB.
type Driver struct{}

// Name implements db.Driver.
func (Driver) Name() string { return "mysql" }

// Dialect for goose.
func (Driver) Dialect() string { return "mysql" }

// MigrationsFS returns the embedded MySQL migrations.
func (Driver) MigrationsFS() embed.FS { return mysql_migrations.FS }

// Open returns a configured *sql.DB.
//
// DSN format: `user:pass@tcp(host:3306)/dbname?parseTime=true&loc=UTC&charset=utf8mb4`
func (Driver) Open(_ context.Context, dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, errors.New("mysql: empty DSN")
	}
	conn, err := sql.Open("mysql", NormalizeDSN(dsn))
	if err != nil {
		return nil, fmt.Errorf("mysql: open: %w", err)
	}
	conn.SetMaxOpenConns(20)
	conn.SetMaxIdleConns(4)
	conn.SetConnMaxIdleTime(5 * time.Minute)
	return conn, nil
}

// NewStore reuses the SQLite Store in MySQL mode: the placeholder syntax (?),
// the column names and the CURRENT_TIMESTAMP literals are shared, and the one
// construct the two engines spell differently — the upsert — is rewritten by
// sqlite.Store.upsert rather than kept as a second copy of every statement.
//
// ⚠ That sharing is only true while someone checks it. It was not checked
// until issue #19, and by then MySQL could not create its schema at all, let
// alone write to it. backend/internal/db runs the migrations, a schema-parity
// comparison and the first-five-minutes write path against a real MySQL
// server; point FILEX_TEST_MYSQL_DSN at one before trusting a change here.
func (Driver) NewStore(sqlDB *sql.DB) db.Store {
	return sqlitedrv.NewMySQLStore(sqlDB)
}

// NormalizeDSN fills in the three settings every statement in filex assumes,
// so an operator who writes a bare DSN gets a working server rather than a
// subtly wrong one.
//
//   - parseTime — without it every DATETIME scans into []byte and the row
//     scan fails.
//   - loc=UTC — how a Go time.Time is rendered on the way in.
//   - time_zone='+00:00' — what the SERVER thinks now() is. The two are
//     independent: with only loc=UTC, CURRENT_TIMESTAMP defaults and
//     comparisons still run in the server's local zone, so a queued op's
//     not_before (written as a UTC string) would be read hours early or late.
//
// An explicit value in the DSN always wins; this only adds what is missing.
func NormalizeDSN(dsn string) string {
	for key, val := range map[string]string{
		"parseTime": "true",
		"loc":       "UTC",
		"time_zone": "%27%2B00%3A00%27", // '+00:00', percent-encoded
	} {
		if strings.Contains(dsn, key+"=") {
			continue
		}
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + key + "=" + val
	}
	return dsn
}
