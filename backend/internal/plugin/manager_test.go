package plugin_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func newManager(t *testing.T) (*plugin.Manager, db.Store, string) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	dir := t.TempDir()
	m, err := plugin.New(plugin.Options{Store: store, Dir: dir, SecretKey: "test-secret-key"})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(m.Shutdown)
	if err := m.Load(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}
	return m, store, dir
}

// A remote plugin is registered, described, and its driver appears in the
// SAME registry the built-in drivers use — which is what makes the
// Connections page render its form with no frontend change.
func TestRemotePluginRegistersItsDriverAndDescriptor(t *testing.T) {
	f := newFakePlugin("acme", fullCaps())
	defer f.Close()
	m, _, _ := newManager(t)
	ctx := context.Background()

	st, err := m.InstallRemote(ctx, "acme", f.URL(), "test-token")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	waitState(t, m, st.ID, plugin.StateRunning)

	drv, err := storage.Get("plugin:acme")
	if err != nil {
		t.Fatalf("driver not registered: %v", err)
	}
	if drv.Name() != "plugin:acme" {
		t.Fatalf("driver names itself %q", drv.Name())
	}
	desc, ok := storage.DescriptorFor("plugin:acme")
	if !ok {
		t.Fatal("no descriptor registered — the admin form would have no fields")
	}
	if desc.Label != "Fake storage" || len(desc.Fields) != 2 {
		t.Fatalf("descriptor did not come from the plugin: %+v", desc)
	}
	if _, hasRoot := desc.RootField(); !hasRoot {
		t.Fatal("plugin descriptor must carry the Root field, or every storage on it is refused")
	}
	// The descriptor is in the list every surface renders from.
	found := false
	for _, d := range storage.Descriptors() {
		if d.Driver == "plugin:acme" {
			found = true
			if !d.Capabilities.Write || !d.Capabilities.Read {
				t.Fatalf("capabilities not computed for the plugin driver: %+v", d.Capabilities)
			}
		}
	}
	if !found {
		t.Fatal("plugin driver missing from storage.Descriptors()")
	}

	// A storage created on it actually works.
	if err := drv.Init(ctx, map[string]any{"root": "/data"}); err != nil {
		t.Fatalf("init through the registry: %v", err)
	}
	f.seed("hello.txt", "hi")
	objs, err := drv.List(ctx, "")
	if err != nil || len(objs) != 1 {
		t.Fatalf("list through the registry: %v %+v", err, objs)
	}
}

// Disabling a plugin removes its driver: a storage on it must fail to open
// rather than silently keep working through a stale registration.
func TestDisableUnregistersTheDriver(t *testing.T) {
	f := newFakePlugin("acme", fullCaps())
	defer f.Close()
	m, _, _ := newManager(t)
	ctx := context.Background()
	st, err := m.InstallRemote(ctx, "acme", f.URL(), "test-token")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	waitState(t, m, st.ID, plugin.StateRunning)

	if _, err := m.SetEnabled(ctx, st.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := storage.Get("plugin:acme"); err == nil {
		t.Fatal("driver still registered after the plugin was disabled")
	}
	if _, ok := storage.DescriptorFor("plugin:acme"); ok {
		t.Fatal("descriptor still registered after the plugin was disabled")
	}

	// …and re-enabling brings it back.
	if _, err := m.SetEnabled(ctx, st.ID, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	waitState(t, m, st.ID, plugin.StateRunning)
	if _, err := storage.Get("plugin:acme"); err != nil {
		t.Fatalf("driver not back after re-enable: %v", err)
	}
}

// Two plugins cannot both provide plugin:acme — the second is refused with a
// message naming the first, instead of one silently replacing the other's
// storages.
func TestDriverNameCollisionIsRefused(t *testing.T) {
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

	b, err := m.InstallRemote(ctx, "second", f2.URL(), "test-token")
	if err != nil {
		t.Fatalf("install second: %v", err)
	}
	got := waitState(t, m, b.ID, plugin.StateRefused)
	if !strings.Contains(got.StateError, "already provided") {
		t.Fatalf("refusal should name the conflict, got %q", got.StateError)
	}
	// The first one still owns the driver.
	if _, err := storage.Get("plugin:acme"); err != nil {
		t.Fatalf("the incumbent lost its driver: %v", err)
	}
}

// The remote token is sealed with the instance key, never stored in the
// clear, and never leaves the server in an API response.
func TestRemoteTokenIsSealed(t *testing.T) {
	f := newFakePlugin("acme", fullCaps())
	defer f.Close()
	m, store, _ := newManager(t)
	ctx := context.Background()
	st, err := m.InstallRemote(ctx, "acme", f.URL(), "test-token")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	row, err := store.GetPlugin(ctx, st.ID)
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	if row.TokenSealed == "" || strings.Contains(row.TokenSealed, "test-token") {
		t.Fatalf("token is not sealed in the database: %q", row.TokenSealed)
	}
	if !strings.HasPrefix(row.TokenSealed, "enc:v1:") {
		t.Fatalf("token not sealed by secretbox: %q", row.TokenSealed)
	}
}

// Without FILEX_SECRET_KEY there is nowhere safe to put the token, so
// registering a remote plugin is refused rather than stored in plaintext.
func TestRemoteWithoutSecretKeyIsRefused(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	m, err := plugin.New(plugin.Options{Store: store, Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(m.Shutdown)
	_, err = m.InstallRemote(context.Background(), "acme", "https://example.com", "tok")
	if err == nil || !strings.Contains(err.Error(), "FILEX_SECRET_KEY") {
		t.Fatalf("want a refusal naming the missing key, got %v", err)
	}
}

func TestInstallRejectsABadName(t *testing.T) {
	m, _, _ := newManager(t)
	for _, name := range []string{"", "Acme", "-bad", "a/b", strings.Repeat("x", 33)} {
		if _, err := m.InstallRemote(context.Background(), name, "https://example.com", "tok"); !errors.Is(err, plugin.ErrBadName) {
			t.Fatalf("name %q should be refused with ErrBadName, got %v", name, err)
		}
	}
}

// ── the end-to-end one: a real binary, built here, run by the manager ───────

// A plugin binary the manager launches, hands a token and a socket
// directory, and talks the real protocol to. This is the whole feature in one
// test: SDK → handshake → describe → registry → a file written and read back
// through storage.Driver.
func TestBinaryPluginRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := buildExamplePlugin(t)
	m, _, dir := newManager(t)
	ctx := context.Background()

	f, err := os.Open(bin)
	if err != nil {
		t.Fatalf("open built plugin: %v", err)
	}
	defer f.Close()
	st, err := m.InstallBinary(ctx, "memfs", filepath.Base(bin), f)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	st = waitState(t, m, st.ID, plugin.StateRunning)
	if st.Version != "1.0.0" || st.Driver != "memfs" {
		t.Fatalf("describe did not reach the row: %+v", st.Plugin)
	}
	if st.Capabilities == nil || !st.Capabilities.Write || !st.Capabilities.SetMtime {
		t.Fatalf("capabilities were not derived from the backend's methods: %+v", st.Capabilities)
	}
	// The SDK derives capabilities from the TYPE: memfs has no Mover, so the
	// host must not claim one.
	if st.Capabilities.Move || st.Capabilities.Watch {
		t.Fatalf("capabilities the example does not implement were claimed: %+v", st.Capabilities)
	}
	if _, err := os.Stat(filepath.Join(dir, "memfs")); err != nil {
		t.Fatalf("plugin directory not created: %v", err)
	}

	drv, err := storage.Get("plugin:memfs")
	if err != nil {
		t.Fatalf("driver not registered: %v", err)
	}
	if err := drv.Init(ctx, map[string]any{"prefix": "demo"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	w, ok := drv.(storage.Writer)
	if !ok {
		t.Fatal("the example implements Write+Delete; the driver must be a Writer")
	}
	if err := w.Write(ctx, "notes/hello.txt", strings.NewReader("from a plugin"), 13); err != nil {
		t.Fatalf("write: %v", err)
	}
	rc, err := drv.Read(ctx, "notes/hello.txt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	b := make([]byte, 32)
	n, _ := rc.Read(b)
	rc.Close()
	if string(b[:n]) != "from a plugin" {
		t.Fatalf("round trip lost the bytes: %q", b[:n])
	}
	objs, err := drv.List(ctx, "notes")
	if err != nil || len(objs) != 1 || objs[0].Name != "hello.txt" {
		t.Fatalf("list: %v %+v", err, objs)
	}

	// A binary that changed since it was installed must not be run again.
	//
	// ⚠ Stop it first: a RUNNING executable cannot be overwritten on Linux
	// ("text file busy"), which is also the real operator sequence for
	// replacing a plugin — and the reason the check has to happen at start
	// rather than only at install.
	if _, err := m.SetEnabled(ctx, st.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	// ⚠ Retry: Linux can answer ETXTBSY ("text file busy") for a short moment
	// AFTER the process that ran the file has exited — the executable mapping
	// is torn down asynchronously. Measured here: the same write failed on one
	// run and succeeded immediately on the next. filex itself never overwrites
	// a plugin binary in place (an upgrade removes the directory first), so
	// this is the test reproducing an operator's `cp`, not a product path.
	target := filepath.Join(dir, "memfs", filepath.Base(bin))
	var tamperErr error
	for i := 0; i < 40; i++ {
		if tamperErr = os.WriteFile(target, []byte("not the binary"), 0o755); tamperErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if tamperErr != nil {
		t.Fatalf("tamper: %v", tamperErr)
	}
	after, err := m.SetEnabled(ctx, st.ID, true)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	after = waitState(t, m, after.ID, plugin.StateRefused)
	if !strings.Contains(after.StateError, "changed on disk") {
		t.Fatalf("a modified binary must be refused by name, got %q", after.StateError)
	}
	if _, err := storage.Get("plugin:memfs"); err == nil {
		t.Fatal("driver still registered after the binary was refused")
	}

	// Removing takes the directory with it.
	if err := m.Remove(ctx, st.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "memfs")); !os.IsNotExist(err) {
		t.Fatalf("plugin directory survived removal: %v", err)
	}
}

func buildExamplePlugin(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out := filepath.Join(t.TempDir(), "memfs")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "../../examples/plugin-memfs")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the example plugin failed — the SDK or the example is broken:\n%s", b)
	}
	return out
}

// waitState polls until the plugin reaches want, or fails the test with what
// it actually reached.
func waitState(t *testing.T, m *plugin.Manager, id int64, want string) *plugin.Status {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last *plugin.Status
	for time.Now().Before(deadline) {
		st, err := m.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		last = st
		if st.State == want {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("plugin never reached %q; state=%q err=%q", want, last.State, last.StateError)
	return nil
}
