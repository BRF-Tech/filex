package ftpsrv_test

import (
	"crypto/tls"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	goftp "github.com/jlaffaye/ftp"

	"github.com/brf-tech/filex/backend/internal/ftpsrv"
)

// serialSeenBy dials with explicit TLS and reports the serial number of the
// certificate the server presented in THAT handshake. InsecureSkipVerify is on
// (the certificates are self-signed), but VerifyConnection still runs, which is
// where the presented leaf is captured.
func serialSeenBy(t *testing.T, addr string) *big.Int {
	t.Helper()
	var serial *big.Int
	c, err := goftp.Dial(addr,
		goftp.DialWithTimeout(10*time.Second),
		goftp.DialWithExplicitTLS(&tls.Config{
			InsecureSkipVerify: true,
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) > 0 {
					serial = cs.PeerCertificates[0].SerialNumber
				}
				return nil
			},
		}),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = c.Quit()
	if serial == nil {
		t.Fatal("no peer certificate was presented")
	}
	return serial
}

// touchLater pushes a file's mtime clearly past whatever it was, so a change
// is visible even on filesystems with coarse timestamps (FAT, some 9p mounts).
func touchLater(t *testing.T, path string, delta time.Duration) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	when := fi.ModTime().Add(delta)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// ⚠ The reason this test exists: the certificate used to be read ONCE, at
// start-up. Mount an ACME-renewed certificate (Caddy renews every ~60 days)
// and the server would keep presenting the expired one until somebody
// restarted it — FileZilla says "certificate expired", the log says nothing,
// /healthz says 200. A real certificate could not be bound safely.
func TestCertificateIsReloadedWhenTheFileChanges(t *testing.T) {
	hz := newHarness(t)
	before := serialSeenBy(t, hz.addr)

	dir := hz.certDir
	certPath := filepath.Join(dir, "ftps.crt")
	keyPath := filepath.Join(dir, "ftps.key")
	if err := ftpsrv.GenerateSelfSigned(certPath, keyPath, "127.0.0.1"); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	touchLater(t, certPath, 10*time.Second)
	touchLater(t, keyPath, 10*time.Second)

	after := serialSeenBy(t, hz.addr)
	if before.Cmp(after) == 0 {
		t.Fatalf("the server still presents the start-up certificate (serial %s) after the files on disk were replaced", before)
	}
}

// A renewal that lands half-written (or a key that does not match its
// certificate) must not take the listener down with it: the previous
// certificate keeps being served until a loadable pair shows up.
func TestABrokenCertificateFileKeepsThePreviousOneServing(t *testing.T) {
	hz := newHarness(t)
	before := serialSeenBy(t, hz.addr)

	dir := hz.certDir
	certPath := filepath.Join(dir, "ftps.crt")
	if err := os.WriteFile(certPath, []byte("-----BEGIN CERTIFICATE-----\nnot a certificate\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	touchLater(t, certPath, 10*time.Second)

	after := serialSeenBy(t, hz.addr)
	if before.Cmp(after) != 0 {
		t.Fatalf("serial changed to %s although the new file is unloadable", after)
	}

	// And once a good pair lands, it is picked up — the bad attempt did not
	// wedge the reloader.
	if err := ftpsrv.GenerateSelfSigned(certPath, filepath.Join(dir, "ftps.key"), "127.0.0.1"); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	touchLater(t, certPath, 20*time.Second)
	touchLater(t, filepath.Join(dir, "ftps.key"), 20*time.Second)
	fixed := serialSeenBy(t, hz.addr)
	if fixed.Cmp(before) == 0 {
		t.Fatal("a good certificate written after a bad one was never loaded")
	}
}
