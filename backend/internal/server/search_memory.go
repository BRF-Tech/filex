package server

import (
	"log/slog"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/sysmem"
)

// setSearchMemoryBudget gives the search index its share of the memory filex
// was given (issue #115): percent of the container's limit, or of the
// machine's RAM, clamped to [config.MinMemoryPercent, config.MaxMemoryPercent].
// Over it, content extraction and a rebuild wait (search/content_batch.go).
//
// The line it logs names the figure and where it came from, so an operator
// who set a container limit can see that filex read it.
func setSearchMemoryBudget(idx *search.Index, percent int) {
	if percent == 0 {
		percent = config.DefaultMemoryPercent
	}
	clamped := sysmem.Clamp(percent, config.MinMemoryPercent, config.MaxMemoryPercent)
	given := sysmem.Allocated()
	budget := given.Share(clamped, config.MinMemoryPercent, config.MaxMemoryPercent)
	if budget == 0 {
		slog.Warn("search: the memory filex was given could not be read; indexing runs without a memory budget",
			slog.String("source", given.Source))
		return
	}
	idx.SetMemoryBudget(budget)
	attrs := []any{
		slog.Int("percent", clamped),
		slog.Uint64("budget_mb", budget>>20),
		slog.Uint64("given_mb", given.Bytes>>20),
		slog.String("from", given.Source),
	}
	if clamped != percent {
		attrs = append(attrs, slog.Int("asked", percent))
	}
	slog.Info("search: indexing memory budget", attrs...)
}
