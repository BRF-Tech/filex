package wasmplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/dailycheck"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/update"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Updates: an installed app follows the source it came from ─────────
//
// Once a day (and whenever an administrator presses "Check now") filex asks
// the source every installed app came from whether there is a newer version
// the running filex can run — and SAYS so. It installs nothing.
//
// ⚠⚠ Owner's rule (2026-09-27): NO plugin updates itself — an app with or
// without an interface, a language pack, a storage plugin. filex 0.47 applied
// a newer version on its own when it asked for nothing new; that is gone. A
// newer version is `available` (or `needs_approval` when it asks for more),
// the administrators are told once per version, and an administrator's
// Review update → approve installs it for everybody; the version it replaced
// is kept to go back to (versions.go). Why: an interface is code that runs in
// every person's browser, and "the grant did not change" says nothing about
// what new code does inside it — the administrator who approved a version is
// the one who decides the next.
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
//     the newest one that does not. The release's notes come along (plain
//     text) for the review.
//   - A GitHub install at a BRANCH (`main`: how language packs are published —
//     they have no releases, a translator pushes a JSON file) follows that
//     branch: its filex-app.json, when its version is newer.
//   - A URL install follows its manifest address (app_plugins.manifest_url;
//     for a pack installed before 0.47 the source_url, which IS the manifest).
//     The module comes from the new manifest's `wasm.url` when it is an
//     absolute address, else from the address it was installed from.
//   - An uploaded app has no source. It is never checked, and the list says so.
//
// Every fetch goes through the SAME code an administrator's install uses
// (githubManifest, FetchGitHub, FetchURL, Upgrade): the https-only fetch with
// its size caps, the sha256 pin, the signature, the describe proof, the
// atomic swap with its roll-back. There is no second downloader.
//
// A demo instance does none of this, and FILEX_APP_PLUGIN_UPDATE_CHECK=0 stops
// the daily check (an air-gapped install): nothing leaves the server until an
// administrator presses "Check now".

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
	// UpdateFailed: an automatic update was tried and undone (filex 0.47,
	// which applied updates by itself; a row may still carry it).
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
	// Requires: incompatible — the range that version declares, or ">" this
	// filex when its manifest carries what this filex does not know (a field
	// a newer filex added: peekManifest).
	Requires string `json:"requires,omitempty"`
	// Notes are the source's release notes for Version, as plain text
	// (a GitHub release's body, clipped).
	Notes string `json:"notes,omitempty"`
	// Added / AddsModule: needs_approval — what the administrator is asked
	// to approve (DryRunUpgrade).
	Added      []string `json:"added,omitempty"`
	AddsModule bool     `json:"adds_module,omitempty"`
	// Refusal: failed / check_failed — the same refusal an install answers,
	// so the panel says it in the reader's words with the wizard's sentences.
	Refusal *InstallRefusal `json:"refusal,omitempty"`
	// Auto is the last automatic update filex 0.47 made, kept across checks
	// for the record. Nothing sets it any more.
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
	m     *Manifest
	ref   string // the GitHub ref it is read from
	notes string // the release's notes, plain text
	// requires (a blocked one): the filex it needs — its range, or ">" this
	// filex when it carries what this filex does not know (beyond).
	requires string
	beyond   string
}

// peekManifest is the update check's reading of a manifest the strict read
// refused (ParseManifest, strictErr): its name, version and filex range, and
// what in it this filex does not know — a field (`user_permissions` to a
// filex before 0.49.0) or a newer manifest_version. beyond is "" when the
// refusal is about something else; err when not even those can be read.
//
// ⚠⚠ Only the CHECK reads this leniently (lesson #751). A newer app's
// release that uses a field a newer filex added is a version that needs a
// newer filex, not a broken source: read strictly, it turned the admin's
// Apps list red ("Could not check … unknown field") as if the app were at
// fault. Install and upgrade stay strict — an unknown field may be a
// permission this filex cannot enforce.
func peekManifest(raw []byte, strictErr error) (m *Manifest, beyond string, err error) {
	var head struct {
		ManifestVersion int    `json:"manifest_version"`
		Name            string `json:"name"`
		Version         string `json:"version"`
		Filex           string `json:"filex"`
		MinFilex        string `json:"min_filex"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, "", err
	}
	m = &Manifest{}
	m.ManifestVersion, m.Name, m.Version, m.Filex, m.MinFilex = head.ManifestVersion, head.Name, head.Version, head.Filex, head.MinFilex
	if head.ManifestVersion > wire.ProtocolVersion {
		return m, fmt.Sprintf("manifest_version %d", head.ManifestVersion), nil
	}
	// Unknown fields and nothing else: the same bytes read without the
	// strict decoder's refusal decode whole.
	field, unknown := unknownField(strictErr)
	var whole wire.Manifest
	if unknown && json.Unmarshal(raw, &whole) == nil {
		return m, field, nil
	}
	return m, "", nil
}

// unknownField reads encoding/json's DisallowUnknownFields refusal
// (`json: unknown field "requires"`) — the first unknown field, quoted.
func unknownField(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	const marker = `json: unknown field `
	msg := err.Error()
	i := strings.Index(msg, marker)
	if i < 0 {
		return "", false
	}
	rest := msg[i+len(marker):]
	if name, uerr := strconv.Unquote(rest); uerr == nil {
		return strconv.Quote(name), true
	}
	return "a field", true
}

// maxNotesBytes clips a release's notes: the review shows them, it does not
// host them.
const maxNotesBytes = 8 << 10

// findUpdate asks src for versions newer than p's. found is the newest one
// this filex can run; blocked is the newest newer one it cannot, when that is
// newer than found (so the list can say why nothing moves, or why only this).
func (r *Registry) findUpdate(ctx context.Context, p *Installed, src updateSource) (found, blocked *candidate, err error) {
	current, err := update.ParseVersion(p.Row.Version)
	if err != nil {
		return nil, nil, &InstallError{Code: ErrCodeManifestInvalid,
			Message: "the installed version " + p.Row.Version + " is not a semantic version, so a newer one cannot be told apart"}
	}
	var notes map[string]string
	consider := func(raw []byte, ref string) (bool, error) {
		m, err := ParseManifest(raw)
		// unread: the strict read's refusal, when the lenient one
		// (peekManifest) stands in to tell "needs a newer filex" from broken.
		var unread error
		beyond := ""
		if err != nil {
			unread = installErr(ErrCodeManifestInvalid, err.Error())
			if m, beyond, err = peekManifest(raw, err); err != nil {
				return false, unread
			}
		}
		if m.Name != p.Row.Name {
			return false, installErr(ErrCodeManifestInvalid, "the source now names "+m.Name+", the installed app is "+p.Row.Name)
		}
		v, ok := releaseVersion(m.Version)
		if !ok || !current.Newer(v) {
			if unread != nil && beyond == "" {
				return false, unread
			}
			return false, nil
		}
		rng, err := compatRange(m)
		if err != nil {
			return false, installErr(ErrCodeManifestInvalid, err.Error())
		}
		host, enforced := hostRelease()
		excluded := enforced && !rng.admits(host)
		if excluded || beyond != "" {
			if blocked == nil {
				blocked = &candidate{m: m, ref: ref, requires: rng.text, beyond: beyond}
				if !excluded {
					blocked.requires = ">" + FilexVersion()
				}
			}
			return false, nil
		}
		if unread != nil {
			return false, unread
		}
		found = &candidate{m: m, ref: ref, notes: notes[ref]}
		return true, nil
	}

	switch {
	case src.Kind == model.AppPluginSourceGitHub && isReleaseRef(src.Ref):
		tags, bodies, err := r.githubReleases(ctx, src.Repo, current)
		if err != nil {
			return nil, nil, err
		}
		notes = bodies
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
func (r *Registry) githubReleases(ctx context.Context, repo string, current update.Version) ([]string, map[string]string, error) {
	u := githubAPIBase + "/repos/" + repo + "/releases?per_page=30"
	b, err := r.fetch(ctx, u, maxReleasesBytes)
	if err != nil {
		ie := fetchFailure(err, FetchReasonManifestNotFound, repo)
		ie.Message = "releases of " + repo + ": " + err.Error()
		return nil, nil, ie
	}
	var rels []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Body       string `json:"body"`
	}
	bodies := map[string]string{}
	if err := json.Unmarshal(b, &rels); err != nil {
		return nil, nil, &InstallError{Code: ErrCodeFetch, Reason: FetchReasonHTTPStatus, Where: u, Message: "releases of " + repo + ": " + err.Error()}
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
		bodies[rel.Tag] = clip(strings.TrimSpace(rel.Body), maxNotesBytes)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[j].v.Compare(out[i].v) < 0 })
	tags := make([]string, len(out))
	for i, t := range out {
		tags[i] = t.tag
	}
	return tags, bodies, nil
}

// ── Applying one ───────────────────────────────────────────────────────

// fetchUpdate reads c through the install path — the manifest AND the
// module, pinned and verified like any install.
func (r *Registry) fetchUpdate(ctx context.Context, src updateSource, c *candidate) (*InstallInput, error) {
	if src.Kind == model.AppPluginSourceGitHub {
		in, err := r.FetchGitHub(ctx, GitHubInput{Repo: src.Repo, Ref: c.ref})
		if in != nil {
			in.Notes = c.notes
		}
		return in, err
	}
	module := ""
	// No module to fetch for a language pack, nor for an app that is only an
	// interface (its bundle's address is in the manifest).
	if !c.m.IsLanguagePack() && (c.m.NeedsModule() || c.m.Wasm != nil) {
		module = src.ModuleURL
		if c.m.Wasm != nil {
			if u, err := url.Parse(strings.TrimSpace(c.m.Wasm.URL)); err == nil && u.IsAbs() {
				module = u.String()
			}
		}
	}
	return r.FetchURL(ctx, URLInput{URL: module, ManifestURL: src.ManifestURL})
}

// FetchUpdate reads the newer version an app's source has — the one the
// update check announced — for an administrator's review and upgrade
// ("Review update" on the Apps list). The same search the check makes, so
// what is reviewed is what was announced; the decision is the
// administrator's.
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
			msg := p.Row.Name + " " + blocked.m.Version + " works with filex " + blocked.requires + "; this is filex " + FilexVersion()
			if blocked.beyond != "" {
				msg = p.Row.Name + " " + blocked.m.Version + " uses " + blocked.beyond + ", which this filex does not know: it needs a newer filex than " + FilexVersion()
			}
			return nil, &InstallError{Code: ErrCodeIncompatible, Requires: blocked.requires, Filex: FilexVersion(), Message: msg}
		}
		return nil, installErr(ErrCodeUpToDate, "the source has nothing newer than "+p.Row.Version)
	}
	return r.fetchUpdate(ctx, src, found)
}

// ── One check ──────────────────────────────────────────────────────────

// CheckUpdates runs one check of every app that has a source and records
// what it found (it installs nothing) — or, when a check is already running,
// waits for that one and answers what it found. One at a time, whoever asks.
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

// checkApp checks one app, records the finding and tells the administrators
// what they need to hear. It installs nothing.
func (r *Registry) checkApp(ctx context.Context, p *Installed, src updateSource, now time.Time) UpdateInfo {
	prev := updateInfoOf(p.Row)
	info := UpdateInfo{CheckedAt: &now, Status: UpdateCurrent, Auto: prev.Auto, Announced: prev.Announced}
	found, blocked, err := r.findUpdate(ctx, p, src)
	switch {
	case err != nil:
		info.Status, info.Refusal = UpdateCheckFailed, RefusalOf(err)
		p.log("warn", "update check: "+err.Error())
	case found != nil:
		info.Version, info.Ref, info.Notes = found.m.Version, found.ref, found.notes
		if up := upgradeOf(p, found.m); len(up.Added) > 0 || up.AddsModule {
			info.Status, info.Added, info.AddsModule = UpdateNeedsApproval, up.Added, up.AddsModule
			p.log("info", "update check: "+found.m.Version+" is available and needs approval")
		} else {
			info.Status = UpdateAvailable
			p.log("info", "update check: "+found.m.Version+" is available")
		}
	}
	if found == nil && blocked != nil && info.Status == UpdateCurrent {
		info.Status, info.Version, info.Requires = UpdateIncompatible, blocked.m.Version, blocked.requires
		if blocked.beyond != "" {
			p.log("info", "update check: "+blocked.m.Version+" uses "+blocked.beyond+", which this filex does not know; it needs a newer filex")
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

// ── Telling people ─────────────────────────────────────────────────────

// announce rings the administrators' bell for what they need to hear: once
// per version, a newer one that waits for them. A check that could not reach
// its source is not rung: an air-gapped install would hear it every day. The
// bell is only a door; the list says the same thing.
func (r *Registry) announce(ctx context.Context, p *Installed, info *UpdateInfo) {
	if r.notify == nil {
		return
	}
	meta := labelMeta(p.Row.Name, p.Manifest.Label)
	var ev notify.Event
	switch {
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
	go r.beat().Run(ctx, func(ctx context.Context) error {
		_, err := r.CheckUpdates(ctx)
		return err
	}, func(err error) { r.log.Warn("app-plugins: update check failed", slog.Any("err", err)) })
}

// beat is the daily schedule (internal/dailycheck), shared with the storage
// plugins' check.
func (r *Registry) beat() dailycheck.Beat {
	return dailycheck.Beat{Store: r.opts.Store, Key: updateCheckedKey, First: updateFirstWait, Interval: updateInterval, Now: r.clock}
}

// BackgroundUpdates reports whether the daily check runs.
func (r *Registry) BackgroundUpdates() bool {
	r.updates.mu.Lock()
	defer r.updates.mu.Unlock()
	return r.updates.background
}

// LastUpdateCheck is when the last check ran; zero when never.
func (r *Registry) LastUpdateCheck(ctx context.Context) time.Time { return r.beat().Last(ctx) }

// untilNextCheck is how long the loop sleeps: a day after the last check, and
// never less than updateFirstWait.
func (r *Registry) untilNextCheck(ctx context.Context) time.Duration { return r.beat().Wait(ctx) }
