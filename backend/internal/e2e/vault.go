package e2e

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"strings"
)

// The vault (encryption level 3): what the server and the command line know
// about its layout. docs/E2E-VAULT-FORMAT.md is the normative text; this file
// spells its constants and path rules ONCE for Go (internal/e2edecrypt imports
// them), as packages/core/src/lib/e2evault/layout.ts does for the browser.
//
// Nothing here decrypts anything. The server reads only what is in the clear:
// the key file's `v`, `req` and `vault` block, the names of packs and index
// files, and their plaintext headers (magic, version, kind, size, id,
// generation). The index body and every byte of content stay ciphertext.

const (
	VaultFeature         = "vault" // the req entry
	VaultFormat          = 1       // vault.v, header byte 8, body version
	VaultMinPackLog2     = 16      // readers
	VaultMaxPackLog2     = 24      // readers
	VaultDefaultPackLog2 = 22      // writers: 4 MiB
	VaultLargePackLog2   = 24      // writers: 16 MiB
	VaultPackHeaderLen   = 32
	VaultIndexHeaderLen  = 40
	VaultIndexMinSize    = 65536
	VaultIndexReadMax    = 64 << 20
	VaultIndexWriteMax   = 32 << 20
	VaultKindPack        = 0x50
	VaultKindIndex       = 0x49
	VaultKeepGenerations = 3
	VaultEntriesWriteMax = 250000
	VaultEntriesWarn     = 200000
	VaultEntriesReadMax  = 1000000
	VaultMaxDepth        = 256
)

// VaultMagicPrefix is the 8-byte magic of packs and index files.
var VaultMagicPrefix = []byte("filexvlt")

// HasVaultMagic reports whether b starts with the vault magic (a pack or an
// index file). HasEncryptedPrefix recognises it too.
func HasVaultMagic(b []byte) bool { return bytes.HasPrefix(b, VaultMagicPrefix) }

// VaultInfo is the key file's `vault` block.
type VaultInfo struct {
	V        int
	ID       [16]byte
	PackLog2 int
}

// IDHex is the vault id as 32 lower-case hex digits - the spelling the
// server keys its write lock by and names in its answers.
func (v VaultInfo) IDHex() string { return hex.EncodeToString(v.ID[:]) }

// ErrVaultMalformed is what ParseVaultKeyFile wraps for a key file that names
// the vault feature and breaks a rule of docs/E2E-VAULT-FORMAT.md → "The key
// file" (or for bytes that are not a JSON object at all).
var ErrVaultMalformed = errors.New("malformed vault key file")

// ErrVaultNewer is what ParseVaultKeyFile wraps for a vault whose format
// (vault.v) is newer than this filex: the reader refuses it and says a newer
// filex is needed.
var ErrVaultNewer = errors.New("the vault was written by a newer filex")

// ErrVaultHeader is what the header checks wrap.
var ErrVaultHeader = errors.New("bad vault header")

func vaultMalformed(why string) error { return fmt.Errorf("%w: %s", ErrVaultMalformed, why) }

// jsonInt reads one JSON integer exactly (no fraction, no exponent, and not a
// string: encoding/json would read "1" into a json.Number too).
func jsonInt(raw json.RawMessage) (int64, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || !(raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') {
		return 0, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var n json.Number
	if err := dec.Decode(&n); err != nil {
		return 0, false
	}
	v, err := n.Int64()
	if err != nil {
		return 0, false
	}
	return v, true
}

func jsonString(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// ParseVaultKeyFile reads a key file. isVault is false for a level-1/2 key
// file (no "vault" in req); err is non-nil for a key file that names "vault"
// and breaks a rule of docs/E2E-VAULT-FORMAT.md → "The key file" (req not
// exactly ["vault"], fmk not "wrapped", vault.v != 1, a bad id or pack), and
// for bytes that are not a JSON object (isVault false then).
//
// errors.Is(err, ErrVaultNewer) for a vault.v above 1 - asked first, so a
// later format that changes anything else is still told apart from damage;
// errors.Is(err, ErrVaultMalformed) for everything else.
func ParseVaultKeyFile(b []byte) (info VaultInfo, isVault bool, err error) {
	var kf map[string]json.RawMessage
	if jerr := json.Unmarshal(b, &kf); jerr != nil || kf == nil {
		return VaultInfo{}, false, vaultMalformed("not a JSON object")
	}
	reqRaw, hasReq := kf["req"]
	if !hasReq {
		return VaultInfo{}, false, nil
	}
	var req []json.RawMessage
	if jerr := json.Unmarshal(reqRaw, &req); jerr != nil {
		// A req that is not a list is no vault's - and no level-2 key file's
		// either; the marker parsers of levels 1 and 2 refuse it themselves.
		return VaultInfo{}, false, nil
	}
	named := false
	for _, r := range req {
		if s, ok := jsonString(r); ok && s == VaultFeature {
			named = true
		}
	}
	if !named {
		return VaultInfo{}, false, nil
	}

	vaultRaw, ok := kf["vault"]
	if !ok {
		return VaultInfo{}, true, vaultMalformed("no vault block")
	}
	var vb map[string]json.RawMessage
	if jerr := json.Unmarshal(vaultRaw, &vb); jerr != nil || vb == nil {
		return VaultInfo{}, true, vaultMalformed("the vault block is not an object")
	}
	ver, ok := jsonInt(vb["v"])
	if !ok {
		return VaultInfo{}, true, vaultMalformed("vault.v is not an integer")
	}
	if ver > VaultFormat {
		return VaultInfo{}, true, fmt.Errorf("%w: vault format %d, this filex reads %d", ErrVaultNewer, ver, VaultFormat)
	}
	if ver != VaultFormat {
		return VaultInfo{}, true, vaultMalformed(fmt.Sprintf("vault.v is %d", ver))
	}
	if v, ok := jsonInt(kf["v"]); !ok || v != 3 {
		return VaultInfo{}, true, vaultMalformed("the key file version is not 3")
	}
	if len(req) != 1 {
		return VaultInfo{}, true, vaultMalformed("req names more than the vault")
	}
	if fmk, ok := jsonString(kf["fmk"]); !ok || fmk != "wrapped" {
		return VaultInfo{}, true, vaultMalformed(`fmk is not "wrapped"`)
	}
	if pw, ok := jsonString(kf["fmk_pw"]); !ok || pw == "" {
		return VaultInfo{}, true, vaultMalformed("no fmk_pw")
	}
	id, ok := jsonString(vb["id"])
	if !ok || len(id) != 22 || strings.ContainsAny(id, "=+/") {
		return VaultInfo{}, true, vaultMalformed("vault.id is not 22 base64url characters")
	}
	raw, derr := base64.RawURLEncoding.DecodeString(id)
	if derr != nil || len(raw) != 16 {
		return VaultInfo{}, true, vaultMalformed("vault.id is not 16 bytes")
	}
	pack, ok := jsonInt(vb["pack"])
	if !ok || pack < VaultMinPackLog2 || pack > VaultMaxPackLog2 {
		return VaultInfo{}, true, vaultMalformed("vault.pack is not 16 to 24")
	}
	info = VaultInfo{V: int(ver), PackLog2: int(pack)}
	copy(info.ID[:], raw)
	return info, true, nil
}

// canonicalJSON re-encodes a JSON value with object keys sorted and no white
// space, numbers kept as written. Two spellings of one value - re-spaced, or
// its keys in another order - compare equal.
func canonicalJSON(raw json.RawMessage) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return out, true
}

// SameVaultBlock reports whether two key files agree on v, req and vault
// (compact JSON) - the rule a key-file rewrite must keep.
//
// The comparison is of the canonical form (keys sorted, no white space): a
// client that re-serialises the key file with its keys in another order has
// not changed the block. A field present in one and missing in the other is a
// change; bytes that are not a JSON object never agree.
func SameVaultBlock(before, after []byte) bool {
	var a, b map[string]json.RawMessage
	if json.Unmarshal(before, &a) != nil || json.Unmarshal(after, &b) != nil || a == nil || b == nil {
		return false
	}
	for _, k := range []string{"v", "req", VaultFeature} {
		av, aok := a[k]
		bv, bok := b[k]
		if aok != bok {
			return false
		}
		if !aok {
			continue
		}
		ac, ok1 := canonicalJSON(av)
		bc, ok2 := canonicalJSON(bv)
		if !ok1 || !ok2 || !bytes.Equal(ac, bc) {
			return false
		}
	}
	return true
}

type VaultPathKind int

const (
	VaultPathOther VaultPathKind = iota
	VaultPathKeyFile
	VaultPathPack
	VaultPathIndex
	VaultPathTemp // v/idx/.tmp-*
)

// VaultTempPrefix starts the name of the server's temporary index file in
// v/idx/ while it commits one.
const VaultTempPrefix = ".tmp-"

func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ParseVaultID reads a pack id or a vault id written as 32 lower-case hex
// digits (the spelling of a pack's name and of the `id` query parameter).
func ParseVaultID(s string) ([16]byte, bool) {
	var id [16]byte
	if !isLowerHex(s, 32) {
		return id, false
	}
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 16 {
		return id, false
	}
	copy(id[:], raw)
	return id, true
}

// ParseVaultGeneration reads a generation written as 16 lower-case hex digits
// (an index file's name without `.fxi`). Generation 0 is no generation.
func ParseVaultGeneration(s string) (uint64, bool) {
	if !isLowerHex(s, 16) {
		return 0, false
	}
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 8 {
		return 0, false
	}
	g := binary.BigEndian.Uint64(raw)
	return g, g != 0
}

// ParseVaultPath classifies rel, a path RELATIVE TO THE VAULT ROOT.
//
// The names are exact: lower-case hex only, a pack's folder the first two
// digits of its id, generation 0 no index. Anything else is VaultPathOther -
// a reader ignores it and a writer never makes it.
func ParseVaultPath(rel string) (kind VaultPathKind, id [16]byte, gen uint64) {
	rel = strings.Trim(rel, "/")
	if rel == MarkerName {
		return VaultPathKeyFile, id, 0
	}
	segs := strings.Split(rel, "/")
	switch {
	case len(segs) == 3 && segs[0] == "v" && segs[1] == "idx":
		name := segs[2]
		if strings.HasPrefix(name, VaultTempPrefix) && len(name) > len(VaultTempPrefix) {
			return VaultPathTemp, id, 0
		}
		if strings.HasSuffix(name, ".fxi") {
			if g, ok := ParseVaultGeneration(strings.TrimSuffix(name, ".fxi")); ok {
				return VaultPathIndex, id, g
			}
		}
	case len(segs) == 4 && segs[0] == "v" && segs[1] == "p":
		name := segs[3]
		if strings.HasSuffix(name, ".fxp") {
			if pid, ok := ParseVaultID(strings.TrimSuffix(name, ".fxp")); ok && segs[2] == name[:2] {
				return VaultPathPack, pid, 0
			}
		}
	}
	return VaultPathOther, [16]byte{}, 0
}

// VaultPackPath is "v/p/b0/b067….fxp".
func VaultPackPath(id [16]byte) string {
	h := hex.EncodeToString(id[:])
	return "v/p/" + h[:2] + "/" + h + ".fxp"
}

// VaultIndexPath is "v/idx/0000000000000003.fxi".
func VaultIndexPath(gen uint64) string {
	return fmt.Sprintf("v/idx/%016x.fxi", gen)
}

// VaultPadme is Padmé(n) (Nikitin et al., PETS 2019): E = ⌊log2 n⌋,
// S = ⌊log2 E⌋ + 1, step = 2^(E-S), the result n rounded up to a multiple of
// step. 0 for n ≤ 0.
func VaultPadme(n int64) int64 {
	if n <= 0 {
		return 0
	}
	e := bits.Len64(uint64(n)) - 1
	s := bits.Len64(uint64(e))
	step := int64(1) << uint(e-s)
	return (n + step - 1) / step * step
}

// VaultIndexFileSize is an index file's size for a body of bodyLen bytes:
// max(65536, Padmé(40+bodyLen+16)).
func VaultIndexFileSize(bodyLen int64) int64 {
	n := VaultPadme(VaultIndexHeaderLen + bodyLen + 16)
	if n < VaultIndexMinSize {
		return VaultIndexMinSize
	}
	return n
}

// ValidVaultIndexSize reports whether n can be an index file's size:
// 65536 ≤ n ≤ 64 MiB and Padmé(n) == n.
func ValidVaultIndexSize(n int64) bool {
	return n >= VaultIndexMinSize && n <= VaultIndexReadMax && VaultPadme(n) == n
}

func headerErr(why string) error { return fmt.Errorf("%w: %s", ErrVaultHeader, why) }

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// CheckVaultPackHeader / CheckVaultIndexHeader check the plaintext headers
// (magic, version, kind, zeros, log2/generation, id) - all the server can.
func CheckVaultPackHeader(b []byte, id [16]byte, packLog2 int) error {
	if len(b) < VaultPackHeaderLen {
		return headerErr("shorter than a pack header")
	}
	if !bytes.Equal(b[:8], VaultMagicPrefix) {
		return headerErr("no vault magic")
	}
	if b[8] != VaultFormat {
		return headerErr(fmt.Sprintf("format version %d", b[8]))
	}
	if b[9] != VaultKindPack {
		return headerErr("not a pack")
	}
	if int(b[10]) != packLog2 {
		return headerErr(fmt.Sprintf("pack size 2^%d, the vault's is 2^%d", b[10], packLog2))
	}
	if !allZero(b[11:16]) {
		return headerErr("reserved bytes are not zero")
	}
	if !bytes.Equal(b[16:32], id[:]) {
		return headerErr("the pack id is not its name")
	}
	return nil
}

func CheckVaultIndexHeader(b []byte, gen uint64) error {
	if len(b) < VaultIndexHeaderLen {
		return headerErr("shorter than an index header")
	}
	if !bytes.Equal(b[:8], VaultMagicPrefix) {
		return headerErr("no vault magic")
	}
	if b[8] != VaultFormat {
		return headerErr(fmt.Sprintf("format version %d", b[8]))
	}
	if b[9] != VaultKindIndex {
		return headerErr("not an index file")
	}
	if !allZero(b[10:16]) {
		return headerErr("reserved bytes are not zero")
	}
	if gen == 0 || binary.BigEndian.Uint64(b[16:24]) != gen {
		return headerErr("the generation is not its name")
	}
	return nil
}
