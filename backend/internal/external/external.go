// Package external is the one place the runtime asks "how is OnlyOffice /
// drawio / the converter configured right now?".
//
// # Why this exists
//
// Before it, the answer came from two places that could not agree. The admin
// UI wrote `external_services` rows and the capability probe read them, so
// `GET /api/files/capabilities` — the thing the explorer consults to decide
// whether to offer the Office editor at all — reported the operator's edit
// immediately. But the code that USES the configuration read
// `cfg.ExternalServices`, a snapshot taken from env/YAML once at boot: the
// OnlyOffice service was constructed (or not) in server.New, and the converter
// URL was baked into the AI handlers in routes.go. An operator who configured
// the services in the admin UI — the documented way, and the only way when the
// services live in a separate compose file — therefore got a green Test button,
// a capability payload that said "configured", and a 503
// `{"error":"onlyoffice not configured"}` the moment they opened a document
// (issue #17). Thumbnails kept working throughout, because the thumbnailer
// shells out to local binaries and never consults external services at all —
// that asymmetry is what named the bug.
//
// # The rule
//
// The DB row is the single runtime source of truth. Env/YAML is declarative
// configuration for it: a non-empty value is ASSERTED onto the row at every
// boot (see server.seedExternalDefaults), so a compose-managed install keeps
// behaving exactly as before and an edit to FILEX_ONLYOFFICE_URL still takes
// effect. Anything env does not pin is owned by the admin UI and applies live,
// with no restart.
package external

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
)

// Known service names. These are the rows server.seedExternalDefaults creates.
const (
	OnlyOffice = "onlyoffice"
	Drawio     = "drawio"
	Convert    = "convert"
)

// Settings is one service's live configuration.
type Settings struct {
	Enabled bool
	URL     string
	Secret  string
}

// Resolver answers from the `external_services` table, with a short cache so a
// per-request lookup does not become a per-request query.
//
// The cache is deliberately short (not "until invalidated"): the admin PATCH
// handler does call Invalidate, but a second filex process behind the same
// database would never see that call, and a stale answer that heals itself in
// a second is a far smaller problem than one that needs a restart — which is
// the bug this package exists to remove.
type Resolver struct {
	store db.Store
	ttl   time.Duration

	mu     sync.RWMutex
	cache  map[string]Settings
	loaded time.Time
}

// New constructs a Resolver. A nil store yields a Resolver that reports
// everything unconfigured, which is what tests and the no-DB paths want.
func New(store db.Store) *Resolver {
	return &Resolver{store: store, ttl: time.Second, cache: map[string]Settings{}}
}

// Invalidate drops the cache so the next Get re-reads the table. Called by the
// admin PATCH handler so an operator's change is visible on the very next
// request rather than up to `ttl` later.
func (r *Resolver) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.loaded = time.Time{}
	r.mu.Unlock()
}

// Get returns the live settings for one service. A missing row, a disabled row
// or a row with no URL all read as "not configured" — Settings.Enabled is only
// true when the service can actually be used.
func (r *Resolver) Get(ctx context.Context, name string) Settings {
	if r == nil || r.store == nil {
		return Settings{}
	}
	r.mu.RLock()
	fresh := !r.loaded.IsZero() && time.Since(r.loaded) < r.ttl
	if fresh {
		s := r.cache[name]
		r.mu.RUnlock()
		return s
	}
	r.mu.RUnlock()

	rows, err := r.store.ListExternalServices(ctx)
	if err != nil {
		// Do NOT poison the cache on a read error: return whatever the last
		// good load said. A database blip must not make a configured editor
		// report itself unconfigured.
		r.mu.RLock()
		s := r.cache[name]
		r.mu.RUnlock()
		return s
	}
	next := make(map[string]Settings, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		url := strings.TrimRight(strings.TrimSpace(row.URL), "/")
		next[row.Name] = Settings{
			Enabled: row.Enabled && url != "",
			URL:     url,
			Secret:  row.SecretEnc,
		}
	}
	r.mu.Lock()
	r.cache = next
	r.loaded = time.Now()
	r.mu.Unlock()
	return next[name]
}

// URL is the convenience form for the services that need nothing but the base
// URL (drawio, the converter). Empty when the service is not usable.
func (r *Resolver) URL(ctx context.Context, name string) string {
	return r.Get(ctx, name).URL
}
