package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// `filex plugin-validator` (#215): the run an app store asks of a storage
// plugin build, over a spool of files - the job in, the result out, the
// heartbeat beside them.

func buildMemfsForValidator(t *testing.T) []byte {
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
		t.Fatalf("building the example plugin: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// pvTestUID is the user a plugin under test runs as when the tests run as
// root: one nothing else on a test machine uses (the sweep kills every
// process of it), inside the 0-65535 a user namespace usually maps.
const pvTestUID = 65123

// validatorOptions is a one-pass validator over spool, its runs under a
// directory of the test's own. Run as root (the store's container), a plugin
// under test runs as pvTestUID; otherwise as the test's own user, which only
// pvServe accepts (pvCheckOptions refuses it on Linux) - the tests of the
// spool use it so they run unprivileged too.
func validatorOptions(t *testing.T, spool string) pvOptions {
	t.Helper()
	o := pvOptions{spool: spool, workRoot: pvTestWorkRoot(t), pluginUID: -1, pluginGID: -1, storeUID: -1,
		maxBinary: 512 << 20, jobTime: 3 * time.Minute, once: true, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if os.Geteuid() == 0 && pvCredentialsSupported {
		o.pluginUID, o.pluginGID = pvTestUID, pvTestUID
	}
	if !pvCredentialsSupported {
		o.insecureDev = true
	}
	return o
}

// pvTestWorkRoot is a --work-dir under a directory the plugin's user can pass
// through (t.TempDir's own is 0700, which a plugin of another user could not
// enter).
func pvTestWorkRoot(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "fxpv-test-")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(d, 0o711)
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return filepath.Join(d, "work")
}

func leaveJob(t *testing.T, spool, id string, build []byte, sha string) {
	t.Helper()
	leaveJobNamed(t, spool, id, build, sha, "memfs")
}

func leaveJobNamed(t *testing.T, spool, id string, build []byte, sha, name string) {
	t.Helper()
	in := filepath.Join(spool, "in")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in, id+".bin"), build, 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(pvJob{Protocol: pvProtocol, ID: id, SHA256: sha, Name: name, Version: "1.0.0", CreatedAt: time.Now().UTC()})
	if err := os.WriteFile(filepath.Join(in, id+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readResult(t *testing.T, spool, id string) pvResultWire {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(spool, "out", id+".json"))
	if err != nil {
		t.Fatalf("no result for the job: %v", err)
	}
	var r pvResultWire
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// pvResultWire is out/<id>.json as the store reads it.
type pvResultWire struct {
	Protocol int    `json:"protocol"`
	ID       string `json:"id"`
	SHA256   string `json:"sha256"`
	Platform string `json:"platform"`
	OK       bool   `json:"ok"`
	Code     string `json:"code"`
	Describe *struct {
		Name         string          `json:"name"`
		Version      string          `json:"version"`
		Capabilities map[string]bool `json:"capabilities"`
	} `json:"describe"`
	Conformance *struct {
		Verified bool `json:"verified"`
		Results  []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"results"`
	} `json:"conformance"`
}

const jobA = "0123456789abcdef0123456789abcdef"

func TestPluginValidator_RunsAJobAndAnswersIt(t *testing.T) {
	build := buildMemfsForValidator(t)
	spool := t.TempDir()
	leaveJob(t, spool, jobA, build, sumOf(build))
	if err := pvServe(context.Background(), validatorOptions(t, spool)); err != nil {
		t.Fatal(err)
	}
	r := readResult(t, spool, jobA)
	if r.Protocol != pvProtocol || r.ID != jobA || r.SHA256 != sumOf(build) || r.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("result %+v", r)
	}
	if !r.OK || r.Describe == nil || r.Describe.Name != "memfs" || r.Describe.Version != "1.0.0" || !r.Describe.Capabilities["write"] {
		t.Fatalf("the example plugin did not pass: %+v", r)
	}
	if r.Conformance == nil || !r.Conformance.Verified || len(r.Conformance.Results) == 0 {
		t.Fatalf("conformance %+v", r.Conformance)
	}
	var st pvStatus
	b, err := os.ReadFile(filepath.Join(spool, "status.json"))
	if err != nil || json.Unmarshal(b, &st) != nil || st.Protocol != pvProtocol || st.Platform == "" || st.FilexVersion == "" {
		t.Fatalf("heartbeat %s (%v)", b, err)
	}
}

func TestPluginValidator_ABuildThatIsNotThePinnedOneIsNeverRun(t *testing.T) {
	spool := t.TempDir()
	build := []byte("#!/bin/sh\necho FILEX-PLUGIN/1 tcp:127.0.0.1:1\n")
	leaveJob(t, spool, jobA, build, strings.Repeat("0", 64))
	if err := pvServe(context.Background(), validatorOptions(t, spool)); err != nil {
		t.Fatal(err)
	}
	r := readResult(t, spool, jobA)
	if r.OK || r.Code != "sha256_mismatch" || r.SHA256 != sumOf(build) || r.Describe != nil {
		t.Fatalf("want sha256_mismatch with nothing run, got %+v", r)
	}
}

func TestPluginValidator_AJobThatIsNotOneIsSkipped(t *testing.T) {
	spool := t.TempDir()
	in := filepath.Join(spool, "in")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	// Not a job id the spool uses, and a job file without its build.
	_ = os.WriteFile(filepath.Join(in, "not-an-id.json"), []byte("{}"), 0o644)
	_ = os.WriteFile(filepath.Join(in, jobA+".json"), []byte(`{"protocol":1,"id":"`+jobA+`","sha256":"`+strings.Repeat("a", 64)+`"}`), 0o644)
	if err := pvServe(context.Background(), validatorOptions(t, spool)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(spool, "out", "not-an-id.json")); !os.IsNotExist(err) {
		t.Fatal("a file that is no job id was answered")
	}
	if r := readResult(t, spool, jobA); r.OK || r.Code != "job_invalid" {
		t.Fatalf("a job without its build: %+v", r)
	}
}

// ---- The test binary as the plugin under test ----
//
// A job named pvTestProbeName (or pvTestLingerName) hands the validator this
// test binary as the build: started by the validator, it finds its name in
// FILEX_PLUGIN_NAME (TestMain), says on stdout what it could see and do from
// inside its run, and exits without a handshake. The validator logs that line;
// the test reads it from the log.

const (
	pvTestProbeName      = "pv-probe"
	pvTestLingerName     = "pv-linger"
	pvTestLingerChildArg = "pv-linger-child"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == pvTestLingerChildArg {
		// The helper a lingering plugin leaves behind: it outlives its
		// parent, in a session of its own, until somebody kills it.
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	}
	if name := os.Getenv("FILEX_PLUGIN_NAME"); name == pvTestProbeName || name == pvTestLingerName {
		pvTestPluginMain(name)
		return
	}
	os.Exit(m.Run())
}

// pvTestPluginMain is the plugin under test: one line, then exit.
func pvTestPluginMain(name string) {
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()
	runDir := os.Getenv("FILEX_PLUGIN_SOCKET_DIR")
	mode := func(p string) string {
		fi, err := os.Stat(p)
		if err != nil {
			return "missing"
		}
		return fmt.Sprintf("%04o", fi.Mode().Perm())
	}
	could := func(err error) string {
		if err != nil {
			return "denied"
		}
		return "ok"
	}
	_, listErr := os.ReadDir(filepath.Dir(cwd))
	parts := []string{"pvprobe",
		"bin=" + mode(exe), "work=" + mode(cwd), "root=" + mode(filepath.Dir(cwd)), "run=" + mode(runDir),
		"write-work=" + could(os.WriteFile(filepath.Join(cwd, "planted"), []byte("x"), 0o600)),
		"chmod-bin=" + could(os.Chmod(exe, 0o755)),
		"list-root=" + could(listErr),
		"write-run=" + could(os.WriteFile(filepath.Join(runDir, "scratch"), []byte("x"), 0o600)),
	}
	if name == pvTestLingerName {
		parts = append(parts, "linger="+could(pvTestStartLingerer(exe)))
	}
	fmt.Println(strings.Join(parts, " "))
	os.Exit(3)
}

// pvTestBuild is this test binary, as the bytes of a build.
func pvTestBuild(t *testing.T) []byte {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Skip("the test binary cannot be found: ", err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pvSyncBuffer is a log the plugin's output relays write into while the
// test reads it.
type pvSyncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *pvSyncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *pvSyncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

var pvProbeLineRe = regexp.MustCompile(`pvprobe[^"\n]*`)

// pvRunProbe runs one job of the test binary named name with o, and answers
// the line it printed as the plugin under test.
func pvRunProbe(t *testing.T, o pvOptions, name string, run func(context.Context, pvOptions) error) string {
	t.Helper()
	build := pvTestBuild(t)
	leaveJobNamed(t, o.spool, jobA, build, sumOf(build), name)
	log := &pvSyncBuffer{}
	o.log = slog.New(slog.NewTextHandler(log, nil))
	if err := run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if r := readResult(t, o.spool, jobA); r.OK || r.SHA256 != sumOf(build) {
		t.Fatalf("the probe never answers a handshake, so its run cannot pass: %+v", r)
	}
	line := pvProbeLineRe.FindString(log.String())
	if line == "" {
		t.Fatalf("the plugin under test said nothing:\n%s", log.String())
	}
	return line
}

func pvProbeSays(t *testing.T, line string, want ...string) {
	t.Helper()
	have := map[string]bool{}
	for _, f := range strings.Fields(line) {
		have[f] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("the plugin under test reports %q, want %s", line, w)
		}
	}
}

// The run's directory and the build are the validator's, read-only to the
// plugin, under a parent nobody else can list; the plugin's only directory of
// its own is its socket directory. (Who OWNS them is what a validator running
// as root changes: TestPluginValidator_ThePluginCannotChangeItsRunOrListTheOthers.)
func TestPluginValidator_ThePluginIsGivenOnlyItsSocketDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are a POSIX matter")
	}
	line := pvRunProbe(t, validatorOptions(t, t.TempDir()), pvTestProbeName, pvServe)
	pvProbeSays(t, line, "bin=0555", "work=0711", "root=0711", "run=0700", "write-run=ok")
}
