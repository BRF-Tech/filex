package perm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Keys a later version wrote.
//
// A saved list - a built-in role's, a custom role's own list and its folder
// part, a person's own exceptions - may hold a permission this version does
// not know: one a later version added and stored before the server was taken
// back to this one. This version cannot show it on a page, so it cannot
// decide it either. Every save keeps it as it was stored, whatever the request
// says, and a request may send it back: it is not a new unknown key. A key
// that was not stored is still refused (400, unknown permission).
//
// ⚠ 0.50 did not keep them, and that is how files.encrypt was lost on the way
// back from 0.51.0 (PR #86, Berk Başarır): the built-in User role's page read
// the list without the key it did not know and wrote it back without it, and
// the role editor kept only the folder permissions it knew. 0.51.0 had merged
// the key once (UpgradeCatalogue) and does not merge it again. From the
// release after 0.51.0 on, a later version's permissions are given back to it
// intact.

// Foreign reports whether k is a permission this version does not know: not
// in the catalogue and not an app permission (perm/app.go, which has rules of
// its own).
func Foreign(k string) bool {
	if IsAppKey(k) {
		return false
	}
	_, ok := Lookup(Perm(k))
	return !ok
}

// ForeignKeys returns the keys of list this version does not know, in their
// order, each once.
func ForeignKeys(list []string) []string {
	var out []string
	for _, k := range list {
		if Foreign(k) && !containsString(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// ForeignEffects returns the entries of an Allow / Deny map whose key this
// version does not know; nil when there are none.
func ForeignEffects(m map[string]string) map[string]string {
	var out map[string]string
	for k, v := range m {
		if !Foreign(k) {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	return out
}

// WithoutForeign returns m without the keys this version does not know: what
// a request leaves when the later version's entries, which it may only send
// back, are set aside to be put back as they were stored (KeepForeignEffects).
func WithoutForeign(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if !Foreign(k) {
			out[k] = v
		}
	}
	return out
}

// KeepForeignEffects puts the entries of stored this version does not know
// into m, as they were stored, and returns m.
func KeepForeignEffects(m, stored map[string]string) map[string]string {
	foreign := ForeignEffects(stored)
	if len(foreign) == 0 {
		return m
	}
	if m == nil {
		m = map[string]string{}
	}
	for k, v := range foreign {
		m[k] = v
	}
	return m
}

// ValidateEffectsEdit is ValidateEffects for a new version of stored (a
// person's exceptions, a role's folder part): a key this version does not
// know passes when stored holds it already. Its effect is not looked at: the
// stored one is kept (KeepForeignEffects).
func ValidateEffectsEdit(m, stored map[string]string) error {
	return validateEffects(m, func(k string) bool {
		_, held := stored[k]
		return held
	})
}

// builtinSetting is the settings key of a built-in role's saved list.
func builtinSetting(role string) (string, error) {
	switch role {
	case model.RoleUser:
		return model.SettingPermissionDefaults, nil
	case model.RoleViewer:
		return model.SettingPermissionViewerDefaults, nil
	default:
		return "", invalid("role %q has no editable permissions", role)
	}
}

// storedList reads a saved list as it is stored, every key in it; nil when it
// was never saved.
func storedList(ctx context.Context, store db.Store, key string) ([]string, error) {
	raw, err := store.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && raw == "") {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return keys, nil
}

// StoredForeign returns the keys a built-in role's saved list holds that this
// version does not know: what its page cannot show and its save keeps.
func StoredForeign(ctx context.Context, store db.Store, role string) ([]string, error) {
	key, err := builtinSetting(role)
	if err != nil {
		return nil, err
	}
	list, err := storedList(ctx, store, key)
	if err != nil {
		return nil, err
	}
	return ForeignKeys(list), nil
}

// saveBuiltinList stores s as a built-in role's list, followed by the keys
// the stored list holds that this version does not know.
func saveBuiltinList(ctx context.Context, store db.Store, role string, s Set) error {
	key, err := builtinSetting(role)
	if err != nil {
		return err
	}
	stored, err := storedList(ctx, store, key)
	if err != nil {
		return err
	}
	b, err := json.Marshal(append(s.Strings(), ForeignKeys(stored)...))
	if err != nil {
		return err
	}
	if err := store.UpsertSetting(ctx, key, string(b)); err != nil {
		return err
	}
	Invalidate()
	return nil
}
