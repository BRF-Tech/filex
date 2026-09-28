package model

import "time"

// Permission effects, as stored in a per-user override or a rule. A
// permission missing from the map is "inherit": the layer below decides.
const (
	PermAllow = "allow"
	PermDeny  = "deny"
)

// ValidPermEffect reports whether s is a storable effect.
func ValidPermEffect(s string) bool { return s == PermAllow || s == PermDeny }

// PermTargetSSOGroup is the one rule target kind: it makes the role the
// STARTING role of a new account whose first SSO sign-in carries this group.
// Compared exactly. A person's role lives in user_custom_roles, one each.
const PermTargetSSOGroup = "sso_group"

// PermRuleTarget is one "who" of a permission rule.
type PermRuleTarget struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

// PermRuleSettings are the valued limits a rule carries (as opposed to the
// yes/no permissions). A nil / zero field is "not set by this rule". When
// several rules apply, the most restrictive value wins (perm.Resolve).
type PermRuleSettings struct {
	// ShareLinkMaxDays caps a public link's lifetime; also forbids links
	// with no expiry.
	ShareLinkMaxDays *int `json:"share_link_max_days,omitempty"`
	// ShareLinkPasswordRequired refuses a public link without a password.
	ShareLinkPasswordRequired bool `json:"share_link_password_required,omitempty"`
	// BlockedExtensions refuses uploads by file extension, lowercase and
	// without the dot ("exe", "bat").
	BlockedExtensions []string `json:"blocked_extensions,omitempty"`
	// MaxUploadBytes caps a single file's size.
	MaxUploadBytes *int64 `json:"max_upload_bytes,omitempty"`
	// Require2FA refuses a session to an account without TOTP enabled.
	Require2FA bool `json:"require_2fa,omitempty"`
}

// PermRuleConditions narrow WHERE a rule's effects apply (migration 00069).
// Empty is everywhere. A rule with conditions may only change file
// permissions checked against a path (perm.Conditionable) and carries no
// settings — perm.NormalizeRule refuses anything else.
type PermRuleConditions struct {
	// StorageIDs limits the rule to these storages; empty is every storage.
	StorageIDs []int64 `json:"storage_ids,omitempty"`
	// Paths limits it to storage-relative paths matching any of these
	// patterns: "Archive" or "Archive/**" (that folder and everything in
	// it), "*" within a segment ("*.psd", "Clients/*/Contracts/**").
	Paths []string `json:"paths,omitempty"`
}

// Empty reports whether c applies everywhere.
func (c PermRuleConditions) Empty() bool { return len(c.StorageIDs) == 0 && len(c.Paths) == 0 }

// PermissionRule is one row of permission_rules (migration 00069): a custom
// role. Permissions is what it allows everywhere; Effects with Conditions is
// its "different in some folders" part — Allow or Deny for file actions in
// the places Conditions names.
//
// ProviderID scopes the rule to one tenant; nil is install-wide. Targets and
// Effects are validated by package perm before they are stored — the store
// persists what it is handed.
type PermissionRule struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	// Permissions is the role's own list (migration 00069): what its people
	// may do everywhere.
	Permissions []string           `json:"permissions"`
	ProviderID  *int64             `json:"provider_id,omitempty"`
	Targets     []PermRuleTarget   `json:"targets"`
	Effects     map[string]string  `json:"effects"`
	Settings    PermRuleSettings   `json:"settings"`
	Conditions  PermRuleConditions `json:"conditions"`
	CreatedBy   *int64             `json:"created_by,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

// SettingPermissionDefaults is the settings key holding the permissions a
// role=user account starts from: a JSON array of permission keys. Absent
// means the Standard preset (perm.PresetStandard), which is everything a
// user could do before permissions existed.
const SettingPermissionDefaults = "permissions.defaults"

// SettingPermissionViewerDefaults is the same for the built-in Viewer role: a
// JSON array of permission keys, capped at what a viewer can ever hold.
// Absent means the Read-only preset (perm.ReadOnly) — what a viewer could do
// before roles were editable.
const SettingPermissionViewerDefaults = "permissions.viewer_defaults"
