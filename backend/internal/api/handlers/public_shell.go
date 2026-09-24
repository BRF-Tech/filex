// Package handlers — public_shell.go
//
// WHICH ANSWER A PUBLIC LINK GIVES.
//
// /s/{token} and /d/{token} have three possible readers and must keep serving
// all three:
//
//  1. a JavaScript browser — gets the SPA shell (the branded public surface:
//     PIN gate, expiry, visit counter, the app page when the link carries one);
//  2. a browser with no JavaScript, or one the shell could not load in — gets
//     the Go pages that have always been here: the file, a PIN form, an
//     explanation. Reduced to that, and kept, because a share link is opened
//     by strangers on whatever browser they have;
//  3. something that is not a browser at all — `curl -O`, wget, a download
//     manager, a backup script — gets the BYTES, exactly as before.
//
// The rules below are the whole decision. They are written as a function
// rather than as a middleware so both handlers make the same call in one
// place, and so a test can ask the question without a router.
package handlers

import (
	"net/http"
	"strings"
)

// publicShellQueryEscapes are the query parameters that mean "I am not asking
// for a page, I am asking for the thing" — every one of them predates the
// shell and every one of them must keep working unchanged.
var publicShellQueryEscapes = []string{
	"nojs",      // the shell's own <noscript> sends the browser here
	"zip",       // a folder share's "download all"
	"confirmed", // the PIN-accepted page auto-posting the real download
	"pin",       // a PIN supplied in the address (programmatic access)
	"inline",    // "render it, do not attach it"
	"download",  // the shell's own download button
	"thumb",     // the folder page's tiles
}

// wantsPublicShell reports whether this request should be answered with the
// SPA document rather than by the Go handler.
//
// ⚠ The Accept test is the one that keeps `curl -O https://host/s/<token>`
// downloading a file instead of collecting an HTML page: curl sends `*/*` (or
// nothing), a browser navigating sends `text/html` first. It is also why the
// existing tests, which build requests with no Accept header at all, keep
// measuring the Go pages.
//
// ⚠ GET only. The no-JS PIN form and the no-JS upload form POST to the same
// address, and handing those a document would break the only path a
// JavaScript-less visitor has.
func wantsPublicShell(r *http.Request, shell http.Handler) bool {
	if shell == nil || r == nil || r.Method != http.MethodGet {
		return false
	}
	if r.Header.Get("X-Filex-Pin") != "" {
		return false
	}
	q := r.URL.Query()
	for _, k := range publicShellQueryEscapes {
		if q.Has(k) {
			return false
		}
	}
	return acceptsHTML(r.Header.Get("Accept"))
}

// acceptsHTML reports whether the Accept header asks for a document.
//
// ⚠ `*/*` alone is NOT a document request. It is what curl, wget and most
// scripted clients send, and treating it as "browser" is how a download
// endpoint starts answering HTML to everything that automated it.
func acceptsHTML(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		media := strings.TrimSpace(part)
		if i := strings.IndexByte(media, ';'); i >= 0 {
			media = strings.TrimSpace(media[:i])
		}
		switch strings.ToLower(media) {
		case "text/html", "application/xhtml+xml":
			return true
		}
	}
	return false
}

// RetiredPagePrefix answers the retired /p/<token> address.
//
// An app plugin's public page is a share now, so its link is /s/<token>. The
// old prefix redirects there permanently: a link already sitting in somebody's
// inbox then lands on a page that can explain itself (and, for the pages that
// existed before the change, say honestly that they are gone) instead of on a
// 404 with no context.
//
// ⚠ 301 and not a rewrite. The address changed for good, and a redirect is
// what makes a browser, a mail client and a crawler all agree on that.
func RetiredPagePrefix() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/s/"+retiredToken(r.URL.Path), http.StatusMovedPermanently)
	})
}

// RetiredPageAPI answers the retired /api/p/<token>/… addresses. The JSON
// surface moved to /api/public/s/<token>, so the redirect points there.
func RetiredPageAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/p/")
		token, tail, _ := strings.Cut(rest, "/")
		target := "/api/public/s/" + token
		// `/view` was the opening surface; it is an ordinary event now.
		if tail != "" && tail != "view" {
			target += "/" + tail
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

// retiredToken pulls the token out of a /p/<token>[/...] path.
func retiredToken(p string) string {
	rest := strings.TrimPrefix(p, "/p/")
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}
