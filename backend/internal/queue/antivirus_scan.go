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
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/trash"
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
const avTrashPrefix = trash.Prefix

// AntivirusScanner owns the antivirus_scan job. Enqueue fires from the
// upload surfaces (upload finalize, manager vfUpload, public drop);
// Handle runs on the worker pool.
type AntivirusScanner struct {
	store    AVNodeStore
	resolver func(int64) (storage.Driver, error)
	// body resolves where a node's bytes are: the driver, or filex's staging
	// area while a staged upload is still transferring. Nil-safe.
	body     *filebody.Resolver
	scanner  AVScanner
	notify   notify.Service
	index    *search.Index
	maxBytes int64
	// maxBytesFn, when set, is asked for the ceiling on every eligibility
	// check instead of using the fixed maxBytes above. That is what makes the
	// admin page's value apply to the next file scanned rather than to the
	// next restart.
	maxBytesFn func() int64
}

// NewAntivirusScanner wires the job. maxBytes <= 0 disables the size
// gate override and callers should pass antivirus.MaxScanBytes().
func NewAntivirusScanner(store AVNodeStore, resolver func(int64) (storage.Driver, error), sc AVScanner, n notify.Service, idx *search.Index, maxBytes int64) *AntivirusScanner {
	return &AntivirusScanner{store: store, resolver: resolver, scanner: sc, notify: n, index: idx, maxBytes: maxBytes}
}

// AttachBody wires the byte-source resolver so a re-queued scan of a file that
// is still being transferred reads the staged bytes.
func (a *AntivirusScanner) AttachBody(b *filebody.Resolver) { a.body = b }

// AttachMaxBytes makes the size ceiling live: fn is consulted per eligibility
// check. nil restores the fixed value passed to the constructor.
func (a *AntivirusScanner) AttachMaxBytes(fn func() int64) { a.maxBytesFn = fn }

// limit returns the ceiling in force. <=0 means "no ceiling".
func (a *AntivirusScanner) limit() int64 {
	if a.maxBytesFn != nil {
		if v := a.maxBytesFn(); v > 0 {
			return v
		}
	}
	return a.maxBytes
}

// Eligible reports whether n qualifies for a scan: a live file within the
// size cap that is not itself a trash/version artifact.
func (a *AntivirusScanner) Eligible(n *model.Node) bool {
	if a == nil || n == nil || n.Type != model.NodeTypeFile || n.DeletedAt != nil {
		return false
	}
	if lim := a.limit(); n.Size <= 0 || (lim > 0 && n.Size > lim) {
		return false
	}
	p := strings.TrimPrefix(n.Path, "/")
	if strings.HasPrefix(p, avTrashPrefix+"/") || strings.HasPrefix(p, ".versions/") {
		return false
	}
	return true
}

// PriorityDiscovered is the Op.Priority given to a scan the STORAGE SYNC asked
// for, as opposed to one a person is waiting on.
//
// ⚠⚠ This exists because of one measurement. Point filex at a folder that
// already holds 20 000 files and every one of them is newly discovered, so one
// pass enqueues 20 000 scans. The queue orders `priority DESC, enqueued_at
// ASC`, so at equal priority those 20 000 rows are simply ahead of everything
// that arrives next — and an upload's scan, enqueued ten seconds into the
// backlog, waited **41 s** to be picked up: not most of a scan, most of the
// whole import. Dropping the sync's scans one step below everything else took
// the same probe to **1 ms**, because the only thing an interactive scan then
// waits for is a worker finishing the one scan it is holding.
//
// Negative rather than "interactive positive" so that every op filex already
// enqueues — content extraction, thumbnails, replica retry, copy/move — keeps
// its current standing and also overtakes the sweep. Nothing else in filex
// sets Priority, so this is the first row that is not 0.
//
// ⚠ SQLite and Postgres order by it; the Redis driver's pending LIST is
// positional and ignores it. On Redis the backlog is FIFO and the 41 s is
// what an operator gets. That is a driver difference, not a policy: the fix
// belongs in the Redis driver (a second list, or a ZSET keyed by priority),
// and it is out of scope here.
const PriorityDiscovered = -1

// Enqueue schedules a scan for n when the scanner is available and n is
// eligible. Best-effort: enqueue failures are logged, never surfaced — a
// scan must not cost a write.
func (a *AntivirusScanner) Enqueue(ctx context.Context, drv Driver, n *model.Node) {
	a.enqueue(ctx, drv, n, 0)
}

// EnqueueDiscovered is Enqueue for a file the STORAGE SYNC found rather than a
// file somebody wrote: same op, same eligibility, same handler, one step down
// the queue. See PriorityDiscovered.
func (a *AntivirusScanner) EnqueueDiscovered(ctx context.Context, drv Driver, n *model.Node) {
	a.enqueue(ctx, drv, n, PriorityDiscovered)
}

func (a *AntivirusScanner) enqueue(ctx context.Context, drv Driver, n *model.Node, priority int) {
	if a == nil || drv == nil || a.scanner == nil || !a.scanner.Supports() || !a.Eligible(n) {
		return
	}
	if _, err := drv.Enqueue(ctx, Op{
		Type:     TypeAntivirusScan,
		Payload:  map[string]any{"node_id": n.ID},
		Priority: priority,
	}); err != nil {
		slog.Warn("antivirus: enqueue failed",
			slog.Int64("node", n.ID), slog.String("err", err.Error()))
	}
}

// AVDedupKey is the coalescing key for a node's pending scan. One pending
// scan per node, which is the whole of the debounce rule: while an op holds
// this key, further requests for the same node are absorbed.
func AVDedupKey(nodeID int64) string {
	return TypeAntivirusScan + ":" + strconv.FormatInt(nodeID, 10)
}

// EnqueueAfterSave schedules a DEBOUNCED scan for a file the browser's text
// editor has just overwritten: one scan, `window` from now, coalesced so that
// a burst of Ctrl+S costs exactly one scan.
//
// Three properties make this work, and none of them is incidental:
//
//   - The delay is the queue's own not_before, never a time.AfterFunc. An
//     in-process timer dies with the process and takes the pending scan with
//     it, so every restart and every deploy would silently drop whatever was
//     in flight — reintroducing, at exactly the moments a server is most
//     likely to be restarted, the gap this feature exists to close. The wider
//     the window, the more scans an in-process timer would lose.
//   - Repeat saves inside the window are DROPPED, not rescheduled. The window
//     therefore starts at the first save and the scan is guaranteed to happen,
//     rather than being pushed out indefinitely by someone who keeps typing.
//   - Handle resolves the node and opens its bytes at EXECUTION time, so the
//     one scan that does run reads the file's final state, not the content of
//     the save that scheduled it.
//
// Best-effort, exactly like Enqueue: a scan must not cost a write. ErrDuplicate
// is the normal, expected outcome of the second save onwards and is not an
// error — it means the scan the caller wanted is already scheduled.
func (a *AntivirusScanner) EnqueueAfterSave(ctx context.Context, drv Driver, n *model.Node, window time.Duration) {
	if a == nil || drv == nil || a.scanner == nil || !a.scanner.Supports() || !a.Eligible(n) {
		return
	}
	if window <= 0 {
		// Refuse to interpret a nonsense window as "right now" or as
		// "never"; the caller resolves and clamps it (antivirus.SaveWindow).
		// Scanning immediately is the safe reading, so take it.
		a.Enqueue(ctx, drv, n)
		return
	}
	at := time.Now().Add(window)
	_, err := drv.Enqueue(ctx, Op{
		Type:      TypeAntivirusScan,
		Payload:   map[string]any{"node_id": n.ID},
		NotBefore: &at,
		DedupKey:  AVDedupKey(n.ID),
	})
	switch {
	case errors.Is(err, ErrDuplicate):
		slog.Debug("antivirus: save scan already scheduled",
			slog.Int64("node", n.ID))
	case err != nil:
		slog.Warn("antivirus: delayed enqueue failed",
			slog.Int64("node", n.ID), slog.String("err", err.Error()))
	default:
		slog.Debug("antivirus: save scan scheduled",
			slog.Int64("node", n.ID), slog.Time("not_before", at))
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
	src, err := a.body.Resolve(ctx, drv, n.StorageID, livePath, n)
	if err != nil {
		return fmt.Errorf("antivirus: resolve %q: %w", livePath, err)
	}
	rc, err := src.Open(ctx)
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
			Title:    "Infected file detected",
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
