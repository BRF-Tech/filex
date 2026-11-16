package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

// Migrate applies all UP migrations using goose with the driver-supplied
// embedded FS, then the schema repairs (schema_repair.go): the columns the
// migrations assume an install has that an earlier release left out.
func Migrate(ctx context.Context, drv Driver, sqlDB *sql.DB) error {
	embedFS := drv.MigrationsFS()
	goose.SetBaseFS(embedFS)
	defer goose.SetBaseFS(nil)

	if err := goose.SetDialect(drv.Dialect()); err != nil {
		return fmt.Errorf("migrate: dialect: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		return err
	}
	added, err := repairSchema(ctx, drv.Dialect(), sqlDB)
	logSchemaRepairs(ctx, drv.Dialect(), added)
	return err
}

// MigrateDown rolls back one step.
func MigrateDown(ctx context.Context, drv Driver, sqlDB *sql.DB) error {
	embedFS := drv.MigrationsFS()
	goose.SetBaseFS(embedFS)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect(drv.Dialect()); err != nil {
		return err
	}
	return goose.DownContext(ctx, sqlDB, ".")
}

// MigrateStatus prints status to stdout.
func MigrateStatus(ctx context.Context, drv Driver, sqlDB *sql.DB) error {
	embedFS := drv.MigrationsFS()
	goose.SetBaseFS(embedFS)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect(drv.Dialect()); err != nil {
		return err
	}
	return goose.StatusContext(ctx, sqlDB, ".")
}
