package update

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// PackageManager names the package manager that owns a packaged install.
type PackageManager string

const (
	// ManagerHomebrew — a cask (Caskroom) or a formula (Cellar), macOS and Linux.
	ManagerHomebrew PackageManager = "homebrew"
	// ManagerWinget — a winget "portable" package, unpacked under
	// …\WinGet\Packages\<id>_<source>\ and linked from …\WinGet\Links.
	ManagerWinget PackageManager = "winget"
	// ManagerSnap — a snap: a read-only squashfs mounted at $SNAP.
	ManagerSnap PackageManager = "snap"
)

// Label is the manager's product name as a person writes it. A proper noun:
// the same in every language, so it is not a catalogue entry.
func (m PackageManager) Label() string {
	switch m {
	case ManagerHomebrew:
		return "Homebrew"
	case ManagerWinget:
		return "winget"
	case ManagerSnap:
		return "Snap"
	}
	return ""
}

// The names filex is published under. They are used when the operator names
// only the manager (FILEX_INSTALL_MODE=homebrew) and the path does not say
// more; a detected install reads its own name from where it lives, so a fork
// or a renamed package is told the command for ITS package.
const (
	DefaultHomebrewCask = "filex"
	DefaultWingetID     = "BRFTech.filex"
	DefaultSnapName     = "filex"
)

// Install is how this filex was installed: the mode decides whether it may
// replace its own binary, the rest says who does it instead.
type Install struct {
	Mode InstallMode
	// Manager owns the binary (ModePackage only). Empty when
	// FILEX_INSTALL_MODE=package was set and nothing could be detected — a
	// distribution package (.deb, .rpm, AUR) whose unit file says so.
	Manager PackageManager
	// Package is the name the manager knows filex by: the cask token or
	// formula name, the winget package id, the snap instance name.
	Package string
	// Cask is true for a Homebrew cask, false for a formula. The two are
	// upgraded by different commands.
	Cask bool
}

// UpgradeCommand is the command that upgrades a packaged install, or "" when
// the manager is unknown (or the install is not packaged).
func (i Install) UpgradeCommand() string {
	if i.Mode != ModePackage {
		return ""
	}
	switch i.Manager {
	case ManagerHomebrew:
		if i.Cask {
			return "brew upgrade --cask " + i.Package
		}
		return "brew upgrade " + i.Package
	case ManagerWinget:
		return "winget upgrade " + i.Package
	case ManagerSnap:
		return "snap refresh " + i.Package
	}
	return ""
}

// Refusal is the error a self-upgrade of this install answers with, nil when
// the install can replace itself.
func (i Install) Refusal() error {
	switch i.Mode {
	case ModeBinary:
		return nil
	case ModePackage:
		return &PackageManagedError{Install: i}
	}
	return ErrNotSelfApplicable
}

// PackageManagedError refuses a self-upgrade of a binary a package manager
// owns. It is also ErrNotSelfApplicable (errors.Is), so a caller that only
// asks "can this install replace itself?" needs no second check.
type PackageManagedError struct{ Install Install }

func (e *PackageManagedError) Error() string {
	name := e.Install.Manager.Label()
	cmd := e.Install.UpgradeCommand()
	if name == "" || cmd == "" {
		return "this filex was installed by a package manager, which owns its binary — upgrade it with that package manager"
	}
	return "this filex was installed with " + name + ", which owns its binary — upgrade it with: " + cmd
}

// Is makes errors.Is(err, ErrNotSelfApplicable) hold.
func (e *PackageManagedError) Is(target error) bool { return target == ErrNotSelfApplicable }

// DetectInstall inspects the runtime: FILEX_INSTALL_MODE first, then a
// container, then a package manager's layout around the running binary, and
// otherwise a plain binary.
func DetectInstall() Install {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	return detectInstall(os.Getenv, resolveExe(exe), runtime.GOOS, inContainer)
}

// resolveExe follows links to the file itself. The name on PATH is usually a
// link (/opt/homebrew/bin/filex → ../Caskroom/…, WinGet\Links\filex.exe →
// WinGet\Packages\…), and the package manager's layout is around the file it
// points at. A path that cannot be resolved is judged as it is.
func resolveExe(exe string) string {
	if exe == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		return real
	}
	return exe
}

// detectInstall is DetectInstall with every input passed in, so the whole
// decision is a pure function: tables of paths for three operating systems
// run on any of them.
func detectInstall(getenv func(string) string, exe, goos string, container func() bool) Install {
	pkg, packaged := packageOf(getenv, exe, goos)
	switch v := strings.ToLower(strings.TrimSpace(getenv("FILEX_INSTALL_MODE"))); v {
	case "binary":
		return Install{Mode: ModeBinary}
	case "docker", "container":
		return Install{Mode: ModeDocker}
	case "package", "packaged":
		if packaged {
			return pkg
		}
		return Install{Mode: ModePackage}
	case "homebrew", "brew", "winget", "snap":
		m := managerNamed(v)
		if packaged && pkg.Manager == m {
			return pkg
		}
		return publishedAs(m)
	}
	// A container first: whatever layout the binary sits in inside an image,
	// the image is what gets replaced, and the image is what an upgrade pulls.
	if container != nil && container() {
		return Install{Mode: ModeDocker}
	}
	if packaged {
		return pkg
	}
	return Install{Mode: ModeBinary}
}

func managerNamed(v string) PackageManager {
	switch v {
	case "homebrew", "brew":
		return ManagerHomebrew
	case "winget":
		return ManagerWinget
	}
	return ManagerSnap
}

// publishedAs is a packaged install named only by its manager: filex as it
// is published there.
func publishedAs(m PackageManager) Install {
	switch m {
	case ManagerHomebrew:
		return Install{Mode: ModePackage, Manager: m, Package: DefaultHomebrewCask, Cask: true}
	case ManagerWinget:
		return Install{Mode: ModePackage, Manager: m, Package: DefaultWingetID}
	}
	return Install{Mode: ModePackage, Manager: ManagerSnap, Package: DefaultSnapName}
}

func inContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if b, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(b)
		if strings.Contains(s, "docker") || strings.Contains(s, "containerd") || strings.Contains(s, "kubepods") {
			return true
		}
	}
	return false
}

// packageOf reads a package manager's layout from the binary's (resolved)
// path. Each rule matches the WHOLE shape the manager creates, segment by
// segment, never a substring: `~/Cellar-backup/filex` or a folder that merely
// mentions WinGet is not a package.
func packageOf(getenv func(string) string, exe, goos string) (Install, bool) {
	if exe == "" {
		return Install{}, false
	}
	switch goos {
	case "windows":
		return wingetPackage(exe)
	case "linux":
		if i, ok := snapPackage(getenv, exe); ok {
			return i, true
		}
		if i, ok := homebrewPackage(exe); ok {
			return i, true
		}
		return distroPackage(exe)
	case "darwin":
		return homebrewPackage(exe)
	}
	return Install{}, false
}

// distroPackage is a binary in the system's own bin directories. On Linux
// only a package manager puts files there (a .deb, an .rpm, an AUR or distro
// package); a hand install goes to /usr/local/bin, which is where docs/CLI.md
// puts it. Before this rule such a package had to say FILEX_INSTALL_MODE=package
// in its unit file, or `filex self-update` replaced a file dpkg or rpm owns.
// Which manager it was is not known, so no upgrade command is named.
func distroPackage(exe string) (Install, bool) {
	switch filepath.ToSlash(filepath.Dir(exe)) {
	case "/usr/bin", "/usr/sbin", "/bin", "/sbin":
		return Install{Mode: ModePackage}, true
	}
	return Install{}, false
}

// segments splits a path into its non-empty parts. Windows accepts both
// separators; elsewhere a backslash is an ordinary file-name character.
func segments(p string, windows bool) []string {
	sep := func(r rune) bool { return r == '/' }
	if windows {
		sep = func(r rune) bool { return r == '/' || r == '\\' }
	}
	return strings.FieldsFunc(p, sep)
}

// homebrewPackage recognises the two places Homebrew keeps what it installs:
//
//	<prefix>/Caskroom/<token>/<version>/[…/]filex        a cask
//	<prefix>/Cellar/<formula>/<version>/<dir>/[…/]filex  a formula (bin/, libexec/)
//
// The directory names are matched exactly (Homebrew never writes them in
// another case), and the name and version segments must both be there.
func homebrewPackage(exe string) (Install, bool) {
	s := segments(exe, false)
	last := len(s) - 1
	for i := last - 1; i >= 0; i-- {
		switch s[i] {
		case "Caskroom":
			// token, version, then the file itself at the least.
			if i+3 <= last {
				return Install{Mode: ModePackage, Manager: ManagerHomebrew, Package: s[i+1], Cask: true}, true
			}
		case "Cellar":
			// formula, version, a directory inside the keg, then the file:
			// nothing executable lives at a keg's root.
			if i+4 <= last {
				return Install{Mode: ModePackage, Manager: ManagerHomebrew, Package: s[i+1]}, true
			}
		}
	}
	return Install{}, false
}

// wingetSource is the suffix winget appends to a community-repository
// package's directory: <PackageIdentifier>_<SourceIdentifier>.
const wingetSource = "_microsoft.winget.source_8wekyb3d8bbwe"

// wingetPackage recognises a winget portable package:
//
//	…\WinGet\Packages\<id>_<source>\[…\]filex.exe   (per-user: %LOCALAPPDATA%\Microsoft;
//	                                                 machine: %ProgramFiles%)
//	…\WinGet\Links\filex.exe                          (the alias, when it was not resolved)
//
// Windows paths compare without case.
func wingetPackage(exe string) (Install, bool) {
	s := segments(exe, true)
	last := len(s) - 1
	for i := 0; i+1 < last; i++ {
		if !strings.EqualFold(s[i], "WinGet") {
			continue
		}
		switch {
		case strings.EqualFold(s[i+1], "Packages") && i+3 <= last:
			dir := s[i+2]
			if id := wingetID(dir); id != "" {
				return Install{Mode: ModePackage, Manager: ManagerWinget, Package: id}, true
			}
		case strings.EqualFold(s[i+1], "Links") && i+2 == last:
			return Install{Mode: ModePackage, Manager: ManagerWinget, Package: DefaultWingetID}, true
		}
	}
	return Install{}, false
}

// wingetID reads the package id out of a package directory's name, "" when
// the name is not winget's <id>_<source> shape.
func wingetID(dir string) string {
	if n := len(dir) - len(wingetSource); n > 0 && strings.EqualFold(dir[n:], wingetSource) {
		return dir[:n]
	}
	// Another source: its identifier follows the first underscore.
	if i := strings.IndexByte(dir, '_'); i > 0 && i < len(dir)-1 {
		return dir[:i]
	}
	return ""
}

// snapPackage recognises a snap: snapd sets SNAP (the mounted revision) and
// SNAP_NAME for everything a snap runs. ⚠ Those variables are inherited:
// a terminal opened from another snap (the VS Code snap's, for one) carries
// SNAP=/snap/code/…, and a filex started there would be told to
// `snap refresh code`. So the running binary must also live under $SNAP.
func snapPackage(getenv func(string) string, exe string) (Install, bool) {
	root, name := getenv("SNAP"), getenv("SNAP_NAME")
	if root == "" || name == "" {
		return Install{}, false
	}
	root = path.Clean(root)
	if root == "/" || !strings.HasPrefix(path.Clean(exe), root+"/") {
		return Install{}, false
	}
	// A parallel install (snap install filex_beta) is refreshed by its
	// instance name.
	if inst := getenv("SNAP_INSTANCE_NAME"); inst != "" {
		name = inst
	}
	return Install{Mode: ModePackage, Manager: ManagerSnap, Package: name}, true
}
