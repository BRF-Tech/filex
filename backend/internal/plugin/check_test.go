package plugin_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/brf-tech/filex/backend/internal/plugin"
)

// CheckBinary (check.go) is the run an app store's plugin validator asks of a
// storage plugin build (#215): filex's own start, describe and conformance
// probes, outside any Manager, answered whatever happens.

// buildTestdata builds one of testdata/'s programs.
func buildTestdata(t *testing.T, name string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out := filepath.Join(t.TempDir(), name)
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "./testdata/"+name)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building testdata/%s failed:\n%s", name, b)
	}
	return out
}

func TestCheckBinary_TheExamplePluginProvesItsClaims(t *testing.T) {
	bin := buildExamplePlugin(t)
	var tuned atomic.Int32
	res := plugin.CheckBinary(context.Background(), plugin.CheckOptions{
		Binary: bin, WorkDir: t.TempDir(), Name: "memfs",
		Tune: func(*exec.Cmd) { tuned.Add(1) },
	})
	if !res.OK {
		t.Fatalf("the example plugin did not pass: %s %s", res.Code, res.Message)
	}
	if res.Describe == nil || res.Describe.Name != "memfs" || res.Describe.Version != "1.0.0" || !res.Describe.Capabilities.Write {
		t.Fatalf("describe %+v", res.Describe)
	}
	if res.Conformance == nil || !res.Conformance.Verified || res.Conformance.Scratch != "selftest" || len(res.Conformance.Results) == 0 {
		t.Fatalf("conformance %+v", res.Conformance)
	}
	if tuned.Load() != 1 {
		t.Fatalf("Tune ran %d times; it adjusts the one start", tuned.Load())
	}
}

func TestCheckBinary_APluginWithoutASelftestProvesNothing(t *testing.T) {
	bin := buildTestdata(t, "noselftest")
	res := plugin.CheckBinary(context.Background(), plugin.CheckOptions{Binary: bin, WorkDir: t.TempDir()})
	if res.OK || res.Code != plugin.CheckNoSelfTest {
		t.Fatalf("want %s, got ok=%v %s %s", plugin.CheckNoSelfTest, res.OK, res.Code, res.Message)
	}
	if res.Describe == nil || res.Describe.Name != "noselftest" {
		t.Fatalf("what it said of itself is kept: %+v", res.Describe)
	}
	if res.Conformance != nil {
		t.Fatalf("nothing was probed, yet a report: %+v", res.Conformance)
	}
}

func TestCheckBinary_AProgramThatIsNoPluginIsAnswered(t *testing.T) {
	bin := buildTestdata(t, "notaplugin")
	res := plugin.CheckBinary(context.Background(), plugin.CheckOptions{Binary: bin, WorkDir: t.TempDir()})
	if res.OK || res.Code != plugin.CheckHandshake {
		t.Fatalf("want %s, got ok=%v %s %s", plugin.CheckHandshake, res.OK, res.Code, res.Message)
	}
	if !strings.Contains(res.Message, "handshake") {
		t.Fatalf("message %q", res.Message)
	}
}

func TestCheckBinary_AFileThatDoesNotRunIsAnswered(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "garbage")
	if err := os.WriteFile(bin, []byte("not a program"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := plugin.CheckBinary(context.Background(), plugin.CheckOptions{Binary: bin, WorkDir: t.TempDir()})
	if res.OK || (res.Code != plugin.CheckStartFailed && res.Code != plugin.CheckHandshake) {
		t.Fatalf("got ok=%v %s %s", res.OK, res.Code, res.Message)
	}
}
