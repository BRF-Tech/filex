package server

import (
	"context"
	"log/slog"
	"strconv"
	"sync"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/pluginlog"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
)

// Audit actions for an entry the storage could not answer for (issue #104),
// written when the entry's state CHANGES - not on every pass that gets the
// same answer.
const (
	AuditActionEntryUnavailable = "storage.entry_unavailable"
	AuditActionEntryAvailable   = "storage.entry_available"
)

// entryStateReporter is what the storage sync tells about an entry the storage
// could not answer for (syncpkg.EntryState):
//
//   - the storage plugin's log, which its admin page shows (a storage that is
//     not a plugin has no page; its lines reach the server log alone). The
//     log counts a repeated line instead of writing it again, and writes it to
//     the server log at most once every five minutes (internal/pluginlog) -
//     the sync asks again on every pass, and every answer is the same;
//   - the audit trail, when the entry's state changed.
func entryStateReporter(store db.Store, plugins *plugin.Manager) func(ctx context.Context, e syncpkg.EntryState) {
	var mu sync.Mutex
	builtin := map[int64]*pluginlog.Log{}
	logFor := func(st *model.Storage) *pluginlog.Log {
		if l := plugins.LogFor(st.Driver); l != nil {
			return l
		}
		mu.Lock()
		defer mu.Unlock()
		l, ok := builtin[st.ID]
		if !ok {
			l = pluginlog.New().Mirror(slog.Default(), slog.String("storage", st.Name))
			builtin[st.ID] = l
		}
		return l
	}
	return func(ctx context.Context, e syncpkg.EntryState) {
		if e.Storage == nil {
			return
		}
		where := "storage " + e.Storage.Name
		if e.Reason != "" {
			logFor(e.Storage).Add("warn", pluginlog.Join(where, e.Path,
				"the storage could not say whether this entry still exists, so it is shown as unavailable", e.Reason))
		} else if e.Changed {
			logFor(e.Storage).Add("info", pluginlog.Join(where, e.Path,
				"the storage answers for this entry again; it is available"))
		}
		if !e.Changed {
			return
		}
		action := AuditActionEntryUnavailable
		meta := map[string]any{"storage": e.Storage.Name, "path": e.Path, "kind": string(e.Kind)}
		if e.Reason == "" {
			action = AuditActionEntryAvailable
		} else {
			meta["reason"] = e.Reason
		}
		if err := store.InsertAuditEntry(context.WithoutCancel(ctx), &model.AuditEntry{
			Action: action, TargetType: "storage", TargetID: strconv.FormatInt(e.Storage.ID, 10), Metadata: meta,
		}); err != nil {
			slog.Warn("sync: audit an entry's availability", slog.String("storage", e.Storage.Name),
				slog.String("path", e.Path), slog.String("err", err.Error()))
		}
	}
}
