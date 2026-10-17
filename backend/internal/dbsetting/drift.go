// Package dbsetting — drift.go
//
// The variable that no longer does anything, said once at start (task #258).
//
// A seeded variable is inert once its row exists (the package rule). An
// operator who edits it in compose and restarts sees nothing change, and
// nothing told them why. WarnDrift names it in the log at start: the
// variable is set, it says something other than the stored row, and the
// way to apply it is `filex settings reseed <key>`.
//
// What the variable "says" is the text a first start would store - the same
// parse, clamp and canonical form Seed uses (seedText) - so "30" and "30 "
// or an out-of-range number clamped to the stored bound are not a drift.
//
// ⚠ The log carries the key and the variable's name, never either value: a
// text setting may hold an address or a path an operator would not want
// repeated in every start's log.
package dbsetting

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Drift is one configured value the stored row overrules.
type Drift struct {
	// Key is the setting; Source names where the configured value came from
	// (the variable, or "config.yaml <key>").
	Key, Source string
}

// seedText answers the text Seed would store for raw, and whether raw is
// usable at all.
func seedText(s Seeder, raw string) (string, bool) {
	switch v := s.(type) {
	case IntSpec:
		return v.seedText(raw)
	case *IntSpec:
		return v.seedText(raw)
	case BoolSpec:
		return v.seedText(raw)
	case *BoolSpec:
		return v.seedText(raw)
	case StringSpec:
		return v.seedText(raw)
	case *StringSpec:
		return v.seedText(raw)
	}
	return "", false
}

func (s IntSpec) seedText(raw string) (string, bool) {
	n, ok := s.parseSeed(raw)
	if !ok {
		return "", false
	}
	v, _ := s.Clamp(n)
	return strconv.Itoa(v), true
}

func (s BoolSpec) seedText(raw string) (string, bool) {
	v, ok := ParseBool(raw)
	if !ok {
		return "", false
	}
	return FormatBool(v), true
}

func (s StringSpec) seedText(raw string) (string, bool) {
	v, err := s.Canonical(raw)
	return v, err == nil && v != ""
}

// Drifted reports whether a configured value (already in the stored text's
// canonical form) is overruled by the row: the row holds something, and
// something else.
func Drifted(configured, stored string) bool {
	return strings.TrimSpace(stored) != "" && configured != stored
}

// WarnStoredWins is the one line a start logs for a drift.
func WarnStoredWins(d Drift) {
	slog.Warn(d.Source+" is set but the stored value wins; run `filex settings reseed "+d.Key+"` to apply it",
		slog.String("key", d.Key), slog.String("source", d.Source))
}

// DriftOf answers the specs whose variable is set to a usable value that the
// stored row overrules. Call it after SeedAll: a row the seed just wrote
// holds the variable's own value and is not a drift.
func DriftOf(ctx context.Context, g Getter, specs ...Seeder) []Drift {
	var out []Drift
	for _, s := range specs {
		env := envVarOf(s)
		if env == "" {
			continue
		}
		raw := os.Getenv(env)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		want, ok := seedText(s, raw)
		if !ok {
			continue
		}
		cur, err := g.GetSetting(ctx, s.SettingKey())
		if err != nil {
			continue
		}
		if Drifted(want, cur) {
			out = append(out, Drift{Key: s.SettingKey(), Source: env})
		}
	}
	return out
}

// WarnDrift logs every drift DriftOf finds - once, at start - and answers
// them.
func WarnDrift(ctx context.Context, g Getter, specs ...Seeder) []Drift {
	out := DriftOf(ctx, g, specs...)
	for _, d := range out {
		WarnStoredWins(d)
	}
	return out
}
