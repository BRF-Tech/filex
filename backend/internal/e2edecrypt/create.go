package e2edecrypt

import (
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

// Writing a folder's key file, `.filex-e2e.json`: the Go twin of
// createEncryptedFolder, enableNames, startConversion and finishConversion
// in packages/core/src/lib/e2ecrypto.ts. The same key hierarchy, the same
// slots, the same JSON (docs/E2E-ENCRYPTION.md → "Key management", "Format
// reference"): a folder made here opens in the browser and a folder made
// there opens here.
//
// A key file that already exists is rewritten field by field over its raw
// JSON, never through the Marker struct: a program that rewrites it "keeps
// every field it does not understand" (Format reference → Common rules).

const (
	markerVersion         = 2
	markerVersionFeatures = 3
	// NamesLongDefault is the shortening threshold a new names slot gets
	// (E2E_NAMES_LONG_DEFAULT).
	NamesLongDefault = 220
	saltLen          = 16
)

// CreateOptions configure CreateFolder.
type CreateOptions struct {
	// Iterations of PBKDF2. Never fewer than MinIterations; 0 means that.
	Iterations int
	// EscrowPublicKey is the installation's escrow key (base64 SPKI DER, as
	// /api/capabilities publishes it), or "" for no escrow slot.
	EscrowPublicKey string
	// EncryptNames makes a level-2 folder (v3, feature "names").
	EncryptNames bool
	// NamesPending marks the names slot pending: an existing folder whose
	// entries still carry their plaintext names (enableNames does the same).
	NamesPending bool
	// Rand is where every random byte comes from; nil is crypto/rand.
	Rand io.Reader
}

// Created is a new folder's key file and the keys it hands back.
type Created struct {
	// Marker is the key file, ready to write as `.filex-e2e.json`.
	Marker []byte
	// Keys are the folder key (and the name key, at level 2).
	Keys *Keys
	// RecoveryKey is shown ONCE. filex never stores it.
	RecoveryKey string
}

// markerSlots is the JSON of a new key file, in the order the browser writes
// its fields.
type markerSlots struct {
	V      int           `json:"v"`
	Salt   string        `json:"salt"`
	Iter   int           `json:"iter"`
	Verify string        `json:"verify"`
	Fmk    string        `json:"fmk"`
	FmkPw  string        `json:"fmk_pw"`
	Rk     *RecoverySlot `json:"rk,omitempty"`
	Esc    *escrowSlot   `json:"esc,omitempty"`
	Req    []string      `json:"req,omitempty"`
	Names  *NamesSlot    `json:"names,omitempty"`
}

type escrowSlot struct {
	KID  string `json:"kid"`
	Alg  string `json:"alg"`
	Blob string `json:"blob"`
}

// FormatRecoveryKey writes 20 raw bytes as the key a person writes down:
// Crockford base32, most significant bits first, 8 groups of 4 joined by "-"
// (formatRecoveryKey).
func FormatRecoveryKey(raw []byte) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var sb strings.Builder
	var acc uint32
	bits := 0
	for _, b := range raw {
		acc = acc<<8 | uint32(b)
		bits += 8
		for bits >= 5 {
			sb.WriteByte(alphabet[(acc>>(bits-5))&31])
			bits -= 5
		}
	}
	if bits > 0 {
		sb.WriteByte(alphabet[(acc<<(5-bits))&31])
	}
	s := sb.String()
	var out strings.Builder
	for i := 0; i < len(s); i += 4 {
		if i > 0 {
			out.WriteByte('-')
		}
		end := min(i+4, len(s))
		out.WriteString(s[i:end])
	}
	return out.String()
}

// PasswordProblem is "" for a password the browser's dialog would take, and
// otherwise why not. The dialog counts a JavaScript string's length, UTF-16
// code units, so this does too: the same password passes in both.
func PasswordProblem(pw string) string {
	if len(utf16.Encode([]rune(pw))) < MinPasswordLen {
		return fmt.Sprintf("the password must be at least %d characters", MinPasswordLen)
	}
	return ""
}

// sealEscrow seals the folder key to the installation's escrow public key:
// RSA-OAEP with SHA-256, the key named by its KID (internal/e2e, the one
// definition the server and the browser share).
func sealEscrow(spkiB64 string, fmk []byte) (*escrowSlot, error) {
	k, err := e2e.ParseEscrowPublicKey(spkiB64)
	if err != nil {
		return nil, err
	}
	der, err := base64.StdEncoding.DecodeString(k.SPKI)
	if err != nil {
		return nil, err
	}
	anyKey, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	pub, ok := anyKey.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("e2e: the escrow key is not RSA")
	}
	blob, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, fmk, nil)
	if err != nil {
		return nil, err
	}
	return &escrowSlot{KID: k.KID, Alg: e2e.EscrowAlg, Blob: base64.StdEncoding.EncodeToString(blob)}, nil
}

// EscrowKID is the short name of an escrow public key (base64 SPKI), the one
// its slots carry; "" when it is not one.
func EscrowKID(spkiB64 string) string {
	k, err := e2e.ParseEscrowPublicKey(spkiB64)
	if err != nil {
		return ""
	}
	return k.KID
}

// newNamesSlot mints a 64-byte name key and the root's folder id, and seals
// the key under the folder key.
func newNamesSlot(fmk []byte, pending bool, rnd io.Reader) (*NamesSlot, *NameKey, error) {
	raw, err := random(rnd, NameKeyBytes)
	if err != nil {
		return nil, nil, err
	}
	defer clear(raw)
	rootID, err := random(rnd, DirIDBytes)
	if err != nil {
		return nil, nil, err
	}
	sealed, err := sealB64(fmk, raw, rnd)
	if err != nil {
		return nil, nil, err
	}
	nk, err := NewNameKey(raw, NamesLongDefault, rootID)
	if err != nil {
		return nil, nil, err
	}
	return &NamesSlot{
		Alg:     NamesAlg,
		Enc:     NamesEnc,
		Long:    NamesLongDefault,
		Key:     sealed,
		RootID:  b64urlEncode(rootID),
		Pending: pending,
	}, nk, nil
}

// CreateFolder makes a new encrypted folder's key file: a random folder key
// wrapped under the password (PBKDF2-SHA-256), under a freshly minted
// recovery key (HKDF-SHA-256), and - when an escrow key is given - to the
// installation's escrow key. At level 2 (EncryptNames) it is v3 with a names
// slot. The password is checked as the browser checks it.
func CreateFolder(password string, opts CreateOptions) (*Created, error) {
	if p := PasswordProblem(password); p != "" {
		return nil, errors.New(p)
	}
	iter := max(opts.Iterations, MinIterations)
	rnd := opts.Rand
	salt, err := random(rnd, saltLen)
	if err != nil {
		return nil, err
	}
	kek, err := pbkdf2.Key(sha256.New, password, salt, iter, fmkLen)
	if err != nil {
		return nil, err
	}
	defer clear(kek)
	fmk, err := random(rnd, fmkLen)
	if err != nil {
		return nil, err
	}
	verify, err := sealB64(kek, []byte(verifyPlaintext), rnd)
	if err != nil {
		return nil, err
	}
	fmkPw, err := sealB64(kek, fmk, rnd)
	if err != nil {
		return nil, err
	}
	rkRaw, err := random(rnd, recoveryBytes)
	if err != nil {
		return nil, err
	}
	defer clear(rkRaw)
	rkSalt, err := random(rnd, saltLen)
	if err != nil {
		return nil, err
	}
	rkek, err := hkdf.Key(sha256.New, rkRaw, rkSalt, recoveryInfo, 32)
	if err != nil {
		return nil, err
	}
	defer clear(rkek)
	rkBlob, err := sealB64(rkek, fmk, rnd)
	if err != nil {
		return nil, err
	}
	m := markerSlots{
		V:      markerVersion,
		Salt:   base64.StdEncoding.EncodeToString(salt),
		Iter:   iter,
		Verify: verify,
		Fmk:    "wrapped",
		FmkPw:  fmkPw,
		Rk:     &RecoverySlot{Salt: base64.StdEncoding.EncodeToString(rkSalt), Blob: rkBlob},
	}
	if strings.TrimSpace(opts.EscrowPublicKey) != "" {
		if m.Esc, err = sealEscrow(opts.EscrowPublicKey, fmk); err != nil {
			return nil, err
		}
	}
	keys := &Keys{FMK: fmk}
	if opts.EncryptNames {
		slot, nk, err := newNamesSlot(fmk, opts.NamesPending, rnd)
		if err != nil {
			return nil, err
		}
		m.V = markerVersionFeatures
		m.Req = []string{"names"}
		m.Names = slot
		keys.Names = nk
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return &Created{Marker: data, Keys: keys, RecoveryKey: FormatRecoveryKey(rkRaw)}, nil
}

// ── rewriting a key file, field by field ──────────────────────────────────

type rawFields map[string]json.RawMessage

func readFields(marker []byte) (rawFields, error) {
	var f rawFields
	if err := json.Unmarshal(marker, &f); err != nil || f == nil {
		return nil, ErrNotMarker
	}
	return f, nil
}

func (f rawFields) set(k string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f[k] = b
	return nil
}

func (f rawFields) req() []string {
	var r []string
	if raw, ok := f["req"]; ok {
		_ = json.Unmarshal(raw, &r)
	}
	return r
}

// setReq writes req and v: v3 with the list, or - nothing left required -
// v2 with no req at all, which every filex since 0.31 opens.
func (f rawFields) setReq(req []string) error {
	if len(req) == 0 {
		delete(f, "req")
		return f.set("v", markerVersion)
	}
	if err := f.set("req", req); err != nil {
		return err
	}
	return f.set("v", markerVersionFeatures)
}

// done re-reads the rewritten key file with the reader's rules: a writer
// never hands out a key file `filex decrypt` (or the browser) would refuse.
func (f rawFields) done() ([]byte, error) {
	out, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	if _, err := ParseMarker(out); err != nil {
		return nil, fmt.Errorf("e2e: the rewritten key file does not read back: %w", err)
	}
	return out, nil
}

// Cleanup is what to remove when a conversion finishes: the plaintext's old
// versions and the trash entries that came from the folder (the owner's
// choice, kept in the key file so a resumed run honours it).
type Cleanup struct {
	Versions bool `json:"versions"`
	Trash    bool `json:"trash"`
}

// isoMillis is JavaScript's Date.toISOString(): UTC, milliseconds, "Z".
func isoMillis(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// StartConversion marks an existing folder as being encrypted in place: v3,
// "conv" required (a filex that does not know it refuses a folder whose files
// are half plaintext), `conv: {pending, started, cleanup}`. Fields it does not
// know are kept (startConversion).
func StartConversion(marker []byte, when time.Time, cleanup *Cleanup) ([]byte, error) {
	f, err := readFields(marker)
	if err != nil {
		return nil, err
	}
	req := f.req()
	has := false
	for _, r := range req {
		if r == "conv" {
			has = true
		}
	}
	if !has {
		req = append(req, "conv")
	}
	conv := map[string]any{"pending": true, "started": isoMillis(when)}
	if cleanup != nil {
		conv["cleanup"] = cleanup
	}
	if err := f.set("conv", conv); err != nil {
		return nil, err
	}
	if err := f.setReq(req); err != nil {
		return nil, err
	}
	return f.done()
}

// ConversionCleanup is the cleanup a pending conversion asked for, or nil.
func ConversionCleanup(marker []byte) *Cleanup {
	f, err := readFields(marker)
	if err != nil {
		return nil
	}
	var c struct {
		Cleanup *Cleanup `json:"cleanup"`
	}
	if raw, ok := f["conv"]; ok && json.Unmarshal(raw, &c) == nil {
		return c.Cleanup
	}
	return nil
}

// FinishConversion drops `conv`: every file carries the magic. Back to v2
// when nothing else is required (finishConversion).
func FinishConversion(marker []byte) ([]byte, error) {
	f, err := readFields(marker)
	if err != nil {
		return nil, err
	}
	delete(f, "conv")
	var req []string
	for _, r := range f.req() {
		if r != "conv" {
			req = append(req, r)
		}
	}
	if err := f.setReq(req); err != nil {
		return nil, err
	}
	return f.done()
}

// EnableNames raises a v2 folder to level 2: a new name key sealed under the
// folder key, v3 with "names" required and the slot pending until every entry
// is renamed (enableNames). Needs the folder key, not the password.
func EnableNames(marker, fmk []byte, rnd io.Reader) ([]byte, *NameKey, error) {
	m, err := ParseMarker(marker)
	if err != nil {
		return nil, nil, err
	}
	if m.V != markerVersion {
		return nil, nil, errors.New("e2e: only a v2 folder can switch to encrypted names")
	}
	f, err := readFields(marker)
	if err != nil {
		return nil, nil, err
	}
	slot, nk, err := newNamesSlot(fmk, true, rnd)
	if err != nil {
		return nil, nil, err
	}
	if err := f.set("names", slot); err != nil {
		return nil, nil, err
	}
	if err := f.setReq([]string{"names"}); err != nil {
		return nil, nil, err
	}
	out, err := f.done()
	if err != nil {
		return nil, nil, err
	}
	return out, nk, nil
}

// FinishNames drops `names.pending`: every entry carries its encrypted name
// (finishNames). The rest of the names slot is kept as it is.
func FinishNames(marker []byte) ([]byte, error) {
	f, err := readFields(marker)
	if err != nil {
		return nil, err
	}
	raw, ok := f["names"]
	if !ok {
		return nil, errors.New("e2e: this folder has no encrypted names")
	}
	var names map[string]json.RawMessage
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, ErrNotMarker
	}
	delete(names, "pending")
	if err := f.set("names", names); err != nil {
		return nil, err
	}
	return f.done()
}
