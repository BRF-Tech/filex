package handlers

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/archivecli"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/writegate"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Archive handles zip-listing, zip-extract, and zip-create operations.
//
// All zip ops materialize the source archive to a tmp file (since
// archive/zip needs an io.ReaderAt + Seeker), then stream extracts back
// to storage.
type Archive struct {
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
	ACL             *acl.Resolver
	// Body resolves where a member's bytes are: the driver, or filex's
	// staging area while a staged upload is still transferring. Nil-safe.
	Body *filebody.Resolver
	// Index and Thumbs feed the shared catalogue bookkeeper below. Both
	// optional.
	Index  *search.Index
	Thumbs *thumb.Pipeline
	// Tickets holds the minted "download this selection as one archive"
	// authorizations (archive_download.go). Nil disables that endpoint pair
	// rather than crashing it.
	Tickets *archiveTicketStore
	// Engine supplies optional 7-Zip/RAR processing and the live archive
	// policy. Nil keeps the historical built-in ZIP-only behaviour.
	Engine *archivecli.Service
	// Ops moves expensive archive creation out of the request. Nil retains the
	// synchronous path for lightweight embedders and focused handler tests.
	Ops *ops.Service
}

// AttachSearchIndex / AttachThumbs wire the two optional halves of the
// bookkeeper.
func (a *Archive) AttachSearchIndex(i *search.Index) { a.Index = i }

// AttachThumbs wires thumbnail generation for extracted members.
func (a *Archive) AttachThumbs(p *thumb.Pipeline) { a.Thumbs = p }

// sync returns the shared catalogue bookkeeper (internal/protocolsync) — row,
// search index, thumbnail, write hook and realtime change frame in one call.
//
// ⚠ Extract and Add wrote bytes to the driver and then did NOTHING ELSE: no
// node row, no index document, no event, no frame. The file existed on the
// storage and was invisible to every read path in filex until the next
// periodic sync walked the folder — which on an `ondemand` storage is never.
// Origin is "manager" because these two endpoints are the browser file
// manager's own Extract/Compress, reached from the SPA under a user session;
// they are not a new protocol.
func (a *Archive) sync() *protocolsync.Syncer {
	return protocolsync.New(a.Store, a.Index, a.Thumbs, writehook.OriginManager).WithResolver(a.StorageResolver)
}

// storageRow fetches the storage record the bookkeeper needs. A miss returns
// nil and every caller degrades to "bytes written, catalogue not updated" —
// which is exactly the old behaviour, so a lookup failure cannot make things
// worse than they were.
func (a *Archive) storageRow(ctx context.Context, id int64) *model.Storage {
	st, err := a.Store.GetStorage(ctx, id)
	if err != nil {
		return nil
	}
	return st
}

// AttachBody wires the byte-source resolver so a file that is still being
// transferred can be zipped/extracted like any other.
func (a *Archive) AttachBody(b *filebody.Resolver) { a.Body = b }

// AttachArchiveEngine wires optional external archive providers.
func (a *Archive) AttachArchiveEngine(engine *archivecli.Service) { a.Engine = engine }

// AttachOps wires the shared long-running file-operation queue.
func (a *Archive) AttachOps(service *ops.Service) { a.Ops = service }

// NewArchive constructs an Archive handler.
func NewArchive(store db.Store, resolver func(int64) (storage.Driver, error)) *Archive {
	return &Archive{Store: store, StorageResolver: resolver}
}

// AttachACL wires the RBAC resolver: list needs ≥viewer on the archive,
// extract/add need ≥editor on the write target (+ ≥viewer on sources read).
func (a *Archive) AttachACL(r *acl.Resolver) { a.ACL = r }

// archiveRequest is the union body for /api/files/archive/{list,extract,add}.
type archiveRequest struct {
	StorageID int64      `json:"storage_id"`
	Path      string     `json:"path"`
	Members   []string   `json:"members,omitempty"`
	DestDir   string     `json:"dest,omitempty"`
	Files     []addEntry `json:"files,omitempty"`
	Password  string     `json:"password,omitempty"`
}

// addEntry is one source for /archive/add.
//
// Source is the path inside the storage to read from; Name is the
// destination path inside the zip.
type addEntry struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// archiveListEntry is the wire format for /archive/list responses.
type archiveListEntry struct {
	Name  string    `json:"name"`
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
	IsDir bool      `json:"is_dir"`
}

// List enumerates archive members.
func (a *Archive) List(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path"})
		return
	}
	storageID, rel, err := a.resolveStorage(r.Context(), req.StorageID, req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req.StorageID = storageID
	req.Path = rel
	if !aclAllowID(r.Context(), a.ACL, a.Store, storageID, rel, acl.LevelViewer) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}
	tmp, err := a.fetchToTemp(r, req.StorageID, req.Path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer os.Remove(tmp)
	if a.Engine != nil && (archivecli.FormatFromPath(req.Path) != "zip" || req.Password != "") {
		entries, err := a.Engine.ListAs(r.Context(), tmp, req.Path, req.Password)
		if err != nil {
			writeArchiveProviderError(w, err)
			return
		}
		if req.Password == "" && archiveEntriesEncrypted(entries) {
			writeArchiveProviderError(w, archivecli.ErrPasswordRequired)
			return
		}
		if req.Password != "" {
			if err := a.Engine.Test(r.Context(), tmp, req.Password); err != nil {
				writeArchiveProviderError(w, err)
				return
			}
		}
		policy := a.Engine.Policy(r.Context())
		if len(entries) > policy.MaxEntries {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "archive contains too many entries", "max": policy.MaxEntries})
			return
		}
		var total int64
		for _, entry := range entries {
			total += entry.Size
		}
		if total > policy.MaxExpandedBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "archive expands beyond the configured limit", "max_bytes": policy.MaxExpandedBytes})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
		return
	}

	zr, err := zip.OpenReader(tmp)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a zip: " + err.Error()})
		return
	}
	defer zr.Close()
	if zipHasEncryptedMembers(zr.File) {
		writeArchiveProviderError(w, archivecli.ErrPasswordRequired)
		return
	}

	out := make([]archiveListEntry, 0, len(zr.File))
	for _, f := range zr.File {
		out = append(out, archiveListEntry{
			Name:  f.Name,
			Size:  int64(f.UncompressedSize64),
			Mtime: f.Modified,
			IsDir: strings.HasSuffix(f.Name, "/"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

// Extract pulls members out of an archive into the destination directory.
//
// DestDir is interpreted on the SAME storage as the source. Members
// defaults to "all" when empty.
func (a *Archive) Extract(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path"})
		return
	}
	storageID, rel, err := a.resolveStorage(r.Context(), req.StorageID, req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req.StorageID = storageID
	req.Path = rel
	if req.DestDir != "" {
		destStorageID, destRel, err := a.resolveStorage(r.Context(), storageID, req.DestDir)
		if err != nil || destStorageID != storageID {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "archive destination must be on the same storage"})
			return
		}
		req.DestDir = destRel
	}
	drv, err := a.StorageResolver(req.StorageID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage"})
		return
	}
	writer, ok := drv.(storage.Writer)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "storage not writable"})
		return
	}
	dest := req.DestDir
	if dest == "" {
		dest = path.Dir(req.Path)
	}
	dest = "/" + strings.TrimLeft(path.Clean("/"+dest), "/")
	req.DestDir = dest

	// The archive is read and the destination gains files: both named, not
	// changed. Each member is judged where it lands, below.
	if gate(w, r, a.ACL, req.StorageID, writegate.Names(req.Path), writegate.Names(dest)) {
		return
	}
	// Authorization stays at submission time. The worker restores the actor on
	// its context for catalogue/audit side effects but must not make a fresh
	// authorization decision after the request has gone away.
	if !aclAllowID(r.Context(), a.ACL, a.Store, req.StorageID, strings.Trim(req.Path, "/"), acl.LevelViewer) ||
		!aclAllowID(r.Context(), a.ACL, a.Store, req.StorageID, strings.Trim(dest, "/"), acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}

	tmp, err := a.fetchToTemp(r, req.StorageID, req.Path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	plan, err := a.inspectArchive(r.Context(), tmp, req)
	if err != nil {
		_ = os.Remove(tmp)
		writeArchiveProviderError(w, err)
		return
	}

	if a.Ops != nil {
		jobReq := req
		jobTmp := tmp
		var actor *model.User
		if current := auth.UserFrom(r.Context()); current != nil {
			copy := *current
			actor = &copy
		}
		op, err := a.Ops.SubmitJobWithCleanup(r.Context(), ops.OpArchiveExtract, req.StorageID,
			[]string{req.Path}, dest, max(1, plan.files),
			func(ctx context.Context, progress func(done int)) error {
				if actor != nil {
					ctx = auth.WithUser(ctx, actor)
				}
				_, err := a.extractArchive(ctx, jobReq, drv, writer, jobTmp, plan.external, progress)
				return err
			}, func() { _ = os.Remove(jobTmp) })
		if err != nil {
			_ = os.Remove(tmp)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"op": op})
		return
	}

	defer os.Remove(tmp)
	result, err := a.extractArchive(r.Context(), req, drv, writer, tmp, plan.external, func(int) {})
	if err != nil {
		writeArchiveProviderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type archiveExtractPlan struct {
	external bool
	files    int
}

// inspectArchive deliberately runs before an extraction job is accepted. It
// provides the total for honest progress and, crucially, reports encrypted
// archives while the initiating request is still alive so the client can ask
// for a password and retry instead of queuing a doomed background operation.
func (a *Archive) inspectArchive(ctx context.Context, archivePath string, req archiveRequest) (archiveExtractPlan, error) {
	wanted := make(map[string]bool, len(req.Members))
	for _, member := range req.Members {
		wanted[member] = true
	}
	count := func(name string, isDir bool) bool {
		if isDir {
			return false
		}
		return len(wanted) == 0 || wanted[name] || wanted[strings.TrimSuffix(name, "/")]
	}
	checkExternal := func() (archiveExtractPlan, error) {
		if a.Engine == nil {
			return archiveExtractPlan{}, archivecli.ErrUnavailable
		}
		entries, err := a.Engine.ListAs(ctx, archivePath, req.Path, req.Password)
		if err != nil {
			return archiveExtractPlan{}, err
		}
		if req.Password == "" && archiveEntriesEncrypted(entries) {
			return archiveExtractPlan{}, archivecli.ErrPasswordRequired
		}
		if req.Password != "" {
			if err := a.Engine.Test(ctx, archivePath, req.Password); err != nil {
				return archiveExtractPlan{}, err
			}
		}
		policy := a.Engine.Policy(ctx)
		var expanded int64
		files := 0
		for _, entry := range entries {
			if _, err := sanitizeZipPath(entry.Name); err != nil {
				return archiveExtractPlan{}, fmt.Errorf("%w: archive contains an unsafe member path", archivecli.ErrUnsupported)
			}
			if entry.IsLink {
				return archiveExtractPlan{}, fmt.Errorf("%w: archive contains a link entry", archivecli.ErrUnsupported)
			}
			if !entry.IsDir {
				expanded += entry.Size
			}
			if count(entry.Name, entry.IsDir) {
				files++
			}
		}
		if len(entries) > policy.MaxEntries || expanded > policy.MaxExpandedBytes {
			return archiveExtractPlan{}, fmt.Errorf("%w: extraction limit exceeded", archivecli.ErrLimits)
		}
		return archiveExtractPlan{external: true, files: files}, nil
	}

	if archivecli.FormatFromPath(req.Path) != "zip" || req.Password != "" {
		return checkExternal()
	}
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return archiveExtractPlan{}, fmt.Errorf("%w: not a zip: %v", archivecli.ErrUnsupported, err)
	}
	defer zr.Close()
	if zipHasEncryptedMembers(zr.File) {
		if req.Password == "" {
			return archiveExtractPlan{}, archivecli.ErrPasswordRequired
		}
		return checkExternal()
	}
	var expanded uint64
	files := 0
	for _, file := range zr.File {
		if _, err := sanitizeZipPath(file.Name); err != nil {
			return archiveExtractPlan{}, fmt.Errorf("%w: archive contains an unsafe member path", archivecli.ErrUnsupported)
		}
		mode := file.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return archiveExtractPlan{}, fmt.Errorf("%w: archive contains a link or special entry", archivecli.ErrUnsupported)
		}
		expanded += file.UncompressedSize64
		if count(file.Name, strings.HasSuffix(file.Name, "/")) {
			files++
		}
	}
	if a.Engine != nil {
		policy := a.Engine.Policy(ctx)
		if len(zr.File) > policy.MaxEntries || expanded > uint64(policy.MaxExpandedBytes) {
			return archiveExtractPlan{}, fmt.Errorf("%w: extraction limit exceeded", archivecli.ErrLimits)
		}
	}
	return archiveExtractPlan{files: files}, nil
}

func archiveEntriesEncrypted(entries []archivecli.Entry) bool {
	for _, entry := range entries {
		if entry.Encrypted {
			return true
		}
	}
	return false
}

func (a *Archive) extractArchive(ctx context.Context, req archiveRequest, drv storage.Driver, writer storage.Writer, archivePath string, external bool, progress func(int)) (map[string]any, error) {
	if external {
		return a.extractExternal(ctx, req, drv, writer, archivePath, progress)
	}
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("not a zip: %w", err)
	}
	defer zr.Close()
	wanted := map[string]bool{}
	for _, member := range req.Members {
		wanted[member] = true
	}
	mkdirer, _ := drv.(storage.Mkdirer)
	st := a.storageRow(ctx, req.StorageID)
	var locks writegate.Locks
	if a.ACL != nil {
		locks = a.ACL.Locks(ctx, req.StorageID)
	}
	// locked counts members that would have landed on a document an app has
	// frozen (writegate) — skipped, and said, rather than written over it.
	locked := 0
	sy := a.sync()
	keys := make([]string, 0)
	refused := 0
	for _, file := range zr.File {
		if err := ctx.Err(); err != nil {
			return map[string]any{"keys": keys, "count": len(keys), "refused": refused}, err
		}
		if len(wanted) > 0 && !wanted[file.Name] && !wanted[strings.TrimSuffix(file.Name, "/")] {
			continue
		}
		safeRel, err := sanitizeZipPath(file.Name)
		if err != nil {
			slog.Warn("archive: skipped zip-slip entry", slog.String("name", file.Name), slog.String("err", err.Error()))
			continue
		}
		target := path.Join(req.DestDir, safeRel)
		// A member under one of filex's own names (`.versions/…`, `.thumbs/`,
		// a `.keepdir`) is skipped like a zip-slip entry: the destination is
		// fine, this one member is not — and extracting it would put files
		// where nobody can ever see them. So is a member that would land on a
		// document an app has frozen (measured before writegate: an archive
		// holding `Sozlesmeler/NDA.docx` replaced the document under
		// signature).
		if gerr := writegate.Check(locks, 0, writegate.Writes(target)); gerr != nil {
			slog.Warn("archive: skipped member", slog.String("name", file.Name), slog.String("why", gerr.Error()))
			if errors.Is(gerr, writegate.ErrLocked) {
				locked++
			}
			continue
		}
		// Defense in depth: ensure the joined target stays under dest.
		if !strings.HasPrefix(target+"/", strings.TrimRight(req.DestDir, "/")+"/") {
			slog.Warn("archive: target escapes dest after join", slog.String("target", target))

			continue
		}
		if strings.HasSuffix(file.Name, "/") {
			if mkdirer != nil && storage.EnsureDirTarget(ctx, drv, target) == nil {
				_ = mkdirer.Mkdir(ctx, target)
				if st != nil {
					sy.Mkdir(ctx, st, target)
				}
			}
			continue
		}
		if err := storage.EnsureFileTarget(ctx, drv, target); err != nil {
			continue
		}
		existed := storage.Exists(ctx, drv, target)
		if err := writehook.BeforeOverwrite(ctx, req.StorageID, target); err != nil {
			refused++
			continue
		}
		rc, err := file.Open()
		if err != nil {
			continue
		}
		err = writer.Write(ctx, target, &contextReader{ctx: ctx, reader: rc}, int64(file.UncompressedSize64))
		_ = rc.Close()
		if err != nil {
			if ctx.Err() != nil {
				removeIncomplete(ctx, drv, target, existed)
				return map[string]any{"keys": keys, "count": len(keys), "refused": refused}, ctx.Err()
			}
			slog.Warn("archive: extract write", slog.String("target", target), slog.String("err", err.Error()))
			continue
		}
		if st != nil {
			sy.WriteWithoutNotification(ctx, st, target, int64(file.UncompressedSize64), mimeByExt(target))
		}
		keys = append(keys, target)
		progress(len(keys))
	}
	if len(keys) == 0 && refused > 0 {
		return nil, errors.New("could not preserve one or more existing files; nothing was written")
	}
	a.emitArchiveExtracted(ctx, req.StorageID, req.Path, req.DestDir, len(keys))
	return map[string]any{"keys": keys, "count": len(keys), "refused": refused, "locked": locked}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func removeIncomplete(ctx context.Context, drv storage.Driver, target string, existed bool) {
	if existed {
		return
	}
	if deleter, ok := drv.(storage.Deleter); ok {
		_ = deleter.Delete(context.WithoutCancel(ctx), target)
	}
}

func zipHasEncryptedMembers(files []*zip.File) bool {
	for _, file := range files {
		if file.Flags&0x1 != 0 {
			return true
		}
	}
	return false
}

// Add packs members into a (new or existing) zip archive on the same storage.
//
// If the destination zip exists, we download it, append the new entries,
// then re-upload. Names are zip-slip protected on the read side.
func (a *Archive) Add(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Path == "" || len(req.Files) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path or files"})
		return
	}
	storageID, rel, err := a.resolveStorage(r.Context(), req.StorageID, req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req.StorageID = storageID
	req.Path = rel
	// Source paths in `Files[].Source` may also carry adapter prefixes —
	// strip them and assume same storage as the target archive.
	for i := range req.Files {
		_, srcRel := splitAdapterPath(req.Files[i].Source)
		if srcRel != "" {
			req.Files[i].Source = srcRel
		}
	}
	if gate(w, r, a.ACL, req.StorageID, writegate.Writes(req.Path)) {
		return
	}
	for _, f := range req.Files {
		if gate(w, r, a.ACL, req.StorageID, writegate.Names(f.Source)) {
			return
		}
	}
	// RBAC: writing the archive needs ≥editor on the target; each source ≥viewer.
	if !aclAllowID(r.Context(), a.ACL, a.Store, req.StorageID, strings.Trim(req.Path, "/"), acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}
	for _, f := range req.Files {
		if !aclAllowID(r.Context(), a.ACL, a.Store, req.StorageID, strings.Trim(f.Source, "/"), acl.LevelViewer) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission: " + f.Source})
			return
		}
	}
	drv, err := a.StorageResolver(req.StorageID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage"})
		return
	}
	writer, ok := drv.(storage.Writer)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "storage not writable"})
		return
	}

	// Try to fetch the existing zip — non-fatal if missing (we just create one).
	var existingMembers []*zip.File
	var existingTmp string
	if tmp, err := a.fetchToTemp(r, req.StorageID, req.Path); err == nil {
		existingTmp = tmp
		zr, zerr := zip.OpenReader(tmp)
		if zerr == nil {
			existingMembers = append(existingMembers, zr.File...)
			defer zr.Close()
		} else {
			slog.Warn("archive: existing zip unreadable, overwriting", slog.String("err", zerr.Error()))
		}
	}
	if existingTmp != "" {
		defer os.Remove(existingTmp)
	}

	// New tmp file we'll stream the rebuilt archive into.
	tmp, err := os.CreateTemp("", "filex-zip-add-*.zip")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	tmpName := tmp.Name()
	// Remove registered BEFORE Close so the defers unwind Close-then-Remove:
	// closed, then deleted, never the reverse. Every early return below
	// (zw.Close, tmp.Seek, EnsureFileTarget, BeforeOverwrite, writer.Write)
	// used to leak this fd -- only the success path closed it. The former
	// trailing `_ = tmp.Close()` is redundant with this and has been dropped.
	defer os.Remove(tmpName)
	defer tmp.Close()

	zw := zip.NewWriter(tmp)
	addedNames := map[string]bool{}
	for _, e := range req.Files {
		safe, err := sanitizeZipPath(e.Name)
		if err != nil {
			continue
		}
		src, err := a.Body.Resolve(r.Context(), drv, req.StorageID, e.Source, nil)
		if err != nil {
			slog.Warn("archive: source resolve", slog.String("source", e.Source), slog.String("err", err.Error()))
			continue
		}
		rc, err := src.Open(r.Context())
		if err != nil {
			slog.Warn("archive: source read", slog.String("source", e.Source), slog.String("err", err.Error()))
			continue
		}
		fw, err := zw.Create(safe)
		if err != nil {
			_ = rc.Close()
			continue
		}
		if _, err := io.Copy(fw, rc); err != nil {
			_ = rc.Close()
			slog.Warn("archive: copy member", slog.String("err", err.Error()))
			continue
		}
		_ = rc.Close()
		addedNames[safe] = true
	}
	for _, f := range existingMembers {
		if addedNames[f.Name] {
			continue // overwrite by name
		}
		fw, err := zw.CreateRaw(&f.FileHeader)
		if err != nil {
			continue
		}
		rc, err := f.OpenRaw()
		if err != nil {
			continue
		}
		_, _ = io.Copy(fw, rc)
	}
	if err := zw.Close(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	stat, _ := tmp.Stat()
	// Writing the archive onto an existing folder name would leave `X` and
	// `X/…` side by side on an object store (storage.ErrKindConflict).
	if err := storage.EnsureFileTarget(r.Context(), drv, req.Path); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": err.Error()})
		return
	}
	// The last moment at which the bytes this archive is about to replace
	// still exist -- see writehook/overwrite.go.
	if err := writehook.BeforeOverwrite(r.Context(), req.StorageID, req.Path); err != nil {
		slog.Warn("archive add refused: snapshot",
			slog.Int64("storage", req.StorageID),
			slog.String("path", req.Path),
			slog.String("err", err.Error()))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "could not preserve the existing file: " + err.Error(),
			"code":  "SNAPSHOT_FAILED",
		})
		return
	}
	if err := writer.Write(r.Context(), req.Path, tmp, stat.Size()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if st := a.storageRow(r.Context(), req.StorageID); st != nil {
		a.sync().Write(r.Context(), st, req.Path, stat.Size(), "application/zip")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": req.Path,
		"size": stat.Size(),
	})
}

// resolveStorage takes the SFC's `path` (which may be either a bare
// relative path or `<adapter>://<rel>`) plus an optional `storage_id`
// and returns the resolved (storage_id, relative_path) pair.
//
// Order of precedence:
//  1. Explicit `storage_id` in the body (legacy embed.js).
//  2. `<adapter>://` prefix on `path`.
//  3. Fall back to storages[0].
func (a *Archive) resolveStorage(ctx context.Context, explicitID int64, fullPath string) (int64, string, error) {
	adapter, rel := splitAdapterPath(fullPath)
	if explicitID > 0 {
		return explicitID, strings.Trim(rel, "/"), nil
	}
	storages, err := a.Store.ListEnabledStorages(ctx)
	if err != nil {
		return 0, "", err
	}
	if len(storages) == 0 {
		return 0, "", errors.New("no storages configured")
	}
	if adapter == "" {
		adapter = storages[0].Name
	}
	for _, s := range storages {
		if s.Name == adapter {
			return s.ID, strings.Trim(rel, "/"), nil
		}
	}
	return 0, "", fmt.Errorf("unknown adapter: %s", adapter)
}

// fetchToTemp pulls a remote object into a local tmp file and returns the path.
func (a *Archive) fetchToTemp(r *http.Request, storageID int64, p string) (string, error) {
	drv, err := a.StorageResolver(storageID)
	if err != nil {
		return "", err
	}
	src, err := a.Body.Resolve(r.Context(), drv, storageID, p, nil)
	if err != nil {
		return "", err
	}
	rc, err := src.Open(r.Context())
	if err != nil {
		return "", err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp("", "filex-arc-*.zip")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, rc); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}

// sanitizeZipPath enforces zip-slip protection.
//
// Rules:
//   - Replace backslashes (Windows-authored zips) with forward slashes
//   - Reject absolute paths (drive letters, leading "/")
//   - Reject any path that resolves to "..", "." escapes, or that contains
//     a literal ".." component
//   - Strip leading "./"
//
// Returns the cleaned RELATIVE path or an error.
func sanitizeZipPath(name string) (string, error) {
	if name == "" {
		return "", errors.New("empty entry name")
	}
	clean := strings.ReplaceAll(name, `\`, `/`)
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimLeft(clean, "/")
	if clean == "" {
		return "", errors.New("empty after sanitize")
	}
	// Drive letters (e.g. "C:foo") — uncommon but possible from Windows zips.
	if len(clean) >= 2 && clean[1] == ':' {
		return "", fmt.Errorf("absolute path: %q", name)
	}
	// Component check.
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("parent traversal: %q", name)
		}
	}
	// Final clean — guarantees no "." segments.
	clean = strings.TrimLeft(path.Clean("/"+clean), "/")
	if clean == "" || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("clean rejected: %q", name)
	}
	return clean, nil
}
