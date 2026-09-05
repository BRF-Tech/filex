package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ── Install-time settings, pinned for the life of the installation ───
//
// Some settings cannot be changed after the first boot because changing
// them would make already-stored data unreadable or, worse, quietly
// inconsistent. E2E key escrow is the first of them: a folder created
// while escrow was off carries no escrow-wrapped key material, so turning
// escrow on later cannot open it. That is arithmetic, not policy.
//
// Those settings carry the FILEX_INSTALLATION_ prefix and are recorded
// here on the first boot that sees them. On every later boot the recorded
// value is compared with the environment, and a mismatch stops the server
// rather than starting one that lies about its own guarantees.
//
// The record lives in two places for the same reason a passport has a
// photo and a chip: the settings table is authoritative (it travels with
// the instance data, including an external database), and
// <data-dir>/installation.json is a human-readable mirror so an operator
// can answer "what is this install pinned to?" without a SQL client.

// SettingInstallation is the settings-table key holding the pinned record.
const SettingInstallation = "installation.pinned"

// InstallationFile is the operator-readable mirror inside the data dir.
const InstallationFile = "installation.json"

// EnvEscrowKey is the install-time escrow public key. Unset or empty means
// escrow is disabled for this installation, permanently.
const EnvEscrowKey = "FILEX_INSTALLATION_E2E_ESCROW_KEY"

// Installation is the pinned record. Fields are add-only: a newer filex
// reading an older record must see zero values, never a parse error.
type Installation struct {
	// E2EEscrowKID is the escrow key id, or "" when escrow is disabled.
	E2EEscrowKID string `json:"e2e_escrow_kid"`
	// E2EEscrowSPKI is the escrow public key, kept so the mirror file is
	// self-contained. It is public material; nothing here is a secret.
	E2EEscrowSPKI string `json:"e2e_escrow_spki,omitempty"`
	E2EEscrowAlg  string `json:"e2e_escrow_alg,omitempty"`
	PinnedAt      string `json:"pinned_at,omitempty"`
	PinnedBy      string `json:"pinned_by,omitempty"`
}

// SettingsStore is the narrow store surface the pin needs. db.Store satisfies it.
type SettingsStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	UpsertSetting(ctx context.Context, key, value string) error
}

// InstallationMismatchError is returned when the environment disagrees
// with what this installation was initialised with. Its message is the
// whole point: it must tell an operator what changed, why filex will not
// simply do as it is told, and what their actual options are.
type InstallationMismatchError struct {
	Setting  string
	Pinned   string
	Supplied string
	DataDir  string
}

func (e *InstallationMismatchError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s changed after installation — refusing to start.\n\n", e.Setting)
	fmt.Fprintf(&b, "  this installation was initialised with: %s\n", describeEscrow(e.Pinned))
	fmt.Fprintf(&b, "  the environment now says:               %s\n\n", describeEscrow(e.Supplied))
	b.WriteString("Escrow is fixed at install time because it is not a policy switch.\n")
	b.WriteString("An encrypted folder stores its master key wrapped once per recovery\n")
	b.WriteString("path, and those wrapped copies are written when the folder is created.\n")
	b.WriteString("So:\n")
	b.WriteString("  - turning escrow ON now would NOT give you access to any existing\n")
	b.WriteString("    folder. They carry no escrow-wrapped key. Nothing can add one\n")
	b.WriteString("    without the folder password, which filex does not have.\n")
	b.WriteString("  - changing the escrow KEY now would leave every existing folder\n")
	b.WriteString("    openable only by the OLD private key, while new folders needed\n")
	b.WriteString("    the new one — two half-working key sets and no way to tell which\n")
	b.WriteString("    is which from the outside.\n")
	b.WriteString("  - turning escrow OFF now would not un-escrow anything already\n")
	b.WriteString("    created; the old private key would keep opening those folders.\n\n")
	b.WriteString("What you can do:\n")
	fmt.Fprintf(&b, "  - restore the original value of %s and start normally;\n", e.Setting)
	b.WriteString("  - or start a NEW installation with a fresh data directory, keeping\n")
	b.WriteString("    the old escrow private key for as long as the old folders exist.\n\n")
	fmt.Fprintf(&b, "The pinned record is in the settings table (key %q) and mirrored at\n", SettingInstallation)
	fmt.Fprintf(&b, "%s.\n", filepath.Join(e.DataDir, InstallationFile))
	return b.String()
}

// ResolveEscrow reads and validates the install-time escrow key from the
// environment. A missing or empty value means escrow is off. A present but
// unparseable value is an error — silently running without escrow when the
// operator believes they configured it is the one outcome to avoid.
func ResolveEscrow() (*EscrowKey, error) {
	raw := strings.TrimSpace(os.Getenv(EnvEscrowKey))
	if raw == "" {
		return nil, nil
	}
	k, err := ParseEscrowPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvEscrowKey, err)
	}
	return k, nil
}

// PinInstallation records the install-time settings on first boot, or
// verifies them on every boot after that.
//
// Returns the effective record. An *InstallationMismatchError means the
// caller must abort — see server.New.
func PinInstallation(ctx context.Context, store SettingsStore, dataDir string, escrow *EscrowKey, now string) (Installation, error) {
	want := Installation{PinnedAt: now, PinnedBy: "first-boot"}
	if escrow != nil {
		want.E2EEscrowKID = escrow.KID
		want.E2EEscrowSPKI = escrow.SPKI
		want.E2EEscrowAlg = EscrowAlg
	}

	have, found, err := loadInstallation(ctx, store, dataDir)
	if err != nil {
		return Installation{}, err
	}
	if !found {
		if err := saveInstallation(ctx, store, dataDir, want); err != nil {
			return Installation{}, err
		}
		return want, nil
	}
	if have.E2EEscrowKID != want.E2EEscrowKID {
		return Installation{}, &InstallationMismatchError{
			Setting:  EnvEscrowKey,
			Pinned:   have.E2EEscrowKID,
			Supplied: want.E2EEscrowKID,
			DataDir:  dataDir,
		}
	}
	// Same key, but the record predates a field or the mirror was lost with
	// an ephemeral data dir — top it up without changing what is pinned.
	if have.E2EEscrowSPKI == "" && want.E2EEscrowSPKI != "" {
		have.E2EEscrowSPKI = want.E2EEscrowSPKI
		have.E2EEscrowAlg = EscrowAlg
		_ = saveInstallation(ctx, store, dataDir, have)
	} else {
		_ = writeMirror(dataDir, have)
	}
	return have, nil
}

// loadInstallation reads the pinned record: settings table first (it is
// authoritative and survives an ephemeral data dir), the mirror file
// second (it survives a wiped database).
func loadInstallation(ctx context.Context, store SettingsStore, dataDir string) (Installation, bool, error) {
	var inst Installation
	if store != nil {
		v, err := store.GetSetting(ctx, SettingInstallation)
		switch {
		case err == nil && strings.TrimSpace(v) != "":
			if err := json.Unmarshal([]byte(v), &inst); err != nil {
				return Installation{}, false, fmt.Errorf("e2e: pinned installation record is corrupt: %w", err)
			}
			return inst, true, nil
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return Installation{}, false, fmt.Errorf("e2e: reading pinned installation record: %w", err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dataDir, InstallationFile))
	if err != nil {
		return Installation{}, false, nil //nolint:nilerr // absent = first boot
	}
	if err := json.Unmarshal(b, &inst); err != nil {
		return Installation{}, false, fmt.Errorf("e2e: %s is corrupt: %w", InstallationFile, err)
	}
	return inst, true, nil
}

func saveInstallation(ctx context.Context, store SettingsStore, dataDir string, inst Installation) error {
	b, err := json.Marshal(inst)
	if err != nil {
		return err
	}
	if store != nil {
		if err := store.UpsertSetting(ctx, SettingInstallation, string(b)); err != nil {
			return fmt.Errorf("e2e: pinning installation record: %w", err)
		}
	}
	return writeMirror(dataDir, inst)
}

// writeMirror is best-effort: the settings row is what counts, and a
// read-only data dir must not stop the server.
func writeMirror(dataDir string, inst Installation) error {
	if dataDir == "" {
		return nil
	}
	b, err := json.MarshalIndent(inst, "", "  ")
	if err != nil {
		return nil //nolint:nilerr // mirror only
	}
	_ = os.WriteFile(filepath.Join(dataDir, InstallationFile), append(b, '\n'), 0o600)
	return nil
}

func describeEscrow(kid string) string {
	if kid == "" {
		return "escrow DISABLED (no key)"
	}
	return "escrow ENABLED, key id " + kid
}
