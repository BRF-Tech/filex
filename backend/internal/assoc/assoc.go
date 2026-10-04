// Package assoc decides which handler opens a kind of file and which draws
// its thumbnail (filex 0.50, docs/APP-PLUGINS.md → Default apps).
//
// Every kind of file - its extension - has two CAPABILITIES, and each an
// ordered list of HANDLERS:
//
//   - open: filex's own viewer ("builtin"), every app interface that opens
//     the kind ("app:<app>/<view>", a `viewer` view) and, for the kinds it
//     opens as a choice (OnlyOfficeOpens: `.csv`, filex 0.51), the document
//     server ("onlyoffice") while OnlyOffice is configured;
//   - thumbnail: the document server ("onlyoffice", for the office kinds it
//     draws, while OnlyOffice is configured), filex's own drawer ("builtin",
//     for the kinds it draws) and every app whose `thumbnails` block names
//     the kind ("app:<app>").
//
// The administrator's RULE for a kind and a capability reorders that list and
// switches handlers off; a kind with no rule keeps the default order. The
// person chooses only among the openers that are on (the explorer keeps that
// choice on the account); thumbnails are the administrator's alone.
//
// ⚠⚠ ONE rule for every caller. The thumbnail pipeline (which handler draws,
// and whether a picture is stale), the explorer's listing (which openers are
// offered) and the server's own refusal of an interface on a kind it was
// switched off for all read Chain - so "off" means the same thing everywhere.
package assoc

import (
	"regexp"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// Capabilities.
const (
	CapOpen      = "open"
	CapThumbnail = "thumbnail"
)

// Builtin is filex's own handler: its viewer, or its thumbnail drawer.
const Builtin = "builtin"

// OnlyOffice is the document server filex is configured with, as a handler of
// filex's own (not an app's):
//
//   - thumbnail (filex 0.50): a picture of an office document's first page
//     (docs/thumbnails.md → Office through OnlyOffice), for the kinds it draws
//     while OnlyOffice is configured, first in their default order;
//   - open (filex 0.51): ONLYOFFICE's spreadsheet editor, for the kinds whose
//     opening is a choice between it and filex's own viewer (OnlyOfficeOpens,
//     `.csv` only), first in their default order while OnlyOffice is
//     configured (docs/ONLYOFFICE.md → CSV files).
//
// ⚠ The office kinds themselves (docx, xlsx, ...) are not opened through this
// chain: the document server is the only thing that opens them, there is
// nothing to choose.
const OnlyOffice = "onlyoffice"

// onlyOfficeOpenKinds are the kinds the document server opens as one choice
// among others: filex's own viewer opens them too (a read-only table for a
// CSV), so "Open with" offers both and the administrator and the person may
// pick. ⚠ The ONE list; packages/core lib/appViewer OFFICE_OPEN_KINDS is its
// twin and a test holds the two together.
var onlyOfficeOpenKinds = []string{"csv"}

// OnlyOfficeOpens reports whether the document server is an open handler of
// this kind (an extension, as ExtOf gives it).
func OnlyOfficeOpens(ext string) bool {
	for _, k := range onlyOfficeOpenKinds {
		if k == ext {
			return true
		}
	}
	return false
}

// OnlyOfficeOpenKinds is the list OnlyOfficeOpens answers from, sorted.
func OnlyOfficeOpenKinds() []string { return append([]string(nil), onlyOfficeOpenKinds...) }

// appPrefix starts every app handler's id.
const appPrefix = "app:"

// ValidCapability reports whether c is a capability.
func ValidCapability(c string) bool { return c == CapOpen || c == CapThumbnail }

// extRe is what a kind may be: a file name's extension, lower-case, no dot.
var extRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_+-]{0,31}$`)

// ValidExt reports whether ext may name a kind.
func ValidExt(ext string) bool { return extRe.MatchString(ext) }

// ExtOf is the kind of a file name: its last extension, lower-case, no dot;
// "" for a name without one (or one that is only a dot-file's name).
func ExtOf(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 || dot == len(name)-1 {
		return ""
	}
	ext := strings.ToLower(name[dot+1:])
	if !ValidExt(ext) {
		return ""
	}
	return ext
}

// Handler is one way to open, or to draw, a kind of file.
type Handler struct {
	// ID is "builtin", "app:<app>/<view>" (open) or "app:<app>" (thumbnail).
	ID string `json:"id"`
	// App and View name an app's handler; both empty for the built-in one.
	App  string `json:"app,omitempty"`
	View string `json:"view,omitempty"`
	// Version is the app's version: part of what a thumbnail records about who
	// drew it, so an upgrade makes its pictures stale.
	Version string `json:"version,omitempty"`
	// Label is the app's (or the view's) name, in its languages; empty for the
	// built-in handler, which every client names itself.
	Label wire.Text `json:"label,omitempty"`
}

// IsBuiltin reports whether h is filex's own handler.
func (h Handler) IsBuiltin() bool { return h.ID == Builtin }

// IsOnlyOffice reports whether h is the document server (its thumbnail
// handler, or its editor for a kind it opens as a choice).
func (h Handler) IsOnlyOffice() bool { return h.ID == OnlyOffice }

// IsApp reports whether h is an app's handler (not one of filex's own).
func (h Handler) IsApp() bool { return strings.HasPrefix(h.ID, appPrefix) }

// Key is how a thumbnail row records the handler: the id, and an app's
// version (`app:pkglist@1.2.0`), so the same app in another version is a
// different handler for freshness.
func (h Handler) Key() string {
	if h.IsBuiltin() || h.Version == "" {
		return h.ID
	}
	return h.ID + "@" + h.Version
}

// OpenID is an app view's handler id for the open capability.
func OpenID(app, view string) string { return appPrefix + app + "/" + view }

// ThumbID is an app's handler id for the thumbnail capability.
func ThumbID(app string) string { return appPrefix + app }

// AppOf is the app an app handler's id names ("" for builtin or a malformed
// id).
func AppOf(id string) string {
	rest, ok := strings.CutPrefix(id, appPrefix)
	if !ok {
		return ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

var (
	appNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	viewIDRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
)

// ValidID reports whether id is a handler id of the capability's shape.
func ValidID(capability, id string) bool {
	if id == Builtin {
		return true
	}
	if id == OnlyOffice {
		return capability == CapThumbnail || capability == CapOpen
	}
	rest, ok := strings.CutPrefix(id, appPrefix)
	if !ok {
		return false
	}
	app, view, hasView := strings.Cut(rest, "/")
	if !appNameRe.MatchString(app) {
		return false
	}
	switch capability {
	case CapOpen:
		return hasView && viewIDRe.MatchString(view)
	case CapThumbnail:
		return !hasView
	}
	return false
}

// AppHandler is an app's handler and the kinds it handles: extensions, and
// media types (exact, or a family like `image/*`).
type AppHandler struct {
	Handler
	Ext  []string
	Mime []string
}

// Matches reports whether the handler handles a file of this extension (as
// ExtOf gives it) or this media type.
func (a AppHandler) Matches(ext, mime string) bool {
	if ext != "" {
		for _, e := range a.Ext {
			if e == ext {
				return true
			}
		}
	}
	m := normMime(mime)
	if m == "" {
		return false
	}
	for _, want := range a.Mime {
		if want == m || (strings.HasSuffix(want, "/*") && strings.HasPrefix(m, strings.TrimSuffix(want, "*"))) {
			return true
		}
	}
	return false
}

// normMime is a media type without parameters, lower-case.
func normMime(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	return m
}

// Rule is the administrator's decision for one kind and one capability.
type Rule struct {
	// Order are the handlers asked first, in this order.
	Order []string `json:"order"`
	// Off are the handlers switched off for this kind.
	Off []string `json:"off"`
}

// Apply orders the available handlers (in their default order) by r:
// the ones r switches off are left out; the ones r names come first, in r's
// order; every other one follows in the default order. A nil rule is the
// default order. `off` is the handlers left out, in the default order.
//
// ⚠ A handler the rule does not name (an app installed after the rule was
// written) is ON, after the named ones: installing an app never needs the
// rule to be rewritten for it to work, and it never jumps the queue.
func Apply(avail []Handler, r *Rule) (on, off []Handler) {
	if r == nil {
		return append([]Handler(nil), avail...), nil
	}
	isOff := map[string]bool{}
	for _, id := range r.Off {
		isOff[id] = true
	}
	byID := map[string]Handler{}
	for _, h := range avail {
		byID[h.ID] = h
	}
	placed := map[string]bool{}
	for _, id := range r.Order {
		h, ok := byID[id]
		if !ok || isOff[id] || placed[id] {
			continue
		}
		placed[id] = true
		on = append(on, h)
	}
	for _, h := range avail {
		switch {
		case isOff[h.ID]:
			off = append(off, h)
		case !placed[h.ID]:
			on = append(on, h)
		}
	}
	return on, off
}

// Chain is the handlers of one kind and one capability: the ones that are on,
// in the order they are asked, and the ones switched off.
type Chain struct {
	On  []Handler
	Off []Handler
	// Custom: an administrator's rule decided this order (false: the default).
	Custom bool
}

// AllOff reports whether handlers exist for the kind and every one of them is
// switched off - as opposed to a kind nobody handles.
func (c Chain) AllOff() bool { return len(c.On) == 0 && len(c.Off) > 0 }

// Has reports whether the handler id is on in the chain.
func (c Chain) Has(id string) bool {
	for _, h := range c.On {
		if h.ID == id {
			return true
		}
	}
	return false
}

// Keys are the chain's handlers as a thumbnail row records them (Key).
func (c Chain) Keys() []string {
	out := make([]string, len(c.On))
	for i, h := range c.On {
		out[i] = h.Key()
	}
	return out
}

// DefaultOrder is the order a kind's handlers are asked in until an
// administrator decides otherwise:
//
//   - open: the apps first (in the order given: by app name, then each app's
//     views in its manifest's order), filex's own viewer last - what 0.49 did,
//     where an installed app opened its kind and *Open with* offered the rest;
//   - thumbnail: filex's own drawer first (when it draws the kind), then the
//     apps by name - an app is asked for what filex could not draw.
func DefaultOrder(capability string, builtin bool, apps []Handler) []Handler {
	out := make([]Handler, 0, len(apps)+1)
	b := Handler{ID: Builtin}
	if capability == CapThumbnail && builtin {
		out = append(out, b)
	}
	out = append(out, apps...)
	if capability == CapOpen {
		out = append(out, b)
	}
	return out
}

// OpenDefault is the open capability's default order: the document server
// first for a kind it opens as a choice while OnlyOffice is configured
// (onlyoffice: the product's default for a CSV is ONLYOFFICE's spreadsheet,
// filex 0.51), then DefaultOrder - the apps, filex's own viewer last.
func OpenDefault(onlyoffice bool, apps []Handler) []Handler {
	out := DefaultOrder(CapOpen, true, apps)
	if onlyoffice {
		out = append([]Handler{{ID: OnlyOffice}}, out...)
	}
	return out
}

// ThumbDefault is the thumbnail capability's default order: the document
// server first for a kind it draws (onlyoffice: a real page of an office
// document), then DefaultOrder - filex's own drawer when it draws the kind,
// then the apps by name.
func ThumbDefault(onlyoffice, builtin bool, apps []Handler) []Handler {
	out := DefaultOrder(CapThumbnail, builtin, apps)
	if onlyoffice {
		out = append([]Handler{{ID: OnlyOffice}}, out...)
	}
	return out
}

// Clean normalises a rule a caller wrote: ids trimmed, duplicates dropped, an
// id in both lists kept as off. It does not judge whether an id exists.
func Clean(r Rule) Rule {
	seen := map[string]bool{}
	off := []string{}
	for _, id := range r.Off {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		off = append(off, id)
	}
	order := []string{}
	for _, id := range r.Order {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		order = append(order, id)
	}
	return Rule{Order: order, Off: off}
}

// sortHandlers orders app handlers by app name, then by the order they were
// given within one app (a stable sort).
func sortHandlers(hs []AppHandler) {
	sort.SliceStable(hs, func(i, j int) bool { return hs[i].App < hs[j].App })
}
