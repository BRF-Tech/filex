// Package mailer sends transactional email (share/invite notices) over an
// operator-configured SMTP server. The config lives in the settings table
// (smtp.*) and is validated by a periodic job (server.Start runs Verify every
// 5 minutes). Send is a no-op error (ErrNotVerified) until a verification
// succeeds, so the invite flow can fall back to showing the link on-screen.
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
)

// Settings keys (read via db.Store.GetSetting / written by the admin settings UI).
const (
	KeyHost       = "smtp.host"
	KeyPort       = "smtp.port"
	KeyUser       = "smtp.username"
	KeyPass       = "smtp.password"
	KeyFrom       = "smtp.from"
	KeyTLS        = "smtp.tls" // "starttls" | "tls" | "none"
	KeyVerifiedAt = "smtp.verified_at"
)

// ErrNotConfigured / ErrNotVerified let callers distinguish "no SMTP set up"
// from "set up but not yet confirmed working".
var (
	ErrNotConfigured = errors.New("smtp not configured")
	ErrNotVerified   = errors.New("smtp config not verified")
)

// Config is the resolved SMTP connection config.
type Config struct {
	Host, Port, Username, Password, From, TLS string
}

func (c Config) configured() bool { return c.Host != "" && c.Port != "" && c.From != "" }

// Service reads config from the store, verifies it, and sends mail.
type Service struct {
	store    db.Store
	mu       sync.RWMutex
	verified bool
}

// New constructs the mailer service.
func New(store db.Store) *Service { return &Service{store: store} }

// Load reads the current SMTP config from settings.
func (s *Service) Load(ctx context.Context) Config {
	get := func(k string) string { v, _ := s.store.GetSetting(ctx, k); return strings.TrimSpace(v) }
	return Config{
		Host:     get(KeyHost),
		Port:     get(KeyPort),
		Username: get(KeyUser),
		Password: get(KeyPass),
		From:     get(KeyFrom),
		TLS:      strings.ToLower(get(KeyTLS)),
	}
}

// Verified reports whether the last verification succeeded.
func (s *Service) Verified() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.verified
}

// PrimeFromStore optimistically restores the verified flag from the persisted
// smtp.verified_at setting. The in-memory flag resets to false on every
// process restart (deploy), which would make sends fall back to "show the
// link" until the next verification tick — priming from last-known-good closes
// that window instantly; the boot verify then refreshes/corrects it.
func (s *Service) PrimeFromStore(ctx context.Context) {
	v, _ := s.store.GetSetting(ctx, KeyVerifiedAt)
	if strings.TrimSpace(v) != "" {
		s.mu.Lock()
		s.verified = true
		s.mu.Unlock()
	}
}

// Verify performs an SMTP handshake (+ auth when credentials are set) and
// records the result. Called on boot and every 5 minutes by server.Start.
func (s *Service) Verify(ctx context.Context) error {
	cfg := s.Load(ctx)
	if !cfg.configured() {
		s.setVerified(ctx, false)
		return ErrNotConfigured
	}
	if err := dialAuthClose(cfg); err != nil {
		s.setVerified(ctx, false)
		return err
	}
	s.setVerified(ctx, true)
	return nil
}

func (s *Service) setVerified(ctx context.Context, v bool) {
	s.mu.Lock()
	s.verified = v
	s.mu.Unlock()
	if v {
		_ = s.store.UpsertSetting(ctx, KeyVerifiedAt, time.Now().UTC().Format(time.RFC3339))
	} else {
		_ = s.store.UpsertSetting(ctx, KeyVerifiedAt, "")
	}
}

// Send delivers a plain-text UTF-8 email. Returns ErrNotVerified when the
// config hasn't passed verification (callers fall back to on-screen link).
func (s *Service) Send(ctx context.Context, to, subject, body string) error {
	if !s.Verified() {
		return ErrNotVerified
	}
	cfg := s.Load(ctx)
	if !cfg.configured() {
		return ErrNotConfigured
	}
	return sendMail(cfg, to, buildMessage(cfg.From, to, subject, body))
}

// dialTimeout bounds the TCP+TLS connect; connDeadline bounds the whole SMTP
// transaction (EHLO/AUTH/MAIL/RCPT/DATA/QUIT). Without these net/smtp blocks on
// the OS TCP timeout (minutes) if the server is briefly unreachable — which
// showed up as the "Test" button spinning forever and share-mail hanging.
const (
	dialTimeout  = 10 * time.Second
	connDeadline = 30 * time.Second
)

// smtpClient opens a client honoring the TLS mode: implicit TLS ("tls",
// port 465), STARTTLS ("starttls"/default, 587), or plaintext ("none"). Both
// the connect and the whole transaction are time-bounded so a call can never
// hang indefinitely.
func smtpClient(cfg Config) (*smtp.Client, error) {
	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	dialer := &net.Dialer{Timeout: dialTimeout}
	var conn net.Conn
	var err error
	if cfg.TLS == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: cfg.Host})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(connDeadline))
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if cfg.TLS != "tls" && (cfg.TLS == "starttls" || cfg.TLS == "") {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
				_ = c.Close()
				return nil, err
			}
		} else if cfg.TLS == "starttls" {
			_ = c.Close()
			return nil, errors.New("server does not advertise STARTTLS")
		}
	}
	return c, nil
}

func dialAuthClose(cfg Config) error {
	c, err := smtpClient(cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return err
		}
	}
	return c.Noop()
}

func sendMail(cfg Config, to string, msg []byte) error {
	c, err := smtpClient(cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	if cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(cfg.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(msg); err != nil {
		_ = wc.Close()
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}
