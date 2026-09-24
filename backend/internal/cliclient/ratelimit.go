package cliclient

import (
	"context"
	"io"
	"sync"
	"time"
)

// RateLimiter caps a direction of traffic at a number of bytes per second.
//
// One limiter is shared by every transfer of that direction, so the four
// parallel transfers of a sync run TOGETHER stay under the limit — which is
// the point: a nine-hour first sync saturated a server's ~18 Mbit line and
// everyone else using that server slowed down, and `--transfers` only
// changes how many files move at once, not how fast.
//
// It paces the bodies, never the connection: HTTP/2 health pings and window
// updates share the connection, and a throttled connection would delay them
// past the client's own dead-connection timeout.
//
// A token bucket holding one second's worth of bytes; a stream that runs ahead
// goes into debt and sleeps it off, so concurrent streams share the rate
// fairly. A nil *RateLimiter never waits.
type RateLimiter struct {
	mu     sync.Mutex
	rate   float64 // bytes per second
	burst  float64
	tokens float64
	last   time.Time
}

// NewRateLimiter returns a limiter for bytesPerSecond, or nil (no limit) for
// zero or less.
func NewRateLimiter(bytesPerSecond int64) *RateLimiter {
	if bytesPerSecond <= 0 {
		return nil
	}
	r := float64(bytesPerSecond)
	return &RateLimiter{rate: r, burst: r, tokens: r, last: time.Now()}
}

// WaitN takes n bytes' worth from the bucket, sleeping as long as that puts
// the bucket in debt. It returns early with the context's error.
func (l *RateLimiter) WaitN(ctx context.Context, n int) error {
	if l == nil || n <= 0 {
		return nil
	}
	l.mu.Lock()
	now := time.Now()
	l.tokens += now.Sub(l.last).Seconds() * l.rate
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	l.last = now
	l.tokens -= float64(n)
	var wait time.Duration
	if l.tokens < 0 {
		wait = time.Duration(-l.tokens / l.rate * float64(time.Second))
	}
	l.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Reader paces r through the limiter. A nil limiter returns r itself.
func (l *RateLimiter) Reader(ctx context.Context, r io.Reader) io.Reader {
	if l == nil {
		return r
	}
	return &pacedReader{ctx: ctx, r: r, l: l}
}

type pacedReader struct {
	ctx context.Context
	r   io.Reader
	l   *RateLimiter
}

// pacedChunk bounds one read, so a big buffer cannot take a whole second's
// worth in one gulp and then stall.
const pacedChunk = 32 << 10

func (p *pacedReader) Read(b []byte) (int, error) {
	if len(b) > pacedChunk {
		b = b[:pacedChunk]
	}
	n, err := p.r.Read(b)
	if n > 0 {
		if werr := p.l.WaitN(p.ctx, n); werr != nil {
			return n, werr
		}
	}
	return n, err
}
