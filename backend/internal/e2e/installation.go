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
// ── One exception: ADOPTION ──────────────────────────────────────────
//
// v0.31.0 refused EVERY change, including "no escrow" -> "escrow". That
// read well and measured badly: on our own two deployments both
// installation.json files came back with `"e2e_escrow_kid": ""`, so the
// owner's decision to turn escrow on could only be carried out by throwing
// the data directory away. And the general case is worse than ours —
// nobody decides key-escrow policy in the first second of an
// installation's life, before they have a single file, so a switch that
// can only be thrown then is a switch nobody ever throws.
//
// So empty -> set is allowed, ONCE, and only when the operator says
// separately that they mean it (EnvEscrowAdopt). Set -> a different key
// and set -> empty stay refused exactly as before; the adopt flag does not
// unlock them, because those two really would leave half the data behind.
//
// ⚠⚠ Adoption is NOT retroactive, and cannot be made so. A folder wraps
// its master key to the escrow identity when it is created; a folder that
// already exists carries no such wrapped copy, and adding one needs the
// folder password, which the server has never had. Every folder older than
// the adoption is permanently outside the escrow key's reach. That is why
// the record keeps the adoption timestamp: it is the line between the
// folders the operator can open and the ones they cannot.
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
// escrow is disabled for this installation.
const EnvEscrowKey = "FILEX_INSTALLATION_E2E_ESCROW_KEY"

// EnvEscrowAdopt is the operator's separate, explicit consent to turn
// escrow on for an installation that already exists.
//
// Why a second variable and not just "the key changed from empty, so they
// must have meant it": an escrow key arrives by being pasted into a
// compose file, a Helm values file or a .env, often copied from another
// deployment or from the quickstart. That is exactly the shape of an
// accident, and the accident is silent — the installation gains a second
// key holder and only new folders get it, so nothing looks wrong until
// somebody needs the key on an old folder and it does not work.
//
// Why an env var and not a CLI subcommand or an admin button: the operator
// has to set EnvEscrowKey anyway, and that is an env var. A CLI step would
// mean shell access to the container plus database credentials — routine
// on a VPS, awkward on Kubernetes and impossible on several managed
// platforms filex is deployed to — and it would still leave two places
// that must agree. An admin button would put "give the operator a key to
// every folder from now on" behind an HTTP session; the person entitled to
// that decision is the person who can edit the environment, which is where
// the decision already lives. One extra line, in the file they are already
// editing, read once and then dead, is the smallest thing that is still a
// deliberate act.
const EnvEscrowAdopt = "FILEX_INSTALLATION_E2E_ESCROW_ADOPT"

// AdoptedByEnv is what the record says when consent came from the env var.
// It names the mechanism, so a future second mechanism is legible in the
// data instead of being flattened into a bare "adopted".
const AdoptedByEnv = "env:" + EnvEscrowAdopt

// PinnedByFirstBoot is what the record says when escrow was there from the
// installation's first boot. Adoption never overwrites it — see Installation.
const PinnedByFirstBoot = "first-boot"

// adoptionNote travels inside the record, because the record is the thing
// somebody reads six months later when they are trying to work out why the
// escrow key opens some folders and not others.
const adoptionNote = "escrow was turned on after this installation already existed; " +
	"folders created BEFORE e2e_escrow_adopted_at have no escrow-wrapped key, so the " +
	"escrow key does not open them and no operator action can change that. Each " +
	"folder's OWNER can grant it from the browser with the folder password"

// Adoption is the operator's explicit consent to adopt escrow, and the
// record of how it was given.
type Adoption struct {
	// Allowed is the consent itself. False means empty -> set is refused
	// exactly as it was before adoption existed.
	Allowed bool
	// By is written verbatim into the record as e2e_escrow_adopted_by.
	By string
}

// ResolveAdoption reads the operator's consent from the environment.
// Booleans follow the same rule as the rest of filex: "1" or "true".
func ResolveAdoption() Adoption {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(EnvEscrowAdopt)))
	if v != "1" && v != "true" {
		return Adoption{}
	}
	return Adoption{Allowed: true, By: AdoptedByEnv}
}

// Installation is the pinned record. Fields are add-only: a newer filex
// reading an older record must see zero values, never a parse error.
type Installation struct {
	// E2EEscrowKID is the escrow key id, or "" when escrow is disabled.
	E2EEscrowKID string `json:"e2e_escrow_kid"`
	// E2EEscrowSPKI is the escrow public key, kept so the mirror file is
	// self-contained. It is public material; nothing here is a secret.
	E2EEscrowSPKI string `json:"e2e_escrow_spki,omitempty"`
	E2EEscrowAlg  string `json:"e2e_escrow_alg,omitempty"`
	// PinnedAt / PinnedBy describe the FIRST BOOT and are never rewritten,
	// not even by an adoption. "When was this installation created?" and
	// "when did it gain a second key?" are two questions, and answering the
	// second by overwriting the first would destroy the only record of the
	// installation's own age.
	PinnedAt string `json:"pinned_at,omitempty"`
	PinnedBy string `json:"pinned_by,omitempty"`
	// E2EEscrowAdoptedAt is set only when escrow was turned on AFTER the
	// first boot. Its presence is the whole answer to "was this first-boot
	// or adoption?", and its value is the boundary: folders created before
	// this instant carry no escrow slot and never will.
	E2EEscrowAdoptedAt string `json:"e2e_escrow_adopted_at,omitempty"`
	// E2EEscrowAdoptedBy records HOW consent was given (AdoptedByEnv today).
	E2EEscrowAdoptedBy string `json:"e2e_escrow_adopted_by,omitempty"`
	// E2EEscrowAdoptionNote is written alongside them so the mirror file
	// explains itself to a reader who has never seen this documentation.
	E2EEscrowAdoptionNote string `json:"e2e_escrow_adoption_note,omitempty"`
	// JustAdopted is true only on the boot that PERFORMED the adoption, so
	// that boot can say so once and loudly. It describes this process, not
	// the installation, so it is never written to the record — comparing
	// E2EEscrowAdoptedAt against the boot clock would have been close
	// enough almost always and wrong for a restart inside the same second.
	JustAdopted bool `json:"-"`
}

// EscrowAdopted reports whether escrow was adopted rather than present from
// the first boot.
func (i Installation) EscrowAdopted() bool { return i.E2EEscrowAdoptedAt != "" }

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
	// Adoptable is true for the one transition that CAN be performed —
	// escrow off -> escrow on — when the operator has not yet said they
	// mean it. The message then explains adoption and its permanent limit
	// instead of only listing the two ways to back out.
	Adoptable bool
}

func (e *InstallationMismatchError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s changed after installation — refusing to start.\n\n", e.Setting)
	fmt.Fprintf(&b, "  this installation was initialised with: %s\n", describeEscrow(e.Pinned))
	fmt.Fprintf(&b, "  the environment now says:               %s\n\n", describeEscrow(e.Supplied))

	if e.Adoptable {
		// Escrow off -> escrow on. This one is allowed, but it has a
		// permanent edge in it, so the message spends its space on that edge
		// rather than on how to type the flag.
		b.WriteString("An installation that already exists CAN adopt escrow. This is not\n")
		b.WriteString("refused because it is impossible; it is refused because it has to be\n")
		b.WriteString("a deliberate act rather than the side effect of an edited env file.\n\n")
		b.WriteString("⚠ Adoption is NOT retroactive, and no later version can make it so.\n")
		b.WriteString("Turning escrow on now would NOT give you access to any existing\n")
		b.WriteString("encrypted folder. A folder wraps its master key to the escrow identity\n")
		b.WriteString("WHEN IT IS CREATED. The folders you already have carry no escrow-wrapped\n")
		b.WriteString("copy, and adding one needs the folder password, which filex has never\n")
		b.WriteString("had. From the moment you adopt:\n")
		b.WriteString("  - folders created AFTER it open with the escrow private key;\n")
		b.WriteString("  - every folder created BEFORE it opens only with its password or its\n")
		b.WriteString("    own recovery key, for the rest of its life.\n\n")
		b.WriteString("If that is what you want, set this alongside the key and start again:\n\n")
		fmt.Fprintf(&b, "  %s=1\n\n", EnvEscrowAdopt)
		b.WriteString("filex records the adoption — how it was authorised and when, kept\n")
		b.WriteString("separate from the first-boot record — and then ignores the flag, so you\n")
		b.WriteString("can drop it again on the next deploy. The recorded timestamp is the\n")
		b.WriteString("line between the folders the escrow key opens and the ones it does not.\n\n")
		b.WriteString("If it is NOT what you want:\n")
		fmt.Fprintf(&b, "  - restore the original value of %s and start normally;\n", e.Setting)
		b.WriteString("  - or start a NEW installation with a fresh data directory.\n\n")
	} else {
		fmt.Fprintf(&b, "This change is refused outright. Unlike turning escrow ON, it cannot be\nadopted, and %s does not cover it.\n\n", EnvEscrowAdopt)
		b.WriteString("An encrypted folder stores its master key wrapped once per recovery\n")
		b.WriteString("path, and those wrapped copies are written when the folder is created.\n")
		b.WriteString("So:\n")
		b.WriteString("  - changing the escrow KEY now would leave every existing folder\n")
		b.WriteString("    openable only by the OLD private key, while new folders needed\n")
		b.WriteString("    the new one — two half-working key sets and no way to tell which\n")
		b.WriteString("    is which from the outside.\n")
		b.WriteString("  - turning escrow OFF now would not un-escrow anything already\n")
		b.WriteString("    created; the old private key would keep opening those folders,\n")
		b.WriteString("    and nothing in the UI would offer them a door any more.\n\n")
		b.WriteString("What you can do:\n")
		fmt.Fprintf(&b, "  - restore the original value of %s and start normally;\n", e.Setting)
		b.WriteString("  - or start a NEW installation with a fresh data directory, keeping\n")
		b.WriteString("    the old escrow private key for as long as the old folders exist.\n\n")
	}

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

// PinInstallation records the install-time settings on first boot,
// verifies them on every boot after that, and performs the one change that
// is allowed: adopting escrow on an installation that already exists.
//
// adopt carries the operator's explicit consent (see Adoption). It is
// consulted for exactly one transition, escrow off -> escrow on, and is
// ignored everywhere else — a first boot does not need consent, and no
// consent makes a key SWAP or a key REMOVAL safe.
//
// Returns the effective record. An *InstallationMismatchError means the
// caller must abort — see server.New.
func PinInstallation(ctx context.Context, store SettingsStore, dataDir string, escrow *EscrowKey, now string, adopt Adoption) (Installation, error) {
	want := Installation{PinnedAt: now, PinnedBy: PinnedByFirstBoot}
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
		// The one adoptable transition: this installation has no escrow key
		// and the environment now supplies one.
		//
		// ⚠ Deliberately `have == "" && want != ""` and nothing else. A swap
		// (have != "" && want != "" && different) and a removal (want == "")
		// fall through to the refusal below whatever the flag says, because
		// both of those leave folders behind that the running configuration
		// can no longer describe. Adoption leaves nothing behind: every
		// folder that existed a second ago still opens exactly the way it
		// did, it simply never becomes escrow-openable.
		adoptable := have.E2EEscrowKID == "" && want.E2EEscrowKID != ""
		if !adoptable || !adopt.Allowed {
			return Installation{}, &InstallationMismatchError{
				Setting:   EnvEscrowKey,
				Pinned:    have.E2EEscrowKID,
				Supplied:  want.E2EEscrowKID,
				DataDir:   dataDir,
				Adoptable: adoptable,
			}
		}
		// Adopt. PinnedAt / PinnedBy keep pointing at the first boot: the
		// installation's age and the day it gained a second key holder are
		// different facts and the record has to keep both, because the
		// question somebody asks six months from now is precisely "when did
		// this get a second key, and which folders are older than that?".
		adopted := have
		adopted.E2EEscrowKID = want.E2EEscrowKID
		adopted.E2EEscrowSPKI = want.E2EEscrowSPKI
		adopted.E2EEscrowAlg = want.E2EEscrowAlg
		adopted.E2EEscrowAdoptedAt = now
		adopted.E2EEscrowAdoptedBy = adopt.By
		adopted.E2EEscrowAdoptionNote = adoptionNote
		adopted.JustAdopted = true
		if adopted.PinnedAt == "" {
			// A record written before this field existed. Say so rather than
			// backdating the install to the adoption, which would read as
			// "escrow was there all along".
			adopted.PinnedBy = "unknown (record predates pinned_at)"
		}
		if err := saveInstallation(ctx, store, dataDir, adopted); err != nil {
			return Installation{}, err
		}
		return adopted, nil
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
