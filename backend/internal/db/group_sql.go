package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ─────────────────── Groups (migration 00072) ───────────────────
//
// ONE implementation for every engine. The statements are written once with
// `?` placeholders and rebound to `$n` for PostgreSQL; the two places the
// dialects really differ — booleans and learning a new row's id — are the
// GroupSQL fields. Both drivers embed a *GroupSQL in their Store, so the
// methods below ARE their Store methods. (MySQL reaches this through the
// SQLite driver, like everything else.)
//
// Nothing here uses an upsert clause: each write that must not duplicate a
// row checks first, inside a transaction where it matters, so there is no
// ON CONFLICT / ON DUPLICATE KEY spelling to keep in step.

// GroupSQL is the group store for one connection.
type GroupSQL struct {
	conn *sql.DB
	// dollar rebinds `?` to `$n` (PostgreSQL).
	dollar bool
}

// NewGroupSQL returns the group store for conn. dollar is true for
// PostgreSQL: `$n` placeholders, BOOLEAN columns and INSERT … RETURNING.
func NewGroupSQL(conn *sql.DB, dollar bool) *GroupSQL {
	return &GroupSQL{conn: conn, dollar: dollar}
}

// q rebinds a `?` statement to this engine's placeholders.
func (g *GroupSQL) q(s string) string {
	if !g.dollar {
		return s
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// b is a boolean as this engine's column takes it.
func (g *GroupSQL) b(v bool) any {
	if g.dollar {
		return v
	}
	if v {
		return 1
	}
	return 0
}

// insert runs an INSERT and returns the new row's id.
func (g *GroupSQL) insert(ctx context.Context, ex execer, stmt string, args ...any) (int64, error) {
	if g.dollar {
		var id int64
		err := ex.QueryRowContext(ctx, g.q(stmt+" RETURNING id"), args...).Scan(&id)
		return id, err
	}
	res, err := ex.ExecContext(ctx, stmt, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// execer is a *sql.DB or *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const groupColumns = `id, name, description, provider_id, role_id, priority, links_json, created_by, created_at, updated_at`

func scanGroup(r RowScanner) (*model.Group, error) {
	g := &model.Group{}
	var provider, role, createdBy sql.NullInt64
	var links string
	if err := r.Scan(&g.ID, &g.Name, &g.Description, &provider, &role, &g.Priority, &links, &createdBy, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return nil, err
	}
	g.ProviderID = nullInt(provider)
	g.RoleID = nullInt(role)
	g.CreatedBy = nullInt(createdBy)
	g.Links = []model.GroupLink{}
	if links != "" {
		if err := json.Unmarshal([]byte(links), &g.Links); err != nil {
			return nil, fmt.Errorf("group %d links: %w", g.ID, err)
		}
	}
	return g, nil
}

func nullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	x := v.Int64
	return &x
}

func encodeGroupLinks(links []model.GroupLink) (string, error) {
	if links == nil {
		links = []model.GroupLink{}
	}
	b, err := json.Marshal(links)
	return string(b), err
}

// ListGroups returns every group, in id order.
func (g *GroupSQL) ListGroups(ctx context.Context) ([]*model.Group, error) {
	rows, err := g.conn.QueryContext(ctx, `SELECT `+groupColumns+` FROM user_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.Group{}
	for rows.Next() {
		gr, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, gr)
	}
	return out, rows.Err()
}

// GetGroup returns one group, or sql.ErrNoRows.
func (g *GroupSQL) GetGroup(ctx context.Context, id int64) (*model.Group, error) {
	return scanGroup(g.conn.QueryRowContext(ctx, g.q(`SELECT `+groupColumns+` FROM user_groups WHERE id=?`), id))
}

// CreateGroup stores a new group and returns it as stored.
func (g *GroupSQL) CreateGroup(ctx context.Context, gr *model.Group) (*model.Group, error) {
	links, err := encodeGroupLinks(gr.Links)
	if err != nil {
		return nil, err
	}
	// tenant_key is provider_id or 0: UNIQUE(tenant_key, name) then keeps
	// names unique among install-wide groups too, where provider_id is NULL
	// and would never collide. A duplicate is the engine's unique error.
	var tenantKey int64
	if gr.ProviderID != nil {
		tenantKey = *gr.ProviderID
	}
	id, err := g.insert(ctx, g.conn,
		`INSERT INTO user_groups (name, description, provider_id, tenant_key, role_id, priority, links_json, created_by) VALUES (?,?,?,?,?,?,?,?)`,
		gr.Name, gr.Description, gr.ProviderID, tenantKey, gr.RoleID, gr.Priority, links, gr.CreatedBy)
	if err != nil {
		return nil, err
	}
	return g.GetGroup(ctx, id)
}

// UpdateGroup rewrites a group's name, description, role, priority and links
// — never its tenant, which is fixed at creation. sql.ErrNoRows for an
// unknown id; a name another group of the tenant has is the engine's unique
// error.
func (g *GroupSQL) UpdateGroup(ctx context.Context, gr *model.Group) error {
	links, err := encodeGroupLinks(gr.Links)
	if err != nil {
		return err
	}
	if _, err := g.conn.ExecContext(ctx,
		g.q(`UPDATE user_groups SET name=?, description=?, role_id=?, priority=?, links_json=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`),
		gr.Name, gr.Description, gr.RoleID, gr.Priority, links, gr.ID); err != nil {
		return err
	}
	// RowsAffected is 0 on MySQL for a row that matched but did not change;
	// ask whether it is there instead.
	_, err = g.GetGroup(ctx, gr.ID)
	return err
}

// DeleteGroup deletes a group; its memberships and grants go with it (ON
// DELETE CASCADE — SQLite runs with foreign_keys on).
func (g *GroupSQL) DeleteGroup(ctx context.Context, id int64) error {
	_, err := g.conn.ExecContext(ctx, g.q(`DELETE FROM user_groups WHERE id=?`), id)
	return err
}

// ReassignGroupRole gives every group holding role `from` the role `to`
// instead (0: no role) — what deleting a role does to its groups.
func (g *GroupSQL) ReassignGroupRole(ctx context.Context, from, to int64) error {
	var next any
	if to != 0 {
		next = to
	}
	_, err := g.conn.ExecContext(ctx, g.q(`UPDATE user_groups SET role_id=?, updated_at=CURRENT_TIMESTAMP WHERE role_id=?`), next, from)
	return err
}

const memberColumns = `group_id, user_id, source, added_at`

func collectMembers(rows *sql.Rows, err error) ([]*model.GroupMember, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.GroupMember{}
	for rows.Next() {
		m := &model.GroupMember{}
		if err := rows.Scan(&m.GroupID, &m.UserID, &m.Source, &m.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListGroupMembers returns one group's members, in user id order.
func (g *GroupSQL) ListGroupMembers(ctx context.Context, groupID int64) ([]*model.GroupMember, error) {
	return collectMembers(g.conn.QueryContext(ctx, g.q(`SELECT `+memberColumns+` FROM user_group_members WHERE group_id=? ORDER BY user_id`), groupID))
}

// ListUserGroupMemberships returns the groups one person is in, in group id
// order.
func (g *GroupSQL) ListUserGroupMemberships(ctx context.Context, userID int64) ([]*model.GroupMember, error) {
	return collectMembers(g.conn.QueryContext(ctx, g.q(`SELECT `+memberColumns+` FROM user_group_members WHERE user_id=? ORDER BY group_id`), userID))
}

// ListAllGroupMembers returns every membership, in (group, user) order — what
// the permission snapshot reads once for the whole install.
func (g *GroupSQL) ListAllGroupMembers(ctx context.Context) ([]*model.GroupMember, error) {
	return collectMembers(g.conn.QueryContext(ctx, `SELECT `+memberColumns+` FROM user_group_members ORDER BY group_id, user_id`))
}

// AddGroupMember puts a person in a group by hand. A membership a link made
// becomes a manual one — the person now stays whatever the directory says.
func (g *GroupSQL) AddGroupMember(ctx context.Context, groupID, userID int64) error {
	tx, err := g.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var src string
	err = tx.QueryRowContext(ctx, g.q(`SELECT source FROM user_group_members WHERE group_id=? AND user_id=?`), groupID, userID).Scan(&src)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, g.q(`INSERT INTO user_group_members (group_id, user_id, source) VALUES (?,?,?)`), groupID, userID, model.GroupSourceManual)
	case err == nil && src != model.GroupSourceManual:
		_, err = tx.ExecContext(ctx, g.q(`UPDATE user_group_members SET source=? WHERE group_id=? AND user_id=?`), model.GroupSourceManual, groupID, userID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveGroupMember takes a person out of a group, whatever put them there.
func (g *GroupSQL) RemoveGroupMember(ctx context.Context, groupID, userID int64) error {
	_, err := g.conn.ExecContext(ctx, g.q(`DELETE FROM user_group_members WHERE group_id=? AND user_id=?`), groupID, userID)
	return err
}

// SetUserLinkedGroups makes groupIDs the groups a person is in through one
// kind of link (source): rows of that source not in groupIDs are removed,
// missing ones added. A membership of any other source — "manual" above
// all — is never touched, and is not duplicated. It reports the group ids
// that were added and removed.
func (g *GroupSQL) SetUserLinkedGroups(ctx context.Context, userID int64, source string, groupIDs []int64) (added, removed []int64, err error) {
	if source == "" || source == model.GroupSourceManual {
		return nil, nil, fmt.Errorf("groups: %q is not a link source", source)
	}
	tx, err := g.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	have, err := collectMembers(tx.QueryContext(ctx, g.q(`SELECT `+memberColumns+` FROM user_group_members WHERE user_id=?`), userID))
	if err != nil {
		return nil, nil, err
	}
	want := map[int64]bool{}
	for _, id := range groupIDs {
		want[id] = true
	}
	in := map[int64]bool{}
	for _, m := range have {
		in[m.GroupID] = true
		if m.Source == source && !want[m.GroupID] {
			if _, err := tx.ExecContext(ctx, g.q(`DELETE FROM user_group_members WHERE group_id=? AND user_id=?`), m.GroupID, userID); err != nil {
				return nil, nil, err
			}
			removed = append(removed, m.GroupID)
		}
	}
	for _, id := range groupIDs {
		if in[id] {
			continue
		}
		in[id] = true
		if _, err := tx.ExecContext(ctx, g.q(`INSERT INTO user_group_members (group_id, user_id, source) VALUES (?,?,?)`), id, userID, source); err != nil {
			return nil, nil, err
		}
		added = append(added, id)
	}
	return added, removed, tx.Commit()
}

// DropForeignMemberships takes a person out of every group of a tenant other
// than providerID — what moving an account to another tenant does. The
// drivers' SetUserProvider calls it, so every path that re-homes an account
// does. Install-wide groups (no tenant) keep them.
func (g *GroupSQL) DropForeignMemberships(ctx context.Context, userID, providerID int64) error {
	_, err := g.conn.ExecContext(ctx, g.q(`DELETE FROM user_group_members WHERE user_id=?
		AND group_id IN (SELECT id FROM user_groups WHERE provider_id IS NOT NULL AND provider_id <> ?)`), userID, providerID)
	return err
}

// ─────────────────── The level a group's role moved ───────────────────

// GetUserGroupLevel returns the built-in level an account had before a
// group's role moved it, and whether one is recorded.
func (g *GroupSQL) GetUserGroupLevel(ctx context.Context, userID int64) (string, bool, error) {
	var level string
	err := g.conn.QueryRowContext(ctx, g.q(`SELECT level_before FROM user_group_levels WHERE user_id=?`), userID).Scan(&level)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return level, err == nil, err
}

// SetUserGroupLevel records the level an account had before a group's role
// moved it. The first record stands: a second move is not "before".
func (g *GroupSQL) SetUserGroupLevel(ctx context.Context, userID int64, level string) error {
	if _, ok, err := g.GetUserGroupLevel(ctx, userID); err != nil || ok {
		return err
	}
	_, err := g.conn.ExecContext(ctx, g.q(`INSERT INTO user_group_levels (user_id, level_before) VALUES (?,?)`), userID, level)
	return err
}

// DeleteUserGroupLevel forgets it — the level is the account's own again.
func (g *GroupSQL) DeleteUserGroupLevel(ctx context.Context, userID int64) error {
	_, err := g.conn.ExecContext(ctx, g.q(`DELETE FROM user_group_levels WHERE user_id=?`), userID)
	return err
}

// ─────────────────── Group folder grants ───────────────────

const groupGrantColumns = `id, storage_id, path_prefix, is_dir, group_id, level, created_by, created_at`

func scanGroupGrant(r RowScanner) (*model.FileGrant, error) {
	gr := &model.FileGrant{}
	var createdBy sql.NullInt64
	if err := r.Scan(&gr.ID, &gr.StorageID, &gr.PathPrefix, &gr.IsDir, &gr.GroupID, &gr.Level, &createdBy, &gr.CreatedAt); err != nil {
		return nil, err
	}
	gr.CreatedBy = nullInt(createdBy)
	return gr, nil
}

func collectGroupGrants(rows *sql.Rows, err error) ([]*model.FileGrant, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.FileGrant
	for rows.Next() {
		gr, err := scanGroupGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, gr)
	}
	return out, rows.Err()
}

// ListGroupFileGrantsByStorage returns every group grant on one storage.
func (g *GroupSQL) ListGroupFileGrantsByStorage(ctx context.Context, storageID int64) ([]*model.FileGrant, error) {
	return collectGroupGrants(g.conn.QueryContext(ctx,
		g.q(`SELECT `+groupGrantColumns+` FROM group_file_grants WHERE storage_id=? ORDER BY path_prefix, group_id`), storageID))
}

// ListGroupFileGrantsByStorageUser returns the grants on one storage of
// every group the person is in — what they reach through their groups.
func (g *GroupSQL) ListGroupFileGrantsByStorageUser(ctx context.Context, storageID, userID int64) ([]*model.FileGrant, error) {
	return collectGroupGrants(g.conn.QueryContext(ctx,
		g.q(`SELECT `+prefixed("g.", groupGrantColumns)+` FROM group_file_grants g
		 JOIN user_group_members m ON m.group_id = g.group_id
		 WHERE g.storage_id=? AND m.user_id=? ORDER BY g.path_prefix, g.group_id`), storageID, userID))
}

// ListGroupFileGrantsByGroup returns one group's grants, on every storage.
func (g *GroupSQL) ListGroupFileGrantsByGroup(ctx context.Context, groupID int64) ([]*model.FileGrant, error) {
	return collectGroupGrants(g.conn.QueryContext(ctx,
		g.q(`SELECT `+groupGrantColumns+` FROM group_file_grants WHERE group_id=? ORDER BY storage_id, path_prefix`), groupID))
}

// ListAllGroupFileGrants returns every group grant.
func (g *GroupSQL) ListAllGroupFileGrants(ctx context.Context) ([]*model.FileGrant, error) {
	return collectGroupGrants(g.conn.QueryContext(ctx,
		`SELECT `+groupGrantColumns+` FROM group_file_grants ORDER BY storage_id, path_prefix, group_id`))
}

// GetGroupFileGrant returns one group grant, or sql.ErrNoRows.
func (g *GroupSQL) GetGroupFileGrant(ctx context.Context, id int64) (*model.FileGrant, error) {
	return scanGroupGrant(g.conn.QueryRowContext(ctx, g.q(`SELECT `+groupGrantColumns+` FROM group_file_grants WHERE id=?`), id))
}

// CreateGroupFileGrant upserts a group's grant on the (storage_id,
// path_prefix, group_id) key, like CreateFileGrant does a person's.
func (g *GroupSQL) CreateGroupFileGrant(ctx context.Context, gr *model.FileGrant) (*model.FileGrant, error) {
	tx, err := g.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, g.q(`SELECT id FROM group_file_grants WHERE storage_id=? AND path_prefix=? AND group_id=?`),
		gr.StorageID, gr.PathPrefix, gr.GroupID).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id, err = g.insert(ctx, tx,
			`INSERT INTO group_file_grants (storage_id, path_prefix, is_dir, group_id, level, created_by) VALUES (?,?,?,?,?,?)`,
			gr.StorageID, gr.PathPrefix, g.b(gr.IsDir), gr.GroupID, gr.Level, gr.CreatedBy)
	case err == nil:
		_, err = tx.ExecContext(ctx, g.q(`UPDATE group_file_grants SET level=?, is_dir=?, created_by=? WHERE id=?`),
			gr.Level, g.b(gr.IsDir), gr.CreatedBy, id)
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return g.GetGroupFileGrant(ctx, id)
}

// UpdateGroupFileGrantLevel changes a group grant's level.
func (g *GroupSQL) UpdateGroupFileGrantLevel(ctx context.Context, id int64, level string) error {
	_, err := g.conn.ExecContext(ctx, g.q(`UPDATE group_file_grants SET level=? WHERE id=?`), level, id)
	return err
}

// DeleteGroupFileGrant revokes a group grant.
func (g *GroupSQL) DeleteGroupFileGrant(ctx context.Context, id int64) error {
	_, err := g.conn.ExecContext(ctx, g.q(`DELETE FROM group_file_grants WHERE id=?`), id)
	return err
}

// prefixed qualifies each column of a select list with p.
func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}
