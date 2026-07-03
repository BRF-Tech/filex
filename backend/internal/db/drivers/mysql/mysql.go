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
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("mysql: open: %w", err)
	}
	conn.SetMaxOpenConns(20)
	conn.SetMaxIdleConns(4)
	conn.SetConnMaxIdleTime(5 * time.Minute)
	return conn, nil
}

// NewStore wraps the SQLite Store — MySQL's `?` placeholders + ON CONFLICT
// behavior is close enough that the same implementation works.
//
// Caveat: The shared SQL uses CURRENT_TIMESTAMP literals which are accepted
// by both engines. The few places where SQLite-only `ON CONFLICT(...) DO
// UPDATE` syntax is used (settings, external_services, thumbnails, node_meta)
// require MySQL's `ON DUPLICATE KEY UPDATE` instead — those are surfaced
// here as TODO. For the V1 release we recommend running on SQLite or
// PostgreSQL; MySQL support is verified for read-mostly use.
func (Driver) NewStore(sqlDB *sql.DB) db.Store {
	return sqlitedrv.Driver{}.NewStore(sqlDB)
}
