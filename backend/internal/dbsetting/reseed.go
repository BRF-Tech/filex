// Package dbsetting — reseed.go
//
// The explicit way back to the environment (task #258).
//
// Since 0.57 every setting a variable gives a first value to follows the one
// rule of this package: the variable SEEDS the row on a start that has none,
// and from then on the row - what an administrator saved - wins. An operator
// who edits the variable in compose and restarts therefore changes nothing,
// which is right for a value someone may have changed on the page since, and
// surprising for one nobody touched. Reseed is the deliberate answer: it
// writes the variable's value over the row now, under the same parse, clamp
// and check a first-start seed uses (`filex settings reseed`).
package dbsetting

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// envVarOf names the variable a spec seeds from ("" when it has none).
func envVarOf(s Seeder) string {
	switch v := s.(type) {
	case IntSpec:
		return v.EnvVar
	case *IntSpec:
		return v.EnvVar
	case BoolSpec:
		return v.EnvVar
	case *BoolSpec:
		return v.EnvVar
	case StringSpec:
		return v.EnvVar
	case *StringSpec:
		return v.EnvVar
	}
	return ""
}

// Reseed writes each spec's environment value over its stored row, for the
// specs whose variable is set; a spec whose variable is empty is left as it
// is. keys narrows it to those settings (empty: every spec given). A value the
// seed refuses (not a number, not a switch, a text its Check rejects) leaves
// the stored row as it was, and says so in the log. It answers the keys it
// wrote.
func Reseed(ctx context.Context, st Store, keys []string, specs ...Seeder) []string {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var done []string
	for _, s := range specs {
		key := s.SettingKey()
		if len(want) > 0 && !want[key] {
			continue
		}
		env := envVarOf(s)
		if env == "" || strings.TrimSpace(os.Getenv(env)) == "" {
			continue
		}
		before, _ := st.GetSetting(ctx, key)
		// The seed writes only into an empty row: empty it, seed, and put the
		// old value back when the seed refused the variable's.
		if err := st.UpsertSetting(ctx, key, ""); err != nil {
			slog.Warn("dbsetting: reseed failed", slog.String("key", key), slog.String("err", err.Error()))
			continue
		}
		if err := s.Seed(ctx, st); err != nil {
			slog.Warn("dbsetting: reseed failed", slog.String("key", key), slog.String("err", err.Error()))
		}
		if after, _ := st.GetSetting(ctx, key); after == "" {
			_ = st.UpsertSetting(ctx, key, before)
			slog.Warn("dbsetting: the environment's value was not usable; the stored one stands",
				slog.String("key", key), slog.String("env", env))
			continue
		}
		done = append(done, key)
	}
	return done
}
