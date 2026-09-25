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
