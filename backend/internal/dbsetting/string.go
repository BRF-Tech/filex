// Package dbsetting — string.go
//
// The text-shaped member of the family: a mode name, a host:port, a path.
//
// ⚠⚠ A string setting needs a validation hook and an IntSpec does not, and
// that difference is the reason this type exists rather than a `Value string`
// field somewhere. A number is validated by its bounds, which the spec already
// declares. Text has no bounds — "clamav:3310" and "clamav 3310" are the same
// shape to the type system and only one of them can be dialled. Without a hook
// the second is accepted at save time and discovered at SCAN time, i.e. by the
// file that did not get scanned. Check is therefore where the meaning of the
// value lives, and Canonical is the only way this package writes one.
package dbsetting

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// StringSpec declares one text-valued setting.
type StringSpec struct {
	// Key is the settings-table key, dotted and namespaced by feature,
	// e.g. "antivirus.clamd_addr".
	Key string
	// EnvVar seeds the first-boot value. Empty means no seeding.
	EnvVar string
	// Default applies when there is no row, no seed, or an unusable value.
	// It must itself pass Check — a default that cannot be saved would make
	// an unreadable row unrecoverable without SQL.
	Default string
	// Normalize canonicalises raw text before it is checked, stored or
	// compared: lowercase a mode name, trim a host. Nil means "trim
	// surrounding whitespace", which every text setting wants and none wants
	// to restate.
	//
	// ⚠ It runs on the seed, on the write and on the read, so a row written
	// by hand in a different case still resolves to the same value.
	Normalize func(raw string) string
	// Check rejects a value that is the wrong shape. Nil accepts anything.
	// It is given the NORMALIZED text and must accept Default.
	//
	// ⚠ Return an error a person can act on — it is rendered in the admin
	// form, next to the field they just typed.
	Check func(v string) error
}

// SettingKey identifies the row this spec owns (Seeder).
func (s StringSpec) SettingKey() string { return s.Key }

// norm applies Normalize, defaulting to a whitespace trim.
func (s StringSpec) norm(raw string) string {
	if s.Normalize != nil {
		return s.Normalize(raw)
	}
	return strings.TrimSpace(raw)
}

// Validate is the write-side gate: an operator typing an unusable value must
// be told no at the moment they save it, in the UI, rather than having it
// discovered later by whatever tries to use it.
func (s StringSpec) Validate(raw string) error {
	_, err := s.Canonical(raw)
	return err
}

// Canonical normalises raw and validates it, returning the exact text to
// store. It is the ONLY shape this package writes: a handler that stores
// s.Canonical's output can never persist a value its own Resolve would then
// reject.
func (s StringSpec) Canonical(raw string) (string, error) {
	v := s.norm(raw)
	if s.Check != nil {
		if err := s.Check(v); err != nil {
			return "", err
		}
	}
	return v, nil
}

// Resolve reads the value in force.
//
// A missing row, a store error or a blank value resolve to Default. A stored
// value that fails Check ALSO resolves to Default — and logs, loudly, because
// that row is the one case here where what the table says and what is running
// differ. (It happens: a row written by hand, or one written before Check grew
// stricter.) This is the string analogue of IntSpec's clamp-on-read: the last
// line of defence, never the intended path, since Validate refuses the same
// value at the API.
func (s StringSpec) Resolve(ctx context.Context, g Getter) string {
	v, err := s.ResolveStrict(ctx, g)
	if err != nil {
		return s.Default
	}
	return v
}

// ResolveStrict is Resolve with the reason attached: it returns the stored
// text and, when that text fails Check, the error explaining why it is not in
// force. Resolve is the same call with the error swallowed.
//
// ⚠⚠ It exists because falling back to Default silently is fine for a value
// that HAS a sensible default and useless for one that does not. An empty
// clamd address means "not configured"; a stored address that will not parse
// means "you typed something wrong" — and collapsing the second into the first
// makes the admin page say "no address is configured" while the field beside
// it visibly contains one. A caller that has somewhere to SHOW the reason
// should ask for it.
func (s StringSpec) ResolveStrict(ctx context.Context, g Getter) (string, error) {
	if g == nil {
		return s.Default, nil
	}
	raw, err := g.GetSetting(ctx, s.Key)
	if err != nil {
		return s.Default, nil
	}
	v := s.norm(raw)
	if v == "" {
		return s.Default, nil
	}
	if s.Check != nil {
		if cerr := s.Check(v); cerr != nil {
			slog.Warn("stored setting is not usable; using default",
				slog.String("key", s.Key),
				slog.String("value", v),
				slog.String("err", cerr.Error()),
				slog.String("in_force", s.Default))
			return v, cerr
		}
	}
	return v, nil
}

// Seed writes the env var's value into the settings table when, and only when,
// no row exists yet. Call once at boot, before anything resolves the setting.
//
// ⚠ A seed that fails Check is IGNORED, not clamped — unlike IntSpec, which
// clamps an out-of-range seed to the nearest bound. There is no nearest legal
// string, so the alternatives are storing nonsense or storing nothing, and
// storing nothing leaves Default in force with a log line naming the variable.
func (s StringSpec) Seed(ctx context.Context, st Store) error {
	if st == nil || s.EnvVar == "" {
		return nil
	}
	raw := os.Getenv(s.EnvVar)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return s.seed(ctx, st, raw, s.EnvVar)
}

// SeedValue seeds an explicitly supplied value under the same first-boot-only
// rule, for a setting whose first value is DERIVED rather than typed — e.g.
// "an address was configured, so the mode is daemon". Keeping it here rather
// than letting callers Upsert directly is what stops a derived seed from
// overwriting a row an admin has since edited.
func (s StringSpec) SeedValue(ctx context.Context, st Store, raw string) error {
	if st == nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	return s.seed(ctx, st, raw, "derived")
}

func (s StringSpec) seed(ctx context.Context, st Store, raw, source string) error {
	if cur, err := st.GetSetting(ctx, s.Key); err == nil && cur != "" {
		slog.Debug("setting already stored; seed ignored",
			slog.String("key", s.Key), slog.String("source", source))
		return nil
	}
	v, err := s.Canonical(raw)
	if err != nil {
		slog.Warn("setting seed is not usable; ignored",
			slog.String("key", s.Key),
			slog.String("source", source),
			slog.String("value", raw),
			slog.String("err", err.Error()))
		return nil
	}
	if err := st.UpsertSetting(ctx, s.Key, v); err != nil {
		return fmt.Errorf("dbsetting: seed %s: %w", s.Key, err)
	}
	slog.Info("setting seeded",
		slog.String("key", s.Key), slog.String("source", source), slog.String("value", v))
	return nil
}

// OneOf builds a Check that accepts only the listed values, with an error
// message that lists them — the common case for a mode-style setting, and one
// worth having in one place so every such setting says no the same way.
func OneOf(allowed ...string) func(string) error {
	return func(v string) error {
		for _, a := range allowed {
			if v == a {
				return nil
			}
		}
		return fmt.Errorf("must be one of: %s", strings.Join(allowed, ", "))
	}
}
