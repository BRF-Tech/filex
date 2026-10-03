// Package enginebin answers one question for the whole server: which of the
// heavy conversion programs - ffmpeg, ImageMagick, Ghostscript, poppler,
// rsvg - are installed here, and which binary each one is. It also names the
// office engine (Office), which is not a program here but the connected
// OnlyOffice Document Server, and its pre-0.50 name `libreoffice`.
//
// # Why there is exactly one answer
//
// Until v0.43.0 two probes asked it separately and disagreed. The About page
// (internal/capability) counted ImageMagick as present when `magick` OR
// `convert` was on PATH; the app runtime (internal/wasmplugin) additionally
// checked that `convert` really was ImageMagick. On Windows `convert` resolves
// to C:\Windows\System32\convert.exe — the FAT→NTFS volume converter — so the
// About page said "ImageMagick: OK" while Apps and the converter said it was
// not installed (release-candidate sweep, 2026-09-21). Every screen now reads
// Probe(), so the answer a person sees cannot depend on which page they are
// looking at.
//
// The probe runs once per process. Installing an engine therefore takes a
// restart to show up — on every screen at once, which is the point: a live
// re-probe on one page and a boot-time answer on another is the disagreement
// this package exists to remove.
package enginebin

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/officecmd"
)

// Engine ids. They are also the `engines:<id>` permission names a plugin
// asks for, so they never change spelling.
const (
	FFmpeg      = "ffmpeg"
	ImageMagick = "imagemagick"
	Ghostscript = "ghostscript"
	Poppler     = "poppler"
	RSVG        = "rsvg"
	// Office is the office-document engine. It is not a program on this
	// machine: it is the OnlyOffice Document Server filex is connected to,
	// reached through its conversion API (internal/wasmplugin/office.go).
	// So it is never probed here, and it is there exactly while a document
	// server is configured.
	Office = officecmd.Engine
	// LibreOffice is the office engine's name until 0.50, kept as an ALIAS
	// of Office: an app that asks for `engines:libreoffice` is granted, told
	// about and run on the office engine, unchanged. Nothing named soffice is
	// looked for or run any more (0.50: LibreOffice left the images, and a
	// LibreOffice installed next to a bare binary is not used either - one
	// office engine, the same everywhere).
	LibreOffice = officecmd.LegacyEngine
)

// Candidates lists, per BINARY engine, the binaries that count as it, in
// order of preference. The office engine has none (see Office).
//
// ⚠ ImageMagick: `magick` FIRST. It is the ImageMagick 7 entry point and the
// only name that is ImageMagick on every platform; `convert` is the legacy
// ImageMagick 6 name and, on Windows, a system tool that happens to share it.
var Candidates = map[string][]string{
	FFmpeg:      {"ffmpeg"},
	ImageMagick: {"magick", "convert"},
	Ghostscript: {"gs"},
	Poppler:     {"pdftoppm"},
	RSVG:        {"rsvg-convert"},
}

// aliases maps an engine's old name to the engine it now is.
var aliases = map[string]string{LibreOffice: Office}

// Canonical is the engine an id names: the id itself, or the engine an alias
// stands for ("libreoffice" → "office").
func Canonical(id string) string {
	if c, ok := aliases[id]; ok {
		return c
	}
	return id
}

// AliasesOf lists the old names that stand for an engine, sorted.
func AliasesOf(id string) []string {
	var out []string
	for a, c := range aliases {
		if c == id {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

// Known reports whether id names an engine a plugin may ask for: a binary
// engine, the office engine, or an alias of one.
func Known(id string) bool {
	if _, ok := Candidates[id]; ok {
		return true
	}
	c := Canonical(id)
	return c == Office
}

// Names lists every engine id, sorted - the binary engines and the office
// engine, never an alias.
func Names() []string {
	out := make([]string, 0, len(Candidates)+1)
	for k := range Candidates {
		out = append(out, k)
	}
	out = append(out, Office)
	sort.Strings(out)
	return out
}

// DisplayName is an engine as a person reads it — the product's own
// spelling. The id stays the machine's word (permissions, logs). An alias
// reads as the engine it stands for: an app asking for `libreoffice` is
// asking for what this server converts office documents with.
func DisplayName(id string) string {
	switch Canonical(id) {
	case FFmpeg:
		return "FFmpeg"
	case ImageMagick:
		return "ImageMagick"
	case Office:
		// The project's own spelling, the one every screen uses for it
		// (web/tests/i18n/serviceNames.test.ts).
		return "ONLYOFFICE"
	case Ghostscript:
		return "Ghostscript"
	case Poppler:
		return "Poppler"
	case RSVG:
		return "librsvg"
	}
	return id
}

// Set is one probe's answer: engine id → resolved binary.
type Set struct {
	bins map[string]string
}

// Path is the binary an engine resolves to, "" when it is not installed.
func (s *Set) Path(engine string) string {
	if s == nil {
		return ""
	}
	return s.bins[engine]
}

// Has reports whether an engine is installed.
func (s *Set) Has(engine string) bool { return s.Path(engine) != "" }

// Available is engine id → installed, for every known engine.
func (s *Set) Available() map[string]bool {
	out := map[string]bool{}
	for name := range Candidates {
		out[name] = s.Has(name)
	}
	return out
}

// Missing lists the given engine ids that are not installed, in the order
// given.
func (s *Set) Missing(engines []string) []string {
	var out []string
	for _, e := range engines {
		if !s.Has(e) {
			out = append(out, e)
		}
	}
	return out
}

// prober is the probe with its three outside dependencies named, so a test
// can stand in a Windows host without being one.
type prober struct {
	lookPath   func(string) (string, error)
	banner     func(bin string) []byte
	systemRoot string
}

func (p prober) probe() *Set {
	s := &Set{bins: map[string]string{}}
	for name, cands := range Candidates {
		for _, c := range cands {
			path, err := p.lookPath(c)
			if err != nil || path == "" {
				continue
			}
			if !p.genuine(name, path) {
				continue
			}
			s.bins[name] = path
			break
		}
	}
	return s
}

// genuine guards the one engine whose names collide with something else.
//
// ⚠⚠ Two independent refusals, because each alone was not enough:
//
//   - A binary inside the Windows system directory is NEVER ImageMagick.
//     `C:\Windows\System32\convert.exe` is the NTFS volume converter; it
//     answers ImageMagick's arguments with "Invalid Parameter - -compress"
//     (measured 2026-09-20 on the owner's PC: every png→pdf failed with
//     exactly that line). Refused by where it lives, before it is ever run —
//     running a disk tool to ask its version is not a thing to do casually.
//   - Anything else must say "ImageMagick" in its `-version` banner, `magick`
//     included: a wrapper script or an unrelated `convert` elsewhere on PATH
//     is not the engine the converter's arguments were written for.
func (p prober) genuine(engine, path string) bool {
	if engine != ImageMagick {
		return true
	}
	if inSystemDir(path, p.systemRoot) {
		return false
	}
	return bytes.Contains(p.banner(path), []byte("ImageMagick"))
}

// inSystemDir reports whether path is inside %SystemRoot%\System32 (or its
// 32-bit twin). Case-insensitive, separators normalised: Windows paths are
// both, and LookPath may answer either spelling.
func inSystemDir(path, systemRoot string) bool {
	norm := func(s string) string {
		return strings.TrimRight(strings.ToLower(strings.ReplaceAll(s, "/", `\`)), `\`)
	}
	p := norm(path)
	roots := []string{`c:\windows`}
	if systemRoot != "" {
		roots = append(roots, norm(systemRoot))
	}
	for _, r := range roots {
		for _, sub := range []string{`\system32\`, `\syswow64\`, `\sysnative\`} {
			if strings.HasPrefix(p, r+sub) {
				return true
			}
		}
	}
	return false
}

func versionBanner(bin string) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, "-version").CombinedOutput()
	return out
}

func systemProber() prober {
	return prober{
		lookPath: func(name string) (string, error) {
			p, err := exec.LookPath(name)
			if err != nil {
				return "", err
			}
			if abs, aerr := filepath.Abs(p); aerr == nil {
				p = abs
			}
			return p, nil
		},
		banner:     versionBanner,
		systemRoot: os.Getenv("SYSTEMROOT"),
	}
}

var (
	mu      sync.Mutex
	current *Set
)

// Probe is the server's one answer, computed on first use and kept for the
// life of the process.
func Probe() *Set {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		current = systemProber().probe()
	}
	return current
}

// SetForTest replaces the process-wide answer (nil = probe again on next
// use) and returns a function that puts the previous one back.
func SetForTest(available map[string]string) func() {
	mu.Lock()
	prev := current
	if available == nil {
		current = nil
	} else {
		current = &Set{bins: map[string]string{}}
		for k, v := range available {
			current.bins[k] = v
		}
	}
	mu.Unlock()
	return func() {
		mu.Lock()
		current = prev
		mu.Unlock()
	}
}
