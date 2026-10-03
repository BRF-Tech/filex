package model

import "time"

// Who may encrypt (migration 00080, internal/e2epolicy, docs/E2E-ENCRYPTION.md
// → Who may encrypt).
//
// Encrypting is CREATING a folder marker (.filex-e2e.json) where there was
// none, or a new single encrypted file (*.fxe). Three layers must all agree:
// the platform's ceiling for the tenant (ProviderE2E.Allowed), the tenant's
// policy (ProviderE2E.Policy — on a single-tenant install the SettingE2EPolicy
// setting) and the person's files.encrypt permission on the path. Under the
// approval policy the person also asks first (E2ERequest).

// The tenant's policies (providers.e2e_policy, the e2e.policy setting).
const (
	// E2EPolicyOff: nobody encrypts anything new, administrators included.
	// Folders that are encrypted already keep working.
	E2EPolicyOff = "off"
	// E2EPolicyAdmins: administrators only.
	E2EPolicyAdmins = "admins"
	// E2EPolicyPermitted: whoever holds files.encrypt on the path — what
	// everybody who could write could do before 00080. The default.
	E2EPolicyPermitted = "permitted"
	// E2EPolicyApproval: whoever holds files.encrypt, once an administrator
	// approved it for that person and that folder.
	E2EPolicyApproval = "approval"
)

// ValidE2EPolicy reports whether s is one of the four policies.
func ValidE2EPolicy(s string) bool {
	switch s {
	case E2EPolicyOff, E2EPolicyAdmins, E2EPolicyPermitted, E2EPolicyApproval:
		return true
	default:
		return false
	}
}

// SettingE2EPolicy holds a single-tenant install's policy. A multi-tenant
// install reads each tenant's providers.e2e_policy instead.
const SettingE2EPolicy = "e2e.policy"

// ProviderE2E is one tenant's two encryption columns, read and written apart
// from the rest of the provider row (Store.GetProviderE2E/SetProviderE2E).
type ProviderE2E struct {
	// Allowed is the platform's ceiling, which only the supertenant changes.
	// Off: nobody in the tenant encrypts anything new, whatever the policy.
	Allowed bool
	// Policy is the tenant's own choice, one of the E2EPolicy* values.
	Policy string
}

// E2ERequest is a person's request to encrypt in one folder, left where the
// policy is E2EPolicyApproval, for an administrator of the tenant to approve
// or reject. An approval is good for ONE encryption of its kind in its folder,
// by that person, before ExpiresAt.
type E2ERequest struct {
	ID int64
	// ProviderID is the tenant whose administrators decide; nil on a
	// single-tenant install.
	ProviderID *int64
	// UserID is who asked; Requester their name as shown when they asked.
	UserID    int64
	Requester string
	// StorageID and Path say where: the folder P approved (E2ERequestFolder:
	// P itself becomes encrypted, or one folder directly inside P), or the
	// folder a single encrypted file goes into (E2ERequestFile).
	// Storage-relative, no leading slash, "" = the root.
	StorageID int64
	Path      string
	// Kind is E2ERequestFolder or E2ERequestFile.
	Kind string
	// Reason is the requester's own words.
	Reason string
	// Status is one of the E2ERequest* states.
	Status string
	// DecidedBy/Decider/DecidedAt: who closed the request and when (an
	// expired one: nobody).
	DecidedBy *int64
	Decider   string
	DecidedAt *time.Time
	// DecisionNote is a rejection's reason.
	DecisionNote string
	// ExpiresAt: a pending request past it expires, and so does an approval
	// nobody used.
	ExpiresAt time.Time
	// UsedAt is when the approved encryption happened (E2ERequestUsed).
	UsedAt    *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Encryption request states and kinds (e2e_requests columns).
const (
	E2ERequestPending  = "pending"
	E2ERequestApproved = "approved"
	E2ERequestRejected = "rejected"
	E2ERequestExpired  = "expired"
	E2ERequestUsed     = "used"

	E2ERequestFolder = "folder"
	E2ERequestFile   = "file"
)

// E2ERequestFilter narrows Store.ListE2ERequests. A nil pointer, "" or a zero
// time is no condition; Limit <= 0 (or over 500) is 500.
type E2ERequestFilter struct {
	ProviderID *int64
	UserID     *int64
	Status     string
	// ExpiresBefore keeps what is due — an expiry at or before it — and lists
	// it oldest expiry first instead of newest first, so a sweep reading a
	// batch at a time reaches every due row, however many newer ones there
	// are.
	ExpiresBefore time.Time
	Limit         int
}
