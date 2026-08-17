package mountfs

import (
	"errors"
	"io"
	"os"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

// One classification of an error, shared by every platform binding.
//
// ⚠ It lives here rather than in each binding because the two bindings speak
// different vocabularies — Linux wants a syscall.Errno, WinFsp wants a negative
// cgofuse constant — and the thing that must NOT differ between them is which
// failure counts as "no such file" and which counts as "out of space". Two
// copies of that judgement would drift, and the drift would show up as a
// Windows mount reporting a quota rejection as a generic I/O error.

// ErrKind is what went wrong, in terms a filesystem can express.
type ErrKind int

const (
	// ErrNone is success.
	ErrNone ErrKind = iota
	// ErrIO is the fallback: something failed and nothing more precise fits.
	ErrIO
	// ErrNotFound — the path is not there.
	ErrNotFound
	// ErrDenied — the caller may not do this.
	ErrDenied
	// ErrExists — something is already there.
	ErrExists
	// ErrReadOnlyFS — the mount, or the storage behind it, refuses writes.
	ErrReadOnlyFS
	// ErrNoSpace — a quota or a disk said no. ⚠ Distinct from ErrDenied on
	// purpose: "you are out of space" is a thing the user can act on, and
	// every file manager on both platforms shows it differently.
	ErrNoSpace
	// ErrTooBig — the file is larger than the write spool allows.
	ErrTooBig
)

// Classify turns an error from the filex client into an ErrKind.
func Classify(err error) ErrKind {
	if err == nil {
		return ErrNone
	}
	switch {
	case errors.Is(err, io.EOF):
		return ErrNone
	case errors.Is(err, errReadOnly):
		return ErrReadOnlyFS
	case errors.Is(err, ErrTooLarge):
		return ErrTooBig
	case os.IsNotExist(err):
		return ErrNotFound
	case os.IsExist(err):
		return ErrExists
	case os.IsPermission(err):
		return ErrDenied
	}
	// The server's own status is the most reliable signal there is — it is the
	// only place that knows whether 403 meant "not yours" or 507 meant "no
	// room", and both look identical from here otherwise.
	var apiErr *cliclient.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 401, 403:
			return ErrDenied
		case 404:
			return ErrNotFound
		case 409:
			return ErrExists
		case 413, 507:
			return ErrNoSpace
		}
	}
	return ErrIO
}
