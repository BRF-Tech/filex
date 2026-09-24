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
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/namefold"
	"github.com/brf-tech/filex/backend/internal/pathkey"

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
	// ⚠ Last gate before the value is persisted. Every writer goes through
	// here — the admin API, the config seed, the CLI — so a mode nothing
	// implements cannot reach the column from any of them. Before this, a
	// typo stored fine and the storage silently polled.
	if err := model.ValidateSyncMode(st.SyncMode); err != nil {
		return nil, err
	}
	cfg := st.ConfigJSON
	if len(cfg) == 0 {
		cfg = []byte("{}")
	}
	if st.UID == "" {
		st.UID = model.NewStorageUID()
	}
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO storages (name, driver, mount_path, config_json, sync_mode, sync_interval_s, enabled, read_only, rbac_enabled, uid)
		 VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7,$8,$9,$10) RETURNING `+storageCols,
		st.Name, st.Driver, st.MountPath, string(cfg), st.SyncMode, st.SyncIntervalS, st.Enabled, st.ReadOnly, st.RBACEnabled, st.UID)
	return scanStorage(row)
}

// storageCols is the one place the storage projection is spelled out; see the
// same constant in the sqlite driver. COALESCE on uid because the column is
// nullable so its UNIQUE index tolerates an unfilled row.
const storageCols = `id, name, driver, mount_path, config_json::text, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at, COALESCE(role,'primary'), replica_of_id, COALESCE(replica_mode,'async'), replica_target_id, rbac_enabled, COALESCE(uid,'')`

func (s *Store) GetStorage(ctx context.Context, id int64) (*model.Storage, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+storageCols+` FROM storages WHERE id=$1`, id)
	return scanStorage(row)
}

func (s *Store) GetStorageByName(ctx context.Context, name string) (*model.Storage, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+storageCols+` FROM storages WHERE name=$1`, name)
	return scanStorage(row)
}

func (s *Store) GetStorageByUID(ctx context.Context, uid string) (*model.Storage, error) {
	if uid == "" {
		return nil, sql.ErrNoRows
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+storageCols+` FROM storages WHERE uid=$1`, uid)
	return scanStorage(row)
}

func (s *Store) ListStorages(ctx context.Context) ([]*model.Storage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+storageCols+` FROM storages ORDER BY id`)
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
	rows, err := s.db.QueryContext(ctx, `SELECT `+storageCols+` FROM storages WHERE enabled=true ORDER BY id`)
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
	// Same gate as Create, with one allowance: a row that ALREADY carries an
	// unsupported mode predates this check. Refusing to save an unrelated
	// edit (a rename, a disable) would strand the operator with a row they
	// cannot fix. Only a CHANGE to an unsupported mode is rejected.
	if err := model.ValidateSyncMode(st.SyncMode); err != nil {
		var prev string
		_ = s.db.QueryRowContext(ctx, `SELECT sync_mode FROM storages WHERE id=$1`, st.ID).Scan(&prev)
		if model.SyncMode(prev) != st.SyncMode {
			return err
		}
	}
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
	// transfer_state is written explicitly rather than left to the column
	// default: a staged upload inserts its node BEFORE the bytes reach the
	// driver, and a node that claims "stored" for even a moment is a node a
	// read path would try to fetch from a backend that has nothing.
	transferState := n.TransferState
	if transferState == "" {
		transferState = model.TransferStateStored
	}
	// See the sqlite driver: attribution rides the INSERT so a bulk write costs
	// no extra round trips. nil owner/actor is SYSTEM and is written as NULL.
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO nodes (storage_id, parent_id, name, path, path_hash, storage_key, type, size, mime, etag, backend_mtime, sync_state, transfer_state, owner_id, last_actor_id, external_upload)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		 RETURNING `+nodeColumns(),
		n.StorageID, n.ParentID, n.Name, n.Path, n.PathHash, n.StorageKey, n.Type, n.Size, n.Mime, n.Etag, n.BackendMtime, n.SyncState, transferState, n.OwnerID, n.LastActorID, n.ExternalUpload)
	return scanNode(row)
}

func (s *Store) GetNode(ctx context.Context, id int64) (*model.Node, error) {
	return scanNode(s.db.QueryRowContext(ctx, `SELECT `+nodeColumns()+` FROM nodes WHERE id=$1`, id))
}

func (s *Store) GetNodeByPath(ctx context.Context, storageID int64, hash string) (*model.Node, error) {
	return scanNode(s.db.QueryRowContext(ctx, `SELECT `+nodeColumns()+` FROM nodes WHERE storage_id=$1 AND path_hash=$2 AND deleted_at IS NULL`, storageID, hash))
}

// GetNodeByPathIncludingDeleted returns the row at a path, live or trashed.
//
// ⚠ ORDER BY is load-bearing since migration 00032 made the unique index over
// (storage_id, path_hash) live-only: several trashed rows may now share a path
// with at most one live one. The live row sorts first, then the most recently
// trashed. Without it the sync worker's view of a path would depend on row
// order.
func (s *Store) GetNodeByPathIncludingDeleted(ctx context.Context, storageID int64, hash string) (*model.Node, error) {
	return scanNode(s.db.QueryRowContext(ctx, `SELECT `+nodeColumns()+
		` FROM nodes WHERE storage_id=$1 AND path_hash=$2`+
		` ORDER BY (deleted_at IS NOT NULL), id DESC LIMIT 1`, storageID, hash))
}

// ListLiveNodesInTrash returns live rows sitting in the trash bucket. See the
// db.Store declaration for why any such row is a defect to be cleaned up.
func (s *Store) ListLiveNodesInTrash(ctx context.Context, storageID int64, trashPrefix string) ([]*model.Node, error) {
	bare := strings.Trim(trashPrefix, "/")
	if bare == "" {
		return nil, nil
	}
	slashed := "/" + bare
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeColumns()+
		` FROM nodes WHERE storage_id=$1 AND deleted_at IS NULL`+
		` AND (path=$2 OR path=$3 OR path LIKE $4 OR path LIKE $5) ORDER BY id`,
		storageID, slashed, bare, slashed+"/%", bare+"/%")
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

// treeSpellings returns the two spellings a row's path can carry for dir —
// "/a/b" and "a/b" — or ok=false for the storage root, which is never a
// subtree.
func treeSpellings(dir string) (slashed, bare string, ok bool) {
	bare = strings.Trim(path.Clean("/"+strings.Trim(dir, "/")), "/")
	if bare == "" || bare == "." {
		return "", "", false
	}
	return "/" + bare, bare, true
}

// belowClause matches every row strictly below a directory, in both
// spellings, exactly; its placeholders start at $n and take the four
// arguments belowArgs returns. See the SQLite store for why the bound is a
// rune count and why this is not LIKE.
func belowClause(n int) string {
	return fmt.Sprintf(`(SUBSTR(path,1,$%d)=$%d OR SUBSTR(path,1,$%d)=$%d)`, n, n+1, n+2, n+3)
}

func belowArgs(slashed, bare string) []any {
	return []any{
		utf8.RuneCountInString(slashed + "/"), slashed + "/",
		utf8.RuneCountInString(bare + "/"), bare + "/",
	}
}

func (s *Store) ListNodesUnder(ctx context.Context, storageID int64, dir string, includeDeleted bool) ([]*model.Node, error) {
	slashed, bare, ok := treeSpellings(dir)
	if !ok {
		return nil, nil
	}
	q := `SELECT ` + nodeColumns() + ` FROM nodes WHERE storage_id=$1 AND (path=$2 OR path=$3 OR ` + belowClause(4) + `)`
	if !includeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	args := append([]any{storageID, slashed, bare}, belowArgs(slashed, bare)...)
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY id`, args...)
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

/* kimlik:e3 cloud */
// Provider plan metadata (cloud preparation, migration 00021 — docs/CLOUD.md).
// Kept as dedicated accessors instead of widening providerCols so the existing
// provider CRUD SQL — and therefore flag-off behavior — stays byte-identical.

func (s *Store) SetProviderPlan(ctx context.Context, providerID int64, plan, limitsJSON, billingRef string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE providers SET plan=$1, limits_json=$2, billing_ref=$3, updated_at=NOW() WHERE id=$4`,
		nullIfEmpty(plan), nullIfEmpty(limitsJSON), nullIfEmpty(billingRef), providerID)
	return err
}

func (s *Store) GetProviderPlan(ctx context.Context, providerID int64) (string, string, string, error) {
	var plan, limitsJSON, billingRef string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(plan,''), COALESCE(limits_json,''), COALESCE(billing_ref,'') FROM providers WHERE id=$1`,
		providerID).Scan(&plan, &limitsJSON, &billingRef)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", nil
	}
	return plan, limitsJSON, billingRef, err
}

// nullIfEmpty maps "" to SQL NULL so the passive cloud columns stay NULL
// (not empty string) when unset.
func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
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
//
// Directories drag their cached subtree into the trash with them (GitHub
// issue #5) — see the sqlite driver's SoftDeleteAndRetag doc comment.
func (s *Store) SoftDeleteAndRetag(ctx context.Context, id int64, trashPath, trashHash, origPath string) error {
	base := path.Base(trashPath)
	var storageID int64
	var nodeType, curPath string
	scanErr := s.db.QueryRowContext(ctx,
		`SELECT storage_id, type, path FROM nodes WHERE id=$1`, id).
		Scan(&storageID, &nodeType, &curPath)
	_, err := s.db.ExecContext(ctx, `
		UPDATE nodes
		SET deleted_at=NOW(), updated_at=NOW(), parent_id=NULL,
		    name=$1, path=$2, path_hash=$3, storage_key=$4
		WHERE id=$5`, base, trashPath, trashHash, origPath, id)
	if err != nil || scanErr != nil {
		return err
	}
	if nodeType == string(model.NodeTypeDirectory) {
		s.retagTrashedSubtree(ctx, storageID, []string{origPath, curPath}, trashPath)
	}
	return nil
}

// retagTrashedSubtree / restoreTrashedSubtree — postgres twins of the sqlite
// driver helpers (see there for semantics). Best-effort by design.
func (s *Store) retagTrashedSubtree(ctx context.Context, storageID int64, origPaths []string, trashPath string) {
	type childRow struct {
		id   int64
		path string
	}
	prefixes := pgSubtreePrefixVariants(origPaths)
	if len(prefixes) == 0 {
		return
	}
	var children []childRow
	for _, pfx := range prefixes {
		rows, err := s.db.QueryContext(ctx, `
			SELECT id, path FROM nodes
			WHERE storage_id=$1 AND deleted_at IS NULL AND SUBSTR(path,1,$2)=$3`,
			storageID, prefixChars(pfx), pfx)
		if err != nil {
			continue
		}
		for rows.Next() {
			var c childRow
			if err := rows.Scan(&c.id, &c.path); err == nil {
				children = append(children, c)
			}
		}
		_ = rows.Err()
		rows.Close()
	}
	seen := map[int64]bool{}
	for _, c := range children {
		if seen[c.id] {
			continue
		}
		seen[c.id] = true
		suffix := pgSubtreeSuffix(c.path, prefixes)
		if suffix == "" {
			continue
		}
		newPath := strings.TrimRight(trashPath, "/") + "/" + suffix
		newHash := pathkey.Hash(storageID, newPath)
		_, _ = s.db.ExecContext(ctx, `
			UPDATE nodes
			SET deleted_at=NOW(), updated_at=NOW(),
			    path=$1, path_hash=$2, storage_key=$3
			WHERE id=$4`, newPath, newHash, c.path, c.id)
	}
}

func (s *Store) restoreTrashedSubtree(ctx context.Context, storageID int64, trashPaths []string, restoredPath string) {
	type childRow struct {
		id   int64
		path string
	}
	prefixes := pgSubtreePrefixVariants(trashPaths)
	if len(prefixes) == 0 {
		return
	}
	var children []childRow
	for _, pfx := range prefixes {
		rows, err := s.db.QueryContext(ctx, `
			SELECT id, path FROM nodes
			WHERE storage_id=$1 AND deleted_at IS NOT NULL AND SUBSTR(path,1,$2)=$3`,
			storageID, prefixChars(pfx), pfx)
		if err != nil {
			continue
		}
		for rows.Next() {
			var c childRow
			if err := rows.Scan(&c.id, &c.path); err == nil {
				children = append(children, c)
			}
		}
		_ = rows.Err()
		rows.Close()
	}
	seen := map[int64]bool{}
	for _, c := range children {
		if seen[c.id] {
			continue
		}
		seen[c.id] = true
		suffix := pgSubtreeSuffix(c.path, prefixes)
		if suffix == "" {
			continue
		}
		newPath := strings.TrimRight(restoredPath, "/") + "/" + suffix
		newHash := pathkey.Hash(storageID, newPath)
		_, _ = s.db.ExecContext(ctx, `
			UPDATE nodes
			SET deleted_at=NULL, updated_at=NOW(),
			    path=$1, path_hash=$2, storage_key=$3
			WHERE id=$4`, newPath, newHash, newPath, c.id)
	}
}

// prefixChars is the length SUBSTR needs for a path prefix: SQL counts
// CHARACTERS (SQLite, MySQL and PostgreSQL alike), Go's len counts bytes. Passing
// len() made every folder whose path is not plain ASCII match none of its own
// rows — "/Müşteri/" is 9 characters and 11 bytes — so the folder went to the
// trash and its contents stayed live, and a restore left them in the trash.
func prefixChars(p string) int { return utf8.RuneCountInString(p) }

// pgSubtreePrefixVariants — postgres copy of the sqlite driver helper.
func pgSubtreePrefixVariants(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		norm := strings.TrimRight(path.Clean("/"+strings.Trim(p, "/")), "/")
		if norm == "" || norm == "/" {
			continue
		}
		for _, v := range []string{norm + "/", strings.TrimPrefix(norm, "/") + "/"} {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

func pgSubtreeSuffix(p string, prefixes []string) string {
	for _, pfx := range prefixes {
		if strings.HasPrefix(p, pfx) {
			return strings.TrimPrefix(p, pfx)
		}
	}
	return ""
}

func (s *Store) SoftDeleteNode(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET deleted_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *Store) HardDeleteNode(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id=$1`, id)
	return err
}

// MoveNode re-homes a node: new parent, name, path and path hash — and,
// for a LIVE row, the storage_key that must mirror that path.
//
// ⚠ storage_key is not decoration. It is what internal/versioning, the
// antivirus quarantine, the id-addressed download (`Manager.Read`) and the
// sync tombstone pass hand to the driver as the file's key, each of them
// preferring it over `path`. Leaving it at the pre-move value made every one
// of those address a path the file no longer occupies, and every one of them
// fails SILENTLY on a miss: the snapshot before an overwrite records nothing
// and lets the write through, the quarantine reports success while the
// infected bytes stay live, `confirmGone` tombstones a file that is fine, and
// a download by id 404s on a file the listing shows.
//
// ⚠⚠ The CASE is the trash exception and it is load-bearing. On a trashed row
// storage_key deliberately holds the ORIGINAL path (SoftDeleteAndRetag writes
// it) — that is the only record of where trash.Service.Restore has to put the
// file back, and sync.reconcileTrash reads it to tell a restorable deletion
// from a row that has to be hard-deleted. Overwriting it here would destroy
// both. Live rows mirror path; trashed rows keep their origin.
func (s *Store) MoveNode(ctx context.Context, id int64, parentID *int64, name, path, hash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes
		    SET parent_id=$1, name=$2, path=$3, path_hash=$4,
		        storage_key=CASE WHEN deleted_at IS NULL THEN $3 ELSE storage_key END,
		        updated_at=NOW()
		  WHERE id=$5`, parentID, name, path, hash, id)
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

func (s *Store) ListStaleNodesUnder(ctx context.Context, storageID int64, dir string, before time.Time) ([]*model.Node, error) {
	slashed, bare, ok := treeSpellings(dir)
	if !ok {
		return nil, nil
	}
	args := append([]any{storageID, before}, belowArgs(slashed, bare)...)
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeColumns()+
		` FROM nodes WHERE storage_id=$1 AND seen_at < $2 AND deleted_at IS NULL AND `+belowClause(3), args...)
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

func (s *Store) CountLiveNodesUnder(ctx context.Context, storageID int64, dir string) (int64, error) {
	slashed, bare, ok := treeSpellings(dir)
	if !ok {
		return 0, nil
	}
	var n int64
	args := append([]any{storageID}, belowArgs(slashed, bare)...)
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM nodes WHERE storage_id=$1 AND deleted_at IS NULL AND `+belowClause(2), args...).Scan(&n)
	return n, err
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

// ListDuplicateNodes returns every live file node whose (size, non-empty
// etag) pair occurs more than once — the raw rows behind the admin
// duplicate report. Mirrors the SQLite driver with $N placeholders.
func (s *Store) ListDuplicateNodes(ctx context.Context, minSize int64) ([]db.DuplicateNode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT n.id, n.storage_id, n.path, n.name, n.size, COALESCE(n.etag,'')
		   FROM nodes n
		   JOIN (SELECT size, etag FROM nodes
		          WHERE deleted_at IS NULL AND type='file' AND COALESCE(etag,'')<>'' AND size>=$1
		          GROUP BY size, etag HAVING COUNT(*)>1) dup
		     ON dup.size=n.size AND dup.etag=n.etag
		  WHERE n.deleted_at IS NULL AND n.type='file'
		  ORDER BY n.size DESC, n.etag, n.id`, minSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.DuplicateNode
	for rows.Next() {
		var d db.DuplicateNode
		if err := rows.Scan(&d.ID, &d.StorageID, &d.Path, &d.Name, &d.Size, &d.Etag); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// likeLiteral escapes s for use inside a LIKE pattern whose escape character
// is `\` (PostgreSQL's default), so every character in it matches only itself.
var likeLiteral = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// pgFoldedName is the stored name through internal/namefold, spelt in SQL:
// composed (normalize, PostgreSQL 13 and later), lower-cased by the
// database's ctype, the dotless ı made the fourth i, and the dot above a full
// lower-casing leaves on an i or a j dropped. PostgreSQL's ILIKE alone knew
// none of that: a decomposed `Gu`+U+0308+`rel` does not answer `%gürel%`,
// and `IŞIK` does not answer `%ışık%`. See the SQLite store's SearchNodes for
// the shape of the statement; TestSearchNodesOnEveryEngine holds the engines
// to one answer.
var pgFoldedName = func() string {
	dot := string(rune(0x0307))
	return "replace(replace(translate(lower(normalize(name, NFC)), 'ı', 'i'), 'i" + dot + "', 'i'), 'j" + dot + "', 'j')"
}()

func (s *Store) SearchNodes(ctx context.Context, storageID int64, m model.NameMatch, limit int) ([]*model.Node, error) {
	if limit <= 0 {
		limit = 100
	}
	words := namefold.Words(m.Words)
	if len(words) == 0 {
		return nil, nil
	}
	args := []any{storageID}
	bind := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	conds := []string{`storage_id=$1`, `deleted_at IS NULL`}
	// ILIKE on the stored bytes for the runs and for every word made only of
	// letters each spelling stores as they are (namefold.AllPlain); the
	// normaliser for the rest. The planner orders quals by cost anyway.
	likeOn := func(w string) string {
		if namefold.AllPlain(w) {
			return `name ILIKE `
		}
		return pgFoldedName + ` LIKE `
	}
	for _, r := range m.Runs {
		if r != "" {
			conds = append(conds, `name ILIKE `+bind("%"+likeLiteral.Replace(r)+"%"))
		}
	}
	for _, w := range words {
		conds = append(conds, likeOn(w)+bind("%"+likeLiteral.Replace(w)+"%"))
	}
	// Rank BEFORE the LIMIT (see db.Store.SearchNodes).
	order := `name`
	if p := namefold.String(m.Prefer); p != "" {
		lit, on := likeLiteral.Replace(p), likeOn(p)
		order = `CASE WHEN ` + on + bind(lit) + ` OR ` + on + bind(lit+".%") +
			` THEN 0 WHEN ` + on + bind(lit+"%") + ` THEN 1 ELSE 2 END, length(name), name`
	}
	q := `SELECT ` + nodeColumns() + ` FROM nodes WHERE ` + strings.Join(conds, ` AND `) + ` ORDER BY ` + order + ` LIMIT ` + bind(limit)
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
	`provider_id, COALESCE(oidc_subject,''), COALESCE(quota_bytes,0), COALESCE(usage_bytes,0), COALESCE(enabled,TRUE), ` +
	`COALESCE(avatar_url,''), COALESCE(username,'')`

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

// GetUserByUsername is the username half of dual-side login (migration 00025).
// Callers should reach it through identity.Resolve rather than directly, so
// the e-mail/username disambiguation lives in one place.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username=$1`, username))
}

// SetUserUsername claims a login name. The unique index — not any check above
// this call — is what actually guarantees uniqueness, so a caller racing
// another creation gets an error here and is expected to try another name.
func (s *Store) SetUserUsername(ctx context.Context, id int64, username string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET username=$1, updated_at=NOW() WHERE id=$2`, username, id)
	return err
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

// SetSessionIDToken implements db.Store (migration 00057).
func (s *Store) SetSessionIDToken(ctx context.Context, token, idToken string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET id_token=$1 WHERE token=$2`, idToken, token)
	return err
}

// GetSessionIDToken implements db.Store — see the SQLite store for the rules.
func (s *Store) GetSessionIDToken(ctx context.Context, token string) (string, error) {
	var idToken sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id_token FROM sessions WHERE token=$1`, token).Scan(&idToken)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return idToken.String, err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= NOW()`)
	return err
}

// ─────────────────── API tokens ───────────────────

func (s *Store) CreateAPIToken(ctx context.Context, t *model.APIToken) (*model.APIToken, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO api_tokens (user_id, label, token_hash, scopes, usernames, kind, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		t.UserID, t.Label, t.TokenHash, t.Scopes, t.Usernames, model.NormalizeTokenKind(t.Kind), t.ExpiresAt).Scan(&id)
	if err != nil {
		return nil, err
	}
	t.ID = id
	// See the sqlite driver: echo back what was stored, not what was asked for.
	t.Kind = model.NormalizeTokenKind(t.Kind)
	t.CreatedAt = time.Now()
	return t, nil
}

// ─────────────────── S3 access keys (migration 00026) ───────────────────

const s3KeyCols = `id, access_key_id, secret_enc, user_id, api_token_id, label, bucket, prefix, created_at, last_used_at, expires_at, disabled_at`

func scanS3AccessKey(r rowScanner) (*model.S3AccessKey, error) {
	k := &model.S3AccessKey{}
	var tokenID sql.NullInt64
	if err := r.Scan(&k.ID, &k.AccessKeyID, &k.SecretEnc, &k.UserID, &tokenID, &k.Label,
		&k.Bucket, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.DisabledAt); err != nil {
		return nil, err
	}
	if tokenID.Valid {
		v := tokenID.Int64
		k.APITokenID = &v
	}
	return k, nil
}

func (s *Store) CreateS3AccessKey(ctx context.Context, k *model.S3AccessKey) (*model.S3AccessKey, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO s3_access_keys (access_key_id, secret_enc, user_id, api_token_id, label, bucket, prefix, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		k.AccessKeyID, k.SecretEnc, k.UserID, k.APITokenID, k.Label, k.Bucket, k.Prefix, k.ExpiresAt).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetS3AccessKeyByID(ctx, id)
}

func (s *Store) GetS3AccessKeyByID(ctx context.Context, id int64) (*model.S3AccessKey, error) {
	return scanS3AccessKey(s.db.QueryRowContext(ctx, `SELECT `+s3KeyCols+` FROM s3_access_keys WHERE id=$1`, id))
}

// GetS3AccessKey is the hot path: every signed request looks its key up here,
// so it is a single indexed read and nothing more.
func (s *Store) GetS3AccessKey(ctx context.Context, accessKeyID string) (*model.S3AccessKey, error) {
	return scanS3AccessKey(s.db.QueryRowContext(ctx, `SELECT `+s3KeyCols+` FROM s3_access_keys WHERE access_key_id=$1`, accessKeyID))
}

func (s *Store) ListS3AccessKeys(ctx context.Context, userID int64) ([]*model.S3AccessKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+s3KeyCols+` FROM s3_access_keys WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Never nil — see the sqlite driver for why an empty collection must
	// serialize as [] and not null.
	out := []*model.S3AccessKey{}
	for rows.Next() {
		k, err := scanS3AccessKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) TouchS3AccessKey(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE s3_access_keys SET last_used_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *Store) SetS3AccessKeyDisabled(ctx context.Context, id int64, disabled bool) error {
	if disabled {
		_, err := s.db.ExecContext(ctx, `UPDATE s3_access_keys SET disabled_at=NOW() WHERE id=$1`, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE s3_access_keys SET disabled_at=NULL WHERE id=$1`, id)
	return err
}

// DeleteS3AccessKey takes the owner id too — see the sqlite driver.
func (s *Store) DeleteS3AccessKey(ctx context.Context, id, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM s3_access_keys WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}

// ─────────────────── SSH public keys (migration 00027) ───────────────────

const sshKeyCols = `id, user_id, name, fingerprint, public_key, created_at, last_used_at, disabled_at`

func scanSSHPublicKey(r rowScanner) (*model.SSHPublicKey, error) {
	k := &model.SSHPublicKey{}
	if err := r.Scan(&k.ID, &k.UserID, &k.Name, &k.Fingerprint, &k.PublicKey,
		&k.CreatedAt, &k.LastUsedAt, &k.DisabledAt); err != nil {
		return nil, err
	}
	return k, nil
}

func (s *Store) CreateSSHPublicKey(ctx context.Context, k *model.SSHPublicKey) (*model.SSHPublicKey, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx,
		`INSERT INTO ssh_public_keys (user_id, name, fingerprint, public_key) VALUES ($1,$2,$3,$4) RETURNING id`,
		k.UserID, k.Name, k.Fingerprint, k.PublicKey).Scan(&id); err != nil {
		return nil, err
	}
	return s.GetSSHPublicKeyByID(ctx, id)
}

// GetSSHPublicKey is the login path: one indexed read on the fingerprint.
func (s *Store) GetSSHPublicKey(ctx context.Context, fingerprint string) (*model.SSHPublicKey, error) {
	return scanSSHPublicKey(s.db.QueryRowContext(ctx,
		`SELECT `+sshKeyCols+` FROM ssh_public_keys WHERE fingerprint=$1`, fingerprint))
}

func (s *Store) GetSSHPublicKeyByID(ctx context.Context, id int64) (*model.SSHPublicKey, error) {
	return scanSSHPublicKey(s.db.QueryRowContext(ctx,
		`SELECT `+sshKeyCols+` FROM ssh_public_keys WHERE id=$1`, id))
}

func (s *Store) ListSSHPublicKeys(ctx context.Context, userID int64) ([]*model.SSHPublicKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+sshKeyCols+` FROM ssh_public_keys WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Never nil — see the sqlite driver.
	out := []*model.SSHPublicKey{}
	for rows.Next() {
		k, err := scanSSHPublicKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) TouchSSHPublicKey(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ssh_public_keys SET last_used_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *Store) SetSSHPublicKeyDisabled(ctx context.Context, id int64, disabled bool) error {
	if disabled {
		_, err := s.db.ExecContext(ctx, `UPDATE ssh_public_keys SET disabled_at=NOW() WHERE id=$1`, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE ssh_public_keys SET disabled_at=NULL WHERE id=$1`, id)
	return err
}

// DeleteSSHPublicKey takes the owner id too — see the sqlite driver.
func (s *Store) DeleteSSHPublicKey(ctx context.Context, id, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ssh_public_keys WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}

// ─────────────────── NFS exports (migration 00028) ───────────────────

const nfsExportCols = `id, user_id, api_token_id, label, token_hash, storage_name, prefix, read_only, allow_cidrs, created_at, last_used_at, expires_at, disabled_at`

func scanNFSExport(r rowScanner) (*model.NFSExport, error) {
	e := &model.NFSExport{}
	var tokenID sql.NullInt64
	if err := r.Scan(&e.ID, &e.UserID, &tokenID, &e.Label, &e.TokenHash,
		&e.StorageName, &e.Prefix, &e.ReadOnly, &e.AllowCIDRs,
		&e.CreatedAt, &e.LastUsedAt, &e.ExpiresAt, &e.DisabledAt); err != nil {
		return nil, err
	}
	if tokenID.Valid {
		v := tokenID.Int64
		e.APITokenID = &v
	}
	return e, nil
}

func (s *Store) CreateNFSExport(ctx context.Context, e *model.NFSExport) (*model.NFSExport, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx,
		`INSERT INTO nfs_exports (user_id, api_token_id, label, token_hash, storage_name, prefix, read_only, allow_cidrs, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		e.UserID, e.APITokenID, e.Label, e.TokenHash, e.StorageName, e.Prefix, e.ReadOnly, e.AllowCIDRs, e.ExpiresAt).Scan(&id); err != nil {
		return nil, err
	}
	return s.GetNFSExportByID(ctx, id)
}

// GetNFSExport is the mount path: one indexed read on the hashed secret.
func (s *Store) GetNFSExport(ctx context.Context, tokenHash string) (*model.NFSExport, error) {
	return scanNFSExport(s.db.QueryRowContext(ctx,
		`SELECT `+nfsExportCols+` FROM nfs_exports WHERE token_hash=$1`, tokenHash))
}

func (s *Store) GetNFSExportByID(ctx context.Context, id int64) (*model.NFSExport, error) {
	return scanNFSExport(s.db.QueryRowContext(ctx,
		`SELECT `+nfsExportCols+` FROM nfs_exports WHERE id=$1`, id))
}

func (s *Store) ListNFSExports(ctx context.Context, userID int64) ([]*model.NFSExport, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+nfsExportCols+` FROM nfs_exports WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.NFSExport{}
	for rows.Next() {
		e, err := scanNFSExport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) TouchNFSExport(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nfs_exports SET last_used_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *Store) SetNFSExportDisabled(ctx context.Context, id int64, disabled bool) error {
	if disabled {
		_, err := s.db.ExecContext(ctx, `UPDATE nfs_exports SET disabled_at=NOW() WHERE id=$1`, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE nfs_exports SET disabled_at=NULL WHERE id=$1`, id)
	return err
}

// DeleteNFSExport takes the owner id too — see the sqlite driver.
func (s *Store) DeleteNFSExport(ctx context.Context, id, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM nfs_exports WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}

func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash string) (*model.APIToken, error) {
	return scanAPIToken(s.db.QueryRowContext(ctx,
		`SELECT id, user_id, label, token_hash, scopes, COALESCE(usernames,''), COALESCE(kind,'app'), last_used_at, expires_at, created_at FROM api_tokens WHERE token_hash=$1`,
		tokenHash))
}

// GetAPITokenByID fetches a token by primary key — see the sqlite driver.
func (s *Store) GetAPITokenByID(ctx context.Context, id int64) (*model.APIToken, error) {
	return scanAPIToken(s.db.QueryRowContext(ctx,
		`SELECT id, user_id, label, token_hash, scopes, COALESCE(usernames,''), COALESCE(kind,'app'), last_used_at, expires_at, created_at FROM api_tokens WHERE id=$1`, id))
}

func (s *Store) ListAPITokens(ctx context.Context) ([]*model.APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, label, token_hash, scopes, COALESCE(usernames,''), COALESCE(kind,'app'), last_used_at, expires_at, created_at FROM api_tokens ORDER BY created_at DESC`)
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
		`SELECT id, user_id, label, token_hash, scopes, COALESCE(usernames,''), COALESCE(kind,'app'), last_used_at, expires_at, created_at FROM api_tokens WHERE user_id=$1 ORDER BY created_at DESC`,
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

// UpdateAPITokenMeta edits label, the username allow-list and/or the token
// kind. nil = keep.
func (s *Store) UpdateAPITokenMeta(ctx context.Context, id int64, label, usernames, kind *string) error {
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
	if kind != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET kind=$1 WHERE id=$2`, model.NormalizeTokenKind(*kind), id); err != nil {
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
	if err := r.Scan(&t.ID, &t.UserID, &t.Label, &t.TokenHash, &t.Scopes, &t.Usernames, &t.Kind, &lastUsed, &expires, &t.CreatedAt); err != nil {
		return nil, err
	}
	t.Kind = model.NormalizeTokenKind(t.Kind)
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

// shareCols is the column list every single-row share read shares — see the
// note on the SQLite twin. ⚠ COALESCE on state_json/files_json: 00046 adds
// them NULLABLE (MySQL cannot default a TEXT column) and a NULL scanned into a
// Go string fails inside the driver.
const shareCols = `id, node_id, token, COALESCE(pin_hash,''), expires_at, max_downloads, download_count, created_by, COALESCE(created_via,''), created_at, COALESCE(kind,'download'), max_uploads, upload_count, drop_settings, plugin_id, COALESCE(page_id,''), COALESCE(subject,''), COALESCE(state_json,''), COALESCE(files_json,''), pin_fails, locked_until, COALESCE(pin_enc,''), visit_count, COALESCE(purpose_json,'')`

func (s *Store) CreateShare(ctx context.Context, sh *model.Share) (*model.Share, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO shares (node_id, token, pin_hash, pin_enc, expires_at, max_downloads, created_by, created_via, kind, max_uploads, drop_settings, plugin_id, page_id, subject, state_json, files_json, purpose_json) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING id`,
		sh.NodeID, sh.Token, sh.PinHash, sh.PinEnc, sh.ExpiresAt, sh.MaxDownloads, sh.CreatedBy, sh.CreatedVia, shareKind(sh.Kind), sh.MaxUploads, sh.DropSettings,
		sh.PluginID, sh.PageID, sh.Subject, sh.StateJSON, sh.FilesJSON, sh.PurposeJSON).Scan(&id)
	if err != nil {
		return nil, err
	}
	sh.ID = id
	sh.CreatedAt = time.Now()
	sh.HasPin = sh.PinHash != ""
	sh.PinRecoverable = sh.PinEnc != ""
	return sh, nil
}

func (s *Store) GetShareByToken(ctx context.Context, token string) (*model.Share, error) {
	return scanShare(s.db.QueryRowContext(ctx, `SELECT `+shareCols+` FROM shares WHERE token=$1`, token))
}

func (s *Store) ListSharesByNode(ctx context.Context, nodeID int64) ([]*model.Share, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+shareCols+` FROM shares WHERE node_id=$1 ORDER BY created_at DESC`, nodeID)
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

// IncrementShareVisit counts one opening of an app page (00052): what the
// page's `max_visits` ceiling is measured against, and never a download.
func (s *Store) IncrementShareVisit(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET visit_count = visit_count + 1 WHERE id=$1`, id)
	return err
}

// ReserveShareDownload claims one download against the cap in a single
// statement, so overlapping requests cannot all pass a check that each of them
// read before any of them wrote. False = the cap is already spent.
func (s *Store) ReserveShareDownload(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE shares SET download_count = download_count + 1
		WHERE id=$1 AND (max_downloads IS NULL OR download_count < max_downloads)`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ReleaseShareDownload hands a reserved slot back, never below zero.
func (s *Store) ReleaseShareDownload(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET download_count = download_count - 1
		WHERE id=$1 AND download_count > 0`, id)
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

// ─────────────────── Staged uploads ───────────────────

const stagedUploadColumns = `id, storage_id, storage_key, COALESCE(user_id,0), total_size, chunk_size,
	COALESCE(mime,''), COALESCE(hash,''), received_bytes, state, COALESCE(error,''),
	node_id, op_id, created_at, updated_at, expires_at`

func scanStagedUpload(r rowScanner) (*model.StagedUpload, error) {
	u := &model.StagedUpload{}
	if err := r.Scan(&u.ID, &u.StorageID, &u.StorageKey, &u.UserID, &u.TotalSize, &u.ChunkSize,
		&u.Mime, &u.Hash, &u.ReceivedBytes, &u.State, &u.Error,
		&u.NodeID, &u.OpID, &u.CreatedAt, &u.UpdatedAt, &u.ExpiresAt); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) CreateStagedUpload(ctx context.Context, u *model.StagedUpload) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO staged_uploads (id, storage_id, storage_key, user_id, total_size, chunk_size, mime, hash, received_bytes, state, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		u.ID, u.StorageID, u.StorageKey, u.UserID, u.TotalSize, u.ChunkSize, u.Mime, u.Hash, u.ReceivedBytes, u.State, u.ExpiresAt)
	return err
}

func (s *Store) GetStagedUpload(ctx context.Context, id string) (*model.StagedUpload, error) {
	return scanStagedUpload(s.db.QueryRowContext(ctx,
		`SELECT `+stagedUploadColumns+` FROM staged_uploads WHERE id=$1`, id))
}

func (s *Store) GetStagedUploadByNode(ctx context.Context, nodeID int64) (*model.StagedUpload, error) {
	return scanStagedUpload(s.db.QueryRowContext(ctx,
		`SELECT `+stagedUploadColumns+` FROM staged_uploads WHERE node_id=$1 ORDER BY updated_at DESC LIMIT 1`, nodeID))
}

func (s *Store) UpdateStagedUploadProgress(ctx context.Context, id string, receivedBytes int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE staged_uploads SET received_bytes=$1, updated_at=NOW() WHERE id=$2`, receivedBytes, id)
	return err
}

func (s *Store) UpdateStagedUploadState(ctx context.Context, id, state, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE staged_uploads SET state=$1, error=$2, updated_at=NOW() WHERE id=$3`, state, errMsg, id)
	return err
}

func (s *Store) AttachStagedUploadTarget(ctx context.Context, id string, nodeID, opID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE staged_uploads SET node_id=$1, op_id=$2, updated_at=NOW() WHERE id=$3`, nodeID, opID, id)
	return err
}

func (s *Store) DeleteStagedUpload(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM staged_uploads WHERE id=$1`, id)
	return err
}

func (s *Store) ListStagedUploads(ctx context.Context, state string, limit int) ([]*model.StagedUpload, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var (
		rows *sql.Rows
		err  error
	)
	if state != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+stagedUploadColumns+` FROM staged_uploads WHERE state=$1 ORDER BY updated_at DESC LIMIT $2`, state, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+stagedUploadColumns+` FROM staged_uploads ORDER BY updated_at DESC LIMIT $1`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.StagedUpload{}
	for rows.Next() {
		u, err := scanStagedUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) ListIdleStagedUploads(ctx context.Context, before time.Time, limit int) ([]*model.StagedUpload, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+stagedUploadColumns+` FROM staged_uploads WHERE updated_at < $1 ORDER BY updated_at ASC LIMIT $2`,
		before.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.StagedUpload{}
	for rows.Next() {
		u, err := scanStagedUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) SumOpenStagedUploadBytes(ctx context.Context, userID int64) (int64, error) {
	if userID <= 0 {
		return 0, nil
	}
	// ⚠ Postgres SUM(bigint) returns NUMERIC, which will not scan into an
	// int64. Cast it back explicitly.
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(total_size),0)::bigint FROM staged_uploads WHERE user_id=$1 AND state IN ('staging','committing')`,
		userID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

func (s *Store) ListUnstoredNodes(ctx context.Context, afterID int64, limit int) ([]*model.Node, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeColumns()+
		` FROM nodes WHERE deleted_at IS NULL AND transfer_state IN ('staged','failed') AND id > $1 ORDER BY id LIMIT $2`,
		afterID, limit)
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

func (s *Store) SetNodeTransferState(ctx context.Context, nodeID int64, state string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET transfer_state=$1, updated_at=NOW() WHERE id=$2`, state, nodeID)
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

func (s *Store) GetLastSyncRunByStatus(ctx context.Context, storageID int64, status string) (*model.SyncRun, error) {
	return scanSyncRun(s.db.QueryRowContext(ctx, `SELECT id, storage_id, started_at, finished_at, COALESCE(cursor_before,''), COALESCE(cursor_after,''), seen_count, added, updated, deleted, status, COALESCE(error,'') FROM sync_runs WHERE storage_id=$1 AND status=$2 AND finished_at IS NOT NULL ORDER BY started_at DESC, id DESC LIMIT 1`, storageID, status))
}

func (s *Store) AbortUnfinishedSyncRuns(ctx context.Context, errMsg string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sync_runs SET status='aborted', finished_at=NOW(), error=$1 WHERE finished_at IS NULL`, errMsg)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(value,'') FROM settings WHERE setting_key=$1`, key).Scan(&v)
	return v, err
}

func (s *Store) UpsertSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (setting_key, value, updated_at) VALUES ($1,$2,NOW()) ON CONFLICT(setting_key) DO UPDATE SET value=excluded.value, updated_at=NOW()`,
		key, value)
	return err
}

func (s *Store) ListSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT setting_key, COALESCE(value,'') FROM settings ORDER BY setting_key`)
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
		name, enabled, urlS, secretEnc, optionsJSON, nullTime(lastCheck), lastState)
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

func (s *Store) DeleteThumbnail(ctx context.Context, nodeID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM thumbnails WHERE node_id=$1`, nodeID)
	return err
}

// ExistingNodeIDs batches the lookup so a large thumbnail cache cannot exceed
// the 65535-parameter ceiling of the extended protocol.
func (s *Store) ExistingNodeIDs(ctx context.Context, ids []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(ids))
	const batch = 500
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		ph := make([]string, 0, len(chunk))
		args := make([]any, 0, len(chunk))
		for i, id := range chunk {
			ph = append(ph, fmt.Sprintf("$%d", i+1))
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM nodes WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
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

// nodeColumnsFmt is the ONE place the node column order is written down.
// %[1]s is the table alias ("" for a plain SELECT, "n." for the ones that JOIN
// node_meta and have to disambiguate).
//
// ⚠ It is a format string rather than four hand-kept copies because four is
// what there were, and a column added to `nodes` reached the ordinary listing,
// the search rebuild — and silently missed the tag and starred reads, whose
// scan then failed at runtime on a path the suite only walks in one test.
const nodeColumnsFmt = `%[1]sid, %[1]sstorage_id, %[1]sparent_id, %[1]sname, %[1]spath, %[1]spath_hash, COALESCE(%[1]sstorage_key,''), %[1]stype, %[1]ssize, COALESCE(%[1]smime,''), COALESCE(%[1]setag,''), %[1]sbackend_mtime, %[1]sdb_mtime, %[1]ssync_state, COALESCE(%[1]stransfer_state,'stored'), %[1]sseen_at, %[1]sdeleted_at, %[1]screated_at, %[1]supdated_at, %[1]sowner_id, %[1]slast_actor_id, COALESCE(%[1]sexternal_upload,FALSE)`

var (
	// nodeColumnList is the unqualified list; nodeColumnsN is the "n."-aliased
	// one. Both feed the same scanNode, which is why they cannot drift.
	nodeColumnList = fmt.Sprintf(nodeColumnsFmt, "")
	nodeColumnsN   = fmt.Sprintf(nodeColumnsFmt, "n.")
)

func nodeColumns() string { return nodeColumnList }

func scanNode(r rowScanner) (*model.Node, error) {
	n := &model.Node{}
	err := r.Scan(&n.ID, &n.StorageID, &n.ParentID, &n.Name, &n.Path, &n.PathHash, &n.StorageKey, &n.Type, &n.Size, &n.Mime, &n.Etag, &n.BackendMtime, &n.DBMtime, &n.SyncState, &n.TransferState, &n.SeenAt, &n.DeletedAt, &n.CreatedAt, &n.UpdatedAt, &n.OwnerID, &n.LastActorID, &n.ExternalUpload)
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
		&st.RBACEnabled, &st.UID,
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
	if err := r.Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Role, &u.TOTPSecret, &u.TOTPPendingSecret, &u.TOTPEnabled, &recoveryJSON, &u.Locale, &u.Timezone, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt, &providerID, &u.OIDCSubject, &u.QuotaBytes, &u.UsageBytes, &u.Enabled, &u.AvatarURL, &u.Username); err != nil {
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
	if err := r.Scan(&sh.ID, &sh.NodeID, &sh.Token, &sh.PinHash, &sh.ExpiresAt, &sh.MaxDownloads, &sh.DownloadCount, &sh.CreatedBy, &sh.CreatedVia, &sh.CreatedAt, &sh.Kind, &sh.MaxUploads, &sh.UploadCount, &sh.DropSettings,
		&sh.PluginID, &sh.PageID, &sh.Subject, &sh.StateJSON, &sh.FilesJSON, &sh.PinFails, &sh.LockedUntil, &sh.PinEnc, &sh.VisitCount, &sh.PurposeJSON); err != nil {
		return nil, err
	}
	sh.HasPin = sh.PinHash != ""
	sh.PinRecoverable = sh.PinEnc != ""
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
	if prefixes, ok := db.AuditActionPrefixes(action); ok {
		likes := make([]string, len(prefixes))
		for i, p := range prefixes {
			likes[i] = fmt.Sprintf(`a.action LIKE $%d ESCAPE '\'`, idx)
			args = append(args, p)
			idx++
		}
		cond += " AND (" + strings.Join(likes, " OR ") + ")"
	} else if action != "" {
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

// UpdateUserAvatar stores (or, with an empty string, clears) the profile
// picture. Validation of the URI belongs to the API layer, which is the only
// place that knows what a browser will accept.
func (s *Store) UpdateUserAvatar(ctx context.Context, id int64, avatarURL string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET avatar_url=$1, updated_at=NOW() WHERE id=$2`, avatarURL, id)
	return err
}

// GetShareByID looks up a share by its row ID.
func (s *Store) GetShareByID(ctx context.Context, id int64) (*model.Share, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+shareCols+` FROM shares WHERE id=$1`, id)
	return scanShare(row)
}

// RevokeShare soft-revokes by setting expires_at = NOW (audit trail intact).
func (s *Store) RevokeShare(ctx context.Context, id int64) error {
	// ⚠ Both columns — see the SQLite twin (00053).
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET expires_at=NOW(), revoked_at=NOW() WHERE id=$1`, id)
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
		conds = append(conds, "(s.max_downloads IS NULL OR (CASE WHEN s.plugin_id > 0 AND COALESCE(s.page_id,'') <> '' THEN s.visit_count ELSE s.download_count END) < s.max_downloads)")
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

	rows, err := s.db.QueryContext(ctx, `SELECT `+shareMetaCols+` FROM shares s `+shareMetaJoins+`
		WHERE `+whereSQL+`
		ORDER BY s.id DESC LIMIT `+limPlaceholder+` OFFSET `+offPlaceholder, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, err := scanShareMeta(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// shareMetaCols / shareMetaJoins / scanShareMeta are the ONE admin-list
// projection, matching the SQLite twin column for column.
//
// ⚠ They exist because the two listings here disagreed with their own SELECT:
// the projection named `created_via` and the Scan did not, so every
// ListAllShares call on PostgreSQL failed with "expected 13 destination
// arguments in Scan, not 12" — the admin Shares page was empty on that engine
// and nowhere else. One projection, one scanner, no second place to forget.
//
// ⚠⚠ `pin_enc` is NOT in this list and must never be — see the note on the
// SQLite twin. The boolean below says whether a PIN could be shown; the sealed
// value itself leaves the server through nothing but the one audited
// single-row endpoint.
const shareMetaCols = `s.id, s.node_id, s.token, COALESCE(s.pin_hash,''), s.expires_at, s.max_downloads, s.download_count, s.visit_count, s.created_by, COALESCE(s.created_via,''), s.created_at,
		       s.plugin_id, COALESCE(s.page_id,''), COALESCE(s.subject,''), COALESCE(s.purpose_json,''),
		       CASE WHEN COALESCE(s.pin_enc,'') <> '' THEN 1 ELSE 0 END,
		       s.revoked_at,
		       COALESCE(u.email,''), COALESCE(n.path,''), COALESCE(st.name,''), COALESCE(ap.name,'')`

const shareMetaJoins = `LEFT JOIN users u     ON u.id = s.created_by
		LEFT JOIN nodes n     ON n.id = s.node_id
		LEFT JOIN storages st ON st.id = n.storage_id
		LEFT JOIN app_plugins ap ON ap.id = s.plugin_id`

func scanShareMeta(rows *sql.Rows) ([]*db.ShareWithMeta, error) {
	out := []*db.ShareWithMeta{}
	for rows.Next() {
		sh := &model.Share{}
		row := &db.ShareWithMeta{Share: sh}
		// 0/1 rather than a bool — see the SQLite twin.
		var pinRecoverable int
		if err := rows.Scan(&sh.ID, &sh.NodeID, &sh.Token, &sh.PinHash, &sh.ExpiresAt, &sh.MaxDownloads, &sh.DownloadCount, &sh.VisitCount, &sh.CreatedBy, &sh.CreatedVia, &sh.CreatedAt,
			&sh.PluginID, &sh.PageID, &sh.Subject, &sh.PurposeJSON, &pinRecoverable,
			&sh.RevokedAt,
			&row.CreatorEmail, &row.NodePath, &row.StorageName, &row.PluginName); err != nil {
			return nil, err
		}
		sh.HasPin = sh.PinHash != ""
		sh.PinRecoverable = pinRecoverable != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListAppPluginShares is the admin view of the links APPS opened. pluginID 0
// means every app; it never means every share (that is ListAllShares).
func (s *Store) ListAppPluginShares(ctx context.Context, pluginID int64, activeOnly bool, limit, offset int) ([]*db.ShareWithMeta, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	conds := []string{"s.plugin_id > 0"}
	args := []any{}
	idx := 1
	if pluginID > 0 {
		conds = append(conds, fmt.Sprintf("s.plugin_id=$%d", idx))
		args = append(args, pluginID)
		idx++
	}
	if activeOnly {
		conds = append(conds, "(s.expires_at IS NULL OR s.expires_at > NOW())")
		conds = append(conds, "(s.max_downloads IS NULL OR (CASE WHEN s.plugin_id > 0 AND COALESCE(s.page_id,'') <> '' THEN s.visit_count ELSE s.download_count END) < s.max_downloads)")
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

	rows, err := s.db.QueryContext(ctx, `SELECT `+shareMetaCols+` FROM shares s `+shareMetaJoins+`
		WHERE `+whereSQL+`
		ORDER BY s.id DESC LIMIT `+limPlaceholder+` OFFSET `+offPlaceholder, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, err := scanShareMeta(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// UpdateShareAppState replaces the plugin's durable record for one link.
func (s *Store) UpdateShareAppState(ctx context.Context, id int64, stateJSON string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET state_json=$1 WHERE id=$2`, orJSON(stateJSON, "{}"), id)
	return err
}

// UpdateSharePinLock writes the strike counter and the lock deadline and
// NOTHING else — a wrong PIN must not be able to move an expiry or a cap.
func (s *Store) UpdateSharePinLock(ctx context.Context, id int64, fails int, until *time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET pin_fails=$1, locked_until=$2 WHERE id=$3`, fails, until, id)
	return err
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

// ListNodeIDsWithVersions returns the distinct node ids holding at least
// one version row — the work list for the daily retention job.
func (s *Store) ListNodeIDsWithVersions(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT node_id FROM node_versions ORDER BY node_id`)
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

// SetUserEnabled flips the account on/off. Files, quota and grants are
// untouched -- this is an access switch, not a soft delete.
func (s *Store) SetUserEnabled(ctx context.Context, userID int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET enabled=$1 WHERE id=$2`, enabled, userID)
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

// RecomputeUserUsage scans the file nodes owned by this user, sets usage_bytes.
//
// ⚠ TRASHED ROWS ARE INCLUDED — no `deleted_at IS NULL`, deliberately. See the
// SQLite driver's copy for the full reasoning; in short, trashed bytes still
// count and the purge is the only release point (docs/QUOTAS.md).
func (s *Store) RecomputeUserUsage(ctx context.Context, userID int64) (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(size),0) FROM nodes WHERE owner_id=$1 AND type='file'`,
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

// GetUserDisplayNames — see the sqlite driver for why this returns names and
// not users.
func (s *Store) GetUserDisplayNames(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	const batch = 500
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		ph := make([]string, 0, len(chunk))
		args := make([]any, 0, len(chunk))
		for i, id := range chunk {
			ph = append(ph, fmt.Sprintf("$%d", i+1))
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT id, COALESCE(display_name,''), COALESCE(username,''), COALESCE(email,'') FROM users WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var display, username, email string
			if err := rows.Scan(&id, &display, &username, &email); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = model.PersonLabel(display, username, email)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SetNodeActor updates the last_actor_id column for one node. nil is SYSTEM.
func (s *Store) SetNodeActor(ctx context.Context, nodeID int64, actorID *int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET last_actor_id=$1 WHERE id=$2`, actorID, nodeID)
	return err
}

// SetNodeExternalUpload marks a node as having arrived through an anonymous
// drop link.
func (s *Store) SetNodeExternalUpload(ctx context.Context, nodeID int64, external bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET external_upload=$1 WHERE id=$2`, external, nodeID)
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

// ListTrashedExpired returns soft-deleted nodes whose deleted_at is older than
// `before`, in id order after `afterID`, narrowed to storageIDs (see db.Store
// for why the id, and why the narrowing is in the SQL).
func (s *Store) ListTrashedExpired(ctx context.Context, before time.Time, storageIDs []int64, afterID int64, limit int) ([]*model.Node, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	if storageIDs != nil && len(storageIDs) == 0 {
		return nil, nil // a scope that reaches no storage sees nothing
	}
	where := `deleted_at IS NOT NULL AND deleted_at < $1 AND id > $2`
	args := []any{before, afterID}
	if storageIDs != nil {
		ph := make([]string, len(storageIDs))
		for i, id := range storageIDs {
			args = append(args, id)
			ph[i] = fmt.Sprintf("$%d", len(args))
		}
		where += ` AND storage_id IN (` + strings.Join(ph, ",") + `)`
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+nodeColumns()+`
		 FROM nodes WHERE `+where+`
		 ORDER BY id ASC LIMIT `+fmt.Sprintf("$%d", len(args)), args...)
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

// CountTrashedExpired tallies per storage what ListTrashedExpired walks for
// the same cutoff.
//
// ⚠ SUM(bigint) is NUMERIC here, which will not scan into an int64: cast it.
func (s *Store) CountTrashedExpired(ctx context.Context, before time.Time) (map[int64]db.TrashTally, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT storage_id, COUNT(*), COALESCE(SUM(CASE WHEN type='file' THEN size ELSE 0 END), 0)::bigint
		  FROM nodes WHERE deleted_at IS NOT NULL AND deleted_at < $1
		 GROUP BY storage_id`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]db.TrashTally{}
	for rows.Next() {
		var sid int64
		var t db.TrashTally
		if err := rows.Scan(&sid, &t.Count, &t.Bytes); err != nil {
			return nil, err
		}
		out[sid] = t
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
// Trashed directories also revive the descendants dragged in with them
// (SoftDeleteAndRetag mirror).
func (s *Store) RestoreNodeAt(ctx context.Context, id int64, parentID *int64, origPath string) error {
	clean := strings.TrimRight(path.Clean("/"+strings.Trim(origPath, "/")), "/")
	if clean == "" {
		clean = "/"
	}
	row := s.db.QueryRowContext(ctx, `SELECT storage_id, type, path FROM nodes WHERE id=$1`, id)
	var sid int64
	var nodeType, trashPath string
	if err := row.Scan(&sid, &nodeType, &trashPath); err != nil {
		return err
	}
	hash := pathkey.Hash(sid, clean)
	name := path.Base(clean)
	if parentID == nil {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE nodes
			SET deleted_at=NULL, updated_at=NOW(), parent_id=NULL,
			    name=$1, path=$2, path_hash=$3, storage_key=$4
			WHERE id=$5`, name, clean, hash, clean, id); err != nil {
			return err
		}
	} else {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE nodes
			SET deleted_at=NULL, updated_at=NOW(), parent_id=$1,
			    name=$2, path=$3, path_hash=$4, storage_key=$5
			WHERE id=$6`, *parentID, name, clean, hash, clean, id); err != nil {
			return err
		}
	}
	if nodeType == string(model.NodeTypeDirectory) && trashPath != "" {
		s.restoreTrashedSubtree(ctx, sid, []string{trashPath}, clean)
	}
	return nil
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

// ─────────────────── User-scoped node meta ───────────────────

// SetUserNodeMeta upserts a (user, node, key) row.
func (s *Store) SetUserNodeMeta(ctx context.Context, userID, nodeID int64, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_node_meta (user_id, node_id, meta_key, value, updated_at)
		 VALUES ($1,$2,$3,$4,NOW())
		 ON CONFLICT(user_id, node_id, meta_key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`,
		userID, nodeID, key, value)
	return err
}

// DeleteUserNodeMeta removes a single (user, node, key) row.
func (s *Store) DeleteUserNodeMeta(ctx context.Context, userID, nodeID int64, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_node_meta WHERE user_id=$1 AND node_id=$2 AND meta_key=$3`, userID, nodeID, key)
	return err
}

// GetUserNodeMeta fetches a single value (returns empty string + sql.ErrNoRows if absent).
func (s *Store) GetUserNodeMeta(ctx context.Context, userID, nodeID int64, key string) (string, error) {
	var v sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT value FROM user_node_meta WHERE user_id=$1 AND node_id=$2 AND meta_key=$3`, userID, nodeID, key).Scan(&v)
	if err != nil {
		return "", err
	}
	return v.String, nil
}

// ListUserNodeMetaForNode returns all (key,value) for one (user,node) pair.
func (s *Store) ListUserNodeMetaForNode(ctx context.Context, userID, nodeID int64, prefix string) (map[string]string, error) {
	q := `SELECT meta_key, COALESCE(value,'') FROM user_node_meta WHERE user_id=$1 AND node_id=$2`
	args := []any{userID, nodeID}
	if prefix != "" {
		q += ` AND meta_key LIKE $3`
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
		`SELECT `+nodeColumnsN+`
		 FROM user_node_meta m
		 INNER JOIN nodes n ON n.id = m.node_id
		 WHERE m.user_id=$1 AND m.meta_key=$2 AND n.deleted_at IS NULL
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

// ─────────────────── Tags (00055: personal + team) ───────────────────
//
// Vocabulary rows in `tags`, links in `node_tags`. Who may SEE which row is
// decided in handlers/tags.go, not here — see the db.Store comment.

const tagCols = `id, kind, tenant_id, owner_id, name, name_key, created_at`

func pgPH(n int) string { return fmt.Sprintf("$%d", n) }

// scanTags drains a tag result set. Always a non-nil slice.
func scanTags(rows *sql.Rows) ([]*model.Tag, error) {
	defer rows.Close()
	out := []*model.Tag{}
	for rows.Next() {
		t := &model.Tag{}
		var tenant, owner sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Kind, &tenant, &owner, &t.Name, &t.Key, &t.CreatedAt); err != nil {
			return nil, err
		}
		if tenant.Valid {
			v := tenant.Int64
			t.TenantID = &v
		}
		if owner.Valid {
			v := owner.Int64
			t.OwnerID = &v
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListTags implements db.Store.
func (s *Store) ListTags(ctx context.Context, q model.TagQuery) ([]*model.Tag, error) {
	where, args := db.TagQueryWhere(q, "t", 1, pgPH)
	rows, err := s.db.QueryContext(ctx, `SELECT `+tagCols+` FROM tags t WHERE `+where+` ORDER BY t.id`, args...)
	if err != nil {
		return nil, err
	}
	return scanTags(rows)
}

// CreateTag implements db.Store.
func (s *Store) CreateTag(ctx context.Context, t *model.Tag) (*model.Tag, error) {
	out := *t
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO tags (kind, tenant_id, owner_id, name, name_key) VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
		t.Kind, t.TenantID, t.OwnerID, t.Name, t.Key).Scan(&out.ID, &out.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListNodeTags implements db.Store.
func (s *Store) ListNodeTags(ctx context.Context, nodeID int64) ([]*model.Tag, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.kind, t.tenant_id, t.owner_id, t.name, t.name_key, t.created_at
		 FROM node_tags nt JOIN tags t ON t.id = nt.tag_id
		 WHERE nt.node_id = $1 ORDER BY t.id`, nodeID)
	if err != nil {
		return nil, err
	}
	return scanTags(rows)
}

// LinkNodeTags implements db.Store.
func (s *Store) LinkNodeTags(ctx context.Context, nodeID int64, add, remove []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range add {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO node_tags (tag_id, node_id) VALUES ($1,$2) ON CONFLICT (tag_id, node_id) DO NOTHING`, id, nodeID); err != nil {
			return err
		}
	}
	for _, id := range remove {
		if _, err := tx.ExecContext(ctx, `DELETE FROM node_tags WHERE tag_id=$1 AND node_id=$2`, id, nodeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM tags WHERE id=$1 AND NOT EXISTS (SELECT 1 FROM node_tags WHERE tag_id=$1)`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListNodesByTagIDs implements db.Store.
func (s *Store) ListNodesByTagIDs(ctx context.Context, tagIDs []int64, limit int) ([]*model.Node, error) {
	if len(tagIDs) == 0 {
		return []*model.Node{}, nil
	}
	if limit <= 0 || limit > 10000 {
		limit = 500
	}
	in, args := db.IDList(tagIDs, 1, pgPH)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+nodeColumnsN+`
		 FROM nodes n
		 WHERE n.deleted_at IS NULL
		   AND n.id IN (SELECT node_id FROM node_tags WHERE tag_id IN (`+in+`))
		 ORDER BY n.updated_at DESC
		 LIMIT `+pgPH(len(args)+1), append(args, limit)...)
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

// ListTagPlacements implements db.Store.
func (s *Store) ListTagPlacements(ctx context.Context, q model.TagQuery) ([]model.TagPlacement, error) {
	where, args := db.TagQueryWhere(q, "t", 1, pgPH)
	rows, err := s.db.QueryContext(ctx,
		`SELECT nt.tag_id, n.id, n.storage_id, n.path
		 FROM node_tags nt
		 JOIN tags t ON t.id = nt.tag_id
		 JOIN nodes n ON n.id = nt.node_id
		 WHERE n.deleted_at IS NULL AND `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.TagPlacement{}
	for rows.Next() {
		var p model.TagPlacement
		if err := rows.Scan(&p.TagID, &p.NodeID, &p.StorageID, &p.Path); err != nil {
			return nil, err
		}
		out = append(out, p)
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

// pgPlaceholders is n numbered placeholders starting at idx, and the next free
// index.
func pgPlaceholders(n, idx int) (string, int) {
	ph := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ph = append(ph, fmt.Sprintf("$%d", idx))
		idx++
	}
	return strings.Join(ph, ","), idx
}

// mutedEventsClause builds the `n.event NOT IN ($n,…)` fragment and its args
// for a per-user mute list, starting numbering at idx. It returns the next free
// placeholder index so the caller can keep numbering LIMIT/OFFSET after it.
// Empty list ⇒ empty clause and idx unchanged.
//
// ⚠ Placeholders, never literals: the event ids come from a user-writable
// JSON column.
func mutedEventsClause(muted []string, idx int) (string, []any, int) {
	if len(muted) == 0 {
		return "", nil, idx
	}
	args := make([]any, 0, len(muted))
	for _, e := range muted {
		args = append(args, e)
	}
	ph, next := pgPlaceholders(len(muted), idx)
	return "n.event NOT IN (" + ph + ")", args, next
}

// hiddenBodiesClause builds `(COALESCE(n.body,”) NOT LIKE $n AND …)` for the
// notify service's read-time filter (db.Store.ListNotifications), numbering
// from idx and returning the next free index. Empty list ⇒ empty clause.
func hiddenBodiesClause(patterns []string, idx int) (string, []any, int) {
	if len(patterns) == 0 {
		return "", nil, idx
	}
	parts := make([]string, 0, len(patterns))
	args := make([]any, 0, len(patterns))
	for _, p := range patterns {
		parts = append(parts, fmt.Sprintf("COALESCE(n.body,'') NOT LIKE $%d", idx))
		args = append(args, p)
		idx++
	}
	return "(" + strings.Join(parts, " AND ") + ")", args, idx
}

// bellClause builds the per-user predicate over `notifications n`, starting
// numbering at idx: the reader's own rows, plus the broadcasts (user_id NULL)
// the filter admits. It returns the next free placeholder index. The zero
// filter admits every broadcast, the predicate this read always had.
//
// since > 0 keeps only the broadcasts after the reader's "mark all read" point
// — an unread read passes it — inside the broadcast branch, where it is a range
// on idx_notifications_unread_since rather than a filter over every broadcast
// nobody stamped. See the SQLite driver.
//
// ⚠ Placeholders, never literals, for the same reason as mutedEventsClause.
// ⚠ Every column is qualified: the per-reader join brings its own user_id.
//
// f.OwnOnly reads the reader's own rows alone, f.BroadcastsOnly the admitted
// broadcasts alone (model.BroadcastFilter).
func bellClause(userID int64, f model.BroadcastFilter, since int64, idx int) (string, []any, int) {
	events, op := f.Only, "IN"
	if len(events) == 0 {
		events, op = f.Except, "NOT IN"
	}
	if f.OwnOnly {
		return fmt.Sprintf("n.user_id = $%d", idx), []any{userID}, idx + 1
	}
	args := make([]any, 0, len(events)+2)
	self := 0
	if !f.BroadcastsOnly {
		self = idx
		idx++
		args = append(args, userID)
	}
	broadcast := "n.user_id IS NULL"
	if since > 0 {
		broadcast += fmt.Sprintf(" AND n.id > $%d", idx)
		args = append(args, since)
		idx++
	}
	if len(events) > 0 {
		var ph string
		ph, idx = pgPlaceholders(len(events), idx)
		broadcast += " AND n.event " + op + " (" + ph + ")"
		for _, e := range events {
			args = append(args, e)
		}
	}
	if f.BroadcastsOnly {
		return "(" + broadcast + ")", args, idx
	}
	return fmt.Sprintf("(n.user_id = $%d OR (%s))", self, broadcast), args, idx
}

// readThrough is a reader's "mark all read" point (migration 00056): every
// broadcast up to `through` is read for them, as of `at`. Zero when they never
// pressed it.
type readThrough struct {
	through int64
	at      time.Time
}

func (s *Store) readThroughOf(ctx context.Context, readerID int64) (readThrough, error) {
	var rt readThrough
	if readerID <= 0 {
		return rt, nil
	}
	err := s.db.QueryRowContext(ctx,
		`SELECT through_id, read_at FROM notification_read_through WHERE user_id=$1`, readerID,
	).Scan(&rt.through, &rt.at)
	if errors.Is(err, sql.ErrNoRows) {
		return readThrough{}, nil
	}
	if err != nil {
		return readThrough{}, fmt.Errorf("postgres: read-through: %w", err)
	}
	return rt, nil
}

// apply gives a broadcast at or below the reader's "mark all read" point the
// time of that press, when nothing else marked it read.
func (rt readThrough) apply(n *model.Notification) {
	if n.UserID == nil && n.ReadAt == nil && rt.through > 0 && n.ID <= rt.through {
		t := rt.at
		n.ReadAt = &t
	}
}

// readerJoin joins a reader's single marks on broadcasts, numbering from idx,
// and names the select-list column that carries them. With no reader there is
// no join and the column is a typed NULL, so the row shape is the same.
func readerJoin(readerID int64, idx int) (join, col string, args []any, next int) {
	if readerID <= 0 {
		return "", "NULL::timestamptz", nil, idx
	}
	return fmt.Sprintf(" LEFT JOIN notification_reads r ON r.notification_id = n.id AND r.user_id = $%d", idx),
		"r.read_at", []any{readerID}, idx + 1
}

// unreadClause is the unread predicate, numbering from idx. For a reader a
// broadcast is unread when nobody stamped its own column, they have not marked
// it on its own, and it is after their "mark all read" point; a row addressed
// to a user has no marks and no point, its own read_at speaks. A per-user read
// carries the point inside bellClause; only the admin-global read (history)
// needs it here.
func unreadClause(readerID int64, rt readThrough, history bool, idx int) (string, []any, int) {
	if readerID <= 0 {
		return "n.read_at IS NULL", nil, idx
	}
	if history && rt.through > 0 {
		return fmt.Sprintf("n.read_at IS NULL AND r.read_at IS NULL AND (n.user_id IS NOT NULL OR n.id > $%d)", idx),
			[]any{rt.through}, idx + 1
	}
	return "n.read_at IS NULL AND r.read_at IS NULL", nil, idx
}

// ListNotifications paginates either user or admin views. onlyUnread keeps the
// rows still unread for the reader; mutedEvents drops the event ids the user
// has muted, empty means no mute filter; hiddenBodies drops rows whose body
// matches one of the LIKE patterns; broadcasts decides which broadcasts the
// bell takes at all and whose read state they carry (see db.Store).
func (s *Store) ListNotifications(ctx context.Context, userID *int64, onlyUnread bool, mutedEvents, hiddenBodies []string, broadcasts model.BroadcastFilter, limit, offset int) ([]*model.Notification, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rt, err := s.readThroughOf(ctx, broadcasts.ReaderID)
	if err != nil {
		return nil, 0, err
	}
	join, readCol, args, idx := readerJoin(broadcasts.ReaderID, 1)
	var since int64
	if onlyUnread {
		since = rt.through
	}
	var whereC []string
	if userID != nil {
		clause, bellArgs, next := bellClause(*userID, broadcasts, since, idx)
		whereC = append(whereC, clause)
		args = append(args, bellArgs...)
		idx = next
	}
	if onlyUnread {
		clause, unreadArgs, next := unreadClause(broadcasts.ReaderID, rt, userID == nil, idx)
		whereC = append(whereC, clause)
		args = append(args, unreadArgs...)
		idx = next
	}
	if clause, muteArgs, next := mutedEventsClause(mutedEvents, idx); clause != "" {
		whereC = append(whereC, clause)
		args = append(args, muteArgs...)
		idx = next
	}
	if clause, hideArgs, next := hiddenBodiesClause(hiddenBodies, idx); clause != "" {
		whereC = append(whereC, clause)
		args = append(args, hideArgs...)
		idx = next
	}
	from := " FROM notifications n" + join
	if len(whereC) > 0 {
		from += " WHERE " + strings.Join(whereC, " AND ")
	}

	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*)"+from, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: count notifications: %w", err)
	}

	args = append(args, limit, offset)
	q := fmt.Sprintf(
		`SELECT n.id, n.event, n.severity, n.title, n.body, n.meta_json::text,
		        n.user_id, n.read_at, n.webhook_status, COALESCE(n.webhook_error,''), n.created_at, %s%s
		 ORDER BY n.created_at DESC, n.id DESC
		 LIMIT $%d OFFSET $%d`, readCol, from, idx, idx+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list notifications: %w", err)
	}
	defer rows.Close()
	var out []*model.Notification
	for rows.Next() {
		n, err := scanNotificationForReaderPg(rows)
		if err != nil {
			return nil, 0, err
		}
		rt.apply(n)
		out = append(out, n)
	}
	return out, total, rows.Err()
}

// MarkNotificationRead bumps read_at on a single row. With a userID the row
// must be ADDRESSED to that user: a broadcast is many readers' row and is
// marked per reader by MarkBroadcastsRead.
func (s *Store) MarkNotificationRead(ctx context.Context, id int64, userID *int64) error {
	q := `UPDATE notifications SET read_at = NOW()
	      WHERE id=$1 AND read_at IS NULL`
	args := []any{id}
	if userID != nil {
		q += ` AND user_id = $2`
		args = append(args, *userID)
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("postgres: mark notif read: %w", err)
	}
	return nil
}

// MarkAllNotificationsRead bumps read_at for every unread row addressed to
// userID — never a broadcast, see MarkNotificationRead. Pass nil for the
// global "mark all" admin sweep.
func (s *Store) MarkAllNotificationsRead(ctx context.Context, userID *int64) error {
	q := `UPDATE notifications SET read_at = NOW() WHERE read_at IS NULL`
	var args []any
	if userID != nil {
		q += ` AND user_id = $1`
		args = append(args, *userID)
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// MarkAllBroadcastsRead moves readerID's "mark all read" point to the newest
// notification — never backwards — and drops the single marks it overtook.
func (s *Store) MarkAllBroadcastsRead(ctx context.Context, readerID int64) error {
	if readerID <= 0 {
		return nil
	}
	var through int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM notifications`).Scan(&through); err != nil {
		return fmt.Errorf("postgres: newest notification: %w", err)
	}
	if through == 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_read_through AS t (user_id, through_id, read_at) VALUES ($1, $2, NOW())
		 ON CONFLICT (user_id) DO UPDATE SET
		   read_at = CASE WHEN EXCLUDED.through_id > t.through_id THEN EXCLUDED.read_at ELSE t.read_at END,
		   through_id = GREATEST(t.through_id, EXCLUDED.through_id)`, readerID, through); err != nil {
		return fmt.Errorf("postgres: mark all broadcasts read: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM notification_reads WHERE user_id = $1 AND notification_id <= $2`, readerID, through); err != nil {
		return fmt.Errorf("postgres: drop overtaken marks: %w", err)
	}
	return nil
}

// markBroadcastsBatch bounds one statement's placeholders.
const markBroadcastsBatch = 400

// MarkBroadcastsRead records readerID's marks on the given broadcasts, in one
// statement per batch: only ids that are broadcasts nobody stamped through the
// shared column get a mark, and a mark that exists already is left alone.
func (s *Store) MarkBroadcastsRead(ctx context.Context, readerID int64, ids []int64) error {
	if readerID <= 0 {
		return nil
	}
	for start := 0; start < len(ids); start += markBroadcastsBatch {
		batch := ids[start:min(start+markBroadcastsBatch, len(ids))]
		args := make([]any, 0, len(batch)+1)
		args = append(args, readerID)
		for _, id := range batch {
			args = append(args, id)
		}
		ph, _ := pgPlaceholders(len(batch), 2)
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO notification_reads (notification_id, user_id)
			 SELECT id, $1 FROM notifications
			 WHERE user_id IS NULL AND read_at IS NULL AND id IN (`+ph+`)
			 ON CONFLICT (notification_id, user_id) DO NOTHING`, args...); err != nil {
			return fmt.Errorf("postgres: mark broadcasts read: %w", err)
		}
	}
	return nil
}

// UnreadNotificationCount returns the bell badge number for a user.
func (s *Store) UnreadNotificationCount(ctx context.Context, userID *int64, mutedEvents, hiddenBodies []string, broadcasts model.BroadcastFilter) (int64, error) {
	rt, err := s.readThroughOf(ctx, broadcasts.ReaderID)
	if err != nil {
		return 0, err
	}
	join, _, args, idx := readerJoin(broadcasts.ReaderID, 1)
	unread, unreadArgs, idx := unreadClause(broadcasts.ReaderID, rt, userID == nil, idx)
	q := `SELECT COUNT(*) FROM notifications n` + join + ` WHERE ` + unread
	args = append(args, unreadArgs...)
	if userID != nil {
		clause, bellArgs, next := bellClause(*userID, broadcasts, rt.through, idx)
		q += ` AND ` + clause
		args = append(args, bellArgs...)
		idx = next
	}
	if clause, muteArgs, next := mutedEventsClause(mutedEvents, idx); clause != "" {
		q += ` AND ` + clause
		args = append(args, muteArgs...)
		idx = next
	}
	if clause, hideArgs, _ := hiddenBodiesClause(hiddenBodies, idx); clause != "" {
		q += ` AND ` + clause
		args = append(args, hideArgs...)
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

// ─────────────────── Webhook targets (webhook v2) ───────────────────

// CreateWebhookTarget inserts a new delivery destination and returns
// the stored row (with id + created_at filled in).
func (s *Store) CreateWebhookTarget(ctx context.Context, t *model.WebhookTarget) (*model.WebhookTarget, error) {
	if t == nil || t.Name == "" || t.URL == "" {
		return nil, errors.New("postgres: webhook target missing name/url")
	}
	var id int64
	if err := s.db.QueryRowContext(ctx,
		`INSERT INTO webhook_targets (name, url, secret, events, enabled)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		t.Name, t.URL, t.Secret, t.Events, t.Enabled,
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("postgres: insert webhook target: %w", err)
	}
	return s.GetWebhookTarget(ctx, id)
}

// GetWebhookTarget returns a single target by id.
func (s *Store) GetWebhookTarget(ctx context.Context, id int64) (*model.WebhookTarget, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, url, secret, events, enabled, created_at, last_status, last_error, last_delivery_at
		 FROM webhook_targets WHERE id=$1`, id)
	return pgScanWebhookTarget(row)
}

// ListWebhookTargets returns every target ordered by id. Enabled/event
// filtering happens in the notify dispatcher, not in SQL — the table is
// tiny and the admin list needs disabled rows too.
func (s *Store) ListWebhookTargets(ctx context.Context) ([]*model.WebhookTarget, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, url, secret, events, enabled, created_at, last_status, last_error, last_delivery_at
		 FROM webhook_targets ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list webhook targets: %w", err)
	}
	defer rows.Close()
	var out []*model.WebhookTarget
	for rows.Next() {
		t, err := pgScanWebhookTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// pgScanWebhookTarget accepts both *sql.Row and *sql.Rows and maps the
// nullable last-delivery columns (migration 00019) into pointers.
func pgScanWebhookTarget(rs interface {
	Scan(...any) error
}) (*model.WebhookTarget, error) {
	t := &model.WebhookTarget{}
	var (
		lastSt  sql.NullInt64
		lastErr sql.NullString
		lastAt  sql.NullTime
	)
	if err := rs.Scan(&t.ID, &t.Name, &t.URL, &t.Secret, &t.Events, &t.Enabled, &t.CreatedAt, &lastSt, &lastErr, &lastAt); err != nil {
		return nil, err
	}
	if lastSt.Valid {
		v := int(lastSt.Int64)
		t.LastStatus = &v
	}
	if lastErr.Valid {
		v := lastErr.String
		t.LastError = &v
	}
	if lastAt.Valid {
		v := lastAt.Time
		t.LastDeliveryAt = &v
	}
	return t, nil
}

// UpdateWebhookTarget replaces the mutable columns of a target row.
func (s *Store) UpdateWebhookTarget(ctx context.Context, t *model.WebhookTarget) error {
	if t == nil || t.ID == 0 || t.Name == "" || t.URL == "" {
		return errors.New("postgres: webhook target missing id/name/url")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE webhook_targets SET name=$1, url=$2, secret=$3, events=$4, enabled=$5 WHERE id=$6`,
		t.Name, t.URL, t.Secret, t.Events, t.Enabled, t.ID)
	if err != nil {
		return fmt.Errorf("postgres: update webhook target: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteWebhookTarget removes a target row.
func (s *Store) DeleteWebhookTarget(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhook_targets WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("postgres: delete webhook target: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateWebhookTargetDelivery records the newest delivery outcome
// (migration 00019). errMsg "" (success) stores NULL in last_error.
func (s *Store) UpdateWebhookTargetDelivery(ctx context.Context, id int64, httpStatus int, errMsg string, at time.Time) error {
	var errVal any
	if errMsg != "" {
		errVal = errMsg
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE webhook_targets SET last_status=$1, last_error=$2, last_delivery_at=$3 WHERE id=$4`,
		httpStatus, errVal, at.UTC(), id)
	if err != nil {
		return fmt.Errorf("postgres: update webhook target delivery: %w", err)
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

// scanNotificationForReaderPg scans a list row: the stored columns plus the
// reader's mark on a broadcast (NULL when there is none, or no reader). The
// mark wins over the row's own read_at — for the reader it is the answer.
func scanNotificationForReaderPg(rs interface {
	Scan(...any) error
}) (*model.Notification, error) {
	n := &model.Notification{}
	var (
		metaRaw  string
		userID   sql.NullInt64
		readAt   sql.NullTime
		errMsg   string
		readerAt sql.NullTime
	)
	if err := rs.Scan(
		&n.ID, &n.Event, &n.Severity, &n.Title, &n.Body, &metaRaw,
		&userID, &readAt, &n.WebhookStatus, &errMsg, &n.CreatedAt, &readerAt,
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
	switch {
	case readerAt.Valid:
		t := readerAt.Time
		n.ReadAt = &t
	case readAt.Valid:
		t := readAt.Time
		n.ReadAt = &t
	}
	n.WebhookError = errMsg
	return n, nil
}

// scanNotificationPg accepts both *sql.Row and *sql.Rows. Matches the
// column list used by GetNotification; a list row carries one more column,
// see scanNotificationForReaderPg.
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

/* calisma:d3 comments */

// ─────────────────── Node comments (v0.6 "Çalışma" (Work)) ───────────────────

// CreateNodeComment inserts a comment row and returns the stored row
// (with id + timestamps filled in). Body validation (length, emptiness)
// belongs to internal/comments — the store persists what it is given.
func (s *Store) CreateNodeComment(ctx context.Context, c *model.NodeComment) (*model.NodeComment, error) {
	if c == nil || c.NodeID == 0 || c.UserID == 0 || c.Body == "" {
		return nil, errors.New("postgres: node comment missing node/user/body")
	}
	var id int64
	if err := s.db.QueryRowContext(ctx,
		`INSERT INTO node_comments (node_id, user_id, body) VALUES ($1,$2,$3) RETURNING id`,
		c.NodeID, c.UserID, c.Body,
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("postgres: insert node comment: %w", err)
	}
	return s.GetNodeComment(ctx, id)
}

// GetNodeComment returns a single live (not soft-deleted) comment by id.
func (s *Store) GetNodeComment(ctx context.Context, id int64) (*model.NodeComment, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT c.id, c.node_id, c.user_id, c.body, c.created_at, c.updated_at,
		        COALESCE(NULLIF(u.display_name, ''), u.email)
		 FROM node_comments c
		 LEFT JOIN users u ON u.id = c.user_id
		 WHERE c.id=$1 AND c.deleted_at IS NULL`, id)
	return pgScanNodeComment(row)
}

// ListNodeComments returns the live comments of one node in chronological
// order (oldest first), each carrying the author's display name (falling
// back to the author's email).
func (s *Store) ListNodeComments(ctx context.Context, nodeID int64) ([]*model.NodeComment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.id, c.node_id, c.user_id, c.body, c.created_at, c.updated_at,
		        COALESCE(NULLIF(u.display_name, ''), u.email)
		 FROM node_comments c
		 LEFT JOIN users u ON u.id = c.user_id
		 WHERE c.node_id=$1 AND c.deleted_at IS NULL
		 ORDER BY c.created_at ASC, c.id ASC`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list node comments: %w", err)
	}
	defer rows.Close()
	var out []*model.NodeComment
	for rows.Next() {
		c, err := pgScanNodeComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SoftDeleteNodeComment flips deleted_at on a live comment.
func (s *Store) SoftDeleteNodeComment(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE node_comments SET deleted_at=NOW(), updated_at=NOW()
		 WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("postgres: soft delete node comment: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteNodeCommentsByNode hard-deletes every comment row of a node —
// the trash purge hook (belt and suspenders next to the nodes FK
// CASCADE).
func (s *Store) DeleteNodeCommentsByNode(ctx context.Context, nodeID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM node_comments WHERE node_id=$1`, nodeID)
	if err != nil {
		return fmt.Errorf("postgres: delete node comments by node: %w", err)
	}
	return nil
}

// pgScanNodeComment accepts both *sql.Row and *sql.Rows (7 columns:
// comment row + joined author name).
func pgScanNodeComment(rs interface {
	Scan(...any) error
}) (*model.NodeComment, error) {
	c := &model.NodeComment{}
	var author sql.NullString
	if err := rs.Scan(&c.ID, &c.NodeID, &c.UserID, &c.Body, &c.CreatedAt, &c.UpdatedAt, &author); err != nil {
		return nil, err
	}
	if author.Valid {
		c.AuthorName = author.String
	}
	return c, nil
}

// ─────────────────── Storage plugins (migration 00029) ───────────────────

const pluginCols = `id, name, kind, binary_path, sha256, address, token_sealed, enabled, version, driver, last_error, created_at, updated_at`

func scanPlugin(r rowScanner) (*model.Plugin, error) {
	p := &model.Plugin{}
	if err := r.Scan(&p.ID, &p.Name, &p.Kind, &p.Binary, &p.SHA256, &p.Address, &p.TokenSealed,
		&p.Enabled, &p.Version, &p.Driver, &p.LastError, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) CreatePlugin(ctx context.Context, p *model.Plugin) (*model.Plugin, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO plugins (name, kind, binary_path, sha256, address, token_sealed, enabled, version, driver, last_error)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		p.Name, p.Kind, p.Binary, p.SHA256, p.Address, p.TokenSealed, p.Enabled, p.Version, p.Driver, p.LastError).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetPlugin(ctx, id)
}

func (s *Store) GetPlugin(ctx context.Context, id int64) (*model.Plugin, error) {
	return scanPlugin(s.db.QueryRowContext(ctx, `SELECT `+pluginCols+` FROM plugins WHERE id=$1`, id))
}

func (s *Store) GetPluginByName(ctx context.Context, name string) (*model.Plugin, error) {
	return scanPlugin(s.db.QueryRowContext(ctx, `SELECT `+pluginCols+` FROM plugins WHERE name=$1`, name))
}

func (s *Store) ListPlugins(ctx context.Context) ([]*model.Plugin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pluginCols+` FROM plugins ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.Plugin{}
	for rows.Next() {
		p, err := scanPlugin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdatePlugin(ctx context.Context, p *model.Plugin) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE plugins SET kind=$1, binary_path=$2, sha256=$3, address=$4, token_sealed=$5, enabled=$6, version=$7, driver=$8, last_error=$9, updated_at=NOW()
		 WHERE id=$10`,
		p.Kind, p.Binary, p.SHA256, p.Address, p.TokenSealed, p.Enabled, p.Version, p.Driver, p.LastError, p.ID)
	return err
}

func (s *Store) DeletePlugin(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM plugins WHERE id=$1`, id)
	return err
}

// nullTime binds a never-set time as NULL rather than as the zero instant.
//
// ⚠ A zero time.Time renders as year 0, which MySQL in its default strict
// mode rejects outright ("Incorrect datetime value: '0000-00-00'"). A service
// row seeded before its first health check has exactly that value, so on
// MySQL the seed failed and OnlyOffice, drawio and the converter were absent
// from a fresh install's settings (issue #19). NULL is also what the column
// means: "not checked yet".
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// App plugins (migration 00042) — see internal/wasmplugin.
//
// PostgreSQL spelling of drivers/sqlite/app_plugins.go: $n placeholders,
// RETURNING id, plain key (not reserved here).

const appPluginCols = `id, name, version, label_json, manifest_json, wasm_path, sha256, source, source_url, signed, permissions_json, enabled, last_error, created_at, updated_at`

func scanAppPlugin(r rowScanner) (*model.AppPlugin, error) {
	p := &model.AppPlugin{}
	if err := r.Scan(&p.ID, &p.Name, &p.Version, &p.LabelJSON, &p.ManifestJSON, &p.WasmPath, &p.SHA256,
		&p.Source, &p.SourceURL, &p.Signed, &p.PermissionsJSON, &p.Enabled, &p.LastError, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) CreateAppPlugin(ctx context.Context, p *model.AppPlugin) (*model.AppPlugin, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO app_plugins (name, version, label_json, manifest_json, wasm_path, sha256, source, source_url, signed, permissions_json, enabled, last_error)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		p.Name, p.Version, orJSON(p.LabelJSON, "{}"), orJSON(p.ManifestJSON, "{}"), p.WasmPath, p.SHA256,
		p.Source, p.SourceURL, p.Signed, orJSON(p.PermissionsJSON, "[]"), p.Enabled, p.LastError).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetAppPlugin(ctx, id)
}

func (s *Store) GetAppPlugin(ctx context.Context, id int64) (*model.AppPlugin, error) {
	return scanAppPlugin(s.db.QueryRowContext(ctx, `SELECT `+appPluginCols+` FROM app_plugins WHERE id=$1`, id))
}

func (s *Store) GetAppPluginByName(ctx context.Context, name string) (*model.AppPlugin, error) {
	return scanAppPlugin(s.db.QueryRowContext(ctx, `SELECT `+appPluginCols+` FROM app_plugins WHERE name=$1`, name))
}

func (s *Store) ListAppPlugins(ctx context.Context) ([]*model.AppPlugin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+appPluginCols+` FROM app_plugins ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.AppPlugin{}
	for rows.Next() {
		p, err := scanAppPlugin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdateAppPlugin(ctx context.Context, p *model.AppPlugin) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE app_plugins SET version=$1, label_json=$2, manifest_json=$3, wasm_path=$4, sha256=$5, source=$6, source_url=$7, signed=$8, permissions_json=$9, enabled=$10, last_error=$11, updated_at=CURRENT_TIMESTAMP
		 WHERE id=$12`,
		p.Version, orJSON(p.LabelJSON, "{}"), orJSON(p.ManifestJSON, "{}"), p.WasmPath, p.SHA256, p.Source, p.SourceURL,
		p.Signed, orJSON(p.PermissionsJSON, "[]"), p.Enabled, p.LastError, p.ID)
	return err
}

func (s *Store) DeleteAppPlugin(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM app_plugin_schedule WHERE plugin_id=$1`,
		`DELETE FROM app_plugin_jobs WHERE plugin_id=$1`,
		`DELETE FROM app_plugin_state WHERE plugin_id=$1`,
		`DELETE FROM app_plugin_locks WHERE plugin_id=$1`,
		`DELETE FROM app_plugin_overrides WHERE plugin_id=$1`,
		`DELETE FROM app_plugin_settings WHERE plugin_id=$1`,
		`DELETE FROM app_plugins WHERE id=$1`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetAppPluginSettings(ctx context.Context, pluginID int64) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM app_plugin_settings WHERE plugin_id=$1", pluginID)
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

func (s *Store) PutAppPluginSettings(ctx context.Context, pluginID int64, values map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_plugin_settings WHERE plugin_id=$1`, pluginID); err != nil {
		return err
	}
	for k, v := range values {
		if _, err := tx.ExecContext(ctx, "INSERT INTO app_plugin_settings (plugin_id, key, value) VALUES ($1,$2,$3)", pluginID, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListAppPluginOverrides(ctx context.Context, pluginID int64) ([]*model.AppPluginOverride, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT plugin_id, action_id, enabled, admin_only, applies_json FROM app_plugin_overrides WHERE plugin_id=$1 ORDER BY action_id`, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.AppPluginOverride{}
	for rows.Next() {
		o := &model.AppPluginOverride{}
		if err := rows.Scan(&o.PluginID, &o.ActionID, &o.Enabled, &o.AdminOnly, &o.AppliesJSON); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) PutAppPluginOverrides(ctx context.Context, pluginID int64, list []*model.AppPluginOverride) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_plugin_overrides WHERE plugin_id=$1`, pluginID); err != nil {
		return err
	}
	for _, o := range list {
		if o == nil || strings.TrimSpace(o.ActionID) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO app_plugin_overrides (plugin_id, action_id, enabled, admin_only, applies_json) VALUES ($1,$2,$3,$4,$5)`,
			pluginID, o.ActionID, o.Enabled, o.AdminOnly, o.AppliesJSON); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetAppPluginState(ctx context.Context, pluginID, storageID int64, pathHash, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx,
		"SELECT value FROM app_plugin_state WHERE plugin_id=$1 AND storage_id=$2 AND path_hash=$3 AND key=$4",
		pluginID, storageID, pathHash, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (s *Store) SetAppPluginState(ctx context.Context, pluginID, storageID int64, pathHash, rel, key, value string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM app_plugin_state WHERE plugin_id=$1 AND storage_id=$2 AND path_hash=$3 AND key=$4",
		pluginID, storageID, pathHash, key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO app_plugin_state (plugin_id, storage_id, path_hash, rel, key, value) VALUES ($1,$2,$3,$4,$5,$6)",
		pluginID, storageID, pathHash, rel, key, value); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteAppPluginState(ctx context.Context, pluginID, storageID int64, pathHash, key string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM app_plugin_state WHERE plugin_id=$1 AND storage_id=$2 AND path_hash=$3 AND key=$4",
		pluginID, storageID, pathHash, key)
	return err
}

const appPluginJobCols = `id, op_id, plugin_id, plugin_name, action_id, storage_id, paths_json, params_json, actor_id, locale, label, status, message, outputs_json, error, created_at, finished_at`

func scanAppPluginJob(r rowScanner) (*model.AppPluginJob, error) {
	j := &model.AppPluginJob{}
	if err := r.Scan(&j.ID, &j.OpID, &j.PluginID, &j.PluginName, &j.ActionID, &j.StorageID, &j.PathsJSON, &j.ParamsJSON,
		&j.ActorID, &j.Locale, &j.Label, &j.Status, &j.Message, &j.OutputsJSON, &j.Error, &j.CreatedAt, &j.FinishedAt); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Store) CreateAppPluginJob(ctx context.Context, j *model.AppPluginJob) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO app_plugin_jobs (id, op_id, plugin_id, plugin_name, action_id, storage_id, paths_json, params_json, actor_id, locale, label, status, message, outputs_json, error)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		j.ID, j.OpID, j.PluginID, j.PluginName, j.ActionID, j.StorageID, orJSON(j.PathsJSON, "[]"), orJSON(j.ParamsJSON, "{}"),
		j.ActorID, j.Locale, j.Label, j.Status, j.Message, orJSON(j.OutputsJSON, "[]"), j.Error)
	return err
}

func (s *Store) GetAppPluginJob(ctx context.Context, id string) (*model.AppPluginJob, error) {
	return scanAppPluginJob(s.db.QueryRowContext(ctx, `SELECT `+appPluginJobCols+` FROM app_plugin_jobs WHERE id=$1`, id))
}

func (s *Store) ListAppPluginJobsByOp(ctx context.Context, opIDs []int64) (map[int64]*model.AppPluginJob, error) {
	out := map[int64]*model.AppPluginJob{}
	if len(opIDs) == 0 {
		return out, nil
	}
	ph := make([]string, len(opIDs))
	args := make([]any, len(opIDs))
	for i, id := range opIDs {
		ph[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+appPluginJobCols+` FROM app_plugin_jobs WHERE op_id IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		j, err := scanAppPluginJob(rows)
		if err != nil {
			return nil, err
		}
		if j.OpID != nil {
			out[*j.OpID] = j
		}
	}
	return out, rows.Err()
}

func (s *Store) SetAppPluginJobOp(ctx context.Context, jobID string, opID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE app_plugin_jobs SET op_id=$1 WHERE id=$2`, opID, jobID)
	return err
}

func (s *Store) UpdateAppPluginJob(ctx context.Context, j *model.AppPluginJob) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE app_plugin_jobs SET op_id=$1, status=$2, message=$3, outputs_json=$4, error=$5, finished_at=$6 WHERE id=$7`,
		j.OpID, j.Status, j.Message, orJSON(j.OutputsJSON, "[]"), j.Error, j.FinishedAt, j.ID)
	return err
}

// orJSON substitutes a default for an empty JSON column so a NOT NULL text
// column never receives "" where a document is expected.
func orJSON(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

const appPluginSigningKeyCols = `id, tenant_id, plugin_id, purpose, subject, cert_pem, key_sealed, created_at, expires_at, destroyed_at, retired_at`

func scanAppPluginSigningKey(r rowScanner) (*model.AppPluginSigningKey, error) {
	k := &model.AppPluginSigningKey{}
	if err := r.Scan(&k.ID, &k.TenantID, &k.PluginID, &k.Purpose, &k.Subject, &k.CertPEM, &k.KeySealed, &k.CreatedAt, &k.ExpiresAt, &k.DestroyedAt, &k.RetiredAt); err != nil {
		return nil, err
	}
	return k, nil
}

func (s *Store) CreateAppPluginSigningKey(ctx context.Context, k *model.AppPluginSigningKey) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO app_plugin_signing_keys (id, tenant_id, plugin_id, purpose, subject, cert_pem, key_sealed, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		k.ID, k.TenantID, k.PluginID, k.Purpose, k.Subject, k.CertPEM, k.KeySealed, k.ExpiresAt)
	return err
}

func (s *Store) GetAppPluginSigningKey(ctx context.Context, id string) (*model.AppPluginSigningKey, error) {
	return scanAppPluginSigningKey(s.db.QueryRowContext(ctx, `SELECT `+appPluginSigningKeyCols+` FROM app_plugin_signing_keys WHERE id=?`, id))
}

func (s *Store) GetAppPluginSigningCA(ctx context.Context, tenantID int64) (*model.AppPluginSigningKey, error) {
	k, err := scanAppPluginSigningKey(s.db.QueryRowContext(ctx,
		`SELECT `+appPluginSigningKeyCols+` FROM app_plugin_signing_keys WHERE tenant_id=? AND purpose='ca' AND retired_at IS NULL AND destroyed_at IS NULL ORDER BY created_at DESC LIMIT 1`, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return k, err
}

func (s *Store) RetireAppPluginSigningCA(ctx context.Context, tenantID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE app_plugin_signing_keys SET retired_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND purpose='ca' AND retired_at IS NULL`, tenantID)
	return err
}

func (s *Store) GetAppPluginSealKey(ctx context.Context, tenantID, pluginID int64) (*model.AppPluginSigningKey, error) {
	k, err := scanAppPluginSigningKey(s.db.QueryRowContext(ctx,
		`SELECT `+appPluginSigningKeyCols+` FROM app_plugin_signing_keys WHERE tenant_id=$1 AND plugin_id=$2 AND purpose='platform' AND destroyed_at IS NULL ORDER BY created_at DESC LIMIT 1`, tenantID, pluginID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return k, err
}

func (s *Store) ListAppPluginSigningCAs(ctx context.Context, tenantID int64) ([]*model.AppPluginSigningKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+appPluginSigningKeyCols+` FROM app_plugin_signing_keys WHERE tenant_id=$1 AND purpose='ca' ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.AppPluginSigningKey{}
	for rows.Next() {
		k, err := scanAppPluginSigningKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) DestroyAppPluginSigningKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE app_plugin_signing_keys SET key_sealed='', destroyed_at=CURRENT_TIMESTAMP WHERE id=$1 AND destroyed_at IS NULL`, id)
	return err
}

// ── app_plugin_locks / state keys (migration 00045) ────────────────────

func (s *Store) ListAppPluginStateFiles(ctx context.Context, pluginID int64, key string, limit int) ([]*model.AppPluginStateFile, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	// LEFT JOIN, not JOIN: the file may not be indexed yet, and a document
	// recorded seconds after upload still belongs in the list. A node row
	// that IS there and is deleted takes the row out.
	q := `SELECT st.storage_id, s.name, st.rel, st.key, st.value
	        FROM app_plugin_state st
	        JOIN storages s ON s.id = st.storage_id
	        LEFT JOIN nodes n ON n.storage_id = st.storage_id AND n.path_hash = st.path_hash
	       WHERE st.plugin_id = $1 AND st.rel <> '' AND (n.id IS NULL OR n.deleted_at IS NULL)`
	args := []any{pluginID}
	if key != "" {
		q += ` AND st.key = $2`
		args = append(args, key)
	}
	q += fmt.Sprintf(` ORDER BY st.rel LIMIT $%d`, len(args)+1)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.AppPluginStateFile{}
	for rows.Next() {
		f := &model.AppPluginStateFile{}
		if err := rows.Scan(&f.StorageID, &f.StorageName, &f.Path, &f.Key, &f.Value); err != nil {
			return nil, err
		}
		f.Name = path.Base(f.Path)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) ListAppPluginStateKeys(ctx context.Context, storageID int64, pathHashes []string) (map[string][]string, error) {
	out := map[string][]string{}
	const chunk = 400
	for start := 0; start < len(pathHashes); start += chunk {
		end := start + chunk
		if end > len(pathHashes) {
			end = len(pathHashes)
		}
		part := pathHashes[start:end]
		args := make([]any, 0, len(part)+1)
		args = append(args, storageID)
		ph := make([]string, len(part))
		for i, h := range part {
			ph[i] = fmt.Sprintf("$%d", i+2)
			args = append(args, h)
		}
		rows, err := s.db.QueryContext(ctx,
			"SELECT st.path_hash, p.name, st.key FROM app_plugin_state st JOIN app_plugins p ON p.id=st.plugin_id WHERE st.storage_id=$1 AND st.path_hash IN ("+strings.Join(ph, ",")+")",
			args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var h, name, key string
			if err := rows.Scan(&h, &name, &key); err != nil {
				rows.Close()
				return nil, err
			}
			if len(out[h]) < 32 {
				out[h] = append(out[h], name+":"+key)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

const appPluginLockCols = `storage_id, path_hash, rel, plugin_id, plugin_name, reason, until, created_by, created_at`

func scanAppPluginLock(r rowScanner) (*model.AppPluginLock, error) {
	l := &model.AppPluginLock{}
	if err := r.Scan(&l.StorageID, &l.PathHash, &l.Rel, &l.PluginID, &l.PluginName, &l.Reason, &l.Until, &l.CreatedBy, &l.CreatedAt); err != nil {
		return nil, err
	}
	return l, nil
}

func (s *Store) PutAppPluginLock(ctx context.Context, l *model.AppPluginLock) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_plugin_locks WHERE storage_id=$1 AND path_hash=$2`, l.StorageID, l.PathHash); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO app_plugin_locks (storage_id, path_hash, rel, plugin_id, plugin_name, reason, until, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		l.StorageID, l.PathHash, l.Rel, l.PluginID, l.PluginName, l.Reason, l.Until, l.CreatedBy); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetAppPluginLock(ctx context.Context, storageID int64, pathHash string) (*model.AppPluginLock, error) {
	l, err := scanAppPluginLock(s.db.QueryRowContext(ctx, `SELECT `+appPluginLockCols+` FROM app_plugin_locks WHERE storage_id=$1 AND path_hash=$2`, storageID, pathHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

func (s *Store) ListAppPluginLocks(ctx context.Context, storageID int64) ([]*model.AppPluginLock, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if storageID > 0 {
		rows, err = s.db.QueryContext(ctx, `SELECT `+appPluginLockCols+` FROM app_plugin_locks WHERE storage_id=$1 ORDER BY created_at`, storageID)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT `+appPluginLockCols+` FROM app_plugin_locks ORDER BY created_at`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.AppPluginLock{}
	for rows.Next() {
		l, err := scanAppPluginLock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAppPluginLock(ctx context.Context, storageID int64, pathHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_plugin_locks WHERE storage_id=$1 AND path_hash=$2`, storageID, pathHash)
	return err
}

func (s *Store) DeleteExpiredAppPluginLocks(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM app_plugin_locks WHERE until IS NOT NULL AND until < $1`, now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ── App-plugin schedule (00050) ────────────────────────────────────────
//
// See sqlite/00050 and the SQLite implementation for what each statement is
// for; this is the same logic in PostgreSQL's placeholder syntax.

const appPluginScheduleCols = `plugin_id, key, due_at, action_id, storage_id, paths_json, params_json, status, attempts, claimed_by, claimed_at, job_id, error, created_at, updated_at`

func scanAppPluginScheduleItem(r rowScanner) (*model.AppPluginScheduleItem, error) {
	it := &model.AppPluginScheduleItem{}
	if err := r.Scan(&it.PluginID, &it.Key, &it.DueAt, &it.ActionID, &it.StorageID, &it.PathsJSON, &it.ParamsJSON,
		&it.Status, &it.Attempts, &it.ClaimedBy, &it.ClaimedAt, &it.JobID, &it.Error, &it.CreatedAt, &it.UpdatedAt); err != nil {
		return nil, err
	}
	return it, nil
}

// PutAppPluginScheduleItem writes the item, leaving a RUNNING row alone: the
// process holding that lease owns it and will finish it.
func (s *Store) PutAppPluginScheduleItem(ctx context.Context, it *model.AppPluginScheduleItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM app_plugin_schedule WHERE plugin_id=$1 AND key=$2 FOR UPDATE`, it.PluginID, it.Key).Scan(&status)
	switch {
	case err == nil && status == model.AppPluginScheduleRunning:
		return nil
	case err == nil:
		if _, derr := tx.ExecContext(ctx, `DELETE FROM app_plugin_schedule WHERE plugin_id=$1 AND key=$2`, it.PluginID, it.Key); derr != nil {
			return derr
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return err
	}
	status = it.Status
	if status == "" {
		status = model.AppPluginScheduleDue
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO app_plugin_schedule (plugin_id, key, due_at, action_id, storage_id, paths_json, params_json, status, attempts, claimed_by, job_id, error, updated_at)`+
			` VALUES ($1,$2,$3,$4,$5,$6,$7,$8,0,'','','',NOW())`,
		it.PluginID, it.Key, it.DueAt.UTC(), it.ActionID, it.StorageID, orJSONPG(it.PathsJSON, "[]"), orJSONPG(it.ParamsJSON, "{}"), status); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ClaimAppPluginScheduleItem(ctx context.Context, pluginID int64, key, owner string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE app_plugin_schedule SET status=$1, claimed_by=$2, claimed_at=$3, attempts=attempts+1, updated_at=NOW()`+
			` WHERE plugin_id=$4 AND key=$5 AND status=$6 AND due_at<=$7`,
		model.AppPluginScheduleRunning, owner, now.UTC(), pluginID, key, model.AppPluginScheduleDue, now.UTC())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) FinishAppPluginScheduleItem(ctx context.Context, pluginID int64, key, status, jobID, errMsg string, rearmAt *time.Time) error {
	if rearmAt != nil {
		_, err := s.db.ExecContext(ctx,
			`UPDATE app_plugin_schedule SET status=$1, due_at=$2, claimed_by='', claimed_at=NULL, job_id=$3, error=$4, updated_at=NOW()`+
				` WHERE plugin_id=$5 AND key=$6`,
			model.AppPluginScheduleDue, rearmAt.UTC(), jobID, errMsg, pluginID, key)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE app_plugin_schedule SET status=$1, job_id=$2, error=$3, updated_at=NOW() WHERE plugin_id=$4 AND key=$5`,
		status, jobID, errMsg, pluginID, key)
	return err
}

func (s *Store) DueAppPluginScheduleItems(ctx context.Context, now time.Time, limit int) ([]*model.AppPluginScheduleItem, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+appPluginScheduleCols+` FROM app_plugin_schedule WHERE status=$1 AND due_at<=$2 ORDER BY due_at ASC, plugin_id ASC, key ASC LIMIT $3`,
		model.AppPluginScheduleDue, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAppPluginScheduleItems(rows)
}

func (s *Store) NextAppPluginScheduleDue(ctx context.Context) (*time.Time, error) {
	var t time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT due_at FROM app_plugin_schedule WHERE status=$1 ORDER BY due_at ASC LIMIT 1`, model.AppPluginScheduleDue).Scan(&t)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) ListAppPluginScheduleItems(ctx context.Context, pluginID int64) ([]*model.AppPluginScheduleItem, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+appPluginScheduleCols+` FROM app_plugin_schedule WHERE plugin_id=$1 ORDER BY due_at ASC, key ASC`, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAppPluginScheduleItems(rows)
}

func (s *Store) ReapAppPluginScheduleItems(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM app_plugin_schedule WHERE status IN ($1,$2,$3) AND due_at < $4`,
		model.AppPluginScheduleQueued, model.AppPluginScheduleFailed, model.AppPluginScheduleSkipped, before.UTC())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func collectAppPluginScheduleItems(rows *sql.Rows) ([]*model.AppPluginScheduleItem, error) {
	out := []*model.AppPluginScheduleItem{}
	for rows.Next() {
		it, err := scanAppPluginScheduleItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// orJSONPG substitutes a default for an empty JSON column so a NOT NULL text
// column never receives "" where a document is expected.
func orJSONPG(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
