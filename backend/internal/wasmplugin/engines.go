package wasmplugin

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/officecmd"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Heavy engines as host functions ────────────────────────────────────
//
// ffmpeg, ImageMagick, Ghostscript, poppler and rsvg do not compile to wasm
// in any usable form (see the research in the plan), and a plugin may not run
// programs itself. So the filex image's own binaries are offered through
// engine_run, under a permission per engine, confined: a private working
// directory per run, a minimal environment, a wall clock, and the process
// group killed when the clock or the job's context ends.
//
// The office engine (`office`, alias `libreoffice`) is not a binary: it is the
// connected OnlyOffice Document Server (office.go). It takes the same request
// shape - inputs staged in the run directory, results collected from it - so
// an app does not care which kind of engine it is talking to.
//
// What keeps a plugin from reading the host through an engine is the ARGUMENT
// rule, not trust in the engine: every argument is a bare token — no path
// separators, no `..`, no `@file` lists, no `file:` schemes — so the only
// files an engine can name are the ones placed in its run directory, which
// are the scope's own inputs. Engines run with that directory as cwd.

type engineRequest struct {
	Engine   string            `json:"engine"`
	Args     []string          `json:"args"`
	Inputs   map[string]string `json:"inputs"`  // name in run dir → scope ref
	Outputs  []string          `json:"outputs"` // names expected; "" = collect every new file
	TimeoutS int               `json:"timeout_s"`
}

type engineResult struct {
	Exit       int              `json:"exit"`
	StdoutTail string           `json:"stdout_tail"`
	StderrTail string           `json:"stderr_tail"`
	Outputs    []wire.OutputRef `json:"outputs"`
	Duration   int64            `json:"duration_ms"`
}

// engineSet is what is installed on this host.
//
// ⚠ It is enginebin.Probe()'s answer, not a probe of its own. There used to
// be two (this one and capability/service.go's), and they disagreed about
// ImageMagick on Windows: the About page said "OK" for System32\convert.exe
// while Apps and the converter said "not installed". One probe, read by
// every screen, cannot disagree with itself.
type engineSet struct {
	bins map[string]string // binary engine → resolved binary path
	// office is the office engine's back end, nil when none is wired. It is
	// asked on every availability question (Ready), so a document server
	// connected or removed in the admin UI is seen without a restart.
	office OfficeConverter
}

// popplerTools are the extra poppler binaries a plugin may pick with
// args[0] = tool name; the engine name resolves pdftoppm otherwise.
var popplerTools = map[string]bool{"pdftoppm": true, "pdftotext": true, "pdfinfo": true, "pdftocairo": true, "pdfseparate": true, "pdfunite": true}

func probeEngines(office OfficeConverter) *engineSet {
	set := enginebin.Probe()
	es := &engineSet{bins: map[string]string{}, office: office}
	for name := range enginebin.Candidates {
		if p := set.Path(name); p != "" {
			es.bins[name] = p
		}
	}
	return es
}

// available reports whether an engine - or the engine an alias stands for -
// can run right now.
func (e *engineSet) available(ctx context.Context, name string) bool {
	if e == nil {
		return false
	}
	if isOffice(name) {
		return e.office != nil && e.office.Ready(ctx)
	}
	_, ok := e.bins[name]
	return ok
}

// Available returns the engine → present map for capabilities and the admin
// panel: every engine by its own id (enginebin.Names), never an alias, so
// the office engine is listed once.
func (e *engineSet) Available(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	for _, name := range enginebin.Names() {
		out[name] = e.available(ctx, name)
	}
	return out
}

const (
	engineDefaultTimeout = 120 * time.Second
	engineMaxTimeout     = 900 * time.Second
	engineTailBytes      = 4096
	engineMaxOutputs     = 64
)

func (e *engineSet) run(ctx context.Context, s *Scope, req *engineRequest) (any, error) {
	// Arguments are judged before anything else, including whether the engine
	// exists: a refused argument is a plugin bug the author must see on every
	// host, not only on the one that happens to have ffmpeg.
	args := append([]string(nil), req.Args...)
	if len(args) > 256 {
		return nil, hostErr(wire.ErrInvalid, "too many arguments")
	}
	for _, a := range args {
		if err := checkEngineArg(a); err != nil {
			return nil, err
		}
	}
	var (
		bin    string
		office *officecmd.Command
	)
	if isOffice(req.Engine) {
		// The command line is read before availability, like the argument
		// rule above: what the office engine cannot do is a bug on every host.
		job, err := parseOfficeArgs(args)
		if err != nil {
			return nil, err
		}
		if !e.available(ctx, req.Engine) {
			return nil, hostErr(wire.ErrUnavailable, officeUnconfiguredMessage(req.Engine))
		}
		office = job
	} else {
		b, ok := e.bins[req.Engine]
		if !ok {
			return nil, hostErr(wire.ErrUnavailable, engineMissingMessage(req.Engine))
		}
		bin = b
		if req.Engine == "poppler" && len(args) > 0 && popplerTools[args[0]] {
			if p, err := exec.LookPath(args[0]); err == nil {
				bin = p
				args = args[1:]
			}
		}
	}

	// Private run directory: inputs copied in under the names the plugin
	// chose, outputs collected from whatever appears.
	s.mu.Lock()
	s.nextRun++
	runDir := filepath.Join(s.dir, "run-"+strconv.Itoa(s.nextRun))
	s.mu.Unlock()
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, hostErr(wire.ErrInternal, "run dir: "+err.Error())
	}
	before := map[string]bool{}
	for name, ref := range req.Inputs {
		name = safeName(name)
		if name == "" {
			return nil, hostErr(wire.ErrInvalid, "bad input name")
		}
		src, err := s.spool(ctx, ref)
		if err != nil {
			return nil, err
		}
		if err := copyFile(src, filepath.Join(runDir, name)); err != nil {
			return nil, hostErr(wire.ErrInternal, "stage input: "+err.Error())
		}
		before[name] = true
	}

	timeout := engineDefaultTimeout
	if req.TimeoutS > 0 {
		timeout = time.Duration(req.TimeoutS) * time.Second
	}
	if timeout > engineMaxTimeout {
		timeout = engineMaxTimeout
	}
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem < timeout {
			timeout = rem
		}
	}
	if timeout <= 0 {
		return nil, hostErr(wire.ErrTimeout, "no time left in this job")
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var res *engineResult
	if office != nil {
		started := time.Now()
		res = &engineResult{}
		if err := e.runOffice(rctx, s, req.Engine, runDir, office, before, res); err != nil {
			return nil, err
		}
		res.Duration = time.Since(started).Milliseconds()
	} else {
		r, err := e.runBinary(rctx, req.Engine, bin, args, runDir)
		if err != nil {
			return nil, err
		}
		res = r
	}
	return e.collect(s, req, runDir, before, res)
}

// runBinary runs a binary engine in its run directory.
func (e *engineSet) runBinary(rctx context.Context, engine, bin string, args []string, runDir string) (*engineResult, error) {
	if engine == "ghostscript" {
		args = append([]string{"-dSAFER", "-dBATCH", "-dNOPAUSE"}, args...)
	}
	cmd := exec.CommandContext(rctx, bin, args...)
	cmd.Dir = runDir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + runDir,
		"TMPDIR=" + runDir,
		"TMP=" + runDir,
		"TEMP=" + runDir,
		"XDG_CACHE_HOME=" + filepath.Join(runDir, ".cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(runDir, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(runDir, ".data"),
		"LANG=C.UTF-8",
	}
	if sr := os.Getenv("SYSTEMROOT"); sr != "" {
		cmd.Env = append(cmd.Env, "SYSTEMROOT="+sr)
	}
	setProcAttr(cmd)
	var stdout, stderr tailBuffer
	stdout.max, stderr.max = engineTailBytes, engineTailBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error { return killTree(cmd) }

	started := time.Now()
	err := cmd.Run()
	res := &engineResult{StdoutTail: stdout.String(), StderrTail: stderr.String(), Duration: time.Since(started).Milliseconds()}
	if rctx.Err() != nil {
		return nil, hostErr(wire.ErrTimeout, engine+" exceeded its time budget")
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.Exit = ee.ExitCode()
		} else {
			return nil, hostErr(wire.ErrUnavailable, engine+": "+err.Error())
		}
	}
	return res, nil
}

// collect hands back the run's artefacts: named outputs when asked for, else
// every new file.
func (e *engineSet) collect(s *Scope, req *engineRequest, runDir string, before map[string]bool, res *engineResult) (any, error) {
	// Collect artefacts: named outputs when asked for, else every new file.
	entries, _ := os.ReadDir(runDir)
	want := map[string]bool{}
	for _, o := range req.Outputs {
		if n := safeName(o); n != "" {
			want[n] = true
		}
	}
	for _, ent := range entries {
		if ent.IsDir() || before[ent.Name()] || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		if len(want) > 0 && !want[ent.Name()] {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		if s.reg.opts.MaxOutputBytes > 0 && info.Size() > s.reg.opts.MaxOutputBytes {
			return nil, hostErr(wire.ErrTooLarge, ent.Name()+" exceeds the per-file limit")
		}
		ref := s.addArtefact(ent.Name(), filepath.Join(runDir, ent.Name()), info.Size())
		res.Outputs = append(res.Outputs, wire.OutputRef{Ref: ref, Name: ent.Name()})
		if len(res.Outputs) >= engineMaxOutputs {
			break
		}
	}
	if res.Outputs == nil {
		res.Outputs = []wire.OutputRef{}
	}
	s.plugin.log("debug", "engine "+req.Engine+" exit "+strconv.Itoa(res.Exit)+" in "+strconv.FormatInt(res.Duration, 10)+"ms")
	return res, nil
}

// checkEngineArg refuses any argument that could name a file outside the run
// directory or make the engine read a list of files.
func checkEngineArg(a string) error {
	if len(a) > 4096 {
		return hostErr(wire.ErrInvalid, "argument too long")
	}
	if strings.ContainsAny(a, "/\\\x00") || strings.Contains(a, "..") {
		return hostErr(wire.ErrInvalid, "argument may not contain path separators or '..': "+clip(a, 40))
	}
	if strings.HasPrefix(a, "@") {
		return hostErr(wire.ErrInvalid, "argument may not read a file list: "+clip(a, 40))
	}
	lower := strings.ToLower(a)
	for _, bad := range []string{"file:", "http:", "https:", "ftp:", "pipe:", "tcp:", "udp:", "rtsp:", "rtmp:", "concat:", "subfile:", "data:", "-safe", "-protocol_whitelist", "-dnosafer", "-dnosafer=", "--infilter", "-shell", "%pipe%", "-sdevice=pipe"} {
		if strings.HasPrefix(lower, bad) || strings.Contains(lower, "="+bad) {
			return hostErr(wire.ErrInvalid, "argument not allowed: "+clip(a, 40))
		}
	}
	return nil
}

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
		return err
	}
	return out.Close()
}

// tailBuffer keeps the last max bytes written.
type tailBuffer struct {
	buf bytes.Buffer
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if t.buf.Len() > t.max {
		b := t.buf.Bytes()
		b = b[len(b)-t.max:]
		var nb bytes.Buffer
		nb.Write(b)
		t.buf = nb
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return strings.TrimSpace(t.buf.String()) }
