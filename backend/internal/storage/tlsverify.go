package storage

// Whether the drivers that speak TLS check the certificate of the server they
// connect to (issue #134).
//
// A private S3 endpoint, WebDAV or FTPS server often answers with a certificate
// no public authority signed, and every connection to it failed. The way that
// keeps the check is to trust that certificate: see "Private and self-signed
// certificates" in docs/STORAGE.md. This switch is for the server where that is
// not an option, and it is one switch for every such driver because the need
// and the risk are the same on all three.
//
// ⚠⚠ With it on, anybody who can sit between filex and the server can read and
// change every file that crosses, and the storage's credentials on the drivers
// that send them (WebDAV, FTPS). It is off unless an operator turns it on.

import (
	"crypto/tls"
	"sync/atomic"
)

var skipTLSVerify atomic.Bool

// SetSkipTLSVerify says whether drivers built from now on check certificates.
// A driver reads it when it builds its connection, so it is set before the
// first storage opens (server.New).
func SetSkipTLSVerify(skip bool) { skipTLSVerify.Store(skip) }

// ApplyTLSVerify is what a driver passes its TLS configuration through. With
// the check on, base comes back as it went in, nil included, so a driver's
// connection stays exactly what it was. With it off, it comes back as a copy
// that accepts any certificate: base may be shared and is never changed.
func ApplyTLSVerify(base *tls.Config) *tls.Config {
	if !skipTLSVerify.Load() {
		return base
	}
	out := base.Clone()
	if out == nil {
		out = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	out.InsecureSkipVerify = true //nolint:gosec // the operator's explicit choice, see the file comment
	return out
}
