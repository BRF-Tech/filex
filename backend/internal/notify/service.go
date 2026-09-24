package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Service is the public façade subsystems use to emit events. Send
// persists to DB synchronously and dispatches the webhook in a
// background goroutine — callers do not block on the upstream POST.
type Service interface {
	// Send fans the event out to (a) the in-app history table and
	// (b) every configured webhook destination: the legacy global
	// webhook plus each enabled, event-matching webhook_targets row.
	// Errors from webhook delivery are recorded against the row, not
	// bubbled up — webhook failure should never break the originating
	// request.
	Send(ctx context.Context, e Event) (id int64, err error)

	// List + Mark + Settings just delegate to the store; they're on
	// the Service interface so handlers don't have to know about the
	// store. Pass userID nil for admin-global views; bell (which
	// broadcasts a per-user read takes) is ignored there.
	List(ctx context.Context, userID *int64, bell Bell, onlyUnread bool, limit, offset int) ([]*model.Notification, int64, error)
	UnreadCount(ctx context.Context, userID *int64, bell Bell) (int64, error)
	// ListVisible is a per-user read whose BROADCASTS the caller judges one by
	// one after the store (keep — a member's grants, a tenant): the reader's
	// own rows are counted in SQL and only the broadcasts their bell admits are
	// walked, so the total is exact and a page is cut where the reader sees
	// it. keep nil is List. keepOwn, when set, judges the reader's own rows too
	// (a folder-confined token reads only its folder's), walked the same way.
	ListVisible(ctx context.Context, userID int64, bell Bell, onlyUnread bool, limit, offset int, keep, keepOwn func(*model.Notification) bool) ([]*model.Notification, int64, error)
	// History is the admin-global list with each broadcast's read state as
	// readerID has it (0: the rows' own column).
	History(ctx context.Context, readerID int64, onlyUnread bool, limit, offset int) ([]*model.Notification, int64, error)
	// MarkRead and MarkAllRead stamp rows ADDRESSED to userID only. A
	// broadcast is read per reader: MarkBroadcastsRead marks single ones,
	// after the caller has checked the reader may see them, and
	// MarkAllBroadcastsRead reads every broadcast up to now for the reader —
	// ones their bell does not show included, which only ever changes what
	// that reader sees.
	MarkRead(ctx context.Context, id int64, userID *int64) error
	MarkAllRead(ctx context.Context, userID *int64) error
	MarkBroadcastsRead(ctx context.Context, readerID int64, ids []int64) error
	MarkAllBroadcastsRead(ctx context.Context, readerID int64) error
	GetSettings(ctx context.Context, userID int64) (*model.NotificationSettings, error)
	UpsertSettings(ctx context.Context, s *model.NotificationSettings) error

	// SetWebhook is invoked by the admin handler when the operator
	// changes the webhook URL/token at runtime. Pass empty values to
	// disable webhook delivery without taking the in-app channel down.
	SetWebhook(url, bearerToken string)

	// WebhookConfig returns the currently effective webhook URL and a
	// flag indicating whether a token is set (the token itself is
	// never echoed back).
	WebhookConfig() (url string, tokenSet bool)

	// TestTarget synchronously fires a sample event at one webhook
	// target (single attempt, no retries) and returns the outcome.
	// Backs the admin "Test" button; the result is also recorded as
	// the target's in-memory last-delivery status.
	TestTarget(ctx context.Context, target *model.WebhookTarget) TargetDeliveryStatus

	// TargetStatuses returns the last-delivery status per target id.
	// In-memory only (process lifetime) — the schema stays at the
	// frozen webhook_targets contract, so per-target status is not
	// persisted; the notifications table remains the durable audit.
	TargetStatuses() map[int64]TargetDeliveryStatus

	// Wait blocks until any in-flight webhook deliveries return. Used
	// by tests; production calls Stop() instead which cancels the
	// dispatch context AND waits.
	Wait()

	// Stop cancels in-flight dispatches and waits for them to finish.
	Stop()
}

// TargetDeliveryStatus is the outcome of the most recent delivery (or
// admin test fire) to one webhook target.
type TargetDeliveryStatus struct {
	Status string    `json:"status"` // "sent" | "failed"
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

// Config bootstraps a Service. WebhookURL and WebhookToken are
// optional — leave empty to skip webhook delivery (in-app still
// records the event).
type Config struct {
	WebhookURL    string
	WebhookToken  string
	HTTPTimeout   time.Duration
	RetryBackoffs []time.Duration // attempt delays; default {1s,3s,9s}
}

// New returns a Service backed by the given store.
func New(store db.Store, cfg Config) Service {
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}
	if len(cfg.RetryBackoffs) == 0 {
		cfg.RetryBackoffs = []time.Duration{
			1 * time.Second,
			3 * time.Second,
			9 * time.Second,
		}
	}
	s := &service{
		store:        store,
		http:         &http.Client{Timeout: cfg.HTTPTimeout},
		backoffs:     cfg.RetryBackoffs,
		stopCh:       make(chan struct{}),
		targetStatus: make(map[int64]TargetDeliveryStatus),
	}
	s.SetWebhook(cfg.WebhookURL, cfg.WebhookToken)
	return s
}

// service is the concrete impl.
type service struct {
	store db.Store
	http  *http.Client

	mu         sync.RWMutex
	webhookURL string
	bearer     string
	backoffs   []time.Duration
	stopOnce   sync.Once
	stopCh     chan struct{}
	inflightWG sync.WaitGroup

	// targetStatus caches the last delivery outcome per webhook target
	// id (guarded by tsMu). Feeds the admin list's "last status" column.
	tsMu         sync.Mutex
	targetStatus map[int64]TargetDeliveryStatus
}

// destination is one webhook endpoint a single event is delivered to —
// either the legacy global webhook (targetID 0, bearer auth) or one
// webhook_targets row (HMAC signing when secret is set).
type destination struct {
	targetID int64
	name     string
	url      string
	bearer   string
	secret   string
}

// Signature computes the X-Filex-Signature header value for a payload
// signed with secret: "sha256=" + hex(HMAC-SHA256(secret, body)).
// Exported so receivers/tests can verify deliveries.
func Signature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Send persists then async-delivers.
func (s *service) Send(ctx context.Context, e Event) (int64, error) {
	if e.Event == "" || e.Severity == "" {
		return 0, errors.New("notify: event and severity required")
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	if e.At.IsZero() {
		e.At = e.TS
	}
	// What the event means to a person — or that it means nothing and is not
	// announced at all (personview.go). Before anything is stored or sent, so
	// the bell row and every webhook body carry the same meaning.
	var announce bool
	if e, announce = personView(e); !announce {
		slog.Debug("notify: not announcing a write inside filex's own directories",
			slog.String("event", string(e.Event)))
		return 0, nil
	}
	if e.Title == "" {
		e.Title = string(e.Event)
	}
	e.Target = s.resolveTarget(ctx, e)
	metaJSON, err := marshalMeta(e)
	if err != nil {
		return 0, fmt.Errorf("notify: marshal meta: %w", err)
	}
	id, err := s.store.InsertNotification(ctx, &model.NotificationInput{
		Event:    string(e.Event),
		Severity: string(e.Severity),
		Title:    e.Title,
		Body:     e.Body,
		MetaJSON: metaJSON,
		UserID:   e.UserID,
	})
	if err != nil {
		return 0, err
	}
	s.dispatch(id, e)
	return id, nil
}

// marshalMeta folds the structured Node/Share/Actor refs into the
// persisted meta_json next to the free-form Meta map, so the in-app
// history keeps the event context without extra columns.
func marshalMeta(e Event) ([]byte, error) {
	hasTarget := e.Target != nil && e.Target.Kind != "" && e.Target.Kind != TargetNone
	if len(e.Meta) == 0 && e.Node == nil && e.Share == nil && e.Actor == nil && !hasTarget {
		return []byte("{}"), nil
	}
	m := make(map[string]any, len(e.Meta)+4)
	for k, v := range e.Meta {
		m[k] = v
	}
	if e.Node != nil {
		m["node"] = e.Node
	}
	if e.Share != nil {
		m["share"] = e.Share
	}
	if e.Actor != nil {
		m["actor"] = e.Actor
	}
	// ⚠ Only a REAL target is persisted. A `{"kind":"none"}` blob in every
	// row would be a field that is always present and never useful, and the
	// read side (model.TargetFromMeta) already treats "absent" and "none" as
	// the same answer — so writing it would only make every historical row
	// look different from a new one for no gain.
	if hasTarget {
		m["target"] = e.Target
	}
	return json.Marshal(m)
}

// resolveTarget produces the ONE target the event ships with — the backend
// half of the single-resolver rule.
//
// Two jobs, both of which have to happen in exactly one place:
//
//  1. Fill in the storage NAME. Emitters know a numeric storage id (it is what
//     their node row carries) and the clients cannot turn one into a name —
//     /api/admin/storages is admin-only, and the explorer addresses storages
//     by name. Resolving it here means one DB lookup per event on a path the
//     caller has already left, instead of a lookup (or a guess) in every
//     emitter.
//  2. Refuse to ship half an address. A target whose storage cannot be
//     resolved — the row is gone, the store errored — is downgraded to
//     TargetNone, which every surface renders as "opens the notifications
//     page". A path with no storage would otherwise resolve, client-side, to
//     whatever storage happened to be open, which is how a click lands
//     somebody in a folder that is not the one the event is about.
//
// An emitter that set no target at all still gets one: a share ref means the
// share, a node means the file. That default is what stops a new emitter from
// silently producing unclickable notifications.
func (s *service) resolveTarget(ctx context.Context, e Event) *Target {
	t := e.Target
	if t == nil {
		switch {
		case e.Share != nil && strings.TrimSpace(e.Share.Token) != "":
			t = ShareTarget(e.Share.Token)
		case e.Node != nil && strings.TrimSpace(e.Node.Path) != "":
			t = FileTarget(e.Node.Path)
		default:
			return &Target{Kind: TargetNone}
		}
	}
	switch t.Kind {
	case TargetShare:
		if strings.TrimSpace(t.ID) == "" {
			return &Target{Kind: TargetNone}
		}
		return &Target{Kind: TargetShare, ID: t.ID}
	case TargetApp:
		// An app's home page: no storage to resolve, and nothing else to
		// carry. Half of one (no plugin, no view) is no address at all.
		if t.Open == nil || strings.TrimSpace(t.Open.Plugin) == "" || strings.TrimSpace(t.Open.View) == "" {
			return &Target{Kind: TargetNone}
		}
		o := *t.Open
		o.Action = ""
		return &Target{Kind: TargetApp, Open: &o}
	case TargetFile, TargetDir, TargetTrash:
	default:
		return &Target{Kind: TargetNone}
	}
	out := &Target{Kind: t.Kind, Storage: strings.TrimSpace(t.Storage), Path: t.Path, ID: t.ID, Open: t.Open}
	if out.Storage == "" && e.Node != nil && e.Node.StorageID != 0 && s.store != nil {
		if st, err := s.store.GetStorage(ctx, e.Node.StorageID); err == nil && st != nil {
			out.Storage = st.Name
		} else if err != nil {
			slog.Warn("notify: resolve target storage",
				slog.Int64("storage_id", e.Node.StorageID),
				slog.String("err", err.Error()))
		}
	}
	if out.Kind == TargetTrash {
		// The Trash view spans every storage, so it is a destination on its
		// own; the storage and path only say which row to SELECT there. Half
		// of that is no selection at all, never a wrong one.
		if out.Storage == "" || out.Path == "" {
			return &Target{Kind: TargetTrash}
		}
		return &Target{Kind: TargetTrash, Storage: out.Storage, Path: out.Path}
	}
	if out.Storage == "" {
		// A file target with no storage is not a location. Say "nothing to
		// open" rather than let a client pick a storage for us.
		return &Target{Kind: TargetNone}
	}
	// A file target whose path is only the storage root names no file.
	if out.Kind == TargetFile && out.Path == "" {
		out.Kind = TargetDir
	}
	return out
}

// dispatch fires off the webhook fan-out. The function returns
// immediately; a background goroutine resolves the destination set
// (legacy global webhook + matching webhook_targets rows), runs each
// destination's retry chain in parallel, and writes one aggregated
// webhook_status back on the notification row.
//
// We deliberately do NOT propagate the request ctx — short-lived HTTP
// handlers don't extend their lifetime to the webhook call. Instead a
// derived context with the configured HTTP timeout per attempt is used,
// and Stop() cancels via stopCh.
func (s *service) dispatch(id int64, e Event) {
	s.mu.RLock()
	legacyURL := s.webhookURL
	token := s.bearer
	backoffs := append([]time.Duration(nil), s.backoffs...)
	s.mu.RUnlock()

	body, err := json.Marshal(e)
	if err != nil {
		_ = s.store.UpdateWebhookStatus(context.Background(), id, string(WebhookStatusFailed), "marshal event: "+err.Error())
		return
	}

	s.inflightWG.Add(1)
	go func() {
		defer s.inflightWG.Done()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-s.stopCh:
				cancel()
			case <-ctx.Done():
			}
		}()

		dests := make([]destination, 0, 4)
		if legacyURL != "" {
			dests = append(dests, destination{url: legacyURL, bearer: token})
		}
		targets, err := s.store.ListWebhookTargets(ctx)
		if err != nil {
			// Degrade to the legacy destination rather than dropping the
			// event on the floor; the admin list will surface DB errors.
			slog.Warn("notify: list webhook targets", slog.String("err", err.Error()))
		}
		for _, t := range targets {
			if !t.Enabled || !t.MatchesEvent(string(e.Event)) {
				continue
			}
			dests = append(dests, destination{targetID: t.ID, name: t.Name, url: t.URL, secret: t.Secret})
		}
		if len(dests) == 0 {
			// No webhook configured — still record the skip for the audit.
			_ = s.store.UpdateWebhookStatus(context.Background(), id, string(WebhookStatusSkipped), "no webhook URL configured")
			return
		}

		var (
			wg    sync.WaitGroup
			errMu sync.Mutex
			errs  []string
		)
		for _, d := range dests {
			wg.Add(1)
			go func(d destination) {
				defer wg.Done()
				code, err := s.deliver(ctx, d, string(e.Event), body, backoffs)
				if d.targetID != 0 {
					s.recordTargetStatus(d.targetID, code, err)
				}
				if err != nil {
					errMu.Lock()
					if d.targetID == 0 {
						errs = append(errs, err.Error())
					} else {
						errs = append(errs, d.name+": "+err.Error())
					}
					errMu.Unlock()
				}
			}(d)
		}
		wg.Wait()

		if len(errs) == 0 {
			_ = s.store.UpdateWebhookStatus(context.Background(), id, string(WebhookStatusSent), "")
		} else {
			_ = s.store.UpdateWebhookStatus(context.Background(), id, string(WebhookStatusFailed), strings.Join(errs, "; "))
		}
	}()
}

// deliver runs the retry chain against one destination. Every attempt
// of one delivery shares a single X-Filex-Delivery id (mint-per-
// delivery, GitHub-style) so receivers can deduplicate retries.
//
// The returned int is the FINAL attempt's HTTP status code — 0 when the
// request never got a response (DNS/connect/timeout/bad URL). It feeds
// the persisted per-target last_status column.
func (s *service) deliver(ctx context.Context, d destination, eventName string, body []byte, backoffs []time.Duration) (int, error) {
	deliveryID := uuid.NewString()
	var (
		lastErr  string
		lastCode int
	)
	for attempt, wait := 0, time.Duration(0); attempt <= len(backoffs); attempt++ {
		if attempt > 0 {
			wait = backoffs[attempt-1]
		}
		if wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return lastCode, errors.New("service stopped mid-retry")
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
		if err != nil {
			return 0, errors.New("build request: " + err.Error()) // don't retry — the URL won't magically parse
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "filex-webhook/1.0")
		req.Header.Set("X-Filex-Event", eventName)
		req.Header.Set("X-Filex-Delivery", deliveryID)
		if d.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+d.bearer)
		}
		if d.secret != "" {
			req.Header.Set("X-Filex-Signature", Signature(d.secret, body))
		}
		resp, err := s.http.Do(req)
		if err != nil {
			lastErr = err.Error()
			lastCode = 0
			continue
		}
		func() {
			defer resp.Body.Close()
			lastCode = resp.StatusCode
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				lastErr = ""
			} else {
				lastErr = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
		}()
		if lastErr == "" {
			return lastCode, nil
		}
	}
	return lastCode, errors.New(lastErr)
}

// recordTargetStatus stores the outcome of the newest delivery to a
// target — both in the in-memory map (sync Test responses) and in the
// webhook_targets last_* columns (migration 00019) so the admin list
// survives restarts. httpStatus is the final attempt's status code
// (0 = no response). Best-effort: a DB error only logs.
func (s *service) recordTargetStatus(targetID int64, httpStatus int, err error) {
	now := time.Now().UTC()
	st := TargetDeliveryStatus{Status: "sent", At: now}
	errMsg := ""
	if err != nil {
		st.Status = "failed"
		st.Error = err.Error()
		errMsg = err.Error()
	}
	s.tsMu.Lock()
	s.targetStatus[targetID] = st
	s.tsMu.Unlock()
	if uerr := s.store.UpdateWebhookTargetDelivery(context.Background(), targetID, httpStatus, errMsg, now); uerr != nil {
		slog.Warn("notify: persist target delivery status",
			slog.Int64("target", targetID), slog.String("err", uerr.Error()))
	}
}

// TestTarget fires a synthetic event at one target with a single
// attempt (no retries) and returns the outcome synchronously.
func (s *service) TestTarget(ctx context.Context, target *model.WebhookTarget) TargetDeliveryStatus {
	now := time.Now().UTC()
	sample := Event{
		Event:    "webhook_test",
		Severity: SeverityInfo,
		Title:    "filex webhook test",
		Body:     "Sample delivery fired from the admin panel to verify this webhook target.",
		Meta:     map[string]any{"source": "webhook_test", "target": target.Name},
		TS:       now,
		At:       now,
		Node: &NodeRef{
			StorageID: 0,
			Path:      "/example/hello.txt",
			Name:      "hello.txt",
			Size:      11,
		},
		// ⚠⚠ `none`, SAID rather than left to happen. The sample carries a
		// `node` so a receiver can see the payload shape it will get, and an
		// event with a node and no target is exactly the silent shape rule 1
		// warns about (docs/NOTIFICATIONS.md → "The bell, and who can reach
		// it"). It did resolve to `none` — but only because storage id 0
		// happens to be unresolvable, which is an accident standing in for a
		// decision. There is no file called `/example/hello.txt`, so a click
		// must open nothing, and saying so keeps that true whatever the
		// resolver later does with id 0.
		Target: &Target{Kind: TargetNone},
	}
	body, err := json.Marshal(sample)
	if err != nil {
		return TargetDeliveryStatus{Status: "failed", Error: "marshal event: " + err.Error(), At: now}
	}
	d := destination{targetID: target.ID, name: target.Name, url: target.URL, secret: target.Secret}
	code, deliverErr := s.deliver(ctx, d, string(sample.Event), body, nil)
	if target.ID != 0 {
		s.recordTargetStatus(target.ID, code, deliverErr)
	}
	st := TargetDeliveryStatus{Status: "sent", At: time.Now().UTC()}
	if deliverErr != nil {
		st.Status = "failed"
		st.Error = deliverErr.Error()
	}
	return st
}

// TargetStatuses returns a copy of the in-memory last-status map.
func (s *service) TargetStatuses() map[int64]TargetDeliveryStatus {
	s.tsMu.Lock()
	defer s.tsMu.Unlock()
	out := make(map[int64]TargetDeliveryStatus, len(s.targetStatus))
	for k, v := range s.targetStatus {
		out[k] = v
	}
	return out
}

// bellPrefs resolves the per-user display preferences that gate the in-app
// bell: silenced is in_app_enabled=false, muted is the muted_events list.
//
// ⚠ userID nil is the admin/global view and is NEVER filtered. It is an audit
// of everything the system recorded, and one admin's personal mute list must
// not delete rows from it.
//
// ⚠ It fails OPEN. A settings row that cannot be read — none written yet, a DB
// hiccup — resolves to "show everything", because the alternative is an
// unreadable preference silently emptying a bell and hiding a real event (an
// antivirus hit, a failed replica) behind a transient error.
//
// muted starts with what the reader's Bell leaves out at every address (the
// operator alarms, for a non-administrator — Bell.never), which rides the same
// SQL filter so the badge and the list agree.
func (s *service) bellPrefs(ctx context.Context, userID *int64, bell Bell) (muted []string, silenced bool) {
	if userID == nil {
		return nil, false
	}
	muted = eventIDs(bell.never())
	st, err := s.store.GetNotificationSettings(ctx, *userID)
	if err != nil {
		slog.Warn("notify: read notification settings",
			slog.Int64("user_id", *userID), slog.String("err", err.Error()))
		return muted, false
	}
	if st == nil {
		return muted, false
	}
	return append(muted, st.MutedList()...), !st.InAppEnabled
}

// List returns the user's bell history with their preferences applied.
//
// ⚠⚠ The preferences gate the READ, not the write. Send still records every
// event, so muting one neither erases it from the audit nor touches webhook
// delivery — that is global and configured in Admin → Webhooks. Muting
// changes what a user sees, not what the system keeps. The same holds for
// bell: a broadcast a bell does not take stays in the table and in the
// admin-global list.
//
// userID nil is the admin-global list, read with the row's own read state
// (History reads it with a reader's).
func (s *service) List(ctx context.Context, userID *int64, bell Bell, onlyUnread bool, limit, offset int) ([]*model.Notification, int64, error) {
	if userID == nil {
		return s.History(ctx, 0, onlyUnread, limit, offset)
	}
	muted, silenced := s.bellPrefs(ctx, userID, bell)
	if silenced {
		return nil, 0, nil
	}
	return s.read(ctx, userID, onlyUnread, muted, bellFilter(userID, bell), limit, offset)
}

// History reads the admin-global list: every row, a broadcast carrying
// readerID's read state (0: the rows' own column), each broadcast labelled
// with who it reaches (model.Notification.Audience).
//
// ⚠ Through the same read as a bell (read below). PR #43 gave the history a
// store call of its own, which skipped v0.43.0's person view: rows about
// filex's own directories and `meta.trash_path` came back on the admin page,
// and so did `user #2` where a name belongs.
func (s *service) History(ctx context.Context, readerID int64, onlyUnread bool, limit, offset int) ([]*model.Notification, int64, error) {
	return s.read(ctx, nil, onlyUnread, nil, model.BroadcastFilter{ReaderID: readerID}, limit, offset)
}

// visibleScanMax bounds the broadcasts ListVisible walks per read. A bell's
// broadcasts are the few kinds it admits at all (Bell), so the walk is short;
// past this, the rest are neither shown nor counted rather than paid for on
// every poll.
const visibleScanMax = 5000

// storePageMax is the store's own per-read ceiling (ListNotifications clamps
// limit to 500 and turns anything above into 50).
const storePageMax = 500

func (s *service) ListVisible(ctx context.Context, userID int64, bell Bell, onlyUnread bool, limit, offset int, keep, keepOwn func(*model.Notification) bool) ([]*model.Notification, int64, error) {
	uid := userID
	if keep == nil {
		return s.List(ctx, &uid, bell, onlyUnread, limit, offset)
	}
	if limit <= 0 || limit > storePageMax {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	muted, silenced := s.bellPrefs(ctx, &uid, bell)
	if silenced {
		return nil, 0, nil
	}
	f := bellFilter(&uid, bell)

	// The broadcasts, walked and judged.
	bf := f
	bf.BroadcastsOnly = true
	shown, err := s.walk(ctx, uid, onlyUnread, muted, bf, keep)
	if err != nil {
		return nil, 0, err
	}

	of := f
	of.OwnOnly = true
	if keepOwn != nil {
		// The reader's own rows judged as well: walked, not counted.
		own, err := s.walk(ctx, uid, onlyUnread, muted, of, keepOwn)
		if err != nil {
			return nil, 0, err
		}
		merged := mergeNewestFirst(own, shown)
		total := int64(len(merged))
		if offset >= len(merged) {
			return []*model.Notification{}, total, nil
		}
		return merged[offset:min(offset+limit, len(merged))], total, nil
	}

	// The reader's own rows, counted in SQL; only as many read as the page
	// can need (a page at `offset` holds at most offset+limit of them).
	var (
		own      []*model.Notification
		ownTotal int64
	)
	need := offset + limit
	for off := 0; ; off += storePageMax {
		n := min(storePageMax, need-off)
		rows, total, err := s.read(ctx, &uid, onlyUnread, muted, of, max(n, 1), off)
		if err != nil {
			return nil, 0, err
		}
		if off == 0 {
			ownTotal = total
		}
		own = append(own, rows...)
		if len(own) >= need || len(rows) < n || int64(off+storePageMax) >= total {
			break
		}
	}

	merged := mergeNewestFirst(own, shown)
	if offset >= len(merged) {
		return []*model.Notification{}, ownTotal + int64(len(shown)), nil
	}
	return merged[offset:min(offset+limit, len(merged))], ownTotal + int64(len(shown)), nil
}

// walk reads one half of a per-user read in store pages, up to visibleScanMax
// rows, and keeps what keep admits.
func (s *service) walk(ctx context.Context, uid int64, onlyUnread bool, muted []string, f model.BroadcastFilter, keep func(*model.Notification) bool) ([]*model.Notification, error) {
	var out []*model.Notification
	for off := 0; off < visibleScanMax; off += storePageMax {
		rows, total, err := s.read(ctx, &uid, onlyUnread, muted, f, storePageMax, off)
		if err != nil {
			return nil, err
		}
		for _, n := range rows {
			if keep(n) {
				out = append(out, n)
			}
		}
		if len(rows) < storePageMax && int64(off+storePageMax) >= total {
			break
		}
	}
	return out, nil
}

// mergeNewestFirst merges two lists already in the store's order (created_at,
// then id, newest first).
func mergeNewestFirst(a, b []*model.Notification) []*model.Notification {
	out := make([]*model.Notification, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if newerThan(a[i], b[j]) {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	return append(out, b[j:]...)
}

func newerThan(x, y *model.Notification) bool {
	if !x.CreatedAt.Equal(y.CreatedAt) {
		return x.CreatedAt.After(y.CreatedAt)
	}
	return x.ID > y.ID
}

// read is the one path every list takes out of the store.
func (s *service) read(ctx context.Context, userID *int64, onlyUnread bool, muted []string, f model.BroadcastFilter, limit, offset int) ([]*model.Notification, int64, error) {
	rows, total, err := s.store.ListNotifications(ctx, userID, onlyUnread, muted, hiddenBodies(), f, limit, offset)
	// One sanitise + hydrate for every reader: the user bell and the
	// admin-global list both come through here, so `target` cannot be present
	// on one surface and missing on the other — and neither can show a row
	// about filex's own directories, whenever it was recorded (personview.go).
	kept := rows[:0]
	for _, n := range rows {
		if !sanitizeRow(n) {
			// Only a row the SQL filter could not recognise by its body gets
			// here; it leaves this page one short rather than showing it.
			total--
			continue
		}
		n.HydrateTarget()
		kept = append(kept, n)
	}
	if userID == nil {
		s.nameOwners(ctx, kept)
		markAudience(kept)
	}
	return kept, total, err
}

// markAudience says, on the admin list, who each broadcast reaches — by the
// same rule the bells apply (BroadcastAudience). ⚠ The Scope column said
// "Everyone" beside "filex v0.42.2 available" — a row no plain user's bell
// shows since the release-candidate sweep (2026-09-21); since PR #42 it would
// have said it beside a drop notice only administrators get, too.
func markAudience(rows []*model.Notification) {
	for _, n := range rows {
		n.Audience = BroadcastAudience(n)
		n.AdminsOnly = n.Audience == AudienceAdmins
	}
}

// nameOwners fills UserName on the admin list's user-scoped rows: one query
// for the page (GetUserDisplayNames, names only). Best-effort — a lookup that
// fails leaves the id, which the page still shows.
func (s *service) nameOwners(ctx context.Context, rows []*model.Notification) {
	ids := make([]int64, 0, len(rows))
	seen := map[int64]bool{}
	for _, n := range rows {
		if n.UserID != nil && !seen[*n.UserID] {
			seen[*n.UserID] = true
			ids = append(ids, *n.UserID)
		}
	}
	if len(ids) == 0 {
		return
	}
	names, err := s.store.GetUserDisplayNames(ctx, ids)
	if err != nil {
		return
	}
	for _, n := range rows {
		if n.UserID != nil {
			n.UserName = names[*n.UserID]
		}
	}
}

func (s *service) MarkBroadcastsRead(ctx context.Context, readerID int64, ids []int64) error {
	return s.store.MarkBroadcastsRead(ctx, readerID, ids)
}

func (s *service) MarkAllBroadcastsRead(ctx context.Context, readerID int64) error {
	return s.store.MarkAllBroadcastsRead(ctx, readerID)
}

func (s *service) UnreadCount(ctx context.Context, userID *int64, bell Bell) (int64, error) {
	muted, silenced := s.bellPrefs(ctx, userID, bell)
	if silenced {
		return 0, nil
	}
	return s.store.UnreadNotificationCount(ctx, userID, muted, hiddenBodies(), bellFilter(userID, bell))
}

func (s *service) MarkRead(ctx context.Context, id int64, userID *int64) error {
	return s.store.MarkNotificationRead(ctx, id, userID)
}

func (s *service) MarkAllRead(ctx context.Context, userID *int64) error {
	return s.store.MarkAllNotificationsRead(ctx, userID)
}

func (s *service) GetSettings(ctx context.Context, userID int64) (*model.NotificationSettings, error) {
	return s.store.GetNotificationSettings(ctx, userID)
}

func (s *service) UpsertSettings(ctx context.Context, st *model.NotificationSettings) error {
	return s.store.UpsertNotificationSettings(ctx, st)
}

func (s *service) SetWebhook(url, token string) {
	s.mu.Lock()
	s.webhookURL = url
	s.bearer = token
	s.mu.Unlock()
}

func (s *service) WebhookConfig() (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.webhookURL, s.bearer != ""
}

func (s *service) Wait() {
	s.inflightWG.Wait()
}

func (s *service) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.inflightWG.Wait()
}
