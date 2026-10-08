package notify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// The notification digest (task #166, digest.go): the kinds a person did not
// mark urgent are told in ONE notification at the end of a window, folder by
// folder. Before it, a bulk upload of 30 files was 30 bell rows, 30 badge
// steps, 30 browser pop-ups and 30 desktop toasts — the owner's report
// ("tek tek bildirince notification spam'e düşüyor").
//
// ⚠ Out of the box EVERY kind is urgent (the owner's decision, 2026-10-06):
// the digest is opt-in. Each test that measures a digest holds its kinds
// first, the way an administrator (a tenant's policy) or a person (their own
// settings) does; TestDigest_ByDefaultEveryKindIsToldAtOnce pins the default.
//
// ⚠ Red on the code before it: notify.Config has no Digest, notify.Digests
// does not exist, and none of this compiles.

// digestClock moves the digest's clock forward without sleeping. The rows are
// stamped by the database with the real time, so the clock starts at now.
type digestClock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *digestClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.off)
}

func (c *digestClock) advance(d time.Duration) {
	c.mu.Lock()
	c.off += d
	c.mu.Unlock()
}

// mailbox records the emails the service sends.
type mailbox struct {
	mu   sync.Mutex
	sent []sentMail
}

type sentMail struct{ lang, to, subject, body string }

func (m *mailbox) send(_ context.Context, lang, to, subject, body string) error {
	m.mu.Lock()
	m.sent = append(m.sent, sentMail{lang, to, subject, body})
	m.mu.Unlock()
	return nil
}

func (m *mailbox) all() []sentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]sentMail(nil), m.sent...)
}

func digestService(t *testing.T, store db.Store, clock *digestClock, cfg notify.Config) (notify.Service, notify.Digests) {
	t.Helper()
	cfg.RetryBackoffs = []time.Duration{}
	if cfg.Digest == nil {
		cfg.Digest = &notify.DigestConfig{Every: time.Hour}
	}
	cfg.Now = clock.now
	svc := notify.New(store, cfg)
	dg, ok := svc.(notify.Digests)
	require.True(t, ok, "the service has no digest half")
	return svc, dg
}

func digestUser(t *testing.T, store db.Store, email, locale string) int64 {
	t.Helper()
	u, err := store.CreateUser(context.Background(), email, "x", model.RoleUser, locale, "UTC")
	require.NoError(t, err)
	return u.ID
}

// holds makes uid hold these kinds for the digest - the person's own choice
// (user settings → Notifications, the Urgent switch off). Every other kind
// stays as the defaults say: told at once.
func holds(t *testing.T, svc notify.Service, dg notify.Digests, uid int64, kinds ...string) {
	t.Helper()
	o := map[string]bool{}
	for _, k := range kinds {
		o[k] = false
	}
	raw, err := json.Marshal(o)
	require.NoError(t, err)
	require.NoError(t, svc.UpsertSettings(context.Background(), &model.NotificationSettings{
		UserID: uid, InAppEnabled: true, MutedEventsRaw: []byte(`[]`), UrgentOverridesRaw: raw,
	}))
	dg.ForgetDigest(uid)
}

// allBut is every kind but these - an urgent list that holds them.
func allBut(kinds ...string) []string {
	var out []string
	for _, e := range notify.DigestEvents() {
		keep := true
		for _, k := range kinds {
			if e == k {
				keep = false
			}
		}
		if keep {
			out = append(out, e)
		}
	}
	return out
}

// upload is what writehook sends for a new file, addressed to the person who
// wrote it.
func upload(t *testing.T, svc notify.Service, uid, storageID int64, path string) int64 {
	t.Helper()
	name := path[strings.LastIndex(path, "/")+1:]
	id, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventFileUploaded,
		Severity: notify.SeverityInfo,
		Body:     path,
		Meta:     map[string]any{"origin": "manager"},
		Node:     &notify.NodeRef{StorageID: storageID, Path: path, Name: name},
		Target:   notify.FileTarget(path),
		UserID:   &uid,
	})
	require.NoError(t, err)
	require.Greater(t, id, int64(0))
	return id
}

func unread(t *testing.T, svc notify.Service, uid int64) []*model.Notification {
	t.Helper()
	rows, total, err := svc.List(context.Background(), &uid, notify.MemberBell, true, 100, 0)
	require.NoError(t, err)
	n, err := svc.UnreadCount(context.Background(), &uid, notify.MemberBell)
	require.NoError(t, err)
	require.EqualValues(t, len(rows), total)
	require.EqualValues(t, total, n, "the badge and the unread list disagree")
	return rows
}

func everyRow(t *testing.T, svc notify.Service, uid int64) []*model.Notification {
	t.Helper()
	rows, _, err := svc.List(context.Background(), &uid, notify.MemberBell, false, 200, 0)
	require.NoError(t, err)
	return rows
}

func digestsOf(rows []*model.Notification) []*model.Notification {
	var out []*model.Notification
	for _, n := range rows {
		if n.Event == string(notify.EventNotificationDigest) {
			out = append(out, n)
		}
	}
	return out
}

type digestMeta struct {
	Count  int `json:"count"`
	Groups []struct {
		Storage   string         `json:"storage"`
		Path      string         `json:"path"`
		Name      string         `json:"name"`
		Encrypted bool           `json:"encrypted"`
		Count     int            `json:"count"`
		Counts    map[string]int `json:"counts"`
	} `json:"groups"`
	Other map[string]int `json:"other"`
	Item  *struct {
		Event string          `json:"event"`
		Meta  json.RawMessage `json:"meta"`
	} `json:"item"`
}

func metaOf(t *testing.T, n *model.Notification) digestMeta {
	t.Helper()
	var m digestMeta
	require.NoError(t, json.Unmarshal(n.MetaJSON, &m))
	return m
}

// The default: every kind is told at once - an upgrade changes nobody's
// notifications. Every kind a choice can be made about is sent, and 30 uploads
// on top: all unread at once, the badge agrees, no window is ever opened and
// no digest is written however long the clock runs.
func TestDigest_ByDefaultEveryKindIsToldAtOnce(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	clock := &digestClock{}
	svc, dg := digestService(t, store, clock, notify.Config{})
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "tr")
	ctx := context.Background()

	require.ElementsMatch(t, notify.DigestEvents(), notify.DefaultUrgentEvents(), "a kind is held out of the box")
	pol, saved, err := dg.DigestPolicy(ctx, 0)
	require.NoError(t, err)
	require.False(t, saved)
	require.ElementsMatch(t, notify.DigestEvents(), pol.Urgent)

	for i := 0; i < 30; i++ {
		upload(t, svc, uid, st.ID, "Rapor/belge-"+string(rune('a'+i%26))+strings.Repeat("x", i/26)+".pdf")
	}
	for _, ev := range notify.DigestEvents() {
		_, err := svc.Send(ctx, notify.Event{
			Event: notify.EventType(ev), Severity: notify.SeverityInfo,
			Node:   &notify.NodeRef{StorageID: st.ID, Path: "Rapor/x.pdf", Name: "x.pdf"},
			UserID: &uid,
		})
		require.NoError(t, err)
	}
	want := 30 + len(notify.DigestEvents())
	// An administrator's bell: the operator alarms are in it too.
	rows, total, err := svc.List(ctx, &uid, notify.AdminBell, true, 500, 0)
	require.NoError(t, err)
	require.EqualValues(t, want, total, "a notification was held although nobody chose to hold anything")
	require.Len(t, rows, want)
	n, err := svc.UnreadCount(ctx, &uid, notify.AdminBell)
	require.NoError(t, err)
	require.EqualValues(t, want, n, "the badge and the list disagree")

	clock.advance(16 * time.Minute)
	made, err := dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Zero(t, made)
	_, err = svc.UnreadCount(ctx, &uid, notify.AdminBell)
	require.NoError(t, err)
	all, _, err := svc.List(ctx, &uid, notify.AdminBell, false, 500, 0)
	require.NoError(t, err)
	require.Empty(t, digestsOf(all), "a digest was written by default")
	_, due, _, err := store.DigestState(ctx, uid)
	require.NoError(t, err)
	require.Nil(t, due, "a window was opened by default")

	view, err := dg.PersonDigest(ctx, uid)
	require.NoError(t, err)
	require.ElementsMatch(t, notify.DigestEvents(), view.Urgent)
}

// 30 files uploaded into one folder within the minute are ONE notification,
// and every one of the 30 rows is still there, in the list, read - once the
// person holds new files for the digest.
func TestDigest_ThirtyUploadsInAMinuteAreOneNotification(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	clock := &digestClock{}
	svc, dg := digestService(t, store, clock, notify.Config{})
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "tr")
	ctx := context.Background()
	holds(t, svc, dg, uid, string(notify.EventFileUploaded))

	for i := 0; i < 30; i++ {
		upload(t, svc, uid, st.ID, "Rapor/belge-"+string(rune('a'+i%26))+strings.Repeat("x", i/26)+".pdf")
	}
	require.Empty(t, unread(t, svc, uid), "a held upload raised the badge")
	rows := everyRow(t, svc, uid)
	require.Len(t, rows, 30, "the history lost a row: every event keeps its own")
	for _, n := range rows {
		require.NotNil(t, n.ReadAt, "a held row reads as unread (a pop-up would be raised for it)")
	}

	made, err := dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Zero(t, made, "told before the window ended")

	clock.advance(61 * time.Second)
	made, err = dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, made)

	got := unread(t, svc, uid)
	require.Len(t, got, 1, "30 uploads were told as more than one notification")
	d := got[0]
	require.Equal(t, string(notify.EventNotificationDigest), d.Event)
	require.NotNil(t, d.UserID)
	require.Equal(t, uid, *d.UserID)
	m := metaOf(t, d)
	require.Equal(t, 30, m.Count)
	require.Len(t, m.Groups, 1)
	require.Equal(t, "ekip", m.Groups[0].Storage)
	require.Equal(t, "Rapor", m.Groups[0].Path)
	require.Equal(t, "Rapor", m.Groups[0].Name)
	require.Equal(t, 30, m.Groups[0].Counts[string(notify.EventFileUploaded)])
	require.Contains(t, d.Body, "Rapor: 30 files added")
	// One folder: a click opens it.
	require.NotNil(t, d.Target)
	require.Equal(t, model.TargetDir, d.Target.Kind)
	require.Equal(t, "ekip", d.Target.Storage)
	require.Equal(t, "Rapor", d.Target.Path)

	// The 30 are read for the person now — for good, not by the quiet rule.
	all := everyRow(t, svc, uid)
	require.Len(t, all, 31)
	for _, n := range all {
		if n.ID != d.ID {
			stored, err := store.GetNotification(ctx, n.ID)
			require.NoError(t, err)
			require.NotNil(t, stored.ReadAt, "row %d was carried by the digest and left unread", n.ID)
		}
	}

	// Nothing more to tell.
	made, err = dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Zero(t, made)
	require.Len(t, unread(t, svc, uid), 1)
}

// What a person holds waits; every other kind - their own urgent choice and
// the defaults alike - is told the moment it happens. A person may put even a
// security alert in the digest (the owner's decision, 2026-10-06).
func TestDigest_AnUrgentKindIsToldAtOnce(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	clock := &digestClock{}
	svc, dg := digestService(t, store, clock, notify.Config{})
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "tr")
	ctx := context.Background()

	infected := func(path string) {
		_, err := svc.Send(ctx, notify.Event{
			Event: notify.EventFileInfected, Severity: notify.SeverityWarning,
			Node: &notify.NodeRef{StorageID: st.ID, Path: path, Name: "virus.exe"}, UserID: &uid,
		})
		require.NoError(t, err)
	}

	// A virus: told at once, as every kind is by default.
	infected("Rapor/virus.exe")
	got := unread(t, svc, uid)
	require.Len(t, got, 1, "a security alert waited for the digest")
	require.Equal(t, string(notify.EventFileInfected), got[0].Event)

	// The person keeps uploads urgent and holds viruses.
	require.NoError(t, svc.UpsertSettings(ctx, &model.NotificationSettings{
		UserID: uid, InAppEnabled: true, MutedEventsRaw: []byte(`[]`),
		UrgentOverridesRaw: []byte(`{"file.uploaded":true,"file.infected":false}`),
	}))
	dg.ForgetDigest(uid)
	upload(t, svc, uid, st.ID, "Rapor/a.pdf")
	got = unread(t, svc, uid)
	require.Len(t, got, 2, "an upload the person kept urgent was held")

	// …and puts viruses in the digest: held now.
	infected("Rapor/virus2.exe")
	require.Len(t, unread(t, svc, uid), 2, "a security alert the person put in the digest was told at once")
	clock.advance(61 * time.Second)
	made, err := dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, made)
	got = unread(t, svc, uid)
	require.Len(t, got, 3)

	// What the settings pane shows.
	view, err := dg.PersonDigest(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, 1, view.WindowMinutes)
	require.Contains(t, view.Urgent, string(notify.EventFileUploaded))
	require.NotContains(t, view.Urgent, string(notify.EventFileInfected))
	require.Contains(t, view.Defaults, string(notify.EventFileInfected), "the default is still urgent")
	require.Contains(t, view.Defaults, string(notify.EventFileUploaded), "the default is every kind")
	require.Contains(t, view.AdminEvents, string(notify.EventUpdateAvailable))
}

// The edges a window has, none of which may lose or repeat a row: a window of
// one row (told as that row, opening what it opens), two windows one after the
// other, a window with nothing in it, and the administrator's window length.
func TestDigest_WindowEdgesLoseNothingAndRepeatNothing(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	clock := &digestClock{}
	svc, dg := digestService(t, store, clock, notify.Config{})
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "en")
	ctx := context.Background()
	holds(t, svc, dg, uid, string(notify.EventFileUploaded))

	// Nothing held: nothing told.
	made, err := dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Zero(t, made)
	require.Empty(t, digestsOf(everyRow(t, svc, uid)))

	// A window of one: the digest says what that one says and opens its file.
	upload(t, svc, uid, st.ID, "Rapor/tek.pdf")
	clock.advance(61 * time.Second)
	made, err = dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, made)
	got := unread(t, svc, uid)
	require.Len(t, got, 1)
	m := metaOf(t, got[0])
	require.Equal(t, 1, m.Count)
	require.NotNil(t, m.Item, "a digest of one does not carry its row")
	require.Equal(t, string(notify.EventFileUploaded), m.Item.Event)
	require.NotNil(t, got[0].Target)
	require.Equal(t, model.TargetFile, got[0].Target.Kind, "a single upload no longer opens its file")
	require.Equal(t, "Rapor/tek.pdf", got[0].Target.Path)

	// Two windows one after the other: two digests, every row in exactly one.
	upload(t, svc, uid, st.ID, "Rapor/b1.pdf")
	upload(t, svc, uid, st.ID, "Fotograflar/b2.jpg")
	clock.advance(61 * time.Second)
	made, err = dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, made)
	upload(t, svc, uid, st.ID, "Rapor/c1.pdf")
	upload(t, svc, uid, st.ID, "Rapor/c2.pdf")
	upload(t, svc, uid, st.ID, "Rapor/c3.pdf")
	made, err = dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Zero(t, made, "the second window was told before it ended")
	clock.advance(61 * time.Second)
	made, err = dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, made)

	digests := digestsOf(everyRow(t, svc, uid))
	require.Len(t, digests, 3)
	counts := []int{}
	for i := len(digests) - 1; i >= 0; i-- { // oldest first
		counts = append(counts, metaOf(t, digests[i]).Count)
	}
	require.Equal(t, []int{1, 2, 3}, counts, "a row was told twice, or not at all")
	second := metaOf(t, digests[1])
	require.Len(t, second.Groups, 2, "two folders, two lines")
	require.Nil(t, digests[1].Target, "a digest of two folders opens neither")

	// The administrator's window: five minutes.
	_, err = dg.SaveDigestPolicy(ctx, model.DigestPolicy{Scope: 0, WindowMinutes: 5})
	require.NoError(t, err)
	upload(t, svc, uid, st.ID, "Rapor/d.pdf")
	clock.advance(61 * time.Second)
	_, err = dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	_, err = svc.UnreadCount(ctx, &uid, notify.MemberBell)
	require.NoError(t, err)
	require.Len(t, digestsOf(everyRow(t, svc, uid)), 3, "a five-minute window was told after one")
	clock.advance(5 * time.Minute)
	made, err = dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, made)

	// The window is kept within 1-15 minutes whatever is asked.
	p, err := dg.SaveDigestPolicy(ctx, model.DigestPolicy{Scope: 0, WindowMinutes: 0})
	require.NoError(t, err)
	require.Equal(t, 1, p.WindowMinutes)
	p, err = dg.SaveDigestPolicy(ctx, model.DigestPolicy{Scope: 0, WindowMinutes: 99, Urgent: []string{"file.uploaded", "no.such.event"}})
	require.NoError(t, err)
	require.Equal(t, 15, p.WindowMinutes)
	require.Equal(t, []string{"file.uploaded"}, p.Urgent, "an unknown event id was stored")
}

// Two people's windows never mix: each digest counts its own person's rows.
func TestDigest_TwoPeopleAreToldApart(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	clock := &digestClock{}
	svc, dg := digestService(t, store, clock, notify.Config{})
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	ayse := digestUser(t, store, "ayse@example.test", "tr")
	ali := digestUser(t, store, "ali@example.test", "tr")
	ctx := context.Background()
	holds(t, svc, dg, ayse, string(notify.EventFileUploaded))
	holds(t, svc, dg, ali, string(notify.EventFileUploaded))

	for i := 0; i < 3; i++ {
		upload(t, svc, ayse, st.ID, "Ayse/a"+string(rune('0'+i))+".pdf")
	}
	for i := 0; i < 2; i++ {
		upload(t, svc, ali, st.ID, "Ali/b"+string(rune('0'+i))+".pdf")
	}
	clock.advance(61 * time.Second)
	made, err := dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, made)

	for _, c := range []struct {
		uid    int64
		folder string
		count  int
		not    string
	}{{ayse, "Ayse", 3, "Ali"}, {ali, "Ali", 2, "Ayse"}} {
		got := unread(t, svc, c.uid)
		require.Len(t, got, 1)
		m := metaOf(t, got[0])
		require.Equal(t, c.count, m.Count)
		require.Len(t, m.Groups, 1)
		require.Equal(t, c.folder, m.Groups[0].Path)
		require.NotContains(t, string(got[0].MetaJSON), c.not+"/", "another person's folder is in this digest")
		require.Len(t, everyRow(t, svc, c.uid), c.count+1, "another person's rows reached this list")
	}
}

// Each tenant's defaults are its own: an administrator holds new files for
// their tenant by default (five minutes in one tenant, one in the next), and
// a tenant whose administrator chose nothing is told of each at once.
func TestDigest_ATenantsDefaultsAreItsOwn(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	clock := &digestClock{}
	svc, dg := digestService(t, store, clock, notify.Config{Digest: &notify.DigestConfig{MultiTenant: true, Every: time.Hour}})
	defer svc.Stop()
	ctx := context.Background()
	st := newStorage(t, store, "ekip")
	slow, err := store.CreateProvider(ctx, &model.Provider{Slug: "yavas", Name: "Yavaş", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	fast, err := store.CreateProvider(ctx, &model.Provider{Slug: "hizli", Name: "Hızlı", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	plain, err := store.CreateProvider(ctx, &model.Provider{Slug: "olagan", Name: "Olağan", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	a := digestUser(t, store, "a@yavas.test", "tr")
	b := digestUser(t, store, "b@hizli.test", "tr")
	c := digestUser(t, store, "c@olagan.test", "tr")
	require.NoError(t, store.SetUserProvider(ctx, a, slow.ID, ""))
	require.NoError(t, store.SetUserProvider(ctx, b, fast.ID, ""))
	require.NoError(t, store.SetUserProvider(ctx, c, plain.ID, ""))
	require.Equal(t, slow.ID, dg.DigestScope(ctx, a))

	held := allBut(string(notify.EventFileUploaded))
	_, err = dg.SaveDigestPolicy(ctx, model.DigestPolicy{Scope: slow.ID, WindowMinutes: 5, Urgent: held})
	require.NoError(t, err)
	_, err = dg.SaveDigestPolicy(ctx, model.DigestPolicy{Scope: fast.ID, WindowMinutes: 1, Urgent: held})
	require.NoError(t, err)
	upload(t, svc, a, st.ID, "A/1.pdf")
	upload(t, svc, a, st.ID, "A/2.pdf")
	upload(t, svc, b, st.ID, "B/1.pdf")
	upload(t, svc, b, st.ID, "B/2.pdf")
	upload(t, svc, c, st.ID, "C/1.pdf")
	upload(t, svc, c, st.ID, "C/2.pdf")
	require.Len(t, unread(t, svc, c), 2, "a tenant that holds nothing waited for a digest")
	clock.advance(61 * time.Second)
	made, err := dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, made, "only the one-minute tenant's window has ended")
	require.Len(t, unread(t, svc, b), 1)
	require.Empty(t, unread(t, svc, a))

	clock.advance(5 * time.Minute)
	made, err = dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, made)
	got := unread(t, svc, a)
	require.Len(t, got, 1)
	require.Equal(t, "A", metaOf(t, got[0]).Groups[0].Path)
	require.Empty(t, digestsOf(everyRow(t, svc, c)), "another tenant's defaults held this one's files")
}

// A digest is made of the rows the person's own bell keeps (View.Keep — the
// HTTP layer's grants and tenant): a broadcast about a file they cannot see is
// neither named nor counted.
func TestDigest_AFileTheReaderCannotSeeIsNotInTheirDigest(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	clock := &digestClock{}
	svc, dg := digestService(t, store, clock, notify.Config{})
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "tr")
	ctx := context.Background()
	// Upload failures are urgent by default; this person holds them.
	require.NoError(t, svc.UpsertSettings(ctx, &model.NotificationSettings{
		UserID: uid, InAppEnabled: true, MutedEventsRaw: []byte(`[]`),
		UrgentOverridesRaw: []byte(`{"file.upload_failed":false}`),
	}))
	dg.ForgetDigest(uid)
	_, err := dg.SettleDigest(ctx, uid, notify.View{Bell: notify.MemberBell}, true)
	require.NoError(t, err)

	failed := func(path string) {
		_, err := svc.Send(ctx, notify.Event{
			Event: notify.EventFileUploadFailed, Severity: notify.SeverityWarning,
			Node: &notify.NodeRef{StorageID: st.ID, Path: path, Name: path[strings.LastIndex(path, "/")+1:]},
		})
		require.NoError(t, err)
	}
	failed("Ortak/a.pdf")
	failed("Gizli/maas-2026.xlsx")
	failed("Gizli/maas-2027.xlsx")

	// The grants: Ortak/ only.
	view := notify.View{Bell: notify.MemberBell, Keep: func(n *model.Notification) bool {
		return strings.Contains(string(n.MetaJSON), `"path":"Ortak/`)
	}}
	made, err := dg.SettleDigest(ctx, uid, view, false)
	require.NoError(t, err)
	require.False(t, made, "told before the window ended")
	clock.advance(61 * time.Second)
	made, err = dg.SettleDigest(ctx, uid, view, false)
	require.NoError(t, err)
	require.True(t, made)

	rows := digestsOf(everyRow(t, svc, uid))
	require.Len(t, rows, 1)
	blob := string(rows[0].MetaJSON) + rows[0].Body + rows[0].Title
	require.NotContains(t, blob, "Gizli", "a folder the reader cannot see is named in their digest")
	require.NotContains(t, blob, "maas", "a file the reader cannot see is named in their digest")
	m := metaOf(t, rows[0])
	require.Equal(t, 1, m.Count, "a file the reader cannot see is counted in their digest")
	require.Equal(t, "Ortak", m.Groups[0].Path)
}

// A server that stops with a window open loses nothing, and two servers (or a
// restart racing a request) never tell one window twice.
func TestDigest_ARestartLosesNothingAndTellsNothingTwice(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	clock := &digestClock{}
	ctx := context.Background()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "tr")

	first, firstDg := digestService(t, store, clock, notify.Config{})
	holds(t, first, firstDg, uid, string(notify.EventFileUploaded))
	for i := 0; i < 5; i++ {
		upload(t, first, uid, st.ID, "Rapor/r"+string(rune('0'+i))+".pdf")
	}
	first.Stop() // the window is still open

	clock.advance(61 * time.Second)
	b, bd := digestService(t, store, clock, notify.Config{})
	defer b.Stop()
	c, cd := digestService(t, store, clock, notify.Config{})
	defer c.Stop()

	var made atomic.Int32
	var wg sync.WaitGroup
	for _, d := range []notify.Digests{bd, cd} {
		wg.Add(1)
		go func(d notify.Digests) {
			defer wg.Done()
			n, err := d.FlushDueDigests(ctx)
			assert.NoError(t, err)
			made.Add(int32(n))
		}(d)
	}
	wg.Wait()
	require.EqualValues(t, 1, made.Load(), "the window was told %d times", made.Load())

	again, err := bd.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Zero(t, again)
	_, err = cd.SettleDigest(ctx, uid, notify.View{Bell: notify.MemberBell}, true)
	require.NoError(t, err)

	digests := digestsOf(everyRow(t, b, uid))
	require.Len(t, digests, 1)
	require.Equal(t, 5, metaOf(t, digests[0]).Count, "a row held before the restart was lost")
	require.Len(t, unread(t, c, uid), 1)
}

// The webhooks receive every event on its own, as before; the digest goes only
// to a target that names `notification.digest` — never to the legacy webhook
// or a target that takes everything, which already had each row.
func TestDigest_WebhooksGetEveryEventAndTheDigestOnlyWhereAsked(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	clock := &digestClock{}
	ctx := context.Background()

	var mu sync.Mutex
	seen := map[string][]string{}
	var digestBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen[r.URL.Path] = append(seen[r.URL.Path], r.Header.Get("X-Filex-Event"))
		if r.Header.Get("X-Filex-Event") == string(notify.EventNotificationDigest) {
			digestBody = body
		}
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	_, err := store.CreateWebhookTarget(ctx, &model.WebhookTarget{Name: "everything", URL: srv.URL + "/all", Enabled: true})
	require.NoError(t, err)
	_, err = store.CreateWebhookTarget(ctx, &model.WebhookTarget{Name: "digests", URL: srv.URL + "/digest", Events: "notification.digest", Enabled: true})
	require.NoError(t, err)

	svc, dg := digestService(t, store, clock, notify.Config{WebhookURL: srv.URL + "/legacy"})
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "tr")
	holds(t, svc, dg, uid, string(notify.EventFileUploaded))
	for i := 0; i < 3; i++ {
		upload(t, svc, uid, st.ID, "Rapor/w"+string(rune('0'+i))+".pdf")
	}
	svc.Wait()
	clock.advance(61 * time.Second)
	made, err := dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, made)
	svc.Wait()

	mu.Lock()
	defer mu.Unlock()
	uploaded := string(notify.EventFileUploaded)
	digest := string(notify.EventNotificationDigest)
	require.Equal(t, []string{uploaded, uploaded, uploaded}, seen["/legacy"], "the legacy webhook lost an event or got the digest")
	require.Equal(t, []string{uploaded, uploaded, uploaded}, seen["/all"], "a target that takes everything lost an event or got the digest twice over")
	require.Equal(t, []string{digest}, seen["/digest"], "the target that asked for digests did not get exactly one")

	var payload struct {
		Event string `json:"event"`
		Meta  struct {
			Count     int `json:"count"`
			Recipient struct {
				ID int64 `json:"id"`
			} `json:"recipient"`
			Groups []struct {
				Path   string         `json:"path"`
				Counts map[string]int `json:"counts"`
			} `json:"groups"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(digestBody, &payload))
	require.Equal(t, digest, payload.Event)
	require.Equal(t, 3, payload.Meta.Count)
	require.Equal(t, uid, payload.Meta.Recipient.ID)
	require.Equal(t, "Rapor", payload.Meta.Groups[0].Path)
	require.Equal(t, 3, payload.Meta.Groups[0].Counts[uploaded])
}

// An email an event asked for (a file request's owner) waits with the row and
// comes once, as the digest's, in the owner's language, when the owner holds
// file-request notices; an urgent one goes at once, as before.
func TestDigest_TheEmailWaitsForTheWindowAndComesOnce(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	clock := &digestClock{}
	box := &mailbox{}
	svc, dg := digestService(t, store, clock, notify.Config{Mail: box.send})
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	owner := digestUser(t, store, "sahip@example.test", "tr")
	ctx := context.Background()
	holds(t, svc, dg, owner, string(notify.EventDropReceived))

	drop := func(n int) {
		_, err := svc.Send(ctx, notify.Event{
			Event: notify.EventDropReceived, Severity: notify.SeverityInfo,
			Title: "Yeni dosya yüklemesi", Body: "Birisi, \"Gelen\" klasörüne dosya bıraktı.",
			Meta:   map[string]any{"folder": "Gelen", "count": n},
			Node:   &notify.NodeRef{StorageID: st.ID, Path: "Gelen", Name: "Gelen"},
			Target: notify.DirTarget("Gelen"),
			UserID: &owner,
			Mail:   &notify.Mail{Lang: "tr", Link: "https://dosya.example.test/admin/"},
		})
		require.NoError(t, err)
	}
	drop(2)
	drop(1)
	drop(4)
	svc.Wait()
	require.Empty(t, box.all(), "a held notice sent its email at once")

	clock.advance(61 * time.Second)
	made, err := dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, made)
	svc.Wait()
	mails := box.all()
	require.Len(t, mails, 1, "the window's email did not come exactly once")
	require.Equal(t, "sahip@example.test", mails[0].to)
	require.Equal(t, "tr", mails[0].lang)
	// The bell's words (say.go): the digest row's title is the subject, its
	// lines the text.
	require.Equal(t, "3 bildirim", mails[0].subject)
	require.Contains(t, mails[0].body, "Gelen: dosya isteğine 3 yükleme")
	require.Contains(t, mails[0].body, "https://dosya.example.test/admin/")

	// Urgent: at once, in the words the bell says for the row - not the
	// emitter's ("Yeni dosya yüklemesi").
	require.NoError(t, svc.UpsertSettings(ctx, &model.NotificationSettings{
		UserID: owner, InAppEnabled: true, MutedEventsRaw: []byte(`[]`),
		UrgentOverridesRaw: []byte(`{"drop.received":true}`),
	}))
	dg.ForgetDigest(owner)
	drop(1)
	svc.Wait()
	mails = box.all()
	require.Len(t, mails, 2)
	require.Equal(t, "1 dosya geldi", mails[1].subject)
	require.True(t, strings.HasPrefix(mails[1].body, "Birisi → Gelen"), "%q", mails[1].body)
	require.Contains(t, mails[1].body, "https://dosya.example.test/admin/")
}

// A client that knows nothing of the digest (an older desktop app) saves the
// two fields it knows and must not wipe the person's urgent choices.
func TestDigest_ASaveWithoutUrgentChoicesKeepsThem(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	uid := digestUser(t, store, "ayse@example.test", "tr")
	require.NoError(t, store.UpsertNotificationSettings(ctx, &model.NotificationSettings{
		UserID: uid, InAppEnabled: true, MutedEventsRaw: []byte(`[]`),
		UrgentOverridesRaw: []byte(`{"comment.added":true}`),
	}))
	require.NoError(t, store.UpsertNotificationSettings(ctx, &model.NotificationSettings{
		UserID: uid, InAppEnabled: false, MutedEventsRaw: []byte(`["share.created"]`),
	}))
	st, err := store.GetNotificationSettings(ctx, uid)
	require.NoError(t, err)
	require.False(t, st.InAppEnabled)
	require.Equal(t, []string{"share.created"}, st.MutedList())
	require.Equal(t, map[string]bool{"comment.added": true}, st.UrgentOverrides())
}
