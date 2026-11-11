package wasmplugin

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	extism "github.com/extism/go-sdk"
)

// ── Warm instances for the calls a stranger can make often ─────────────────
//
// Every other call starts a fresh instance (Compiled.Call): the guest's
// runtime starts, the call runs, the instance is thrown away. Measured on a
// 12-thread workstation (2026-11-11, the echo fixture): 10 ms and 15 MB of
// allocation a call, about a hundred calls a second a core. A public route
// (a short link) is opened by every person it is sent to and every chat
// preview of it, and by whoever wants to flood it, so its calls - `route`,
// and `shorten` beside it - run on instances kept started: about 0.2 ms a
// call, 3 MB resident each.
//
// The pool is also the wall a flood meets:
//
//   - at most `size` instances, so a flood takes at most `size` CPUs and the
//     rest of filex (the explorer, the API, shares) keeps the others;
//   - at most `queue` calls wait, each for at most its own wait, then the
//     answer is "busy" (the route door says 503 with Retry-After);
//   - a call has its budget; a call that outlives it, crashes or runs out
//     of memory takes its instance with it (the next call gets a fresh one),
//     and an instance is retired after maxCallsPerInstance calls anyway;
//   - a breaker: when half of the last breakerWindow calls failed, the app
//     is not called for breakerOpen and every call is answered "busy" at
//     once; then one call is let through to try.
//
// ⚠ A warm instance keeps its globals from one call to the next. That is
// the point (an app's cache survives), and it is why the guest SDK tells
// authors never to keep one visit's facts in a global.

const (
	maxPoolSize         = 16
	maxCallsPerInstance = 10000
	breakerWindow       = 20
	breakerOpen         = 30 * time.Second
)

// poolSize is the size the operator asked for (0: half the CPUs), 1..16.
func poolSize(asked int) int {
	n := asked
	if n <= 0 {
		n = runtime.GOMAXPROCS(0) / 2
	}
	return min(max(n, 1), maxPoolSize)
}

type warm struct {
	inst  *extism.Plugin
	calls int
	// busy is set while a call runs on the instance: an extism Plugin - and
	// the functions it keeps (third_party/extism-go-sdk/FILEX.md) - is for
	// one caller at a time.
	busy atomic.Bool
}

type hotPool struct {
	c     *Compiled
	size  int
	queue int32

	idle    chan *warm
	slots   chan struct{}
	waiting atomic.Int32
	// inUse is how many calls run right now (tests read the high-water mark).
	inUse, peak atomic.Int32
	// overlaps counts calls handed an instance another call was still
	// using: never, unless the pool is broken (tests read it).
	overlaps atomic.Int32

	mu     sync.Mutex
	closed bool
	br     breaker
}

func newHotPool(c *Compiled, size int) *hotPool {
	return &hotPool{
		c: c, size: size, queue: int32(2 * size),
		idle:  make(chan *warm, size),
		slots: make(chan struct{}, size),
	}
}

// errPoolBusy: every instance is taken and the queue is full, or the wait
// ran out, or the breaker is open.
func errPoolBusy(export, why string) *CallError {
	return &CallError{Code: CodeBusy, Export: export, Message: why}
}

func (h *hotPool) acquire(ctx context.Context, export string, wait time.Duration) (*warm, error) {
	if !h.br.allow(time.Now()) {
		return nil, errPoolBusy(export, "the app is not answering; it is left alone for a moment")
	}
	select {
	case w := <-h.idle:
		return w, nil
	default:
	}
	select {
	case h.slots <- struct{}{}:
		w, err := h.start(ctx)
		if err != nil {
			<-h.slots
			return nil, err
		}
		return w, nil
	default:
	}
	if h.waiting.Add(1) > h.queue {
		h.waiting.Add(-1)
		return nil, errPoolBusy(export, "the app is busy; try again in a moment")
	}
	defer h.waiting.Add(-1)
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case w := <-h.idle:
		return w, nil
	case <-t.C:
	case <-ctx.Done():
	}
	return nil, errPoolBusy(export, "the app is busy; try again in a moment")
}

func (h *hotPool) start(ctx context.Context) (*warm, error) {
	var clock AppClock
	if h.c.rt != nil {
		clock = h.c.rt.clock
	}
	// The start-up has its own budget: it is paid once per instance, not per
	// call, and a cold module on a busy machine takes longer than a call.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	inst, err := h.c.cp.Instance(sctx, extism.PluginInstanceConfig{ModuleConfig: clock.moduleConfig()})
	if err != nil {
		return nil, &CallError{Code: CodePluginTrap, Message: "the plugin could not be started", Cause: err}
	}
	logger := h.c.logger
	inst.SetLogger(func(level extism.LogLevel, msg string) {
		if level <= extism.LogLevelDebug && sdkChatter(msg) {
			return
		}
		logger(levelName(level), msg)
	})
	return &warm{inst: inst}, nil
}

// release hands an instance back, or retires it.
func (h *hotPool) release(w *warm, keep bool) {
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if keep && !closed && w.calls < maxCallsPerInstance {
		select {
		case h.idle <- w:
			return
		default:
		}
	}
	_ = w.inst.Close(context.Background())
	<-h.slots
}

// call runs one export on a warm instance.
func (h *hotPool) call(ctx context.Context, export string, input []byte, budget, wait time.Duration) ([]byte, error) {
	w, err := h.acquire(ctx, export, wait)
	if err != nil {
		return nil, err
	}
	if !w.busy.CompareAndSwap(false, true) {
		h.overlaps.Add(1)
	}
	// done gives the instance back: free first, so whoever takes it next
	// finds it free.
	done := func(keep bool) {
		w.busy.Store(false)
		h.release(w, keep)
	}
	n := h.inUse.Add(1)
	for {
		p := h.peak.Load()
		if n <= p || h.peak.CompareAndSwap(p, n) {
			break
		}
	}
	defer h.inUse.Add(-1)
	cctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if !w.inst.FunctionExists(export) {
		done(true)
		return nil, &CallError{Code: CodePluginError, Export: export, Message: "the plugin has no " + export + " export"}
	}
	w.calls++
	started := time.Now()
	exit, out, err := w.inst.CallWithContext(cctx, export, input)
	switch {
	case err != nil:
		// A call cut by its budget closed its module (WithCloseOnContextDone),
		// a trap left it in no state to trust: either way it goes.
		cerr := classify(cctx, export, err, exit, w.inst.GetErrorWithContext(context.WithoutCancel(cctx)), time.Since(started), budget)
		done(false)
		h.br.record(time.Now(), true)
		return nil, cerr
	case exit != 0:
		msg := strings.TrimSpace(w.inst.GetErrorWithContext(context.WithoutCancel(cctx)))
		if msg == "" {
			msg = fmt.Sprintf("exit code %d", exit)
		}
		// The guest answered with an error of its own: the instance is
		// fine, the answer is not.
		done(true)
		h.br.record(time.Now(), true)
		return nil, &CallError{Code: CodePluginError, Export: export, Message: clip(msg, 1000)}
	}
	cp := append([]byte(nil), out...)
	done(true)
	h.br.record(time.Now(), false)
	return cp, nil
}

// close retires every idle instance; the ones in use go when they come back.
func (h *hotPool) close() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	for {
		select {
		case w := <-h.idle:
			_ = w.inst.Close(context.Background())
			<-h.slots
		default:
			return
		}
	}
}

// breaker opens when half of the last breakerWindow calls failed.
type breaker struct {
	mu        sync.Mutex
	ring      [breakerWindow]bool
	n, next   int
	fails     int
	openUntil time.Time
	probing   bool
}

func (b *breaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true
	}
	if now.Before(b.openUntil) || b.probing {
		return false
	}
	b.probing = true
	return true
}

func (b *breaker) record(now time.Time, failed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.probing {
		b.probing = false
		if failed {
			b.openUntil = now.Add(breakerOpen)
			return
		}
		*b = breaker{}
		return
	}
	if b.n == breakerWindow && b.ring[b.next] {
		b.fails--
	}
	b.ring[b.next] = failed
	if failed {
		b.fails++
	}
	b.next = (b.next + 1) % breakerWindow
	if b.n < breakerWindow {
		b.n++
	}
	if b.n == breakerWindow && b.fails*2 >= breakerWindow {
		b.openUntil = now.Add(breakerOpen)
	}
}

// open reports whether the breaker refuses calls now.
func (b *breaker) open(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.openUntil.IsZero() && (now.Before(b.openUntil) || b.probing)
}

// ── The pool of an app ─────────────────────────────────────────────────────

// hot is the pool of the app's compiled module: made on the first call,
// replaced when the module is (an upgrade, a reload), and closed with it.
func (c *Compiled) hot(size int) *hotPool {
	c.hotMu.Lock()
	defer c.hotMu.Unlock()
	if c.hotPool == nil {
		c.hotPool = newHotPool(c, size)
	}
	return c.hotPool
}

// closeHot retires the pool's instances (Compiled.Close).
func (c *Compiled) closeHot() {
	c.hotMu.Lock()
	h := c.hotPool
	c.hotPool = nil
	c.hotMu.Unlock()
	if h != nil {
		h.close()
	}
}

// hotCall runs export on the app's pool under scope.
func (r *Registry) hotCall(ctx context.Context, c *Compiled, s *Scope, export string, input []byte, budget, wait time.Duration) ([]byte, error) {
	if c == nil {
		return nil, errors.New("wasmplugin: no module")
	}
	return c.hot(poolSize(r.opts.RoutePool)).call(WithScope(ctx, s), export, input, budget, wait)
}
