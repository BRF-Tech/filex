package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/shortlink"
)

// noTenantHosts is a store with no tenant addresses at all.
type noTenantHosts struct{ db.Store }

func (noTenantHosts) GetProviderByHost(context.Context, string) (*model.Provider, error) {
	return nil, nil
}

// A short-link host of its own is certified, asked by the proxy itself; a
// switched-off one is not, and a forwarded stranger learns nothing.
func TestTLSAsk_AShortLinkHostIsCertified(t *testing.T) {
	var snap shortlink.Snapshot
	snap.Store(shortlink.NewRoutes([]shortlink.Base{
		{ID: 1, Host: "l.example.com", Enabled: true},
		{ID: 2, Host: "off.example.com", Enabled: false},
		{ID: 3, Prefix: "l", Enabled: true},
	}))
	h := &TLSHooks{Store: noTenantHosts{}, PublicURL: "https://fm.example.com", ShortLinks: &snap}
	ask := func(name, xff string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/tls/ask?domain="+name, nil)
		r.RemoteAddr = "127.0.0.1:41000"
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		w := httptest.NewRecorder()
		h.Ask(w, r)
		return w.Code
	}
	if got := ask("l.example.com", ""); got != http.StatusOK {
		t.Fatalf("short-link host: %d", got)
	}
	if got := ask("L.Example.com.", ""); got != http.StatusOK {
		t.Fatalf("spelled otherwise: %d", got)
	}
	if got := ask("off.example.com", ""); got != http.StatusNotFound {
		t.Fatalf("switched-off host: %d", got)
	}
	if got := ask("l.example.com", "203.0.113.7"); got != http.StatusNotFound {
		t.Fatalf("forwarded stranger: %d", got)
	}
	if got := ask("fm.example.com", ""); got != http.StatusOK {
		t.Fatalf("platform host: %d", got)
	}
}
