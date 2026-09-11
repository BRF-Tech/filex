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
	"encoding/json"
	"fmt"
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
	// CallbackURL is the address the SERVICE uses to reach filex — the third
	// address in the three-address problem (see advisory.go). Empty means "use
	// filex's public URL", which is right whenever one address serves both
	// purposes.
	//
	// ⚠ It exists because that is not always true. filex has ONE public URL
	// and it builds both the share links people click and the document URL it
	// hands the document server; a container that cannot resolve the public
	// hostname needs a different address for the second, and until this field
	// there was no way to give it one — the document opened and the save never
	// came back (issue #17, third round). Stored in options_json so no
	// migration is needed and the row keeps one shape.
	CallbackURL string
}

// OptionKeyCallbackURL is where CallbackURL lives inside options_json.
const OptionKeyCallbackURL = "callback_url"

// CallbackURLFromOptions reads the callback URL out of a row's options blob.
// A malformed blob reads as empty rather than failing: this is configuration
// the operator can retype, not a reason to refuse to serve documents.
func CallbackURLFromOptions(optionsJSON string) string {
	if strings.TrimSpace(optionsJSON) == "" {
		return ""
	}
	var opts map[string]any
	if err := json.Unmarshal([]byte(optionsJSON), &opts); err != nil {
		return ""
	}
	v, _ := opts[OptionKeyCallbackURL].(string)
	return strings.TrimRight(strings.TrimSpace(v), "/")
}

// WithCallbackURL returns optionsJSON with the callback URL set (or removed,
// when url is empty), preserving every other key the blob carries.
func WithCallbackURL(optionsJSON, rawURL string) (string, error) {
	opts := map[string]any{}
	if strings.TrimSpace(optionsJSON) != "" {
		if err := json.Unmarshal([]byte(optionsJSON), &opts); err != nil {
			// Do not silently drop an operator's other options.
			return "", fmt.Errorf("external: options_json is not an object: %w", err)
		}
	}
	clean := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if clean == "" {
		delete(opts, OptionKeyCallbackURL)
	} else {
		opts[OptionKeyCallbackURL] = clean
	}
	out, err := json.Marshal(opts)
	if err != nil {
		return "", err
	}
	return string(out), nil
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
			Enabled:     row.Enabled && url != "",
			URL:         url,
			Secret:      row.SecretEnc,
			CallbackURL: CallbackURLFromOptions(row.OptionsJSON),
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
