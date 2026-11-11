// Package shortlink is filex's side of an app's public route (a link
// shortener's `go.example.com/<code>`): the host split that keeps a route's
// host of its own to that route, the per-client miss limiter, the
// link-preview filter, and the live snapshot the certificate questions read.
//
// The app keeps its data and answers each visit; filex holds the invariants
// around it, whichever app it is (docs/APP-PLUGINS-API.md → Public routes).
package shortlink
