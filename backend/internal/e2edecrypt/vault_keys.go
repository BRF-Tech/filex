package e2edecrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// The keys of a vault (encryption level 3) - docs/E2E-VAULT-FORMAT.md →
// "Keys and the nonce rule". The browser's side is
// packages/core/src/lib/e2evault/keys.ts.
//
//	FMK (32 bytes, from a key slot)
//	 |- HKDF-SHA-256(FMK, salt = vault id, "filex-vault-index-v1"   ‖ seal id)    -> one index file
//	 '- HKDF-SHA-256(FMK, salt = vault id, "filex-vault-content-v1" ‖ content id) -> one file version
//
// Every key encrypts exactly one plaintext: an index key seals one index file
// under the all-zero nonce, a content key one version of one file as a
// STREAM with a 7-zero-byte prefix. The ids are 16 random bytes each, so a
// key is never used twice. The FMK itself never encrypts anything.

const (
	vaultIndexLabel   = "filex-vault-index-v1"
	vaultContentLabel = "filex-vault-content-v1"
	// vaultIDLen is the length of every id of a vault: the vault's own, a
	// pack's, a seal's, a content's.
	vaultIDLen = 16
	// VaultChunkLog2 is the STREAM chunk size vault writers use (2^20).
	VaultChunkLog2 = StreamChunkLog2
)

// VaultKeys derives the keys of one vault from its folder master key.
type VaultKeys struct {
	fmk []byte
	id  [16]byte
}

// NewVaultKeys keeps a copy of fmk; Wipe zeroes it.
func NewVaultKeys(fmk []byte, vaultID [16]byte) (*VaultKeys, error) {
	if len(fmk) != fmkLen {
		return nil, fmt.Errorf("e2edecrypt: a vault key needs a %d-byte folder key, not %d bytes", fmkLen, len(fmk))
	}
	k := &VaultKeys{fmk: make([]byte, fmkLen), id: vaultID}
	copy(k.fmk, fmk)
	return k, nil
}

// ID is the vault id the keys are salted with.
func (k *VaultKeys) ID() [16]byte { return k.id }

// Wipe drops the folder key from memory. The keys are useless afterwards.
func (k *VaultKeys) Wipe() {
	if k == nil {
		return
	}
	clear(k.fmk)
	k.fmk = nil
}

// IndexKey is the key of the index file sealed under sealID.
func (k *VaultKeys) IndexKey(sealID [16]byte) ([]byte, error) {
	return k.derive(vaultIndexLabel, sealID)
}

// ContentKey is the key of the file version whose content id is contentID.
func (k *VaultKeys) ContentKey(contentID [16]byte) ([]byte, error) {
	return k.derive(vaultContentLabel, contentID)
}

func (k *VaultKeys) derive(label string, id [16]byte) ([]byte, error) {
	if k == nil || len(k.fmk) != fmkLen {
		return nil, errors.New("e2edecrypt: the vault is locked (its key was dropped)")
	}
	return hkdf.Key(sha256.New, k.fmk, k.id[:], label+string(id[:]), 32)
}

// vaultAEAD is AES-256-GCM under a derived key.
func vaultAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// contentAEAD is the AEAD of one file version.
func (k *VaultKeys) contentAEAD(contentID [16]byte) (cipher.AEAD, error) {
	key, err := k.ContentKey(contentID)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	return vaultAEAD(key)
}

// vaultChunkNonce is the STREAM nonce of chunk i of a vault file: 7 zero
// bytes (the key is used once, so the prefix carries nothing) ‖ i ‖ last.
func vaultChunkNonce(i uint32, last bool) []byte {
	n := make([]byte, ivLen)
	binary.BigEndian.PutUint32(n[streamPrefixLen:], i)
	if last {
		n[ivLen-1] = 1
	}
	return n
}
