package e2edecrypt

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// A single encrypted file, `.fxe` — docs/E2E-ENCRYPTION.md → "Single
// encrypted files"; the browser's side is packages/core/src/lib/e2efile.ts.
//
//	[0..8)      magic "filexfxe"
//	[8]         version 0x01
//	[9..13)     header length H, uint32 big-endian (1 … 65 536)
//	[13..13+H)  header, UTF-8 JSON: the folder marker's slots (salt, iter,
//	            verify, fmk "wrapped", fmk_pw, rk, esc) plus dek, name,
//	            chunk, nonce, size and optional req
//	[13+H..)    STREAM body under the DEK
//
// The slots are read with the marker code (a v2 marker view), so a .fxe
// opens with its password or its recovery key exactly like a folder.

// FxeMagic starts every single encrypted file.
var FxeMagic = []byte("filexfxe")

// FxeExtension is the stored-name suffix of a single encrypted file.
const FxeExtension = ".fxe"

const (
	fxeVersion   = 1
	fxeFixedLen  = 13
	fxeMaxHeader = 64 * 1024
)

// knownFxeFeatures are the entries of a .fxe header's `req` this build
// honours (none yet).
var knownFxeFeatures = map[string]bool{}

// HasFxeMagic reports whether b starts with the .fxe magic.
func HasFxeMagic(b []byte) bool { return bytes.HasPrefix(b, FxeMagic) }

// FxeHeader is a parsed .fxe header.
type FxeHeader struct {
	slots  *Marker
	dek    string
	name   string
	chunk  int
	prefix []byte
	size   int64
}

type rawFxe struct {
	Salt   *string         `json:"salt"`
	Iter   *float64        `json:"iter"`
	Verify *string         `json:"verify"`
	Fmk    *string         `json:"fmk"`
	FmkPw  *string         `json:"fmk_pw"`
	Rk     *RecoverySlot   `json:"rk"`
	Dek    *string         `json:"dek"`
	Name   *string         `json:"name"`
	Chunk  *float64        `json:"chunk"`
	Nonce  *string         `json:"nonce"`
	Size   *float64        `json:"size"`
	Req    json.RawMessage `json:"req"`
}

// errFxeDamaged is a header that is not what any writer produces.
func errFxeDamaged(what string) error {
	return &CorruptError{Path: ".fxe header", Err: fmt.Errorf("damaged (%s)", what)}
}

// ParseFxeHeader reads the JSON header with the browser's rules
// (parseFxeHeader). An unknown `req` entry returns an *UnsupportedError.
func ParseFxeHeader(data []byte) (*FxeHeader, error) {
	if !utf8.Valid(data) {
		return nil, errFxeDamaged("not UTF-8")
	}
	var r rawFxe
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, errFxeDamaged("not JSON")
	}
	if r.Salt == nil || r.Iter == nil || r.Verify == nil {
		return nil, errFxeDamaged("password slot")
	}
	salt, err := base64.StdEncoding.DecodeString(*r.Salt)
	if err != nil || len(salt) == 0 {
		return nil, errFxeDamaged("salt")
	}
	if *r.Iter < 1 || *r.Iter != math.Trunc(*r.Iter) || *r.Iter > maxIterations {
		return nil, errFxeDamaged("iter")
	}
	if r.Fmk == nil || *r.Fmk != "wrapped" || r.FmkPw == nil {
		return nil, errFxeDamaged("fmk")
	}
	if r.Dek == nil || r.Name == nil {
		return nil, errFxeDamaged("dek/name")
	}
	if r.Chunk == nil || *r.Chunk != math.Trunc(*r.Chunk) || !ValidChunkLog2(int(*r.Chunk)) {
		return nil, errFxeDamaged("chunk")
	}
	if r.Nonce == nil {
		return nil, errFxeDamaged("nonce")
	}
	prefix, err := base64.StdEncoding.DecodeString(*r.Nonce)
	if err != nil || len(prefix) != streamPrefixLen {
		return nil, errFxeDamaged("nonce")
	}
	if r.Size == nil || *r.Size < 0 || *r.Size != math.Trunc(*r.Size) || *r.Size > (1<<53-1) {
		return nil, errFxeDamaged("size")
	}
	if r.Rk != nil && (r.Rk.Salt == "" || r.Rk.Blob == "") {
		return nil, errFxeDamaged("rk")
	}
	h := &FxeHeader{
		slots: &Marker{V: 2, Salt: salt, Iter: int(*r.Iter), Verify: *r.Verify, Fmk: "wrapped", FmkPw: *r.FmkPw, Rk: r.Rk},
		dek:   *r.Dek, name: *r.Name, chunk: int(*r.Chunk), prefix: prefix, size: int64(*r.Size),
	}
	if len(r.Req) > 0 && string(r.Req) != "null" {
		var req []string
		if json.Unmarshal(r.Req, &req) != nil {
			return nil, errFxeDamaged("req")
		}
		var unknown []string
		for _, f := range req {
			if !knownFxeFeatures[f] {
				unknown = append(unknown, f)
			}
		}
		if len(unknown) > 0 {
			return h, &UnsupportedError{Features: unknown}
		}
	}
	return h, nil
}

// Size is the plaintext length the header promises.
func (h *FxeHeader) Size() int64 { return h.size }

// HasRecovery reports whether the file has a recovery key slot.
func (h *FxeHeader) HasRecovery() bool { return h.slots.Rk != nil }

// FxeKeys is what an unlock hands back: the STREAM key and the original name.
type FxeKeys struct {
	DEK  []byte
	Name string
}

// Unlock opens the header with the password, or with the recovery key when
// recovery is true. A header whose key or name does not open under the
// unlocked file key is damage (a *CorruptError), not a wrong password.
func (h *FxeHeader) Unlock(secret string, recovery bool) (*FxeKeys, error) {
	var k *Keys
	var err error
	if recovery {
		k, err = h.slots.UnlockRecoveryKey(secret)
	} else {
		k, err = h.slots.UnlockPassword(secret)
	}
	if err != nil {
		return nil, err
	}
	dek, ok := gcmOpenB64(k.FMK, h.dek)
	if !ok || len(dek) != 32 {
		return nil, &CorruptError{Path: ".fxe header", Err: errors.New("its file key does not open under its own key (damaged)")}
	}
	nameBytes, ok := gcmOpenB64(k.FMK, h.name)
	if !ok || !utf8.Valid(nameBytes) {
		return nil, &CorruptError{Path: ".fxe header", Err: errors.New("its name does not open under its own key (damaged)")}
	}
	name := norm.NFC.String(string(nameBytes))
	if p := NameProblem(name); p != "" {
		return nil, &CorruptError{Path: ".fxe header", Err: fmt.Errorf("the decrypted name is not safe to write (%s)", p)}
	}
	return &FxeKeys{DEK: dek, Name: name}, nil
}

// ReadFxePrefix reads magic, version, length and header from r, leaving r at
// the first byte of the body.
func ReadFxePrefix(r io.Reader) (*FxeHeader, error) {
	fixed := make([]byte, fxeFixedLen)
	if _, err := io.ReadFull(r, fixed); err != nil {
		return nil, &CorruptError{Path: ".fxe", Err: errors.New("truncated before its header")}
	}
	if !HasFxeMagic(fixed) {
		return nil, errors.New("not a filex encrypted file (.fxe)")
	}
	if fixed[8] != fxeVersion {
		return nil, &UnsupportedError{Features: []string{fmt.Sprintf(".fxe version %d", fixed[8])}}
	}
	n := binary.BigEndian.Uint32(fixed[9:13])
	if n == 0 || n > fxeMaxHeader {
		return nil, errFxeDamaged("header length out of range")
	}
	hdr := make([]byte, n)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, &CorruptError{Path: ".fxe", Err: errors.New("truncated inside its header")}
	}
	return ParseFxeHeader(hdr)
}

// DecryptFxe writes the plaintext of a .fxe body (r positioned after the
// prefix) to w, checked end to end against the header's size.
func DecryptFxe(w io.Writer, r io.Reader, h *FxeHeader, k *FxeKeys) error {
	_, err := DecryptStream(w, r, k.DEK, h.prefix, h.chunk, h.size)
	if err != nil && errors.Is(err, ErrContent) {
		return &CorruptError{Path: k.Name, Err: err}
	}
	return err
}

// fxeInput is `filex decrypt <file>.fxe`: one file, its own key slots, no
// marker.
type fxeInput struct {
	path   string
	header *FxeHeader
	keys   *FxeKeys
	// explicit is the -o the person gave (the file to write), or "".
	explicit string
}

// openFxe resolves a .fxe input. With no -o the output is the ORIGINAL name
// next to the input — known only after the unlock, so that check happens in
// Run.
func openFxe(abs, out string) (*fxeInput, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h, err := ReadFxePrefix(bufio.NewReader(f))
	if err != nil {
		return nil, err
	}
	in := &fxeInput{path: abs, header: h}
	if out != "" {
		if in.explicit, err = filepath.Abs(out); err != nil {
			return nil, err
		}
		if _, err := os.Lstat(in.explicit); err == nil {
			return nil, fmt.Errorf("%s: %w", in.explicit, ErrFileExists)
		}
	}
	return in, nil
}

// ErrFileExists: the file a .fxe would decrypt to is already there.
var ErrFileExists = errors.New("the output file already exists — pass another with -o")

// target is where the plaintext goes: -o, or the original name next to the
// input (made safe for this OS).
func (in *fxeInput) target(goos string) (string, []string) {
	if in.explicit != "" {
		return in.explicit, nil
	}
	name := safeOutputName(in.keys.Name, goos)
	var warns []string
	if name != in.keys.Name {
		warns = append(warns, fmt.Sprintf("%s: written as %q (not a valid file name on %s)", in.keys.Name, name, goos))
	}
	return filepath.Join(filepath.Dir(in.path), name), warns
}

// run decrypts into a temporary file next to the target and renames it into
// place only when the last chunk verified: a wrong key or a damaged file
// leaves nothing behind.
func (in *fxeInput) run(goos string) (*Result, error) {
	target, warns := in.target(goos)
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("%s: %w", target, ErrFileExists)
	}
	src, err := os.Open(in.path)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	br := bufio.NewReaderSize(src, 256*1024)
	if _, err := ReadFxePrefix(br); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".partial-*")
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	bw := bufio.NewWriterSize(tmp, 256*1024)
	if err := DecryptFxe(bw, br, in.header, in.keys); err != nil {
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("%s: %w", target, ErrFileExists)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return nil, err
	}
	done = true
	return &Result{Out: target, Files: 1, Warnings: warns}, nil
}

// dirHasFxe reports whether a folder holds .fxe files at its top level — to
// say what to do instead of "no marker".
func dirHasFxe(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), FxeExtension) {
			return true
		}
	}
	return false
}
