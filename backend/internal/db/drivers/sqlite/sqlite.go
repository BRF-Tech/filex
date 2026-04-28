// Package sqlite is the SQLite DB driver. Uses modernc.org/sqlite
// (pure Go, CGO_ENABLED=0).
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/model"

	sqlite_migrations "gitlab.com/brftech/filemanager/backend/db/migrations/sqlite"
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
		`INSERT INTO storages (name, driver, mount_path, config_json, sync_mode, sync_interval_s, enabled, read_only)
		 VALUES (?,?,?,?,?,?,?,?)`,
		st.Name, st.Driver, st.MountPath, string(cfg), st.SyncMode, st.SyncIntervalS, btoi(st.Enabled), btoi(st.ReadOnly))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetStorage(ctx, id)
}

func (s *Store) GetStorage(ctx context.Context, id int64) (*model.Storage, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, driver, mount_path, config_json, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at FROM storages WHERE id=?`, id)
	return scanStorage(row)
}

func (s *Store) GetStorageByName(ctx context.Context, name string) (*model.Storage, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, driver, mount_path, config_json, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at FROM storages WHERE name=?`, name)
	return scanStorage(row)
}

func (s *Store) ListStorages(ctx context.Context) ([]*model.Storage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, driver, mount_path, config_json, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at FROM storages ORDER BY id`)
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, driver, mount_path, config_json, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at FROM storages WHERE enabled=1 ORDER BY id`)
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
	_, err := s.db.ExecContext(ctx,
		`UPDATE storages SET name=?, driver=?, mount_path=?, config_json=?, sync_mode=?, sync_interval_s=?, enabled=?, read_only=? WHERE id=?`,
		st.Name, st.Driver, st.MountPath, string(cfg), st.SyncMode, st.SyncIntervalS, btoi(st.Enabled), btoi(st.ReadOnly), st.ID)
	return err
}

func (s *Store) UpdateStorageSyncCursor(ctx context.Context, id int64, at time.Time, token string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE storages SET last_sync_at=?, last_sync_token=? WHERE id=?`, at, token, id)
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
	rows, err := s.db.QueryContext(ctx, nodeSelectColumns()+` FROM nodes WHERE storage_id=? AND seen_at < ? AND deleted_at IS NULL`, storageID, before)
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

// ─────────────────── Shares ───────────────────

func (s *Store) CreateShare(ctx context.Context, sh *model.Share) (*model.Share, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO shares (node_id, token, pin_hash, expires_at, max_downloads, created_by) VALUES (?,?,?,?,?,?)`,
		sh.NodeID, sh.Token, sh.PinHash, sh.ExpiresAt, sh.MaxDownloads, sh.CreatedBy)
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
	row := s.db.QueryRowContext(ctx, `SELECT id, node_id, token, COALESCE(pin_hash,''), expires_at, max_downloads, download_count, created_by, created_at FROM shares WHERE token=?`, token)
	return scanShare(row)
}

func (s *Store) GetShareByID(ctx context.Context, id int64) (*model.Share, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, node_id, token, COALESCE(pin_hash,''), expires_at, max_downloads, download_count, created_by, created_at FROM shares WHERE id=?`, id)
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
		        COALESCE(u.email,''), COALESCE(n.path,'')
		 FROM shares s
		 LEFT JOIN users u ON u.id=s.created_by
		 LEFT JOIN nodes n ON n.id=s.node_id
		 WHERE `+whereSQL+` ORDER BY s.created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*db.ShareWithMeta
	for rows.Next() {
		sh := &model.Share{}
		var creatorEmail, nodePath string
		if err := rows.Scan(&sh.ID, &sh.NodeID, &sh.Token, &sh.PinHash, &sh.ExpiresAt, &sh.MaxDownloads, &sh.DownloadCount, &sh.CreatedBy, &sh.CreatedAt, &creatorEmail, &nodePath); err != nil {
			return nil, 0, err
		}
		sh.HasPin = sh.PinHash != ""
		out = append(out, &db.ShareWithMeta{Share: sh, CreatorEmail: creatorEmail, NodePath: nodePath})
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, node_id, token, COALESCE(pin_hash,''), expires_at, max_downloads, download_count, created_by, created_at FROM shares WHERE node_id=? ORDER BY created_at DESC`, nodeID)
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
func (s *Store) ListSyncRunsAcrossAll(ctx context.Context, storageID int64, status string, limit, offset int) ([]*model.SyncRun, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	where := []string{"1=1"}
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
	err := r.Scan(&st.ID, &st.Name, &st.Driver, &st.MountPath, &cfg, &st.SyncMode, &st.SyncIntervalS, &st.LastSyncAt, &st.LastSyncToken, &st.Enabled, &st.ReadOnly, &st.CreatedAt)
	if err != nil {
		return nil, err
	}
	st.ConfigJSON = []byte(cfg)
	return st, nil
}

func userSelect() string {
	return `SELECT id, email, COALESCE(password_hash,''), role, COALESCE(totp_secret,''), COALESCE(totp_pending_secret,''), COALESCE(totp_enabled,0), COALESCE(totp_recovery_codes_json,'[]'), locale, timezone, created_at, updated_at, last_login_at`
}

func scanUser(r rowScanner) (*model.User, error) {
	u := &model.User{}
	var totpEnabled int
	var recoveryJSON string
	if err := r.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.TOTPSecret, &u.TOTPPendingSecret, &totpEnabled, &recoveryJSON, &u.Locale, &u.Timezone, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt); err != nil {
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
	if err := r.Scan(&sh.ID, &sh.NodeID, &sh.Token, &sh.PinHash, &sh.ExpiresAt, &sh.MaxDownloads, &sh.DownloadCount, &sh.CreatedBy, &sh.CreatedAt); err != nil {
		return nil, err
	}
	sh.HasPin = sh.PinHash != ""
	return sh, nil
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
