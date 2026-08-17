package cliclient

// The resumable upload path, shared by `filex upload`, `filex upload -r` and
// `filex sync` (and therefore by the desktop app, which drives sync by running
// this same binary).
//
// Protocol: docs/UPLOADS.md.
//
//	POST   /api/files/upload/begin      → {id, chunk_size, offset}
//	PUT    /api/files/upload/{id}       Content-Range: bytes A-B/total
//	GET    /api/files/upload/{id}       → {offset, state, …}   ← the resume oracle
//	POST   /api/files/upload/{id}/commit→ {op_id, node_id}
//
// Three rules the implementation follows and the tests pin:
//
//  1. **The server's offset is the only resume point.** The local bookmark says
//     which session to ask about; it never says where to continue. A client that
//     trusted its own last-written number would re-send bytes the server holds
//     (harmless) or skip bytes it does not (a silently corrupt file).
//  2. **The bookmark is written before the first chunk**, not after. The window
//     this closes is a crash between `begin` and the first PUT — small, and
//     exactly the window a flaky link keeps landing in.
//  3. **A body is a *os.File section**, never a wrapped reader. io.NewSectionReader
//     keeps the request body seekable and exactly as long as the Content-Range
//     claims; net/http can then retry it, and a short body is impossible rather
//     than merely unlikely.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultStagedThreshold is the size at which the CLI switches from the
// one-shot multipart POST to the resumable staged protocol. It matches the
// server's default chunk size, so "large" means the same thing on both ends.
const DefaultStagedThreshold = 8 << 20

// maxChunkAttempts is how many times one chunk is retried before the upload
// gives up for this run. Giving up is not losing the work: the bookmark stays
// on disk and the next run continues from the server's offset.
const maxChunkAttempts = 5

// errStagedUnsupported means the server has no staged upload path at all — an
// older filex, or one with no staging directory configured. The caller falls
// back to the plain multipart POST; nothing about the local file is lost.
var errStagedUnsupported = errors.New("server does not support staged uploads")

// stagedThreshold resolves the configured cutover, defaulting when unset.
// A negative value disables the staged path entirely (embedders / tests).
func (c *Client) stagedThreshold() int64 {
	if c.StagedThreshold == 0 {
		return DefaultStagedThreshold
	}
	return c.StagedThreshold
}

// beginResponse is POST /api/files/upload/begin.
type beginResponse struct {
	ID        string `json:"id"`
	ChunkSize int64  `json:"chunk_size"`
	Offset    int64  `json:"offset"`
	TotalSize int64  `json:"total_size"`
	State     string `json:"state"`
}

// statusResponse is GET /api/files/upload/{id} — the resume oracle.
type statusResponse struct {
	ID        string `json:"id"`
	Offset    int64  `json:"offset"`
	Received  int64  `json:"received"`
	TotalSize int64  `json:"total_size"`
	ChunkSize int64  `json:"chunk_size"`
	State     string `json:"state"`
	Complete  bool   `json:"complete"`
	Error     string `json:"error,omitempty"`
	OpID      *int64 `json:"op_id,omitempty"`
	NodeID    *int64 `json:"node_id,omitempty"`
}

// commitResponse is POST /api/files/upload/{id}/commit.
type commitResponse struct {
	OpID          int64  `json:"op_id"`
	NodeID        int64  `json:"node_id"`
	Path          string `json:"path"`
	TransferState string `json:"transfer_state"`
}

// opStatus is the slice of GET /api/files/ops/{id} the CLI waits on.
type opStatus struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// uploadStaged pushes localPath into destDir/name over the resumable protocol,
// continuing an interrupted session when one exists. It returns the raw commit
// response so `--json` keeps printing a server payload.
func (c *Client) uploadStaged(ctx context.Context, destDir RemotePath, name, localPath string, size int64, mod time.Time) ([]byte, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	remote := destDir.Join(name).String()
	key := resumeKey(c.BaseURL, remote, localPath)

	// The digest is declared up front so the SERVER verifies the assembled
	// bytes at commit. That is the check a resume actually needs: parts written
	// across two processes, days apart, are only provably the same file if
	// something compares the whole of it against a value fixed before the first
	// chunk went out.
	sum, err := fileSHA256(f)
	if err != nil {
		return nil, err
	}
	declared := "sha256:" + sum

	id, chunk, offset, err := c.resumeOrBegin(ctx, key, destDir, name, remote, localPath, size, mod, declared)
	if err != nil {
		return nil, err
	}

	rec := &resumeRecord{
		ID: id, URL: c.BaseURL, Remote: remote, Local: localPath,
		Size: size, ModTimeNS: mod.UnixNano(), ChunkSize: chunk, Offset: offset,
		Hash: declared,
	}

	for offset < size {
		n := chunk
		if rem := size - offset; rem < n {
			n = rem
		}
		next, perr := c.putChunkWithRetry(ctx, id, f, offset, n, size)
		if perr != nil {
			// The bookmark stays: the bytes already staged are the server's,
			// and the next run asks it where to continue.
			return nil, perr
		}
		if next <= offset {
			return nil, fmt.Errorf("upload %s stalled at offset %d", id, offset)
		}
		offset = next
		rec.Offset = offset
		c.saveResume(key, rec)
	}

	raw, err := c.commitStaged(ctx, id)
	if err != nil {
		return nil, err
	}
	var commit commitResponse
	_ = json.Unmarshal(raw, &commit)
	if commit.OpID > 0 {
		// Wait for the transfer. Every byte is already safe inside filex, so a
		// drop here costs nothing — but the CLI's exit code (and `filex sync`'s
		// notion of "uploaded") must not claim success for a transfer that
		// failed on the driver.
		if err := c.waitForTransfer(ctx, id, commit.OpID); err != nil {
			return nil, err
		}
	}
	c.clearResume(key)
	return raw, nil
}

// resumeOrBegin returns the upload id, chunk size and byte offset to continue
// from — resuming a bookmarked session when the server still has it, otherwise
// starting a new one.
func (c *Client) resumeOrBegin(
	ctx context.Context,
	key string,
	destDir RemotePath,
	name, remote, localPath string,
	size int64,
	mod time.Time,
	declared string,
) (string, int64, int64, error) {
	if rec := c.loadResume(key, size, mod); rec != nil {
		st, err := c.stagedStatus(ctx, rec.ID)
		switch {
		case err == nil && st.TotalSize == size && stagedResumable(st.State):
			chunk := st.ChunkSize
			if chunk <= 0 {
				chunk = rec.ChunkSize
			}
			return rec.ID, chunk, st.Offset, nil
		case err == nil:
			// The session exists but is not ours to continue (wrong size, or
			// already committing). Drop the bookmark and start over rather than
			// pushing bytes into a session with different semantics.
			c.clearResume(key)
		default:
			// 404 (swept, aborted, another machine), 403 (permission changed),
			// or the server is simply gone. None of them is a reason to fail
			// the upload — begin decides that.
			c.clearResume(key)
		}
	}

	body, err := json.Marshal(map[string]any{
		"path":       destDir.String(),
		"name":       name,
		"size":       size,
		"hash":       declared,
		"chunk_size": c.ChunkSize,
	})
	if err != nil {
		return "", 0, 0, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/files/upload/begin", nil, strings.NewReader(string(body)))
	if err != nil {
		return "", 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	raw, err := c.doJSON(req)
	if err != nil {
		if stagedUnsupported(err) {
			return "", 0, 0, errStagedUnsupported
		}
		return "", 0, 0, err
	}
	var begun beginResponse
	if err := json.Unmarshal(raw, &begun); err != nil {
		return "", 0, 0, fmt.Errorf("parse begin response: %w", err)
	}
	if begun.ID == "" || begun.ChunkSize <= 0 {
		return "", 0, 0, errors.New("begin response carried no upload id")
	}
	// Written BEFORE the first chunk: a crash between begin and the first PUT
	// would otherwise leave a staging directory nobody can find again.
	c.saveResume(key, &resumeRecord{
		ID: begun.ID, URL: c.BaseURL, Remote: remote, Local: localPath,
		Size: size, ModTimeNS: mod.UnixNano(), ChunkSize: begun.ChunkSize,
		Offset: begun.Offset, Hash: declared,
	})
	return begun.ID, begun.ChunkSize, begun.Offset, nil
}

// stagedResumable reports whether more chunks may be sent to a session in this
// state. Only `staging` qualifies — see model.StagedUpload* for the rest.
func stagedResumable(state string) bool { return state == "" || state == "staging" }

// stagedUnsupported distinguishes "this server has no staged path" (fall back
// to multipart) from a real refusal (report it).
func stagedUnsupported(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Status == http.StatusNotFound || ae.Status == http.StatusNotImplemented
}

// putChunkWithRetry sends one chunk and returns the server's new offset.
//
// On any failure it re-asks the server where it stands before trying again:
// a chunk can fail after the bytes landed (the response was lost), and
// re-sending blindly would be correct but slower, while assuming success would
// be wrong. Asking is neither.
func (c *Client) putChunkWithRetry(ctx context.Context, id string, f *os.File, offset, length, total int64) (int64, error) {
	var lastErr error
	for attempt := 0; attempt < maxChunkAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
			}
			st, serr := c.stagedStatus(ctx, id)
			if serr == nil {
				if st.Offset >= offset+length {
					// It landed after all — the response was what got lost.
					return st.Offset, nil
				}
				if st.Offset != offset {
					// Another writer, or a partially applied grid. Continue
					// from what the server actually has.
					offset = st.Offset
					if rem := total - offset; rem < length {
						length = rem
					}
					if length <= 0 {
						return st.Offset, nil
					}
				}
			}
		}
		next, err := c.putChunk(ctx, id, f, offset, length, total)
		if err == nil {
			return next, nil
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		lastErr = err
		var ae *APIError
		if errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500 && ae.Status != http.StatusRequestTimeout {
			// 400 SHORT_CHUNK is retryable (the connection cut mid-body) and
			// so is 409 while another chunk settles; the rest — 403, 404, 413
			// — will not become true by repeating them.
			if ae.Status != http.StatusBadRequest && ae.Status != http.StatusConflict {
				return 0, err
			}
		}
	}
	return 0, fmt.Errorf("chunk at offset %d failed after %d attempts: %w", offset, maxChunkAttempts, lastErr)
}

// putChunk sends exactly one chunk and returns the server's offset afterwards.
func (c *Client) putChunk(ctx context.Context, id string, f *os.File, offset, length, total int64) (int64, error) {
	// A section of the open file: seekable, exactly `length` long, and
	// replayable by net/http. Never io.MultiReader — that is what once cost
	// the S3 SDK its ability to measure a body (manager_mutate.go:585).
	body := io.NewSectionReader(f, offset, length)
	req, err := c.newRequest(ctx, http.MethodPut, "/api/files/upload/"+id, nil, body)
	if err != nil {
		return 0, err
	}
	req.ContentLength = length
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+length-1, total))
	raw, err := c.doJSON(req)
	if err != nil {
		return 0, err
	}
	var out statusResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, fmt.Errorf("parse chunk response: %w", err)
	}
	return out.Offset, nil
}

// stagedStatus asks the resume oracle where this upload stands.
func (c *Client) stagedStatus(ctx context.Context, id string) (*statusResponse, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/files/upload/"+id, nil, nil)
	if err != nil {
		return nil, err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	var out statusResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse upload status: %w", err)
	}
	return &out, nil
}

// commitStaged finalises the upload; the server verifies size and the declared
// digest before it accepts.
func (c *Client) commitStaged(ctx context.Context, id string) ([]byte, error) {
	req, err := c.newRequest(ctx, http.MethodPost, "/api/files/upload/"+id+"/commit", nil, nil)
	if err != nil {
		return nil, err
	}
	return c.doJSON(req)
}

// waitForTransfer polls the ops tray until the staged bytes are on the driver.
//
// A failed transfer is reported as an error, and the bookmark is deliberately
// left in place by the caller: `commit` may be called again on a failed row and
// costs no bytes.
func (c *Client) waitForTransfer(ctx context.Context, uploadID string, opID int64) error {
	delay := 100 * time.Millisecond
	for {
		req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/api/files/ops/%d", opID), nil, nil)
		if err != nil {
			return err
		}
		raw, err := c.doJSON(req)
		if err != nil {
			// The tray is not the upload. A hiccup reading it must not turn a
			// finished transfer into a failure — but neither can we claim
			// success, so keep polling until the context says stop.
			if ctx.Err() != nil {
				return ctx.Err()
			}
		} else {
			var op opStatus
			if err := json.Unmarshal(raw, &op); err == nil {
				switch op.Status {
				case "ok":
					return nil
				case "failed", "partial":
					msg := op.Error
					if msg == "" {
						msg = "transfer failed"
					}
					return fmt.Errorf("upload %s staged but not stored: %s (commit can be retried without re-uploading)", uploadID, msg)
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

// fileSHA256 digests the whole file and rewinds it, so the caller's handle is
// still positioned at byte 0.
func fileSHA256(f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
