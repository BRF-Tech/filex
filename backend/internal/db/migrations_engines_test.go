package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mysqldsn "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"

	// Register every engine we tell operators they can install on.
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/mysql"
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/postgres"
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"
)

// The engines docs/INSTALLATION.md offers. A migration that only ever ran on
// SQLite is not a migration that ships: `binary` is a perfectly ordinary
// column name there and a reserved word in both of the others, so 00029
// aborted the FIRST boot of every Postgres and MySQL install (issue #19)
// while every test in this repo stayed green.
//
// Point the two DSNs at throwaway servers to run the real thing:
//
//	FILEX_TEST_PG_DSN='postgres://filex:filex@127.0.0.1:5432/postgres?sslmode=disable'
//	FILEX_TEST_MYSQL_DSN='root:filex@tcp(127.0.0.1:3306)/mysql?parseTime=true&loc=UTC&charset=utf8mb4'
//
// Each subtest creates its own database on that server and drops it again, so
// the DSN's own database is only ever used to issue CREATE DATABASE.
type engine struct {
	name   string
	driver string
	// dsnEnv is the environment variable holding an admin DSN. Empty means the
	// engine needs no server (SQLite) and therefore always runs.
	dsnEnv string
	// fresh returns a DSN for a brand-new empty database and registers its
	// teardown. Called once per subtest.
	fresh func(t *testing.T, admin string) string
}

func engines() []engine {
	return []engine{
		{name: "sqlite", driver: "sqlite", fresh: freshSQLite},
		{name: "postgres", driver: "postgres", dsnEnv: "FILEX_TEST_PG_DSN", fresh: freshPostgres},
		{name: "mysql", driver: "mysql", dsnEnv: "FILEX_TEST_MYSQL_DSN", fresh: freshMySQL},
	}
}

// TestMigrationsApplyOnEveryEngine runs the full migration set, from nothing,
// on every engine filex claims to support — then runs it a second time to
// prove a restart is a no-op.
func TestMigrationsApplyOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)

			wanted := migrationCount(t, drv)
			applied := appliedCount(t, sqlDB)
			require.Equal(t, wanted, applied,
				"%s: %d migration files on disk, %d applied — a dialect is missing a file or one was skipped",
				e.name, wanted, applied)

			// Second run: what every restart does.
			require.NoError(t, db.Migrate(context.Background(), drv, sqlDB),
				"%s: re-running migrations must be a no-op", e.name)
		})
	}
}

// TestPluginStoreRoundTripOnEveryEngine exercises the store methods for the
// table issue #19 was reported against. A DDL fix alone is not enough: the
// queries name the same columns, and a reserved word in a SELECT list fails
// exactly as loudly as one in a CREATE TABLE — just later, on the first
// operator who opens the plugins page.
func TestPluginStoreRoundTripOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			created, err := store.CreatePlugin(ctx, &model.Plugin{
				Name:        "s3-alt",
				Kind:        "binary",
				Binary:      "filex-plugin-s3-alt",
				SHA256:      strings.Repeat("ab", 32),
				Enabled:     true,
				Version:     "1.2.3",
				Driver:      "s3alt",
				TokenSealed: "sealed",
			})
			require.NoError(t, err)
			require.NotZero(t, created.ID)
			require.Equal(t, "filex-plugin-s3-alt", created.Binary)

			byName, err := store.GetPluginByName(ctx, "s3-alt")
			require.NoError(t, err)
			require.Equal(t, created.ID, byName.ID)
			require.Equal(t, "filex-plugin-s3-alt", byName.Binary)

			list, err := store.ListPlugins(ctx)
			require.NoError(t, err)
			require.Len(t, list, 1)

			byName.Binary = "filex-plugin-s3-alt-v2"
			byName.LastError = "refused: checksum changed"
			require.NoError(t, store.UpdatePlugin(ctx, byName))

			reread, err := store.GetPlugin(ctx, created.ID)
			require.NoError(t, err)
			require.Equal(t, "filex-plugin-s3-alt-v2", reread.Binary)
			require.Equal(t, "refused: checksum changed", reread.LastError)

			require.NoError(t, store.DeletePlugin(ctx, created.ID))
			_, err = store.GetPlugin(ctx, created.ID)
			require.Error(t, err)
		})
	}
}

// openMigrated skips when the engine's server is not configured, otherwise
// returns a migrated, empty database.
func openMigrated(t *testing.T, e engine) (*sql.DB, db.Driver) {
	t.Helper()

	admin := ""
	if e.dsnEnv != "" {
		admin = os.Getenv(e.dsnEnv)
		if admin == "" {
			t.Skipf("%s not exercised: set %s to a throwaway server", e.name, e.dsnEnv)
		}
	}

	drv, err := db.Get(e.driver)
	require.NoError(t, err)

	sqlDB, err := drv.Open(context.Background(), e.fresh(t, admin))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.Migrate(context.Background(), drv, sqlDB), "%s: migrate", e.name)
	return sqlDB, drv
}

func migrationCount(t *testing.T, drv db.Driver) int {
	t.Helper()
	embedded := drv.MigrationsFS()
	files, err := fs.Glob(embedded, "*.sql")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	return len(files)
}

// appliedCount counts real migrations, excluding goose's own version-0 row.
func appliedCount(t *testing.T, sqlDB *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, sqlDB.QueryRow(
		`SELECT COUNT(*) FROM goose_db_version WHERE version_id > 0 AND is_applied = true`).Scan(&n))
	return n
}

func freshSQLite(t *testing.T, _ string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "filex-migrations.sqlite")
}

func freshPostgres(t *testing.T, admin string) string {
	t.Helper()

	name := scratchDBName()
	adminDB, err := sql.Open("pgx", admin)
	require.NoError(t, err, "postgres: open admin connection")
	defer adminDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = adminDB.ExecContext(ctx, `CREATE DATABASE "`+name+`"`)
	require.NoError(t, err, "postgres: create scratch database")

	u, err := url.Parse(admin)
	require.NoError(t, err, "postgres: DSN must be a URL")
	u.Path = "/" + name

	t.Cleanup(func() {
		drop, err := sql.Open("pgx", admin)
		if err != nil {
			return
		}
		defer drop.Close()
		_, _ = drop.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`)
	})
	return u.String()
}

func freshMySQL(t *testing.T, admin string) string {
	t.Helper()

	name := scratchDBName()
	adminDB, err := sql.Open("mysql", admin)
	require.NoError(t, err, "mysql: open admin connection")
	defer adminDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = adminDB.ExecContext(ctx, "CREATE DATABASE `"+name+"`")
	require.NoError(t, err, "mysql: create scratch database")

	cfg, err := mysqldsn.ParseDSN(admin)
	require.NoError(t, err, "mysql: DSN must parse")
	cfg.DBName = name

	t.Cleanup(func() {
		drop, err := sql.Open("mysql", admin)
		if err != nil {
			return
		}
		defer drop.Close()
		_, _ = drop.Exec("DROP DATABASE IF EXISTS `" + name + "`")
	})
	return cfg.FormatDSN()
}

func scratchDBName() string {
	return fmt.Sprintf("filex_migtest_%d", time.Now().UnixNano())
}
