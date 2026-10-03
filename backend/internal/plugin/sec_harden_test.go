package plugin_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// ── P-1: every start runs a binary checked at that start ────────────────────

// The supervisor restarts a plugin that died. The file it starts again must be
// held to the sha256 (and the signature) recorded at install EVERY time, not
// only at the first start: a binary replaced on disk between two starts - by
// the plugin itself, which runs as filex's user, or by anything else that can
// write there - used to be run by the next restart unchecked. What runs is a
// private copy made and hashed at that start, never the installed file.
func TestSupervisorVerifiesTheBinaryBeforeEveryStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the plugin")
	}
	work := t.TempDir()
	runs := filepath.Join(work, "runs.log")
	mark := filepath.Join(work, "tampered")
	script := "#!/bin/sh\necho \"$0\" >> " + runs + "\nexit 3\n"

	m, _, dir := newManagerWith(t, nil)
	st, err := m.InstallBinary(context.Background(), "crashy", "crashy", strings.NewReader(script), "")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !waitFile(t, runs, 10*time.Second) {
		t.Fatal("the plugin never ran")
	}
	installed := filepath.Join(dir, "crashy", "crashy")
	b, _ := os.ReadFile(runs)
	first := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	if first == installed {
		t.Errorf("the installed file itself was executed (%s); a verified private copy should run", first)
	}

	// Between two starts the installed file is replaced.
	tmp := installed + ".swap"
	if err := os.WriteFile(tmp, []byte("#!/bin/sh\ntouch "+mark+"\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, installed); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(mark); err == nil {
			t.Fatal("the restart ran a binary that was replaced since install")
		}
		got, err := m.Get(context.Background(), st.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == plugin.StateRefused {
			if !strings.Contains(got.StateError, "changed on disk") {
				t.Fatalf("refused, but not for the replaced file: %q", got.StateError)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never refused; state=%q err=%q", got.State, got.StateError)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("the replaced binary ran after all")
	}
}

// ── P-2: a driver name belongs to the plugin that first claimed it ──────────

// The driver name is the plugin's own claim, and a storage's credentials go to
// whichever plugin provides plugin:<name>. A second plugin claiming the name
// while the first one is stopped used to be handed every storage built on the
// first one; and a plugin could change its own claim at an upgrade.
func TestADriverNameBelongsToThePluginThatFirstClaimedIt(t *testing.T) {
	f1 := newFakePlugin("acme", fullCaps())
	defer f1.Close()
	f2 := newFakePlugin("acme", fullCaps())
	defer f2.Close()
	m, _, _ := newManager(t)
	ctx := context.Background()

	a, err := m.InstallRemote(ctx, "first", f1.URL(), "test-token")
	if err != nil {
		t.Fatalf("install first: %v", err)
	}
	waitState(t, m, a.ID, plugin.StateRunning)
	if _, err := m.SetEnabled(ctx, a.ID, false); err != nil {
		t.Fatalf("disable first: %v", err)
	}

	b, err := m.InstallRemote(ctx, "second", f2.URL(), "test-token")
	if err != nil {
		t.Fatalf("install second: %v", err)
	}
	got := waitState(t, m, b.ID, plugin.StateRefused)
	if !strings.Contains(got.StateError, `"first"`) {
		t.Fatalf("the refusal should name the plugin the driver belongs to, got %q", got.StateError)
	}
	if _, err := storage.Get("plugin:acme"); err == nil {
		t.Fatal("the second plugin was registered as plugin:acme")
	}

	// The first plugin keeps its name - and may not trade it for another.
	f1.setName("other")
	if _, err := m.SetEnabled(ctx, a.ID, true); err != nil {
		t.Fatalf("enable first: %v", err)
	}
	got = waitState(t, m, a.ID, plugin.StateRefused)
	if !strings.Contains(got.StateError, `"acme"`) {
		t.Fatalf("a plugin that changes its driver name should be refused naming the old one, got %q", got.StateError)
	}
	f1.setName("acme")
	if _, err := m.Restart(ctx, a.ID); err != nil {
		t.Fatalf("restart: %v", err)
	}
	waitState(t, m, a.ID, plugin.StateRunning)
}

// ── P-4: a remote plugin's answers are not redirects to somewhere else ──────

// The token and every storage credential go to the address the operator
// registered. A remote that answers with a redirect sent them - the bearer
// token included, Go keeps Authorization for the same host - wherever the
// redirect pointed.
func TestRemotePluginRedirectsAreNotFollowed(t *testing.T) {
	f := newFakePlugin("acme", fullCaps())
	defer f.Close()
	var bounced atomic.Int32
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bounced.Add(1)
		http.Redirect(w, r, f.URL()+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}))
	defer front.Close()
	m, _, _ := newManager(t)

	st, err := m.InstallRemote(context.Background(), "bouncy", front.URL, "test-token")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	got := waitState(t, m, st.ID, plugin.StateRefused)
	if !strings.Contains(got.StateError, "redirect") {
		t.Fatalf("the refusal should say it was a redirect, got %q", got.StateError)
	}
	if bounced.Load() == 0 {
		t.Fatal("the front was never asked")
	}
	if d, _, _ := f.counts(); d != 0 {
		t.Fatalf("the redirect target was asked %d times", d)
	}
}

// ── P-5: a launched plugin's handshake names its own socket ─────────────────

// A launched plugin may listen on a unix socket in the directory filex gave
// it, or on a loopback port - nothing else. A handshake naming another
// socket, or a remote http(s) address, used to send the plugin's token and
// every storage credential to whatever listened there.
func TestHandshakeMayNameOnlyItsOwnSocketOrLoopback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the plugin")
	}
	sockDir, err := os.MkdirTemp("", "fxs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	elsewhere := filepath.Join(sockDir, "other.sock")
	ln, err := net.Listen("unix", elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	outside := newFakePluginOn(ln, "outside", fullCaps())
	defer outside.Close()
	remote := newFakePlugin("remote", fullCaps())
	remote.anyToken = true
	defer remote.Close()

	m, _, _ := newManagerWith(t, nil)
	cases := []struct {
		name, addr string
		f          *fakePlugin
	}{
		{"outside", "unix:" + elsewhere, outside},
		{"remote", remote.URL(), remote},
	}
	for _, c := range cases {
		script := "#!/bin/sh\necho 'FILEX-PLUGIN/1 " + c.addr + "'\nexec sleep 60\n"
		st, err := m.InstallBinary(context.Background(), c.name, c.name, strings.NewReader(script), "")
		if err != nil {
			t.Fatalf("%s: install: %v", c.name, err)
		}
		got := waitState(t, m, st.ID, plugin.StateRefused)
		if !strings.Contains(got.StateError, "handshake") {
			t.Errorf("%s: the refusal should name the handshake rule, got %q", c.name, got.StateError)
		}
		if d, _, _ := c.f.counts(); d != 0 {
			t.Errorf("%s: filex spoke to %s (%d describes)", c.name, c.addr, d)
		}
	}
}

// ── P-7: a row with a malformed sha256 is refused, not a crash ──────────────

func TestARowWithAMalformedSHA256IsRefusedNotACrash(t *testing.T) {
	m, store, dir := newManagerWith(t, nil)
	row := seedBinaryRow(t, store, dir, "short", []byte("#!/bin/sh\nexit 0\n"))
	row.SHA256 = "abc"
	if err := store.UpdatePlugin(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}
	got := waitState(t, m, row.ID, plugin.StateRefused)
	if !strings.Contains(got.StateError, "invalid sha256") {
		t.Fatalf("the refusal should name the malformed sha256, got %q", got.StateError)
	}
}

// ── P-8: what a storage handed a plugin does not outlive the storage ────────

// A storage's configuration - its credentials - lives in the plugin as an
// instance. Closing the driver (an edit, a delete, the end of a "Test
// connection") releases it, and so does initialising the driver again.
func TestClosingADriverReleasesItsInstance(t *testing.T) {
	f, drv := newDriver(t, fullCaps())
	if _, _, live := f.counts(); live != 1 {
		t.Fatalf("one instance after Init, got %d", live)
	}
	if err := drv.Init(context.Background(), map[string]any{"root": "/data2", "secret": "s2"}); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	if _, _, live := f.counts(); live != 1 {
		t.Fatalf("a second Init must release the first instance: %d live", live)
	}
	c, ok := drv.(interface{ Close() error })
	if !ok {
		t.Fatal("a plugin driver must be closable")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, deletes, live := f.counts(); live != 0 || deletes != 2 {
		t.Fatalf("after Close: %d live, %d deleted; want 0 and 2", live, deletes)
	}
}
