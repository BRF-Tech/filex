package perm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Lists that allow a permission but not the one carved out of it.
//
// A permission carved out of an older one (inheritOnAdd: files.encrypt out of
// files.create) is given once, at the first start of the version that adds it,
// to every saved list that allows the older one (UpgradeCatalogue). A save on
// a version without the key can still take it away without a word: 0.50's
// built-in User role page wrote its list back without files.encrypt, and its
// role editor kept only the folder permissions it knew (PR #86). The merge
// does not run again, and such a list looks exactly like one an administrator
// took the key away from on purpose - nothing stored tells the two apart.
//
// So nothing is given back by itself. A Gap is such a list: the built-in User
// role's when it allows the older key and not the carved-out one, and a custom
// role's folder part when it allows the older key there, does not decide the
// carved-out one and the role's own list does not hold it either. Admin ->
// Roles shows each with one click to give the key back and one to say it was
// on purpose (dismissed: not shown again while it stays as it is), and the
// administrators are told once in the bell (internal/permgap).
//
// A role's own list is not looked at: 0.50 refused to save one that held
// files.encrypt (400), and "may add files, may not encrypt" is a role an
// administrator makes on purpose.
//
// A save on a version that knows the key, from an editor that showed it
// (NoteGapsSaved), is a decision: a gap it opens is dismissed there and then.

// Gap is one list that allows From and not Key.
type Gap struct {
	// ID names the gap for the restore and dismiss requests:
	// "builtin:user:files.encrypt", "role:12:files.encrypt".
	ID   string `json:"id"`
	Key  Perm   `json:"key"`
	From Perm   `json:"from"`
	// Role is the built-in role whose list it is ("user"); empty for a
	// custom role.
	Role string `json:"role,omitempty"`
	// RuleID and RuleName are the custom role whose folder part it is.
	RuleID   int64  `json:"rule_id,omitempty"`
	RuleName string `json:"rule_name,omitempty"`
	// ProviderID is the custom role's tenant: nil for the platform's own
	// roles and on an install without tenants.
	ProviderID *int64 `json:"-"`
}

// Builtin reports whether the gap is in a built-in role's list.
func (g Gap) Builtin() bool { return g.Role != "" }

func builtinGapID(role string, key Perm) string { return "builtin:" + role + ":" + string(key) }

func ruleGapID(id int64, key Perm) string {
	return "role:" + strconv.FormatInt(id, 10) + ":" + string(key)
}

// carvedOut is inheritOnAdd in key order.
func carvedOut() []inheritance {
	out := make([]inheritance, 0, len(inheritOnAdd))
	for k, from := range inheritOnAdd {
		out = append(out, inheritance{key: k, from: from})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// CarvedOut returns the permissions carved out of an older one, in key order:
// the ones a gap can be about.
func CarvedOut() []Perm {
	var out []Perm
	for _, f := range carvedOut() {
		out = append(out, f.key)
	}
	return out
}

// builtinGaps returns the gaps of a built-in role's list as stored. A list
// never saved (nil) has none: the role reads its preset, which holds every
// key of this version.
func builtinGaps(role string, list []string, room Set) []Gap {
	if list == nil {
		return nil
	}
	s := FromStrings(list)
	var out []Gap
	for _, f := range carvedOut() {
		if room.Has(f.key) && s.Has(f.from) && !s.Has(f.key) {
			out = append(out, Gap{ID: builtinGapID(role, f.key), Key: f.key, From: f.from, Role: role})
		}
	}
	return out
}

// RuleGaps returns the gaps of a custom role's folder part: it allows the
// older key there, does not decide the carved-out one, and the role's own
// list does not hold it (where it does, the folder part inherits it).
func RuleGaps(r *model.PermissionRule) []Gap {
	if r == nil || r.Conditions.Empty() {
		return nil
	}
	own := FromStrings(r.Permissions)
	var out []Gap
	for _, f := range carvedOut() {
		if !conditionable.Has(f.key) || r.Effects[string(f.from)] != model.PermAllow || own.Has(f.key) {
			continue
		}
		if _, decided := r.Effects[string(f.key)]; decided {
			continue
		}
		out = append(out, Gap{
			ID: ruleGapID(r.ID, f.key), Key: f.key, From: f.from,
			RuleID: r.ID, RuleName: r.Name, ProviderID: r.ProviderID,
		})
	}
	return out
}

// BuiltinRoleGaps returns the gaps of a built-in role's saved list.
func BuiltinRoleGaps(ctx context.Context, store db.Store, role string) ([]Gap, error) {
	for _, b := range builtinLists {
		if b.role != role {
			continue
		}
		list, err := storedList(ctx, store, b.key)
		if err != nil {
			return nil, err
		}
		return builtinGaps(role, list, b.room), nil
	}
	return nil, nil
}

// AllGaps returns every gap, dismissed or not: the built-in roles' first, then
// the custom roles' in id order.
func AllGaps(ctx context.Context, store db.Store) ([]Gap, error) {
	var out []Gap
	for _, b := range builtinLists {
		g, err := BuiltinRoleGaps(ctx, store, b.role)
		if err != nil {
			return nil, err
		}
		out = append(out, g...)
	}
	rules, err := store.ListPermissionRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("roles: %w", err)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	for _, r := range rules {
		out = append(out, RuleGaps(r)...)
	}
	return out, nil
}

// OpenGaps returns the gaps nobody has said were on purpose.
func OpenGaps(ctx context.Context, store db.Store) ([]Gap, error) {
	all, err := AllGaps(ctx, store)
	if err != nil {
		return nil, err
	}
	dismissed, err := readGapIDs(ctx, store, model.SettingPermissionGapsDismissed)
	if err != nil {
		return nil, err
	}
	var out []Gap
	for _, g := range all {
		if !dismissed[g.ID] {
			out = append(out, g)
		}
	}
	return out, nil
}

// FindGap returns the gap named id, dismissed or not; false when there is
// no such gap any more (the list was saved, the role deleted).
func FindGap(ctx context.Context, store db.Store, id string) (Gap, bool, error) {
	all, err := AllGaps(ctx, store)
	if err != nil {
		return Gap{}, false, err
	}
	for _, g := range all {
		if g.ID == id {
			return g, true, nil
		}
	}
	return Gap{}, false, nil
}

// DismissGap records that the gap named id is on purpose: it is not shown
// again while it stays as it is.
func DismissGap(ctx context.Context, store db.Store, id string) error {
	return updateGapIDs(ctx, store, model.SettingPermissionGapsDismissed, func(ids map[string]bool) { ids[id] = true })
}

// NoteGapsSaved records what a save on this version did to one list's gaps:
// before and after are that list's gaps either side of the save. A gap the
// save opened is on purpose when the editor showed the administrator its key
// (shown: the keys it showed) and is dismissed there and then; a dismissed gap
// the save closed leaves the dismissed list, so that it is shown again should
// it ever open the other way.
func NoteGapsSaved(ctx context.Context, store db.Store, before, after []Gap, shown []string) error {
	was := map[string]bool{}
	for _, g := range before {
		was[g.ID] = true
	}
	is := map[string]bool{}
	var opened []string
	for _, g := range after {
		is[g.ID] = true
		if !was[g.ID] && containsString(shown, string(g.Key)) {
			opened = append(opened, g.ID)
		}
	}
	var closed []string
	for _, g := range before {
		if !is[g.ID] {
			closed = append(closed, g.ID)
		}
	}
	if len(opened) == 0 && len(closed) == 0 {
		return nil
	}
	return updateGapIDs(ctx, store, model.SettingPermissionGapsDismissed, func(ids map[string]bool) {
		for _, id := range opened {
			ids[id] = true
		}
		for _, id := range closed {
			delete(ids, id)
		}
	})
}

// RestoreBuiltinGap gives a built-in role's stored list the gap's key, and
// returns the list before and after. Everything else in it - a later
// version's keys too - stays as it is.
func RestoreBuiltinGap(ctx context.Context, store db.Store, g Gap) ([]string, []string, error) {
	key, err := builtinSetting(g.Role)
	if err != nil {
		return nil, nil, err
	}
	before, err := storedList(ctx, store, key)
	if err != nil {
		return nil, nil, err
	}
	if before == nil || containsString(before, string(g.Key)) || !containsString(before, string(g.From)) {
		return before, before, nil
	}
	after := append(append([]string(nil), before...), string(g.Key))
	b, err := json.Marshal(after)
	if err != nil {
		return nil, nil, err
	}
	if err := store.UpsertSetting(ctx, key, string(b)); err != nil {
		return nil, nil, err
	}
	Invalidate()
	return before, after, nil
}

// AnnounceGaps returns the open gaps, and those of them administrators have
// not been told of yet, and records that they now have been. It also lets go
// of the dismissals of gaps that are gone. Called at start and after a save
// that can open one (internal/permgap).
func AnnounceGaps(ctx context.Context, store db.Store) (fresh, open []Gap, err error) {
	all, err := AllGaps(ctx, store)
	if err != nil {
		return nil, nil, err
	}
	exists := map[string]bool{}
	for _, g := range all {
		exists[g.ID] = true
	}
	dismissed, err := readGapIDs(ctx, store, model.SettingPermissionGapsDismissed)
	if err != nil {
		return nil, nil, err
	}
	stale := false
	for id := range dismissed {
		if !exists[id] {
			stale = true
		}
	}
	if stale {
		if err := updateGapIDs(ctx, store, model.SettingPermissionGapsDismissed, func(ids map[string]bool) {
			for id := range ids {
				if !exists[id] {
					delete(ids, id)
				}
			}
		}); err != nil {
			return nil, nil, err
		}
	}
	told, err := readGapIDs(ctx, store, model.SettingPermissionGapsNotified)
	if err != nil {
		return nil, nil, err
	}
	now := map[string]bool{}
	for _, g := range all {
		if dismissed[g.ID] {
			continue
		}
		open = append(open, g)
		now[g.ID] = true
		if !told[g.ID] {
			fresh = append(fresh, g)
		}
	}
	if !sameIDs(told, now) {
		if err := writeGapIDs(ctx, store, model.SettingPermissionGapsNotified, now); err != nil {
			return nil, nil, err
		}
	}
	return fresh, open, nil
}

func sameIDs(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}

func readGapIDs(ctx context.Context, store db.Store, key string) (map[string]bool, error) {
	list, err := storedList(ctx, store, key)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(list))
	for _, id := range list {
		out[id] = true
	}
	return out, nil
}

func writeGapIDs(ctx context.Context, store db.Store, key string, ids map[string]bool) error {
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Strings(list)
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return store.UpsertSetting(ctx, key, string(b))
}

func updateGapIDs(ctx context.Context, store db.Store, key string, change func(map[string]bool)) error {
	ids, err := readGapIDs(ctx, store, key)
	if err != nil {
		return err
	}
	change(ids)
	return writeGapIDs(ctx, store, key, ids)
}
