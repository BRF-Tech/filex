package e2edecrypt

import (
	"fmt"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

// Packs - docs/E2E-VAULT-FORMAT.md → "Packs". A pack is exactly 2^pack bytes:
//
//	[0..8)  magic "filexvlt"   [8] 0x01   [9] 'P'   [10] pack size, log2
//	[11..16) zero              [16..32) pack id (= its name)
//	[32..)  extents of encrypted file contents, back to back, then random
//	        padding (never zeros: zeros would show how full a pack is)
//
// A pack is uploaded whole, once, and never appended to, rewritten or renamed.
// It has no key of its own: copying a file's ciphertext into another pack
// encrypts nothing.

// VaultPackDataArea is how many bytes of a pack of 2^packLog2 bytes hold data.
func VaultPackDataArea(packLog2 int) int64 { return int64(1)<<packLog2 - e2e.VaultPackHeaderLen }

// vaultPack is a writer's open pack: the whole pack in memory, header first.
type vaultPack struct {
	id   [16]byte
	buf  []byte
	used int // data bytes written after the header
}

func newVaultPack(id [16]byte, packLog2 int) *vaultPack {
	p := &vaultPack{id: id, buf: make([]byte, 1<<packLog2)}
	writeVaultPackHeader(p.buf, id, packLog2)
	return p
}

func writeVaultPackHeader(b []byte, id [16]byte, packLog2 int) {
	copy(b, e2e.VaultMagicPrefix)
	b[8] = byte(e2e.VaultFormat)
	b[9] = e2e.VaultKindPack
	b[10] = byte(packLog2)
	clear(b[11:16])
	copy(b[16:32], id[:])
}

// room is how many data bytes still fit.
func (p *vaultPack) room() int { return len(p.buf) - e2e.VaultPackHeaderLen - p.used }

// setID renames the pack (its header) before it is uploaded again.
func (p *vaultPack) setID(id [16]byte) {
	p.id = id
	copy(p.buf[16:32], id[:])
}

// CheckVaultPack checks a whole pack against its name and the vault's pack
// size - what a reader that holds the whole pack does (`filex decrypt` on a
// copy, `filex vault prune`). Any mismatch is damage.
func CheckVaultPack(b []byte, id [16]byte, packLog2 int) error {
	where := e2e.VaultPackPath(id)
	if int64(len(b)) != int64(1)<<packLog2 {
		return vaultDamage(where, "%d bytes, a pack of this vault is %d", len(b), int64(1)<<packLog2)
	}
	return checkVaultPackHeader(b[:e2e.VaultPackHeaderLen], id, packLog2)
}

// checkVaultPackHeader checks the 32-byte header of a pack.
func checkVaultPackHeader(h []byte, id [16]byte, packLog2 int) error {
	if err := e2e.CheckVaultPackHeader(h, id, packLog2); err != nil {
		return vaultDamage(e2e.VaultPackPath(id), "%s", err.Error())
	}
	return nil
}

// vaultPackName is a pack id as it is written in a pack's name.
func vaultPackName(id [16]byte) string { return fmt.Sprintf("%x", id[:]) }
