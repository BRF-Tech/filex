package s3api

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// What an object KEY may be on this endpoint, and how a key and a storage's
// answer about it travel back to a client (task #238, the hostile names).
//
// An S3 key is an opaque string to AWS; here every key is a path in a storage,
// so it must be one the storage could have: one spelling per file. A key with
// an empty, `.` or `..` segment is a second spelling of another path - the
// drivers clean it - and is refused before anything is asked of it, on every
// verb: the confinement, the grant, the lock and the same-object test of a
// copy all compare the key as written, so they must never meet a key the
// storage reads otherwise.

// maxKeyBytes is S3's own limit on a key: 1024 bytes of UTF-8.
const maxKeyBytes = 1024

// errKeyTooLong is a key over maxKeyBytes, or one whose name the storage
// itself refused as too long (storage.IsNameTooLong).
var errKeyTooLong = errors.New("your key is too long")

// keyError reports why key cannot name an object here, or nil. A trailing
// slash (a folder marker) is allowed once.
//
// It is the question of a key that READS or DELETES (readableSegment): one
// spelling per path, no NUL. What a request that puts something at the key
// must also answer - every name it makes is one a file may be GIVEN - is
// newKeyError's, asked once the storage is known.
func keyError(key string) error {
	if len(key) > maxKeyBytes {
		return errKeyTooLong
	}
	return segmentsError(strings.TrimSuffix(key, "/"))
}

// segmentsError is readableSegment on every segment of a slash-separated
// path, with no trailing slash allowed.
func segmentsError(p string) error {
	for _, seg := range strings.Split(p, "/") {
		if err := readableSegment(seg); err != nil {
			return err
		}
	}
	return nil
}

// readableSegment is what a key that only reads or deletes may not hold in a
// segment: nothing (an empty segment), only dots, a NUL - another spelling of
// a path, or no name at all - and, on a Windows host, a backslash, which
// separates there. A control character, or elsewhere a backslash, is a name
// a file ALREADY on the disk may have: it is read, listed, overwritten and
// deleted like any other (the local driver, issue #34; the owner's rule for
// 0.57); only a NEW name may not carry one (newKeyError).
func readableSegment(seg string) error {
	switch {
	case seg == "":
		return &leafname.Error{Name: seg, Reason: leafname.Empty}
	case leafname.DotSegment(seg):
		return &leafname.Error{Name: seg, Reason: leafname.Dots}
	case strings.IndexByte(seg, 0) >= 0:
		return &leafname.Error{Name: seg, Reason: leafname.NUL}
	case filepath.Separator == '\\' && strings.ContainsRune(seg, '\\'):
		return &leafname.Error{Name: seg, Reason: leafname.Separator}
	}
	return nil
}

// newKeyError is the rule for a request that puts something at key (PUT, a
// folder marker, CopyObject's destination, a multipart upload): every name
// it makes is one a file may be given (internal/leafname). A segment that is
// already there - the file a PUT replaces, a folder on the way - is not made
// by the request and keeps the name it has, whatever made it.
func (h *Handler) newKeyError(ctx context.Context, st *model.Storage, key string) error {
	segs := strings.Split(strings.TrimSuffix(key, "/"), "/")
	var drv storage.Driver
	for i, seg := range segs {
		err := leafname.Check(seg)
		if err == nil {
			continue
		}
		if drv == nil {
			d, derr := h.cfg.Resolver(st.ID)
			if derr != nil {
				return err
			}
			drv = d
		}
		if _, serr := drv.Stat(ctx, strings.Join(segs[:i+1], "/")); serr != nil {
			return err
		}
	}
	return nil
}

// deletesNothing reports whether an S3 DELETE of key - DeleteObject, or one
// entry of DeleteObjects - names no object filex holds as one, and so must
// change nothing (the answer is still a success: an S3 DELETE is idempotent).
//
// ⚠⚠ S3 deletes AT MOST THE ONE OBJECT NAMED. It never cascades to the keys
// under a prefix: `mc rm b/folder/` without --recursive, or a sync client
// cleaning up the marker it once made, removes the marker and leaves every
// `folder/...` key - and the prefix stays listed, because its children are
// there. filex's `folder/` is a real directory, so the same request maps to:
//
//   - `folder/` that still holds anything: left exactly as it is. On S3 the
//     marker would go and the prefix would stay listed; here the client sees
//     that very listing. Trashing the folder here deleted every file in it,
//     which is what no S3 client asked for (task #238).
//   - `folder/` that holds nothing (filex's own entries aside): the folder IS
//     the marker (putDirectoryMarker made it), so it goes.
//   - `folder` without the slash, where folder is a directory: S3 has no object
//     of that name, only keys under the prefix - nothing to delete.
//   - `name/` where name is a FILE: not that object's key either.
//
// A key that is not there at all is not decided here: the delete goes on and
// finds it missing, which is the same success.
func deletesNothing(ctx context.Context, drv storage.Driver, key string) bool {
	rel := dirOf(key)
	obj, err := drv.Stat(ctx, rel)
	if err != nil {
		return false
	}
	isDir := obj.Kind == storage.KindDirectory
	if !isDirKey(key) {
		return isDir
	}
	if !isDir {
		return true
	}
	children, err := drv.List(ctx, rel)
	if err != nil {
		// Cannot tell whether it is empty: leave it, which is never wrong.
		return true
	}
	for _, c := range children {
		if !syspath.IsName(c.Name) {
			return true
		}
	}
	return false
}

// prefixError reports a listing prefix whose folders could not be a key's:
// every segment but the last (which is matched as the start of a name) is
// asked what keyError asks of a key that is read. The walk starts in the
// prefix's folder, and a `..` there would start it somewhere else.
func prefixError(prefix string) error {
	if len(prefix) > maxKeyBytes {
		return errKeyTooLong
	}
	i := strings.LastIndex(prefix, "/")
	if i < 0 {
		return nil
	}
	return segmentsError(prefix[:i])
}

// keyErrorCode is the S3 code and message for a keyError.
func keyErrorCode(err error) (string, string) {
	if errors.Is(err, errKeyTooLong) {
		return "KeyTooLongError", "your key is too long"
	}
	return "InvalidArgument", "the object key is not valid here: " + err.Error()
}

// writeKeyError refuses a request for its key: 400 with keyErrorCode's code.
func writeKeyError(w http.ResponseWriter, r *http.Request, err error) {
	code, msg := keyErrorCode(err)
	WriteError(w, r, http.StatusBadRequest, code, msg)
}

// storageAnswer is what a client is told when a storage driver fails: the
// status and code it acts on, and a message that names no server path. A
// driver's own text carries the storage's place on the server's disk
// (`open /srv/<root>/...: file name too long`), which a client has no use for
// and should not see; a 500 is logged in full instead (storageFailed).
func storageAnswer(err error) (int, string, string) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return http.StatusNotFound, "NoSuchKey", "the specified key does not exist"
	case storage.IsNameTooLong(err):
		// A name longer than this storage holds is the client's to fix -
		// a 500 would only make it retry.
		return http.StatusBadRequest, "KeyTooLongError", "a name in this key is too long for this storage"
	case errors.Is(err, storage.ErrReadOnly):
		return http.StatusForbidden, "AccessDenied", "this bucket is read-only"
	case errors.Is(err, storage.ErrUnsupported):
		return http.StatusNotImplemented, "NotImplemented", "this storage does not support the operation"
	}
	return http.StatusInternalServerError, "InternalError", "the storage could not complete the request"
}

// storageFailed answers a storage driver's error (storageAnswer); a HEAD gets
// the status alone (objectError).
func (h *Handler) storageFailed(w http.ResponseWriter, r *http.Request, bodyWanted bool, err error) {
	status, code, msg := storageAnswer(err)
	if status >= http.StatusInternalServerError {
		slog.Warn("s3api: storage error", slog.String("method", r.Method),
			slog.String("path", r.URL.Path), slog.Any("err", err))
	}
	h.objectError(w, r, bodyWanted, status, code, msg)
}

// urlEncodeKey is a key as `encoding-type=url` returns it: the form encoding
// AWS uses (a space is `+`, a `+` is `%2B`, `%` is `%25`) with the slashes
// left as they are. Every SDK that asks for it decodes with the form decoder
// (botocore's unquote_plus, Go's url.QueryUnescape), and a key with a control
// character - which XML 1.0 cannot carry - reaches it intact.
func urlEncodeKey(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "%2F", "/")
}

// encodeListing applies `encoding-type=url` to a listing: the elements AWS
// encodes (V2: Delimiter, Prefix, Key, StartAfter; V1: Delimiter, Marker,
// Prefix, NextMarker, Key) and the common prefixes, which are keys too.
func encodeListing(out *ListBucketResult) {
	out.Prefix = urlEncodeKey(out.Prefix)
	out.Delimiter = urlEncodeKey(out.Delimiter)
	out.StartAfter = urlEncodeKey(out.StartAfter)
	out.Marker = urlEncodeKey(out.Marker)
	out.NextMarker = urlEncodeKey(out.NextMarker)
	for i := range out.Contents {
		out.Contents[i].Key = urlEncodeKey(out.Contents[i].Key)
	}
	for i := range out.CommonPrefixes {
		out.CommonPrefixes[i].Prefix = urlEncodeKey(out.CommonPrefixes[i].Prefix)
	}
}

// tokenMark starts a continuation token this endpoint issued.
const tokenMark = "~"

// continuationToken is the V2 continuation token for a page that ended at
// key: opaque, as AWS's are, and plain ASCII, so a key XML cannot carry (a
// control character) does not take the page with it.
func continuationToken(key string) string {
	if key == "" {
		return ""
	}
	return tokenMark + base64.RawURLEncoding.EncodeToString([]byte(key))
}

// keyFromToken reads a continuation token back. A token that is not one of
// ours - the raw key an older server issued, mid-listing across an upgrade -
// is taken as the key itself.
func keyFromToken(tok string) string {
	if rest, ok := strings.CutPrefix(tok, tokenMark); ok {
		if b, err := base64.RawURLEncoding.DecodeString(rest); err == nil {
			return string(b)
		}
	}
	return tok
}

// escapeKeyPath percent-encodes each segment of a slash-separated key for a
// URL path (a completed upload's Location): a space, `#` or `?` in a name
// must not end the path.
func escapeKeyPath(key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}
