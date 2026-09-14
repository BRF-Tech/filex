// Package handlers — quota_storages.go
//
// Endpoint:
//
//	GET /api/files/quota/storages   (auth)
//
// "How full is this drive", for somebody who is not an administrator.
//
// The admin panel has always had this number: `/api/admin/storages` enriches
// every row with `stats.total_size_bytes` (handlers/storages.go), which is what
// the Home page's storage card printed for an operator and for nobody else.
// The only other figure in reach was `/api/files/quota/me`, and that is a
// per-USER sum across every storage — printing it under one drive's name would
// be a number about the person wearing a label about the drive.
//
// So this answers the same quantity the admin row carries, for the storages the
// caller is allowed to see, and nothing about the ones they are not.
package handlers

import (
	"net/http"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
)

// storageUsageTTL is how long a storage's (files, bytes) pair is reused before
// it is recounted.
//
// The count is an aggregate over every live file row of the storage
// (`SELECT COUNT(*), SUM(size) … WHERE storage_id=? AND type='file' AND
// deleted_at IS NULL`), and Home asks for every visible storage on every load
// — so an unthrottled endpoint turns one page open into N table scans, once
// per person, on a page people leave open. The figure itself moves only when
// the sync worker writes, and it is rendered as "1.2 GB" under a drive name;
// a few seconds of staleness is invisible there, while the scan is not.
//
// ⚠ Deliberately NOT shared with the admin list. That page is read by one
// operator who has just changed something and wants to see it; this one is
// read by everybody, repeatedly, and wants to be cheap.
const storageUsageTTL = 15 * time.Second

type storageUsageEntry struct {
	files int64
	bytes int64
	at    time.Time
}

// storageUsageCache is the process-local TTL cache behind the endpoint. Keyed
// by storage id, so two people looking at the same drive pay for one count.
type storageUsageCache struct {
	mu  sync.Mutex
	m   map[int64]storageUsageEntry
	ttl time.Duration
	now func() time.Time
}

func newStorageUsageCache(ttl time.Duration) *storageUsageCache {
	return &storageUsageCache{m: map[int64]storageUsageEntry{}, ttl: ttl, now: time.Now}
}

// get returns the cached pair when it is still fresh.
func (c *storageUsageCache) get(id int64) (int64, int64, bool) {
	if c == nil || c.ttl <= 0 {
		return 0, 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[id]
	if !ok || c.now().Sub(e.at) > c.ttl {
		return 0, 0, false
	}
	return e.files, e.bytes, true
}

func (c *storageUsageCache) put(id, files, bytes int64) {
	if c == nil || c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[id] = storageUsageEntry{files: files, bytes: bytes, at: c.now()}
}

// StorageUsageRow is one drive's answer.
type StorageUsageRow struct {
	Name      string `json:"name"`
	UsedBytes int64  `json:"used_bytes"`
	FileCount int64  `json:"file_count"`
}

// AttachACL wires the RBAC resolver. nil (ACL unwired) means no grant
// filtering, matching every other file handler — but see StorageUsage: an
// unwired resolver is the single-tenant "no RBAC configured" install, where
// every enabled storage is already everybody's.
func (h *Quota) AttachACL(r *acl.Resolver) { h.ACL = r }

// StorageUsage answers per-storage usage for the signed-in caller.
//
// ⚠⚠ The filter is the SAME one the explorer's own root listing applies
// (handlers/manager.go → listVuefinder): list the enabled storages, then drop
// every one whose `acl.Set` is not `StorageVisible()`. It has to be the same
// one, because the guarantee is not "this endpoint is careful" — it is "the
// set of drives you can measure is exactly the set of drives you can open".
// Two independently-written filters would be two chances for one of them to
// answer for a drive the other hides.
//
// Two dimensions are already closed before this code runs and are named here
// so the next reader does not add a second, weaker copy of them:
//
//   - Tenancy. h.Store is the tenant-scoped store (server.go → tenantstore),
//     and ListEnabledStorages is one of the methods it confines. A tenant admin
//     never sees another provider's storages in the list at all.
//   - Root confinement. A token carrying `root:<adapter>://<rel>` (or a trusted
//     proxy's X-Filex-Root) is not looking at a storage, it is looking at one
//     folder inside one — so a whole-storage total is not its number. Rather
//     than invent a subtree total nothing else in the product reports, such a
//     caller is answered with the storage it is confined to ONLY when the
//     confinement is the storage root, and an empty list otherwise.
//
// ⚠ What this figure is NOT: it is the drive's total, not the caller's share
// of it, and item-level grants do not narrow it. Somebody granted one folder
// on an RBAC storage learns how full that storage is. That is deliberate — the
// card says "this drive is X full", which is a property of the drive, and the
// alternative (per-grant subtotals) would make the same card mean a different
// thing for every reader. The line that must not move is the storage one: a
// drive you cannot open is not reported at all.
func (h *Quota) StorageUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.UserFrom(ctx)
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}

	storages, err := h.Store.ListEnabledStorages(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Root confinement — see the header note.
	if root, confined := confine.RootFrom(ctx); confined {
		keep := storages[:0]
		if root.Rel == "" {
			for _, s := range storages {
				if s.Name == root.Adapter {
					keep = append(keep, s)
				}
			}
		}
		storages = keep
	}

	rows := make([]StorageUsageRow, 0, len(storages))
	for _, s := range storages {
		if h.ACL != nil {
			set, aerr := h.ACL.LoadSet(ctx, u, s)
			if aerr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": aerr.Error()})
				return
			}
			if set != nil && !set.StorageVisible() {
				continue
			}
		}
		files, bytes, ok := h.usage.get(s.ID)
		if !ok {
			c, sz, serr := h.Store.StorageStats(ctx, s.ID)
			if serr != nil {
				// One unreadable drive is not a reason to refuse the others:
				// the card simply falls back to naming the kind of thing, the
				// way it did before this endpoint existed.
				continue
			}
			files, bytes = c, sz
			h.usage.put(s.ID, files, bytes)
		}
		rows = append(rows, StorageUsageRow{Name: s.Name, UsedBytes: bytes, FileCount: files})
	}

	writeJSON(w, http.StatusOK, map[string]any{"storages": rows})
}
