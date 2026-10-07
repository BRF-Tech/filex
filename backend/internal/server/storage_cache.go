package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/replica"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// storageCache is the storage resolver behind every API handler, the ops
// worker, trash, versions, the protocol servers and the thumbnail pipeline:
// one live driver per storage, built from the storage's row on first use and
// kept until the row changes (forget).
//
// ⚠⚠ A storage LINKED to an enabled replication target is handed out wrapped
// (storage.NewReplicated, with the primary's own optional interfaces kept -
// ReplicatedDriver.Shaped): every write, delete, move and copy that goes
// through the resolver fans out to the target, and a read the primary fails
// falls back to it. Until 0.53 the resolver built the bare driver whatever the
// row said, so a linked storage replicated nothing, wrote no failure and sent
// no notification - for every version since the feature was added (#186,
// GitHub Discussion #91).
//
// The sync worker opens drivers of its own and only reads, so it never needs
// the wrapper.
type storageCache struct {
	// ctx is the server's: the drivers are initialised on it.
	ctx   context.Context
	store db.Store
	// rules is the replica rule engine every wrapper consults (the one the
	// admin endpoints reload).
	rules storage.RuleEngine
	// notifier names the storage in what a wrapper sends; its service is read
	// when something is sent (the server makes it after the storages).
	notifier *replica.Notifier
	// attach hands each driver built to the thumbnail pipeline.
	attach func(id int64, d storage.Driver)
	// open builds and initialises a driver (tests replace it).
	open func(ctx context.Context, driver string, configJSON []byte) (storage.Driver, error)

	mu      sync.RWMutex
	entries map[int64]cacheEntry
}

// cacheEntry is a storage's live driver and the target its wrapper fans out
// to (0: not wrapped).
type cacheEntry struct {
	drv      storage.Driver
	targetID int64
}

func newStorageCache(ctx context.Context, store db.Store, rules storage.RuleEngine, notifier *replica.Notifier, attach func(int64, storage.Driver)) *storageCache {
	if rules == nil {
		rules = storage.DefaultRules()
	}
	return &storageCache{
		ctx:      ctx,
		store:    store,
		rules:    rules,
		notifier: notifier,
		attach:   attach,
		open:     openDriver,
		entries:  map[int64]cacheEntry{},
	}
}

// openDriver builds a registered driver and initialises it with a row's
// configuration.
func openDriver(ctx context.Context, driver string, configJSON []byte) (storage.Driver, error) {
	drv, err := storage.Get(driver)
	if err != nil {
		return nil, err
	}
	cfg := map[string]any{}
	if len(configJSON) > 0 {
		_ = jsonDecode(configJSON, &cfg)
	}
	if err := drv.Init(ctx, cfg); err != nil {
		return nil, err
	}
	return drv, nil
}

// resolve is the resolver: the storage's live driver.
func (c *storageCache) resolve(id int64) (storage.Driver, error) {
	c.mu.RLock()
	e, ok := c.entries[id]
	c.mu.RUnlock()
	if ok {
		return e.drv, nil
	}
	built, err := c.build(id)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if e, ok := c.entries[id]; ok {
		// Another request built it at the same time: theirs stands, ours goes.
		c.mu.Unlock()
		storage.CloseDriver(built.drv)
		return e.drv, nil
	}
	c.entries[id] = built
	c.mu.Unlock()
	if c.attach != nil {
		c.attach(id, built.drv)
	}
	return built.drv, nil
}

// build makes a storage's driver from its row, wrapped when the row is linked
// to a replication target that exists and is enabled.
func (c *storageCache) build(id int64) (cacheEntry, error) {
	st, err := c.store.GetStorage(c.ctx, id)
	if err != nil {
		return cacheEntry{}, err
	}
	primary, err := c.open(c.ctx, st.Driver, st.ConfigJSON)
	if err != nil {
		return cacheEntry{}, err
	}
	if st.ReplicaTargetID == nil {
		return cacheEntry{drv: primary}, nil
	}
	t, err := c.store.GetReplicationTarget(c.ctx, *st.ReplicaTargetID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			slog.Warn("replica: storage is linked to a replication target that does not exist; not replicating",
				slog.String("storage", st.Name), slog.Int64("target_id", *st.ReplicaTargetID))
			return cacheEntry{drv: primary}, nil
		}
		// Not knowing is not "no target": a storage that should replicate
		// must not quietly stop because one lookup failed.
		storage.CloseDriver(primary)
		return cacheEntry{}, fmt.Errorf("storage %d: replication target: %w", id, err)
	}
	if !t.Enabled {
		return cacheEntry{drv: primary}, nil
	}
	var target storage.Driver
	target, err = c.open(c.ctx, t.Driver, t.ConfigJSON)
	if err != nil {
		// The storage keeps working; every change that would reach the
		// backup becomes a REPLICA_UNAVAILABLE failure saying why.
		slog.Warn("replica: replication target could not be opened; its changes are recorded as failures",
			slog.String("storage", st.Name), slog.String("target", t.Name), slog.String("err", err.Error()))
		target = storage.UnavailableDriver(t.Driver,
			fmt.Errorf("replication target %q could not be opened: %w", t.Name, err))
	}
	// The storage's own folder on the target: chosen and kept the first time
	// (internal/replica folder.go). Not knowing it is not "the root": writing
	// there could land on another storage's files.
	folder, err := replica.EnsureFolder(c.ctx, c.store, st, t.ID)
	if err != nil {
		storage.CloseDriver(primary)
		storage.CloseDriver(target)
		return cacheEntry{}, fmt.Errorf("storage %d: replication folder: %w", id, err)
	}
	var notifier storage.EventNotifier
	if c.notifier != nil {
		notifier = c.notifier.ForStorage(st.ID, st.Name)
	}
	rd := storage.NewReplicated(primary, target, c.rules, replica.NewFailureRecorder(c.store, st.ID), notifier).
		WithFolder(folder)
	return cacheEntry{drv: rd.Shaped(), targetID: t.ID}, nil
}

// forget drops a storage's cached driver so the next resolve rebuilds it from
// the row as it now stands. The cache is keyed by storage id and never
// expires, which is right for a hot path and wrong the moment an operator
// edits the storage: without this, the endpoint, bucket and credentials a
// driver was built with outlive the admin page that changed them, and only a
// restart applies the fix (issue #21). The same holds for its replication
// link: linking, unlinking and editing the target all go through here.
//
// Drivers that hold a connection (sftp, ftp, smb) close it rather than leak
// one per edit, and a storage plugin's driver releases the instance holding
// the storage's credentials. A replication wrapper is retired at once (a
// change still arriving through it is recorded for Fix all instead of going
// to a target being closed) and closes both of its drivers once the fan-outs
// in flight are over.
func (c *storageCache) forget(id int64) {
	c.mu.Lock()
	e, ok := c.entries[id]
	delete(c.entries, id)
	c.mu.Unlock()
	if !ok {
		return
	}
	storage.CloseDriver(e.drv)
}

// onTarget lists the cached storages whose wrapper fans out to targetID.
func (c *storageCache) onTarget(targetID int64) []int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []int64
	for id, e := range c.entries {
		if e.targetID == targetID {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Replicated implements replica.Wrappers: the storage's live wrapper, through
// the same cache every writer uses.
func (c *storageCache) Replicated(ctx context.Context, storageID int64) (replica.Link, error) {
	st, err := c.store.GetStorage(ctx, storageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return replica.Link{}, replica.ErrNotLinked
		}
		return replica.Link{}, err
	}
	if st == nil || st.ReplicaTargetID == nil {
		return replica.Link{}, replica.ErrNotLinked
	}
	drv, err := c.resolve(storageID)
	if err != nil {
		return replica.Link{}, err
	}
	rd, ok := storage.AsReplicated(drv)
	if !ok {
		return replica.Link{}, replica.ErrNotLinked
	}
	c.mu.RLock()
	targetID := c.entries[storageID].targetID
	c.mu.RUnlock()
	return replica.Link{StorageID: storageID, TargetID: targetID, Driver: rd}, nil
}

// stopAll retires every wrapper and waits, up to grace, for the fan-outs in
// flight - at shutdown, before the database they record into is closed.
func (c *storageCache) stopAll(grace time.Duration) {
	c.mu.RLock()
	var wrappers []*storage.ReplicatedDriver
	for _, e := range c.entries {
		if rd, ok := storage.AsReplicated(e.drv); ok {
			wrappers = append(wrappers, rd)
		}
	}
	c.mu.RUnlock()
	if len(wrappers) == 0 {
		return
	}
	done := make(chan struct{})
	go func() {
		for _, rd := range wrappers {
			rd.Stop()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(grace):
		slog.Warn("replica: fan-outs still running at shutdown; the next start does not redo them - Fix all replays what was recorded",
			slog.Int("wrappers", len(wrappers)))
	}
}

// replicaLinks is what the admin handlers tell the running process about
// replication (handlers.ReplicaLinks): the drivers are rebuilt around the new
// pairing and the initial copy is started, restarted or dropped.
type replicaLinks struct {
	cache *storageCache
	svc   *replica.Service
}

// StorageLinkChanged: a storage was linked, relinked, unlinked or deleted.
// Its driver is rebuilt (the caller's ForgetStorage) and its initial copy
// follows the row.
func (l replicaLinks) StorageLinkChanged(ctx context.Context, storageID int64) {
	if l.svc == nil {
		return
	}
	if err := l.svc.LinkChanged(context.WithoutCancel(ctx), storageID); err != nil {
		slog.Warn("replica: initial copy not updated after a link change",
			slog.Int64("storage_id", storageID), slog.String("err", err.Error()))
	}
}

// FolderChanged: the storage's folder on its target was changed (PUT
// /api/admin/replica/links/{id}). Its driver is rebuilt to write into the new
// folder, and its initial copy begins again there.
func (l replicaLinks) FolderChanged(ctx context.Context, storageID int64) {
	if l.cache != nil {
		l.cache.forget(storageID)
	}
	if l.svc == nil {
		return
	}
	if err := l.svc.RestartInitialCopy(context.WithoutCancel(ctx), storageID); err != nil {
		slog.Warn("replica: initial copy not restarted after a folder change",
			slog.Int64("storage_id", storageID), slog.String("err", err.Error()))
	}
}

// TargetChanged: a replication target was edited or deleted. Every storage
// that pointed at it - storageIDs, read by the handler before a delete
// cleared the links, and whatever the cache holds wrapped for it - gets its
// driver rebuilt; with restartCopies (another driver or configuration, or
// switched back on) their initial copies begin again.
func (l replicaLinks) TargetChanged(ctx context.Context, targetID int64, storageIDs []int64, restartCopies bool) {
	seen := map[int64]bool{}
	ids := append([]int64(nil), storageIDs...)
	if l.cache != nil {
		ids = append(ids, l.cache.onTarget(targetID)...)
	}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if l.cache != nil {
			l.cache.forget(id)
		}
		if l.svc == nil {
			continue
		}
		var err error
		if restartCopies {
			err = l.svc.RestartInitialCopy(context.WithoutCancel(ctx), id)
		} else {
			err = l.svc.LinkChanged(context.WithoutCancel(ctx), id)
		}
		if err != nil {
			slog.Warn("replica: initial copy not updated after a target change",
				slog.Int64("storage_id", id), slog.String("err", err.Error()))
		}
	}
}

var _ replica.Wrappers = (*storageCache)(nil)
