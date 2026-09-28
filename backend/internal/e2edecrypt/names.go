package e2edecrypt

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Encrypted names — the Go twin of packages/core/src/lib/e2enames.ts
// (docs/E2E-ENCRYPTION.md → "Encrypted names", "Folder ids").
//
// A name is sealed with AES-SIV under the name key, the associated data being
// the 16-byte id of the folder it is in: the same name in two folders is two
// different stored names. The encrypted root's id is in the marker
// (names.root_id); every other folder carries its own id IN ITS STORED NAME,
// so a path decrypts one segment at a time with no other state.
//
// A stored name inside a names-encrypted folder is exactly one of:
//
//	file     S — base64url (no padding) of AES-SIV(NFC(name), AD=[parent id])
//	dir      S.D — the same, then a dot and D, the folder's own id (22 chars)
//	long     "<H>.fxl" (a file) or "<H>.fxl.D" (a folder): S was longer than
//	         the threshold (a folder counts its ".D"); H = base64url(SHA-256(S)),
//	         and S lives in the sibling sidecar "<H>.fxl.name"
//	sidecar  "<H>.fxl.name" — bookkeeping, never an entry of its own
//	plain    anything else, and a file-shaped name that fails the SIV check: a
//	         name that was never encrypted (written over WebDAV, or not yet
//	         reached by a level change)
//
// A folder whose name was never encrypted still has an id — the one it will
// carry: SIV-V(NFC(name), AD=[parent id, "filex-e2e-dir-id"]), the 16-byte
// synthetic IV (DeriveDirID). Its contents may already be sealed under it.

// Name encryption constants, identical to the browser's.
const (
	NamesAlg         = "AES-SIV-512"
	NamesEnc         = "b64url"
	NameKeyBytes     = 64
	NameMaxBytes     = 255
	DirIDBytes       = 16
	LongSuffix       = ".fxl"
	SidecarSuffix    = ".fxl.name"
	minEncodedLength = 23 // 16-byte tag + 1 byte = 17 bytes = 23 characters
	dirIDLabel       = "filex-e2e-dir-id"
)

// NameKind is what a stored name is, from its spelling alone.
type NameKind int

const (
	KindPlain NameKind = iota
	KindFile
	KindDir
	KindLong
	KindLongDir
	KindSidecar
)

var (
	b64urlRE  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	longRE    = regexp.MustCompile(`^([A-Za-z0-9_-]{43})\.fxl$`)
	longDirRE = regexp.MustCompile(`^([A-Za-z0-9_-]{43})\.fxl\.([A-Za-z0-9_-]{22})$`)
	dirRE     = regexp.MustCompile(`^([A-Za-z0-9_-]{23,})\.([A-Za-z0-9_-]{22})$`)
	sidecarRE = regexp.MustCompile(`^([A-Za-z0-9_-]{43})\.fxl\.name$`)
)

// StoredName is a classified stored name.
type StoredName struct {
	Kind    NameKind
	Hash    string // long, longdir, sidecar: the 43-character hash they share
	Encoded string // file, dir: the encoded ciphertext S
	DirID   []byte // dir, longdir: the folder's id
}

func dirID(s string) []byte {
	id, ok := b64urlDecode(s)
	if !ok || len(id) != DirIDBytes {
		return nil
	}
	return id
}

// ClassifyStoredName reports what a stored name is, without a key.
func ClassifyStoredName(stored string) StoredName {
	if m := sidecarRE.FindStringSubmatch(stored); m != nil {
		return StoredName{Kind: KindSidecar, Hash: m[1]}
	}
	if m := longDirRE.FindStringSubmatch(stored); m != nil {
		if id := dirID(m[2]); id != nil {
			return StoredName{Kind: KindLongDir, Hash: m[1], DirID: id}
		}
	}
	if m := longRE.FindStringSubmatch(stored); m != nil {
		return StoredName{Kind: KindLong, Hash: m[1]}
	}
	if m := dirRE.FindStringSubmatch(stored); m != nil {
		if id := dirID(m[2]); id != nil {
			return StoredName{Kind: KindDir, Encoded: m[1], DirID: id}
		}
	}
	if len(stored) >= minEncodedLength && b64urlRE.MatchString(stored) {
		return StoredName{Kind: KindFile, Encoded: stored}
	}
	return StoredName{Kind: KindPlain}
}

// DirIDOf returns the folder id a stored folder name carries, or nil.
func DirIDOf(stored string) []byte {
	return ClassifyStoredName(stored).DirID
}

// SidecarNameFor returns the sidecar that belongs to a long stored name
// (file or folder).
func SidecarNameFor(stored string) (string, bool) {
	c := ClassifyStoredName(stored)
	if c.Kind != KindLong && c.Kind != KindLongDir {
		return "", false
	}
	return c.Hash + SidecarSuffix, true
}

// ItemPrefixForSidecar returns the "<H>.fxl" an item next to this sidecar
// starts with (the item is "<H>.fxl" or "<H>.fxl.D").
func ItemPrefixForSidecar(sidecar string) (string, bool) {
	m := sidecarRE.FindStringSubmatch(sidecar)
	if m == nil {
		return "", false
	}
	return m[1] + LongSuffix, true
}

// b64urlDecode is strict: only the base64url alphabet, no padding, and the
// canonical spelling only (unused trailing bits must be zero) — two spellings
// of the same bytes would be two stored names for one plaintext.
func b64urlDecode(s string) ([]byte, bool) {
	if s == "" || !b64urlRE.MatchString(s) || len(s)%4 == 1 {
		return nil, false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, false
	}
	if base64.RawURLEncoding.EncodeToString(b) != s {
		return nil, false
	}
	return b, true
}

func b64urlEncode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func sha256b64url(s string) string {
	d := sha256.Sum256([]byte(s))
	return b64urlEncode(d[:])
}

// NameKey is a folder's unwrapped name key.
type NameKey struct {
	siv    *sivKey
	Long   int    // shortening threshold the folder was created with
	RootID []byte // the encrypted root's own folder id
}

// NewNameKey imports a raw 64-byte name key and the root's 16-byte id.
func NewNameKey(raw []byte, long int, rootID []byte) (*NameKey, error) {
	if len(raw) != NameKeyBytes {
		return nil, errors.New("e2e: name key must be 64 bytes")
	}
	if len(rootID) != DirIDBytes {
		return nil, errors.New("e2e: a folder id is 16 bytes")
	}
	k, err := newSivKey(raw)
	if err != nil {
		return nil, err
	}
	return &NameKey{siv: k, Long: long, RootID: append([]byte(nil), rootID...)}, nil
}

// DeriveDirID is the id of a folder whose name was never encrypted: a keyed
// PRF of its parent's id and its name.
func (k *NameKey) DeriveDirID(parentID []byte, plain string) []byte {
	sealed := k.siv.seal([]byte(norm.NFC.String(plain)), parentID, []byte(dirIDLabel))
	return sealed[:DirIDBytes]
}

// EffectiveDirID is the id the CHILDREN of a folder are sealed under: the one
// its stored name carries, or — a plaintext-named folder — the derived one.
func (k *NameKey) EffectiveDirID(parentID []byte, stored string) []byte {
	if id := DirIDOf(stored); id != nil {
		return id
	}
	return k.DeriveDirID(parentID, stored)
}

// EncryptedName is what the browser writes for one plaintext name.
type EncryptedName struct {
	Stored         string // the entry's name on the server
	Encoded        string // the encoded ciphertext S (without a folder's ".D")
	SidecarName    string // set only for a long name
	SidecarContent string
}

// EncryptName encrypts a plaintext name for the folder with id parentID,
// exactly as the browser does; dirID is the entry's own id when it is a
// folder, nil for a file. The decryptor does not need it; tests use it to
// build folders.
func (k *NameKey) EncryptName(plain string, parentID, dirID []byte) (EncryptedName, error) {
	n := norm.NFC.String(plain)
	if p := NameProblem(n); p != "" {
		return EncryptedName{}, errors.New("e2e: invalid name (" + p + ")")
	}
	if dirID != nil && len(dirID) != DirIDBytes {
		return EncryptedName{}, errors.New("e2e: a folder id is 16 bytes")
	}
	enc := b64urlEncode(k.siv.seal([]byte(n), parentID))
	suffix := ""
	if dirID != nil {
		suffix = "." + b64urlEncode(dirID)
	}
	if len(enc)+len(suffix) <= k.Long {
		return EncryptedName{Stored: enc + suffix, Encoded: enc}, nil
	}
	h := sha256b64url(enc)
	return EncryptedName{
		Stored:         h + LongSuffix + suffix,
		Encoded:        enc,
		SidecarName:    h + SidecarSuffix,
		SidecarContent: enc,
	}, nil
}

// DecryptEncoded decrypts an encoded ciphertext name sealed for the folder
// with id parentID. ok is false when it is not one.
func (k *NameKey) DecryptEncoded(encoded string, parentID []byte) (string, bool) {
	raw, ok := b64urlDecode(encoded)
	if !ok || len(raw) <= sivBlock {
		return "", false
	}
	p, err := k.siv.open(raw, parentID)
	if err != nil || !utf8.Valid(p) {
		return "", false
	}
	return string(p), true
}

// NameState is the outcome of reading one stored name.
type NameState int

const (
	NameDecrypted  NameState = iota // was encrypted, now plaintext
	NamePlain                       // was never encrypted; kept as stored
	NameUnreadable                  // ours by its spelling, did not decrypt here
	NameSidecar                     // bookkeeping; skip it
)

// EncodedOf returns the encoded ciphertext a stored name carries — through
// its sidecar for a long one — or ok=false (a plain name, a sidecar, or a
// long name whose sidecar is missing or does not match).
func EncodedOf(stored string, readSidecar func(name string) ([]byte, bool)) (string, bool) {
	c := ClassifyStoredName(stored)
	switch c.Kind {
	case KindFile, KindDir:
		return c.Encoded, true
	case KindLong, KindLongDir:
		if readSidecar == nil {
			return "", false
		}
		content, ok := readSidecar(c.Hash + SidecarSuffix)
		if !ok {
			return "", false
		}
		enc := strings.TrimSpace(string(content))
		// The sidecar has to be the one this name was made from.
		if enc == "" || sha256b64url(enc) != c.Hash {
			return "", false
		}
		return enc, true
	}
	return "", false
}

// DecryptStoredName reads one stored name found in the folder with id
// parentID. readSidecar returns a sibling sidecar's content, or ok=false when
// there is no such file; it is only called for a long name.
func (k *NameKey) DecryptStoredName(stored string, parentID []byte, readSidecar func(name string) ([]byte, bool)) (string, NameState) {
	c := ClassifyStoredName(stored)
	switch c.Kind {
	case KindSidecar:
		return "", NameSidecar
	case KindPlain:
		return stored, NamePlain
	}
	enc, ok := EncodedOf(stored, readSidecar)
	if !ok {
		return "", NameUnreadable
	}
	if p, ok := k.DecryptEncoded(enc, parentID); ok {
		return p, NameDecrypted
	}
	// A file-shaped base64url name that does not open is a plaintext name
	// that happens to look like one ("ReadMe_2024-final"). Folder-shaped and
	// long names are ours by construction: not opening here means they were
	// moved in without being re-sealed for this folder, or are damaged.
	if c.Kind == KindFile {
		return stored, NamePlain
	}
	return "", NameUnreadable
}

// RecoverMoved tries a name sealed for ANOTHER folder of this encrypted
// folder (an entry moved outside filex): every id in ids but here. ok=false
// when none opens it.
func (k *NameKey) RecoverMoved(stored string, here []byte, ids [][]byte, readSidecar func(name string) ([]byte, bool)) (string, bool) {
	enc, ok := EncodedOf(stored, readSidecar)
	if !ok {
		return "", false
	}
	for _, id := range ids {
		if bytes.Equal(id, here) {
			continue
		}
		if p, ok := k.DecryptEncoded(enc, id); ok {
			return p, true
		}
	}
	return "", false
}

// NameProblem mirrors the browser's namePlainProblem: "" when the name is one
// a local disk can hold, else why not.
func NameProblem(name string) string {
	switch {
	case name == "":
		return "empty"
	case name == "." || name == "..":
		return "dot"
	case strings.ContainsAny(name, `/\`):
		return "slash"
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "control"
		}
	}
	if len(name) > NameMaxBytes {
		return "too_long"
	}
	return ""
}
