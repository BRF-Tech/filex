package handlers

import (
	"context"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
)

// LazyCatalogue is what the file handlers ask the sync worker about a
// storage's catalogue (internal/sync lazy_api.go; docs/LAZY-CATALOGUE.md).
// *sync.Worker satisfies it. nil everywhere means "the catalogue is whatever
// the last scan left", which is how every handler behaved before it existed.
type LazyCatalogue interface {
	// CatalogueOpened: somebody listed dir of a lazy storage. Never blocks.
	CatalogueOpened(storageID int64, dir string)
	// CatalogueCurrent: is the storage lazy, and does its catalogue vouch
	// for dir right now?
	CatalogueCurrent(storageID int64, dir string) (lazy, current bool)
	// CatalogueCoverage: how much of st the catalogue covers; nil = all.
	CatalogueCoverage(ctx context.Context, st *model.Storage) *syncpkg.CatalogueCoverage
	// CatalogueSizePartial: which of these folders' sizes leave something out.
	CatalogueSizePartial(ctx context.Context, storageID int64, dirs []string) map[string]bool
	// CatalogueSubtree: catalogue everything under dir (a desktop sync pair).
	CatalogueSubtree(storageID int64, dir string)
	// NoteActivity: somebody is using the storage (slows the filler).
	NoteActivity(storageID int64)
}

// AttachLazy wires the lazy catalogue. nil keeps the handler as it was.
func (h *Manager) AttachLazy(l LazyCatalogue) { h.Lazy = l }

// coverageOf is how much of s its catalogue covers, nil when all of it.
func (h *Manager) coverageOf(ctx context.Context, s *model.Storage) *syncpkg.CatalogueCoverage {
	if h.Lazy != nil {
		return h.Lazy.CatalogueCoverage(ctx, s)
	}
	if s.LastSyncAt == nil && s.SyncMode != model.SyncModeLazy {
		return &syncpkg.CatalogueCoverage{Reason: syncpkg.CoverageFirstScan}
	}
	return nil
}

// catalogueVouches reports whether the catalogue can be trusted to list rel
// of s as it stands, and — on a lazy storage — tells the catalogue somebody
// opened it (which queues the folder for cataloguing when it cannot vouch).
//
//   - A lazy storage vouches for a folder only while it is watched and its
//     last reconcile held nothing back.
//   - Any other storage vouches once its first full scan has finished. Before
//     that a partly catalogued folder used to be listed from whatever the scan
//     had reached — the root of a big storage showed a handful of entries for
//     as long as the first scan ran (issue #45).
func (h *Manager) catalogueVouches(s *model.Storage, rel string) bool {
	if h.Lazy != nil {
		if lazy, current := h.Lazy.CatalogueCurrent(s.ID, rel); lazy {
			h.Lazy.CatalogueOpened(s.ID, rel)
			return current
		}
	}
	return s.LastSyncAt != nil
}

// vfIndexMerged lists rel from the storage itself, with the catalogue laid
// over it: an entry that has a row is projected FROM the row (its id, owner,
// thumbnail, badges), an entry that has none from the driver. It answers the
// folders the catalogue cannot vouch for (catalogueVouches).
//
// Returns false when the driver cannot list the folder; the caller then
// answers from the catalogue, which is better than nothing.
func (h *Manager) vfIndexMerged(w http.ResponseWriter, r *http.Request, s *model.Storage, rel, dirname string,
	storageNames []string, dirsOnly bool, set *acl.Set, nodes []*model.Node) bool {
	if h.StorageResolver == nil {
		return false
	}
	drv, err := h.StorageResolver(s.ID)
	if err != nil {
		return false
	}
	clean := strings.Trim(rel, "/")
	objs, err := drv.List(r.Context(), clean)
	if err != nil {
		return false
	}
	rows, diskOnly := mergeListing(nodes, objs)
	for _, n := range rows {
		if n.Type != model.NodeTypeFile {
			continue
		}
		if t, terr := h.Store.GetThumbnail(r.Context(), n.ID); terr == nil && t != nil {
			n.Thumb = t
		}
	}
	files := projectFileNodes(s.Name, rows, dirsOnly, set, h.ThumbSigner, h.hydrateOwnerNames(r.Context(), rows))
	files = append(files, projectDriverObjects(s.Name, clean, diskOnly, dirsOnly, set)...)
	sort.SliceStable(files, func(i, j int) bool {
		di, dj := files[i]["type"] == "dir", files[j]["type"] == "dir"
		if di != dj {
			return di
		}
		bi, _ := files[i]["basename"].(string)
		bj, _ := files[j]["basename"].(string)
		return bi < bj
	})
	h.respondIndex(w, r, s, rel, dirname, storageNames, dirsOnly, set, files, objs)
	return true
}

// mergeListing lays the catalogue rows of a folder over its listing on disk.
//
//   - An entry with a row of the same kind is the row. When the row has drifted
//     from the disk by the scan's own rule (sync.ObjectDrift), a COPY of it
//     carries the disk's size, date and etag: the person sees the file that is
//     there, and the upload precondition accepts that signature
//     (uploadExpectHolds). The stored row is the reconcile's to update.
//   - An entry with no row (or a row of another kind) is the disk's.
//   - A row with no entry is dropped — it is not there — unless it is an
//     upload whose bytes are still on their way to the storage.
func mergeListing(nodes []*model.Node, objs []storage.Object) (rows []*model.Node, diskOnly []storage.Object) {
	byName := make(map[string]*model.Node, len(nodes))
	for _, n := range nodes {
		if n.DeletedAt == nil {
			byName[n.Name] = n
		}
	}
	for _, o := range objs {
		n, ok := byName[o.Name]
		if !ok || !sameKind(n, o) {
			diskOnly = append(diskOnly, o)
			continue
		}
		delete(byName, o.Name)
		if n.Type == model.NodeTypeFile && syncpkg.ObjectDrift(n, o) {
			c := *n
			c.Size = o.Size
			if !o.Mtime.IsZero() {
				mt := o.Mtime
				c.BackendMtime = &mt
			}
			if o.Etag != "" {
				c.Etag = o.Etag
			}
			rows = append(rows, &c)
			continue
		}
		rows = append(rows, n)
	}
	for _, n := range nodes {
		if _, left := byName[n.Name]; left && n.DeletedAt == nil && n.Type == model.NodeTypeFile &&
			n.TransferState != "" && n.TransferState != model.TransferStateStored {
			rows = append(rows, n)
		}
	}
	return rows, diskOnly
}

func sameKind(n *model.Node, o storage.Object) bool {
	switch o.Kind {
	case storage.KindDirectory:
		return n.Type == model.NodeTypeDirectory
	case storage.KindSymlink:
		return n.Type == model.NodeTypeSymlink
	default:
		return n.Type == model.NodeTypeFile
	}
}

// annotateSizes marks the folder rows whose size is a lower bound.
//
//   - A folder listed from the disk with no row of its own has no size the
//     catalogue knows: `size` 0 and `size_partial`, never the directory
//     entry's own few kilobytes.
//   - While a storage's first scan runs, every folder's size is partial.
//   - On a lazy storage whose catalogue is incomplete, a folder's size is
//     partial when the folder itself or anything below it is not catalogued.
func (h *Manager) annotateSizes(ctx context.Context, s *model.Storage, files []map[string]any, cov *syncpkg.CatalogueCoverage) {
	var (
		dirs  []string
		byDir = map[string]map[string]any{}
	)
	for _, e := range files {
		if e["type"] != "dir" {
			continue
		}
		if _, catalogued := e["id"]; !catalogued {
			e["size"] = int64(0)
			e["size_partial"] = true
			continue
		}
		if cov == nil {
			continue
		}
		if cov.Reason == syncpkg.CoverageFirstScan {
			e["size_partial"] = true
			continue
		}
		p, _ := e["path"].(string)
		_, childRel := splitAdapterPath(p)
		key := path.Clean("/" + childRel)
		dirs = append(dirs, key)
		byDir[key] = e
	}
	if len(dirs) == 0 || h.Lazy == nil {
		return
	}
	for key, partial := range h.Lazy.CatalogueSizePartial(ctx, s.ID, dirs) {
		if e, ok := byDir[key]; ok && partial {
			e["size_partial"] = true
		}
	}
}
