package plugintest

import (
	"fmt"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// CheckManifest measures the manifest against what filex refuses at
// install, and fails t on a breach.
func CheckManifest(t TB, m wire.Manifest) {
	t.Helper()
	InspectManifest(m).Report(t, false)
}

// InspectManifest reads a manifest the way the host's installer does:
// the name shape, the closed permission set, unique ids, an action whose
// view exists, a placement filex can draw, and a settings form that obeys
// the same field rules as a screen.
func InspectManifest(m wire.Manifest) Report {
	var r Report
	if m.ManifestVersion != 0 && m.ManifestVersion != wire.ProtocolVersion {
		r = r.err("manifest.manifest_version", "this host speaks version %d, the manifest says %d", wire.ProtocolVersion, m.ManifestVersion)
	}
	if !namePattern.MatchString(m.Name) {
		r = r.err("manifest.name", "%q is not a plugin name: [a-z0-9][a-z0-9_-]{0,31}", m.Name)
	}
	if strings.TrimSpace(m.Version) == "" {
		r = r.err("manifest.version", "a plugin without a version cannot be upgraded")
	}
	if len(m.Label) == 0 {
		r = r.err("manifest.label", "the plugin has no label; it would be listed as its bare name")
	}

	for i, p := range m.Permissions {
		w := fmt.Sprintf("manifest.permissions[%d]", i)
		switch {
		case Permissions[p]:
		case strings.HasPrefix(p, "engines:"):
			name := strings.TrimPrefix(p, "engines:")
			if !KnownEngines[name] {
				r = r.err(w, "unknown engine %q", name)
			}
		case strings.HasPrefix(p, "http:"):
			host := strings.TrimPrefix(p, "http:")
			if host == "" || strings.ContainsAny(host, "/ \t") {
				r = r.err(w, "expected http:<host or *.host>, got %q", p)
			}
		case strings.HasPrefix(p, "events:"):
			// Refused by the host too (internal/wasmplugin/permissions.go):
			// nothing delivers a file event to an app yet, so the grant would
			// mean nothing to the administrator who approved it.
			r = r.err(w, "filex does not deliver file events to apps yet, so %q would grant nothing — leave it out", p)
		default:
			r = r.err(w, "unknown permission %q — the set is closed, and a grant the administrator cannot read is not a grant", p)
		}
	}

	langs := Languages(m)
	hasEN := false
	for i, l := range m.Languages {
		if !langTag.MatchString(l) {
			r = r.err(fmt.Sprintf("manifest.languages[%d]", i), "%q is not a language tag", l)
		}
		if l == "en" {
			hasEN = true
		}
	}
	if len(m.Languages) > 0 && !hasEN {
		r = r.err("manifest.languages", "English is required everywhere a Text appears, so it belongs in the declared list")
	}
	// ⚠ The same limits the host refuses at install (wire/langpack.go), so an
	// author meets them at `go test` rather than in an administrator's panel.
	// The GRAMMAR of the strings — placeholders, plural forms, the escapes the
	// admin panel's vue-i18n needs — is checked by scripts/i18n-validate.mjs
	// against the catalogue, which this package does not carry.
	total := 0
	for tag, keys := range m.UILocales {
		w := "manifest.ui_locales." + tag
		if !langTag.MatchString(strings.ToLower(tag)) {
			r = r.err("manifest.ui_locales", "%q is not a language tag", tag)
		}
		if len(keys) == 0 {
			r = r.err(w, "an empty language pack adds a language the interface cannot speak — the host refuses it")
		}
		size := 0
		for k, v := range keys {
			if !wire.UILocaleKeyOK(k) {
				r = r.err(w, "%q is not a filex string key (dotted segments of letters, digits, _ and -, at most %d bytes)", k, wire.MaxUILocaleKeyBytes)
			}
			if len(v) > wire.MaxUILocaleValueBytes {
				r = r.err(w, "%s is %d bytes, at most %d", k, len(v), wire.MaxUILocaleValueBytes)
			}
			size += len(k) + len(v)
		}
		if size > wire.MaxUILocaleBytes {
			r = r.err(w, "%d bytes of strings, at most %d", size, wire.MaxUILocaleBytes)
		}
		total += size
	}
	if total > wire.MaxUILocalesBytes {
		r = r.err("manifest.ui_locales", "%d bytes of strings across its languages, at most %d", total, wire.MaxUILocalesBytes)
	}

	seen := map[string]bool{}
	views := map[string]wire.View{}
	for _, v := range m.Views {
		views[v.ID] = v
	}
	for i, a := range m.Actions {
		w := fmt.Sprintf("manifest.actions[%d] (%s)", i, a.ID)
		if a.ID == "" {
			r = r.err(w, "an action with no id")
		}
		if seen["a:"+a.ID] {
			r = r.err(w, "two actions share the id %q", a.ID)
		}
		seen["a:"+a.ID] = true
		switch a.Output.Mode {
		case "sibling", "version", "none":
		case "":
			r = r.err(w, "output.mode is required (sibling|version|none)")
		case "folder":
			r = r.err(w, "output.mode folder is the person's choice for one job (JobRequest.Output); declare sibling with output.elsewhere")
		default:
			r = r.err(w, "unknown output.mode %q (sibling|version|none)", a.Output.Mode)
		}
		if a.Output.Mode != "none" && !hasPerm(m, "files:write") {
			r = r.err(w, "the action writes files (output.mode %q) but the manifest never asks for files:write", a.Output.Mode)
		}
		if a.Output.Elsewhere && a.Output.Mode != "sibling" {
			r = r.err(w, "output.elsewhere is for a new file (mode sibling), not %q", a.Output.Mode)
		}
		if a.Output.Dir != "" {
			r = r.err(w, "output.dir is chosen per job, not declared in the manifest")
		}
		// ⚠ A flow that ends in a write must say so, or filex offers it on a
		// read-only storage and the app's first screen can only refuse.
		if a.Applies.Writable && !hasPerm(m, "files:write") {
			r = r.err(w, "applies.writable says the flow writes the file, but the manifest never asks for files:write")
		}
		if a.View != "" {
			if _, ok := views[a.View]; !ok {
				r = r.err(w, "opens the view %q, which the manifest does not declare", a.View)
			}
		}
		switch a.MinRole {
		case "", "viewer", "editor", "owner":
		default:
			r = r.err(w, "unknown min_role %q (viewer|editor|owner)", a.MinRole)
		}
		r = append(r, inspectApplies(w, a.Applies)...)
		if a.Hidden && a.Applies.Kind != "" {
			r = r.warn(w, "a hidden action is never in a menu, so its `applies` is only documentation")
		}
	}

	for i, v := range m.Views {
		w := fmt.Sprintf("manifest.views[%d] (%s)", i, v.ID)
		if v.ID == "" {
			r = r.err(w, "a view with no id")
		}
		if seen["v:"+v.ID] {
			r = r.err(w, "two views share the id %q", v.ID)
		}
		seen["v:"+v.ID] = true
		switch v.Placement {
		case "modal", "page", "inspector", "home":
		case "":
			r = r.err(w, "placement is required (modal|page|inspector|home)")
		default:
			r = r.err(w, "unknown placement %q (modal|page|inspector|home)", v.Placement)
		}
		if v.Placement == "inspector" && v.Applies.Kind == "" && len(v.Applies.Ext) == 0 && len(v.Applies.Mime) == 0 {
			r = r.err(w, "an inspector section needs `applies`, or it is shown on every file")
		}
		r = append(r, inspectApplies(w, v.Applies)...)
	}

	for i, p := range m.PublicPages {
		w := fmt.Sprintf("manifest.public_pages[%d] (%s)", i, p.ID)
		if p.ID == "" {
			r = r.err(w, "a public page with no id")
		}
		if seen["p:"+p.ID] {
			r = r.err(w, "two public pages share the id %q", p.ID)
		}
		seen["p:"+p.ID] = true
		switch p.PIN {
		case "", "optional", "required", "none":
		default:
			r = r.err(w, "unknown pin policy %q (optional|required|none)", p.PIN)
		}
		if p.MaxTTLDays > 0 && p.DefaultTTLDays > p.MaxTTLDays {
			r = r.err(w, "the default TTL (%d days) is past the ceiling (%d)", p.DefaultTTLDays, p.MaxTTLDays)
		}
		if !hasPerm(m, "public_pages") {
			r = r.err(w, "the manifest declares a public page but never asks for the public_pages permission")
		}
	}

	r = append(r, inspectFields("manifest.settings", m.Settings, nil)...)
	if len(m.Settings) > 0 && !hasPerm(m, "settings") {
		r = r.warn("manifest.settings", "the administrator can fill these in, but the plugin never asks for the `settings` permission, so it can only read the non-secret ones a call carries")
	}
	_ = langs
	return r
}

func hasPerm(m wire.Manifest, p string) bool {
	for _, x := range m.Permissions {
		if x == p {
			return true
		}
	}
	return false
}

func inspectApplies(where string, a wire.Applies) Report {
	var r Report
	switch a.Kind {
	case "", "file", "dir", "any":
	default:
		r = r.err(where, "unknown applies.kind %q (file|dir|any)", a.Kind)
	}
	for _, e := range a.Ext {
		if e != strings.ToLower(e) || strings.HasPrefix(e, ".") {
			r = r.err(where, "applies.ext %q must be lower-case and without the dot", e)
		}
	}
	if a.Min > 0 && a.Max > 0 && a.Min > a.Max {
		r = r.err(where, "applies.min (%d) is above applies.max (%d)", a.Min, a.Max)
	}
	if (a.Min > 1 || a.Max > 1) && !a.Multi {
		r = r.warn(where, "applies names a range but is not `multi`, so only one file is ever offered")
	}
	for _, list := range [][]string{a.State, a.NoState} {
		for _, k := range list {
			if at := strings.Index(k, "@"); at >= 0 && (at == 0 || k[at:] != wire.PersonalStateSuffix) {
				r = r.err(where, "applies state %q: a personal key is named `<key>@me` (wire.PersonalState writes `<key>@<user id>`)", k)
			}
		}
	}
	return r
}

// CheckRegistered measures the plugin against its own manifest: every
// action, view and public page the manifest advertises has a function
// behind it, and nothing is registered that the manifest never declared.
// A menu row that opens a screen nobody wrote is the failure this catches.
func CheckRegistered(t TB, p *pluginkit.Plugin) {
	t.Helper()
	InspectRegistered(p).Report(t, false)
}

// InspectRegistered is CheckRegistered's finding list.
func InspectRegistered(p *pluginkit.Plugin) Report {
	var r Report
	if p == nil {
		return r.err("plugin", "no plugin registered")
	}
	m := p.Manifest
	for _, a := range m.Actions {
		if p.Actions[a.ID] == nil {
			r = r.err("plugin.actions", "the manifest offers the action %q, but nothing is registered under that id — the menu row would fail on every file", a.ID)
		}
	}
	for id := range p.Actions {
		if !hasAction(m, id) {
			r = r.err("plugin.actions", "an action %q is registered that the manifest never declares; filex would never call it", id)
		}
	}
	for _, v := range m.Views {
		if p.Views[v.ID] == nil {
			r = r.err("plugin.views", "the manifest declares the view %q, but nothing is registered to draw it", v.ID)
		}
	}
	for id := range p.Views {
		if !hasView(m, id) {
			r = r.err("plugin.views", "a view %q is registered that the manifest never declares", id)
		}
	}
	for _, pg := range m.PublicPages {
		if p.Pages[pg.ID] == nil {
			r = r.err("plugin.pages", "the manifest declares the public page %q, but nothing is registered to draw it — the link would open on nothing", pg.ID)
		}
	}
	for id := range p.Pages {
		if _, ok := pageByID(m, id); !ok {
			r = r.err("plugin.pages", "a public page %q is registered that the manifest never declares", id)
		}
	}
	return r
}
