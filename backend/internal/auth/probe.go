package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strings"
)

// ── Testing a sign-in provider for real ────────────────────────────────
//
// The admin panel's "Test now" on an identity provider used to answer OK
// whatever the configuration said — an LDAP entry with no address at all got
// "Provider OK" (`"V0.1 stub — drivers self-test on Init"`, release-candidate
// sweep 2026-09-21). A test that always passes is worse than none: it is the
// one thing an operator trusts before switching sign-in over. A driver that
// can check a configuration implements Prober and says, step by step, what
// it reached and what it could not; a driver that cannot is not offered a
// test button at all.

// Probe statuses.
const (
	// ProbeOK: this step was checked and holds.
	ProbeOK = "ok"
	// ProbeFail: this step was checked and does not hold.
	ProbeFail = "fail"
	// ProbeUnchecked: this step cannot be checked without a person signing
	// in (an OIDC redirect address, a public client). Said, never assumed.
	ProbeUnchecked = "unchecked"
)

// ProbeCheck is one step of a provider test. ID names the step (the panel
// words it in the reader's language); Params carries what the sentence
// needs — a host, a field list, a reason, the technical detail.
type ProbeCheck struct {
	ID     string            `json:"id"`
	Status string            `json:"status"`
	Params map[string]string `json:"params,omitempty"`
}

// Prober is a driver that can test a configuration without anyone signing
// in. cfg is the configuration to test (the form's values over the stored
// ones), not the running driver's; r is the administrator's own request,
// which the proxy-header driver examines.
type Prober interface {
	Probe(ctx context.Context, cfg map[string]any, r *http.Request) []ProbeCheck
}

// Check builds a ProbeCheck from key/value pairs.
func Check(id, status string, kv ...string) ProbeCheck {
	c := ProbeCheck{ID: id, Status: status}
	if len(kv) > 1 {
		c.Params = map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			c.Params[kv[i]] = kv[i+1]
		}
	}
	return c
}

// ProbeOKAll reports whether a probe passed: nothing failed and something
// was actually checked.
func ProbeOKAll(checks []ProbeCheck) bool {
	ok := false
	for _, c := range checks {
		switch c.Status {
		case ProbeFail:
			return false
		case ProbeOK:
			ok = true
		}
	}
	return ok
}

// NetReason classifies a connection error into the reasons the panel has
// words for: refused, timeout, dns, tls, or other.
func NetReason(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var certErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	if errors.As(err, &certErr) || errors.As(err, &unknownAuth) || errors.As(err, &hostErr) {
		return "tls"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "connection refused") || strings.Contains(s, "actively refused"):
		return "refused"
	case strings.Contains(s, "no such host"):
		return "dns"
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return "timeout"
	case strings.Contains(s, "certificate") || strings.Contains(s, "x509") || strings.Contains(s, "tls"):
		return "tls"
	}
	return "other"
}

// CfgString reads a configuration value as a trimmed string.
func CfgString(cfg map[string]any, key string) string {
	switch v := cfg[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case nil:
		return ""
	case []string:
		return strings.Join(v, ",")
	case []any:
		parts := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ",")
	}
	return ""
}

// CfgBool reads a configuration value as a bool. The settings table stores
// every value as a string, so "true"/"1"/"yes"/"on" count as well as a JSON
// true.
func CfgBool(cfg map[string]any, key string) bool {
	switch v := cfg[key].(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "on":
			return true
		}
	}
	return false
}
