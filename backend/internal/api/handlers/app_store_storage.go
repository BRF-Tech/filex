// Package handlers - app_store_storage.go
//
// Installing a STORAGE plugin from a store (#215, docs/PLUGINS.md →
// "Installing from a store"). The same link, the same page
// (/admin/store-install), the same trust question and the same routes as an
// app (app_store_intent.go: /store-intent, /store-intent/install,
// /store-intent/cancel); what differs is what is fetched and what is shown:
//
//   - the review reads the release's own filex-storage.json - the feed the
//     store reviewed, held to the link's manifest_sha256 - and holds this
//     platform's build in it to the link's pin, the name and the version too
//     (the "from its source" install, pinned to the store's review rather
//     than to the source's latest release);
//   - it says, in the reader's language and from the server, what installing
//     means: a native program with filex's own rights and every storage's
//     credentials, outside any sandbox; what the store's plugin validator
//     measured, and on which platform; whether this server's signature rule
//     (FILEX_PLUGIN_TRUSTED_KEYS) is met by the store's or the publisher's
//     signature - and refuses the install when it is not;
//   - the install downloads that build, holds it to its pin before anything
//     runs, and installs or upgrades it the way every binary plugin is
//     (internal/plugin: the staged path, the conformance gate at start, the
//     roll-back of a failed upgrade). A paid plugin's license is the store's,
//     kept under "storage:<name>" (appstore.LicenseID), and held from before
//     it lands.
//
// The license of a paid storage plugin, inside /api/admin/app-plugins (the
// same gate: the platform operator, signed in to the panel):
//
//	GET  /storage/{name}/license
//	PUT  /storage/{name}/license          - {key}: a new key, checked at once
//	POST /storage/{name}/license/verify   - check now
package handlers

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/update"
)

// storageNotice is one sentence of a storage plugin's review, in the
// reader's language: warning (read before installing), info, or error (the
// install is refused).
type storageNotice struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// storageCapability is a capability the store's run proved, in the reader's
// words.
type storageCapability struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// storageSignature is where the build's signature stands on this server.
type storageSignature struct {
	StoreSigned     bool `json:"store_signed"`
	PublisherSigned bool `json:"publisher_signed"`
	Required        bool `json:"required"`
	Verifies        bool `json:"verifies"`
}

// storageReview is what the store review shows of a storage plugin's link.
type storageReview struct {
	Name         string                `json:"name"`
	Version      string                `json:"version"`
	Platform     string                `json:"platform"`
	Platforms    []string              `json:"platforms"`
	SHA256       string                `json:"sha256"`
	Size         int64                 `json:"size,omitempty"`
	URL          string                `json:"url"`
	FeedURL      string                `json:"feed_url"`
	Notes        string                `json:"notes,omitempty"`
	Source       string                `json:"source"`
	Paid         bool                  `json:"paid"`
	Conformance  *appstore.Conformance `json:"conformance,omitempty"`
	Capabilities []storageCapability   `json:"capabilities"`
	Signature    storageSignature      `json:"signature"`
	Notices      []storageNotice       `json:"notices"`
	// CanInstall is the server's answer: false when something above refuses
	// the install (a signature this server requires and cannot verify).
	CanInstall bool `json:"can_install"`
}

// storageCapabilityIDs are the capabilities a run can prove, in the order
// the review lists them.
var storageCapabilityIDs = []string{"write", "delete", "range", "move", "copy", "mkdir", "set_mtime", "watch", "presign", "multipart"}

// storageOff answers a storage link on a server whose storage plugins are off.
func (h *AppStore) storageOff(w http.ResponseWriter, r *http.Request) bool {
	if h.Plugins != nil {
		return false
	}
	storeFail(w, r, &appstore.Error{Code: appstore.CodePluginsOff, Message: srvtext.Text(langOf(r), "server.store_storage.plugins_off", nil),
		Detail: map[string]any{"kind": appstore.KindStorage}})
	return true
}

// storageBuild is what a storage link installs here: this platform's build,
// the signatures that may stand for it (the store's first), and the feed it
// is held to. Every pin is checked; nothing is downloaded but the feed.
func (h *AppStore) storageBuild(ctx context.Context, lang, origin string, in *appstore.Intent) (plugin.FeedBinary, []string, *plugin.Feed, error) {
	plat := h.Plugins.Platform()
	b, ok := in.Binaries[plat]
	if !ok {
		have := make([]string, 0, len(in.Binaries))
		for p := range in.Binaries {
			have = append(have, p)
		}
		sort.Strings(have)
		return plugin.FeedBinary{}, nil, nil, &appstore.Error{Code: appstore.CodeNoBuild,
			Message: srvtext.Text(lang, "server.store_storage.no_build", srvtext.Vars{"store": origin, "name": in.App, "platform": plat, "platforms": strings.Join(have, ", ")}),
			Detail:  map[string]any{"kind": appstore.KindStorage, "platform": plat, "platforms": have}}
	}
	feed, err := h.Plugins.ReadPinnedFeed(ctx, in.FeedURL, in.ManifestSHA256)
	if errors.Is(err, plugin.ErrFeedChanged) {
		return plugin.FeedBinary{}, nil, nil, &appstore.Error{Code: appstore.CodePinMismatch,
			Message: srvtext.Text(lang, "server.store_storage.feed_changed", srvtext.Vars{"store": origin}),
			Detail:  map[string]any{"kind": appstore.KindStorage, "mismatches": []appstore.Mismatch{{Field: "manifest_sha256", Link: in.ManifestSHA256, Source: err.Error()}}}}
	}
	if err != nil {
		return plugin.FeedBinary{}, nil, nil, &appstore.Error{Code: appstore.CodeUnreachable,
			Message: srvtext.Text(lang, "server.store_storage.feed_unreachable", srvtext.Vars{"reason": err.Error()}),
			Detail:  map[string]any{"kind": appstore.KindStorage}}
	}
	var mm []appstore.Mismatch
	if strings.TrimSpace(feed.Name) != in.App {
		mm = append(mm, appstore.Mismatch{Field: "app", Link: in.App, Source: feed.Name})
	}
	if v := strings.TrimPrefix(strings.TrimSpace(feed.Version), "v"); v != strings.TrimPrefix(in.Version, "v") {
		mm = append(mm, appstore.Mismatch{Field: "version", Link: in.Version, Source: v})
	}
	fb, ok := feed.Binaries[plat]
	if !ok || !strings.EqualFold(fb.SHA256, b.SHA256) {
		mm = append(mm, appstore.Mismatch{Field: "sha256", Link: b.SHA256, Source: fb.SHA256})
	}
	if len(mm) > 0 {
		fields := make([]string, 0, len(mm))
		for _, m := range mm {
			fields = append(fields, m.Field)
		}
		return plugin.FeedBinary{}, nil, nil, &appstore.Error{Code: appstore.CodePinMismatch,
			Message: srvtext.Text(lang, "server.store_storage.pin_mismatch", srvtext.Vars{"store": origin, "fields": strings.Join(fields, ", ")}),
			Detail:  map[string]any{"kind": appstore.KindStorage, "mismatches": mm}}
	}
	if ok, requires, filex, err := h.Plugins.JudgeRange(feed.Filex); err != nil || !ok {
		msg := srvtext.Text(lang, "server.store_storage.incompatible", srvtext.Vars{"name": in.App, "version": in.Version, "requires": requires, "filex": filex})
		if err != nil {
			msg = srvtext.Text(lang, "server.store_storage.range_unreadable", srvtext.Vars{"name": in.App, "version": in.Version, "requires": strings.TrimSpace(feed.Filex)})
		}
		return plugin.FeedBinary{}, nil, nil, &appstore.Error{Code: appstore.CodeIncompatible, Message: msg,
			Detail: map[string]any{"kind": appstore.KindStorage, "requires": requires, "filex": filex}}
	}
	return plugin.FeedBinary{URL: b.URL, SHA256: strings.ToLower(b.SHA256)}, []string{b.Sig, fb.Signature}, feed, nil
}

// signatureRefusalKey is the server's sentence for a storage build no
// signature stands for: the build gate's (a store signed it and does not
// vouch for it through this link, plugin.ErrStoreBuild) or the ordinary one
// (no signature verifies with FILEX_PLUGIN_TRUSTED_KEYS).
func signatureRefusalKey(err error) string {
	if errors.Is(err, plugin.ErrStoreBuild) {
		return "server.store_storage.signature_store_build"
	}
	return "server.store_storage.signature_refused"
}

// storeLinkOf is what a storage link says of its build beyond the file: the
// store whose link it is and the version it names - what the build's
// signature is checked as (plugin.VerifyBuild, the build gate).
func storeLinkOf(origin string, in *appstore.Intent) plugin.StoreLink {
	return plugin.StoreLink{Store: origin, Version: in.Version}
}

// storageRepoOf is the GitHub repository a storage plugin's source names
// (owner/name), "" when it names none or an address.
func storageRepoOf(source string) string {
	s := strings.TrimSpace(source)
	if rest, ok := strings.CutPrefix(s, "https://github.com/"); ok {
		s = strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".git")
	}
	if strings.Count(s, "/") == 1 && !strings.Contains(s, ":") {
		return s
	}
	return ""
}

// installedStorage is the storage plugin of the link's name already here,
// held to the rules a store link keeps for an app (installedFor): the same
// store and repository, a newer version, and a paid link never taking a
// plugin installed without a store under its license.
func (h *AppStore) installedStorage(ctx context.Context, lang, origin string, in *appstore.Intent) (*plugin.Status, *installedSource, error) {
	st, err := h.Plugins.ByName(ctx, in.App)
	if err != nil || st == nil {
		return nil, nil, err
	}
	src := installedSource{Version: st.Version, Repo: storageRepoOf(st.Source), SourceURL: st.Source}
	src.Store, _ = h.Svc.StorageInstalledFrom(ctx, in.App)
	adopts := src.Store == "" && in.Paid
	if st.Kind != model.PluginKindBinary || !strings.EqualFold(src.Repo, in.Repo) || (src.Store != "" && src.Store != origin) || adopts {
		return nil, &src, &appstore.Error{Code: appstore.CodeSourceChanged, Message: storageSourceChanged(lang, origin, in, src),
			Detail: map[string]any{"kind": appstore.KindStorage, "installed": src, "link": installedSource{Store: origin, Repo: in.Repo, Version: in.Version}}}
	}
	cur, err1 := update.ParseVersion(st.Version)
	next, err2 := update.ParseVersion(in.Version)
	if err1 != nil || err2 != nil || next.Compare(cur) <= 0 {
		return nil, &src, &appstore.Error{Code: appstore.CodeVersionRollback,
			Message: srvtext.Text(lang, "server.store_storage.version_rollback", srvtext.Vars{"name": in.App, "installed": st.Version, "version": in.Version}),
			Detail:  map[string]any{"kind": appstore.KindStorage, "installed": st.Version, "link": in.Version}}
	}
	return st, &src, nil
}

// storageSourceChanged is the server's sentence for a store link that would
// move an installed storage plugin to another store or repository - or take
// one installed straight from its repository under a store's paid license.
func storageSourceChanged(lang, origin string, in *appstore.Intent, src installedSource) string {
	v := srvtext.Vars{"name": in.App, "repo": in.Repo, "store": origin, "installed": src.Repo, "was_store": src.Store}
	switch {
	case in.Paid && src.Store == "" && src.Repo != "" && strings.EqualFold(src.Repo, in.Repo):
		return srvtext.Text(lang, "server.store_storage.source_changed_direct", v)
	case src.Store != "" && src.Repo != "":
		return srvtext.Text(lang, "server.store_storage.source_changed_store", v)
	case src.Repo != "":
		return srvtext.Text(lang, "server.store_storage.source_changed", v)
	default:
		return srvtext.Text(lang, "server.store_storage.source_changed_other", v)
	}
}

// storageReviewOf is the review's body: the build, and every sentence the
// administrator reads before pressing Install.
func (h *AppStore) storageReviewOf(ctx context.Context, lang, origin string, in *appstore.Intent, b plugin.FeedBinary, sigs []string, feed *plugin.Feed) storageReview {
	plat := h.Plugins.Platform()
	rv := storageReview{Name: in.App, Version: in.Version, Platform: plat, SHA256: b.SHA256, URL: b.URL, FeedURL: in.FeedURL,
		Notes: feed.Notes, Source: in.Repo, Paid: in.Paid, Conformance: in.Conformance,
		Capabilities: []storageCapability{}, Notices: []storageNotice{}, Platforms: []string{}}
	if ib, ok := in.Binaries[plat]; ok {
		rv.Size = ib.Size
	}
	for p := range in.Binaries {
		rv.Platforms = append(rv.Platforms, p)
	}
	sort.Strings(rv.Platforms)
	say := func(level, key string, vars srvtext.Vars) {
		rv.Notices = append(rv.Notices, storageNotice{Level: level, Text: srvtext.Text(lang, key, vars)})
	}
	say("warning", "server.store_storage.native", srvtext.Vars{"name": in.App, "repo": in.Repo})
	if c := in.Conformance; c != nil {
		say("info", "server.store_storage.conformance", srvtext.Vars{"store": origin, "filex": c.Filex, "platform": c.Platform,
			"passed": strconv.Itoa(c.Passed), "skipped": strconv.Itoa(c.Skipped)})
		if c.Platform != "" && c.Platform != plat {
			say("info", "server.store_storage.other_platform", srvtext.Vars{"platform": plat, "checked": c.Platform})
		}
		proved := map[string]bool{}
		for _, id := range c.Capabilities {
			proved[id] = true
		}
		for _, id := range storageCapabilityIDs {
			if proved[id] {
				rv.Capabilities = append(rv.Capabilities, storageCapability{ID: id, Label: srvtext.Text(lang, "server.store_storage.cap."+id, nil)})
			}
		}
	} else {
		say("warning", "server.store_storage.conformance_none", nil)
	}
	say("info", "server.store_storage.probed_here", nil)
	rv.Signature.StoreSigned = len(sigs) > 0 && strings.TrimSpace(sigs[0]) != ""
	rv.Signature.PublisherSigned = len(sigs) > 1 && strings.TrimSpace(sigs[1]) != ""
	required, sigErr := h.Plugins.StoreSignature(ctx, in.App, b, storeLinkOf(origin, in), sigs)
	rv.Signature.Required, rv.Signature.Verifies = required, required && sigErr == nil
	rv.CanInstall = true
	switch {
	case rv.Signature.Required && rv.Signature.Verifies:
		say("info", "server.store_storage.signature_verified", nil)
	case rv.Signature.Required:
		say("error", signatureRefusalKey(sigErr), srvtext.Vars{"name": in.App, "version": in.Version})
		rv.CanInstall = false
	case rv.Signature.StoreSigned:
		say("info", "server.store_storage.signature_kept", nil)
	default:
		say("warning", "server.store_storage.unsigned", nil)
	}
	if in.Paid {
		say("info", "server.store_storage.held_until_license", nil)
	}
	return rv
}

// storageIntent is Intent for a storage plugin's link (the link already read
// and verified: trusted store, signed, current, for this filex).
func (h *AppStore) storageIntent(w http.ResponseWriter, r *http.Request, origin, token string, in *appstore.Intent) {
	if h.storageOff(w, r) {
		return
	}
	lang := langOf(r)
	b, sigs, feed, err := h.storageBuild(r.Context(), lang, origin, in)
	if err != nil {
		storeFail(w, r, err)
		return
	}
	was, wasFrom, err := h.installedStorage(r.Context(), lang, origin, in)
	if err != nil {
		storeFail(w, r, err)
		return
	}
	reqID := h.Svc.RequestFor(r.Context(), origin, in.TokenID)
	p := h.Svc.HoldFor(origin, token, in, actorUserID(r), reqID)
	body := map[string]any{
		"handle": p.Handle, "store": origin, "store_trust": h.Svc.TrustStatus(r.Context(), origin),
		"intent": viewOfIntent(in), "storage_review": h.storageReviewOf(r.Context(), lang, origin, in, b, sigs, feed),
	}
	if reqID != 0 {
		body["request_id"] = reqID
	}
	if was != nil {
		body["upgrade_of"] = map[string]any{"id": was.ID, "version": was.Version, "state": was.State,
			"store": wasFrom.Store, "repo": wasFrom.Repo, "source_url": wasFrom.SourceURL}
	}
	writeJSON(w, http.StatusOK, body)
}

// storageInstall is Install for a reviewed storage plugin link: the feed is
// read and every pin checked AGAIN (what lands is what was reviewed), the
// license held from before, then the build downloaded, held to its pin and
// installed or upgraded.
func (h *AppStore) storageInstall(w http.ResponseWriter, r *http.Request, p *appstore.Pending, licenseKey string) {
	installed := false
	defer func() {
		if !installed {
			h.Svc.PutBack(p)
		}
	}()
	if h.storageOff(w, r) {
		return
	}
	in := p.Intent
	ctx := r.Context()
	lang := langOf(r)
	unlock := h.Svc.LockApp(in.LicenseID())
	defer unlock()
	b, sigs, _, err := h.storageBuild(ctx, lang, p.Origin, in)
	if err != nil {
		storeFail(w, r, err)
		return
	}
	if required, sigErr := h.Plugins.StoreSignature(ctx, in.App, b, storeLinkOf(p.Origin, in), sigs); required && sigErr != nil {
		storeFail(w, r, &appstore.Error{Code: appstore.CodeSignatureRequired,
			Message: srvtext.Text(lang, signatureRefusalKey(sigErr), srvtext.Vars{"name": in.App, "version": in.Version}),
			Detail:  map[string]any{"kind": appstore.KindStorage}})
		return
	}
	was, _, err := h.installedStorage(ctx, lang, p.Origin, in)
	if err != nil {
		storeFail(w, r, err)
		return
	}
	key := strings.TrimSpace(licenseKey)
	if key == "" {
		key = in.LicenseKey
	}
	actor := actorIDOf(r)
	dctx := context.WithoutCancel(ctx)
	var undo *appstore.Undo
	if in.Paid {
		// Held from BEFORE the install: the new build does not start until
		// the license holds. (A restart holds it again before any plugin
		// starts - server.go loads the holds first, and with the app store
		// off a plugin under a store's license starts held.)
		if undo, err = h.Svc.RequireUndoableAs(ctx, in.LicenseID(), in.App, p.Origin, key, actor); err != nil {
			storeFail(w, r, err)
			return
		}
	}
	var st *plugin.Status
	if was != nil {
		st, err = h.Plugins.UpgradeBuild(dctx, was.ID, b, storeLinkOf(p.Origin, in), sigs)
	} else {
		st, err = h.Plugins.InstallBuild(dctx, in.App, in.Repo, b, storeLinkOf(p.Origin, in), sigs)
	}
	if err != nil {
		h.Svc.Undo(dctx, undo)
		body := map[string]any{"error": "plugin_install_failed",
			"message": srvtext.Text(lang, "server.store_storage.install_failed", srvtext.Vars{"name": in.App, "version": in.Version, "reason": err.Error()})}
		if st != nil {
			body["plugin"] = st
		}
		writeJSON(w, installStatus(err), body)
		return
	}
	installed = true
	// A build whose signature verified only in the old sha256-only form is
	// accepted during 0.55: the plugin's row says so, and so does the audit.
	p.LegacySignature = st != nil && st.LegacySignature
	body := map[string]any{"kind": appstore.KindStorage}
	if in.Paid && key != "" {
		if lic, lerr := h.Svc.Check(dctx, in.LicenseID(), actor); lerr == nil {
			body["license"] = lic
		}
	} else if in.Paid {
		if lic, _ := h.Svc.LicenseOf(ctx, in.LicenseID()); lic != nil {
			body["license"] = lic
		}
	}
	h.Svc.Finish(dctx, p, "installed", actor)
	if now, _ := h.Plugins.ByName(dctx, in.App); now != nil {
		st = now
	}
	body["plugin"] = st
	if p.RequestID != 0 && h.Requests != nil {
		if closed, ok, err := h.Requests.CompleteStore(dctx, p.RequestID, actorOf(r), st); err == nil && ok {
			body["request"] = pluginRequestView(closed, langOf(r), false)
		}
	}
	writeJSON(w, http.StatusCreated, body)
}

// ── A storage plugin's license ─────────────────────────────────────────

// storageLicenseName is the {name} of a license route whose plugin is
// installed here (404 otherwise).
func (h *AppStore) storageLicenseName(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.storageOff(w, r) {
		return "", false
	}
	name := chi.URLParam(r, "name")
	notHere := map[string]string{"error": "not_found", "message": srvtext.Text(langOf(r), "server.store_storage.not_installed", srvtext.Vars{"name": name})}
	if !plugin.ValidName(name) {
		writeJSON(w, http.StatusNotFound, notHere)
		return "", false
	}
	if st, err := h.Plugins.ByName(r.Context(), name); err != nil || st == nil {
		writeJSON(w, http.StatusNotFound, notHere)
		return "", false
	}
	return name, true
}

// StorageLicense answers a storage plugin's license (status free when it is
// not a paid plugin from a store).
func (h *AppStore) StorageLicense(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "reading a license") {
		return
	}
	name, ok := h.storageLicenseName(w, r)
	if !ok {
		return
	}
	id := appstore.LicenseID(appstore.KindStorage, name)
	v, err := h.Svc.LicenseOf(r.Context(), id)
	if err != nil {
		storeFail(w, r, err)
		return
	}
	if v == nil {
		v = &appstore.View{App: id, Name: name, Kind: appstore.KindStorage, Required: false, Status: appstore.StatusFree}
	}
	writeJSON(w, http.StatusOK, v)
}

// PutStorageLicense gives a paid storage plugin a new key, checked at once.
func (h *AppStore) PutStorageLicense(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "changing a license key") {
		return
	}
	name, ok := h.storageLicenseName(w, r)
	if !ok {
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	auth.SkipAuditRow(r.Context())
	v, err := h.Svc.SetKey(context.WithoutCancel(r.Context()), appstore.LicenseID(appstore.KindStorage, name), req.Key, actorIDOf(r))
	if err != nil {
		storeFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// VerifyStorageLicense asks the store about a storage plugin's license now.
func (h *AppStore) VerifyStorageLicense(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "checking a license") {
		return
	}
	name, ok := h.storageLicenseName(w, r)
	if !ok {
		return
	}
	auth.SkipAuditRow(r.Context())
	v, err := h.Svc.Check(context.WithoutCancel(r.Context()), appstore.LicenseID(appstore.KindStorage, name), actorIDOf(r))
	if err != nil {
		storeFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
