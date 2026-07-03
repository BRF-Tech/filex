// Package versioning persists historical snapshots of node contents inside
// the same storage backend, under a `.versions/<node_id>/<version_n>` prefix.
//
// Snapshots are taken before a destructive write (upload finalize, archive
// extract that overwrites, manager save). Listing + restore are exposed via
// dedicated /api/files/versions endpoints.
//
// Storage drivers that implement storage.Copier get fast server-side copies;
// anything else falls back to stream-and-rewrite via Read + Write.
package versioning

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// DefaultRetention is the per-node version count kept by Cleanup if the
// caller does not specify a different value.
const DefaultRetention = 20

// VersionsPrefix is prepended to each snapshot key. Changing it invalidates
// existing snapshots; do not change without a migration.
const VersionsPrefix = ".versions"

// StorageResolver maps a storage_id to a live driver. Same shape as the
// resolver used by the rest of the API layer.
type StorageResolver func(int64) (storage.Driver, error)

// Service is the high-level entry point for versioning operations.
type Service struct {
	Store    db.Store
	Resolver StorageResolver
}

// New constructs a Service.
func New(store db.Store, resolver StorageResolver) *Service {
	return &Service{Store: store, Resolver: resolver}
}

// Snapshot copies the current live storage_key into the .versions/ tree and
// records a node_versions row. Returns nil with no error when there's nothing
// to snapshot (node has no live storage_key yet — first ever write).
//
// Callers should invoke this BEFORE a destructive write. If the snapshot
// itself fails (storage / DB error) the caller should NOT proceed with the
// destructive write — losing version history is preferable to corruption.
func (s *Service) Snapshot(ctx context.Context, nodeID int64) (*model.NodeVersion, error) {
	if s == nil || s.Store == nil || s.Resolver == nil {
		return nil, errors.New("versioning: service not initialised")
	}
	node, err := s.Store.GetNode(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("versioning: get node: %w", err)
	}
	if node.Type != model.NodeTypeFile {
		return nil, nil // directories and symlinks aren't versioned
	}
	livePath := node.Path
	if node.StorageKey != "" {
		livePath = node.StorageKey
	}
	if livePath == "" {
		return nil, nil // nothing to snapshot
	}
	drv, err := s.Resolver(node.StorageID)
	if err != nil {
		return nil, fmt.Errorf("versioning: resolve storage: %w", err)
	}

	// Probe — if the live path doesn't exist on the backend yet, skip.
	if _, err := drv.Stat(ctx, livePath); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("versioning: stat live: %w", err)
	}

	versionN, err := s.Store.NextNodeVersionNumber(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("versioning: next number: %w", err)
	}
	snapshotKey := versionKey(nodeID, versionN)

	if err := copyOrStream(ctx, drv, livePath, snapshotKey); err != nil {
		return nil, fmt.Errorf("versioning: snapshot copy: %w", err)
	}

	v := &model.NodeVersion{
		NodeID:     nodeID,
		VersionN:   versionN,
		StorageKey: snapshotKey,
		Size:       node.Size,
		Etag:       node.Etag,
	}
	created, err := s.Store.CreateNodeVersion(ctx, v)
	if err != nil {
		// Best-effort cleanup — orphan snapshot otherwise.
		_ = tryDelete(ctx, drv, snapshotKey)
		return nil, fmt.Errorf("versioning: persist row: %w", err)
	}

	// Trim retention asynchronously is overkill — do it inline.
	if _, err := s.Cleanup(ctx, nodeID, DefaultRetention); err != nil {
		// Don't fail the snapshot path on cleanup error.
		_ = err
	}
	return created, nil
}

// List returns all versions for one node, newest-first.
func (s *Service) List(ctx context.Context, nodeID int64) ([]*model.NodeVersion, error) {
	if s == nil || s.Store == nil {
		return nil, errors.New("versioning: service not initialised")
	}
	return s.Store.ListNodeVersions(ctx, nodeID)
}

// Restore copies a recorded version back over the live path.
//
// If snapshotCurrent is true the current live content is first snapshotted
// so the operation is reversible.
func (s *Service) Restore(ctx context.Context, nodeID, versionID int64, snapshotCurrent bool) error {
	if s == nil || s.Store == nil || s.Resolver == nil {
		return errors.New("versioning: service not initialised")
	}
	node, err := s.Store.GetNode(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("versioning: get node: %w", err)
	}
	v, err := s.Store.GetNodeVersion(ctx, versionID)
	if err != nil {
		return fmt.Errorf("versioning: get version: %w", err)
	}
	if v.NodeID != nodeID {
		return errors.New("versioning: version belongs to a different node")
	}
	drv, err := s.Resolver(node.StorageID)
	if err != nil {
		return fmt.Errorf("versioning: resolve storage: %w", err)
	}

	livePath := node.Path
	if node.StorageKey != "" {
		livePath = node.StorageKey
	}
	if livePath == "" {
		return errors.New("versioning: node has no live path")
	}

	if snapshotCurrent {
		if _, err := s.Snapshot(ctx, nodeID); err != nil {
			return fmt.Errorf("versioning: pre-restore snapshot: %w", err)
		}
	}

	if err := copyOrStream(ctx, drv, v.StorageKey, livePath); err != nil {
		return fmt.Errorf("versioning: restore copy: %w", err)
	}

	// Update node row's size/etag from the restored version so subsequent
	// reads/quotas line up.
	if err := s.Store.UpdateNodeMeta(ctx, nodeID, v.Size, node.Mime, v.Etag, node.DBMtime); err != nil {
		return fmt.Errorf("versioning: update node meta: %w", err)
	}
	return nil
}

// Cleanup deletes versions for a node beyond the keepN newest. It also tries
// to delete the underlying storage objects but tolerates per-object failures.
func (s *Service) Cleanup(ctx context.Context, nodeID int64, keepN int) (int, error) {
	if keepN <= 0 {
		keepN = DefaultRetention
	}
	if s == nil || s.Store == nil {
		return 0, errors.New("versioning: service not initialised")
	}
	doomed, err := s.Store.DeleteOldNodeVersions(ctx, nodeID, keepN)
	if err != nil {
		return 0, fmt.Errorf("versioning: delete old: %w", err)
	}
	if len(doomed) == 0 {
		return 0, nil
	}
	node, err := s.Store.GetNode(ctx, nodeID)
	if err != nil {
		// DB rows are gone — return success-with-warning.
		return len(doomed), nil
	}
	drv, err := s.Resolver(node.StorageID)
	if err != nil {
		return len(doomed), nil
	}
	for _, v := range doomed {
		_ = tryDelete(ctx, drv, v.StorageKey)
	}
	return len(doomed), nil
}

// HardDeleteVersion drops one version row + its storage object (admin op).
func (s *Service) HardDeleteVersion(ctx context.Context, versionID int64) error {
	if s == nil || s.Store == nil || s.Resolver == nil {
		return errors.New("versioning: service not initialised")
	}
	v, err := s.Store.GetNodeVersion(ctx, versionID)
	if err != nil {
		return fmt.Errorf("versioning: get version: %w", err)
	}
	node, err := s.Store.GetNode(ctx, v.NodeID)
	if err == nil {
		if drv, err2 := s.Resolver(node.StorageID); err2 == nil {
			_ = tryDelete(ctx, drv, v.StorageKey)
		}
	}
	return s.Store.DeleteNodeVersion(ctx, versionID)
}

// versionKey returns the storage key used to persist a snapshot.
func versionKey(nodeID int64, versionN int) string {
	return path.Join(VersionsPrefix, strconv.FormatInt(nodeID, 10), strconv.Itoa(versionN))
}

// copyOrStream uses Copier when available, otherwise streams Read→Write.
func copyOrStream(ctx context.Context, drv storage.Driver, src, dst string) error {
	if cp, ok := drv.(storage.Copier); ok {
		if err := cp.Copy(ctx, src, dst); err == nil {
			return nil
		} else if !errors.Is(err, storage.ErrUnsupported) {
			return err
		}
	}
	wr, ok := drv.(storage.Writer)
	if !ok {
		return fmt.Errorf("%w: driver lacks Writer", storage.ErrUnsupported)
	}
	rc, err := drv.Read(ctx, src)
	if err != nil {
		return err
	}
	defer rc.Close()
	// Stat the source so Writer gets the real size; fall back to -1 (unknown).
	var size int64 = -1
	if obj, err := drv.Stat(ctx, src); err == nil {
		size = obj.Size
	}
	return wr.Write(ctx, dst, rc, size)
}

// tryDelete is best-effort — used when a write fails or in cleanup.
func tryDelete(ctx context.Context, drv storage.Driver, key string) error {
	d, ok := drv.(storage.Deleter)
	if !ok {
		return storage.ErrUnsupported
	}
	return d.Delete(ctx, key)
}

// Compile-time: io.Reader is used in copyOrStream via rc; ensure io is imported.
var _ io.Reader = (*nopReader)(nil)

type nopReader struct{}

func (nopReader) Read([]byte) (int, error) { return 0, io.EOF }
