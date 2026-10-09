// Package handlers - app_install_words.go
//
// An app's install, upgrade and update refused, said by the server in the
// reader's language (wasmplugin.InstallRefusal.Said, `server.install.*`).
//
// ⚠⚠ Why (0.55). The refusal carried Go's English in `message`, and the
// install wizard and the Apps list built the sentence in the browser from
// the code and the fields (web lib/appPluginRefusal.ts and the locales'
// appPlugins.wizard.errors.*) - a second copy of every sentence, which the
// command line and an agent never had. The panel now prints `message`.
package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// writeAppFailure answers a registry error in lang: an install's refusal
// with the status that says whose move it is and its sentence, anything
// else as internal_error with Go's words as `detail`. AppPluginsAdmin.fail
// is it for a request; PluginRequests.fail, which has only the language,
// calls it directly.
func writeAppFailure(w http.ResponseWriter, lang string, err error) {
	var ie *wasmplugin.InstallError
	if errors.As(err, &ie) {
		code := http.StatusBadRequest
		switch ie.Code {
		case wasmplugin.ErrCodeNameTaken, wasmplugin.ErrCodeDescribeMismatch, wasmplugin.ErrCodePermissionsChanged, wasmplugin.ErrCodeIncompatible:
			code = http.StatusConflict
		case wasmplugin.ErrCodeNotFound:
			code = http.StatusNotFound
		case wasmplugin.ErrCodeDemo:
			code = http.StatusForbidden
		case wasmplugin.ErrCodeTooLarge:
			code = http.StatusRequestEntityTooLarge
		case wasmplugin.ErrCodeUpToDate:
			code = http.StatusConflict
		case wasmplugin.ErrCodeFetch:
			code = http.StatusBadGateway
		}
		writeJSON(w, code, installErrorBody(lang, ie))
		return
	}
	writeJSON(w, http.StatusInternalServerError, apierr.Map(lang, "internal_error", nil, map[string]any{"detail": err.Error()}))
}

// writeInstallRefusal answers a refusal of an install's body - the code the
// wizard has always read (manifest_invalid, too_large), with Go's English as
// `detail` - said by the sentence `server.install.<said>` (one code, the
// reason), filled with params.
func writeInstallRefusal(w http.ResponseWriter, r *http.Request, status int, code, said, detail string, params srvtext.Vars) {
	lang := langOf(r)
	body := (&wasmplugin.InstallRefusal{Code: code, Message: detail}).Said(lang)
	if said != "" && srvtext.Has("server.install."+said) {
		body.Message = srvtext.Text(lang, "server.install."+said, params)
	}
	writeJSON(w, status, body)
}

// sayStatus is sayStatusAt with its moments on UTC, for a caller that has
// no reader's clock (the wire fixtures).
func sayStatus(lang string, st *wasmplugin.Status) *wasmplugin.Status {
	return sayStatusAt(lang, utcClock, st)
}

// sayStatusFor is sayStatusAt for the reader of r: their language, their
// clock (readerClock).
func sayStatusFor(r *http.Request, st *wasmplugin.Status) *wasmplugin.Status {
	return sayStatusAt(langOf(r), func(t time.Time) string { return readerClock(r, t) }, st)
}

func utcClock(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") + " UTC" }

// sayStatusAt says an app's row in lang: the refusal its last update check
// stored (update.refusal), the line the Apps list shows about its updates
// (update_said), a range that leaves this filex out (compat.message) and the
// version kept to go back to (previous.message, on clock). st is the
// answer's own copy (StatusOf reads the row afresh); what is stored is left
// as it is.
//
// ⚠⚠ Why (0.55): the list built these lines in the browser from the row's
// fields (web lib/appPluginUpdates.ts with appPlugins.update.* and
// appPlugins.compat.needs), deciding there too which one a row gets.
func sayStatusAt(lang string, clock func(time.Time) string, st *wasmplugin.Status) *wasmplugin.Status {
	if st == nil {
		return nil
	}
	if st.Update != nil {
		u := *st.Update
		if u.Refusal != nil {
			u.Refusal = u.Refusal.Said(lang)
		}
		st.Update = &u
	}
	st.UpdateSaid = appUpdateSaid(lang, st)
	if st.Compat != nil && !st.Compat.OK {
		c := *st.Compat
		c.Message = srvtext.Text(lang, "server.update_line.compat_outside", srvtext.Vars{"requires": c.Requires, "filex": c.Filex})
		st.Compat = &c
	}
	if st.Previous != nil {
		p := *st.Previous
		p.Message = srvtext.Text(lang, "server.update_line.previous", srvtext.Vars{"version": p.Version, "when": clock(p.ReplacedAt)})
		st.Previous = &p
	}
	return st
}

// appUpdateSaid is the Apps list's line about st's updates in lang, "" where
// the status word says it all.
func appUpdateSaid(lang string, st *wasmplugin.Status) string {
	line := func(key string, v srvtext.Vars) string { return srvtext.Text(lang, "server.update_line."+key, v) }
	if st.UpdateSource == "" {
		// ⚠ Not "installed from a file" for every app without one: an app
		// installed from an ADDRESS before 0.47 has one, but filex did not
		// keep its manifest's address, and "from a file" would be false.
		if st.Source == "url" {
			return line("no_manifest_address", nil)
		}
		return line("no_source", nil)
	}
	u := st.Update
	if u == nil {
		return ""
	}
	switch u.Status {
	case wasmplugin.UpdateIncompatible:
		return line("needs_newer_filex", srvtext.Vars{"version": u.Version, "requires": u.Requires})
	case wasmplugin.UpdateFailed:
		return line("failed_detail", srvtext.Vars{"version": u.Version, "current": st.Version})
	case wasmplugin.UpdateNeedsApproval:
		var parts []string
		if len(u.Added) > 0 {
			parts = append(parts, line("new_permissions", srvtext.Vars{"permissions": strings.Join(u.Added, ", ")}))
		}
		if u.AddsModule {
			parts = append(parts, line("adds_module", nil))
		}
		return strings.Join(parts, " · ")
	case wasmplugin.UpdateCheckFailed:
		if u.Refusal != nil {
			return u.Refusal.Message
		}
	}
	return ""
}

// runtimeSaid is the Apps tab's header in lang, from the list answer's
// facts (runtimeFacts): whether apps are on (`state`), a processor that
// cannot run them (`arch`), signed apps only (`signature`), a development
// build that checks no range (`dev_build`), and the update check (off, last
// run on clock, or not yet - `update_check`). Absent keys say nothing.
// ⚠ The panel used to pick and word each line itself from these facts
// (appPlugins.runtime.*, 0.55 moved them here).
func runtimeSaid(lang string, clock func(time.Time) string, facts map[string]any) map[string]string {
	on := func(k string) bool { v, _ := facts[k].(bool); return v }
	say := func(key string, v srvtext.Vars) string { return srvtext.Text(lang, "server.app_runtime."+key, v) }
	out := map[string]string{"state": say("off", nil)}
	if on("enabled") {
		out["state"] = say("on", nil)
	}
	if !on("arch_ok") {
		out["arch"] = say("arch_bad", nil)
	}
	if on("requires_signature") {
		out["signature"] = say("signature", nil)
	}
	if !on("enabled") {
		return out
	}
	if !on("compat_enforced") {
		version, _ := facts["filex_version"].(string)
		out["dev_build"] = say("dev_build", srvtext.Vars{"version": version})
	}
	switch at, _ := facts["updates_checked_at"].(time.Time); {
	case !on("update_check"):
		out["update_check"] = say("update_check_off", nil)
	case !at.IsZero():
		out["update_check"] = say("last_check", srvtext.Vars{"when": clock(at)})
	default:
		out["update_check"] = say("never_checked", nil)
	}
	return out
}

// storageUpdateSaid is the storage plugins list's line about a plugin's
// updates in lang (pluginWire.update_said), "" where the status word says it
// all. It was web StoragePluginsTab's plugins.update.incompatible and the
// check's English error.
func storageUpdateSaid(lang string, u *plugin.UpdateInfo) string {
	if u == nil {
		return ""
	}
	switch u.Status {
	case plugin.UpdateIncompatible:
		return srvtext.Text(lang, "server.update_line.needs_newer_filex", srvtext.Vars{"version": u.Version, "requires": u.Requires})
	case plugin.UpdateCheckFailed:
		return srvtext.Text(lang, "server.update_line.check_failed", srvtext.Vars{"detail": u.Error})
	}
	return ""
}

// placeFailuresSaid is the File types choices an install, an upgrade or an
// approved request could not write, one sentence each in lang
// (server.error.place_*, a refused rule's server.error.rule_*). They used to
// be the English of assoc.PlaceForApp, printed inside a Turkish notice.
func placeFailuresSaid(lang string, fs []assoc.PlaceFailure) []string {
	if len(fs) == 0 {
		return nil
	}
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		p := apierr.Params{"handler": f.Handler, "ext": f.Ext, "detail": f.Message}
		for k, v := range f.Params {
			p[k] = v
		}
		key := f.Say
		if !apierr.Known(key) {
			key = "place_failed"
		}
		out = append(out, apierr.Text(lang, key, p))
	}
	return out
}
