// Package dbsetting — seedvalue.go
//
// Seeding from a value the caller already holds, for the number and the
// switch (StringSpec.SeedValue is the same thing for text).
//
// It exists for settings that had TWO ways in before they moved to the
// database: an environment variable and a key in config.yaml. Seed covers the
// variable. A deployment that configured the YAML key instead must keep its
// value across the upgrade too, so the caller hands that value here, under
// the very same rule: written only on a boot where no row exists yet, so it
// can never overwrite what an admin has since saved. The search settings are
// the first such family (internal/search settings.go).
//
// ⚠ "Only when it was set" is the CALLER's question, because only the caller
// knows where the value came from. A value equal to Default should not be
// passed: a row holding the default pins it, and a later release that
// changes the default would then not reach this install.
package dbsetting

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
)

// SeedValue writes v as the first value of the setting when, and only when,
// no row exists yet. Out of range clamps and logs, as Seed does for the
// environment variable. source names where v came from, for the log line
// ("config.yaml search.memory_percent").
func (s IntSpec) SeedValue(ctx context.Context, st Store, v int, source string) error {
	if st == nil || stored(ctx, st, s.Key) {
		return nil
	}
	in, clamped := s.Clamp(v)
	if clamped {
		slog.Warn("setting seed out of range; clamped",
			slog.String("key", s.Key), slog.String("source", source),
			slog.Int("given", v), slog.Int("seeded", in))
	}
	if err := st.UpsertSetting(ctx, s.Key, strconv.Itoa(in)); err != nil {
		return fmt.Errorf("dbsetting: seed %s: %w", s.Key, err)
	}
	slog.Info("setting seeded",
		slog.String("key", s.Key), slog.String("source", source), slog.Int("value", in))
	return nil
}

// SeedValue is IntSpec.SeedValue for a switch.
func (s BoolSpec) SeedValue(ctx context.Context, st Store, v bool, source string) error {
	if st == nil || stored(ctx, st, s.Key) {
		return nil
	}
	if err := st.UpsertSetting(ctx, s.Key, FormatBool(v)); err != nil {
		return fmt.Errorf("dbsetting: seed %s: %w", s.Key, err)
	}
	slog.Info("setting seeded",
		slog.String("key", s.Key), slog.String("source", source), slog.Bool("value", v))
	return nil
}

// stored reports whether the settings table already holds a value for key:
// the one condition under which no seed of any kind may write.
func stored(ctx context.Context, g Getter, key string) bool {
	cur, err := g.GetSetting(ctx, key)
	return err == nil && cur != ""
}
