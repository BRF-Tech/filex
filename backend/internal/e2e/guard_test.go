package e2e

import (
	"context"
	"errors"
	"testing"
)

// The bug this guard exists for, measured.
//
// Before it, filex's own UI would put a plaintext file inside an encrypted
// folder: uploads are intercepted and encrypted in the browser, but paste,
// drag-and-drop and duplicate are server-side byte copies that never touch
// the crypto. Nothing warned anyone, and the file looked exactly like its
// encrypted neighbours in the listing.

func guardFixture() (*fakeLookup, context.Context) {
	// Two encrypted folders and one ordinary one, on storage 7.
	return &fakeLookup{
		storageID: 7,
		paths: map[string]bool{
			"vault/" + MarkerName:      true,
			"other/" + MarkerName:      true,
			"vault/deep/" + MarkerName: false,
			"public/" + MarkerName:     false,
		},
	}, context.Background()
}

func TestGuardTransfer_RefusesPlaintextIntoAnEncryptedFolder(t *testing.T) {
	lk, ctx := guardFixture()
	err := GuardTransfer(ctx, lk, 7, []string{"public/notes.txt"}, 7, "vault")
	if !errors.Is(err, ErrPlaintextIntoEncrypted) {
		t.Fatalf("want ErrPlaintextIntoEncrypted, got %v", err)
	}
	var ge *TransferGuardError
	if !errors.As(err, &ge) || ge.Source != "public/notes.txt" {
		t.Fatalf("the error must name the file, got %v", err)
	}
}

func TestGuardTransfer_RefusesAnEncryptedFileLeavingItsFolder(t *testing.T) {
	lk, ctx := guardFixture()
	if err := GuardTransfer(ctx, lk, 7, []string{"vault/secret.txt"}, 7, "public"); !errors.Is(err, ErrEncryptedOutOfFolder) {
		t.Fatalf("want ErrEncryptedOutOfFolder, got %v", err)
	}
	// Including "out" meaning the storage root.
	if err := GuardTransfer(ctx, lk, 7, []string{"vault/secret.txt"}, 7, ""); !errors.Is(err, ErrEncryptedOutOfFolder) {
		t.Fatalf("moving to the storage root is still leaving, got %v", err)
	}
}

func TestGuardTransfer_RefusesBetweenTwoEncryptedFolders(t *testing.T) {
	lk, ctx := guardFixture()
	if err := GuardTransfer(ctx, lk, 7, []string{"vault/secret.txt"}, 7, "other"); !errors.Is(err, ErrAcrossEncrypted) {
		t.Fatalf("want ErrAcrossEncrypted, got %v", err)
	}
}

func TestGuardTransfer_AllowsMovesInsideOneEncryptedFolder(t *testing.T) {
	lk, ctx := guardFixture()
	if err := GuardTransfer(ctx, lk, 7, []string{"vault/a.txt"}, 7, "vault/deep"); err != nil {
		t.Fatalf("a move within one encrypted folder is fine: %v", err)
	}
	if err := GuardTransfer(ctx, lk, 7, []string{"vault/deep/a.txt"}, 7, "vault"); err != nil {
		t.Fatalf("and back again: %v", err)
	}
}

func TestGuardTransfer_LeavesOrdinaryTransfersAlone(t *testing.T) {
	lk, ctx := guardFixture()
	if err := GuardTransfer(ctx, lk, 7, []string{"public/a.txt", "public/b.txt"}, 7, "public/sub"); err != nil {
		t.Fatalf("the ordinary case must be untouched: %v", err)
	}
	if err := GuardTransfer(ctx, lk, 7, []string{"a.txt"}, 7, ""); err != nil {
		t.Fatalf("root to root: %v", err)
	}
}

func TestGuardTransfer_TheEncryptedFolderItselfMayBeMoved(t *testing.T) {
	// The marker travels with the folder, so relocating it is harmless — and
	// refusing it would make an encrypted folder impossible to reorganise.
	lk, ctx := guardFixture()
	if err := GuardTransfer(ctx, lk, 7, []string{"vault"}, 7, "public"); err != nil {
		t.Fatalf("moving the encrypted folder itself must be allowed: %v", err)
	}
	// But not into another one: nesting makes "which folder am I in"
	// unanswerable for the lock screen.
	if err := GuardTransfer(ctx, lk, 7, []string{"vault"}, 7, "other"); !errors.Is(err, ErrNestedEncrypted) {
		t.Fatalf("want ErrNestedEncrypted, got %v", err)
	}
}

func TestGuardTransfer_TreatsTwoStoragesAsTwoPlaces(t *testing.T) {
	lk, ctx := guardFixture()
	// Storage 9 has no marker at "vault" as far as this lookup is concerned,
	// so a cross-storage move out of an encrypted folder is still leaving it.
	if err := GuardTransfer(ctx, lk, 7, []string{"vault/secret.txt"}, 9, "vault"); !errors.Is(err, ErrEncryptedOutOfFolder) {
		t.Fatalf("want ErrEncryptedOutOfFolder, got %v", err)
	}
}

func TestGuardTransfer_IsANoOpWithoutALookup(t *testing.T) {
	// Tests and unwired deployments must not be broken by a guard that has
	// nothing to consult.
	if err := GuardTransfer(context.Background(), nil, 7, []string{"a"}, 7, "b"); err != nil {
		t.Fatalf("nil lookup must allow: %v", err)
	}
}

func TestGuardTransfer_ChecksEveryItemInABatch(t *testing.T) {
	lk, ctx := guardFixture()
	// One bad apple in a multi-select paste refuses the whole batch: a
	// partially applied transfer is harder to explain than a refused one.
	err := GuardTransfer(ctx, lk, 7, []string{"public/ok.txt", "public/also-ok.txt", "vault/secret.txt"}, 7, "public")
	if !errors.Is(err, ErrEncryptedOutOfFolder) {
		t.Fatalf("want the batch refused, got %v", err)
	}
}
