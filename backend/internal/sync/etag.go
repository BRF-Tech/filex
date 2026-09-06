package sync

import (
	"crypto/md5"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
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

// mtimeGranularity is the resolution at which two modification times are
// judged to be the same instant.
//
// One second, because every layer between the file and the comparison rounds
// somewhere: Postgres stores `TIMESTAMPTZ` to the microsecond while a local
// stat reports nanoseconds, FTP's `MDTM` has no sub-second field at all, and
// FAT keeps two-second steps. Comparing at a finer resolution than the
// coarsest link in that chain does not detect more changes — it reports drift
// on EVERY pass for files nothing touched, which on an instance with antivirus
// enabled is one scan per file per pass, forever.
const mtimeGranularity = time.Second

// objectDrift reports whether the object a backend just listed differs from
// the row the catalogue holds for it.
//
// The etag is the authoritative answer and is used whenever both sides have
// one. Most drivers do not: **only S3 and WebDAV report an etag at all**, so
// on local, FTP, SFTP and SMB storages `etagDrift("", "")` was false on every
// pass, forever. A file replaced underneath filex looked unchanged, which left
// its size stale in the catalogue, its extracted text stale in the search
// index, and — since the walk began queueing antivirus scans — meant a clean
// file could be swapped for an infected one and nothing would ever read it
// again. The sync exists precisely to discover changes filex did not make, so
// on those drivers it was doing half its job.
//
// The fallback is size + modification time, which is what every one of those
// drivers does report and what `rsync` compares by default. It is cheap: both
// fields are already in the listing the walk just made, so a full walk costs
// exactly what it cost before — no extra request, no read of the bytes.
//
// What it catches: an ordinary edit (mtime moves), a same-size rewrite (mtime
// moves), a file that grew or shrank even with its mtime preserved, and a
// restore from backup that moves the mtime BACKWARDS (the comparison is
// inequality, not "newer than").
//
// What slips through, honestly: a replacement that preserves BOTH the size and
// the modification time — `cp -p` of a same-size file, `rsync --times` over
// one — and a rewrite that lands in the same clock second as the mtime already
// recorded, with the size unchanged. Catching those needs the content, and
// hashing every file on every pass would turn a walk of 20 000 files into
// something nobody can run. This is the same trade-off filex's own folder sync
// documents in SYNC.md.
func objectDrift(n *model.Node, obj storage.Object) bool {
	if n == nil {
		return false
	}
	if obj.Etag != "" {
		// The backend has an opinion: use it. That covers the S3/WebDAV case
		// and the one-time backfill of a row catalogued before the driver
		// reported etags (empty vs non-empty reads as drift, exactly as it
		// always has, and the row carries the etag from then on).
		return etagDrift(n.Etag, obj.Etag)
	}

	// No etag from the backend. A directory has no content of its own to
	// drift, and its row does NOT hold what the listing reports: sync caches
	// each folder's RECURSIVE size there so the explorer can show folder
	// sizes, while a local stat reports the directory entry's own few
	// kilobytes. Comparing those two would report drift on every folder on
	// every pass.
	if n.Type != model.NodeTypeFile {
		return false
	}
	if n.Size != obj.Size {
		return true
	}
	if n.BackendMtime == nil || obj.Mtime.IsZero() {
		// No baseline to compare against — a row first synced by a version
		// that did not record mtime, or a driver that reports none. Reading
		// that as drift would re-scan every file on the storage once per pass
		// forever; the walk backfills the missing mtime instead.
		return false
	}
	return !n.BackendMtime.UTC().Truncate(mtimeGranularity).
		Equal(obj.Mtime.UTC().Truncate(mtimeGranularity))
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
