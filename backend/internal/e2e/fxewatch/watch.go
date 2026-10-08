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
//
// ⚠ And only for a writer who may (e2e/slotchange): the file's owner or an
// administrator. Anybody else who may write the file may also upload a header
// that names its file key under a password slot of their own; that rewrite
// keeps every version, and the owner is told.
package fxewatch

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/e2e/slotchange"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
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
	// Owners answers who owns the file and who wrote (db.Store). nil: no
	// owner is known, so only an administrator's rewrite deletes anything.
	Owners slotchange.Store
	// Notify tells the owner (nil: nobody is told; the audit rows stay).
	Notify notify.Service
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
	subject := slotchange.Subject{StorageID: storageID, Root: strings.Trim(node.Path, "/"), File: true}

	var retired []*model.NodeVersion
	if diff.Valid && w.Versions != nil {
		for i, v := range versions {
			old := before
			if i > 0 {
				old = head(ctx, drv, v.StorageKey)
			}
			if e2e.RetiredSecret(old, after) {
				retired = append(retired, v)
			}
		}
	}
	// A password change of THIS file - the same file key under another
	// secret - is told; a different file written under the name is not.
	changed := diff.Valid && e2e.RetiredSecret(before, after)
	var verdict slotchange.Verdict
	if changed || len(retired) > 0 {
		verdict = slotchange.Judge(ctx, w.Owners, subject)
	}
	deleted, kept := 0, 0
	if verdict.MayRetire {
		for _, v := range retired {
			if err := w.Versions.HardDeleteVersion(ctx, v.ID); err != nil {
				slog.Warn("e2e: could not delete an old version of an encrypted file",
					slog.Int64("storage", storageID), slog.String("path", node.Path), slog.String("err", err.Error()))
				continue
			}
			deleted++
		}
	} else {
		kept = len(retired)
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
	if kept > 0 {
		meta["versions_kept"] = kept
	}
	var actorID *int64
	if wr := slotchange.WriterOf(ctx, w.Owners); wr.ID > 0 {
		id := wr.ID
		actorID = &id
		if wr.User != nil {
			meta["actor_email"] = wr.User.Email
		}
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

	// The password change itself, said by the server from what it saw change
	// - never from a client's announcement (e2e/slotchange).
	if changed {
		t := &slotchange.Teller{Store: w.Owners, Audit: w.Audit, Notify: w.Notify}
		t.Tell(ctx, slotchange.Change{
			Subject: subject,
			Changes: diff.Changes,
			Origin:  origin,
			Deleted: deleted,
			Kept:    kept,
		}, verdict)
	}
}
