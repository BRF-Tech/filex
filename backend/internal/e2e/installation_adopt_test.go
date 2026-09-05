package e2e

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Adopting escrow on an installation that already exists.
//
// v0.31.0 refused every change to the escrow key, including "none" ->
// "some". Measured on our own two deployments right after the rollout,
// both installation.json files read `"e2e_escrow_kid": ""` — so the only
// way to act on the decision to turn escrow on was to throw the data
// directory away, which for a production instance is not a way at all.
//
// These tests hold two lines at once, and they pull in opposite
// directions: adoption must be POSSIBLE (or nobody ever chooses escrow,
// because nobody decides key-escrow policy before they have any files),
// and it must stay DELIBERATE and RECORDED (or an installation quietly
// grows a second key holder because somebody pasted a compose file).

func TestPinInstallation_AdoptionFromEmptyIsRecordedAsAdoption(t *testing.T) {
	ctx := context.Background()
	st := newFakeSettings()
	dir := t.TempDir()
	key := testKey(t)

	first, err := PinInstallation(ctx, st, dir, nil, "2026-09-05T12:02:24Z", Adoption{})
	if err != nil {
		t.Fatalf("first boot without escrow: %v", err)
	}
	if first.E2EEscrowKID != "" || first.EscrowAdopted() {
		t.Fatalf("a first boot without escrow is not an adoption: %+v", first)
	}

	got, err := PinInstallation(ctx, st, dir, key, "2026-11-20T08:30:00Z",
		Adoption{Allowed: true, By: AdoptedByEnv})
	if err != nil {
		t.Fatalf("adoption with explicit consent must start, got %v", err)
	}
	if got.E2EEscrowKID != key.KID {
		t.Fatalf("kid = %q, want %q", got.E2EEscrowKID, key.KID)
	}
	if got.E2EEscrowSPKI != key.SPKI || got.E2EEscrowAlg != EscrowAlg {
		t.Error("the adopted record must carry the key material, like a first boot does")
	}

	// The record has to answer "was this first-boot or adoption?" on its own,
	// months later, with no access to the log that carried the boot line.
	if !got.EscrowAdopted() {
		t.Fatal("the record does not say this was an adoption")
	}
	if !got.JustAdopted {
		t.Error("the boot that performed the adoption must be able to say so")
	}
	if got.E2EEscrowAdoptedAt != "2026-11-20T08:30:00Z" {
		t.Errorf("adopted_at = %q, want the adopting boot's clock", got.E2EEscrowAdoptedAt)
	}
	if got.E2EEscrowAdoptedBy != AdoptedByEnv {
		t.Errorf("adopted_by = %q, want %q — the record must name HOW consent was given",
			got.E2EEscrowAdoptedBy, AdoptedByEnv)
	}
	// ...and it must not eat the installation's own birthday doing it. Those
	// are two different questions and a reader needs both to work out which
	// folders predate the second key.
	if got.PinnedAt != "2026-09-05T12:02:24Z" {
		t.Errorf("pinned_at = %q, want the FIRST boot — adoption must not backdate the install",
			got.PinnedAt)
	}
	if got.PinnedBy != PinnedByFirstBoot {
		t.Errorf("pinned_by = %q, want %q", got.PinnedBy, PinnedByFirstBoot)
	}
	// The note has to carry BOTH halves of the truth, or it becomes the kind
	// of page that lies. Half one: the boundary, and that no operator action
	// moves it. Half two: the owner CAN grant it from inside — an earlier
	// wording said "can never be opened with the escrow key", which stopped
	// being true the day folders could be offered a slot at unlock.
	for _, want := range []string{"BEFORE e2e_escrow_adopted_at", "no operator action", "OWNER"} {
		if !strings.Contains(got.E2EEscrowAdoptionNote, want) {
			t.Errorf("the record's own warning is missing %q, got %q", want, got.E2EEscrowAdoptionNote)
		}
	}

	// The mirror is the copy an operator actually reads.
	b, err := os.ReadFile(filepath.Join(dir, InstallationFile))
	if err != nil {
		t.Fatalf("mirror: %v", err)
	}
	var mirrored Installation
	if err := json.Unmarshal(b, &mirrored); err != nil {
		t.Fatalf("mirror is not JSON: %v", err)
	}
	if mirrored.E2EEscrowAdoptedAt != got.E2EEscrowAdoptedAt ||
		mirrored.E2EEscrowAdoptedBy != got.E2EEscrowAdoptedBy ||
		mirrored.E2EEscrowAdoptionNote == "" {
		t.Errorf("the mirror does not carry the adoption record: %s", b)
	}

	// Restarts afterwards are quiet, and they do not re-date the adoption —
	// the flag is very likely still sitting in the operator's env file.
	again, err := PinInstallation(ctx, st, dir, key, "2027-01-01T00:00:00Z",
		Adoption{Allowed: true, By: AdoptedByEnv})
	if err != nil {
		t.Fatalf("restart after adoption: %v", err)
	}
	if again.E2EEscrowAdoptedAt != got.E2EEscrowAdoptedAt {
		t.Errorf("a restart moved adopted_at to %q — the adoption happened once",
			again.E2EEscrowAdoptedAt)
	}
	if again.JustAdopted {
		t.Error("a later restart must not announce a second adoption")
	}
	// JustAdopted describes the process, not the installation, so it must
	// never reach the record — a reader of installation.json would take it
	// for a fact about the install.
	if b, _ := json.Marshal(again); strings.Contains(string(b), "JustAdopted") ||
		strings.Contains(string(b), "just_adopted") {
		t.Errorf("JustAdopted leaked into the record: %s", b)
	}
}

func TestPinInstallation_AdoptionNeedsExplicitConsent(t *testing.T) {
	// Without the flag, empty -> set is still a start-time failure. An
	// escrow key arrives by being pasted into a compose or values file,
	// usually copied from somewhere else, and that is exactly the shape of
	// an accident: the installation gains a second key holder, only new
	// folders get it, and nothing looks wrong until the key is needed on an
	// old folder and does not work.
	ctx := context.Background()
	st := newFakeSettings()
	dir := t.TempDir()
	if _, err := PinInstallation(ctx, st, dir, nil, "t0", Adoption{}); err != nil {
		t.Fatalf("first boot: %v", err)
	}

	_, err := PinInstallation(ctx, st, dir, testKey(t), "t1", Adoption{})
	if err == nil {
		t.Fatal("setting the escrow key alone must not adopt")
	}
	var mismatch *InstallationMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("want InstallationMismatchError, got %T", err)
	}
	if !mismatch.Adoptable {
		t.Error("empty -> set is the adoptable transition and the error must say so")
	}
	msg := err.Error()
	// The message is the feature: it has to name the flag AND the limit, or
	// an operator adopts believing their existing folders came along.
	for _, want := range []string{
		EnvEscrowAdopt,
		"NOT retroactive",
		"would NOT give you access to any existing",
		"created BEFORE it opens only with its password",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message is missing %q\n--- message ---\n%s", want, msg)
		}
	}
}

func TestPinInstallation_AdoptionDoesNotUnlockASwapOrARemoval(t *testing.T) {
	// The two transitions that stay refused. The flag is consent to ADD a
	// key to an installation that has none; it is not a general override,
	// and a reader who found it in a changelog must not be able to use it
	// to rotate a key.
	ctx := context.Background()
	consent := Adoption{Allowed: true, By: AdoptedByEnv}

	t.Run("set to a different key", func(t *testing.T) {
		st, dir := newFakeSettings(), t.TempDir()
		if _, err := PinInstallation(ctx, st, dir, testKey(t), "t0", Adoption{}); err != nil {
			t.Fatalf("first boot: %v", err)
		}
		_, err := PinInstallation(ctx, st, dir, testKey(t), "t1", consent)
		if err == nil {
			t.Fatal("swapping the escrow key must still refuse to start, flag or no flag")
		}
		var mismatch *InstallationMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("want InstallationMismatchError, got %T", err)
		}
		if mismatch.Adoptable {
			t.Error("a key swap is not adoptable and the message must not offer the flag")
		}
		if !strings.Contains(err.Error(), EnvEscrowAdopt) {
			t.Error("the message should still say the flag does not cover this, " +
				"or an operator will try it and read the same refusal twice")
		}
	})

	t.Run("set to empty", func(t *testing.T) {
		st, dir := newFakeSettings(), t.TempDir()
		if _, err := PinInstallation(ctx, st, dir, testKey(t), "t0", Adoption{}); err != nil {
			t.Fatalf("first boot: %v", err)
		}
		_, err := PinInstallation(ctx, st, dir, nil, "t1", consent)
		if err == nil {
			t.Fatal("removing the escrow key must still refuse to start, flag or no flag")
		}
		var mismatch *InstallationMismatchError
		if !errors.As(err, &mismatch) || mismatch.Adoptable {
			t.Errorf("removal is not adoptable, got %#v", mismatch)
		}
	})
}

func TestPinInstallation_ConsentOnAFirstBootIsNotAnAdoption(t *testing.T) {
	// An operator who reads about the flag and sets it on a brand-new
	// install must not end up with a record that says escrow was adopted.
	// The distinction is the whole reason the fields exist.
	st, dir := newFakeSettings(), t.TempDir()
	got, err := PinInstallation(context.Background(), st, dir, testKey(t), "t0",
		Adoption{Allowed: true, By: AdoptedByEnv})
	if err != nil {
		t.Fatalf("first boot with the flag set: %v", err)
	}
	if got.EscrowAdopted() {
		t.Errorf("a first boot is not an adoption, got adopted_at=%q", got.E2EEscrowAdoptedAt)
	}
	if got.PinnedBy != PinnedByFirstBoot {
		t.Errorf("pinned_by = %q, want %q", got.PinnedBy, PinnedByFirstBoot)
	}
}

func TestResolveAdoption(t *testing.T) {
	t.Setenv(EnvEscrowAdopt, "")
	if a := ResolveAdoption(); a.Allowed {
		t.Error("unset means no consent")
	}
	for _, v := range []string{"0", "false", "yes", "please", " "} {
		t.Setenv(EnvEscrowAdopt, v)
		if a := ResolveAdoption(); a.Allowed {
			t.Errorf("%q must not be consent — filex booleans are 1/true only", v)
		}
	}
	for _, v := range []string{"1", "true", "TRUE", " true "} {
		t.Setenv(EnvEscrowAdopt, v)
		a := ResolveAdoption()
		if !a.Allowed || a.By != AdoptedByEnv {
			t.Errorf("%q should be consent, got %#v", v, a)
		}
	}
}

// ── The one that matters: adoption is not retroactive ────────────────
//
// Everything above is bookkeeping. This is the arithmetic, and it is
// measured over real key material and real ciphertext rather than asserted
// about a struct field.
//
// The two folders in testdata/adoption were produced by
// packages/core/src/lib/e2ecrypto.ts — the module the browser actually
// runs — via testdata/adoption/gen.mjs. `before/` was created while escrow
// was off; `after/` was created once escrow had been adopted. This test
// then boots the real pin/adopt sequence and does the crypto in Go: real
// RSA-OAEP-256 against the real escrow private key, real AES-GCM against
// the real file headers.
//
// What it has to show is not "the marker has no esc field" — that is the
// input, not the finding. It is that an operator standing there WITH the
// escrow private key, after a successful adoption, cannot turn it into the
// bytes of a file that existed a minute earlier.
func TestEscrowAdoption_IsNotRetroactive(t *testing.T) {
	ctx := context.Background()
	fx := loadAdoptionFixtures(t)

	// The boot sequence, for real: an installation that ran without escrow,
	// then adopted it.
	st, dir := newFakeSettings(), t.TempDir()
	if _, err := PinInstallation(ctx, st, dir, nil, "2026-09-05T12:02:24Z", Adoption{}); err != nil {
		t.Fatalf("first boot without escrow: %v", err)
	}
	inst, err := PinInstallation(ctx, st, dir, fx.escrowPub, "2026-11-20T08:30:00Z",
		Adoption{Allowed: true, By: AdoptedByEnv})
	if err != nil {
		t.Fatalf("adoption: %v", err)
	}
	if !inst.EscrowAdopted() {
		t.Fatal("adoption did not take")
	}
	// The server's key id and the client's must be the same string, or the
	// two halves of this feature are talking about different keys. The
	// fixture's `esc.kid` was computed in the browser; ours in Go.
	if fx.after.Esc.KID != inst.E2EEscrowKID {
		t.Fatalf("kid disagreement: client wrote %q, server pinned %q",
			fx.after.Esc.KID, inst.E2EEscrowKID)
	}

	// (1) The escrow key works on a folder created AFTER the adoption, all
	// the way down to plaintext. Without this the rest proves nothing: a
	// broken escrow path would also "fail" on the old folder.
	fmkAfter := unsealEscrowFMK(t, fx.after, fx.escrowPriv)
	if got := decryptE2EFile(t, fmkAfter, fx.afterFile); got != fx.plaintext {
		t.Fatalf("escrow key did not open the post-adoption folder: %q", got)
	}

	// (2) The folder that existed BEFORE the adoption has no escrow slot,
	// so there is nothing for the private key to open. This is the input.
	if fx.before.Esc != nil {
		t.Fatal("fixture is wrong: the pre-adoption folder must have no escrow slot")
	}

	// (3) ...and that is not a formality the operator can route around. The
	// only FMK the escrow private key can produce on this installation is
	// the post-adoption folder's, and it is the wrong key for these bytes:
	// a real AES-GCM tag mismatch, not a policy check.
	if _, err := tryDecryptE2EFile(fmkAfter, fx.beforeFile); err == nil {
		t.Fatal("the pre-adoption file decrypted with an escrow-derived key — " +
			"the folders are not cryptographically separated")
	}

	// (4) The pre-adoption folder is not damaged, merely out of reach: its
	// own password still opens it. Adoption takes nothing away, it just
	// does not reach backwards.
	fmkBefore := unsealPasswordFMK(t, fx.before, fx.password)
	if got := decryptE2EFile(t, fmkBefore, fx.beforeFile); got != fx.plaintext {
		t.Fatalf("the pre-adoption folder stopped opening with its own password: %q", got)
	}

	// (5) And symmetrically, the pre-adoption folder's own key is not a way
	// into the new one either. Each folder has its own FMK; that is what
	// makes (3) a statement about escrow rather than about this fixture.
	if _, err := tryDecryptE2EFile(fmkBefore, fx.afterFile); err == nil {
		t.Fatal("two different folders shared a master key")
	}
}

// ── The owner's door: an existing folder can be OFFERED a slot ───────
//
// Adoption is still not retroactive and the server still cannot reach a
// folder that already exists — that is the test above and it has not moved.
// What changed is who else can: the folder's OWNER, from the browser, with
// the password, at the moment of an unlock.
//
// That distinction is worth exactly nothing if it is only asserted about a
// struct field, so it is measured the same way the adoption test is. The
// folders in testdata/adoption/upgraded and .../declined were produced by
// packages/core/src/lib/e2ecrypto.ts — the module the browser runs — via
// gen.mjs, and the crypto below is real RSA-OAEP-256 and real AES-GCM.
//
// ⚠ The file in `upgraded/` was encrypted BEFORE the slot was added and was
// never rewritten. If the offer worked by re-encrypting anything, this test
// would still pass and the feature would still be wrong; that is why the
// marker fields are compared too.
func TestEscrowOffer_OwnerCanGrantWhatTheOperatorCannotTake(t *testing.T) {
	fx := loadAdoptionFixtures(t)

	// (1) The folder whose owner accepted. Its file predates the slot and
	// opens with the escrow private key, all the way to plaintext.
	if fx.upgraded.Esc == nil {
		t.Fatal("fixture is wrong: the upgraded folder must have an escrow slot")
	}
	if fx.upgraded.Esc.KID != fx.escrowPub.KID {
		t.Fatalf("the slot names key %q, this installation holds %q — a folder must never be "+
			"labelled with a key that does not open it", fx.upgraded.Esc.KID, fx.escrowPub.KID)
	}
	fmkUpgraded := unsealEscrowFMK(t, fx.upgraded, fx.escrowPriv)
	if got := decryptE2EFile(t, fmkUpgraded, fx.upgradedFile); got != fx.plaintext {
		t.Fatalf("escrow key did not open the upgraded folder: %q", got)
	}

	// (2) ...and the owner did not lose their own way in doing it. The
	// password still produces the same key, which is the same key, which is
	// how we know nothing was re-encrypted behind their back.
	fmkByPassword := unsealPasswordFMK(t, fx.upgraded, fx.password)
	if !bytes.Equal(fmkByPassword, fmkUpgraded) {
		t.Fatal("the escrow slot holds a different master key than the password does — " +
			"one of the two doors opens a folder that is not this one")
	}

	// (3) The folder whose owner declined. The answer is recorded, and it is
	// recorded IN THE FOLDER: a decision that lived in browser storage would
	// not survive a second device or a cleared cache, and the question would
	// come back until somebody clicked it away without reading.
	if fx.declined.EscDeclined == "" {
		t.Error("a refusal must be recorded, or the offer returns on every unlock")
	}
	if fx.declined.Esc != nil {
		t.Fatal("declining must not create a slot")
	}

	// (4) And declining actually means it. Not "no slot in the struct" —
	// the operator, holding the real private key, gets nothing. The only FMK
	// that key can produce on this installation belongs to another folder,
	// and it is the wrong key for these bytes: a real AES-GCM tag mismatch.
	if _, err := tryDecryptE2EFile(fmkUpgraded, fx.declinedFile); err == nil {
		t.Fatal("a declined folder decrypted with an escrow-derived key — declining is decoration")
	}
	if _, err := tryDecryptE2EFile(unsealEscrowFMK(t, fx.after, fx.escrowPriv), fx.declinedFile); err == nil {
		t.Fatal("a declined folder decrypted with the post-adoption folder's key")
	}

	// (5) The declined folder is not damaged, only private: its own password
	// still opens it. Saying no costs nothing.
	fmkDeclined := unsealPasswordFMK(t, fx.declined, fx.password)
	if got := decryptE2EFile(t, fmkDeclined, fx.declinedFile); got != fx.plaintext {
		t.Fatalf("declining broke the folder: %q", got)
	}

	// (6) The untouched control. A folder nobody was asked about behaves
	// exactly as it did before any of this existed.
	if fx.before.Esc != nil || fx.before.EscDeclined != "" {
		t.Error("the pre-adoption fixture should carry neither a slot nor an answer")
	}
}

// ── fixture loading + the client's crypto, in Go ─────────────────────

type escSlot struct {
	KID  string `json:"kid"`
	Alg  string `json:"alg"`
	Blob string `json:"blob"`
}

type marker struct {
	V      int      `json:"v"`
	Salt   string   `json:"salt"`
	Iter   int      `json:"iter"`
	Verify string   `json:"verify"`
	FMK    string   `json:"fmk"`
	FMKPw  string   `json:"fmk_pw"`
	Esc    *escSlot `json:"esc"`
	// EscDeclined records that this folder's owner was offered an escrow
	// slot and said no. The server never writes it and never reads it for
	// any decision — it is here so this test can prove that a declined
	// folder carries the answer and still carries no key.
	EscDeclined string `json:"esc_declined"`
}

type adoptionFixtures struct {
	escrowPub  *EscrowKey
	escrowPriv *rsa.PrivateKey
	before     marker
	after      marker
	upgraded   marker
	declined   marker
	beforeFile []byte
	afterFile  []byte
	// upgradedFile was encrypted BEFORE its folder gained an escrow slot and
	// was never rewritten. It is the byte-level claim the offer makes.
	upgradedFile []byte
	declinedFile []byte
	password     string
	plaintext    string
}

func loadAdoptionFixtures(t *testing.T) adoptionFixtures {
	t.Helper()
	base := filepath.Join("testdata", "adoption")
	read := func(parts ...string) []byte {
		b, err := os.ReadFile(filepath.Join(append([]string{base}, parts...)...))
		if err != nil {
			t.Fatalf("fixture %v: %v (regenerate with testdata/adoption/gen.mjs)", parts, err)
		}
		return b
	}
	readMarker := func(dir string) marker {
		var m marker
		if err := json.Unmarshal(read(dir, "marker.json"), &m); err != nil {
			t.Fatalf("%s/marker.json: %v", dir, err)
		}
		if m.V != 2 || m.FMK != "wrapped" {
			t.Fatalf("%s/marker.json is not the v2 wrapped-FMK shape this test reasons about: %+v", dir, m)
		}
		return m
	}
	pub, err := ParseEscrowPublicKey(string(read("escrow_pub.b64")))
	if err != nil {
		t.Fatalf("fixture escrow public key: %v", err)
	}
	privDER, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(read("escrow_priv.b64"))))
	if err != nil {
		t.Fatalf("fixture escrow private key is not base64: %v", err)
	}
	anyKey, err := x509.ParsePKCS8PrivateKey(privDER)
	if err != nil {
		t.Fatalf("fixture escrow private key: %v", err)
	}
	priv, ok := anyKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("fixture escrow private key is %T, want RSA", anyKey)
	}
	var meta struct {
		Password  string `json:"password"`
		Plaintext string `json:"plaintext"`
	}
	if err := json.Unmarshal(read("meta.json"), &meta); err != nil {
		t.Fatalf("meta.json: %v", err)
	}
	return adoptionFixtures{
		escrowPub:    pub,
		escrowPriv:   priv,
		before:       readMarker("before"),
		after:        readMarker("after"),
		upgraded:     readMarker("upgraded"),
		declined:     readMarker("declined"),
		beforeFile:   read("before", "secret.bin"),
		afterFile:    read("after", "secret.bin"),
		upgradedFile: read("upgraded", "secret.bin"),
		declinedFile: read("declined", "secret.bin"),
		password:     meta.Password,
		plaintext:    meta.Plaintext,
	}
}

// unsealEscrowFMK is the operator's path: RSA-OAEP-256 with the escrow
// private key over the marker's `esc.blob`.
func unsealEscrowFMK(t *testing.T, m marker, priv *rsa.PrivateKey) []byte {
	t.Helper()
	if m.Esc == nil {
		t.Fatal("no escrow slot")
	}
	if m.Esc.Alg != EscrowAlg {
		t.Fatalf("escrow slot alg = %q, want %q", m.Esc.Alg, EscrowAlg)
	}
	blob, err := base64.StdEncoding.DecodeString(m.Esc.Blob)
	if err != nil {
		t.Fatalf("esc.blob: %v", err)
	}
	fmk, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, priv, blob, nil)
	if err != nil {
		t.Fatalf("escrow unwrap: %v", err)
	}
	if len(fmk) != 32 {
		t.Fatalf("escrow slot yielded %d bytes, want a 32-byte FMK", len(fmk))
	}
	return fmk
}

// unsealPasswordFMK is the user's path: PBKDF2-SHA256 to the KEK, then
// AES-GCM over `fmk_pw` (12-byte IV || ciphertext), exactly as
// deriveKek + gcmOpen do in e2ecrypto.ts.
func unsealPasswordFMK(t *testing.T, m marker, password string) []byte {
	t.Helper()
	salt, err := base64.StdEncoding.DecodeString(m.Salt)
	if err != nil {
		t.Fatalf("marker salt: %v", err)
	}
	kek, err := pbkdf2.Key(sha256.New, password, salt, m.Iter, 32)
	if err != nil {
		t.Fatalf("pbkdf2: %v", err)
	}
	blob, err := base64.StdEncoding.DecodeString(m.FMKPw)
	if err != nil {
		t.Fatalf("fmk_pw: %v", err)
	}
	fmk, err := gcmOpen(kek, blob)
	if err != nil {
		t.Fatalf("password unwrap: %v", err)
	}
	return fmk
}

// decryptE2EFile / tryDecryptE2EFile mirror decryptFile in e2ecrypto.ts:
// a fixed 97-byte header, the DEK unwrapped by the FMK, the body by the DEK.
func decryptE2EFile(t *testing.T, fmk, data []byte) string {
	t.Helper()
	out, err := tryDecryptE2EFile(fmk, data)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	return string(out)
}

func tryDecryptE2EFile(fmk, data []byte) ([]byte, error) {
	const headerLen = 97
	if len(data) < headerLen || string(data[:8]) != string(MagicPrefix) {
		return nil, errors.New("not a filex e2e file")
	}
	if data[8] != 1 {
		return nil, errors.New("unsupported file version")
	}
	wrapIV, wrappedDEK, dataIV := data[9:21], data[21:69], data[69:81]
	dek, err := gcmOpenIV(fmk, wrapIV, wrappedDEK)
	if err != nil {
		return nil, err
	}
	return gcmOpenIV(dek, dataIV, data[headerLen:])
}

// gcmOpen splits the 12-byte IV the client prepends, then opens.
func gcmOpen(key, blob []byte) ([]byte, error) {
	if len(blob) < 12 {
		return nil, errors.New("blob too short")
	}
	return gcmOpenIV(key, blob[:12], blob[12:])
}

func gcmOpenIV(key, iv, ct []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return g.Open(nil, iv, ct, nil)
}
