package appstore

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/plugin"
)

// Key uses: what a store key may sign. A key signs for ONE use, and a
// signature is accepted only from a key of the use the answer needs: an
// install link is signed with an `index` key, a license answer with a
// `license` key. One key leaking does not open the other's door.
const (
	UseIndex    = "index"
	UseArtifact = "artifact"
	UseLicense  = "license"
)

// Key statuses in a store's keys.json. A key signs while it is `active`; a
// `next` key is published before it signs (rotation), a `retired` one is
// kept to identify old signatures and signs nothing new.
const (
	KeyActive  = "active"
	KeyNext    = "next"
	KeyRetired = "retired"
)

// Signature errors, matched by the callers to pick an error code.
var (
	ErrUnknownKey       = errors.New("signed by a key this store was not trusted with")
	ErrWrongKeyUse      = errors.New("signed by a key of another use")
	ErrKeyNotActive     = errors.New("signed by a key that is not active")
	ErrSignatureInvalid = errors.New("signature does not verify")
	ErrMalformed        = errors.New("not a signed answer")
)

// Envelope is a signed answer: `{"payload": {...}, "key_id": "...",
// "signature": "<hex>"}`. signature = ed25519 over the lower-hex sha256 of
// Canonical(payload) - the rule plugin.VerifyDetached applies to a module.
type Envelope struct {
	Payload   json.RawMessage `json:"payload"`
	KeyID     string          `json:"key_id"`
	Signature string          `json:"signature"`
}

// Key is one key of a store's keys.json (`{"keys": [...]}`), and what a
// trusted store keeps of the keys an administrator saw.
type Key struct {
	ID      string `json:"id"`
	Use     string `json:"use"`
	Ed25519 string `json:"ed25519"`
	Status  string `json:"status,omitempty"`
}

// KeySet is a store's keys.json.
type KeySet struct {
	Keys []Key `json:"keys"`
}

// PublicKey parses the key's material (hex or base64, 32 bytes).
func (k Key) PublicKey() (ed25519.PublicKey, error) {
	pub, err := plugin.ParsePublicKey(strings.TrimSpace(k.Ed25519))
	if err != nil {
		return nil, fmt.Errorf("key %q: %w", k.ID, err)
	}
	return pub, nil
}

// Fingerprint is what an administrator compares with what the store
// publishes: the lower-hex sha256 of the 32 raw key bytes. "" when the
// material does not parse.
func (k Key) Fingerprint() string {
	pub, err := k.PublicKey()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// signs reports whether the key may sign answers now.
func (k Key) signs() bool {
	return k.Status == "" || k.Status == KeyActive
}

// Validate checks a keys.json: every key has an id, a known use and material
// that parses; no id twice. A store whose list does not hold together is
// refused whole rather than read in part.
func (s *KeySet) Validate() error {
	if s == nil || len(s.Keys) == 0 {
		return errors.New("the store publishes no keys")
	}
	seen := map[string]bool{}
	for _, k := range s.Keys {
		if strings.TrimSpace(k.ID) == "" {
			return errors.New("a key has no id")
		}
		if !ValidKeyID(k.ID) {
			return fmt.Errorf("key id %q is not 1-64 characters of A-Z a-z 0-9 . _ -", k.ID)
		}
		if seen[k.ID] {
			return fmt.Errorf("key id %q appears twice", k.ID)
		}
		seen[k.ID] = true
		switch k.Use {
		case UseIndex, UseArtifact, UseLicense:
		default:
			return fmt.Errorf("key %q: unknown use %q", k.ID, k.Use)
		}
		switch k.Status {
		case "", KeyActive, KeyNext, KeyRetired:
		default:
			return fmt.Errorf("key %q: unknown status %q", k.ID, k.Status)
		}
		if _, err := k.PublicKey(); err != nil {
			return err
		}
	}
	return nil
}

// TrustedKeys is the part of a keys.json filex pins when a store is trusted:
// the keys that sign what filex reads (install links, license answers) and
// may sign now or soon. A retired key is not pinned; an artifact key is a
// module's business (FILEX_PLUGIN_TRUSTED_KEYS), not this package's.
func (s *KeySet) TrustedKeys() []Key {
	out := []Key{}
	for _, k := range s.Keys {
		if (k.Use == UseIndex || k.Use == UseLicense) && k.Status != KeyRetired {
			out = append(out, Key{ID: k.ID, Use: k.Use, Ed25519: strings.ToLower(strings.TrimSpace(k.Ed25519)), Status: k.Status})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Fingerprints is the sorted fingerprint list of keys: what an
// administrator's approval names, so the keys they saw are the keys pinned.
func Fingerprints(keys []Key) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.Use+":"+k.ID+":"+k.Fingerprint())
	}
	sort.Strings(out)
	return out
}

// ParseEnvelope reads a signed answer, refusing one without its three parts.
func ParseEnvelope(raw []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if len(env.Payload) == 0 || string(env.Payload) == "null" || strings.TrimSpace(env.KeyID) == "" || strings.TrimSpace(env.Signature) == "" {
		return nil, fmt.Errorf("%w: payload, key_id and signature are required", ErrMalformed)
	}
	if !ValidKeyID(env.KeyID) {
		return nil, fmt.Errorf("%w: key_id is not 1-64 characters of A-Z a-z 0-9 . _ -", ErrMalformed)
	}
	return &env, nil
}

// keyIDRe is what a key id may be: it is shown to an administrator beside
// the key's fingerprint on the trust question, so it is a short name in
// plain ASCII - no spaces, no markup, no direction marks that could make the
// question read other than it is.
var keyIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidKeyID reports whether id may name a store key.
func ValidKeyID(id string) bool { return keyIDRe.MatchString(id) }

// PayloadSHA256 is the lower-hex sha256 of the payload's canonical form:
// what the signature is over.
func PayloadSHA256(payload []byte) (string, error) {
	c, err := Canonical(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c)
	return hex.EncodeToString(sum[:]), nil
}

// Verify checks an envelope against the keys a store was trusted with: the
// key named by key_id must be one of them, of the use the answer needs, and
// signing; the signature must verify over the canonical payload. Only that
// one key is tried - a signature from another trusted key of another use is
// still refused.
func Verify(env *Envelope, trusted []Key, use string) error {
	if env == nil {
		return ErrMalformed
	}
	var key *Key
	for i := range trusted {
		if trusted[i].ID == env.KeyID {
			key = &trusted[i]
			break
		}
	}
	if key == nil {
		return fmt.Errorf("%w (key_id %q)", ErrUnknownKey, env.KeyID)
	}
	if key.Use != use {
		return fmt.Errorf("%w: key %q is for %q, this answer needs %q", ErrWrongKeyUse, key.ID, key.Use, use)
	}
	if !key.signs() {
		return fmt.Errorf("%w: key %q is %q", ErrKeyNotActive, key.ID, key.Status)
	}
	pub, err := key.PublicKey()
	if err != nil {
		return err
	}
	sum, err := PayloadSHA256(env.Payload)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
	}
	// ⚠ One key in the list, never an empty one: VerifyDetached accepts
	// anything when it is handed no key at all.
	if err := plugin.VerifyDetached([]ed25519.PublicKey{pub}, sum, env.Signature); err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
	}
	return nil
}
