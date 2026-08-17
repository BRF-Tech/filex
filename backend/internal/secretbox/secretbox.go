// Package secretbox encrypts the secrets filex must be able to READ BACK.
//
// # Why this exists at all
//
// Almost every credential filex stores is one-way hashed: account passwords are
// bcrypt, API tokens are sha256 (apitoken.go:153). Verification only ever needs
// to compare, so the plaintext is never needed again and never kept.
//
// The protocol gateway breaks that. SigV4 does not send the secret — it sends
// an HMAC chain computed FROM the secret, so the server must derive the same
// chain, which means holding the secret in recoverable form. NTLMv2 is the same
// shape for a different reason (it needs MD4 of the password). No amount of
// code makes a one-way hash verify either of them.
//
// So the choice is not "hash or encrypt". It is "plaintext in a column, or
// encrypted with a key that lives somewhere else", and the owner chose the
// second (2026-08-16).
//
// # What it actually buys, stated honestly
//
// One thing: a leaked DATABASE is not a leaked set of secrets. That is not
// hypothetical here — the database travels, by design, into Backrest snapshots,
// an S3 mirror and a DR restore on another machine, and this month a stray
// 15 GB file proved how far those copies reach. The key is supplied through the
// environment and stays on the host.
//
// It buys nothing at all against an attacker who already has the host, since
// the process must be able to decrypt. Anyone treating this as more than
// "backups no longer carry live credentials" is mis-reading it.
//
// # Deliberately NOT included
//
// The existing `storages.config.secret_key` values are left in plaintext for
// now (the owner picked the narrow option). That inconsistency is recorded
// rather than hidden: this package is the mechanism, and pointing the storage
// configs at it later is a migration, not a redesign.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Errors callers distinguish. Everything else is a programming mistake.
var (
	// ErrNoKey means the box was built without a key. Encryption fails loudly
	// rather than silently storing plaintext — a "temporarily unencrypted"
	// secret is one nobody ever goes back for.
	ErrNoKey = errors.New("secretbox: no encryption key configured")
	// ErrCorrupt covers a ciphertext that is malformed, truncated, or was
	// sealed under a different key. They are one error on purpose: the caller's
	// action ("this credential can no longer be used — issue a new one") is the
	// same, and distinguishing them would leak whether a guess got the format
	// right.
	ErrCorrupt = errors.New("secretbox: ciphertext cannot be opened")
)

// prefix marks a value this package produced. It exists so a column can hold
// both encrypted and legacy plaintext values during a migration and each row
// can be told apart WITHOUT guessing — a decrypt-and-fall-back-on-failure
// heuristic would silently treat a corrupt ciphertext as a valid plaintext
// secret, and the first sign of that is a credential that authenticates
// nobody.
const prefix = "enc:v1:"

// Box seals and opens secrets with one key.
type Box struct {
	aead cipher.AEAD
}

// New derives a Box from the configured key material.
//
// The key is hashed to 32 bytes rather than required to BE 32 bytes, so an
// operator can use a passphrase from a password manager instead of hunting for
// a base64 generator. sha256 is enough here: the input is a high-entropy secret
// from a config file, not a human-chosen password being stretched.
//
// An empty key returns a usable Box that refuses to Seal (ErrNoKey) and passes
// plaintext through Open unchanged, so an install that has not configured a key
// keeps working for everything that does not need one.
func New(key string) (*Box, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return &Box{}, nil
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("secretbox: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Enabled reports whether a key was configured.
func (b *Box) Enabled() bool { return b != nil && b.aead != nil }

// Seal encrypts a secret for storage. The nonce is random per call and stored
// with the ciphertext, so sealing the same secret twice gives different bytes —
// which is what stops the column from revealing that two accounts share a
// secret.
func (b *Box) Seal(plaintext string) (string, error) {
	if !b.Enabled() {
		return "", ErrNoKey
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("secretbox: nonce: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return prefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Open decrypts a stored value.
//
// A value without the prefix is returned as-is. That is what lets a column hold
// legacy plaintext next to encrypted rows during a migration, and it is safe
// precisely because the prefix is explicit: nothing is ever guessed.
func (b *Box) Open(stored string) (string, error) {
	if !strings.HasPrefix(stored, prefix) {
		return stored, nil
	}
	if !b.Enabled() {
		// The row is encrypted and we have no key. Saying "corrupt" would send
		// an operator hunting for data damage; the actual cause is a missing
		// FILEX_SECRET_KEY, and that is what they need to hear.
		return "", ErrNoKey
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(stored, prefix))
	if err != nil {
		return "", ErrCorrupt
	}
	n := b.aead.NonceSize()
	if len(raw) < n {
		return "", ErrCorrupt
	}
	out, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", ErrCorrupt
	}
	return string(out), nil
}

// IsSealed reports whether a stored value was produced by this package. Used by
// a migration to tell the rows it still has to convert from the ones it has.
func IsSealed(stored string) bool { return strings.HasPrefix(stored, prefix) }
