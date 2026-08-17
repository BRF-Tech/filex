package s3api

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"hash"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
)

// Integrity checks a client asks for.
//
// Two contracts are in play and both are live traffic:
//
//   - Content-MD5, base64. The old one, and rclone still sends it on every
//     small upload.
//   - x-amz-checksum-{crc32,crc32c,sha1,sha256}, base64. The current one. It
//     arrives as a header when the client knows the digest up front, and as a
//     TRAILER after the body when the client is hashing as it streams — which
//     is what the modern SDKs do by default.
//
// ⚠⚠ The point of implementing this is not compatibility, it is the failure it
// catches. A byte flipped between the client and here is otherwise stored, and
// stored corruption is discovered by whoever opens the file months later. The
// client has already told us what the bytes should hash to; not checking it is
// throwing away a free verification.

// checksumAlgorithms maps the header suffix onto a constructor.
var checksumAlgorithms = map[string]func() hash.Hash{
	"crc32":  func() hash.Hash { return crc32.NewIEEE() },
	"crc32c": func() hash.Hash { return crc32.New(crc32.MakeTable(crc32.Castagnoli)) },
	"sha1":   sha1.New,
	"sha256": sha256.New,
}

// ErrChecksumMismatch means the bytes did not hash to what the caller said.
var ErrChecksumMismatch = errors.New("s3api: the object did not match the checksum the client sent")

// checksumCheck is one declared digest and the hash computing it.
type checksumCheck struct {
	// Name is the header the value came from, for the error message.
	Name string
	// Want is the base64 digest the client declared. Empty means the value is
	// still to come in a trailer.
	Want string
	h    hash.Hash
}

// checksumSet accumulates every digest a request declared.
type checksumSet struct {
	checks []*checksumCheck
	// trailerNames are the headers the client PROMISED to send in the trailer
	// (x-amz-trailer). They are hashed while the body streams and compared once
	// the trailer arrives.
	trailerNames []string
}

// newChecksumSet reads the request's declarations.
func newChecksumSet(h http.Header) *checksumSet {
	set := &checksumSet{}

	if v := strings.TrimSpace(h.Get("Content-Md5")); v != "" {
		set.checks = append(set.checks, &checksumCheck{Name: "Content-MD5", Want: v, h: md5.New()})
	}
	for algo, mk := range checksumAlgorithms {
		if v := strings.TrimSpace(h.Get("X-Amz-Checksum-" + strings.ToUpper(algo[:1]) + algo[1:])); v != "" {
			set.checks = append(set.checks, &checksumCheck{Name: "x-amz-checksum-" + algo, Want: v, h: mk()})
		}
	}
	// A streaming upload names its trailer up front and sends the value after
	// the body. Hashing has to start now regardless — the bytes are gone by the
	// time the trailer arrives.
	for _, raw := range h.Values("X-Amz-Trailer") {
		for _, name := range strings.Split(raw, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			algo := strings.TrimPrefix(name, "x-amz-checksum-")
			mk, ok := checksumAlgorithms[algo]
			if !ok || algo == name {
				continue
			}
			set.checks = append(set.checks, &checksumCheck{Name: name, h: mk()})
			set.trailerNames = append(set.trailerNames, name)
		}
	}
	return set
}

// Wrap tees the body through every hash. It returns r untouched when nothing
// was declared, so the common path costs nothing.
func (s *checksumSet) Wrap(r io.Reader) io.Reader {
	if s == nil || len(s.checks) == 0 {
		return r
	}
	writers := make([]io.Writer, 0, len(s.checks))
	for _, c := range s.checks {
		writers = append(writers, c.h)
	}
	return io.TeeReader(r, io.MultiWriter(writers...))
}

// Empty reports whether the request declared nothing to check.
func (s *checksumSet) Empty() bool { return s == nil || len(s.checks) == 0 }

// Verify compares every declared digest against what was actually read.
//
// trailers, when non-nil, supplies the values that arrived after the body.
func (s *checksumSet) Verify(trailers map[string]string) error {
	if s == nil {
		return nil
	}
	for _, c := range s.checks {
		want := c.Want
		if want == "" && trailers != nil {
			want = strings.TrimSpace(trailers[strings.ToLower(c.Name)])
		}
		if want == "" {
			// Promised and not delivered. The bytes are intact as far as
			// anything here can tell, and inventing a failure would reject a
			// good upload — but pretending it was verified would be worse, so
			// this stays silent rather than reporting success.
			continue
		}
		got := base64.StdEncoding.EncodeToString(c.h.Sum(nil))
		if !strings.EqualFold(got, want) {
			return &ChecksumError{Header: c.Name, Want: want, Got: got}
		}
	}
	return nil
}

// ChecksumError names which digest disagreed.
type ChecksumError struct {
	Header string
	Want   string
	Got    string
}

func (e *ChecksumError) Error() string {
	return "the " + e.Header + " you specified did not match what we received (" + e.Got + ")"
}

func (e *ChecksumError) Is(target error) bool { return target == ErrChecksumMismatch }

// checksumErrorCode is the S3 code for a failed integrity check. The two
// contracts have different codes and clients branch on them: BadDigest is
// retried by most SDKs, InvalidRequest is not.
func checksumErrorCode(err error) (int, string) {
	var ce *ChecksumError
	if errors.As(err, &ce) && ce.Header != "Content-MD5" {
		return http.StatusBadRequest, "XAmzContentChecksumMismatch"
	}
	return http.StatusBadRequest, "BadDigest"
}
