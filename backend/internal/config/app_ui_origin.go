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
	origin, ok := bareOrigin(v)
	if !ok {
		return "", fmt.Errorf("app_ui_origin (FILEX_APP_UI_ORIGIN) %q is not an origin: write a scheme and a host, e.g. https://apps.files-usercontent.example", v)
	}
	if isPublicOrigin(publicURL, origin) {
		return "", fmt.Errorf("app_ui_origin (FILEX_APP_UI_ORIGIN) %q is filex's own origin: name a different host (leave it empty to serve interfaces from filex's own origin, opaque by sandbox)", v)
	}
	return origin, nil
}

// bareOrigin reads a configured origin, the one shape every *_ORIGIN setting
// takes: an https or http scheme and a host with an optional port, and
// nothing else (no user, path, query or fragment, no wildcard or list).
// It comes back lower-cased with no trailing slash; ok is false for anything
// else. Shared by FILEX_APP_UI_ORIGIN and FILEX_ONLYOFFICE_FRAME_ORIGIN, so
// the two cannot come to disagree about what an origin is.
func bareOrigin(v string) (origin string, ok bool) {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(u.Host, "*'\" ;,") {
		return "", false
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), true
}

// isPublicOrigin reports whether origin is the public URL's own origin.
func isPublicOrigin(publicURL, origin string) bool {
	p, err := url.Parse(strings.TrimSpace(publicURL))
	return err == nil && p.Host != "" && strings.EqualFold(p.Scheme+"://"+p.Host, origin)
}
