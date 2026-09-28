package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/dailycheck"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/update"
)

// ── Updates: a binary plugin follows the source it names ────────────────
//
// ⚠⚠ Owner's rule (2026-09-27): NO plugin updates itself — a storage plugin
// no more than an app (wasmplugin/updates.go). A binary storage plugin may
// name a SOURCE, and filex reads it once a day (and on "Check for updates")
// and SAYS when a newer version for this platform is there. An
// administrator's "Review update" installs it through the ordinary upgrade:
// the staged swap, the signature on an instance that requires one, the
// conformance probe of the plugin's own claims, and the roll-back when the
// new binary does not come up.
//
// A source is either
//
//   - `owner/name` — the `filex-storage.json` attached to the GitHub
//     repository's LATEST release
//     (https://github.com/<owner>/<name>/releases/latest/download/filex-storage.json), or
//   - the https address of a `filex-storage.json` anywhere.
//
// The feed:
//
//	{"name": "myfs", "version": "1.3.0", "filex": ">=0.47.0",
//	 "notes": "What changed, as plain text.",
//	 "binaries": {"linux/amd64": {"url": "https://…/myfs-linux-amd64", "sha256": "…", "signature": "…"},
//	              "windows/amd64": {…}, "darwin/arm64": {…}}}
//
// "Newer" is a higher semantic version than the one the running plugin
// describes; a pre-release is never taken. `filex` is read with the grammar
// an app's range uses (wasmplugin.JudgeFilexRange, wired in by the server:
// one grammar, not two). A binary is chosen by GOOS/GOARCH; its sha256 is
// REQUIRED, and the bytes are held to it before anything is stopped.
//
// A plugin has no permission list, so there is no "needs approval" state:
// every newer version is `available` and waits. A REMOTE plugin is upgraded
// where it runs and has no source; its row shows the version it describes.
//
// Every fetch goes through the download client an install by address uses —
// the guarded one that refuses private, loopback and link-local targets on
// every hop. FILEX_APP_PLUGIN_UPDATE_CHECK=0 stops the daily check for storage
// plugins too (one switch for "no request leaves the server by itself").

// Update check outcomes (UpdateInfo.Status).
const (
	UpdateCurrent      = "current"
	UpdateAvailable    = "available"
	UpdateIncompatible = "incompatible"
	UpdateCheckFailed  = "check_failed"
)

const (
	// FeedFileName is what a GitHub release attaches.
	FeedFileName = "filex-storage.json"
	// maxFeedBytes caps a feed: it is a small JSON document.
	maxFeedBytes = 1 << 20
	// maxNotesBytes clips a feed's notes; the review shows them.
	maxNotesBytes = 8 << 10
	// updateInterval / updateFirstWait / updateCheckedKey: the same rhythm
	// as the apps' check, remembered across restarts.
	updateInterval   = 24 * time.Hour
	updateFirstWait  = 2 * time.Minute
	updateCheckedKey = "plugins.updates_checked_at"
)

// Feed is a filex-storage.json.
type Feed struct {
	Name     string                `json:"name"`
	Version  string                `json:"version"`
	Filex    string                `json:"filex,omitempty"`
	Notes    string                `json:"notes,omitempty"`
	Binaries map[string]FeedBinary `json:"binaries"`
}

// FeedBinary is one platform's build.
type FeedBinary struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	// Signature is the detached ed25519 signature over the sha256, for an
	// instance that only runs signed plugins (FILEX_PLUGIN_TRUSTED_KEYS).
	Signature string `json:"signature,omitempty"`
}

// UpdateInfo is what the last check of a plugin's source found.
type UpdateInfo struct {
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Status    string     `json:"status"`
	// Version: the newer version (available, incompatible).
	Version string `json:"version,omitempty"`
	// Requires: incompatible — the range that version declares.
	Requires string `json:"requires,omitempty"`
	// Notes: the feed's notes for Version, plain text.
	Notes string `json:"notes,omitempty"`
	// SHA256: the build for this platform (available).
	SHA256 string `json:"sha256,omitempty"`
	// Platform the build was chosen for ("linux/amd64").
	Platform string `json:"platform,omitempty"`
	// Error: check_failed — why.
	Error string `json:"error,omitempty"`
	// Announced is "<status>@<version>" once the administrators were told.
	Announced string `json:"announced,omitempty"`
}

// UpdateHooks wire the check to the rest of filex (server.go).
type UpdateHooks struct {
	// Range judges a feed's `filex` range against the running filex
	// (wasmplugin.JudgeFilexRange). Nil = no range is checked.
	Range func(rng string) (ok bool, requires, filex string, err error)
	// Announce rings the administrators' bell, once per newer version.
	Announce func(ctx context.Context, name, version, notes string)
}

// UpdateReport is one check, as it went.
type UpdateReport struct {
	CheckedAt time.Time `json:"checked_at"`
	Checked   int       `json:"checked"`
	Available []string  `json:"available"`
	Failed    []string  `json:"failed"`
}

type updateState struct {
	mu         sync.Mutex
	hooks      UpdateHooks
	platform   string
	background bool
	running    sync.Mutex
}

// SetUpdateHooks wires the range judge and the bell.
func (m *Manager) SetUpdateHooks(h UpdateHooks) {
	m.updates.mu.Lock()
	m.updates.hooks = h
	m.updates.mu.Unlock()
}

func (m *Manager) hooks() UpdateHooks {
	m.updates.mu.Lock()
	defer m.updates.mu.Unlock()
	return m.updates.hooks
}

// platform is the GOOS/GOARCH key a build is chosen by.
func (m *Manager) platform() string {
	m.updates.mu.Lock()
	defer m.updates.mu.Unlock()
	if m.updates.platform != "" {
		return m.updates.platform
	}
	return runtime.GOOS + "/" + runtime.GOARCH
}

var sourceRepoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// FeedURL is where a source's feed is read: `owner/name` (or its GitHub
// address) → the latest release's filex-storage.json; an https address →
// itself. "" for an empty source.
func FeedURL(source string) (string, error) {
	s := strings.TrimSpace(source)
	if s == "" {
		return "", nil
	}
	repo := s
	if rest, ok := strings.CutPrefix(s, "https://github.com/"); ok {
		repo = strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".git")
	}
	if sourceRepoRe.MatchString(repo) {
		return "https://github.com/" + repo + "/releases/latest/download/" + FeedFileName, nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return "", reject("a source is owner/name (a GitHub repository) or the https address of a %s", FeedFileName)
	}
	return u.String(), nil
}

// SetSource names (or, with "", clears) where a binary plugin's newer
// versions are published. Nothing is fetched: the next check reads it.
func (m *Manager) SetSource(ctx context.Context, id int64, source string) (*Status, error) {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return nil, err
	}
	source = strings.TrimSpace(source)
	if _, err := FeedURL(source); err != nil {
		return m.statusOf(ctx, e), err
	}
	e.mu.Lock()
	if e.row.Kind != model.PluginKindBinary && source != "" {
		e.mu.Unlock()
		return m.statusOf(ctx, e), reject("a remote plugin is upgraded where it runs; it has no source")
	}
	e.row.Source = source
	e.row.UpdateJSON = ""
	row := *e.row
	e.mu.Unlock()
	if err := m.store.UpdatePlugin(ctx, &row); err != nil {
		return m.statusOf(ctx, e), err
	}
	return m.statusOf(ctx, e), nil
}

// fetchFeed reads and checks a source's feed.
func (m *Manager) fetchFeed(ctx context.Context, source string) (*Feed, error) {
	addr, err := FeedURL(source)
	if err != nil {
		return nil, err
	}
	if addr == "" {
		return nil, reject("this plugin names no source")
	}
	body, err := m.get(ctx, addr, maxFeedBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FeedFileName, err)
	}
	var f Feed
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", FeedFileName, err)
	}
	if strings.TrimSpace(f.Version) == "" {
		return nil, fmt.Errorf("%s names no version", FeedFileName)
	}
	for plat, b := range f.Binaries {
		sum := strings.ToLower(strings.TrimSpace(b.SHA256))
		if len(sum) != 64 || strings.Trim(sum, "0123456789abcdef") != "" {
			return nil, fmt.Errorf("%s: the %s build has no sha256 (64 hex characters)", FeedFileName, plat)
		}
		b.SHA256 = sum
		f.Binaries[plat] = b
	}
	f.Notes = clipNotes(strings.TrimSpace(f.Notes))
	return &f, nil
}

// get fetches addr through the guarded download client, capped at max.
func (m *Manager) get(ctx context.Context, addr string, max int64) ([]byte, error) {
	resp, err := m.download(ctx, addr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("download: larger than %d KiB", max>>10)
	}
	return b, nil
}

func clipNotes(s string) string {
	if len(s) <= maxNotesBytes {
		return s
	}
	cut := maxNotesBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// newer reports whether candidate is a newer release than current (the
// version the plugin describes). A pre-release is never newer; a current
// version that is not semantic is compared by text.
func newer(candidate, current string) bool {
	c, err := update.ParseVersion(candidate)
	if err != nil || c.IsPre() {
		return false
	}
	cur, err := update.ParseVersion(current)
	if err != nil {
		return strings.TrimPrefix(strings.TrimSpace(current), "v") != strings.TrimPrefix(c.Raw, "v")
	}
	return cur.Newer(c)
}

// judge decides what a feed means for a plugin running version current: the
// status, and the build for this platform when it is available.
func (m *Manager) judge(row *model.Plugin, f *Feed) (UpdateInfo, *FeedBinary) {
	info := UpdateInfo{Status: UpdateCurrent, Platform: m.platform()}
	if n := strings.TrimSpace(f.Name); n != "" && n != row.Name && n != row.Driver {
		info.Status, info.Error = UpdateCheckFailed, fmt.Sprintf("%s describes %q, not %q", FeedFileName, n, row.Name)
		return info, nil
	}
	if !newer(f.Version, row.Version) {
		return info, nil
	}
	info.Version = strings.TrimPrefix(strings.TrimSpace(f.Version), "v")
	if rng := strings.TrimSpace(f.Filex); rng != "" {
		if judge := m.hooks().Range; judge != nil {
			ok, requires, _, err := judge(rng)
			if err != nil {
				info.Status, info.Error = UpdateCheckFailed, fmt.Sprintf("%s: filex: %v", FeedFileName, err)
				return info, nil
			}
			if !ok {
				info.Status, info.Requires = UpdateIncompatible, requires
				return info, nil
			}
		}
	}
	b, ok := f.Binaries[info.Platform]
	if !ok {
		info.Status, info.Error = UpdateCheckFailed, fmt.Sprintf("%s %s has no build for %s", FeedFileName, info.Version, info.Platform)
		return info, nil
	}
	info.Status, info.Notes, info.SHA256 = UpdateAvailable, f.Notes, b.SHA256
	return info, &b
}

// updateInfoOf is a row's stored finding, adjusted for what runs now: a
// version that is no longer newer (an upgrade happened since) reads current.
func updateInfoOf(row *model.Plugin) *UpdateInfo {
	if strings.TrimSpace(row.UpdateJSON) == "" {
		return nil
	}
	var info UpdateInfo
	if err := json.Unmarshal([]byte(row.UpdateJSON), &info); err != nil {
		return nil
	}
	if (info.Status == UpdateAvailable || info.Status == UpdateIncompatible) && !newer(info.Version, row.Version) {
		info.Status, info.Version, info.Notes, info.SHA256, info.Requires = UpdateCurrent, "", "", "", ""
	}
	return &info
}

// CheckUpdates reads every binary plugin's source once and records what it
// found. It installs nothing. One check at a time; a second caller waits.
func (m *Manager) CheckUpdates(ctx context.Context) (*UpdateReport, error) {
	m.updates.running.Lock()
	defer m.updates.running.Unlock()
	rows, err := m.store.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	rep := &UpdateReport{CheckedAt: now, Available: []string{}, Failed: []string{}}
	for _, r := range rows {
		if r.Kind != model.PluginKindBinary || strings.TrimSpace(r.Source) == "" {
			continue
		}
		e := m.ensureEntry(r)
		info := m.checkOne(ctx, e, now)
		rep.Checked++
		switch info.Status {
		case UpdateAvailable:
			rep.Available = append(rep.Available, r.Name)
		case UpdateCheckFailed:
			rep.Failed = append(rep.Failed, r.Name)
		}
	}
	if err := m.store.UpsertSetting(context.WithoutCancel(ctx), updateCheckedKey, now.Format(time.RFC3339)); err != nil {
		m.log.Warn("plugins: could not record the update check", slog.Any("err", err))
	}
	return rep, nil
}

// checkOne checks one plugin, records the finding and tells the
// administrators once per newer version.
func (m *Manager) checkOne(ctx context.Context, e *entry, now time.Time) UpdateInfo {
	e.mu.Lock()
	row := *e.row
	e.mu.Unlock()
	prev := updateInfoOf(&row)
	var info UpdateInfo
	if f, err := m.fetchFeed(ctx, row.Source); err != nil {
		info = UpdateInfo{Status: UpdateCheckFailed, Error: err.Error(), Platform: m.platform()}
	} else {
		info, _ = m.judge(&row, f)
	}
	info.CheckedAt = &now
	if prev != nil {
		info.Announced = prev.Announced
	}
	if info.Status == UpdateAvailable && info.Announced != UpdateAvailable+"@"+info.Version {
		if a := m.hooks().Announce; a != nil {
			a(context.WithoutCancel(ctx), row.Name, info.Version, info.Notes)
		}
		info.Announced = UpdateAvailable + "@" + info.Version
	}
	b, _ := json.Marshal(info)
	e.mu.Lock()
	e.row.UpdateJSON = string(b)
	saved := *e.row
	e.mu.Unlock()
	if err := m.store.UpdatePlugin(context.WithoutCancel(ctx), &saved); err != nil {
		m.log.Warn("plugins: could not record an update check", slog.String("plugin", row.Name), slog.Any("err", err))
	}
	return info
}

// UpgradeFromSource installs the newer version the plugin's source has — an
// administrator's approval ("Review update"). The same search the check
// makes, the bytes held to the feed's sha256 before anything is stopped, then
// the ordinary upgrade with its signature check, conformance probe and
// roll-back.
func (m *Manager) UpgradeFromSource(ctx context.Context, id int64) (*Status, error) {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	row := *e.row
	e.mu.Unlock()
	if row.Kind != model.PluginKindBinary {
		return m.statusOf(ctx, e), reject("a remote plugin is upgraded where it runs")
	}
	f, err := m.fetchFeed(ctx, row.Source)
	if err != nil {
		return m.statusOf(ctx, e), err
	}
	info, bin := m.judge(&row, f)
	switch info.Status {
	case UpdateCurrent:
		return m.statusOf(ctx, e), reject("%s is up to date: its source has nothing newer than %s", row.Name, row.Version)
	case UpdateIncompatible:
		return m.statusOf(ctx, e), reject("%s %s works with filex %s, not this one", row.Name, info.Version, info.Requires)
	case UpdateCheckFailed:
		return m.statusOf(ctx, e), reject("%s", info.Error)
	}
	resp, err := m.download(ctx, strings.TrimSpace(bin.URL))
	if err != nil {
		return m.statusOf(ctx, e), err
	}
	defer resp.Body.Close()
	st, err := m.upgrade(ctx, id, resp.Body, bin.Signature, bin.SHA256)
	if err != nil {
		return st, err
	}
	m.log.Info("plugins: upgraded from its source", slog.String("plugin", row.Name), slog.String("from", row.Version), slog.String("to", info.Version))
	return st, nil
}

// InstallFromSource installs a binary plugin from the build its source
// publishes for this platform — the feed's version, held to the feed's
// sha256 — and keeps the source, so the daily check follows it.
func (m *Manager) InstallFromSource(ctx context.Context, name, source string) (*Status, error) {
	if !validName(name) {
		return nil, ErrBadName
	}
	source = strings.TrimSpace(source)
	f, err := m.fetchFeed(ctx, source)
	if err != nil {
		return nil, err
	}
	plat := m.platform()
	b, ok := f.Binaries[plat]
	if !ok {
		return nil, reject("%s %s has no build for %s", FeedFileName, f.Version, plat)
	}
	if rng := strings.TrimSpace(f.Filex); rng != "" {
		if judge := m.hooks().Range; judge != nil {
			ok, requires, filex, err := judge(rng)
			if err != nil {
				return nil, reject("%s: filex: %v", FeedFileName, err)
			}
			if !ok {
				return nil, reject("%s %s works with filex %s; this is %s", name, f.Version, requires, filex)
			}
		}
	}
	st, err := m.InstallFromURL(ctx, name, b.URL, b.SHA256, b.Signature)
	if err != nil {
		return st, err
	}
	return m.SetSource(ctx, st.ID, source)
}

// StartUpdater runs the daily check (FILEX_APP_PLUGIN_UPDATE_CHECK): a day
// after the last one, remembered across restarts.
func (m *Manager) StartUpdater(ctx context.Context, enabled bool) {
	if m == nil {
		return
	}
	m.updates.mu.Lock()
	m.updates.background = enabled
	m.updates.mu.Unlock()
	if !enabled {
		m.log.Info("plugins: the daily update check is off")
		return
	}
	go m.beat().Run(ctx, func(ctx context.Context) error {
		_, err := m.CheckUpdates(ctx)
		return err
	}, func(err error) { m.log.Warn("plugins: update check failed", slog.Any("err", err)) })
}

// beat is the daily schedule, the same one the apps' check keeps
// (internal/dailycheck).
func (m *Manager) beat() dailycheck.Beat {
	return dailycheck.Beat{Store: m.store, Key: updateCheckedKey, First: updateFirstWait, Interval: updateInterval}
}

// BackgroundUpdates reports whether the daily check runs.
func (m *Manager) BackgroundUpdates() bool {
	m.updates.mu.Lock()
	defer m.updates.mu.Unlock()
	return m.updates.background
}

// LastUpdateCheck is when the last check ran; zero when never.
func (m *Manager) LastUpdateCheck(ctx context.Context) time.Time { return m.beat().Last(ctx) }
