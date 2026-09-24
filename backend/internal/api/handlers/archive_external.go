package handlers

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/archivecli"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

type archiveCreateRequest struct {
	StorageID    int64    `json:"storage_id,omitempty"`
	Dest         string   `json:"dest"`
	Sources      []string `json:"sources"`
	Format       string   `json:"format,omitempty"`
	Password     string   `json:"password,omitempty"`
	EncryptNames bool     `json:"encrypt_filenames,omitempty"`
	Compression  int      `json:"compression,omitempty"`
	Solid        *bool    `json:"solid,omitempty"`
	DictionaryMB int      `json:"dictionary_size_mb,omitempty"`
}

func archiveErrorCode(err error) (int, string) {
	switch {
	case errors.Is(err, archivecli.ErrPasswordRequired):
		return http.StatusUnauthorized, "PASSWORD_REQUIRED"
	case errors.Is(err, archivecli.ErrBadPassword):
		return http.StatusUnauthorized, "BAD_PASSWORD"
	case errors.Is(err, archivecli.ErrUnsupported):
		return http.StatusBadRequest, "UNSUPPORTED_FORMAT"
	case errors.Is(err, archivecli.ErrLimits):
		return http.StatusRequestEntityTooLarge, "ARCHIVE_LIMIT_EXCEEDED"
	case errors.Is(err, archivecli.ErrUnavailable):
		return http.StatusServiceUnavailable, "PROVIDER_UNAVAILABLE"
	default:
		return http.StatusBadGateway, "ARCHIVE_PROVIDER_FAILED"
	}
}

func writeArchiveProviderError(w http.ResponseWriter, err error) {
	status, code := archiveErrorCode(err)
	slog.Warn("archive provider request failed", slog.String("code", code), slog.String("err", err.Error()))
	message := map[string]string{
		"PASSWORD_REQUIRED":       "archive password is required",
		"BAD_PASSWORD":            "incorrect archive password",
		"UNSUPPORTED_FORMAT":      "archive format is not supported",
		"ARCHIVE_LIMIT_EXCEEDED":  "archive exceeds the configured limits",
		"PROVIDER_UNAVAILABLE":    "archive provider is unavailable",
		"ARCHIVE_PROVIDER_FAILED": "archive provider failed",
	}[code]
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}

func (a *Archive) archiveWorkDir(prefix string) (string, error) {
	base := ""
	if a.Engine != nil {
		base = a.Engine.WorkDir()
	}
	if base != "" {
		if err := os.MkdirAll(base, 0o700); err != nil {
			return "", err
		}
	}
	return os.MkdirTemp(base, prefix)
}

func (a *Archive) extractExternal(ctx context.Context, req archiveRequest, drv storage.Driver, writer storage.Writer, archivePath string, progress func(int)) (map[string]any, error) {
	policy := a.Engine.Policy(ctx)
	if !policy.Enabled {
		return nil, archivecli.ErrUnavailable
	}
	entries, err := a.Engine.ListAs(ctx, archivePath, req.Path, req.Password)
	if err != nil {
		return nil, err
	}
	var declared int64
	for _, entry := range entries {
		if _, err := sanitizeZipPath(entry.Name); err != nil {
			return nil, errors.New("archive contains an unsafe member path")
		}
		if !entry.IsDir {
			declared += entry.Size
		}
	}
	if len(entries) > policy.MaxEntries || declared > policy.MaxExpandedBytes {
		return nil, fmt.Errorf("%w: extraction limit exceeded", archivecli.ErrLimits)
	}
	root, err := a.archiveWorkDir("filex-archive-extract-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	if err := a.Engine.ExtractAs(ctx, archivePath, req.Path, root, req.Password, req.Members); err != nil {
		return nil, err
	}
	dest := req.DestDir
	type extracted struct {
		local string
		rel   string
		size  int64
	}
	files := make([]extracted, 0, len(entries))
	var actual int64
	err = filepath.WalkDir(root, func(local string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if local == root {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
			return errors.New("archive contains a link or special file")
		}
		rel, err := filepath.Rel(root, local)
		if err != nil {
			return err
		}
		rel, err = sanitizeZipPath(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			actual += info.Size()
			if actual > policy.MaxExpandedBytes || len(files)+1 > policy.MaxEntries {
				return fmt.Errorf("%w: extraction limit exceeded", archivecli.ErrLimits)
			}
			files = append(files, extracted{local: local, rel: rel, size: info.Size()})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	mkdirer, _ := drv.(storage.Mkdirer)
	st := a.storageRow(ctx, req.StorageID)
	sy := a.sync()
	keys := make([]string, 0, len(files))
	refused := 0
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return map[string]any{"keys": keys, "count": len(keys), "refused": refused}, err
		}
		target := path.Join(dest, file.rel)
		if err := storage.EnsureFileTarget(ctx, drv, target); err != nil {
			continue
		}
		existed := storage.Exists(ctx, drv, target)
		if mkdirer != nil {
			parent := path.Dir(target)
			_ = mkdirer.Mkdir(ctx, parent)
			if st != nil {
				sy.Mkdir(ctx, st, parent)
			}
		}
		if err := writehook.BeforeOverwrite(ctx, req.StorageID, target); err != nil {
			refused++
			continue
		}
		f, err := os.Open(file.local)
		if err != nil {
			continue
		}
		err = writer.Write(ctx, target, &contextReader{ctx: ctx, reader: f}, file.size)
		_ = f.Close()
		if err != nil {
			if ctx.Err() != nil {
				removeIncomplete(ctx, drv, target, existed)
				return map[string]any{"keys": keys, "count": len(keys), "refused": refused}, ctx.Err()
			}
			slog.Warn("archive: external extract write", slog.String("target", target), slog.String("err", err.Error()))
			continue
		}
		if st != nil {
			sy.WriteWithoutNotification(ctx, st, target, file.size, mimeByExt(target))
		}
		keys = append(keys, target)
		progress(len(keys))
	}
	if len(keys) == 0 && refused > 0 {
		return nil, errors.New("could not preserve one or more existing files; nothing was written")
	}
	a.emitArchiveExtracted(ctx, req.StorageID, req.Path, dest, len(keys))
	return map[string]any{"keys": keys, "count": len(keys), "refused": refused}, nil
}

func (a *Archive) emitArchiveExtracted(ctx context.Context, storageID int64, archivePath, dest string, count int) {
	if count <= 0 {
		return
	}
	emitFileEvent(ctx, notify.Event{
		Event:  notify.EventArchiveExtracted,
		Body:   dest,
		Meta:   map[string]any{"path": dest, "count": count, "archive": archivePath},
		Node:   &notify.NodeRef{StorageID: storageID, Path: dest, Name: path.Base(dest)},
		Target: notify.DirTarget(dest),
	})
}

// Create writes a new archive from files and folders already in filex. It
// refuses an occupied destination rather than silently replacing user data;
// the historical /archive/add endpoint retains ZIP update semantics, which
// solid 7z and RAR archives cannot share safely.
func (a *Archive) Create(w http.ResponseWriter, r *http.Request) {
	if a.Engine == nil || a.Body == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "archive creation unavailable", "code": "PROVIDER_UNAVAILABLE"})
		return
	}
	var req archiveCreateRequest
	if err := jsonNewDecoder(r).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Dest == "" || len(req.Sources) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing dest or sources"})
		return
	}
	policy := a.Engine.Policy(r.Context())
	format := archivecli.CanonicalFormat(req.Format)
	if format == "" {
		format = archivecli.FormatFromPath(req.Dest)
	}
	if format == "" {
		format = policy.DefaultFormat
	}
	// RAR and the bare single-stream compressors remain extraction-only.
	if !archivecli.IsCreateFormat(format) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "archive creation format is not supported", "code": "UNSUPPORTED_FORMAT"})
		return
	}
	if !policy.Enabled && (format != "zip" || req.Password != "") {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "external archive processing is disabled", "code": "PROVIDER_UNAVAILABLE"})
		return
	}
	if !containsString(policy.AllowedFormats, format) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "archive format is not allowed", "code": "UNSUPPORTED_FORMAT"})
		return
	}
	if req.Compression < 0 || req.Compression > 9 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "compression must be between 0 and 9", "code": "INVALID_ARCHIVE_OPTIONS"})
		return
	}
	if !archivecli.ValidDictionarySizeMiB(req.DictionaryMB) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dictionary_size_mb must be 1, 2, 4, 8, 16, 32, 64, 128, or 256", "code": "INVALID_ARCHIVE_OPTIONS"})
		return
	}
	if format != "zip" && format != "7z" && (req.Password != "" || req.EncryptNames) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "passwords and filename encryption are available only for zip and 7z archives", "code": "INVALID_ARCHIVE_OPTIONS"})
		return
	}
	if format != "7z" && (req.Solid != nil || req.DictionaryMB != 0) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "solid and dictionary size are available only for 7z archives", "code": "INVALID_ARCHIVE_OPTIONS"})
		return
	}
	destStorageID, destRel, err := a.resolveStorage(r.Context(), req.StorageID, req.Dest)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !ownsStorage(w, r, destStorageID, "storage") {
		return
	}
	if !aclAllowID(r.Context(), a.ACL, a.Store, destStorageID, destRel, acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}
	destDriver, err := a.StorageResolver(destStorageID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage"})
		return
	}
	// Creating an archive can involve downloading and compressing gigabytes.
	// Reject a name collision before doing any of that work, and never turn a
	// harmless retry/double-click into an implicit overwrite.
	if storage.Exists(r.Context(), destDriver, destRel) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "an item named " + path.Base(destRel) + " already exists; choose another name or remove it first",
			"code":  "TARGET_EXISTS",
		})
		return
	}
	var members []archiveMember
	sets := map[int64]*acl.Set{}
	for _, raw := range req.Sources {
		storageID, rel, err := a.resolveStorage(r.Context(), 0, raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !ownsStorage(w, r, storageID, "storage") {
			return
		}
		if !aclAllowID(r.Context(), a.ACL, a.Store, storageID, rel, acl.LevelViewer) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission: " + raw})
			return
		}
		drv, err := a.StorageResolver(storageID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage"})
			return
		}
		st, err := drv.Stat(r.Context(), rel)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": raw + ": " + err.Error()})
			return
		}
		if st.Kind == storage.KindDirectory {
			set, ok := sets[storageID]
			if !ok {
				set = a.aclSetFor(r.Context(), storageID)
				sets[storageID] = set
			}
			found, err := a.expandDir(r.Context(), drv, storageID, rel, set)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			members = append(members, found...)
		} else {
			members = append(members, archiveMember{StorageID: storageID, Path: rel, Name: path.Base(rel), Size: st.Size, Mtime: st.Mtime})
		}
	}
	var total int64
	for _, member := range members {
		total += member.Size
	}
	if len(members) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "selection contains no readable files"})
		return
	}
	if len(members) > policy.MaxEntries || total > policy.MaxExpandedBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "selection exceeds configured archive limits", "max_entries": policy.MaxEntries, "max_bytes": policy.MaxExpandedBytes})
		return
	}
	seen := map[string]bool{}
	for _, member := range members {
		name, err := sanitizeZipPath(member.Name)
		if err != nil || seen[name] {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "archive member names collide or are unsafe: " + member.Name})
			return
		}
		seen[name] = true
	}

	// Production uses the shared operation queue: the dialog can close as soon
	// as validation succeeds and the operations center tracks staging, provider
	// compression progress and the destination write. The closure and password
	// are memory-only; pending_ops receives no credentials.
	if a.Ops != nil {
		jobReq := req
		jobMembers := append([]archiveMember(nil), members...)
		var actor *model.User
		if current := auth.UserFrom(r.Context()); current != nil {
			copy := *current
			actor = &copy
		}
		op, err := a.Ops.SubmitJob(r.Context(), ops.OpArchiveCreate, destStorageID, append([]string(nil), req.Sources...), destRel, 100,
			func(ctx context.Context, progress func(done int)) error {
				if actor != nil {
					ctx = auth.WithUser(ctx, actor)
				}
				_, err := a.createArchive(ctx, jobReq, format, destStorageID, destRel, jobMembers, progress)
				return err
			})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"op": op})
		return
	}

	result, err := a.createArchive(r.Context(), req, format, destStorageID, destRel, members, func(int) {})
	if err != nil {
		writeArchiveProviderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *Archive) createArchive(ctx context.Context, req archiveCreateRequest, format string, destStorageID int64, destRel string, members []archiveMember, progress func(done int)) (map[string]any, error) {
	destDriver, err := a.StorageResolver(destStorageID)
	if err != nil {
		return nil, fmt.Errorf("bad destination storage: %w", err)
	}
	// A target can appear while the job waits behind another operation. Refuse
	// it again here so a queued archive never turns into an overwrite.
	if storage.Exists(ctx, destDriver, destRel) {
		return nil, fmt.Errorf("an item named %s already exists; choose another name or remove it first", path.Base(destRel))
	}
	root, err := a.archiveWorkDir("filex-archive-create-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	sourceDir := filepath.Join(root, "source")
	if err := os.Mkdir(sourceDir, 0o700); err != nil {
		return nil, err
	}
	for i, member := range members {
		name, err := sanitizeZipPath(member.Name)
		if err != nil {
			return nil, fmt.Errorf("unsafe archive member name %q: %w", member.Name, err)
		}
		target := filepath.Join(sourceDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, err
		}
		drv, err := a.StorageResolver(member.StorageID)
		if err != nil {
			return nil, fmt.Errorf("bad source storage: %w", err)
		}
		src, err := a.Body.Resolve(ctx, drv, member.StorageID, member.Path, nil)
		if err != nil {
			return nil, err
		}
		rc, err := src.Open(ctx)
		if err != nil {
			return nil, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, err = io.Copy(out, rc)
			_ = out.Close()
		}
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		progress((i + 1) * 10 / len(members))
	}
	output := filepath.Join(root, "output."+format)
	if format == "zip" && req.Password == "" {
		err = createBuiltinZip(ctx, sourceDir, output)
	} else {
		err = a.Engine.Create(ctx, sourceDir, output, archivecli.CreateOptions{
			Format: format, Password: req.Password, EncryptNames: req.EncryptNames,
			Compression: req.Compression, Solid: req.Solid, DictionarySizeMiB: req.DictionaryMB,
			Progress: func(percent int) {
				progress(10 + percent*80/100)
			},
		})
	}
	if err != nil {
		return nil, err
	}
	progress(90)
	writer, ok := destDriver.(storage.Writer)
	if !ok {
		return nil, errors.New("storage not writable")
	}
	if err := storage.EnsureFileTarget(ctx, destDriver, destRel); err != nil {
		return nil, err
	}
	if mkdirer, ok := destDriver.(storage.Mkdirer); ok {
		parent := path.Dir(destRel)
		_ = mkdirer.Mkdir(ctx, parent)
		if st := a.storageRow(ctx, destStorageID); st != nil {
			a.sync().Mkdir(ctx, st, parent)
		}
	}
	if err := writehook.BeforeOverwrite(ctx, destStorageID, destRel); err != nil {
		return nil, fmt.Errorf("could not preserve the existing file: %w", err)
	}
	if storage.Exists(ctx, destDriver, destRel) {
		return nil, fmt.Errorf("an item named %s already exists; choose another name or remove it first", path.Base(destRel))
	}
	f, err := os.Open(output)
	if err != nil {
		return nil, err
	}
	stat, _ := f.Stat()
	err = writer.Write(ctx, destRel, &contextReader{ctx: ctx, reader: f}, stat.Size())
	_ = f.Close()
	if err != nil {
		if ctx.Err() != nil {
			removeIncomplete(ctx, destDriver, destRel, false)
			return nil, ctx.Err()
		}
		return nil, err
	}
	if st := a.storageRow(ctx, destStorageID); st != nil {
		a.sync().WriteWithoutNotification(ctx, st, destRel, stat.Size(), archiveMIME(format))
	}
	emitFileEvent(ctx, notify.Event{
		Event:  notify.EventArchiveCreated,
		Body:   destRel,
		Meta:   map[string]any{"path": destRel, "format": format},
		Node:   &notify.NodeRef{StorageID: destStorageID, Path: destRel, Name: path.Base(destRel), Size: stat.Size()},
		Target: notify.FileTarget(destRel),
	})
	progress(100)
	return map[string]any{"path": destRel, "size": stat.Size(), "format": format}, nil
}

// jsonNewDecoder is kept tiny so Create's body remains easy to read while
// still sharing the standard handler behaviour (unknown fields are ignored).
func jsonNewDecoder(r *http.Request) *json.Decoder { return json.NewDecoder(r.Body) }

func createBuiltinZip(ctx context.Context, sourceDir, output string) error {
	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	walkErr := filepath.WalkDir(sourceDir, func(local string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(sourceDir, local)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		r, err := os.Open(local)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(w, &contextReader{ctx: ctx, reader: r})
		_ = r.Close()
		return copyErr
	})
	closeErr := zw.Close()
	fileErr := f.Close()
	if walkErr != nil {
		return walkErr
	}
	if closeErr != nil {
		return closeErr
	}
	return fileErr
}

func archiveMIME(format string) string {
	switch format {
	case "zip":
		return "application/zip"
	case "7z":
		return "application/x-7z-compressed"
	case "rar":
		return "application/vnd.rar"
	case "tar":
		return "application/x-tar"
	case "tar.gz":
		return "application/gzip"
	case "tar.bz2":
		return "application/x-bzip2"
	case "tar.xz":
		return "application/x-xz"
	default:
		return "application/octet-stream"
	}
}
