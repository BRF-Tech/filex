package ftpsrv

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// certSource hands every TLS handshake the certificate that is on disk NOW.
//
// ⚠ It replaced a one-time tls.LoadX509KeyPair at start-up, and the reason is
// renewal: an ACME certificate (Caddy, certbot) is rewritten every ~60 days,
// and a server that read it once keeps presenting the expired copy until it is
// restarted. That failure is silent on the server side — FileZilla says
// "certificate expired", the log says nothing, /healthz says 200 — which is
// why a real certificate could not be bound to FTPS before this existed.
//
// Each handshake stats the two files (cheap; a handshake is already a
// round-trip of asymmetric crypto) and reloads when either's mtime or size
// moved. A pair that fails to load — half-written by the renewer, key and
// certificate from two different runs — keeps the PREVIOUS pair serving and
// logs once per broken version, so a bad renewal degrades to "still the old
// certificate" rather than "no listener".
type certSource struct {
	certPath string
	keyPath  string

	mu   sync.Mutex
	cert *tls.Certificate
	// seen is the stamp of the files the current cert was loaded from.
	seen fileStamp
	// failed is the stamp of a version that would not load, so the same bad
	// pair is not retried and re-logged on every handshake.
	failed fileStamp
	// missing is set while the files cannot be stat'ed, so the warning is
	// written once per disappearance rather than once per handshake.
	missing bool
}

// fileStamp identifies one version of the certificate pair on disk.
type fileStamp struct {
	certMod, keyMod   time.Time
	certSize, keySize int64
}

func (a fileStamp) equal(b fileStamp) bool {
	return a.certMod.Equal(b.certMod) && a.keyMod.Equal(b.keyMod) && a.certSize == b.certSize && a.keySize == b.keySize
}

func stampOf(certPath, keyPath string) (fileStamp, error) {
	ci, err := os.Stat(certPath)
	if err != nil {
		return fileStamp{}, err
	}
	ki, err := os.Stat(keyPath)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{certMod: ci.ModTime(), keyMod: ki.ModTime(), certSize: ci.Size(), keySize: ki.Size()}, nil
}

// newCertSource loads the pair once so a server with an unloadable certificate
// refuses to START (loud) instead of refusing every handshake (quiet).
func newCertSource(certPath, keyPath string) (*certSource, error) {
	cs := &certSource{certPath: certPath, keyPath: keyPath}
	st, err := stampOf(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	cs.cert, cs.seen = &cert, st
	return cs, nil
}

// tlsConfig returns a config whose certificate is resolved per handshake.
func (cs *certSource) tlsConfig() *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: cs.getCertificate,
	}
}

// getCertificate is the tls.Config callback: the pair on disk now, or the last
// good one when what is on disk cannot be loaded.
func (cs *certSource) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	st, err := stampOf(cs.certPath, cs.keyPath)
	if err != nil {
		// Vanished mid-renewal (rename in flight) or unmounted: keep serving
		// what we have. Logged once per disappearance, not per handshake.
		if !cs.missing {
			cs.missing = true
			slog.Warn("ftps: certificate files unreadable, still serving the previous certificate",
				slog.String("cert", cs.certPath), slog.String("err", err.Error()))
		}
		return cs.cert, nil
	}
	cs.missing = false
	if st.equal(cs.seen) || st.equal(cs.failed) {
		return cs.cert, nil
	}
	cert, err := tls.LoadX509KeyPair(cs.certPath, cs.keyPath)
	if err != nil {
		cs.failed = st
		slog.Warn("ftps: new certificate files do not load, still serving the previous certificate",
			slog.String("cert", cs.certPath), slog.String("err", err.Error()))
		return cs.cert, nil
	}
	cs.cert, cs.seen, cs.failed = &cert, st, fileStamp{}
	slog.Info("ftps: certificate reloaded", slog.String("cert", cs.certPath), slog.String("note", describeLeaf(&cert)))
	return cs.cert, nil
}

// describeLeaf renders the leaf's validity window for the reload log line —
// the one fact an operator wants to see when a renewal lands.
func describeLeaf(c *tls.Certificate) string {
	if c == nil || c.Leaf == nil {
		return ""
	}
	return fmt.Sprintf("valid %s → %s", c.Leaf.NotBefore.UTC().Format("2006-01-02"), c.Leaf.NotAfter.UTC().Format("2006-01-02"))
}
