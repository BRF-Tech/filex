package config

import (
	"fmt"
	"net/url"
	"strings"
)

// normalizeAppUIOrigin reads FILEX_APP_UI_ORIGIN: "" stays "" (interfaces on
// filex's own origin, opaque by sandbox); otherwise a scheme and a host with
// an optional port — lower-cased, no trailing slash — that is not the public
// URL's origin (the point of it is to be somewhere else).
func normalizeAppUIOrigin(raw, publicURL string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(u.Host, "*'\" ;,") {
		return "", fmt.Errorf("app_ui_origin (FILEX_APP_UI_ORIGIN) %q is not an origin: write a scheme and a host, e.g. https://apps.files-usercontent.example", v)
	}
	origin := strings.ToLower(u.Scheme + "://" + u.Host)
	if p, err := url.Parse(strings.TrimSpace(publicURL)); err == nil && p.Host != "" && strings.EqualFold(p.Scheme+"://"+p.Host, origin) {
		return "", fmt.Errorf("app_ui_origin (FILEX_APP_UI_ORIGIN) %q is filex's own origin: name a different host (leave it empty to serve interfaces from filex's own origin, opaque by sandbox)", v)
	}
	return origin, nil
}
