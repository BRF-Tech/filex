package replica

import (
	"context"
	"log/slog"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// NewRulesEngine wires storage.NewRulesEngine with a reload function
// that pulls from db.Store. Call .Reload() after admin CRUD ops.
func NewRulesEngine(store db.Store) (storage.RuleEngine, *RulesReloader) {
	rr := &RulesReloader{store: store}
	eng := storage.NewRulesEngine(rr.fetch)
	rr.engine = eng
	return eng, rr
}

// RulesReloader is the helper an admin handler calls after rules CRUD.
type RulesReloader struct {
	store  db.Store
	engine storage.RuleEngine
}

// Reload pulls the latest rules + settings from DB and refreshes the
// engine's cache.
func (r *RulesReloader) Reload(ctx context.Context) error {
	if reloader, ok := r.engine.(storage.Reloader); ok {
		return reloader.Reload()
	}
	return nil
}

// fetch is the closure handed to storage.NewRulesEngine — pulls the
// latest rules and the settings.default_mode for the catch-all.
func (r *RulesReloader) fetch() ([]storage.RuleSpec, storage.ReplicaMode) {
	ctx, cancel := contextWithTimeoutShort()
	defer cancel()
	rules, err := r.store.ListReplicaRules(ctx)
	if err != nil {
		slog.Warn("replica rules: list failed; using empty cache", slog.String("err", err.Error()))
		return nil, storage.ModeMirror
	}
	out := make([]storage.RuleSpec, 0, len(rules))
	for _, m := range rules {
		out = append(out, storage.RuleSpec{
			ID:       m.ID,
			Pattern:  m.PathPattern,
			Mode:     storage.ReplicaMode(m.Mode),
			Priority: m.Priority,
			Enabled:  m.Enabled,
		})
	}
	defMode := storage.ModeMirror
	st, err := r.store.GetReplicaSettings(ctx)
	if err == nil && st != nil && st.DefaultMode != "" {
		defMode = storage.ReplicaMode(st.DefaultMode)
	}
	return out, defMode
}
