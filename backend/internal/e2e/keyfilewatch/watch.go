package keyfilewatch

// Package keyfilewatch is the server's own record of a rewritten E2E key file
// (`.filex-e2e.json`): an audit row for every rewrite, and the key file's old
// versions deleted when a key slot changed. What changed is decided by
// e2e.DiffKeyFiles; docs/E2E-ENCRYPTION.md → "Who is told" and "What a
// password change does not undo".

import (
	"context"
	"io"
	"log/slog"
	"path"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Versions is the narrow versioning surface the watch needs
// (versioning.Service satisfies it).
type Versions interface {
	List(ctx context.Context, nodeID int64) ([]*model.NodeVersion, error)
	HardDeleteVersion(ctx context.Context, versionID int64) error
}

// Audit is the narrow store surface the watch needs.
type Audit interface {
	InsertAuditEntry(ctx context.Context, e *model.AuditEntry) error
}

// Watch turns a written key file into an audit row, and a changed key
// slot into deleted versions. Wired as writehook's after-write observer.
type Watch struct {
	Audit    Audit
	Versions Versions // nil: nothing to compare against, nothing to delete
	Resolver func(storageID int64) (storage.Driver, error)
}

func readAll(ctx context.Context, drv storage.Driver, key string) []byte {
	rc, err := drv.Read(ctx, key)
	if err != nil {
		return nil
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		return nil
	}
	return b
}

// OnWritten is called after every file write; it acts only on a key file
// that REPLACED another one (a new key file is a new encrypted folder, which
// is not an event of its own).
func (w *Watch) OnWritten(ctx context.Context, storageID int64, node *model.Node, origin string, replaced bool) {
	if w == nil || node == nil || node.Name != e2e.MarkerName || !replaced {
		return
	}
	ctx = context.WithoutCancel(ctx)
	live := node.Path
	if node.StorageKey != "" {
		live = node.StorageKey
	}
	var drv storage.Driver
	if w.Resolver != nil {
		drv, _ = w.Resolver(storageID)
	}
	var after, before []byte
	var versions []*model.NodeVersion
	if drv != nil {
		after = readAll(ctx, drv, live)
		if w.Versions != nil && node.ID != 0 {
			versions, _ = w.Versions.List(ctx, node.ID)
			// Newest first: the one the pre-write guard just took is what
			// this write replaced.
			if len(versions) > 0 {
				before = readAll(ctx, drv, versions[0].StorageKey)
			}
		}
	}
	diff := e2e.DiffKeyFiles(before, after)

	purged := 0
	if diff.Valid && diff.KeySlotChanged && w.Versions != nil {
		for _, v := range versions {
			if err := w.Versions.HardDeleteVersion(ctx, v.ID); err != nil {
				slog.Warn("e2e: could not delete an old key-file version",
					slog.Int64("storage", storageID), slog.String("path", node.Path), slog.String("err", err.Error()))
				continue
			}
			purged++
		}
	}

	folder := path.Dir("/" + node.Path)
	meta := map[string]any{
		"storage_id": storageID,
		"folder":     folder,
		"origin":     origin,
		"changes":    diff.Changes,
		"compared":   before != nil,
		"valid":      diff.Valid,
	}
	if purged > 0 {
		meta["versions_deleted"] = purged
	}
	var actorID *int64
	if u := auth.UserFrom(ctx); u != nil {
		actorID = &u.ID
		meta["actor_email"] = u.Email
	}
	target := ""
	if node.ID != 0 {
		target = strconv.FormatInt(node.ID, 10)
	}
	if w.Audit != nil {
		_ = w.Audit.InsertAuditEntry(ctx, &model.AuditEntry{
			UserID:     actorID,
			Action:     "e2e.key_file_rewritten",
			TargetType: "node",
			TargetID:   target,
			Metadata:   meta,
		})
	}
}
