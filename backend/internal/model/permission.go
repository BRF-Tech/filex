package model

import (
	"strings"
	"time"
)

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
	// Apps are the role's decisions about app permissions (perm/app.go):
	// "app.sign.request" → allow | deny. A key it does not name follows the
	// built-in role's decision, then the app's own default.
	Apps map[string]string `json:"apps,omitempty"`
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
	// Names and Descriptions are the role in other interface languages
	// (migration 00071): language code → text, {"tr": "Muhasebe"}. Name and
	// Description stay the role's own — the answer for a language without an
	// entry, and what the audit log records. Pick for a reader with NameFor
	// and DescriptionFor, never by indexing these.
	Names        map[string]string `json:"names,omitempty"`
	Descriptions map[string]string `json:"descriptions,omitempty"`
	Enabled      bool              `json:"enabled"`
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

// NameFor is the role's name for a reader whose interface language is lang
// (LocalizedText): the one place that choice is made — the refusal sentences
// reach it through perm.Source.
func (r *PermissionRule) NameFor(lang string) string {
	if r == nil {
		return ""
	}
	return LocalizedText(r.Name, r.Names, lang)
}

// DescriptionFor is NameFor for the description.
func (r *PermissionRule) DescriptionFor(lang string) string {
	if r == nil {
		return ""
	}
	return LocalizedText(r.Description, r.Descriptions, lang)
}

// LocalizedText is base as a reader of lang sees it: the translation for lang
// itself, then for its primary language ("pt-br" reads "pt"), then base. The
// tag is compared lower-cased with "_" read as "-"; a blank translation is
// none. The web admin's roleName (web/src/lib/roleName.ts) is its twin.
func LocalizedText(base string, texts map[string]string, lang string) string {
	if len(texts) == 0 {
		return base
	}
	tag := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(lang)), "_", "-")
	if tag == "" {
		return base
	}
	if v := strings.TrimSpace(texts[tag]); v != "" {
		return v
	}
	if i := strings.IndexByte(tag, '-'); i > 0 {
		if v := strings.TrimSpace(texts[tag[:i]]); v != "" {
			return v
		}
	}
	return base
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

// SettingPermissionAppDefaults holds the built-in roles' decisions about app
// permissions (perm/app.go): {"user": {"app.sign.request": "deny"}, …}.
const SettingPermissionAppDefaults = "permissions.app_defaults"

// SettingPermissionCatalogue is the settings key holding the permission
// catalogue this install last started with: a JSON array of keys, written by
// perm.UpgradeCatalogue. Absent means v0.49.0's, the release before it was
// recorded.
const SettingPermissionCatalogue = "permissions.catalogue"
