// Package search — settings.go
//
// The search behaviours an administrator can change, as settings-table rows
// (task #261): db → env → default, the internal/dbsetting rule every other
// admin-edited setting follows.
//
//	key                     env (first-boot seed)          default  live?
//	search.content          FILEX_SEARCH_CONTENT           true     yes - the next write is or is not extracted
//	search.content_max_mb   FILEX_SEARCH_CONTENT_MAX (B)   5        yes - the next eligibility check
//	search.memory_percent   FILEX_SEARCH_MEMORY_PERCENT    25       yes - applied the moment it is saved
//	search.auto_rebuild     FILEX_SEARCH_AUTO_REBUILD      true     at the next start (it is a startup action)
//
// ⚠⚠ The variables are SEEDS, not overrides (internal/dbsetting): consumed on
// a boot where no row exists yet - for an upgrading install, the first boot of
// 0.57 - and inert after that. An install that ran with FILEX_SEARCH_CONTENT=0
// keeps content search off across the upgrade, and from then on the switch is
// on the Search page, not in compose. The same keys in config.yaml
// (`search.content`, `search.content_max_bytes`, `search.memory_percent`,
// `search.auto_rebuild`) seed the same way when their variable is not set
// (SeedSettings), so a YAML-configured install does not lose its values either.
//
// What stays in the environment, deliberately: FILEX_SEARCH_ENABLED (whether
// the index is opened at all - a process that never opened it cannot switch it
// on from a page) and `search.index_path` (where the index lives on disk).
//
// ⚠ The memory bounds and default are config's (config.DefaultMemoryPercent
// and friends): config.go still reads the variable for the seed, and one set
// of numbers serves both.
package search

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/dbsetting"
	"github.com/brf-tech/filex/backend/internal/sysmem"
)

// bytesPerMB is the binary megabyte the rest of filex uses.
const bytesPerMB = 1 << 20

// The bounds and default of ContentMaxSetting, in megabytes. 5 is the 5 MiB
// FILEX_SEARCH_CONTENT_MAX always defaulted to. The ceiling exists because the
// extracted TEXT is capped at 200 KiB anyway (extract.DefaultLimit): a
// gigabyte source is read to its end to yield those 200 KiB, and a larger cap
// mostly buys longer extraction jobs, not more search.
const (
	DefaultContentMaxMB = 5
	MinContentMaxMB     = 1
	MaxContentMaxMB     = 1024
)

// ContentSetting is the content-search switch: whether a file's text is
// extracted into the index when it is written. Off, nothing new is enqueued
// and a queued job that comes up does nothing; text already in the index
// keeps matching. Switched back on, files are extracted as they change - a
// rebuild with content re-extraction catches up the rest.
var ContentSetting = dbsetting.BoolSpec{
	Key:     "search.content",
	EnvVar:  "FILEX_SEARCH_CONTENT",
	Default: true,
}

// ContentMaxSetting is the largest SOURCE file whose text is extracted.
//
// ⚠⚠ The variable is FILEX_SEARCH_CONTENT_MAX and it is in BYTES; the row is
// in MEGABYTES, the number an admin types. SeedParse converts, rounding UP,
// so a deployment's cap can only stay or grow by less than a megabyte -
// rounding down would quietly stop extracting files that used to be.
var ContentMaxSetting = dbsetting.IntSpec{
	Key:       "search.content_max_mb",
	EnvVar:    "FILEX_SEARCH_CONTENT_MAX",
	Default:   DefaultContentMaxMB,
	Min:       MinContentMaxMB,
	Max:       MaxContentMaxMB,
	Unit:      "MB",
	SeedParse: bytesToMB,
}

// MemoryPercentSetting is the share of the memory filex was given (the
// container's limit, or the machine's RAM) that indexing may take on top of
// what filex held when the budget was first set (issue #115). Over it,
// content extraction and a rebuild wait for the index to give memory back.
var MemoryPercentSetting = dbsetting.IntSpec{
	Key:       "search.memory_percent",
	EnvVar:    "FILEX_SEARCH_MEMORY_PERCENT",
	Default:   config.DefaultMemoryPercent,
	Min:       config.MinMemoryPercent,
	Max:       config.MaxMemoryPercent,
	Unit:      "percent",
	SeedParse: parsePercent,
}

// AutoRebuildSetting lets filex repair an index written by an older document
// schema, in the background, at startup. Read once per start: it decides
// what the start does, so a change applies at the next one.
var AutoRebuildSetting = dbsetting.BoolSpec{
	Key:     "search.auto_rebuild",
	EnvVar:  "FILEX_SEARCH_AUTO_REBUILD",
	Default: true,
}

// Settings is the family, in the order it is seeded.
func Settings() []dbsetting.Seeder {
	return []dbsetting.Seeder{ContentSetting, ContentMaxSetting, MemoryPercentSetting, AutoRebuildSetting}
}

// SeedSettings writes the first values of the search settings, on a boot
// where their rows do not exist yet: from the environment first, then from
// config.yaml for a setting whose variable is not set. c is the search block
// filex was configured with (config.Load: YAML, then the environment on top).
//
// ⚠ The YAML value is passed on only when it differs from the default: a row
// holding the default pins it (dbsetting seedvalue.go). And only when the
// variable is unset - when it is set, the environment's seed above owned the
// decision, including the decision to ignore a value it could not read.
func SeedSettings(ctx context.Context, st dbsetting.Store, c config.SearchConfig) {
	dbsetting.SeedAll(ctx, st, Settings()...)
	const yaml = "config.yaml search."
	unset := func(env string) bool { return os.Getenv(env) == "" }
	seeded := func(key string, err error) {
		if err != nil {
			slog.Warn("search: seeding a setting from config.yaml failed",
				slog.String("key", key), slog.String("err", err.Error()))
		}
	}
	if unset(ContentSetting.EnvVar) && c.Content != ContentSetting.Default {
		seeded(ContentSetting.Key, ContentSetting.SeedValue(ctx, st, c.Content, yaml+"content"))
	}
	if unset(ContentMaxSetting.EnvVar) && c.ContentMaxBytes > 0 {
		if mb := ceilMB(c.ContentMaxBytes); mb != ContentMaxSetting.Default {
			seeded(ContentMaxSetting.Key, ContentMaxSetting.SeedValue(ctx, st, mb, yaml+"content_max_bytes"))
		}
	}
	if unset(MemoryPercentSetting.EnvVar) && c.MemoryPercent != 0 && c.MemoryPercent != MemoryPercentSetting.Default {
		seeded(MemoryPercentSetting.Key, MemoryPercentSetting.SeedValue(ctx, st, c.MemoryPercent, yaml+"memory_percent"))
	}
	if unset(AutoRebuildSetting.EnvVar) && c.AutoRebuild != AutoRebuildSetting.Default {
		seeded(AutoRebuildSetting.Key, AutoRebuildSetting.SeedValue(ctx, st, c.AutoRebuild, yaml+"auto_rebuild"))
	}
}

// ContentMaxBytes is the extraction size cap in force right now, in bytes.
// Read at the point of use (queue.ContentIndexer), so a change on the Search
// page applies to the next file, not the next restart.
func ContentMaxBytes(ctx context.Context, g dbsetting.Getter) int64 {
	return int64(ContentMaxSetting.Resolve(ctx, g)) * bytesPerMB
}

// bytesToMB reads FILEX_SEARCH_CONTENT_MAX (bytes) as megabytes, rounding up.
func bytesToMB(raw string) (int, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return ceilMB(n), true
}

// ceilMB is n bytes in whole megabytes, never fewer than it takes to hold n.
func ceilMB(n int64) int {
	return int((n + bytesPerMB - 1) / bytesPerMB)
}

// parsePercent reads FILEX_SEARCH_MEMORY_PERCENT as an operator writes it:
// "25", " 30% ". It is the spelling config.go has accepted since 0.56.3.
func parsePercent(raw string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "%")))
	return n, err == nil
}

// MemoryBudget is the indexing memory budget in force, as the Search page
// shows it: the percent it was set from, what that came to, and the memory it
// is a share of (and where filex read that figure).
type MemoryBudget struct {
	Percent    int
	Bytes      uint64
	GivenBytes uint64
	From       string
}

// budgetNote remembers the last ApplyMemoryPercent of an index.
type budgetNote struct {
	mu   sync.Mutex
	last MemoryBudget
}

// ApplyMemoryPercent gives the index its share of the memory filex was given
// (issue #115): percent of the container's limit, or of the machine's RAM,
// clamped to MemoryPercentSetting's bounds (0 = the default). It is called at
// startup with the stored setting and again by the Search page the moment the
// setting is saved, so a change needs no restart.
//
// The line it logs names the figure and where it came from, so an operator
// who set a container limit can see that filex read it.
func (i *Index) ApplyMemoryPercent(percent int) MemoryBudget {
	if percent == 0 {
		percent = MemoryPercentSetting.Default
	}
	s := MemoryPercentSetting
	clamped := sysmem.Clamp(percent, s.Min, s.Max)
	given := sysmem.Allocated()
	out := MemoryBudget{Percent: clamped, GivenBytes: given.Bytes, From: given.Source}
	out.Bytes = given.Share(clamped, s.Min, s.Max)
	if out.Bytes == 0 {
		slog.Warn("search: the memory filex was given could not be read; indexing runs without a memory budget",
			slog.String("source", given.Source))
		i.SetMemoryBudget(0)
	} else {
		i.SetMemoryBudget(out.Bytes)
		attrs := []any{
			slog.Int("percent", clamped),
			slog.Uint64("budget_mb", out.Bytes>>20),
			slog.Uint64("given_mb", given.Bytes>>20),
			slog.String("from", given.Source),
		}
		if clamped != percent {
			attrs = append(attrs, slog.Int("asked", percent))
		}
		slog.Info("search: indexing memory budget", attrs...)
	}
	i.budget.mu.Lock()
	i.budget.last = out
	i.budget.mu.Unlock()
	return out
}

// AppliedMemoryBudget is what the last ApplyMemoryPercent set (zero before
// any call - an index whose server never set a budget).
func (i *Index) AppliedMemoryBudget() MemoryBudget {
	i.budget.mu.Lock()
	defer i.budget.mu.Unlock()
	return i.budget.last
}
