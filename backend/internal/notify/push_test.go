package notify_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/webpush"
)

// Web Push (task #191, push.go): what a person's bell tells them reaches their
// phone while filex is closed - and ONLY that. A push is one more reader of the
// bell, so the bell's rules are the push's: told at once is pushed at once, a
// kind held for the digest is pushed as its digest, a muted kind or a bell
// switched off is pushed nothing, another person's row never.
//
// ⚠ Red on the code before it: notify.PushConfig, notify.Pushes and
// internal/webpush do not exist, and none of this compiles.

// pushed is one push as the device's worker reads it (web/public/notify-sw.js).
type pushed struct {
	V        int    `json:"v"`
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Tag      string `json:"tag"`
	Open     bool   `json:"open"`
	Renotify bool   `json:"renotify"`
}

// handset is one browser: the keys it subscribes with.
type handset struct {
	priv *ecdh.PrivateKey
	auth []byte
}

// pushService stands in for the browsers' push services: it decrypts each
// push with the device's own key, as the browser would.
type pushService struct {
	*httptest.Server
	mu      sync.Mutex
	devices map[string]*handset
	status  map[string]int
	got     map[string][]pushed
	authz   []string
}

func newPushService(t *testing.T) *pushService {
	t.Helper()
	ps := &pushService{devices: map[string]*handset{}, status: map[string]int{}, got: map[string][]pushed{}}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/push/")
		body, _ := io.ReadAll(r.Body)
		ps.mu.Lock()
		defer ps.mu.Unlock()
		ps.authz = append(ps.authz, r.Header.Get("Authorization"))
		if st := ps.status[name]; st != 0 {
			w.WriteHeader(st)
			return
		}
		d := ps.devices[name]
		if d == nil {
			w.WriteHeader(http.StatusGone)
			return
		}
		plain, err := webpush.Decrypt(body, d.priv, d.auth)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var p pushed
		if json.Unmarshal(plain, &p) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ps.got[name] = append(ps.got[name], p)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(ps.Close)
	return ps
}

// handset makes a browser called name and answers what it hands filex.
func (ps *pushService) handset(t *testing.T, name string) notify.PushSubscriptionInput {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	ps.mu.Lock()
	ps.devices[name] = &handset{priv: priv, auth: auth}
	ps.mu.Unlock()
	return notify.PushSubscriptionInput{
		Endpoint: ps.URL + "/push/" + name,
		P256dh:   base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
		Label:    "Chrome on Android",
	}
}

func (ps *pushService) received(name string) []pushed {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return append([]pushed(nil), ps.got[name]...)
}

func (ps *pushService) answer(name string, status int) {
	ps.mu.Lock()
	ps.status[name] = status
	ps.mu.Unlock()
}

func pushBox(t *testing.T, key string) *secretbox.Box {
	t.Helper()
	box, err := secretbox.New(key)
	require.NoError(t, err)
	return box
}

// pushConfig is push switched on against ps, the push service on this machine.
func pushConfig(t *testing.T, ps *pushService, secret string) *notify.PushConfig {
	return &notify.PushConfig{
		Box:       pushBox(t, secret),
		Subject:   "mailto:ops@example.com",
		Endpoints: webpush.Policy{Insecure: true},
		Client:    ps.Client(),
		Debounce:  5 * time.Millisecond,
	}
}

func pushSetup(t *testing.T, store db.Store, clock *digestClock, ps *pushService) (notify.Service, notify.Digests, notify.Pushes) {
	t.Helper()
	svc, dg := digestService(t, store, clock, notify.Config{Push: pushConfig(t, ps, "push-test-secret")})
	p, ok := svc.(notify.Pushes)
	require.True(t, ok, "the service has no push half")
	return svc, dg, p
}

func flush(t *testing.T, p notify.Pushes) int {
	t.Helper()
	n, err := p.FlushPush(context.Background())
	require.NoError(t, err)
	return n
}

func TestPush_WhatTheBellTellsAtOnceIsPushedAtOnce(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ps := newPushService(t)
	svc, _, p := pushSetup(t, store, &digestClock{}, ps)
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "tr")
	ctx := context.Background()

	upload(t, svc, uid, st.ID, "Rapor/eski.pdf")
	dev, err := p.PushSubscribe(ctx, uid, ps.handset(t, "phone"))
	require.NoError(t, err)
	require.Equal(t, "Chrome on Android", dev.Label)
	require.Zero(t, flush(t, p))
	require.Empty(t, ps.received("phone"), "a row from before the device subscribed was pushed")

	id := upload(t, svc, uid, st.ID, "Rapor/rapor.pdf")
	require.Equal(t, 1, flush(t, p))
	got := ps.received("phone")
	require.Len(t, got, 1)
	require.Equal(t, 1, got[0].V)
	require.Equal(t, id, got[0].ID)
	require.Equal(t, fmt.Sprintf("filex-notification-%d", id), got[0].Tag, "the tag of the page's own toast of the row")
	require.Equal(t, "filex", got[0].Title, "the instance's name says whose push it is")
	// ⚠ The bell's own words (say.go), laid out as the page's pop-up lays
	// them out - never a sentence of the push's own (it said "rapor.pdf -
	// Rapor: 1 dosya eklendi" while the bell said "Yeni dosya: rapor.pdf").
	require.Equal(t, "Yeni dosya: rapor.pdf - Rapor/rapor.pdf", got[0].Body, "said in the reader's language")
	require.True(t, got[0].Open, "a file has somewhere to go")
	require.False(t, got[0].Renotify)

	// Once. A pass with nothing new pushes nothing, and a row read before
	// the pass reaches it is not news.
	require.Zero(t, flush(t, p))
	seen := upload(t, svc, uid, st.ID, "Rapor/okundu.pdf")
	require.NoError(t, svc.MarkRead(ctx, seen, &uid))
	require.Zero(t, flush(t, p))
	require.Len(t, ps.received("phone"), 1)

	// The instance's own name when the Branding page set one.
	require.NoError(t, store.UpsertSetting(ctx, "branding.name", "Acme Dosya"))
	upload(t, svc, uid, st.ID, "Rapor/yeni.pdf")
	require.Equal(t, 1, flush(t, p))
	got = ps.received("phone")
	require.Equal(t, "Acme Dosya", got[len(got)-1].Title)
}

func TestPush_AKindHeldForTheDigestIsPushedAsItsDigest(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ps := newPushService(t)
	clock := &digestClock{}
	svc, dg, p := pushSetup(t, store, clock, ps)
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "tr")
	ctx := context.Background()
	holds(t, svc, dg, uid, string(notify.EventFileUploaded))
	_, err := p.PushSubscribe(ctx, uid, ps.handset(t, "phone"))
	require.NoError(t, err)

	for _, name := range []string{"a.pdf", "b.pdf", "c.pdf"} {
		upload(t, svc, uid, st.ID, "Rapor/"+name)
	}
	require.Zero(t, flush(t, p), "a held row was pushed on its own")

	clock.advance(61 * time.Second)
	made, err := dg.FlushDueDigests(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, made)
	require.Equal(t, 1, flush(t, p), "the digest is the notification, on the phone as in the bell")
	got := ps.received("phone")
	require.Len(t, got, 1)
	require.Equal(t, "3 bildirim - Rapor: 3 dosya eklendi", got[0].Body, "the digest row's own words")
	require.True(t, got[0].Open, "a digest of one folder opens the folder")
}

func TestPush_AMutedKindOrABellSwitchedOffIsPushedNothing(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ps := newPushService(t)
	svc, _, p := pushSetup(t, store, &digestClock{}, ps)
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "en")
	ctx := context.Background()
	_, err := p.PushSubscribe(ctx, uid, ps.handset(t, "phone"))
	require.NoError(t, err)

	require.NoError(t, svc.UpsertSettings(ctx, &model.NotificationSettings{
		UserID: uid, InAppEnabled: true, MutedEventsRaw: []byte(`["file.uploaded"]`),
	}))
	upload(t, svc, uid, st.ID, "Rapor/a.pdf")
	require.Zero(t, flush(t, p), "a muted kind was pushed")

	require.NoError(t, svc.UpsertSettings(ctx, &model.NotificationSettings{
		UserID: uid, InAppEnabled: false, MutedEventsRaw: []byte(`[]`),
	}))
	upload(t, svc, uid, st.ID, "Rapor/b.pdf")
	require.Zero(t, flush(t, p), "a person whose bell is off was pushed")
	require.Empty(t, ps.received("phone"))
}

func TestPush_ABurstIsOnePush(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ps := newPushService(t)
	svc, _, p := pushSetup(t, store, &digestClock{}, ps)
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "en")
	_, err := p.PushSubscribe(context.Background(), uid, ps.handset(t, "phone"))
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		upload(t, svc, uid, st.ID, fmt.Sprintf("Rapor/%d.pdf", i))
	}
	require.Equal(t, 1, flush(t, p))
	got := ps.received("phone")
	require.Len(t, got, 1, "five rows in one pass were five pushes")
	require.Equal(t, "5 new notifications", got[0].Body)
	require.Equal(t, "filex-push-summary", got[0].Tag)
	require.True(t, got[0].Renotify, "a new summary alerts again")
	require.Zero(t, got[0].ID)
	require.False(t, got[0].Open)
}

// TestPush_ABroadcastReachesTheDevicesOfThoseWhoseBellTakesIt: an antivirus
// alert written to nobody in particular is pushed to the administrator, whose
// bell shows it, and not to the member whose bell's per-row pass keeps it out.
func TestPush_ABroadcastReachesTheDevicesOfThoseWhoseBellTakesIt(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ps := newPushService(t)
	svc, dg, p := pushSetup(t, store, &digestClock{}, ps)
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	admin := digestUser(t, store, "admin@example.test", "en")
	member := digestUser(t, store, "uye@example.test", "tr")
	other := digestUser(t, store, "baska@example.test", "tr")
	ctx := context.Background()
	dg.SetViewer(func(_ context.Context, uid int64) (notify.View, error) {
		if uid == admin {
			return notify.View{Bell: notify.AdminBell}, nil
		}
		return notify.View{Bell: notify.MemberBell, Keep: func(n *model.Notification) bool { return n.UserID != nil }}, nil
	})
	_, err := p.PushSubscribe(ctx, admin, ps.handset(t, "admin"))
	require.NoError(t, err)
	_, err = p.PushSubscribe(ctx, member, ps.handset(t, "member"))
	require.NoError(t, err)

	_, err = svc.Send(ctx, notify.Event{
		Event: notify.EventFileInfected, Severity: notify.SeverityCritical,
		Node: &notify.NodeRef{StorageID: st.ID, Path: "Gizli/virus.exe", Name: "virus.exe"},
	})
	require.NoError(t, err)
	// Another person's own row is theirs alone.
	upload(t, svc, other, st.ID, "Baska/not-mine.pdf")

	require.Equal(t, 1, flush(t, p))
	require.Len(t, ps.received("admin"), 1)
	require.Contains(t, ps.received("admin")[0].Body, "virus.exe")
	require.Empty(t, ps.received("member"), "a broadcast the member's bell keeps out was pushed to them")
}

func TestPush_ADeviceThatIsGoneIsForgotten(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ps := newPushService(t)
	svc, _, p := pushSetup(t, store, &digestClock{}, ps)
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "en")
	ctx := context.Background()
	_, err := p.PushSubscribe(ctx, uid, ps.handset(t, "phone"))
	require.NoError(t, err)
	_, err = p.PushSubscribe(ctx, uid, ps.handset(t, "laptop"))
	require.NoError(t, err)

	// One refusal is counted, not fatal; the browser that unsubscribed (410)
	// is forgotten at once.
	ps.answer("laptop", http.StatusForbidden)
	ps.answer("phone", http.StatusGone)
	upload(t, svc, uid, st.ID, "Rapor/a.pdf")
	require.Zero(t, flush(t, p))
	devs, err := p.PushDevices(ctx, uid)
	require.NoError(t, err)
	require.Len(t, devs, 1)
	require.Equal(t, "laptop", devs[0].Endpoint[strings.LastIndex(devs[0].Endpoint, "/")+1:])

	// Past a few refusals in a row, the laptop goes too.
	for i := 0; i < 5; i++ {
		upload(t, svc, uid, st.ID, fmt.Sprintf("Rapor/r%d.pdf", i))
		flush(t, p)
	}
	devs, err = p.PushDevices(ctx, uid)
	require.NoError(t, err)
	require.Empty(t, devs)
}

// TestPush_ASecondServerNeverPushesARowTwice: a server that starts pushes what
// was written while none ran, and the one that wrote it - woken but never
// flushed - finds the device's mark already past it.
func TestPush_ASecondServerNeverPushesARowTwice(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ps := newPushService(t)
	svcA, _, pA := pushSetup(t, store, &digestClock{}, ps)
	defer svcA.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "en")
	ctx := context.Background()
	_, err := pA.PushSubscribe(ctx, uid, ps.handset(t, "phone"))
	require.NoError(t, err)
	upload(t, svcA, uid, st.ID, "Rapor/a.pdf")

	svcB, _, pB := pushSetup(t, store, &digestClock{}, ps)
	pB.StartPush(ctx)
	require.Eventually(t, func() bool { return len(ps.received("phone")) == 1 }, 5*time.Second, 10*time.Millisecond,
		"the starting server did not push what was waiting")
	svcB.Stop()

	require.Zero(t, flush(t, pA))
	require.Len(t, ps.received("phone"), 1, "a row was pushed twice")
}

// TestPush_TheKeyIsMadeOnceSealedAndARotationForgetsEveryDevice: the private
// key is never in the clear, the same key on every server that has the same
// FILEX_SECRET_KEY, push off (never re-keyed behind the operator's back) where
// the secret is another or missing, and every push names the key its device
// subscribed with.
func TestPush_TheKeyIsMadeOnceSealedAndARotationForgetsEveryDevice(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ps := newPushService(t)
	svc, _, p := pushSetup(t, store, &digestClock{}, ps)
	defer svc.Stop()
	st := newStorage(t, store, "ekip")
	uid := digestUser(t, store, "ayse@example.test", "en")
	ctx := context.Background()

	info := p.PushInfo(ctx)
	require.True(t, info.Available, "%+v", info)
	raw, err := base64.RawURLEncoding.DecodeString(info.PublicKey)
	require.NoError(t, err)
	require.Len(t, raw, 65)
	row, err := store.GetPushVAPIDKey(ctx)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, info.PublicKey, row.PublicKey)
	require.True(t, secretbox.IsSealed(row.PrivateSealed), "the private key is stored in the clear")
	require.Equal(t, info.PublicKey, p.PushInfo(ctx).PublicKey, "a second read made another key")

	same := notify.New(store, notify.Config{Push: pushConfig(t, ps, "push-test-secret")}).(notify.Pushes)
	require.Equal(t, info.PublicKey, same.PushInfo(ctx).PublicKey, "another server with the same secret has another key")
	other := notify.New(store, notify.Config{Push: pushConfig(t, ps, "another-secret")}).(notify.Pushes)
	require.Equal(t, notify.PushInfo{Reason: notify.PushOffKeyUnreadable}, other.PushInfo(ctx))
	none := notify.New(store, notify.Config{Push: pushConfig(t, ps, "")}).(notify.Pushes)
	require.Equal(t, notify.PushInfo{Reason: notify.PushOffNoSecretKey}, none.PushInfo(ctx))
	_, err = none.PushSubscribe(ctx, uid, ps.handset(t, "x"))
	var off *notify.PushOff
	require.True(t, errors.As(err, &off), "%v", err)
	off2 := notify.New(store, notify.Config{}).(notify.Pushes)
	require.Equal(t, notify.PushInfo{Reason: notify.PushOffDisabled}, off2.PushInfo(ctx))
	again, err := store.GetPushVAPIDKey(ctx)
	require.NoError(t, err)
	require.Equal(t, row.PrivateSealed, again.PrivateSealed, "a server that could not open the key replaced it")

	_, err = p.PushSubscribe(ctx, uid, ps.handset(t, "phone"))
	require.NoError(t, err)
	upload(t, svc, uid, st.ID, "Rapor/a.pdf")
	require.Equal(t, 1, flush(t, p))
	ps.mu.Lock()
	authz := ps.authz[len(ps.authz)-1]
	ps.mu.Unlock()
	require.True(t, strings.HasSuffix(authz, ", k="+info.PublicKey), "the push is not signed with the key the device subscribed with: %s", authz)

	rotated, dropped, err := p.RotatePushKey(ctx)
	require.NoError(t, err)
	require.True(t, rotated.Available)
	require.NotEqual(t, info.PublicKey, rotated.PublicKey)
	require.EqualValues(t, 1, dropped)
	devs, err := p.PushDevices(ctx, uid)
	require.NoError(t, err)
	require.Empty(t, devs, "a device subscribed with the old key outlived the rotation")
	require.Equal(t, rotated.PublicKey, same.PushInfo(ctx).PublicKey, "the other server reads the new key")
}

// TestPush_OnlyAPushServiceOfTheBrowsersIsAccepted: an endpoint is an address
// a person sends; anything but a browser's push service is refused before it
// is stored, so no account can make the server POST into its own network.
func TestPush_OnlyAPushServiceOfTheBrowsersIsAccepted(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ps := newPushService(t)
	cfg := pushConfig(t, ps, "push-test-secret")
	cfg.Endpoints = webpush.Policy{}
	svc := notify.New(store, notify.Config{Push: cfg})
	defer svc.Stop()
	p := svc.(notify.Pushes)
	uid := digestUser(t, store, "ayse@example.test", "en")
	ctx := context.Background()
	good := ps.handset(t, "phone")

	for _, endpoint := range []string{
		ps.URL + "/push/phone", // this machine, plain http
		"https://169.254.169.254/latest/meta-data",
		"https://intranet.example/hook",
		"https://user:pw@fcm.googleapis.com/fcm/send/x",
	} {
		in := good
		in.Endpoint = endpoint
		_, err := p.PushSubscribe(ctx, uid, in)
		var ref *notify.PushRefusal
		require.True(t, errors.As(err, &ref), "%s: %v", endpoint, err)
		require.Equal(t, "push_endpoint_refused", ref.Code)
	}
	bad := good
	bad.Endpoint = "https://fcm.googleapis.com/fcm/send/abc"
	bad.P256dh = "bm90IGEga2V5"
	_, err := p.PushSubscribe(ctx, uid, bad)
	var ref *notify.PushRefusal
	require.True(t, errors.As(err, &ref), "%v", err)
	require.Equal(t, "push_keys_invalid", ref.Code)

	ok := good
	ok.Endpoint = "https://fcm.googleapis.com/fcm/send/abc"
	ok.Label = "  Chrome\non\tAndroid  " + strings.Repeat("x", 200)
	dev, err := p.PushSubscribe(ctx, uid, ok)
	require.NoError(t, err)
	require.Equal(t, "fcm.googleapis.com", dev.Service)
	require.True(t, strings.HasPrefix(dev.Label, "Chrome on Android x"), dev.Label)
	require.Equal(t, 80, len([]rune(dev.Label)))
	require.Equal(t, webpush.EndpointHash(ok.Endpoint), dev.EndpointHash)
	devs, err := p.PushDevices(ctx, uid)
	require.NoError(t, err)
	require.Len(t, devs, 1)
}
