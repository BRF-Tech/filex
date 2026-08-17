package handlers

import (
	"context"
	"errors"
	"io"
	"io/fs"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// rangeSource is the ranged-read shape the seeker needs, already bound to
// one file. *filebody.Source implements it for BOTH backings — the storage
// driver and filex's staging area — which is why a file that is still
// being transferred seeks and resumes exactly like a stored one.
type rangeSource interface {
	ReadRange(ctx context.Context, off, length int64) (io.ReadCloser, error)
}

// rangeSeeker is a lazy io.ReadSeeker over a rangeSource, so a file
// body can be served through http.ServeContent (206, Content-Range,
// If-Range, 416) without the backend ever sending a byte the client did
// not ask for.
//
// ⚠ The whole point is that Seek is free. http.ServeContent measures the
// body by seeking to the end and back before it serves anything; a seeker
// that opened a stream to answer that would fetch the entire object on
// every request — slower than the io.Copy path it replaces. So Seek only
// moves a counter, and a stream is opened on the first Read that follows,
// starting AT the requested offset. Nothing is ever re-read from 0 to
// reach a seek target.
type rangeSeeker struct {
	ctx  context.Context
	src  rangeSource
	size int64

	rc   io.ReadCloser
	pos  int64 // logical read position (what Seek moves)
	rpos int64 // position of rc's next byte; rc is stale when pos != rpos
}

// newRangeSeeker builds a seeker positioned at the start of the object.
func newRangeSeeker(ctx context.Context, src rangeSource, size int64) *rangeSeeker {
	return &rangeSeeker{ctx: ctx, src: src, size: size}
}

// adopt takes ownership of an already-open stream that starts at byte 0.
// The download path uses it so a request without a Range header opens
// exactly one stream, at the same moment it did before this existed —
// which is also what lets a backend read error still answer 500 instead of
// a truncated 200.
func (s *rangeSeeker) adopt(rc io.ReadCloser) {
	s.rc, s.pos, s.rpos = rc, 0, 0
}

func (s *rangeSeeker) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = s.pos + offset
	case io.SeekEnd:
		abs = s.size + offset
	default:
		return 0, fs.ErrInvalid
	}
	if abs < 0 {
		return 0, fs.ErrInvalid
	}
	s.pos = abs
	return abs, nil
}

func (s *rangeSeeker) Read(p []byte) (int, error) {
	if s.pos >= s.size {
		// Past the end we know about: answer EOF rather than asking the
		// backend for a window it would have to refuse.
		return 0, io.EOF
	}
	if s.rc == nil || s.pos != s.rpos {
		if err := s.open(); err != nil {
			return 0, err
		}
	}
	n, err := s.rc.Read(p)
	s.pos += int64(n)
	s.rpos += int64(n)
	return n, err
}

// open drops any stale stream and starts a new one at the current
// position, running to the end of the object — http.ServeContent stops
// copying at the range length itself, and Close then aborts the rest.
func (s *rangeSeeker) open() error {
	s.closeStream()
	rc, err := s.src.ReadRange(s.ctx, s.pos, -1)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fs.ErrNotExist
		}
		return err
	}
	s.rc = rc
	s.rpos = s.pos
	return nil
}

func (s *rangeSeeker) closeStream() {
	if s.rc != nil {
		_ = s.rc.Close()
		s.rc = nil
	}
}

// Close releases the backend stream. Safe to call more than once.
func (s *rangeSeeker) Close() error {
	s.closeStream()
	return nil
}
