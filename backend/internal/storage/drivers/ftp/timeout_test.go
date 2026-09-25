package ftp

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

// The form shows the descriptor's defaults and bounds; the driver applies the
// policy's. They are the same values — this keeps it that way.
func TestDescriptorShowsThePolicy(t *testing.T) {
	desc, ok := storage.DescriptorFor("ftp")
	if !ok {
		t.Fatal("no ftp descriptor")
	}
	d := &Driver{}
	if err := d.Init(context.Background(), map[string]any{"host": "h", "user": "u", "password": "p"}); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{ def, max int }{
		"attempt_timeout_s": {int(d.policy.AttemptTimeout / time.Second), stall.AttemptTimeoutLimitS},
		"max_attempts":      {d.policy.MaxAttempts, stall.MaxAttemptsLimit},
		"total_timeout_s":   {int(d.policy.TotalTimeout / time.Second), stall.TotalTimeoutLimitS},
	}
	for key, w := range want {
		f, ok := desc.Field(key)
		if !ok {
			t.Errorf("%s is not in the descriptor, so no form offers it", key)
			continue
		}
		if f.Type != storage.FieldInt || !f.Advanced || f.Default != w.def || *f.Min != 1 || *f.Max != w.max {
			t.Errorf("%s: %+v, want int/advanced/%d/1/%d", key, f, w.def, w.max)
		}
	}
	if d.policy.AttemptTimeout != 15*time.Second {
		t.Errorf("default attempt timeout %v, want the 15 s the connect alone had", d.policy.AttemptTimeout)
	}
}

// ⚠ The driver now dials every connection itself (to bound its silence), and
// with a dial function the FTP library hands DATA connections back as they
// come: without the driver's own TLS wrap an FTPS session would send its files
// in the clear — and a server that insists on PROT P would refuse them. This
// runs an explicit-FTPS stand-in that fails any data connection not speaking
// TLS.
func TestFTPS_DataConnectionsSpeakTLS(t *testing.T) {
	cert, pool := selfSigned(t)
	testRootCAs = pool
	t.Cleanup(func() { testRootCAs = nil })
	s := newFakeFTP(t)
	s.tlsConf = &tls.Config{Certificates: []tls.Certificate{cert}}
	d := ftpDriver(t, s.addr(), map[string]any{"tls": true})

	body := bytes.Repeat([]byte("secret "), 10000)
	if err := d.Write(context.Background(), "/x.bin", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("FTPS upload: %v", err)
	}
	objs, err := d.List(context.Background(), "/")
	if err != nil || len(objs) != 2 {
		t.Fatalf("FTPS listing: %v %v", objs, err)
	}
	rc, err := d.Read(context.Background(), "/x.bin")
	if err != nil {
		t.Fatalf("FTPS download: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("FTPS download: %d bytes, err=%v", len(got), err)
	}
	if n := s.plainData.Load(); n != 0 {
		t.Fatalf("%d data connections did not speak TLS", n)
	}
}

// selfSigned makes a certificate for 127.0.0.1 and a pool that trusts it.
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(73),
		Subject:               pkix.Name{CommonName: "filex ftps test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}
