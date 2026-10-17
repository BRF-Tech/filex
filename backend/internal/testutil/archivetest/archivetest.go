// Package archivetest builds hostile archives for tests. It imports nothing
// from filex, so the archive reader's own tests can use it too.
package archivetest

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"hash/crc32"
	"testing"
)

// LyingZip builds a ZIP of one deflated member, payload.bin, holding size
// zero bytes while its local and central headers both say it holds 100: the
// shape of a decompression bomb that lies about its size. Readers that trust
// the declared size must stop at 100 bytes.
func LyingZip(t *testing.T, size int) []byte {
	t.Helper()
	var body bytes.Buffer
	fw, err := flate.NewWriter(&body, flate.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	sum := crc32.NewIEEE()
	zero := make([]byte, 1<<20)
	for written := 0; written < size; written += len(zero) {
		_, _ = fw.Write(zero)
		_, _ = sum.Write(zero)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name: "payload.bin", Method: zip.Deflate, CRC32: sum.Sum32(),
		CompressedSize64: uint64(body.Len()), UncompressedSize64: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// RawMember is one member of RawNameZip: its name as raw BYTES, and its body.
type RawMember struct {
	Name string
	Body string
}

// RawNameZip builds a ZIP whose members carry their names as raw bytes with
// the UTF-8 flag (general purpose bit 11) CLEAR - the shape of an archive
// Windows' "Compressed folder" writes in the machine's DOS code page (IBM
// 857 on a Turkish Windows, 437 by the ZIP specification).
func RawNameZip(t *testing.T, members ...RawMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range members {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: m.Name, Method: zip.Deflate, NonUTF8: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(m.Body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
