package wasmplugin

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── The interface's bundle: one zip, served from its own index ──────────
//
// ⚠⚠ The zip is NEVER unpacked. It is checked once, at install (every entry
// read to the end, so a header that lies about a size or a checksum is caught
// before anybody is served from it), written to the app's directory as it
// came, and indexed at load: name → where its bytes are in the file. A request
// is answered by looking its path up in that index — so no request path is
// ever joined onto a directory, and "zip slip" has nothing to slip into.
// (VS Code CVE-2022-41042: a path was checked before it was normalised. Here a
// path is normalised by refusing everything that is not already normal —
// uiPathOK — and then only compared.)

const (
	// DefaultMaxUIBytes is the zipped bundle's ceiling unless
	// FILEX_APP_PLUGIN_MAX_UI_MB says otherwise. draw.io's whole editor is about
	// 60 MB zipped; 128 MiB leaves room without letting an app park arbitrary
	// data on the server.
	DefaultMaxUIBytes = 128 << 20
	// maxUIUnpacked bounds what the zip unpacks to (a zip bomb's ratio).
	maxUIUnpacked = 512 << 20
	// maxUIFiles bounds the entries.
	maxUIFiles = 20000
	// maxUIFileBytes bounds one entry.
	maxUIFileBytes = 64 << 20
	// maxUIHTMLBytes bounds one HTML file: it is read whole to put the
	// bootstrap in (uiserve.go).
	maxUIHTMLBytes = 8 << 20
	// maxUIMirrorBytes bounds one mirrored external file, maxUIMirrorsBytes
	// all of them together.
	maxUIMirrorBytes  = 32 << 20
	maxUIMirrorsBytes = 128 << 20

	uiZipName = "ui.zip"
	uiExtDir  = "ui-ext"
)

// uiTypes is the served list: extension → Content-Type. A bundle holding any
// other extension is refused at install, so nothing is ever served as a type
// the browser has to guess (the answer is nosniff anyway). A file with no
// extension at all (LICENSE, README) is served as text.
var uiTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".htm": "text/html; charset=utf-8",
	".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8", ".cjs": "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8", ".json": "application/json", ".map": "application/json",
	".webmanifest": "application/manifest+json", ".wasm": "application/wasm",
	".txt": "text/plain; charset=utf-8", ".md": "text/plain; charset=utf-8", ".csv": "text/csv; charset=utf-8",
	".xml": "application/xml", ".xsl": "application/xml", ".properties": "text/plain; charset=utf-8",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".avif": "image/avif", ".svg": "image/svg+xml", ".ico": "image/x-icon", ".bmp": "image/bmp", ".cur": "image/x-icon",
	".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf", ".eot": "application/vnd.ms-fontobject",
	".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".oga": "audio/ogg", ".wav": "audio/wav", ".flac": "audio/flac", ".m4a": "audio/mp4",
	".mp4": "video/mp4", ".webm": "video/webm", ".ogv": "video/ogg",
	".glb": "model/gltf-binary", ".gltf": "model/gltf+json", ".bin": "application/octet-stream",
	".pdf": "application/pdf",
}

// uiTypeOf is the Content-Type a bundle path is served with; ok is false for
// an extension that is not on the list.
func uiTypeOf(name string) (string, bool) {
	ext := strings.ToLower(path.Ext(name))
	if ext == "" {
		return "text/plain; charset=utf-8", true
	}
	t, ok := uiTypes[ext]
	return t, ok
}

// mirrorType is the Content-Type a mirrored file is served with: the one its
// name gives, but ONLY when that is a type of what it was declared as (`as`).
// Anything else is application/octet-stream, which under nosniff is never a
// script or a stylesheet — the package's own address is in the page's
// script-src, so an "image" at …/evil.js must not be one (security review
// UI-8).
func mirrorType(as, name string) string {
	ct, ok := uiTypeOf(name)
	if !ok {
		return "application/octet-stream"
	}
	kind := strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])
	switch as {
	case "style":
		if kind == "text/css" {
			return ct
		}
	case "font":
		if strings.HasPrefix(kind, "font/") || kind == "application/vnd.ms-fontobject" {
			return ct
		}
	case "img":
		if strings.HasPrefix(kind, "image/") {
			return ct
		}
	case "media":
		if strings.HasPrefix(kind, "audio/") || strings.HasPrefix(kind, "video/") {
			return ct
		}
	}
	return "application/octet-stream"
}

func isUIHTML(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".html" || ext == ".htm"
}

// uiEntry is one file of the bundle: where its bytes are in the zip.
type uiEntry struct {
	method uint16
	off    int64
	csize  int64
	usize  int64
	// crc is the entry's CRC-32 — what an upgrade's review compares to say
	// which files changed.
	crc uint32
}

// uiIndex is a checked bundle: its hash and every file.
type uiIndex struct {
	sum      string
	bytes    int64
	unpacked int64
	files    map[string]uiEntry
}

// Count is how many files the bundle holds.
func (x *uiIndex) Count() int { return len(x.files) }

// Names are the bundle's files, for the upgrade review's file list.
func (x *uiIndex) Names() []string {
	out := make([]string, 0, len(x.files))
	for n := range x.files {
		out = append(out, n)
	}
	return out
}

// indexUIZip reads a zip's central directory into an index — every name held
// to uiPathOK and the served list, no link, no duplicate — and, with
// `verify`, reads every entry to the end so a lying header is refused (what
// install does; a load of what install already checked does not repeat it).
func indexUIZip(ra io.ReaderAt, size int64, verify bool) (*uiIndex, error) {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return nil, fmt.Errorf("the interface bundle is not a zip: %w", err)
	}
	if len(zr.File) > maxUIFiles {
		return nil, fmt.Errorf("the interface bundle has %d entries, at most %d", len(zr.File), maxUIFiles)
	}
	x := &uiIndex{bytes: size, files: map[string]uiEntry{}}
	folded := map[string]string{}
	for _, f := range zr.File {
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("the interface bundle holds a link (%s); only files are served", clip(f.Name, 120))
		}
		if mode.IsDir() || strings.HasSuffix(f.Name, "/") {
			continue
		}
		if !mode.IsRegular() && mode&os.ModeType != 0 {
			return nil, fmt.Errorf("the interface bundle holds %s, which is not a file", clip(f.Name, 120))
		}
		name := f.Name
		if !uiPathOK(name) {
			return nil, fmt.Errorf("the interface bundle holds %q: a name must be a relative path with forward slashes, no empty, . or .. segment, no backslash and no drive", clip(name, 120))
		}
		if _, ok := uiTypeOf(name); !ok {
			return nil, fmt.Errorf("the interface bundle holds %s: %s files are not served (HTML, scripts, styles, JSON, source maps, wasm, images, fonts, audio, video, text and XML are)", clip(name, 120), strings.ToLower(path.Ext(name)))
		}
		low := strings.ToLower(name)
		if prev, dup := folded[low]; dup {
			return nil, fmt.Errorf("the interface bundle holds %s and %s - names that differ only in case are one file on some systems", clip(prev, 80), clip(name, 80))
		}
		folded[low] = name
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, fmt.Errorf("the interface bundle's %s is compressed with method %d; use stored or deflate", clip(name, 120), f.Method)
		}
		if f.UncompressedSize64 > maxUIFileBytes {
			return nil, fmt.Errorf("the interface bundle's %s is over %d MiB", clip(name, 120), maxUIFileBytes>>20)
		}
		if isUIHTML(name) && f.UncompressedSize64 > maxUIHTMLBytes {
			return nil, fmt.Errorf("the interface bundle's %s is over %d MiB - an HTML page that large is not an interface", clip(name, 120), maxUIHTMLBytes>>20)
		}
		x.unpacked += int64(f.UncompressedSize64)
		if x.unpacked > maxUIUnpacked {
			return nil, fmt.Errorf("the interface bundle unpacks to more than %d MiB", maxUIUnpacked>>20)
		}
		off, err := f.DataOffset()
		if err != nil {
			return nil, fmt.Errorf("the interface bundle's %s: %w", clip(name, 120), err)
		}
		if verify {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("the interface bundle's %s: %w", clip(name, 120), err)
			}
			n, err := io.Copy(io.Discard, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
			rc.Close()
			if err != nil || n != int64(f.UncompressedSize64) {
				return nil, fmt.Errorf("the interface bundle's %s does not read back as its header says (a damaged zip)", clip(name, 120))
			}
		}
		x.files[name] = uiEntry{method: f.Method, off: off, csize: int64(f.CompressedSize64), usize: int64(f.UncompressedSize64), crc: f.CRC32}
	}
	if len(x.files) == 0 {
		return nil, errors.New("the interface bundle is empty")
	}
	return x, nil
}

// stagedUI is a checked bundle and its mirrored files, still in memory.
type stagedUI struct {
	zip     []byte
	idx     *uiIndex
	mirrors []stagedMirror
}

type stagedMirror struct {
	ext   wire.UIExternal
	bytes []byte
}

// stageUI reads, pins and checks the bundle an install carries, and fetches
// the mirrored external files. m has a `ui` block.
func (r *Registry) stageUI(ctx context.Context, m *Manifest, in *InstallInput) (*stagedUI, error) {
	if in.UI == nil {
		return nil, installErr(ErrCodeManifestInvalid, "the manifest declares an interface (ui) - supply its bundle: the multipart part `ui`, or ui.bundle.url for an install from GitHub or an address")
	}
	b, err := io.ReadAll(io.LimitReader(in.UI, r.maxUIBytes()+1))
	if err != nil {
		return nil, installErr(ErrCodeFetch, "read the interface bundle: "+err.Error())
	}
	if int64(len(b)) > r.maxUIBytes() {
		return nil, installErr(ErrCodeTooLarge, fmt.Sprintf("the interface bundle is larger than %d MiB (FILEX_APP_PLUGIN_MAX_UI_MB)", r.maxUIBytes()>>20))
	}
	h := sha256.Sum256(b)
	sum := hex.EncodeToString(h[:])
	if pin := m.UI.Bundle.SHA256; pin != "" && pin != sum {
		return nil, installErr(ErrCodeSHA256Mismatch, "interface bundle sha256 "+sum[:12]+"… does not match the manifest's "+pin[:12]+"…")
	}
	if in.UIPin != "" && !strings.EqualFold(in.UIPin, sum) {
		return nil, installErr(ErrCodeSHA256Mismatch, "interface bundle sha256 "+sum[:12]+"… is not the one recorded for this version")
	}
	if m.UI.Bundle.SHA256 == "" && in.UIPin == "" && r.RequiresSignature() {
		// The signature covers the module (whose describe echoes the
		// manifest) or, with no module, the manifest itself. A bundle the
		// manifest does not pin is covered by neither.
		return nil, installErr(ErrCodeSignatureRequired, "this instance only runs signed apps: the manifest must pin the interface bundle (ui.bundle.sha256), so the signature covers it")
	}
	idx, err := indexUIZip(bytes.NewReader(b), int64(len(b)), true)
	if err != nil {
		return nil, installErr(ErrCodeManifestInvalid, err.Error())
	}
	idx.sum = sum
	for _, v := range m.Views {
		if v.UI == "" {
			continue
		}
		if _, ok := idx.files[v.UI]; !ok {
			return nil, installErr(ErrCodeManifestInvalid, "view "+v.ID+" opens "+v.UI+", which the interface bundle does not hold")
		}
	}
	for _, d := range m.NewDocuments {
		if d.Template == "" {
			continue
		}
		e, ok := idx.files[d.Template]
		if !ok {
			return nil, installErr(ErrCodeManifestInvalid, "a new ."+d.Ext+" file is made from "+d.Template+", which the interface bundle does not hold")
		}
		if e.usize > maxNewDocTemplate {
			return nil, installErr(ErrCodeManifestInvalid, "the template "+d.Template+" is larger than 16 MiB")
		}
	}
	st := &stagedUI{zip: b, idx: idx}
	var total int64
	for _, e := range m.UI.External {
		if e.SHA256 == "" {
			continue
		}
		if _, clash := idx.files[mirrorPath(e.URL)]; clash {
			return nil, installErr(ErrCodeManifestInvalid, "the bundle holds "+mirrorPath(e.URL)+", which is where the mirrored "+e.URL+" is served - rename one")
		}
		body, have := in.Mirrors[e.SHA256]
		if have {
			h := sha256.Sum256(body)
			have = hex.EncodeToString(h[:]) == e.SHA256
		}
		if !have {
			var err error
			if body, err = r.fetchMirror(ctx, e); err != nil {
				return nil, err
			}
		}
		total += int64(len(body))
		if total > maxUIMirrorsBytes {
			return nil, installErr(ErrCodeTooLarge, fmt.Sprintf("the mirrored files come to more than %d MiB", maxUIMirrorsBytes>>20))
		}
		st.mirrors = append(st.mirrors, stagedMirror{ext: e, bytes: body})
	}
	return st, nil
}

// fetchMirror downloads one mirrored external file through the guarded
// outbound client — the one asset_fetch and http_request use, which refuses
// private and loopback addresses, so an app's manifest cannot make an install
// reach into the server's own network — and holds it to its sha256.
func (r *Registry) fetchMirror(ctx context.Context, e wire.UIExternal) ([]byte, error) {
	u, err := url.Parse(e.URL)
	if err != nil {
		return nil, installErr(ErrCodeManifestInvalid, "ui.external: "+e.URL+" is not an address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, installErr(ErrCodeManifestInvalid, "ui.external: "+err.Error())
	}
	req.Header.Set("User-Agent", "filex-app-plugins/"+HostVersion)
	client := &http.Client{Transport: r.outbound, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= httpMaxRedirect {
			return errors.New("too many redirects")
		}
		if next.URL.Scheme != "https" {
			return errors.New("redirect away from https")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &InstallError{Code: ErrCodeFetch, Reason: FetchReasonUnreachable, Where: e.URL, Message: "mirrored file " + e.URL + ": " + clip(err.Error(), 200)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		reason := FetchReasonHTTPStatus
		return nil, &InstallError{Code: ErrCodeFetch, Reason: reason, Where: e.URL, Status: resp.StatusCode, Message: fmt.Sprintf("mirrored file %s: http %d", e.URL, resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUIMirrorBytes+1))
	if err != nil {
		return nil, &InstallError{Code: ErrCodeFetch, Reason: FetchReasonUnreachable, Where: e.URL, Message: "mirrored file " + e.URL + ": " + clip(err.Error(), 200)}
	}
	if len(body) > maxUIMirrorBytes {
		return nil, &InstallError{Code: ErrCodeFetch, Reason: FetchReasonTooLarge, Where: e.URL, Message: fmt.Sprintf("mirrored file %s is over %d MiB", e.URL, maxUIMirrorBytes>>20)}
	}
	h := sha256.Sum256(body)
	if got := hex.EncodeToString(h[:]); got != e.SHA256 {
		return nil, installErr(ErrCodeSHA256Mismatch, "mirrored file "+e.URL+" sha256 "+got[:12]+"… does not match the manifest's "+e.SHA256[:12]+"…")
	}
	return body, nil
}

// writeUI puts a staged bundle and its mirrors into the app's directory.
func writeUI(dir string, st *stagedUI) error {
	if st == nil {
		return nil
	}
	if err := os.WriteFile(filepath.Join(dir, uiZipName), st.zip, 0o600); err != nil {
		return fmt.Errorf("app-plugins: write interface bundle: %w", err)
	}
	if len(st.mirrors) == 0 {
		return nil
	}
	ext := filepath.Join(dir, uiExtDir)
	if err := os.MkdirAll(ext, 0o700); err != nil {
		return fmt.Errorf("app-plugins: mirrored files: %w", err)
	}
	for _, mf := range st.mirrors {
		if err := os.WriteFile(filepath.Join(ext, mf.ext.SHA256), mf.bytes, 0o600); err != nil {
			return fmt.Errorf("app-plugins: write mirrored file: %w", err)
		}
	}
	return nil
}

// uiBundle is a loaded interface: the index of the zip on disk, and the
// mirrored files by the path they are served at.
type uiBundle struct {
	app   string
	sum   string
	short string
	zip   string
	idx   *uiIndex
	// mirrors: served path (ext/<host>/<path>) → the file and its type.
	mirrors map[string]uiMirror
}

type uiMirror struct {
	file string
	as   string
	size int64
}

// uiShortLen is how much of the bundle's sha256 its address carries.
const uiShortLen = 16

// loadUIBundle opens what writeUI wrote for an installed app, holds the zip
// to the sha256 its row recorded — the same at-rest check a module gets at
// every compile — and indexes it. The index keeps no file open: on Windows an
// open handle would stop the next upgrade from moving the directory.
func loadUIBundle(dir, name, sum string, m *Manifest) (*uiBundle, error) {
	file := filepath.Join(dir, uiZipName)
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("interface bundle: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return nil, fmt.Errorf("interface bundle: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, sum) {
		return nil, fmt.Errorf("interface bundle sha256 %s… does not match the installed %s…", got[:12], clip(sum, 12))
	}
	idx, err := indexUIZip(f, size, false)
	if err != nil {
		return nil, err
	}
	idx.sum = strings.ToLower(sum)
	b := &uiBundle{app: name, sum: idx.sum, short: idx.sum[:uiShortLen], zip: file, idx: idx, mirrors: map[string]uiMirror{}}
	if m != nil && m.UI != nil {
		for _, e := range m.UI.External {
			if e.SHA256 == "" {
				continue
			}
			mf := filepath.Join(dir, uiExtDir, e.SHA256)
			st, err := os.Stat(mf)
			if err != nil {
				return nil, fmt.Errorf("mirrored file %s is missing: %w", e.URL, err)
			}
			b.mirrors[mirrorPath(e.URL)] = uiMirror{file: mf, as: e.As, size: st.Size()}
		}
	}
	return b, nil
}

// open reads one file of the bundle: a reader of its uncompressed bytes, its
// size. The caller closes it.
func (b *uiBundle) open(name string) (io.ReadCloser, int64, error) {
	e, ok := b.idx.files[name]
	if !ok {
		return nil, 0, os.ErrNotExist
	}
	f, err := os.Open(b.zip)
	if err != nil {
		return nil, 0, err
	}
	sec := io.NewSectionReader(f, e.off, e.csize)
	var rd io.Reader = sec
	var fl io.ReadCloser
	if e.method == zip.Deflate {
		fl = flate.NewReader(sec)
		rd = fl
	}
	return &uiReader{Reader: io.LimitReader(rd, e.usize), f: f, fl: fl}, e.usize, nil
}

type uiReader struct {
	io.Reader
	f  *os.File
	fl io.ReadCloser
}

func (r *uiReader) Close() error {
	if r.fl != nil {
		_ = r.fl.Close()
	}
	return r.f.Close()
}
