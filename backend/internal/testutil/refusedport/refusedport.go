// Package refusedport gives a test a loopback address whose connections are
// refused for as long as the test runs.
package refusedport

import (
	"net"
	"testing"
)

// Addr is "127.0.0.1:<port>" where nothing listens, and where nothing can
// start listening until t ends: the port is the local end of a loopback
// connection held open for the test. No socket listens on it, so a connect
// is answered with a reset (ECONNREFUSED; "actively refused" on Windows),
// and a port in use is never handed to a listener asking for a free one
// (":0"), nor bound by one naming it.
//
// ⚠ Not the port of a listener the test closed: closed, it is free again,
// and `go test ./...` runs packages side by side - a WebDAV "connection
// refused" upload was answered "SSH-2.0-filex" by another package's SSH
// server, which had been given the same port (2026-10-16).
func Addr(t testing.TB) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		_ = l.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		_ = l.Close()
	})
	return c.LocalAddr().String()
}
