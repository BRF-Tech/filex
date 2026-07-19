package queue

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// TypeAntivirusScan is the op type for async ClamAV scanning ("Koru",
// v0.4): read a freshly written file from its storage driver, run the
// resolved clamdscan/clamscan binary over it, and — only when infected —
// quarantine the node into `.filex-trash/` + emit a `file.infected`
// event. Clean files see zero side effects, and the upload path is never
// blocked (content_index pattern: enqueue is best-effort, work happens on
// the worker pool).
const TypeAntivirusScan = "antivirus_scan"

// AVScanner is the slim scan contract the job needs — satisfied by
// *antivirus.Scanner, faked in tests.
type AVScanner interface {
	Supports() bool
	Scan(ctx context.Context, r io.Reader) (infected bool, signature string, err error)
}

// AVNodeStore is the slim store contract for lookup + quarantine retag.
type AVNodeStore interface {
	GetNode(ctx context.Context, id int64) (*model.Node, error)
	SoftDeleteAndRetag(ctx context.Context, id int64, trashPath, trashHash, origPath string) error
}

// avTrashPrefix mirrors handlers' trashPrefix (unexported there — same
// duplication precedent as internal/dav/dbsync.go): quarantined files
// must land where the trash listing/restore/purge machinery already
// looks.
const avTrashPrefix = ".filex-trash"

// AntivirusScanner owns the antivirus_scan job. Enqueue fires from the
// upload surfaces (upload finalize, manager vfUpload, public drop);
// Handle runs on the worker pool.
type AntivirusScanner struct {
	store    AVNodeStore
	resolver func(int64) (storage.Driver, error)
	scanner  AVScanner
	notify   notify.Service
	index    *search.Index
	maxBytes int64
}

// NewAntivirusScanner wires the job. maxBytes <= 0 disables the size
// gate override and callers should pass antivirus.MaxScanBytes().
func NewAntivirusScanner(store AVNodeStore, resolver func(int64) (storage.Driver, error), sc AVScanner, n notify.Service, idx *search.Index, maxBytes int64) *AntivirusScanner {
	return &AntivirusScanner{store: store, resolver: resolver, scanner: sc, notify: n, index: idx, maxBytes: maxBytes}
}

// Eligible reports whether n qualifies for a scan: a live file within the
// size cap that is not itself a trash/version artifact.
func (a *AntivirusScanner) Eligible(n *model.Node) bool {
	if a == nil || n == nil || n.Type != model.NodeTypeFile || n.DeletedAt != nil {
		return false
	}
	if n.Size <= 0 || (a.maxBytes > 0 && n.Size > a.maxBytes) {
		return false
	}
	p := strings.TrimPrefix(n.Path, "/")
	if strings.HasPrefix(p, avTrashPrefix+"/") || strings.HasPrefix(p, ".versions/") {
		return false
	}
	return true
}

// Enqueue schedules a scan for n when the scanner is available and n is
// eligible. Best-effort: enqueue failures are logged, never surfaced — a
// scan must not cost a write.
func (a *AntivirusScanner) Enqueue(ctx context.Context, drv Driver, n *model.Node) {
	if a == nil || drv == nil || a.scanner == nil || !a.scanner.Supports() || !a.Eligible(n) {
		return
	}
	if _, err := drv.Enqueue(ctx, Op{
		Type:    TypeAntivirusScan,
		Payload: map[string]any{"node_id": n.ID},
	}); err != nil {
		slog.Warn("antivirus: enqueue failed",
			slog.Int64("node", n.ID), slog.String("err", err.Error()))
	}
}

// Handle processes one antivirus_scan op. Vanished/ineligible nodes
// resolve as done (nil). Storage-read and scan failures return an error
// so the queue's retry budget applies. A clean verdict has no side
// effects; an infected verdict quarantines the node (storage rename into
// `.filex-trash/` + DB soft-delete retag) and emits `file.infected`.
func (a *AntivirusScanner) Handle(ctx context.Context, op Op) error {
	nodeID := payloadInt64(op.Payload, "node_id")
	if nodeID == 0 {
		return nil
	}
	n, err := a.store.GetNode(ctx, nodeID)
	if err != nil || n == nil {
		return nil // deleted before the worker got to it
	}
	if !a.Eligible(n) {
		return nil
	}
	drv, err := a.resolver(n.StorageID)
	if err != nil {
		return fmt.Errorf("antivirus: resolve storage %d: %w", n.StorageID, err)
	}
	livePath := n.Path
	if n.StorageKey != "" {
		livePath = n.StorageKey
	}
	rc, err := drv.Read(ctx, livePath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil // vanished between enqueue and scan
		}
		return fmt.Errorf("antivirus: read %q: %w", livePath, err)
	}
	infected, sig, err := a.scanner.Scan(ctx, rc)
	rc.Close()
	if err != nil {
		return fmt.Errorf("antivirus: scan %q: %w", livePath, err)
	}
	if !infected {
		return nil
	}
	return a.quarantine(ctx, drv, n, livePath, sig)
}

// quarantine renames the infected object into `.filex-trash/` (same key
// scheme as the manager's soft delete, so trash listing/restore/purge all
// work on it), retags the DB row, drops the node from the search index
// and emits the `file.infected` event.
func (a *AntivirusScanner) quarantine(ctx context.Context, drv storage.Driver, n *model.Node, livePath, sig string) error {
	base := path.Base(livePath)
	trashRel := fmt.Sprintf("%s/%d-%s__%s", avTrashPrefix, time.Now().Unix(), avRandHex6(), base)
	quarantined := false
	if mv, ok := drv.(storage.Mover); ok {
		if err := mv.Move(ctx, livePath, trashRel); err != nil && !errors.Is(err, storage.ErrNotFound) {
			// Bytes still live — fail the op so the retry budget re-attempts
			// the quarantine instead of leaving an infected file in place
			// with a lying DB row.
			return fmt.Errorf("antivirus: quarantine move %q: %w", livePath, err)
		}
		quarantined = true
		origClean := avNormalizePath(n.Path)
		trashClean := avNormalizePath(trashRel)
		if err := a.store.SoftDeleteAndRetag(ctx, n.ID, trashClean, pathkey.Hash(n.StorageID, trashClean), origClean); err != nil {
			slog.Warn("antivirus: quarantine retag failed",
				slog.Int64("node", n.ID), slog.String("err", err.Error()))
		}
		if a.index != nil {
			_ = a.index.DeleteNode(ctx, n.ID)
		}
	} else {
		slog.Warn("antivirus: driver lacks move; infected file NOT quarantined",
			slog.Int64("node", n.ID), slog.String("path", n.Path))
	}

	slog.Warn("antivirus: infected file detected",
		slog.Int64("node", n.ID),
		slog.String("path", n.Path),
		slog.String("signature", sig),
		slog.Bool("quarantined", quarantined))

	if a.notify != nil {
		ev := notify.Event{
			Event:    notify.EventFileInfected,
			Severity: notify.SeverityWarning,
			Title:    "Virüs tespit edildi",
			Body:     fmt.Sprintf("%s: %s", n.Path, sig),
			Meta: map[string]any{
				"signature":   sig,
				"quarantined": quarantined,
			},
			Node: &notify.NodeRef{StorageID: n.StorageID, Path: n.Path, Name: n.Name, Size: n.Size},
			TS:   time.Now(),
		}
		if quarantined {
			ev.Meta["trash_path"] = avNormalizePath(trashRel)
		}
		if _, err := a.notify.Send(ctx, ev); err != nil {
			slog.Warn("antivirus: file.infected send failed",
				slog.Int64("node", n.ID), slog.String("err", err.Error()))
		}
	}
	return nil
}

// avNormalizePath canonicalises a path the way the shared pathkey.Hash key
// expects (handlers.normalizeDBPath twin): the retagged trash row must
// collide with the rows the manager and sync worker write.
func avNormalizePath(rel string) string {
	rel = strings.Trim(rel, "/")
	clean := path.Clean("/" + rel)
	return strings.TrimRight(clean, "/")
}

// avRandHex6 returns a 6-char lowercase hex string for trash-key
// uniqueness (handlers.randHex6 twin).
func avRandHex6() string {
	var b [3]byte
	_, _ = crand.Read(b[:])
	return hex.EncodeToString(b[:])
}
