package storage

import (
	"context"
	"time"
)

// Landed reads back what a write just left at p — its size, etag and
// modification time — from the driver's own Stat, for the caller to record on
// the node row.
//
// ⚠ The alternative every overwrite path used to take was to keep the row's
// etag, i.e. the etag of the file that had just been REPLACED. On a backend
// that reports etags (S3, WebDAV) that value is load-bearing: content search
// re-extracts a file's text only when its fingerprint moves, and the
// fingerprint is the etag (search.ContentFingerprint); clients tell a changed
// file from an unchanged one by the listing's etag; and a scan that finds the
// etag it recorded believes nothing happened.
//
// ⚠⚠ The mtime is the DRIVER's too, not time.Now(). The row is what every
// listing reports, and the storage scan later compares it with the driver: a
// handler-clock mtime that straddles a second boundary reads as drift and is
// rewritten, so the file's last_modified CHANGES with no byte of it changing
// (measured on a local storage: a desktop upload listed as ...692000 became
// ...692587 after one scan). A sync client sees that as a new server version
// and downloads its own upload back, and its `expect` precondition — the
// listing signature <size>:<mtime-ms> — no longer matches the file it wrote.
//
// When Stat fails — or answers with something that is not a file — the answer
// is the caller's size, an EMPTY etag and now. An empty etag never matches the
// backend's on the next scan, so the row is corrected there, and the search
// fingerprint falls back to size+mtime at once. It is never the old etag.
func Landed(ctx context.Context, d Driver, p string, size int64) (int64, string, time.Time) {
	if d != nil {
		if obj, err := d.Stat(ctx, p); err == nil && obj.Kind != KindDirectory {
			mtime := obj.Mtime
			if mtime.IsZero() {
				mtime = time.Now()
			}
			return obj.Size, obj.Etag, mtime
		}
	}
	return size, "", time.Now()
}
