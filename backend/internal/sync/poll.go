package sync

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// RunOnce performs one full sync pass for the storage:
//  1. Open a sync_runs row (status=running)
//  2. Recursively walk the backend, upserting nodes and updating seen_at
//  3. Tombstone-pass: any node whose seen_at < runStart is soft-deleted —
//     but only if seen_count >= 0.7 * lastSeenCount (false-positive guard).
//  4. Close the sync_runs row with the final status.
func (s *storageSyncer) RunOnce(ctx context.Context) error {
	// `runStart` is truncated to second precision to match SQLite's
	// CURRENT_TIMESTAMP resolution. Without the truncation a sub-second
	// runStart compares STRICTLY GREATER than every same-second seen_at
	// touched during the run (because TouchNodeSeen + UpdateNodeMeta
	// both write CURRENT_TIMESTAMP, which has no fractional part). The
	// tombstone-pass would then re-delete the nodes the walk just
	// resurrected — exactly the loop we hit on s3-test://.
	runStart := time.Now().Truncate(time.Second)
	prevSeen, _ := s.previousSeenCount(ctx)
	run, err := s.store.CreateSyncRun(ctx, s.storage.ID, s.storage.LastSyncToken)
	if err != nil {
		return err
	}
	added, updated := 0, 0
	seen, err := s.walk(ctx, "/", nil, &added, &updated)
	if err != nil {
		_ = s.store.FinishSyncRun(ctx, run.ID, "", seen, added, updated, 0, "failed", err.Error())
		return err
	}

	deleted := 0
	if guardOK(seen, prevSeen) {
		stale, err := s.store.ListStaleNodes(ctx, s.storage.ID, runStart)
		if err == nil {
			for _, n := range stale {
				if err := s.store.SoftDeleteNode(ctx, n.ID); err == nil {
					deleted++
					if s.index != nil {
						_ = s.index.DeleteNode(ctx, n.ID)
					}
				}
			}
		}
	} else {
		slog.Warn("sync: tombstone guard tripped",
			slog.Int("seen", seen),
			slog.Int("prev_seen", prevSeen),
			slog.String("storage", s.storage.Name))
	}

	_ = s.store.UpdateStorageSyncCursor(ctx, s.storage.ID, runStart, "")
	// Cache each folder's recursive size on its own row so the explorer can show
	// folder sizes without re-scanning the backend (best-effort; never fails the
	// sync).
	if err := RecomputeFolderSizes(ctx, s.store, s.storage.ID); err != nil {
		slog.Warn("sync: folder-size recompute",
			slog.String("storage", s.storage.Name), slog.String("err", err.Error()))
	}
	_ = s.store.FinishSyncRun(ctx, run.ID, "", seen, added, updated, deleted, "ok", "")
	return nil
}

// walk recursively lists the storage from `path` downwards. parent is the
// DB id of the parent node (nil at root).
func (s *storageSyncer) walk(ctx context.Context, p string, parent *int64, added, updated *int) (int, error) {
	objs, err := s.driver.List(ctx, p)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, obj := range objs {
		select {
		case <-ctx.Done():
			return count, ctx.Err()
		default:
		}
		hash := pathHash(s.storage.ID, obj.Path)
		existing, _ := s.store.GetNodeByPath(ctx, s.storage.ID, hash)
		if existing == nil {
			// Maybe a soft-deleted row at the same path? UNIQUE
			// constraint on (storage_id, path_hash) blocks a fresh
			// insert, AND the live-row lookup excludes deleted rows.
			// Without this branch the second sync after a stray
			// delete left the dir invisible until manual SQL surgery.
			if zombie, _ := s.store.GetNodeByPathIncludingDeleted(ctx, s.storage.ID, hash); zombie != nil && zombie.DeletedAt != nil {
				if err := s.store.RestoreNode(ctx, zombie.ID); err != nil {
					slog.Warn("sync: restore node failed",
						slog.String("path", obj.Path),
						slog.String("err", err.Error()))
					continue
				}
				// Touch seen_at so the tombstone-pass at end of run
				// doesn't immediately re-delete the row we just
				// restored.
				_ = s.store.TouchNodeSeen(ctx, zombie.ID)
				if etagDrift(zombie.Etag, obj.Etag) {
					_ = s.store.UpdateNodeMeta(ctx, zombie.ID, obj.Size, obj.Mime, obj.Etag, obj.Mtime)
					*updated++
				}
				if s.index != nil {
					if fresh, _ := s.store.GetNode(ctx, zombie.ID); fresh != nil {
						_ = s.index.IndexNode(ctx, fresh)
					}
				}
				count++
				if zombie.Type == model.NodeTypeDirectory {
					cn, err := s.walk(ctx, obj.Path, &zombie.ID, added, updated)
					if err == nil {
						count += cn
					}
				}
				continue
			}
			n := &model.Node{
				StorageID:    s.storage.ID,
				ParentID:     parent,
				Name:         obj.Name,
				Path:         obj.Path,
				PathHash:     hash,
				StorageKey:   obj.Path,
				Type:         model.NodeType(string(obj.Kind)),
				Size:         obj.Size,
				Mime:         obj.Mime,
				Etag:         obj.Etag,
				BackendMtime: timePtr(obj.Mtime),
				SyncState:    model.SyncStateSynced,
			}
			if obj.Kind == storage.KindDirectory {
				n.Type = model.NodeTypeDirectory
			} else {
				n.Type = model.NodeTypeFile
			}
			created, err := s.store.CreateNode(ctx, n)
			if err != nil {
				slog.Warn("sync: create node failed", slog.String("path", obj.Path), slog.String("err", err.Error()))
				continue
			}
			*added++
			count++
			if s.index != nil {
				_ = s.index.IndexNode(ctx, created)
			}
			if obj.Kind == storage.KindDirectory {
				cn, err := s.walk(ctx, obj.Path, &created.ID, added, updated)
				if err == nil {
					count += cn
				}
			}
		} else {
			// existing — update if etag drift detected
			drifted := false
			if etagDrift(existing.Etag, obj.Etag) {
				if err := s.store.UpdateNodeMeta(ctx, existing.ID, obj.Size, obj.Mime, obj.Etag, obj.Mtime); err == nil {
					*updated++
					drifted = true
				}
			} else {
				_ = s.store.TouchNodeSeen(ctx, existing.ID)
				// Backfill a missing backend_mtime for nodes first synced by an
				// older version (before mtime was recorded on insert). Without
				// this, files whose content never drifts keep a null date
				// forever, so their folders never get a "last activity" date
				// after an upgrade. One cheap write per node, only while null.
				if existing.BackendMtime == nil && !obj.Mtime.IsZero() {
					_ = s.store.SetNodeMtime(ctx, existing.ID, timePtr(obj.Mtime))
				}
			}
			if s.index != nil && drifted {
				if fresh, _ := s.store.GetNode(ctx, existing.ID); fresh != nil {
					_ = s.index.IndexNode(ctx, fresh)
				}
			}
			count++
			if existing.Type == model.NodeTypeDirectory {
				cn, err := s.walk(ctx, obj.Path, &existing.ID, added, updated)
				if err == nil {
					count += cn
				}
			}
		}
	}
	return count, nil
}

func (s *storageSyncer) previousSeenCount(ctx context.Context) (int, error) {
	last, err := s.store.GetLastSyncRun(ctx, s.storage.ID)
	if err != nil || last == nil {
		return 0, err
	}
	return last.SeenCount, nil
}

// guardOK returns true if it's safe to delete stale nodes.
//
// Block tombstone pass when seen_count drops more than 30% vs previous run —
// usually a transient backend glitch (network, perms, eventual consistency)
// rather than a real wholesale deletion.
func guardOK(seen, prev int) bool {
	if prev == 0 {
		return true
	}
	threshold := float64(prev) * 0.7
	return float64(seen) >= threshold
}

func pathHash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
}

func decodeJSON(b []byte, out any) error {
	return json.Unmarshal(b, out)
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
