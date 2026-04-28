// Package capability probes runtime feature availability — both binary
// tools (ffmpeg, gs, libreoffice, vips) and external HTTP services
// (OnlyOffice, Drawio).
//
// Results are cached for 1h so the /api/capabilities endpoint is cheap.
package capability

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/model"
)

// Service answers /api/capabilities and persists external_services state.
type Service struct {
	store db.Store

	mu     sync.RWMutex
	cached *model.Capabilities
	until  time.Time
}

// New constructs a Service.
func New(store db.Store) *Service { return &Service{store: store} }

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
		Upload:        true,
		Move:          true,
		Copy:          true,
		Delete:        true,
		Mkdir:         true,
		Search:        true,
		Versions:      true,
		Presign:       false,
		Thumbs: model.ThumbCapabilities{
			Image: true,
		},
		External:      map[string]model.ExternalServiceState{},
		MaxUploadSize: 5 * 1024 * 1024 * 1024,  // 5 GB default
		ChunkSize:     8 * 1024 * 1024,         // 8 MB default
	}
	if has("ffmpeg") {
		caps.Thumbs.Video = true
	}
	if has("gs") || has("pdftoppm") {
		caps.Thumbs.PDF = true
	}
	if has("libreoffice") || has("soffice") {
		caps.Thumbs.Office = true
	}

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

	s.mu.Lock()
	s.cached = caps
	s.until = time.Now().Add(time.Hour)
	s.mu.Unlock()
	return caps, nil
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
