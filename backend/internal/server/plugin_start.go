package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/plugin"
)

// startStoragePlugins starts every enabled storage plugin the database names
// - AFTER every license hold is in place (#215, sec055 S12).
//
// ⚠⚠ The order is the point. The plugins used to be loaded (started, and
// waited for) before the app store was even built, and a paid plugin's hold
// arrived only with the store's ApplyHolds a moment later: every restart ran
// an unlicensed plugin - and opened its storages - until then, and with the
// app store off (no app runtime) the hold never came at all. Now:
//
//   - with the app store on, svc.Start (server.go, before this) has already
//     judged every license from its kept rows and held what does not hold;
//   - with it off, nobody can check a license, so every storage plugin a
//     store's license names starts held (appstore.HoldReasonStoreOff).
//
// The build gate (appstore.StorageBuildGate) is wired here too: a build a
// store signed is taken only through that store's link, or once the store
// says the version is still listed.
func startStoragePlugins(ctx context.Context, m *plugin.Manager, st appstore.StateStore, svc *appstore.Service) {
	if m == nil {
		return
	}
	m.SetBuildGate(appstore.StorageBuildGate(st, svc))
	if svc == nil {
		holdStorageWithoutStore(ctx, st, m)
	}
	if err := m.Load(ctx); err != nil {
		// A broken plugin must not stop the server: the whole point of
		// running them out of process is that they cannot take filex
		// down. The failure is logged and visible in the admin list.
		slog.Warn("plugins: load failed; continuing without them", slog.Any("err", err))
		return
	}
	m.WaitReady(10 * time.Second)
}

// holdStorageWithoutStore holds every storage plugin a store's license
// names, on a server whose app store is off.
func holdStorageWithoutStore(ctx context.Context, st appstore.StateStore, h holdSetter) {
	names, err := appstore.StorageLicensed(ctx, st)
	if err != nil {
		slog.Warn("plugins: the store licenses could not be read; storage plugins under one are not held", slog.Any("err", err))
		return
	}
	for _, name := range names {
		h.SetLicenseHold(name, appstore.HoldReasonStoreOff)
	}
	if len(names) > 0 {
		slog.Warn("plugins: the app store is off, so the storage plugins under a store's license are held", slog.Any("plugins", names))
	}
}
