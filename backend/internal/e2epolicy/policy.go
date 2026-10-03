// Package e2epolicy decides who may end-to-end encrypt what, and where
// (docs/E2E-ENCRYPTION.md → Who may encrypt, docs/PERMISSIONS.md → The
// permissions).
//
// Encrypting is CREATING one of two names: a folder marker (.filex-e2e.json)
// where there was none — a new encrypted folder, or the first step of
// encrypting a folder in place — or a new single encrypted file (*.fxe).
// Everything else an encrypted folder does stays free: rewriting its marker
// (a new password, a recovery key, another level), adding files to it,
// taking the encryption off, copying or moving what is encrypted already (a
// folder, a `.fxe` that stays a `.fxe`; RelocationEncrypts).
//
// Three layers must all say yes, asked in this order:
//
//  1. the platform's ceiling for the tenant (providers.e2e_allowed, which
//     only the supertenant changes): off binds administrators too;
//  2. the tenant's policy (providers.e2e_policy; the e2e.policy setting on a
//     single-tenant install): off, admins, permitted — the default, what
//     filex always did — or approval;
//  3. the person's files.encrypt on the path. Administrators always hold it:
//     roles cannot narrow them, the policy can. Under the approval policy a
//     person also needs an administrator's approval for that folder, good
//     for one encryption and ApprovalTTL.
//
// The server cannot see what a client encrypts, only the names it creates.
// So the rule is enforced where a name is created: every door that creates
// a file calls Service.CheckCreate at the moment it knows the write CREATES
// the path (nothing is catalogued there), and the explorer only mirrors the
// answer (Service.AnswerFor). The server decides.
package e2epolicy

import (
	"errors"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Answer is the rule's verdict on one encryption at one place.
type Answer string

const (
	// AnswerAllowed: go ahead.
	AnswerAllowed Answer = "allowed"
	// AnswerRequest: allowed once an administrator approves it. The explorer
	// offers "Request encryption" in its place.
	AnswerRequest Answer = "request"
	// AnswerDenied: not here, not by this person.
	AnswerDenied Answer = "denied"
)

// Reason says which layer refused. It is the `reason` of a 403
// e2e_not_allowed and the last segment of its message key
// (server.e2e.not_allowed.<reason>); "" with AnswerAllowed.
type Reason string

const (
	ReasonTenantDisabled   Reason = "tenant_disabled"
	ReasonPolicyOff        Reason = "policy_off"
	ReasonAdminsOnly       Reason = "admins_only"
	ReasonPermission       Reason = "permission"
	ReasonApprovalRequired Reason = "approval_required"
)

// ApprovalTTL is how long an approval may wait to be used, and how long a
// request may wait for a decision.
const ApprovalTTL = 7 * 24 * time.Hour

// Input is everything Decide weighs.
type Input struct {
	// TenantAllowed is the platform's ceiling (always true on a single-tenant
	// install).
	TenantAllowed bool
	// Policy is one of the model.E2EPolicy* values. Anything else is read as
	// model.E2EPolicyPermitted, the default.
	Policy string
	// IsAdmin: an administrator of the install or of the tenant.
	IsAdmin bool
	// HasPermission: files.encrypt on the path (acl.Set.Can).
	HasPermission bool
	// HasApproval: an unused, unexpired approval for this person and folder.
	HasApproval bool
}

// Decide is the rule, with nothing left to look up.
func Decide(in Input) (Answer, Reason) {
	if !in.TenantAllowed {
		return AnswerDenied, ReasonTenantDisabled
	}
	switch in.Policy {
	case model.E2EPolicyOff:
		return AnswerDenied, ReasonPolicyOff
	case model.E2EPolicyAdmins:
		if !in.IsAdmin {
			return AnswerDenied, ReasonAdminsOnly
		}
	}
	// Roles cannot narrow an administrator (docs/PERMISSIONS.md), and an
	// administrator is who approves: the only thing that stops one is the
	// ceiling or the policy above.
	if in.IsAdmin {
		return AnswerAllowed, ""
	}
	if !in.HasPermission {
		return AnswerDenied, ReasonPermission
	}
	if in.Policy == model.E2EPolicyApproval && !in.HasApproval {
		return AnswerRequest, ReasonApprovalRequired
	}
	return AnswerAllowed, ""
}

// IsEncryptionName reports whether creating rel encrypts something: its name
// is the folder marker or a single encrypted file's (".fxe"), either in any
// case. The name is all that counts; the server cannot read what a client
// meant.
func IsEncryptionName(rel string) bool {
	_, kind := TargetOf(rel)
	return kind != ""
}

// TargetOf is what creating rel encrypts, and where: the folder the marker
// sits in (the folder becoming encrypted), or the folder a .fxe goes into.
// kind is model.E2ERequestFolder or model.E2ERequestFile, and "" (with dir
// "") for a name that encrypts nothing. dir is storage-relative, with no
// leading slash; "" is the storage root.
//
// ⚠ The marker is matched in any case, as .fxe is. A case-insensitive
// storage keeps one file under ".filex-e2e.json" and ".FILEX-E2E.JSON", so a
// creation spelled the second way would escape the rule and still be read
// back as the marker.
func TargetOf(rel string) (dir string, kind string) {
	clean := acl.CleanRel(rel)
	switch base := path.Base(clean); {
	case strings.EqualFold(base, e2e.MarkerName):
		return parentOf(clean), model.E2ERequestFolder
	case e2e.LooksEncryptedFile(base):
		return parentOf(clean), model.E2ERequestFile
	}
	return "", ""
}

// ApprovalPath is the folder an approval for encrypting rel as kind is kept
// under, and looked for by: the folder itself for a folder, the folder the
// file is in for a file. Requests are stored under it and CheckCreate and
// AnswerFor look for them there, so the two cannot disagree.
//
// ⚠ A file's approval is its FOLDER's. The name a .fxe is stored under
// cannot be known when the person asks: a hidden name is random, a visible
// one takes the next free "name (2).ext.fxe". An approval is for this
// person, this folder, one encryption of this kind.
func ApprovalPath(rel, kind string) string {
	clean := acl.CleanRel(rel)
	if kind == model.E2ERequestFile {
		return parentOf(clean)
	}
	return clean
}

// parentOf is the folder holding clean (an acl.CleanRel path), "" for the
// storage root.
func parentOf(clean string) string {
	if i := strings.LastIndexByte(clean, '/'); i >= 0 {
		return clean[:i]
	}
	return ""
}

// ErrRefused is what every refusal of the rule unwraps to.
var ErrRefused = errors.New("e2e: encryption not allowed here")

// RefusedError is the rule's refusal, naming the layer that refused. The
// HTTP doors answer it as 403 e2e_not_allowed with the reason.
type RefusedError struct {
	Reason Reason
}

func (e *RefusedError) Error() string { return ErrRefused.Error() + ": " + string(e.Reason) }

// Unwrap lets errors.Is(err, ErrRefused) find the refusal under any wrapping.
func (e *RefusedError) Unwrap() error { return ErrRefused }
