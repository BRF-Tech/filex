package dav

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// A name longer than the storage holds (storage.IsNameTooLong: 255 bytes of
// UTF-8 in one name on a local disk) is the client's to fix, and it has to be
// told so. x/net/webdav cannot say it: the library picks the status from the
// kind of call that failed - a refused PUT is 405 "Method Not Allowed", which
// Windows' WebDAV client reads as "this server does not take uploads here", a
// refused MKCOL 405, a MOVE 403, a COPY 500 - and writes it itself.
//
// So the FileSystem notes the refusal on the request (noteMapErr), and the
// response writer the library is handed turns the library's error status for
// that request into a 400 that says what happened (task #238).

type nameNoteKey struct{}

// nameNote is one request's record that the storage refused a name as too
// long.
type nameNote struct{ tooLong atomic.Bool }

// withNameNote gives a request its note.
func withNameNote(ctx context.Context) (context.Context, *nameNote) {
	n := &nameNote{}
	return context.WithValue(ctx, nameNoteKey{}, n), n
}

// noteMapErr is mapErr with a too-long name noted on the request first: for
// a driver call that names something (a write, a mkdir, a move, the Stat the
// library asks of a MOVE's or COPY's destination before it moves anything).
func noteMapErr(ctx context.Context, err error) error {
	if storage.IsNameTooLong(err) {
		if n, ok := ctx.Value(nameNoteKey{}).(*nameNote); ok {
			n.tooLong.Store(true)
		}
	}
	return mapErr(err)
}

// tooLongText is what the client reads instead of the library's status text.
const tooLongText = "a name in this path is too long for this storage\n"

// tooLongWriter is the ResponseWriter the library writes to: an error status
// on a request whose name the storage refused as too long goes out as 400
// with tooLongText, and the library's own text after it is dropped. Every
// other response passes through untouched.
type tooLongWriter struct {
	http.ResponseWriter
	note    *nameNote
	swapped bool
}

func (t *tooLongWriter) WriteHeader(code int) {
	if code >= http.StatusBadRequest && t.note.tooLong.Load() {
		t.swapped = true
		h := t.ResponseWriter.Header()
		h.Del("Content-Length")
		h.Set("Content-Type", "text/plain; charset=utf-8")
		t.ResponseWriter.WriteHeader(http.StatusBadRequest)
		_, _ = t.ResponseWriter.Write([]byte(tooLongText))
		return
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *tooLongWriter) Write(b []byte) (int, error) {
	if t.swapped {
		return len(b), nil
	}
	return t.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the connection's own writer.
func (t *tooLongWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }
