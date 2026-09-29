package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ─────────────────── Per-user permissions: the dialect-free half ──────────
//
// The same split as customtheme_scan.go, for the same reason. The drivers
// (drivers/sqlite, which MySQL shares, and drivers/postgres) spell each
// statement in their own dialect; everything that has no dialect in it —
// scanning a rule row, collecting a result set, the delete-then-insert of a
// group list — lives here, once, and both call it.

// RowScanner (catalogue_scan.go) is a *sql.Row or *sql.Rows.

// PermRuleColumns is the select list both drivers use for permission_rules,
// in the order ScanPermRule reads them. ⚠ The order is a contract with the
// function below — change them together.
const PermRuleColumns = `id, name, description, enabled, permissions_json, provider_id, targets_json, effects_json, settings_json, conditions_json, created_by, created_at, updated_at, names_json, descriptions_json`

// ScanPermRule reads one permission_rules row selected with PermRuleColumns.
func ScanPermRule(r RowScanner) (*model.PermissionRule, error) {
	rule := &model.PermissionRule{}
	var provider, createdBy sql.NullInt64
	var permissions sql.NullString
	var targets, effects, settings, conditions, names, descriptions string
	if err := r.Scan(&rule.ID, &rule.Name, &rule.Description, &rule.Enabled, &permissions, &provider,
		&targets, &effects, &settings, &conditions, &createdBy, &rule.CreatedAt, &rule.UpdatedAt,
		&names, &descriptions); err != nil {
		return nil, err
	}
	if provider.Valid {
		v := provider.Int64
		rule.ProviderID = &v
	}
	if createdBy.Valid {
		v := createdBy.Int64
		rule.CreatedBy = &v
	}
	if err := DecodePermRule(rule, targets, effects, settings); err != nil {
		return nil, err
	}
	c, err := DecodePermConditions(conditions)
	if err != nil {
		return nil, err
	}
	rule.Conditions = c
	if rule.Permissions, err = DecodePermList(permissions); err != nil {
		return nil, fmt.Errorf("permission rule %d permissions: %w", rule.ID, err)
	}
	if rule.Names, err = DecodePermTexts(names); err != nil {
		return nil, fmt.Errorf("permission rule %d names: %w", rule.ID, err)
	}
	if rule.Descriptions, err = DecodePermTexts(descriptions); err != nil {
		return nil, fmt.Errorf("permission rule %d descriptions: %w", rule.ID, err)
	}
	return rule, nil
}

// CollectPermRules scans every row of a PermRuleColumns query and closes it.
func CollectPermRules(rows *sql.Rows, err error) ([]*model.PermissionRule, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.PermissionRule
	for rows.Next() {
		r, err := ScanPermRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CollectPermOverrides scans (user_id, overrides_json) rows into a map and
// closes them.
func CollectPermOverrides(rows *sql.Rows, err error) (map[int64]map[string]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]string{}
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		m, err := DecodePermOverrides(raw)
		if err != nil {
			return nil, err
		}
		out[id] = m
	}
	return out, rows.Err()
}

// CollectStrings scans a one-string-column result set and closes it; an
// empty set is an empty (not nil) slice.
func CollectStrings(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ReplaceUserSSOGroups deletes a user's groups and inserts the given ones in
// one transaction — blanks and duplicates dropped. del takes (user_id), ins
// takes (user_id, group_name), each in the caller's dialect. A context that
// already carries a transaction on pool runs in that one (RunInTx).
func ReplaceUserSSOGroups(ctx context.Context, pool *sql.DB, del, ins string, userID int64, groups []string) error {
	return RunInTx(ctx, pool, func(ctx context.Context) error {
		q := Conn(ctx, pool)
		if _, err := q.ExecContext(ctx, del, userID); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, g := range groups {
			if g == "" || seen[g] {
				continue
			}
			seen[g] = true
			if _, err := q.ExecContext(ctx, ins, userID, g); err != nil {
				return err
			}
		}
		return nil
	})
}

// OverridesOrEmpty turns the GetUserPermissionOverrides scan result into the
// Store contract: no row is an empty map, not an error.
func OverridesOrEmpty(raw string, err error) (map[string]string, error) {
	if err == sql.ErrNoRows {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return DecodePermOverrides(raw)
}

// CollectUserCustomRoles scans (user_id, role_id) rows into a map and closes
// them.
func CollectUserCustomRoles(rows *sql.Rows, err error) (map[int64]int64, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var uid, rid int64
		if err := rows.Scan(&uid, &rid); err != nil {
			return nil, err
		}
		out[uid] = rid
	}
	return out, rows.Err()
}

// CustomRoleOrNone turns a single role_id lookup into 0 for "no row".
func CustomRoleOrNone(id int64, err error) (int64, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// EncodePermList renders a role's permission list; nil is an empty one.
func EncodePermList(list []string) (any, error) {
	if list == nil {
		list = []string{}
	}
	b, err := json.Marshal(list)
	return string(b), err
}

// DecodePermList parses a permissions_json column into a non-nil list
// (possibly empty); NULL is empty.
func DecodePermList(v sql.NullString) ([]string, error) {
	out := []string{}
	if !v.Valid || v.String == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(v.String), &out); err != nil {
		return nil, err
	}
	return out, nil
}
