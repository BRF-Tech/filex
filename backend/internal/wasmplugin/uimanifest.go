package wasmplugin

import (
	"fmt"
	"mime"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── An app's own interface: the manifest's `ui` block ─────────────────────
//
// An app is a module (the wasm engine), an interface (its own HTML/JS/CSS,
// served by filex in a sandboxed frame), or both. This file is what the
// manifest may say about the interface and what that says about the grant;
// uibundle.go is the zip itself, uipolicy.go the policy it is served under,
// and docs/APP-PLUGINS-API.md → "An app's own interface" the contract.
//
// ⚠⚠ The interface's permissions are DERIVED from the `ui` block and appended
// to Perms, so they are part of the grant like any other: the review lists
// them, the install must grant them, and an upgrade that adds one — a new live
// address above all — meets the same `permissions_changed` every other new
// permission does (upgradeOf). Writing one into `permissions` by hand is
// refused: the manifest has ONE place that says what the interface loads.

// The kinds an external address may be loaded as.
var uiExternalAs = map[string]bool{"style": true, "font": true, "img": true, "media": true}

// The script-policy exceptions a manifest may ask for, and the permission each
// one is.
var uiCSPPerm = map[string]Permission{"unsafe-eval": PermUIEval, "wasm-unsafe-eval": PermUIWasmEval}

const (
	// maxUIExternal bounds the addresses one interface may name.
	maxUIExternal = 32
	// maxUIPath bounds a bundle path (a view's `ui`, a file in the zip).
	maxUIPath = 512
)

// uiPathOK reports whether p is a clean, relative, forward-slash path: no
// empty, `.` or `..` segment, no backslash, no drive, no leading slash, no NUL
// or control character. It is the ONE check of a bundle path — a view's `ui`,
// every name in the zip (uibundle.go), and every request (uiserve.go) — so the
// three can never disagree about what a path is.
func uiPathOK(p string) bool {
	if p == "" || len(p) > maxUIPath || strings.HasPrefix(p, "/") {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c < 0x20 || c == 0x7f || c == '\\' || c == ':' {
			return false
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return path.Clean(p) == p
}

// parseUINet reads the part of a `ui-net:` permission after the prefix:
// `<as>:<url>`.
func parseUINet(rest string) (as, u string, err error) {
	as, u, ok := strings.Cut(rest, ":")
	if !ok || !uiExternalAs[as] {
		return "", "", fmt.Errorf("expected ui-net:<style|font|img|media>:<https url>")
	}
	if err := checkExternalURL(u, false); err != nil {
		return "", "", err
	}
	return as, u, nil
}

// UINetPermission is the permission a live address is: `ui-net:<as>:<url>`.
func UINetPermission(as, u string) Permission {
	return Permission(permPrefixUINet + as + ":" + u)
}

// uiNetOf splits a `ui-net:` permission; ok is false for any other.
func uiNetOf(p Permission) (as, u string, ok bool) {
	rest, found := strings.CutPrefix(string(p), permPrefixUINet)
	if !found {
		return "", "", false
	}
	as, u, err := parseUINet(rest)
	return as, u, err == nil
}

// isUIPerm reports whether p is one of the interface's derived permissions.
func isUIPerm(p Permission) bool {
	return p == PermUI || p == PermUIEval || p == PermUIWasmEval || p == PermUIPackageFetch || p == PermUIDownload ||
		strings.HasPrefix(string(p), permPrefixUINet) || strings.HasPrefix(string(p), permPrefixUIViewer) ||
		strings.HasPrefix(string(p), permPrefixUINew)
}

// maxNewDocuments is how many "New" rows one app may add.
const maxNewDocuments = 8

// maxNewDocTemplate is the largest template a new document is made from.
const maxNewDocTemplate = 16 << 20

// checkNewDocuments holds `new_documents` to what a row of the "New" menu
// is: a kind of file (a plain extension), named in every language the app
// declares, opened by one of the app's `viewer` views that opens that kind,
// made from a file of the package or empty — and saved by an app that may
// write. Each kind is a derived permission.
func (m *Manifest) checkNewDocuments() ([]Permission, error) {
	if len(m.NewDocuments) == 0 {
		return nil, nil
	}
	if m.UI == nil {
		return nil, fmt.Errorf("manifest: new_documents open in the app's interface, and the manifest has no ui block")
	}
	if len(m.NewDocuments) > maxNewDocuments {
		return nil, fmt.Errorf("manifest: new_documents: at most %d", maxNewDocuments)
	}
	writes := false
	for _, p := range m.Perms {
		writes = writes || p == PermFilesWrite
	}
	if !writes {
		return nil, fmt.Errorf("manifest: new_documents need the files:write permission — a new document is saved by the app")
	}
	var perms []Permission
	seen := map[string]bool{}
	for i := range m.NewDocuments {
		d := &m.NewDocuments[i]
		d.Ext = strings.TrimSpace(d.Ext)
		d.View = strings.TrimSpace(d.View)
		d.Template = strings.TrimSpace(d.Template)
		if !viewerExtRe.MatchString(d.Ext) {
			return nil, fmt.Errorf("manifest: new_documents[%d]: ext %q is a lower-case file extension without the dot", i, d.Ext)
		}
		if seen[d.Ext] {
			return nil, fmt.Errorf("manifest: new_documents: .%s is named twice", d.Ext)
		}
		seen[d.Ext] = true
		if strings.TrimSpace(d.Label["en"]) == "" {
			return nil, fmt.Errorf("manifest: new_documents[%d] (.%s): label.en is required", i, d.Ext)
		}
		for _, lang := range m.Languages {
			if strings.TrimSpace(d.Label[lang]) == "" {
				return nil, fmt.Errorf("manifest: new_documents[%d] (.%s) has no %q label, but the app declares that language", i, d.Ext, lang)
			}
		}
		v, ok := m.View(d.View)
		if !ok || v.Placement != "viewer" || v.UI == "" {
			return nil, fmt.Errorf("manifest: new_documents[%d] (.%s): view %q is not one of the app's viewer views — a new document opens in one", i, d.Ext, d.View)
		}
		a := v.Applies
		a.Multi = true
		kind := strings.TrimSpace(strings.SplitN(mime.TypeByExtension("."+d.Ext), ";", 2)[0])
		if !Matches(a, []Item{{Kind: "file", Ext: d.Ext, Mime: kind}}) {
			return nil, fmt.Errorf("manifest: new_documents[%d] (.%s): view %q does not open .%s files", i, d.Ext, d.View, d.Ext)
		}
		if d.Template != "" && !uiPathOK(d.Template) {
			return nil, fmt.Errorf("manifest: new_documents[%d] (.%s): template %q is not a relative path inside the bundle", i, d.Ext, d.Template)
		}
		perms = append(perms, Permission(permPrefixUINew+"."+d.Ext))
	}
	return perms, nil
}

var (
	viewerExtRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_+-]{0,31}(\.[a-z0-9][a-z0-9_+-]{0,31}){0,2}$`)
	viewerMimeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,63}/([a-z0-9][a-z0-9.+-]{0,127}|\*)$`)
)

// viewerKindOK is the grammar of one `ui-viewer:` kind: `.<ext>` or a media
// type (a family `image/*` included, never `*/*`).
func viewerKindOK(kind string) bool {
	if ext, ok := strings.CutPrefix(kind, "."); ok {
		return viewerExtRe.MatchString(ext)
	}
	return viewerMimeRe.MatchString(kind)
}

// checkViewerApplies holds a `viewer` view to what it is: the app's interface
// opening FILES in place of filex's preview. So it names the kinds it opens —
// an extension or a media type, never "any file" — and it opens files, not
// folders. ⚠ Security review UI-6: a viewer with an empty rule matched every
// file, and the app became the default viewer of everything the person
// opened.
func checkViewerApplies(a *wire.Applies) error {
	if a.Kind != "file" {
		return fmt.Errorf("a viewer opens files: applies.kind must be file, not %q", a.Kind)
	}
	if len(a.Ext) == 0 && len(a.Mime) == 0 {
		return fmt.Errorf("a viewer names the files it opens: give applies.ext or applies.mime (a viewer with no rule would open every file)")
	}
	if len(a.EngineExt) > 0 {
		return fmt.Errorf("a viewer opens the kinds it names: applies.engine_ext is for actions")
	}
	for _, e := range a.Ext {
		if !viewerKindOK("." + e) {
			return fmt.Errorf("applies.ext %q is not a file extension", e)
		}
	}
	for _, mt := range a.Mime {
		if !viewerKindOK(mt) {
			return fmt.Errorf("applies.mime %q is not a media type (a family like image/* is; */* is not)", mt)
		}
	}
	return nil
}

// hostLabelRe (one DNS label) is shared with the `http:` permission rule — permissions.go.

// checkExternalURL holds one external address to the rules: https, a whole
// host (no wildcard, no user, no IP-literal trickery beyond a plain host), no
// query or fragment, a path that is a full file — or, for a live address, a
// prefix ending in `/`. `mirrored` refuses the prefix: only a file can be
// downloaded once and checked against a hash.
func checkExternalURL(raw string, mirrored bool) error {
	// ⚠ Security review UI-3: the address goes RAW into a CSP header and into
	// the Connection-Allowlist structured field, where Chrome reads it as a
	// URLPattern. So the check is on the raw text itself, not on what
	// url.Parse makes of it: one plain grammar — lower-case host, optional
	// port, a path of letters, digits, `. _ ~ @ - /` (`@` for the npm-style
	// `name@1.2.3` of CDN paths) and %HH escapes — with
	// nothing that could end a directive, break the structured field
	// (quote, backslash, non-ASCII) or widen the pattern (`( ) { } * + ? :`).
	if !plainExternalRe.MatchString(raw) {
		return fmt.Errorf("%q: an external address is a plain https URL — a lower-case host, and a path of letters, digits, . _ ~ @ - / and %%HH escapes only (no user, port name, query, fragment, wildcard, quote, space, backslash or other character)", raw)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Opaque != "" {
		return fmt.Errorf("%q is not an https address", raw)
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return fmt.Errorf("%q: name the host, not an IP address", raw)
	}
	for _, label := range strings.Split(host, ".") {
		if !hostLabelRe.MatchString(label) {
			return fmt.Errorf("%q: %q is not a host name", raw, host)
		}
	}
	if !strings.Contains(host, ".") || !publicHost(host) {
		return fmt.Errorf("%q: %q is not a public host name", raw, host)
	}
	p := u.EscapedPath()
	if p != raw[len("https://")+len(u.Host):] {
		return fmt.Errorf("%q is not in its canonical form", raw)
	}
	if p == "" || p == "/" {
		return fmt.Errorf("%q: name a file or a folder (a path ending in /), not the whole site", raw)
	}
	for _, esc := range pctRe.FindAllString(p, -1) {
		switch strings.ToLower(esc) {
		case "%2f", "%5c", "%2e", "%00":
			return fmt.Errorf("%q: an escaped separator or dot is not a path", raw)
		}
	}
	if !uiPathOK(strings.TrimSuffix(strings.TrimPrefix(p, "/"), "/")) {
		return fmt.Errorf("%q: the path has an empty, . or .. segment", raw)
	}
	if mirrored && strings.HasSuffix(p, "/") {
		return fmt.Errorf("%q: a mirrored address (one with sha256) is ONE file; a folder can only be live", raw)
	}
	return nil
}

// plainExternalRe is the one grammar an external address is written in
// (checkExternalURL): what CSP, a structured-field string and a URLPattern
// all read literally.
var plainExternalRe = regexp.MustCompile(`^https://[a-z0-9.-]+(:[0-9]{1,5})?/([A-Za-z0-9._~@/-]|%[0-9A-Fa-f]{2})*$`)

var pctRe = regexp.MustCompile(`%[0-9A-Fa-f]{2}`)

// reservedSuffixes are names that never reach the public internet — a live
// address there would point the reader's browser into somebody's network
// (security review UI-13).
var reservedSuffixes = []string{
	"localhost", "local", "internal", "intranet", "corp", "home", "lan", "private",
	"home.arpa", "test", "invalid", "example", "onion", "arpa",
}

func publicHost(host string) bool {
	for _, s := range reservedSuffixes {
		if host == s || strings.HasSuffix(host, "."+s) {
			return false
		}
	}
	return true
}

// checkDescribedUI is the module's word on the interface (security review
// UI-2). A module app's signature covers the module, and the module's
// describe is its proof of what it is — so when the installed manifest has
// an interface, a module that describes one must describe THIS one (the same
// bundle hash, script exceptions and addresses), and on an instance that only
// runs signed apps it must describe one at all: otherwise the interface would
// run under a signature that never saw it. An engineless app's signature is
// over its manifest, which holds the pin, so it needs nothing here.
func (r *Registry) checkDescribedUI(m *Manifest, got *wire.Manifest) error {
	if m == nil || m.UI == nil {
		return nil
	}
	if got == nil || got.UI == nil {
		if r.RequiresSignature() {
			return fmt.Errorf("this instance only runs signed apps and the signature covers the module: the module's describe must declare the same interface (ui) as the manifest, and it declares none")
		}
		return nil
	}
	want, have := m.UI, got.UI
	if !strings.EqualFold(strings.TrimSpace(want.Bundle.SHA256), strings.TrimSpace(have.Bundle.SHA256)) {
		return fmt.Errorf("the module describes another interface bundle (sha256 %q) than the manifest pins (%q)", have.Bundle.SHA256, want.Bundle.SHA256)
	}
	if !sameStrings(want.CSP, have.CSP) {
		return fmt.Errorf("the module describes other script exceptions (%v) than the manifest (%v)", have.CSP, want.CSP)
	}
	if want.PackageFetch != have.PackageFetch {
		return fmt.Errorf("the module describes package_fetch %v, the manifest %v", have.PackageFetch, want.PackageFetch)
	}
	if want.Download != have.Download {
		return fmt.Errorf("the module describes download %v, the manifest %v", have.Download, want.Download)
	}
	ext := func(list []wire.UIExternal) []string {
		out := make([]string, 0, len(list))
		for _, e := range list {
			out = append(out, e.As+" "+e.URL+" "+strings.ToLower(e.SHA256))
		}
		return out
	}
	if !sameStrings(ext(want.External), ext(have.External)) {
		return fmt.Errorf("the module describes other external addresses than the manifest")
	}
	return nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// mirrorPath is where a mirrored external file is served inside the package:
// `ext/<host>/<path>` — the address with its scheme dropped, so an author
// writes the one from the other without looking anything up.
func mirrorPath(raw string) string {
	u, _ := url.Parse(raw)
	return "ext/" + u.Host + u.EscapedPath()
}

// checkUI validates the `ui` block and every view's `ui`, and appends the
// interface's derived permissions (with their reasons) to Perms. Called by
// Validate once Perms holds the manifest's own list.
func (m *Manifest) checkUI() error {
	if m.UI == nil {
		if len(m.NewDocuments) > 0 {
			return fmt.Errorf("manifest: new_documents open in the app's interface, and the manifest has no ui block")
		}
		for i, v := range m.Views {
			if v.UI != "" {
				return fmt.Errorf("manifest: views[%d] (%s) opens %q, but the manifest has no ui block", i, v.ID, v.UI)
			}
		}
		return nil
	}
	ui := m.UI
	if s := strings.ToLower(strings.TrimSpace(ui.Bundle.SHA256)); s != "" {
		if !sha256Re.MatchString(s) {
			return fmt.Errorf("manifest: ui.bundle.sha256 must be 64 hex characters")
		}
		ui.Bundle.SHA256 = s
	}
	perms := []Permission{PermUI}
	seenCSP := map[string]bool{}
	for i, c := range ui.CSP {
		c = strings.TrimSpace(c)
		p, ok := uiCSPPerm[c]
		if !ok {
			return fmt.Errorf("manifest: ui.csp[%d] %q: the exceptions an interface may ask for are \"unsafe-eval\" and \"wasm-unsafe-eval\"", i, c)
		}
		if !seenCSP[c] {
			seenCSP[c] = true
			perms = append(perms, p)
		}
		ui.CSP[i] = c
	}
	if ui.PackageFetch {
		perms = append(perms, PermUIPackageFetch)
	}
	if ui.Download {
		perms = append(perms, PermUIDownload)
	}
	if len(ui.External) > maxUIExternal {
		return fmt.Errorf("manifest: ui.external: at most %d addresses", maxUIExternal)
	}
	reasons := map[Permission]wire.Text{}
	seenURL := map[string]bool{}
	for i := range ui.External {
		e := &ui.External[i]
		e.URL = strings.TrimSpace(e.URL)
		e.As = strings.ToLower(strings.TrimSpace(e.As))
		switch {
		case e.As == "script":
			return fmt.Errorf("manifest: ui.external[%d]: a script is never loaded from outside the package — the package's sha256 is what the administrator approved, and a script from anywhere else would make it mean nothing. Put the script in the bundle", i)
		case e.As == "connect":
			return fmt.Errorf("manifest: ui.external[%d]: an interface does not talk to the network. Ask your module (engine.call) for the data; the module's http:<host> permission is approved by the administrator and every request goes through the server", i)
		case !uiExternalAs[e.As]:
			return fmt.Errorf("manifest: ui.external[%d]: as %q must be style, font, img or media", i, e.As)
		}
		e.SHA256 = strings.ToLower(strings.TrimSpace(e.SHA256))
		mirrored := e.SHA256 != ""
		if mirrored && !sha256Re.MatchString(e.SHA256) {
			return fmt.Errorf("manifest: ui.external[%d]: sha256 must be 64 hex characters", i)
		}
		if err := checkExternalURL(e.URL, mirrored); err != nil {
			return fmt.Errorf("manifest: ui.external[%d]: %w", i, err)
		}
		if seenURL[e.URL] {
			return fmt.Errorf("manifest: ui.external: %q is named twice", e.URL)
		}
		seenURL[e.URL] = true
		for _, lang := range m.Languages {
			if strings.TrimSpace(e.Reason[lang]) == "" {
				return fmt.Errorf("manifest: ui.external[%d] (%s) has no %q reason, but the app declares that language — the administrator reads it before allowing the address", i, e.URL, lang)
			}
		}
		if !mirrored {
			p := UINetPermission(e.As, e.URL)
			perms = append(perms, p)
			reasons[p] = e.Reason
		}
	}
	views := 0
	for i := range m.Views {
		v := &m.Views[i]
		if v.UI == "" {
			continue
		}
		if !uiPathOK(v.UI) {
			return fmt.Errorf("manifest: views[%d] (%s): ui %q is not a relative path inside the bundle", i, v.ID, v.UI)
		}
		if ext := strings.ToLower(path.Ext(v.UI)); ext != ".html" && ext != ".htm" {
			return fmt.Errorf("manifest: views[%d] (%s): ui %q must be an .html file", i, v.ID, v.UI)
		}
		views++
		if v.Placement == "viewer" {
			for _, e := range v.Applies.Ext {
				perms = append(perms, Permission(permPrefixUIViewer+"."+e))
			}
			for _, mt := range v.Applies.Mime {
				perms = append(perms, Permission(permPrefixUIViewer+mt))
			}
		}
	}
	if views == 0 {
		return fmt.Errorf("manifest: the ui block is opened by no view — give a view `ui: \"index.html\"`")
	}
	docPerms, err := m.checkNewDocuments()
	if err != nil {
		return err
	}
	perms = append(perms, docPerms...)
	seenPerm := map[Permission]bool{}
	for _, p := range perms {
		if !seenPerm[p] {
			seenPerm[p] = true
			m.Perms = append(m.Perms, p)
		}
	}
	m.uiReasons = reasons
	return nil
}

// uiOnlyPerm: the permissions an app without a module may hold — what the
// bridge answers to (files:read, files:write, settings) and the interface's
// own. Everything else is used by a module's host functions.
func uiOnlyPerm(p Permission) bool {
	switch p {
	case PermFilesRead, PermFilesWrite, PermSettings:
		return true
	}
	return isUIPerm(p)
}

// NeedsModule reports whether anything the manifest declares runs a module:
// an action that does not just open an interface, a view the module draws, a
// public page, or a permission only a module can use. A manifest with an
// interface and none of these may install without a module (an app that is
// ONLY an interface, like draw.io's editor). A language pack never needs one.
func (m *Manifest) NeedsModule() bool {
	if m.IsLanguagePack() {
		return false
	}
	if m.UI == nil || len(m.PublicPages) > 0 {
		return true
	}
	for _, v := range m.Views {
		if v.UI == "" {
			return true
		}
	}
	for _, a := range m.Actions {
		v, ok := m.View(a.View)
		if !ok || v.UI == "" {
			return true
		}
	}
	for _, p := range m.Perms {
		if !uiOnlyPerm(p) {
			return true
		}
	}
	return false
}

// moduleOnlyReason names the first thing that needs a module, for the refusal
// an install without one answers.
func (m *Manifest) moduleOnlyReason() string {
	if m.UI == nil {
		return "it has no interface of its own"
	}
	if len(m.PublicPages) > 0 {
		return "it declares public pages"
	}
	for _, v := range m.Views {
		if v.UI == "" {
			return "view " + v.ID + " is drawn by the module (it has no ui file)"
		}
	}
	for _, a := range m.Actions {
		if v, ok := m.View(a.View); !ok || v.UI == "" {
			return "action " + a.ID + " runs the module"
		}
	}
	var mod []string
	for _, p := range m.Perms {
		if !uiOnlyPerm(p) {
			mod = append(mod, string(p))
		}
	}
	sort.Strings(mod)
	return "the permissions " + strings.Join(mod, ", ") + " are used by a module"
}

// UIViews is every view of m that the interface draws, by id.
func (m *Manifest) UIViews() map[string]*wire.View {
	out := map[string]*wire.View{}
	for i := range m.Views {
		if m.Views[i].UI != "" {
			out[m.Views[i].ID] = &m.Views[i]
		}
	}
	return out
}

// UIInfo is an interface as the admin screens describe it — the install
// review, the upgrade's comparison, the Apps list.
type UIInfo struct {
	SHA256 string `json:"sha256"`
	// Files and Bytes are the bundle's file count and zipped size.
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
	// Unpacked is what the files come to.
	Unpacked int64 `json:"unpacked"`
	// CSP are the script-policy exceptions it asks for.
	CSP []string `json:"csp,omitempty"`
	// External are the addresses outside the package, each mirrored or live.
	External []UIExternalRow `json:"external,omitempty"`
}

// UIExternalRow is one external address as the review shows it.
type UIExternalRow struct {
	URL string `json:"url"`
	As  string `json:"as"`
	// Mode is "mirror" (filex serves its own checked copy; no browser asks
	// the address) or "live" (the reader's browser fetches it).
	Mode   string    `json:"mode"`
	SHA256 string    `json:"sha256,omitempty"`
	Bytes  int64     `json:"bytes,omitempty"`
	Reason wire.Text `json:"reason,omitempty"`
	// Path is where a mirrored file is served inside the package.
	Path string `json:"path,omitempty"`
}

// uiInfoOf describes m's interface; idx (the checked bundle) and sizes (a
// mirrored file's bytes by sha256) may be nil.
func uiInfoOf(m *Manifest, idx *uiIndex, sizes map[string]int64) *UIInfo {
	if m == nil || m.UI == nil {
		return nil
	}
	info := &UIInfo{CSP: append([]string(nil), m.UI.CSP...)}
	if idx != nil {
		info.SHA256, info.Files, info.Bytes, info.Unpacked = idx.sum, len(idx.files), idx.bytes, idx.unpacked
	}
	for _, e := range m.UI.External {
		row := UIExternalRow{URL: e.URL, As: e.As, Reason: e.Reason, Mode: "live"}
		if e.SHA256 != "" {
			row.Mode, row.SHA256, row.Path = "mirror", e.SHA256, mirrorPath(e.URL)
			row.Bytes = sizes[e.SHA256]
		}
		info.External = append(info.External, row)
	}
	return info
}

// uiInfo describes a loaded interface.
func (p *Installed) uiInfo() *UIInfo {
	b := p.UI()
	if b == nil {
		return uiInfoOf(p.Manifest, nil, nil)
	}
	sizes := map[string]int64{}
	for _, mf := range b.mirrors {
		sizes[filepath.Base(mf.file)] = mf.size
	}
	return uiInfoOf(p.Manifest, b.idx, sizes)
}

// stagedInfo describes a staged interface.
func (s *stagedUI) info(m *Manifest) *UIInfo {
	if s == nil {
		return uiInfoOf(m, nil, nil)
	}
	sizes := map[string]int64{}
	for _, mf := range s.mirrors {
		sizes[mf.ext.SHA256] = int64(len(mf.bytes))
	}
	return uiInfoOf(m, s.idx, sizes)
}
