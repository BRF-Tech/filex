package officeengine

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ── The parent side: engine processes, one job at a time each ───────────

// The memory cap (FILEX_OFFICE_ENGINE_MAX_MB): every instance's memory, in
// MiB. x2t starts at 320 MiB and may use 4 GiB, its own maximum.
const (
	MaxMBDefault = 2048
	MaxMBMin     = 512
	MaxMBMax     = 4096
)

// ClampMaxMB is the cap in force for a configured value (0: the default).
func ClampMaxMB(mb int) int {
	switch {
	case mb <= 0:
		return MaxMBDefault
	case mb < MaxMBMin:
		return MaxMBMin
	case mb > MaxMBMax:
		return MaxMBMax
	}
	return mb
}

// Package is the engine an app brings: its engine.zip and the interface
// bundle that holds the editor's scripts and fonts, each with the hash it
// was installed with.
type Package struct {
	App, Version string
	Engine       string
	EngineSHA256 string
	UI           string
	UISHA256     string
	EditorDir    string
	// HostVersion is this filex's version: a new filex (and with it a new
	// wazero) prepares the engine again.
	HostVersion string
}

// key is what the compiled engine depends on: both packages, this filex
// (and with it wazero) and the machine. A new key is prepared again.
func (p *Package) key() string {
	h := sha256.New()
	for _, s := range []string{p.EngineSHA256, p.UISHA256, p.EditorDir, p.HostVersion, runtime.GOOS, runtime.GOARCH} {
		h.Write([]byte(strings.ToLower(s)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// State is where the engine is.
type State string

// The states.
const (
	// StateAbsent: no running app brings an office engine.
	StateAbsent State = "absent"
	// StatePreparing: compiling it, in the background (3.5-5 minutes the
	// first time on a machine).
	StatePreparing State = "preparing"
	// StateReady: jobs run.
	StateReady State = "ready"
	// StateFailed: it could not be prepared (Status.Error says why); tried
	// again after Options.RetryPrepare.
	StateFailed State = "failed"
)

// Status is the engine as the administrator's screens show it.
type Status struct {
	State   State     `json:"state"`
	App     string    `json:"app,omitempty"`
	Version string    `json:"version,omitempty"`
	Error   string    `json:"error,omitempty"`
	Since   time.Time `json:"since"`
}

// Purpose names the pool a job runs in: thumbnails and conversions each
// have their own processes, so a long conversion never holds the
// thumbnails up (and the other way round).
type Purpose string

// The pools.
const (
	PurposeConvert   Purpose = "convert"
	PurposeThumbnail Purpose = "thumbnail"
)

// Options tune a Runner.
type Options struct {
	// Command is the argv that starts an engine process (default: this
	// executable, `office-engine`). Env is added to its environment.
	Command []string
	Env     []string
	// CacheDir is the compilation cache (filex's data directory).
	CacheDir string
	// WorkDir is where each process's temporary directory is made (default:
	// the system's).
	WorkDir string
	// MaxMB is the memory cap in force, read when a process starts.
	MaxMB func() int
	// Slots is how many processes a pool may run at once (thumbs.office_slots,
	// 1-4), read at every job.
	Slots func() int
	// ProcessLimit is the most memory a process may hold (bytes), read at
	// every job; default about twice the cap. 0 from the function: none.
	ProcessLimit func() int64
	// IdleTimeout ends a process nobody used for that long (default 3 min);
	// JobsPerProcess ends one after that many jobs (default 50).
	IdleTimeout    time.Duration
	JobsPerProcess int
	// StartTimeout bounds a process's start (default 2 min: the modules come
	// from the cache); PrepareTimeout a prepare run (default 30 min);
	// RetryPrepare is how long after a failed one it is tried again
	// (default 30 min).
	StartTimeout   time.Duration
	PrepareTimeout time.Duration
	RetryPrepare   time.Duration
	Log            *slog.Logger
}

// Runner runs the office engine of the package it is given, in processes
// of its own.
type Runner struct {
	o Options

	mu       sync.Mutex
	pkg      *Package
	gen      int
	state    State
	stateErr string
	since    time.Time
	stopPrep context.CancelFunc
	retry    *time.Timer
	pools    map[Purpose]*pool
	live     map[*proc]bool
	closed   bool

	nextID atomic.Int64
	quit   chan struct{}
}

// pool is one purpose's processes.
type pool struct {
	idle []*proc
	busy int
	wake chan struct{}
}

// New makes a runner with no package (StateAbsent).
func New(o Options) *Runner {
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = 3 * time.Minute
	}
	if o.JobsPerProcess <= 0 {
		o.JobsPerProcess = 50
	}
	if o.StartTimeout <= 0 {
		o.StartTimeout = 2 * time.Minute
	}
	if o.PrepareTimeout <= 0 {
		o.PrepareTimeout = 30 * time.Minute
	}
	if o.RetryPrepare <= 0 {
		o.RetryPrepare = 30 * time.Minute
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.MaxMB == nil {
		o.MaxMB = func() int { return MaxMBDefault }
	}
	if o.Slots == nil {
		o.Slots = func() int { return 1 }
	}
	r := &Runner{o: o, state: StateAbsent, since: time.Now(), pools: map[Purpose]*pool{}, live: map[*proc]bool{}, quit: make(chan struct{})}
	go r.reap()
	return r
}

// Status is the engine's state now.
func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Status{State: r.state, Error: r.stateErr, Since: r.since}
	if r.pkg != nil {
		s.App, s.Version = r.pkg.App, r.pkg.Version
	}
	return s
}

// Ready reports whether jobs run now. From memory: thumbnails ask it for
// every listed office file.
func (r *Runner) Ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pkg != nil && r.state == StateReady && !r.closed
}

// SetPackage switches the runner to an engine package (nil: none). The same
// package again changes nothing; another one ends every process of the
// previous one and prepares the new one unless this machine already has.
func (r *Runner) SetPackage(p *Package) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	switch {
	case p == nil && r.pkg == nil:
		return
	case p != nil && r.pkg != nil && *p == *r.pkg:
		return
	}
	r.gen++
	r.endAllLocked()
	r.pkg = nil
	if p == nil {
		r.setStateLocked(StateAbsent, "")
		return
	}
	cp := *p
	r.pkg = &cp
	if r.preparedLocked() {
		r.setStateLocked(StateReady, "")
		return
	}
	r.startPrepareLocked()
}

// Release ends every process of an app's engine and forgets the package -
// before the app's files are moved (an upgrade, a removal): on Windows a
// file a process holds open cannot be moved. SetPackage brings it back.
func (r *Runner) Release(app string) {
	r.mu.Lock()
	match := r.pkg != nil && r.pkg.App == app
	r.mu.Unlock()
	if match {
		r.SetPackage(nil)
	}
}

// Close ends every process; the runner takes no more jobs.
func (r *Runner) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	close(r.quit)
	r.endAllLocked()
}

func (r *Runner) setStateLocked(s State, why string) {
	if r.state != s || r.stateErr != why {
		r.since = time.Now()
	}
	r.state, r.stateErr = s, why
}

// endAllLocked ends every process, the prepare run included, and wakes the
// jobs waiting for a slot (they see the new generation).
func (r *Runner) endAllLocked() {
	if r.stopPrep != nil {
		r.stopPrep()
		r.stopPrep = nil
	}
	if r.retry != nil {
		r.retry.Stop()
		r.retry = nil
	}
	for _, pl := range r.pools {
		for _, p := range pl.idle {
			go p.end()
		}
		pl.idle = nil
		pl.signal()
	}
	// A process running a job, and the prepare run, end now; whoever runs
	// it sees its job fail and ends it (release, spawn).
	for p := range r.live {
		p.kill()
	}
}

// ── Preparing ───────────────────────────────────────────────────────────

func (r *Runner) markerPath(p *Package) string {
	return filepath.Join(r.o.CacheDir, "prepared", p.key())
}

func (r *Runner) preparedLocked() bool {
	if r.pkg == nil || r.o.CacheDir == "" {
		return false
	}
	_, err := os.Stat(r.markerPath(r.pkg))
	return err == nil
}

func (r *Runner) startPrepareLocked() {
	r.setStateLocked(StatePreparing, "")
	ctx, cancel := context.WithTimeout(context.Background(), r.o.PrepareTimeout)
	r.stopPrep = cancel
	gen, pkg := r.gen, *r.pkg
	go r.prepare(ctx, cancel, gen, pkg)
}

// prepare runs one prepare process: it compiles both modules into the cache
// and starts a worker of each kind once, then ends.
func (r *Runner) prepare(ctx context.Context, cancel context.CancelFunc, gen int, pkg Package) {
	defer cancel()
	t0 := time.Now()
	r.o.Log.Info("office engine: preparing", slog.String("app", pkg.App), slog.String("version", pkg.Version))
	pr, hello, err := r.spawn(ctx, &pkg, true)
	if pr != nil {
		pr.end()
	}
	if err == nil && !hello.Prepared {
		err = errors.New("the prepare run did not say it finished")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gen != gen || r.closed {
		return
	}
	r.stopPrep = nil
	if err != nil {
		why := err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			why = "preparing took longer than " + r.o.PrepareTimeout.String()
		}
		r.o.Log.Warn("office engine: could not be prepared", slog.String("app", pkg.App), slog.String("err", why))
		r.setStateLocked(StateFailed, why)
		r.retry = time.AfterFunc(r.o.RetryPrepare, func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.gen == gen && !r.closed && r.state == StateFailed {
				r.startPrepareLocked()
			}
		})
		return
	}
	if r.o.CacheDir != "" {
		m := r.markerPath(&pkg)
		_ = os.MkdirAll(filepath.Dir(m), 0o700)
		_ = os.WriteFile(m, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	}
	r.o.Log.Info("office engine: ready", slog.String("app", pkg.App), slog.Duration("took", time.Since(t0)))
	r.setStateLocked(StateReady, "")
}

// ── Processes ───────────────────────────────────────────────────────────

// proc is one engine process.
type proc struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	out     *bufio.Reader
	outFile *os.File
	stderr  *tail
	work    string
	gen     int
	jobs    int
	used    time.Time
	pid     int
	// memKilled: the watch ended it for its memory.
	memKilled atomic.Bool
	exited    chan struct{}
	endOnce   sync.Once
	r         *Runner
}

// kill ends the process at once (it is the only way to stop a module
// compiled without wazero's context watch).
func (p *proc) kill() {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// end closes the process's stdin (so it ends on its own), kills it if it
// does not, and removes its work directory.
func (p *proc) end() {
	p.endOnce.Do(func() {
		if p.stdin != nil {
			_ = p.stdin.Close()
		}
		select {
		case <-p.exited:
		case <-time.After(5 * time.Second):
			p.kill()
			<-p.exited
		}
		if p.outFile != nil {
			_ = p.outFile.Close()
		}
		if p.work != "" {
			_ = os.RemoveAll(p.work)
		}
		if p.r != nil {
			p.r.mu.Lock()
			delete(p.r.live, p)
			p.r.mu.Unlock()
		}
	})
}

// command is the argv that starts an engine process.
func (r *Runner) command() ([]string, error) {
	if len(r.o.Command) > 0 {
		return r.o.Command, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return []string{exe, "office-engine"}, nil
}

// childEnv is the engine process's environment: the few variables a Go
// program needs to run and to find a temporary directory, never the
// server's own (its secrets stay with it).
func (r *Runner) childEnv(cfg ChildConfig) []string {
	keep := map[string]bool{"PATH": true, "SYSTEMROOT": true, "SystemRoot": true, "TEMP": true, "TMP": true, "TMPDIR": true,
		"HOME": true, "USERPROFILE": true, "LOCALAPPDATA": true, "GOMAXPROCS": true, "LANG": true}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if keep[k] {
			env = append(env, kv)
		}
	}
	b, _ := json.Marshal(cfg)
	env = append(env, ConfigEnv+"="+string(b))
	return append(env, r.o.Env...)
}

// spawn starts an engine process for pkg and reads its Hello.
func (r *Runner) spawn(ctx context.Context, pkg *Package, prepare bool) (*proc, Hello, error) {
	argv, err := r.command()
	if err != nil {
		return nil, Hello{}, err
	}
	work, err := os.MkdirTemp(r.o.WorkDir, "filex-office-")
	if err != nil {
		return nil, Hello{}, err
	}
	cfg := ChildConfig{
		Engine: pkg.Engine, EngineSHA256: pkg.EngineSHA256, Package: pkg.UI, PackageSHA256: pkg.UISHA256,
		EditorDir: pkg.EditorDir, Cache: r.o.CacheDir, Work: work, MaxMB: ClampMaxMB(r.o.MaxMB()), Prepare: prepare,
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = r.childEnv(cfg)
	cmd.Dir = work
	setChildAttr(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(work)
		return nil, Hello{}, err
	}
	// The answers come through an OS pipe of our own, not StdoutPipe: Wait
	// closes StdoutPipe's end when the process exits, and a process that
	// says its last line and ends at once (a prepare run) would lose it.
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = os.RemoveAll(work)
		return nil, Hello{}, err
	}
	cmd.Stdout = outW
	st := &tail{max: 4096}
	cmd.Stderr = st
	err = cmd.Start()
	outW.Close()
	if err != nil {
		outR.Close()
		_ = os.RemoveAll(work)
		return nil, Hello{}, fmt.Errorf("office engine: could not start %s: %w", filepath.Base(argv[0]), err)
	}
	p := &proc{cmd: cmd, stdin: stdin, out: bufio.NewReaderSize(outR, 1<<16), outFile: outR, stderr: st, work: work,
		pid: cmd.Process.Pid, used: time.Now(), exited: make(chan struct{}), r: r}
	go func() {
		_ = cmd.Wait()
		close(p.exited)
	}()
	r.mu.Lock()
	r.live[p] = true
	r.mu.Unlock()

	wait := r.o.StartTimeout
	if prepare {
		wait = r.o.PrepareTimeout
	}
	sctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var hello Hello
	line, err := p.readLine(sctx)
	if err == nil {
		err = json.Unmarshal(line, &hello)
	}
	if err == nil && hello.Error != "" {
		err = errors.New(hello.Error)
	}
	if err != nil {
		p.kill()
		p.end()
		if tailText := strings.TrimSpace(st.String()); tailText != "" && !strings.Contains(err.Error(), tailText) {
			err = fmt.Errorf("%w (%s)", err, clipTail(tailText, 400))
		}
		return nil, hello, err
	}
	return p, hello, nil
}

// readLine reads the process's next line, or ends the process when ctx ends
// first.
func (p *proc) readLine(ctx context.Context) ([]byte, error) {
	type res struct {
		b   []byte
		err error
	}
	got := make(chan res, 1)
	go func() {
		b, err := p.out.ReadBytes('\n')
		got <- res{b, err}
	}()
	select {
	case x := <-got:
		if x.err != nil && len(x.b) == 0 {
			return nil, fmt.Errorf("office engine: the process ended: %w", x.err)
		}
		return x.b, nil
	case <-ctx.Done():
		p.kill()
		<-got
		return nil, ctx.Err()
	}
}

// ── Jobs ────────────────────────────────────────────────────────────────

// notReadyLocked is why a job cannot run now.
func (r *Runner) notReadyLocked() error {
	switch {
	case r.closed:
		return ErrClosed
	case r.pkg == nil:
		return ErrNoEngine
	case r.state == StatePreparing:
		return ErrPreparing
	case r.state == StateFailed:
		return fmt.Errorf("%w: %s", ErrFailed, r.stateErr)
	case r.state != StateReady:
		return ErrNoEngine
	}
	return nil
}

func (p *pool) signal() {
	if p.wake != nil {
		close(p.wake)
		p.wake = nil
	}
}

// acquire takes an idle process of the purpose's pool, starts one when the
// pool has a free slot, or waits for one.
func (r *Runner) acquire(ctx context.Context, purpose Purpose) (*proc, error) {
	for {
		r.mu.Lock()
		if err := r.notReadyLocked(); err != nil {
			r.mu.Unlock()
			return nil, err
		}
		pl := r.pools[purpose]
		if pl == nil {
			pl = &pool{}
			r.pools[purpose] = pl
		}
		for len(pl.idle) > 0 {
			p := pl.idle[len(pl.idle)-1]
			pl.idle = pl.idle[:len(pl.idle)-1]
			if p.gen == r.gen {
				pl.busy++
				r.mu.Unlock()
				return p, nil
			}
			go p.end()
		}
		slots := r.o.Slots()
		if slots < 1 {
			slots = 1
		}
		if pl.busy < slots {
			pl.busy++
			gen, pkg := r.gen, *r.pkg
			r.mu.Unlock()
			p, _, err := r.spawn(ctx, &pkg, false)
			if err != nil {
				r.mu.Lock()
				pl.busy--
				pl.signal()
				r.mu.Unlock()
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, &Error{Code: CodeUnknown, What: "start", Err: err}
			}
			p.gen = gen
			return p, nil
		}
		if pl.wake == nil {
			pl.wake = make(chan struct{})
		}
		wake := pl.wake
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wake:
		}
	}
}

// release gives a process back to its pool, or ends it: after a failed round
// trip, after JobsPerProcess jobs, or when it belongs to an older package.
func (r *Runner) release(purpose Purpose, p *proc, reuse bool) {
	r.mu.Lock()
	pl := r.pools[purpose]
	pl.busy--
	keep := reuse && !r.closed && p.gen == r.gen && p.jobs < r.o.JobsPerProcess
	if keep {
		p.used = time.Now()
		pl.idle = append(pl.idle, p)
	}
	pl.signal()
	r.mu.Unlock()
	if !keep {
		go p.end()
	}
}

// processLimit is the most memory a process may hold now.
func (r *Runner) processLimit() int64 {
	if r.o.ProcessLimit != nil {
		return r.o.ProcessLimit()
	}
	return int64(2*ClampMaxMB(r.o.MaxMB())) << 20
}

// watchEvery is how often a running job's process memory is read.
const watchEvery = 500 * time.Millisecond

// do runs one request in a process of the purpose's pool.
func (r *Runner) do(ctx context.Context, purpose Purpose, req Request) (*Answer, error) {
	p, err := r.acquire(ctx, purpose)
	if err != nil {
		return nil, err
	}
	ans, err := r.roundTrip(ctx, p, req)
	r.release(purpose, p, err == nil)
	if err != nil {
		return nil, err
	}
	return ans, answerError(ans)
}

// roundTrip writes one request and reads its answer, watching the process's
// memory meanwhile. When ctx ends first the process is killed: the job's
// deadline is filex's to keep.
func (r *Runner) roundTrip(ctx context.Context, p *proc, req Request) (*Answer, error) {
	req.ID = r.nextID.Add(1)
	p.jobs++
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := p.stdin.Write(append(b, '\n')); err != nil {
		p.kill()
		return nil, &Error{Code: CodeUnknown, What: "crash", Err: err}
	}
	type res struct {
		b   []byte
		err error
	}
	got := make(chan res, 1)
	go func() {
		line, err := p.out.ReadBytes('\n')
		got <- res{line, err}
	}()
	limit := r.processLimit()
	tick := time.NewTicker(watchEvery)
	defer tick.Stop()
	for {
		select {
		case x := <-got:
			if x.err != nil && len(x.b) == 0 {
				if p.memKilled.Load() {
					return nil, &Error{Code: CodeTooLarge, What: "memory", Err: fmt.Errorf("the engine process passed %d MiB", limit>>20)}
				}
				return nil, &Error{Code: CodeUnknown, What: "crash", Err: fmt.Errorf("the engine process ended: %s", clipTail(p.stderr.String(), 400))}
			}
			var ans Answer
			if err := json.Unmarshal(x.b, &ans); err != nil {
				p.kill()
				return nil, &Error{Code: CodeUnknown, What: "answer", Err: err}
			}
			if ans.ID != req.ID {
				p.kill()
				return nil, &Error{Code: CodeUnknown, What: "answer", Err: fmt.Errorf("answer %d for request %d", ans.ID, req.ID)}
			}
			return &ans, nil
		case <-ctx.Done():
			p.kill()
			<-got
			return nil, ctx.Err()
		case <-tick.C:
			if limit <= 0 {
				continue
			}
			if rss, ok := processRSS(p.pid); ok && rss > limit {
				r.o.Log.Warn("office engine: a process passed its memory limit; ended", slog.Int64("rss_mb", rss>>20), slog.Int64("limit_mb", limit>>20))
				p.memKilled.Store(true)
				p.kill()
			}
		}
	}
}

// reap ends processes left idle for IdleTimeout.
func (r *Runner) reap() {
	every := 15 * time.Second
	if half := r.o.IdleTimeout / 2; half < every {
		every = half
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-r.quit:
			return
		case <-t.C:
		}
		var old []*proc
		r.mu.Lock()
		cut := time.Now().Add(-r.o.IdleTimeout)
		for _, pl := range r.pools {
			keep := pl.idle[:0]
			for _, p := range pl.idle {
				if p.used.Before(cut) {
					old = append(old, p)
				} else {
					keep = append(keep, p)
				}
			}
			pl.idle = keep
		}
		r.mu.Unlock()
		for _, p := range old {
			p.end()
		}
	}
}

// ── What the server asks ───────────────────────────────────────────────

// Conversion is one office conversion (the office engine apps use).
type Conversion struct {
	// Src is the input, From its format; To the conversion API's output type.
	Src, From, To string
	// Delimiter and CodePage are the conversion API's CSV knobs.
	Delimiter, CodePage int
	// Dst is where the result goes (it must not exist); MaxBytes its ceiling.
	Dst      string
	MaxBytes int64
}

// Convert runs one conversion. A failure is an *Error (Over: the result was
// over MaxBytes), ErrNoEngine / ErrPreparing / ErrFailed when the engine
// does not run, or ctx's error when the deadline passed.
func (r *Runner) Convert(ctx context.Context, c Conversion) error {
	_, err := r.do(ctx, PurposeConvert, Request{
		Op: OpConvert, In: c.Src, From: c.From, To: c.To, Out: c.Dst, MaxBytes: c.MaxBytes,
		Delimiter: c.Delimiter, CodePage: c.CodePage,
	})
	if err != nil {
		_ = os.Remove(c.Dst)
	}
	return err
}

// Thumbnail is one picture of a document's first page.
type Thumbnail struct {
	// Src is the document, From its format.
	Src, From string
	// Size is the box the page is fitted into; Params the editor's json
	// parameters (a sheet's page layout); MaxBytes the largest picture.
	Size     int
	Params   json.RawMessage
	MaxBytes int64
}

// DrawThumbnail draws a PNG of the document's first page.
func (r *Runner) DrawThumbnail(ctx context.Context, t Thumbnail) ([]byte, error) {
	dir, err := os.MkdirTemp(r.o.WorkDir, "filex-office-thumb-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "page.png")
	if _, err := r.do(ctx, PurposeThumbnail, Request{
		Op: OpThumbnail, In: t.Src, From: t.From, Out: out, Size: t.Size, Params: t.Params, MaxBytes: t.MaxBytes,
	}); err != nil {
		return nil, err
	}
	return os.ReadFile(out)
}

// tail keeps the last max bytes written to it, safely for the goroutine
// exec copies a process's stderr from.
type tail struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func clipTail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
