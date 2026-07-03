package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Service is the public façade subsystems use to emit events. Send
// persists to DB synchronously and dispatches the webhook in a
// background goroutine — callers do not block on the upstream POST.
type Service interface {
	// Send fans the event out to (a) the in-app history table and
	// (b) the configured webhook. Errors from webhook delivery are
	// recorded against the row, not bubbled up — webhook failure
	// should never break the originating request.
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

	// Wait blocks until any in-flight webhook deliveries return. Used
	// by tests; production calls Stop() instead which cancels the
	// dispatch context AND waits.
	Wait()

	// Stop cancels in-flight dispatches and waits for them to finish.
	Stop()
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
		store:    store,
		http:     &http.Client{Timeout: cfg.HTTPTimeout},
		backoffs: cfg.RetryBackoffs,
		stopCh:   make(chan struct{}),
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
}

// Send persists then async-delivers.
func (s *service) Send(ctx context.Context, e Event) (int64, error) {
	if e.Event == "" || e.Severity == "" {
		return 0, errors.New("notify: event and severity required")
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	if e.Title == "" {
		e.Title = string(e.Event)
	}
	metaJSON := []byte("{}")
	if len(e.Meta) > 0 {
		j, err := json.Marshal(e.Meta)
		if err != nil {
			return 0, fmt.Errorf("notify: marshal meta: %w", err)
		}
		metaJSON = j
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

// dispatch fires off the webhook attempt chain. The function returns
// immediately; the goroutine updates webhook_status when finished.
//
// We deliberately do NOT propagate the request ctx — short-lived HTTP
// handlers don't extend their lifetime to the webhook call. Instead a
// derived context with the configured HTTP timeout per attempt is used,
// and Stop() cancels via stopCh.
func (s *service) dispatch(id int64, e Event) {
	s.mu.RLock()
	url := s.webhookURL
	token := s.bearer
	backoffs := append([]time.Duration(nil), s.backoffs...)
	s.mu.RUnlock()

	if url == "" {
		// No webhook configured — still record the skip for the audit.
		_ = s.store.UpdateWebhookStatus(context.Background(), id, string(WebhookStatusSkipped), "no webhook URL configured")
		return
	}

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

		var lastErr string
		for attempt, wait := 0, time.Duration(0); attempt <= len(backoffs); attempt++ {
			if attempt > 0 {
				wait = backoffs[attempt-1]
			}
			if wait > 0 {
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					_ = s.store.UpdateWebhookStatus(context.Background(), id, string(WebhookStatusFailed), "service stopped mid-retry")
					return
				}
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
			if err != nil {
				lastErr = "build request: " + err.Error()
				break // don't retry — no chance the URL will magically parse
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "filex-webhook/1.0")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, err := s.http.Do(req)
			if err != nil {
				lastErr = err.Error()
				continue
			}
			func() {
				defer resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					_ = s.store.UpdateWebhookStatus(context.Background(), id, string(WebhookStatusSent), "")
					lastErr = ""
				} else {
					lastErr = fmt.Sprintf("HTTP %d", resp.StatusCode)
				}
			}()
			if lastErr == "" {
				return
			}
		}
		_ = s.store.UpdateWebhookStatus(context.Background(), id, string(WebhookStatusFailed), lastErr)
	}()
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
