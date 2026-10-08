package realtime

import "testing"

// A vault's frames go to the vault folder's room, each client with its own
// spelling of it, and to nobody else.
func TestHubVaultFrames(t *testing.T) {
	h := NewHub()
	in := NewClient(1, "Ayşe", 8)
	embedded := NewClient(2, "Can", 8)
	beside := NewClient(3, "Deniz", 8)
	h.Subscribe(in, 3, "Kasa", "depo://Kasa")
	h.Subscribe(embedded, 3, "/Kasa/", "Kasa")
	h.Subscribe(beside, 3, "", "depo://")
	drainAll(in)
	drainAll(embedded)
	drainAll(beside)

	h.EmitVault(3, "Kasa", VaultEvent{Type: VaultGenerationEvent, Generation: 4})
	got := drain(t, in)
	if got["type"] != "vault.generation" || got["path"] != "depo://Kasa" || got["generation"] != float64(4) {
		t.Fatalf("generation frame: %v", got)
	}
	if got := drain(t, embedded); got["path"] != "Kasa" {
		t.Fatalf("the embedded explorer's own path: %v", got)
	}
	if len(beside.Send) != 0 {
		t.Fatal("the folder above the vault was told about its commits")
	}

	h.EmitVault(3, "/Kasa", VaultEvent{Type: VaultLockEvent, Held: true, Holder: &VaultHolder{Name: "Ayşe", Client: "web", Label: "Firefox"}})
	got = drain(t, in)
	holder, _ := got["holder"].(map[string]any)
	if got["type"] != "vault.lock" || got["held"] != true || holder["name"] != "Ayşe" || holder["client"] != "web" {
		t.Fatalf("lock frame: %v", got)
	}
	h.EmitVault(3, "Kasa", VaultEvent{Type: VaultLockEvent, Held: false, Holder: &VaultHolder{Name: "Ayşe"}})
	got = drain(t, in)
	if got["held"] != false || got["holder"] != nil {
		t.Fatalf("a free vault names no holder: %v", got)
	}

	// Another storage, an unknown frame type, an empty room: nothing.
	drainAll(embedded)
	h.EmitVault(4, "Kasa", VaultEvent{Type: VaultGenerationEvent, Generation: 1})
	h.EmitVault(3, "Kasa", VaultEvent{Type: "vault.other"})
	h.EmitVault(3, "Bos", VaultEvent{Type: VaultGenerationEvent, Generation: 1})
	if len(in.Send) != 0 || len(embedded.Send) != 0 {
		t.Fatal("a frame reached a room it was not for")
	}
}
