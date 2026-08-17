// Package s3api serves the S3 wire protocol on top of filex storages.
//
// This file is the half that has to be exactly right before anything else can
// be built: verifying that a request really was signed by the holder of a
// secret.
//
// # Why this is written by hand
//
// aws-sdk-go-v2 ships a SigV4 signer, and it cannot help here: it SIGNS. There
// is no verify direction, because a client never needs one. The server does,
// so the canonical-request construction is reimplemented here — and the tests
// use the SDK's signer as the golden source, which is the strongest check
// available: if the two disagree about a single byte, the signatures differ.
//
// # The traps, in the order they bite
//
//   - The Host header is SIGNED. A proxy that rewrites it breaks every
//     request with SignatureDoesNotMatch and nothing in the error says so.
//     Go keeps Host off Header, so it is read from r.Host explicitly.
//   - Content-Length is likewise absent from Header (Go promotes it to a
//     field) and some clients sign it.
//   - S3 does NOT normalize or double-encode the path, unlike every other AWS
//     service. `a//b` and `a/./b` are distinct keys, so the raw escaped path
//     is what gets signed.
//   - The payload hash comes from the client (X-Amz-Content-Sha256) and is
//     NOT verified here — verifying it would mean buffering the whole body.
//     It is an input to the signature, so a client cannot lie about it
//     without invalidating its own request, but the bytes themselves are
//     checked where they are read, not here.
package s3api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Errors a caller maps onto S3 error codes.
var (
	// ErrNoCredentials means the request carried no SigV4 material at all.
	ErrNoCredentials = errors.New("s3api: request is not signed")
	// ErrMalformed covers anything unparseable in the signing material.
	ErrMalformed = errors.New("s3api: malformed authorization")
	// ErrSignatureMismatch is the one that matters: the request was signed,
	// and not by the holder of this secret.
	ErrSignatureMismatch = errors.New("s3api: signature does not match")
	// ErrExpired covers both a stale X-Amz-Date and an elapsed presign window.
	ErrExpired = errors.New("s3api: request has expired")
)

const (
	algorithm       = "AWS4-HMAC-SHA256"
	unsignedPayload = "UNSIGNED-PAYLOAD"
	// MaxSkew is how far a request's timestamp may be from ours. AWS uses 15
	// minutes; matching it means a client whose clock is tolerated by S3 is
	// tolerated here, and one that is not gets the same error from both.
	MaxSkew = 15 * time.Minute
)

// Credentials is what the verifier needs about the key that signed a request.
type Credentials struct {
	AccessKeyID string
	SecretKey   string
}

// SecretLookup returns the secret for an access key id, or an error if there
// is no such key. Returning an error and returning a wrong secret must be
// indistinguishable to the caller — both end as ErrSignatureMismatch — so an
// unauthenticated client cannot probe which key ids exist.
type SecretLookup func(accessKeyID string) (string, error)

// Request is the parsed signing material, filled by Parse and consumed by
// Verify. It is exposed because the caller needs the access key id BEFORE it
// can look up a secret.
type Request struct {
	AccessKeyID   string
	Region        string
	Service       string
	Date          string // yyyymmdd, the credential scope date
	SignedHeaders []string
	Signature     string
	// Presigned requests carry their timestamp and lifetime in the query.
	Presigned bool
	Expires   time.Duration
	Timestamp time.Time
	// PayloadHash is what the client claims the body hashes to, or
	// UNSIGNED-PAYLOAD.
	PayloadHash string
}

// Parse extracts the signing material from a request without verifying it.
func Parse(r *http.Request) (*Request, error) {
	if q := r.URL.Query(); q.Get("X-Amz-Algorithm") != "" {
		return parsePresigned(r, q)
	}
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return nil, ErrNoCredentials
	}
	return parseHeader(r, auth)
}

func parseHeader(r *http.Request, auth string) (*Request, error) {
	if !strings.HasPrefix(auth, algorithm+" ") {
		return nil, ErrMalformed
	}
	out := &Request{PayloadHash: r.Header.Get("X-Amz-Content-Sha256")}
	if out.PayloadHash == "" {
		// Older clients omit it entirely and sign the empty-body hash.
		out.PayloadHash = emptyPayloadHash
	}

	for _, part := range strings.Split(strings.TrimPrefix(auth, algorithm+" "), ",") {
		part = strings.TrimSpace(part)
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return nil, ErrMalformed
		}
		switch k {
		case "Credential":
			if err := out.parseCredential(v); err != nil {
				return nil, err
			}
		case "SignedHeaders":
			out.SignedHeaders = strings.Split(v, ";")
		case "Signature":
			out.Signature = v
		}
	}
	if out.AccessKeyID == "" || out.Signature == "" || len(out.SignedHeaders) == 0 {
		return nil, ErrMalformed
	}

	ts := r.Header.Get("X-Amz-Date")
	if ts == "" {
		ts = r.Header.Get("Date")
	}
	t, err := parseAmzTime(ts)
	if err != nil {
		return nil, ErrMalformed
	}
	out.Timestamp = t
	return out, nil
}

func parsePresigned(r *http.Request, q url.Values) (*Request, error) {
	if q.Get("X-Amz-Algorithm") != algorithm {
		return nil, ErrMalformed
	}
	out := &Request{Presigned: true, PayloadHash: unsignedPayload}
	if err := out.parseCredential(q.Get("X-Amz-Credential")); err != nil {
		return nil, err
	}
	out.SignedHeaders = strings.Split(q.Get("X-Amz-SignedHeaders"), ";")
	out.Signature = q.Get("X-Amz-Signature")
	if out.AccessKeyID == "" || out.Signature == "" || len(out.SignedHeaders) == 0 {
		return nil, ErrMalformed
	}
	t, err := parseAmzTime(q.Get("X-Amz-Date"))
	if err != nil {
		return nil, ErrMalformed
	}
	out.Timestamp = t
	secs, err := strconv.Atoi(q.Get("X-Amz-Expires"))
	if err != nil || secs <= 0 {
		return nil, ErrMalformed
	}
	out.Expires = time.Duration(secs) * time.Second
	// A signed payload hash may still be pinned explicitly.
	if h := r.Header.Get("X-Amz-Content-Sha256"); h != "" {
		out.PayloadHash = h
	}
	return out, nil
}

// parseCredential reads "<akid>/<date>/<region>/<service>/aws4_request".
func (sr *Request) parseCredential(v string) error {
	parts := strings.Split(v, "/")
	if len(parts) != 5 || parts[4] != "aws4_request" {
		return ErrMalformed
	}
	sr.AccessKeyID, sr.Date, sr.Region, sr.Service = parts[0], parts[1], parts[2], parts[3]
	return nil
}

// Verify recomputes the signature and compares it in constant time.
//
// ⚠ It must be called with the request EXACTLY as received — before any
// middleware rewrites a header, normalizes the path, or strips a query
// parameter. Everything in the canonical request is part of the MAC.
func Verify(r *http.Request, sr *Request, lookup SecretLookup, now time.Time) error {
	if sr == nil {
		return ErrNoCredentials
	}
	if err := checkTime(sr, now); err != nil {
		return err
	}

	secret, err := lookup(sr.AccessKeyID)
	if err != nil || secret == "" {
		// Deliberately the same answer as a bad signature: an unknown key id
		// and a wrong secret must be indistinguishable, or the endpoint
		// becomes a way to enumerate which keys exist.
		return ErrSignatureMismatch
	}

	canonical, err := canonicalRequest(r, sr)
	if err != nil {
		return err
	}
	scope := sr.Date + "/" + sr.Region + "/" + sr.Service + "/aws4_request"
	stringToSign := strings.Join([]string{
		algorithm,
		sr.Timestamp.UTC().Format("20060102T150405Z"),
		scope,
		hashHex([]byte(canonical)),
	}, "\n")

	want := hex.EncodeToString(sign(signingKey(secret, sr.Date, sr.Region, sr.Service), []byte(stringToSign)))
	if !hmac.Equal([]byte(want), []byte(sr.Signature)) {
		return ErrSignatureMismatch
	}
	return nil
}

func checkTime(sr *Request, now time.Time) error {
	if sr.Presigned {
		if now.After(sr.Timestamp.Add(sr.Expires)) {
			return ErrExpired
		}
		// A presign from the future is as suspect as a stale one.
		if sr.Timestamp.Sub(now) > MaxSkew {
			return ErrExpired
		}
		return nil
	}
	if d := now.Sub(sr.Timestamp); d > MaxSkew || d < -MaxSkew {
		return ErrExpired
	}
	return nil
}

// canonicalRequest builds the string whose hash is signed.
func canonicalRequest(r *http.Request, sr *Request) (string, error) {
	headers, err := canonicalHeaders(r, sr.SignedHeaders)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		r.Method,
		canonicalURI(r.URL),
		canonicalQuery(r.URL, sr.Presigned),
		headers,
		strings.Join(sr.SignedHeaders, ";"),
		sr.PayloadHash,
	}, "\n"), nil
}

// canonicalURI is the escaped path, unnormalized.
//
// ⚠ S3 is the exception among AWS services: it does NOT normalize the path and
// does NOT double-encode it. `a//b` and `a/./b` are different keys, so
// collapsing them here would both break the signature and lose the caller's
// intent.
func canonicalURI(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	return p
}

// canonicalQuery sorts and re-encodes the query, excluding the signature
// itself on a presigned request.
func canonicalQuery(u *url.URL, presigned bool) string {
	q := u.Query()
	if presigned {
		q.Del("X-Amz-Signature")
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(uriEncode(k, true))
			b.WriteByte('=')
			b.WriteString(uriEncode(v, true))
		}
	}
	return b.String()
}

// canonicalHeaders renders the signed headers, in the order the client listed
// them (which SigV4 requires to be sorted, and which we therefore do not
// re-sort — re-sorting would silently repair a malformed request and make the
// signature of a client bug verify).
func canonicalHeaders(r *http.Request, signed []string) (string, error) {
	var b strings.Builder
	for _, name := range signed {
		lower := strings.ToLower(strings.TrimSpace(name))
		var value string
		switch lower {
		case "host":
			// ⚠ Go keeps the Host header out of r.Header. Reading it from the
			// map would yield "" and produce a signature that never matches,
			// with nothing in the message to say why.
			value = r.Host
		case "content-length":
			// Likewise promoted to a field. -1 means unknown, which no client
			// signs.
			if r.ContentLength < 0 {
				return "", ErrMalformed
			}
			value = strconv.FormatInt(r.ContentLength, 10)
		default:
			vals := r.Header.Values(http.CanonicalHeaderKey(lower))
			trimmed := make([]string, 0, len(vals))
			for _, v := range vals {
				trimmed = append(trimmed, trimAll(v))
			}
			value = strings.Join(trimmed, ",")
		}
		b.WriteString(lower)
		b.WriteByte(':')
		b.WriteString(value)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// trimAll strips surrounding space and collapses internal runs, which is what
// SigV4 specifies for header values.
func trimAll(v string) string {
	return strings.Join(strings.Fields(v), " ")
}

// uriEncode percent-encodes per AWS's rules, which are not net/url's: a space
// is %20 (never +), and the unreserved set is exactly A-Za-z0-9-_.~ . A slash
// is unreserved in a path segment and encoded in a query value.
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'),
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte('/')
		default:
			b.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return b.String()
}

// signingKey derives the per-request key: the whole reason the secret cannot
// be stored as a one-way hash.
func signingKey(secret, date, region, service string) []byte {
	k := sign([]byte("AWS4"+secret), []byte(date))
	k = sign(k, []byte(region))
	k = sign(k, []byte(service))
	return sign(k, []byte("aws4_request"))
}

func sign(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// emptyPayloadHash is sha256 of the empty string, what a client signs when it
// omits X-Amz-Content-Sha256 on a bodyless request.
const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func parseAmzTime(v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, ErrMalformed
	}
	for _, layout := range []string{"20060102T150405Z", time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, ErrMalformed
}
