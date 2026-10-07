package appstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/keylock"
	"github.com/brf-tech/filex/backend/internal/memcache"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// httpTimeout bounds one exchange with a store, body included.
const httpTimeout = 30 * time.Second

// StateStore is the part of db.Store this package uses: its own rows
// (app_store_state, migration 00081) and the audit log.
type StateStore interface {
	GetAppStoreState(ctx context.Context, key string) (string, bool, error)
	PutAppStoreState(ctx context.Context, key, value string) error
	DeleteAppStoreState(ctx context.Context, key string) (bool, error)
	ListAppStoreState(ctx context.Context, prefix string) (map[string]string, error)
	InsertAuditEntry(ctx context.Context, e *model.AuditEntry) error
}

// Holder is what a license decides about an installed app: held (it does
// not run, and says why) or not. wasmplugin.Registry is the one in service.
type Holder interface {
	SetLicenseHold(app, reason string)
}

// Options configure a Service.
type Options struct {
	Store  StateStore
	Client *Client
	// Box seals license keys (the installation's FILEX_SECRET_KEY). Without a
	// key a license cannot be kept, and a paid app is refused at install.
	Box *secretbox.Box
	// Loopback admits a store on this machine over plain http
	// (FILEX_PLUGIN_LOOPBACK_SOURCES): development and tests.
	Loopback bool
	// ConfigStores (FILEX_APP_STORE_URLS) are stores the operator trusts by
	// configuration; ConfigKeys (FILEX_APP_STORE_KEYS) the keys they sign
	// with. A configured store is never put to an administrator: its keys are
	// these, and a key that is not among them is refused.
	ConfigStores []string
	ConfigKeys   []string
	// ConfigStoresSet: FILEX_APP_STORE_URLS was given a value, also one that
	// names no store - the allow list is in force either way.
	ConfigStoresSet bool
	FilexVersion    string
	Holder          Holder
	Log             *slog.Logger
	// Now is the wall clock and Mono the time elapsed since start on a
	// monotonic clock - for tests; zero values are the real clocks.
	Now  func() time.Time
	Mono func() time.Duration
}

// configKey is one FILEX_APP_STORE_KEYS entry: `[index:|license:]<key>`.
type configKey struct {
	use string // "" = either use
	hex string
	fpr string
}

// Service is the store side of filex: trust, install links, licenses.
type Service struct {
	opts Options
	log  *slog.Logger

	cfgStores map[string]bool
	cfgKeys   []configKey
	// allowList: FILEX_APP_STORE_URLS was given, so it is the whole list of
	// stores this filex takes links and licenses from - no administrator
	// trusts another one on first use (refused with CodeStoreRefused). Set
	// even when no entry parsed: a list that names nothing usable admits
	// nothing, it does not fall back to trust on first use.
	allowList bool

	clk *clock

	mu      sync.Mutex
	pending map[string]*Pending

	licMu sync.Mutex // one license write at a time

	appLocks keylock.Map // one store install per app name at a time

	// The embedded store's catalogs (catalog.go): fresh for catalogTTL; the
	// last one that verified, served stale while the store is unreachable;
	// the icons, held to their names' sha256.
	catalogs *memcache.Cache[string, *Catalog]
	lastGood *memcache.Cache[string, *Catalog]
	media    *memcache.Cache[string, []byte]
}

// New builds the service. Configured stores that do not parse are logged and
// left out, never guessed at.
func New(o Options) *Service {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Mono == nil {
		start := time.Now()
		o.Mono = func() time.Duration { return time.Since(start) }
	}
	s := &Service{opts: o, log: o.Log, cfgStores: map[string]bool{}, pending: map[string]*Pending{},
		catalogs: memcache.New[string, *Catalog](memcache.Options{MaxEntries: 64, TTL: catalogTTL, Now: o.Now}),
		lastGood: memcache.New[string, *Catalog](memcache.Options{MaxEntries: 64}),
		media:    memcache.New[string, []byte](memcache.Options{MaxEntries: 512, TTL: mediaTTL, Now: o.Now}),
	}
	// ⚠ Given at all, the list is in force: FILEX_APP_STORE_URLS=" , " names
	// no store and admits none; it does not fall back to trust on first use
	// (store review, second round, Y6).
	s.allowList = o.ConfigStoresSet
	if s.allowList && len(o.ConfigStores) == 0 {
		s.log.Warn("app-store: FILEX_APP_STORE_URLS is set but names no store; no store is trusted")
	}
	for _, raw := range o.ConfigStores {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		s.allowList = true
		origin, err := NormalizeOrigin(raw, o.Loopback)
		if err != nil {
			s.log.Warn("app-store: FILEX_APP_STORE_URLS entry ignored", slog.String("entry", raw), slog.Any("err", err))
			continue
		}
		s.cfgStores[origin] = true
	}
	for _, raw := range o.ConfigKeys {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		ck := configKey{}
		if use, rest, ok := strings.Cut(raw, ":"); ok && (use == UseIndex || use == UseLicense) {
			ck.use, raw = use, rest
		}
		pub, err := plugin.ParsePublicKey(raw)
		if err != nil {
			s.log.Warn("app-store: FILEX_APP_STORE_KEYS entry ignored", slog.Any("err", err))
			continue
		}
		ck.hex = hex.EncodeToString(pub)
		ck.fpr = Key{Ed25519: ck.hex}.Fingerprint()
		s.cfgKeys = append(s.cfgKeys, ck)
	}
	if len(s.cfgKeys) > 0 && len(s.cfgStores) == 0 {
		s.log.Warn("app-store: FILEX_APP_STORE_KEYS is set but FILEX_APP_STORE_URLS names no store; the keys are not used")
	}
	s.clk = newClock(o.Now, o.Mono)
	return s
}

// Loopback reports whether plain-http loopback stores are admitted.
func (s *Service) Loopback() bool { return s.opts.Loopback }

// Configured reports whether origin is a store trusted by configuration.
func (s *Service) Configured(origin string) bool { return s.cfgStores[origin] }

// Now is the latest time filex has proof of, across every store (clock.go):
// for the panel and the log. A license is judged by its own store's proof.
func (s *Service) Now() time.Time { return s.clk.Now() }

// Start loads what the service keeps between runs (every store's proven
// time) and applies every paid app's hold before anything is served. After a
// run that did not stop cleanly, the proven times move on by the restart
// allowance first (clock.go); ApplyHolds keeps that at once, marked not
// clean, so a run killed before its first round still owes it.
func (s *Service) Start(ctx context.Context) {
	var cs clockState
	if ok, err := s.getJSON(ctx, keyClock, &cs); err == nil && ok {
		s.clk.Restore(cs.Stores, cs.Debt, cs.Signed)
		if !cs.Clean {
			s.clk.Allow(restartAllowance)
		}
	}
	s.ApplyHolds(ctx)
}

// InstanceID is this installation's opaque id, made once: what a store
// counts a license's seats by. Random, not derived from anything about the
// server or its people.
func (s *Service) InstanceID(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok, err := s.opts.Store.GetAppStoreState(ctx, keyInstance); err != nil {
		return "", err
	} else if ok && v != "" {
		return v, nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := "fx-" + hex.EncodeToString(b)
	if err := s.opts.Store.PutAppStoreState(ctx, keyInstance, id); err != nil {
		return "", err
	}
	return id, nil
}

// State keys (app_store_state.state_key).
const (
	keyInstance      = "instance_id"
	keyClock         = "clock"
	keyTrustPrefix   = "trust:"
	keyLicensePrefix = "license:"
	keyIntentPrefix  = "intent:"
	keySourcePrefix  = "source:"
)

// audit writes one row; a failure is logged, never fatal.
func (s *Service) audit(ctx context.Context, actor *int64, action, targetType, targetID string, meta map[string]any) {
	if err := s.opts.Store.InsertAuditEntry(ctx, &model.AuditEntry{
		UserID: actor, Action: action, TargetType: targetType, TargetID: targetID, Metadata: meta,
	}); err != nil {
		s.log.Warn("app-store: audit row not written", slog.String("action", action), slog.Any("err", err))
	}
}

// putJSON stores v under key.
func (s *Service) putJSON(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.opts.Store.PutAppStoreState(ctx, key, string(b))
}

// getJSON reads key into v; ok is false when there is no row.
func (s *Service) getJSON(ctx context.Context, key string, v any) (bool, error) {
	raw, ok, err := s.opts.Store.GetAppStoreState(ctx, key)
	if err != nil || !ok {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), v)
}
