package wasmplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/wasmfixture"
)

// ── Updates: an installed app follows its source ──────────────────────
//
// The language-pack tests need no wasm fixture (a pack has no module); the
// ones that swap a MODULE install the echo fixture.

const packRepo = "BRF-Tech/filex-lang-es"

func packRaw(ref string) string {
	return "https://raw.githubusercontent.com/" + packRepo + "/" + ref + "/filex-app.json"
}

func releasesURL(repo string) string {
	return githubAPIBase + "/repos/" + repo + "/releases?per_page=30"
}

// installPackFromGitHub installs the pack web currently serves at ref.
func installPackFromGitHub(t *testing.T, reg *Registry, ref string) *Status {
	t.Helper()
	in, err := reg.FetchGitHub(context.Background(), GitHubInput{Repo: packRepo, Ref: ref})
	require.NoError(t, err)
	in.Granted = []string{}
	st, _, err := reg.Install(context.Background(), in)
	require.NoError(t, err)
	return st
}

func auditActions(t *testing.T, o Options) []string {
	t.Helper()
	rows, err := o.Store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	var out []string
	for _, r := range rows {
		out = append(out, r.Action)
	}
	return out
}

// ⭐⭐ Owner's rule (2026-09-27): NOTHING updates itself. A translator pushes
// a new version of a pack; the check SAYS so (list + bell, once) and moves
// nothing. An administrator approves it: everybody moves, the audit row names
// who approved, the bell says it, and the version it replaced is KEPT — "back
// to 0.1.3" puts it back without a new approval.
func TestUpdates_ANewPackVersionWaitsForAnAdministratorAndCanBeUndone(t *testing.T) {
	withHost(t, "0.47.0")
	web := &fakeWeb{ok: map[string]string{packRaw("main"): string(packWithRange(t, "lang-es", "0.1.3", "", ""))}}
	reg, o := newPackRegistry(t, withWeb(web))
	nf := &fakeNotify{}
	reg.SetNotify(nf)
	var heard []string
	reg.SetUpgradeListener(func(app, version string) { heard = append(heard, app+"@"+version) })
	st := installPackFromGitHub(t, reg, "main")
	assert.Equal(t, "github", st.UpdateSource)
	assert.Nil(t, st.Previous, "nothing kept yet")

	rep, err := reg.CheckUpdates(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Checked)
	assert.Empty(t, nf.events)

	web.ok[packRaw("main")] = string(packWithRange(t, "lang-es", "0.1.4", "", ""))
	for i := 0; i < 2; i++ {
		rep, err = reg.CheckUpdates(context.Background())
		require.NoError(t, err)
		assert.Empty(t, rep.Updated, "the check installs nothing")
		assert.Equal(t, []string{"lang-es"}, rep.Available)
	}
	p, ok := reg.ByName("lang-es")
	require.True(t, ok)
	st = reg.StatusOf(p)
	assert.Equal(t, "0.1.3", st.Version, "nothing moved by itself")
	assert.Equal(t, UpdateAvailable, st.Update.Status)
	assert.Equal(t, "0.1.4", st.Update.Version)
	require.Len(t, nf.events, 1, "announced once, not every day")
	assert.Equal(t, notify.EventAppUpdateAvailable, nf.events[0].Event)
	assert.Empty(t, heard)

	// The administrator approves (Review update → Upgrade).
	u, err := o.Store.CreateUser(context.Background(), "approver@local", "x", "admin", "en", "UTC")
	require.NoError(t, err)
	admin := u.ID
	in, err := reg.FetchUpdate(context.Background(), st.ID)
	require.NoError(t, err)
	in.ActorID = &admin
	st, _, err = reg.Upgrade(context.Background(), st.ID, in)
	require.NoError(t, err)
	assert.Equal(t, "0.1.4", st.Version)
	assert.Equal(t, UpdateCurrent, st.Update.Status)
	require.NotNil(t, st.Previous)
	assert.Equal(t, "0.1.3", st.Previous.Version, "the replaced version is kept")
	assert.Equal(t, []string{"lang-es@0.1.4"}, heard, "the open explorers are told")
	require.Len(t, nf.events, 2)
	assert.Equal(t, notify.EventAppUpdated, nf.events[1].Event)
	assert.Equal(t, "0.1.4", nf.events[1].Meta["version"])
	assert.Equal(t, "0.1.3", nf.events[1].Meta["from"])
	rows, err := o.Store.ListAuditRecent(context.Background(), 20)
	require.NoError(t, err)
	var audited bool
	for _, r := range rows {
		if r.Action == "app_plugin.upgrade" && r.UserID != nil && *r.UserID == admin {
			audited = r.Metadata["from"] == "0.1.3" && r.Metadata["to"] == "0.1.4"
		}
	}
	assert.True(t, audited, "the audit row names the administrator who approved, from and to")

	// Back to 0.1.3: no approval asked, and 0.1.4 is kept in its turn.
	st, err = reg.Rollback(context.Background(), st.ID, &admin, "en")
	require.NoError(t, err)
	assert.Equal(t, "0.1.3", st.Version)
	assert.Equal(t, StateRunning, st.State)
	require.NotNil(t, st.Previous)
	assert.Equal(t, "0.1.4", st.Previous.Version)
	assert.Contains(t, auditActions(t, o), "app_plugin.rollback")
	assert.Equal(t, []string{"lang-es@0.1.4", "lang-es@0.1.3"}, heard)

	// A restart keeps all of it.
	reg.Close(context.Background())
	again, err := New(o)
	require.NoError(t, err)
	t.Cleanup(func() { again.Close(context.Background()) })
	require.NoError(t, again.Load(context.Background()))
	p, _ = again.ByName("lang-es")
	got := again.StatusOf(p)
	assert.Equal(t, "0.1.3", got.Version)
	require.NotNil(t, got.Previous)
	assert.Equal(t, "0.1.4", got.Previous.Version)
}

// An app installed at a version TAG follows the releases, and announces the
// newest one THIS filex can run — stepping over one that needs a newer filex
// — with the release's notes for the review.
func TestUpdates_ReleasesAreFollowedToTheNewestCompatibleOne(t *testing.T) {
	withHost(t, "0.47.0")
	releases := []map[string]any{
		{"tag_name": "v0.3.0", "draft": false, "prerelease": false, "body": "needs 0.48"},
		{"tag_name": "v0.2.1", "draft": false, "prerelease": false, "body": "Fixes the plural of *archivo*."},
		{"tag_name": "v0.2.5-rc.1", "draft": false, "prerelease": true},
		{"tag_name": "v0.2.9", "draft": true, "prerelease": false},
		{"tag_name": "v0.1.0", "draft": false, "prerelease": false},
	}
	rel, _ := json.Marshal(releases)
	web := &fakeWeb{ok: map[string]string{
		packRaw("v0.1.0"):      string(packWithRange(t, "lang-es", "0.1.0", "", "")),
		packRaw("v0.2.1"):      string(packWithRange(t, "lang-es", "0.2.1", ">=0.46.0", "")),
		packRaw("v0.3.0"):      string(packWithRange(t, "lang-es", "0.3.0", ">=0.48.0", "")),
		packRaw("v0.2.9"):      string(packWithRange(t, "lang-es", "0.2.9", "", "")),
		packRaw("v0.2.5-rc.1"): string(packWithRange(t, "lang-es", "0.2.5", "", "")),
		releasesURL(packRepo):  string(rel),
	}}
	reg, _ := newPackRegistry(t, withWeb(web))
	st := installPackFromGitHub(t, reg, "v0.1.0")

	rep, err := reg.CheckUpdates(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"lang-es"}, rep.Available)
	p, _ := reg.ByName("lang-es")
	got := reg.StatusOf(p)
	assert.Equal(t, "0.1.0", got.Version, "announced, not installed")
	assert.Equal(t, "0.2.1", got.Update.Version, "0.3.0 needs filex 0.48; the draft and the pre-release are not releases")
	assert.Equal(t, "Fixes the plural of *archivo*.", got.Update.Notes)

	in, err := reg.FetchUpdate(context.Background(), st.ID)
	require.NoError(t, err)
	assert.Equal(t, "Fixes the plural of *archivo*.", in.Notes, "the review gets the notes")
	st, _, err = reg.Upgrade(context.Background(), st.ID, in)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/"+packRepo+"@v0.2.1", st.SourceURL, "the next check follows the releases from here")
}

// Only newer versions that need a newer filex: nothing moves, the list says
// which version and what it needs, and nobody's bell rings (it is the
// server that is behind, and filex's own update notice says that).
func TestUpdates_OnlyNewerVersionsThisFilexCannotRun(t *testing.T) {
	withHost(t, "0.47.0")
	web := &fakeWeb{ok: map[string]string{packRaw("main"): string(packWithRange(t, "lang-es", "0.1.0", "", ""))}}
	reg, _ := newPackRegistry(t, withWeb(web))
	nf := &fakeNotify{}
	reg.SetNotify(nf)
	installPackFromGitHub(t, reg, "main")
	web.ok[packRaw("main")] = string(packWithRange(t, "lang-es", "0.2.0", ">=0.48.0", ""))

	_, err := reg.CheckUpdates(context.Background())
	require.NoError(t, err)
	p, _ := reg.ByName("lang-es")
	st := reg.StatusOf(p)
	assert.Equal(t, "0.1.0", st.Version)
	require.NotNil(t, st.Update)
	assert.Equal(t, UpdateIncompatible, st.Update.Status)
	assert.Equal(t, "0.2.0", st.Update.Version)
	assert.Equal(t, ">=0.48.0", st.Update.Requires)
	assert.Empty(t, nf.events)
}

// A language pack that now brings a module is code that runs where there was
// none: never installed by itself, whatever its permissions.
func TestUpdates_APackThatGrowsAModuleWaitsForApproval(t *testing.T) {
	withHost(t, "0.47.0")
	web := &fakeWeb{ok: map[string]string{packRaw("main"): string(packWithRange(t, "lang-es", "0.1.0", "", ""))}}
	reg, _ := newPackRegistry(t, withWeb(web))
	nf := &fakeNotify{}
	reg.SetNotify(nf)
	installPackFromGitHub(t, reg, "main")
	var m map[string]any
	require.NoError(t, json.Unmarshal(packWithRange(t, "lang-es", "0.2.0", "", ""), &m))
	m["permissions"] = []string{"files:read"}
	m["wasm"] = map[string]any{"url": "plugin.wasm", "sha256": sha256Hex([]byte("a module"))}
	b, _ := json.Marshal(m)
	web.ok[packRaw("main")] = string(b)

	rep, err := reg.CheckUpdates(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"lang-es"}, rep.NeedsApproval)
	p, _ := reg.ByName("lang-es")
	st := reg.StatusOf(p)
	assert.Equal(t, "0.1.0", st.Version)
	require.Equal(t, UpdateNeedsApproval, st.Update.Status, "%+v", st.Update.Refusal)
	assert.True(t, st.Update.AddsModule)
	assert.Equal(t, []string{"files:read"}, st.Update.Added)
	require.Len(t, nf.events, 1)
	assert.Equal(t, notify.EventAppUpdateNeedsApproval, nf.events[0].Event)
	assert.Equal(t, "files:read", nf.events[0].Meta["added"])
}

// A source that cannot be read is said on the list, not in the bell: an
// air-gapped install would hear it every day.
func TestUpdates_AnUnreachableSourceIsNotRung(t *testing.T) {
	web := &fakeWeb{ok: map[string]string{packRaw("main"): string(packWithRange(t, "lang-es", "0.1.0", "", ""))}}
	reg, _ := newPackRegistry(t, withWeb(web))
	nf := &fakeNotify{}
	reg.SetNotify(nf)
	installPackFromGitHub(t, reg, "main")
	web.down = true
	rep, err := reg.CheckUpdates(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"lang-es"}, rep.Failed)
	p, _ := reg.ByName("lang-es")
	st := reg.StatusOf(p)
	assert.Equal(t, UpdateCheckFailed, st.Update.Status)
	require.NotNil(t, st.Update.Refusal)
	assert.Equal(t, ErrCodeFetch, st.Update.Refusal.Code)
	assert.Equal(t, FetchReasonUnreachable, st.Update.Refusal.Reason)
	assert.Empty(t, nf.events)
}

// An uploaded app has nowhere to ask: it is not checked, and its row says so.
func TestUpdates_AnUploadedAppHasNoSource(t *testing.T) {
	reg, _ := newPackRegistry(t, withWeb(&fakeWeb{}))
	st, _, err := reg.Install(context.Background(), &InstallInput{Manifest: packWithRange(t, "lang-es", "0.1.0", "", ""), Source: "upload", Granted: []string{}})
	require.NoError(t, err)
	assert.Empty(t, st.UpdateSource)
	rep, err := reg.CheckUpdates(context.Background())
	require.NoError(t, err)
	assert.Zero(t, rep.Checked)
}

// Demo: no check, no background loop.
func TestUpdates_TheDemoNeverChecks(t *testing.T) {
	reg, _ := newPackRegistry(t, func(o *Options) { o.Demo = true })
	_, err := reg.CheckUpdates(context.Background())
	assert.Equal(t, ErrCodeDemo, installError(t, err).Code)
	reg.StartUpdater(context.Background(), true)
	assert.False(t, reg.BackgroundUpdates())
	_, err = reg.Rollback(context.Background(), 1, nil, "en")
	assert.Error(t, err)
}

// blockingWeb holds every request until released, counting them.
type blockingWeb struct {
	fakeWeb
	release chan struct{}
	asked   atomic.Int32
}

func (b *blockingWeb) RoundTrip(req *http.Request) (*http.Response, error) {
	b.asked.Add(1)
	<-b.release
	return b.fakeWeb.RoundTrip(req)
}

// One check at a time, whoever asks: a second caller waits for the one in
// flight and gets its answer — one read of the source, not two.
func TestUpdates_OneCheckAtATime(t *testing.T) {
	manifest := string(packWithRange(t, "lang-es", "0.1.0", "", ""))
	web := &fakeWeb{ok: map[string]string{packRaw("main"): manifest}}
	reg, _ := newPackRegistry(t, withWeb(web))
	installPackFromGitHub(t, reg, "main")

	bw := &blockingWeb{fakeWeb: fakeWeb{ok: map[string]string{packRaw("main"): manifest}}, release: make(chan struct{})}
	reg.opts.HTTP = &http.Client{Transport: bw}
	var wg sync.WaitGroup
	reports := make([]*UpdateReport, 2)
	for i := range reports {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rep, err := reg.CheckUpdates(context.Background())
			assert.NoError(t, err)
			reports[i] = rep
		}(i)
		if i == 0 {
			require.Eventually(t, func() bool { return bw.asked.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
		}
	}
	time.Sleep(50 * time.Millisecond)
	close(bw.release)
	wg.Wait()
	assert.EqualValues(t, 1, bw.asked.Load(), "the second caller joined the first check")
	assert.Same(t, reports[0], reports[1])
}

// The daily beat is kept across restarts: the time of the last check is
// stored, and the next one is due a day after it — never at every boot, and
// never sooner than a couple of minutes after one.
func TestUpdates_TheDailyBeatSurvivesARestart(t *testing.T) {
	reg, o := newPackRegistry(t, withWeb(&fakeWeb{}))
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	reg.updates.now = func() time.Time { return now }
	ctx := context.Background()
	assert.Equal(t, updateFirstWait, reg.untilNextCheck(ctx), "never checked: soon after start")

	_, err := reg.CheckUpdates(ctx)
	require.NoError(t, err)
	reg.Close(ctx)

	again, err := New(o)
	require.NoError(t, err)
	t.Cleanup(func() { again.Close(ctx) })
	assert.True(t, again.LastUpdateCheck(ctx).Equal(now), "stored, not in memory")
	again.updates.now = func() time.Time { return now.Add(3 * time.Hour) }
	assert.Equal(t, 21*time.Hour, again.untilNextCheck(ctx))
	again.updates.now = func() time.Time { return now.Add(30 * time.Hour) }
	assert.Equal(t, updateFirstWait, again.untilNextCheck(ctx), "overdue: soon after start, not in the middle of it")
}

// ⚠ A process stopped in the middle of an upgrade leaves the old files
// stashed beside the new ones. At the next start the row decides which of
// the two it describes: a stash the row still describes goes back; a stash
// left over after the row was written goes.
func TestLoad_AnInterruptedUpgradeIsUndone(t *testing.T) {
	ctx := context.Background()
	reg, o := newPackRegistry(t, nil)
	v1 := packWithRange(t, "lang-es", "0.1.0", "", "")
	_, _, err := reg.Install(ctx, &InstallInput{Manifest: v1, Granted: []string{}})
	require.NoError(t, err)
	reg.Close(ctx)

	dir := filepath.Join(o.Dir, "lang-es")
	// Killed after the stash and the new files, before the row was written.
	require.NoError(t, os.Rename(dir, dir+".prev"))
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "filex-app.json"), packWithRange(t, "lang-es", "0.2.0", "", ""), 0o600))

	again, err := New(o)
	require.NoError(t, err)
	require.NoError(t, again.Load(ctx))
	p, ok := again.ByName("lang-es")
	require.True(t, ok)
	assert.Equal(t, StateRunning, again.StatusOf(p).State)
	got, err := os.ReadFile(filepath.Join(dir, "filex-app.json"))
	require.NoError(t, err)
	assert.Equal(t, string(v1), string(got), "the version the row describes is back")
	assert.NoDirExists(t, dir+".prev")
	again.Close(ctx)

	// Killed after the row was written: the stash is garbage.
	require.NoError(t, os.MkdirAll(dir+".prev", 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir+".prev", "filex-app.json"), []byte("{}"), 0o600))
	third, err := New(o)
	require.NoError(t, err)
	t.Cleanup(func() { third.Close(ctx) })
	require.NoError(t, third.Load(ctx))
	got, _ = os.ReadFile(filepath.Join(dir, "filex-app.json"))
	assert.Equal(t, string(v1), string(got))
	assert.NoDirExists(t, dir+".prev")
}

// A shutdown waits for the upgrade in flight to finish its swap, and no
// upgrade starts after it.
func TestClose_WaitsForTheUpgradeInFlight(t *testing.T) {
	reg, _ := newPackRegistry(t, nil)
	st, _, err := reg.Install(context.Background(), &InstallInput{Manifest: packWithRange(t, "lang-es", "0.1.0", "", ""), Granted: []string{}})
	require.NoError(t, err)

	reg.upgradeMu.Lock() // an upgrade is swapping files
	closed := make(chan struct{})
	go func() {
		reg.Close(context.Background())
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close did not wait for the upgrade in flight")
	case <-time.After(100 * time.Millisecond):
	}
	reg.upgradeMu.Unlock()
	<-closed

	_, _, err = reg.Upgrade(context.Background(), st.ID, &InstallInput{Manifest: packWithRange(t, "lang-es", "0.2.0", "", "")})
	assert.Error(t, err, "nothing is swapped once the registry is closing")
}

// ── With a module (the echo fixture) ───────────────────────────────────

// echoFromURL installs the echo module through a URL install the fake web
// serves, and returns the web so a test can publish the next version.
func echoFromURL(t *testing.T) (*harness, *fakeWeb, *Status) {
	t.Helper()
	// Skips without the fixture like every other module test (and fails on
	// CI, where FILEX_REQUIRE_WASM_FIXTURE is set) instead of failing on a
	// missing file.
	wasmfixture.Require(t, fixtureWasm)
	raw, err := os.ReadFile("testdata/echo/manifest.json")
	require.NoError(t, err)
	wasm, err := os.ReadFile(fixtureWasm)
	require.NoError(t, err)
	web := &fakeWeb{ok: map[string]string{
		"https://apps.example.test/echo/filex-app.json": string(raw),
		"https://apps.example.test/echo/plugin.wasm":    string(wasm),
	}}
	h := newHarness(t, withWeb(web))
	// The echo manifest carries no wasm.sha256, so the module is pinned here
	// the way an administrator installing it by address would.
	in, err := h.reg.FetchURL(context.Background(), URLInput{
		URL: "https://apps.example.test/echo/plugin.wasm", ManifestURL: "https://apps.example.test/echo/filex-app.json",
		SHA256: sha256Hex(wasm),
	})
	require.NoError(t, err)
	var m Manifest
	require.NoError(t, json.Unmarshal(raw, &m.Manifest))
	in.Granted = m.Permissions
	st, _, err := h.reg.Install(context.Background(), in)
	require.NoError(t, err)
	return h, web, st
}

// nextEcho publishes the echo manifest at `version`, changed by mutate, with
// the module pinned by its hash.
func nextEcho(t *testing.T, web *fakeWeb, version string, mutate func(m map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile("testdata/echo/manifest.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	m["version"] = version
	m["wasm"] = map[string]any{"url": "https://apps.example.test/echo/plugin.wasm", "sha256": sha256Hex([]byte(web.ok["https://apps.example.test/echo/plugin.wasm"]))}
	if mutate != nil {
		mutate(m)
	}
	b, _ := json.Marshal(m)
	web.ok["https://apps.example.test/echo/filex-app.json"] = string(b)
}

// A newer version that asks for a permission the app was not granted is
// NEVER installed by itself: it waits, marked, and the bell says what it
// asks for — once.
func TestUpdates_ANewPermissionWaitsForApproval(t *testing.T) {
	withHost(t, "0.47.0")
	h, web, st := echoFromURL(t)
	nf := &fakeNotify{}
	h.reg.SetNotify(nf)
	nextEcho(t, web, "0.0.2", func(m map[string]any) {
		m["permissions"] = append(m["permissions"].([]any), "http:example.org")
	})

	for i := 0; i < 2; i++ {
		rep, err := h.reg.CheckUpdates(context.Background())
		require.NoError(t, err)
		assert.Equal(t, []string{"echo"}, rep.NeedsApproval)
	}
	p, _ := h.reg.ByID(st.ID)
	got := h.reg.StatusOf(p)
	assert.Equal(t, "0.0.1", got.Version, "nothing moved")
	assert.Equal(t, UpdateNeedsApproval, got.Update.Status)
	assert.Equal(t, []string{"http:example.org"}, got.Update.Added)
	require.Len(t, nf.events, 1)
	assert.Equal(t, notify.EventAppUpdateNeedsApproval, nf.events[0].Event)
	assert.Equal(t, "http:example.org", nf.events[0].Meta["added"])
	assert.Contains(t, nf.events[0].Title, "new permission http:example.org")
}

// A newer MODULE that asks for nothing new is not installed by itself either:
// the grant says what a module may reach, not what new code does with it.
func TestUpdates_ANewModuleIsNeverInstalledByTheCheck(t *testing.T) {
	withHost(t, "0.47.0")
	h, web, st := echoFromURL(t)
	nf := &fakeNotify{}
	h.reg.SetNotify(nf)
	nextEcho(t, web, "0.0.2", nil)

	rep, err := h.reg.CheckUpdates(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"echo"}, rep.Available)
	assert.Empty(t, rep.Updated)
	p, _ := h.reg.ByID(st.ID)
	got := h.reg.StatusOf(p)
	assert.Equal(t, "0.0.1", got.Version)
	assert.Equal(t, UpdateAvailable, got.Update.Status)
	require.Len(t, nf.events, 1)
	assert.Equal(t, notify.EventAppUpdateAvailable, nf.events[0].Event)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// "Review update" reads what the check would install, through the same
// search and fetch — and says so when there is nothing newer, or only a
// version this filex cannot run.
func TestFetchUpdate_IsWhatTheCheckWouldInstall(t *testing.T) {
	withHost(t, "0.47.0")
	web := &fakeWeb{ok: map[string]string{packRaw("main"): string(packWithRange(t, "lang-es", "0.1.0", "", ""))}}
	reg, _ := newPackRegistry(t, withWeb(web))
	st := installPackFromGitHub(t, reg, "main")
	ctx := context.Background()

	_, err := reg.FetchUpdate(ctx, st.ID)
	assert.Equal(t, ErrCodeUpToDate, installError(t, err).Code)

	web.ok[packRaw("main")] = string(packWithRange(t, "lang-es", "0.2.0", ">=0.48.0", ""))
	_, err = reg.FetchUpdate(ctx, st.ID)
	ie := installError(t, err)
	assert.Equal(t, ErrCodeIncompatible, ie.Code)
	assert.Equal(t, ">=0.48.0", ie.Requires)

	web.ok[packRaw("main")] = string(packWithRange(t, "lang-es", "0.1.5", "", ""))
	in, err := reg.FetchUpdate(ctx, st.ID)
	require.NoError(t, err)
	in.DryRun = true
	_, dry, err := reg.Upgrade(ctx, st.ID, in)
	require.NoError(t, err)
	assert.Equal(t, "0.1.5", dry.Manifest.Version)
	require.NotNil(t, dry.Upgrade)
	assert.Equal(t, "0.1.0", dry.Upgrade.From)

	up, _, err := reg.Install(ctx, &InstallInput{Manifest: packWithRange(t, "lang-de", "0.1.0", "", ""), Source: "upload", Granted: []string{}})
	require.NoError(t, err)
	_, err = reg.FetchUpdate(ctx, up.ID)
	assert.Equal(t, ErrCodeManifestInvalid, installError(t, err).Code, "an uploaded app has no source")
}
