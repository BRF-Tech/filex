package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What "immutable after install" has to mean in practice.
//
// The interesting failure is not "the operator changed the value" — it is
// "the operator changed the value and the server started anyway", because
// from that moment the installation's own answer about who can open an
// encrypted folder is different for folders created before and after, with
// nothing on either to say which. These tests hold the line at boot.

type fakeSettings struct {
	m       map[string]string
	getErr  error
	putErr  error
	putHits int
}

func newFakeSettings() *fakeSettings { return &fakeSettings{m: map[string]string{}} }

func (f *fakeSettings) GetSetting(_ context.Context, key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.m[key]
	if !ok {
		return "", sql.ErrNoRows
	}
	return v, nil
}

func (f *fakeSettings) UpsertSetting(_ context.Context, key, value string) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.putHits++
	f.m[key] = value
	return nil
}

func testKey(t *testing.T) *EscrowKey {
	t.Helper()
	spki, _, err := GenerateEscrowKeyPair(EscrowMinKeyBits)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	k, err := ParseEscrowPublicKey(spki)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return k
}

func TestPinInstallation_FirstBootRecordsWhatItWasGiven(t *testing.T) {
	st := newFakeSettings()
	dir := t.TempDir()
	key := testKey(t)

	got, err := PinInstallation(context.Background(), st, dir, key, "2026-09-05T00:00:00Z", Adoption{})
	if err != nil {
		t.Fatalf("first boot should pin, got %v", err)
	}
	if got.E2EEscrowKID != key.KID {
		t.Fatalf("kid = %q, want %q", got.E2EEscrowKID, key.KID)
	}
	if _, ok := st.m[SettingInstallation]; !ok {
		t.Fatal("nothing was written to the settings table")
	}
	// The mirror must be readable by a human with no SQL client.
	b, err := os.ReadFile(filepath.Join(dir, InstallationFile))
	if err != nil {
		t.Fatalf("mirror not written: %v", err)
	}
	var mirrored Installation
	if err := json.Unmarshal(b, &mirrored); err != nil {
		t.Fatalf("mirror is not JSON: %v", err)
	}
	if mirrored.E2EEscrowKID != key.KID {
		t.Fatalf("mirror kid = %q, want %q", mirrored.E2EEscrowKID, key.KID)
	}
}

func TestPinInstallation_SameKeyStartsQuietly(t *testing.T) {
	st := newFakeSettings()
	dir := t.TempDir()
	key := testKey(t)

	if _, err := PinInstallation(context.Background(), st, dir, key, "t0", Adoption{}); err != nil {
		t.Fatalf("first boot: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := PinInstallation(context.Background(), st, dir, key, "t1", Adoption{}); err != nil {
			t.Fatalf("restart %d should be a no-op, got %v", i, err)
		}
	}
}

func TestPinInstallation_TurningEscrowOnLaterRefusesToStart(t *testing.T) {
	// The case the whole mechanism exists for, and the one that later grew
	// a supervised exception. An installation that ran without escrow has
	// folders with no escrow-wrapped key; enabling escrow silently would
	// give the operator a key that opens the new half of the data and fails
	// on the old half with nothing to say which is which.
	//
	// This transition CAN now be performed — see
	// installation_adopt_test.go — but only when the operator says so in a
	// second, separate variable. Setting the key alone still stops the
	// server, and the message below is what they read when it does.
	st := newFakeSettings()
	dir := t.TempDir()
	if _, err := PinInstallation(context.Background(), st, dir, nil, "t0", Adoption{}); err != nil {
		t.Fatalf("first boot without escrow: %v", err)
	}

	_, err := PinInstallation(context.Background(), st, dir, testKey(t), "t1", Adoption{})
	var mismatch *InstallationMismatchError
	if err == nil {
		t.Fatal("enabling escrow after install must refuse to start")
	}
	if !asMismatch(err, &mismatch) {
		t.Fatalf("want InstallationMismatchError, got %T: %v", err, err)
	}
	msg := err.Error()
	// The message is the feature. An operator meeting it at 3am must learn
	// what changed, why it is refused, and what they can actually do.
	for _, want := range []string{
		EnvEscrowKey,
		"escrow DISABLED",
		"escrow ENABLED",
		"would NOT give you access to any existing",
		"restore the original value",
		"fresh data directory",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message is missing %q\n--- message ---\n%s", want, msg)
		}
	}
}

func TestPinInstallation_TurningEscrowOffLaterRefusesToStart(t *testing.T) {
	st := newFakeSettings()
	dir := t.TempDir()
	if _, err := PinInstallation(context.Background(), st, dir, testKey(t), "t0", Adoption{}); err != nil {
		t.Fatalf("first boot with escrow: %v", err)
	}
	if _, err := PinInstallation(context.Background(), st, dir, nil, "t1", Adoption{}); err == nil {
		t.Fatal("removing the escrow key after install must refuse to start")
	}
}

func TestPinInstallation_SwappingTheKeyRefusesToStart(t *testing.T) {
	st := newFakeSettings()
	dir := t.TempDir()
	if _, err := PinInstallation(context.Background(), st, dir, testKey(t), "t0", Adoption{}); err != nil {
		t.Fatalf("first boot: %v", err)
	}
	if _, err := PinInstallation(context.Background(), st, dir, testKey(t), "t1", Adoption{}); err == nil {
		t.Fatal("a different escrow key after install must refuse to start")
	}
}

func TestPinInstallation_SurvivesAWipedDatabaseViaTheMirror(t *testing.T) {
	// Deployments exist where the database is external and the data dir is
	// not, and vice versa. Losing one must not silently un-pin the install.
	dir := t.TempDir()
	key := testKey(t)
	st := newFakeSettings()
	if _, err := PinInstallation(context.Background(), st, dir, key, "t0", Adoption{}); err != nil {
		t.Fatalf("first boot: %v", err)
	}

	fresh := newFakeSettings() // database wiped, data dir intact
	if _, err := PinInstallation(context.Background(), fresh, dir, key, "t1", Adoption{}); err != nil {
		t.Fatalf("same key after a DB wipe should be fine: %v", err)
	}
	if _, err := PinInstallation(context.Background(), newFakeSettings(), dir, nil, "t2", Adoption{}); err == nil {
		t.Fatal("the mirror must still refuse a changed value after a DB wipe")
	}
}

func TestPinInstallation_RestoresTheMirrorWhenTheDataDirWasEphemeral(t *testing.T) {
	dir := t.TempDir()
	key := testKey(t)
	st := newFakeSettings()
	if _, err := PinInstallation(context.Background(), st, dir, key, "t0", Adoption{}); err != nil {
		t.Fatalf("first boot: %v", err)
	}
	_ = os.Remove(filepath.Join(dir, InstallationFile)) // container restart, new volume

	if _, err := PinInstallation(context.Background(), st, dir, key, "t1", Adoption{}); err != nil {
		t.Fatalf("settings row should carry the pin: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, InstallationFile)); err != nil {
		t.Fatalf("the mirror should have been rewritten: %v", err)
	}
}

func TestPinInstallation_CorruptRecordIsAnErrorNotADefault(t *testing.T) {
	st := newFakeSettings()
	st.m[SettingInstallation] = "{not json"
	if _, err := PinInstallation(context.Background(), st, t.TempDir(), nil, "t0", Adoption{}); err == nil {
		t.Fatal("a corrupt pin must not be treated as 'no pin' — that re-pins silently")
	}
}

func TestResolveEscrow(t *testing.T) {
	t.Setenv(EnvEscrowKey, "")
	k, err := ResolveEscrow()
	if err != nil || k != nil {
		t.Fatalf("unset means escrow off, got key=%v err=%v", k, err)
	}

	t.Setenv(EnvEscrowKey, "   ")
	if k, err = ResolveEscrow(); err != nil || k != nil {
		t.Fatalf("whitespace means escrow off, got key=%v err=%v", k, err)
	}

	// A garbled key must be fatal. Falling back to "escrow off" would leave
	// an operator who believes they configured escrow with folders that have
	// no escrow slot, and no way to find out until they need it.
	t.Setenv(EnvEscrowKey, "this is not a key")
	if _, err = ResolveEscrow(); err == nil {
		t.Fatal("an unparseable escrow key must be fatal, not ignored")
	}

	spki, _, err := GenerateEscrowKeyPair(EscrowMinKeyBits)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	t.Setenv(EnvEscrowKey, spki)
	if k, err = ResolveEscrow(); err != nil || k == nil {
		t.Fatalf("a good key should resolve, got key=%v err=%v", k, err)
	}
}

func asMismatch(err error, target **InstallationMismatchError) bool {
	m, ok := err.(*InstallationMismatchError)
	if ok {
		*target = m
	}
	return ok
}
