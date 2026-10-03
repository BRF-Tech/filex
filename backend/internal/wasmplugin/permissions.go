package wasmplugin

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// Permission is one capability a plugin asks for in its manifest and an
// administrator grants at install. The set is CLOSED: a manifest naming a
// permission this host does not know is refused at install, because a grant
// the admin cannot read is not a grant.
//
// Two shapes: bare names ("files:read") and parameterised ones
// ("http:api.example.com", "engines:ffmpeg"). The parameter is part of the
// permission — granting "http:a.example.com" says nothing about b.example.com.
type Permission string

// Bare permissions.
const (
	PermFilesRead   Permission = "files:read"
	PermFilesWrite  Permission = "files:write"
	PermFilesLock   Permission = "files:lock"
	PermSign        Permission = "sign"
	PermMailSend    Permission = "mail:send"
	PermNotifySend  Permission = "notify:send"
	PermUsersLookup Permission = "users:lookup"
	PermSettings    Permission = "settings"
	PermState       Permission = "state"
	PermPublicPages Permission = "public_pages"
	// PermSchedule is the one permission that makes an app run when nobody
	// asked it to: it is woken once an hour and runs the work it scheduled
	// at the minute it named. An app without it is never woken, and there
	// is no other way in — see schedule.go.
	PermSchedule Permission = "schedule"
	// The interface's own permissions (uiperm.go) — DERIVED from the
	// manifest's `ui` block, never listed in `permissions`: `ui` for having
	// an interface at all, `ui:eval` / `ui:wasm-eval` for its script-policy
	// exceptions. Live addresses are `ui-net:<as>:<url>`.
	PermUI         Permission = "ui"
	PermUIEval     Permission = "ui:eval"
	PermUIWasmEval Permission = "ui:wasm-eval"
	// PermUIPackageFetch: the interface reads its own package — this
	// version's files, nothing else (`ui.package_fetch`).
	PermUIPackageFetch Permission = "ui:package-fetch"
	// PermUIDownload: the interface hands the person files to keep on their
	// own disk, each time with their say (`ui.download`).
	PermUIDownload Permission = "ui:download"
)

// Parameterised prefixes.
const (
	permPrefixHTTP    = "http:"
	permPrefixEngines = "engines:"
	// permPrefixUINet is a live address an interface loads from:
	// `ui-net:<as>:<url>` (uiperm.go).
	permPrefixUINet = "ui-net:"
	// permPrefixUIViewer is a kind of file the app's interface opens IN
	// PLACE OF filex's preview (a `viewer` view): `ui-viewer:.drawio`,
	// `ui-viewer:image/svg+xml`. Derived, like every `ui` permission, so an
	// update that makes the app the viewer of one more kind is a new grant
	// (security review UI-6).
	permPrefixUIViewer = "ui-viewer:"
	// permPrefixUINew is a kind of file the app adds to filex's "New" menu:
	// `ui-new:.drawio` (`new_documents`).
	permPrefixUINew = "ui-new:"
)

// permPrefixEvents is NOT in the set. `events:<name>` parsed, was granted and
// was listed at the install review as "Is told about {event} events (not
// wired yet)" — a developer's note in the list an administrator reads before
// trusting an app with their files. Nothing delivers a file event to an app:
// `on_event` (pkg/pluginkit/exports_wasm.go) returns 0 and no host code calls
// it. A permission that does nothing is worse than a missing one, because the
// person believes they granted something, so the manifest is refused until
// events are real — the same rule as every other name the host does not know.
const permPrefixEvents = "events:"

// Engines a plugin may ask for are enginebin's list - the binaries the probe
// resolves, the office engine, and `libreoffice`, the office engine's name
// until 0.50 (an alias: an app built for LibreOffice still installs).
// Availability on this server is a separate question; the grant only says the
// plugin may try.
func knownEngine(name string) bool { return enginebin.Known(name) }

// sameEngineGrant is the permission that grants the same engine under its
// other name (`engines:libreoffice` ↔ `engines:office`), "" when there is
// none. The two are ONE grant: an app that moves from one name to the other
// is not asking for anything new, and a grant stored under the old name keeps
// working.
func sameEngineGrant(p Permission) Permission {
	name, ok := strings.CutPrefix(string(p), permPrefixEngines)
	if !ok {
		return ""
	}
	c := enginebin.Canonical(name)
	if c != name {
		return Permission(permPrefixEngines + c)
	}
	if a := enginebin.AliasesOf(c); len(a) > 0 {
		return Permission(permPrefixEngines + a[0])
	}
	return ""
}

var bare = map[Permission]bool{
	PermFilesRead: true, PermFilesWrite: true, PermFilesLock: true, PermSign: true, PermMailSend: true,
	PermNotifySend: true, PermUsersLookup: true, PermSettings: true, PermState: true,
	PermPublicPages: true, PermSchedule: true,
	PermUI: true, PermUIEval: true, PermUIWasmEval: true, PermUIPackageFetch: true, PermUIDownload: true,
}

// ParsePermission validates one manifest entry.
func ParsePermission(s string) (Permission, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("empty permission")
	}
	p := Permission(s)
	if bare[p] {
		return p, nil
	}
	switch {
	case strings.HasPrefix(s, permPrefixHTTP):
		host := strings.ToLower(strings.TrimPrefix(s, permPrefixHTTP))
		if !grantableHost(host) {
			return "", fmt.Errorf("permission %q: expected http:<host> or http:*.<domain> (a wildcard covers the subdomains of a name with at least two labels, never a whole top-level domain)", s)
		}
		return Permission(permPrefixHTTP + host), nil
	case strings.HasPrefix(s, permPrefixEngines):
		name := strings.TrimPrefix(s, permPrefixEngines)
		if !knownEngine(name) {
			return "", fmt.Errorf("permission %q: unknown engine (known: %s)", s, strings.Join(enginebin.Names(), ", "))
		}
		return p, nil
	case strings.HasPrefix(s, permPrefixUINet):
		as, u, err := parseUINet(strings.TrimPrefix(s, permPrefixUINet))
		if err != nil {
			return "", fmt.Errorf("permission %q: %w", s, err)
		}
		return UINetPermission(as, u), nil
	case strings.HasPrefix(s, permPrefixUIViewer):
		kind := strings.TrimPrefix(s, permPrefixUIViewer)
		if !viewerKindOK(kind) {
			return "", fmt.Errorf("permission %q: expected ui-viewer:.<ext> or ui-viewer:<type/subtype>", s)
		}
		return p, nil
	case strings.HasPrefix(s, permPrefixUINew):
		ext, ok := strings.CutPrefix(strings.TrimPrefix(s, permPrefixUINew), ".")
		if !ok || !viewerExtRe.MatchString(ext) {
			return "", fmt.Errorf("permission %q: expected ui-new:.<ext>", s)
		}
		return p, nil
	case strings.HasPrefix(s, permPrefixThumbnail):
		kind := strings.TrimPrefix(s, permPrefixThumbnail)
		if !viewerKindOK(kind) {
			return "", fmt.Errorf("permission %q: expected thumbnail:.<ext> or thumbnail:<type/subtype>", s)
		}
		return p, nil
	case strings.HasPrefix(s, permPrefixEvents):
		return "", fmt.Errorf("permission %q: filex does not deliver file events to apps yet, so this permission would grant nothing - leave it out", s)
	}
	return "", fmt.Errorf("unknown permission %q", s)
}

// hostLabelRe is one DNS label: letters, digits and inner hyphens.
var hostLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// grantableHost is what an `http:` permission may name: a host name, an IP
// literal, or `*.` and a name of at least two labels.
//
// ⚠⚠ The grant is what an administrator reads in the review, so it must mean
// what it says: no bare `*`, no whole top-level domain, no port or user-info,
// nothing HasHost's suffix match would read wider than it looks.
func grantableHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	labels := strings.Split(strings.TrimPrefix(host, "*."), ".")
	if strings.HasPrefix(host, "*.") && len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if !hostLabelRe.MatchString(l) {
			return false
		}
	}
	return true
}

// Grants is the set an administrator approved.
type Grants map[Permission]bool

// NewGrants builds a set from validated permissions.
func NewGrants(perms []Permission) Grants {
	g := Grants{}
	for _, p := range perms {
		g[p] = true
	}
	return g
}

// Has answers a permission. An engine is granted under either of its names
// (sameEngineGrant).
func (g Grants) Has(p Permission) bool {
	if g[p] {
		return true
	}
	if o := sameEngineGrant(p); o != "" {
		return g[o]
	}
	return false
}

// HasEngine answers engines:<name>, under either of the engine's names.
func (g Grants) HasEngine(name string) bool {
	return g.Has(Permission(permPrefixEngines + name))
}

// HasHost answers whether an outbound HTTP request to host is allowed:
// an exact "http:host" grant, or a "http:*.domain" grant covering a subdomain.
func (g Grants) HasHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if g[Permission(permPrefixHTTP+host)] {
		return true
	}
	for p := range g {
		s := string(p)
		if !strings.HasPrefix(s, permPrefixHTTP+"*.") {
			continue
		}
		suffix := strings.TrimPrefix(s, permPrefixHTTP+"*")
		if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
			return true
		}
	}
	return false
}

// AllowedHosts lists the http:* grants as Extism allowed_hosts patterns.
func (g Grants) AllowedHosts() []string {
	out := []string{}
	for p := range g {
		if strings.HasPrefix(string(p), permPrefixHTTP) {
			out = append(out, strings.TrimPrefix(string(p), permPrefixHTTP))
		}
	}
	sort.Strings(out)
	return out
}

// Missing returns the permissions in want that are not in g — what an
// upgrade must ask the administrator to approve again.
func (g Grants) Missing(want []Permission) []Permission {
	var out []Permission
	for _, p := range want {
		if !g.Has(p) {
			out = append(out, p)
		}
	}
	return out
}

// Label is the plain-language meaning shown at install, in lang — any
// language the server catalogue speaks, a language pack's included (keys
// `server.perm.*`, internal/srvtext). It serves the API (`GET /api/admin/
// app-plugins/{id}` → permission review) and the CLI.
//
// ⚠ This is what an administrator reads before trusting an app with their
// files. It was English or Turkish — a Spanish administrator read the one
// screen that asks for consent in English, around a Spanish wizard. A key a
// pack lacks falls back to English, never to the permission id.
func (p Permission) Label(lang string) string {
	s := string(p)
	switch {
	case p == PermFilesRead:
		return permText(lang, "files_read", nil)
	case p == PermFilesWrite:
		return permText(lang, "files_write", nil)
	case p == PermFilesLock:
		return permText(lang, "files_lock", nil)
	case p == PermSign:
		return permText(lang, "sign", nil)
	case p == PermMailSend:
		return permText(lang, "mail_send", nil)
	case p == PermNotifySend:
		return permText(lang, "notify_send", nil)
	case p == PermUsersLookup:
		return permText(lang, "users_lookup", nil)
	case p == PermSettings:
		return permText(lang, "settings", nil)
	case p == PermState:
		return permText(lang, "state", nil)
	case p == PermPublicPages:
		return permText(lang, "public_pages", nil)
	case p == PermSchedule:
		return permText(lang, "schedule", nil)
	case p == PermUI:
		return permText(lang, "ui", nil)
	case p == PermUIEval:
		return permText(lang, "ui_eval", nil)
	case p == PermUIWasmEval:
		return permText(lang, "ui_wasm_eval", nil)
	case p == PermUIPackageFetch:
		return permText(lang, "ui_package_fetch", nil)
	case p == PermUIDownload:
		return permText(lang, "ui_download", nil)
	case strings.HasPrefix(s, permPrefixUINet):
		as, u, _ := parseUINet(strings.TrimPrefix(s, permPrefixUINet))
		return permText(lang, "ui_net_"+as, srvtext.Vars{"url": u})
	case strings.HasPrefix(s, permPrefixUIViewer):
		return permText(lang, "ui_viewer", srvtext.Vars{"kind": strings.TrimPrefix(s, permPrefixUIViewer)})
	case strings.HasPrefix(s, permPrefixUINew):
		return permText(lang, "ui_new", srvtext.Vars{"ext": strings.TrimPrefix(s, permPrefixUINew)})
	case strings.HasPrefix(s, permPrefixThumbnail):
		return permText(lang, "thumbnail", srvtext.Vars{"kind": strings.TrimPrefix(s, permPrefixThumbnail)})
	case strings.HasPrefix(s, permPrefixHTTP):
		h := strings.TrimPrefix(s, permPrefixHTTP)
		return permText(lang, "http", srvtext.Vars{"host": h})
	case strings.HasPrefix(s, permPrefixEngines):
		// The engine by its product name ("ImageMagick"), not its permission
		// id: this sentence is what the install review and the app's page
		// print for a person to read.
		e := enginebin.DisplayName(strings.TrimPrefix(s, permPrefixEngines))
		return permText(lang, "engines", srvtext.Vars{"engine": e})
	}
	return s
}

// permText is one `server.perm.<key>` sentence in lang.
func permText(lang, key string, vars srvtext.Vars) string {
	return srvtext.Text(lang, srvtext.Prefix+"perm."+key, vars)
}
