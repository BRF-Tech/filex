// Package fxewatch is the server's own record of a rewritten single encrypted
// file (`.fxe`): an audit row for every rewrite of one, and — when the rewrite
// changed its password or recovery slot — the deletion of the older versions
// that still open the file with the old secret.
//
// A `.fxe` password change happens in the browser: a new header, the body
// carried over byte for byte (docs/E2E-ENCRYPTION.md → "Single encrypted
// files"). The web UI announces it (POST /api/files/e2e/password-changed), but
// an announcement is only as good as the client that makes it. This watch sees
// the rewrite itself, on every surface that writes a file, and compares the
// header with the version the pre-write guard just kept (e2e.DiffFileHeaders).
//
// ⚠ What it deletes, and why only that. After a password change the version
// the overwrite kept holds the OLD header over the SAME file key: whoever knows
// the old password opens the file's current contents with it. Those versions
// (e2e.RetiredSecret: the same file key under a password or recovery slot the
// file no longer uses) go. A version holding different content under a
// different key is history the owner may want, and it opens only what it
// always opened; so is one under the password still in use. Both stay.
package fxewatch

import (
	"context"
	"io"
	"log/slog"
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

// Watch turns a rewritten `.fxe` into an audit row, and a retired secret into
// deleted versions. Wired as a writehook after-write observer.
type Watch struct {
	Audit    Audit
	Versions Versions // nil: nothing to compare against, nothing to delete
	Resolver func(storageID int64) (storage.Driver, error)
}

// head reads as much of a stored object as a `.fxe` header can take.
func head(ctx context.Context, drv storage.Driver, key string) []byte {
	if drv == nil || key == "" {
		return nil
	}
	rc, err := drv.Read(ctx, key)
	if err != nil {
		return nil
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, e2e.FileHeaderFixedLen+e2e.FileHeaderMaxLen))
	if err != nil {
		return nil
	}
	return b
}

// OnWritten is called after every file write. It acts only on a file named
// like a `.fxe` that REPLACED another one: a new `.fxe` is a new encrypted
// file, not an event of its own.
func (w *Watch) OnWritten(ctx context.Context, storageID int64, node *model.Node, origin string, replaced bool) {
	if w == nil || node == nil || !replaced || !e2e.LooksEncryptedFile(node.Name) {
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
	after := head(ctx, drv, live)
	var before []byte
	var versions []*model.NodeVersion
	if drv != nil && w.Versions != nil && node.ID != 0 {
		versions, _ = w.Versions.List(ctx, node.ID)
		// Newest first: the one the pre-write guard just took is what this
		// write replaced.
		if len(versions) > 0 {
			before = head(ctx, drv, versions[0].StorageKey)
		}
	}
	diff := e2e.DiffFileHeaders(before, after)

	deleted := 0
	if diff.Valid && w.Versions != nil {
		for i, v := range versions {
			old := before
			if i > 0 {
				old = head(ctx, drv, v.StorageKey)
			}
			if !e2e.RetiredSecret(old, after) {
				continue
			}
			if err := w.Versions.HardDeleteVersion(ctx, v.ID); err != nil {
				slog.Warn("e2e: could not delete an old version of an encrypted file",
					slog.Int64("storage", storageID), slog.String("path", node.Path), slog.String("err", err.Error()))
				continue
			}
			deleted++
		}
	}

	meta := map[string]any{
		"storage_id": storageID,
		"path":       node.Path,
		"file":       node.Name,
		"origin":     origin,
		"changes":    diff.Changes,
		"compared":   diff.Compared,
		"valid":      diff.Valid,
	}
	if deleted > 0 {
		meta["versions_deleted"] = deleted
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
		if err := w.Audit.InsertAuditEntry(ctx, &model.AuditEntry{
			UserID:     actorID,
			Action:     "e2e.fxe_header_rewritten",
			TargetType: "node",
			TargetID:   target,
			Metadata:   meta,
		}); err != nil {
			slog.Warn("e2e: could not audit a rewritten encrypted file",
				slog.Int64("storage", storageID), slog.String("path", node.Path), slog.String("err", err.Error()))
		}
	}
}
