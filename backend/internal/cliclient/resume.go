package cliclient

// Resume bookmarks for interrupted uploads.
//
// A dropped connection used to cost the whole file: `uploadFile` streamed one
// multipart request and had nowhere to write down how far it got. `filex sync`
// runs on the same code, so a laptop that closed its lid halfway through a 4 GB
// file started that file again from byte 0 on the next run.
//
// The bookmark lives on disk, not in memory, because the interesting failure is
// not a retry inside one run — it is the process going away. `filex sync run
// --watch` restarting, the desktop app relaunching, a reboot: all of those must
// pick up where the bytes stopped.
//
// ⚠ The bookmark is a HINT, never an authority. `offset` here is only used to
// decide whether a session is worth asking about; the byte to continue from is
// always the server's answer to GET /api/files/upload/{id}. A stale local
// number would splice a hole into the file.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// resumeRecord is one interrupted upload, keyed by (server, destination, local
// file). Size and ModTimeNS pin it to the exact bytes it was opened for: if the
// local file changed, resuming would append the tail of a different file to the
// head of the old one, which no checksum after the fact could undo.
type resumeRecord struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Remote    string    `json:"remote"`
	Local     string    `json:"local"`
	Size      int64     `json:"size"`
	ModTimeNS int64     `json:"mtime_ns"`
	ChunkSize int64     `json:"chunk_size"`
	Offset    int64     `json:"offset"`
	Hash      string    `json:"hash,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DefaultResumeDir is where upload bookmarks live: $FILEX_UPLOAD_STATE when
// set (tests, unusual homes), otherwise ~/.filex/uploads.
func DefaultResumeDir() (string, error) {
	if p := os.Getenv("FILEX_UPLOAD_STATE"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".filex", "uploads"), nil
}

// resumeKey identifies one (server, destination, local file) triple. Hashed
// rather than encoded so the filename is fixed-length and free of separators —
// a remote path contains `/` and `:`.
func resumeKey(baseURL, remote, local string) string {
	abs, err := filepath.Abs(local)
	if err != nil {
		abs = local
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{baseURL, remote, filepath.ToSlash(abs)}, "\n")))
	return hex.EncodeToString(sum[:16])
}

// resumePath is the bookmark file for one key, or "" when no state directory
// is configured (an embedder that does not want files on disk).
func (c *Client) resumePath(key string) string {
	if c.ResumeDir == "" {
		return ""
	}
	return filepath.Join(c.ResumeDir, key+".json")
}

// loadResume reads the bookmark for this upload. It returns nil — not an
// error — for anything that makes the bookmark unusable: no file, unreadable,
// corrupt, or describing a file whose size/mtime no longer match. A caller that
// gets nil simply begins a fresh upload, which is always correct.
func (c *Client) loadResume(key string, size int64, mod time.Time) *resumeRecord {
	p := c.resumePath(key)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			// A bookmark we cannot read is debris, not a failure to report:
			// the upload still works, it just starts over.
			_ = os.Remove(p)
		}
		return nil
	}
	var rec resumeRecord
	if err := json.Unmarshal(b, &rec); err != nil || rec.ID == "" {
		_ = os.Remove(p)
		return nil
	}
	if rec.Size != size || rec.ModTimeNS != mod.UnixNano() {
		// The local file changed under us. Resuming here is the one way this
		// design can corrupt a file, so the bookmark goes.
		_ = os.Remove(p)
		return nil
	}
	return &rec
}

// saveResume writes the bookmark atomically (temp + rename) and owner-only:
// it names a server, a path and an upload id, and a torn write would be read
// back as corrupt on the very next run.
func (c *Client) saveResume(key string, rec *resumeRecord) {
	p := c.resumePath(key)
	if p == "" {
		return
	}
	rec.UpdatedAt = time.Now().UTC()
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".resume-*.tmp")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Chmod(name, 0o600)
	_ = os.Rename(name, p)
}

// clearResume drops the bookmark once the upload is finished (or provably
// dead). Missing is success.
func (c *Client) clearResume(key string) {
	if p := c.resumePath(key); p != "" {
		_ = os.Remove(p)
	}
}
