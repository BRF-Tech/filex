// Package dbsetting — bool.go
//
// The switch-shaped member of the family. Same four behaviours as IntSpec
// (resolution order, seed-on-first-boot-only, fall back on read, refuse on
// write), minus the one that has no meaning for a boolean: there is no range
// to clamp to, because every parseable value is already legal.
//
// ⚠ That is the whole reason a BoolSpec is not "an IntSpec with Min 0 Max 1".
// A number out of range has a nearest legal neighbour and clamping to it keeps
// the operator's intent; an unparseable switch has none, and guessing which
// way an operator meant a typo'd kill-switch to point is exactly the kind of
// decision software must not make. Unparseable text therefore resolves to
// Default and says so in the log.
package dbsetting

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// BoolSpec declares one boolean-valued setting: a switch an admin flips.
type BoolSpec struct {
	// Key is the settings-table key, dotted and namespaced by feature,
	// e.g. "antivirus.enabled".
	Key string
	// EnvVar seeds the first-boot value. Empty means no seeding.
	EnvVar string
	// Default applies when there is no row, no seed, or an unreadable value.
	Default bool
}

// SettingKey identifies the row this spec owns (Seeder).
func (s BoolSpec) SettingKey() string { return s.Key }

// ParseBool reads the spellings an operator actually types into a compose
// file, in either case: 1/true/t/yes/y/on and 0/false/f/no/n/off. ok is false
// for anything else — including the empty string, which is "not set", not
// "false".
//
// ⚠ It is deliberately wider than strconv.ParseBool (which rejects yes/no/on/
// off) and deliberately not wider still: a value like "enabled" or "disable"
// is refused rather than guessed, so a typo shows up as a log line naming the
// key instead of as a switch silently pointing the wrong way.
func ParseBool(raw string) (value bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on":
		return true, true
	case "0", "false", "f", "no", "n", "off":
		return false, true
	}
	return false, false
}

// FormatBool is the single spelling this package ever WRITES, so a row can be
// compared as text and a human reading the settings table sees one form.
func FormatBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// Validate is the write-side gate, kept for symmetry with IntSpec.Validate so
// a handler treats every spec the same way. It cannot fail: by the time a
// caller holds a Go bool the only two values that exist are both legal. The
// gate that matters for a switch is the JSON decoder, which refuses "maybe"
// before this is ever reached.
func (s BoolSpec) Validate(bool) error { return nil }

// Resolve reads the value in force. Call it at the point of USE, like every
// spec in this package.
//
// A missing row, a store error, a blank value or unparseable text all resolve
// to Default: none of them carries operator intent.
func (s BoolSpec) Resolve(ctx context.Context, g Getter) bool {
	if g == nil {
		return s.Default
	}
	raw, err := g.GetSetting(ctx, s.Key)
	if err != nil || strings.TrimSpace(raw) == "" {
		return s.Default
	}
	v, ok := ParseBool(raw)
	if !ok {
		slog.Warn("setting is not a boolean; using default",
			slog.String("key", s.Key),
			slog.String("value", raw),
			slog.Bool("in_force", s.Default))
		return s.Default
	}
	return v
}

// Seed writes the env var's value into the settings table when, and only when,
// no row exists yet. Call once at boot, before anything resolves the setting.
//
// Unparseable text is ignored entirely — there is no honest value to store —
// and Default applies. Returns nil when there is nothing to do, which is the
// normal case on every boot after the first.
func (s BoolSpec) Seed(ctx context.Context, st Store) error {
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
	v, ok := ParseBool(raw)
	if !ok {
		slog.Warn("setting seed is not a boolean; ignored",
			slog.String("key", s.Key),
			slog.String("env", s.EnvVar),
			slog.String("value", raw))
		return nil
	}
	if err := st.UpsertSetting(ctx, s.Key, FormatBool(v)); err != nil {
		return fmt.Errorf("dbsetting: seed %s: %w", s.Key, err)
	}
	slog.Info("setting seeded from environment",
		slog.String("key", s.Key), slog.String("env", s.EnvVar), slog.Bool("value", v))
	return nil
}
