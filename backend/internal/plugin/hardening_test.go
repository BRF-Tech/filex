package plugin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"

	"github.com/brf-tech/filex/backend/internal/netguard"
)

// ── environment ─────────────────────────────────────────────────────────────

// The child gets what it needs to run and the FILEX_PLUGIN_* variables filex
// set on purpose. Nothing else — least of all the secrets filex itself was
// handed through the environment.
func TestBuildEnvPassesOnlyTheAllowList(t *testing.T) {
	host := []string{
		"PATH=/usr/bin",
		"Path=C:\\Windows", // Windows spelling: the allow-list is case-insensitive
		"HOME=/root",
		"LC_ALL=C.UTF-8",
		"LANG=en_US.UTF-8",
		"TZ=Europe/Istanbul",
		"SystemRoot=C:\\Windows",
		"FILEX_SECRET_KEY=hostsecret",
		"filex_db_dsn=postgres://u:p@db/filex", // lower-case does not slip past
		"FILEX_PLUGIN_TOKEN=leaked-from-host",  // even one of OUR names, when it comes from the host
		"SMTP_PASSWORD=hunter2",
		"DATABASE_URL=postgres://x",
		"AWS_SECRET_ACCESS_KEY=abc",
		"garbage-without-equals",
		"=novalue",
	}
	own := []string{"FILEX_PLUGIN_TOKEN=minted", "FILEX_PLUGIN_NAME=memfs"}
	got := buildEnv(host, own)

	want := map[string]string{
		"PATH": "/usr/bin", "Path": "C:\\Windows", "HOME": "/root", "LC_ALL": "C.UTF-8",
		"LANG": "en_US.UTF-8", "TZ": "Europe/Istanbul", "SystemRoot": "C:\\Windows",
		"FILEX_PLUGIN_TOKEN": "minted", "FILEX_PLUGIN_NAME": "memfs",
	}
	seen := map[string]string{}
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		if _, ok := want[k]; !ok {
			t.Errorf("variable %q must not reach the plugin", kv)
		}
		seen[k] = v
	}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("%s: got %q, want %q", k, seen[k], v)
		}
	}
	for _, kv := range got {
		if strings.HasPrefix(strings.ToUpper(kv), "FILEX_") && !strings.HasPrefix(kv, "FILEX_PLUGIN_") {
			t.Errorf("a host FILEX_* variable crossed: %q", kv)
		}
	}
}

// ── isNetErr ────────────────────────────────────────────────────────────────

func TestIsNetErrIsTypedNotSearched(t *testing.T) {
	opErr := &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	yes := []error{
		&url.Error{Op: "Get", URL: "http://x", Err: opErr},
		opErr,
		&url.Error{Op: "Get", URL: "http://x", Err: io.EOF},
		&url.Error{Op: "Get", URL: "http://x", Err: io.ErrUnexpectedEOF},
		&url.Error{Op: "Get", URL: "http://x", Err: context.DeadlineExceeded},
		&net.DNSError{Err: "no such host", Name: "plugin.invalid", IsNotFound: true},
		context.DeadlineExceeded,
		syscall.ECONNREFUSED,
	}
	for _, err := range yes {
		wrapped := errors.Join(errors.New("plugin: GET /v1/describe"), err)
		if !isNetErr(wrapped) {
			t.Errorf("%T %v should be a network error", err, err)
		}
	}
	no := []error{
		nil,
		errors.New("plugin says: timeout while opening bucket"), // the WORD, in a plugin's own message
		errors.New("unexpected EOF reading config"),
		io.EOF, // a bare EOF is a decode of an empty body, not the wire
		&pluginError{Status: 400, Code: ErrCodeInvalid, Message: "connection refused by policy"},
		&url.Error{Op: "Get", URL: "https://x", Err: errors.New("x509: certificate signed by unknown authority")},
	}
	for _, err := range no {
		if isNetErr(err) {
			t.Errorf("%T %v must not count as a network error", err, err)
		}
	}
}

// ── network rules ───────────────────────────────────────────────────────────

func TestRefusedAndPrivateHost(t *testing.T) {
	cases := []struct {
		ip               string
		refused, private bool
	}{
		{"127.0.0.1", true, true},
		{"::1", true, true},
		{"10.1.2.3", true, true},
		{"172.16.0.9", true, true},
		{"192.168.1.10", true, true},
		{"169.254.169.254", true, true}, // the metadata service: refused for downloads, private for remotes
		{"fd00::1", true, true},         // ULA
		{"fe80::1", true, true},
		{"0.0.0.0", true, false},
		{"224.0.0.1", true, false},
		{"203.0.113.10", false, false},
		{"2001:db8::10", false, false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if got := netguard.Refused(ip); got != c.refused {
			t.Errorf("netguard.Refused(%s) = %v, want %v", c.ip, got, c.refused)
		}
		if got := netguard.Private(ip); got != c.private {
			t.Errorf("netguard.Private(%s) = %v, want %v", c.ip, got, c.private)
		}
	}
	if !netguard.Refused(nil) || netguard.Private(nil) {
		t.Fatal("nil is refused as a download target and never private")
	}
}

func TestCheckDownloadURLRefusesPrivateLiterals(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:8080/x", "http://10.0.0.5/x", "http://169.254.169.254/latest", "http://[::1]/x", "http://[fd00::2]/x"} {
		_, err := checkDownloadURL(u, true)
		var rej RejectedError
		if err == nil || !errors.As(err, &rej) {
			t.Errorf("%s: want a rejection, got %v", u, err)
		}
		// An embedder that supplied its own client has taken the guard over.
		if _, err := checkDownloadURL(u, false); err != nil {
			t.Errorf("%s: with the guard off only the scheme is checked, got %v", u, err)
		}
	}
	for _, u := range []string{"ftp://x/y", "file:///etc/passwd", "http:///nohost", "not a url"} {
		if _, err := checkDownloadURL(u, false); err == nil {
			t.Errorf("%s: should be refused whatever the guard says", u)
		}
	}
	if _, err := checkDownloadURL("https://releases.example.com/memfs", true); err != nil {
		t.Fatalf("a public https URL is what this is for: %v", err)
	}
}

func TestCheckRemoteAddressPlainHTTPOnlyInsideThePrivateNetwork(t *testing.T) {
	ctx := context.Background()
	ok := []string{
		"http://127.0.0.1:9099", "http://[::1]:9099", "http://10.2.3.4:9000", "http://192.168.1.5",
		"http://172.26.0.3:8080", "http://[fd00::5]:9000", "http://localhost:9099",
		"https://plugins.example.com", "https://203.0.113.10:8443",
	}
	for _, a := range ok {
		if err := checkRemoteAddress(ctx, a); err != nil {
			t.Errorf("%s should be accepted: %v", a, err)
		}
	}
	bad := []string{"http://203.0.113.10:9000", "http://[2001:db8::10]:9000", "http://8.8.8.8"}
	for _, a := range bad {
		err := checkRemoteAddress(ctx, a)
		var rej RejectedError
		if err == nil || !errors.As(err, &rej) || !strings.Contains(err.Error(), "must use https://") {
			t.Errorf("%s: want the https refusal, got %v", a, err)
		}
	}
	if err := checkRemoteAddress(ctx, "ftp://x"); err == nil {
		t.Fatal("a non-http scheme must be refused")
	}
}

// ── wire objects ────────────────────────────────────────────────────────────

func TestObjectToStorageCleansWhatThePluginSent(t *testing.T) {
	cases := []struct {
		in             Object
		path, name     string
		size           int64
		listable, kind bool
	}{
		{Object{Path: "a/b.txt", Name: "wrong.txt", Size: 5, Kind: "file"}, "a/b.txt", "b.txt", 5, true, true},
		{Object{Path: "/a/b/", Name: "", Size: -1, Kind: "dir"}, "a/b", "b", 0, true, true},
		{Object{Path: "a/../../../etc/passwd", Kind: "file"}, "etc/passwd", "passwd", 0, true, true},
		{Object{Path: "./x/./y", Kind: "file"}, "x/y", "y", 0, true, true},
		{Object{Path: "", Name: "root", Kind: "dir"}, "", "", 0, false, true},
		{Object{Path: ".", Kind: "dir"}, "", "", 0, false, true},
		{Object{Path: "..", Kind: "dir"}, "", "", 0, false, true},
		{Object{Path: "/", Kind: "dir"}, "", "", 0, false, true},
		{Object{Path: "f", Kind: "banana"}, "f", "f", 0, true, false}, // unknown kind → file
	}
	for _, c := range cases {
		got := c.in.toStorage()
		if got.Path != c.path || got.Name != c.name || got.Size != c.size {
			t.Errorf("%+v → path=%q name=%q size=%d, want %q %q %d", c.in, got.Path, got.Name, got.Size, c.path, c.name, c.size)
		}
		if c.in.listable() != c.listable {
			t.Errorf("%+v listable = %v, want %v", c.in, c.in.listable(), c.listable)
		}
		if !c.kind && got.Kind != storage.KindFile {
			t.Errorf("%+v: unknown kind should fall back to file, got %q", c.in, got.Kind)
		}
	}
}

// ── success bodies are bounded ──────────────────────────────────────────────

func TestDecodeLimitedRefusesAnOversizedAnswer(t *testing.T) {
	big := `{"instance":"` + strings.Repeat("x", 3000) + `"}`
	var out InstanceResponse
	err := decodeLimited(strings.NewReader(big), &out, 1024, 200)
	var pe *pluginError
	if !errors.As(err, &pe) || pe.Code != ErrCodeInvalid {
		t.Fatalf("an answer past the limit must be the plugin's invalid answer, got %v", err)
	}
	out = InstanceResponse{}
	if err := decodeLimited(strings.NewReader(`{"instance":"i1"}`), &out, 1024, 200); err != nil || out.Instance != "i1" {
		t.Fatalf("a small answer decodes as before: %v %+v", err, out)
	}
}

// ── the supervisor stops trying ─────────────────────────────────────────────

// A binary that never comes up is restarted with backoff — ten times. After
// that the supervisor reports ErrGaveUp and stops; the admin's Restart is the
// way back. Without the ceiling a broken plugin was "starting" forever.
func TestSupervisorGivesUpAfterTenFailedStarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the plugin")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "crash.sh")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	downs := make(chan error, 32)
	p := &Process{
		Name: "crash", Binary: bin, Token: "t", SockDir: filepath.Join(dir, "run"),
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		restartBackoff: time.Millisecond,
		OnDown:         func(err error) { downs <- err },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	t.Cleanup(p.Stop)

	deadline := time.After(30 * time.Second)
	n := 0
	for {
		select {
		case err := <-downs:
			n++
			if errors.Is(err, ErrGaveUp) {
				if n != maxConsecutiveFailures {
					t.Fatalf("gave up after %d failures, want %d", n, maxConsecutiveFailures)
				}
				if p.Err() == nil || !errors.Is(p.Err(), ErrGaveUp) {
					t.Fatalf("Err() should carry the give-up: %v", p.Err())
				}
				// …and it really stopped: no further OnDown within a while.
				select {
				case err := <-downs:
					t.Fatalf("the supervisor kept restarting after giving up: %v", err)
				case <-time.After(300 * time.Millisecond):
				}
				return
			}
			if n > maxConsecutiveFailures {
				t.Fatalf("%d failures without giving up", n)
			}
		case <-deadline:
			t.Fatalf("supervisor never gave up (saw %d failures)", n)
		}
	}
}
