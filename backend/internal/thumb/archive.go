package thumb

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// An archive's thumbnail is the list of what is in it: the top-level names
// (a folder bold, with its slash), and across the top the kind and how many
// entries it holds. Nothing is unpacked and no member is read.
//
//   - ZIP: only the central directory, the index at the end of the file,
//     through ranged reads where the storage has them (a few requests for a
//     whole archive). Where it has none, a ZIP up to archiveCopyLimit is
//     copied to a temporary file first; a larger one is skipped.
//   - TAR, TAR.GZ, TAR.BZ2: a tar has no index, so the headers are read in
//     order and reading stops as soon as the list is full.
//
// ⚠ An archive is hostile input (a ZIP bomb, a directory of millions of
// entries, a tar whose first member is a terabyte of zeros). Every read is
// bounded: archiveDirBudget bytes of ZIP directory, archiveStreamBudget bytes
// of tar stream before or after decompression, archiveMaxEntries entries,
// and archiveTimeout in all. Past any of them the file is skipped with the
// reason `archive_too_large` and keeps its type icon; an encrypted one is
// skipped as `archive_encrypted`; a damaged one fails with the reader's own
// words. None of them is the placeholder card.
//
// The bounds are variables only so a test can lower them.
var (
	archiveDirBudget    int64 = 8 << 20
	archiveStreamBudget int64 = 64 << 20
	archiveCopyLimit    int64 = 64 << 20
	archiveMaxEntries         = 200_000
	archiveTimeout            = 5 * time.Second
)

const (
	archiveNames = 13
	archiveTail  = 256 << 10
)

// Skip reasons an archive can record.
const (
	SkipArchiveEncrypted = "archive_encrypted"
	SkipArchiveTooLarge  = "archive_too_large"
)

var (
	errArchiveEncrypted = errors.New("thumb: archive: encrypted")
	errArchiveLimits    = errors.New("thumb: archive: past the listing limits")
)

// archiveKind is the archive a file name says it is, "" for none.
func archiveKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".zip"):
		return "zip"
	case strings.HasSuffix(n, ".tar"):
		return "tar"
	case strings.HasSuffix(n, ".tar.gz"), strings.HasSuffix(n, ".tgz"):
		return "tgz"
	case strings.HasSuffix(n, ".tar.bz2"), strings.HasSuffix(n, ".tbz2"), strings.HasSuffix(n, ".tbz"):
		return "tbz2"
	}
	return ""
}

func (p *Pipeline) generateArchive(ctx context.Context, node *model.Node, drv storage.Driver, kind string) error {
	ctx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()
	var (
		entries []string
		count   int
		partial bool
		err     error
	)
	if kind == "zip" {
		entries, err = p.zipEntries(ctx, node, drv)
		count = len(entries)
	} else {
		entries, count, partial, err = p.tarEntries(ctx, node, drv, kind)
	}
	if err != nil {
		if ctx.Err() != nil && !errors.Is(err, errArchiveEncrypted) {
			return errArchiveLimits
		}
		return err
	}
	header := fmt.Sprintf("%s · %d", strings.ToUpper(kindLabel(kind)), count)
	if partial {
		header += "+"
	}
	img, err := drawLines(header, archiveLines(entries))
	if err != nil {
		return err
	}
	return p.writeJPEG(node.ID, img, thumbQuality)
}

func kindLabel(kind string) string {
	switch kind {
	case "tgz":
		return "tar.gz"
	case "tbz2":
		return "tar.bz2"
	}
	return kind
}

// archiveLines is the top level of an archive: each first path segment once,
// in the order the archive has them, a folder bold with its slash, and a
// muted "+N" when there were more than fit.
func archiveLines(entries []string) []textLine {
	seen := map[string]bool{}
	var out []textLine
	more := 0
	for _, e := range entries {
		e = strings.TrimLeft(strings.ReplaceAll(e, "\\", "/"), "/")
		for strings.HasPrefix(e, "./") {
			e = e[2:]
		}
		if e == "" || e == "." {
			continue
		}
		first, rest, isDir := strings.Cut(e, "/")
		isDir = isDir && (rest != "" || strings.HasSuffix(e, "/"))
		key := first
		if isDir {
			key += "/"
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(out) < archiveNames-1 {
			out = append(out, textLine{Text: key, Bold: isDir})
		} else {
			more++
		}
	}
	if more > 0 {
		out = append(out, textLine{Text: fmt.Sprintf("+%d", more), Muted: true})
	}
	return out
}

// ── ZIP: the central directory only ─────────────────────────────────────

func (p *Pipeline) zipEntries(ctx context.Context, node *model.Node, drv storage.Driver) ([]string, error) {
	src, err := p.body.Resolve(ctx, drv, node.StorageID, node.Path, node)
	if err != nil {
		return nil, err
	}
	size := node.Size
	if st, err := src.Stat(ctx); err == nil && st.Size > 0 {
		size = st.Size
	}
	if size <= 0 {
		return nil, errors.New("thumb: archive: empty file")
	}
	var ra io.ReaderAt
	if src.CanRange() {
		ra = &budgetReaderAt{ctx: ctx, size: size, budget: archiveDirBudget, fetch: func(off, n int64) (io.ReadCloser, error) {
			return src.ReadRange(ctx, off, n)
		}}
	} else {
		if size > archiveCopyLimit {
			return nil, errArchiveLimits
		}
		f, err := os.CreateTemp("", "filex-zip-*")
		if err != nil {
			return nil, err
		}
		defer func() { f.Close(); os.Remove(f.Name()) }()
		if err := p.copyTo(ctx, drv, node, f, archiveCopyLimit); err != nil {
			return nil, err
		}
		ra = f
	}
	// An end-to-end encrypted file is ciphertext: never parsed.
	head := make([]byte, len(e2e.MagicPrefix))
	if n, _ := ra.ReadAt(head, 0); n == len(head) && e2e.HasEncryptedPrefix(head) {
		return nil, errEncryptedContent
	}
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		if errors.Is(err, errArchiveLimits) {
			return nil, err
		}
		return nil, fmt.Errorf("thumb: archive: %w", err)
	}
	if len(zr.File) > archiveMaxEntries {
		return nil, errArchiveLimits
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		if f.Flags&0x1 != 0 {
			return nil, errArchiveEncrypted
		}
		names = append(names, f.Name)
	}
	return names, nil
}

// copyTo copies node's bytes into w through openSource, at most limit bytes.
func (p *Pipeline) copyTo(ctx context.Context, drv storage.Driver, node *model.Node, w io.Writer, limit int64) error {
	rc, err := p.openSource(ctx, drv, node)
	if err != nil {
		return err
	}
	defer rc.Close()
	n, err := io.Copy(w, io.LimitReader(rc, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return errArchiveLimits
	}
	return nil
}

// budgetReaderAt reads a ZIP's tail through ranged reads: one window of
// archiveTail bytes at a time (the whole directory of a normal archive in one
// request), at most budget bytes in all, and not past the context.
type budgetReaderAt struct {
	ctx    context.Context
	size   int64
	budget int64
	fetch  func(off, n int64) (io.ReadCloser, error)
	winOff int64
	win    []byte
}

func (r *budgetReaderAt) ReadAt(b []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if off >= r.size {
		return 0, io.EOF
	}
	total := 0
	for total < len(b) && off < r.size {
		if r.win == nil || off < r.winOff || off >= r.winOff+int64(len(r.win)) {
			if err := r.load(off); err != nil {
				return total, err
			}
		}
		n := copy(b[total:], r.win[off-r.winOff:])
		total += n
		off += int64(n)
	}
	if total < len(b) {
		return total, io.EOF
	}
	return total, nil
}

func (r *budgetReaderAt) load(off int64) error {
	n := int64(archiveTail)
	if off+n > r.size {
		n = r.size - off
	}
	if n > r.budget {
		return errArchiveLimits
	}
	r.budget -= n
	rc, err := r.fetch(off, n)
	if err != nil {
		return err
	}
	defer rc.Close()
	buf, err := io.ReadAll(io.LimitReader(rc, n))
	if err != nil {
		return err
	}
	if len(buf) == 0 {
		return io.ErrUnexpectedEOF
	}
	r.win, r.winOff = buf, off
	return nil
}

// ── TAR: headers in order, until the list is full ─────────────────────────

func (p *Pipeline) tarEntries(ctx context.Context, node *model.Node, drv storage.Driver, kind string) ([]string, int, bool, error) {
	rc, err := p.openSource(ctx, drv, node)
	if err != nil {
		return nil, 0, false, err
	}
	defer rc.Close()
	var in io.Reader = &budgetReader{ctx: ctx, r: rc, remaining: archiveStreamBudget}
	switch kind {
	case "tgz":
		gz, err := gzip.NewReader(in)
		if err != nil {
			return nil, 0, false, fmt.Errorf("thumb: archive: %w", err)
		}
		defer gz.Close()
		in = gz
	case "tbz2":
		in = bzip2.NewReader(in)
	}
	// The decompressed side is bounded too: a small .tar.gz can inflate to
	// a great deal of nothing, and skipping a member reads through it.
	tr := tar.NewReader(&budgetReader{ctx: ctx, r: in, remaining: archiveStreamBudget})
	var names []string
	tops := map[string]bool{}
	count := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names, count, false, nil
		}
		if err != nil {
			if errors.Is(err, errArchiveLimits) || ctx.Err() != nil {
				if len(names) > 0 {
					return names, count, true, nil
				}
				return nil, 0, false, errArchiveLimits
			}
			return nil, 0, false, fmt.Errorf("thumb: archive: %w", err)
		}
		count++
		names = append(names, h.Name)
		top, _, _ := strings.Cut(strings.TrimLeft(path.Clean("/"+h.Name), "/"), "/")
		tops[top] = true
		// One more top-level name than fits says "there is more".
		if len(tops) > archiveNames || count >= archiveMaxEntries {
			return names, count, true, nil
		}
	}
}

// budgetReader stops a stream after remaining bytes and at the context's end.
type budgetReader struct {
	ctx       context.Context
	r         io.Reader
	remaining int64
}

func (b *budgetReader) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.remaining <= 0 {
		return 0, errArchiveLimits
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.r.Read(p)
	b.remaining -= int64(n)
	return n, err
}
