package space

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/storage"
)

const (
	// ttl is how long a storage's answer is reused. Explorer, Mountain Duck and
	// WinSCP ask on every folder they show; a share or a remote server must not
	// be asked that often.
	ttl = 30 * time.Second
	// askTimeout bounds one question to a backend. A storage that does not
	// answer in time is unknown for ttl, not a hung `df`.
	askTimeout = 5 * time.Second
	// maxEntries bounds the cache: one entry per storage and path asked about.
	maxEntries = 1024
)

// Service answers for storages and for the virtual root. One per server, so
// every surface shares the cache.
type Service struct {
	resolve func(int64) (storage.Driver, error)
	quota   *quota.Service
	now     func() time.Time
	timeout time.Duration

	mu    sync.Mutex
	cache map[cacheKey]answer
}

type cacheKey struct {
	storage int64
	rel     string
}

type answer struct {
	sp storage.Space
	ok bool
	at time.Time
}

// New builds a service over the live driver resolver and the quota service
// (nil: no account has a quota).
func New(resolve func(int64) (storage.Driver, error), q *quota.Service) *Service {
	return &Service{resolve: resolve, quota: q, now: time.Now, timeout: askTimeout, cache: map[cacheKey]answer{}}
}

// Ensure returns s, or a service of its own over resolve and q when s is nil:
// a protocol server whose wiring line was lost still answers with real
// figures, only without the shared cache.
func Ensure(s *Service, resolve func(int64) (storage.Driver, error), q *quota.Service) *Service {
	if s != nil {
		return s
	}
	return New(resolve, q)
}

// Storage is the room at rel inside st, for the account signed in on ctx.
func (s *Service) Storage(ctx context.Context, st *model.Storage, rel string) Figures {
	if s == nil {
		return Figures{}
	}
	sp, ok := s.measure(ctx, st, rel)
	f := Figures{}
	if ok {
		f = Figures{Total: sp.Total, Used: sp.Used, Free: sp.Free, Known: true}
	}
	return s.withQuota(ctx, f)
}

// Root is the room at the virtual root over sts, the storages the caller can
// see: every storage's figures added up, a Volume two of them share counted
// once. Storages that cannot say are left out of the sum rather than guessed.
// For the account signed in on ctx.
func (s *Service) Root(ctx context.Context, sts []*model.Storage) Figures {
	if s == nil {
		return Figures{}
	}
	type result struct {
		sp storage.Space
		ok bool
	}
	// Asked side by side: a root with a slow share and a dead server waits
	// for the slower of them once, not for the sum.
	results := make([]result, len(sts))
	var wg sync.WaitGroup
	for i, st := range sts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sp, ok := s.measure(ctx, st, "")
			results[i] = result{sp, ok}
		}()
	}
	wg.Wait()

	var sum Figures
	seen := map[string]bool{}
	for _, r := range results {
		if !r.ok {
			continue
		}
		if r.sp.Volume != "" {
			if seen[r.sp.Volume] {
				continue
			}
			seen[r.sp.Volume] = true
		}
		sum.Total += r.sp.Total
		sum.Used += r.sp.Used
		sum.Free += r.sp.Free
		sum.Known = true
	}
	return s.withQuota(ctx, sum)
}

// measure asks st's driver, through the cache.
func (s *Service) measure(ctx context.Context, st *model.Storage, rel string) (storage.Space, bool) {
	if st == nil || s.resolve == nil {
		return storage.Space{}, false
	}
	k := cacheKey{st.ID, rel}
	now := s.now()
	s.mu.Lock()
	if a, hit := s.cache[k]; hit && now.Sub(a.at) < ttl {
		s.mu.Unlock()
		return a.sp, a.ok
	}
	s.mu.Unlock()

	drv, err := s.resolve(st.ID)
	var sp storage.Space
	if err == nil {
		actx, cancel := context.WithTimeout(ctx, s.timeout)
		sp, err = storage.SpaceOf(actx, drv, rel)
		cancel()
	}
	// The caller hung up: say nothing, and do not remember its cancellation as
	// the storage's answer.
	if ctx.Err() != nil {
		return storage.Space{}, false
	}
	ok := err == nil && sp.Total > 0
	if err != nil && !errors.Is(err, storage.ErrUnsupported) {
		slog.Debug("space: storage did not say how much room it has",
			slog.String("storage", st.Name), slog.String("err", err.Error()))
	}
	s.mu.Lock()
	if len(s.cache) >= maxEntries {
		clear(s.cache)
	}
	s.cache[k] = answer{sp: sp, ok: ok, at: now}
	s.mu.Unlock()
	return sp, ok
}

// withQuota caps f by the account's quota, which is a ceiling of its own: an
// account with 10 GB left on a disk with 2 TB free can write 10 GB, and an
// account with 10 GB left on a disk with 2 GB free can write 2 GB. Used is the
// account's own, across every storage, since that is what the quota counts.
// No account on ctx (a public link, a system job): no quota.
func (s *Service) withQuota(ctx context.Context, f Figures) Figures {
	u := auth.UserFrom(ctx)
	if u == nil || u.ID <= 0 || s.quota == nil {
		return f
	}
	snap, err := s.quota.Get(ctx, u.ID)
	if err != nil || snap.Unlimited || snap.QuotaBytes <= 0 {
		return f
	}
	limit := uint64(snap.QuotaBytes)
	used := uint64(max(snap.UsedBytes, 0))
	out := Figures{Total: limit, Used: used, Known: true}
	if used < limit {
		out.Free = limit - used
	}
	if f.Known {
		out.Total = min(out.Total, f.Total)
		out.Free = min(out.Free, f.Free)
	}
	out.Used = min(out.Used, out.Total)
	return out
}
