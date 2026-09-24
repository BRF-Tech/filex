package proxyheader

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// Probe tests a header-trust configuration with the one request it has: the
// administrator's own, which reached this server either straight or through
// the proxy the configuration is about.
//
//	required         trusted_proxies is filled in
//	trusted_proxies  every entry is an address or a CIDR range
//	via_proxy        this request came from one of those ranges — or, when
//	                 it came straight to filex, that is said (NOT a failure:
//	                 the test simply cannot see the proxy from here)
//	header           a request that came through the proxy carries the
//	                 user header the configuration names
//
// ⚠ Nothing is provisioned and nobody is signed in: Authenticate would
// create the account the header names, so the test reads the request and
// never calls it.
func (d *Driver) Probe(_ context.Context, cfg map[string]any, r *http.Request) []auth.ProbeCheck {
	if len(stringSlice(cfg, "trusted_proxies")) == 0 {
		return []auth.ProbeCheck{auth.Check("required", auth.ProbeFail, "fields", "trusted_proxies")}
	}
	out := []auth.ProbeCheck{auth.Check("required", auth.ProbeOK)}
	p := &Driver{}
	if err := p.load(cfg); err != nil {
		return append(out, auth.Check("trusted_proxies", auth.ProbeFail, "detail", err.Error()))
	}
	out = append(out, auth.Check("trusted_proxies", auth.ProbeOK))
	if r == nil {
		return out
	}
	peer := r.RemoteAddr
	if h, _, err := net.SplitHostPort(peer); err == nil {
		peer = h
	}
	if !sourceTrusted(r, p.trustedProxies) {
		return append(out, auth.Check("via_proxy", auth.ProbeUnchecked, "peer", peer))
	}
	out = append(out, auth.Check("via_proxy", auth.ProbeOK, "peer", peer))
	if user := strings.TrimSpace(r.Header.Get(p.headerUser)); user != "" {
		return append(out, auth.Check("header", auth.ProbeOK, "header", p.headerUser, "user", user))
	}
	return append(out, auth.Check("header", auth.ProbeFail, "header", p.headerUser))
}
