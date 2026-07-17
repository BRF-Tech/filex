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
	// store. Pass userID nil for admin-global views.
	List(ctx context.Context, userID *int64, onlyUnread bool, limit, offset int) ([]*model.Notification, int64, error)
	UnreadCount(ctx context.Context, userID *int64) (int64, error)
	MarkRead(ctx context.Context, id int64, userID *int64) error
	MarkAllRead(ctx context.Context, userID *int64) error
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
	if e.Title == "" {
		e.Title = string(e.Event)
	}
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
	if len(e.Meta) == 0 && e.Node == nil && e.Share == nil && e.Actor == nil {
		return []byte("{}"), nil
	}
	m := make(map[string]any, len(e.Meta)+3)
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
	return json.Marshal(m)
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

func (s *service) List(ctx context.Context, userID *int64, onlyUnread bool, limit, offset int) ([]*model.Notification, int64, error) {
	return s.store.ListNotifications(ctx, userID, onlyUnread, limit, offset)
}

func (s *service) UnreadCount(ctx context.Context, userID *int64) (int64, error) {
	return s.store.UnreadNotificationCount(ctx, userID)
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
