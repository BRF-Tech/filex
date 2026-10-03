package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
)

// acmeProblems is the ACME client's transport (FILEX_TLS_MODE=acme) with one
// addition: when the authority answers that it could not validate an address
// (an authorization whose status is "invalid"), filex logs what the authority
// said, once per authorization.
//
// ⚠ Why this exists. autocert tries each challenge type it can answer in a
// new order and, when the last one fails, returns only
// `acme/autocert: unable to satisfy "<authz>" for domain "<name>": no viable
// challenge type found` (x/crypto/acme/autocert, verifyRFC): the authority's
// own reason - "DNS problem: NXDOMAIN looking up A for <name>", "Connection
// refused", "Incorrect validation certificate for tls-alpn-01 challenge" - is
// dropped, and the TLS handshake error filex's log shows sends an operator to
// the challenge configuration instead of to the DNS. The authorization object
// the client polls carries the reason, so it is read here, on its way past,
// and the body is handed on untouched. Upstream: golang/go#60554
// ("x/crypto/acme/autocert: better error messages for failed validations",
// open; x/crypto v0.57.0 still drops it).
type acmeProblems struct {
	base http.RoundTripper
	// onProblem, when set, is told each challenge's reason too (the screen's
	// tenantdomain.ACMEStatus).
	onProblem func(domain, reason string)

	mu   sync.Mutex
	seen map[string]bool
}

func newACMEProblems(base http.RoundTripper, onProblem func(domain, reason string)) *acmeProblems {
	if base == nil {
		base = http.DefaultTransport
	}
	return &acmeProblems{base: base, onProblem: onProblem}
}

// acmeAuthz is the part of an ACME authorization object (RFC 8555, 7.1.4)
// that says why it failed.
type acmeAuthz struct {
	Status     string `json:"status"`
	Identifier *struct {
		Value string `json:"value"`
	} `json:"identifier"`
	Challenges []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
		Error  *struct {
			Type   string `json:"type"`
			Detail string `json:"detail"`
		} `json:"error"`
	} `json:"challenges"`
}

// acmeObjectMax bounds what is read to look at: an authorization is a few
// hundred bytes, a certificate chain is not JSON and is never read here.
const acmeObjectMax = 256 << 10

func (t *acmeProblems) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(req)
	if err != nil || res == nil || res.StatusCode != http.StatusOK ||
		!strings.Contains(res.Header.Get("Content-Type"), "json") || res.ContentLength > acmeObjectMax {
		return res, err
	}
	body, rerr := io.ReadAll(io.LimitReader(res.Body, acmeObjectMax+1))
	rest := res.Body
	var tail io.Reader = rest
	if rerr != nil {
		tail = errorReader{rerr}
	}
	res.Body = readCloser{Reader: io.MultiReader(bytes.NewReader(body), tail), Closer: rest}
	if rerr == nil && len(body) <= acmeObjectMax {
		t.inspect(req.URL.String(), body)
	}
	return res, nil
}

// inspect logs the challenges of an invalid authorization.
func (t *acmeProblems) inspect(url string, body []byte) {
	var z acmeAuthz
	if json.Unmarshal(body, &z) != nil || z.Status != "invalid" || z.Identifier == nil {
		return
	}
	t.mu.Lock()
	if t.seen == nil || len(t.seen) > 1024 {
		t.seen = map[string]bool{}
	}
	dup := t.seen[url]
	t.seen[url] = true
	t.mu.Unlock()
	if dup {
		return
	}
	logged := false
	for _, c := range z.Challenges {
		if c.Error == nil {
			continue
		}
		logged = true
		slog.Warn("tls: the ACME authority could not validate an address",
			slog.String("domain", z.Identifier.Value),
			slog.String("challenge", c.Type),
			slog.String("problem", c.Error.Type),
			slog.String("detail", c.Error.Detail))
		if t.onProblem != nil {
			t.onProblem(z.Identifier.Value, c.Error.Detail)
		}
	}
	if !logged {
		slog.Warn("tls: the ACME authority could not validate an address",
			slog.String("domain", z.Identifier.Value), slog.String("detail", "the authority gave no reason"))
	}
}

type readCloser struct {
	io.Reader
	io.Closer
}

// errorReader hands back, after the bytes already read, the error reading
// them stopped on.
type errorReader struct{ err error }

func (e errorReader) Read([]byte) (int, error) { return 0, e.err }
