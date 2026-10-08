package realtime

import "encoding/json"

// ── A vault's own news (docs/E2E-VAULT-FORMAT.md → Audit and events) ─────
//
// A vault's folder listing does not change when it is written: its packs and
// index files live under `v/`, and an explorer shows the tree from the index.
// So a vault has two frames of its own, sent to everyone viewing the vault
// folder (its room - they passed the folder's access check when they joined):
//
//   - `vault.generation` after a commit: {path, generation}. A reader loads
//     the new index; a writer that does not hold the lock knows its copy is
//     old.
//   - `vault.lock` when the write lock changes: {path, held, holder}. The
//     explorer's strip says who is writing, or that nobody is.
//
// Like every other frame, `path` is each client's OWN spelling of the room it
// subscribed to, and a stalled reader misses a frame rather than blocking the
// writer: clients also ask `state` every 30 seconds while a vault is open.

// VaultHolder is who holds a vault's write lock, as a frame names it.
type VaultHolder struct {
	Name   string `json:"name"`
	Client string `json:"client"`
	Label  string `json:"label,omitempty"`
}

// VaultEvent is one vault frame: Generation set for `vault.generation`, Held
// (and Holder while held) for `vault.lock`.
type VaultEvent struct {
	Type       string       `json:"type"` // "vault.generation" | "vault.lock"
	Generation uint64       `json:"generation,omitempty"`
	Held       bool         `json:"held"`
	Holder     *VaultHolder `json:"holder,omitempty"`
}

// The two frame types.
const (
	VaultGenerationEvent = "vault.generation"
	VaultLockEvent       = "vault.lock"
)

type wireVaultGeneration struct {
	Type       string `json:"type"`
	Path       string `json:"path"`
	Generation uint64 `json:"generation"`
}

type wireVaultLock struct {
	Type   string       `json:"type"`
	Path   string       `json:"path"`
	Held   bool         `json:"held"`
	Holder *VaultHolder `json:"holder"`
}

// EmitVault sends ev to every client viewing the vault folder (storageID,
// dir). A room nobody is in costs nothing.
func (h *Hub) EmitVault(storageID int64, dir string, ev VaultEvent) {
	key := RoomKey(storageID, dir)
	h.mu.Lock()
	defer h.mu.Unlock()
	rm := h.rooms[key]
	if rm == nil {
		return
	}
	for c := range rm.clients {
		var (
			frame []byte
			err   error
		)
		switch ev.Type {
		case VaultGenerationEvent:
			frame, err = json.Marshal(wireVaultGeneration{Type: ev.Type, Path: c.path, Generation: ev.Generation})
		case VaultLockEvent:
			holder := ev.Holder
			if !ev.Held {
				holder = nil
			}
			frame, err = json.Marshal(wireVaultLock{Type: ev.Type, Path: c.path, Held: ev.Held, Holder: holder})
		default:
			return
		}
		if err != nil {
			continue
		}
		trySend(c, frame)
	}
}
