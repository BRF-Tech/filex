package sync

import (
	"crypto/md5"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// etagDrift returns true when the DB-side etag differs from the backend's.
//
// S3-style multipart ETags have the form `<md5>-<count>` and CANNOT be
// compared with a plain MD5. When both sides use the same multipart layout
// the strings are equal; if a backend rewrote the file with different part
// boundaries the count differs and we still detect drift.
func etagDrift(dbEtag, backendEtag string) bool {
	if dbEtag == "" || backendEtag == "" {
		return dbEtag != backendEtag
	}
	a := strings.Trim(dbEtag, `"`)
	b := strings.Trim(backendEtag, `"`)
	return a != b
}

// MultipartETag computes the S3-style multipart ETag of an io.Reader.
// `partSize` should match the upload chunk size (default 8MB).
//
// The format is `<md5_of_concatenated_part_md5s>-<part_count>`.
func MultipartETag(r io.Reader, partSize int64) (string, error) {
	if partSize <= 0 {
		partSize = 8 * 1024 * 1024
	}
	var concat []byte
	parts := 0
	buf := make([]byte, partSize)
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			h := md5.Sum(buf[:n])
			concat = append(concat, h[:]...)
			parts++
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	if parts == 0 {
		return `"d41d8cd98f00b204e9800998ecf8427e"`, nil
	}
	if parts == 1 {
		// Single part — backend returns plain md5 (no -1 suffix).
		h := md5.Sum(concat)
		return fmt.Sprintf(`"%x"`, h), nil
	}
	final := md5.Sum(concat)
	return fmt.Sprintf(`"%x-%s"`, final, strconv.Itoa(parts)), nil
}

// CountParts extracts the part count from an `<md5>-<count>` etag, or 1
// for single-part etags.
func CountParts(etag string) int {
	clean := strings.Trim(etag, `"`)
	if i := strings.LastIndexByte(clean, '-'); i > 0 {
		if n, err := strconv.Atoi(clean[i+1:]); err == nil {
			return n
		}
	}
	return 1
}
