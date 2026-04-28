// Package mysql_migrations exposes the MySQL migration files.
package mysql_migrations

import "embed"

// FS holds all *.sql files in this directory.
//
//go:embed *.sql
var FS embed.FS
