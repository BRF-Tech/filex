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
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Heavy engines as host functions ────────────────────────────────────
//
// ffmpeg, ImageMagick, LibreOffice, Ghostscript, poppler and rsvg do not
// compile to wasm in any usable form (see the research in the plan), and a
// plugin may not run programs itself. So the filex image's own binaries are
// offered through engine_run, under a permission per engine, with the same
// confinement thumb/office.go uses for its LibreOffice calls: a private
// working directory per run, a minimal environment, a wall clock, and the
// process group killed when the clock or the job's context ends.
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
	bins map[string]string // engine → resolved binary path
}

// popplerTools are the extra poppler binaries a plugin may pick with
// args[0] = tool name; the engine name resolves pdftoppm otherwise.
var popplerTools = map[string]bool{"pdftoppm": true, "pdftotext": true, "pdfinfo": true, "pdftocairo": true, "pdfseparate": true, "pdfunite": true}

func probeEngines() *engineSet {
	set := enginebin.Probe()
	es := &engineSet{bins: map[string]string{}}
	for name := range enginebin.Candidates {
		if p := set.Path(name); p != "" {
			es.bins[name] = p
		}
	}
	return es
}

func (e *engineSet) available(name string) bool { _, ok := e.bins[name]; return ok }

// Available returns the engine → present map for capabilities and describe.
func (e *engineSet) Available() map[string]bool {
	out := map[string]bool{}
	for name := range enginebin.Candidates {
		out[name] = e.available(name)
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
	bin, ok := e.bins[req.Engine]
	if !ok {
		return nil, hostErr(wire.ErrUnavailable, engineMissingMessage(req.Engine))
	}
	if req.Engine == "poppler" && len(args) > 0 && popplerTools[args[0]] {
		if p, err := exec.LookPath(args[0]); err == nil {
			bin = p
			args = args[1:]
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

	switch req.Engine {
	case "ghostscript":
		args = append([]string{"-dSAFER", "-dBATCH", "-dNOPAUSE"}, args...)
	case "libreoffice":
		args = append([]string{"--headless", "--norestore", "--nologo", "--nofirststartwizard",
			"-env:UserInstallation=file://" + filepath.ToSlash(filepath.Join(runDir, ".lo-profile"))}, args...)
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
		return nil, hostErr(wire.ErrTimeout, req.Engine+" exceeded its time budget")
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.Exit = ee.ExitCode()
		} else {
			return nil, hostErr(wire.ErrUnavailable, req.Engine+": "+err.Error())
		}
	}

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
