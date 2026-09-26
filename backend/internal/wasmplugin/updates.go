package wasmplugin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/update"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Updates: an installed app follows the source it came from ─────────
//
// Once a day (and whenever an administrator presses "Check now") filex asks
// the source every installed app came from whether there is a newer version
// the running filex can run, and — when that version asks for nothing the
// administrator has not already approved — installs it by itself.
//
// WHERE a newer version is looked for is the source the app was installed
// from, read the way the maintainers publish:
//
//   - A GitHub install at a VERSION TAG (`v0.1.1`: how apps with a module are
//     released) follows the repository's RELEASES: the newest non-draft,
//     non-pre-release release whose tag is a newer version, whose
//     filex-app.json at that tag names the same app, and whose `filex` range
//     lets this filex in. Up to maxReleaseCandidates tags are read per check,
//     newest first, so a release that needs a newer filex is stepped over to
//     the newest one that does not.
//   - A GitHub install at a BRANCH (`main`: how language packs are published —
//     they have no releases, a translator pushes a JSON file) follows that
//     branch: its filex-app.json, when its version is newer.
//   - A URL install follows its manifest address (app_plugins.manifest_url;
//     for a pack installed before 0.47 the source_url, which IS the manifest).
//     The module comes from the new manifest's `wasm.url` when it is an
//     absolute address, else from the address it was installed from.
//   - An uploaded app has no source. It is never checked, and the list says so.
//
// Every fetch and every install goes through the SAME code an administrator's
// install uses (githubManifest, FetchGitHub, FetchURL, Upgrade): the https-only
// fetch with its size caps, the sha256 pin, the signature, the describe proof,
// the atomic swap with its roll-back. There is no second downloader.
//
// WHAT is applied without asking — the per-app switch (auto_update, on by
// default for every app):
//
//   - a newer version that asks for exactly the permissions already granted,
//     or fewer, is installed by itself. The grant IS the administrator's
//     decision about an app — every host function is held to it — so a new
//     version inside it can do nothing that was not already allowed; what a
//     module does inside its grant changes with every version anyway. That is
//     the language pack (no module, no permissions) and most releases of an
//     app;
//   - a newer version that asks for a permission the app was not granted, or
//     a language pack that now brings a module (code that runs, where there
//     was none), is NEVER installed by itself: it waits, marked "needs
//     approval", and the administrator is notified once;
//   - with the switch off, or on an instance that only runs signed apps (a
//     repository carries no detached signature), a newer version is only
//     announced.
//
// Every automatic update is written to the app's log, to the audit log
// (`app_plugin.update`, by nobody, metadata automatic) and to the
// administrators' bell (notify.EventAppUpdated …). A demo instance does none
// of this, and FILEX_APP_PLUGIN_UPDATE_CHECK=0 stops the daily check (an
// air-gapped install): nothing leaves the server until an administrator
// presses "Check now".

const (
	// updateInterval is the time between two background checks.
	updateInterval = 24 * time.Hour
	// updateFirstWait is how long after start a due check waits, so a boot is
	// not slowed by it (and a restart loop cannot turn it into a stream).
	updateFirstWait = 2 * time.Minute
	// updateCheckedKey stores when the last check ran (settings table), so the
	// daily beat survives restarts.
	updateCheckedKey = "app_plugins.updates_checked_at"
	// maxReleaseCandidates bounds the manifests read per app per check.
	maxReleaseCandidates = 5
	// maxReleasesBytes caps the GitHub releases answer (it carries notes).
	maxReleasesBytes = 4 << 20
	// updateCheckTimeout bounds one whole check.
	updateCheckTimeout = 15 * time.Minute
)

// githubAPIBase is where the releases of a repository are listed.
const githubAPIBase = "https://api.github.com"

// What the last check found (UpdateInfo.Status).
const (
	// UpdateCurrent: nothing newer that this filex can run.
	UpdateCurrent = "current"
	// UpdateAvailable: a newer version waits for the administrator.
	UpdateAvailable = "available"
	// UpdateNeedsApproval: a newer version asks for more than was granted.
	UpdateNeedsApproval = "needs_approval"
	// UpdateIncompatible: newer versions exist, but none this filex can run.
	UpdateIncompatible = "incompatible"
	// UpdateFailed: the automatic update was tried and undone.
	UpdateFailed = "failed"
	// UpdateCheckFailed: the source could not be read (nothing was tried).
	UpdateCheckFailed = "check_failed"
)

// UpdateInfo is what the last check found for one app — stored with it
// (app_plugins.update_json) and shown on the Apps list.
type UpdateInfo struct {
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Status    string     `json:"status,omitempty"`
	// Version is the newer version found (available, needs_approval,
	// failed), or the newest one this filex cannot run (incompatible).
	Version string `json:"version,omitempty"`
	// Ref is the git ref that version is read from (a GitHub source).
	Ref string `json:"ref,omitempty"`
	// Requires: incompatible — the range that version declares.
	Requires string `json:"requires,omitempty"`
	// Added / AddsModule: needs_approval — what the administrator is asked
	// to approve (DryRunUpgrade).
	Added      []string `json:"added,omitempty"`
	AddsModule bool     `json:"adds_module,omitempty"`
	// Refusal: failed / check_failed — the same refusal an install answers,
	// so the panel says it in the reader's words with the wizard's sentences.
	Refusal *InstallRefusal `json:"refusal,omitempty"`
	// Auto is the last automatic update, kept across checks.
	Auto *AutoUpdated `json:"auto,omitempty"`
	// Announced is "<status>@<version>" as last told to the administrators,
	// so a daily check does not ring the bell again for the same thing.
	Announced string `json:"announced,omitempty"`
}

// InstallRefusal is an InstallError as the wire carries it — the body of a
// refused install (handlers.installErrorBody) and the reason an update check
// or an automatic update stopped. One shape, so the panel explains both with
// the same sentences.
type InstallRefusal struct {
	Code     string   `json:"error"`
	Message  string   `json:"message,omitempty"`
	Missing  []string `json:"missing,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Where    string   `json:"where,omitempty"`
	Refs     []string `json:"refs,omitempty"`
	Status   int      `json:"status,omitempty"`
	Requires string   `json:"requires,omitempty"`
	Filex    string   `json:"filex,omitempty"`
}

// AutoUpdated is one automatic update: from which version, to which, when.
type AutoUpdated struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	At   time.Time `json:"at"`
}

// UpdateReport is one check, as "Check now" answers it: how many apps were
// asked, and the names of those that moved, wait, or failed.
type UpdateReport struct {
	CheckedAt     time.Time `json:"checked_at"`
	Checked       int       `json:"checked"`
	Updated       []string  `json:"updated"`
	Available     []string  `json:"available"`
	NeedsApproval []string  `json:"needs_approval"`
	Failed        []string  `json:"failed"`
}

// updateState is the check's own state on the registry.
type updateState struct {
	mu     sync.Mutex
	flight *updateFlight
	// background: the daily check runs (StartUpdater).
	background bool
	// now is the clock (tests).
	now func() time.Time
}

// updateFlight is the check in progress: a second caller waits for it.
type updateFlight struct {
	done   chan struct{}
	report *UpdateReport
	err    error
}

func (r *Registry) clock() time.Time {
	if r.updates.now != nil {
		return r.updates.now()
	}
	return time.Now()
}

// ── Where to ask ───────────────────────────────────────────────────────

// updateSource is where an installed app can be asked for a newer version.
type updateSource struct {
	Kind        string // model.AppPluginSourceGitHub | model.AppPluginSourceURL
	Repo, Ref   string // GitHub
	ManifestURL string // URL
	ModuleURL   string // URL, the address the module was installed from
}

// sourceOf answers where p's updates come from; false when nowhere (an
// upload, a bundled app, a URL app installed before its manifest address
// was kept).
func sourceOf(p *Installed) (updateSource, bool) {
	row := p.Row
	switch row.Source {
	case model.AppPluginSourceGitHub:
		repo, ref, ok := parseGitHubSource(row.SourceURL)
		if !ok {
			return updateSource{}, false
		}
		return updateSource{Kind: row.Source, Repo: repo, Ref: ref}, true
	case model.AppPluginSourceURL:
		if row.ManifestURL != "" {
			return updateSource{Kind: row.Source, ManifestURL: row.ManifestURL, ModuleURL: row.SourceURL}, true
		}
		// A pack installed before 0.47: its source_url is its manifest.
		if p.Manifest.IsLanguagePack() && row.SourceURL != "" {
			return updateSource{Kind: row.Source, ManifestURL: row.SourceURL}, true
		}
	}
	return updateSource{}, false
}

// parseGitHubSource reads the source_url a GitHub install stores
// (FetchGitHub): https://github.com/<owner>/<name>@<ref>.
func parseGitHubSource(s string) (repo, ref string, ok bool) {
	rest, found := strings.CutPrefix(s, "https://github.com/")
	if !found {
		return "", "", false
	}
	repo, ref, found = strings.Cut(rest, "@")
	if !found || !githubRepoRe.MatchString(repo) || strings.TrimSpace(ref) == "" {
		return "", "", false
	}
	return repo, ref, true
}

// releaseVersion reads a tag as a release version: `v0.1.2` / `0.1.2`, not a
// pre-release. False for anything else — a branch, a commit.
func releaseVersion(tag string) (update.Version, bool) {
	v, err := update.ParseVersion(tag)
	if err != nil || v.IsPre() {
		return update.Version{}, false
	}
	return v, true
}

// ── What is there ──────────────────────────────────────────────────────

// candidate is a newer version found at the source.
type candidate struct {
	m   *Manifest
	ref string // the GitHub ref it is read from
}

// findUpdate asks src for versions newer than p's. found is the newest one
// this filex can run; blocked is the newest newer one it cannot, when that is
// newer than found (so the list can say why nothing moves, or why only this).
func (r *Registry) findUpdate(ctx context.Context, p *Installed, src updateSource) (found, blocked *candidate, err error) {
	current, err := update.ParseVersion(p.Row.Version)
	if err != nil {
		return nil, nil, &InstallError{Code: ErrCodeManifestInvalid,
			Message: "the installed version " + p.Row.Version + " is not a semantic version, so a newer one cannot be told apart"}
	}
	consider := func(raw []byte, ref string) (bool, error) {
		m, err := ParseManifest(raw)
		if err != nil {
			return false, installErr(ErrCodeManifestInvalid, err.Error())
		}
		if m.Name != p.Row.Name {
			return false, installErr(ErrCodeManifestInvalid, "the source now names "+m.Name+", the installed app is "+p.Row.Name)
		}
		v, ok := releaseVersion(m.Version)
		if !ok || !current.Newer(v) {
			return false, nil
		}
		rng, err := compatRange(m)
		if err != nil {
			return false, installErr(ErrCodeManifestInvalid, err.Error())
		}
		host, enforced := hostRelease()
		if enforced && !rng.admits(host) {
			if blocked == nil {
				blocked = &candidate{m: m, ref: ref}
			}
			return false, nil
		}
		found = &candidate{m: m, ref: ref}
		return true, nil
	}

	switch {
	case src.Kind == model.AppPluginSourceGitHub && isReleaseRef(src.Ref):
		tags, err := r.githubReleases(ctx, src.Repo, current)
		if err != nil {
			return nil, nil, err
		}
		var firstErr error
		for i, tag := range tags {
			if i >= maxReleaseCandidates {
				break
			}
			raw, _, err := r.githubManifest(ctx, src.Repo, []string{tag})
			if err == nil {
				var done bool
				if done, err = consider(raw, tag); done {
					break
				}
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if found == nil && blocked == nil && firstErr != nil {
			return nil, nil, firstErr
		}
	case src.Kind == model.AppPluginSourceGitHub:
		raw, ref, err := r.githubManifest(ctx, src.Repo, []string{src.Ref})
		if err != nil {
			return nil, nil, err
		}
		if _, err := consider(raw, ref); err != nil {
			return nil, nil, err
		}
	default:
		raw, err := r.fetch(ctx, src.ManifestURL, wire.MaxManifestBytes)
		if err != nil {
			ie := fetchFailure(err, FetchReasonManifestNotFound, src.ManifestURL)
			ie.Message = "manifest: " + err.Error()
			return nil, nil, ie
		}
		if _, err := consider(raw, ""); err != nil {
			return nil, nil, err
		}
	}
	if found != nil && blocked != nil {
		fv, _ := releaseVersion(found.m.Version)
		bv, _ := releaseVersion(blocked.m.Version)
		if !fv.Newer(bv) {
			blocked = nil
		}
	}
	return found, blocked, nil
}

// isReleaseRef: the app was installed at a version tag, so it follows the
// repository's releases rather than the ref itself.
func isReleaseRef(ref string) bool {
	_, ok := releaseVersion(ref)
	return ok
}

// githubReleases lists a repository's published releases newer than
// current, newest first: no draft, no pre-release, only tags that are
// versions.
func (r *Registry) githubReleases(ctx context.Context, repo string, current update.Version) ([]string, error) {
	u := githubAPIBase + "/repos/" + repo + "/releases?per_page=30"
	b, err := r.fetch(ctx, u, maxReleasesBytes)
	if err != nil {
		ie := fetchFailure(err, FetchReasonManifestNotFound, repo)
		ie.Message = "releases of " + repo + ": " + err.Error()
		return nil, ie
	}
	var rels []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(b, &rels); err != nil {
		return nil, &InstallError{Code: ErrCodeFetch, Reason: FetchReasonHTTPStatus, Where: u, Message: "releases of " + repo + ": " + err.Error()}
	}
	type tagged struct {
		tag string
		v   update.Version
	}
	var out []tagged
	for _, rel := range rels {
		v, ok := releaseVersion(rel.Tag)
		if rel.Draft || rel.Prerelease || !ok || !current.Newer(v) {
			continue
		}
		out = append(out, tagged{rel.Tag, v})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[j].v.Compare(out[i].v) < 0 })
	tags := make([]string, len(out))
	for i, t := range out {
		tags[i] = t.tag
	}
	return tags, nil
}

// ── Applying one ───────────────────────────────────────────────────────

// fetchUpdate reads c through the install path — the manifest AND the
// module, pinned and verified like any install.
func (r *Registry) fetchUpdate(ctx context.Context, src updateSource, c *candidate) (*InstallInput, error) {
	if src.Kind == model.AppPluginSourceGitHub {
		return r.FetchGitHub(ctx, GitHubInput{Repo: src.Repo, Ref: c.ref})
	}
	module := ""
	if !c.m.IsLanguagePack() {
		module = src.ModuleURL
		if c.m.Wasm != nil {
			if u, err := url.Parse(strings.TrimSpace(c.m.Wasm.URL)); err == nil && u.IsAbs() {
				module = u.String()
			}
		}
	}
	return r.FetchURL(ctx, URLInput{URL: module, ManifestURL: src.ManifestURL})
}

// applyUpdate installs c over p through Upgrade — the swap, the proof, the
// roll-back. ⚠ It decides again on what was FETCHED, not on what the check
// read a moment earlier: a branch can move in between, and an automatic
// update must never install something nobody would have let through.
func (r *Registry) applyUpdate(ctx context.Context, p *Installed, src updateSource, c *candidate) (*Status, error) {
	in, err := r.fetchUpdate(ctx, src, c)
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(in.Manifest)
	if err != nil {
		return nil, installErr(ErrCodeManifestInvalid, err.Error())
	}
	if m.Version != c.m.Version {
		where := src.ManifestURL
		if src.Kind == model.AppPluginSourceGitHub {
			where = src.Repo
		}
		return nil, &InstallError{Code: ErrCodeFetch, Reason: FetchReasonChanged, Where: where,
			Message: "the source changed while it was being read (" + c.m.Version + " became " + m.Version + "); the next check reads it again"}
	}
	if up := upgradeOf(p, m); len(up.Added) > 0 || up.AddsModule {
		return nil, &InstallError{Code: ErrCodePermissionsChanged, Message: "the new version asks for more than was granted", Missing: up.Added}
	}
	// No grant in the input: Upgrade holds the new manifest to the one the
	// app already has (and refuses anything wider).
	in.Granted = nil
	in.Lang = "en"
	st, _, err := r.Upgrade(ctx, p.Row.ID, in)
	return st, err
}

// FetchUpdate reads the newer version an app's source has — the one the
// update check would install — for an administrator's review and upgrade
// ("Review update" on the Apps list). The same search and the same fetch
// the automatic update uses, so what is reviewed is what would have been
// installed; only the decision is the administrator's.
func (r *Registry) FetchUpdate(ctx context.Context, id int64) (*InstallInput, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	src, ok := sourceOf(p)
	if !ok {
		return nil, installErr(ErrCodeManifestInvalid, p.Row.Name+" was not installed from a repository or an address, so it has no source to read a newer version from")
	}
	found, blocked, err := r.findUpdate(ctx, p, src)
	if err != nil {
		return nil, err
	}
	if found == nil {
		if blocked != nil {
			c := compatOf(blocked.m)
			return nil, &InstallError{Code: ErrCodeIncompatible, Requires: c.Requires, Filex: c.Filex,
				Message: p.Row.Name + " " + blocked.m.Version + " works with filex " + c.Requires + "; this is filex " + c.Filex}
		}
		return nil, installErr(ErrCodeUpToDate, "the source has nothing newer than "+p.Row.Version)
	}
	return r.fetchUpdate(ctx, src, found)
}

// ── One check ──────────────────────────────────────────────────────────

// CheckUpdates runs one check of every app that has a source, applying what
// may be applied — or, when a check is already running, waits for that one
// and answers what it found. One at a time, whoever asks.
func (r *Registry) CheckUpdates(ctx context.Context) (*UpdateReport, error) {
	if r.opts.Demo {
		return nil, installErr(ErrCodeDemo, "apps are not updated on the demo instance")
	}
	r.updates.mu.Lock()
	if f := r.updates.flight; f != nil {
		r.updates.mu.Unlock()
		select {
		case <-f.done:
			return f.report, f.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f := &updateFlight{done: make(chan struct{})}
	r.updates.flight = f
	r.updates.mu.Unlock()
	defer func() {
		r.updates.mu.Lock()
		r.updates.flight = nil
		r.updates.mu.Unlock()
		close(f.done)
	}()
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()
	f.report, f.err = r.runUpdateCheck(ctx)
	return f.report, f.err
}

func (r *Registry) runUpdateCheck(ctx context.Context) (*UpdateReport, error) {
	now := r.clock().UTC()
	rep := &UpdateReport{CheckedAt: now, Updated: []string{}, Available: []string{}, NeedsApproval: []string{}, Failed: []string{}}
	for _, p := range r.All() {
		// Between two apps, never inside one: an upgrade in flight finishes
		// its swap (Upgrade, Close) whatever happens to ctx.
		if ctx.Err() != nil || r.isClosing() {
			break
		}
		src, ok := sourceOf(p)
		if !ok {
			continue
		}
		rep.Checked++
		switch info := r.checkApp(ctx, p, src, now); {
		case info.Auto != nil && info.Auto.At.Equal(now) && info.Status == UpdateCurrent:
			rep.Updated = append(rep.Updated, p.Row.Name)
		case info.Status == UpdateAvailable:
			rep.Available = append(rep.Available, p.Row.Name)
		case info.Status == UpdateNeedsApproval:
			rep.NeedsApproval = append(rep.NeedsApproval, p.Row.Name)
		case info.Status == UpdateFailed || info.Status == UpdateCheckFailed:
			rep.Failed = append(rep.Failed, p.Row.Name)
		}
	}
	if err := r.opts.Store.UpsertSetting(context.WithoutCancel(ctx), updateCheckedKey, now.Format(time.RFC3339)); err != nil {
		r.log.Warn("app-plugins: the update check's time was not stored", slog.Any("err", err))
	}
	return rep, nil
}

func (r *Registry) isClosing() bool {
	r.upgradeMu.Lock()
	defer r.upgradeMu.Unlock()
	return r.closing
}

// checkApp checks one app, applies what may be applied, records the finding
// and tells the administrators what they need to hear.
func (r *Registry) checkApp(ctx context.Context, p *Installed, src updateSource, now time.Time) UpdateInfo {
	prev := updateInfoOf(p.Row)
	info := UpdateInfo{CheckedAt: &now, Status: UpdateCurrent, Auto: prev.Auto, Announced: prev.Announced}
	found, blocked, err := r.findUpdate(ctx, p, src)
	switch {
	case err != nil:
		info.Status, info.Refusal = UpdateCheckFailed, RefusalOf(err)
		p.log("warn", "update check: "+err.Error())
	case found != nil:
		info.Version, info.Ref = found.m.Version, found.ref
		up := upgradeOf(p, found.m)
		switch {
		case len(up.Added) > 0 || up.AddsModule:
			info.Status, info.Added, info.AddsModule = UpdateNeedsApproval, up.Added, up.AddsModule
			p.log("info", "update check: "+found.m.Version+" is available and needs approval")
		case !p.Row.AutoUpdate || r.RequiresSignature():
			info.Status = UpdateAvailable
			p.log("info", "update check: "+found.m.Version+" is available")
		default:
			from := p.Row.Version
			if _, err := r.applyUpdate(ctx, p, src, found); err != nil {
				var ie *InstallError
				if errors.As(err, &ie) && ie.Code == ErrCodePermissionsChanged {
					info.Status, info.Added = UpdateNeedsApproval, ie.Missing
				} else {
					info.Status, info.Refusal = UpdateFailed, RefusalOf(err)
				}
				p.log("error", "automatic update to "+found.m.Version+" failed, "+from+" stays: "+err.Error())
			} else {
				info = UpdateInfo{CheckedAt: &now, Status: UpdateCurrent, Announced: info.Announced,
					Auto: &AutoUpdated{From: from, To: found.m.Version, At: now}}
				if np, ok := r.ByID(p.Row.ID); ok {
					p = np
				}
				p.log("info", "updated automatically from "+from+" to "+found.m.Version)
				r.auditUpdate(ctx, p, from, found.m.Version, src)
			}
		}
	}
	if found == nil && blocked != nil && info.Status == UpdateCurrent {
		info.Status, info.Version = UpdateIncompatible, blocked.m.Version
		if c := compatOf(blocked.m); c != nil {
			info.Requires = c.Requires
		}
	}
	r.announce(ctx, p, &info)
	r.saveUpdate(ctx, p, info)
	return info
}

// RefusalOf is err as the panel reads it.
func RefusalOf(err error) *InstallRefusal {
	var ie *InstallError
	if !errors.As(err, &ie) {
		return &InstallRefusal{Code: "error", Message: err.Error()}
	}
	return &InstallRefusal{Code: ie.Code, Message: ie.Message, Missing: ie.Missing, Reason: ie.Reason, Where: ie.Where,
		Refs: ie.Refs, Status: ie.Status, Requires: ie.Requires, Filex: ie.Filex}
}

// updateInfoOf reads what the last check stored for row.
func updateInfoOf(row *model.AppPlugin) UpdateInfo {
	var info UpdateInfo
	if strings.TrimSpace(row.UpdateJSON) != "" {
		_ = json.Unmarshal([]byte(row.UpdateJSON), &info)
	}
	return info
}

// saveUpdate stores info with the app. ⚠ Under upgradeMu: the row is shared
// with an upgrade that may be rewriting it (Upgrade), and a write of the
// whole row in the middle of one would put a version the disk does not hold
// into the database.
func (r *Registry) saveUpdate(ctx context.Context, p *Installed, info UpdateInfo) {
	r.upgradeMu.Lock()
	defer r.upgradeMu.Unlock()
	if cur, ok := r.ByID(p.Row.ID); ok {
		p = cur
	} else {
		return // removed meanwhile
	}
	p.Row.UpdateJSON = jsonOf(info)
	if err := r.opts.Store.UpdateAppPlugin(context.WithoutCancel(ctx), p.Row); err != nil {
		r.log.Warn("app-plugins: update check result not stored", slog.String("plugin", p.Row.Name), slog.Any("err", err))
	}
}

// settleUpdate clears what the last check found once the app has reached
// that version by any road (an administrator's upgrade included), keeping
// the history (the last automatic update, what was announced).
func settleUpdate(row *model.AppPlugin, version string) {
	info := updateInfoOf(row)
	if info.Version == "" {
		return
	}
	have, err1 := update.ParseVersion(version)
	want, err2 := update.ParseVersion(info.Version)
	if err1 != nil || err2 != nil || have.Compare(want) < 0 {
		return
	}
	row.UpdateJSON = jsonOf(UpdateInfo{CheckedAt: info.CheckedAt, Status: UpdateCurrent, Auto: info.Auto, Announced: info.Announced})
}

// SetAutoUpdate switches automatic updates for one app.
func (r *Registry) SetAutoUpdate(ctx context.Context, id int64, on bool) (*Status, error) {
	if r.opts.Demo {
		return nil, installErr(ErrCodeDemo, "app plugins cannot be changed on the demo instance")
	}
	r.upgradeMu.Lock()
	p, ok := r.ByID(id)
	if !ok {
		r.upgradeMu.Unlock()
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	p.Row.AutoUpdate = on
	err := r.opts.Store.UpdateAppPlugin(ctx, p.Row)
	r.upgradeMu.Unlock()
	if err != nil {
		return nil, err
	}
	if on {
		p.log("info", "automatic updates switched on")
	} else {
		p.log("info", "automatic updates switched off")
	}
	return r.StatusOf(p), nil
}

// ── Telling people ─────────────────────────────────────────────────────

// auditUpdate writes the audit row of an automatic update: by nobody, the
// versions, where it came from.
func (r *Registry) auditUpdate(ctx context.Context, p *Installed, from, to string, src updateSource) {
	where := src.ManifestURL
	if src.Kind == model.AppPluginSourceGitHub {
		where = "https://github.com/" + src.Repo
	}
	_ = r.opts.Store.InsertAuditEntry(context.WithoutCancel(ctx), &model.AuditEntry{
		Action: "app_plugin.update", TargetType: "app_plugin", TargetID: p.Row.Name,
		Metadata: map[string]any{"plugin": p.Row.Name, "from": from, "to": to, "automatic": true, "source": where},
	})
}

// announce rings the administrators' bell for what they need to hear — an
// app that moved, and once per version a newer one that waits for them or
// failed to install. A check that could not reach its source is not rung:
// an air-gapped install would hear it every day. The bell is only a door;
// the list says the same thing.
func (r *Registry) announce(ctx context.Context, p *Installed, info *UpdateInfo) {
	if r.notify == nil {
		return
	}
	meta := labelMeta(p.Row.Name, p.Manifest.Label)
	var ev notify.Event
	switch {
	case info.Auto != nil && info.CheckedAt != nil && info.Auto.At.Equal(*info.CheckedAt):
		// Moved in THIS check (a later check carries the same Auto along).
		meta["version"], meta["from"] = info.Auto.To, info.Auto.From
		ev = notify.Event{Event: notify.EventAppUpdated, Severity: notify.SeverityInfo,
			Title: p.Row.Name + " updated to " + info.Auto.To, Body: "from " + info.Auto.From + ", automatically"}
	case info.Status == UpdateAvailable, info.Status == UpdateNeedsApproval, info.Status == UpdateFailed:
		key := info.Status + "@" + info.Version
		if info.Announced == key {
			return
		}
		meta["version"], meta["from"] = info.Version, p.Row.Version
		switch info.Status {
		case UpdateAvailable:
			ev = notify.Event{Event: notify.EventAppUpdateAvailable, Severity: notify.SeverityInfo,
				Title: p.Row.Name + " " + info.Version + " is available"}
		case UpdateNeedsApproval:
			meta["added"] = strings.Join(info.Added, ", ")
			meta["adds_module"] = info.AddsModule
			title := p.Row.Name + " " + info.Version + " needs approval"
			if len(info.Added) > 0 {
				title += ": new permission " + strings.Join(info.Added, ", ")
			}
			ev = notify.Event{Event: notify.EventAppUpdateNeedsApproval, Severity: notify.SeverityWarning, Title: title}
		default:
			if info.Refusal != nil {
				meta["error"] = clip(info.Refusal.Message, 300)
				meta["code"] = info.Refusal.Code
			}
			ev = notify.Event{Event: notify.EventAppUpdateFailed, Severity: notify.SeverityWarning,
				Title: p.Row.Name + " " + info.Version + " could not be installed automatically"}
		}
		info.Announced = key
	default:
		return
	}
	ev.Meta = meta
	if _, err := r.notify.Send(context.WithoutCancel(ctx), ev); err != nil {
		r.log.Warn("app-plugins: update notice not sent", slog.String("plugin", p.Row.Name), slog.Any("err", err))
	}
}

// ── The daily beat ─────────────────────────────────────────────────────

// StartUpdater runs the daily check until ctx ends. enabled is the operator's
// switch (FILEX_APP_PLUGIN_UPDATE_CHECK); a demo never runs it. The time of
// the last check is stored, so a restart neither skips a day nor checks at
// every boot.
func (r *Registry) StartUpdater(ctx context.Context, enabled bool) {
	if r == nil {
		return
	}
	r.updates.mu.Lock()
	r.updates.background = enabled && !r.opts.Demo
	on := r.updates.background
	r.updates.mu.Unlock()
	if !on {
		r.log.Info("app-plugins: the daily update check is off")
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(r.untilNextCheck(ctx)):
			}
			if _, err := r.CheckUpdates(ctx); err != nil && ctx.Err() == nil {
				r.log.Warn("app-plugins: update check failed", slog.Any("err", err))
			}
		}
	}()
}

// BackgroundUpdates reports whether the daily check runs.
func (r *Registry) BackgroundUpdates() bool {
	r.updates.mu.Lock()
	defer r.updates.mu.Unlock()
	return r.updates.background
}

// LastUpdateCheck is when the last check ran; zero when never.
func (r *Registry) LastUpdateCheck(ctx context.Context) time.Time {
	v, err := r.opts.Store.GetSetting(ctx, updateCheckedKey)
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}
	}
	return t
}

// untilNextCheck is how long the loop sleeps: a day after the last check, and
// never less than updateFirstWait.
func (r *Registry) untilNextCheck(ctx context.Context) time.Duration {
	last := r.LastUpdateCheck(ctx)
	if last.IsZero() {
		return updateFirstWait
	}
	d := last.Add(updateInterval).Sub(r.clock())
	if d < updateFirstWait {
		return updateFirstWait
	}
	return d
}
