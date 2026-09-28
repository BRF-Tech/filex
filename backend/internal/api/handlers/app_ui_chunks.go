// Package handlers — app_ui_chunks.go
//
// An app's interface saving a LARGE file: the explorer streams what the
// interface hands it (a ReadableStream, transferred into the explorer's page)
// in chunks of at most 8 MiB, each one request — a reverse proxy with a body
// limit (nginx's default is 1 MiB) lets every one of them through — into a
// spool file, and the file is written to the storage ONCE, when the last
// chunk arrives, through the same checks and the same commit as a one-shot
// save (app_ui.go):
//
//	PUT …/save?path=<q>&chunk=start            (body: first chunk)  → {session, received}
//	PUT …/save?session=<id>&offset=<n>          (body: next chunk)   → {session, received}
//	PUT …/save?session=<id>&offset=<n>&final=1  (body: last chunk)   → the save's answer
//
// A session belongs to the person, the app, the view and the target it was
// started for; another caller naming it gets 404. An abandoned one is removed
// after uiChunkTTL.
package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

const (
	// uiChunkMax is one chunk's ceiling — the client's chunk size.
	uiChunkMax = 8 << 20
	// uiChunkTTL is how long a session may wait for its next chunk.
	uiChunkTTL = 30 * time.Minute
)

type uiChunkSession struct {
	// mu is held while one chunk is written: chunks of one save go in
	// order, chunks of different saves side by side.
	mu       sync.Mutex
	user     int64
	plugin   string
	view     string
	target   string // "path:<q>" or "new:<dir>\x00<name>"
	file     *os.File
	received int64
	touched  time.Time
}

var uiChunks = struct {
	sync.Mutex
	m map[string]*uiChunkSession
}{m: map[string]*uiChunkSession{}}

// sweepUIChunks drops the sessions nobody finished. Called on every chunk,
// under the lock.
func sweepUIChunks(now time.Time) {
	for id, s := range uiChunks.m {
		if now.Sub(s.touched) > uiChunkTTL {
			_ = s.file.Close()
			_ = os.Remove(s.file.Name())
			delete(uiChunks.m, id)
		}
	}
}

func uiChunkTarget(r *http.Request) (string, bool) {
	q := r.URL.Query()
	if p := strings.TrimSpace(q.Get("path")); p != "" {
		return "path:" + p, true
	}
	dir, name := strings.TrimSpace(q.Get("dir")), strings.TrimSpace(q.Get("name"))
	if dir != "" && name != "" {
		return "new:" + dir + "\x00" + name, true
	}
	return "", false
}

func (h *AppPlugins) uiSaveChunk(w http.ResponseWriter, r *http.Request, p *wasmplugin.Installed, v *wire.View, uid, limit int64) {
	q := r.URL.Query()
	target, ok := uiChunkTarget(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad name", "message": "give path, or dir and a file name"})
		return
	}
	now := time.Now()
	uiChunks.Lock()
	sweepUIChunks(now)
	var s *uiChunkSession
	id := q.Get("session")
	if q.Get("chunk") == "start" {
		f, err := os.CreateTemp("", "filex-ui-save-*")
		if err != nil {
			uiChunks.Unlock()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "spool unavailable"})
			return
		}
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		id = hex.EncodeToString(b)
		s = &uiChunkSession{user: uid, plugin: p.Row.Name, view: v.ID, target: target, file: f, touched: now}
		uiChunks.m[id] = s
	} else {
		s = uiChunks.m[id]
		// Another person's session, another app's, another file: it does
		// not exist for this caller.
		if s == nil || s.user != uid || s.plugin != p.Row.Name || s.view != v.ID || s.target != target {
			uiChunks.Unlock()
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "no such save in progress"})
			return
		}
		s.touched = now
	}
	uiChunks.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if q.Get("chunk") != "start" {
		if off, err := strconv.ParseInt(q.Get("offset"), 10, 64); err != nil || off != s.received {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "offset", "message": "the save continues from " + strconv.FormatInt(s.received, 10), "received": s.received})
			return
		}
	}
	n, err := io.Copy(s.file, http.MaxBytesReader(w, r.Body, uiChunkMax))
	s.received += n
	drop := func() {
		_ = s.file.Close()
		_ = os.Remove(s.file.Name())
		uiChunks.Lock()
		delete(uiChunks.m, id)
		uiChunks.Unlock()
	}
	if err != nil {
		drop()
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large", "message": "a chunk is at most 8 MiB"})
		return
	}
	if s.received > limit {
		drop()
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large"})
		return
	}
	if q.Get("final") != "1" {
		writeJSON(w, http.StatusAccepted, map[string]any{"session": id, "received": s.received})
		return
	}
	defer drop()
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "spool unreadable"})
		return
	}
	size := s.received
	if rest, isPath := strings.CutPrefix(s.target, "path:"); isPath {
		h.uiSaveOver(w, r, p, v, rest, s.file, size, uid)
		return
	}
	dir, name, _ := strings.Cut(strings.TrimPrefix(s.target, "new:"), "\x00")
	h.uiSaveNew(w, r, p, v, dir, name, s.file, size, uid)
}
