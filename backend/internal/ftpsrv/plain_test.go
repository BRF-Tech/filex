package ftpsrv_test

// A server that permits a connection without TLS (#298: "server izin verirse
// tls kapanır"). Off is TestCleartextLoginIsRefused. On, what the device that
// cannot do TLS needs works, and nothing else moved.

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goftp "github.com/jlaffaye/ftp"

	"github.com/brf-tech/filex/backend/internal/ftpsrv"
)

func allowPlain(c *ftpsrv.Config) { c.AllowPlain = true }

func plainDial(t *testing.T, hz *harness) *goftp.ServerConn {
	t.Helper()
	c, err := goftp.Dial(hz.addr, goftp.DialWithTimeout(10*time.Second))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Quit() })
	return c
}

func TestAllowPlain_AClientWithoutTLSSignsInAndTransfers(t *testing.T) {
	hz := newHarnessCfg(t, allowPlain)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "docs/report.txt", []byte("already here"))
	c := plainDial(t, hz)

	if err := c.Login("ftp@example.com", testPassword); err != nil {
		t.Fatalf("plain login on a server that allows it: %v", err)
	}
	r, err := c.Retr("/main/docs/report.txt")
	if err != nil {
		t.Fatalf("retr: %v", err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(got) != "already here" {
		t.Fatalf("retr = %q (%v)", got, err)
	}
	body := bytes.Repeat([]byte("scan. "), 100000)
	if err := c.Stor("/main/docs/scan.pdf", bytes.NewReader(body)); err != nil {
		t.Fatalf("stor: %v", err)
	}
	onDisk, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "docs", "scan.pdf"))
	if err != nil || !bytes.Equal(onDisk, body) {
		t.Fatalf("stored %d bytes (%v), want %d", len(onDisk), err, len(body))
	}
}

// Everything else a sign-in is judged on stands.
func TestAllowPlain_AWrongPasswordIsStillRefused(t *testing.T) {
	hz := newHarnessCfg(t, allowPlain)
	hz.user(t, "ftp@example.com")
	hz.storage(t, "main")

	if err := plainDial(t, hz).Login("ftp@example.com", "wrong"); err == nil {
		t.Fatal("a wrong password was accepted")
	}
	if err := plainDial(t, hz).Login("nobody@example.com", testPassword); err == nil {
		t.Fatal("an unknown account was accepted")
	}
}

func TestAllowPlain_TLSStillWorksBesideIt(t *testing.T) {
	hz := newHarnessCfg(t, allowPlain)
	hz.user(t, "ftp@example.com")
	hz.storage(t, "main")

	if _, err := hz.login(t, "ftp@example.com", testPassword); err != nil {
		t.Fatalf("explicit TLS on a server that allows plain: %v", err)
	}
}

// signInOverTLSThenList speaks the protocol by hand, because every client
// library asks for PROT P by itself: AUTH TLS, a sign-in, and then a listing
// with the data channel left in the clear.
func signInOverTLSThenList(t *testing.T, hz *harness) (int, string) {
	t.Helper()
	raw, err := net.DialTimeout("tcp", hz.addr, 10*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	_ = raw.SetDeadline(time.Now().Add(15 * time.Second))
	plain := textproto.NewConn(raw)
	if _, _, err := plain.ReadResponse(220); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	if err := plain.PrintfLine("AUTH TLS"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := plain.ReadResponse(234); err != nil {
		t.Fatalf("AUTH TLS: %v", err)
	}
	secure := tls.Client(raw, &tls.Config{InsecureSkipVerify: true})
	if err := secure.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	rw := bufio.NewReadWriter(bufio.NewReader(secure), bufio.NewWriter(secure))
	say := func(line string) (int, string) {
		t.Helper()
		if _, err := rw.WriteString(line + "\r\n"); err != nil {
			t.Fatal(err)
		}
		if err := rw.Flush(); err != nil {
			t.Fatal(err)
		}
		code, msg, err := textproto.NewReader(rw.Reader).ReadResponse(0)
		if err != nil && code == 0 {
			t.Fatalf("%s: %v", strings.Fields(line)[0], err)
		}
		return code, msg
	}
	if code, msg := say("USER ftp@example.com"); code != 331 {
		t.Fatalf("USER: %d %s", code, msg)
	}
	if code, msg := say("PASS " + testPassword); code != 230 {
		t.Fatalf("PASS: %d %s", code, msg)
	}
	if code, msg := say("PASV"); code != 227 {
		t.Fatalf("PASV: %d %s", code, msg)
	}
	return say("LIST /")
}

// ⚠ AllowPlain is for the device that cannot do TLS. A client that signed in
// over TLS and then sends the file in the clear is the FTPS misconfiguration
// people actually ship, and the switch must not open it.
func TestAllowPlain_AClientThatSignedInOverTLSStaysOnIt(t *testing.T) {
	hz := newHarnessCfg(t, allowPlain)
	hz.user(t, "ftp@example.com")
	hz.storage(t, "main")

	code, msg := signInOverTLSThenList(t, hz)

	if code != 421 || !strings.Contains(msg, "TLS is required") {
		t.Fatalf("LIST with a clear data channel after a TLS sign-in: %d %s", code, msg)
	}
}

func TestDefault_AClientThatSignedInOverTLSStaysOnIt(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	hz.storage(t, "main")

	code, msg := signInOverTLSThenList(t, hz)

	if code != 421 || !strings.Contains(msg, "TLS is required") {
		t.Fatalf("LIST with a clear data channel: %d %s", code, msg)
	}
}
