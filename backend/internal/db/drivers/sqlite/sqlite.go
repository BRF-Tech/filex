// Package sqlite is the SQLite DB driver. Uses modernc.org/sqlite
// (pure Go, CGO_ENABLED=0).
package sqlite

import (
	"context"
	"crypto/md5"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"

	sqlite_migrations "github.com/brf-tech/filex/backend/db/migrations/sqlite"
)

func init() {
	db.Register("sqlite", func() db.Driver { return &Driver{} })
}

// Driver implements db.Driver.
type Driver struct{}

// Name implements db.Driver.
func (Driver) Name() string { return "sqlite" }

// Dialect returns the goose-compatible dialect name.
func (Driver) Dialect() string { return "sqlite3" }

// MigrationsFS returns the embedded SQLite migrations.
func (Driver) MigrationsFS() embed.FS { return sqlite_migrations.FS }

// Open returns a configured *sql.DB.
//
// Default DSN tweaks: WAL mode, busy timeout 5s, foreign keys on.
func (Driver) Open(_ context.Context, dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, errors.New("sqlite: empty DSN")
	}
	if !strings.Contains(dsn, "_pragma") {
		joiner := "?"
		if strings.Contains(dsn, "?") {
			joiner = "&"
		}
		dsn += joiner + "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	}
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	conn.SetMaxOpenConns(1) // SQLite serializes writes — one writer.
	return conn, nil
}

// NewStore returns a Store backed by the given *sql.DB.
func (Driver) NewStore(sqlDB *sql.DB) db.Store {
	return &Store{db: sqlDB}
}

// Store implements db.Store atop SQLite.
type Store struct {
	db *sql.DB
}

// Ping implements db.Store.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close implements db.Store.
func (s *Store) Close() error { return s.db.Close() }

// ─────────────────── Storages ───────────────────

func (s *Store) CreateStorage(ctx context.Context, st *model.Storage) (*model.Storage, error) {
	cfg := st.ConfigJSON
	if len(cfg) == 0 {
		cfg = []byte("{}")
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO storages (name, driver, mount_path, config_json, sync_mode, sync_interval_s, enabled, read_only, rbac_enabled)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		st.Name, st.Driver, st.MountPath, string(cfg), st.SyncMode, st.SyncIntervalS, btoi(st.Enabled), btoi(st.ReadOnly), btoi(st.RBACEnabled))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetStorage(ctx, id)
}

func (s *Store) GetStorage(ctx context.Context, id int64) (*model.Storage, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, driver, mount_path, config_json, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at, COALESCE(role,'primary'), replica_of_id, COALESCE(replica_mode,'async'), replica_target_id, rbac_enabled FROM storages WHERE id=?`, id)
	return scanStorage(row)
}

func (s *Store) GetStorageByName(ctx context.Context, name string) (*model.Storage, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, driver, mount_path, config_json, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at, COALESCE(role,'primary'), replica_of_id, COALESCE(replica_mode,'async'), replica_target_id, rbac_enabled FROM storages WHERE name=?`, name)
	return scanStorage(row)
}

func (s *Store) ListStorages(ctx context.Context) ([]*model.Storage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, driver, mount_path, config_json, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at, COALESCE(role,'primary'), replica_of_id, COALESCE(replica_mode,'async'), replica_target_id, rbac_enabled FROM storages ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Storage
	for rows.Next() {
		st, err := scanStorage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) ListEnabledStorages(ctx context.Context) ([]*model.Storage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, driver, mount_path, config_json, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at, COALESCE(role,'primary'), replica_of_id, COALESCE(replica_mode,'async'), replica_target_id, rbac_enabled FROM storages WHERE enabled=1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Storage
	for rows.Next() {
		st, err := scanStorage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) UpdateStorage(ctx context.Context, st *model.Storage) error {
	cfg := st.ConfigJSON
	if len(cfg) == 0 {
		cfg = []byte("{}")
	}
	role := st.Role
	if role == "" {
		role = "primary"
	}
	repMode := st.ReplicaMode
	if repMode == "" {
		repMode = "async"
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE storages SET name=?, driver=?, mount_path=?, config_json=?, sync_mode=?, sync_interval_s=?, enabled=?, read_only=?, rbac_enabled=?, role=?, replica_of_id=?, replica_mode=?, replica_target_id=? WHERE id=?`,
		st.Name, st.Driver, st.MountPath, string(cfg), st.SyncMode, st.SyncIntervalS,
		btoi(st.Enabled), btoi(st.ReadOnly), btoi(st.RBACEnabled),
		role, st.ReplicaOfID, repMode, st.ReplicaTargetID,
		st.ID)
	return err
}

func (s *Store) UpdateStorageSyncCursor(ctx context.Context, id int64, at time.Time, token string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE storages SET last_sync_at=?, last_sync_token=? WHERE id=?`, at, token, id)
	return err
}

// ──────── ReplicationTarget (v0.1.18+) ────────

func scanReplicationTarget(r rowScanner) (*model.ReplicationTarget, error) {
	rt := &model.ReplicationTarget{}
	var cfg string
	if err := r.Scan(&rt.ID, &rt.Name, &rt.Driver, &cfg, &rt.Mode, &rt.Enabled, &rt.CreatedAt, &rt.UpdatedAt); err != nil {
		return nil, err
	}
	rt.ConfigJSON = []byte(cfg)
	return rt, nil
}

func (s *Store) ListReplicationTargets(ctx context.Context) ([]*model.ReplicationTarget, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, driver, config_json, mode, enabled, created_at, updated_at
		   FROM replication_targets ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.ReplicationTarget
	for rows.Next() {
		rt, err := scanReplicationTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, rows.Err()
}

func (s *Store) GetReplicationTarget(ctx context.Context, id int64) (*model.ReplicationTarget, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, driver, config_json, mode, enabled, created_at, updated_at
		   FROM replication_targets WHERE id=?`, id)
	return scanReplicationTarget(row)
}

func (s *Store) CreateReplicationTarget(ctx context.Context, rt *model.ReplicationTarget) (*model.ReplicationTarget, error) {
	cfg := rt.ConfigJSON
	if len(cfg) == 0 {
		cfg = []byte("{}")
	}
	mode := rt.Mode
	if mode == "" {
		mode = "async"
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO replication_targets (name, driver, config_json, mode, enabled)
		 VALUES (?, ?, ?, ?, ?)`,
		rt.Name, rt.Driver, string(cfg), mode, btoi(rt.Enabled),
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetReplicationTarget(ctx, id)
}

func (s *Store) UpdateReplicationTarget(ctx context.Context, rt *model.ReplicationTarget) error {
	cfg := rt.ConfigJSON
	if len(cfg) == 0 {
		cfg = []byte("{}")
	}
	mode := rt.Mode
	if mode == "" {
		mode = "async"
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE replication_targets
		    SET name=?, driver=?, config_json=?, mode=?, enabled=?, updated_at=CURRENT_TIMESTAMP
		  WHERE id=?`,
		rt.Name, rt.Driver, string(cfg), mode, btoi(rt.Enabled), rt.ID)
	return err
}

func (s *Store) DeleteReplicationTarget(ctx context.Context, id int64) error {
	// Clear FK on any primary that was pointing here so the orphan
	// reference doesn't 404 on the UI later.
	if _, err := s.db.ExecContext(ctx, `UPDATE storages SET replica_target_id=NULL WHERE replica_target_id=?`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM replication_targets WHERE id=?`, id)
	return err
}

func (s *Store) DeleteStorage(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM storages WHERE id=?`, id)
	return err
}

// ─────────────────── Nodes ───────────────────

func (s *Store) CreateNode(ctx context.Context, n *model.Node) (*model.Node, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO nodes (storage_id, parent_id, name, path, path_hash, storage_key, type, size, mime, etag, backend_mtime, sync_state)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.StorageID, n.ParentID, n.Name, n.Path, n.PathHash, n.StorageKey, n.Type, n.Size, n.Mime, n.Etag, n.BackendMtime, n.SyncState)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetNode(ctx, id)
}

func (s *Store) GetNode(ctx context.Context, id int64) (*model.Node, error) {
	row := s.db.QueryRowContext(ctx, nodeSelectColumns()+` FROM nodes WHERE id=?`, id)
	return scanNode(row)
}

func (s *Store) GetNodeByPath(ctx context.Context, storageID int64, hash string) (*model.Node, error) {
	row := s.db.QueryRowContext(ctx, nodeSelectColumns()+` FROM nodes WHERE storage_id=? AND path_hash=? AND deleted_at IS NULL`, storageID, hash)
	return scanNode(row)
}

func (s *Store) GetNodeByPathIncludingDeleted(ctx context.Context, storageID int64, hash string) (*model.Node, error) {
	row := s.db.QueryRowContext(ctx, nodeSelectColumns()+` FROM nodes WHERE storage_id=? AND path_hash=?`, storageID, hash)
	return scanNode(row)
}

func (s *Store) ListNodesByParent(ctx context.Context, storageID int64, parentID *int64) ([]*model.Node, error) {
	q := nodeSelectColumns() + ` FROM nodes WHERE storage_id=? AND deleted_at IS NULL AND parent_id `
	args := []any{storageID}
	if parentID == nil {
		q += `IS NULL`
	} else {
		q += `=?`
		args = append(args, *parentID)
	}
	q += ` ORDER BY type DESC, name`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) AggNodes(ctx context.Context, storageID int64) ([]db.NodeAgg, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, parent_id, type, size FROM nodes WHERE storage_id=? AND deleted_at IS NULL`,
		storageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.NodeAgg
	for rows.Next() {
		var n db.NodeAgg
		var typ string
		if err := rows.Scan(&n.ID, &n.ParentID, &typ, &n.Size); err != nil {
			return nil, err
		}
		n.IsDir = typ == string(model.NodeTypeDirectory)
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) SetNodeSize(ctx context.Context, id int64, size int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET size=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, size, id)
	return err
}

func (s *Store) UpdateNodeMeta(ctx context.Context, id int64, size int64, mime, etag string, mtime time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET size=?, mime=?, etag=?, backend_mtime=?, seen_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		size, mime, etag, mtime, id)
	return err
}

func (s *Store) TouchNodeSeen(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET seen_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

// SoftDeleteAndRetag is the trash-aware soft-delete: it sets deleted_at,
// rewrites path/path_hash to the supplied trash key, and stashes the
// original path in storage_key for later Restore. The parent_id is
// nulled so listings of the original parent forget the row.
func (s *Store) SoftDeleteAndRetag(ctx context.Context, id int64, trashPath, trashHash, origPath string) error {
	base := path.Base(trashPath)
	_, err := s.db.ExecContext(ctx, `
		UPDATE nodes
		SET deleted_at=CURRENT_TIMESTAMP,
		    updated_at=CURRENT_TIMESTAMP,
		    parent_id=NULL,
		    name=?, path=?, path_hash=?, storage_key=?
		WHERE id=?`, base, trashPath, trashHash, origPath, id)
	return err
}

func (s *Store) SoftDeleteNode(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

func (s *Store) HardDeleteNode(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id=?`, id)
	return err
}

func (s *Store) MoveNode(ctx context.Context, id int64, parentID *int64, name, path, hash string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET parent_id=?, name=?, path=?, path_hash=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		parentID, name, path, hash, id)
	return err
}

func (s *Store) ListStaleNodes(ctx context.Context, storageID int64, before time.Time) ([]*model.Node, error) {
	// SQLite stores `CURRENT_TIMESTAMP` as `YYYY-MM-DD HH:MM:SS` (space
	// separator, second precision, no timezone). Go's time.Time bound
	// via `?` is formatted as RFC3339 (`YYYY-MM-DDTHH:MM:SSZ`). That
	// makes string comparison return seen_at < before for ANY same-
	// second touch — `' '` (0x20) < `T` (0x54) — and the tombstone
	// pass nukes rows the walk just touched. Format `before` to match
	// CURRENT_TIMESTAMP's wire format so the comparison is honest.
	beforeStr := before.UTC().Format("2006-01-02 15:04:05")
	rows, err := s.db.QueryContext(ctx, nodeSelectColumns()+` FROM nodes WHERE storage_id=? AND seen_at < ? AND deleted_at IS NULL`, storageID, beforeStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) CountNodesByStorage(ctx context.Context, storageID int64) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE storage_id=? AND deleted_at IS NULL`, storageID).Scan(&n)
	return n, err
}

func (s *Store) StorageStats(ctx context.Context, storageID int64) (int64, int64, error) {
	var (
		count int64
		size  sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM nodes
		   WHERE storage_id=? AND type='file' AND deleted_at IS NULL`,
		storageID,
	).Scan(&count, &size)
	if err != nil {
		return 0, 0, err
	}
	return count, size.Int64, nil
}

func (s *Store) SearchNodes(ctx context.Context, storageID int64, like string, limit int) ([]*model.Node, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, nodeSelectColumns()+` FROM nodes WHERE storage_id=? AND name LIKE ? AND deleted_at IS NULL ORDER BY name LIMIT ?`,
		storageID, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ─────────────────── Users ───────────────────

func (s *Store) CreateUser(ctx context.Context, email, passwordHash, role, locale, tz string) (*model.User, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (email, password_hash, role, locale, timezone) VALUES (?,?,?,?,?)`,
		email, passwordHash, role, locale, tz)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetUser(ctx, id)
}

func (s *Store) GetUser(ctx context.Context, id int64) (*model.User, error) {
	row := s.db.QueryRowContext(ctx, userSelect()+` FROM users WHERE id=?`, id)
	return scanUser(row)
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	row := s.db.QueryRowContext(ctx, userSelect()+` FROM users WHERE email=?`, email)
	return scanUser(row)
}

func (s *Store) ListUsers(ctx context.Context) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx, userSelect()+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) UpdateUserPassword(ctx context.Context, id int64, hash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, hash, id)
	return err
}

func (s *Store) UpdateUserEmail(ctx context.Context, id int64, email string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET email=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, email, id)
	return err
}

func (s *Store) UpdateUserDisplayName(ctx context.Context, id int64, displayName string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET display_name=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, displayName, id)
	return err
}

// SetTotpPendingSecret stores a freshly-enrolled TOTP secret + recovery
// codes prior to the user verifying with a one-time code.
func (s *Store) SetTotpPendingSecret(ctx context.Context, id int64, secret string, recoveryCodes []string) error {
	codes, _ := json.Marshal(recoveryCodes)
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_pending_secret=?, totp_recovery_codes_json=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		secret, string(codes), id)
	return err
}

// ActivateTotp moves the pending secret into totp_secret and flips the
// totp_enabled flag on.
func (s *Store) ActivateTotp(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_secret=COALESCE(totp_pending_secret,''), totp_pending_secret=NULL, totp_enabled=1, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		id)
	return err
}

// ClearTotp wipes all 2FA state.
func (s *Store) ClearTotp(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_secret=NULL, totp_pending_secret=NULL, totp_enabled=0, totp_recovery_codes_json='[]', updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		id)
	return err
}

func (s *Store) UpdateUserLocale(ctx context.Context, id int64, locale, tz string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET locale=?, timezone=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, locale, tz, id)
	return err
}

func (s *Store) UpdateUserRole(ctx context.Context, id int64, role string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET role=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, role, id)
	return err
}

func (s *Store) TouchLastLogin(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	return err
}

// ─────────────────── Sessions ───────────────────

func (s *Store) CreateSession(ctx context.Context, userID int64, token string, expiresAt time.Time, ip, ua string) (*model.Session, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (user_id, token, expires_at, ip, user_agent) VALUES (?,?,?,?,?)`,
		userID, token, expiresAt, ip, ua)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &model.Session{ID: id, UserID: userID, Token: token, ExpiresAt: expiresAt, IP: ip, UserAgent: ua, CreatedAt: time.Now()}, nil
}

func (s *Store) GetSessionByToken(ctx context.Context, token string) (*model.Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, token, expires_at, COALESCE(ip,''), COALESCE(user_agent,''), created_at FROM sessions WHERE token=? AND expires_at > CURRENT_TIMESTAMP`,
		token)
	out := &model.Session{}
	if err := row.Scan(&out.ID, &out.UserID, &out.Token, &out.ExpiresAt, &out.IP, &out.UserAgent, &out.CreatedAt); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token=?`, token)
	return err
}

// DeleteSessionsForUser removes every session for the user except the
// supplied "current" token (so the caller stays signed in after a
// password change).
func (s *Store) DeleteSessionsForUser(ctx context.Context, userID int64, exceptToken string) error {
	if exceptToken == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND token<>?`, userID, exceptToken)
	return err
}

// CountActiveSessions returns the count of unexpired sessions.
func (s *Store) CountActiveSessions(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE expires_at > CURRENT_TIMESTAMP`).Scan(&n)
	return n, err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= CURRENT_TIMESTAMP`)
	return err
}

// ─────────────────── API tokens ───────────────────

func (s *Store) CreateAPIToken(ctx context.Context, t *model.APIToken) (*model.APIToken, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO api_tokens (user_id, label, token_hash, scopes, expires_at) VALUES (?,?,?,?,?)`,
		t.UserID, t.Label, t.TokenHash, t.Scopes, t.ExpiresAt)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	t.ID = id
	t.CreatedAt = time.Now()
	return t, nil
}

func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash string) (*model.APIToken, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, label, token_hash, scopes, last_used_at, expires_at, created_at FROM api_tokens WHERE token_hash=?`,
		tokenHash)
	return scanAPIToken(row)
}

func (s *Store) ListAPITokens(ctx context.Context) ([]*model.APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, label, token_hash, scopes, last_used_at, expires_at, created_at FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ListAPITokensByUser(ctx context.Context, userID int64) ([]*model.APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, label, token_hash, scopes, last_used_at, expires_at, created_at FROM api_tokens WHERE user_id=? ORDER BY created_at DESC`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) TouchAPIToken(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

func (s *Store) DeleteAPIToken(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id=?`, id)
	return err
}

// scanAPIToken reads one api_tokens row. Accepts both *sql.Row and *sql.Rows
// via the rowScanner interface.
func scanAPIToken(row rowScanner) (*model.APIToken, error) {
	t := &model.APIToken{}
	var lastUsed, expires sql.NullTime
	if err := row.Scan(&t.ID, &t.UserID, &t.Label, &t.TokenHash, &t.Scopes, &lastUsed, &expires, &t.CreatedAt); err != nil {
		return nil, err
	}
	if lastUsed.Valid {
		t.LastUsedAt = &lastUsed.Time
	}
	if expires.Valid {
		t.ExpiresAt = &expires.Time
	}
	return t, nil
}

// ─────────────────── File grants (RBAC/ACL, migration 00012) ───────────────────

const fileGrantCols = `id, storage_id, path_prefix, is_dir, user_id, level, created_by, created_at`

func scanFileGrant(r rowScanner) (*model.FileGrant, error) {
	g := &model.FileGrant{}
	var createdBy sql.NullInt64
	if err := r.Scan(&g.ID, &g.StorageID, &g.PathPrefix, &g.IsDir, &g.UserID, &g.Level, &createdBy, &g.CreatedAt); err != nil {
		return nil, err
	}
	if createdBy.Valid {
		v := createdBy.Int64
		g.CreatedBy = &v
	}
	return g, nil
}

func (s *Store) ListFileGrantsByStorageUser(ctx context.Context, storageID, userID int64) ([]*model.FileGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+fileGrantCols+` FROM file_grants WHERE storage_id=? AND user_id=?`, storageID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.FileGrant
	for rows.Next() {
		g, err := scanFileGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) ListFileGrantsByStorage(ctx context.Context, storageID int64) ([]*model.FileGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+fileGrantCols+` FROM file_grants WHERE storage_id=? ORDER BY path_prefix, user_id`, storageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.FileGrant
	for rows.Next() {
		g, err := scanFileGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) GetFileGrant(ctx context.Context, id int64) (*model.FileGrant, error) {
	return scanFileGrant(s.db.QueryRowContext(ctx, `SELECT `+fileGrantCols+` FROM file_grants WHERE id=?`, id))
}

func (s *Store) ListAllFileGrants(ctx context.Context) ([]*model.FileGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+fileGrantCols+` FROM file_grants ORDER BY storage_id, path_prefix, user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.FileGrant
	for rows.Next() {
		g, err := scanFileGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CreateFileGrant upserts a grant on the (storage_id, path_prefix, user_id)
// unique key. Uses a portable check-then-write (UPDATE, else INSERT) so the
// same code path works for the MySQL driver that wraps this Store — MySQL does
// not understand SQLite's `ON CONFLICT ... excluded.` upsert.
func (s *Store) CreateFileGrant(ctx context.Context, g *model.FileGrant) (*model.FileGrant, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE file_grants SET level=?, is_dir=?, created_by=? WHERE storage_id=? AND path_prefix=? AND user_id=?`,
		g.Level, btoi(g.IsDir), g.CreatedBy, g.StorageID, g.PathPrefix, g.UserID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return scanFileGrant(s.db.QueryRowContext(ctx,
			`SELECT `+fileGrantCols+` FROM file_grants WHERE storage_id=? AND path_prefix=? AND user_id=?`,
			g.StorageID, g.PathPrefix, g.UserID))
	}
	ins, err := s.db.ExecContext(ctx,
		`INSERT INTO file_grants (storage_id, path_prefix, is_dir, user_id, level, created_by) VALUES (?,?,?,?,?,?)`,
		g.StorageID, g.PathPrefix, btoi(g.IsDir), g.UserID, g.Level, g.CreatedBy)
	if err != nil {
		return nil, err
	}
	id, _ := ins.LastInsertId()
	return s.GetFileGrant(ctx, id)
}

func (s *Store) UpdateFileGrantLevel(ctx context.Context, id int64, level string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE file_grants SET level=? WHERE id=?`, level, id)
	return err
}

func (s *Store) DeleteFileGrant(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM file_grants WHERE id=?`, id)
	return err
}

// ─────────────────── Shares ───────────────────

func (s *Store) CreateShare(ctx context.Context, sh *model.Share) (*model.Share, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO shares (node_id, token, pin_hash, expires_at, max_downloads, created_by, kind, max_uploads, drop_settings) VALUES (?,?,?,?,?,?,?,?,?)`,
		sh.NodeID, sh.Token, sh.PinHash, sh.ExpiresAt, sh.MaxDownloads, sh.CreatedBy, shareKind(sh.Kind), sh.MaxUploads, sh.DropSettings)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	sh.ID = id
	sh.CreatedAt = time.Now()
	sh.HasPin = sh.PinHash != ""
	return sh, nil
}

func (s *Store) GetShareByToken(ctx context.Context, token string) (*model.Share, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, node_id, token, COALESCE(pin_hash,''), expires_at, max_downloads, download_count, created_by, created_at, COALESCE(kind,'download'), max_uploads, upload_count, drop_settings FROM shares WHERE token=?`, token)
	return scanShare(row)
}

func (s *Store) GetShareByID(ctx context.Context, id int64) (*model.Share, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, node_id, token, COALESCE(pin_hash,''), expires_at, max_downloads, download_count, created_by, created_at, COALESCE(kind,'download'), max_uploads, upload_count, drop_settings FROM shares WHERE id=?`, id)
	return scanShare(row)
}

// ListAllShares returns the admin overview of every share. `creatorID`
// nil means all users; activeOnly excludes expired/revoked rows.
func (s *Store) ListAllShares(ctx context.Context, creatorID *int64, activeOnly bool, limit, offset int) ([]*db.ShareWithMeta, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	where := []string{"1=1"}
	args := []any{}
	if creatorID != nil {
		where = append(where, `s.created_by=?`)
		args = append(args, *creatorID)
	}
	if activeOnly {
		where = append(where, `(s.expires_at IS NULL OR s.expires_at > CURRENT_TIMESTAMP)`)
		where = append(where, `(s.max_downloads IS NULL OR s.download_count < s.max_downloads)`)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM shares s WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id, s.node_id, s.token, COALESCE(s.pin_hash,''), s.expires_at, s.max_downloads, s.download_count, s.created_by, s.created_at,
		        COALESCE(u.email,''), COALESCE(n.path,''), COALESCE(st.name,'')
		 FROM shares s
		 LEFT JOIN users u    ON u.id=s.created_by
		 LEFT JOIN nodes n    ON n.id=s.node_id
		 LEFT JOIN storages st ON st.id=n.storage_id
		 WHERE `+whereSQL+` ORDER BY s.created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*db.ShareWithMeta
	for rows.Next() {
		sh := &model.Share{}
		var creatorEmail, nodePath, storageName string
		if err := rows.Scan(&sh.ID, &sh.NodeID, &sh.Token, &sh.PinHash, &sh.ExpiresAt, &sh.MaxDownloads, &sh.DownloadCount, &sh.CreatedBy, &sh.CreatedAt, &creatorEmail, &nodePath, &storageName); err != nil {
			return nil, 0, err
		}
		sh.HasPin = sh.PinHash != ""
		out = append(out, &db.ShareWithMeta{Share: sh, CreatorEmail: creatorEmail, NodePath: nodePath, StorageName: storageName})
	}
	return out, total, rows.Err()
}

// RevokeShare soft-revokes by setting expires_at = NOW. Audit trail is
// kept (the row is not deleted).
func (s *Store) RevokeShare(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET expires_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

func (s *Store) ListSharesByNode(ctx context.Context, nodeID int64) ([]*model.Share, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, node_id, token, COALESCE(pin_hash,''), expires_at, max_downloads, download_count, created_by, created_at, COALESCE(kind,'download'), max_uploads, upload_count, drop_settings FROM shares WHERE node_id=? ORDER BY created_at DESC`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Share
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

func (s *Store) IncrementShareDownload(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET download_count = download_count + 1 WHERE id=?`, id)
	return err
}

func (s *Store) IncrementShareUpload(ctx context.Context, id int64, n int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET upload_count = upload_count + ? WHERE id=?`, n, id)
	return err
}

func (s *Store) DeleteShare(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM shares WHERE id=?`, id)
	return err
}

func (s *Store) DeleteExpiredShares(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM shares WHERE expires_at IS NOT NULL AND expires_at < CURRENT_TIMESTAMP`)
	return err
}

// ─────────────────── Chunked uploads ───────────────────

func (s *Store) CreateChunkedUpload(ctx context.Context, u *model.ChunkedUpload) error {
	parts, _ := json.Marshal(u.Parts)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chunked_uploads (id, storage_id, storage_key, upload_id, total_size, parts_json, expires_at) VALUES (?,?,?,?,?,?,?)`,
		u.ID, u.StorageID, u.StorageKey, u.UploadID, u.TotalSize, string(parts), u.ExpiresAt)
	return err
}

func (s *Store) GetChunkedUpload(ctx context.Context, id string) (*model.ChunkedUpload, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, storage_id, storage_key, upload_id, total_size, parts_json, expires_at FROM chunked_uploads WHERE id=?`, id)
	out := &model.ChunkedUpload{}
	var partsJSON string
	if err := row.Scan(&out.ID, &out.StorageID, &out.StorageKey, &out.UploadID, &out.TotalSize, &partsJSON, &out.ExpiresAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(partsJSON), &out.Parts)
	return out, nil
}

func (s *Store) UpdateChunkedUploadParts(ctx context.Context, id string, parts []model.UploadPart) error {
	pj, _ := json.Marshal(parts)
	_, err := s.db.ExecContext(ctx, `UPDATE chunked_uploads SET parts_json=? WHERE id=?`, string(pj), id)
	return err
}

func (s *Store) DeleteChunkedUpload(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chunked_uploads WHERE id=?`, id)
	return err
}

func (s *Store) DeleteExpiredChunkedUploads(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chunked_uploads WHERE expires_at < CURRENT_TIMESTAMP`)
	return err
}

// ─────────────────── Sync ───────────────────

func (s *Store) CreateSyncRun(ctx context.Context, storageID int64, cursorBefore string) (*model.SyncRun, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO sync_runs (storage_id, cursor_before, status) VALUES (?,?,'running')`, storageID, cursorBefore)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &model.SyncRun{ID: id, StorageID: storageID, StartedAt: time.Now(), CursorBefore: cursorBefore, Status: "running"}, nil
}

func (s *Store) FinishSyncRun(ctx context.Context, id int64, cursorAfter string, seen, added, updated, deleted int, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sync_runs SET finished_at=CURRENT_TIMESTAMP, cursor_after=?, seen_count=?, added=?, updated=?, deleted=?, status=?, error=? WHERE id=?`,
		cursorAfter, seen, added, updated, deleted, status, errMsg, id)
	return err
}

func (s *Store) GetLastSyncRun(ctx context.Context, storageID int64) (*model.SyncRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, storage_id, started_at, finished_at, COALESCE(cursor_before,''), COALESCE(cursor_after,''), seen_count, added, updated, deleted, status, COALESCE(error,'')
		 FROM sync_runs WHERE storage_id=? ORDER BY started_at DESC LIMIT 1`, storageID)
	return scanSyncRun(row)
}

func (s *Store) GetSyncRun(ctx context.Context, id int64) (*model.SyncRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, storage_id, started_at, finished_at, COALESCE(cursor_before,''), COALESCE(cursor_after,''), seen_count, added, updated, deleted, status, COALESCE(error,'')
		 FROM sync_runs WHERE id=?`, id)
	return scanSyncRun(row)
}

// ListSyncRunsAcrossAll returns paginated runs across every storage,
// optionally filtered by storageID (0=all) and status (""=all).
//
// Runs older than 5 days are filtered out — the admin Sync history
// page only cares about recent activity, and older runs clutter the
// list (a busy storage produces hundreds per day).
func (s *Store) ListSyncRunsAcrossAll(ctx context.Context, storageID int64, status string, limit, offset int) ([]*model.SyncRun, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	where := []string{"started_at >= datetime('now', '-5 days')"}
	args := []any{}
	if storageID > 0 {
		where = append(where, `storage_id=?`)
		args = append(args, storageID)
	}
	if status != "" {
		where = append(where, `status=?`)
		args = append(args, status)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_runs WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, storage_id, started_at, finished_at, COALESCE(cursor_before,''), COALESCE(cursor_after,''), seen_count, added, updated, deleted, status, COALESCE(error,'')
		 FROM sync_runs WHERE `+whereSQL+` ORDER BY started_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*model.SyncRun
	for rows.Next() {
		sr, err := scanSyncRun(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, sr)
	}
	return out, total, rows.Err()
}

func (s *Store) ListSyncRuns(ctx context.Context, storageID int64, limit int) ([]*model.SyncRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, storage_id, started_at, finished_at, COALESCE(cursor_before,''), COALESCE(cursor_after,''), seen_count, added, updated, deleted, status, COALESCE(error,'')
		 FROM sync_runs WHERE storage_id=? ORDER BY started_at DESC LIMIT ?`, storageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.SyncRun
	for rows.Next() {
		r, err := scanSyncRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateSyncConflict(ctx context.Context, c *model.SyncConflict) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sync_conflicts (node_id, storage_id, storage_key, db_etag, backend_etag, db_mtime, backend_mtime) VALUES (?,?,?,?,?,?,?)`,
		c.NodeID, c.StorageID, c.StorageKey, c.DBEtag, c.BackendEtag, c.DBMtime, c.BackendMtime)
	return err
}

func (s *Store) ListUnresolvedConflicts(ctx context.Context) ([]*model.SyncConflict, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, storage_id, COALESCE(storage_key,''), COALESCE(db_etag,''), COALESCE(backend_etag,''), db_mtime, backend_mtime, detected_at, resolved_at, COALESCE(resolution,'')
		 FROM sync_conflicts WHERE resolved_at IS NULL ORDER BY detected_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.SyncConflict
	for rows.Next() {
		c := &model.SyncConflict{}
		if err := rows.Scan(&c.ID, &c.NodeID, &c.StorageID, &c.StorageKey, &c.DBEtag, &c.BackendEtag, &c.DBMtime, &c.BackendMtime, &c.DetectedAt, &c.ResolvedAt, &c.Resolution); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ResolveConflict(ctx context.Context, id int64, resolution string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sync_conflicts SET resolved_at=CURRENT_TIMESTAMP, resolution=? WHERE id=?`, resolution, id)
	return err
}

// ListConflictsByStorage returns the most recent unresolved conflicts
// for one storage — used by /api/admin/storages/:id/drift.
func (s *Store) ListConflictsByStorage(ctx context.Context, storageID int64, limit int) ([]*model.SyncConflict, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, storage_id, COALESCE(storage_key,''), COALESCE(db_etag,''), COALESCE(backend_etag,''), db_mtime, backend_mtime, detected_at, resolved_at, COALESCE(resolution,'')
		 FROM sync_conflicts WHERE storage_id=? ORDER BY detected_at DESC LIMIT ?`, storageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.SyncConflict
	for rows.Next() {
		c := &model.SyncConflict{}
		if err := rows.Scan(&c.ID, &c.NodeID, &c.StorageID, &c.StorageKey, &c.DBEtag, &c.BackendEtag, &c.DBMtime, &c.BackendMtime, &c.DetectedAt, &c.ResolvedAt, &c.Resolution); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountSyncConflictsByRun returns the count of conflicts attributed to a
// run via timestamp window. We don't store run_id on conflicts (it's not
// in the schema), so we approximate by detected_at proximity.
func (s *Store) CountSyncConflictsByRun(ctx context.Context, runID int64) (int64, error) {
	var n int64
	// Match conflicts detected during the run window for the same storage.
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sync_conflicts c
		 INNER JOIN sync_runs r ON r.storage_id=c.storage_id
		 WHERE r.id=? AND c.detected_at >= r.started_at AND (r.finished_at IS NULL OR c.detected_at <= r.finished_at)`,
		runID).Scan(&n)
	return n, err
}

// CountQueueDepth returns the number of running sync_runs (a stand-in
// "queue depth" until we ship a real op queue table).
func (s *Store) CountQueueDepth(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_runs WHERE status='running'`).Scan(&n)
	return n, err
}

// ─────────────────── Audit ───────────────────

func (s *Store) InsertAuditEntry(ctx context.Context, e *model.AuditEntry) error {
	mj, _ := json.Marshal(e.Metadata)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, action, target_type, target_id, metadata_json, ip) VALUES (?,?,?,?,?,?)`,
		e.UserID, e.Action, e.TargetType, e.TargetID, string(mj), e.IP)
	return err
}

func (s *Store) ListAuditRecent(ctx context.Context, limit int) ([]*model.AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, action, COALESCE(target_type,''), COALESCE(target_id,''), metadata_json, COALESCE(ip,''), created_at
		 FROM audit_log ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.AuditEntry
	for rows.Next() {
		e := &model.AuditEntry{}
		var meta string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Action, &e.TargetType, &e.TargetID, &meta, &e.IP, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(meta), &e.Metadata)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ─────────────────── Settings ───────────────────

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	return v, err
}

func (s *Store) UpsertSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`,
		key, value)
	return err
}

func (s *Store) ListSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, COALESCE(value,'') FROM settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ─────────────────── External services ───────────────────

func (s *Store) UpsertExternalService(ctx context.Context, name string, enabled bool, urlS, secretEnc, optionsJSON string, lastCheck time.Time, lastState string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO external_services (name, enabled, url, secret_enc, options_json, last_check, last_state) VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(name) DO UPDATE SET enabled=excluded.enabled, url=excluded.url, secret_enc=excluded.secret_enc, options_json=excluded.options_json, last_check=excluded.last_check, last_state=excluded.last_state`,
		name, btoi(enabled), urlS, secretEnc, optionsJSON, lastCheck, lastState)
	return err
}

func (s *Store) GetExternalService(ctx context.Context, name string) (*db.ExternalService, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT name, enabled, COALESCE(url,''), COALESCE(secret_enc,''), options_json, last_check, COALESCE(last_state,'') FROM external_services WHERE name=?`, name)
	return scanExternalService(row)
}

func (s *Store) ListExternalServices(ctx context.Context) ([]*db.ExternalService, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, enabled, COALESCE(url,''), COALESCE(secret_enc,''), options_json, last_check, COALESCE(last_state,'') FROM external_services ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*db.ExternalService
	for rows.Next() {
		es, err := scanExternalService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, es)
	}
	return out, rows.Err()
}

func (s *Store) UpdateExternalServiceState(ctx context.Context, name string, lastCheck time.Time, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE external_services SET last_check=?, last_state=? WHERE name=?`, lastCheck, state, name)
	return err
}

// ─────────────────── Thumbnails / versions ───────────────────

func (s *Store) GetThumbnail(ctx context.Context, nodeID int64) (*model.Thumbnail, error) {
	row := s.db.QueryRowContext(ctx, `SELECT node_id, state, COALESCE(storage_key,''), COALESCE(width,0), COALESCE(height,0), COALESCE(error,''), generated_at FROM thumbnails WHERE node_id=?`, nodeID)
	t := &model.Thumbnail{}
	if err := row.Scan(&t.NodeID, &t.State, &t.StorageKey, &t.Width, &t.Height, &t.Error, &t.GeneratedAt); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Store) UpsertThumbnail(ctx context.Context, t *model.Thumbnail) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO thumbnails (node_id, state, storage_key, width, height, error, generated_at) VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(node_id) DO UPDATE SET state=excluded.state, storage_key=excluded.storage_key, width=excluded.width, height=excluded.height, error=excluded.error, generated_at=excluded.generated_at`,
		t.NodeID, t.State, t.StorageKey, t.Width, t.Height, t.Error, t.GeneratedAt)
	return err
}

func (s *Store) SetThumbnailState(ctx context.Context, nodeID int64, state, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE thumbnails SET state=?, error=? WHERE node_id=?`, state, errMsg, nodeID)
	return err
}

func (s *Store) CreateNodeVersion(ctx context.Context, v *model.NodeVersion) (*model.NodeVersion, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO node_versions (node_id, version_n, storage_key, size, etag) VALUES (?,?,?,?,?)`,
		v.NodeID, v.VersionN, v.StorageKey, v.Size, v.Etag)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	v.ID = id
	v.CreatedAt = time.Now()
	return v, nil
}

func (s *Store) ListNodeVersions(ctx context.Context, nodeID int64) ([]*model.NodeVersion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, node_id, version_n, COALESCE(storage_key,''), size, COALESCE(etag,''), created_at FROM node_versions WHERE node_id=? ORDER BY version_n DESC`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.NodeVersion
	for rows.Next() {
		v := &model.NodeVersion{}
		if err := rows.Scan(&v.ID, &v.NodeID, &v.VersionN, &v.StorageKey, &v.Size, &v.Etag, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ─────────────────── helpers ───────────────────

type rowScanner interface {
	Scan(dst ...any) error
}

func nodeSelectColumns() string {
	return `SELECT id, storage_id, parent_id, name, path, path_hash, COALESCE(storage_key,''), type, size, COALESCE(mime,''), COALESCE(etag,''), backend_mtime, db_mtime, sync_state, seen_at, deleted_at, created_at, updated_at`
}

func scanNode(r rowScanner) (*model.Node, error) {
	n := &model.Node{}
	err := r.Scan(&n.ID, &n.StorageID, &n.ParentID, &n.Name, &n.Path, &n.PathHash, &n.StorageKey, &n.Type, &n.Size, &n.Mime, &n.Etag, &n.BackendMtime, &n.DBMtime, &n.SyncState, &n.SeenAt, &n.DeletedAt, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return n, nil
}

func scanStorage(r rowScanner) (*model.Storage, error) {
	st := &model.Storage{}
	var cfg string
	var role, replicaMode sql.NullString
	var replicaOf, replicaTarget sql.NullInt64
	err := r.Scan(
		&st.ID, &st.Name, &st.Driver, &st.MountPath, &cfg,
		&st.SyncMode, &st.SyncIntervalS, &st.LastSyncAt, &st.LastSyncToken,
		&st.Enabled, &st.ReadOnly, &st.CreatedAt,
		&role, &replicaOf, &replicaMode, &replicaTarget,
		&st.RBACEnabled,
	)
	if err != nil {
		return nil, err
	}
	st.ConfigJSON = []byte(cfg)
	if role.Valid {
		st.Role = role.String
	}
	if replicaOf.Valid {
		v := replicaOf.Int64
		st.ReplicaOfID = &v
	}
	if replicaMode.Valid {
		st.ReplicaMode = replicaMode.String
	}
	if replicaTarget.Valid {
		v := replicaTarget.Int64
		st.ReplicaTargetID = &v
	}
	return st, nil
}

func userSelect() string {
	return `SELECT id, email, COALESCE(display_name,''), COALESCE(password_hash,''), role, COALESCE(totp_secret,''), COALESCE(totp_pending_secret,''), COALESCE(totp_enabled,0), COALESCE(totp_recovery_codes_json,'[]'), locale, timezone, created_at, updated_at, last_login_at`
}

func scanUser(r rowScanner) (*model.User, error) {
	u := &model.User{}
	var totpEnabled int
	var recoveryJSON string
	if err := r.Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Role, &u.TOTPSecret, &u.TOTPPendingSecret, &totpEnabled, &recoveryJSON, &u.Locale, &u.Timezone, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt); err != nil {
		return nil, err
	}
	u.TOTPEnabled = totpEnabled == 1
	if recoveryJSON != "" {
		_ = json.Unmarshal([]byte(recoveryJSON), &u.TOTPRecoveryCodes)
	}
	return u, nil
}

func scanShare(r rowScanner) (*model.Share, error) {
	sh := &model.Share{}
	if err := r.Scan(&sh.ID, &sh.NodeID, &sh.Token, &sh.PinHash, &sh.ExpiresAt, &sh.MaxDownloads, &sh.DownloadCount, &sh.CreatedBy, &sh.CreatedAt, &sh.Kind, &sh.MaxUploads, &sh.UploadCount, &sh.DropSettings); err != nil {
		return nil, err
	}
	sh.HasPin = sh.PinHash != ""
	return sh, nil
}

// shareKind defaults an empty kind to "download" so old callers keep working.
func shareKind(k string) string {
	if k == "" {
		return model.ShareKindDownload
	}
	return k
}

func scanSyncRun(r rowScanner) (*model.SyncRun, error) {
	sr := &model.SyncRun{}
	if err := r.Scan(&sr.ID, &sr.StorageID, &sr.StartedAt, &sr.FinishedAt, &sr.CursorBefore, &sr.CursorAfter, &sr.SeenCount, &sr.Added, &sr.Updated, &sr.Deleted, &sr.Status, &sr.Error); err != nil {
		return nil, err
	}
	return sr, nil
}

func scanExternalService(r rowScanner) (*db.ExternalService, error) {
	es := &db.ExternalService{}
	var enabled int
	if err := r.Scan(&es.Name, &enabled, &es.URL, &es.SecretEnc, &es.OptionsJSON, &es.LastCheck, &es.LastState); err != nil {
		return nil, err
	}
	es.Enabled = enabled == 1
	return es, nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ─────────────────── Sync conflicts (admin) ───────────────────

const conflictColumns = `id, node_id, storage_id, storage_key, db_etag, backend_etag, db_mtime, backend_mtime, detected_at, resolved_at, resolution`

// ListSyncConflictsByRun returns conflicts attributed to a specific sync_run.
//
// V0.1 schema does not link conflicts to a run; we approximate by returning
// conflicts detected within the run's time window (best effort).
func (s *Store) ListSyncConflictsByRun(ctx context.Context, runID int64) ([]*model.SyncConflict, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+conflictColumns+`
		FROM sync_conflicts c
		WHERE c.detected_at >= COALESCE((SELECT started_at FROM sync_runs WHERE id=?), c.detected_at)
		  AND c.detected_at <= COALESCE((SELECT finished_at FROM sync_runs WHERE id=?), CURRENT_TIMESTAMP)
		ORDER BY c.detected_at DESC
		LIMIT 500`, runID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConflicts(rows)
}

// ListSyncConflictsByStorage returns recent unresolved conflicts.
func (s *Store) ListSyncConflictsByStorage(ctx context.Context, storageID int64, limit int) ([]*model.SyncConflict, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+conflictColumns+`
		FROM sync_conflicts
		WHERE storage_id=? AND resolved_at IS NULL
		ORDER BY detected_at DESC
		LIMIT ?`, storageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConflicts(rows)
}

func scanConflicts(rows *sql.Rows) ([]*model.SyncConflict, error) {
	out := []*model.SyncConflict{}
	for rows.Next() {
		c := &model.SyncConflict{}
		if err := rows.Scan(&c.ID, &c.NodeID, &c.StorageID, &c.StorageKey, &c.DBEtag, &c.BackendEtag, &c.DBMtime, &c.BackendMtime, &c.DetectedAt, &c.ResolvedAt, &c.Resolution); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ─────────────────── Search rebuild support ───────────────────

const nodeColumnsForIndex = `id, storage_id, parent_id, name, path, path_hash, COALESCE(storage_key,''), type, size, COALESCE(mime,''), COALESCE(etag,''), backend_mtime, db_mtime, sync_state, seen_at, deleted_at, created_at, updated_at`

// AllNodesForIndex returns every non-deleted node for the search rebuild job.
func (s *Store) AllNodesForIndex(ctx context.Context) ([]*model.Node, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+nodeColumnsForIndex+`
		FROM nodes
		WHERE deleted_at IS NULL
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ─────────────────── Counters needed by dashboard / metrics ───────────────────

// CountNodesAddedSince counts non-deleted nodes created in the given window.
func (s *Store) CountNodesAddedSince(ctx context.Context, storageID int64, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM nodes WHERE storage_id=? AND created_at >= ? AND deleted_at IS NULL`,
		storageID, since).Scan(&n)
	return n, err
}

// CountNodesDeletedSince counts soft-deleted nodes in the given window.
func (s *Store) CountNodesDeletedSince(ctx context.Context, storageID int64, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM nodes WHERE storage_id=? AND deleted_at IS NOT NULL AND deleted_at >= ?`,
		storageID, since).Scan(&n)
	return n, err
}

// CountTotalShares returns the number of currently-active shares.
func (s *Store) CountTotalShares(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM shares WHERE expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP`).Scan(&n)
	return n, err
}

// ListAuditFiltered returns paginated audit entries with user_email join + filters.
func (s *Store) ListAuditFiltered(ctx context.Context, userID *int64, action string, from, to *time.Time, limit, offset int) ([]*db.AuditEntryWithUser, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	cond := "1=1"
	args := []any{}
	if userID != nil {
		cond += " AND a.user_id = ?"
		args = append(args, *userID)
	}
	if action != "" {
		cond += " AND a.action = ?"
		args = append(args, action)
	}
	if from != nil {
		cond += " AND a.created_at >= ?"
		args = append(args, *from)
	}
	if to != nil {
		cond += " AND a.created_at <= ?"
		args = append(args, *to)
	}

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log a WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.user_id, COALESCE(u.email,''), a.action, COALESCE(a.target_type,''),
		       COALESCE(a.target_id,''), COALESCE(a.metadata_json,''), COALESCE(a.ip,''), a.created_at
		FROM audit_log a
		LEFT JOIN users u ON u.id = a.user_id
		WHERE `+cond+`
		ORDER BY a.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []*db.AuditEntryWithUser{}
	for rows.Next() {
		entry := &model.AuditEntry{}
		var metaJSON string
		row := &db.AuditEntryWithUser{Entry: entry}
		if err := rows.Scan(&entry.ID, &entry.UserID, &row.UserEmail, &entry.Action, &entry.TargetType, &entry.TargetID, &metaJSON, &entry.IP, &entry.CreatedAt); err != nil {
			return nil, 0, err
		}
		if metaJSON != "" {
			_ = json.Unmarshal([]byte(metaJSON), &entry.Metadata)
		}
		out = append(out, row)
	}
	return out, total, rows.Err()
}

// SumNodesBytesByStorage returns the total size in bytes of non-deleted files for one storage.
func (s *Store) SumNodesBytesByStorage(ctx context.Context, storageID int64) (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(size),0) FROM nodes WHERE storage_id=? AND type=1 AND deleted_at IS NULL`,
		storageID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// ─────────────────── Node versions (extended) ───────────────────

// GetNodeVersion looks up a single version row by id.
func (s *Store) GetNodeVersion(ctx context.Context, id int64) (*model.NodeVersion, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, node_id, version_n, COALESCE(storage_key,''), size, COALESCE(etag,''), created_at FROM node_versions WHERE id=?`, id)
	v := &model.NodeVersion{}
	if err := row.Scan(&v.ID, &v.NodeID, &v.VersionN, &v.StorageKey, &v.Size, &v.Etag, &v.CreatedAt); err != nil {
		return nil, err
	}
	return v, nil
}

// NextNodeVersionNumber returns COALESCE(MAX(version_n),0)+1 for a node.
func (s *Store) NextNodeVersionNumber(ctx context.Context, nodeID int64) (int, error) {
	var n sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_n),0) FROM node_versions WHERE node_id=?`, nodeID).Scan(&n); err != nil {
		return 0, err
	}
	return int(n.Int64) + 1, nil
}

// DeleteNodeVersion removes a single version row.
func (s *Store) DeleteNodeVersion(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM node_versions WHERE id=?`, id)
	return err
}

// DeleteOldNodeVersions deletes all but the newest `keep` versions for a node.
// Returns the rows that were removed (so the caller can clean storage objects).
func (s *Store) DeleteOldNodeVersions(ctx context.Context, nodeID int64, keep int) ([]*model.NodeVersion, error) {
	if keep < 0 {
		keep = 0
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, version_n, COALESCE(storage_key,''), size, COALESCE(etag,''), created_at
		 FROM node_versions
		 WHERE node_id=?
		 ORDER BY version_n DESC
		 LIMIT -1 OFFSET ?`, nodeID, keep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var doomed []*model.NodeVersion
	for rows.Next() {
		v := &model.NodeVersion{}
		if err := rows.Scan(&v.ID, &v.NodeID, &v.VersionN, &v.StorageKey, &v.Size, &v.Etag, &v.CreatedAt); err != nil {
			return nil, err
		}
		doomed = append(doomed, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, v := range doomed {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM node_versions WHERE id=?`, v.ID); err != nil {
			return doomed, err
		}
	}
	return doomed, nil
}

// ─────────────────── Quota ───────────────────

// GetUserUsage returns (used_bytes, quota_bytes).
func (s *Store) GetUserUsage(ctx context.Context, userID int64) (int64, int64, error) {
	var used, limit int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(usage_bytes,0), COALESCE(quota_bytes,0) FROM users WHERE id=?`, userID).
		Scan(&used, &limit)
	if err != nil {
		return 0, 0, err
	}
	return used, limit, nil
}

// IncrementUserUsage atomically adjusts usage_bytes (delta may be negative);
// clamps the resulting value at 0 to keep it sane.
func (s *Store) IncrementUserUsage(ctx context.Context, userID int64, delta int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET usage_bytes = MAX(0, COALESCE(usage_bytes,0) + ?) WHERE id=?`, delta, userID)
	return err
}

// SetUserQuota sets the quota_bytes value for a user (0 = unlimited).
func (s *Store) SetUserQuota(ctx context.Context, userID int64, bytes int64) error {
	if bytes < 0 {
		bytes = 0
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET quota_bytes=? WHERE id=?`, bytes, userID)
	return err
}

// RecomputeUserUsage scans nodes owned by this user, sets usage_bytes to the
// sum of their (non-deleted) sizes, and returns the value.
func (s *Store) RecomputeUserUsage(ctx context.Context, userID int64) (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(size),0) FROM nodes WHERE owner_id=? AND deleted_at IS NULL AND type='file'`,
		userID).Scan(&total)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET usage_bytes=? WHERE id=?`, total.Int64, userID); err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// ─────────────────── Node owner ───────────────────

// SetNodeOwner updates the owner_id column for one node.
func (s *Store) SetNodeOwner(ctx context.Context, nodeID int64, ownerID *int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET owner_id=? WHERE id=?`, ownerID, nodeID)
	return err
}

// GetNodeOwner returns the owner_id (nullable) for one node.
func (s *Store) GetNodeOwner(ctx context.Context, nodeID int64) (*int64, error) {
	var owner sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM nodes WHERE id=?`, nodeID).Scan(&owner)
	if err != nil {
		return nil, err
	}
	if !owner.Valid {
		return nil, nil
	}
	v := owner.Int64
	return &v, nil
}

// ─────────────────── Trash retention ───────────────────

// ListTrashedExpired returns soft-deleted nodes whose deleted_at is older than `before`.
func (s *Store) ListTrashedExpired(ctx context.Context, before time.Time, limit int) ([]*model.Node, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, nodeSelectColumns()+`
		FROM nodes WHERE deleted_at IS NOT NULL AND deleted_at < ?
		ORDER BY deleted_at ASC LIMIT ?`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// RestoreNode flips deleted_at back to NULL.
func (s *Store) RestoreNode(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET deleted_at=NULL, updated_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

// ListTrashed returns paginated soft-deleted rows, optionally narrowed to a
// single storage. Total count returned alongside so the UI can paginate.
func (s *Store) ListTrashed(ctx context.Context, storageID *int64, limit, offset int) ([]*model.Node, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	args := []any{}
	where := `WHERE deleted_at IS NOT NULL`
	if storageID != nil {
		where += ` AND storage_id = ?`
		args = append(args, *storageID)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx,
		nodeSelectColumns()+` FROM nodes `+where+` ORDER BY deleted_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*model.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, n)
	}
	return out, total, rows.Err()
}

// RestoreNodeAt flips deleted_at to NULL while reverting path/parent.
//
// Used by trash.Service after `vfDelete` left `storage_key` holding the
// original path (and `path` holding the trash key). Caller resolves
// `parentID` via `LookupParentByPath`.
func (s *Store) RestoreNodeAt(ctx context.Context, id int64, parentID *int64, origPath string) error {
	clean := strings.TrimRight(path.Clean("/"+strings.Trim(origPath, "/")), "/")
	if clean == "" {
		clean = "/"
	}
	row := s.db.QueryRowContext(ctx, `SELECT storage_id FROM nodes WHERE id=?`, id)
	var sid int64
	if err := row.Scan(&sid); err != nil {
		return err
	}
	hash := nodePathHash(sid, clean)
	name := path.Base(clean)
	if parentID == nil {
		_, err := s.db.ExecContext(ctx, `
			UPDATE nodes
			SET deleted_at=NULL,
			    updated_at=CURRENT_TIMESTAMP,
			    parent_id=NULL,
			    name=?, path=?, path_hash=?, storage_key=?
			WHERE id=?`, name, clean, hash, clean, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE nodes
		SET deleted_at=NULL,
		    updated_at=CURRENT_TIMESTAMP,
		    parent_id=?,
		    name=?, path=?, path_hash=?, storage_key=?
		WHERE id=?`, *parentID, name, clean, hash, clean, id)
	return err
}

// LookupParentByPath returns parent_id (nil at root) for `fullPath`'s
// parent dir, by walking the cache one segment at a time.
func (s *Store) LookupParentByPath(ctx context.Context, storageID int64, fullPath string) (*int64, error) {
	clean := strings.Trim(fullPath, "/")
	dir := path.Dir(clean)
	if dir == "" || dir == "." {
		return nil, nil
	}
	parts := strings.Split(strings.Trim(dir, "/"), "/")
	var parentPtr *int64
	for _, seg := range parts {
		if seg == "" {
			continue
		}
		var id int64
		if parentPtr == nil {
			err := s.db.QueryRowContext(ctx, `
				SELECT id FROM nodes
				WHERE storage_id=? AND name=? AND deleted_at IS NULL
				  AND parent_id IS NULL
				LIMIT 1`, storageID, seg).Scan(&id)
			if err != nil {
				return nil, err
			}
		} else {
			err := s.db.QueryRowContext(ctx, `
				SELECT id FROM nodes
				WHERE storage_id=? AND name=? AND deleted_at IS NULL
				  AND parent_id=?
				LIMIT 1`, storageID, seg, *parentPtr).Scan(&id)
			if err != nil {
				return nil, err
			}
		}
		parentPtr = &id
	}
	return parentPtr, nil
}

// nodePathHash computes the same MD5 used by sync.pathHash and the
// manager handler — see manager_mutate.go's `managerPathHash`.
func nodePathHash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
}

// ─────────────────── User-scoped node meta ───────────────────

// SetUserNodeMeta upserts a (user, node, key) row.
func (s *Store) SetUserNodeMeta(ctx context.Context, userID, nodeID int64, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_node_meta (user_id, node_id, key, value, updated_at)
		 VALUES (?,?,?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id, node_id, key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`,
		userID, nodeID, key, value)
	return err
}

// DeleteUserNodeMeta removes a single (user, node, key) row.
func (s *Store) DeleteUserNodeMeta(ctx context.Context, userID, nodeID int64, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_node_meta WHERE user_id=? AND node_id=? AND key=?`, userID, nodeID, key)
	return err
}

// GetUserNodeMeta fetches a single value (returns empty string + sql.ErrNoRows if absent).
func (s *Store) GetUserNodeMeta(ctx context.Context, userID, nodeID int64, key string) (string, error) {
	var v sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT value FROM user_node_meta WHERE user_id=? AND node_id=? AND key=?`, userID, nodeID, key).Scan(&v)
	if err != nil {
		return "", err
	}
	return v.String, nil
}

// ListUserNodeMetaForNode returns all (key,value) for one (user,node) pair,
// optionally restricted to keys that start with `prefix`.
func (s *Store) ListUserNodeMetaForNode(ctx context.Context, userID, nodeID int64, prefix string) (map[string]string, error) {
	q := `SELECT key, COALESCE(value,'') FROM user_node_meta WHERE user_id=? AND node_id=?`
	args := []any{userID, nodeID}
	if prefix != "" {
		q += ` AND key LIKE ?`
		args = append(args, prefix+"%")
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ListNodesByUserMeta returns the nodes flagged with (key) for the given user,
// joined with the live node row, ordered by user_node_meta.updated_at DESC.
func (s *Store) ListNodesByUserMeta(ctx context.Context, userID int64, key string, limit int) ([]*model.Node, error) {
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT n.id, n.storage_id, n.parent_id, n.name, n.path, n.path_hash, COALESCE(n.storage_key,''), n.type, n.size, COALESCE(n.mime,''), COALESCE(n.etag,''), n.backend_mtime, n.db_mtime, n.sync_state, n.seen_at, n.deleted_at, n.created_at, n.updated_at
		 FROM user_node_meta m
		 INNER JOIN nodes n ON n.id = m.node_id
		 WHERE m.user_id=? AND m.key=? AND n.deleted_at IS NULL
		 ORDER BY m.updated_at DESC
		 LIMIT ?`, userID, key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ─────────────────── Tags (shared) ───────────────────

const tagPrefix = "tag:"

// SetNodeTags wipes all existing tag:* rows for a node and writes new ones.
func (s *Store) SetNodeTags(ctx context.Context, nodeID int64, tags []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_meta WHERE node_id=? AND key LIKE ?`, nodeID, tagPrefix+"%"); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, raw := range tags {
		t := strings.ToLower(strings.TrimSpace(raw))
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO node_meta (node_id, key, value) VALUES (?,?,?)
			 ON CONFLICT(node_id, key) DO UPDATE SET value=excluded.value`,
			nodeID, tagPrefix+t, "1"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetNodeTags returns the tag list (without prefix) for one node.
func (s *Store) GetNodeTags(ctx context.Context, nodeID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM node_meta WHERE node_id=? AND key LIKE ? ORDER BY key`, nodeID, tagPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, strings.TrimPrefix(k, tagPrefix))
	}
	return out, rows.Err()
}

// ListAllTagsForStorage returns every distinct tag used in a storage.
func (s *Store) ListAllTagsForStorage(ctx context.Context, storageID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT m.key
		 FROM node_meta m
		 INNER JOIN nodes n ON n.id = m.node_id
		 WHERE n.storage_id=? AND n.deleted_at IS NULL AND m.key LIKE ?
		 ORDER BY m.key`, storageID, tagPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, strings.TrimPrefix(k, tagPrefix))
	}
	return out, rows.Err()
}

// ListAllTags returns every distinct tag across all storages (alphabetical).
func (s *Store) ListAllTags(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT m.key
		 FROM node_meta m
		 INNER JOIN nodes n ON n.id = m.node_id
		 WHERE n.deleted_at IS NULL AND m.key LIKE ?
		 ORDER BY m.key`, tagPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, strings.TrimPrefix(k, tagPrefix))
	}
	return out, rows.Err()
}

// ListNodesByTag returns non-deleted nodes carrying the given tag, newest-first.
func (s *Store) ListNodesByTag(ctx context.Context, tag string, limit int) ([]*model.Node, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT n.id, n.storage_id, n.parent_id, n.name, n.path, n.path_hash, COALESCE(n.storage_key,''), n.type, n.size, COALESCE(n.mime,''), COALESCE(n.etag,''), n.backend_mtime, n.db_mtime, n.sync_state, n.seen_at, n.deleted_at, n.created_at, n.updated_at
		 FROM node_meta m
		 INNER JOIN nodes n ON n.id = m.node_id
		 WHERE m.key=? AND n.deleted_at IS NULL
		 ORDER BY n.updated_at DESC
		 LIMIT ?`, tagPrefix+tag, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ─────────────────── Notifications ───────────────────

// InsertNotification persists a new in-app notification row. Webhook
// status starts at "pending" — Service.Send updates it after the HTTP
// attempt finishes. meta_json is normalized to "{}" when empty so the
// scan path always finds valid JSON.
func (s *Store) InsertNotification(ctx context.Context, n *model.NotificationInput) (int64, error) {
	if n == nil {
		return 0, errors.New("sqlite: nil notification")
	}
	if n.Event == "" || n.Severity == "" || n.Title == "" {
		return 0, errors.New("sqlite: notification missing event/severity/title")
	}
	meta := n.MetaJSON
	if len(meta) == 0 {
		meta = []byte("{}")
	}
	var userID any
	if n.UserID != nil {
		userID = *n.UserID
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO notifications (event, severity, title, body, meta_json, user_id, webhook_status)
		 VALUES (?,?,?,?,?,?,?)`,
		n.Event, n.Severity, n.Title, n.Body, string(meta), userID, "pending",
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: insert notification: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// GetNotification returns a single row by id.
func (s *Store) GetNotification(ctx context.Context, id int64) (*model.Notification, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, event, severity, title, body, meta_json,
		        user_id, read_at, webhook_status, COALESCE(webhook_error,''), created_at
		 FROM notifications WHERE id=?`, id)
	return scanNotification(row)
}

// ListNotifications paginates either a user's view (broadcasts +
// user-scoped) or admin/global view (userID == nil).
//
// onlyUnread filters read_at IS NULL.
func (s *Store) ListNotifications(ctx context.Context, userID *int64, onlyUnread bool, limit, offset int) ([]*model.Notification, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var (
		args   []any
		whereC []string
	)
	if userID != nil {
		whereC = append(whereC, "(user_id IS NULL OR user_id = ?)")
		args = append(args, *userID)
	}
	if onlyUnread {
		whereC = append(whereC, "read_at IS NULL")
	}
	whereSQL := ""
	if len(whereC) > 0 {
		whereSQL = "WHERE " + strings.Join(whereC, " AND ")
	}

	var total int64
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM notifications "+whereSQL, args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("sqlite: count notifications: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, event, severity, title, body, meta_json,
		        user_id, read_at, webhook_status, COALESCE(webhook_error,''), created_at
		 FROM notifications `+whereSQL+`
		 ORDER BY created_at DESC, id DESC
		 LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("sqlite: list notifications: %w", err)
	}
	defer rows.Close()
	var out []*model.Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, n)
	}
	return out, total, rows.Err()
}

// MarkNotificationRead bumps read_at on a single row. When userID is
// non-nil it must match the row (or the row must be a broadcast).
func (s *Store) MarkNotificationRead(ctx context.Context, id int64, userID *int64) error {
	q := `UPDATE notifications SET read_at = CURRENT_TIMESTAMP
	      WHERE id=? AND read_at IS NULL`
	args := []any{id}
	if userID != nil {
		q += ` AND (user_id IS NULL OR user_id = ?)`
		args = append(args, *userID)
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("sqlite: mark notif read: %w", err)
	}
	return nil
}

// MarkAllNotificationsRead bumps read_at for every unread row visible
// to userID. Pass nil for the global "mark all" admin sweep.
func (s *Store) MarkAllNotificationsRead(ctx context.Context, userID *int64) error {
	q := `UPDATE notifications SET read_at = CURRENT_TIMESTAMP WHERE read_at IS NULL`
	var args []any
	if userID != nil {
		q += ` AND (user_id IS NULL OR user_id = ?)`
		args = append(args, *userID)
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// UnreadNotificationCount returns the bell badge number for a user.
// Pass nil for the global unread count (admin dashboard).
func (s *Store) UnreadNotificationCount(ctx context.Context, userID *int64) (int64, error) {
	q := `SELECT COUNT(*) FROM notifications WHERE read_at IS NULL`
	var args []any
	if userID != nil {
		q += ` AND (user_id IS NULL OR user_id = ?)`
		args = append(args, *userID)
	}
	var n int64
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// UpdateWebhookStatus is invoked by Service.Send after the HTTP attempt
// chain completes (or skips).
func (s *Store) UpdateWebhookStatus(ctx context.Context, id int64, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notifications SET webhook_status=?, webhook_error=? WHERE id=?`,
		status, errMsg, id)
	if err != nil {
		return fmt.Errorf("sqlite: update webhook status: %w", err)
	}
	return nil
}

// GetNotificationSettings returns the per-user toggle. A missing row
// is treated as the default (in_app_enabled=true, no muted events).
func (s *Store) GetNotificationSettings(ctx context.Context, userID int64) (*model.NotificationSettings, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT user_id, in_app_enabled, muted_events
		 FROM notification_settings WHERE user_id=?`, userID)
	out := &model.NotificationSettings{UserID: userID, InAppEnabled: true, MutedEventsRaw: []byte("[]")}
	var (
		gotUser  int64
		enabled  int
		mutedRaw string
	)
	if err := row.Scan(&gotUser, &enabled, &mutedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		return nil, fmt.Errorf("sqlite: get notif settings: %w", err)
	}
	out.UserID = gotUser
	out.InAppEnabled = enabled != 0
	out.MutedEventsRaw = json.RawMessage(mutedRaw)
	if len(out.MutedEventsRaw) == 0 {
		out.MutedEventsRaw = []byte("[]")
	}
	return out, nil
}

// UpsertNotificationSettings stores the user's preferences.
func (s *Store) UpsertNotificationSettings(ctx context.Context, st *model.NotificationSettings) error {
	if st == nil || st.UserID == 0 {
		return errors.New("sqlite: invalid notif settings")
	}
	muted := st.MutedEventsRaw
	if len(muted) == 0 {
		muted = []byte("[]")
	}
	enabled := 0
	if st.InAppEnabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_settings (user_id, in_app_enabled, muted_events, updated_at)
		 VALUES (?,?,?, CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id) DO UPDATE SET
		   in_app_enabled = excluded.in_app_enabled,
		   muted_events   = excluded.muted_events,
		   updated_at     = CURRENT_TIMESTAMP`,
		st.UserID, enabled, string(muted))
	if err != nil {
		return fmt.Errorf("sqlite: upsert notif settings: %w", err)
	}
	return nil
}

// ─────────────────── Replica ───────────────────

// ListReplicaRules returns the rule list ordered priority asc.
func (s *Store) ListReplicaRules(ctx context.Context) ([]*model.ReplicaRule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, path_pattern, mode, priority, enabled, description, created_at, updated_at
		 FROM replica_rules
		 ORDER BY priority ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list replica rules: %w", err)
	}
	defer rows.Close()
	var out []*model.ReplicaRule
	for rows.Next() {
		r := &model.ReplicaRule{}
		var enabled int
		if err := rows.Scan(&r.ID, &r.PathPattern, &r.Mode, &r.Priority, &enabled, &r.Description, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetReplicaRule returns a single rule by id.
func (s *Store) GetReplicaRule(ctx context.Context, id int64) (*model.ReplicaRule, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, path_pattern, mode, priority, enabled, description, created_at, updated_at
		 FROM replica_rules WHERE id=?`, id)
	r := &model.ReplicaRule{}
	var enabled int
	if err := row.Scan(&r.ID, &r.PathPattern, &r.Mode, &r.Priority, &enabled, &r.Description, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.Enabled = enabled != 0
	return r, nil
}

// CreateReplicaRule inserts a new rule.
func (s *Store) CreateReplicaRule(ctx context.Context, in *model.ReplicaRuleInput) (*model.ReplicaRule, error) {
	if err := validateReplicaRule(in); err != nil {
		return nil, err
	}
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO replica_rules (path_pattern, mode, priority, enabled, description)
		 VALUES (?,?,?,?,?)`,
		in.PathPattern, in.Mode, in.Priority, enabled, in.Description)
	if err != nil {
		return nil, fmt.Errorf("sqlite: create replica rule: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetReplicaRule(ctx, id)
}

// UpdateReplicaRule replaces a rule's fields by id.
func (s *Store) UpdateReplicaRule(ctx context.Context, id int64, in *model.ReplicaRuleInput) (*model.ReplicaRule, error) {
	if err := validateReplicaRule(in); err != nil {
		return nil, err
	}
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE replica_rules
		 SET path_pattern=?, mode=?, priority=?, enabled=?, description=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		in.PathPattern, in.Mode, in.Priority, enabled, in.Description, id)
	if err != nil {
		return nil, fmt.Errorf("sqlite: update replica rule: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, sql.ErrNoRows
	}
	return s.GetReplicaRule(ctx, id)
}

// DeleteReplicaRule removes a rule.
func (s *Store) DeleteReplicaRule(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM replica_rules WHERE id=?`, id)
	return err
}

// UpsertReplicaFailure either inserts a new failure or bumps attempts
// + last_attempt_at + the latest error code/message for the existing
// (path, op) row. Idempotent under retry.
func (s *Store) UpsertReplicaFailure(ctx context.Context, path, op, errCode, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO replica_failures (path, op, error_code, error_msg, attempts, last_attempt_at)
		 VALUES (?,?,?,?,1, CURRENT_TIMESTAMP)
		 ON CONFLICT(path, op) DO UPDATE SET
		   error_code      = excluded.error_code,
		   error_msg       = excluded.error_msg,
		   attempts        = replica_failures.attempts + 1,
		   last_attempt_at = CURRENT_TIMESTAMP,
		   resolved_at     = NULL`,
		path, op, errCode, errMsg)
	if err != nil {
		return fmt.Errorf("sqlite: upsert replica failure: %w", err)
	}
	return nil
}

// ResolveReplicaFailure stamps resolved_at on the matching row.
// Missing rows are a no-op.
func (s *Store) ResolveReplicaFailure(ctx context.Context, path, op string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE replica_failures SET resolved_at = CURRENT_TIMESTAMP
		 WHERE path=? AND op=? AND resolved_at IS NULL`, path, op)
	return err
}

// ListReplicaFailures paginates either all rows or only unresolved.
func (s *Store) ListReplicaFailures(ctx context.Context, onlyUnresolved bool, limit, offset int) ([]*model.ReplicaFailure, int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	whereSQL := ""
	if onlyUnresolved {
		whereSQL = "WHERE resolved_at IS NULL"
	}
	var total int64
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM replica_failures "+whereSQL,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("sqlite: count replica failures: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, path, op, error_code, error_msg, attempts, last_attempt_at, resolved_at
		 FROM replica_failures `+whereSQL+`
		 ORDER BY last_attempt_at DESC, id DESC
		 LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("sqlite: list replica failures: %w", err)
	}
	defer rows.Close()

	var out []*model.ReplicaFailure
	for rows.Next() {
		f := &model.ReplicaFailure{}
		var resolvedAt sql.NullTime
		if err := rows.Scan(&f.ID, &f.Path, &f.Op, &f.ErrorCode, &f.ErrorMsg, &f.Attempts, &f.LastAttemptAt, &resolvedAt); err != nil {
			return nil, 0, err
		}
		if resolvedAt.Valid {
			t := resolvedAt.Time
			f.ResolvedAt = &t
		}
		out = append(out, f)
	}
	return out, total, rows.Err()
}

// CountUnresolvedReplicaFailures returns the unresolved count
// directly — cheaper than List for the dashboard counter.
func (s *Store) CountUnresolvedReplicaFailures(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM replica_failures WHERE resolved_at IS NULL`,
	).Scan(&n)
	return n, err
}

// CountRecentlyResolvedReplicaFailures returns the number of rows
// whose resolved_at is more recent than `since`. Used by the cron
// status report's "repaired_count" metric.
func (s *Store) CountRecentlyResolvedReplicaFailures(ctx context.Context, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM replica_failures
		 WHERE resolved_at IS NOT NULL AND resolved_at >= ?`, since,
	).Scan(&n)
	return n, err
}

// UpsertReplicaStatusReport replaces (id=1) the singleton report row.
func (s *Store) UpsertReplicaStatusReport(ctx context.Context, total, failed, repaired int64, summaryJSON []byte) error {
	if len(summaryJSON) == 0 {
		summaryJSON = []byte("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO replica_status_reports (id, generated_at, total_files, failed_count, repaired_count, summary_json)
		 VALUES (1, CURRENT_TIMESTAMP, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   generated_at   = CURRENT_TIMESTAMP,
		   total_files    = excluded.total_files,
		   failed_count   = excluded.failed_count,
		   repaired_count = excluded.repaired_count,
		   summary_json   = excluded.summary_json`,
		total, failed, repaired, string(summaryJSON))
	if err != nil {
		return fmt.Errorf("sqlite: upsert replica status report: %w", err)
	}
	return nil
}

// GetReplicaStatusReport returns the singleton row. nil + nil err
// when no report has been generated yet.
func (s *Store) GetReplicaStatusReport(ctx context.Context) (*model.ReplicaStatusReport, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT generated_at, total_files, failed_count, repaired_count, summary_json
		 FROM replica_status_reports WHERE id=1`)
	out := &model.ReplicaStatusReport{}
	var summaryRaw string
	if err := row.Scan(&out.GeneratedAt, &out.TotalFiles, &out.FailedCount, &out.RepairedCount, &summaryRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite: get replica status report: %w", err)
	}
	out.SummaryJSON = json.RawMessage(summaryRaw)
	if len(out.SummaryJSON) == 0 {
		out.SummaryJSON = []byte("{}")
	}
	return out, nil
}

// GetReplicaSettings returns the singleton row. Missing row maps to
// defaults (mirror, no cron).
func (s *Store) GetReplicaSettings(ctx context.Context) (*model.ReplicaSettings, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT report_cron, report_enabled, default_mode FROM replica_settings WHERE id=1`)
	out := &model.ReplicaSettings{DefaultMode: model.ReplicaModeMirror}
	var enabled int
	if err := row.Scan(&out.ReportCron, &enabled, &out.DefaultMode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		return nil, fmt.Errorf("sqlite: get replica settings: %w", err)
	}
	out.ReportEnabled = enabled != 0
	return out, nil
}

// UpsertReplicaSettings stores the singleton config row.
func (s *Store) UpsertReplicaSettings(ctx context.Context, st *model.ReplicaSettings) error {
	if st == nil {
		return errors.New("sqlite: nil replica settings")
	}
	if st.DefaultMode == "" {
		st.DefaultMode = model.ReplicaModeMirror
	}
	enabled := 0
	if st.ReportEnabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO replica_settings (id, report_cron, report_enabled, default_mode, updated_at)
		 VALUES (1, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(id) DO UPDATE SET
		   report_cron    = excluded.report_cron,
		   report_enabled = excluded.report_enabled,
		   default_mode   = excluded.default_mode,
		   updated_at     = CURRENT_TIMESTAMP`,
		st.ReportCron, enabled, st.DefaultMode)
	if err != nil {
		return fmt.Errorf("sqlite: upsert replica settings: %w", err)
	}
	return nil
}

func validateReplicaRule(in *model.ReplicaRuleInput) error {
	if in == nil {
		return errors.New("nil rule")
	}
	if in.PathPattern == "" {
		return errors.New("path_pattern required")
	}
	switch in.Mode {
	case model.ReplicaModeMirror, model.ReplicaModeAppendOnly, model.ReplicaModeSkip:
	default:
		return fmt.Errorf("invalid mode %q (mirror | append_only | skip)", in.Mode)
	}
	return nil
}

// scanNotification accepts both *sql.Row and *sql.Rows (rowScanner).
func scanNotification(rs interface {
	Scan(...any) error
}) (*model.Notification, error) {
	n := &model.Notification{}
	var (
		metaRaw string
		userID  sql.NullInt64
		readAt  sql.NullTime
		errMsg  string
	)
	if err := rs.Scan(
		&n.ID, &n.Event, &n.Severity, &n.Title, &n.Body, &metaRaw,
		&userID, &readAt, &n.WebhookStatus, &errMsg, &n.CreatedAt,
	); err != nil {
		return nil, err
	}
	if metaRaw == "" {
		metaRaw = "{}"
	}
	n.MetaJSON = json.RawMessage(metaRaw)
	if userID.Valid {
		v := userID.Int64
		n.UserID = &v
	}
	if readAt.Valid {
		t := readAt.Time
		n.ReadAt = &t
	}
	n.WebhookError = errMsg
	return n, nil
}
