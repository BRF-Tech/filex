package e2e

import (
	"bytes"
	"encoding/json"
	"sort"
)

// wiring:e2 keyfile — what a rewrite of a key file changed. The watch that
// acts on it (audit row, old versions deleted) is package keyfilewatch.
//
// The server's own record of a rewritten key file:
//
// The browser changes an encrypted folder's password, its folder key, its
// level or its escrow slot by writing a new `.filex-e2e.json`. The server
// cannot read what is in the slots, but it can see WHICH of them changed, and
// it sees every write, whichever surface it came through. So:
//
//   - every rewrite of a key file lands in the audit log
//     (`e2e.key_file_rewritten`), with what changed and the surface it came
//     from — whether or not the client announced it (POST
//     /api/files/e2e/password-changed is the announcement; this is the part
//     that does not depend on the client's good behaviour);
//   - when a KEY SLOT changed — the password slot (`salt`/`verify`/`fmk_pw`)
//     or the recovery slot (`rk`) — the earlier versions of the key file are
//     deleted. Each of them is the folder key wrapped under an earlier
//     password or recovery key: keeping them would keep the old password
//     working for anyone who can restore a version. A change of level or an
//     added escrow slot leaves them alone (nothing old opens anything new).
//
// ⚠ Deleting the versions removes the server's copies only. A key file
// downloaded, synced to a desktop, or in a backup still opens with the old
// password; docs/E2E-ENCRYPTION.md → "What a password change does not undo".

// keyFile is the part of a marker the diff looks at. Unknown fields are
// compared too (as raw JSON), so a slot this server does not know about still
// counts as a change.
type keyFile map[string]json.RawMessage

// KeyFileDiff says what a rewrite changed.
type KeyFileDiff struct {
	// Valid: the new bytes are a JSON object with a version and a salt — a
	// key file, not a stray upload under its name. Nothing is deleted
	// otherwise.
	Valid bool
	// Changes, sorted: "password", "recovery_key", "escrow", "level",
	// "rekey", "conversion", "created" and "other".
	Changes []string
	// KeySlotChanged: the password or the recovery slot changed, so every
	// earlier version wraps the folder key under a secret that should no
	// longer open it.
	KeySlotChanged bool
}

func parseKeyFile(b []byte) (keyFile, bool) {
	var k keyFile
	if err := json.Unmarshal(b, &k); err != nil || k == nil {
		return nil, false
	}
	if _, ok := k["v"]; !ok {
		return nil, false
	}
	if _, ok := k["salt"]; !ok {
		return nil, false
	}
	return k, true
}

func sameField(a, b keyFile, key string) bool {
	av, aok := a[key]
	bv, bok := b[key]
	if aok != bok {
		return false
	}
	return !aok || bytes.Equal(compactJSON(av), compactJSON(bv))
}

func compactJSON(b json.RawMessage) []byte {
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		return b
	}
	return out.Bytes()
}

// DiffKeyFiles compares the key file before a rewrite with the one after it.
// before may be nil (not known — no version was kept).
func DiffKeyFiles(before, after []byte) KeyFileDiff {
	next, ok := parseKeyFile(after)
	d := KeyFileDiff{Valid: ok}
	if !ok {
		return d
	}
	prev, ok := parseKeyFile(before)
	if !ok {
		d.Changes = []string{"created"}
		return d
	}
	set := map[string]bool{}
	for _, k := range []string{"salt", "verify", "fmk_pw", "iter"} {
		if !sameField(prev, next, k) {
			set["password"] = true
		}
	}
	if !sameField(prev, next, "rk") {
		set["recovery_key"] = true
	}
	if !sameField(prev, next, "esc") {
		set["escrow"] = true
	}
	if !sameField(prev, next, "names") {
		set["level"] = true
	}
	if !sameField(prev, next, "rekey") || !sameField(prev, next, "fmk") {
		set["rekey"] = true
	}
	if !sameField(prev, next, "conv") {
		set["conversion"] = true
	}
	// `v` and `req` follow the slots above (a names slot, a re-key or a
	// conversion makes a v3 with a `req` entry); moving on their own they are
	// something else.
	if (!sameField(prev, next, "req") || !sameField(prev, next, "v")) && !set["level"] && !set["rekey"] && !set["conversion"] {
		set["other"] = true
	}
	known := map[string]bool{
		"v": true, "salt": true, "verify": true, "fmk_pw": true, "iter": true, "rk": true,
		"esc": true, "names": true, "req": true, "rekey": true, "fmk": true, "conv": true,
	}
	for k := range prev {
		if !known[k] && !sameField(prev, next, k) {
			set["other"] = true
		}
	}
	for k := range next {
		if !known[k] && !sameField(prev, next, k) {
			set["other"] = true
		}
	}
	for c := range set {
		d.Changes = append(d.Changes, c)
	}
	sort.Strings(d.Changes)
	d.KeySlotChanged = set["password"] || set["recovery_key"]
	return d
}

// ConversionPending reports whether a key file says an in-place conversion is
// under way: v3, `req` naming "conv", and a conv slot with `pending: true`
// (docs/E2E-ENCRYPTION.md → "Encrypting a folder you already have"). While it
// is, and only then, the server lets a write replace a plaintext file in the
// folder with its ciphertext without keeping the plaintext as a version.
func ConversionPending(markerBytes []byte) bool {
	var m struct {
		V    int      `json:"v"`
		Req  []string `json:"req"`
		Conv *struct {
			Pending bool `json:"pending"`
		} `json:"conv"`
	}
	if err := json.Unmarshal(markerBytes, &m); err != nil || m.V != 3 || m.Conv == nil || !m.Conv.Pending {
		return false
	}
	for _, f := range m.Req {
		if f == "conv" {
			return true
		}
	}
	return false
}
