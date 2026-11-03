package captcha

import (
	"context"
	"sync"
	"time"
)

// Marks is the bot check's shared state: the outage flag and the once-only
// marks (a provider token, a local solution, an upload pass). In the
// database (captcha_marks, migration 00134; db.Store satisfies it) because
// several filex instances can share one: the instance that answered 409 and
// the one that receives the solution are not always the same.
//
// Times are unix seconds.
type Marks interface {
	// AddCaptchaMark writes key until expires; false when a mark with that key
	// is still live at now (it was spent already).
	AddCaptchaMark(ctx context.Context, key string, expires, now int64) (bool, error)
	// PutCaptchaMark writes key until expires, over any mark it had.
	PutCaptchaMark(ctx context.Context, key string, expires int64) error
	// CaptchaMarkUntil answers the end of key's mark; ok false when none.
	CaptchaMarkUntil(ctx context.Context, key string) (int64, bool, error)
	// PruneCaptchaMarks deletes the marks that ended before now.
	PruneCaptchaMarks(ctx context.Context, now int64) (int64, error)
}

// The mark keys.
const (
	// FlagFallback is the outage flag: while it lives, a local solution is
	// accepted.
	FlagFallback = "captcha:fallback:active"
	// prefixLocalUsed + signature: a spent local solution.
	prefixLocalUsed = "captcha:local:used:"
	// prefixToken + sha256(token): a provider token already accepted.
	prefixToken = "captcha:token:"
	// prefixPass + pass id: an upload pass already spent.
	prefixPass = "captcha:pass:"
)

// MemoryMarks keeps the marks in this process: a test, or a server built
// without a database.
type MemoryMarks struct {
	mu sync.Mutex
	m  map[string]int64
}

// NewMemoryMarks makes an empty set.
func NewMemoryMarks() *MemoryMarks { return &MemoryMarks{m: map[string]int64{}} }

func (m *MemoryMarks) AddCaptchaMark(_ context.Context, key string, expires, now int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if until, ok := m.m[key]; ok && until > now {
		return false, nil
	}
	m.m[key] = expires
	return true, nil
}

func (m *MemoryMarks) PutCaptchaMark(_ context.Context, key string, expires int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[key] = expires
	return nil
}

func (m *MemoryMarks) CaptchaMarkUntil(_ context.Context, key string) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.m[key]
	return v, ok, nil
}

func (m *MemoryMarks) PruneCaptchaMarks(_ context.Context, now int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for k, v := range m.m {
		if v <= now {
			delete(m.m, k)
			n++
		}
	}
	return n, nil
}

// Len is the number of marks held (tests).
func (m *MemoryMarks) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.m)
}

// unix is t in seconds.
func unix(t time.Time) int64 { return t.Unix() }
