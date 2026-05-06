package queue

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Handler is a function that processes a single Op and returns nil on
// success or an error to trigger retry/fail logic.
//
// Handlers receive the parent ctx — they should respect cancellation so
// graceful shutdown completes within the configured timeout.
type Handler func(ctx context.Context, op Op) error

// Pool runs N goroutines, each looping Dequeue → handler → Ack/Fail.
type Pool struct {
	drv      Driver
	handlers map[string]Handler
	workers  int

	pollInterval time.Duration
	maxBackoff   time.Duration

	stop   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
	mu     sync.RWMutex
	logger *slog.Logger
}

// PoolOption configures a Pool.
type PoolOption func(*Pool)

// WithPollInterval overrides the default 500ms poll cadence used when
// the queue is empty. Lower values reduce latency but increase DB load.
func WithPollInterval(d time.Duration) PoolOption {
	return func(p *Pool) { p.pollInterval = d }
}

// WithMaxBackoff caps the exponential backoff used after errors.
func WithMaxBackoff(d time.Duration) PoolOption {
	return func(p *Pool) { p.maxBackoff = d }
}

// WithLogger plugs in a non-default slog.Logger.
func WithLogger(l *slog.Logger) PoolOption {
	return func(p *Pool) { p.logger = l }
}

// NewPool wires a worker pool with `workers` goroutines. workers must be
// ≥ 1; zero is silently corrected to 1 to avoid a stuck pool.
func NewPool(drv Driver, workers int, opts ...PoolOption) *Pool {
	if workers < 1 {
		workers = 1
	}
	p := &Pool{
		drv:          drv,
		handlers:     map[string]Handler{},
		workers:      workers,
		pollInterval: 500 * time.Millisecond,
		maxBackoff:   30 * time.Second,
		stop:         make(chan struct{}),
		logger:       slog.Default(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Register attaches a handler for the given Op.Type. Re-registering
// overrides — useful in tests.
func (p *Pool) Register(opType string, h Handler) {
	if opType == "" || h == nil {
		return
	}
	p.mu.Lock()
	p.handlers[opType] = h
	p.mu.Unlock()
}

// Types returns the list of currently registered op types — used to
// filter Dequeue so workers don't pick up an op they can't dispatch.
func (p *Pool) Types() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.handlers))
	for t := range p.handlers {
		out = append(out, t)
	}
	return out
}

// Start launches the worker goroutines. Blocks the caller only briefly;
// goroutines run until Stop or ctx is cancelled.
func (p *Pool) Start(ctx context.Context) {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.loop(ctx, i)
	}
}

// Stop signals workers to drain and waits up to 30s for the final op to
// return. Idempotent — calling twice is safe.
func (p *Pool) Stop() {
	p.once.Do(func() { close(p.stop) })
	p.wg.Wait()
}

func (p *Pool) loop(ctx context.Context, workerID int) {
	defer p.wg.Done()
	backoff := p.pollInterval

	logger := p.logger.With(slog.Int("worker", workerID))

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		default:
		}

		types := p.Types()
		op, err := p.drv.Dequeue(ctx, types)
		switch {
		case errors.Is(err, ErrEmpty):
			if !sleepCtx(ctx, p.stop, p.pollInterval) {
				return
			}
			backoff = p.pollInterval
			continue
		case err != nil:
			logger.Warn("queue: dequeue error", slog.String("err", err.Error()))
			if !sleepCtx(ctx, p.stop, backoff) {
				return
			}
			backoff = nextBackoff(backoff, p.maxBackoff)
			continue
		}
		backoff = p.pollInterval

		p.mu.RLock()
		h := p.handlers[op.Type]
		p.mu.RUnlock()
		if h == nil {
			// Unregistered type; park as failed without retry.
			_ = p.drv.Fail(ctx, op.ID, "no handler for type "+op.Type, false)
			logger.Warn("queue: no handler", slog.String("type", op.Type), slog.String("id", op.ID))
			continue
		}
		// Run with a derived context so a poolwide cancel still cuts
		// long-running handlers.
		hctx, cancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-p.stop:
				cancel()
			case <-hctx.Done():
			}
		}()
		hErr := h(hctx, op)
		cancel()

		if hErr == nil {
			if err := p.drv.Ack(ctx, op.ID); err != nil {
				logger.Warn("queue: ack failed", slog.String("err", err.Error()))
			}
			continue
		}
		retry := op.Attempts < op.MaxAttempts
		if err := p.drv.Fail(ctx, op.ID, hErr.Error(), retry); err != nil {
			logger.Warn("queue: fail update failed", slog.String("err", err.Error()))
		}
		if !retry {
			logger.Warn("queue: op exhausted retries",
				slog.String("type", op.Type),
				slog.String("id", op.ID),
				slog.Int("attempts", op.Attempts),
				slog.String("err", hErr.Error()))
		}
	}
}

// sleepCtx blocks for d, returning true when the timer fired and false
// when ctx was cancelled or the pool stopped — callers exit in the
// false case.
func sleepCtx(ctx context.Context, stop <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-stop:
		return false
	case <-t.C:
		return true
	}
}

// nextBackoff doubles d up to max.
func nextBackoff(d, max time.Duration) time.Duration {
	d *= 2
	if d > max {
		return max
	}
	return d
}
