package plugin

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ── P-5: the handshake names the plugin's own socket or a loopback port ─────

func TestParseHandshake(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets and links")
	}
	base, err := os.MkdirTemp("", "fxh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	sockDir := filepath.Join(base, "run")
	other := filepath.Join(base, "other")
	for _, d := range []string{sockDir, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	listen := func(p string) {
		ln, err := net.Listen("unix", p)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
	}
	own := filepath.Join(sockDir, "p1.sock")
	listen(own)
	foreign := filepath.Join(other, "x.sock")
	listen(foreign)
	link := filepath.Join(sockDir, "link.sock")
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(sockDir, "sub")); err != nil {
		t.Fatal(err)
	}

	if a, err := parseHandshake("unix:"+own, sockDir); err != nil || a.Network != "unix" {
		t.Fatalf("its own socket: %+v, %v", a, err)
	}
	if a, err := parseHandshake("tcp:127.0.0.1:4567", sockDir); err != nil || a.URL != "http://127.0.0.1:4567" {
		t.Fatalf("a loopback port: %+v, %v", a, err)
	}
	for _, line := range []string{
		"unix:" + foreign,
		"unix:" + link,
		"unix:" + filepath.Join(sockDir, "sub", "x.sock"),
		"unix:" + filepath.Join(sockDir, "..", "other", "x.sock"),
		"unix:" + sockDir,
		"unix:" + filepath.Join(sockDir, "missing.sock"),
		"tcp:10.0.0.5:4567",
		"tcp:0.0.0.0:4567",
		"http://127.0.0.1:4567",
		"https://plugins.example.com",
	} {
		if a, err := parseHandshake(line, sockDir); err == nil {
			t.Errorf("%s: accepted as %+v", line, a)
		}
	}
}

// ── P-4: plain http to a plugin stays inside the private network ────────────

// The address a plain-http plugin is reached at is resolved at every dial,
// and an answer outside the private network is refused before a connection
// is made: a name that was private at registration and is public now (DNS
// rebinding) does not carry the token in the clear.
func TestPrivateOnlyDialRefusesAPublicAnswer(t *testing.T) {
	dial := privateOnlyDial(&net.Dialer{Timeout: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, addr := range []string{"203.0.113.10:80", "8.8.8.8:53", "[2606:4700:4700::1111]:80"} {
		if _, err := dial(ctx, "tcp", addr); !errors.Is(err, errRemoteNeedsTLS) {
			t.Errorf("%s: err = %v, want errRemoteNeedsTLS", addr, err)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c, err := dial(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("loopback: %v", err)
	}
	_ = c.Close()

	// The client a plain-http remote gets dials that way.
	cl := NewClient(Address{Network: "tcp", Target: "203.0.113.10:9", URL: "http://203.0.113.10:9"}, "tok")
	defer cl.Close()
	if _, err := cl.Describe(ctx); !errors.Is(err, errRemoteNeedsTLS) {
		t.Fatalf("plain http to a public address: err = %v, want errRemoteNeedsTLS", err)
	}
}

// ── P-6: nothing is executable before it is verified ────────────────────────

// A binary arrives as a private, non-executable file (0600). It becomes 0755
// at its final path only once the sha256 and the signature have passed - an
// install that is refused never leaves an executable file where the plugin
// lives, not even for the moment the checks take.
func TestAnArrivingBinaryIsNotExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	p := filepath.Join(t.TempDir(), "bin")
	if _, err := writeBinary(p, strings.NewReader("#!/bin/sh\nexit 0\n"), 1<<20); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("an unverified binary is %v, want 0600", got)
	}
}
