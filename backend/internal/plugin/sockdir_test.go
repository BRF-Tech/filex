package plugin

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// longDir is a directory under t.TempDir() whose socket path is over the
// platform's limit, the shape of <a long data dir>/plugins/<name>/run.
func longDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for len(dir)+sockNameRoom <= sockPathMax()+8 {
		dir = filepath.Join(dir, "a-rather-long-folder-name")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestShortSocketDir_AShortDirectoryIsUsedAsItIs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	if len(dir)+sockNameRoom > sockPathMax() {
		t.Skip("this machine's temp directory is itself too long")
	}
	got, done, err := shortSocketDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if got != dir {
		t.Errorf("got %q, want the directory itself", got)
	}
}

// A socket in a directory past the limit cannot be bound; the plugin is given
// a short link to the same directory instead, and the link goes when it is
// done with.
func TestShortSocketDir_ALongDirectoryGetsAShortPrivateLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("plugins listen on a loopback port on Windows")
	}
	dir := longDir(t)
	got, done, err := shortSocketDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got)+sockNameRoom > sockPathMax() {
		t.Fatalf("%q (%d characters) leaves no room for the socket", got, len(got))
	}
	real, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	if real != want {
		t.Errorf("the link leads to %q, want %q", real, want)
	}
	parent := filepath.Dir(got)
	if fi, err := os.Stat(parent); err != nil || fi.Mode().Perm() != 0o711 {
		t.Errorf("the link's directory is %v (err %v): others may pass, not list or write", fi.Mode().Perm(), err)
	}
	done()
	if _, err := os.Lstat(parent); !os.IsNotExist(err) {
		t.Errorf("the link's directory outlived done: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("done removed the socket directory itself: %v", err)
	}
}

// The case the 0.56.3 pretag hit: a plugin whose socket directory is past the
// limit never printed its handshake ("listen unix …: bind: invalid argument").
// It comes up now, and its socket is in its own directory.
func TestProcess_APluginInALongDirectoryComesUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("plugins listen on a loopback port on Windows")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	bin := filepath.Join(t.TempDir(), "memfs")
	cmd := exec.Command("go", "build", "-o", bin, "../../examples/plugin-memfs")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the example plugin: %v\n%s", err, b)
	}

	sockDir := filepath.Join(longDir(t), "run")
	ups := make(chan *Client, 1)
	downs := make(chan error, 4)
	p := &Process{
		Name: "memfs", Binary: bin, Token: "t", SockDir: sockDir,
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		restartBackoff: time.Hour,
		// filex talks to it, as the manager does when it adopts a plugin: the
		// socket must be reachable, not only bound.
		OnUp: func(ctx context.Context, c *Client) error {
			if _, err := c.Describe(ctx); err != nil {
				return err
			}
			select {
			case ups <- c:
			default:
			}
			return nil
		},
		OnDown: func(err error) { downs <- err },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	t.Cleanup(p.Stop)

	select {
	case <-ups:
	case err := <-downs:
		t.Fatalf("the plugin did not come up: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("the plugin did not come up within a minute")
	}
	entries, err := os.ReadDir(sockDir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		found = found || strings.HasSuffix(e.Name(), ".sock")
	}
	if !found {
		t.Errorf("no socket in the plugin's own directory %s", sockDir)
	}
}

// A socket filex checked in the real directory is dialled through the link;
// anything else is left exactly as the check answered it.
func TestDialThrough_OnlyRewritesASocketInsideTheDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("plugins listen on a loopback port on Windows")
	}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "s")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	base, _ := filepath.EvalSymlinks(real)

	in := dialThrough(Address{Network: "unix", Target: filepath.Join(base, "p1.sock")}, real, link)
	if in.Target != filepath.Join(link, "p1.sock") {
		t.Errorf("inside: got %q, want it through the link", in.Target)
	}
	out := Address{Network: "unix", Target: filepath.Join(filepath.Dir(base), "elsewhere.sock")}
	if got := dialThrough(out, real, link); got != out {
		t.Errorf("outside: got %q, want it untouched", got.Target)
	}
	tcp := Address{Network: "tcp", Target: "127.0.0.1:9"}
	if got := dialThrough(tcp, real, link); got != tcp {
		t.Errorf("tcp rewritten to %q", got.Target)
	}
	same := Address{Network: "unix", Target: filepath.Join(base, "p1.sock")}
	if got := dialThrough(same, real, real); got != same {
		t.Errorf("no link: got %q", got.Target)
	}
}
