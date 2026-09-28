package e2edecrypt

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// STREAM content — docs/E2E-ENCRYPTION.md → "Streaming content (STREAM)";
// the browser's side is packages/core/src/lib/e2estream.ts.
//
// STREAM (Hoang, Reyhanitabar, Rogaway, Vizár, CRYPTO 2015) over
// AES-256-GCM under a per-file DEK:
//
//	plaintext = P0 ‖ … ‖ Pn-1, every Pi 2^log2 bytes except the last
//	            (1 … 2^log2 bytes; empty only when the whole plaintext is)
//	nonce(i)  = prefix (7B) ‖ uint32 BE i ‖ last (1B: 1 for i = n-1, else 0)
//	chunk(i)  = AES-GCM(DEK, nonce(i), Pi) — ciphertext ‖ 16B tag, no AD
//
// The counter binds position, the last-flag binds the end: truncating at a
// chunk boundary, reordering and appending all fail a tag.

const (
	// StreamChunkLog2 is the chunk size writers use (2^20 = 1 MiB).
	StreamChunkLog2 = 20
	// minChunkLog2 / maxChunkLog2 bound what a reader accepts: a hostile
	// header must not make it allocate gigabytes for one chunk.
	minChunkLog2    = 10
	maxChunkLog2    = 24
	streamPrefixLen = 7
	// fileVersionStream is the folder-file header version of a STREAM body.
	fileVersionStream = 2
	prefixOff         = 69
	log2Off           = 76
)

// ValidChunkLog2 reports whether a reader accepts this chunk size.
func ValidChunkLog2(n int) bool { return n >= minChunkLog2 && n <= maxChunkLog2 }

// StreamCiphertextSize is the body length for a plaintext of n bytes.
func StreamCiphertextSize(n int64, log2 int) int64 {
	c := int64(1) << log2
	chunks := int64(1)
	if n > 0 {
		chunks = (n + c - 1) / c
	}
	return n + gcmTagLen*chunks
}

func streamNonce(prefix []byte, i uint32, last bool) []byte {
	n := make([]byte, ivLen)
	copy(n, prefix)
	binary.BigEndian.PutUint32(n[streamPrefixLen:], i)
	if last {
		n[ivLen-1] = 1
	}
	return n
}

// errStreamTruncated / errStreamLength name the two ways a STREAM body can
// have the wrong length; both are ErrContent to a caller.
var (
	errStreamTruncated = fmt.Errorf("the encrypted content is truncated: %w", ErrContent)
	errStreamLength    = fmt.Errorf("the content length does not match its header: %w", ErrContent)
)

// DecryptStream writes the plaintext of the STREAM body read from r to w.
// expect is the plaintext length the header promised, or -1. Any tampering —
// a flipped bit, a truncated, reordered or extended body, a length that
// disagrees with expect — returns an error wrapping ErrContent.
//
// ⚠ w has received the plaintext of every chunk before the failing one. The
// caller writes into a temporary place and throws it away on error.
func DecryptStream(w io.Writer, r io.Reader, dek, prefix []byte, log2 int, expect int64) (int64, error) {
	if !ValidChunkLog2(log2) {
		return 0, fmt.Errorf("unsupported chunk size 2^%d: %w", log2, ErrContent)
	}
	if len(prefix) != streamPrefixLen {
		return 0, fmt.Errorf("the nonce prefix is not 7 bytes: %w", ErrContent)
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return 0, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return 0, err
	}
	full := (1 << log2) + gcmTagLen
	br := bufio.NewReaderSize(r, 64*1024)
	buf := make([]byte, full)
	var out int64
	for i := uint64(0); ; i++ {
		if i > 0xffffffff {
			return out, fmt.Errorf("too many chunks: %w", ErrContent)
		}
		n, rerr := io.ReadFull(br, buf)
		switch {
		case errors.Is(rerr, io.EOF):
			// Nothing at all where a chunk should start: the body ended on a
			// chunk that was sealed as "not last" (or is empty).
			return out, errStreamTruncated
		case rerr != nil && !errors.Is(rerr, io.ErrUnexpectedEOF):
			return out, rerr
		}
		last := n < full
		if !last {
			if _, perr := br.Peek(1); errors.Is(perr, io.EOF) {
				last = true
			} else if perr != nil {
				return out, perr
			}
		}
		if n < gcmTagLen {
			return out, errStreamTruncated
		}
		if last && i > 0 && n == gcmTagLen {
			return out, fmt.Errorf("the body ends in an empty chunk no writer produces: %w", ErrContent)
		}
		plain, oerr := g.Open(buf[:0], streamNonce(prefix, uint32(i), last), buf[:n], nil)
		if oerr != nil {
			return out, fmt.Errorf("chunk %d failed authentication (damaged, reordered, truncated or extended): %w", i, ErrContent)
		}
		out += int64(len(plain))
		if expect >= 0 && out > expect {
			return out, errStreamLength
		}
		if _, werr := w.Write(plain); werr != nil {
			return out, werr
		}
		if last {
			break
		}
	}
	if expect >= 0 && out != expect {
		return out, errStreamLength
	}
	return out, nil
}

// streamHeader is a parsed 0x02 folder-file header.
type streamHeader struct {
	wrapIV, wrappedDEK, prefix []byte
	log2                       int
}

// parseStreamHeader reads the 97-byte header of a 0x02 folder file:
//
//	[0..8)   magic "filexe2e"   [8] 0x02
//	[9..21)  wrapIV             [21..69) wrappedDEK = AES-GCM(FMK, wrapIV, DEK)
//	[69..76) STREAM nonce prefix [76] chunk size, log2   [77..97) zeros (ignored)
func parseStreamHeader(h []byte) (*streamHeader, error) {
	if len(h) < headerLen || !HasMagic(h) || h[8] != fileVersionStream {
		return nil, fmt.Errorf("not a version 2 encrypted file: %w", ErrContent)
	}
	log2 := int(h[log2Off])
	if !ValidChunkLog2(log2) {
		return nil, fmt.Errorf("unsupported chunk size 2^%d: %w", log2, ErrContent)
	}
	return &streamHeader{
		wrapIV:     h[wrapIVOff : wrapIVOff+ivLen],
		wrappedDEK: h[wrappedDEKOff : wrappedDEKOff+wrappedDEKLen],
		prefix:     h[prefixOff : prefixOff+streamPrefixLen],
		log2:       log2,
	}, nil
}

// unwrapDEK opens a folder file's DEK with the folder key, or the previous
// one of a folder mid re-key.
func unwrapDEK(fmk, previous, wrapIV, wrapped []byte) ([]byte, bool) {
	for _, k := range [][]byte{fmk, previous} {
		if k == nil {
			continue
		}
		if dek, ok := gcmOpen(k, wrapIV, wrapped); ok && len(dek) == 32 {
			return dek, true
		}
	}
	return nil, false
}

// DecryptFileStream decrypts one folder file — header version 0x01 or 0x02 —
// from r to w. previous is the prior folder key of a folder mid re-key, or
// nil. A 0x01 file is one AES-GCM message and is read whole (at most
// 200 MiB); a 0x02 file streams.
func DecryptFileStream(w io.Writer, r io.Reader, fmk, previous []byte) error {
	head := make([]byte, headerLen)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	head = head[:n]
	if !HasMagic(head) || n < headerLen {
		return fmt.Errorf("not a complete encrypted file: %w", ErrContent)
	}
	switch head[8] {
	case fileVersion:
		rest, err := io.ReadAll(io.LimitReader(r, maxV1Body+1))
		if err != nil {
			return err
		}
		if len(rest) > maxV1Body {
			return fmt.Errorf("a version 1 file larger than the one-shot limit: %w", ErrContent)
		}
		data := append(head, rest...)
		plain, err := DecryptContent(fmk, data)
		if err != nil && previous != nil {
			plain, err = DecryptContent(previous, data)
		}
		if err != nil {
			return err
		}
		_, err = w.Write(plain)
		return err
	case fileVersionStream:
		h, err := parseStreamHeader(head)
		if err != nil {
			return err
		}
		dek, ok := unwrapDEK(fmk, previous, h.wrapIV, h.wrappedDEK)
		if !ok {
			return fmt.Errorf("the file key does not unwrap: %w", ErrContent)
		}
		defer clear(dek)
		_, err = DecryptStream(w, r, dek, h.prefix, h.log2, -1)
		return err
	}
	return &UnsupportedError{Features: []string{fmt.Sprintf("encrypted-file version %d", head[8])}}
}

// maxV1Body is the most a 0x01 file carries after its header: the one-shot
// content limit plus the tag.
const maxV1Body = 200*1024*1024 + gcmTagLen
