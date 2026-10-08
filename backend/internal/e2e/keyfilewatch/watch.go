package keyfilewatch

// Package keyfilewatch is the server's own record of a rewritten E2E key file
// (`.filex-e2e.json`): an audit row for every rewrite and, when a key slot
// changed, the owner told and - when the writer may retire them - the key
// file's old versions deleted. What changed is decided by e2e.DiffKeyFiles,
// who may delete and who is told by e2e/slotchange; docs/E2E-ENCRYPTION.md →
// "Who is told" and "What a password change does not undo".

import (
	"context"
	"io"
	"log/slog"
	"path"
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

// Watch turns a written key file into an audit row, and a changed key
// slot into the owner told and - when the writer may retire them - deleted
// versions. Wired as writehook's after-write observer.
type Watch struct {
	Audit    Audit
	Versions Versions // nil: nothing to compare against, nothing to delete
	Resolver func(storageID int64) (storage.Driver, error)
	// Owners answers who owns the encrypted folder and who wrote
	// (db.Store). nil: no owner is known, so only an administrator's
	// rewrite deletes anything.
	Owners slotchange.Store
	// Notify tells the owner (nil: nobody is told; the audit rows stay).
	Notify notify.Service
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
	folder := path.Dir("/" + node.Path)
	subject := slotchange.Subject{StorageID: storageID, Root: strings.Trim(folder, "/")}

	// A changed password or recovery slot: every earlier version wraps the
	// folder key under the secret that was just changed. Deleting them is
	// the owner's (or an administrator's) to do - anybody else who may write
	// the folder may also upload a file under this name, and its bytes prove
	// nothing (e2e/slotchange). Their rewrite keeps every version, and the
	// owner is told.
	purged, kept := 0, 0
	var verdict slotchange.Verdict
	if diff.Valid && diff.KeySlotChanged {
		verdict = slotchange.Judge(ctx, w.Owners, subject)
		if w.Versions != nil {
			if !verdict.MayRetire {
				kept = len(versions)
			} else {
				for _, v := range versions {
					if err := w.Versions.HardDeleteVersion(ctx, v.ID); err != nil {
						slog.Warn("e2e: could not delete an old key-file version",
							slog.Int64("storage", storageID), slog.String("path", node.Path), slog.String("err", err.Error()))
						continue
					}
					purged++
				}
			}
		}
	}
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
		_ = w.Audit.InsertAuditEntry(ctx, &model.AuditEntry{
			UserID:     actorID,
			Action:     "e2e.key_file_rewritten",
			TargetType: "node",
			TargetID:   target,
			Metadata:   meta,
		})
	}

	// The password change itself, said by the server from what it saw change
	// - never from a client's announcement (e2e/slotchange).
	if diff.Valid && diff.KeySlotChanged {
		t := &slotchange.Teller{Store: w.Owners, Audit: w.Audit, Notify: w.Notify}
		t.Tell(ctx, slotchange.Change{
			Subject: subject,
			Changes: diff.Changes,
			Origin:  origin,
			Deleted: purged,
			Kept:    kept,
		}, verdict)
	}
}
