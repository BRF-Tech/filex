package s3api

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"hash"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// ETags, and the reason this file exists at all.
//
// S3 promises that an object's ETag is the MD5 of its bytes, and clients act on
// it: restic and rclone compare it after a transfer and read a mismatch as
// corruption. Several filex drivers report one already (S3 passes the
// backend's through); `local` does not, and neither will a NAS behind it.
//
// So the choice is between three answers, and only one of them is honest:
//
//   - Synthesise something from size and mtime. ⚠ NO. It looks like an ETag,
//     compares unequal to the real MD5, and turns every verifying client into
//     a corruption report. A WRONG ETag is far worse than an absent one — this
//     is §10.2 of the gateway plan, and it is the trap that has bitten other
//     S3 implementations.
//   - Omit it always. Safe, and it costs the verification clients rely on.
//   - Compute the real MD5 on first use and remember it. Correct, and it costs
//     one read of the object.
//
// The third, bounded by a size limit, is what this does.
//
// # The cost, stated plainly
//
// A cold read of an object below the limit reads it twice: once to hash,
// because the header must be written before the body, and once to serve. After
// that it is cached until the object changes. Above the limit no ETag is
// offered at all, which is the honest refusal rather than a slow lie.
//
// ⚠ Once writes land (chunk S5) filex computes the digest as the bytes arrive,
// so anything filex itself wrote never takes the cold path. What remains is
// exactly the out-of-band case — a file dropped onto the NAS by something else
// — which is also the case where a stale stored ETag would be wrong, so
// deriving it from the current size and mtime is what keeps it honest.

// etagMaxCompute is the largest object this will hash on demand. Chosen to
// cover the sizes a backup tool uses for its packs while keeping the cold-read
// cost bounded.
const etagMaxCompute = 64 << 20 // 64 MiB

// etagCache remembers computed digests. The key includes size and mtime, so an
// object that changes out of band gets a new key rather than a stale answer —
// which is the failure mode that makes a wrong ETag dangerous in the first
// place.
type etagCache struct {
	mu sync.Mutex
	m  map[string]string
}

func (c *etagCache) get(k string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok
}

func (c *etagCache) put(k, v string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]string{}
	}
	// A crude bound. The entries are tiny and the working set is the hot
	// objects, so dropping everything is cheaper than tracking an LRU.
	if len(c.m) > 8192 {
		c.m = map[string]string{}
	}
	c.m[k] = v
}

// etagFor returns the ETag to report, computing it when the backend has none.
//
// It returns "" rather than a guess when it cannot know — a client that sees no
// ETag skips verification, while a client that sees a wrong one reports data
// loss.
func (h *Handler) etagFor(ctx context.Context, src *filebody.Source, stat storage.Object, storageID int64, key string) string {
	if stat.Etag != "" {
		return quoteETag(stat.Etag)
	}
	// ⚠ The cache is consulted BEFORE the source is required. A caller that has
	// no reader to offer (a metadata-only copy, which moves no bytes) can still
	// report the digest filex computed when the object was written; making the
	// reader mandatory first would answer "" for an object whose ETag is known.
	ck := etagKey(storageID, key, stat.Size, stat.Mtime)
	if v, ok := h.etags.get(ck); ok {
		return quoteETag(v)
	}
	if stat.Size > etagMaxCompute || src == nil {
		return ""
	}

	body, err := src.Open(ctx)
	if err != nil {
		return ""
	}
	defer body.Close()
	sum := md5.New()
	if _, err := io.Copy(sum, body); err != nil {
		// A partial read must not produce a digest: it would be a wrong ETag,
		// which is the one outcome this whole file exists to avoid.
		return ""
	}
	digest := hex.EncodeToString(sum.Sum(nil))
	h.etags.put(ck, digest)
	return quoteETag(digest)
}

// newMD5 / hexOf keep the digest plumbing in one place: the write path, the
// copy path and this file all compute the same thing the same way.
func newMD5() hash.Hash { return md5.New() }

func hexOf(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

func etagKey(storageID int64, key string, size int64, mtime time.Time) string {
	return strconv.FormatInt(storageID, 10) + "\x00" + key + "\x00" +
		strconv.FormatInt(size, 10) + "\x00" + strconv.FormatInt(mtime.UnixNano(), 10)
}
