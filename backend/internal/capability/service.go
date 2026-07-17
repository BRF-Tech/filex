// Package capability probes runtime feature availability — both binary
// tools (ffmpeg, gs, libreoffice, vips) and external HTTP services
// (OnlyOffice, Drawio).
//
// Results are cached for 1h so the /api/capabilities endpoint is cheap.
package capability

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/antivirus"
	"github.com/brf-tech/filex/backend/internal/db"
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

	// Static fields filled by the bootstrap that don't need probing.
	authDrivers      []string
	storageDrivers   []string
	dbDriver         string
	searchEnabled    bool
	version          string
	build            string
	demoMode         bool
	demoUser         string
	defaultLocale    string
	oidcAutoRedirect bool
}

// New constructs a Service.
func New(store db.Store) *Service { return &Service{store: store} }

// SetStaticInventory wires the boot-time-known fields into the
// Capabilities response. Safe to call once before the first Get().
func (s *Service) SetStaticInventory(
	authDrivers, storageDrivers []string,
	dbDriver string,
	searchEnabled bool,
	version, build string,
	demoMode bool, demoUser string,
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
	s.defaultLocale = defaultLocale
	s.oidcAutoRedirect = oidcAutoRedirect
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

// Get returns the current Capabilities snapshot, refreshing if cache
// expired (1h).
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
	case probeHTTP(es.URL):
		st.State = "ok"
	default:
		st.State = "unreachable"
	}
	_ = s.store.UpdateExternalServiceState(ctx, name, now, st.State)
	s.Invalidate()
	return st, nil
}

func (s *Service) refresh(ctx context.Context) (*model.Capabilities, error) {
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
	caps.DefaultLocale = s.defaultLocale
	caps.OIDCAutoRedirect = s.oidcAutoRedirect
	s.mu.RUnlock()
	if has("magick") || has("convert") {
		caps.Thumbs.ImageMagick = true
	}
	if has("ffmpeg") {
		caps.Thumbs.Video = true
		caps.Thumbs.Audio = true
	}
	if has("gs") || has("pdftoppm") {
		caps.Thumbs.PDF = true
	}
	if has("libreoffice") || has("soffice") {
		caps.Thumbs.Office = true
	}
	if has("rsvg-convert") {
		caps.Thumbs.SVG = true
	}
	// Optional OCR for content search — resolution shared with the
	// extractor (FILEX_TESSERACT_BIN authoritative, else $PATH) so the
	// advertised flag and the actual pipeline can never disagree.
	caps.OCR = extract.TesseractBin() != ""
	// Optional ClamAV upload scanning (v0.4 "Koru") — same shared-resolution
	// pattern via internal/antivirus (FILEX_CLAMAV kill-switch,
	// FILEX_CLAMAV_BIN authoritative, else $PATH clamdscan/clamscan).
	caps.Antivirus = antivirus.ResolveBin() != ""

	// External services from DB.
	if list, err := s.store.ListExternalServices(ctx); err == nil {
		for _, es := range list {
			st := model.ExternalServiceState{
				Enabled:   es.Enabled,
				URL:       es.URL,
				State:     es.LastState,
				LastCheck: es.LastCheck,
			}
			if es.Enabled && es.URL != "" {
				if probeHTTP(es.URL) {
					st.State = "ok"
					_ = s.store.UpdateExternalServiceState(ctx, es.Name, time.Now(), "ok")
				} else {
					st.State = "unreachable"
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

	s.mu.Lock()
	s.cached = caps
	s.until = time.Now().Add(time.Hour)
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

// probeHTTP returns true if the URL responds with 2xx within 3 seconds.
func probeHTTP(rawURL string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode/100 == 2
}

// MarshalJSONForResponse serializes Capabilities for the public API.
func MarshalJSONForResponse(c *model.Capabilities) ([]byte, error) {
	return json.Marshal(c)
}
