package e2edecrypt

import (
	"bytes"
	"errors"
	"fmt"
)

// File content — the 97-byte `filexe2e` header followed by AES-GCM ciphertext.
//
//	[0..8)   magic "filexe2e"
//	[8]      version 0x01 (0x02 is a STREAM body: stream.go)
//	[9..21)  wrapIV (12B)
//	[21..69) wrappedDEK = AES-GCM(FMK, wrapIV, DEK) (32B + 16B tag)
//	[69..81) dataIV (12B)
//	[81..97) reserved (zeros)
//	[97..)   AES-GCM(DEK, dataIV, content) (+16B tag)

// MagicPrefix starts every encrypted file.
var MagicPrefix = []byte("filexe2e")

const (
	fileVersion   = 1
	headerLen     = 97
	wrapIVOff     = 9
	wrappedDEKOff = 21
	wrappedDEKLen = 48
	dataIVOff     = 69
	gcmTagLen     = 16
)

// HasMagic reports whether b starts with the encrypted-file magic.
func HasMagic(b []byte) bool { return bytes.HasPrefix(b, MagicPrefix) }

// ErrContent: the content failed authentication — the file is damaged or was
// tampered with, or it belongs to another folder.
var ErrContent = errors.New("content failed authentication (damaged, tampered with, or from another folder)")

// DecryptContent opens one encrypted file with the folder master key.
func DecryptContent(fmk, data []byte) ([]byte, error) {
	if !HasMagic(data) || len(data) < headerLen+gcmTagLen {
		return nil, fmt.Errorf("not a complete encrypted file: %w", ErrContent)
	}
	if data[8] == fileVersionStream {
		// A 0x02 (STREAM) file held in memory.
		var out bytes.Buffer
		if err := DecryptFileStream(&out, bytes.NewReader(data), fmk, nil); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
	if data[8] != fileVersion {
		return nil, fmt.Errorf("unsupported encrypted-file version %d", data[8])
	}
	dek, ok := gcmOpen(fmk, data[wrapIVOff:wrapIVOff+ivLen], data[wrappedDEKOff:wrappedDEKOff+wrappedDEKLen])
	if !ok || len(dek) != 32 {
		return nil, fmt.Errorf("the file key does not unwrap: %w", ErrContent)
	}
	plain, ok := gcmOpen(dek, data[dataIVOff:dataIVOff+ivLen], data[headerLen:])
	for i := range dek {
		dek[i] = 0
	}
	if !ok {
		return nil, ErrContent
	}
	return plain, nil
}
