// Package handlers — storage_drivers.go
//
//	GET /api/admin/storage-drivers — the config contract of every
//	registered storage driver (see storage/descriptor.go).
//
// Why its own admin route instead of a field on /api/capabilities:
// capabilities is public (embedders and the share/drop pages fetch it
// without a session) and already hot; driver config schemas — including
// which fields hold credentials, and the operator-facing hints for them —
// are only useful to an admin building a storage. `storage_drivers:
// []string` stays on capabilities untouched for every existing caller.
package handlers

import (
	"net/http"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// StorageDrivers serves the driver descriptor catalogue.
type StorageDrivers struct{}

// NewStorageDrivers constructs the handler.
func NewStorageDrivers() *StorageDrivers { return &StorageDrivers{} }

// List returns every registered driver's descriptor, driver-name sorted.
//
// A driver a storage may be catalogued lazily on (model.LazyDrivers) also
// carries `lazy_fields`, the settings of sync_mode `lazy`; the storage form
// offers that mode only where they are (docs/LAZY-CATALOGUE.md).
func (h *StorageDrivers) List(w http.ResponseWriter, r *http.Request) {
	out := storage.Descriptors()
	for i := range out {
		if model.ValidateSyncModeFor(model.SyncModeLazy, out[i].Driver) == nil {
			out[i].LazyFields = storage.LazyFields()
		}
	}
	writeJSON(w, http.StatusOK, out)
}
