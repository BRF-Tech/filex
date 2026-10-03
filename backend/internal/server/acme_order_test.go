package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme"
)

// fakeACME is the part of an ACME authority an order's finalization walks.
// It answers finalize with the order still "processing"; with no Location
// header when finalizeLocation is "" (Pebble; RFC 8555 asks for one only when
// the order is created), else with that path as Location (Let's Encrypt's
// Boulder, wfe2 FinalizeOrder). polls counts the order polls per path.
// Signatures are not checked: the client under test is the real one.
func fakeACME(t *testing.T, finalizeLocation string) (*httptest.Server, map[string]*atomic.Int32) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "files.example"}, DNSNames: []string{"files.example"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	chain := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	polls := map[string]*atomic.Int32{"/order/1": {}, "/order/1-by-finalize": {}}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", time.Now().UnixNano()))
		order := func(status string) map[string]any {
			o := map[string]any{
				"status": status, "identifiers": []map[string]string{{"type": "dns", "value": "files.example"}},
				"authorizations": []string{}, "finalize": srv.URL + "/finalize/1",
			}
			if status == "valid" {
				o["certificate"] = srv.URL + "/cert/1"
			}
			return o
		}
		reply := func(code int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(v)
		}
		switch r.URL.Path {
		case "/dir":
			reply(http.StatusOK, map[string]string{
				"newNonce": srv.URL + "/nonce", "newAccount": srv.URL + "/account", "newOrder": srv.URL + "/order",
				"revokeCert": srv.URL + "/revoke", "keyChange": srv.URL + "/key",
			})
		case "/nonce":
			w.WriteHeader(http.StatusOK)
		case "/account":
			w.Header().Set("Location", srv.URL+"/account/1")
			reply(http.StatusCreated, map[string]any{"status": "valid"})
		case "/order":
			w.Header().Set("Location", srv.URL+"/order/1")
			reply(http.StatusCreated, order("ready"))
		case "/finalize/1":
			if finalizeLocation != "" {
				w.Header().Set("Location", srv.URL+finalizeLocation)
			}
			reply(http.StatusOK, order("processing"))
		case "/order/1", "/order/1-by-finalize":
			polls[r.URL.Path].Add(1)
			reply(http.StatusOK, order("valid"))
		case "/cert/1":
			w.Header().Set("Content-Type", "application/pem-certificate-chain")
			_, _ = w.Write(chain)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, polls
}

func finalizeThrough(t *testing.T, transport http.RoundTripper, finalizeLocation string) ([][]byte, map[string]*atomic.Int32, error) {
	t.Helper()
	srv, polls := fakeACME(t, finalizeLocation)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	c := &acme.Client{Key: key, DirectoryURL: srv.URL + "/dir", HTTPClient: &http.Client{Transport: transport}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = c.Register(ctx, &acme.Account{}, acme.AcceptTOS)
	require.NoError(t, err)
	o, err := c.AuthorizeOrder(ctx, acme.DomainIDs("files.example"))
	require.NoError(t, err)
	csrKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{"files.example"}}, csrKey)
	require.NoError(t, err)
	der, _, err := c.CreateOrderCert(ctx, o.FinalizeURL, csr, true)
	return der, polls, err
}

// TestACMEOrderLocation_FinalizeWithoutLocation: an authority that answers the
// finalize request with the order still processing and no Location header
// (Pebble, measured in e2e/realenv) gets its order polled at the address it
// gave when the order was created, and the certificate is fetched. Without the
// repair the real x/crypto client polls the empty address.
func TestACMEOrderLocation_FinalizeWithoutLocation(t *testing.T) {
	_, _, err := finalizeThrough(t, http.DefaultTransport, "")
	require.Error(t, err, "the bare client cannot finish such an order")
	assert.Contains(t, err.Error(), "unsupported protocol scheme")

	der, polls, err := finalizeThrough(t, newACMEOrderLocation(http.DefaultTransport), "")
	require.NoError(t, err)
	require.Len(t, der, 1)
	cert, err := x509.ParseCertificate(der[0])
	require.NoError(t, err)
	assert.Equal(t, "files.example", cert.Subject.CommonName)
	assert.Positive(t, polls["/order/1"].Load(), "polled at the address the order was created at")
}

// TestACMEOrderLocation_BoulderPathUntouched: Let's Encrypt's Boulder puts a
// Location on its finalize answer; the client polls exactly that address,
// with or without the repair in between.
func TestACMEOrderLocation_BoulderPathUntouched(t *testing.T) {
	for _, tr := range []http.RoundTripper{http.DefaultTransport, newACMEOrderLocation(http.DefaultTransport)} {
		der, polls, err := finalizeThrough(t, tr, "/order/1-by-finalize")
		require.NoError(t, err)
		require.Len(t, der, 1)
		assert.Positive(t, polls["/order/1-by-finalize"].Load())
		assert.Zero(t, polls["/order/1"].Load(), "the authority's own Location is the one polled")
	}
}

// TestACMEOrderLocation_LeavesALocationAlone: a finalize answer that names the
// order itself keeps its own Location, and nothing but a finalize answer is
// touched.
func TestACMEOrderLocation_LeavesALocationAlone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/order":
			w.Header().Set("Location", "https://ca.example/order/9")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"status":"pending","finalize":"%s/finalize/9"}`, "http://"+r.Host)
		case "/finalize/9":
			w.Header().Set("Location", "https://ca.example/its-own")
			_, _ = w.Write([]byte(`{"status":"processing"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	c := &http.Client{Transport: newACMEOrderLocation(nil)}
	res, err := c.Post(srv.URL+"/order", "application/jose+json", strings.NewReader("{}"))
	require.NoError(t, err)
	_ = res.Body.Close()
	res, err = c.Post(srv.URL+"/finalize/9", "application/jose+json", strings.NewReader("{}"))
	require.NoError(t, err)
	_ = res.Body.Close()
	assert.Equal(t, "https://ca.example/its-own", res.Header.Get("Location"))
	res, err = c.Post(srv.URL+"/other", "application/jose+json", strings.NewReader("{}"))
	require.NoError(t, err)
	_ = res.Body.Close()
	assert.Empty(t, res.Header.Get("Location"))
}
