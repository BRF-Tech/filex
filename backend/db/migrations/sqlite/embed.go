// Package sqlite_migrations exposes the SQLite migration files as an embed.FS.
//
// We keep this in the same directory as the .sql files so //go:embed works,
// and the dialect-specific db driver imports this package.
package sqlite_migrations

import "embed"

// FS holds all *.sql files in this directory.
//
//go:embed *.sql
var FS embed.FS
