package plugin

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// The detached-signature scheme both plugin kinds share: an ed25519 signature
// over the LOWER-HEX sha256 of the artefact (binary or wasm module), supplied
// as hex or standard base64, verified against the keys in
// FILEX_PLUGIN_TRUSTED_KEYS. Exported so internal/wasmplugin verifies exactly
// what internal/plugin verifies — one rule, one test suite (signature_test.go).

// ErrSignatureRequired / ErrSignatureInvalid are what VerifyDetached returns
// (wrapped in RejectedError by the storage-plugin manager); callers switch on
// them to pick an error code rather than reading the message.
var (
	ErrSignatureRequired = errors.New("signature required")
	ErrSignatureInvalid  = errors.New("signature invalid")
)

// ParsePublicKey accepts an ed25519 key as hex or standard base64.
func ParsePublicKey(k string) (ed25519.PublicKey, error) {
	return parsePublicKey(k)
}

// ParsePublicKeys parses every key, refusing the whole list on the first bad
// one so a typo cannot silently shrink the trust set.
func ParsePublicKeys(keys []string) ([]ed25519.PublicKey, error) {
	out := make([]ed25519.PublicKey, 0, len(keys))
	for _, k := range keys {
		pub, err := parsePublicKey(k)
		if err != nil {
			return nil, fmt.Errorf("trusted key %q: %w", short(k), err)
		}
		out = append(out, pub)
	}
	return out, nil
}

// VerifyDetached checks signature (hex or base64) over the lower-hex sha256
// against the trusted keys. No trusted keys → nothing is required and nil is
// returned; trusted keys and no signature → ErrSignatureRequired; otherwise
// ErrSignatureInvalid unless one key verifies.
func VerifyDetached(trusted []ed25519.PublicKey, sha, signature string) error {
	if len(trusted) == 0 {
		return nil
	}
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return ErrSignatureRequired
	}
	sig, err := hex.DecodeString(signature)
	if err != nil {
		if sig, err = base64.StdEncoding.DecodeString(signature); err != nil {
			return fmt.Errorf("%w: neither hex nor base64", ErrSignatureInvalid)
		}
	}
	for _, pub := range trusted {
		if ed25519.Verify(pub, []byte(strings.ToLower(sha)), sig) {
			return nil
		}
	}
	return fmt.Errorf("%w: does not verify against any trusted key", ErrSignatureInvalid)
}
