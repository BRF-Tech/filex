package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// acmeOrderLocation is the ACME client's transport (FILEX_TLS_MODE=acme) with
// one repair: a finalize answer that carries no Location header gets the
// order's address.
//
// ⚠ Why this exists. An authority may answer the finalize request with the
// order still "processing" (RFC 8555, 7.4: issuance can be asynchronous), and
// RFC 8555 asks for a Location header only on the order's creation. The ACME
// client autocert uses (x/crypto/acme, CreateOrderCert) polls the order at the
// finalize answer's Location, so when there is none it asks the empty
// address: `Post "": unsupported protocol scheme ""`, and no certificate is
// ever issued. Measured against Pebble, Let's Encrypt's own test authority
// (e2e/realenv, 2026-10-02): Pebble issued the certificate and filex never
// fetched it; every handshake after that failed with "acme/autocert: missing
// certificate". The order's address is known - the creation answer's
// Location, beside the finalize address in its body - so it is remembered
// here and put on a finalize answer that has none.
//
// Which authorities: Let's Encrypt's production and staging servers (Boulder,
// wfe2 FinalizeOrder) DO set the Location, since they restored it after their
// 2023 asynchronous-finalization brownout broke clients that relied on it
// (community.letsencrypt.org/t/195882), so filex worked there. Pebble does
// not, nor does EJBCA (WildFly Elytron ELY-1975 hit the same): RFC 8555 does
// not require it. x/crypto v0.57.0 (2026-09-08, the newest) still polls the
// finalize answer's Location; no upstream issue was found for it
// (2026-10-02). A finalize answer that carries a Location keeps it untouched.
type acmeOrderLocation struct {
	base http.RoundTripper

	mu sync.Mutex
	// orders maps a finalize address to its order's address.
	orders map[string]string
}

func newACMEOrderLocation(base http.RoundTripper) *acmeOrderLocation {
	if base == nil {
		base = http.DefaultTransport
	}
	return &acmeOrderLocation{base: base}
}

func (t *acmeOrderLocation) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(req)
	if err != nil || res == nil || req.Method != http.MethodPost {
		return res, err
	}
	key := req.URL.String()
	if res.StatusCode == http.StatusOK && res.Header.Get("Location") == "" {
		t.mu.Lock()
		order, ok := t.orders[key]
		t.mu.Unlock()
		if ok {
			res.Header.Set("Location", order)
			return res, nil
		}
	}
	// An order: its creation (201 + Location) or a poll of it (200, the
	// request's own address). Remember where its finalize address leads.
	if (res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK) ||
		!strings.Contains(res.Header.Get("Content-Type"), "json") || res.ContentLength > acmeObjectMax {
		return res, nil
	}
	order := res.Header.Get("Location")
	if res.StatusCode == http.StatusOK {
		order = key
	}
	if order == "" {
		return res, nil
	}
	body, rerr := io.ReadAll(io.LimitReader(res.Body, acmeObjectMax+1))
	rest := res.Body
	var tail io.Reader = rest
	if rerr != nil {
		tail = errorReader{rerr}
	}
	res.Body = readCloser{Reader: io.MultiReader(bytes.NewReader(body), tail), Closer: rest}
	if rerr != nil || len(body) > acmeObjectMax {
		return res, nil
	}
	var o struct {
		Finalize string `json:"finalize"`
	}
	if json.Unmarshal(body, &o) == nil && o.Finalize != "" {
		t.mu.Lock()
		if t.orders == nil || len(t.orders) > 1024 {
			t.orders = map[string]string{}
		}
		t.orders[o.Finalize] = order
		t.mu.Unlock()
	}
	return res, nil
}
