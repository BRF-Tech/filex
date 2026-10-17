package wasmplugin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── An app that brings the office engine (filex 0.57.0, task #268) ──────
//
// The Office editor app edits documents in the browser; the work a Document
// Server did on the SERVER - office conversions for apps (`engines:office`)
// and office thumbnails - it brings as an engine: engine.zip, named in its
// manifest's `engines.office` block with its SHA-256, beside its interface
// bundle (docs/APP-PLUGINS-API.md → engines.office). The registry only
// checks, keeps and hands the package on; internal/officeengine runs it, in
// a process of its own, never inside filex's binary (the engine is AGPL,
// filex is MIT).
//
// The grant is DERIVED from the block (`office-engine`), as `co_edit`
// derives `files:co-edit`: one place says what the app does, and an update
// that starts bringing an engine is a new grant the review says in its own
// sentence (srvtext `server.perm.office_engine`).
//
// The zip is checked at install (every entry read to the end, the files the
// engine needs present) and written to the app's directory as it came; it is
// never unpacked. The engine process reads it in place, held again to its
// pin every time it starts.

// PermOfficeEngine: the app brings an office engine filex runs on the
// server. Derived from `engines.office`, never listed in `permissions`.
const PermOfficeEngine Permission = "office-engine"

// Parsed here so a stored grant and an approval naming it read back.
func init() { bare[PermOfficeEngine] = true }

// isOfficeEnginePerm reports whether p is derived from `engines.office`.
func isOfficeEnginePerm(p Permission) bool { return p == PermOfficeEngine }

const (
	engineZipName = "engine.zip"
	// MaxOfficeEngineBytes is engine.zip's ceiling, zipped: x2t-server.wasm is
	// about 40 MB, the rest a few.
	MaxOfficeEngineBytes = 256 << 20
	// maxEngineEntry bounds one entry, maxEngineUnpacked all of them.
	maxEngineEntry    = 160 << 20
	maxEngineUnpacked = 512 << 20
	// defaultEngineEditor is where the Office editor's bundle keeps the
	// editor's files.
	defaultEngineEditor = "editor"
)

// The files engine.zip must hold (internal/officeengine reads them).
var engineRequired = []string{"x2t-server.wasm", "layout-host.wasm", "layout/worker.js"}

var sha256HexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// officeEngineOf is the manifest's `engines.office`, nil-safe.
func officeEngineOf(m *Manifest) *wire.OfficeEngineSpec {
	if m == nil || m.Engines == nil {
		return nil
	}
	return m.Engines.Office
}

// engineEditorDir is the bundle directory with the editor's sdkjs/ and
// fonts/.
func engineEditorDir(s *wire.OfficeEngineSpec) string {
	if s == nil || strings.TrimSpace(s.Editor) == "" {
		return defaultEngineEditor
	}
	return strings.Trim(strings.TrimSpace(s.Editor), "/")
}

// checkOfficeEngine validates `engines` and adds the derived permission.
// Called by Validate once Perms holds the manifest's own list and the
// interface is checked.
func (m *Manifest) checkOfficeEngine() error {
	for _, raw := range m.Permissions {
		if Permission(strings.TrimSpace(raw)) == PermOfficeEngine {
			return fmt.Errorf("manifest: permission %q is derived from the engines.office block - leave it out of permissions", raw)
		}
	}
	if m.Engines == nil {
		return nil
	}
	s := m.Engines.Office
	if s == nil {
		return errors.New("manifest: engines names no engine (the one an app may bring is office)")
	}
	if m.UI == nil {
		return errors.New("manifest: engines.office reads the editor's scripts and fonts from the app's interface bundle, and the manifest has no ui block")
	}
	s.SHA256 = strings.ToLower(strings.TrimSpace(s.SHA256))
	if !sha256HexRe.MatchString(s.SHA256) {
		return errors.New("manifest: engines.office.sha256 must be engine.zip's SHA-256 (64 lower-case hex digits) - the engine runs code on the server, so it is always pinned")
	}
	if dir := engineEditorDir(s); !uiPathOK(dir) {
		return fmt.Errorf("manifest: engines.office.editor %q must be a directory of the interface bundle (a relative path, forward slashes)", s.Editor)
	}
	m.Perms = append(m.Perms, PermOfficeEngine)
	return nil
}

// stagedEngine is a checked engine.zip, still in memory.
type stagedEngine struct {
	zip []byte
	sum string
}

// stageEngine reads, pins and checks the engine an install carries. m has
// an `engines.office` block; ui is its staged interface (the editor's files
// must be in it).
func (r *Registry) stageEngine(m *Manifest, in *InstallInput, ui *stagedUI) (*stagedEngine, error) {
	spec := officeEngineOf(m)
	if in.Engine == nil {
		return nil, installErr(ErrCodeManifestInvalid, "the manifest brings an office engine (engines.office) - supply engine.zip: the multipart part `engine`, or engines.office.url for an install from GitHub or an address")
	}
	b, err := io.ReadAll(io.LimitReader(in.Engine, MaxOfficeEngineBytes+1))
	if err != nil {
		return nil, installErr(ErrCodeFetch, "read the office engine: "+err.Error())
	}
	if int64(len(b)) > MaxOfficeEngineBytes {
		return nil, installErr(ErrCodeTooLarge, fmt.Sprintf("the office engine is larger than %d MiB", MaxOfficeEngineBytes>>20))
	}
	h := sha256.Sum256(b)
	sum := hex.EncodeToString(h[:])
	if sum != spec.SHA256 {
		return nil, installErr(ErrCodeSHA256Mismatch, "office engine sha256 "+sum[:12]+"… does not match the manifest's "+spec.SHA256[:12]+"…")
	}
	if err := checkEngineZip(b); err != nil {
		return nil, installErr(ErrCodeManifestInvalid, err.Error())
	}
	if ui != nil {
		dir := engineEditorDir(spec)
		for _, sub := range []string{"sdkjs/", "fonts/"} {
			if !ui.idx.hasPrefix(dir + "/" + sub) {
				return nil, installErr(ErrCodeManifestInvalid, "the office engine reads "+dir+"/"+sub+" from the interface bundle, which holds no such directory")
			}
		}
	}
	return &stagedEngine{zip: b, sum: sum}, nil
}

// hasPrefix reports whether any file of the bundle is under prefix.
func (x *uiIndex) hasPrefix(prefix string) bool {
	for name := range x.files {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// checkEngineZip holds engine.zip to what the engine reads: files only,
// clean relative names, stored or deflated, within the limits, every entry
// reading back as its header says, and the required files present (the two
// modules WebAssembly).
func checkEngineZip(b []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return fmt.Errorf("the office engine is not a zip: %w", err)
	}
	seen := map[string]bool{}
	var unpacked uint64
	for _, f := range zr.File {
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("the office engine holds a link (%s)", clip(f.Name, 120))
		}
		if f.Mode().IsDir() || strings.HasSuffix(f.Name, "/") {
			continue
		}
		if !f.Mode().IsRegular() && f.Mode()&os.ModeType != 0 {
			return fmt.Errorf("the office engine holds %s, which is not a file", clip(f.Name, 120))
		}
		if !uiPathOK(f.Name) {
			return fmt.Errorf("the office engine holds %q: a name must be a relative path with forward slashes", clip(f.Name, 120))
		}
		if seen[strings.ToLower(f.Name)] {
			return fmt.Errorf("the office engine holds %s twice (names that differ only in case are one file on some systems)", clip(f.Name, 120))
		}
		seen[strings.ToLower(f.Name)] = true
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return fmt.Errorf("the office engine's %s is compressed with method %d; use stored or deflate", clip(f.Name, 120), f.Method)
		}
		if f.UncompressedSize64 > maxEngineEntry {
			return fmt.Errorf("the office engine's %s is over %d MiB", clip(f.Name, 120), maxEngineEntry>>20)
		}
		unpacked += f.UncompressedSize64
		if unpacked > maxEngineUnpacked {
			return fmt.Errorf("the office engine unpacks to more than %d MiB", maxEngineUnpacked>>20)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("the office engine's %s: %w", clip(f.Name, 120), err)
		}
		head := make([]byte, 4)
		n, _ := io.ReadFull(rc, head)
		rest, err := io.Copy(io.Discard, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
		rc.Close()
		if err != nil || uint64(int64(n)+rest) != f.UncompressedSize64 {
			return fmt.Errorf("the office engine's %s does not read back as its header says (a damaged zip)", clip(f.Name, 120))
		}
		if strings.HasSuffix(f.Name, ".wasm") && !bytes.Equal(head[:n], []byte{0x00, 0x61, 0x73, 0x6d}) {
			return fmt.Errorf("the office engine's %s is not WebAssembly", clip(f.Name, 120))
		}
	}
	for _, want := range engineRequired {
		if !seen[want] {
			return fmt.Errorf("the office engine has no %s", want)
		}
	}
	return nil
}

// writeEngine puts a staged engine into the app's directory.
func writeEngine(dir string, st *stagedEngine) error {
	if st == nil {
		return nil
	}
	if err := os.WriteFile(filepath.Join(dir, engineZipName), st.zip, 0o600); err != nil {
		return fmt.Errorf("app-plugins: write office engine: %w", err)
	}
	return nil
}

// checkInstalledEngine holds the engine on disk to the manifest's pin - the
// at-rest check every load makes, as for the module and the interface.
func (r *Registry) checkInstalledEngine(p *Installed) error {
	spec := officeEngineOf(p.Manifest)
	if spec == nil {
		return nil
	}
	f, err := os.Open(filepath.Join(r.opts.Dir, p.Row.Name, engineZipName))
	if err != nil {
		return fmt.Errorf("office engine: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("office engine: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != spec.SHA256 {
		return fmt.Errorf("office engine sha256 %s… does not match the installed %s…", got[:12], spec.SHA256[:12])
	}
	return nil
}

// fetchEngine downloads the engine a manifest names (resolve turns its url
// into an address, as for the interface bundle). Nil when it brings none.
func (r *Registry) fetchEngine(ctx context.Context, m *Manifest, resolve func(string) string) (io.Reader, error) {
	spec := officeEngineOf(m)
	if spec == nil {
		return nil, nil
	}
	if strings.TrimSpace(spec.URL) == "" {
		return nil, installErr(ErrCodeManifestInvalid, "the manifest has no engines.office.url; an install from a repository or an address needs the office engine's address")
	}
	u := resolve(strings.TrimSpace(spec.URL))
	b, err := r.fetch(ctx, u, MaxOfficeEngineBytes)
	if err != nil {
		ie := fetchFailure(err, FetchReasonModuleNotFound, u)
		ie.Message = "office engine: " + err.Error()
		return nil, ie
	}
	return bytes.NewReader(b), nil
}

// ── What the server runs ───────────────────────────────────────────────

// OfficeEnginePackage is an installed app's office engine, as the server's
// engine runner needs it: both zips, each with the hash it was installed
// with.
type OfficeEnginePackage struct {
	App, Version string
	Label        wire.Text
	// Engine is engine.zip; UI the interface bundle, whose EditorDir holds
	// the editor's scripts and fonts.
	Engine, EngineSHA256 string
	UI, UISHA256         string
	EditorDir            string
}

// OfficeEngines is every app that brings an office engine and runs now
// (installed, switched on, loaded, not held by its license), by name.
func (r *Registry) OfficeEngines() []OfficeEnginePackage {
	if r == nil {
		return nil
	}
	var out []OfficeEnginePackage
	for _, p := range r.All() {
		spec := officeEngineOf(p.Manifest)
		if spec == nil || !p.Row.Enabled {
			continue
		}
		if state, _ := p.State(); state != StateRunning {
			continue
		}
		dir := filepath.Join(r.opts.Dir, p.Row.Name)
		out = append(out, OfficeEnginePackage{
			App: p.Row.Name, Version: p.Row.Version, Label: p.Manifest.Label,
			Engine: filepath.Join(dir, engineZipName), EngineSHA256: spec.SHA256,
			UI: filepath.Join(dir, uiZipName), UISHA256: strings.ToLower(p.Row.UISHA256),
			EditorDir: engineEditorDir(spec),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].App < out[j].App })
	return out
}

// EngineHandlers are the office engines as Default apps lists them (the
// `engine` capability, internal/assoc): one handler per app that brings one.
func (r *Registry) EngineHandlers() []assoc.AppHandler {
	var out []assoc.AppHandler
	for _, e := range r.OfficeEngines() {
		out = append(out, assoc.AppHandler{Handler: assoc.Handler{
			ID: assoc.EngineID(e.App), App: e.App, Version: e.Version, Label: e.Label,
		}, Ext: []string{assoc.EngineOffice}})
	}
	return out
}

// EngineHost is the server side of the apps' engines: told when the set of
// running engines may have changed, and asked to let go of an app's files
// before they are moved or removed.
type EngineHost interface {
	// EnginesChanged: install, upgrade, removal, a switch, a license hold.
	// The host reads OfficeEngines again.
	EnginesChanged()
	// ReleaseEngine ends every process that holds app's engine files open
	// (on Windows a file a process holds cannot be moved).
	ReleaseEngine(app string)
}

// SetEngineHost wires the engine host and tells it what runs now.
func (r *Registry) SetEngineHost(h EngineHost) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.engineHost = h
	r.mu.Unlock()
	r.enginesChanged()
}

func (r *Registry) enginesChanged() {
	if r == nil {
		return
	}
	r.mu.RLock()
	h := r.engineHost
	r.mu.RUnlock()
	if h != nil {
		h.EnginesChanged()
	}
}

func (r *Registry) releaseEngine(app string) {
	if r == nil {
		return
	}
	r.mu.RLock()
	h := r.engineHost
	r.mu.RUnlock()
	if h != nil {
		h.ReleaseEngine(app)
	}
}
