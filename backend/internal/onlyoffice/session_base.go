package onlyoffice

// A save never lands over a version its editing session did not see (#184).
//
// The document server edits the version it fetched when the session opened,
// and saves the whole document back when the session ends. If the file
// changed in between - another person's save through WebDAV, a sync client,
// an agent writing it on a mounted folder, a second editing session on the
// newer version - that save used to be written over the newer version, and
// the change it replaced was gone without a word.
//
// # The base of a session
//
// When filex hands out an EDIT config it records, per document key, the
// version the driver reports for the file at that moment (size, mtime,
// etag): the session's base. A second person handed the same key joins the
// running session and sees ITS version, so the first record stands. filex's
// own save moves the base on (a session that force-saves twice is not
// stale after its first save).
//
// # The save
//
// Before the callback writes over the file it asks the driver for the
// version now. Same as the base: written, as always. Different: the save is
// written BESIDE the file as `<name>.filex-conflict-<time>.<ext>` (the same
// name the desktop app gives its conflict copies), for an editor of the
// session who may create files there, and the editors are told. The file
// keeps the other change.
//
// # The open editor
//
// The editor page asks POST /api/files/onlyoffice/session whether its
// session is still on the current version when the folder's realtime feed
// says the document changed (packages/core PreviewModal), and the person's
// answer comes back the same way: "write mine" moves the base to the version
// now (the save is written over it), "keep the outside version" drops the
// session's save (they chose to throw those edits away).
//
// # Where the record lives
//
// In the database (table office_sessions, migration 00092), so a restart or a
// second filex instance behind the same database still knows which version a
// running session stands on, with filex's in-process cache in front of it
// (internal/memcache, the owner's ruling: the database, and a simple
// in-memory cache in front of it where it is not fast enough - not Redis).
//
// ⚠⚠ The cache only makes the frequent reads cheap (every editing config
// asks whether its session is recorded already). Every DECISION - may this
// save go over the file, has the person dropped it, is the editor's session
// still current - is read from the database (memcache Through.Fresh), because
// another instance's write (a rebase, a drop) is not in this instance's
// cache. A write goes to the database first and to the cache only when the
// database took it.
//
// A row is removed when its session ends (the callback's last save, or
// "closed with no change"), and an hour's sweep removes the ones whose
// session never said so (PruneSessions, after baseTTL).

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/memcache"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// AuditActionSavedConflict: the document changed while an editing session had
// it open, and the session's save was written beside it, not over it.
const AuditActionSavedConflict = "file.office_saved_conflict"

const (
	savedConflictTitle      = "server.onlyoffice.saved_conflict_title"
	savedConflictBody       = "server.onlyoffice.saved_conflict_body"
	refusedConflictNoCreate = "server.onlyoffice.refused_conflict_no_create"
)

// baseTTL is how long a session's record is kept when the session never says
// it ended; the cache in front of it keeps an entry for basesCacheTTL, at most
// basesCacheMax of them; the expired rows are swept at most once per
// pruneEvery.
const (
	baseTTL       = 48 * time.Hour
	basesCacheTTL = 10 * time.Minute
	basesCacheMax = 4096
	pruneEvery    = time.Hour
)

// sessionBase is the version of the document an editing session stands on.
type sessionBase struct {
	nodeID int64
	size   int64
	// mtimeNs is the modification time in Unix nanoseconds (0: none), kept as
	// an integer so it compares exactly after a round trip through any
	// database.
	mtimeNs int64
	etag    string
	// unknown: the session is known to be on an older version, but not on
	// which (see SessionState). Never the current one.
	unknown bool
	// drop: the person chose the outside version; this session's save is not
	// written anywhere.
	drop bool
}

// SaveVerdict is what becomes of a session's save.
type SaveVerdict int

const (
	// SaveCurrent: the session is on the file's current version (or filex
	// knows nothing about it): written over the file.
	SaveCurrent SaveVerdict = iota
	// SaveStale: the file changed since the session opened it: written
	// beside it.
	SaveStale
	// SaveDropped: the person chose the outside version: not written.
	SaveDropped
)

// keyFor is the document key an editing session of the node gets now: the
// version part (which file, which version, how many refused saves) sealed
// with the node's id (sealKey, callback_trust.go), so a callback naming it is
// known to be this document's.
func (s *Service) keyFor(ctx context.Context, node *model.Node) string {
	mtime := int64(0)
	if node.BackendMtime != nil {
		mtime = node.BackendMtime.Unix()
	}
	keyInput := fmt.Sprintf("%d|%s|%d|%d", node.ID, node.PathHash, mtime, node.Size)
	if n := s.refusals(node.ID); n > 0 {
		keyInput += fmt.Sprintf("|refused-%d", n)
	}
	return s.sealKey(ctx, node.ID, md5Hex(keyInput))
}

// ErrNotThisDocument: a session key filex did not make for the document it is
// presented with (KeyBelongsTo).
var ErrNotThisDocument = errors.New("onlyoffice: the session is not this document's")

// md5Hex is the hex MD5 of s - the shape of a document key.
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// mtimeNs is t in Unix nanoseconds, 0 for no time at all.
func mtimeNs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// sessionRows is the office_sessions table as the backing of the cache
// (memcache.Backing): a row past its expiry reads as no row.
type sessionRows struct {
	store db.Store
	now   func() time.Time
}

func (r sessionRows) Load(ctx context.Context, key string) (sessionBase, bool, error) {
	row, err := r.store.GetOfficeSession(ctx, key)
	if err != nil || row == nil || row.ExpiresUnix <= r.now().Unix() {
		return sessionBase{}, false, err
	}
	return baseFromRow(row), true, nil
}

func (r sessionRows) Store(ctx context.Context, key string, b sessionBase) error {
	return r.store.PutOfficeSession(ctx, r.row(key, b))
}

func (r sessionRows) Add(ctx context.Context, key string, b sessionBase) (sessionBase, error) {
	row, err := r.store.AddOfficeSession(ctx, r.row(key, b), r.now())
	if err != nil || row == nil {
		return sessionBase{}, err
	}
	return baseFromRow(row), nil
}

func (r sessionRows) Remove(ctx context.Context, key string) error {
	return r.store.DeleteOfficeSession(ctx, key)
}

func (r sessionRows) row(key string, b sessionBase) *model.OfficeSession {
	return &model.OfficeSession{
		DocKey: key, NodeID: b.nodeID, Size: b.size, MtimeNs: b.mtimeNs, Etag: b.etag,
		Unknown: b.unknown, Dropped: b.drop, ExpiresUnix: r.now().Add(baseTTL).Unix(),
	}
}

func baseFromRow(row *model.OfficeSession) sessionBase {
	return sessionBase{
		nodeID: row.NodeID, size: row.Size, mtimeNs: row.MtimeNs, etag: row.Etag,
		unknown: row.Unknown, drop: row.Dropped,
	}
}

// bases is the record: the database with the cache in front of it, or the
// cache alone for a Service built without a database (unit tests). Built on
// first use, like the probe registry.
func (s *Service) bases() *memcache.Through[string, sessionBase] {
	s.basesOnce.Do(func() {
		var back memcache.Backing[string, sessionBase]
		if s.Store != nil {
			back = sessionRows{store: s.Store, now: time.Now}
		}
		s.basesV = memcache.NewThrough[string, sessionBase](back, memcache.Options{
			MaxEntries: basesCacheMax,
			TTL:        basesCacheTTL,
		})
	})
	return s.basesV
}

// cachedBase is the record of the session with this key, from the cache when
// it has it: for a read that only saves work. nil when there is none (or the
// database could not say).
func (s *Service) cachedBase(ctx context.Context, key string) *sessionBase {
	if key == "" {
		return nil
	}
	b, ok, err := s.bases().Get(ctx, key)
	if err != nil {
		slog.Warn("onlyoffice: could not read a session record", slog.Any("err", err))
		return nil
	}
	if !ok {
		return nil
	}
	return &b
}

// freshBase is the record of the session with this key, from the DATABASE:
// for a decision another instance's write may have changed (see the header).
// A database that cannot answer leaves the cache's entry, if it has one, as
// the best there is - logged, because the decision is then this instance's
// alone.
func (s *Service) freshBase(ctx context.Context, key string) *sessionBase {
	if key == "" {
		return nil
	}
	b, ok, err := s.bases().Fresh(ctx, key)
	if err != nil {
		slog.Warn("onlyoffice: could not read a session record; deciding on this instance's copy", slog.Any("err", err))
		if cb, cok := s.bases().Cache().Get(key); cok {
			return &cb
		}
		return nil
	}
	if !ok {
		return nil
	}
	return &b
}

// putBase records b for key, the database first.
func (s *Service) putBase(ctx context.Context, key string, b sessionBase) error {
	if key == "" {
		return nil
	}
	if err := s.bases().Set(ctx, key, b); err != nil {
		slog.Warn("onlyoffice: could not write a session record", slog.Any("err", err))
		return err
	}
	return nil
}

// forgetBase removes the record of a session that has ended.
func (s *Service) forgetBase(ctx context.Context, key string) {
	if key == "" {
		return
	}
	if err := s.bases().Delete(ctx, key); err != nil {
		slog.Warn("onlyoffice: could not remove a session record; the sweep will", slog.Any("err", err))
	}
}

// sessionOver forgets the session's record when the document server's answer
// ends it: the last save (status 2) handled, or closed with no change.
func (s *Service) sessionOver(ctx context.Context, p CallbackPayload, answer map[string]any) {
	if p.Status == StatusClosedNoChange || (p.Status == StatusReadyForSaving && answer["error"] == 0) {
		s.forgetBase(ctx, keyOf(p))
	}
}

// PruneSessions removes the records whose session never said it ended, at
// most once per pruneEvery however often it is called (the server calls it
// from a maintenance tick).
func (s *Service) PruneSessions(ctx context.Context) {
	if s == nil {
		return
	}
	now := time.Now()
	last := s.prunedAt.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < pruneEvery {
		return
	}
	if !s.prunedAt.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	s.bases().Cache().Expire()
	if s.Store == nil {
		return
	}
	n, err := s.Store.PruneOfficeSessions(ctx, now)
	if err != nil {
		slog.Warn("onlyoffice: could not sweep the session records", slog.Any("err", err))
		return
	}
	if n > 0 {
		slog.Info("onlyoffice: swept session records whose session never ended", slog.Int64("rows", n))
	}
}

// baseFromObject is the version the driver reports, as a base.
func baseFromObject(nodeID int64, obj storage.Object) sessionBase {
	return sessionBase{nodeID: nodeID, size: obj.Size, mtimeNs: mtimeNs(obj.Mtime), etag: obj.Etag}
}

// sameVersion: the driver reports the version the base names. The etag when
// both have one (an object store), else size and mtime (a disk).
func sameVersion(b *sessionBase, obj storage.Object) bool {
	if b.unknown || b.size != obj.Size {
		return false
	}
	if b.etag != "" && obj.Etag != "" {
		return b.etag == obj.Etag
	}
	return b.mtimeNs == mtimeNs(obj.Mtime)
}

// noteBase records the version an editing session opened on
// (BuildConfigForNode). The first record of a key stands: a second opener
// joins the running session (the database keeps the first writer's row, on
// every instance).
func (s *Service) noteBase(ctx context.Context, key string, node *model.Node) {
	if key == "" || node == nil || s.StorageResolver == nil || s.cachedBase(ctx, key) != nil {
		return
	}
	drv, err := s.StorageResolver(node.StorageID)
	if err != nil || drv == nil {
		return
	}
	obj, err := drv.Stat(ctx, node.Path)
	if err != nil {
		// Not answerable now (a staged upload still on its way): no record,
		// and the save is written as it always was.
		return
	}
	if _, err := s.bases().Add(ctx, key, baseFromObject(node.ID, obj)); err != nil {
		slog.Warn("onlyoffice: could not record a session's version", slog.Any("err", err))
	}
}

// advanceBase moves a session's base to the version its own save left.
func (s *Service) advanceBase(ctx context.Context, key string, node *model.Node, obj storage.Object) {
	b := s.cachedBase(ctx, key)
	if b == nil || b.nodeID != node.ID {
		return
	}
	_ = s.putBase(ctx, key, baseFromObject(node.ID, obj))
}

// dropped: the person chose the outside version over this session's edits.
// Read from the database (a decision).
func (s *Service) dropped(ctx context.Context, node *model.Node, key string) bool {
	b := s.freshBase(ctx, key)
	return b != nil && b.nodeID == node.ID && b.drop
}

// verdict is what becomes of the save of the session with this key. Read from
// the database (a decision).
func (s *Service) verdict(ctx context.Context, drv storage.Driver, node *model.Node, key string) SaveVerdict {
	b := s.freshBase(ctx, key)
	if b == nil || b.nodeID != node.ID {
		return SaveCurrent
	}
	if b.drop {
		return SaveDropped
	}
	if b.unknown {
		return SaveStale
	}
	obj, err := drv.Stat(ctx, node.Path)
	if err != nil {
		// The file's own checks (EnsureFileTarget, the write) say what is
		// wrong with it.
		return SaveCurrent
	}
	if sameVersion(b, obj) {
		return SaveCurrent
	}
	return SaveStale
}

// SessionState answers the editor page: is the session with this key still on
// the document's current version? known is false when filex has no record of
// it (one that expired, or was opened by a filex older than its table) and
// the answer comes from the key the document would get now.
//
// A key filex did not make for this document (KeyBelongsTo) is answered
// "current, unknown" and nothing is recorded for it: a person who may view
// one document cannot leave a record under another document's key.
func (s *Service) SessionState(ctx context.Context, node *model.Node, key string) (stale, known bool) {
	if !s.KeyBelongsTo(ctx, key, node) {
		return false, false
	}
	if b := s.freshBase(ctx, key); b != nil && b.nodeID == node.ID {
		if b.drop {
			return true, true
		}
		drv, err := s.StorageResolver(node.StorageID)
		if err != nil || drv == nil {
			return false, true
		}
		return s.verdict(ctx, drv, node, key) == SaveStale, true
	}
	if key == s.keyFor(ctx, node) {
		return false, false
	}
	// On an older version, which one unknown: recorded, so the session's
	// save is kept beside the file rather than written over it.
	_, _ = s.bases().Add(ctx, key, sessionBase{nodeID: node.ID, unknown: true})
	return true, false
}

// RebaseSession: the person chose to write their version over the outside
// one. The session's base becomes the version now, so its save is written
// over it - over THAT version only: a change after this answer makes the
// session stale again.
func (s *Service) RebaseSession(ctx context.Context, node *model.Node, key string) error {
	if key == "" || s.StorageResolver == nil {
		return fmt.Errorf("onlyoffice: no session")
	}
	if !s.KeyBelongsTo(ctx, key, node) {
		return ErrNotThisDocument
	}
	drv, err := s.StorageResolver(node.StorageID)
	if err != nil {
		return err
	}
	obj, err := drv.Stat(ctx, node.Path)
	if err != nil {
		return err
	}
	return s.putBase(ctx, key, baseFromObject(node.ID, obj))
}

// DropSession: the person chose the outside version; the session's save, when
// it comes, is not written.
func (s *Service) DropSession(ctx context.Context, node *model.Node, key string) error {
	if !s.KeyBelongsTo(ctx, key, node) {
		return ErrNotThisDocument
	}
	b := s.freshBase(ctx, key)
	if b == nil || b.nodeID != node.ID {
		b = &sessionBase{nodeID: node.ID, unknown: true}
	}
	b.drop = true
	return s.putBase(ctx, key, *b)
}

// conflictName is `<stem>.filex-conflict-<UTC time>.<ext>` - the desktop
// app's conflict copy name too (desktop/src/openwith.ts conflictPathFor).
func conflictName(name string, now time.Time) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem, ext = name, ""
	}
	return stem + ".filex-conflict-" + now.UTC().Format("20060102T150405") + ext
}

// saveConflict writes a stale session's save beside the document as a
// conflict copy, for one of the session's editors who may create it there.
func (s *Service) saveConflict(ctx context.Context, drv storage.Driver, writer storage.Writer, node *model.Node, src io.Reader, length int64, key string, users []string) (map[string]any, error) {
	target, err := s.freeBeside(ctx, drv, node, path.Join(path.Dir(node.Path), conflictName(node.Name, time.Now())))
	if err != nil {
		return map[string]any{"error": 1, "message": "no free name beside the document"}, nil
	}
	ext := docExt(node.Name)
	slog.Info("onlyoffice callback: the document changed while it was edited",
		slog.Int64("storage", node.StorageID), slog.String("path", node.Path), slog.String("saved", target))
	return s.writeBeside(ctx, drv, writer, node, src, length, key, users, besideSave{
		target:   target,
		got:      ext,
		mime:     assoc.MimeOf(ext),
		titleKey: savedConflictTitle,
		bodyKey:  savedConflictBody,
		vars:     srvtext.Vars{"name": node.Name, "saved": path.Base(target)},
		action:   AuditActionSavedConflict,
		why:      "changed while it was edited",
		noCreate: refusedConflictNoCreate,
	})
}
