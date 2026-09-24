// Package capability probes runtime feature availability — both binary
// tools (ffmpeg, gs, libreoffice, vips) and external HTTP services
// (OnlyOffice, Drawio).
//
// Results are cached so the /api/capabilities endpoint is cheap: 1h after
// a fully-successful probe round, but only 2 minutes when any external
// probe failed — a transient outage must not pin an "unreachable" banner
// in the UI for a whole hour.
package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/antivirus"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search/extract"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Service answers /api/capabilities and persists external_services state.
type Service struct {
	store db.Store

	mu              sync.RWMutex
	cached          *model.Capabilities
	until           time.Time
	storageResolver func(int64) (storage.Driver, error)

	// Asymmetric cache TTLs (fields so tests can shorten them): a snapshot
	// where every enabled external probe succeeded lives okTTL (1h); one
	// with any failed probe lives only failTTL (2m) so the next Get
	// re-probes soon and a transient outage clears itself quickly.
	okTTL   time.Duration
	failTTL time.Duration

	// Static fields filled by the bootstrap that don't need probing.
	authDrivers      []string
	storageDrivers   []string
	dbDriver         string
	searchEnabled    bool
	version          string
	build            string
	demoMode         bool
	demoUser         string
	demoPass         string
	defaultLocale    string
	oidcAutoRedirect bool
	recoveryLogin    bool
	appPlugins       model.AppPluginsCapabilities
}

// New constructs a Service.
func New(store db.Store) *Service {
	return &Service{
		store:   store,
		okTTL:   time.Hour,
		failTTL: 2 * time.Minute,
	}
}

// SetStaticInventory wires the boot-time-known fields into the
// Capabilities response. Safe to call once before the first Get().
func (s *Service) SetStaticInventory(
	authDrivers, storageDrivers []string,
	dbDriver string,
	searchEnabled bool,
	version, build string,
	demoMode bool, demoUser, demoPass string,
	defaultLocale string,
	oidcAutoRedirect bool,
) {
	s.mu.Lock()
	s.authDrivers = append(s.authDrivers[:0], authDrivers...)
	s.storageDrivers = append(s.storageDrivers[:0], storageDrivers...)
	s.dbDriver = dbDriver
	s.searchEnabled = searchEnabled
	s.version = version
	s.build = build
	s.demoMode = demoMode
	s.demoUser = demoUser
	s.demoPass = demoPass
	s.defaultLocale = defaultLocale
	s.oidcAutoRedirect = oidcAutoRedirect
	s.cached = nil
	s.mu.Unlock()
}

// SetAuthDrivers records the sign-in methods the login page offers. The
// Identity providers page changes them without a restart
// (internal/authsetup), so this is set on every swap, not only at boot.
func (s *Service) SetAuthDrivers(names []string) {
	s.mu.Lock()
	s.authDrivers = append(s.authDrivers[:0], names...)
	s.cached = nil
	s.mu.Unlock()
}

// SetRecoveryLogin records whether recovery sign-in is active (see
// model.Capabilities.AuthRecoveryLogin).
func (s *Service) SetRecoveryLogin(on bool) {
	s.mu.Lock()
	s.recoveryLogin = on
	s.cached = nil
	s.mu.Unlock()
}

// SetAppPlugins records the app-plugin runtime state for the snapshot.
func (s *Service) SetAppPlugins(v model.AppPluginsCapabilities) {
	s.mu.Lock()
	s.appPlugins = v
	s.cached = nil
	s.mu.Unlock()
}

// AttachStorageResolver wires the resolver used for per-storage capability
// probes. Optional — when nil the response omits the per-storage map.
func (s *Service) AttachStorageResolver(resolver func(int64) (storage.Driver, error)) {
	s.mu.Lock()
	s.storageResolver = resolver
	s.cached = nil
	s.mu.Unlock()
}

// Get returns the current Capabilities snapshot, refreshing if the cache
// expired (okTTL after a clean probe round, failTTL when any external
// probe failed).
func (s *Service) Get(ctx context.Context) (*model.Capabilities, error) {
	s.mu.RLock()
	if s.cached != nil && time.Now().Before(s.until) {
		c := *s.cached
		s.mu.RUnlock()
		return &c, nil
	}
	s.mu.RUnlock()
	return s.refresh(ctx)
}

// Invalidate forces the next Get to re-probe.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
}

// ProbeExternal probes a single named external service immediately and
// returns its fresh state. The capability cache is invalidated as a side
// effect so the next /api/capabilities call sees the updated row.
func (s *Service) ProbeExternal(ctx context.Context, name string) (*model.ExternalServiceState, error) {
	es, err := s.store.GetExternalService(ctx, name)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	st := &model.ExternalServiceState{
		Enabled:   es.Enabled,
		URL:       es.URL,
		LastCheck: &now,
	}
	switch {
	case !es.Enabled:
		st.State = "disabled"
	case es.URL == "":
		st.State = "unconfigured"
	case missingSecret(name, es.SecretEnc):
		// ⚠ Reachable is not the same as configured. A Document Server with no
		// JWT secret on filex's side answers /healthcheck perfectly happily and
		// then refuses every editor session, which is how a green Test button
		// sat next to "OnlyOffice is not configured" in issue #17. Say the true
		// thing here rather than probing and calling it healthy.
		st.State = "unconfigured"
	default:
		if ok, detail := probeHTTPDetail(externalProbeURL(name, es.URL)); ok {
			st.State = "ok"
		} else {
			st.State = "unreachable"
			st.Detail = externalProbeHint(name, detail)
		}
	}
	_ = s.store.UpdateExternalServiceState(ctx, name, now, st.State)
	s.Invalidate()
	return st, nil
}

func (s *Service) refresh(ctx context.Context) (*model.Capabilities, error) {
	// ⚠⚠ A refresh is SHARED work: whatever it finds is what every caller
	// reads until the cache expires (okTTL, an hour). It must not run on the
	// context of the one request that happened to trigger it — a browser that
	// navigated away cancelled that request, ListExternalServices failed with
	// "context canceled", and `external` was cached EMPTY for an hour: no
	// ONLYOFFICE, no draw.io, for everybody (found by the v0.43.0 e2e suite,
	// 2026-09-22). Detached, with a bound of its own.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	caps := &model.Capabilities{
		Upload:   true,
		Move:     true,
		Copy:     true,
		Delete:   true,
		Mkdir:    true,
		Search:   true,
		Versions: true,
		Presign:  false,
		Thumbs: model.ThumbCapabilities{
			Image: true,
		},
		External:      map[string]model.ExternalServiceState{},
		MaxUploadSize: 5 * 1024 * 1024 * 1024, // 5 GB default
		ChunkSize:     8 * 1024 * 1024,        // 8 MB default
	}
	// Static inventory wired from server bootstrap.
	s.mu.RLock()
	caps.AuthDrivers = append([]string(nil), s.authDrivers...)
	caps.StorageDrivers = append([]string(nil), s.storageDrivers...)
	caps.DBDriver = s.dbDriver
	caps.SearchEnabled = s.searchEnabled
	caps.Version = s.version
	caps.Build = s.build
	caps.DemoMode = s.demoMode
	caps.DemoUser = s.demoUser
	// The demo password rides along ONLY on a demo instance. On a normal
	// install this field stays empty, so a public capabilities response
	// never carries a password that means anything.
	if s.demoMode {
		caps.DemoPass = s.demoPass
	}
	caps.DefaultLocale = s.defaultLocale
	caps.OIDCAutoRedirect = s.oidcAutoRedirect
	caps.AuthRecoveryLogin = s.recoveryLogin
	caps.AppPlugins = s.appPlugins
	s.mu.RUnlock()
	// ⚠⚠ The engines are enginebin's ONE answer — the same one Apps and the
	// converter read (wasmplugin.probeEngines). This block used to ask
	// `has("magick") || has("convert")` itself, and on Windows `convert` is
	// C:\Windows\System32\convert.exe, the disk converter: the About page
	// said "ImageMagick: OK" while Apps said it was not installed and the
	// converter greyed every ImageMagick format (release-candidate sweep,
	// 2026-09-21). Nothing here may probe a binary of its own again.
	engines := enginebin.Probe()
	caps.Thumbs.ImageMagick = engines.Has(enginebin.ImageMagick)
	if engines.Has(enginebin.FFmpeg) {
		caps.Thumbs.Video = true
		caps.Thumbs.Audio = true
	}
	caps.Thumbs.PDF = engines.Has(enginebin.Ghostscript) || engines.Has(enginebin.Poppler)
	caps.Thumbs.Office = engines.Has(enginebin.LibreOffice)
	caps.Thumbs.SVG = engines.Has(enginebin.RSVG)
	// Optional OCR for content search — resolution shared with the
	// extractor (FILEX_TESSERACT_BIN authoritative, else $PATH) so the
	// advertised flag and the actual pipeline can never disagree.
	caps.OCR = extract.TesseractBin() != ""
	// Optional ClamAV upload scanning (v0.4 "Koru") — same shared-resolution
	// pattern via internal/antivirus. ⚠ One call, antivirus.Resolve, answers
	// both "is it on" and "how is it reached", and it is the SAME call the
	// scan pipeline and the admin page make: the advertised flag cannot drift
	// from what actually scans, because there is nothing else to drift from.
	//
	// ⚠ Configured, not probed. In daemon mode this says an address is set
	// and parses; whether clamd answers is a network round-trip, made by
	// GET /api/admin/protection where an admin is waiting, not on every
	// capabilities fetch.
	avRes := antivirus.Resolve(ctx, s.store)
	caps.Antivirus = avRes.Available()
	if caps.Antivirus {
		caps.AntivirusMode = avRes.Mode
	}

	// External services from DB.
	probeFailed := false
	list, listErr := s.store.ListExternalServices(ctx)
	if listErr != nil {
		// A snapshot that could not read the table says nothing about the
		// services; keep it only as long as a failed probe (failTTL).
		probeFailed = true
		slog.Warn("capability: list external services", slog.String("err", listErr.Error()))
	}
	if listErr == nil {
		for _, es := range list {
			st := model.ExternalServiceState{
				Enabled:   es.Enabled,
				URL:       es.URL,
				State:     es.LastState,
				LastCheck: es.LastCheck,
			}
			if es.Enabled && es.URL != "" && missingSecret(es.Name, es.SecretEnc) {
				// Configured by halves — see ProbeExternal.
				st.State = "unconfigured"
				_ = s.store.UpdateExternalServiceState(ctx, es.Name, time.Now(), "unconfigured")
			} else if es.Enabled && es.URL != "" {
				if probeHTTP(externalProbeURL(es.Name, es.URL)) {
					st.State = "ok"
					_ = s.store.UpdateExternalServiceState(ctx, es.Name, time.Now(), "ok")
				} else {
					st.State = "unreachable"
					probeFailed = true
					_ = s.store.UpdateExternalServiceState(ctx, es.Name, time.Now(), "unreachable")
				}
			} else {
				st.State = "disabled"
			}
			caps.External[es.Name] = st
		}
	}

	// Per-storage capability probe — opt-in via AttachStorageResolver.
	s.mu.RLock()
	resolver := s.storageResolver
	s.mu.RUnlock()
	if resolver != nil {
		caps.Storage = map[string]model.StorageCapabilities{}
		if storages, err := s.store.ListEnabledStorages(ctx); err == nil {
			for _, st := range storages {
				drv, err := resolver(st.ID)
				if err != nil {
					slog.Debug("capability: resolve storage", slog.String("name", st.Name), slog.String("err", err.Error()))
					continue
				}
				caps.Storage[strconv.FormatInt(st.ID, 10)] = probeStorage(drv)
				// If any backend supports presign, mark global presign too.
				if _, ok := drv.(storage.Presigner); ok {
					caps.Presign = true
				}
			}
		}
	}

	// Asymmetric TTL: a snapshot carrying a failed probe expires quickly so
	// a transient outage banners the UI for at most failTTL, not okTTL.
	ttl := s.okTTL
	if probeFailed {
		ttl = s.failTTL
	}
	s.mu.Lock()
	s.cached = caps
	s.until = time.Now().Add(ttl)
	s.mu.Unlock()
	return caps, nil
}

// probeStorage uses ComputeCapabilities (which uses interface assertions)
// plus the additional MultipartUploader / Watcher checks that need the
// driver's actual type.
func probeStorage(drv storage.Driver) model.StorageCapabilities {
	c := storage.ComputeCapabilities(drv)
	out := model.StorageCapabilities{
		Read:    c.Read,
		Range:   c.Range,
		Write:   c.Write,
		Move:    c.Move,
		Copy:    c.Copy,
		Delete:  c.Delete,
		Mkdir:   c.Mkdir,
		Presign: c.Presign,
		Events:  c.Watch,
	}
	if _, ok := drv.(storage.MultipartUploader); ok {
		out.Multipart = true
	}
	return out
}

// has reports whether bin is in $PATH.
func has(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// externalSecretRequired names the services that need more than a URL before
// they can do anything. OnlyOffice signs every editor descriptor and every
// fetch URL with a shared HS256 secret; without it the integration is not
// configured, however reachable the Document Server is.
var externalSecretRequired = map[string]bool{"onlyoffice": true}

func missingSecret(name, secret string) bool {
	return externalSecretRequired[name] && secret == ""
}

// externalHealthPaths maps external service names to their dedicated
// health endpoints. Probing these instead of the app root avoids false
// "unreachable" verdicts from services whose root URL redirects or 4xxes
// while the service itself is healthy. Services without an entry (drawio)
// keep the raw-URL probe.
var externalHealthPaths = map[string]string{
	"onlyoffice": "/healthcheck",
	"convert":    "/healthz",
}

// externalProbeURL returns the URL to probe for the named service — the
// configured base URL joined with the service's health path when one is
// known. Trailing slashes on the base collapse so `http://x/` and
// `http://x` both yield `http://x/healthcheck`.
func externalProbeURL(name, rawURL string) string {
	p, ok := externalHealthPaths[name]
	if !ok {
		return rawURL
	}
	return strings.TrimRight(rawURL, "/") + p
}

// probeTimeout is how long a health probe waits for an answer.
const probeTimeout = 3 * time.Second

// probeHTTP returns true if the URL responds with 2xx within probeTimeout.
func probeHTTP(rawURL string) bool {
	ok, _ := probeHTTPDetail(rawURL)
	return ok
}

// probeHTTPDetail is probeHTTP that also says what it saw when the answer was
// not a healthy one: the status code, a timeout, or the connection error.
func probeHTTPDetail(rawURL string) (bool, string) {
	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Get(rawURL)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return false, fmt.Sprintf("GET %s: no answer within %s", rawURL, probeTimeout)
		}
		return false, fmt.Sprintf("GET %s: %s", rawURL, probeErrorText(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		return true, ""
	}
	return false, fmt.Sprintf("GET %s returned HTTP %d", rawURL, resp.StatusCode)
}

// probeErrorText drops the `Get "<url>": ` prefix net/http puts in front of
// every client error — the URL is already in the sentence around it.
func probeErrorText(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr.Err != nil {
		return uerr.Err.Error()
	}
	return err.Error()
}

// externalProbeHint adds the one next step a detail on its own does not
// suggest. ONLYOFFICE Docs serves /healthcheck through its own nginx from the
// docservice process behind it: a 502/503/504 there means nginx answered and
// docservice did not, so the network is fine and the fix is inside that
// container (issue #17: a supervisor change left docservice stopped while the
// static welcome page still loaded).
func externalProbeHint(name, detail string) string {
	if name == "onlyoffice" && (strings.HasSuffix(detail, "HTTP 502") || strings.HasSuffix(detail, "HTTP 503") || strings.HasSuffix(detail, "HTTP 504")) {
		return detail + " — the document server's web server answered, but its docservice did not: run `supervisorctl status` in that container"
	}
	return detail
}

// MarshalJSONForResponse serializes Capabilities for the public API.
func MarshalJSONForResponse(c *model.Capabilities) ([]byte, error) {
	return json.Marshal(c)
}
