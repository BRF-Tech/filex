package coedit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/brf-tech/filex/backend/internal/model"
)

// A session's sealed blobs are files under BlobDir/<sid>/, named by the
// blob's name (hex) and the digest of their bytes. The database row
// (coedit_blobs) is what says a blob is complete: a file without a row is
// one whose transaction lost, and goes with the session's folder.

var sidRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidSID reports whether s is a session id (32 lower-case hex digits).
// Nothing that is not one ever reaches the filesystem.
func ValidSID(s string) bool { return sidRE.MatchString(s) }

func (r *Relay) blobDir(sid string) (string, error) {
	if r.opts.BlobDir == "" || !ValidSID(sid) {
		return "", ErrNoBlob
	}
	return filepath.Join(r.opts.BlobDir, sid), nil
}

func blobFile(dir, name, sum string) string {
	return filepath.Join(dir, hex.EncodeToString([]byte(name))+"-"+sum)
}

// PutBlob stores a sealed blob of a session, once per name, and returns its
// size. The base document may be stored by any member while the session has
// none (its creator, in practice: nobody else joins before); any other blob
// only by a member that may write. The session's blobs together stay under
// Options.MaxBlobBytes.
func (r *Relay) PutBlob(ctx context.Context, sid, client, name string, body io.Reader) (int64, error) {
	if !ValidBlobName(name) {
		return 0, ErrBlobName
	}
	dir, err := r.blobDir(sid)
	if err != nil {
		return 0, err
	}
	m, err := r.member(ctx, sid, client)
	if err != nil {
		return 0, err
	}
	s, err := r.Session(ctx, sid)
	if err != nil {
		return 0, err
	}
	if name == BaseBlob {
		if s.HasBase {
			return 0, ErrBlobSet
		}
	} else if !m.CanEdit {
		return 0, ErrReadOnly
	}
	budget := r.opts.MaxBlobBytes - s.BlobBytes
	if budget <= 0 {
		return 0, ErrTooLarge
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(dir, ".put-*")
	if err != nil {
		return 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(body, budget+1))
	cerr := tmp.Close()
	if err == nil {
		err = cerr
	}
	if err == nil && n > budget {
		err = ErrTooLarge
	}
	if err == nil && n == 0 {
		err = ErrEmpty
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return 0, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	final := blobFile(dir, name, sum)
	if err := os.Rename(tmp.Name(), final); err != nil {
		_ = os.Remove(tmp.Name())
		return 0, err
	}
	err = r.change(ctx, sid, func(ctx context.Context, s *model.CoEditSession) (bool, error) {
		if s.BlobBytes+n > r.opts.MaxBlobBytes {
			return false, ErrTooLarge
		}
		ok, err := r.store.InsertCoEditBlob(ctx, &model.CoEditBlob{SID: sid, Name: name, Size: n, SHA256: sum, CreatedMs: r.nowMs()})
		if err != nil {
			return false, err
		}
		if !ok {
			return false, ErrBlobSet
		}
		s.BlobBytes += n
		if name == BaseBlob {
			s.HasBase = true
		}
		s.ActiveMs = r.nowMs()
		return true, nil
	})
	if err != nil {
		// Keep the file only when it is the one a row names (another writer
		// stored the same bytes under the same name first).
		if row, gerr := r.store.GetCoEditBlob(ctx, sid, name); gerr != nil || row == nil || row.SHA256 != sum {
			_ = os.Remove(final)
		}
		return 0, err
	}
	return n, nil
}

// Blob opens a stored sealed blob for a member, with its size.
func (r *Relay) Blob(ctx context.Context, sid, client, name string) (io.ReadCloser, int64, error) {
	if !ValidBlobName(name) {
		return nil, 0, ErrBlobName
	}
	dir, err := r.blobDir(sid)
	if err != nil {
		return nil, 0, err
	}
	if _, err := r.member(ctx, sid, client); err != nil {
		return nil, 0, err
	}
	row, err := r.store.GetCoEditBlob(ctx, sid, name)
	if err != nil {
		return nil, 0, err
	}
	if row == nil {
		return nil, 0, ErrNoBlob
	}
	f, err := os.Open(blobFile(dir, name, row.SHA256))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNoBlob
	}
	if err != nil {
		return nil, 0, err
	}
	return f, row.Size, nil
}

// dropBlobs removes a session's blob folder.
func (r *Relay) dropBlobs(sid string) {
	if dir, err := r.blobDir(sid); err == nil {
		_ = os.RemoveAll(dir)
	}
}
