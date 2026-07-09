// Package postgres is the PostgreSQL DB driver.
//
// Uses jackc/pgx/v5 via the stdlib database/sql interface. Most query
// implementations mirror the SQLite Store but adapted to use $N placeholders
// and JSONB column types.
//
// To keep the skeleton compact, this file embeds the pgx-flavoured Store
// methods only as forwarding shells — the actual SQL bodies live inline
// here and are functionally identical to the SQLite version with the
// dialect-specific tweaks. Any TODO marker indicates a path that needs the
// full pg-specific tuning before going to production.
package postgres

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

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"

	postgres_migrations "github.com/brf-tech/filex/backend/db/migrations/postgres"
)

func init() {
	db.Register("postgres", func() db.Driver { return &Driver{} })
}

// Driver implements db.Driver for Postgres.
type Driver struct{}

// Name implements db.Driver.
func (Driver) Name() string { return "postgres" }

// Dialect for goose.
func (Driver) Dialect() string { return "postgres" }

// MigrationsFS returns the embedded Postgres migrations.
func (Driver) MigrationsFS() embed.FS { return postgres_migrations.FS }

// Open returns a configured *sql.DB. DSN follows pgx semantics, e.g.
// `postgres://user:pass@host:5432/dbname?sslmode=require`.
func (Driver) Open(_ context.Context, dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, errors.New("postgres: empty DSN")
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}
	conn.SetMaxOpenConns(20)
	conn.SetMaxIdleConns(4)
	conn.SetConnMaxIdleTime(5 * time.Minute)
	return conn, nil
}

// NewStore returns a Store backed by the given *sql.DB.
func (Driver) NewStore(sqlDB *sql.DB) db.Store {
	return &Store{db: sqlDB}
}

// Store implements db.Store atop Postgres.
//
// NOTE: This is a stub for the parts of the surface that have non-trivial
// dialect-specific quirks. The methods that already work cleanly with
// $N substitution are filled in; the rest delegate to TODOs that callers
// will recognize.
type Store struct {
	db *sql.DB
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) Close() error                   { return s.db.Close() }

// ─────────────────── Storages ───────────────────

func (s *Store) CreateStorage(ctx context.Context, st *model.Storage) (*model.Storage, error) {
	cfg := st.ConfigJSON
	if len(cfg) == 0 {
		cfg = []byte("{}")
	}
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO storages (name, driver, mount_path, config_json, sync_mode, sync_interval_s, enabled, read_only, rbac_enabled)
		 VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7,$8,$9) RETURNING id, name, driver, mount_path, config_json::text, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at, COALESCE(role,'primary'), replica_of_id, COALESCE(replica_mode,'async'), replica_target_id, rbac_enabled`,
		st.Name, st.Driver, st.MountPath, string(cfg), st.SyncMode, st.SyncIntervalS, st.Enabled, st.ReadOnly, st.RBACEnabled)
	return scanStorage(row)
}

func (s *Store) GetStorage(ctx context.Context, id int64) (*model.Storage, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, driver, mount_path, config_json::text, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at, COALESCE(role,'primary'), replica_of_id, COALESCE(replica_mode,'async'), replica_target_id, rbac_enabled FROM storages WHERE id=$1`, id)
	return scanStorage(row)
}

func (s *Store) GetStorageByName(ctx context.Context, name string) (*model.Storage, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, driver, mount_path, config_json::text, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at, COALESCE(role,'primary'), replica_of_id, COALESCE(replica_mode,'async'), replica_target_id, rbac_enabled FROM storages WHERE name=$1`, name)
	return scanStorage(row)
}

func (s *Store) ListStorages(ctx context.Context) ([]*model.Storage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, driver, mount_path, config_json::text, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at, COALESCE(role,'primary'), replica_of_id, COALESCE(replica_mode,'async'), replica_target_id, rbac_enabled FROM storages ORDER BY id`)
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, driver, mount_path, config_json::text, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at, COALESCE(role,'primary'), replica_of_id, COALESCE(replica_mode,'async'), replica_target_id, rbac_enabled FROM storages WHERE enabled=true ORDER BY id`)
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
		`UPDATE storages SET name=$1, driver=$2, mount_path=$3, config_json=$4::jsonb, sync_mode=$5, sync_interval_s=$6, enabled=$7, read_only=$8, rbac_enabled=$9, role=$10, replica_of_id=$11, replica_mode=$12, replica_target_id=$13 WHERE id=$14`,
		st.Name, st.Driver, st.MountPath, string(cfg), st.SyncMode, st.SyncIntervalS,
		st.Enabled, st.ReadOnly, st.RBACEnabled,
		role, st.ReplicaOfID, repMode, st.ReplicaTargetID,
		st.ID)
	return err
}

func (s *Store) UpdateStorageSyncCursor(ctx context.Context, id int64, at time.Time, token string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE storages SET last_sync_at=$1, last_sync_token=$2 WHERE id=$3`, at, token, id)
	return err
}

func (s *Store) DeleteStorage(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM storages WHERE id=$1`, id)
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
		`SELECT id, name, driver, config_json::text, mode, enabled, created_at, updated_at
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
		`SELECT id, name, driver, config_json::text, mode, enabled, created_at, updated_at
		   FROM replication_targets WHERE id=$1`, id)
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
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO replication_targets (name, driver, config_json, mode, enabled)
		 VALUES ($1, $2, $3::jsonb, $4, $5)
		 RETURNING id, name, driver, config_json::text, mode, enabled, created_at, updated_at`,
		rt.Name, rt.Driver, string(cfg), mode, rt.Enabled,
	)
	return scanReplicationTarget(row)
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
		    SET name=$1, driver=$2, config_json=$3::jsonb, mode=$4, enabled=$5, updated_at=NOW()
		  WHERE id=$6`,
		rt.Name, rt.Driver, string(cfg), mode, rt.Enabled, rt.ID)
	return err
}

func (s *Store) DeleteReplicationTarget(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE storages SET replica_target_id=NULL WHERE replica_target_id=$1`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM replication_targets WHERE id=$1`, id)
	return err
}

// ─────────────────── Nodes (key paths only) ───────────────────

func (s *Store) CreateNode(ctx context.Context, n *model.Node) (*model.Node, error) {
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO nodes (storage_id, parent_id, name, path, path_hash, storage_key, type, size, mime, etag, backend_mtime, sync_state)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		 RETURNING `+nodeColumns(),
		n.StorageID, n.ParentID, n.Name, n.Path, n.PathHash, n.StorageKey, n.Type, n.Size, n.Mime, n.Etag, n.BackendMtime, n.SyncState)
	return scanNode(row)
}

func (s *Store) GetNode(ctx context.Context, id int64) (*model.Node, error) {
	return scanNode(s.db.QueryRowContext(ctx, `SELECT `+nodeColumns()+` FROM nodes WHERE id=$1`, id))
}

func (s *Store) GetNodeByPath(ctx context.Context, storageID int64, hash string) (*model.Node, error) {
	return scanNode(s.db.QueryRowContext(ctx, `SELECT `+nodeColumns()+` FROM nodes WHERE storage_id=$1 AND path_hash=$2 AND deleted_at IS NULL`, storageID, hash))
}

func (s *Store) GetNodeByPathIncludingDeleted(ctx context.Context, storageID int64, hash string) (*model.Node, error) {
	return scanNode(s.db.QueryRowContext(ctx, `SELECT `+nodeColumns()+` FROM nodes WHERE storage_id=$1 AND path_hash=$2`, storageID, hash))
}

func (s *Store) ListNodesByParent(ctx context.Context, storageID int64, parentID *int64) ([]*model.Node, error) {
	q := `SELECT ` + nodeColumns() + ` FROM nodes WHERE storage_id=$1 AND deleted_at IS NULL AND parent_id `
	args := []any{storageID}
	if parentID == nil {
		q += `IS NULL`
	} else {
		q += `=$2`
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
		`SELECT id, parent_id, type, size, backend_mtime FROM nodes WHERE storage_id=$1 AND deleted_at IS NULL`,
		storageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.NodeAgg
	for rows.Next() {
		var n db.NodeAgg
		var typ string
		if err := rows.Scan(&n.ID, &n.ParentID, &typ, &n.Size, &n.Mtime); err != nil {
			return nil, err
		}
		n.IsDir = typ == string(model.NodeTypeDirectory)
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) SetNodeSize(ctx context.Context, id int64, size int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET size=$1, updated_at=NOW() WHERE id=$2`, size, id)
	return err
}

func (s *Store) SetNodeMtime(ctx context.Context, id int64, mtime *time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET backend_mtime=$1, updated_at=NOW() WHERE id=$2`, mtime, id)
	return err
}

// ─── Providers (tenants) — see docs/MULTI-TENANCY.md ─────────────────

const providerCols = `id, slug, name, COALESCE(host,''), auth_type, ` +
	`COALESCE(oidc_issuer,''), COALESCE(oidc_client_id,''), COALESCE(oidc_client_secret,''), ` +
	`COALESCE(oidc_redirect_url,''), COALESCE(role_claim,''), COALESCE(admin_group,''), ` +
	`COALESCE(cookie_domain,''), is_supertenant, enabled, created_at, updated_at`

func scanProvider(r rowScanner) (*model.Provider, error) {
	p := &model.Provider{}
	if err := r.Scan(&p.ID, &p.Slug, &p.Name, &p.Host, &p.AuthType,
		&p.OIDCIssuer, &p.OIDCClientID, &p.OIDCClientSecret, &p.OIDCRedirectURL,
		&p.RoleClaim, &p.AdminGroup, &p.CookieDomain, &p.IsSupertenant, &p.Enabled,
		&p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) CreateProvider(ctx context.Context, p *model.Provider) (*model.Provider, error) {
	at := p.AuthType
	if at == "" {
		at = model.AuthTypeOIDC
	}
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO providers (slug, name, host, auth_type, oidc_issuer, oidc_client_id, oidc_client_secret, oidc_redirect_url, role_claim, admin_group, cookie_domain, is_supertenant, enabled)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING `+providerCols,
		p.Slug, p.Name, p.Host, at, p.OIDCIssuer, p.OIDCClientID, p.OIDCClientSecret,
		p.OIDCRedirectURL, p.RoleClaim, p.AdminGroup, p.CookieDomain, p.IsSupertenant, p.Enabled)
	return scanProvider(row)
}

func (s *Store) GetProvider(ctx context.Context, id int64) (*model.Provider, error) {
	return scanProvider(s.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE id=$1`, id))
}

func (s *Store) GetProviderBySlug(ctx context.Context, slug string) (*model.Provider, error) {
	p, err := scanProvider(s.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE slug=$1`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (s *Store) GetProviderByHost(ctx context.Context, host string) (*model.Provider, error) {
	p, err := scanProvider(s.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE host=$1 AND enabled=TRUE`, host))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (s *Store) GetSupertenant(ctx context.Context) (*model.Provider, error) {
	p, err := scanProvider(s.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE is_supertenant=TRUE ORDER BY id LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (s *Store) ListProviders(ctx context.Context) ([]*model.Provider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+providerCols+` FROM providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdateProvider(ctx context.Context, p *model.Provider) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE providers SET slug=$1, name=$2, host=$3, auth_type=$4, oidc_issuer=$5, oidc_client_id=$6, oidc_client_secret=$7, oidc_redirect_url=$8, role_claim=$9, admin_group=$10, cookie_domain=$11, is_supertenant=$12, enabled=$13, updated_at=NOW() WHERE id=$14`,
		p.Slug, p.Name, p.Host, p.AuthType, p.OIDCIssuer, p.OIDCClientID, p.OIDCClientSecret,
		p.OIDCRedirectURL, p.RoleClaim, p.AdminGroup, p.CookieDomain, p.IsSupertenant, p.Enabled, p.ID)
	return err
}

func (s *Store) DeleteProvider(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM providers WHERE id=$1`, id)
	return err
}

func (s *Store) LinkProviderStorage(ctx context.Context, providerID, storageID int64) error {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM provider_storages WHERE provider_id=$1 AND storage_id=$2`,
		providerID, storageID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO provider_storages (provider_id, storage_id) VALUES ($1,$2)`, providerID, storageID)
	return err
}

func (s *Store) UnlinkProviderStorage(ctx context.Context, providerID, storageID int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM provider_storages WHERE provider_id=$1 AND storage_id=$2`, providerID, storageID)
	return err
}

func (s *Store) ListProviderStorageIDs(ctx context.Context, providerID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT storage_id FROM provider_storages WHERE provider_id=$1 ORDER BY storage_id`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) GetProviderIDForStorage(ctx context.Context, storageID int64) (int64, bool, error) {
	var pid int64
	err := s.db.QueryRowContext(ctx,
		`SELECT provider_id FROM provider_storages WHERE storage_id=$1 ORDER BY provider_id LIMIT 1`,
		storageID).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return pid, true, nil
}

func (s *Store) UpdateNodeMeta(ctx context.Context, id int64, size int64, mime, etag string, mtime time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET size=$1, mime=$2, etag=$3, backend_mtime=$4, seen_at=NOW(), updated_at=NOW() WHERE id=$5`, size, mime, etag, mtime, id)
	return err
}

func (s *Store) TouchNodeSeen(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET seen_at=NOW() WHERE id=$1`, id)
	return err
}

// SoftDeleteAndRetag — see store.go interface for semantics.
func (s *Store) SoftDeleteAndRetag(ctx context.Context, id int64, trashPath, trashHash, origPath string) error {
	base := path.Base(trashPath)
	_, err := s.db.ExecContext(ctx, `
		UPDATE nodes
		SET deleted_at=NOW(), updated_at=NOW(), parent_id=NULL,
		    name=$1, path=$2, path_hash=$3, storage_key=$4
		WHERE id=$5`, base, trashPath, trashHash, origPath, id)
	return err
}

func (s *Store) SoftDeleteNode(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET deleted_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *Store) HardDeleteNode(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id=$1`, id)
	return err
}

func (s *Store) MoveNode(ctx context.Context, id int64, parentID *int64, name, path, hash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET parent_id=$1, name=$2, path=$3, path_hash=$4, updated_at=NOW() WHERE id=$5`, parentID, name, path, hash, id)
	return err
}

func (s *Store) ListStaleNodes(ctx context.Context, storageID int64, before time.Time) ([]*model.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeColumns()+` FROM nodes WHERE storage_id=$1 AND seen_at < $2 AND deleted_at IS NULL`, storageID, before)
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
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE storage_id=$1 AND deleted_at IS NULL`, storageID).Scan(&n)
	return n, err
}

func (s *Store) StorageStats(ctx context.Context, storageID int64) (int64, int64, error) {
	var (
		count int64
		size  sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM nodes
		   WHERE storage_id=$1 AND type='file' AND deleted_at IS NULL`,
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
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeColumns()+` FROM nodes WHERE storage_id=$1 AND name ILIKE $2 AND deleted_at IS NULL ORDER BY name LIMIT $3`, storageID, like, limit)
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

// ─────────────────── Users / sessions / shares / chunks / sync / audit / settings ───────────────────
//
// To keep this file readable, the methods below mirror the SQLite Store
// 1:1 with $N placeholders and NOW()/CURRENT_TIMESTAMP swaps. They are
// intentionally lightweight — no fancy Postgres-specific optimization yet.

// userCols is the canonical user column projection shared by every user
// read path so scanUser always receives the same shape. It includes the
// full TOTP state (enabled flag + pending/active secret + recovery codes)
// — earlier this projection omitted totp_enabled, which silently disabled
// the login second-factor check on Postgres.
const userCols = `id, email, COALESCE(display_name,''), COALESCE(password_hash,''), role, ` +
	`COALESCE(totp_secret,''), COALESCE(totp_pending_secret,''), COALESCE(totp_enabled,FALSE), ` +
	`COALESCE(totp_recovery_codes_json::text,'[]'), locale, timezone, created_at, updated_at, last_login_at, ` +
	`provider_id, COALESCE(oidc_subject,'')`

func (s *Store) CreateUser(ctx context.Context, email, hash, role, locale, tz string) (*model.User, error) {
	// New users default to the always-present "default" provider (the
	// supertenant); OIDC JIT overrides via SetUserProvider. See sqlite driver.
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO users (email, password_hash, role, locale, timezone, provider_id)
		 VALUES ($1,$2,$3,$4,$5, (SELECT id FROM providers WHERE slug='default'))
		 RETURNING `+userCols,
		email, hash, role, locale, tz)
	return scanUser(row)
}

func (s *Store) SetUserProvider(ctx context.Context, userID, providerID int64, oidcSubject string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET provider_id=$1, oidc_subject=$2, updated_at=NOW() WHERE id=$3`,
		providerID, oidcSubject, userID)
	return err
}

func (s *Store) GetUserByProviderEmail(ctx context.Context, providerID int64, email string) (*model.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE provider_id=$1 AND email=$2`, providerID, email))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

func (s *Store) ListUsersByProvider(ctx context.Context, providerID int64) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users WHERE provider_id=$1 ORDER BY id`, providerID)
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

func (s *Store) GetUser(ctx context.Context, id int64) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE email=$1`, email))
}

func (s *Store) ListUsers(ctx context.Context) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
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
	_, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash=$1, updated_at=NOW() WHERE id=$2`, hash, id)
	return err
}

func (s *Store) UpdateUserLocale(ctx context.Context, id int64, locale, tz string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET locale=$1, timezone=$2, updated_at=NOW() WHERE id=$3`, locale, tz, id)
	return err
}

func (s *Store) UpdateUserRole(ctx context.Context, id int64, role string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET role=$1, updated_at=NOW() WHERE id=$2`, role, id)
	return err
}

func (s *Store) TouchLastLogin(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, id)
	return err
}

func (s *Store) CreateSession(ctx context.Context, userID int64, token string, expiresAt time.Time, ip, ua string) (*model.Session, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO sessions (user_id, token, expires_at, ip, user_agent) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		userID, token, expiresAt, ip, ua).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &model.Session{ID: id, UserID: userID, Token: token, ExpiresAt: expiresAt, IP: ip, UserAgent: ua, CreatedAt: time.Now()}, nil
}

func (s *Store) GetSessionByToken(ctx context.Context, token string) (*model.Session, error) {
	out := &model.Session{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, token, expires_at, COALESCE(ip,''), COALESCE(user_agent,''), created_at FROM sessions WHERE token=$1 AND expires_at > NOW()`,
		token).Scan(&out.ID, &out.UserID, &out.Token, &out.ExpiresAt, &out.IP, &out.UserAgent, &out.CreatedAt)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token=$1`, token)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= NOW()`)
	return err
}

// ─────────────────── API tokens ───────────────────

func (s *Store) CreateAPIToken(ctx context.Context, t *model.APIToken) (*model.APIToken, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO api_tokens (user_id, label, token_hash, scopes, usernames, expires_at) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		t.UserID, t.Label, t.TokenHash, t.Scopes, t.Usernames, t.ExpiresAt).Scan(&id)
	if err != nil {
		return nil, err
	}
	t.ID = id
	t.CreatedAt = time.Now()
	return t, nil
}

func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash string) (*model.APIToken, error) {
	return scanAPIToken(s.db.QueryRowContext(ctx,
		`SELECT id, user_id, label, token_hash, scopes, COALESCE(usernames,''), last_used_at, expires_at, created_at FROM api_tokens WHERE token_hash=$1`,
		tokenHash))
}

func (s *Store) ListAPITokens(ctx context.Context) ([]*model.APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, label, token_hash, scopes, COALESCE(usernames,''), last_used_at, expires_at, created_at FROM api_tokens ORDER BY created_at DESC`)
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
		`SELECT id, user_id, label, token_hash, scopes, COALESCE(usernames,''), last_used_at, expires_at, created_at FROM api_tokens WHERE user_id=$1 ORDER BY created_at DESC`,
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
	_, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=NOW() WHERE id=$1`, id)
	return err
}

// UpdateAPITokenMeta edits label and/or the username allow-list. nil = keep.
func (s *Store) UpdateAPITokenMeta(ctx context.Context, id int64, label, usernames *string) error {
	if label != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET label=$1 WHERE id=$2`, *label, id); err != nil {
			return err
		}
	}
	if usernames != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET usernames=$1 WHERE id=$2`, *usernames, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteAPIToken(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id=$1`, id)
	return err
}

func scanAPIToken(r rowScanner) (*model.APIToken, error) {
	t := &model.APIToken{}
	var lastUsed, expires sql.NullTime
	if err := r.Scan(&t.ID, &t.UserID, &t.Label, &t.TokenHash, &t.Scopes, &t.Usernames, &lastUsed, &expires, &t.CreatedAt); err != nil {
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
	rows, err := s.db.QueryContext(ctx, `SELECT `+fileGrantCols+` FROM file_grants WHERE storage_id=$1 AND user_id=$2`, storageID, userID)
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
	rows, err := s.db.QueryContext(ctx, `SELECT `+fileGrantCols+` FROM file_grants WHERE storage_id=$1 ORDER BY path_prefix, user_id`, storageID)
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
	return scanFileGrant(s.db.QueryRowContext(ctx, `SELECT `+fileGrantCols+` FROM file_grants WHERE id=$1`, id))
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

func (s *Store) CreateFileGrant(ctx context.Context, g *model.FileGrant) (*model.FileGrant, error) {
	return scanFileGrant(s.db.QueryRowContext(ctx,
		`INSERT INTO file_grants (storage_id, path_prefix, is_dir, user_id, level, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (storage_id, path_prefix, user_id)
		 DO UPDATE SET level=EXCLUDED.level, is_dir=EXCLUDED.is_dir, created_by=EXCLUDED.created_by
		 RETURNING `+fileGrantCols,
		g.StorageID, g.PathPrefix, g.IsDir, g.UserID, g.Level, g.CreatedBy))
}

func (s *Store) UpdateFileGrantLevel(ctx context.Context, id int64, level string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE file_grants SET level=$1 WHERE id=$2`, level, id)
	return err
}

func (s *Store) DeleteFileGrant(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM file_grants WHERE id=$1`, id)
	return err
}

func (s *Store) CreateShare(ctx context.Context, sh *model.Share) (*model.Share, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO shares (node_id, token, pin_hash, expires_at, max_downloads, created_by, created_via, kind, max_uploads, drop_settings) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		sh.NodeID, sh.Token, sh.PinHash, sh.ExpiresAt, sh.MaxDownloads, sh.CreatedBy, sh.CreatedVia, shareKind(sh.Kind), sh.MaxUploads, sh.DropSettings).Scan(&id)
	if err != nil {
		return nil, err
	}
	sh.ID = id
	sh.CreatedAt = time.Now()
	sh.HasPin = sh.PinHash != ""
	return sh, nil
}

func (s *Store) GetShareByToken(ctx context.Context, token string) (*model.Share, error) {
	return scanShare(s.db.QueryRowContext(ctx, `SELECT id, node_id, token, COALESCE(pin_hash,''), expires_at, max_downloads, download_count, created_by, COALESCE(created_via,''), created_at, COALESCE(kind,'download'), max_uploads, upload_count, drop_settings FROM shares WHERE token=$1`, token))
}

func (s *Store) ListSharesByNode(ctx context.Context, nodeID int64) ([]*model.Share, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, node_id, token, COALESCE(pin_hash,''), expires_at, max_downloads, download_count, created_by, COALESCE(created_via,''), created_at, COALESCE(kind,'download'), max_uploads, upload_count, drop_settings FROM shares WHERE node_id=$1 ORDER BY created_at DESC`, nodeID)
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
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET download_count = download_count + 1 WHERE id=$1`, id)
	return err
}

func (s *Store) IncrementShareUpload(ctx context.Context, id int64, n int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET upload_count = upload_count + $1 WHERE id=$2`, n, id)
	return err
}

func (s *Store) DeleteShare(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM shares WHERE id=$1`, id)
	return err
}

func (s *Store) DeleteExpiredShares(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM shares WHERE expires_at IS NOT NULL AND expires_at < NOW()`)
	return err
}

func (s *Store) CreateChunkedUpload(ctx context.Context, u *model.ChunkedUpload) error {
	parts, _ := json.Marshal(u.Parts)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chunked_uploads (id, storage_id, storage_key, upload_id, total_size, parts_json, expires_at) VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7)`,
		u.ID, u.StorageID, u.StorageKey, u.UploadID, u.TotalSize, string(parts), u.ExpiresAt)
	return err
}

func (s *Store) GetChunkedUpload(ctx context.Context, id string) (*model.ChunkedUpload, error) {
	out := &model.ChunkedUpload{}
	var partsJSON string
	err := s.db.QueryRowContext(ctx, `SELECT id, storage_id, storage_key, upload_id, total_size, parts_json::text, expires_at FROM chunked_uploads WHERE id=$1`, id).
		Scan(&out.ID, &out.StorageID, &out.StorageKey, &out.UploadID, &out.TotalSize, &partsJSON, &out.ExpiresAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(partsJSON), &out.Parts)
	return out, nil
}

func (s *Store) UpdateChunkedUploadParts(ctx context.Context, id string, parts []model.UploadPart) error {
	pj, _ := json.Marshal(parts)
	_, err := s.db.ExecContext(ctx, `UPDATE chunked_uploads SET parts_json=$1::jsonb WHERE id=$2`, string(pj), id)
	return err
}

func (s *Store) DeleteChunkedUpload(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chunked_uploads WHERE id=$1`, id)
	return err
}

func (s *Store) DeleteExpiredChunkedUploads(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chunked_uploads WHERE expires_at < NOW()`)
	return err
}

func (s *Store) CreateSyncRun(ctx context.Context, storageID int64, cursorBefore string) (*model.SyncRun, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO sync_runs (storage_id, cursor_before, status) VALUES ($1,$2,'running') RETURNING id`, storageID, cursorBefore).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &model.SyncRun{ID: id, StorageID: storageID, StartedAt: time.Now(), CursorBefore: cursorBefore, Status: "running"}, nil
}

func (s *Store) FinishSyncRun(ctx context.Context, id int64, cursorAfter string, seen, added, updated, deleted int, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sync_runs SET finished_at=NOW(), cursor_after=$1, seen_count=$2, added=$3, updated=$4, deleted=$5, status=$6, error=$7 WHERE id=$8`,
		cursorAfter, seen, added, updated, deleted, status, errMsg, id)
	return err
}

func (s *Store) GetLastSyncRun(ctx context.Context, storageID int64) (*model.SyncRun, error) {
	return scanSyncRun(s.db.QueryRowContext(ctx, `SELECT id, storage_id, started_at, finished_at, COALESCE(cursor_before,''), COALESCE(cursor_after,''), seen_count, added, updated, deleted, status, COALESCE(error,'') FROM sync_runs WHERE storage_id=$1 ORDER BY started_at DESC LIMIT 1`, storageID))
}

func (s *Store) ListSyncRuns(ctx context.Context, storageID int64, limit int) ([]*model.SyncRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, storage_id, started_at, finished_at, COALESCE(cursor_before,''), COALESCE(cursor_after,''), seen_count, added, updated, deleted, status, COALESCE(error,'') FROM sync_runs WHERE storage_id=$1 ORDER BY started_at DESC LIMIT $2`, storageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.SyncRun
	for rows.Next() {
		sr, err := scanSyncRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

func (s *Store) CreateSyncConflict(ctx context.Context, c *model.SyncConflict) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sync_conflicts (node_id, storage_id, storage_key, db_etag, backend_etag, db_mtime, backend_mtime) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		c.NodeID, c.StorageID, c.StorageKey, c.DBEtag, c.BackendEtag, c.DBMtime, c.BackendMtime)
	return err
}

func (s *Store) ListUnresolvedConflicts(ctx context.Context) ([]*model.SyncConflict, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, storage_id, COALESCE(storage_key,''), COALESCE(db_etag,''), COALESCE(backend_etag,''), db_mtime, backend_mtime, detected_at, resolved_at, COALESCE(resolution,'') FROM sync_conflicts WHERE resolved_at IS NULL ORDER BY detected_at DESC`)
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
	_, err := s.db.ExecContext(ctx, `UPDATE sync_conflicts SET resolved_at=NOW(), resolution=$1 WHERE id=$2`, resolution, id)
	return err
}

func (s *Store) InsertAuditEntry(ctx context.Context, e *model.AuditEntry) error {
	mj, _ := json.Marshal(e.Metadata)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, action, target_type, target_id, metadata_json, ip) VALUES ($1,$2,$3,$4,$5::jsonb,$6)`,
		e.UserID, e.Action, e.TargetType, e.TargetID, string(mj), e.IP)
	return err
}

func (s *Store) ListAuditRecent(ctx context.Context, limit int) ([]*model.AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, action, COALESCE(target_type,''), COALESCE(target_id,''), metadata_json::text, COALESCE(ip,''), created_at FROM audit_log ORDER BY created_at DESC LIMIT $1`, limit)
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

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(value,'') FROM settings WHERE key=$1`, key).Scan(&v)
	return v, err
}

func (s *Store) UpsertSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES ($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=NOW()`,
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

func (s *Store) UpsertExternalService(ctx context.Context, name string, enabled bool, urlS, secretEnc, optionsJSON string, lastCheck time.Time, lastState string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO external_services (name, enabled, url, secret_enc, options_json, last_check, last_state) VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7)
		 ON CONFLICT(name) DO UPDATE SET enabled=excluded.enabled, url=excluded.url, secret_enc=excluded.secret_enc, options_json=excluded.options_json, last_check=excluded.last_check, last_state=excluded.last_state`,
		name, enabled, urlS, secretEnc, optionsJSON, lastCheck, lastState)
	return err
}

func (s *Store) GetExternalService(ctx context.Context, name string) (*db.ExternalService, error) {
	es := &db.ExternalService{}
	err := s.db.QueryRowContext(ctx,
		`SELECT name, enabled, COALESCE(url,''), COALESCE(secret_enc,''), options_json::text, last_check, COALESCE(last_state,'') FROM external_services WHERE name=$1`, name).
		Scan(&es.Name, &es.Enabled, &es.URL, &es.SecretEnc, &es.OptionsJSON, &es.LastCheck, &es.LastState)
	if err != nil {
		return nil, err
	}
	return es, nil
}

func (s *Store) ListExternalServices(ctx context.Context) ([]*db.ExternalService, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, enabled, COALESCE(url,''), COALESCE(secret_enc,''), options_json::text, last_check, COALESCE(last_state,'') FROM external_services ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*db.ExternalService
	for rows.Next() {
		es := &db.ExternalService{}
		if err := rows.Scan(&es.Name, &es.Enabled, &es.URL, &es.SecretEnc, &es.OptionsJSON, &es.LastCheck, &es.LastState); err != nil {
			return nil, err
		}
		out = append(out, es)
	}
	return out, rows.Err()
}

func (s *Store) UpdateExternalServiceState(ctx context.Context, name string, lastCheck time.Time, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE external_services SET last_check=$1, last_state=$2 WHERE name=$3`, lastCheck, state, name)
	return err
}

func (s *Store) GetThumbnail(ctx context.Context, nodeID int64) (*model.Thumbnail, error) {
	t := &model.Thumbnail{}
	err := s.db.QueryRowContext(ctx, `SELECT node_id, state, COALESCE(storage_key,''), COALESCE(width,0), COALESCE(height,0), COALESCE(error,''), generated_at FROM thumbnails WHERE node_id=$1`, nodeID).
		Scan(&t.NodeID, &t.State, &t.StorageKey, &t.Width, &t.Height, &t.Error, &t.GeneratedAt)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Store) UpsertThumbnail(ctx context.Context, t *model.Thumbnail) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO thumbnails (node_id, state, storage_key, width, height, error, generated_at) VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT(node_id) DO UPDATE SET state=excluded.state, storage_key=excluded.storage_key, width=excluded.width, height=excluded.height, error=excluded.error, generated_at=excluded.generated_at`,
		t.NodeID, t.State, t.StorageKey, t.Width, t.Height, t.Error, t.GeneratedAt)
	return err
}

func (s *Store) SetThumbnailState(ctx context.Context, nodeID int64, state, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE thumbnails SET state=$1, error=$2 WHERE node_id=$3`, state, errMsg, nodeID)
	return err
}

func (s *Store) CreateNodeVersion(ctx context.Context, v *model.NodeVersion) (*model.NodeVersion, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO node_versions (node_id, version_n, storage_key, size, etag) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		v.NodeID, v.VersionN, v.StorageKey, v.Size, v.Etag).Scan(&id)
	if err != nil {
		return nil, err
	}
	v.ID = id
	v.CreatedAt = time.Now()
	return v, nil
}

func (s *Store) ListNodeVersions(ctx context.Context, nodeID int64) ([]*model.NodeVersion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, node_id, version_n, COALESCE(storage_key,''), size, COALESCE(etag,''), created_at FROM node_versions WHERE node_id=$1 ORDER BY version_n DESC`, nodeID)
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

func nodeColumns() string {
	return `id, storage_id, parent_id, name, path, path_hash, COALESCE(storage_key,''), type, size, COALESCE(mime,''), COALESCE(etag,''), backend_mtime, db_mtime, sync_state, seen_at, deleted_at, created_at, updated_at`
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
	if replicaTarget.Valid {
		v := replicaTarget.Int64
		st.ReplicaTargetID = &v
	}
	if replicaMode.Valid {
		st.ReplicaMode = replicaMode.String
	}
	return st, nil
}

func scanUser(r rowScanner) (*model.User, error) {
	u := &model.User{}
	var recoveryJSON string
	var providerID sql.NullInt64
	if err := r.Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Role, &u.TOTPSecret, &u.TOTPPendingSecret, &u.TOTPEnabled, &recoveryJSON, &u.Locale, &u.Timezone, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt, &providerID, &u.OIDCSubject); err != nil {
		return nil, err
	}
	if recoveryJSON != "" {
		_ = json.Unmarshal([]byte(recoveryJSON), &u.TOTPRecoveryCodes)
	}
	if providerID.Valid {
		v := providerID.Int64
		u.ProviderID = &v
	}
	return u, nil
}

func scanShare(r rowScanner) (*model.Share, error) {
	sh := &model.Share{}
	if err := r.Scan(&sh.ID, &sh.NodeID, &sh.Token, &sh.PinHash, &sh.ExpiresAt, &sh.MaxDownloads, &sh.DownloadCount, &sh.CreatedBy, &sh.CreatedVia, &sh.CreatedAt, &sh.Kind, &sh.MaxUploads, &sh.UploadCount, &sh.DropSettings); err != nil {
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

// ─────────────────── Sync conflicts (admin) ───────────────────

const conflictColumnsPg = `id, node_id, storage_id, storage_key, db_etag, backend_etag, db_mtime, backend_mtime, detected_at, resolved_at, resolution`

// ListSyncConflictsByRun returns conflicts attributed to a specific sync_run.
func (s *Store) ListSyncConflictsByRun(ctx context.Context, runID int64) ([]*model.SyncConflict, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+conflictColumnsPg+`
		FROM sync_conflicts c
		WHERE c.detected_at >= COALESCE((SELECT started_at FROM sync_runs WHERE id=$1), c.detected_at)
		  AND c.detected_at <= COALESCE((SELECT finished_at FROM sync_runs WHERE id=$1), NOW())
		ORDER BY c.detected_at DESC
		LIMIT 500`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConflictsPg(rows)
}

// ListSyncConflictsByStorage returns recent unresolved conflicts.
func (s *Store) ListSyncConflictsByStorage(ctx context.Context, storageID int64, limit int) ([]*model.SyncConflict, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+conflictColumnsPg+`
		FROM sync_conflicts
		WHERE storage_id=$1 AND resolved_at IS NULL
		ORDER BY detected_at DESC
		LIMIT $2`, storageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConflictsPg(rows)
}

func scanConflictsPg(rows *sql.Rows) ([]*model.SyncConflict, error) {
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

// AllNodesForIndex returns every non-deleted node for the search rebuild job.
func (s *Store) AllNodesForIndex(ctx context.Context) ([]*model.Node, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+nodeColumns()+`
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

// ─────────────────── TOTP / 2FA ───────────────────

// SetTotpPendingSecret stores a freshly-enrolled TOTP secret + recovery
// codes prior to the user verifying with a one-time code.
func (s *Store) SetTotpPendingSecret(ctx context.Context, id int64, secret string, recoveryCodes []string) error {
	codes, _ := json.Marshal(recoveryCodes)
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_pending_secret=$1, totp_recovery_codes_json=$2::jsonb, updated_at=NOW() WHERE id=$3`,
		secret, string(codes), id)
	return err
}

// ActivateTotp moves the pending secret into totp_secret and flips totp_enabled.
func (s *Store) ActivateTotp(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_secret=COALESCE(totp_pending_secret,''), totp_pending_secret=NULL, totp_enabled=TRUE, updated_at=NOW() WHERE id=$1`,
		id)
	return err
}

// ClearTotp wipes all 2FA state.
func (s *Store) ClearTotp(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_secret=NULL, totp_pending_secret=NULL, totp_enabled=FALSE, totp_recovery_codes_json='[]'::jsonb, updated_at=NOW() WHERE id=$1`,
		id)
	return err
}

// ─────────────────── Counters needed by dashboard / metrics ───────────────────

// CountActiveSessions counts non-expired sessions.
func (s *Store) CountActiveSessions(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE expires_at > NOW()`).Scan(&n)
	return n, err
}

// CountNodesAddedSince counts non-deleted nodes created in the given window.
func (s *Store) CountNodesAddedSince(ctx context.Context, storageID int64, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM nodes WHERE storage_id=$1 AND created_at >= $2 AND deleted_at IS NULL`,
		storageID, since).Scan(&n)
	return n, err
}

// CountNodesDeletedSince counts soft-deleted nodes in the given window.
func (s *Store) CountNodesDeletedSince(ctx context.Context, storageID int64, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM nodes WHERE storage_id=$1 AND deleted_at IS NOT NULL AND deleted_at >= $2`,
		storageID, since).Scan(&n)
	return n, err
}

// CountTotalShares returns the number of currently-active shares.
func (s *Store) CountTotalShares(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM shares WHERE expires_at IS NULL OR expires_at > NOW()`).Scan(&n)
	return n, err
}

// CountQueueDepth returns the number of running sync_runs.
func (s *Store) CountQueueDepth(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sync_runs WHERE status='running'`).Scan(&n)
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
	idx := 1
	if userID != nil {
		cond += fmt.Sprintf(" AND a.user_id = $%d", idx)
		args = append(args, *userID)
		idx++
	}
	if action != "" {
		cond += fmt.Sprintf(" AND a.action = $%d", idx)
		args = append(args, action)
		idx++
	}
	if from != nil {
		cond += fmt.Sprintf(" AND a.created_at >= $%d", idx)
		args = append(args, *from)
		idx++
	}
	if to != nil {
		cond += fmt.Sprintf(" AND a.created_at <= $%d", idx)
		args = append(args, *to)
		idx++
	}

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log a WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limPlaceholder := fmt.Sprintf("$%d", idx)
	idx++
	offPlaceholder := fmt.Sprintf("$%d", idx)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.user_id, COALESCE(u.email,''), a.action, COALESCE(a.target_type,''),
		       COALESCE(a.target_id,''), COALESCE(a.metadata_json::text,''), COALESCE(a.ip,''), a.created_at
		FROM audit_log a
		LEFT JOIN users u ON u.id = a.user_id
		WHERE `+cond+`
		ORDER BY a.id DESC LIMIT `+limPlaceholder+` OFFSET `+offPlaceholder, args...)
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
		`SELECT COALESCE(SUM(size),0) FROM nodes WHERE storage_id=$1 AND type=1 AND deleted_at IS NULL`,
		storageID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// CountSyncConflictsByRun counts conflicts within a run's time window.
func (s *Store) CountSyncConflictsByRun(ctx context.Context, runID int64) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sync_conflicts c
		WHERE c.detected_at >= COALESCE((SELECT started_at FROM sync_runs WHERE id=$1), c.detected_at)
		  AND c.detected_at <= COALESCE((SELECT finished_at FROM sync_runs WHERE id=$1), NOW())`,
		runID).Scan(&n)
	return n, err
}

// ─────────────────── Missing methods carried over from sqlite ───────────────────

// DeleteSessionsForUser revokes all sessions for a user, optionally
// keeping `exceptToken` (the current session) alive.
func (s *Store) DeleteSessionsForUser(ctx context.Context, userID int64, exceptToken string) error {
	if exceptToken == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=$1 AND token<>$2`, userID, exceptToken)
	return err
}

// UpdateUserEmail changes a user's email address.
func (s *Store) UpdateUserEmail(ctx context.Context, id int64, email string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET email=$1, updated_at=NOW() WHERE id=$2`, email, id)
	return err
}

// UpdateUserDisplayName sets the user's human-friendly display name.
func (s *Store) UpdateUserDisplayName(ctx context.Context, id int64, displayName string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET display_name=$1, updated_at=NOW() WHERE id=$2`, displayName, id)
	return err
}

// GetShareByID looks up a share by its row ID.
func (s *Store) GetShareByID(ctx context.Context, id int64) (*model.Share, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, node_id, token, COALESCE(pin_hash,''), expires_at, max_downloads, download_count, created_by, COALESCE(created_via,''), created_at, COALESCE(kind,'download'), max_uploads, upload_count, drop_settings FROM shares WHERE id=$1`, id)
	return scanShare(row)
}

// RevokeShare soft-revokes by setting expires_at = NOW (audit trail intact).
func (s *Store) RevokeShare(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET expires_at=NOW() WHERE id=$1`, id)
	return err
}

// ListAllShares returns the admin overview of every share. `creatorID`
// nil means all users; activeOnly excludes expired/revoked rows.
func (s *Store) ListAllShares(ctx context.Context, creatorID *int64, activeOnly bool, limit, offset int) ([]*db.ShareWithMeta, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	conds := []string{"1=1"}
	args := []any{}
	idx := 1
	if creatorID != nil {
		conds = append(conds, fmt.Sprintf("s.created_by=$%d", idx))
		args = append(args, *creatorID)
		idx++
	}
	if activeOnly {
		conds = append(conds, "(s.expires_at IS NULL OR s.expires_at > NOW())")
		conds = append(conds, "(s.max_downloads IS NULL OR s.download_count < s.max_downloads)")
	}
	whereSQL := strings.Join(conds, " AND ")

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM shares s WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limPlaceholder := fmt.Sprintf("$%d", idx)
	idx++
	offPlaceholder := fmt.Sprintf("$%d", idx)
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.node_id, s.token, COALESCE(s.pin_hash,''), s.expires_at, s.max_downloads, s.download_count, s.created_by, COALESCE(s.created_via,''), s.created_at,
		       COALESCE(u.email,''), COALESCE(n.path,''), COALESCE(st.name,'')
		FROM shares s
		LEFT JOIN users u     ON u.id = s.created_by
		LEFT JOIN nodes n     ON n.id = s.node_id
		LEFT JOIN storages st ON st.id = n.storage_id
		WHERE `+whereSQL+`
		ORDER BY s.id DESC LIMIT `+limPlaceholder+` OFFSET `+offPlaceholder, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []*db.ShareWithMeta{}
	for rows.Next() {
		sh := &model.Share{}
		row := &db.ShareWithMeta{Share: sh}
		if err := rows.Scan(&sh.ID, &sh.NodeID, &sh.Token, &sh.PinHash, &sh.ExpiresAt, &sh.MaxDownloads, &sh.DownloadCount, &sh.CreatedBy, &sh.CreatedAt, &row.CreatorEmail, &row.NodePath, &row.StorageName); err != nil {
			return nil, 0, err
		}
		sh.HasPin = sh.PinHash != ""
		out = append(out, row)
	}
	return out, total, rows.Err()
}

// GetSyncRun looks up a sync_run by id.
func (s *Store) GetSyncRun(ctx context.Context, id int64) (*model.SyncRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, storage_id, started_at, finished_at, COALESCE(cursor_before,''), COALESCE(cursor_after,''), seen_count, added, updated, deleted, status, COALESCE(error,'')
		 FROM sync_runs WHERE id=$1`, id)
	return scanSyncRun(row)
}

// ListSyncRunsAcrossAll returns paginated runs across every storage.
// storageID==0 means "all", status=="" means "all".
func (s *Store) ListSyncRunsAcrossAll(ctx context.Context, storageID int64, status string, limit, offset int) ([]*model.SyncRun, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	// 5-day window — see SQLite ListSyncRunsAcrossAll comment.
	conds := []string{"started_at >= NOW() - INTERVAL '5 days'"}
	args := []any{}
	idx := 1
	if storageID > 0 {
		conds = append(conds, fmt.Sprintf("storage_id=$%d", idx))
		args = append(args, storageID)
		idx++
	}
	if status != "" {
		conds = append(conds, fmt.Sprintf("status=$%d", idx))
		args = append(args, status)
		idx++
	}
	whereSQL := strings.Join(conds, " AND ")

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_runs WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limPlaceholder := fmt.Sprintf("$%d", idx)
	idx++
	offPlaceholder := fmt.Sprintf("$%d", idx)
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, storage_id, started_at, finished_at, COALESCE(cursor_before,''), COALESCE(cursor_after,''), seen_count, added, updated, deleted, status, COALESCE(error,'')
		 FROM sync_runs
		 WHERE `+whereSQL+`
		 ORDER BY id DESC LIMIT `+limPlaceholder+` OFFSET `+offPlaceholder, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []*model.SyncRun{}
	for rows.Next() {
		sr, err := scanSyncRun(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, sr)
	}
	return out, total, rows.Err()
}

// ListConflictsByStorage returns recent sync conflicts for one storage.
func (s *Store) ListConflictsByStorage(ctx context.Context, storageID int64, limit int) ([]*model.SyncConflict, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, storage_id, COALESCE(storage_key,''), COALESCE(db_etag,''), COALESCE(backend_etag,''), db_mtime, backend_mtime, detected_at, resolved_at, COALESCE(resolution,'')
		 FROM sync_conflicts WHERE storage_id=$1 ORDER BY detected_at DESC LIMIT $2`, storageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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

// ─────────────────── Node versions (extended) ───────────────────

// GetNodeVersion looks up a single version row by id.
func (s *Store) GetNodeVersion(ctx context.Context, id int64) (*model.NodeVersion, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, node_id, version_n, COALESCE(storage_key,''), size, COALESCE(etag,''), created_at FROM node_versions WHERE id=$1`, id)
	v := &model.NodeVersion{}
	if err := row.Scan(&v.ID, &v.NodeID, &v.VersionN, &v.StorageKey, &v.Size, &v.Etag, &v.CreatedAt); err != nil {
		return nil, err
	}
	return v, nil
}

// NextNodeVersionNumber returns COALESCE(MAX(version_n),0)+1 for a node.
func (s *Store) NextNodeVersionNumber(ctx context.Context, nodeID int64) (int, error) {
	var n sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_n),0) FROM node_versions WHERE node_id=$1`, nodeID).Scan(&n); err != nil {
		return 0, err
	}
	return int(n.Int64) + 1, nil
}

// DeleteNodeVersion removes a single version row.
func (s *Store) DeleteNodeVersion(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM node_versions WHERE id=$1`, id)
	return err
}

// DeleteOldNodeVersions deletes all but the newest `keep` versions for a node.
// Returns the rows that were removed.
func (s *Store) DeleteOldNodeVersions(ctx context.Context, nodeID int64, keep int) ([]*model.NodeVersion, error) {
	if keep < 0 {
		keep = 0
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, version_n, COALESCE(storage_key,''), size, COALESCE(etag,''), created_at
		 FROM node_versions
		 WHERE node_id=$1
		 ORDER BY version_n DESC
		 OFFSET $2`, nodeID, keep)
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
		if _, err := s.db.ExecContext(ctx, `DELETE FROM node_versions WHERE id=$1`, v.ID); err != nil {
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
		`SELECT COALESCE(usage_bytes,0), COALESCE(quota_bytes,0) FROM users WHERE id=$1`, userID).
		Scan(&used, &limit)
	if err != nil {
		return 0, 0, err
	}
	return used, limit, nil
}

// IncrementUserUsage atomically adjusts usage_bytes (delta may be negative).
func (s *Store) IncrementUserUsage(ctx context.Context, userID int64, delta int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET usage_bytes = GREATEST(0, COALESCE(usage_bytes,0) + $1) WHERE id=$2`, delta, userID)
	return err
}

// SetUserQuota writes the quota_bytes value (0 = unlimited).
func (s *Store) SetUserQuota(ctx context.Context, userID int64, bytes int64) error {
	if bytes < 0 {
		bytes = 0
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET quota_bytes=$1 WHERE id=$2`, bytes, userID)
	return err
}

// RecomputeUserUsage scans nodes owned by this user, sets usage_bytes.
func (s *Store) RecomputeUserUsage(ctx context.Context, userID int64) (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(size),0) FROM nodes WHERE owner_id=$1 AND deleted_at IS NULL AND type='file'`,
		userID).Scan(&total)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET usage_bytes=$1 WHERE id=$2`, total.Int64, userID); err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// ─────────────────── Node owner ───────────────────

// SetNodeOwner updates the owner_id column for one node.
func (s *Store) SetNodeOwner(ctx context.Context, nodeID int64, ownerID *int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET owner_id=$1 WHERE id=$2`, ownerID, nodeID)
	return err
}

// GetNodeOwner returns the owner_id (nullable) for one node.
func (s *Store) GetNodeOwner(ctx context.Context, nodeID int64) (*int64, error) {
	var owner sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM nodes WHERE id=$1`, nodeID).Scan(&owner)
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
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+nodeColumns()+`
		 FROM nodes WHERE deleted_at IS NOT NULL AND deleted_at < $1
		 ORDER BY deleted_at ASC LIMIT $2`, before, limit)
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
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET deleted_at=NULL, updated_at=NOW() WHERE id=$1`, id)
	return err
}

// ListTrashed returns paginated soft-deleted rows (storage filter optional).
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
		where += ` AND storage_id = $1`
		args = append(args, *storageID)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	limPlace := fmt.Sprintf("$%d", len(args)-1)
	offPlace := fmt.Sprintf("$%d", len(args))
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+nodeColumns()+` FROM nodes `+where+
			` ORDER BY deleted_at DESC LIMIT `+limPlace+` OFFSET `+offPlace, args...)
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

// RestoreNodeAt flips deleted_at and reverts the path/parent in one shot.
func (s *Store) RestoreNodeAt(ctx context.Context, id int64, parentID *int64, origPath string) error {
	clean := strings.TrimRight(path.Clean("/"+strings.Trim(origPath, "/")), "/")
	if clean == "" {
		clean = "/"
	}
	row := s.db.QueryRowContext(ctx, `SELECT storage_id FROM nodes WHERE id=$1`, id)
	var sid int64
	if err := row.Scan(&sid); err != nil {
		return err
	}
	hash := pgNodePathHash(sid, clean)
	name := path.Base(clean)
	if parentID == nil {
		_, err := s.db.ExecContext(ctx, `
			UPDATE nodes
			SET deleted_at=NULL, updated_at=NOW(), parent_id=NULL,
			    name=$1, path=$2, path_hash=$3, storage_key=$4
			WHERE id=$5`, name, clean, hash, clean, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE nodes
		SET deleted_at=NULL, updated_at=NOW(), parent_id=$1,
		    name=$2, path=$3, path_hash=$4, storage_key=$5
		WHERE id=$6`, *parentID, name, clean, hash, clean, id)
	return err
}

// LookupParentByPath walks the cache one segment at a time to resolve the
// parent_id (nil at root) for the dir that owns `fullPath`.
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
				WHERE storage_id=$1 AND name=$2 AND deleted_at IS NULL
				  AND parent_id IS NULL
				LIMIT 1`, storageID, seg).Scan(&id)
			if err != nil {
				return nil, err
			}
		} else {
			err := s.db.QueryRowContext(ctx, `
				SELECT id FROM nodes
				WHERE storage_id=$1 AND name=$2 AND deleted_at IS NULL
				  AND parent_id=$3
				LIMIT 1`, storageID, seg, *parentPtr).Scan(&id)
			if err != nil {
				return nil, err
			}
		}
		parentPtr = &id
	}
	return parentPtr, nil
}

func pgNodePathHash(storageID int64, p string) string {
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
		 VALUES ($1,$2,$3,$4,NOW())
		 ON CONFLICT (user_id, node_id, key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`,
		userID, nodeID, key, value)
	return err
}

// DeleteUserNodeMeta removes a single (user, node, key) row.
func (s *Store) DeleteUserNodeMeta(ctx context.Context, userID, nodeID int64, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_node_meta WHERE user_id=$1 AND node_id=$2 AND key=$3`, userID, nodeID, key)
	return err
}

// GetUserNodeMeta fetches a single value (returns empty string + sql.ErrNoRows if absent).
func (s *Store) GetUserNodeMeta(ctx context.Context, userID, nodeID int64, key string) (string, error) {
	var v sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT value FROM user_node_meta WHERE user_id=$1 AND node_id=$2 AND key=$3`, userID, nodeID, key).Scan(&v)
	if err != nil {
		return "", err
	}
	return v.String, nil
}

// ListUserNodeMetaForNode returns all (key,value) for one (user,node) pair.
func (s *Store) ListUserNodeMetaForNode(ctx context.Context, userID, nodeID int64, prefix string) (map[string]string, error) {
	q := `SELECT key, COALESCE(value,'') FROM user_node_meta WHERE user_id=$1 AND node_id=$2`
	args := []any{userID, nodeID}
	if prefix != "" {
		q += ` AND key LIKE $3`
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

// ListNodesByUserMeta returns the nodes flagged with (key) for the given user.
func (s *Store) ListNodesByUserMeta(ctx context.Context, userID int64, key string, limit int) ([]*model.Node, error) {
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT n.id, n.storage_id, n.parent_id, n.name, n.path, n.path_hash, COALESCE(n.storage_key,''), n.type, n.size, COALESCE(n.mime,''), COALESCE(n.etag,''), n.backend_mtime, n.db_mtime, n.sync_state, n.seen_at, n.deleted_at, n.created_at, n.updated_at
		 FROM user_node_meta m
		 INNER JOIN nodes n ON n.id = m.node_id
		 WHERE m.user_id=$1 AND m.key=$2 AND n.deleted_at IS NULL
		 ORDER BY m.updated_at DESC
		 LIMIT $3`, userID, key, limit)
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

// ─────────────────── Tags (shared via node_meta) ───────────────────

const tagPrefixPg = "tag:"

// SetNodeTags wipes tag:* rows and writes new ones.
func (s *Store) SetNodeTags(ctx context.Context, nodeID int64, tags []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_meta WHERE node_id=$1 AND key LIKE $2`, nodeID, tagPrefixPg+"%"); err != nil {
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
			`INSERT INTO node_meta (node_id, key, value) VALUES ($1,$2,$3)
			 ON CONFLICT (node_id, key) DO UPDATE SET value=EXCLUDED.value`,
			nodeID, tagPrefixPg+t, "1"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetNodeTags returns the tag list (without prefix) for one node.
func (s *Store) GetNodeTags(ctx context.Context, nodeID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM node_meta WHERE node_id=$1 AND key LIKE $2 ORDER BY key`, nodeID, tagPrefixPg+"%")
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
		out = append(out, strings.TrimPrefix(k, tagPrefixPg))
	}
	return out, rows.Err()
}

// ListAllTagsForStorage returns every distinct tag used in a storage.
func (s *Store) ListAllTagsForStorage(ctx context.Context, storageID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT m.key
		 FROM node_meta m
		 INNER JOIN nodes n ON n.id = m.node_id
		 WHERE n.storage_id=$1 AND n.deleted_at IS NULL AND m.key LIKE $2
		 ORDER BY m.key`, storageID, tagPrefixPg+"%")
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
		out = append(out, strings.TrimPrefix(k, tagPrefixPg))
	}
	return out, rows.Err()
}

// ListAllTags returns every distinct tag across all storages (alphabetical).
func (s *Store) ListAllTags(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT m.key
		 FROM node_meta m
		 INNER JOIN nodes n ON n.id = m.node_id
		 WHERE n.deleted_at IS NULL AND m.key LIKE $1
		 ORDER BY m.key`, tagPrefixPg+"%")
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
		out = append(out, strings.TrimPrefix(k, tagPrefixPg))
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
		 WHERE m.key=$1 AND n.deleted_at IS NULL
		 ORDER BY n.updated_at DESC
		 LIMIT $2`, tagPrefixPg+tag, limit)
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

// InsertNotification persists a new in-app notification row. The
// payload mirrors the SQLite implementation but uses $N placeholders
// and JSONB casting on meta_json.
func (s *Store) InsertNotification(ctx context.Context, n *model.NotificationInput) (int64, error) {
	if n == nil {
		return 0, errors.New("postgres: nil notification")
	}
	if n.Event == "" || n.Severity == "" || n.Title == "" {
		return 0, errors.New("postgres: notification missing event/severity/title")
	}
	meta := n.MetaJSON
	if len(meta) == 0 {
		meta = []byte("{}")
	}
	var userID any
	if n.UserID != nil {
		userID = *n.UserID
	}
	var id int64
	if err := s.db.QueryRowContext(ctx,
		`INSERT INTO notifications (event, severity, title, body, meta_json, user_id, webhook_status)
		 VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7) RETURNING id`,
		n.Event, n.Severity, n.Title, n.Body, string(meta), userID, "pending",
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("postgres: insert notification: %w", err)
	}
	return id, nil
}

// GetNotification returns a single row by id.
func (s *Store) GetNotification(ctx context.Context, id int64) (*model.Notification, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, event, severity, title, body, meta_json::text,
		        user_id, read_at, webhook_status, COALESCE(webhook_error,''), created_at
		 FROM notifications WHERE id=$1`, id)
	return scanNotificationPg(row)
}

// ListNotifications paginates either user or admin views.
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
		idx    = 1
	)
	if userID != nil {
		whereC = append(whereC, fmt.Sprintf("(user_id IS NULL OR user_id = $%d)", idx))
		args = append(args, *userID)
		idx++
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
		return nil, 0, fmt.Errorf("postgres: count notifications: %w", err)
	}

	args = append(args, limit, offset)
	q := fmt.Sprintf(
		`SELECT id, event, severity, title, body, meta_json::text,
		        user_id, read_at, webhook_status, COALESCE(webhook_error,''), created_at
		 FROM notifications %s
		 ORDER BY created_at DESC, id DESC
		 LIMIT $%d OFFSET $%d`, whereSQL, idx, idx+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list notifications: %w", err)
	}
	defer rows.Close()
	var out []*model.Notification
	for rows.Next() {
		n, err := scanNotificationPg(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, n)
	}
	return out, total, rows.Err()
}

// MarkNotificationRead bumps read_at on a single row.
func (s *Store) MarkNotificationRead(ctx context.Context, id int64, userID *int64) error {
	q := `UPDATE notifications SET read_at = NOW()
	      WHERE id=$1 AND read_at IS NULL`
	args := []any{id}
	if userID != nil {
		q += ` AND (user_id IS NULL OR user_id = $2)`
		args = append(args, *userID)
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("postgres: mark notif read: %w", err)
	}
	return nil
}

// MarkAllNotificationsRead bumps read_at for every unread row visible
// to userID. Pass nil for the global "mark all" admin sweep.
func (s *Store) MarkAllNotificationsRead(ctx context.Context, userID *int64) error {
	q := `UPDATE notifications SET read_at = NOW() WHERE read_at IS NULL`
	var args []any
	if userID != nil {
		q += ` AND (user_id IS NULL OR user_id = $1)`
		args = append(args, *userID)
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// UnreadNotificationCount returns the bell badge number for a user.
func (s *Store) UnreadNotificationCount(ctx context.Context, userID *int64) (int64, error) {
	q := `SELECT COUNT(*) FROM notifications WHERE read_at IS NULL`
	var args []any
	if userID != nil {
		q += ` AND (user_id IS NULL OR user_id = $1)`
		args = append(args, *userID)
	}
	var n int64
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// UpdateWebhookStatus updates the delivery audit fields.
func (s *Store) UpdateWebhookStatus(ctx context.Context, id int64, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notifications SET webhook_status=$1, webhook_error=$2 WHERE id=$3`,
		status, errMsg, id)
	if err != nil {
		return fmt.Errorf("postgres: update webhook status: %w", err)
	}
	return nil
}

// GetNotificationSettings returns the per-user toggle (default-on
// when no row exists).
func (s *Store) GetNotificationSettings(ctx context.Context, userID int64) (*model.NotificationSettings, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT user_id, in_app_enabled, muted_events::text
		 FROM notification_settings WHERE user_id=$1`, userID)
	out := &model.NotificationSettings{UserID: userID, InAppEnabled: true, MutedEventsRaw: []byte("[]")}
	var (
		gotUser  int64
		enabled  bool
		mutedRaw string
	)
	if err := row.Scan(&gotUser, &enabled, &mutedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		return nil, fmt.Errorf("postgres: get notif settings: %w", err)
	}
	out.UserID = gotUser
	out.InAppEnabled = enabled
	out.MutedEventsRaw = json.RawMessage(mutedRaw)
	if len(out.MutedEventsRaw) == 0 {
		out.MutedEventsRaw = []byte("[]")
	}
	return out, nil
}

// UpsertNotificationSettings stores the user's preferences via
// INSERT ... ON CONFLICT.
func (s *Store) UpsertNotificationSettings(ctx context.Context, st *model.NotificationSettings) error {
	if st == nil || st.UserID == 0 {
		return errors.New("postgres: invalid notif settings")
	}
	muted := st.MutedEventsRaw
	if len(muted) == 0 {
		muted = []byte("[]")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_settings (user_id, in_app_enabled, muted_events, updated_at)
		 VALUES ($1,$2,$3::jsonb, NOW())
		 ON CONFLICT (user_id) DO UPDATE SET
		   in_app_enabled = EXCLUDED.in_app_enabled,
		   muted_events   = EXCLUDED.muted_events,
		   updated_at     = NOW()`,
		st.UserID, st.InAppEnabled, string(muted))
	if err != nil {
		return fmt.Errorf("postgres: upsert notif settings: %w", err)
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
		return nil, fmt.Errorf("postgres: list replica rules: %w", err)
	}
	defer rows.Close()
	var out []*model.ReplicaRule
	for rows.Next() {
		r := &model.ReplicaRule{}
		if err := rows.Scan(&r.ID, &r.PathPattern, &r.Mode, &r.Priority, &r.Enabled, &r.Description, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetReplicaRule returns a single rule by id.
func (s *Store) GetReplicaRule(ctx context.Context, id int64) (*model.ReplicaRule, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, path_pattern, mode, priority, enabled, description, created_at, updated_at
		 FROM replica_rules WHERE id=$1`, id)
	r := &model.ReplicaRule{}
	if err := row.Scan(&r.ID, &r.PathPattern, &r.Mode, &r.Priority, &r.Enabled, &r.Description, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return r, nil
}

// CreateReplicaRule inserts a new rule.
func (s *Store) CreateReplicaRule(ctx context.Context, in *model.ReplicaRuleInput) (*model.ReplicaRule, error) {
	if err := validateReplicaRulePg(in); err != nil {
		return nil, err
	}
	var id int64
	if err := s.db.QueryRowContext(ctx,
		`INSERT INTO replica_rules (path_pattern, mode, priority, enabled, description)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		in.PathPattern, in.Mode, in.Priority, in.Enabled, in.Description,
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("postgres: create replica rule: %w", err)
	}
	return s.GetReplicaRule(ctx, id)
}

// UpdateReplicaRule replaces a rule by id.
func (s *Store) UpdateReplicaRule(ctx context.Context, id int64, in *model.ReplicaRuleInput) (*model.ReplicaRule, error) {
	if err := validateReplicaRulePg(in); err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE replica_rules
		 SET path_pattern=$1, mode=$2, priority=$3, enabled=$4, description=$5, updated_at=NOW()
		 WHERE id=$6`,
		in.PathPattern, in.Mode, in.Priority, in.Enabled, in.Description, id)
	if err != nil {
		return nil, fmt.Errorf("postgres: update replica rule: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, sql.ErrNoRows
	}
	return s.GetReplicaRule(ctx, id)
}

// DeleteReplicaRule removes a rule.
func (s *Store) DeleteReplicaRule(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM replica_rules WHERE id=$1`, id)
	return err
}

// UpsertReplicaFailure inserts or bumps the (path, op) row.
func (s *Store) UpsertReplicaFailure(ctx context.Context, path, op, errCode, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO replica_failures (path, op, error_code, error_msg, attempts, last_attempt_at)
		 VALUES ($1,$2,$3,$4,1, NOW())
		 ON CONFLICT (path, op) DO UPDATE SET
		   error_code      = EXCLUDED.error_code,
		   error_msg       = EXCLUDED.error_msg,
		   attempts        = replica_failures.attempts + 1,
		   last_attempt_at = NOW(),
		   resolved_at     = NULL`,
		path, op, errCode, errMsg)
	if err != nil {
		return fmt.Errorf("postgres: upsert replica failure: %w", err)
	}
	return nil
}

// ResolveReplicaFailure stamps resolved_at on the matching row.
func (s *Store) ResolveReplicaFailure(ctx context.Context, path, op string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE replica_failures SET resolved_at = NOW()
		 WHERE path=$1 AND op=$2 AND resolved_at IS NULL`, path, op)
	return err
}

// ListReplicaFailures paginates rows.
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
		return nil, 0, fmt.Errorf("postgres: count replica failures: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(
			`SELECT id, path, op, error_code, error_msg, attempts, last_attempt_at, resolved_at
			 FROM replica_failures %s
			 ORDER BY last_attempt_at DESC, id DESC
			 LIMIT $1 OFFSET $2`, whereSQL),
		limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list replica failures: %w", err)
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

// CountUnresolvedReplicaFailures returns the unresolved count.
func (s *Store) CountUnresolvedReplicaFailures(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM replica_failures WHERE resolved_at IS NULL`,
	).Scan(&n)
	return n, err
}

// CountRecentlyResolvedReplicaFailures counts rows resolved since the
// given time.
func (s *Store) CountRecentlyResolvedReplicaFailures(ctx context.Context, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM replica_failures
		 WHERE resolved_at IS NOT NULL AND resolved_at >= $1`, since,
	).Scan(&n)
	return n, err
}

// UpsertReplicaStatusReport replaces (id=1) the singleton report.
func (s *Store) UpsertReplicaStatusReport(ctx context.Context, total, failed, repaired int64, summaryJSON []byte) error {
	if len(summaryJSON) == 0 {
		summaryJSON = []byte("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO replica_status_reports (id, generated_at, total_files, failed_count, repaired_count, summary_json)
		 VALUES (1, NOW(), $1, $2, $3, $4::jsonb)
		 ON CONFLICT (id) DO UPDATE SET
		   generated_at   = NOW(),
		   total_files    = EXCLUDED.total_files,
		   failed_count   = EXCLUDED.failed_count,
		   repaired_count = EXCLUDED.repaired_count,
		   summary_json   = EXCLUDED.summary_json`,
		total, failed, repaired, string(summaryJSON))
	if err != nil {
		return fmt.Errorf("postgres: upsert replica status report: %w", err)
	}
	return nil
}

// GetReplicaStatusReport returns the singleton row.
func (s *Store) GetReplicaStatusReport(ctx context.Context) (*model.ReplicaStatusReport, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT generated_at, total_files, failed_count, repaired_count, summary_json::text
		 FROM replica_status_reports WHERE id=1`)
	out := &model.ReplicaStatusReport{}
	var summaryRaw string
	if err := row.Scan(&out.GeneratedAt, &out.TotalFiles, &out.FailedCount, &out.RepairedCount, &summaryRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: get replica status report: %w", err)
	}
	out.SummaryJSON = json.RawMessage(summaryRaw)
	if len(out.SummaryJSON) == 0 {
		out.SummaryJSON = []byte("{}")
	}
	return out, nil
}

// GetReplicaSettings returns the singleton row.
func (s *Store) GetReplicaSettings(ctx context.Context) (*model.ReplicaSettings, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT report_cron, report_enabled, default_mode FROM replica_settings WHERE id=1`)
	out := &model.ReplicaSettings{DefaultMode: model.ReplicaModeMirror}
	if err := row.Scan(&out.ReportCron, &out.ReportEnabled, &out.DefaultMode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		return nil, fmt.Errorf("postgres: get replica settings: %w", err)
	}
	return out, nil
}

// UpsertReplicaSettings replaces (id=1) the singleton config.
func (s *Store) UpsertReplicaSettings(ctx context.Context, st *model.ReplicaSettings) error {
	if st == nil {
		return errors.New("postgres: nil replica settings")
	}
	if st.DefaultMode == "" {
		st.DefaultMode = model.ReplicaModeMirror
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO replica_settings (id, report_cron, report_enabled, default_mode, updated_at)
		 VALUES (1, $1, $2, $3, NOW())
		 ON CONFLICT (id) DO UPDATE SET
		   report_cron    = EXCLUDED.report_cron,
		   report_enabled = EXCLUDED.report_enabled,
		   default_mode   = EXCLUDED.default_mode,
		   updated_at     = NOW()`,
		st.ReportCron, st.ReportEnabled, st.DefaultMode)
	if err != nil {
		return fmt.Errorf("postgres: upsert replica settings: %w", err)
	}
	return nil
}

func validateReplicaRulePg(in *model.ReplicaRuleInput) error {
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

// scanNotificationPg accepts both *sql.Row and *sql.Rows. Matches the
// column list used by GetNotification + ListNotifications.
func scanNotificationPg(rs interface {
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
