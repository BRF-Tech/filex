// Package dbsetting is the shared shape for a filex setting that lives in the
// `settings` table, is seeded once from an environment variable, is clamped to
// a range on read, and is validated at the API that writes it.
//
// It exists because that is four behaviours, and every one of them is a place
// a per-setting reimplementation quietly diverges: one setting clamps and the
// next rejects, one treats a blank row as the default and the next as zero,
// one documents its env var as an override when it is really a seed. Declaring
// a setting as an IntSpec, a BoolSpec or a StringSpec means the next one is a
// struct literal rather than a redesign.
//
// # The three kinds, and why they are three
//
//   - IntSpec    — a magnitude with bounds (minutes, days, MB). Out of range
//     clamps on read and is refused on write.
//   - BoolSpec   — a switch (bool.go). No bounds exist, so nothing clamps;
//     unparseable text falls back to Default rather than being guessed at.
//   - StringSpec — text with a shape (string.go): a mode name, an address.
//     Bounds are replaced by a Check hook, because "clamav 3310" is the same
//     shape to the compiler as "clamav:3310" and only one of them can be
//     dialled.
//
// They share Seeder (first-boot seeding), so one SeedAll call at boot carries
// a mixed list.
//
// # Resolution order
//
//	stored row (settings table)  →  env var, on first boot only  →  Default
//
// ⚠⚠ The env var is a SEED, not an override. It is consumed exactly once, by
// Seed at boot, and only when no row exists yet. From the moment a row exists
// — written by that seeding or by an admin in the UI — the env var is inert:
// editing it in compose and restarting changes nothing, because the row wins.
// This is the single most surprising thing about the pattern and it belongs in
// every doc row that describes one of these variables.
//
// # Why seed instead of override
//
// The value is editable in the admin UI. If the env var kept overriding, every
// restart would silently undo what an operator changed on the page, and the UI
// would be lying about what is in force. Seeding gives a fresh install a way
// to be provisioned from compose without taking the setting away from the
// person running it afterwards.
package dbsetting

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
)

// Getter is the settings-table read. db.Store satisfies it.
type Getter interface {
	GetSetting(ctx context.Context, key string) (string, error)
}

// Store is read + write, needed for seeding. db.Store satisfies it.
type Store interface {
	Getter
	UpsertSetting(ctx context.Context, key, value string) error
}

// IntSpec declares one integer-valued setting.
//
// Zero is never a legal value: every setting shaped this way is a magnitude
// (minutes, days, counts) whose "off" state, if it has one, is a separate and
// explicit switch. Min is therefore expected to be ≥ 1 and callers get told
// so rather than discovering it through a clamp.
type IntSpec struct {
	// Key is the settings-table key, dotted and namespaced by feature,
	// e.g. "antivirus.save_scan_window_minutes".
	Key string
	// EnvVar seeds the first-boot value. Empty means no seeding.
	EnvVar string
	// Default applies when there is no row and no seed.
	Default int
	// Min and Max are inclusive bounds. Values outside clamp on read and
	// are refused on write.
	Min, Max int
	// Unit names what the number counts, for log and error text
	// ("minutes", "days"). Purely cosmetic.
	Unit string
	// SeedParse converts the env var's text into this setting's unit. Nil
	// means "the env var is already in this unit" (plain integer parse).
	//
	// It exists for settings whose environment variable predates the move to
	// the database and speaks a different unit — FILEX_CLAMAV_MAX is bytes
	// while the setting an admin edits is megabytes. Converting at SEED time
	// rather than renaming the variable is what keeps existing deployments
	// from losing their configuration.
	SeedParse func(raw string) (int, bool)
}

// SettingKey identifies the row this spec owns (Seeder).
func (s IntSpec) SettingKey() string { return s.Key }

// Clamp returns the value that will be in force for v, and whether v had to
// be changed to get there.
func (s IntSpec) Clamp(v int) (int, bool) {
	switch {
	case v < s.Min:
		return s.Min, true
	case v > s.Max:
		return s.Max, true
	}
	return v, false
}

// Validate is the write-side gate: an operator typing an impossible value must
// be told no at the moment they save it, in the UI, rather than having it
// silently clamped on some later read. Clamping on read stays as the last
// line of defence for rows that predate a bound or were written by hand.
func (s IntSpec) Validate(v int) error {
	if v < s.Min || v > s.Max {
		return fmt.Errorf("%s must be between %d and %d %s", s.Key, s.Min, s.Max, s.Unit)
	}
	return nil
}

// Resolve reads the value in force.
//
// ⚠ Call it at the point of USE, not once at boot: reading per use is what
// makes these settings live-changeable, which is the reason they are in the
// database rather than in the environment. Callers that have already acted on
// an earlier value (a scheduled job carrying a deadline, say) keep it — a
// changed setting affects the next decision, never one already made.
//
// A missing row, a store error, a blank value or unparseable text all resolve
// to Default: none of them carries operator intent. An out-of-range NUMBER
// does carry intent, so it clamps to the nearest bound and logs once, because
// the operator has to be able to find out from the logs that the number they
// typed is not the number in force.
func (s IntSpec) Resolve(ctx context.Context, g Getter) int {
	if g == nil {
		return s.Default
	}
	raw, err := g.GetSetting(ctx, s.Key)
	if err != nil || raw == "" {
		return s.Default
	}
	n, perr := strconv.Atoi(raw)
	if perr != nil {
		slog.Warn("setting is not a number; using default",
			slog.String("key", s.Key),
			slog.String("value", raw),
			slog.Int("in_force", s.Default))
		return s.Default
	}
	v, clamped := s.Clamp(n)
	if clamped {
		slog.Warn("setting out of range; clamped",
			slog.String("key", s.Key),
			slog.Int("stored", n),
			slog.Int("in_force", v),
			slog.Int("min", s.Min),
			slog.Int("max", s.Max),
			slog.String("unit", s.Unit))
	}
	return v
}

// Seed writes the env var's value into the settings table when, and only when,
// no row exists yet. Call once at boot, before anything resolves the setting.
//
// A seed value outside the bounds is clamped and logged rather than refused:
// the alternative is a container that will not start because of a scan
// interval, which is disproportionate. Unparseable text is ignored entirely —
// there is no honest number to store — and Default applies.
//
// Returns nil when there is nothing to do, which is the normal case on every
// boot after the first.
func (s IntSpec) Seed(ctx context.Context, st Store) error {
	if st == nil || s.EnvVar == "" {
		return nil
	}
	raw := os.Getenv(s.EnvVar)
	if raw == "" {
		return nil
	}
	if cur, err := st.GetSetting(ctx, s.Key); err == nil && cur != "" {
		slog.Debug("setting already stored; env seed ignored",
			slog.String("key", s.Key), slog.String("env", s.EnvVar))
		return nil
	}
	n, ok := s.parseSeed(raw)
	if !ok {
		slog.Warn("setting seed is not a number; ignored",
			slog.String("key", s.Key),
			slog.String("env", s.EnvVar),
			slog.String("value", raw))
		return nil
	}
	v, clamped := s.Clamp(n)
	if clamped {
		slog.Warn("setting seed out of range; clamped",
			slog.String("key", s.Key),
			slog.String("env", s.EnvVar),
			slog.Int("given", n),
			slog.Int("seeded", v))
	}
	if err := st.UpsertSetting(ctx, s.Key, strconv.Itoa(v)); err != nil {
		return fmt.Errorf("dbsetting: seed %s: %w", s.Key, err)
	}
	slog.Info("setting seeded from environment",
		slog.String("key", s.Key), slog.String("env", s.EnvVar), slog.Int("value", v))
	return nil
}

// parseSeed applies SeedParse when set, otherwise a plain integer parse.
func (s IntSpec) parseSeed(raw string) (int, bool) {
	if s.SeedParse != nil {
		return s.SeedParse(raw)
	}
	n, err := strconv.Atoi(raw)
	return n, err == nil
}

// Seeder is the one behaviour every spec kind shares: consume an environment
// variable into the settings table on a boot where no row exists yet.
//
// It exists so a single SeedAll call at boot can carry a MIXED list — an
// IntSpec, a BoolSpec and a StringSpec side by side. Splitting it into
// SeedAllInts / SeedAllBools would put the ordering of boot-time seeding in
// three places and make "did this setting get seeded?" a question with three
// answers.
type Seeder interface {
	// SettingKey is the row the spec owns, for log text.
	SettingKey() string
	// Seed writes the environment's value, first boot only.
	Seed(ctx context.Context, st Store) error
}

// SeedAll runs Seed for each spec, logging failures rather than aborting boot.
// Adding a setting to the family is one more entry at the call site.
//
// ⚠ A failed seed is not fatal on purpose: the setting falls back to its
// Default and the server comes up. A container that refuses to start because
// it could not write a scan interval is a worse outcome than one that starts
// with the default and says so in the log.
func SeedAll(ctx context.Context, st Store, specs ...Seeder) {
	for _, s := range specs {
		if err := s.Seed(ctx, st); err != nil {
			slog.Warn("dbsetting: seed failed",
				slog.String("key", s.SettingKey()), slog.String("err", err.Error()))
		}
	}
}
