package model

import "time"

// CatalogueFolderState is where one folder of a lazily catalogued storage
// stands (migration 00059, docs/LAZY-CATALOGUE.md). A folder with no row at all
// is not catalogued.
type CatalogueFolderState string

const (
	// FolderUncatalogued: seen in its parent's listing, never listed itself.
	// These rows are the background filler's work list.
	FolderUncatalogued CatalogueFolderState = "uncatalogued"
	// FolderCatalogued: its listing has been applied to the catalogue.
	FolderCatalogued CatalogueFolderState = "catalogued"
	// FolderWatched: catalogued, and under an fsnotify watch in the running
	// process. Demoted to catalogued (with ReconcileOnOpen) at every start.
	FolderWatched CatalogueFolderState = "watched"
)

// CatalogueFolder is one row of catalogue_folders.
type CatalogueFolder struct {
	StorageID int64
	PathHash  string
	// Path is canonical: "/" for the storage root, "/a/b" below it.
	Path  string
	Depth int
	State CatalogueFolderState
	// ReconciledAt is when the folder's last complete listing was applied.
	ReconciledAt *time.Time
	// VisitedAt is when a person last opened the folder.
	VisitedAt *time.Time
	// WatchedAt is when its current watch was placed; nil = no watch.
	WatchedAt *time.Time
	// ReconcileOnOpen: the watch was evicted, expired or lost with the
	// process, so the catalogue may have drifted from the disk.
	ReconcileOnOpen bool
	// Entries is how many entries the last listing held; HeldBack how many
	// deletions its guard refused to make.
	Entries  int
	HeldBack int
}

// Catalogued reports whether the folder's listing has been applied at least
// once.
func (f *CatalogueFolder) Catalogued() bool {
	return f != nil && (f.State == FolderCatalogued || f.State == FolderWatched) && f.ReconciledAt != nil
}

// CatalogueFolderFilter selects catalogue_folders rows for the lazy
// catalogue's work lists.
type CatalogueFolderFilter struct {
	// State, when set, keeps rows in that state only.
	State CatalogueFolderState
	// ReconciledBefore, when set, keeps catalogued rows whose last listing is
	// older than it — oldest first — instead of the shallowest-first frontier
	// order.
	ReconciledBefore *time.Time
	// Under, when set, keeps the folder itself and every folder below it.
	Under string
	Limit int
}

// CatalogueCounts is how far a storage's lazy catalogue has come.
type CatalogueCounts struct {
	Uncatalogued int64 `json:"uncatalogued"`
	Catalogued   int64 `json:"catalogued"`
	Watched      int64 `json:"watched"`
	// HeldBack counts folders whose last reconcile refused a deletion.
	HeldBack int64 `json:"held_back"`
	// RootCatalogued: the storage root's listing has been applied.
	RootCatalogued bool `json:"root_catalogued"`
}
