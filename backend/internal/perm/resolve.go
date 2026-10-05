package perm

import (
	"errors"
	"fmt"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
)

// SourceKind says which layer decided a permission.
type SourceKind string

const (
	// SourceRole: the account role decided outright (admin holds everything;
	// admin.full follows the role alone).
	SourceRole SourceKind = "role"
	// SourceBase: nothing overrode it; it is the role's starting set (the
	// install defaults for role=user, the Read-only preset for role=viewer).
	SourceBase SourceKind = "base"
	// SourceRule: the account's custom role decided it (its list, or its
	// "different in some folders" part).
	SourceRule SourceKind = "rule"
	// SourceOverride: the user's own override.
	SourceOverride SourceKind = "override"
	// SourceViewerCeiling: something allowed it, but a viewer account can
	// never hold it.
	SourceViewerCeiling SourceKind = "viewer_ceiling"
	// SourceRoleOff: the account's custom role is switched off, and a
	// switched-off role gives nothing (never the built-in role instead — for
	// a role that takes things away, that would be MORE access).
	SourceRoleOff SourceKind = "role_off"
)

// Source is where one permission's answer came from.
type Source struct {
	Kind     SourceKind `json:"kind"`
	RuleID   int64      `json:"rule_id,omitempty"`
	RuleName string     `json:"rule_name,omitempty"`
	// RuleNames are the role's names in other languages (migration 00071),
	// kept so a refusal can name the role in its reader's language
	// (RuleNameFor). Not on the wire: rule_name stays the role's own name,
	// and the admin pages hold the translations with the role itself.
	RuleNames map[string]string `json:"-"`
	// GroupID and GroupName say the custom role is the account's through a
	// group (it has none of its own) — set on a SourceRule or SourceRoleOff
	// answer only.
	GroupID   int64  `json:"group_id,omitempty"`
	GroupName string `json:"group_name,omitempty"`
}

// GroupRef names the group an account's custom role came from.
type GroupRef struct {
	ID   int64
	Name string
}

// ruleSource is the Source for an answer role r gave.
func ruleSource(kind SourceKind, r *model.PermissionRule) Source {
	src := Source{Kind: kind, RuleID: r.ID, RuleName: r.Name}
	if len(r.Names) > 0 {
		src.RuleNames = r.Names
	}
	return src
}

// RuleNameFor is the deciding role's name for a reader of lang: its
// translation, else its own name (model.LocalizedText, the one rule
// PermissionRule.NameFor follows too).
func (s Source) RuleNameFor(lang string) string {
	return model.LocalizedText(s.RuleName, s.RuleNames, lang)
}

// Input is everything Resolve needs about one account. The Loader fills it
// from the store; tests build it by hand.
type Input struct {
	UserID     int64
	Role       string
	ProviderID *int64
	// CustomRoleID is the one custom role the account holds: its own
	// (user_custom_roles), else its groups' (EffectiveRole); 0 is none.
	CustomRoleID int64
	// ViaGroup is the group CustomRoleID came from; nil when it is the
	// account's own.
	ViaGroup *GroupRef
	// Groups are the account's groups, for Loader.Preview only: filled from
	// the store before the change, so a change can leave one out or alter
	// one (a group deleted, its role cleared), and the role is then picked
	// from them. Resolve does not read it.
	Groups []*model.Group
	// Defaults is the base set for role=user (model.SettingPermissionDefaults).
	Defaults Set
	// ViewerBase is the base set for role=viewer
	// (model.SettingPermissionViewerDefaults); nil is the Read-only preset.
	ViewerBase *Set
	// Rules is every custom role on the install; Resolve applies the
	// account's own (CustomRoleID), if it is enabled and in scope.
	Rules []*model.PermissionRule
	// Overrides is the user's own allow/deny map (keys are Perm names, and
	// app permission keys — perm/app.go).
	Overrides map[string]string
	// AppDecisions are the account's built-in role's decisions about app
	// permissions (SettingPermissionAppDefaults for its role).
	AppDecisions map[string]string
}

// Result is one account's resolved permissions.
type Result struct {
	Allowed Set
	Sources map[Perm]Source
	// Settings is the most restrictive merge of every applying rule's
	// settings.
	Settings model.PermRuleSettings
	// Rules are the ids of the rules that applied, in input order.
	Rules []int64

	// ViaGroup is the group the account's custom role came from, or nil.
	ViaGroup *GroupRef

	// role and gen are what Loader's per-request memo checks an entry
	// against before reusing it.
	role string
	gen  uint64

	// What CanAt needs to re-decide one permission for one path: the layers
	// below the conditioned rules, and those rules themselves.
	admin       bool
	base        Set
	baseSrc     Source
	overrides   map[string]string
	hits        map[Perm]*ruleHit
	conditional []*model.PermissionRule

	// What AppAllowed needs (perm/app.go): the custom role the account holds
	// when it applies, and its built-in role's app decisions.
	customRole  *model.PermissionRule
	builtinApps map[string]string
}

// ruleHit is, for one permission, the first rule that denied it and the first
// that allowed it.
type ruleHit struct{ allow, deny *model.PermissionRule }

// ConditionalRules returns the ids of the applying rules that bind only some
// storages or paths — they are not in Rules, and not in Allowed.
func (r *Result) ConditionalRules() []int64 {
	if r == nil {
		return nil
	}
	out := make([]int64, 0, len(r.conditional))
	for _, c := range r.conditional {
		out = append(out, c.ID)
	}
	return out
}

// AllowedInFolders is what the account may do only in some folders: the
// permissions its role's "different in some folders" part allows that it
// does not hold everywhere — minus what its own exceptions deny, and what a
// viewer can never hold. A client offers these actions and lets the server
// decide per path (CanAt); it must not hide them.
func (r *Result) AllowedInFolders() Set {
	if r == nil || r.admin {
		return 0
	}
	var out Set
	for _, c := range r.conditional {
		for k, eff := range c.Effects {
			p := Perm(k)
			d, ok := Lookup(p)
			if !ok || eff != model.PermAllow || r.Allowed.Has(p) || d.RoleOnly {
				continue
			}
			if r.overrides[k] == model.PermDeny || (r.role == model.RoleViewer && d.ViewerCapped) {
				continue
			}
			out = out.With(p)
		}
	}
	return out
}

// VariesByFolder is every permission whose answer can differ from path to
// path: those the role's folder part allows or denies somewhere — minus what
// an exception of the account's own fixes everywhere, and what a viewer can
// never hold. A client asks the server (CanAt, per path) about these before
// offering them, and trusts Allowed for the rest.
func (r *Result) VariesByFolder() Set {
	if r == nil || r.admin {
		return 0
	}
	var out Set
	for _, c := range r.conditional {
		for k := range c.Effects {
			p := Perm(k)
			d, ok := Lookup(p)
			if !ok || d.RoleOnly || !Conditionable(p) {
				continue
			}
			if _, fixed := r.overrides[k]; fixed {
				continue
			}
			if r.role == model.RoleViewer && d.ViewerCapped {
				continue
			}
			out = out.With(p)
		}
	}
	return out
}

// CanAt is Can for an action on one path of one storage: the same answer,
// except where a conditioned rule (model.PermRuleConditions) matches — its
// allow or deny then counts as a rule's does, beneath the user's overrides.
func (r *Result) CanAt(storageID int64, rel string, p Perm) bool {
	ok, _ := r.decideAt(storageID, rel, p)
	return ok
}

// WhyAt is Why for one path.
func (r *Result) WhyAt(storageID int64, rel string, p Perm) Source {
	_, src := r.decideAt(storageID, rel, p)
	return src
}

func (r *Result) decideAt(storageID int64, rel string, p Perm) (bool, Source) {
	if r == nil {
		return false, Source{}
	}
	if r.admin || len(r.conditional) == 0 || !Conditionable(p) {
		return r.Can(p), r.Why(p)
	}
	var h ruleHit
	if base := r.hits[p]; base != nil {
		h = *base
	}
	matched := false
	for _, c := range r.conditional {
		eff, ok := c.Effects[string(p)]
		if !ok || !conditionsMatch(c.Conditions, storageID, rel) {
			continue
		}
		matched = true
		switch eff {
		case model.PermDeny:
			if h.deny == nil {
				h.deny = c
			}
		case model.PermAllow:
			if h.allow == nil {
				h.allow = c
			}
		}
	}
	if !matched {
		return r.Can(p), r.Why(p)
	}
	d, _ := Lookup(p)
	ok, src := decide(d, r.base, r.baseSrc, &h, r.overrides, r.role)
	return ok, r.viaGroup(src)
}

// viaGroup adds the group the custom role came from to an answer that the
// role decided.
func (r *Result) viaGroup(src Source) Source {
	if r.ViaGroup != nil && (src.Kind == SourceRule || src.Kind == SourceRoleOff) {
		src.GroupID, src.GroupName = r.ViaGroup.ID, r.ViaGroup.Name
	}
	return src
}

// decide is one permission's answer from its layers — shared by Resolve (the
// account-wide answer) and CanAt (one path's).
func decide(d Def, base Set, baseSrc Source, h *ruleHit, overrides map[string]string, role string) (bool, Source) {
	p := d.Key
	allowed, src := base.Has(p), baseSrc
	if h != nil {
		switch {
		case h.deny != nil:
			allowed, src = false, ruleSource(SourceRule, h.deny)
		case h.allow != nil:
			allowed, src = true, ruleSource(SourceRule, h.allow)
		}
	}
	if eff, ok := overrides[string(p)]; ok && !d.RoleOnly {
		switch eff {
		case model.PermAllow:
			allowed, src = true, Source{Kind: SourceOverride}
		case model.PermDeny:
			allowed, src = false, Source{Kind: SourceOverride}
		}
	}
	if d.RoleOnly {
		allowed, src = false, Source{Kind: SourceRole}
	}
	if allowed && role == model.RoleViewer && d.ViewerCapped {
		allowed, src = false, Source{Kind: SourceViewerCeiling}
	}
	return allowed, src
}

// Can reports whether p is allowed.
func (r *Result) Can(p Perm) bool { return r != nil && r.Allowed.Has(p) }

// Why returns where p's answer came from.
func (r *Result) Why(p Perm) Source {
	if r == nil {
		return Source{}
	}
	return r.Sources[p]
}

// Resolve computes an account's permissions. Layers, lowest first:
//
//  1. base — role=user: in.Defaults; role=viewer: in.ViewerBase (the
//     Read-only preset when unset); other: nothing.
//  2. the account's custom role (one per person, CustomRoleID — its own, or
//     else its groups', EffectiveRole), when it is enabled and in the
//     account's tenant: its own list REPLACES the base, and its "different
//     in some folders" part applies per path (CanAt).
//  3. overrides — the user's own allow/deny; always wins over rules.
//
// Then two hard lines no layer can cross: role-only permissions (admin.full)
// follow the role alone, and a viewer account never holds a ViewerCapped
// permission. role=admin short-circuits everything: an administrator holds
// every permission and is bound by no rule (they could edit the rule anyway).
func Resolve(in Input) *Result {
	res := &Result{Sources: make(map[Perm]Source, len(catalogue)), role: in.Role, ViaGroup: in.ViaGroup}

	if in.Role == model.RoleAdmin {
		res.admin = true
		res.Allowed = allSet
		for _, d := range catalogue {
			res.Sources[d.Key] = Source{Kind: SourceRole}
		}
		return res
	}

	var base Set
	viewerBase := ReadOnly
	if in.ViewerBase != nil {
		viewerBase = *in.ViewerBase & viewerCeiling
	}
	switch in.Role {
	case model.RoleUser:
		base = in.Defaults
	case model.RoleViewer:
		base = viewerBase
	}
	baseSrc := Source{Kind: SourceBase}

	// The account's custom role, if it holds one (one per person) and it is
	// enabled and in scope, IS its starting set: a stand-alone list, not a
	// change to the built-in role. Its "different in some folders" part is
	// kept aside for CanAt — account-wide it applies nowhere in particular.
	hits := map[Perm]*ruleHit{}
	for _, r := range in.Rules {
		if r == nil || in.CustomRoleID == 0 || r.ID != in.CustomRoleID {
			continue
		}
		// ⚠ A held role outside the account's tenant (given across tenants
		// by the supertenant, or the account moved since) is a switched-off
		// role, not "no role": falling back to the built-in role would turn
		// a role that takes things away into more access.
		if !r.Enabled || !inScope(r, in.ProviderID) {
			base, baseSrc = 0, ruleSource(SourceRoleOff, r)
			break
		}
		base = RoleSet(r)
		baseSrc = ruleSource(SourceRule, r)
		res.customRole = r
		res.Rules = append(res.Rules, r.ID)
		mergeSettings(&res.Settings, r.Settings)
		if !r.Conditions.Empty() && len(r.Effects) > 0 {
			res.conditional = append(res.conditional, r)
		}
		break
	}

	res.base, res.baseSrc, res.overrides, res.hits = base, baseSrc, in.Overrides, hits
	res.builtinApps = in.AppDecisions
	for _, d := range catalogue {
		p := d.Key
		allowed, src := decide(d, base, baseSrc, hits[p], in.Overrides, in.Role)
		if allowed {
			res.Allowed = res.Allowed.With(p)
		}
		res.Sources[p] = res.viaGroup(src)
	}
	return res
}

// ruleApplies reports whether r is the account's custom role, enabled and in
// its tenant. Targets play no part: an SSO-group target only chooses a new
// account's STARTING role (StartingRole), after which the role is the
// person's like any other.
func ruleApplies(r *model.PermissionRule, in Input) bool {
	if r == nil || !r.Enabled || in.CustomRoleID == 0 || r.ID != in.CustomRoleID {
		return false
	}
	return inScope(r, in.ProviderID)
}

func inScope(r *model.PermissionRule, providerID *int64) bool {
	return r.ProviderID == nil || (providerID != nil && *providerID == *r.ProviderID)
}

// StartingRole is the custom role a NEW account is given when its first SSO
// sign-in carries one of the groups a role names: the lowest-numbered enabled
// role in the account's tenant with a matching sso_group target, or nil.
func StartingRole(rules []*model.PermissionRule, groups []string, providerID *int64) *model.PermissionRule {
	for _, r := range rules {
		if r == nil || !r.Enabled || !inScope(r, providerID) {
			continue
		}
		for _, t := range r.Targets {
			if t.Kind == model.PermTargetSSOGroup && containsString(groups, t.Value) {
				return r
			}
		}
	}
	return nil
}

// mergeSettings folds src into dst keeping the most restrictive of each.
func mergeSettings(dst *model.PermRuleSettings, src model.PermRuleSettings) {
	if src.ShareLinkMaxDays != nil && (dst.ShareLinkMaxDays == nil || *src.ShareLinkMaxDays < *dst.ShareLinkMaxDays) {
		v := *src.ShareLinkMaxDays
		dst.ShareLinkMaxDays = &v
	}
	if src.MaxUploadBytes != nil && (dst.MaxUploadBytes == nil || *src.MaxUploadBytes < *dst.MaxUploadBytes) {
		v := *src.MaxUploadBytes
		dst.MaxUploadBytes = &v
	}
	// App decisions: a deny beats an allow (the most restrictive, as above).
	for k, eff := range src.Apps {
		if dst.Apps == nil {
			dst.Apps = map[string]string{}
		}
		if cur, ok := dst.Apps[k]; !ok || cur != model.PermDeny {
			dst.Apps[k] = eff
		}
	}
	dst.ShareLinkPasswordRequired = dst.ShareLinkPasswordRequired || src.ShareLinkPasswordRequired
	dst.Require2FA = dst.Require2FA || src.Require2FA
	for _, ext := range src.BlockedExtensions {
		if !containsString(dst.BlockedExtensions, ext) {
			dst.BlockedExtensions = append(dst.BlockedExtensions, ext)
		}
	}
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// ErrInvalid wraps every validation failure so callers can map it to a 400.
var ErrInvalid = errors.New("perm: invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
}

// ValidateEffects checks an overrides or rule effects map: known keys, not
// role-only, allow/deny only.
func ValidateEffects(m map[string]string) error { return validateEffects(m, nil) }

// validateEffects is ValidateEffects where a key this version does not know
// passes when kept says it may (a later version's key the map held already,
// ValidateEffectsEdit).
func validateEffects(m map[string]string, kept func(string) bool) error {
	for _, k := range sortedStrings(m) {
		// An app permission (perm/app.go) is not in the catalogue; it is
		// valid by its shape, and a key whose app is gone is never asked.
		if IsAppKey(k) {
			if !model.ValidPermEffect(m[k]) {
				return invalid("%q: effect must be %q or %q, got %q", k, model.PermAllow, model.PermDeny, m[k])
			}
			continue
		}
		d, ok := Lookup(Perm(k))
		if !ok {
			if kept != nil && kept(k) {
				continue
			}
			return invalid("unknown permission %q", k)
		}
		if d.RoleOnly {
			return invalid("%q follows the account role and cannot be set", k)
		}
		if !model.ValidPermEffect(m[k]) {
			return invalid("%q: effect must be %q or %q, got %q", k, model.PermAllow, model.PermDeny, m[k])
		}
	}
	return nil
}

// MaxRuleNameLen bounds a rule's name.
const MaxRuleNameLen = 100

// NormalizeRule validates r in place and canonicalizes what it can (trimmed
// name and targets, lowercase dot-less extensions). It does not check that
// user ids or the provider exist — that is the handler's job, with the store.
func NormalizeRule(r *model.PermissionRule) error { return NormalizeRuleEdit(r, nil) }

// NormalizeRuleEdit is NormalizeRule for a new version of prev (nil for a new
// role). Two things differ: a language prev's translations already carry
// stays acceptable (normalizeRuleTexts), and a permission prev holds that this
// version does not know - a later version's - is kept as prev has it, in the
// role's own list and in its folder part, whether the request sends it back or
// leaves it out (foreign.go). A folder part taken away takes those with it.
func NormalizeRuleEdit(r, prev *model.PermissionRule) error {
	var keepList []string
	var keepEffects map[string]string
	if prev != nil {
		keepList = ForeignKeys(prev.Permissions)
		keepEffects = ForeignEffects(prev.Effects)
	}
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return invalid("rule name is required")
	}
	if len([]rune(r.Name)) > MaxRuleNameLen {
		return invalid("rule name is longer than %d characters", MaxRuleNameLen)
	}
	r.Description = strings.TrimSpace(r.Description)
	if err := normalizeRuleTexts(r, prev); err != nil {
		return err
	}
	// A stand-alone role: its own list, every name known and none role-only.
	// An empty list is a role that may do nothing.
	if r.Permissions == nil {
		r.Permissions = []string{}
	}
	for _, k := range r.Permissions {
		d, ok := Lookup(Perm(k))
		if !ok {
			if containsString(keepList, k) {
				continue // put back below, where prev had it
			}
			return invalid("unknown permission %q", k)
		}
		if d.RoleOnly {
			return invalid("%q follows the account role and cannot be set", k)
		}
	}
	r.Permissions = append(FromStrings(r.Permissions).Strings(), keepList...)
	// Targets only name SSO groups — the role a new account starts with when
	// its first sign-in carries one. A person is given a role on their own
	// page (one per person), never through the role's targets.
	seen := map[model.PermRuleTarget]bool{}
	targets := r.Targets[:0]
	for _, t := range r.Targets {
		t.Value = strings.TrimSpace(t.Value)
		switch t.Kind {
		case model.PermTargetSSOGroup:
			if t.Value == "" {
				return invalid("sso_group target needs a group name")
			}
		default:
			return invalid("unknown target kind %q: a role is given to a person on their own page; targets only name SSO groups (the starting role)", t.Kind)
		}
		if !seen[t] {
			seen[t] = true
			targets = append(targets, t)
		}
	}
	r.Targets = targets
	if r.Effects == nil {
		r.Effects = map[string]string{}
	}
	if err := ValidateEffectsEdit(r.Effects, keepEffects); err != nil {
		return err
	}
	// The later version's folder keys are put back as prev has them, once
	// the folder part is known to stay (below).
	r.Effects = WithoutForeign(r.Effects)
	if len(r.Effects) > 0 && r.Conditions.Empty() {
		return invalid("Allow and Deny are only for some folders: pick the folders, or set the permission in the role's own list")
	}

	s := &r.Settings
	if s.ShareLinkMaxDays != nil && *s.ShareLinkMaxDays < 1 {
		return invalid("share_link_max_days must be at least 1")
	}
	if s.MaxUploadBytes != nil && *s.MaxUploadBytes < 1 {
		return invalid("max_upload_bytes must be at least 1")
	}
	if err := validateAppEffects(s.Apps); err != nil {
		return err
	}
	if len(s.Apps) == 0 {
		s.Apps = nil
	}
	var exts []string
	for _, e := range s.BlockedExtensions {
		e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
		if e == "" {
			continue
		}
		if strings.ContainsAny(e, `/\ `) {
			return invalid("blocked extension %q is not a file extension", e)
		}
		if !containsString(exts, e) {
			exts = append(exts, e)
		}
	}
	s.BlockedExtensions = exts

	if err := normalizeConditions(r); err != nil {
		return err
	}
	if !r.Conditions.Empty() {
		r.Effects = KeepForeignEffects(r.Effects, keepEffects)
	}
	return nil
}
