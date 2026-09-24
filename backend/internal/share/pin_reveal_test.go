package share

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// A share's PIN is stored TWICE and the two halves do different jobs
// (migration 00049): bcrypt still gates the visitor, secretbox lets the OWNER
// and an ADMIN be told the PIN again.
//
// Owner's decision, 2026-09-20: *"paylaşımın sahibi ve admin alabilir
// şifreyi."* Before it, a PIN was unrecoverable by construction and the only
// answer to "what did I send them?" was to mint a new link — which breaks the
// one already sitting in somebody's inbox.
//
// ⚠ The thing this file has to keep true is not "the PIN comes back". It is
// that it comes back WITHOUT the column holding it in clear, and without the
// gate getting any weaker.

const testKey = "a-long-enough-instance-secret-from-the-environment"

// withKey builds a Service whose PIN box is armed with an instance secret —
// i.e. the production wiring (internal/server.New calls AttachSecret).
func withKey(t *testing.T, hash string) (*Service, *model.Node) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	n := seedNode(t, store, "pinenc-"+hash)
	svc := NewService(store)
	svc.AttachSecret(testKey)
	return svc, n
}

// The whole round trip: Create seals, the column is not the PIN, RevealPIN
// gives it back.
func TestPINSealRoundTrip(t *testing.T) {
	svc, n := withKey(t, "roundtrip")
	const pin = "834595"

	sh, err := svc.Create(context.Background(), CreateOpts{NodeID: n.ID, PIN: pin})
	require.NoError(t, err)

	require.NotEmpty(t, sh.PinEnc, "a PIN minted on an instance WITH a key must be sealed, or it can never be shown")
	require.True(t, secretbox.IsSealed(sh.PinEnc),
		"the stored value must carry the enc:v1: marker — a column that mixes sealed and plain values with nothing to tell them apart is one that gets guessed at")
	require.NotContains(t, sh.PinEnc, pin, "the sealed column must not contain the PIN itself")
	require.True(t, sh.PinRecoverable, "a sealed PIN must be advertised as recoverable, or the screen has no way to offer it")

	got, err := svc.RevealPIN(sh)
	require.NoError(t, err)
	require.Equal(t, pin, got, "the PIN must come back exactly as it went in")
}

// The PIN survives the DATABASE, not just the in-memory struct: the round trip
// that matters is the one a week later, from a row read back by id.
func TestPINSurvivesAStoreRoundTrip(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	n := seedNode(t, store, "pinenc-store")
	svc := NewService(store)
	svc.AttachSecret(testKey)
	const pin = "kapı-42-ÖZEL"

	created, err := svc.Create(context.Background(), CreateOpts{NodeID: n.ID, PIN: pin})
	require.NoError(t, err)

	reread, err := store.GetShareByID(context.Background(), created.ID)
	require.NoError(t, err)
	require.True(t, reread.PinRecoverable)

	got, err := svc.RevealPIN(reread)
	require.NoError(t, err)
	require.Equal(t, pin, got, "a PIN read back from the database must be the one that was sealed into it")
}

// ⚠ Making the PIN recoverable must not make it cheaper to GUESS. The gate is
// still bcrypt over pin_hash, and nothing verifies against the sealed value.
func TestSealingDoesNotWeakenTheGate(t *testing.T) {
	svc, n := withKey(t, "gate")
	const pin = "834595"

	sh, err := svc.Create(context.Background(), CreateOpts{NodeID: n.ID, PIN: pin})
	require.NoError(t, err)

	require.NotEmpty(t, sh.PinHash, "the bcrypt hash must still be written")
	require.True(t, strings.HasPrefix(sh.PinHash, "$2"), "pin_hash must still be a bcrypt hash, not the sealed value moved sideways")
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(sh.PinHash), []byte(pin)))

	ctx := context.Background()
	require.NoError(t, svc.CheckPIN(ctx, sh, pin), "the right PIN must still pass the gate")
	require.ErrorIs(t, svc.CheckPIN(ctx, sh, "000000"), ErrBadPIN, "a wrong PIN must still be refused by the gate")

	// The decisive one: a link whose SEALED value is intact but whose hash is
	// gone must not open. If anything ever verified against pin_enc, this is
	// where it would show.
	noHash := *sh
	noHash.PinHash = ""
	require.NoError(t, svc.CheckPIN(ctx, &noHash, "anything-at-all"),
		"a link with no hash has no gate — which is precisely why the sealed value must never become one")
}

// Two links with the same PIN must not produce the same column, or the column
// itself says which links share a PIN.
func TestSealIsPerLink(t *testing.T) {
	svc, n := withKey(t, "perlink")
	a, err := svc.Create(context.Background(), CreateOpts{NodeID: n.ID, PIN: "1234"})
	require.NoError(t, err)
	b, err := svc.Create(context.Background(), CreateOpts{NodeID: n.ID, PIN: "1234"})
	require.NoError(t, err)
	require.NotEqual(t, a.PinEnc, b.PinEnc, "two seals of one PIN must differ — the nonce is what stops the column from grouping links by PIN")
}

// ── degrading honestly ─────────────────────────────────────────────────
//
// Three different "cannot be shown", three different sentences, because the
// operator's next move differs for each.

func TestRevealSaysWhyItCannot(t *testing.T) {
	t.Run("a link with no PIN", func(t *testing.T) {
		svc, n := withKey(t, "nopin")
		sh, err := svc.Create(context.Background(), CreateOpts{NodeID: n.ID})
		require.NoError(t, err)
		_, err = svc.RevealPIN(sh)
		require.ErrorIs(t, err, ErrNoPIN)
		require.False(t, sh.PinRecoverable)
	})

	t.Run("an instance with no secret key", func(t *testing.T) {
		_, store := dbtest.NewTestDB(t)
		n := seedNode(t, store, "pinenc-nokey")
		svc := NewService(store) // AttachSecret never called — no FILEX_SECRET_KEY
		sh, err := svc.Create(context.Background(), CreateOpts{NodeID: n.ID, PIN: "4242"})
		require.NoError(t, err, "a missing key must not stop a link being minted — that would break sharing to add a convenience")
		require.Empty(t, sh.PinEnc, "with no key there is nothing to seal, and a plaintext column here would be the one thing this must never do")
		require.False(t, sh.PinRecoverable)
		require.NoError(t, svc.CheckPIN(context.Background(), sh, "4242"), "the link must still work")

		_, err = svc.RevealPIN(sh)
		require.ErrorIs(t, err, ErrNoSecretKey,
			"the instance-level cause must be named: this one an operator can fix, and 'not recoverable' would send them looking at the link instead")
	})

	t.Run("a link minted before 00049", func(t *testing.T) {
		svc, _ := withKey(t, "legacy")
		// What the migration leaves behind: a hash, and no sealed value, on an
		// instance that HAS a key.
		legacy := &model.Share{ID: 7, PinHash: "$2a$10$abcdefghijklmnopqrstuv", PinEnc: ""}
		_, err := svc.RevealPIN(legacy)
		require.ErrorIs(t, err, ErrPinNotRecoverable,
			"an old link's PIN is gone for good and must say so plainly, not blame a key that is present")
	})

	t.Run("a key that was rotated away", func(t *testing.T) {
		svc, n := withKey(t, "rotated")
		sh, err := svc.Create(context.Background(), CreateOpts{NodeID: n.ID, PIN: "4242"})
		require.NoError(t, err)

		rotated := NewService(nil)
		rotated.AttachSecret("a-completely-different-instance-secret-value")
		_, err = rotated.RevealPIN(sh)
		require.ErrorIs(t, err, ErrPinNotRecoverable,
			"a ciphertext sealed under a key nobody has any more is unreadable forever — which is what 'cannot be shown' already means")
	})

	t.Run("sealed rows and no key in the process", func(t *testing.T) {
		svc, n := withKey(t, "keyless")
		sh, err := svc.Create(context.Background(), CreateOpts{NodeID: n.ID, PIN: "4242"})
		require.NoError(t, err)

		keyless := NewService(nil) // e.g. a restore onto a host that never had the key
		_, err = keyless.RevealPIN(sh)
		require.ErrorIs(t, err, ErrNoSecretKey,
			"a sealed row with no key is a missing FILEX_SECRET_KEY, not data damage — secretbox.Open draws exactly that distinction and it must reach the operator")
	})
}
