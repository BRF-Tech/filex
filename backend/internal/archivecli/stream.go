package archivecli

import (
	"archive/tar"
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// The TAR family and the bare single-stream compressors are read by filex
// itself, not by 7-Zip writing into a folder that a monitor then samples.
//
// ⚠⚠ Why (the 0.44 review of #48, fix 3): a monitor that walks the workspace
// every 100 ms–2 s cannot stop a bomb exactly. Measured with 7-Zip 26.01, a
// gzip whose trailer claims 100 bytes put 92 MiB on disk against a 64 MiB
// limit before the poll caught it; the overshoot is the decoder's write rate
// times the poll interval, so it grows with fast disks and slow scans. Read
// here, every byte passes through a writer that refuses the first one past
// the limit, every member is counted before it is created, and a link, device
// or FIFO is refused from its header before anything of that kind exists.
// Nothing is written twice either: the old path unpacked the outer .gz to a
// .tar on disk (for the listing AND again for the extraction) before 7-Zip
// read it.
//
// gzip and bzip2 are Go's own. xz is not in the standard library, so its
// layer alone is decompressed by 7-Zip — to a PIPE (`x -txz -so`), which only
// ever holds what filex has not yet read; the members are still read, counted
// and written here. The formats only 7-Zip reads (7z, RAR, encrypted ZIP) keep
// the declared-size preflight, 7-Zip's own bounded decoders and the monitor.

// streamFormat says how filex reads a format itself: codec is the
// single-stream layer ("", "gz", "bz2", "xz") and tar whether a TAR is inside.
type streamFormat struct {
	codec string
	tar   bool
}

func streamFormatFor(logicalName string) (streamFormat, bool) {
	switch FormatFromPath(logicalName) {
	case "tar":
		return streamFormat{tar: true}, true
	case "tar.gz":
		return streamFormat{codec: "gz", tar: true}, true
	case "tar.bz2":
		return streamFormat{codec: "bz2", tar: true}, true
	case "tar.xz":
		return streamFormat{codec: "xz", tar: true}, true
	case "gz":
		return streamFormat{codec: "gz"}, true
	case "bz2":
		return streamFormat{codec: "bz2"}, true
	case "xz":
		return streamFormat{codec: "xz"}, true
	}
	return streamFormat{}, false
}

// ReadsItself reports whether filex reads this archive without 7-Zip (xz
// layers aside): ZIP through archive/zip, the TAR family and gzip/bzip2 here.
func ReadsItself(logicalName string) bool {
	f := FormatFromPath(logicalName)
	if f == "zip" {
		return true
	}
	sf, ok := streamFormatFor(logicalName)
	return ok && sf.codec != "xz"
}

// decompressed is an opened single-stream layer: the bytes, the gzip header's
// stored name when there is one, and a closer that also reaps 7-Zip.
type decompressed struct {
	r      io.Reader
	name   string
	close  func() error
	failed func() error
}

func (s *Service) openStream(ctx context.Context, archivePath string, codec string) (*decompressed, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(f, 256<<10)
	switch codec {
	case "":
		return &decompressed{r: br, close: f.Close, failed: func() error { return nil }}, nil
	case "gz":
		gr, err := gzip.NewReader(br)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("%w: not gzip: %v", ErrUnsupported, err)
		}
		return &decompressed{r: gr, name: gr.Name, close: f.Close, failed: func() error { return nil }}, nil
	case "bz2":
		// bzip2.NewReader checks nothing until the first Read; peek the magic
		// so a file that is not bzip2 is refused as such, not as a data error.
		if magic, err := br.Peek(3); err != nil || string(magic) != "BZh" {
			_ = f.Close()
			return nil, fmt.Errorf("%w: not bzip2", ErrUnsupported)
		}
		return &decompressed{r: bzip2.NewReader(br), close: f.Close, failed: func() error { return nil }}, nil
	case "xz":
		_ = f.Close()
		return s.openXZ(ctx, archivePath)
	}
	_ = f.Close()
	return nil, ErrUnsupported
}

// openXZ decompresses the one xz stream through 7-Zip into a pipe. Only the
// xz decoder runs (`-txz`), so whatever the bytes are, no other 7-Zip parser
// sees them, and cancelling ctx kills the process.
func (s *Service) openXZ(ctx context.Context, archivePath string) (*decompressed, error) {
	bin, err := s.sevenZipReady(ctx, "xz")
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithCancel(ctx)
	cmd := s.command(cctx, bin, []string{"x", "-txz", "-so", "-bd", "-bso0", "-bsp0", "--", archivePath}, "")
	var stderr strings.Builder
	cmd.Stderr = &limitedBuilder{b: &stderr, max: 4096}
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	var once sync.Once
	var waitErr error
	wait := func() error {
		once.Do(func() { waitErr = cmd.Wait() })
		return waitErr
	}
	return &decompressed{
		r: out,
		close: func() error {
			cancel()
			_ = wait()
			return nil
		},
		failed: func() error {
			// Called once the reader is done: a truncated or foreign file ends
			// the stream early and says so only through 7-Zip's exit status.
			// A TAR ends at its end-of-archive blocks, usually before the
			// stream does (record padding), so read on to the end first — a
			// little way: whatever follows is not part of the archive, and a
			// stream that goes on past that is cut rather than decompressed.
			if n, _ := io.Copy(io.Discard, io.LimitReader(out, 1<<20)); n == 1<<20 {
				cancel()
				_ = wait()
				return nil
			}
			if err := wait(); err != nil && cctx.Err() == nil {
				return classify(stderr.String(), err)
			}
			return nil
		},
	}, nil
}

type limitedBuilder struct {
	b   *strings.Builder
	max int
}

func (l *limitedBuilder) Write(p []byte) (int, error) {
	if room := l.max - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}

// tarEntryKind sorts a TAR header into what filex will create: a folder, a
// regular file, or something it refuses (link / special).
func tarEntryKind(h *tar.Header) (isDir, isFile, isLink, isSpecial bool) {
	switch h.Typeflag {
	case tar.TypeDir:
		return true, false, false, false
	case tar.TypeReg, tar.TypeCont, tar.TypeGNUSparse:
		return false, true, false, false
	case tar.TypeSymlink, tar.TypeLink:
		return false, false, true, false
	default:
		// FIFOs, character and block devices, and every vendor extension
		// ('V' volume labels, 'D' dump directories, …) filex will not create.
		return false, false, false, true
	}
}

// tarEntryName is the name a listing shows: the cleaned relative path, or the
// raw name when it cannot be cleaned (so the preflight refuses it by name).
func tarEntryName(h *tar.Header) string {
	if clean, err := CleanMemberPath(h.Name); err == nil {
		return clean
	}
	return strings.TrimSuffix(h.Name, "/")
}

// isArchiveRoot reports a folder entry that names the archive's own root —
// `./` is the first member of every `tar -C dir -czf x.tgz .` — which is not
// a member to create and must not read as an unsafe path.
func isArchiveRoot(name string) bool {
	n := strings.ReplaceAll(name, `\`, "/")
	for _, part := range strings.Split(n, "/") {
		if part != "" && part != "." {
			return false
		}
	}
	return true
}

// listStream is the listing half: headers only, nothing written. It stops at
// the entry limit and once the DECLARED sizes pass the byte limit, so a huge
// archive is refused without being read to its end.
func (s *Service) listStream(ctx context.Context, archivePath, logicalName string, sf streamFormat) ([]Entry, error) {
	policy := s.Policy(ctx)
	if !sf.tar {
		return s.listSingleStream(ctx, archivePath, logicalName, sf)
	}
	src, err := s.openStream(ctx, archivePath, sf.codec)
	if err != nil {
		return nil, err
	}
	defer src.close()
	tr := tar.NewReader(&ctxReader{ctx: ctx, r: src.r})
	var entries []Entry
	var declared int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if ferr := src.failed(); ferr != nil {
				return nil, ferr
			}
			return nil, fmt.Errorf("%w: not a readable tar: %v", ErrUnsupported, err)
		}
		isDir, _, isLink, isSpecial := tarEntryKind(h)
		if isDir && isArchiveRoot(h.Name) {
			continue
		}
		entries = append(entries, Entry{
			Name: tarEntryName(h), Size: h.Size, Mtime: h.ModTime.UTC(),
			IsDir: isDir, IsLink: isLink, Special: isSpecial,
		})
		if len(entries) > policy.MaxEntries {
			return nil, fmt.Errorf("%w: archive has more than %d entries", ErrLimits, policy.MaxEntries)
		}
		if !isDir {
			if h.Size < 0 || h.Size > policy.MaxExpandedBytes-declared {
				return nil, extractionLimitError()
			}
			declared += h.Size
		}
	}
	if err := src.failed(); err != nil {
		return nil, err
	}
	return entries, nil
}

// listSingleStream lists a bare .gz/.bz2/.xz: one member, named like 7-Zip
// names it (the gzip header's name, else the archive's name minus the
// suffix). Its size is not known without inflating it, so it is reported as 0
// and the extraction holds the limit byte for byte instead.
func (s *Service) listSingleStream(ctx context.Context, archivePath, logicalName string, sf streamFormat) ([]Entry, error) {
	src, err := s.openStream(ctx, archivePath, sf.codec)
	if err != nil {
		return nil, err
	}
	defer src.close()
	return []Entry{{Name: singleStreamName(src.name, logicalName)}}, nil
}

func singleStreamName(stored, logicalName string) string {
	if stored != "" {
		if clean, err := CleanMemberPath(stored); err == nil && !strings.Contains(clean, "/") {
			return clean
		}
	}
	base := path.Base(strings.ReplaceAll(logicalName, `\`, `/`))
	lower := strings.ToLower(base)
	for _, suffix := range []string{".gz", ".gzip", ".bz2", ".bzip2", ".xz"} {
		if strings.HasSuffix(lower, suffix) && len(base) > len(suffix) {
			return base[:len(base)-len(suffix)]
		}
	}
	return base + ".out"
}

// memberWanted mirrors 7-Zip's selection: a named folder brings its contents.
func memberWanted(members []string, name string) bool {
	if len(members) == 0 {
		return true
	}
	for _, m := range members {
		if clean, err := CleanMemberPath(m); err == nil {
			m = clean
		}
		if name == m || strings.HasPrefix(name, m+"/") {
			return true
		}
	}
	return false
}

// extractStream writes the wanted members into destDir, which must be a
// private, empty workspace: no member can be a link, so nothing written here
// ever resolves outside it.
func (s *Service) extractStream(ctx context.Context, archivePath, logicalName, destDir string, members []string, sf streamFormat) error {
	policy := s.Policy(ctx)
	src, err := s.openStream(ctx, archivePath, sf.codec)
	if err != nil {
		return err
	}
	defer src.close()
	budget := &byteBudget{remaining: policy.MaxExpandedBytes}

	if !sf.tar {
		name := singleStreamName(src.name, logicalName)
		if !memberWanted(members, name) {
			return nil
		}
		if err := writeMember(ctx, filepath.Join(destDir, name), src.r, budget); err != nil {
			return err
		}
		return src.failed()
	}

	tr := tar.NewReader(&ctxReader{ctx: ctx, r: src.r})
	entries := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if ferr := src.failed(); ferr != nil {
				return ferr
			}
			return fmt.Errorf("%w: not a readable tar: %v", ErrUnsupported, err)
		}
		entries++
		if entries > policy.MaxEntries {
			return fmt.Errorf("%w: archive has more than %d entries", ErrLimits, policy.MaxEntries)
		}
		isDir, isFile, isLink, isSpecial := tarEntryKind(h)
		if isDir && isArchiveRoot(h.Name) {
			continue
		}
		if isLink || isSpecial {
			return fmt.Errorf("%w: archive contains a link or special entry", ErrUnsupported)
		}
		rel, err := CleanMemberPath(h.Name)
		if err != nil {
			return fmt.Errorf("%w: archive contains an unsafe member path", ErrUnsupported)
		}
		if !memberWanted(members, rel) {
			continue
		}
		target := filepath.Join(destDir, filepath.FromSlash(rel))
		switch {
		case isDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case isFile:
			if h.Size > budget.remaining {
				return extractionLimitError()
			}
			if err := writeMember(ctx, target, tr, budget); err != nil {
				return err
			}
		}
	}
	return src.failed()
}

// byteBudget is the exact limit: Write refuses the whole chunk that would
// cross it, so the bytes on disk never exceed MaxExpandedBytes.
type byteBudget struct{ remaining int64 }

type budgetWriter struct {
	w      io.Writer
	budget *byteBudget
}

func (b *budgetWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > b.budget.remaining {
		return 0, extractionLimitError()
	}
	n, err := b.w.Write(p)
	b.budget.remaining -= int64(n)
	return n, err
}

func writeMember(ctx context.Context, target string, r io.Reader, budget *byteBudget) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(&budgetWriter{w: out, budget: budget}, &ctxReader{ctx: ctx, r: r})
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// CleanMemberPath turns an archive member name into a safe relative path, or
// refuses it. One rule for every reader (archive/zip in the handlers, TAR
// here): backslashes are separators, leading slashes and "./" are dropped, a
// drive letter or any ".." component is refused.
func CleanMemberPath(name string) (string, error) {
	if name == "" {
		return "", errors.New("empty entry name")
	}
	clean := strings.ReplaceAll(name, `\`, `/`)
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimLeft(clean, "/")
	if clean == "" {
		return "", errors.New("empty after sanitize")
	}
	if len(clean) >= 2 && clean[1] == ':' {
		return "", fmt.Errorf("absolute path: %q", name)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("parent traversal: %q", name)
		}
	}
	clean = strings.TrimLeft(path.Clean("/"+clean), "/")
	if clean == "" || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("clean rejected: %q", name)
	}
	return clean, nil
}
