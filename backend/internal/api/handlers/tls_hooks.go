package handlers

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/tenantdomain"
)

// TLSHooks answers the reverse proxy's two questions about certificates
// (FILEX_TLS_MODE=proxy, docs/TENANT-ADMIN.md, "TLS"):
//
//	GET /api/tls/ask?domain=<name>                 200: certify it; 404: do not
//	GET /api/tls/certificate?server_name=<name>    the tenant's own certificate (PEM), or 204
//
// The first is Caddy's on-demand TLS `ask`: a stranger pointing a name at
// the platform gets no certificate, because the platform serves only its own
// address, enabled tenants' addresses, their platform subdomains and their
// ACTIVE own domains. The second is Caddy's `get_certificate http`: a tenant
// that brought a certificate for its domain has it served.
//
// ⚠ Both answer the proxy ITSELF only (clientip.ProxyItself: a trusted peer,
// no forwarded-for header): the same proxy forwards every stranger's request
// to filex, and those must not learn the platform's customers' domains, let
// alone read a private key. Anyone else gets 404, the answer an unknown name
// gets.
type TLSHooks struct {
	Store db.Store
	Box   *secretbox.Box
	// PublicURL is the platform's own address (FILEX_PUBLIC_URL), always
	// certified.
	PublicURL string
}

func (h *TLSHooks) platformHost(name string) bool {
	u, err := url.Parse(h.PublicURL)
	return err == nil && u.Hostname() != "" && strings.EqualFold(u.Hostname(), name)
}

// Ask answers whether the proxy may certify a name.
func (h *TLSHooks) Ask(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(r.URL.Query().Get("domain")), "."))
	if !clientip.ProxyItself(r) || name == "" {
		http.NotFound(w, r)
		return
	}
	if h.platformHost(name) || tenantdomain.Serves(r.Context(), h.Store, name) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	http.NotFound(w, r)
}

// Certificate hands the proxy a tenant's own certificate for a name (the
// chain, then the key, PEM), or 204 when the tenant brought none: the proxy
// then issues its own.
func (h *TLSHooks) Certificate(w http.ResponseWriter, r *http.Request) {
	if !clientip.ProxyItself(r) {
		http.NotFound(w, r)
		return
	}
	bundle, _, ok := tenantdomain.OwnCertificate(r.Context(), h.Store, h.Box, r.URL.Query().Get("server_name"))
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(bundle)
}
