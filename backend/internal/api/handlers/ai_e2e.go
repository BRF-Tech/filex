// Package handlers - ai_e2e.go
//
// End-to-end encryption on the AI surface: GET/POST /api/ai/* and the MCP
// file tools, and everything that writes through the same core (ShareX, the
// upload ticket).
//
// The surface holds no key and never will, so it cannot decrypt a file or
// encrypt one. Before this file it did not know that either: file_read handed
// an agent an encrypted PDF as `mime: application/pdf, encoding: base64` (the
// agent took it for a broken PDF), file_list showed the folder's key file,
// and file_write put plaintext into an encrypted folder without a word
// (measured on v0.48.1, task #113). What it does now, each from one place:
//
//   - every row says `encrypted` and `e2e_root` (e2eRoots.mark, the rules the
//     explorer's rows use);
//   - a read of ciphertext is refused with E2E_ENCRYPTED (encryptedRefusal);
//   - a write into an encrypted folder is refused with E2E_PLAINTEXT_REFUSED
//     unless the caller says `allow_plaintext` (plaintextRefusal);
//   - a copy across the boundary is refused with E2E_BOUNDARY
//     (e2e.GuardTransfer, the rule every transfer surface asks);
//   - the key file is left out of listings (syspath.Unlisted) and never
//     written, moved or deleted from here (syspath.Keyless, through aiOps.gate);
//   - a public link into an encrypted folder is refused (publicLinkRefusal).
//
// Recognising any of it is a catalogue lookup for the marker's row and a
// name test for `.fxe`: no content is read and no key is asked for.
package handlers

import (
	"context"
	"errors"

	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// The wire codes an agent matches on. REST answers them in `code` (409), the
// MCP tools as the first word of the error text (toolErr). aiErrCode also
// passes on the explorer's RESERVED_NAME (403), NO_FREE_NAME (409) and
// ENTRY_UNAVAILABLE (409).
const (
	codeE2EEncrypted        = "E2E_ENCRYPTED"
	codeE2EPlaintextRefused = "E2E_PLAINTEXT_REFUSED"
	codeE2EBoundary         = "E2E_BOUNDARY"
)

// errE2EEncrypted is the sentinel behind every E2E_ENCRYPTED refusal: a read
// of ciphertext, and a public link to an encrypted folder or into one.
var errE2EEncrypted = errors.New("end-to-end encrypted")

// errE2EPlaintext is the sentinel behind E2E_PLAINTEXT_REFUSED.
var errE2EPlaintext = errors.New("plaintext into an encrypted folder")

// aiErrCode is the wire code an AI-surface error carries, "" for none.
func aiErrCode(err error) string {
	var guard *e2e.TransferGuardError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errE2EEncrypted):
		return codeE2EEncrypted
	case errors.Is(err, errE2EPlaintext):
		return codeE2EPlaintextRefused
	case errors.As(err, &guard):
		return codeE2EBoundary
	case errors.Is(err, syspath.ErrReserved):
		// The explorer's word for it (writeReserved): one of filex's own
		// names, or here also an encrypted folder's key file (Keyless).
		return "RESERVED_NAME"
	case errors.Is(err, writegate.ErrVaultKeyFile):
		// The explorer's words for writegate's vault rule (answerVaultGate).
		return "VAULT_KEYFILE"
	case errors.Is(err, writegate.ErrVaultPath):
		return "VAULT_PATH"
	case errors.Is(err, errEntryUnavailable):
		// An entry the storage could not answer for (entry_unavailable.go,
		// #104); the REST answer also names it (writeAIError).
		return CodeEntryUnavailable
	case errors.Is(err, ops.ErrNoFreeName):
		// A move whose destination and every name beside it are taken; the
		// explorer's move answers the same code (manager_mutate.go, task #116).
		return "NO_FREE_NAME"
	case errors.Is(err, quota.ErrFileTooLarge):
		// Before ErrQuotaExceeded, which it wraps: the explorer's two codes.
		return "FILE_TOO_LARGE"
	case errors.Is(err, quota.ErrQuotaExceeded):
		return "QUOTA_EXCEEDED"
	}
	return ""
}

type plaintextConsentKey struct{}

// withPlaintextConsent marks ctx as carrying the caller's `allow_plaintext`:
// it knows the destination is an encrypted folder and stores plaintext there
// on purpose. false leaves ctx as it is.
func withPlaintextConsent(ctx context.Context, allow bool) context.Context {
	if !allow {
		return ctx
	}
	return context.WithValue(ctx, plaintextConsentKey{}, true)
}

func plaintextConsented(ctx context.Context) bool {
	ok, _ := ctx.Value(plaintextConsentKey{}).(bool)
	return ok
}

// plaintextRefusal is THE rule for bytes this surface writes INTO dir: inside
// an encrypted folder they would be stored unencrypted (it has no key to
// encrypt them with), so the write is refused unless the caller consented
// (withPlaintextConsent). file_write, /api/ai/upload, ShareX, the upload
// ticket (minted and redeemed), the zip a file_zip writes and the members a
// file_unzip extracts all ask it.
func (a *aiOps) plaintextRefusal(ctx context.Context, s *model.Storage, dir string) error {
	lk, ok := a.store.(e2e.NodeByPathLookup)
	if !ok || plaintextConsented(ctx) {
		return nil
	}
	root, enc := e2e.FindRoot(ctx, lk, s.ID, dir)
	if !enc {
		return nil
	}
	return denied(errE2EPlaintext,
		"%s is inside the end-to-end encrypted folder %s: filex holds no key, so what you write would be stored there UNENCRYPTED. "+
			"Upload it through the filex web UI with the folder unlocked, or repeat the call with allow_plaintext=true to store it unencrypted on purpose",
		joinAdapterPath(s.Name, dir), joinAdapterPath(s.Name, root))
}

// encryptedRefusal refuses to hand out the bytes at rel when they are
// ciphertext (e2eRoots.mark: inside an encrypted folder, or a `.fxe`): the
// surface could only return them as they are, and an agent reads an encrypted
// PDF as a broken one.
func (a *aiOps) encryptedRefusal(ctx context.Context, s *model.Storage, rel string) error {
	enc, root := newE2eRoots(a.store).mark(ctx, s.ID, s.Name, rel, false)
	if !enc {
		return nil
	}
	where := "a single encrypted file (.fxe)"
	if root != "" {
		where = "inside the end-to-end encrypted folder " + root
	}
	return denied(errE2EEncrypted,
		"%s is %s: filex holds no key, so it can only hand out ciphertext. "+
			"Open it in the filex web UI with the folder unlocked, or download it there and decrypt it with `filex decrypt`",
		joinAdapterPath(s.Name, rel), where)
}

// markE2e fills e's `encrypted` and `e2e_root` (e2eRoots.mark). roots is one
// per answer, so a listing of one folder costs one ancestor walk.
func markE2e(ctx context.Context, roots *e2eRoots, s *model.Storage, rel string, e *aiEntry) {
	e.Encrypted, e.E2eRoot = roots.mark(ctx, s.ID, s.Name, rel, e.Type == "dir")
}
