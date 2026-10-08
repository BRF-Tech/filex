package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/webpush"
)

// Web Push (task #191): what a person's bell tells them reaches their phone
// and their browsers while filex is closed.
//
// ⚠⚠ ONE decision. A push is not a channel with rules of its own: it is one
// more READER of the person's bell, beside the bell itself, the browser's
// pop-up and the desktop app's toast. The pass reads the same unread list
// those read (ListVisible through the person's View - the HTTP layer's bell,
// built without a request: Viewer), so a muted kind, the bell switched off
// (in_app_enabled), a kind held for the digest (quiet until its digest row is
// written - the digest row is what is pushed), an operator alarm a member
// never sees, another tenant's row and a file outside the person's grants are
// all decided where they always were, without a line of push-specific code.
// Urgent or digest, bell or e-mail or push: the same answer.
//
// When. Send wakes the pass for the row's addressee (a broadcast: for everyone
// with a device), a digest written wakes it for its person, and the pass
// pushes each device the unread rows above the device's mark, oldest first.
// The mark moves by compare-and-set before anything is sent, so two servers
// (or two passes) never push one row twice; a row that is read before the pass
// runs is not news and is not pushed.
//
// ⚠ The key. The VAPID key (RFC 8292) is made at the first start, its private
// half sealed with FILEX_SECRET_KEY and bound to its row; without that key
// push stays off - a private key is never stored in the clear. A rotation
// replaces it and forgets every device (a subscription is bound to the key it
// was made with); each device subscribes again the next time filex opens on it.
//
// ⚠ What a push says. EXACTLY what the bell says (say.go Say - the one code
// path every channel's words come from), in the person's language, laid out
// as the page's own pop-up lays it out: the instance's name as the title,
// "<sentence> - <detail>" as the body (ToastBody). The service worker only
// shows it. It carries a name, a count and where - never file content, never
// a credential, never an encrypted item's name (the lock word stands there);
// and the payload is encrypted for the one browser (RFC 8291), so the push
// service carries ciphertext.

// Why push is off, as PushInfo and the settings pane say it.
const (
	// PushOffDisabled: switched off (FILEX_PUSH_ENABLED=false), or the
	// service runs without it.
	PushOffDisabled = "disabled"
	// PushOffNoSecretKey: FILEX_SECRET_KEY is not set, so the private key
	// could not be stored sealed.
	PushOffNoSecretKey = "no_secret_key"
	// PushOffKeyUnreadable: the stored key does not open (FILEX_SECRET_KEY
	// changed); an administrator rotates it.
	PushOffKeyUnreadable = "key_unreadable"
	// PushOffError: the key could not be read or made (the database).
	PushOffError = "error"
)

const (
	// pushHead is how many of a person's unread rows one pass reads.
	pushHead = 20
	// pushPerPass: more new rows than this for one device in one pass are
	// one push ("12 new notifications"), not twelve.
	pushPerPass = 3
	// pushMaxAge: a row older than this when the pass reaches it (a server
	// that was down) is passed over - late news is noise on a phone.
	pushMaxAge = time.Hour
	// pushTTL is how long a push service keeps a push for a device that is
	// offline.
	pushTTL = 12 * time.Hour
	// pushDebounce is how long the pass waits after it is woken, so a burst
	// of rows is one pass.
	pushDebounce = 750 * time.Millisecond
	// pushMaxDevices is how many devices one person keeps; the oldest goes.
	pushMaxDevices = 20
	// pushDropAfter is how many refusals in a row forget a device.
	pushDropAfter = 5
	// pushLabelMax is the longest device label kept, in characters.
	pushLabelMax = 80
	// pushBodyMax and pushTitleMax bound a push's text, in bytes, so the
	// payload always fits one push (webpush.MaxPayload).
	pushBodyMax  = 1200
	pushTitleMax = 120
	// pushSummaryTag replaces the previous summary on the device.
	pushSummaryTag = "filex-push-summary"
	// pushTestTag is the test push's.
	pushTestTag = "filex-push-test"
	// pushKeyAAD binds the sealed private key to its row.
	pushKeyAAD = "push_vapid_keys:1"
)

// PushConfig switches Web Push on (Config.Push). Nil: no push, and the
// settings pane says so.
type PushConfig struct {
	// Box seals the VAPID private key (FILEX_SECRET_KEY). Without a key in it
	// push stays off.
	Box *secretbox.Box
	// Subject is the VAPID contact (FILEX_PUSH_SUBJECT): mailto: or https:.
	Subject string
	// Endpoints is which push services a device may name (FILEX_PUSH_HOSTS).
	Endpoints webpush.Policy
	// Client sends the pushes. Nil: webpush.SafeClient.
	Client *http.Client
	// Debounce overrides pushDebounce (tests).
	Debounce time.Duration
}

// PushInfo is whether push works on this server, and the key a browser
// subscribes with.
type PushInfo struct {
	Available    bool       `json:"available"`
	Reason       string     `json:"reason,omitempty"`
	PublicKey    string     `json:"public_key,omitempty"`
	KeyCreatedAt *time.Time `json:"key_created_at,omitempty"`
}

// PushSubscriptionInput is what a browser hands over (PushSubscription.toJSON)
// and what it calls itself.
type PushSubscriptionInput struct {
	Endpoint string
	P256dh   string
	Auth     string
	Label    string
}

// PushTestResult is how many of a person's devices took the test push.
type PushTestResult struct {
	Sent   int `json:"sent"`
	Failed int `json:"failed"`
}

// PushOff answers a push request on a server where push does not work.
type PushOff struct{ Reason string }

func (e *PushOff) Error() string { return "notify: push notifications are off (" + e.Reason + ")" }

// PushRefusal is a device push cannot be sent to: Code says why
// (push_endpoint_refused, push_keys_invalid).
type PushRefusal struct {
	Code string
	Err  error
}

func (e *PushRefusal) Error() string { return e.Err.Error() }
func (e *PushRefusal) Unwrap() error { return e.Err }

// Pushes is the Web Push half of the service. ⚠ Its own interface, not
// methods of Service: a test double of Service does not need it, and the
// queue tests' double writes every Service method out by hand.
type Pushes interface {
	// PushInfo says whether push works here, and with which public key.
	PushInfo(ctx context.Context) PushInfo
	// PushDevices is a person's devices (never their endpoints or keys).
	PushDevices(ctx context.Context, userID int64) ([]*model.PushSubscription, error)
	// PushSubscribe records a device for a person: from now on what their
	// bell tells them is pushed to it. Rows from before are not.
	PushSubscribe(ctx context.Context, userID int64, in PushSubscriptionInput) (*model.PushSubscription, error)
	// PushForget removes one of the person's devices by id; PushForgetEndpoint
	// by its endpoint (the browser turning push off, or signing out). Both
	// answer false for a device that is not theirs.
	PushForget(ctx context.Context, userID, id int64) (bool, error)
	PushForgetEndpoint(ctx context.Context, userID int64, endpoint string) (bool, error)
	// PushTest pushes a test to every device of the person, now.
	PushTest(ctx context.Context, userID int64) (PushTestResult, error)
	// PushCount is how many devices there are, everybody's.
	PushCount(ctx context.Context) (int64, error)
	// RotatePushKey replaces the VAPID key and forgets every device; it
	// answers the new key and how many devices were forgotten.
	RotatePushKey(ctx context.Context) (PushInfo, int64, error)
	// StartPush runs the background pass until ctx ends or Stop.
	StartPush(ctx context.Context)
	// FlushPush runs one pass now over the people woken since the last one
	// and answers how many pushes the push services took.
	FlushPush(ctx context.Context) (int, error)
}

var _ Pushes = (*service)(nil)

// pushRuntime is push's state in memory. Everything that must survive a
// restart (the key, the devices, their marks) is in the database.
type pushRuntime struct {
	cfg    PushConfig
	sender *webpush.Sender

	mu      sync.Mutex
	pending map[int64]bool
	all     bool
	wake    chan struct{}

	// The opened key, kept while the stored (sealed) text is the same.
	keyMu     sync.Mutex
	key       *webpush.Key
	keySealed string
}

func newPushRuntime(cfg PushConfig) *pushRuntime {
	return &pushRuntime{
		cfg:     cfg,
		sender:  &webpush.Sender{Client: cfg.Client, Subject: cfg.Subject},
		pending: map[int64]bool{},
		wake:    make(chan struct{}, 1),
	}
}

// wakePush asks the pass to push what is new for userID - for everyone with a
// device when userID is nil (a broadcast: who reads it is decided per reader).
func (s *service) wakePush(userID *int64) {
	p := s.push
	if p == nil {
		return
	}
	p.mu.Lock()
	if userID == nil {
		p.all = true
	} else {
		p.pending[*userID] = true
	}
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// ── the key ─────────────────────────────────────────────────────────────

// pushKey is the instance's VAPID key, made when there is none. A *PushOff
// says why there is no key to push with.
func (s *service) pushKey(ctx context.Context) (*webpush.Key, *model.PushVAPIDKey, error) {
	p := s.push
	if p == nil || s.store == nil {
		return nil, nil, &PushOff{Reason: PushOffDisabled}
	}
	if !p.cfg.Box.Enabled() {
		return nil, nil, &PushOff{Reason: PushOffNoSecretKey}
	}
	row, err := s.store.GetPushVAPIDKey(ctx)
	if err != nil {
		return nil, nil, err
	}
	if row == nil {
		if row, err = s.makePushKey(ctx); err != nil {
			return nil, nil, err
		}
	}
	p.keyMu.Lock()
	defer p.keyMu.Unlock()
	if p.key != nil && p.keySealed == row.PrivateSealed {
		return p.key, row, nil
	}
	// ⚠ Sealed or nothing: a key written in the clear (by hand, or by a
	// build that did not seal it) is not used.
	if !secretbox.IsSealed(row.PrivateSealed) {
		return nil, nil, &PushOff{Reason: PushOffKeyUnreadable}
	}
	plain, err := p.cfg.Box.OpenFor(row.PrivateSealed, pushKeyAAD)
	if err != nil {
		return nil, nil, &PushOff{Reason: PushOffKeyUnreadable}
	}
	k, err := webpush.ParseKey(plain)
	if err != nil || k.Public() != row.PublicKey {
		return nil, nil, &PushOff{Reason: PushOffKeyUnreadable}
	}
	p.key, p.keySealed = k, row.PrivateSealed
	return k, row, nil
}

// newPushKey is a fresh key, its private half sealed.
func (s *service) newPushKey() (*model.PushVAPIDKey, error) {
	k, err := webpush.GenerateKey()
	if err != nil {
		return nil, err
	}
	text, err := k.MarshalPrivate()
	if err != nil {
		return nil, err
	}
	sealed, err := s.push.cfg.Box.SealFor(text, pushKeyAAD)
	if err != nil {
		return nil, err
	}
	return &model.PushVAPIDKey{PublicKey: k.Public(), PrivateSealed: sealed}, nil
}

// makePushKey stores the first key. Two servers starting at once both try;
// the one stored first is the instance's.
func (s *service) makePushKey(ctx context.Context) (*model.PushVAPIDKey, error) {
	row, err := s.newPushKey()
	if err != nil {
		return nil, err
	}
	if err := s.store.CreatePushVAPIDKey(ctx, row); err != nil {
		if again, gerr := s.store.GetPushVAPIDKey(ctx); gerr == nil && again != nil {
			return again, nil
		}
		return nil, err
	}
	slog.Info("notify: a Web Push key was made for this instance")
	stored, err := s.store.GetPushVAPIDKey(ctx)
	if err != nil || stored == nil {
		return row, nil
	}
	return stored, nil
}

func (s *service) PushInfo(ctx context.Context) PushInfo {
	_, row, err := s.pushKey(ctx)
	if err != nil {
		var off *PushOff
		if errors.As(err, &off) {
			return PushInfo{Reason: off.Reason}
		}
		slog.Warn("notify: push key unreadable", slog.String("err", err.Error()))
		return PushInfo{Reason: PushOffError}
	}
	out := PushInfo{Available: true, PublicKey: row.PublicKey}
	if !row.CreatedAt.IsZero() {
		t := row.CreatedAt
		out.KeyCreatedAt = &t
	}
	return out
}

func (s *service) RotatePushKey(ctx context.Context) (PushInfo, int64, error) {
	if s.push == nil || s.store == nil {
		return PushInfo{Reason: PushOffDisabled}, 0, &PushOff{Reason: PushOffDisabled}
	}
	if !s.push.cfg.Box.Enabled() {
		return PushInfo{Reason: PushOffNoSecretKey}, 0, &PushOff{Reason: PushOffNoSecretKey}
	}
	row, err := s.newPushKey()
	if err != nil {
		return PushInfo{}, 0, err
	}
	var dropped int64
	if err := s.store.WithTx(ctx, func(ctx context.Context) error {
		var err error
		dropped, err = s.store.ReplacePushVAPIDKey(ctx, row)
		return err
	}); err != nil {
		return PushInfo{}, 0, err
	}
	s.push.keyMu.Lock()
	s.push.key, s.push.keySealed = nil, ""
	s.push.keyMu.Unlock()
	slog.Info("notify: the Web Push key was rotated", slog.Int64("devices_forgotten", dropped))
	return s.PushInfo(ctx), dropped, nil
}

// ── the devices ─────────────────────────────────────────────────────────

func (s *service) PushDevices(ctx context.Context, userID int64) ([]*model.PushSubscription, error) {
	subs, err := s.store.ListPushSubscriptions(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, d := range subs {
		d.Service = webpush.ServiceOf(d.Endpoint)
	}
	return subs, nil
}

func (s *service) PushSubscribe(ctx context.Context, userID int64, in PushSubscriptionInput) (*model.PushSubscription, error) {
	if _, _, err := s.pushKey(ctx); err != nil {
		return nil, err
	}
	if err := s.push.cfg.Endpoints.Check(in.Endpoint); err != nil {
		return nil, &PushRefusal{Code: "push_endpoint_refused", Err: err}
	}
	if err := webpush.CheckKeys(in.P256dh, in.Auth); err != nil {
		return nil, &PushRefusal{Code: "push_keys_invalid", Err: err}
	}
	// ⚠ The mark starts at the newest row there is: a device that subscribes
	// is told what happens from now on, never the person's history.
	newest, err := s.store.NewestNotificationID(ctx)
	if err != nil {
		return nil, err
	}
	saved, err := s.store.SavePushSubscription(ctx, &model.PushSubscription{
		UserID:       userID,
		Endpoint:     in.Endpoint,
		EndpointHash: webpush.EndpointHash(in.Endpoint),
		P256dh:       strings.TrimSpace(in.P256dh),
		Auth:         strings.TrimSpace(in.Auth),
		Label:        cleanLabel(in.Label),
		ThroughID:    newest,
	})
	if err != nil {
		return nil, err
	}
	s.trimPushDevices(ctx, userID)
	saved.Service = webpush.ServiceOf(saved.Endpoint)
	return saved, nil
}

// trimPushDevices keeps a person's newest pushMaxDevices devices.
func (s *service) trimPushDevices(ctx context.Context, userID int64) {
	subs, err := s.store.ListPushSubscriptions(ctx, userID)
	if err != nil {
		return
	}
	for len(subs) > pushMaxDevices {
		_ = s.store.DropPushSubscription(ctx, subs[0].ID)
		subs = subs[1:]
	}
}

// cleanLabel is a device's label as kept: one line, no control characters,
// at most pushLabelMax characters.
func cleanLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > pushLabelMax {
		s = string([]rune(s)[:pushLabelMax])
	}
	return s
}

func (s *service) PushForget(ctx context.Context, userID, id int64) (bool, error) {
	return s.store.DeletePushSubscription(ctx, userID, id)
}

func (s *service) PushForgetEndpoint(ctx context.Context, userID int64, endpoint string) (bool, error) {
	return s.store.DeletePushSubscriptionByHash(ctx, userID, webpush.EndpointHash(endpoint))
}

func (s *service) PushCount(ctx context.Context) (int64, error) {
	return s.store.CountPushSubscriptions(ctx)
}

func (s *service) PushTest(ctx context.Context, userID int64) (PushTestResult, error) {
	key, _, err := s.pushKey(ctx)
	if err != nil {
		return PushTestResult{}, err
	}
	subs, err := s.store.ListPushSubscriptions(ctx, userID)
	if err != nil {
		return PushTestResult{}, err
	}
	w := s.pushWords(ctx, userID)
	m := pushMessage{Title: w.brand, Body: srvtext.Text(w.lang, "server.notify.push.test", nil), Tag: pushTestTag, Renotify: true}
	var out PushTestResult
	for _, sub := range subs {
		if taken, _ := s.deliverPush(ctx, key, sub, m); taken {
			out.Sent++
		} else {
			out.Failed++
		}
	}
	return out, nil
}

// ── the pass ────────────────────────────────────────────────────────────

func (s *service) StartPush(ctx context.Context) {
	p := s.push
	if p == nil {
		return
	}
	s.inflightMu.Lock()
	if s.stopped || s.pushDone != nil {
		s.inflightMu.Unlock()
		return
	}
	done := make(chan struct{})
	s.pushDone = done
	s.inflightMu.Unlock()
	debounce := p.cfg.Debounce
	if debounce <= 0 {
		debounce = pushDebounce
	}
	go func() {
		defer close(done)
		// The key is made at the first start, so the settings pane has one to
		// offer before anybody asks for it.
		if info := s.PushInfo(ctx); !info.Available && info.Reason != PushOffDisabled {
			slog.Info("notify: push notifications are off", slog.String("reason", info.Reason))
		}
		// What was written while no server ran reaches the devices now (the
		// rows younger than pushMaxAge).
		s.wakePush(nil)
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-p.wake:
			}
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-time.After(debounce):
			}
			if _, err := s.FlushPush(ctx); err != nil {
				var off *PushOff
				if !errors.As(err, &off) {
					slog.Warn("notify: push pass", slog.String("err", err.Error()))
				}
			}
		}
	}()
}

func (s *service) FlushPush(ctx context.Context) (int, error) {
	p := s.push
	if p == nil {
		return 0, nil
	}
	p.mu.Lock()
	all := p.all
	users := make([]int64, 0, len(p.pending))
	for uid := range p.pending {
		users = append(users, uid)
	}
	p.all, p.pending = false, map[int64]bool{}
	p.mu.Unlock()
	if !all && len(users) == 0 {
		return 0, nil
	}
	key, _, err := s.pushKey(ctx)
	if err != nil {
		return 0, err
	}
	if all {
		if users, err = s.store.ListPushSubscribers(ctx); err != nil {
			return 0, err
		}
	}
	// Stop ends the pass, the requests in flight included.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-s.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	sent := 0
	for _, uid := range users {
		if ctx.Err() != nil {
			break
		}
		n, err := s.pushPerson(ctx, uid, key)
		if err != nil {
			slog.Warn("notify: push not sent", slog.Int64("user_id", uid), slog.String("err", err.Error()))
			continue
		}
		sent += n
	}
	return sent, nil
}

// pushPerson pushes uid's devices what is new in their bell.
func (s *service) pushPerson(ctx context.Context, uid int64, key *webpush.Key) (int, error) {
	subs, err := s.store.ListPushSubscriptions(ctx, uid)
	if err != nil || len(subs) == 0 {
		return 0, err
	}
	// ⚠ The bell's own read: their View, the unread rows, the quiet ones
	// held back, an ended digest window told first (quietFilter settles it).
	view := s.viewOf(ctx, uid)
	rows, _, err := s.ListVisible(ctx, uid, view.Bell, true, pushHead, 0, view.Keep, nil)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	var top int64
	for _, n := range rows {
		top = max(top, n.ID)
	}
	var words *pushWords
	sent := 0
	for _, sub := range subs {
		var fresh []*model.Notification
		for _, n := range rows {
			if n.ID > sub.ThroughID {
				fresh = append(fresh, n)
			}
		}
		if len(fresh) == 0 {
			continue
		}
		// The mark first: whoever moves it pushes these rows, nobody else.
		moved, err := s.store.AdvancePushMark(ctx, sub.ID, sub.ThroughID, top)
		if err != nil {
			slog.Warn("notify: push mark not moved", slog.Int64("device", sub.ID), slog.String("err", err.Error()))
			continue
		}
		if !moved {
			continue
		}
		if words == nil {
			w := s.pushWords(ctx, uid)
			words = &w
		}
		for _, m := range s.pushMessages(ctx, *words, fresh) {
			taken, gone := s.deliverPush(ctx, key, sub, m)
			if taken {
				sent++
			}
			if gone {
				break
			}
		}
	}
	return sent, nil
}

// pushWords is what every push to one person says the same: their language
// and the instance's name.
type pushWords struct {
	lang  string
	brand string
}

func (s *service) pushWords(ctx context.Context, uid int64) pushWords {
	u, err := s.store.GetUser(ctx, uid)
	if err != nil || u == nil {
		return pushWords{lang: PersonLang(nil), brand: s.brandName(ctx, nil)}
	}
	return pushWords{lang: PersonLang(u), brand: s.brandName(ctx, u)}
}

// brandName is the instance's name as the Branding page set it - a tenant's
// own for its people - and "filex" where nobody set one: the title of every
// push, as it is of the browser's pop-up.
func (s *service) brandName(ctx context.Context, u *model.User) string {
	keys := []string{"branding.name"}
	if u != nil && u.ProviderID != nil {
		keys = append([]string{fmt.Sprintf("tenant.%d.branding.name", *u.ProviderID)}, keys...)
	}
	for _, k := range keys {
		if v, err := s.store.GetSetting(ctx, k); err == nil && strings.TrimSpace(v) != "" {
			return clip(strings.TrimSpace(v), pushTitleMax)
		}
	}
	return "filex"
}

// pushMessage is one push's payload (web/public/notify-sw.js reads it).
type pushMessage struct {
	// V is the payload's version.
	V int `json:"v"`
	// ID is the notification; the worker opens it through the app
	// (`<scope>notify/<id>`), which resolves where it goes. 0: a summary.
	ID    int64  `json:"id,omitempty"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag"`
	// Open: the row has somewhere to go. Without it a tap only opens filex.
	Open bool `json:"open,omitempty"`
	// Renotify: alert again when it replaces one with the same tag (a
	// summary, a test). A row's own push replaces the page's toast of the
	// same row silently.
	Renotify bool `json:"renotify,omitempty"`

	urgency string
}

// pushMessages turns a device's new rows (newest first, as read) into pushes,
// oldest first: one each, or one summary for a burst.
func (s *service) pushMessages(ctx context.Context, w pushWords, fresh []*model.Notification) []pushMessage {
	cutoff := time.Now().Add(-pushMaxAge)
	var keep []*model.Notification
	for i := len(fresh) - 1; i >= 0; i-- {
		if fresh[i].CreatedAt.After(cutoff) {
			keep = append(keep, fresh[i])
		}
	}
	if len(keep) == 0 {
		return nil
	}
	if len(keep) > pushPerPass {
		return []pushMessage{{
			Title:    w.brand,
			Body:     srvtext.Plural(w.lang, "server.mail.digest.subject", len(keep), nil),
			Tag:      pushSummaryTag,
			Renotify: true,
			urgency:  urgencyOf(keep...),
		}}
	}
	out := make([]pushMessage, 0, len(keep))
	for _, n := range keep {
		out = append(out, pushMessage{
			ID:      n.ID,
			Title:   w.brand,
			Body:    clip(ToastBody(Say(w.lang, n)), pushBodyMax),
			Tag:     fmt.Sprintf("filex-notification-%d", n.ID),
			Open:    Opens(n.Target),
			urgency: urgencyOf(n),
		})
	}
	return out
}

// urgencyOf is a push service's urgency for these rows: high for an error or
// worse, so a phone in power saving still wakes for it.
func urgencyOf(rows ...*model.Notification) string {
	for _, n := range rows {
		if n.Severity == string(SeverityError) || n.Severity == string(SeverityCritical) {
			return "high"
		}
	}
	return "normal"
}

// clip cuts s to at most n bytes on a character boundary, with an ellipsis.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= len("…") {
		return ""
	}
	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// deliverPush sends m to one device. taken: the push service took it; gone:
// the device was forgotten (it no longer exists, its address is no longer
// one filex pushes to, or it refused too many pushes in a row).
func (s *service) deliverPush(ctx context.Context, key *webpush.Key, sub *model.PushSubscription, m pushMessage) (taken, gone bool) {
	forget := func(why string) (bool, bool) {
		slog.Info("notify: a push device was forgotten", slog.Int64("device", sub.ID), slog.String("why", why))
		_ = s.store.DropPushSubscription(ctx, sub.ID)
		return false, true
	}
	if err := s.push.cfg.Endpoints.Check(sub.Endpoint); err != nil {
		return forget("its push service is no longer accepted")
	}
	body, err := pushPayload(m)
	if err != nil {
		return false, false
	}
	res, err := s.push.sender.Send(ctx, key,
		webpush.Subscription{Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth},
		webpush.Message{Payload: body, TTL: pushTTL, Urgency: m.urgency})
	switch {
	case err == nil:
		_, _ = s.store.RecordPushResult(ctx, sub.ID, true)
		return true, false
	case res.Gone:
		return forget(fmt.Sprintf("the push service answered %d", res.Status))
	case errors.Is(err, webpush.ErrKeys), errors.Is(err, webpush.ErrEndpoint):
		return forget(err.Error())
	case res.Status >= 400 && res.Status < 500 &&
		res.Status != http.StatusTooManyRequests && res.Status != http.StatusRequestEntityTooLarge:
		// Refused (a key it no longer matches, a token it does not take):
		// counted, and forgotten past a few in a row - a server that is
		// briefly misconfigured does not lose everybody's devices at once.
		if n, _ := s.store.RecordPushResult(ctx, sub.ID, false); n >= pushDropAfter {
			return forget(fmt.Sprintf("refused %d pushes in a row", n))
		}
	}
	slog.Debug("notify: push not taken", slog.Int64("device", sub.ID), slog.String("err", err.Error()))
	return false, false
}

// pushPayload is m as the worker reads it, made to fit one push: JSON without
// HTML escaping (a `<` in a name is one byte, not six), the body shortened
// until it fits.
func pushPayload(m pushMessage) ([]byte, error) {
	m.V = 1
	for {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(m); err != nil {
			return nil, err
		}
		out := bytes.TrimRight(buf.Bytes(), "\n")
		if len(out) <= webpush.MaxPayload || m.Body == "" {
			return out, nil
		}
		m.Body = clip(m.Body, len(m.Body)/2)
	}
}
