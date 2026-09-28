package e2edecrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strings"
)

// The folder marker `.filex-e2e.json` and the three ways it hands back the
// folder master key (FMK). Reference: packages/core/src/lib/e2ecrypto.ts and
// docs/E2E-ENCRYPTION.md → "Folder marker".
//
// Deliberately absent: the operator escrow key. The supported escrow path in
// the web UI announces itself to the folder's owner before it unlocks
// anything; an offline decryptor would be a silent one, so filex does not
// ship it. (An operator holding the escrow private key can of course write
// one — the documentation says so plainly; filex just does not make it the
// easy path.)

// MarkerName is the marker's file name at an encrypted folder's root.
const MarkerName = ".filex-e2e.json"

const (
	verifyPlaintext = "filex-e2e-verify-v1"
	recoveryInfo    = "filex-e2e-recovery-v1"
	ivLen           = 12
	fmkLen          = 32
	recoveryBytes   = 20
	// maxIterations refuses a marker that would keep PBKDF2 busy for hours.
	// Real markers say 600,000; this leaves two orders of magnitude of room.
	maxIterations = 100_000_000
)

// knownFeatures are the entries of a v3 marker's `req` this build honours.
var knownFeatures = map[string]bool{"names": true, "rekey": true, "conv": true}

var (
	// ErrWrongPassword: the password does not open the marker's verify blob.
	ErrWrongPassword = errors.New("wrong password")
	// ErrWrongRecoveryKey: malformed, wrong, or the folder has no recovery key.
	ErrWrongRecoveryKey = errors.New("wrong recovery key (or this folder has no recovery key)")
	// ErrNotMarker: the file is not a filex encrypted-folder marker.
	ErrNotMarker = errors.New("not a filex encrypted-folder marker (.filex-e2e.json)")
)

// UnsupportedError: the folder requires a feature this build does not know.
// Opening it anyway is how an old client breaks a folder, so it is refused.
type UnsupportedError struct{ Features []string }

func (e *UnsupportedError) Error() string {
	return "this folder needs a newer filex: unsupported feature(s) " + strings.Join(e.Features, ", ")
}

// RecoverySlot is the marker's user-recovery-key slot.
type RecoverySlot struct {
	Salt string `json:"salt"`
	Blob string `json:"blob"`
}

// NamesSlot is the marker's encrypted-names slot (v3, feature "names").
type NamesSlot struct {
	Alg  string `json:"alg"`
	Enc  string `json:"enc"`
	Long int    `json:"long"`
	Key  string `json:"key"`
	// RootID is the encrypted root's own folder id (16 bytes, base64url):
	// the associated data of every name directly inside the root.
	RootID  string `json:"root_id"`
	Pending bool   `json:"pending,omitempty"`
}

// Marker is a parsed `.filex-e2e.json`.
type Marker struct {
	V      int
	Salt   []byte
	Iter   int
	Verify string
	Fmk    string // "" (v1), "kek" or "wrapped"
	FmkPw  string
	Rk     *RecoverySlot
	Req    []string
	Names  *NamesSlot
	// Rekey is the previous FMK sealed under the current one, while a re-key
	// (a password change that replaced the folder key) is re-wrapping the
	// files' keys. Feature "rekey"; nil otherwise.
	Rekey *RekeySlot
	// ConvPending: an existing folder is being encrypted in place (feature
	// "conv"); files it has not reached yet are still plaintext.
	ConvPending bool
}

// RekeySlot is the marker's re-key-in-progress slot (v3, feature "rekey").
type RekeySlot struct {
	From    string `json:"from"`
	Pending bool   `json:"pending"`
}

// HasNames reports whether file and folder names are encrypted.
func (m *Marker) HasNames() bool {
	if m == nil || m.V != 3 || m.Names == nil {
		return false
	}
	for _, f := range m.Req {
		if f == "names" {
			return true
		}
	}
	return false
}

type rawMarker struct {
	V      *float64        `json:"v"`
	Salt   *string         `json:"salt"`
	Iter   *float64        `json:"iter"`
	Verify *string         `json:"verify"`
	Fmk    *string         `json:"fmk"`
	FmkPw  *string         `json:"fmk_pw"`
	Rk     *RecoverySlot   `json:"rk"`
	Req    json.RawMessage `json:"req"`
	Names  json.RawMessage `json:"names"`
	Rekey  json.RawMessage `json:"rekey"`
	Conv   json.RawMessage `json:"conv"`
}

// ParseMarker reads a marker with the browser's rules (parseMarkerDetailed).
// A well-formed v3 marker that requires an unknown feature returns an
// *UnsupportedError; anything malformed returns ErrNotMarker.
func ParseMarker(data []byte) (*Marker, error) {
	var r rawMarker
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, ErrNotMarker
	}
	if r.V == nil || r.Salt == nil || r.Verify == nil || r.Iter == nil {
		return nil, ErrNotMarker
	}
	v := *r.V
	if v != 1 && v != 2 && v != 3 {
		return nil, ErrNotMarker
	}
	if *r.Iter < 1 || *r.Iter != math.Trunc(*r.Iter) || *r.Iter > maxIterations {
		return nil, ErrNotMarker
	}
	salt, err := base64.StdEncoding.DecodeString(*r.Salt)
	if err != nil {
		return nil, ErrNotMarker
	}
	m := &Marker{V: int(v), Salt: salt, Iter: int(*r.Iter), Verify: *r.Verify, Rk: r.Rk}
	if m.V >= 2 {
		if r.Fmk == nil || (*r.Fmk != "kek" && *r.Fmk != "wrapped") {
			return nil, ErrNotMarker
		}
		m.Fmk = *r.Fmk
		if m.Fmk == "wrapped" {
			if r.FmkPw == nil {
				return nil, ErrNotMarker
			}
			m.FmkPw = *r.FmkPw
		}
	}
	present := func(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }
	if m.V != 3 {
		// A v1/v2 marker carrying v3 fields is not something any filex wrote.
		if present(r.Req) || present(r.Names) || present(r.Rekey) || present(r.Conv) {
			return nil, ErrNotMarker
		}
		return m, nil
	}
	if !present(r.Req) || json.Unmarshal(r.Req, &m.Req) != nil {
		return nil, ErrNotMarker
	}
	var unknown []string
	hasNames, hasRekey, hasConv := false, false, false
	for _, f := range m.Req {
		if f == "names" {
			hasNames = true
		}
		if f == "rekey" {
			hasRekey = true
		}
		if f == "conv" {
			hasConv = true
		}
		if !knownFeatures[f] {
			unknown = append(unknown, f)
		}
	}
	if hasConv {
		var c struct {
			Pending bool `json:"pending"`
		}
		if !present(r.Conv) || json.Unmarshal(r.Conv, &c) != nil || !c.Pending {
			return nil, ErrNotMarker
		}
		m.ConvPending = true
	} else if present(r.Conv) {
		return nil, ErrNotMarker
	}
	if hasNames {
		var n NamesSlot
		if !present(r.Names) || json.Unmarshal(r.Names, &n) != nil || !validNames(n) {
			return nil, ErrNotMarker
		}
		m.Names = &n
	}
	if hasRekey {
		var rk RekeySlot
		if m.Fmk != "wrapped" || !present(r.Rekey) || json.Unmarshal(r.Rekey, &rk) != nil || rk.From == "" {
			return nil, ErrNotMarker
		}
		m.Rekey = &rk
	} else if present(r.Rekey) {
		return nil, ErrNotMarker
	}
	if len(unknown) > 0 {
		return m, &UnsupportedError{Features: unknown}
	}
	return m, nil
}

func validNames(n NamesSlot) bool {
	return n.Alg == NamesAlg && n.Enc == NamesEnc && n.Key != "" && n.Long >= 64 && n.Long <= 255 &&
		dirID(n.RootID) != nil
}

// Keys is what an unlock hands back: the FMK, for a names-encrypted folder
// the name key, and for a folder mid re-key the PREVIOUS FMK — what the files
// not re-wrapped yet are still under.
type Keys struct {
	FMK      []byte
	Names    *NameKey
	Previous []byte
}

// gcmOpenB64 opens base64(12B IV || AES-GCM ciphertext) under key.
func gcmOpenB64(key []byte, b64 string) ([]byte, bool) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) <= ivLen {
		return nil, false
	}
	return gcmOpen(key, raw[:ivLen], raw[ivLen:])
}

func gcmOpen(key, iv, ct []byte) ([]byte, bool) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, false
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, false
	}
	p, err := g.Open(nil, iv, ct, nil)
	if err != nil {
		return nil, false
	}
	return p, true
}

// UnlockPassword derives the KEK (PBKDF2-SHA256), proves it against the
// verify blob, and returns the FMK (+ name key).
func (m *Marker) UnlockPassword(password string) (*Keys, error) {
	kek, err := pbkdf2.Key(sha256.New, password, m.Salt, m.Iter, fmkLen)
	if err != nil {
		return nil, err
	}
	pt, ok := gcmOpenB64(kek, m.Verify)
	if !ok || string(pt) != verifyPlaintext {
		return nil, ErrWrongPassword
	}
	var fmk []byte
	if m.V == 1 || m.Fmk == "kek" {
		// v1, and a v1 folder upgraded in place: the FMK IS the KEK.
		fmk = kek
	} else {
		f, ok := gcmOpenB64(kek, m.FmkPw)
		if !ok || len(f) != fmkLen {
			return nil, &CorruptError{Path: MarkerName, Err: errors.New("the password opens it, but its folder-key slot is damaged")}
		}
		fmk = f
	}
	return m.withNames(fmk)
}

// ParseRecoveryKey reads a typed recovery key back to its 20 bytes, forgiving
// case, dashes, spaces and the Crockford look-alikes (O→0, I/L→1).
func ParseRecoveryKey(s string) ([]byte, bool) {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	clean := strings.ToUpper(s)
	clean = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f', '-':
			return -1
		case 'O':
			return '0'
		case 'I', 'L':
			return '1'
		}
		return r
	}, clean)
	need := (recoveryBytes*8 + 4) / 5 // 32
	if len(clean) != need {
		return nil, false
	}
	out := make([]byte, 0, recoveryBytes)
	var acc uint32
	bits := 0
	for i := 0; i < len(clean); i++ {
		v := strings.IndexByte(alphabet, clean[i])
		if v < 0 {
			return nil, false
		}
		acc = acc<<5 | uint32(v)
		bits += 5
		if bits >= 8 {
			out = append(out, byte(acc>>(bits-8)))
			bits -= 8
		}
	}
	if len(out) != recoveryBytes {
		return nil, false
	}
	return out, true
}

// UnlockRecoveryKey opens the user-recovery slot and returns the FMK (+ name
// key).
func (m *Marker) UnlockRecoveryKey(recoveryKey string) (*Keys, error) {
	if m.V < 2 || m.Rk == nil {
		return nil, ErrWrongRecoveryKey
	}
	raw, ok := ParseRecoveryKey(recoveryKey)
	if !ok {
		return nil, ErrWrongRecoveryKey
	}
	salt, err := base64.StdEncoding.DecodeString(m.Rk.Salt)
	if err != nil {
		return nil, ErrWrongRecoveryKey
	}
	rkek, err := hkdf.Key(sha256.New, raw, salt, recoveryInfo, 32)
	if err != nil {
		return nil, err
	}
	fmk, ok := gcmOpenB64(rkek, m.Rk.Blob)
	if !ok || len(fmk) != fmkLen {
		return nil, ErrWrongRecoveryKey
	}
	return m.withNames(fmk)
}

func (m *Marker) withNames(fmk []byte) (*Keys, error) {
	k := &Keys{FMK: fmk}
	if m.Rekey != nil {
		prev, ok := gcmOpenB64(fmk, m.Rekey.From)
		if !ok || len(prev) != fmkLen {
			return nil, &CorruptError{Path: MarkerName, Err: errors.New("its re-key slot does not open under this folder's key (damaged)")}
		}
		k.Previous = prev
	}
	if !m.HasNames() {
		return k, nil
	}
	raw, ok := gcmOpenB64(fmk, m.Names.Key)
	if !ok || len(raw) != NameKeyBytes {
		return nil, &CorruptError{Path: MarkerName, Err: errors.New("its name-key slot does not open under this folder's key (damaged)")}
	}
	nk, err := NewNameKey(raw, m.Names.Long, dirID(m.Names.RootID))
	for i := range raw {
		raw[i] = 0
	}
	if err != nil {
		return nil, err
	}
	k.Names = nk
	return k, nil
}
