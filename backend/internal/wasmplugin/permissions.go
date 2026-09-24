package wasmplugin

import (
	"fmt"
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
)

// Parameterised prefixes.
const (
	permPrefixHTTP    = "http:"
	permPrefixEngines = "engines:"
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

// Engines a plugin may ask for are enginebin's list — the same table the
// probe resolves, so a grant can never name an engine the server would not
// know how to find. Availability on this server is a separate question
// (enginebin.Probe); the grant only says the plugin may try.
func knownEngine(name string) bool { _, ok := enginebin.Candidates[name]; return ok }

var bare = map[Permission]bool{
	PermFilesRead: true, PermFilesWrite: true, PermFilesLock: true, PermSign: true, PermMailSend: true,
	PermNotifySend: true, PermUsersLookup: true, PermSettings: true, PermState: true,
	PermPublicPages: true, PermSchedule: true,
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
		host := strings.TrimPrefix(s, permPrefixHTTP)
		if host == "" || strings.ContainsAny(host, "/ \t") {
			return "", fmt.Errorf("permission %q: expected http:<host or *.host>", s)
		}
		return Permission(permPrefixHTTP + strings.ToLower(host)), nil
	case strings.HasPrefix(s, permPrefixEngines):
		name := strings.TrimPrefix(s, permPrefixEngines)
		if !knownEngine(name) {
			return "", fmt.Errorf("permission %q: unknown engine (known: %s)", s, strings.Join(enginebin.Names(), ", "))
		}
		return p, nil
	case strings.HasPrefix(s, permPrefixEvents):
		return "", fmt.Errorf("permission %q: filex does not deliver file events to apps yet, so this permission would grant nothing — leave it out", s)
	}
	return "", fmt.Errorf("unknown permission %q", s)
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

// Has answers a bare permission.
func (g Grants) Has(p Permission) bool { return g[p] }

// HasEngine answers engines:<name>.
func (g Grants) HasEngine(name string) bool {
	return g[Permission(permPrefixEngines+name)]
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
		if !g[p] {
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
