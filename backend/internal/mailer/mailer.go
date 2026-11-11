// Package mailer sends transactional email (share/invite notices) over an
// operator-configured SMTP server. What a mail says is internal/templates'
// (the HTML and plain-text parts an administrator can edit); this package
// only puts the parts into one message and delivers it. The config lives in the settings table
// (smtp.*) and is validated by a periodic job (server.Start runs Verify every
// 5 minutes). Send is a no-op error (ErrNotVerified) until a verification
// succeeds, so the invite flow can fall back to showing the link on-screen.
package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
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

// Ready reports whether Send can deliver at all right now: SMTP is configured
// AND its last verification succeeded — exactly the two preconditions Send
// checks. The capabilities answer publishes it (as a boolean, no host) so the
// explorer does not offer a "Send by email" that is certain to fail: an
// administrator sees it greyed with where to set mail up, everybody else is
// not offered it (owner, 2026-09-21).
func (s *Service) Ready(ctx context.Context) bool {
	if s == nil || !s.Verified() {
		return false
	}
	return s.Load(ctx).configured()
}

// Message is one mail's content: its subject, its plain-text part and, when
// it has one, its HTML part (internal/templates renders all three).
type Message struct {
	Subject string
	Text    string
	HTML    string
}

// Send delivers a plain-text UTF-8 email. Returns ErrNotVerified when the
// config hasn't passed verification (callers fall back to on-screen link).
func (s *Service) Send(ctx context.Context, to, subject, body string) error {
	return s.SendMessage(ctx, to, Message{Subject: subject, Text: body})
}

// SendMessage delivers m: multipart/alternative (the plain text, then the
// HTML - a program shows the last part it can) when m has HTML, plain text
// otherwise. Errors as Send.
func (s *Service) SendMessage(ctx context.Context, to string, m Message) error {
	if !s.Verified() {
		return ErrNotVerified
	}
	cfg := s.Load(ctx)
	if !cfg.configured() {
		return ErrNotConfigured
	}
	return sendMail(cfg, to, composeMessage(cfg.From, to, m, languageFrom(ctx), time.Now()))
}

type langKey struct{}

// WithLanguage tags the mail sent under ctx with the language it is written
// in (a BCP 47 tag, as internal/srvtext picked it). Send writes it as the
// message's Content-Language, which mail clients use for spell-checking,
// "translate this message" offers and screen-reader voices.
//
// ⚠ A context value rather than a Send parameter: Send has callers in five
// packages and fakes in their tests, and the language is a label ON the
// message, not an input to delivery — an untagged send is still correct.
//
// ⚠ Right-to-left languages need nothing more here: filex sends text/plain
// only, and a plain-text body carries no direction — clients lay each
// paragraph out by its own letters (the Unicode bidirectional algorithm). A
// `dir="rtl"` belongs on an HTML body, and there is none; if an HTML part is
// ever added, it must carry dir from srvtext.IsRTL(lang).
func WithLanguage(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, langKey{}, lang)
}

func languageFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(langKey{}).(string)
	return v
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
	c, err := openAuthed(cfg, smtpClient)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Noop()
}

func sendMail(cfg Config, to string, msg []byte) error {
	c, err := openAuthed(cfg, smtpClient)
	if err != nil {
		return err
	}
	defer c.Close()
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

func buildMessage(from, to, subject, body, lang string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + headerText(subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	if l := headerLang(lang); l != "" {
		b.WriteString("Content-Language: " + l + "\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

// composeMessage is the message Send and SendMessage deliver: the headers of
// buildMessage plus Date and Message-ID (a message without them is what spam
// filters score), and the body as one quoted-printable part or two.
//
// ⚠ Quoted-printable, not 8bit: an HTML part is a few long lines, and SMTP
// refuses (or a relay folds, breaking a tag) a line over 998 bytes. QP keeps
// every line short and every byte the template wrote.
//
// ⚠ A right-to-left mail needs nothing here: its HTML carries dir from the
// template (internal/templates, `dir`), and a plain-text part has no
// direction to carry (each paragraph is laid out by its own letters).
func composeMessage(from, to string, m Message, lang string, now time.Time) []byte {
	var b bytes.Buffer
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + headerText(m.Subject) + "\r\n")
	b.WriteString("Date: " + now.UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + messageID(from) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	if l := headerLang(lang); l != "" {
		b.WriteString("Content-Language: " + l + "\r\n")
	}
	text := crlf(m.Text)
	if strings.TrimSpace(m.HTML) == "" {
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		writeQP(&b, text)
		return b.Bytes()
	}
	boundary := "filex-" + randomHex(12)
	b.WriteString(`Content-Type: multipart/alternative; boundary="` + boundary + `"` + "\r\n\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	writeQP(&b, text)
	b.WriteString("\r\n--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	writeQP(&b, crlf(m.HTML))
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return b.Bytes()
}

// crlf is s with every line ending a CR LF, as a message body has them.
func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

func writeQP(b *bytes.Buffer, s string) {
	w := quotedprintable.NewWriter(b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return strings.Repeat("0", 2*n)
	}
	return hex.EncodeToString(buf)
}

// messageID is a Message-ID at the sender's domain (the From address's).
func messageID(from string) string {
	domain := "filex.local"
	if a, err := mail.ParseAddress(from); err == nil {
		if i := strings.LastIndexByte(a.Address, '@'); i >= 0 && i < len(a.Address)-1 {
			domain = a.Address[i+1:]
		}
	}
	return "<" + randomHex(16) + "@" + strings.Trim(domain, "<>") + ">"
}

// headerText makes a subject safe to be a header value.
//
// ⚠⚠ Two things, both newly reachable now that a language pack writes
// subjects: (1) a line break ends the header — a translation carrying CR LF
// would append headers of its own choosing (a Bcc:) to every mail of that
// kind — so CR and LF become spaces; (2) a header is ASCII (RFC 5322), and a
// subject in Turkish, Spanish or Arabic is not, so it goes out as RFC 2047
// encoded-words. Before, raw UTF-8 went out and depended on the relay
// tolerating it.
func headerText(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\r' || r == '\n' }), " ")
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return mime.BEncoding.Encode("utf-8", s)
		}
	}
	return s
}

// headerLang is a language tag fit for Content-Language, or "" — the tag
// comes from an account setting or a request, so anything that is not
// letters, digits and dashes is left out rather than written into a header.
func headerLang(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" || len(tag) > 35 {
		return ""
	}
	for _, r := range tag {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return ""
		}
	}
	return tag
}
