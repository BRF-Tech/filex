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

	"github.com/brf-tech/filex/backend/internal/storage"
)

// StorageDrivers serves the driver descriptor catalogue.
type StorageDrivers struct{}

// NewStorageDrivers constructs the handler.
func NewStorageDrivers() *StorageDrivers { return &StorageDrivers{} }

// List returns every registered driver's descriptor, driver-name sorted.
func (h *StorageDrivers) List(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, storage.Descriptors())
}
