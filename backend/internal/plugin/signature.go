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
// supplied as hex or standard base64, verified against the keys in
// FILEX_PLUGIN_TRUSTED_KEYS. An app's wasm module is signed over its
// LOWER-HEX sha256 (VerifyDetached); a storage plugin build, from 0.55, over
// its name, version, platform and sha256 (VerifyBuild, below - the sha256
// alone is its old form). Exported so internal/wasmplugin verifies exactly
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
//
// ⚠ A storage plugin's BUILD is not verified with this any more (VerifyBuild):
// a signature over the sha256 alone says nothing about which plugin, which
// version or which platform the bytes were signed as.
func VerifyDetached(trusted []ed25519.PublicKey, sha, signature string) error {
	if len(trusted) == 0 {
		return nil
	}
	sig, err := decodeSignature(signature)
	if err != nil {
		return err
	}
	for _, pub := range trusted {
		if ed25519.Verify(pub, []byte(strings.ToLower(sha)), sig) {
			return nil
		}
	}
	return fmt.Errorf("%w: does not verify against any trusted key", ErrSignatureInvalid)
}

// decodeSignature reads a detached signature given as hex or standard
// base64: ErrSignatureRequired when there is none.
func decodeSignature(signature string) ([]byte, error) {
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return nil, ErrSignatureRequired
	}
	sig, err := hex.DecodeString(signature)
	if err != nil {
		if sig, err = base64.StdEncoding.DecodeString(signature); err != nil {
			return nil, fmt.Errorf("%w: neither hex nor base64", ErrSignatureInvalid)
		}
	}
	return sig, nil
}

// ── A storage plugin build's signature ─────────────────────────────────
//
// What a build signature is over (docs/PLUGINS.md → "What is signed"): the
// canonical text
//
//	filex-storage-build:v1
//	<name>
//	<version>
//	<platform>
//	<sha256>
//
// - the plugin's name, its version without a leading "v", the GOOS/GOARCH
// key ("linux/amd64") and the lower-hex sha256 of the build, joined by a
// single line feed, no line feed at the end - signed with ed25519 as it is
// (not hashed again). A store signs every build it lists this way
// (apps.filex.sh signBuilds), and a publisher signs theirs the same way.
//
// ⚠ Why: a signature over the sha256 alone vouched for the bytes and for
// nothing else. A trusted key's signature over an old build, over another
// plugin's build or over another platform's build verified just as well on
// every install path, so "signed" did not mean "this plugin, this version,
// for this server". The text starts with its own name so it can never be
// mistaken for the sha256-only form, nor for any other text a store signs.
//
// The old form (the sha256 alone) is still accepted, with a warning on the
// plugin's row and in the audit log, during filex 0.55; 0.56 refuses it.

// BuildPayloadV1 is the first line of a build signature's text.
const BuildPayloadV1 = "filex-storage-build:v1"

// BuildClaim is what a build signature names: the plugin, its version, the
// platform the build is for and the build's sha256.
type BuildClaim struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	SHA256   string `json:"sha256"`
}

// Normal is the claim as it is signed: trimmed, the version without a
// leading "v", the platform and the sha256 lower case.
func (c BuildClaim) Normal() BuildClaim {
	return BuildClaim{
		Name:     strings.TrimSpace(c.Name),
		Version:  strings.TrimPrefix(strings.TrimSpace(c.Version), "v"),
		Platform: strings.ToLower(strings.TrimSpace(c.Platform)),
		SHA256:   strings.ToLower(strings.TrimSpace(c.SHA256)),
	}
}

// Payload is the exact text a build signature is over.
func (c BuildClaim) Payload() []byte {
	n := c.Normal()
	return []byte(strings.Join([]string{BuildPayloadV1, n.Name, n.Version, n.Platform, n.SHA256}, "\n"))
}

// complete reports whether the claim names everything the text needs.
func (c BuildClaim) complete() bool {
	n := c.Normal()
	return n.Name != "" && n.Version != "" && n.Platform != "" && n.SHA256 != ""
}

// BuildSignature is how a build signature verified: the key it verified
// with, and whether it was the old form (the sha256 alone).
type BuildSignature struct {
	Key    ed25519.PublicKey
	Legacy bool
}

// VerifyBuild checks a storage plugin build's signature against the trusted
// keys: over the claim's text first (BuildClaim.Payload), then - the old
// form, accepted during 0.55 - over the lower-hex sha256 alone. No trusted
// keys → nothing is required (zero BuildSignature, nil); no signature →
// ErrSignatureRequired; no key verifies either form → ErrSignatureInvalid.
func VerifyBuild(trusted []ed25519.PublicKey, c BuildClaim, signature string) (BuildSignature, error) {
	if len(trusted) == 0 {
		return BuildSignature{}, nil
	}
	sig, err := decodeSignature(signature)
	if err != nil {
		return BuildSignature{}, err
	}
	if c.complete() {
		payload := c.Payload()
		for _, pub := range trusted {
			if ed25519.Verify(pub, payload, sig) {
				return BuildSignature{Key: pub}, nil
			}
		}
	}
	sha := []byte(c.Normal().SHA256)
	for _, pub := range trusted {
		if ed25519.Verify(pub, sha, sig) {
			return BuildSignature{Key: pub, Legacy: true}, nil
		}
	}
	return BuildSignature{}, fmt.Errorf("%w: does not verify against any trusted key for %s", ErrSignatureInvalid, c.describe())
}

// describe names the claim in a refusal.
func (c BuildClaim) describe() string {
	n := c.Normal()
	v := n.Version
	if v == "" {
		v = "(no version named)"
	}
	return n.Name + " " + v + " on " + n.Platform
}
