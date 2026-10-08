package server

import (
	"log/slog"
	"net/url"
	"strings"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/webpush"
)

// pushConfig is Web Push as the configuration asks for it (task #191,
// internal/notify push.go), nil when it is switched off.
//
// ⚠ It is handed the box of FILEX_SECRET_KEY whatever is set: with no key the
// box seals nothing, push says `no_secret_key` in the settings pane and on the
// admin page, and a key added later switches it on at the next start - no
// second switch to remember.
func pushConfig(cfg config.Config) *notify.PushConfig {
	pc := cfg.Notify.Push
	if !pc.Enabled {
		return nil
	}
	box, err := secretbox.New(cfg.SecretKey)
	if err != nil {
		slog.Warn("push notifications off: the secret key is unusable", slog.String("err", err.Error()))
		return nil
	}
	return &notify.PushConfig{
		Box:       box,
		Subject:   pushSubject(pc.Subject, cfg.PublicURL),
		Endpoints: pushPolicy(pc.Hosts),
	}
}

// pushSubject is the VAPID contact: the configured one, else the public
// address when it is https, else a mailto: at its host. A push service may
// write to it about this server; Apple accepts only mailto: and https:.
func pushSubject(configured, publicURL string) string {
	if s := strings.TrimSpace(configured); s != "" {
		return s
	}
	u, err := url.Parse(strings.TrimSpace(publicURL))
	if err == nil && u.Scheme == "https" && u.Host != "" {
		return "https://" + u.Host
	}
	host := "localhost"
	if err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return "mailto:filex@" + host
}

// pushPolicy is which push services a device may name: the browsers' own,
// the ones FILEX_PUSH_HOSTS adds, or any name for `*`.
func pushPolicy(hosts string) webpush.Policy {
	p := webpush.Policy{Hosts: append([]string(nil), webpush.DefaultHosts...)}
	for _, h := range strings.FieldsFunc(hosts, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if h == "*" {
			p.AnyHost = true
			continue
		}
		p.Hosts = append(p.Hosts, strings.ToLower(strings.TrimSpace(h)))
	}
	return p
}
