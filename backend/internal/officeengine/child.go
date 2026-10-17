package officeengine

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
)

// ── The engine process (`filex office-engine`) ──────────────────────────

// What engine.zip holds (docs/APP-PLUGINS-API.md → engines.office).
const (
	EngineX2T     = "x2t-server.wasm"
	EngineLayout  = "layout-host.wasm"
	EngineWorker  = "layout/worker.js"
	engineMaxWasm = 128 << 20
)

// Main is `filex office-engine`: it reads its ChildConfig from ConfigEnv and
// serves jobs on stdin and stdout until stdin closes. The exit code is 0
// for a process that ended because filex closed it (or a Prepare run that
// finished), 1 for one that could not start.
func Main(stdin io.Reader, stdout io.Writer) int {
	var cfg ChildConfig
	if err := json.Unmarshal([]byte(os.Getenv(ConfigEnv)), &cfg); err != nil {
		writeLine(stdout, Hello{Error: "office engine: no configuration (" + ConfigEnv + "): this command is started by filex, not by hand"})
		return 1
	}
	if err := Serve(context.Background(), cfg, stdin, stdout); err != nil {
		return 1
	}
	return 0
}

var stdoutMu sync.Mutex

func writeLine(w io.Writer, v any) {
	b, _ := json.Marshal(v)
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	_, _ = w.Write(append(b, '\n'))
	if f, ok := w.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}
}

// Serve is the engine process's whole life: open the packages, compile both
// modules, say Hello, then answer one job per line of in.
func Serve(ctx context.Context, cfg ChildConfig, in io.Reader, out io.Writer) error {
	t0 := time.Now()
	e, err := openEngine(ctx, cfg)
	if err != nil {
		writeLine(out, Hello{Error: err.Error()})
		return err
	}
	defer e.close(ctx)
	if cfg.Prepare {
		if err := e.warm(ctx); err != nil {
			writeLine(out, Hello{Error: err.Error()})
			return err
		}
		writeLine(out, Hello{Prepared: true, CompileMs: time.Since(t0).Milliseconds()})
		return nil
	}
	writeLine(out, Hello{Ready: true, CompileMs: time.Since(t0).Milliseconds()})
	r := bufio.NewReaderSize(in, 1<<16)
	for {
		line, err := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var req Request
			var ans Answer
			if jerr := json.Unmarshal(line, &req); jerr != nil {
				ans = Answer{Code: CodeUnknown, What: "request", Error: jerr.Error()}
			} else {
				ans = e.handle(ctx, req)
			}
			ans.ID = req.ID
			writeLine(out, ans)
			// A job's buffers (a document read whole, x2t's memory) go back
			// to the system before the next one.
			debug.FreeOSMemory()
		}
		if err != nil {
			return nil
		}
	}
}

// engine is the engine process's state.
type engine struct {
	cfg     ChildConfig
	zips    []*zip.ReadCloser
	cache   wazero.CompilationCache
	x2t     *x2tEngine
	lay     *layoutEngine
	work    string
	n       int
	compile time.Duration
}

// openEngine checks both packages against their pins, builds the layout's
// view of them and compiles the two modules (from the cache when it has
// them).
func openEngine(ctx context.Context, cfg ChildConfig) (*engine, error) {
	if cfg.Work == "" || cfg.Cache == "" {
		return nil, errors.New("office engine: no work or cache directory")
	}
	e := &engine{cfg: cfg, work: cfg.Work}
	ok := false
	defer func() {
		if !ok {
			e.close(ctx)
		}
	}()
	ez, err := openPinned(cfg.Engine, cfg.EngineSHA256)
	if err != nil {
		return nil, err
	}
	e.zips = append(e.zips, ez)
	uz, err := openPinned(cfg.Package, cfg.PackageSHA256)
	if err != nil {
		return nil, err
	}
	e.zips = append(e.zips, uz)

	tree := newZipTree()
	if n, err := tree.graft(&ez.Reader, "layout", "layout"); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, errors.New("office engine: engine.zip has no layout/ directory")
	}
	editor := strings.Trim(cfg.EditorDir, "/")
	for _, sub := range []string{"sdkjs", "fonts"} {
		from := sub
		if editor != "" {
			from = editor + "/" + sub
		}
		n, err := tree.graft(&uz.Reader, from, sub)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, fmt.Errorf("office engine: the interface bundle has no %s/ directory", from)
		}
	}

	x2tWasm, err := readEntry(&ez.Reader, EngineX2T, engineMaxWasm)
	if err != nil {
		return nil, err
	}
	layWasm, err := readEntry(&ez.Reader, EngineLayout, engineMaxWasm)
	if err != nil {
		return nil, err
	}
	wdir := filepath.Join(cfg.Cache, "wazero")
	if err := os.MkdirAll(wdir, 0o700); err != nil {
		return nil, err
	}
	if e.cache, err = wazero.NewCompilationCacheWithDir(wdir); err != nil {
		return nil, err
	}
	bytecode := filepath.Join(cfg.Cache, "bytecode", clip(strings.ToLower(cfg.EngineSHA256), 16)+"-"+clip(strings.ToLower(cfg.PackageSHA256), 16))
	if err := os.MkdirAll(bytecode, 0o700); err != nil {
		return nil, err
	}
	limit := pagesOf(ClampMaxMB(cfg.MaxMB))
	t0 := time.Now()
	if e.x2t, err = newX2T(ctx, e.cache, x2tWasm, limit); err != nil {
		return nil, err
	}
	if e.lay, err = newLayout(ctx, e.cache, layWasm, limit, tree, cfg.Work, bytecode); err != nil {
		return nil, err
	}
	e.compile = time.Since(t0)
	ok = true
	return e, nil
}

func (e *engine) close(ctx context.Context) {
	if e.lay != nil {
		e.lay.close(ctx)
	}
	if e.x2t != nil {
		e.x2t.close(ctx)
	}
	if e.cache != nil {
		_ = e.cache.Close(ctx)
	}
	for _, z := range e.zips {
		_ = z.Close()
	}
}

// warm starts a worker of each kind once: the first worker of a kind writes
// the layout's bytecode for it, so every later one starts without parsing
// sdkjs.
func (e *engine) warm(ctx context.Context) error {
	for _, kind := range []string{KindWord, KindCell, KindSlide} {
		w, err := e.lay.start(ctx, kind)
		if err != nil {
			return err
		}
		w.close(ctx)
	}
	return nil
}

// handle runs one job.
func (e *engine) handle(ctx context.Context, req Request) Answer {
	e.n++
	dir := filepath.Join(e.work, "j"+strconv.Itoa(e.n))
	defer os.RemoveAll(dir)
	ans, err := e.job(ctx, req, dir)
	if err != nil {
		var ee *Error
		if !errors.As(err, &ee) {
			ee = &Error{Code: CodeUnknown, What: "io", Err: err}
		}
		a := Answer{Code: ee.Code, What: ee.What, Over: ee.Over}
		if ee.Err != nil {
			a.Error = clip(ee.Err.Error(), 2000)
		}
		return a
	}
	ans.OK = true
	return ans
}

func (e *engine) job(ctx context.Context, req Request, dir string) (Answer, error) {
	from := strings.ToLower(req.From)
	var p plan
	switch req.Op {
	case OpConvert:
		var err error
		if p, err = planFor(from, req.To); err != nil {
			return Answer{}, err
		}
	case OpThumbnail:
		p = plan{kind: KindOf(from), mode: "image", formatTo: formatPNG, ext: "png"}
		if p.kind == "" || !extRe.MatchString(from) {
			return Answer{}, &Error{Code: CodeParams, What: "from-" + from}
		}
	default:
		return Answer{}, &Error{Code: CodeUnknown, What: "op", Err: fmt.Errorf("unknown op %q", req.Op)}
	}
	for _, d := range []string{"", "media", "fonts", "themes", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			return Answer{}, err
		}
	}
	input := "input." + from
	head, err := copyIn(req.In, filepath.Join(dir, input))
	if err != nil {
		return Answer{}, &Error{Code: CodeUnknown, What: "input", Err: err}
	}
	ms := map[string]int64{}
	lap := func(name string, t time.Time) { ms[name] = time.Since(t).Milliseconds() }
	out := "output." + p.ext

	if p.mode == "" {
		// x2t alone: office to office.
		t := time.Now()
		err := e.x2t.run(ctx, dir, x2tJob{
			from: input, to: out, formatTo: p.formatTo,
			text: firstText(textFor(from, req.Delimiter, head), textFor(req.To, req.Delimiter, nil)),
		})
		lap("convert", t)
		if err != nil {
			return Answer{}, err
		}
		return e.deliver(dir, out, req, ms, 0)
	}

	// 1. The editor's format.
	t := time.Now()
	if err := e.x2t.run(ctx, dir, x2tJob{
		from: input, to: "Editor.bin", formatTo: canvasFormat[p.kind], noBase64: true,
		text: textFor(from, req.Delimiter, head),
	}); err != nil {
		return Answer{}, err
	}
	lap("to_bin", t)

	// 2. The layout: the pages' drawing, or the document's HTML.
	t = time.Now()
	w, err := e.lay.workerFor(ctx, p.kind)
	if err != nil {
		return Answer{}, &Error{Code: CodeUnknown, What: "layout-start", Err: err}
	}
	jobDir := "/j/" + filepath.Base(dir)
	lj := layoutJob{In: jobDir + "/Editor.bin", Out: jobDir + "/pdf.bin", Fonts: jobDir + "/fonts", Mode: p.mode, Params: req.Params}
	if p.mode == "html" {
		lj = layoutJob{In: jobDir + "/Editor.bin", Out: jobDir + "/tmp/index.html", Mode: "html", Params: req.Params}
	}
	la, broken, err := w.do(lj)
	e.lay.retire(ctx, w, broken)
	if err != nil {
		return Answer{}, err
	}
	lap("layout", t)

	// 3. From the editor's format to the target.
	t = time.Now()
	switch {
	case req.To == "html":
		out = "tmp/index.html"
	case req.To == "md":
		// x2t's own html2md, from the layout's HTML.
		if err := os.Rename(filepath.Join(dir, "tmp", "index.html"), filepath.Join(dir, "page.html")); err != nil {
			return Answer{}, err
		}
		err = e.x2t.run(ctx, dir, x2tJob{from: "page.html", to: out, formatTo: formatMD})
	case req.To == "epub":
		// x2t reads the HTML where doctrenderer would have written it
		// (m_sTempDir/index.html).
		err = e.x2t.run(ctx, dir, x2tJob{from: "Editor.bin", to: out, formatTo: formatEPUB})
	default:
		j := x2tJob{from: "Editor.bin", to: out, formatTo: p.formatTo, fonts: true}
		if p.mode == "image" {
			// An image is drawn from the temporary directory's pdf.bin
			// (x2t's doct_bin2image), a PDF from the one beside the document.
			if cerr := copyFile(filepath.Join(dir, "pdf.bin"), filepath.Join(dir, "tmp", "pdf.bin")); cerr != nil {
				return Answer{}, cerr
			}
			format := 4
			if p.ext == "jpg" {
				format = 3
			}
			if req.Op == OpThumbnail {
				size := req.Size
				if size <= 0 {
					size = 320
				}
				j.thumb = &thumbOptions{format: format, aspect: 1, first: true, width: size, height: size}
			} else {
				// The Document Server's defaults for an image: the first
				// page, at its own size.
				j.thumb = &thumbOptions{format: format, aspect: 2, first: true, width: 100, height: 100}
			}
		}
		err = e.x2t.run(ctx, dir, j)
	}
	if err != nil {
		return Answer{}, err
	}
	lap("from_bin", t)
	return e.deliver(dir, out, req, ms, la.Pages)
}

// deliver moves the result to the request's Out, within its MaxBytes.
func (e *engine) deliver(dir, name string, req Request, ms map[string]int64, pages int) (Answer, error) {
	src := filepath.Join(dir, filepath.FromSlash(name))
	st, err := os.Stat(src)
	if err != nil || st.Size() == 0 {
		return Answer{}, &Error{Code: CodeCorrupt, What: "empty", Err: errors.New("the engine made nothing")}
	}
	if req.MaxBytes > 0 && st.Size() > req.MaxBytes {
		return Answer{}, &Error{Code: CodeTooLarge, What: "result", Over: true,
			Err: fmt.Errorf("the result is %d bytes, over the %d the request allows", st.Size(), req.MaxBytes)}
	}
	if err := copyFile(src, req.Out); err != nil {
		return Answer{}, err
	}
	return Answer{Bytes: st.Size(), Pages: pages, Ms: ms}, nil
}

// firstText is the first of two text options that is set.
func firstText(a, b *textOptions) *textOptions {
	if a != nil {
		return a
	}
	return b
}

// sniffBytes is how much of an input copyIn keeps for a csv's delimiter.
const sniffBytes = 64 << 10

// copyIn copies the request's input into the job's directory, and answers
// its first bytes.
func copyIn(src, dst string) ([]byte, error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("the input is not a file")
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	head := &headWriter{max: sniffBytes}
	if _, err := io.Copy(io.MultiWriter(out, head), in); err != nil {
		out.Close()
		return nil, err
	}
	return head.buf, out.Close()
}

type headWriter struct {
	buf []byte
	max int
}

func (h *headWriter) Write(p []byte) (int, error) {
	if room := h.max - len(h.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		h.buf = append(h.buf, p[:room]...)
	}
	return len(p), nil
}

// copyFile copies src to dst, which must not exist.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}
