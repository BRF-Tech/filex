package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/config"
)

// snapshotBeforeUpgrade takes a database backup immediately before a
// self-upgrade replaces the binary. It is wired as the updater's PreApply
// hook, and a non-nil error ABORTS the upgrade with nothing changed.
//
// Why this exists: filex has no down migrations. Once a release has migrated
// the schema there is no code path back — the backup IS the rollback. An
// upgrade that cannot produce one should not happen unattended.
//
// Two shapes:
//
//   - sqlite (the default): filex takes the snapshot itself with VACUUM INTO,
//     which produces a consistent copy of a live database. A plain file copy
//     would race with WAL content and can yield a torn file.
//   - external engines (postgres/mysql): filex does not carry pg_dump/mysqldump
//     and will not shell out to tools it cannot verify. The operator supplies
//     the command via FILEX_UPDATE_PRE_COMMAND; if none is set the upgrade
//     still proceeds, but the absence is logged as a warning — a patch release
//     carries no migration by policy, so this is a real but bounded risk.
func snapshotBeforeUpgrade(ctx context.Context, cfg config.Config, targetVersion string) error {
	if cmdline := strings.TrimSpace(os.Getenv("FILEX_UPDATE_PRE_COMMAND")); cmdline != "" {
		return runPreCommand(ctx, cmdline, targetVersion)
	}
	if !strings.EqualFold(cfg.DB.Driver, "sqlite") {
		slog.Warn("update: no database snapshot taken",
			slog.String("driver", cfg.DB.Driver),
			slog.String("hint", "set FILEX_UPDATE_PRE_COMMAND to a dump command for external databases"))
		return nil
	}
	return snapshotSQLite(ctx, cfg.DB.DSN, targetVersion)
}

// snapshotSQLite writes a consistent copy next to the live database, named
// after the version being installed so the restore path is obvious months
// later: instance.sqlite.pre-v0.7.6-20260729T113000Z.
func snapshotSQLite(ctx context.Context, dsn, targetVersion string) error {
	path := sqlitePathFromDSN(dsn)
	if path == "" {
		return fmt.Errorf("cannot determine sqlite path from DSN %q", dsn)
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	dest := fmt.Sprintf("%s.pre-%s-%s", path, strings.TrimPrefix(targetVersion, "v"), stamp)

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("snapshot: cannot open database: %w", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	// VACUUM INTO refuses to overwrite, which is what we want: a name clash
	// means something else already wrote there and we should not clobber it.
	if _, err := conn.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("snapshot failed (upgrade aborted): %w", err)
	}
	if fi, err := os.Stat(dest); err == nil {
		slog.Info("update: database snapshot taken",
			slog.String("path", dest), slog.Int64("bytes", fi.Size()))
	}
	return nil
}

// runPreCommand executes the operator-supplied backup command through the
// system shell, with the target version exposed as FILEX_UPDATE_TARGET.
func runPreCommand(ctx context.Context, cmdline, targetVersion string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdline)
	cmd.Env = append(os.Environ(), "FILEX_UPDATE_TARGET="+targetVersion)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("FILEX_UPDATE_PRE_COMMAND failed (upgrade aborted): %w: %s", err, strings.TrimSpace(string(out)))
	}
	slog.Info("update: pre-upgrade command finished", slog.String("cmd", cmdline))
	return nil
}

// sqlitePathFromDSN strips the "file:" scheme and any ?query parameters that
// modernc/sqlite accepts, leaving a filesystem path.
func sqlitePathFromDSN(dsn string) string {
	p := strings.TrimSpace(dsn)
	p = strings.TrimPrefix(p, "file:")
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if p == "" || p == ":memory:" {
		return ""
	}
	return filepath.Clean(p)
}
