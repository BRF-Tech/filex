// Package embedded re-exports the bundled web/dist + admin assets via
// embed.FS so that other packages can mount them without colliding with
// //go:embed's "no parent paths" rule.
//
// The build pipeline copies the frontend output (web/dist) into this
// directory before `go build`. The two subdirectories below MUST exist at
// build time, even if empty — local dev keeps a `.keep` placeholder in each
// so `go build` doesn't error out before the frontend has been built.
package embedded

import "embed"

// FS contains every file under embed/admin (the Vue 3 admin SPA) and
// embed/web (the Web Component bundle that ships at /embed.js).
//
// `all:` lets //go:embed include hidden files (e.g. `.keep` placeholders).
//
//go:embed all:admin all:web
var FS embed.FS
