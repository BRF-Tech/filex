package s3api

import (
	"bufio"
	"crypto/hmac"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// aws-chunked bodies: the framing a client uses when it signs the payload as
// it streams.
//
// The body is NOT the object. It is the object cut into chunks, each one
// prefixed with its length and (in the signed variant) its own signature:
//
//	<hex-length>;chunk-signature=<64 hex>\r\n
//	<length bytes>\r\n
//	…
//	0;chunk-signature=<64 hex>\r\n
//	\r\n
//
// Writing that through verbatim stores the framing inside the file — silent
// corruption, found whenever somebody finally opens the object. So it has to
// be decoded, and what cannot be decoded has to be refused.
//
// # Why the signatures are verified rather than skipped
//
// Decoding alone is easy and wrong. In the signed variant each chunk carries a
// MAC chained to the one before it, seeded by the request's own signature —
// and that chain is the ONLY thing protecting the body in flight, because the
// request signature covers the literal string
// "STREAMING-AWS4-HMAC-SHA256-PAYLOAD" rather than any bytes. Stripping the
// framing without checking the chain would accept a body altered en route and
// report success.
//
// The chain per chunk is:
//
//	string-to-sign = "AWS4-HMAC-SHA256-PAYLOAD\n" +
//	                 <amz-date> "\n" <scope> "\n" +
//	                 <previous signature> "\n" +
//	                 sha256("") "\n" +
//	                 sha256(chunk data)
//
// signed with the same key the request used, seeded from the Authorization
// header's signature.
const (
	streamingSigned          = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
	streamingSignedTrailer   = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER"
	streamingUnsignedTrailer = "STREAMING-UNSIGNED-PAYLOAD-TRAILER"

	// maxChunkHeader bounds one header line so a malformed body cannot make
	// us buffer without limit before failing.
	maxChunkHeader = 1 << 12
	// maxChunkSize bounds one chunk's payload.
	//
	// ⚠ Verification needs the whole chunk in memory (the MAC covers it, and
	// it must be checked BEFORE the bytes are handed on), so this is a memory
	// bound, not a taste. Clients use 64 KiB; 16 MiB is far above that and
	// still finite.
	maxChunkSize = 16 << 20
)

// Errors the caller maps onto S3 codes.
var (
	// ErrChunkFraming means the body is not valid aws-chunked.
	ErrChunkFraming = errors.New("s3api: malformed aws-chunked body")
	// ErrChunkSignature means a chunk's MAC did not match — the body was
	// altered between the client and here.
	ErrChunkSignature = errors.New("s3api: chunk signature does not match")
)

// IsChunked reports whether a payload hash marks an aws-chunked body.
func IsChunked(payloadHash string) bool {
	return strings.HasPrefix(payloadHash, "STREAMING-")
}

// chunkedSupported reports whether this variant can be decoded.
func chunkedSupported(payloadHash string) bool {
	switch payloadHash {
	case streamingSigned, streamingSignedTrailer, streamingUnsignedTrailer:
		return true
	}
	return false
}

// chunkedReader unwraps an aws-chunked body, verifying each chunk's signature
// when the variant carries one.
type chunkedReader struct {
	br *bufio.Reader

	verify    bool
	key       []byte
	prevSig   string
	dateStamp string
	scope     string

	buf  []byte // the current chunk's undelivered bytes
	done bool
	err  error

	// trailers holds the headers that follow the terminating chunk, lower-cased.
	// The trailer variants carry the whole-object checksum there — it is the
	// only place it can be, since the client is still hashing while it sends.
	trailers map[string]string
}

// Trailers returns the trailing headers, available once the body has been read
// to EOF. Reading them earlier returns what has arrived so far, which is
// nothing — the caller must finish the body first, and every caller here does.
func (c *chunkedReader) Trailers() map[string]string { return c.trailers }

// newChunkedReader wraps r.
//
// verify=false is for the unsigned-trailer variant, which deliberately trades
// the per-chunk MAC for a trailing checksum — the client chose that, and
// pretending to verify what carries no signature would be theatre.
func newChunkedReader(r io.Reader, sr *Request, secret, seedSig string, verify bool) *chunkedReader {
	c := &chunkedReader{
		br:      bufio.NewReaderSize(r, 64<<10),
		verify:  verify,
		prevSig: seedSig,
	}
	if verify {
		c.key = signingKey(secret, sr.Date, sr.Region, sr.Service)
		c.dateStamp = sr.Timestamp.UTC().Format("20060102T150405Z")
		c.scope = sr.Date + "/" + sr.Region + "/" + sr.Service + "/aws4_request"
	}
	return c
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	for len(c.buf) == 0 {
		if c.err != nil {
			return 0, c.err
		}
		if c.done {
			return 0, io.EOF
		}
		if err := c.nextChunk(); err != nil {
			c.err = err
			return 0, err
		}
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

// nextChunk reads one header line, its payload and its trailing CRLF, and
// verifies the signature before the bytes become readable.
func (c *chunkedReader) nextChunk() error {
	line, err := c.readLine()
	if err != nil {
		return err
	}
	sizeStr, rest, _ := strings.Cut(line, ";")
	size, err := strconv.ParseInt(strings.TrimSpace(sizeStr), 16, 64)
	if err != nil || size < 0 || size > maxChunkSize {
		return ErrChunkFraming
	}

	sig := ""
	if v := strings.TrimSpace(rest); v != "" {
		name, val, ok := strings.Cut(v, "=")
		if !ok || strings.TrimSpace(name) != "chunk-signature" {
			return ErrChunkFraming
		}
		sig = strings.TrimSpace(val)
	}
	if c.verify && sig == "" {
		return ErrChunkFraming
	}

	if size == 0 {
		// The terminating chunk signs the empty payload.
		if c.verify {
			if err := c.checkChunk(nil, sig); err != nil {
				return err
			}
		}
		c.done = true
		// A CRLF and, in the trailer variants, trailer headers follow. They are
		// not part of the object — but they are where the client puts the
		// whole-object checksum, so they are read rather than discarded.
		c.readTrailers()
		return nil
	}

	data := make([]byte, size)
	if _, err := io.ReadFull(c.br, data); err != nil {
		return ErrChunkFraming
	}
	if c.verify {
		if err := c.checkChunk(data, sig); err != nil {
			return err
		}
	}
	if err := c.expectCRLF(); err != nil {
		return err
	}
	c.buf = data
	return nil
}

// readTrailers consumes whatever follows the terminating chunk.
//
// ⚠ Failures here are silent on purpose: the object's bytes are already
// complete and verified by the chunk chain, so a malformed trailer must not
// turn a good upload into an error. A checksum the caller wanted verified and
// did not arrive simply is not there, and the caller can tell the difference
// between absent and wrong.
func (c *chunkedReader) readTrailers() {
	c.trailers = map[string]string{}
	for i := 0; i < 8; i++ { // a handful; a client sends one or two
		line, err := c.readLine()
		if err != nil {
			return
		}
		if strings.TrimSpace(line) == "" {
			if len(c.trailers) > 0 {
				return // the blank line after the trailer block
			}
			continue // the CRLF that closes the terminating chunk
		}
		name, val, ok := strings.Cut(line, ":")
		if !ok {
			return
		}
		c.trailers[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(val)
	}
}

// checkChunk verifies one chunk against the chain and advances it.
func (c *chunkedReader) checkChunk(data []byte, sig string) error {
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256-PAYLOAD",
		c.dateStamp,
		c.scope,
		c.prevSig,
		emptyPayloadHash,
		hashHex(data),
	}, "\n")
	want := hex.EncodeToString(sign(c.key, []byte(stringToSign)))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return ErrChunkSignature
	}
	c.prevSig = want
	return nil
}

func (c *chunkedReader) readLine() (string, error) {
	line, err := c.br.ReadString('\n')
	if err != nil {
		return "", ErrChunkFraming
	}
	if len(line) > maxChunkHeader {
		return "", ErrChunkFraming
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (c *chunkedReader) expectCRLF() error {
	b := make([]byte, 2)
	if _, err := io.ReadFull(c.br, b); err != nil {
		return ErrChunkFraming
	}
	if b[0] != '\r' || b[1] != '\n' {
		return ErrChunkFraming
	}
	return nil
}

// decodedLength converts the encoded length a client declares into the
// object's real size.
//
// ⚠ A chunked request sends Content-Length for the FRAMED body and the real
// size in x-amz-decoded-content-length. Using the former as the object size
// records the framing overhead as part of the file — the bytes would be right
// and every listing wrong.
func decodedLength(header string) (int64, error) {
	v := strings.TrimSpace(header)
	if v == "" {
		return -1, fmt.Errorf("%w: x-amz-decoded-content-length is required for a chunked body", ErrChunkFraming)
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return -1, ErrChunkFraming
	}
	return n, nil
}
