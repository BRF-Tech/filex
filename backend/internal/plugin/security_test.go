package plugin_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// newManagerWith is newManager with the options a test needs to change, and
// WITHOUT Load — the tests that seed rows by hand call it themselves.
func newManagerWith(t *testing.T, mutate func(*plugin.Options)) (*plugin.Manager, db.Store, string) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	dir := t.TempDir()
	o := plugin.Options{Store: store, Dir: dir, SecretKey: "test-secret-key"}
	if mutate != nil {
		mutate(&o)
	}
	m, err := plugin.New(o)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(m.Shutdown)
	return m, store, dir
}

// plainClient reaches loopback: the tests that download from an httptest
// server need to get past the guard the default client applies.
func plainClient() *http.Client { return &http.Client{Timeout: time.Minute} }

func shaOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// waitFile polls for a file the plugin under test is expected to create.
func waitFile(t *testing.T, path string, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// ── 1. sha256 before anything runs ──────────────────────────────────────────

// A download whose sha256 does not match is refused BEFORE it is executed.
// The served "plugin" leaves a marker when run; under the old order (install,
// start, then compare) the marker appeared and the row was created and
// removed again — a mismatched download had already run as filex's user.
func TestInstallFromURLComparesSHABeforeAnythingRuns(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	script := []byte("#!/bin/sh\ntouch " + marker + "\nexit 0\n")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(script)
	}))
	defer srv.Close()

	m, store, dir := newManagerWith(t, func(o *plugin.Options) { o.HTTP = plainClient() })
	ctx := context.Background()
	wrong := shaOf([]byte("something else"))
	_, err := m.InstallFromURL(ctx, "urlfs", srv.URL+"/urlfs.sh", wrong, "")
	if err == nil {
		t.Fatal("a download with the wrong sha256 must be refused")
	}
	var rej plugin.RejectedError
	if !errors.As(err, &rej) || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("want a 400-class sha256 mismatch, got %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("the file should have been fetched exactly once, got %d", hits.Load())
	}
	if _, err := store.GetPluginByName(ctx, "urlfs"); err == nil {
		t.Fatal("no row may exist for a refused download")
	}
	if _, err := os.Stat(filepath.Join(dir, "urlfs")); !os.IsNotExist(err) {
		t.Fatalf("the plugin directory must not be left behind: %v", err)
	}
	if runtime.GOOS != "windows" {
		// Give a would-be process every chance to leave its mark, then look.
		time.Sleep(300 * time.Millisecond)
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("the downloaded file was EXECUTED before its sha256 was checked")
		}
	}

	// And the right sha256 installs it — the file is on disk, the row exists.
	st, err := m.InstallFromURL(ctx, "urlfs", srv.URL+"/urlfs.sh", shaOf(script), "")
	if err != nil {
		t.Fatalf("install with the right sha256: %v", err)
	}
	if st.SHA256 != shaOf(script) || st.Binary != plugin.ExecName("urlfs.sh", runtime.GOOS) {
		t.Fatalf("row does not describe the download: %+v", st.Plugin)
	}
	if _, err := os.Stat(filepath.Join(dir, "urlfs", st.Binary)); err != nil {
		t.Fatalf("binary not on disk: %v", err)
	}
}

// ── 6. a download may not probe the private network ─────────────────────────

func TestInstallFromURLRefusesPrivateTargets(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("#!/bin/sh\n"))
	}))
	defer srv.Close()
	m, _, _ := newManagerWith(t, nil) // the DEFAULT client: guarded
	ctx := context.Background()
	sum := shaOf([]byte("#!/bin/sh\n"))

	// A literal loopback address is refused before any connection.
	_, err := m.InstallFromURL(ctx, "p1", srv.URL+"/x", sum, "")
	var rej plugin.RejectedError
	if err == nil || !errors.As(err, &rej) || !strings.Contains(err.Error(), "private or local address") {
		t.Fatalf("loopback literal: want a rejection naming the private address, got %v", err)
	}
	// A NAME that resolves to loopback is refused at dial time — the SSRF
	// shape, where the URL looks harmless and the DNS answer is not.
	byName := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	_, err = m.InstallFromURL(ctx, "p2", byName+"/x", sum, "")
	if err == nil || !errors.As(err, &rej) || !strings.Contains(err.Error(), "private or local address") {
		t.Fatalf("loopback by name: want a rejection naming the private address, got %v", err)
	}
	for _, u := range []string{"http://169.254.169.254/latest/meta-data", "http://10.0.0.5:9200/_cat", "http://[fd00::1]/x"} {
		if _, err := m.InstallFromURL(ctx, "p3", u, sum, ""); err == nil || !errors.As(err, &rej) {
			t.Errorf("%s: want a rejection, got %v", u, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("the private server was reached %d times", hits.Load())
	}
}

// ── 3. a remote plugin is TLS unless it stays on the private network ────────

func TestInstallRemoteRequiresTLSOutsideThePrivateNetwork(t *testing.T) {
	m, _, _ := newManagerWith(t, nil)
	ctx := context.Background()
	for _, a := range []string{"http://203.0.113.10:9000", "http://8.8.8.8/plugin"} {
		_, err := m.InstallRemote(ctx, "pub", a, "tok")
		var rej plugin.RejectedError
		if err == nil || !errors.As(err, &rej) || !strings.Contains(err.Error(), "remote plugins outside the private network must use https://") {
			t.Fatalf("%s: want the https refusal, got %v", a, err)
		}
	}
	// Private targets over plain http register; a public one over https too.
	for i, a := range []string{"http://127.0.0.1:1", "http://10.9.8.7:9000", "http://[fd00::9]:9000", "https://203.0.113.10:9000"} {
		if _, err := m.InstallRemote(ctx, "ok"+string(rune('a'+i)), a, "tok"); err != nil {
			t.Fatalf("%s should register: %v", a, err)
		}
	}
}

// ── 2. the plugin does not see filex's environment ──────────────────────────

// A launched plugin sees the allow-list and FILEX_PLUGIN_*, and nothing filex
// itself was handed — measured on the real spawned process, not on the
// function that builds the list.
func TestLaunchedPluginDoesNotInheritFilexSecrets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the plugin")
	}
	t.Setenv("FILEX_SECRET_KEY", "the-instance-secret")
	t.Setenv("FILEX_DB_DSN", "postgres://filex:pw@db/filex")
	t.Setenv("SMTP_PASSWORD", "hunter2")
	t.Setenv("LC_ALL", "C.UTF-8")
	out := filepath.Join(t.TempDir(), "env.txt")
	script := "#!/bin/sh\nenv > " + out + "\nexit 0\n"

	m, _, _ := newManagerWith(t, nil)
	if _, err := m.InstallBinary(context.Background(), "envdump", "envdump.sh", strings.NewReader(script), ""); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !waitFile(t, out, 10*time.Second) {
		t.Fatal("the plugin never ran")
	}
	// The file is written by the child; give the redirect a moment to close.
	time.Sleep(100 * time.Millisecond)
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	env := string(b)
	for _, leak := range []string{"FILEX_SECRET_KEY=", "FILEX_DB_DSN=", "SMTP_PASSWORD="} {
		if strings.Contains(env, leak) {
			t.Errorf("%s reached the plugin:\n%s", leak, env)
		}
	}
	for _, want := range []string{"FILEX_PLUGIN_TOKEN=", "FILEX_PLUGIN_SOCKET_DIR=", "FILEX_PLUGIN_NAME=envdump", "FILEX_PLUGIN_PROTOCOL=1", "PATH=", "LC_ALL=C.UTF-8"} {
		if !strings.Contains(env, want) {
			t.Errorf("%s missing from the plugin's environment:\n%s", want, env)
		}
	}
}

// ── 5. the signature is kept and checked again at every start ───────────────

func seedBinaryRow(t *testing.T, store db.Store, dir, name string, content []byte) *model.Plugin {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := plugin.ExecName(name, runtime.GOOS)
	if err := os.WriteFile(filepath.Join(dir, name, bin), content, 0o755); err != nil {
		t.Fatal(err)
	}
	row, err := store.CreatePlugin(context.Background(), &model.Plugin{
		Name: name, Kind: model.PluginKindBinary, Binary: bin, SHA256: shaOf(content), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestStoredSignatureIsVerifiedAtStart(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trusted := func(o *plugin.Options) { o.TrustedKeys = []string{hex.EncodeToString(pub)} }
	content := []byte("#!/bin/sh\nexit 0\n")
	sum := shaOf(content)
	goodSig := hex.EncodeToString(ed25519.Sign(priv, []byte(sum)))

	t.Run("install keeps the signature beside the binary", func(t *testing.T) {
		m, _, dir := newManagerWith(t, trusted)
		st, err := m.InstallBinary(context.Background(), "signed", "signed", strings.NewReader(string(content)), goodSig)
		if err != nil {
			t.Fatalf("install: %v", err)
		}
		b, err := os.ReadFile(filepath.Join(dir, "signed", st.Binary+".sig"))
		if err != nil {
			t.Fatalf("no .sig beside the binary: %v", err)
		}
		if strings.TrimSpace(string(b)) != goodSig {
			t.Fatalf(".sig holds %q, want the signature that was verified", b)
		}
	})

	t.Run("no signature file and trusted keys → refused with the way out", func(t *testing.T) {
		m, store, dir := newManagerWith(t, trusted)
		row := seedBinaryRow(t, store, dir, "legacy", content) // installed "before the keys were set"
		if err := m.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		st := waitState(t, m, row.ID, plugin.StateRefused)
		if !strings.Contains(st.StateError, "signature required") || !strings.Contains(st.StateError, "reinstall") {
			t.Fatalf("refusal must say a signature is required and how to fix it, got %q", st.StateError)
		}
	})

	t.Run("a signature file that does not verify → refused", func(t *testing.T) {
		m, store, dir := newManagerWith(t, trusted)
		row := seedBinaryRow(t, store, dir, "forged", content)
		_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
		forged := hex.EncodeToString(ed25519.Sign(otherPriv, []byte(sum)))
		if err := os.WriteFile(filepath.Join(dir, "forged", row.Binary+".sig"), []byte(forged), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := m.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		st := waitState(t, m, row.ID, plugin.StateRefused)
		if !strings.Contains(st.StateError, "signature") {
			t.Fatalf("refusal should name the signature, got %q", st.StateError)
		}
	})

	t.Run("a valid signature file gets past the gate", func(t *testing.T) {
		m, store, dir := newManagerWith(t, trusted)
		row := seedBinaryRow(t, store, dir, "vouched", content)
		if err := os.WriteFile(filepath.Join(dir, "vouched", row.Binary+".sig"), []byte(goodSig+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := m.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		// The script is not a plugin, so it will not reach running — but it
		// must not be REFUSED, and the reason must not be its signature.
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			st, _ := m.Get(context.Background(), row.ID)
			if st.State == plugin.StateRefused {
				t.Fatalf("a correctly signed binary was refused: %q", st.StateError)
			}
			if st.State == plugin.StateFailed {
				if strings.Contains(st.StateError, "signature") {
					t.Fatalf("failure blames the signature: %q", st.StateError)
				}
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("plugin never left starting")
	})

	t.Run("without trusted keys nothing is demanded", func(t *testing.T) {
		m, store, dir := newManagerWith(t, nil)
		row := seedBinaryRow(t, store, dir, "open", content)
		if err := m.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			st, _ := m.Get(context.Background(), row.ID)
			if st.State == plugin.StateRefused {
				t.Fatalf("no keys configured, yet refused: %q", st.StateError)
			}
			if st.State == plugin.StateFailed {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
}

// ── 7. a row that would leave the plugins directory is never started ────────

func TestLoadRefusesRowsThatLeaveThePluginsDirectory(t *testing.T) {
	m, store, _ := newManagerWith(t, nil)
	ctx := context.Background()
	traversal, err := store.CreatePlugin(ctx, &model.Plugin{
		Name: "sneaky", Kind: model.PluginKindBinary, Binary: "../../evil", SHA256: shaOf([]byte("x")), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	badName, err := store.CreatePlugin(ctx, &model.Plugin{
		Name: "../up", Kind: model.PluginKindBinary, Binary: "evil", SHA256: shaOf([]byte("x")), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Load(ctx); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, m, traversal.ID, plugin.StateRefused)
	if !strings.Contains(st.StateError, "invalid binary name") {
		t.Fatalf("a binary name with a path in it must be refused by name, got %q", st.StateError)
	}
	st = waitState(t, m, badName.ID, plugin.StateRefused)
	if !strings.Contains(st.StateError, "invalid name") {
		t.Fatalf("an invalid plugin name must be refused by name, got %q", st.StateError)
	}
	// Restart and enable go through the same gate.
	if st, _ := m.Restart(ctx, traversal.ID); st.State != plugin.StateRefused {
		t.Fatalf("restart must not start it: %s", st.State)
	}
}

// ── 4. an upgrade under another file name still rolls back ──────────────────

// The upgrade keeps the file name the plugin was installed under, so the
// backup, the swap and the rollback all talk about ONE path. Before, a new
// upload called differently found "no old file" at its own path, skipped the
// rollback, and reported that the previous binary was restored while the
// plugin sat refused.
func TestUpgradeUnderAnotherNameRollsBackToTheOldBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := buildExamplePlugin(t)
	m, store, dir := newManager(t)
	ctx := context.Background()

	f, err := os.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := m.InstallBinary(ctx, "memfs", filepath.Base(bin), f, "")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	st = waitState(t, m, st.ID, plugin.StateRunning)
	oldBinary, oldSum := st.Binary, st.SHA256
	oldPath := filepath.Join(dir, "memfs", oldBinary)

	_, err = m.Upgrade(ctx, st.ID, "memfs-v2", strings.NewReader("this is not a plugin binary"), "")
	if err == nil {
		t.Fatal("upgrading to garbage must fail")
	}
	if !strings.Contains(err.Error(), "the previous one was restored") {
		t.Fatalf("the error should say the old binary is back: %v", err)
	}
	after := waitState(t, m, st.ID, plugin.StateRunning)
	if after.Binary != oldBinary || after.SHA256 != oldSum {
		t.Fatalf("row should describe the OLD binary again: %+v", after.Plugin)
	}
	row, _ := store.GetPlugin(ctx, st.ID)
	if row.Binary != oldBinary || row.SHA256 != oldSum {
		t.Fatalf("database row not rolled back: %+v", row)
	}
	b, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("old binary missing after rollback: %v", err)
	}
	if shaOf(b) != oldSum {
		t.Fatal("the file at the old path is not the old binary")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "memfs"))
	for _, e := range entries {
		switch e.Name() {
		case oldBinary, "run":
		default:
			t.Errorf("leftover after rollback: %s", e.Name())
		}
	}
}
