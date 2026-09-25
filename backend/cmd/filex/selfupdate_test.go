package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/update"
	"github.com/brf-tech/filex/backend/internal/version"
)

// selfUpdateLab points `filex self-update` at a manifest served here and at
// temporary folders only: HOME, the data dir and the config are the test's,
// never the machine's ~/.filex. The one release offers an asset whose digest
// is wrong, so even a mistaken attempt stops before anything on disk; the
// counter says whether one was made.
func selfUpdateLab(t *testing.T, installMode string) *atomic.Int32 {
	t.Helper()
	var downloads atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stable.json":
			_ = json.NewEncoder(w).Encode(update.Manifest{Releases: []update.Release{{
				Version: "v0.7.6", AutoOK: true,
				Assets: []update.Asset{{
					OS: runtime.GOOS, Arch: runtime.GOARCH,
					URL:    srv.URL + "/filex.tar.gz",
					SHA256: strings.Repeat("0", 64),
				}},
			}}})
		case "/filex.tar.gz":
			downloads.Add(1)
			_, _ = w.Write([]byte("not an archive"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("FILEX_CONFIG", "")
	t.Setenv("FILEX_DATA_DIR", t.TempDir())
	t.Setenv("FILEX_UPDATE_POLICY", "patch")
	t.Setenv("FILEX_UPDATE_MANIFEST_URL", srv.URL+"/stable.json")
	t.Setenv("FILEX_INSTALL_MODE", installMode)
	oldPath, oldVersion := configPath, version.Version
	configPath, version.Version = "", "v0.7.5"
	t.Cleanup(func() { configPath, version.Version = oldPath, oldVersion })
	return &downloads
}

// runSelfUpdate runs the command and returns what it printed on stdout (the
// report goes there through fmt.Printf). What cobra itself would print (an
// "Error:" line, the usage) is checked here: a refusal is an answer, not a
// misuse, and main prints the error once.
func runSelfUpdate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	old := os.Stdout
	os.Stdout = w
	out := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		out <- string(b)
	}()
	var cobraOut bytes.Buffer
	cmd := selfUpdateCmd()
	cmd.SetArgs(args)
	cmd.SetOut(&cobraOut)
	cmd.SetErr(&cobraOut)
	runErr := cmd.Execute()
	_ = w.Close()
	os.Stdout = old
	assert.NotContains(t, cobraOut.String(), "Usage:", "no flag list under a runtime answer")
	assert.NotContains(t, cobraOut.String(), "Error:", "main prints the error, once")
	return <-out, runErr
}

// On an install a package manager owns, `filex self-update` refuses and
// names the manager's command — with --force and with --to as well: those
// override the policy and the target, not who owns the file.
func TestSelfUpdate_PackageInstallRefusesWithTheCommand(t *testing.T) {
	for _, tc := range []struct {
		mode, command string
	}{
		{"homebrew", "brew upgrade --cask filex"},
		{"winget", "winget upgrade BRFTech.filex"},
		{"snap", "snap refresh filex"},
	} {
		for _, args := range [][]string{nil, {"--force"}, {"--to", "v0.7.6"}} {
			t.Run(tc.mode+" "+strings.Join(args, " "), func(t *testing.T) {
				downloads := selfUpdateLab(t, tc.mode)
				out, err := runSelfUpdate(t, args...)

				require.Error(t, err)
				assert.ErrorIs(t, err, update.ErrNotSelfApplicable)
				assert.Contains(t, err.Error(), tc.command, "the error main prints names the command")
				assert.Contains(t, out, "latest:  v0.7.6", "the check itself still ran")
				assert.Contains(t, out, "install: package (")
				assert.Contains(t, out, tc.command)
				assert.NotContains(t, out, "installing", "no install was started")
				assert.Zero(t, downloads.Load(), "nothing was downloaded")
			})
		}
	}
}

// --check reports the release and, on a packaged install, the command that
// takes it.
func TestSelfUpdate_CheckOnAPackageInstallSaysHowToUpgrade(t *testing.T) {
	downloads := selfUpdateLab(t, "homebrew")
	out, err := runSelfUpdate(t, "--check")
	require.NoError(t, err)
	assert.Contains(t, out, "latest:  v0.7.6")
	assert.Contains(t, out, "install: package (Homebrew: brew upgrade --cask filex)")
	assert.Contains(t, out, "upgrade: brew upgrade --cask filex")
	assert.Zero(t, downloads.Load())
}

func TestRefuseSelfUpdate(t *testing.T) {
	d := update.Decision{Target: update.Release{Version: "v0.7.6"}}

	var buf bytes.Buffer
	assert.NoError(t, refuseSelfUpdate(&buf, update.Install{Mode: update.ModeBinary}, d, ""))
	assert.Empty(t, buf.String(), "a binary install is not refused")

	buf.Reset()
	err := refuseSelfUpdate(&buf, update.Install{Mode: update.ModeDocker}, d, "")
	require.Error(t, err)
	assert.Contains(t, buf.String(), "docker compose pull filex", "the container keeps its compose steps")
	assert.Contains(t, buf.String(), "ghcr.io/brf-tech/filex:v0.7.6")

	buf.Reset()
	err = refuseSelfUpdate(&buf, update.Install{Mode: update.ModePackage, Manager: update.ManagerHomebrew, Package: "filex"}, d, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, update.ErrNotSelfApplicable)
	assert.Contains(t, err.Error(), "brew upgrade filex", "a formula is not a cask")
	assert.NotContains(t, buf.String(), "docker", "no compose steps for a package")

	err = refuseSelfUpdate(&buf, update.Install{Mode: update.ModePackage}, d, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "package manager")
}

func TestInstallLine(t *testing.T) {
	assert.Equal(t, "install: binary", installLine(update.Install{Mode: update.ModeBinary}))
	assert.Equal(t, "install: docker", installLine(update.Install{Mode: update.ModeDocker}))
	assert.Equal(t, "install: package (winget: winget upgrade BRFTech.filex)",
		installLine(update.Install{Mode: update.ModePackage, Manager: update.ManagerWinget, Package: "BRFTech.filex"}))
	assert.Equal(t, "install: package (a package manager)", installLine(update.Install{Mode: update.ModePackage}))
}
