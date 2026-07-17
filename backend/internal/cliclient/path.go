// Package cliclient implements the HTTP client behind `filex client` —
// the subcommand family that talks to a REMOTE filex server over its
// public REST API. It deliberately reuses only the wire contracts (no
// server-side imports) so the same binary can act as a pure API consumer
// from any machine.
package cliclient

import (
	"fmt"
	"path"
	"strings"
)

// RemotePath is a parsed `adapter://relative/path` reference. Adapter is
// the storage name on the server; Rel is the slash-separated relative
// path inside it ("" = storage root).
type RemotePath struct {
	Adapter string
	Rel     string
}

// ParseRemotePath validates and splits an `adapter://rel/path` argument.
// The adapter prefix is mandatory — a bare relative path is ambiguous
// when the server hosts several storages. `..` segments are rejected
// before they ever reach the wire.
func ParseRemotePath(raw string) (RemotePath, error) {
	idx := strings.Index(raw, "://")
	if idx <= 0 {
		return RemotePath{}, fmt.Errorf("bad remote path %q: want adapter://path", raw)
	}
	adapter := raw[:idx]
	rel := strings.Trim(raw[idx+3:], "/")
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return RemotePath{}, fmt.Errorf("bad remote path %q: parent traversal not allowed", raw)
		}
	}
	if rel != "" {
		rel = strings.Trim(path.Clean(rel), "/")
		if rel == "." {
			rel = ""
		}
	}
	return RemotePath{Adapter: adapter, Rel: rel}, nil
}

// String renders the wire form `adapter://rel` (root: `adapter://`).
func (p RemotePath) String() string {
	if p.Rel == "" {
		return p.Adapter + "://"
	}
	return p.Adapter + "://" + p.Rel
}

// Base returns the last path segment ("" at the storage root).
func (p RemotePath) Base() string {
	if p.Rel == "" {
		return ""
	}
	return path.Base(p.Rel)
}

// Dir returns the parent directory (the storage root stays the root).
func (p RemotePath) Dir() RemotePath {
	d := path.Dir(p.Rel)
	if d == "." || d == "/" {
		d = ""
	}
	return RemotePath{Adapter: p.Adapter, Rel: d}
}

// Join appends one segment to the path.
func (p RemotePath) Join(name string) RemotePath {
	return RemotePath{Adapter: p.Adapter, Rel: strings.Trim(path.Join(p.Rel, name), "/")}
}

// IsRoot reports whether the path points at the storage root.
func (p RemotePath) IsRoot() bool { return p.Rel == "" }
