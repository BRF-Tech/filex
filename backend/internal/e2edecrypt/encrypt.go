package e2edecrypt

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// The WRITING half of the folder file format: the Go twin of encryptFile,
// createStreamEncryptor and encryptFolderFileStream in
// packages/core/src/lib/e2ecrypto.ts + e2estream.ts. With the reading half
// in this package it is the one Go source of the format
// (docs/E2E-ENCRYPTION.md → "Format reference"); `filex encrypt` writes
// through it, `filex decrypt` reads through the other half.
//
// Like the reading half, it runs on a user's machine only: nothing in the
// server imports this package (imports_test.go).
//
// Every random byte comes from the io.Reader a caller passes (nil:
// crypto/rand). Tests pass a fixed stream, so a writer can be compared byte
// for byte with vectors an independent implementation wrote
// (testdata/stream-vectors.json, Python `cryptography`).

// MaxOneShot is the most content a 0x01 file carries. Writers use the
// one-shot format up to it and STREAM (0x02) above it, exactly as the browser
// does (E2E_MAX_FILE_BYTES), so every filex since the feature shipped reads
// the smaller files.
const MaxOneShot = 200 * 1024 * 1024

// MinIterations is the fewest PBKDF2 iterations a writer uses. Readers accept
// any count from 1; a writer never goes below this (E2E_MIN_ITERATIONS).
const MinIterations = 600_000

// MinPasswordLen is the shortest folder password the writers accept, as the
// browser's dialog (E2E_MIN_PASSWORD_LEN).
const MinPasswordLen = 8

// ErrSizeMismatch: the plaintext a writer was given is not the length it was
// told. Nothing a writer produced from it is a valid file.
var ErrSizeMismatch = errors.New("the content is not the size it was said to be (changed while it was read?)")

func random(rnd io.Reader, n int) ([]byte, error) {
	if rnd == nil {
		rnd = rand.Reader
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(rnd, b); err != nil {
		return nil, fmt.Errorf("e2e: random bytes: %w", err)
	}
	return b, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// gcmSealIV seals plain under key with a fresh 12-byte IV and returns the IV
// and the ciphertext with its tag.
func gcmSealIV(key, plain []byte, rnd io.Reader) (iv, ct []byte, err error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	if iv, err = random(rnd, ivLen); err != nil {
		return nil, nil, err
	}
	return iv, g.Seal(nil, iv, plain, nil), nil
}

// sealB64 is a "sealed blob": base64(IV ‖ AES-GCM ciphertext ‖ tag), the
// shape of every key slot in a marker.
func sealB64(key, plain []byte, rnd io.Reader) (string, error) {
	iv, ct, err := gcmSealIV(key, plain, rnd)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(append(iv, ct...)), nil
}

// wrapDEK mints a random 32-byte file key and wraps it under the folder key:
// the [9..69) part every folder file header shares.
func wrapDEK(fmk []byte, rnd io.Reader) (dek, wrapIV, wrapped []byte, err error) {
	if len(fmk) != fmkLen {
		return nil, nil, nil, errors.New("e2e: a folder key is 32 bytes")
	}
	if dek, err = random(rnd, 32); err != nil {
		return nil, nil, nil, err
	}
	wrapIV, wrapped, err = gcmSealIV(fmk, dek, rnd)
	if err != nil {
		clear(dek)
		return nil, nil, nil, err
	}
	return dek, wrapIV, wrapped, nil
}

// EncryptContent encrypts a whole file held in memory, up to MaxOneShot, as
// a 0x01 file: the 97-byte header, then AES-GCM(DEK, dataIV, content).
// Byte-compatible with the browser's encryptFile.
func EncryptContent(fmk, plain []byte, rnd io.Reader) ([]byte, error) {
	if len(plain) > MaxOneShot {
		return nil, fmt.Errorf("e2e: %d bytes is over the one-shot limit; use the streamed format", len(plain))
	}
	dek, wrapIV, wrapped, err := wrapDEK(fmk, rnd)
	if err != nil {
		return nil, err
	}
	defer clear(dek)
	dataIV, ct, err := gcmSealIV(dek, plain, rnd)
	if err != nil {
		return nil, err
	}
	out := make([]byte, headerLen, headerLen+len(ct))
	copy(out, MagicPrefix)
	out[8] = fileVersion
	copy(out[wrapIVOff:], wrapIV)
	copy(out[wrappedDEKOff:], wrapped)
	copy(out[dataIVOff:], dataIV)
	// [81..97) reserved zeros
	return append(out, ct...), nil
}

// EncryptStream writes the STREAM body of a plaintext of exactly size bytes,
// read from r, under dek with the 7-byte nonce prefix. It refuses a reader
// that yields fewer or more bytes than size: the last chunk is the one the
// size says is last, and sealing a chunk as "last" that is not would make a
// body no reader accepts. Returns the bytes written.
func EncryptStream(w io.Writer, r io.Reader, dek, prefix []byte, log2 int, size int64) (int64, error) {
	if !ValidChunkLog2(log2) {
		return 0, fmt.Errorf("e2e: unsupported chunk size 2^%d", log2)
	}
	if len(prefix) != streamPrefixLen {
		return 0, errors.New("e2e: the nonce prefix must be 7 bytes")
	}
	if size < 0 {
		return 0, errors.New("e2e: a negative size")
	}
	g, err := newGCM(dek)
	if err != nil {
		return 0, err
	}
	chunk := int64(1) << log2
	n := int64(1)
	if size > 0 {
		n = (size + chunk - 1) / chunk
	}
	if n-1 > 0xffffffff {
		return 0, errors.New("e2e: too many chunks for one stream")
	}
	br := bufio.NewReaderSize(r, 64*1024)
	buf := make([]byte, chunk, chunk+gcmTagLen)
	var written int64
	for i := int64(0); i < n; i++ {
		want := chunk
		last := i == n-1
		if last {
			want = size - i*chunk
		}
		if _, err := io.ReadFull(br, buf[:want]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return written, ErrSizeMismatch
			}
			return written, err
		}
		if last {
			if _, perr := br.Peek(1); perr == nil {
				return written, ErrSizeMismatch
			} else if !errors.Is(perr, io.EOF) {
				return written, perr
			}
		}
		sealed := g.Seal(buf[:0], streamNonce(prefix, uint32(i), last), buf[:want], nil)
		m, werr := w.Write(sealed)
		written += int64(m)
		if werr != nil {
			return written, werr
		}
		buf = buf[:chunk]
	}
	return written, nil
}

// StreamFileHeader is the 97-byte header of a 0x02 folder file: the magic,
// the version, the wrapped file key (exactly as 0x01 wraps it), the STREAM
// nonce prefix and the chunk size. It returns the header and the DEK and
// prefix the body is to be sealed with; the caller zeroes the DEK.
func StreamFileHeader(fmk []byte, log2 int, rnd io.Reader) (header, dek, prefix []byte, err error) {
	if !ValidChunkLog2(log2) {
		return nil, nil, nil, fmt.Errorf("e2e: unsupported chunk size 2^%d", log2)
	}
	dek, wrapIV, wrapped, err := wrapDEK(fmk, rnd)
	if err != nil {
		return nil, nil, nil, err
	}
	if prefix, err = random(rnd, streamPrefixLen); err != nil {
		clear(dek)
		return nil, nil, nil, err
	}
	h := make([]byte, headerLen)
	copy(h, MagicPrefix)
	h[8] = fileVersionStream
	copy(h[wrapIVOff:], wrapIV)
	copy(h[wrappedDEKOff:], wrapped)
	copy(h[prefixOff:], prefix)
	h[log2Off] = byte(log2)
	// [77..97) zeros
	return h, dek, prefix, nil
}

// EncryptedFileSize is the length of the encrypted file a plaintext of size
// bytes becomes: 0x01 up to MaxOneShot, 0x02 (1 MiB chunks) above it.
func EncryptedFileSize(size int64) int64 {
	if size <= MaxOneShot {
		return headerLen + size + gcmTagLen
	}
	return headerLen + StreamCiphertextSize(size, StreamChunkLog2)
}

// EncryptFile encrypts a plaintext of exactly size bytes read from r into a
// folder file written to w, under the folder key: the one-shot format (0x01)
// up to MaxOneShot - read whole, at most 200 MiB - and STREAM (0x02, 1 MiB
// chunks) above it, read and written as a stream. The same choice, the same
// bytes, as an upload in the browser.
func EncryptFile(w io.Writer, r io.Reader, size int64, fmk []byte, rnd io.Reader) error {
	if size < 0 {
		return errors.New("e2e: a negative size")
	}
	if size <= MaxOneShot {
		plain := make([]byte, size)
		if _, err := io.ReadFull(r, plain); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return ErrSizeMismatch
			}
			return err
		}
		// One byte more than promised is a size mismatch. ⚠ Any other error
		// than the end of the input is an error too, not "nothing more": for
		// an empty file this probe is the ONLY read, so a stop request
		// (ctxReader's ErrStopped) used to be swallowed here and the file in
		// flight written whole after the run was told to stop.
		var probe [1]byte
		if m, err := io.ReadFull(r, probe[:]); m > 0 {
			return ErrSizeMismatch
		} else if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		out, err := EncryptContent(fmk, plain, rnd)
		clear(plain)
		if err != nil {
			return err
		}
		_, err = w.Write(out)
		return err
	}
	return encryptStreamFile(w, r, size, fmk, StreamChunkLog2, rnd)
}

// encryptStreamFile is the 0x02 file at any chunk size (tests use small ones,
// so a few kilobytes span many chunks; writers always use StreamChunkLog2).
func encryptStreamFile(w io.Writer, r io.Reader, size int64, fmk []byte, log2 int, rnd io.Reader) error {
	h, dek, prefix, err := StreamFileHeader(fmk, log2, rnd)
	if err != nil {
		return err
	}
	defer clear(dek)
	if _, err := w.Write(h); err != nil {
		return err
	}
	_, err = EncryptStream(w, r, dek, prefix, log2, size)
	return err
}
