// Package postgres_migrations exposes the Postgres migration files.
package postgres_migrations

import "embed"

// FS holds all *.sql files in this directory.
//
//go:embed *.sql
var FS embed.FS
