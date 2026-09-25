package update

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// env is a fake environment: only what the case sets exists.
func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func noContainer() bool { return false }
func isContainer() bool { return true }
func brewCask(name string) Install {
	return Install{Mode: ModePackage, Manager: ManagerHomebrew, Package: name, Cask: true}
}
func brewFormula(name string) Install {
	return Install{Mode: ModePackage, Manager: ManagerHomebrew, Package: name}
}
func winget(id string) Install {
	return Install{Mode: ModePackage, Manager: ManagerWinget, Package: id}
}
func snap(name string) Install {
	return Install{Mode: ModePackage, Manager: ManagerSnap, Package: name}
}

var binary = Install{Mode: ModeBinary}

// A distribution's package manager owns the binary; which one is not known.
var distro = Install{Mode: ModePackage}

// The whole detection as a table: every path is judged the way the named
// operating system would see it, on whichever one runs the test. The binary
// rows are the point as much as the package rows — a false positive turns
// `filex self-update` off on an install that could have used it.
func TestDetectInstall_Layouts(t *testing.T) {
	const wingetDir = `BRFTech.filex_Microsoft.Winget.Source_8wekyb3d8bbwe`
	for _, tc := range []struct {
		name string
		goos string
		exe  string
		env  map[string]string
		want Install
	}{
		// ── Homebrew (macOS, and Linux under linuxbrew) ──
		{"cask, Apple silicon prefix", "darwin", "/opt/homebrew/Caskroom/filex/0.44.0/filex", nil, brewCask("filex")},
		{"cask, Intel prefix, archive with a folder", "darwin", "/usr/local/Caskroom/filex/0.44.0/filex_0.44.0_darwin_amd64/filex", nil, brewCask("filex")},
		{"cask, the token is read from the path", "darwin", "/opt/homebrew/Caskroom/filex-nightly/0.45.0/filex", nil, brewCask("filex-nightly")},
		{"formula, keg bin", "darwin", "/usr/local/Cellar/filex/0.44.0/bin/filex", nil, brewFormula("filex")},
		{"formula, keg libexec", "darwin", "/opt/homebrew/Cellar/filex/0.44.0/libexec/filex", nil, brewFormula("filex")},
		{"formula under linuxbrew", "linux", "/home/linuxbrew/.linuxbrew/Cellar/filex/0.44.0/bin/filex", nil, brewFormula("filex")},
		{"cask under linuxbrew", "linux", "/home/linuxbrew/.linuxbrew/Caskroom/filex/0.44.0/filex", nil, brewCask("filex")},

		{"not Homebrew: a folder NAMED like the Cellar", "darwin", "/Users/me/Cellar-backup/filex", nil, binary},
		{"not Homebrew: a Cellar-backup that copies the keg shape", "darwin", "/Users/me/Cellar-backup/filex/0.44.0/bin/filex", nil, binary},
		{"not Homebrew: Caskroom-old", "darwin", "/Users/me/Caskroom-old/filex/0.44.0/filex", nil, binary},
		{"not Homebrew: a Cellar folder with no version and no keg dir", "darwin", "/Users/me/backup/Cellar/filex", nil, binary},
		{"not Homebrew: a keg root holds no executable", "darwin", "/usr/local/Cellar/filex/0.44.0/filex", nil, binary},
		{"not Homebrew: a Caskroom token with no version", "darwin", "/opt/homebrew/Caskroom/filex/filex", nil, binary},
		{"not Homebrew: Homebrew never writes cellar in lower case", "darwin", "/Users/me/cellar/filex/0.44.0/bin/filex", nil, binary},
		{"not Homebrew: the link, when it was not resolved", "darwin", "/opt/homebrew/bin/filex", nil, binary},
		{"not Homebrew: a plain install", "linux", "/usr/local/bin/filex", nil, binary},
		{"not Homebrew on Windows: the rules are per OS", "windows", `C:\opt\homebrew\Caskroom\filex\0.44.0\filex.exe`, nil, binary},

		// ── a distribution package (.deb, .rpm, AUR …): the system's own bin ──
		{"distro package, /usr/bin", "linux", "/usr/bin/filex", nil, distro},
		{"distro package, /usr/sbin", "linux", "/usr/sbin/filex", nil, distro},
		{"distro package, merged /bin", "linux", "/bin/filex", nil, distro},
		{"not a distro package: /usr/local/bin is where docs/CLI.md puts it", "linux", "/usr/local/bin/filex", nil, binary},
		{"not a distro package: a folder under /usr/bin", "linux", "/usr/bin/tools/filex", nil, binary},
		{"not a distro package: /usr/binaries only starts the same", "linux", "/usr/binaries/filex", nil, binary},
		{"not a distro package on macOS: the rule is Linux's", "darwin", "/usr/bin/filex", nil, binary},

		// ── winget (portable package) ──
		{"winget, per user", "windows", `C:\Users\me\AppData\Local\Microsoft\WinGet\Packages\` + wingetDir + `\filex.exe`, nil, winget("BRFTech.filex")},
		{"winget, machine scope", "windows", `C:\Program Files\WinGet\Packages\` + wingetDir + `\filex.exe`, nil, winget("BRFTech.filex")},
		{"winget, archive with a folder", "windows", `C:\Users\me\AppData\Local\Microsoft\WinGet\Packages\` + wingetDir + `\filex_0.44.0_windows_amd64\filex.exe`, nil, winget("BRFTech.filex")},
		{"winget, paths compare without case", "windows", `c:\users\me\appdata\local\microsoft\winget\packages\brftech.filex_microsoft.winget.source_8wekyb3d8bbwe\filex.exe`, nil, winget("brftech.filex")},
		{"winget, forward slashes", "windows", `C:/Users/me/AppData/Local/Microsoft/WinGet/Packages/` + wingetDir + `/filex.exe`, nil, winget("BRFTech.filex")},
		{"winget, another source", "windows", `C:\Users\me\AppData\Local\Microsoft\WinGet\Packages\Acme.filex_internal\filex.exe`, nil, winget("Acme.filex")},
		{"winget, the alias when it was not resolved", "windows", `C:\Users\me\AppData\Local\Microsoft\WinGet\Links\filex.exe`, nil, winget(DefaultWingetID)},

		{"not winget: a file directly in a Packages folder", "windows", `C:\Users\me\Downloads\WinGet\Packages\filex.exe`, nil, binary},
		{"not winget: a folder without winget's <id>_<source> name", "windows", `C:\Users\me\WinGet\Packages\filex\filex.exe`, nil, binary},
		{"not winget: WinGet-Packages is one name", "windows", `C:\tools\WinGet-Packages\a_b\filex.exe`, nil, binary},
		{"not winget: something deeper under Links", "windows", `C:\Users\me\AppData\Local\Microsoft\WinGet\Links\tools\filex.exe`, nil, binary},
		{"not winget: a plain install", "windows", `C:\filex\filex.exe`, nil, binary},
		{"not winget on Linux: a backslash is a file-name character there", "linux", `/home/me/WinGet\Packages\` + wingetDir + `\filex`, nil, binary},

		// ── Snap ──
		{"snap", "linux", "/snap/filex/123/bin/filex",
			map[string]string{"SNAP": "/snap/filex/123", "SNAP_NAME": "filex"}, snap("filex")},
		{"snap, a parallel install is refreshed by its instance name", "linux", "/snap/filex_beta/7/bin/filex",
			map[string]string{"SNAP": "/snap/filex_beta/7", "SNAP_NAME": "filex", "SNAP_INSTANCE_NAME": "filex_beta"}, snap("filex_beta")},
		{"not snap: SNAP inherited from another snap's terminal", "linux", "/usr/local/bin/filex",
			map[string]string{"SNAP": "/snap/code/180", "SNAP_NAME": "code"}, binary},
		{"not snap: a revision whose name merely starts the same", "linux", "/snap/filex/1234/bin/filex",
			map[string]string{"SNAP": "/snap/filex/123", "SNAP_NAME": "filex"}, binary},
		{"not snap: SNAP without SNAP_NAME", "linux", "/snap/filex/123/bin/filex",
			map[string]string{"SNAP": "/snap/filex/123"}, binary},
		{"not snap: SNAP=/ would contain everything", "linux", "/usr/local/bin/filex",
			map[string]string{"SNAP": "/", "SNAP_NAME": "filex"}, binary},
		{"not snap on macOS", "darwin", "/snap/filex/123/bin/filex",
			map[string]string{"SNAP": "/snap/filex/123", "SNAP_NAME": "filex"}, binary},

		// ── nothing to go on ──
		{"no executable path", "linux", "", nil, binary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := detectInstall(env(tc.env), tc.exe, tc.goos, noContainer)
			assert.Equal(t, tc.want, got, tc.exe)
		})
	}
}

// FILEX_INSTALL_MODE is the operator's word and beats every heuristic — in
// both directions: `binary` on a Homebrew path is honored (they know what
// they are doing), and `package` marks a distribution package (.deb, .rpm,
// AUR) whose layout filex cannot see.
func TestDetectInstall_OverrideWins(t *testing.T) {
	const cask = "/opt/homebrew/Caskroom/filex/0.44.0/filex"
	for _, tc := range []struct {
		name      string
		override  string
		goos, exe string
		container func() bool
		want      Install
	}{
		{"binary on a Homebrew path", "binary", "darwin", cask, noContainer, binary},
		{"docker on a Homebrew path", "docker", "darwin", cask, noContainer, Install{Mode: ModeDocker}},
		{"container is docker", "container", "linux", "/usr/bin/filex", noContainer, Install{Mode: ModeDocker}},
		{"package keeps what was detected", "package", "darwin", cask, noContainer, brewCask("filex")},
		{"package with nothing detected: an unnamed manager", "package", "linux", "/usr/bin/filex", noContainer, Install{Mode: ModePackage}},
		{"package beats the container check", "package", "linux", "/usr/bin/filex", isContainer, Install{Mode: ModePackage}},
		{"packaged is package", " Packaged ", "linux", "/usr/bin/filex", noContainer, Install{Mode: ModePackage}},
		{"homebrew with nothing detected: filex as published", "homebrew", "darwin", "/usr/local/bin/filex", noContainer, brewCask(DefaultHomebrewCask)},
		{"brew keeps a detected formula", "BREW", "darwin", "/usr/local/Cellar/filex/0.44.0/bin/filex", noContainer, brewFormula("filex")},
		{"winget: filex as published", "winget", "windows", `C:\filex\filex.exe`, noContainer, winget(DefaultWingetID)},
		{"snap: filex as published", "snap", "linux", "/usr/bin/filex", noContainer, snap(DefaultSnapName)},
		{"a manager other than the detected one is the override's", "snap", "darwin", cask, noContainer, snap(DefaultSnapName)},
		{"an unknown value is ignored, detection decides", "rpm", "darwin", cask, noContainer, brewCask("filex")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := detectInstall(env(map[string]string{"FILEX_INSTALL_MODE": tc.override}), tc.exe, tc.goos, tc.container)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Inside a container the image is what gets upgraded, whatever layout the
// binary sits in there.
func TestDetectInstall_ContainerBeforePackageLayout(t *testing.T) {
	got := detectInstall(env(nil), "/home/linuxbrew/.linuxbrew/Cellar/filex/0.44.0/bin/filex", "linux", isContainer)
	assert.Equal(t, Install{Mode: ModeDocker}, got)
	got = detectInstall(env(nil), "/usr/local/bin/filex", "linux", isContainer)
	assert.Equal(t, Install{Mode: ModeDocker}, got)
}

// The name on PATH is a link; the layout is judged at the file it points
// to. A real link in a temporary folder, never a system path.
func TestDetectInstall_FollowsTheLinkOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link needs a privilege on Windows; the path rules are covered by the table")
	}
	prefix := t.TempDir()
	keg := filepath.Join(prefix, "Caskroom", "filex", "0.44.0")
	require.NoError(t, os.MkdirAll(keg, 0o755))
	target := filepath.Join(keg, "filex")
	require.NoError(t, os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(prefix, "bin"), 0o755))
	link := filepath.Join(prefix, "bin", "filex")
	require.NoError(t, os.Symlink("../Caskroom/filex/0.44.0/filex", link))

	// Premise: the link itself says nothing — only following it does.
	require.Equal(t, binary, detectInstall(env(nil), link, "darwin", noContainer))

	assert.Equal(t, brewCask("filex"), detectInstall(env(nil), resolveExe(link), "darwin", noContainer))
	assert.Equal(t, "", resolveExe(""))
	missing := filepath.Join(prefix, "nope", "filex")
	assert.Equal(t, missing, resolveExe(missing), "an unresolvable path is judged as it is")
}

func TestInstall_UpgradeCommand(t *testing.T) {
	for _, tc := range []struct {
		in   Install
		want string
	}{
		{brewCask("filex"), "brew upgrade --cask filex"},
		{brewFormula("filex"), "brew upgrade filex"},
		{winget("BRFTech.filex"), "winget upgrade BRFTech.filex"},
		{snap("filex"), "snap refresh filex"},
		{snap("filex_beta"), "snap refresh filex_beta"},
		{Install{Mode: ModePackage}, ""},
		{binary, ""},
		{Install{Mode: ModeDocker}, ""},
		// A manager on a non-package mode says nothing: only the mode decides.
		{Install{Mode: ModeBinary, Manager: ManagerSnap, Package: "filex"}, ""},
	} {
		assert.Equal(t, tc.want, tc.in.UpgradeCommand(), "%+v", tc.in)
	}
}

func TestInstall_Refusal(t *testing.T) {
	assert.NoError(t, binary.Refusal(), "a plain binary replaces itself")
	assert.Same(t, ErrNotSelfApplicable, Install{Mode: ModeDocker}.Refusal())

	err := brewCask("filex").Refusal()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotSelfApplicable, "a caller asking only 'can it replace itself?' needs no second check")
	var pm *PackageManagedError
	require.True(t, errors.As(err, &pm))
	assert.Contains(t, err.Error(), "Homebrew")
	assert.Contains(t, err.Error(), "brew upgrade --cask filex")
	assert.NotContains(t, err.Error(), "container", "the container sentence is not this install's")

	assert.Contains(t, winget("BRFTech.filex").Refusal().Error(), "winget upgrade BRFTech.filex")
	assert.Contains(t, snap("filex").Refusal().Error(), "snap refresh filex")

	unnamed := Install{Mode: ModePackage}.Refusal()
	assert.ErrorIs(t, unnamed, ErrNotSelfApplicable)
	assert.Contains(t, unnamed.Error(), "package manager")
}

func TestPackageManager_Label(t *testing.T) {
	assert.Equal(t, "Homebrew", ManagerHomebrew.Label())
	assert.Equal(t, "winget", ManagerWinget.Label())
	assert.Equal(t, "Snap", ManagerSnap.Label())
	assert.Equal(t, "", PackageManager("").Label())
}
