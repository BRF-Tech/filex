// Command plugin-memfs is a complete, runnable filex storage plugin in about
// a hundred lines — a storage that keeps its files in memory.
//
// It exists to be read, and to be run: filex's own tests install this binary
// through the admin API and drive a storage on it end to end, so the example
// cannot rot into something that no longer works.
//
// Build and install:
//
//	go build -o memfs ./examples/plugin-memfs
//	# Admin → Plugins → Install → upload `memfs`
//
// Then Connections → Add a storage → "In-memory (example)".
//
// A real plugin differs from this one only in what the methods do: swap the
// map for your API, your appliance, your database — the shape stays.
package main

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginsdk"
)

type file struct {
	data  []byte
	mtime time.Time
}

// memFS is one opened storage. filex creates one per storage row and hands
// it back on every call, so per-storage state (a connection, a session, a
// cache) belongs here.
type memFS struct {
	prefix string

	mu    sync.RWMutex
	files map[string]*file
}

// The three required methods ────────────────────────────────────────────────

func (m *memFS) List(_ context.Context, dir string) ([]pluginsdk.Object, error) {
	dir = clean(dir)
	m.mu.RLock()
	defer m.mu.RUnlock()

	// One entry per immediate child; a child with a slash after it is a
	// directory, which is how an object store presents a tree.
	seen := map[string]pluginsdk.Object{}
	for p, f := range m.files {
		if dir != "" && !strings.HasPrefix(p, dir+"/") {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, dir), "/")
		if rel == "" {
			continue
		}
		if i := strings.Index(rel, "/"); i >= 0 {
			name := rel[:i]
			seen[name] = pluginsdk.Object{Path: path.Join(dir, name), Name: name, Kind: pluginsdk.KindDir}
			continue
		}
		seen[rel] = pluginsdk.Object{
			Path: p, Name: rel, Size: int64(len(f.data)), Kind: pluginsdk.KindFile, Mtime: f.mtime,
		}
	}
	out := make([]pluginsdk.Object, 0, len(seen))
	for _, o := range seen {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *memFS) Stat(_ context.Context, p string) (pluginsdk.Object, error) {
	p = clean(p)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if f, ok := m.files[p]; ok {
		return pluginsdk.Object{
			Path: p, Name: path.Base(p), Size: int64(len(f.data)),
			Kind: pluginsdk.KindFile, Mtime: f.mtime,
		}, nil
	}
	// A directory exists when something lives under it (and the root always).
	if p == "" {
		return pluginsdk.Object{Path: "", Name: "/", Kind: pluginsdk.KindDir}, nil
	}
	for q := range m.files {
		if strings.HasPrefix(q, p+"/") {
			return pluginsdk.Object{Path: p, Name: path.Base(p), Kind: pluginsdk.KindDir}, nil
		}
	}
	// ⚠ Return this error, not a made-up one: filex maps it to "not found"
	// and answers a 404 instead of a 500 — the difference between a missing
	// file and a broken storage.
	return pluginsdk.Object{}, fmt.Errorf("%q: %w", p, pluginsdk.ErrNotFound)
}

func (m *memFS) Read(_ context.Context, p string) (io.ReadCloser, error) {
	p = clean(p)
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.files[p]
	if !ok {
		return nil, fmt.Errorf("%q: %w", p, pluginsdk.ErrNotFound)
	}
	return io.NopCloser(strings.NewReader(string(f.data))), nil
}

// Optional methods ──────────────────────────────────────────────────────────
//
// Each one filex sees turns into a capability. Delete this pair and the
// storage becomes read-only everywhere in the UI — no flag to remember.

func (m *memFS) Write(_ context.Context, p string, r io.Reader, _ int64) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[clean(p)] = &file{data: b, mtime: time.Now().UTC()}
	return nil
}

func (m *memFS) Delete(_ context.Context, p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, clean(p))
	return nil
}

func (m *memFS) SetMtime(_ context.Context, p string, t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.files[clean(p)]; ok {
		f.mtime = t
	}
	return nil
}

func clean(p string) string { return strings.Trim(strings.TrimSpace(p), "/") }

func main() {
	pluginsdk.Serve(pluginsdk.Plugin[*memFS]{
		Name:    "memfs",
		Version: "1.0.0",
		Label:   "In-memory (example)",
		Fields: []pluginsdk.Field{
			{
				Key: "prefix", Type: pluginsdk.FieldString,
				Label: "Prefix", Help: "Names this in-memory area. Anything non-empty will do.",
				Required: true,
				// ⚠ Exactly one field must be Root: it is what scopes the
				// storage inside the backend, and filex refuses to mount a
				// backend's root. Without it every storage on this plugin is
				// rejected with ROOT_PATH_FORBIDDEN.
				Root: true, Placeholder: "demo",
			},
		},
		// SelfTest hands filex a throwaway area to probe. Without it the
		// plugin is registered UNVERIFIED and the checks fall on somebody's
		// first real storage instead.
		SelfTest: func(_ context.Context) (*memFS, error) {
			return &memFS{prefix: "selftest", files: map[string]*file{}}, nil
		},
		Open: func(_ context.Context, cfg map[string]any) (*memFS, error) {
			prefix := pluginsdk.String(cfg, "prefix")
			if prefix == "" {
				return nil, fmt.Errorf("%w: prefix is required", pluginsdk.ErrUnsupported)
			}
			return &memFS{prefix: prefix, files: map[string]*file{}}, nil
		},
	})
}
