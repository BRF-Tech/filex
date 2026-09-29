package postgres

import (
	"context"
	"database/sql"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ─────────────────── Per-user permissions (migration 00069) ───────────────────
//
// The PostgreSQL twin of drivers/sqlite/permissions.go: the same statements
// in this dialect; scanning and collecting are shared in
// internal/db/permission_scan.go.

func (s *Store) GetUserPermissionOverrides(ctx context.Context, userID int64) (map[string]string, error) {
	var raw string
	err := s.conn(ctx).QueryRowContext(ctx, `SELECT overrides_json FROM user_permissions WHERE user_id=$1`, userID).Scan(&raw)
	return db.OverridesOrEmpty(raw, err)
}

func (s *Store) SetUserPermissionOverrides(ctx context.Context, userID int64, overrides map[string]string, updatedBy *int64) error {
	if len(overrides) == 0 {
		_, err := s.conn(ctx).ExecContext(ctx, `DELETE FROM user_permissions WHERE user_id=$1`, userID)
		return err
	}
	raw, err := db.EncodePermOverrides(overrides)
	if err != nil {
		return err
	}
	_, err = s.conn(ctx).ExecContext(ctx,
		`INSERT INTO user_permissions (user_id, overrides_json, updated_by, updated_at) VALUES ($1,$2,$3,NOW())
		 ON CONFLICT(user_id) DO UPDATE SET overrides_json=excluded.overrides_json, updated_by=excluded.updated_by, updated_at=NOW()`,
		userID, raw, updatedBy)
	return err
}

func (s *Store) ListUserPermissionOverrides(ctx context.Context) (map[int64]map[string]string, error) {
	return db.CollectPermOverrides(s.conn(ctx).QueryContext(ctx, `SELECT user_id, overrides_json FROM user_permissions ORDER BY user_id`))
}

func (s *Store) ListPermissionRules(ctx context.Context) ([]*model.PermissionRule, error) {
	return db.CollectPermRules(s.conn(ctx).QueryContext(ctx, `SELECT `+db.PermRuleColumns+` FROM permission_rules ORDER BY id`))
}

func (s *Store) GetPermissionRule(ctx context.Context, id int64) (*model.PermissionRule, error) {
	return db.ScanPermRule(s.conn(ctx).QueryRowContext(ctx, `SELECT `+db.PermRuleColumns+` FROM permission_rules WHERE id=$1`, id))
}

func (s *Store) CreatePermissionRule(ctx context.Context, r *model.PermissionRule) (*model.PermissionRule, error) {
	targets, effects, settings, conditions, err := db.EncodePermRuleAll(r)
	if err != nil {
		return nil, err
	}
	list, err := db.EncodePermList(r.Permissions)
	if err != nil {
		return nil, err
	}
	names, descriptions, err := db.EncodePermRuleTexts(r)
	if err != nil {
		return nil, err
	}
	return db.ScanPermRule(s.conn(ctx).QueryRowContext(ctx,
		`INSERT INTO permission_rules (name, description, enabled, permissions_json, provider_id, targets_json, effects_json, settings_json, conditions_json, created_by, names_json, descriptions_json)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING `+db.PermRuleColumns,
		r.Name, r.Description, r.Enabled, list, r.ProviderID, targets, effects, settings, conditions, r.CreatedBy, names, descriptions))
}

// UpdatePermissionRule rewrites everything but id, created_by and created_at.
func (s *Store) UpdatePermissionRule(ctx context.Context, r *model.PermissionRule) error {
	targets, effects, settings, conditions, err := db.EncodePermRuleAll(r)
	if err != nil {
		return err
	}
	list, err := db.EncodePermList(r.Permissions)
	if err != nil {
		return err
	}
	names, descriptions, err := db.EncodePermRuleTexts(r)
	if err != nil {
		return err
	}
	res, err := s.conn(ctx).ExecContext(ctx,
		`UPDATE permission_rules SET name=$1, description=$2, enabled=$3, permissions_json=$4, provider_id=$5, targets_json=$6, effects_json=$7, settings_json=$8, conditions_json=$9, names_json=$10, descriptions_json=$11, updated_at=NOW()
		 WHERE id=$12`,
		r.Name, r.Description, r.Enabled, list, r.ProviderID, targets, effects, settings, conditions, names, descriptions, r.ID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeletePermissionRule(ctx context.Context, id int64) error {
	_, err := s.conn(ctx).ExecContext(ctx, `DELETE FROM permission_rules WHERE id=$1`, id)
	return err
}

func (s *Store) ListUserSSOGroups(ctx context.Context, userID int64) ([]string, error) {
	return db.CollectStrings(s.conn(ctx).QueryContext(ctx, `SELECT group_name FROM user_sso_groups WHERE user_id=$1 ORDER BY group_name`, userID))
}

func (s *Store) SetUserSSOGroups(ctx context.Context, userID int64, groups []string) error {
	return db.ReplaceUserSSOGroups(ctx, s.db,
		`DELETE FROM user_sso_groups WHERE user_id=$1`,
		`INSERT INTO user_sso_groups (user_id, group_name) VALUES ($1,$2)`,
		userID, groups)
}

func (s *Store) GetUserCustomRole(ctx context.Context, userID int64) (int64, error) {
	var id int64
	err := s.conn(ctx).QueryRowContext(ctx, `SELECT role_id FROM user_custom_roles WHERE user_id=$1`, userID).Scan(&id)
	return db.CustomRoleOrNone(id, err)
}

func (s *Store) SetUserCustomRole(ctx context.Context, userID, roleID int64) error {
	if roleID == 0 {
		_, err := s.conn(ctx).ExecContext(ctx, `DELETE FROM user_custom_roles WHERE user_id=$1`, userID)
		return err
	}
	_, err := s.conn(ctx).ExecContext(ctx,
		`INSERT INTO user_custom_roles (user_id, role_id) VALUES ($1,$2)
		 ON CONFLICT (user_id) DO UPDATE SET role_id=EXCLUDED.role_id`,
		userID, roleID)
	return err
}

func (s *Store) ListUserCustomRoles(ctx context.Context) (map[int64]int64, error) {
	return db.CollectUserCustomRoles(s.conn(ctx).QueryContext(ctx, `SELECT user_id, role_id FROM user_custom_roles`))
}
