// Package external is the one place the runtime asks "how is OnlyOffice /
// drawio configured right now?".
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
	// EditorLang is the administrator's ONLYOFFICE editor language: "" or
	// "auto" (each person's own) or a language code, AS STORED - the
	// onlyoffice package reads it (onlyoffice.NormalizeEditorLang) and treats
	// anything it does not offer as "auto". Set on External services or by
	// FILEX_ONLYOFFICE_LANG; in options_json like CallbackURL, so no migration.
	EditorLang string
}

// OptionKeyCallbackURL is where CallbackURL lives inside options_json.
const OptionKeyCallbackURL = "callback_url"

// OptionKeyEditorLang is where EditorLang lives inside options_json.
const OptionKeyEditorLang = "editor_lang"

// EnvPinEditorLang is the key, in the "pinned by the environment" map the
// admin handler is given (api.envManagedExternal), that says
// FILEX_ONLYOFFICE_LANG pins ONLYOFFICE's editor language. The map's other
// keys are service names; this one is a field of one.
const EnvPinEditorLang = OnlyOffice + ".editor_lang"

// CallbackURLFromOptions reads the callback URL out of a row's options blob.
// A malformed blob reads as empty rather than failing: this is configuration
// the operator can retype, not a reason to refuse to serve documents.
func CallbackURLFromOptions(optionsJSON string) string {
	return strings.TrimRight(optionString(optionsJSON, OptionKeyCallbackURL), "/")
}

// WithCallbackURL returns optionsJSON with the callback URL set (or removed,
// when url is empty), preserving every other key the blob carries.
func WithCallbackURL(optionsJSON, rawURL string) (string, error) {
	return withOption(optionsJSON, OptionKeyCallbackURL, strings.TrimRight(strings.TrimSpace(rawURL), "/"))
}

// EditorLangFromOptions reads the editor language out of a row's options
// blob, as stored ("" when absent or the blob is malformed).
func EditorLangFromOptions(optionsJSON string) string {
	return optionString(optionsJSON, OptionKeyEditorLang)
}

// WithEditorLang returns optionsJSON with the editor language set, or
// removed when lang is "" or "auto" (the default needs no key), preserving
// every other key the blob carries. The caller has checked lang
// (onlyoffice.NormalizeEditorLang).
func WithEditorLang(optionsJSON, lang string) (string, error) {
	lang = strings.TrimSpace(lang)
	if strings.EqualFold(lang, "auto") {
		lang = ""
	}
	return withOption(optionsJSON, OptionKeyEditorLang, lang)
}

// optionString is one string option out of a row's options blob, trimmed.
// A malformed blob reads as empty rather than failing.
func optionString(optionsJSON, key string) string {
	if strings.TrimSpace(optionsJSON) == "" {
		return ""
	}
	var opts map[string]any
	if err := json.Unmarshal([]byte(optionsJSON), &opts); err != nil {
		return ""
	}
	v, _ := opts[key].(string)
	return strings.TrimSpace(v)
}

// withOption returns optionsJSON with one string option set (or removed, when
// value is empty), preserving every other key the blob carries.
func withOption(optionsJSON, key, value string) (string, error) {
	opts := map[string]any{}
	if strings.TrimSpace(optionsJSON) != "" {
		if err := json.Unmarshal([]byte(optionsJSON), &opts); err != nil {
			// Do not silently drop an operator's other options.
			return "", fmt.Errorf("external: options_json is not an object: %w", err)
		}
	}
	if value == "" {
		delete(opts, key)
	} else {
		opts[key] = value
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
			EditorLang:  EditorLangFromOptions(row.OptionsJSON),
		}
	}
	r.mu.Lock()
	r.cache = next
	r.loaded = time.Now()
	r.mu.Unlock()
	return next[name]
}

// URL is the convenience form for the services that need nothing but the base
// URL (drawio). Empty when the service is not usable.
func (r *Resolver) URL(ctx context.Context, name string) string {
	return r.Get(ctx, name).URL
}
