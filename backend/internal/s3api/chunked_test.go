package s3api

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// The decoder, tested against a body built the way a client builds one — the
// chain included, because the chain is the only thing protecting the bytes in
// flight and a decoder that ignores it looks identical until someone tampers.

const (
	chunkSecret = "sekritsekritsekritsekritsekritsekritsekr"
	chunkSeed   = "0000000000000000000000000000000000000000000000000000000000000000"
)

func chunkReq(at time.Time) *Request {
	return &Request{
		AccessKeyID: testAKID,
		Region:      testRegion,
		Service:     "s3",
		Date:        at.UTC().Format("20060102"),
		Timestamp:   at.UTC(),
		Signature:   chunkSeed,
	}
}

// buildChunked frames body into aws-chunked, signing each chunk the way a
// client does.
func buildChunked(sr *Request, secret string, body []byte, chunkSize int, signed bool) string {
	key := signingKey(secret, sr.Date, sr.Region, sr.Service)
	stamp := sr.Timestamp.UTC().Format("20060102T150405Z")
	scope := sr.Date + "/" + sr.Region + "/" + sr.Service + "/aws4_request"
	prev := sr.Signature

	var b strings.Builder
	emit := func(data []byte) {
		sig := ""
		if signed {
			sts := strings.Join([]string{
				"AWS4-HMAC-SHA256-PAYLOAD", stamp, scope, prev, emptyPayloadHash, hashHex(data),
			}, "\n")
			sig = hex.EncodeToString(sign(key, []byte(sts)))
			prev = sig
			fmt.Fprintf(&b, "%x;chunk-signature=%s\r\n", len(data), sig)
		} else {
			fmt.Fprintf(&b, "%x\r\n", len(data))
		}
		b.Write(data)
		b.WriteString("\r\n")
	}
	for off := 0; off < len(body); off += chunkSize {
		end := off + chunkSize
		if end > len(body) {
			end = len(body)
		}
		emit(body[off:end])
	}
	emit(nil) // terminating chunk
	return b.String()
}

func TestChunkedReaderReturnsTheObjectNotTheFraming(t *testing.T) {
	at := time.Now().UTC()
	sr := chunkReq(at)
	body := []byte(strings.Repeat("the quick brown fox. ", 500))

	for _, size := range []int{7, 64, 4096, len(body)} {
		framed := buildChunked(sr, chunkSecret, body, size, true)
		r := newChunkedReader(strings.NewReader(framed), sr, chunkSecret, sr.Signature, true)
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("chunk size %d: %v", size, err)
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("chunk size %d: decoded %d bytes, want %d", size, len(got), len(body))
		}
		// ⚠ The framing must not survive into the object. This is the whole
		// point: a decoder that passed the body through would put
		// "chunk-signature=" inside the file.
		if bytes.Contains(got, []byte("chunk-signature")) {
			t.Fatalf("chunk size %d: the framing leaked into the object", size)
		}
	}
}

// The chain is what protects the bytes: the request signature covers the
// literal STREAMING sentinel, not the payload, so without this check a body
// altered in flight would be accepted and reported as success.
func TestChunkedReaderRefusesATamperedChunk(t *testing.T) {
	at := time.Now().UTC()
	sr := chunkReq(at)
	body := []byte("hello world, this is the payload")
	framed := buildChunked(sr, chunkSecret, body, 8, true)

	// Flip one payload byte, leaving the framing intact.
	i := strings.Index(framed, "hello")
	tampered := []byte(framed)
	tampered[i] = 'H'

	r := newChunkedReader(bytes.NewReader(tampered), sr, chunkSecret, sr.Signature, true)
	_, err := io.ReadAll(r)
	if !errors.Is(err, ErrChunkSignature) {
		t.Fatalf("tampered chunk = %v, want ErrChunkSignature", err)
	}
}

// A chunk signed under a different secret must not verify either — otherwise
// the chain proves nothing about WHO sent the bytes.
func TestChunkedReaderRefusesAForeignSignature(t *testing.T) {
	at := time.Now().UTC()
	sr := chunkReq(at)
	framed := buildChunked(sr, "a-completely-different-secret-000000000", []byte("payload"), 4, true)

	r := newChunkedReader(strings.NewReader(framed), sr, chunkSecret, sr.Signature, true)
	if _, err := io.ReadAll(r); !errors.Is(err, ErrChunkSignature) {
		t.Fatalf("foreign signature = %v, want ErrChunkSignature", err)
	}
}

// The unsigned-trailer variant carries no per-chunk MAC by design — the client
// chose that trade. Decoding must still be exact.
func TestChunkedReaderUnsignedVariant(t *testing.T) {
	at := time.Now().UTC()
	sr := chunkReq(at)
	body := []byte("unsigned but still framed")
	framed := buildChunked(sr, chunkSecret, body, 5, false)

	r := newChunkedReader(strings.NewReader(framed), sr, "", "", false)
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unsigned: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("unsigned decoded %q, want %q", got, body)
	}
}

func TestChunkedReaderRefusesGarbage(t *testing.T) {
	at := time.Now().UTC()
	sr := chunkReq(at)
	cases := map[string]string{
		"no header":         "just some bytes",
		"bad length":        "zz;chunk-signature=" + chunkSeed + "\r\nab\r\n",
		"missing crlf":      "2;chunk-signature=" + chunkSeed + "\r\nabXX",
		"truncated payload": "ff;chunk-signature=" + chunkSeed + "\r\nab\r\n",
		"unknown extension": "2;not-a-signature=x\r\nab\r\n",
	}
	for name, framed := range cases {
		t.Run(name, func(t *testing.T) {
			r := newChunkedReader(strings.NewReader(framed), sr, chunkSecret, sr.Signature, true)
			if _, err := io.ReadAll(r); err == nil {
				t.Fatal("garbage decoded without error")
			}
		})
	}
}

// ⚠ Two lengths are in play and confusing them is a quiet bug: Content-Length
// describes the FRAMED body, x-amz-decoded-content-length the object.
func TestDecodedLength(t *testing.T) {
	if n, err := decodedLength("1234"); err != nil || n != 1234 {
		t.Errorf("decodedLength(1234) = %d, %v", n, err)
	}
	for _, bad := range []string{"", "  ", "-1", "abc"} {
		if _, err := decodedLength(bad); err == nil {
			t.Errorf("decodedLength(%q) accepted", bad)
		}
	}
}

func TestChunkedVariantSupport(t *testing.T) {
	for _, v := range []string{streamingSigned, streamingSignedTrailer, streamingUnsignedTrailer} {
		if !IsChunked(v) || !chunkedSupported(v) {
			t.Errorf("%s should be recognised and supported", v)
		}
	}
	if chunkedSupported("STREAMING-SOMETHING-NEW") {
		t.Error("an unknown streaming variant must NOT be claimed as supported — a wrong guess writes framing into the file")
	}
	if IsChunked("UNSIGNED-PAYLOAD") {
		t.Error("UNSIGNED-PAYLOAD is not chunked")
	}
}
