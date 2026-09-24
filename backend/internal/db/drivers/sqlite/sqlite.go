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
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/namefold"
	"github.com/brf-tech/filex/backend/internal/pathkey"

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

// NewMySQLStore returns the same Store in MySQL/MariaDB mode. The MySQL driver
// reuses this implementation — the placeholder syntax is identical and the
// column names are the same — and this flag covers the one construct where the
// two dialects genuinely disagree. See upsert.
func NewMySQLStore(sqlDB *sql.DB) db.Store {
	return &Store{db: sqlDB, mysql: true}
}

// Store implements db.Store atop SQLite — and, through the MySQL driver, atop
// MySQL/MariaDB as well.
type Store struct {
	db *sql.DB
	// mysql switches the handful of statements that cannot be written once for
	// both engines. Everything else in this file is deliberately portable.
	mysql bool
}

// upsertClause matches SQLite's upsert tail so it can be swapped for MySQL's.
var upsertClause = regexp.MustCompile(`(?is)\s*ON CONFLICT\s*\([^)]*\)\s*DO UPDATE SET\s+`)

// excludedRef matches a reference to the row that was being inserted.
var excludedRef = regexp.MustCompile(`excluded\.([A-Za-z_][A-Za-z0-9_]*)`)

// upsert translates SQLite's `ON CONFLICT (...) DO UPDATE SET x = excluded.x`
// into MySQL's `ON DUPLICATE KEY UPDATE x = VALUES(x)`, and is a no-op on
// SQLite.
//
// ⚠ Every upsert in this file must go through it. MySQL does not understand
// ON CONFLICT at all, so a statement that skips this returns a syntax error
// the moment an operator saves a setting, configures OnlyOffice or stores a
// thumbnail — the store compiles and every SQLite test stays green.
// backend/internal/db's cross-engine write-path test is what catches it.
//
// VALUES(col) rather than MySQL 8.0.19's row alias: the alias form is a syntax
// error on MariaDB, and VALUES() is understood by both (deprecated in MySQL
// 8.0.20, still supported).
func (s *Store) upsert(q string) string {
	if !s.mysql {
		return q
	}
	return UpsertForMySQL(q)
}

// UpsertForMySQL is the rewrite upsert applies in MySQL mode, exported so the
// cross-engine gate in backend/internal/db can prepare every statement in this
// file exactly as a MySQL server receives it.
func UpsertForMySQL(q string) string {
	loc := upsertClause.FindStringIndex(q)
	if loc == nil {
		return q
	}
	return q[:loc[0]] + "\n ON DUPLICATE KEY UPDATE " + excludedRef.ReplaceAllString(q[loc[1]:], "VALUES($1)")
}

// Ping implements db.Store.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close implements db.Store.
func (s *Store) Close() error { return s.db.Close() }

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
	// Assigned here rather than by the caller: a storage without one would be
	// addressable only by a name that can change under its clients.
	if st.UID == "" {
		st.UID = model.NewStorageUID()
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO storages (name, driver, mount_path, config_json, sync_mode, sync_interval_s, enabled, read_only, rbac_enabled, uid)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		st.Name, st.Driver, st.MountPath, string(cfg), st.SyncMode, st.SyncIntervalS, btoi(st.Enabled), btoi(st.ReadOnly), btoi(st.RBACEnabled), st.UID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetStorage(ctx, id)
}

// storageCols is the one place the storage projection is spelled out. It was
// repeated in four queries, and a column added to three of them would have
// been scanned by scanStorage from a row that did not have it.
//
// ⚠ COALESCE on uid: the column is nullable so its UNIQUE index tolerates a
// row that somehow arrives unfilled, and an empty string is what the rest of
// the code treats as "not addressable by uid yet".
const storageCols = `id, name, driver, mount_path, config_json, sync_mode, sync_interval_s, last_sync_at, COALESCE(last_sync_token,''), enabled, read_only, created_at, COALESCE(role,'primary'), replica_of_id, COALESCE(replica_mode,'async'), replica_target_id, rbac_enabled, COALESCE(uid,'')`

func (s *Store) GetStorage(ctx context.Context, id int64) (*model.Storage, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+storageCols+` FROM storages WHERE id=?`, id)
	return scanStorage(row)
}

func (s *Store) GetStorageByName(ctx context.Context, name string) (*model.Storage, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+storageCols+` FROM storages WHERE name=?`, name)
	return scanStorage(row)
}

// GetStorageByUID resolves the address that does not move. ⚠ An empty uid
// must never match: on an install migrated from before the column existed a
// row could carry one, and "" would then resolve to whichever came first.
func (s *Store) GetStorageByUID(ctx context.Context, uid string) (*model.Storage, error) {
	if uid == "" {
		return nil, sql.ErrNoRows
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+storageCols+` FROM storages WHERE uid=?`, uid)
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
	rows, err := s.db.QueryContext(ctx, `SELECT `+storageCols+` FROM storages WHERE enabled=1 ORDER BY id`)
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
		_ = s.db.QueryRowContext(ctx, `SELECT sync_mode FROM storages WHERE id=?`, st.ID).Scan(&prev)
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
	// transfer_state is written explicitly rather than left to the column
	// default: a staged upload inserts its node BEFORE the bytes reach the
	// driver, and a node that claims "stored" for even a moment is a node a
	// read path would try to fetch from a backend that has nothing.
	transferState := n.TransferState
	if transferState == "" {
		transferState = model.TransferStateStored
	}
	// Attribution is written by the INSERT, not by a follow-up UPDATE: the
	// decorator that resolves the acting identity (internal/quotastore) stamps
	// the model before it gets here, so a bulk write — an archive extract, a
	// desktop sync, a scanner walk — costs the same number of round trips it
	// did before ownership existed. nil owner/actor is SYSTEM and is written
	// as NULL on purpose (see migration 00038).
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO nodes (storage_id, parent_id, name, path, path_hash, storage_key, type, size, mime, etag, backend_mtime, sync_state, transfer_state, owner_id, last_actor_id, external_upload)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.StorageID, n.ParentID, n.Name, n.Path, n.PathHash, n.StorageKey, n.Type, n.Size, n.Mime, n.Etag, n.BackendMtime, n.SyncState, transferState, n.OwnerID, n.LastActorID, n.ExternalUpload)
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

// GetNodeByPathIncludingDeleted returns the row at a path, live or trashed.
//
// ⚠ ORDER BY is load-bearing since migration 00032 made the unique index over
// (storage_id, path_hash) live-only: several trashed rows may now share a path
// with at most one live one. `deleted_at IS NULL` sorts the live row first
// (0 before 1), and id DESC picks the most recent among the trashed. Without
// it the sync worker's view of a path would depend on row order.
func (s *Store) GetNodeByPathIncludingDeleted(ctx context.Context, storageID int64, hash string) (*model.Node, error) {
	row := s.db.QueryRowContext(ctx, nodeSelectColumns()+
		` FROM nodes WHERE storage_id=? AND path_hash=?`+
		` ORDER BY CASE WHEN deleted_at IS NULL THEN 0 ELSE 1 END, id DESC LIMIT 1`,
		storageID, hash)
	return scanNode(row)
}

// ListLiveNodesInTrash returns live rows sitting in the trash bucket. See the
// db.Store declaration for why any such row is a defect to be cleaned up.
func (s *Store) ListLiveNodesInTrash(ctx context.Context, storageID int64, trashPrefix string) ([]*model.Node, error) {
	bare := strings.Trim(trashPrefix, "/")
	if bare == "" {
		return nil, nil
	}
	slashed := "/" + bare
	rows, err := s.db.QueryContext(ctx, nodeSelectColumns()+
		` FROM nodes WHERE storage_id=? AND deleted_at IS NULL`+
		` AND (path=? OR path=? OR path LIKE ? OR path LIKE ?) ORDER BY id`,
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
// spellings, exactly. Its four arguments come from belowArgs.
//
// ⚠ SUBSTR counts CHARACTERS on SQLite, MySQL and PostgreSQL alike, so the
// bound is the prefix's rune count, never len(): a byte count overshoots a
// prefix with a single non-ASCII letter in it and the comparison then matches
// nothing at all. LIKE is not used on purpose: it is case-insensitive on
// SQLite and treats `_` and `%` in a folder name as wildcards.
const belowClause = `(SUBSTR(path,1,?)=? OR SUBSTR(path,1,?)=?)`

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
	q := nodeSelectColumns() + ` FROM nodes WHERE storage_id=? AND (path=? OR path=? OR ` + belowClause + `)`
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
		`SELECT id, parent_id, type, size, backend_mtime FROM nodes WHERE storage_id=? AND deleted_at IS NULL`,
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
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET size=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, size, id)
	return err
}

func (s *Store) SetNodeMtime(ctx context.Context, id int64, mtime *time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET backend_mtime=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, mtime, id)
	return err
}

// ─── Providers (tenants) — see docs/MULTI-TENANCY.md ─────────────────
// SQL here is kept portable (no ON CONFLICT / INSERT OR IGNORE) because the
// MySQL driver reuses this Store verbatim.

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
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO providers (slug, name, host, auth_type, oidc_issuer, oidc_client_id, oidc_client_secret, oidc_redirect_url, role_claim, admin_group, cookie_domain, is_supertenant, enabled)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Slug, p.Name, p.Host, at, p.OIDCIssuer, p.OIDCClientID, p.OIDCClientSecret,
		p.OIDCRedirectURL, p.RoleClaim, p.AdminGroup, p.CookieDomain, btoi(p.IsSupertenant), btoi(p.Enabled))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetProvider(ctx, id)
}

func (s *Store) GetProvider(ctx context.Context, id int64) (*model.Provider, error) {
	return scanProvider(s.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE id=?`, id))
}

func (s *Store) GetProviderBySlug(ctx context.Context, slug string) (*model.Provider, error) {
	p, err := scanProvider(s.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE slug=?`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (s *Store) GetProviderByHost(ctx context.Context, host string) (*model.Provider, error) {
	p, err := scanProvider(s.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE host=? AND enabled=1`, host))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (s *Store) GetSupertenant(ctx context.Context) (*model.Provider, error) {
	p, err := scanProvider(s.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE is_supertenant=1 ORDER BY id LIMIT 1`))
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
		`UPDATE providers SET slug=?, name=?, host=?, auth_type=?, oidc_issuer=?, oidc_client_id=?, oidc_client_secret=?, oidc_redirect_url=?, role_claim=?, admin_group=?, cookie_domain=?, is_supertenant=?, enabled=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		p.Slug, p.Name, p.Host, p.AuthType, p.OIDCIssuer, p.OIDCClientID, p.OIDCClientSecret,
		p.OIDCRedirectURL, p.RoleClaim, p.AdminGroup, p.CookieDomain, btoi(p.IsSupertenant), btoi(p.Enabled), p.ID)
	return err
}

func (s *Store) DeleteProvider(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM providers WHERE id=?`, id)
	return err
}

func (s *Store) LinkProviderStorage(ctx context.Context, providerID, storageID int64) error {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM provider_storages WHERE provider_id=? AND storage_id=?`,
		providerID, storageID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO provider_storages (provider_id, storage_id) VALUES (?,?)`, providerID, storageID)
	return err
}

func (s *Store) UnlinkProviderStorage(ctx context.Context, providerID, storageID int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM provider_storages WHERE provider_id=? AND storage_id=?`, providerID, storageID)
	return err
}

func (s *Store) ListProviderStorageIDs(ctx context.Context, providerID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT storage_id FROM provider_storages WHERE provider_id=? ORDER BY storage_id`, providerID)
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
		`SELECT provider_id FROM provider_storages WHERE storage_id=? ORDER BY provider_id LIMIT 1`,
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
		`UPDATE providers SET plan=?, limits_json=?, billing_ref=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		nullIfEmpty(plan), nullIfEmpty(limitsJSON), nullIfEmpty(billingRef), providerID)
	return err
}

func (s *Store) GetProviderPlan(ctx context.Context, providerID int64) (string, string, string, error) {
	var plan, limitsJSON, billingRef string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(plan,''), COALESCE(limits_json,''), COALESCE(billing_ref,'') FROM providers WHERE id=?`,
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
//
// Directories additionally drag their whole cached subtree along (GitHub
// issue #5): every live descendant row is rewritten to the matching
// trash-prefixed path (path_hash recomputed, original path preserved in
// storage_key) and soft-deleted. parent_id + name stay untouched on the
// children so RestoreNodeAt can mirror the rewrite back. Without this the
// children kept their ORIGINAL paths while soft-deleted, wedging the
// unique indexes for every future sync create at the same location.
func (s *Store) SoftDeleteAndRetag(ctx context.Context, id int64, trashPath, trashHash, origPath string) error {
	base := path.Base(trashPath)
	var nodeType string
	var curPath string
	var storageID int64
	scanErr := s.db.QueryRowContext(ctx,
		`SELECT storage_id, type, path FROM nodes WHERE id=?`, id).
		Scan(&storageID, &nodeType, &curPath)
	_, err := s.db.ExecContext(ctx, `
		UPDATE nodes
		SET deleted_at=CURRENT_TIMESTAMP,
		    updated_at=CURRENT_TIMESTAMP,
		    parent_id=NULL,
		    name=?, path=?, path_hash=?, storage_key=?
		WHERE id=?`, base, trashPath, trashHash, origPath, id)
	if err != nil || scanErr != nil {
		return err
	}
	if nodeType == string(model.NodeTypeDirectory) {
		s.retagTrashedSubtree(ctx, storageID, []string{origPath, curPath}, trashPath)
	}
	return nil
}

// retagTrashedSubtree soft-deletes every live descendant under any of the
// supplied original folder paths, rewriting each row's path to live under
// trashPath (storage_key keeps the original path so per-row restore still
// works). Best-effort: a failing child row is skipped — the sync worker
// reconciles leftovers, and the partial unique index (migration 00018)
// keeps them from wedging future creates.
func (s *Store) retagTrashedSubtree(ctx context.Context, storageID int64, origPaths []string, trashPath string) {
	type childRow struct {
		id   int64
		path string
	}
	prefixes := subtreePrefixVariants(origPaths)
	if len(prefixes) == 0 {
		return
	}
	var children []childRow
	for _, pfx := range prefixes {
		rows, err := s.db.QueryContext(ctx, `
			SELECT id, path FROM nodes
			WHERE storage_id=? AND deleted_at IS NULL AND SUBSTR(path,1,?)=?`,
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
		suffix := subtreeSuffix(c.path, prefixes)
		if suffix == "" {
			continue
		}
		newPath := strings.TrimRight(trashPath, "/") + "/" + suffix
		newHash := pathkey.Hash(storageID, newPath)
		_, _ = s.db.ExecContext(ctx, `
			UPDATE nodes
			SET deleted_at=CURRENT_TIMESTAMP,
			    updated_at=CURRENT_TIMESTAMP,
			    path=?, path_hash=?, storage_key=?
			WHERE id=?`, newPath, newHash, c.path, c.id)
	}
}

// restoreTrashedSubtree is the mirror of retagTrashedSubtree: every
// soft-deleted descendant still parked under the folder's trash path is
// revived at the folder's restored location (path prefix swapped back,
// path_hash recomputed, storage_key synced to the new path). parent_id and
// name were never touched on children so the tree re-links itself.
func (s *Store) restoreTrashedSubtree(ctx context.Context, storageID int64, trashPaths []string, restoredPath string) {
	type childRow struct {
		id   int64
		path string
	}
	prefixes := subtreePrefixVariants(trashPaths)
	if len(prefixes) == 0 {
		return
	}
	var children []childRow
	for _, pfx := range prefixes {
		rows, err := s.db.QueryContext(ctx, `
			SELECT id, path FROM nodes
			WHERE storage_id=? AND deleted_at IS NOT NULL AND SUBSTR(path,1,?)=?`,
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
		suffix := subtreeSuffix(c.path, prefixes)
		if suffix == "" {
			continue
		}
		newPath := strings.TrimRight(restoredPath, "/") + "/" + suffix
		newHash := pathkey.Hash(storageID, newPath)
		_, _ = s.db.ExecContext(ctx, `
			UPDATE nodes
			SET deleted_at=NULL,
			    updated_at=CURRENT_TIMESTAMP,
			    path=?, path_hash=?, storage_key=?
			WHERE id=?`, newPath, newHash, newPath, c.id)
	}
}

// prefixChars is the length SUBSTR needs for a path prefix: SQL counts
// CHARACTERS (SQLite, MySQL and PostgreSQL alike), Go's len counts bytes. Passing
// len() made every folder whose path is not plain ASCII match none of its own
// rows — "/Müşteri/" is 9 characters and 11 bytes — so the folder went to the
// trash and its contents stayed live, and a restore left them in the trash.
func prefixChars(p string) int { return utf8.RuneCountInString(p) }

// subtreePrefixVariants normalizes candidate folder paths into the two
// on-disk conventions the nodes.path column historically mixes (with and
// without a leading slash), each with a trailing "/" so only strict
// descendants match.
func subtreePrefixVariants(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		norm := strings.TrimRight(path.Clean("/"+strings.Trim(p, "/")), "/")
		if norm == "" || norm == "/" {
			continue // never treat the storage root as a subtree
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

// subtreeSuffix strips the first matching prefix variant off p ("" = no match).
func subtreeSuffix(p string, prefixes []string) string {
	for _, pfx := range prefixes {
		if strings.HasPrefix(p, pfx) {
			return strings.TrimPrefix(p, pfx)
		}
	}
	return ""
}

func (s *Store) SoftDeleteNode(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

func (s *Store) HardDeleteNode(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id=?`, id)
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
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes
		    SET parent_id=?, name=?, path=?, path_hash=?,
		        storage_key=CASE WHEN deleted_at IS NULL THEN ? ELSE storage_key END,
		        updated_at=CURRENT_TIMESTAMP
		  WHERE id=?`,
		parentID, name, path, hash, path, id)
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

func (s *Store) ListStaleNodesUnder(ctx context.Context, storageID int64, dir string, before time.Time) ([]*model.Node, error) {
	slashed, bare, ok := treeSpellings(dir)
	if !ok {
		return nil, nil
	}
	// The same CURRENT_TIMESTAMP wire format ListStaleNodes explains.
	beforeStr := before.UTC().Format("2006-01-02 15:04:05")
	args := append([]any{storageID, beforeStr}, belowArgs(slashed, bare)...)
	rows, err := s.db.QueryContext(ctx, nodeSelectColumns()+
		` FROM nodes WHERE storage_id=? AND seen_at < ? AND deleted_at IS NULL AND `+belowClause, args...)
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
		`SELECT COUNT(*) FROM nodes WHERE storage_id=? AND deleted_at IS NULL AND `+belowClause, args...).Scan(&n)
	return n, err
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

// ListDuplicateNodes returns every live file node whose (size, non-empty
// etag) pair occurs more than once — the raw rows behind the admin
// duplicate report. Plain GROUP BY … HAVING in a derived table, so the
// same SQL runs on SQLite and MySQL (this driver backs both).
func (s *Store) ListDuplicateNodes(ctx context.Context, minSize int64) ([]db.DuplicateNode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT n.id, n.storage_id, n.path, n.name, n.size, COALESCE(n.etag,'')
		   FROM nodes n
		   JOIN (SELECT size, etag FROM nodes
		          WHERE deleted_at IS NULL AND type='file' AND COALESCE(etag,'')<>'' AND size>=?
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
// is `\`, so every character in it matches only itself.
var likeLiteral = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// SearchNodes — see db.Store. Every condition is on the name, which
// idx_nodes_storage_parent_name holds, so SQLite turns a row away without
// reading it from the table (a condition on the path read every candidate row
// — 1.06 s for an eleven-word query on the 169k-file catalogue of PR #46).
// In the order a row meets them:
//
//  1. The engine's own LIKE on the stored bytes — cheap, in C: m.Runs, and
//     every word made only of letters every stored spelling holds as it is
//     (namefold.AllPlain: `plan`, `2026`). For those the native LIKE IS the
//     normaliser's answer, give or take a mark composing onto the word's last
//     letter, which the scorer re-checks.
//  2. Every other word through the normaliser: fx_match on SQLite (fold.go),
//     one call a row for all of them. MySQL can compose nothing, so it gets
//     its accent- and case-insensitive collation over both the composed and
//     the decomposed form of each word, on the name with the i's that
//     collation keeps apart made one (mysqlNameLike).
//  3. m.Prefer ranks before the LIMIT. A plain word natively; any other only
//     for the rows that natively start with its plain prefix, since a Go
//     function in an ORDER BY runs for EVERY matching row — measured: 1.2 s
//     for `plan` over 170 000 rows that all held it, 0.2 s without.
//
// ⚠ The escape character is spelled out on SQLite and must NOT be on MySQL.
// SQLite has none unless the statement names one; MySQL escapes with `\` by
// default, and `'\'` is an unterminated string literal in its dialect.
// ⚠ On MySQL, nodes.name compares byte for byte since migration 00041 — two
// files that differ only by case are two files — so a search names the
// collation it wants explicitly.
func (s *Store) SearchNodes(ctx context.Context, storageID int64, m model.NameMatch, limit int) ([]*model.Node, error) {
	if limit <= 0 {
		limit = 100
	}
	// Folded once per query: the planner already folds them, and a caller
	// that did not is not wrong, only slower to be right.
	words := namefold.Words(m.Words)
	if len(words) == 0 {
		return nil, nil
	}
	native := `name LIKE ? ESCAPE '\'`
	nameLength := `length(name)` // characters, for a TEXT value
	if s.mysql {
		native = `name COLLATE utf8mb4_0900_ai_ci LIKE ?`
		nameLength = `CHAR_LENGTH(name)` // LENGTH() counts bytes there
	}
	var q strings.Builder
	q.WriteString(nodeSelectColumns() + ` FROM nodes WHERE storage_id=? AND deleted_at IS NULL`)
	args := []any{storageID}
	var folded []string
	for _, r := range m.Runs {
		if r != "" {
			q.WriteString(` AND ` + native)
			args = append(args, "%"+likeLiteral.Replace(r)+"%")
		}
	}
	for _, w := range words {
		if namefold.AllPlain(w) {
			q.WriteString(` AND ` + native)
			args = append(args, "%"+likeLiteral.Replace(w)+"%")
		} else {
			folded = append(folded, w)
		}
	}
	switch {
	case len(folded) == 0:
	case s.mysql:
		for _, w := range folded {
			var ors []string
			for _, form := range wordForms(w) {
				ors = append(ors, mysqlNameLike)
				args = append(args, "%"+likeLiteral.Replace(form)+"%")
			}
			q.WriteString(` AND (` + strings.Join(ors, ` OR `) + `)`)
		}
	default:
		q.WriteString(` AND ` + matchFunc + `(name` + strings.Repeat(`, ?`, len(folded)) + `)`)
		for _, w := range folded {
			args = append(args, w)
		}
	}
	// Rank BEFORE the LIMIT (see db.Store.SearchNodes). The patterns are
	// built here and bound, never concatenated in SQL: `||` is a logical OR on
	// MySQL.
	switch p := namefold.String(m.Prefer); {
	case p == "":
		q.WriteString(` ORDER BY name`)
	case namefold.AllPlain(p):
		lit := likeLiteral.Replace(p)
		q.WriteString(` ORDER BY CASE WHEN ` + native + ` OR ` + native + ` THEN 0 WHEN ` + native + ` THEN 1 ELSE 2 END, ` +
			nameLength + `, name`)
		args = append(args, lit, lit+".%", lit+"%")
	case s.mysql:
		var exact, prefix []string
		var exactArgs, prefixArgs []any
		for _, form := range wordForms(p) {
			lit := likeLiteral.Replace(form)
			exact = append(exact, mysqlNameLike, mysqlNameLike)
			exactArgs = append(exactArgs, lit, lit+".%")
			prefix = append(prefix, mysqlNameLike)
			prefixArgs = append(prefixArgs, lit+"%")
		}
		q.WriteString(` ORDER BY CASE WHEN ` + strings.Join(exact, ` OR `) + ` THEN 0 WHEN ` +
			strings.Join(prefix, ` OR `) + ` THEN 1 ELSE 2 END, ` + nameLength + `, name`)
		args = append(append(args, exactArgs...), prefixArgs...)
	default:
		q.WriteString(` ORDER BY CASE WHEN ` + native + ` THEN ` + rankFunc + `(name, ?) ELSE 2 END, ` + nameLength + `, name`)
		args = append(args, likeLiteral.Replace(namefold.PlainPrefix(p))+"%", p)
	}
	q.WriteString(` LIMIT ?`)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q.String(), args...)
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
	// Every user belongs to a provider (tenant). New users default to the
	// always-present "default" provider (the supertenant), so single-tenant
	// installs behave unchanged and every user can log in; OIDC JIT overrides
	// this with the host-resolved tenant via SetUserProvider. This keeps the
	// CreateUser signature stable for its many callers.
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (email, password_hash, role, locale, timezone, provider_id)
		 VALUES (?,?,?,?,?, (SELECT id FROM providers WHERE slug='default'))`,
		email, passwordHash, role, locale, tz)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetUser(ctx, id)
}

// SetUserProvider re-homes a user to a provider (tenant) and records its OIDC
// subject. Used by OIDC JIT to stamp the host-resolved tenant. Passing an empty
// oidcSubject leaves the column as-is is NOT done here — it is overwritten, so
// callers should pass the current value when only changing the provider.
func (s *Store) SetUserProvider(ctx context.Context, userID, providerID int64, oidcSubject string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET provider_id=?, oidc_subject=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		providerID, oidcSubject, userID)
	return err
}

// GetUserByProviderEmail looks a user up within a single provider (tenant), the
// multi-tenant analogue of GetUserByEmail. Returns (nil, nil) if absent.
func (s *Store) GetUserByProviderEmail(ctx context.Context, providerID int64, email string) (*model.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, userSelect()+` FROM users WHERE provider_id=? AND email=?`, providerID, email))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// ListUsersByProvider lists a tenant's users (provider admin + delete-cascade).
func (s *Store) ListUsersByProvider(ctx context.Context, providerID int64) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx, userSelect()+` FROM users WHERE provider_id=? ORDER BY id`, providerID)
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
	row := s.db.QueryRowContext(ctx, userSelect()+` FROM users WHERE id=?`, id)
	return scanUser(row)
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	row := s.db.QueryRowContext(ctx, userSelect()+` FROM users WHERE email=?`, email)
	return scanUser(row)
}

// GetUserByUsername is the username half of dual-side login (migration 00025).
// Callers should reach it through identity.Resolve rather than directly, so
// the e-mail/username disambiguation lives in one place.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	row := s.db.QueryRowContext(ctx, userSelect()+` FROM users WHERE username=?`, username)
	return scanUser(row)
}

// SetUserUsername claims a login name. The unique index — not any check above
// this call — is what actually guarantees uniqueness, so a caller racing
// another creation gets an error here and is expected to try another name.
func (s *Store) SetUserUsername(ctx context.Context, id int64, username string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET username=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, username, id)
	return err
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

// UpdateUserAvatar stores (or, with an empty string, clears) the profile
// picture. Validation of the URI belongs to the API layer, which is the only
// place that knows what a browser will accept.
func (s *Store) UpdateUserAvatar(ctx context.Context, id int64, avatarURL string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET avatar_url=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, avatarURL, id)
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

// SetSessionIDToken implements db.Store (migration 00057).
func (s *Store) SetSessionIDToken(ctx context.Context, token, idToken string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET id_token=? WHERE token=?`, idToken, token)
	return err
}

// GetSessionIDToken implements db.Store: "" for a session without one or no
// session at all. Expiry is deliberately not checked — the IdP accepts an
// expired id_token as a hint, and sign-out is exactly when it has expired.
func (s *Store) GetSessionIDToken(ctx context.Context, token string) (string, error) {
	var idToken sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id_token FROM sessions WHERE token=?`, token).Scan(&idToken)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return idToken.String, err
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
		`INSERT INTO api_tokens (user_id, label, token_hash, scopes, usernames, kind, expires_at) VALUES (?,?,?,?,?,?,?)`,
		t.UserID, t.Label, t.TokenHash, t.Scopes, t.Usernames, model.NormalizeTokenKind(t.Kind), t.ExpiresAt)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	t.ID = id
	// Echo back what was actually stored, not what the caller passed: a caller
	// that left Kind empty gets "app" persisted (migration 00030) and must see
	// that, or the mint response would describe a token that does not exist.
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
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO s3_access_keys (access_key_id, secret_enc, user_id, api_token_id, label, bucket, prefix, expires_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		k.AccessKeyID, k.SecretEnc, k.UserID, k.APITokenID, k.Label, k.Bucket, k.Prefix, k.ExpiresAt)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetS3AccessKeyByID(ctx, id)
}

func (s *Store) GetS3AccessKeyByID(ctx context.Context, id int64) (*model.S3AccessKey, error) {
	return scanS3AccessKey(s.db.QueryRowContext(ctx, `SELECT `+s3KeyCols+` FROM s3_access_keys WHERE id=?`, id))
}

// GetS3AccessKey is the hot path: every signed request looks its key up here,
// so it is a single indexed read and nothing more.
func (s *Store) GetS3AccessKey(ctx context.Context, accessKeyID string) (*model.S3AccessKey, error) {
	return scanS3AccessKey(s.db.QueryRowContext(ctx, `SELECT `+s3KeyCols+` FROM s3_access_keys WHERE access_key_id=?`, accessKeyID))
}

func (s *Store) ListS3AccessKeys(ctx context.Context, userID int64) ([]*model.S3AccessKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+s3KeyCols+` FROM s3_access_keys WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Never nil: a JSON handler returning null for an empty collection breaks
	// every consumer that calls .length on it, and does so exactly when it is
	// most likely to be read — a fresh install with no keys yet.
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
	_, err := s.db.ExecContext(ctx, `UPDATE s3_access_keys SET last_used_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

func (s *Store) SetS3AccessKeyDisabled(ctx context.Context, id int64, disabled bool) error {
	if disabled {
		_, err := s.db.ExecContext(ctx, `UPDATE s3_access_keys SET disabled_at=CURRENT_TIMESTAMP WHERE id=?`, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE s3_access_keys SET disabled_at=NULL WHERE id=?`, id)
	return err
}

// DeleteS3AccessKey takes the owner id too: deletion is reachable from a
// self-service surface, and a query scoped to the owner cannot delete another
// account key even if the handler above it forgets to check.
func (s *Store) DeleteS3AccessKey(ctx context.Context, id, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM s3_access_keys WHERE id=? AND user_id=?`, id, userID)
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
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO ssh_public_keys (user_id, name, fingerprint, public_key) VALUES (?,?,?,?)`,
		k.UserID, k.Name, k.Fingerprint, k.PublicKey)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetSSHPublicKeyByID(ctx, id)
}

// GetSSHPublicKey is the login path: one indexed read on the fingerprint.
func (s *Store) GetSSHPublicKey(ctx context.Context, fingerprint string) (*model.SSHPublicKey, error) {
	return scanSSHPublicKey(s.db.QueryRowContext(ctx,
		`SELECT `+sshKeyCols+` FROM ssh_public_keys WHERE fingerprint=?`, fingerprint))
}

func (s *Store) GetSSHPublicKeyByID(ctx context.Context, id int64) (*model.SSHPublicKey, error) {
	return scanSSHPublicKey(s.db.QueryRowContext(ctx,
		`SELECT `+sshKeyCols+` FROM ssh_public_keys WHERE id=?`, id))
}

func (s *Store) ListSSHPublicKeys(ctx context.Context, userID int64) ([]*model.SSHPublicKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+sshKeyCols+` FROM ssh_public_keys WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Never nil — see ListS3AccessKeys for why an empty collection must still
	// be an array on the wire.
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
	_, err := s.db.ExecContext(ctx, `UPDATE ssh_public_keys SET last_used_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

func (s *Store) SetSSHPublicKeyDisabled(ctx context.Context, id int64, disabled bool) error {
	if disabled {
		_, err := s.db.ExecContext(ctx, `UPDATE ssh_public_keys SET disabled_at=CURRENT_TIMESTAMP WHERE id=?`, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE ssh_public_keys SET disabled_at=NULL WHERE id=?`, id)
	return err
}

// DeleteSSHPublicKey takes the owner id for the same reason
// DeleteS3AccessKey does: the surface is self-service, and a query scoped to
// the owner cannot delete somebody else key even if a handler forgets to check.
func (s *Store) DeleteSSHPublicKey(ctx context.Context, id, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ssh_public_keys WHERE id=? AND user_id=?`, id, userID)
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
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO nfs_exports (user_id, api_token_id, label, token_hash, storage_name, prefix, read_only, allow_cidrs, expires_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		e.UserID, e.APITokenID, e.Label, e.TokenHash, e.StorageName, e.Prefix, e.ReadOnly, e.AllowCIDRs, e.ExpiresAt)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetNFSExportByID(ctx, id)
}

// GetNFSExport is the mount path: one indexed read on the hashed secret.
func (s *Store) GetNFSExport(ctx context.Context, tokenHash string) (*model.NFSExport, error) {
	return scanNFSExport(s.db.QueryRowContext(ctx,
		`SELECT `+nfsExportCols+` FROM nfs_exports WHERE token_hash=?`, tokenHash))
}

func (s *Store) GetNFSExportByID(ctx context.Context, id int64) (*model.NFSExport, error) {
	return scanNFSExport(s.db.QueryRowContext(ctx,
		`SELECT `+nfsExportCols+` FROM nfs_exports WHERE id=?`, id))
}

func (s *Store) ListNFSExports(ctx context.Context, userID int64) ([]*model.NFSExport, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+nfsExportCols+` FROM nfs_exports WHERE user_id=? ORDER BY created_at DESC`, userID)
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
	_, err := s.db.ExecContext(ctx, `UPDATE nfs_exports SET last_used_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

func (s *Store) SetNFSExportDisabled(ctx context.Context, id int64, disabled bool) error {
	if disabled {
		_, err := s.db.ExecContext(ctx, `UPDATE nfs_exports SET disabled_at=CURRENT_TIMESTAMP WHERE id=?`, id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE nfs_exports SET disabled_at=NULL WHERE id=?`, id)
	return err
}

// DeleteNFSExport takes the owner id too — see DeleteS3AccessKey.
func (s *Store) DeleteNFSExport(ctx context.Context, id, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM nfs_exports WHERE id=? AND user_id=?`, id, userID)
	return err
}

func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash string) (*model.APIToken, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, label, token_hash, scopes, COALESCE(usernames,''), COALESCE(kind,'app'), last_used_at, expires_at, created_at FROM api_tokens WHERE token_hash=?`,
		tokenHash)
	return scanAPIToken(row)
}

// GetAPITokenByID fetches a token by primary key. Used where a row already
// references a token and the credential itself was never presented — an S3
// access key checking that the token it inherits from is still valid.
func (s *Store) GetAPITokenByID(ctx context.Context, id int64) (*model.APIToken, error) {
	return scanAPIToken(s.db.QueryRowContext(ctx,
		`SELECT id, user_id, label, token_hash, scopes, COALESCE(usernames,''), COALESCE(kind,'app'), last_used_at, expires_at, created_at FROM api_tokens WHERE id=?`, id))
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
		`SELECT id, user_id, label, token_hash, scopes, COALESCE(usernames,''), COALESCE(kind,'app'), last_used_at, expires_at, created_at FROM api_tokens WHERE user_id=? ORDER BY created_at DESC`,
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

// UpdateAPITokenMeta edits label, the username allow-list and/or the token
// kind. nil = keep.
func (s *Store) UpdateAPITokenMeta(ctx context.Context, id int64, label, usernames, kind *string) error {
	if label != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET label=? WHERE id=?`, *label, id); err != nil {
			return err
		}
	}
	if usernames != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET usernames=? WHERE id=?`, *usernames, id); err != nil {
			return err
		}
	}
	if kind != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET kind=? WHERE id=?`, model.NormalizeTokenKind(*kind), id); err != nil {
			return err
		}
	}
	return nil
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
	if err := row.Scan(&t.ID, &t.UserID, &t.Label, &t.TokenHash, &t.Scopes, &t.Usernames, &t.Kind, &lastUsed, &expires, &t.CreatedAt); err != nil {
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

// shareCols is the column list every single-row share read shares. ONE
// spelling, because a column that reaches scanShare in a different order is a
// silent mis-scan rather than an error, and the four reads below are four
// chances to add a column to three of them.
//
// ⚠ COALESCE on every nullable text column: 00046 adds state_json/files_json
// as NULLABLE (MySQL cannot default a TEXT column), and scanning NULL into a
// Go string fails inside the driver rather than anywhere that could explain it.
const shareCols = `id, node_id, token, COALESCE(pin_hash,''), expires_at, max_downloads, download_count, created_by, COALESCE(created_via,''), created_at, COALESCE(kind,'download'), max_uploads, upload_count, drop_settings, plugin_id, COALESCE(page_id,''), COALESCE(subject,''), COALESCE(state_json,''), COALESCE(files_json,''), pin_fails, locked_until, COALESCE(pin_enc,''), visit_count, COALESCE(purpose_json,'')`

func (s *Store) CreateShare(ctx context.Context, sh *model.Share) (*model.Share, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO shares (node_id, token, pin_hash, pin_enc, expires_at, max_downloads, created_by, created_via, kind, max_uploads, drop_settings, plugin_id, page_id, subject, state_json, files_json, purpose_json) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sh.NodeID, sh.Token, sh.PinHash, sh.PinEnc, sh.ExpiresAt, sh.MaxDownloads, sh.CreatedBy, sh.CreatedVia, shareKind(sh.Kind), sh.MaxUploads, sh.DropSettings,
		sh.PluginID, sh.PageID, sh.Subject, sh.StateJSON, sh.FilesJSON, sh.PurposeJSON)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	sh.ID = id
	sh.CreatedAt = time.Now()
	sh.HasPin = sh.PinHash != ""
	sh.PinRecoverable = sh.PinEnc != ""
	return sh, nil
}

func (s *Store) GetShareByToken(ctx context.Context, token string) (*model.Share, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+shareCols+` FROM shares WHERE token=?`, token)
	return scanShare(row)
}

func (s *Store) GetShareByID(ctx context.Context, id int64) (*model.Share, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+shareCols+` FROM shares WHERE id=?`, id)
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
		where = append(where, `(s.max_downloads IS NULL OR (CASE WHEN s.plugin_id > 0 AND COALESCE(s.page_id,'') <> '' THEN s.visit_count ELSE s.download_count END) < s.max_downloads)`)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM shares s WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT `+shareMetaCols+` FROM shares s `+shareMetaJoins+` WHERE `+whereSQL+` ORDER BY s.created_at DESC LIMIT ? OFFSET ?`, args...)
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
// projection: the share, the creator's address, the node's path, the storage's
// name and — since 00046 — the app that opened the link. Both listings read
// it, so a column added for one cannot go missing from the other.
//
// ⚠⚠ `pin_enc` is NOT in this list and must never be: 00049 makes a PIN
// recoverable for its owner and for an admin, through ONE audited endpoint that
// reads the single row. A listing that carried the sealed value would hand a
// page of ciphertexts to anything that can open an admin table — and the whole
// point of sealing is that the ciphertext travels where the plaintext must not.
// What the row DOES carry is the boolean below: whether a PIN could be shown,
// which is what lets a screen offer the verb or say plainly that it cannot.
const shareMetaCols = `s.id, s.node_id, s.token, COALESCE(s.pin_hash,''), s.expires_at, s.max_downloads, s.download_count, s.visit_count, s.created_by, COALESCE(s.created_via,''), s.created_at,
		        s.plugin_id, COALESCE(s.page_id,''), COALESCE(s.subject,''), COALESCE(s.purpose_json,''),
		        CASE WHEN COALESCE(s.pin_enc,'') <> '' THEN 1 ELSE 0 END,
		        s.revoked_at,
		        COALESCE(u.email,''), COALESCE(n.path,''), COALESCE(st.name,''), COALESCE(ap.name,'')`

const shareMetaJoins = `LEFT JOIN users u    ON u.id=s.created_by
		 LEFT JOIN nodes n    ON n.id=s.node_id
		 LEFT JOIN storages st ON st.id=n.storage_id
		 LEFT JOIN app_plugins ap ON ap.id=s.plugin_id`

func scanShareMeta(rows *sql.Rows) ([]*db.ShareWithMeta, error) {
	var out []*db.ShareWithMeta
	for rows.Next() {
		sh := &model.Share{}
		var creatorEmail, nodePath, storageName, pluginName string
		// Scanned as 0/1 rather than into a bool: the same projection runs on
		// SQLite, MySQL and (in its twin) PostgreSQL, and the three do not agree
		// on what a comparison yields to the driver.
		var pinRecoverable int
		if err := rows.Scan(&sh.ID, &sh.NodeID, &sh.Token, &sh.PinHash, &sh.ExpiresAt, &sh.MaxDownloads, &sh.DownloadCount, &sh.VisitCount, &sh.CreatedBy, &sh.CreatedVia, &sh.CreatedAt,
			&sh.PluginID, &sh.PageID, &sh.Subject, &sh.PurposeJSON, &pinRecoverable,
			&sh.RevokedAt,
			&creatorEmail, &nodePath, &storageName, &pluginName); err != nil {
			return nil, err
		}
		sh.HasPin = sh.PinHash != ""
		sh.PinRecoverable = pinRecoverable != 0
		out = append(out, &db.ShareWithMeta{Share: sh, CreatorEmail: creatorEmail, NodePath: nodePath, StorageName: storageName, PluginName: pluginName})
	}
	return out, rows.Err()
}

// ListAppPluginShares is the admin view of the links APPS opened — the
// signature requests an e-signature plugin sent out, and every other public
// page an installed app is holding. pluginID 0 means every app; it never means
// every share, which is what ListAllShares is for.
func (s *Store) ListAppPluginShares(ctx context.Context, pluginID int64, activeOnly bool, limit, offset int) ([]*db.ShareWithMeta, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	where := []string{"s.plugin_id > 0"}
	args := []any{}
	if pluginID > 0 {
		where = append(where, `s.plugin_id=?`)
		args = append(args, pluginID)
	}
	if activeOnly {
		where = append(where, `(s.expires_at IS NULL OR s.expires_at > CURRENT_TIMESTAMP)`)
		where = append(where, `(s.max_downloads IS NULL OR (CASE WHEN s.plugin_id > 0 AND COALESCE(s.page_id,'') <> '' THEN s.visit_count ELSE s.download_count END) < s.max_downloads)`)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM shares s WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT `+shareMetaCols+` FROM shares s `+shareMetaJoins+` WHERE `+whereSQL+` ORDER BY s.created_at DESC LIMIT ? OFFSET ?`, args...)
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
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET state_json=? WHERE id=?`, orJSON(stateJSON, "{}"), id)
	return err
}

// UpdateSharePinLock writes the strike counter and the lock deadline and
// NOTHING else — a wrong PIN must not be able to move an expiry or a cap by
// riding along in a whole-row save.
func (s *Store) UpdateSharePinLock(ctx context.Context, id int64, fails int, until *time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET pin_fails=?, locked_until=? WHERE id=?`, fails, until, id)
	return err
}

// RevokeShare soft-revokes by setting expires_at = NOW. Audit trail is
// kept (the row is not deleted).
func (s *Store) RevokeShare(ctx context.Context, id int64) error {
	// ⚠ Both columns, one statement: `expires_at` is what stops the link
	// (every public path already checks it); `revoked_at` is only the word a
	// listing needs to say "revoked" instead of "expired" (00053).
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET expires_at=CURRENT_TIMESTAMP, revoked_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

func (s *Store) ListSharesByNode(ctx context.Context, nodeID int64) ([]*model.Share, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+shareCols+` FROM shares WHERE node_id=? ORDER BY created_at DESC`, nodeID)
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

// IncrementShareVisit counts one opening of an app page (00052): what the
// page's `max_visits` ceiling is measured against, and never a download.
func (s *Store) IncrementShareVisit(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE shares SET visit_count = visit_count + 1 WHERE id=?`, id)
	return err
}

// ReserveShareDownload claims one download against the cap in a single
// statement, so overlapping requests cannot all pass a check that each of them
// read before any of them wrote. False = the cap is already spent.
func (s *Store) ReserveShareDownload(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE shares SET download_count = download_count + 1
		WHERE id=? AND (max_downloads IS NULL OR download_count < max_downloads)`, id)
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
		WHERE id=? AND download_count > 0`, id)
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
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		u.ID, u.StorageID, u.StorageKey, u.UserID, u.TotalSize, u.ChunkSize, u.Mime, u.Hash, u.ReceivedBytes, u.State, u.ExpiresAt)
	return err
}

func (s *Store) GetStagedUpload(ctx context.Context, id string) (*model.StagedUpload, error) {
	return scanStagedUpload(s.db.QueryRowContext(ctx,
		`SELECT `+stagedUploadColumns+` FROM staged_uploads WHERE id=?`, id))
}

func (s *Store) GetStagedUploadByNode(ctx context.Context, nodeID int64) (*model.StagedUpload, error) {
	return scanStagedUpload(s.db.QueryRowContext(ctx,
		`SELECT `+stagedUploadColumns+` FROM staged_uploads WHERE node_id=? ORDER BY updated_at DESC LIMIT 1`, nodeID))
}

func (s *Store) UpdateStagedUploadProgress(ctx context.Context, id string, receivedBytes int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE staged_uploads SET received_bytes=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, receivedBytes, id)
	return err
}

func (s *Store) UpdateStagedUploadState(ctx context.Context, id, state, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE staged_uploads SET state=?, error=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, state, errMsg, id)
	return err
}

func (s *Store) AttachStagedUploadTarget(ctx context.Context, id string, nodeID, opID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE staged_uploads SET node_id=?, op_id=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, nodeID, opID, id)
	return err
}

func (s *Store) DeleteStagedUpload(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM staged_uploads WHERE id=?`, id)
	return err
}

func (s *Store) ListStagedUploads(ctx context.Context, state string, limit int) ([]*model.StagedUpload, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT ` + stagedUploadColumns + ` FROM staged_uploads`
	args := []any{}
	if state != "" {
		q += ` WHERE state=?`
		args = append(args, state)
	}
	q += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
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
	// Same trap as ListStaleNodes: CURRENT_TIMESTAMP is written as
	// `YYYY-MM-DD HH:MM:SS` while a bound time.Time goes out as RFC3339, and
	// `' '` < `T`, so an unformatted bound would call every row of the current
	// second "idle" and sweep uploads that are in flight right now.
	beforeStr := before.UTC().Format("2006-01-02 15:04:05")
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+stagedUploadColumns+` FROM staged_uploads WHERE updated_at < ? ORDER BY updated_at ASC LIMIT ?`,
		beforeStr, limit)
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
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT SUM(total_size) FROM staged_uploads WHERE user_id=? AND state IN ('staging','committing')`,
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
	rows, err := s.db.QueryContext(ctx, nodeSelectColumns()+
		` FROM nodes WHERE deleted_at IS NULL AND transfer_state IN ('staged','failed') AND id > ? ORDER BY id LIMIT ?`,
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
		`UPDATE nodes SET transfer_state=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, state, nodeID)
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

func (s *Store) GetLastSyncRunByStatus(ctx context.Context, storageID int64, status string) (*model.SyncRun, error) {
	// id breaks a same-second tie: CURRENT_TIMESTAMP has no fraction here.
	row := s.db.QueryRowContext(ctx,
		`SELECT id, storage_id, started_at, finished_at, COALESCE(cursor_before,''), COALESCE(cursor_after,''), seen_count, added, updated, deleted, status, COALESCE(error,'')
		 FROM sync_runs WHERE storage_id=? AND status=? AND finished_at IS NOT NULL ORDER BY started_at DESC, id DESC LIMIT 1`, storageID, status)
	return scanSyncRun(row)
}

func (s *Store) AbortUnfinishedSyncRuns(ctx context.Context, errMsg string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sync_runs SET status='aborted', finished_at=CURRENT_TIMESTAMP, error=? WHERE finished_at IS NULL`, errMsg)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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
	// ⚠ The cutoff is computed here and bound, not written as SQL. This Store
	// also serves MySQL, and `datetime('now', '-5 days')` is SQLite's own
	// function: on MySQL the statement was a syntax error, so the admin Sync
	// history page and every storage's sync-run list answered 500 there. The
	// layout matches what CURRENT_TIMESTAMP writes on SQLite, so the text
	// comparison orders correctly, and MySQL converts it to a DATETIME.
	cutoff := time.Now().UTC().Add(-5 * 24 * time.Hour).Format("2006-01-02 15:04:05")
	where := []string{"started_at >= ?"}
	args := []any{cutoff}
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
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE setting_key=?`, key).Scan(&v)
	return v, err
}

func (s *Store) UpsertSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		s.upsert(`INSERT INTO settings (setting_key, value, updated_at) VALUES (?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(setting_key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`),
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

// ─────────────────── External services ───────────────────

func (s *Store) UpsertExternalService(ctx context.Context, name string, enabled bool, urlS, secretEnc, optionsJSON string, lastCheck time.Time, lastState string) error {
	_, err := s.db.ExecContext(ctx,
		s.upsert(`INSERT INTO external_services (name, enabled, url, secret_enc, options_json, last_check, last_state) VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(name) DO UPDATE SET enabled=excluded.enabled, url=excluded.url, secret_enc=excluded.secret_enc, options_json=excluded.options_json, last_check=excluded.last_check, last_state=excluded.last_state`),
		name, btoi(enabled), urlS, secretEnc, optionsJSON, nullTime(lastCheck), lastState)
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
		s.upsert(`INSERT INTO thumbnails (node_id, state, storage_key, width, height, error, generated_at) VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(node_id) DO UPDATE SET state=excluded.state, storage_key=excluded.storage_key, width=excluded.width, height=excluded.height, error=excluded.error, generated_at=excluded.generated_at`),
		t.NodeID, t.State, t.StorageKey, t.Width, t.Height, t.Error, t.GeneratedAt)
	return err
}

func (s *Store) SetThumbnailState(ctx context.Context, nodeID int64, state, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE thumbnails SET state=?, error=? WHERE node_id=?`, state, errMsg, nodeID)
	return err
}

func (s *Store) DeleteThumbnail(ctx context.Context, nodeID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM thumbnails WHERE node_id=?`, nodeID)
	return err
}

// ExistingNodeIDs answers in batches of 500 placeholders — SQLite's default
// variable ceiling is 999 and the caller feeds it whatever the thumbnail cache
// directory happens to hold.
func (s *Store) ExistingNodeIDs(ctx context.Context, ids []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(ids))
	const batch = 500
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk))
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM nodes WHERE id IN (`+ph+`)`, args...)
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

// nodeColumnsFmt is the ONE place the node column order is written down.
// %[1]s is the table alias ("" for a plain SELECT, "n." for the ones that JOIN
// node_meta and have to disambiguate).
//
// ⚠ It is a format string rather than four hand-kept copies because four is
// what there were, and a column added to `nodes` reached the ordinary listing,
// the search rebuild — and silently missed the tag and starred reads, whose
// scan then failed at runtime on a path the suite only walks in one test.
const nodeColumnsFmt = `%[1]sid, %[1]sstorage_id, %[1]sparent_id, %[1]sname, %[1]spath, %[1]spath_hash, COALESCE(%[1]sstorage_key,''), %[1]stype, %[1]ssize, COALESCE(%[1]smime,''), COALESCE(%[1]setag,''), %[1]sbackend_mtime, %[1]sdb_mtime, %[1]ssync_state, COALESCE(%[1]stransfer_state,'stored'), %[1]sseen_at, %[1]sdeleted_at, %[1]screated_at, %[1]supdated_at, %[1]sowner_id, %[1]slast_actor_id, COALESCE(%[1]sexternal_upload,0)`

var (
	// nodeColumnList is the unqualified list; nodeColumnsN is the "n."-aliased
	// one. Both feed the same scanNode, which is why they cannot drift.
	nodeColumnList = fmt.Sprintf(nodeColumnsFmt, "")
	nodeColumnsN   = fmt.Sprintf(nodeColumnsFmt, "n.")
)

func nodeSelectColumns() string {
	return `SELECT ` + nodeColumnList
}

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
	return `SELECT id, email, COALESCE(display_name,''), COALESCE(password_hash,''), role, COALESCE(totp_secret,''), COALESCE(totp_pending_secret,''), COALESCE(totp_enabled,0), COALESCE(totp_recovery_codes_json,'[]'), locale, timezone, created_at, updated_at, last_login_at, provider_id, COALESCE(oidc_subject,''), COALESCE(quota_bytes,0), COALESCE(usage_bytes,0), COALESCE(enabled,1), COALESCE(avatar_url,''), COALESCE(username,'')`
}

func scanUser(r rowScanner) (*model.User, error) {
	u := &model.User{}
	var totpEnabled int
	var recoveryJSON string
	var providerID sql.NullInt64
	var enabled int
	if err := r.Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Role, &u.TOTPSecret, &u.TOTPPendingSecret, &totpEnabled, &recoveryJSON, &u.Locale, &u.Timezone, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt, &providerID, &u.OIDCSubject, &u.QuotaBytes, &u.UsageBytes, &enabled, &u.AvatarURL, &u.Username); err != nil {
		return nil, err
	}
	u.TOTPEnabled = totpEnabled == 1
	u.Enabled = enabled == 1
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

var nodeColumnsForIndex = nodeColumnList

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
	if prefixes, ok := db.AuditActionPrefixes(action); ok {
		likes := make([]string, len(prefixes))
		for i, p := range prefixes {
			likes[i] = `a.action LIKE ? ESCAPE '\'`
			args = append(args, p)
		}
		cond += " AND (" + strings.Join(likes, " OR ") + ")"
	} else if action != "" {
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
	// ⚠ Not `LIMIT -1`: that is SQLite's spelling of "no limit", and MySQL —
	// which this Store also serves — rejects it as a syntax error. The prune
	// failed on every snapshot there, and Snapshot deliberately ignores a
	// cleanup error, so version history grew without bound and nobody was
	// told. The largest signed 64-bit value means "no limit" to both engines.
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, version_n, COALESCE(storage_key,''), size, COALESCE(etag,''), created_at
		 FROM node_versions
		 WHERE node_id=?
		 ORDER BY version_n DESC
		 LIMIT 9223372036854775807 OFFSET ?`, nodeID, keep)
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
		`SELECT COALESCE(usage_bytes,0), COALESCE(quota_bytes,0) FROM users WHERE id=?`, userID).
		Scan(&used, &limit)
	if err != nil {
		return 0, 0, err
	}
	return used, limit, nil
}

// IncrementUserUsage atomically adjusts usage_bytes (delta may be negative);
// clamps the resulting value at 0 to keep it sane.
//
// ⚠ Not `MAX(0, …)`. SQLite has a two-argument scalar max(); in MySQL, which
// this Store also serves, MAX is an aggregate only and the statement was a
// syntax error. quotastore logs that error and lets the write through, so on
// MySQL every upload succeeded, usage_bytes never moved, and no quota was ever
// enforced. CASE means the same thing to both engines.
func (s *Store) IncrementUserUsage(ctx context.Context, userID int64, delta int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET usage_bytes = CASE
		   WHEN COALESCE(usage_bytes,0) + ? < 0 THEN 0
		   ELSE COALESCE(usage_bytes,0) + ?
		 END WHERE id=?`, delta, delta, userID)
	return err
}

// SetUserEnabled flips the account on/off. Files, quota and grants are
// untouched -- this is an access switch, not a soft delete.
func (s *Store) SetUserEnabled(ctx context.Context, userID int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET enabled=? WHERE id=?`, v, userID)
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

// RecomputeUserUsage scans the file nodes owned by this user, sets
// usage_bytes to the sum of their sizes, and returns the value.
//
// ⚠ TRASHED ROWS ARE INCLUDED — there is deliberately no `deleted_at IS NULL`
// here. Trashed bytes still occupy the storage and still count against the
// ceiling; the purge is the only release point (docs/QUOTAS.md). The filter
// used to exclude them, which put this reconciler at odds with the
// incremental accounting: a recompute forgave every trashed byte, and the
// SubUsage at purge then subtracted them a second time — clamped at zero, so
// the drift was silent.
func (s *Store) RecomputeUserUsage(ctx context.Context, userID int64) (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(size),0) FROM nodes WHERE owner_id=? AND type='file'`,
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

// GetUserDisplayNames answers "what do I call these ids" for a whole listing
// page in ONE query.
//
// ⚠ It deliberately returns names, not users. The Owner column needs a label
// and nothing else, and hydrating full user rows to read one field would drag
// password hashes, TOTP secrets and recovery codes through the listing path
// for every account that happens to own a file in the folder.
//
// The fallback chain is display_name → username → email, because a row with an
// empty display_name is common (OIDC providers that send no name) and an
// Owner column that reads blank is worse than one that reads an address. The
// chain is model.PersonLabel — the one rule every screen names a person by —
// applied in Go, not re-typed as a COALESCE in each dialect's SQL.
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
		ph := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk))
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT id, COALESCE(display_name,''), COALESCE(username,''), COALESCE(email,'') FROM users WHERE id IN (`+ph+`)`, args...)
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

// SetNodeActor updates the last_actor_id column for one node. A nil actor is
// SYSTEM — a change that arrived from outside filex has nobody to name, and
// writing the previous actor there instead would be a lie the UI cannot see
// through.
func (s *Store) SetNodeActor(ctx context.Context, nodeID int64, actorID *int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET last_actor_id=? WHERE id=?`, actorID, nodeID)
	return err
}

// SetNodeExternalUpload marks (or unmarks) a node as having arrived through an
// anonymous drop link. Only ever set to true, by the drop handler.
func (s *Store) SetNodeExternalUpload(ctx context.Context, nodeID int64, external bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET external_upload=? WHERE id=?`, external, nodeID)
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
	where := `deleted_at IS NOT NULL AND deleted_at < ? AND id > ?`
	args := []any{before, afterID}
	if storageIDs != nil {
		where += ` AND storage_id IN (?` + strings.Repeat(`,?`, len(storageIDs)-1) + `)`
		for _, id := range storageIDs {
			args = append(args, id)
		}
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, nodeSelectColumns()+`
		FROM nodes WHERE `+where+`
		ORDER BY id ASC LIMIT ?`, args...)
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
// the same cutoff. One grouped read over the partial deleted_at index.
func (s *Store) CountTrashedExpired(ctx context.Context, before time.Time) (map[int64]db.TrashTally, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT storage_id, COUNT(*), COALESCE(SUM(CASE WHEN type='file' THEN size ELSE 0 END), 0)
		  FROM nodes WHERE deleted_at IS NOT NULL AND deleted_at < ?
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
	row := s.db.QueryRowContext(ctx, `SELECT storage_id, type, path FROM nodes WHERE id=?`, id)
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
			SET deleted_at=NULL,
			    updated_at=CURRENT_TIMESTAMP,
			    parent_id=NULL,
			    name=?, path=?, path_hash=?, storage_key=?
			WHERE id=?`, name, clean, hash, clean, id); err != nil {
			return err
		}
	} else {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE nodes
			SET deleted_at=NULL,
			    updated_at=CURRENT_TIMESTAMP,
			    parent_id=?,
			    name=?, path=?, path_hash=?, storage_key=?
			WHERE id=?`, *parentID, name, clean, hash, clean, id); err != nil {
			return err
		}
	}
	// Restored a trashed directory → revive the descendants that were
	// dragged into the trash with it (SoftDeleteAndRetag mirror).
	if nodeType == string(model.NodeTypeDirectory) && trashPath != "" {
		s.restoreTrashedSubtree(ctx, sid, []string{trashPath}, clean)
	}
	return nil
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

// ─────────────────── User-scoped node meta ───────────────────

// SetUserNodeMeta upserts a (user, node, key) row.
func (s *Store) SetUserNodeMeta(ctx context.Context, userID, nodeID int64, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		s.upsert(`INSERT INTO user_node_meta (user_id, node_id, meta_key, value, updated_at)
		 VALUES (?,?,?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id, node_id, meta_key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`),
		userID, nodeID, key, value)
	return err
}

// DeleteUserNodeMeta removes a single (user, node, key) row.
func (s *Store) DeleteUserNodeMeta(ctx context.Context, userID, nodeID int64, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_node_meta WHERE user_id=? AND node_id=? AND meta_key=?`, userID, nodeID, key)
	return err
}

// GetUserNodeMeta fetches a single value (returns empty string + sql.ErrNoRows if absent).
func (s *Store) GetUserNodeMeta(ctx context.Context, userID, nodeID int64, key string) (string, error) {
	var v sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT value FROM user_node_meta WHERE user_id=? AND node_id=? AND meta_key=?`, userID, nodeID, key).Scan(&v)
	if err != nil {
		return "", err
	}
	return v.String, nil
}

// ListUserNodeMetaForNode returns all (key,value) for one (user,node) pair,
// optionally restricted to keys that start with `prefix`.
func (s *Store) ListUserNodeMetaForNode(ctx context.Context, userID, nodeID int64, prefix string) (map[string]string, error) {
	q := `SELECT meta_key, COALESCE(value,'') FROM user_node_meta WHERE user_id=? AND node_id=?`
	args := []any{userID, nodeID}
	if prefix != "" {
		q += ` AND meta_key LIKE ?`
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
		`SELECT `+nodeColumnsN+`
		 FROM user_node_meta m
		 INNER JOIN nodes n ON n.id = m.node_id
		 WHERE m.user_id=? AND m.meta_key=? AND n.deleted_at IS NULL
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

// ─────────────────── Tags (00055: personal + team) ───────────────────
//
// Vocabulary rows in `tags`, links in `node_tags`. Who may SEE which row is
// decided in handlers/tags.go, not here — see the db.Store comment.

const tagCols = `id, kind, tenant_id, owner_id, name, name_key, created_at`

func sqlitePH(int) string { return "?" }

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
	where, args := db.TagQueryWhere(q, "t", 1, sqlitePH)
	rows, err := s.db.QueryContext(ctx, `SELECT `+tagCols+` FROM tags t WHERE `+where+` ORDER BY t.id`, args...)
	if err != nil {
		return nil, err
	}
	return scanTags(rows)
}

// CreateTag implements db.Store.
func (s *Store) CreateTag(ctx context.Context, t *model.Tag) (*model.Tag, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO tags (kind, tenant_id, owner_id, name, name_key) VALUES (?,?,?,?,?)`,
		t.Kind, t.TenantID, t.OwnerID, t.Name, t.Key)
	if err != nil {
		return nil, err
	}
	out := *t
	out.ID, _ = res.LastInsertId()
	out.CreatedAt = time.Now().UTC()
	return &out, nil
}

// ListNodeTags implements db.Store.
func (s *Store) ListNodeTags(ctx context.Context, nodeID int64) ([]*model.Tag, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.kind, t.tenant_id, t.owner_id, t.name, t.name_key, t.created_at
		 FROM node_tags nt JOIN tags t ON t.id = nt.tag_id
		 WHERE nt.node_id = ? ORDER BY t.id`, nodeID)
	if err != nil {
		return nil, err
	}
	return scanTags(rows)
}

// LinkNodeTags implements db.Store.
//
// The link insert is an upsert that changes nothing, so adding a tag a file
// already carries is not an error on either engine (MySQL has no ON CONFLICT
// DO NOTHING; the rewrite in upsert turns this into ON DUPLICATE KEY UPDATE).
func (s *Store) LinkNodeTags(ctx context.Context, nodeID int64, add, remove []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range add {
		if _, err := tx.ExecContext(ctx,
			s.upsert(`INSERT INTO node_tags (tag_id, node_id) VALUES (?,?)
			 ON CONFLICT(tag_id, node_id) DO UPDATE SET tag_id=excluded.tag_id`), id, nodeID); err != nil {
			return err
		}
	}
	for _, id := range remove {
		if _, err := tx.ExecContext(ctx, `DELETE FROM node_tags WHERE tag_id=? AND node_id=?`, id, nodeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM tags WHERE id=? AND NOT EXISTS (SELECT 1 FROM node_tags WHERE tag_id=?)`, id, id); err != nil {
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
	in, args := db.IDList(tagIDs, 1, sqlitePH)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+nodeColumnsN+`
		 FROM nodes n
		 WHERE n.deleted_at IS NULL
		   AND n.id IN (SELECT node_id FROM node_tags WHERE tag_id IN (`+in+`))
		 ORDER BY n.updated_at DESC
		 LIMIT ?`, append(args, limit)...)
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
	where, args := db.TagQueryWhere(q, "t", 1, sqlitePH)
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

// qmarks is n comma-separated placeholders.
func qmarks(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// mutedEventsClause builds the `n.event NOT IN (?,?,…)` fragment and its args
// for a per-user mute list. Empty list ⇒ empty clause, so the caller appends
// nothing and the query is byte-identical to the unfiltered one.
//
// ⚠ The list is interpolated as PLACEHOLDERS, never as literals: the event ids
// arrive from a user-writable JSON column.
func mutedEventsClause(muted []string) (string, []any) {
	if len(muted) == 0 {
		return "", nil
	}
	args := make([]any, 0, len(muted))
	for _, e := range muted {
		args = append(args, e)
	}
	return "n.event NOT IN (" + qmarks(len(muted)) + ")", args
}

// hiddenBodiesClause builds `(n.body NOT LIKE ? AND …)` for the notify
// service's read-time filter (db.Store.ListNotifications). Empty list ⇒ empty
// clause. Placeholders, never literals, like the mute list. COALESCE because a
// NULL body is not LIKE anything and NOT LIKE would drop it too.
func hiddenBodiesClause(patterns []string) (string, []any) {
	if len(patterns) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(patterns))
	args := make([]any, 0, len(patterns))
	for _, p := range patterns {
		parts = append(parts, "COALESCE(n.body,'') NOT LIKE ?")
		args = append(args, p)
	}
	return "(" + strings.Join(parts, " AND ") + ")", args
}

// bellClause builds the per-user predicate over `notifications n`: the
// reader's own rows, plus the broadcasts (user_id NULL) the filter admits. The
// zero filter admits every broadcast, the predicate this read always had.
//
// since > 0 keeps only the broadcasts after the reader's "mark all read" point
// — an unread read passes it. It sits INSIDE the broadcast branch on purpose:
// SQLite splits the OR into one index search per branch, and only there does
// `n.id > ?` become a range seek on idx_notifications_unread_since instead of a
// filter over every broadcast nobody stamped.
//
// ⚠ Placeholders, never literals, for the same reason as mutedEventsClause.
// ⚠ Every column is qualified: the per-reader join brings its own user_id.
//
// f.OwnOnly reads the reader's own rows alone, f.BroadcastsOnly the admitted
// broadcasts alone (model.BroadcastFilter).
func bellClause(userID int64, f model.BroadcastFilter, since int64) (string, []any) {
	events, op := f.Only, "IN"
	if len(events) == 0 {
		events, op = f.Except, "NOT IN"
	}
	if f.OwnOnly {
		return "n.user_id = ?", []any{userID}
	}
	broadcast := "n.user_id IS NULL"
	var bargs []any
	if since > 0 {
		broadcast += " AND n.id > ?"
		bargs = append(bargs, since)
	}
	if len(events) > 0 {
		broadcast += " AND n.event " + op + " (" + qmarks(len(events)) + ")"
		for _, e := range events {
			bargs = append(bargs, e)
		}
	}
	if f.BroadcastsOnly {
		return "(" + broadcast + ")", bargs
	}
	return "(n.user_id = ? OR (" + broadcast + "))", append([]any{userID}, bargs...)
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
		`SELECT through_id, read_at FROM notification_read_through WHERE user_id=?`, readerID,
	).Scan(&rt.through, &rt.at)
	if errors.Is(err, sql.ErrNoRows) {
		return readThrough{}, nil
	}
	if err != nil {
		return readThrough{}, fmt.Errorf("sqlite: read-through: %w", err)
	}
	return rt, nil
}

// readerJoin joins a reader's single marks on broadcasts and names the
// select-list column that carries them. With no reader there is no join: the
// column is a NULL, so the row shape is the same either way.
func readerJoin(readerID int64) (join, col string, args []any) {
	if readerID <= 0 {
		return "", "NULL", nil
	}
	return " LEFT JOIN notification_reads r ON r.notification_id = n.id AND r.user_id = ?", "r.read_at", []any{readerID}
}

// unreadClause is the unread predicate. For a reader a broadcast is unread when
// nobody stamped its own column, they have not marked it on its own, and it is
// after their "mark all read" point; a row addressed to a user has no marks and
// no point, its own read_at speaks. A per-user read carries the point inside
// bellClause (see there); only the admin-global read (history) needs it here.
func unreadClause(readerID int64, rt readThrough, history bool) (string, []any) {
	if readerID <= 0 {
		return "n.read_at IS NULL", nil
	}
	if history && rt.through > 0 {
		return "n.read_at IS NULL AND r.read_at IS NULL AND (n.user_id IS NOT NULL OR n.id > ?)", []any{rt.through}
	}
	return "n.read_at IS NULL AND r.read_at IS NULL", nil
}

// ListNotifications paginates either a user's view (broadcasts +
// user-scoped) or admin/global view (userID == nil).
//
// onlyUnread keeps the rows still unread for the reader. mutedEvents drops the
// event ids the user has muted; empty means no mute filter. hiddenBodies drops
// rows whose body matches one of the LIKE patterns (see db.Store). broadcasts
// decides which broadcasts the bell takes at all and whose read state they
// carry (see db.Store).
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
	join, readCol, args := readerJoin(broadcasts.ReaderID)
	var since int64
	if onlyUnread {
		since = rt.through
	}
	var whereC []string
	if userID != nil {
		clause, bellArgs := bellClause(*userID, broadcasts, since)
		whereC = append(whereC, clause)
		args = append(args, bellArgs...)
	}
	if onlyUnread {
		unread, unreadArgs := unreadClause(broadcasts.ReaderID, rt, userID == nil)
		whereC = append(whereC, unread)
		args = append(args, unreadArgs...)
	}
	if clause, muteArgs := mutedEventsClause(mutedEvents); clause != "" {
		whereC = append(whereC, clause)
		args = append(args, muteArgs...)
	}
	if clause, hideArgs := hiddenBodiesClause(hiddenBodies); clause != "" {
		whereC = append(whereC, clause)
		args = append(args, hideArgs...)
	}
	from := " FROM notifications n" + join
	if len(whereC) > 0 {
		from += " WHERE " + strings.Join(whereC, " AND ")
	}

	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*)"+from, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("sqlite: count notifications: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT n.id, n.event, n.severity, n.title, n.body, n.meta_json,
		        n.user_id, n.read_at, n.webhook_status, COALESCE(n.webhook_error,''), n.created_at, `+readCol+from+`
		 ORDER BY n.created_at DESC, n.id DESC
		 LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("sqlite: list notifications: %w", err)
	}
	defer rows.Close()
	var out []*model.Notification
	for rows.Next() {
		n, err := scanNotificationForReader(rows)
		if err != nil {
			return nil, 0, err
		}
		rt.apply(n)
		out = append(out, n)
	}
	return out, total, rows.Err()
}

// apply gives a broadcast at or below the reader's "mark all read" point the
// time of that press, when nothing else marked it read.
func (rt readThrough) apply(n *model.Notification) {
	if n.UserID == nil && n.ReadAt == nil && rt.through > 0 && n.ID <= rt.through {
		t := rt.at
		n.ReadAt = &t
	}
}

// MarkNotificationRead bumps read_at on a single row. With a userID the row
// must be ADDRESSED to that user: a broadcast is many readers' row and is
// marked per reader by MarkBroadcastsRead.
func (s *Store) MarkNotificationRead(ctx context.Context, id int64, userID *int64) error {
	q := `UPDATE notifications SET read_at = CURRENT_TIMESTAMP
	      WHERE id=? AND read_at IS NULL`
	args := []any{id}
	if userID != nil {
		q += ` AND user_id = ?`
		args = append(args, *userID)
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("sqlite: mark notif read: %w", err)
	}
	return nil
}

// MarkAllNotificationsRead bumps read_at for every unread row addressed to
// userID — never a broadcast, see MarkNotificationRead. Pass nil for the
// global "mark all" admin sweep.
func (s *Store) MarkAllNotificationsRead(ctx context.Context, userID *int64) error {
	q := `UPDATE notifications SET read_at = CURRENT_TIMESTAMP WHERE read_at IS NULL`
	var args []any
	if userID != nil {
		q += ` AND user_id = ?`
		args = append(args, *userID)
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// MarkAllBroadcastsRead moves readerID's "mark all read" point to the newest
// notification and drops the single marks it has overtaken.
//
// ⚠ The point only moves forward: `read_at` is assigned FIRST and compares
// against the old `through_id`. MySQL applies ON DUPLICATE KEY assignments left
// to right, so in the other order it would already see the new value; SQLite
// evaluates every assignment against the old row, so either order is right
// there.
func (s *Store) MarkAllBroadcastsRead(ctx context.Context, readerID int64) error {
	if readerID <= 0 {
		return nil
	}
	var through int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM notifications`).Scan(&through); err != nil {
		return fmt.Errorf("sqlite: newest notification: %w", err)
	}
	if through == 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, s.upsert(
		`INSERT INTO notification_read_through (user_id, through_id, read_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id) DO UPDATE SET
		   read_at = CASE WHEN excluded.through_id > through_id THEN excluded.read_at ELSE read_at END,
		   through_id = CASE WHEN excluded.through_id > through_id THEN excluded.through_id ELSE through_id END`),
		readerID, through); err != nil {
		return fmt.Errorf("sqlite: mark all broadcasts read: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM notification_reads WHERE user_id = ? AND notification_id <= ?`, readerID, through); err != nil {
		return fmt.Errorf("sqlite: drop overtaken marks: %w", err)
	}
	return nil
}

// markBroadcastsBatch bounds one statement's placeholders well under every
// engine's limit.
const markBroadcastsBatch = 400

// MarkBroadcastsRead records readerID's marks on the given broadcasts. Ids that
// are not unread broadcasts are dropped first — a row addressed to a user keeps
// its read state on the row, and a broadcast stamped through the shared column
// before 00056 is already read for everyone.
//
// ⚠ Two statements, not INSERT … SELECT: MySQL resolves the no-op upsert's
// column against the SELECT's tables too, and notifications has columns of the
// same names. The select's rows are closed before the insert — SQLite runs on
// one connection.
func (s *Store) MarkBroadcastsRead(ctx context.Context, readerID int64, ids []int64) error {
	if readerID <= 0 {
		return nil
	}
	for start := 0; start < len(ids); start += markBroadcastsBatch {
		batch := ids[start:min(start+markBroadcastsBatch, len(ids))]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		keep, err := s.unreadBroadcastIDs(ctx, args)
		if err != nil {
			return err
		}
		if len(keep) == 0 {
			continue
		}
		vals := make([]any, 0, 2*len(keep))
		for _, id := range keep {
			vals = append(vals, id, readerID)
		}
		if _, err := s.db.ExecContext(ctx, s.upsert(
			`INSERT INTO notification_reads (notification_id, user_id) VALUES `+
				strings.TrimSuffix(strings.Repeat("(?,?),", len(keep)), ",")+`
			 ON CONFLICT(notification_id, user_id) DO UPDATE SET notification_id=excluded.notification_id`),
			vals...); err != nil {
			return fmt.Errorf("sqlite: mark broadcasts read: %w", err)
		}
	}
	return nil
}

// unreadBroadcastIDs keeps the ids that are broadcasts nobody stamped through
// the shared column.
func (s *Store) unreadBroadcastIDs(ctx context.Context, ids []any) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM notifications WHERE user_id IS NULL AND read_at IS NULL AND id IN (`+qmarks(len(ids))+`)`, ids...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: find broadcasts: %w", err)
	}
	defer rows.Close()
	var keep []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		keep = append(keep, id)
	}
	return keep, rows.Err()
}

// UnreadNotificationCount returns the bell badge number for a user.
// Pass nil for the global unread count (admin dashboard).
func (s *Store) UnreadNotificationCount(ctx context.Context, userID *int64, mutedEvents, hiddenBodies []string, broadcasts model.BroadcastFilter) (int64, error) {
	rt, err := s.readThroughOf(ctx, broadcasts.ReaderID)
	if err != nil {
		return 0, err
	}
	join, _, args := readerJoin(broadcasts.ReaderID)
	unread, unreadArgs := unreadClause(broadcasts.ReaderID, rt, userID == nil)
	q := `SELECT COUNT(*) FROM notifications n` + join + ` WHERE ` + unread
	args = append(args, unreadArgs...)
	if userID != nil {
		clause, bellArgs := bellClause(*userID, broadcasts, rt.through)
		q += ` AND ` + clause
		args = append(args, bellArgs...)
	}
	if clause, muteArgs := mutedEventsClause(mutedEvents); clause != "" {
		q += ` AND ` + clause
		args = append(args, muteArgs...)
	}
	if clause, hideArgs := hiddenBodiesClause(hiddenBodies); clause != "" {
		q += ` AND ` + clause
		args = append(args, hideArgs...)
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

// ─────────────────── Webhook targets (webhook v2) ───────────────────

// CreateWebhookTarget inserts a new delivery destination and returns
// the stored row (with id + created_at filled in).
func (s *Store) CreateWebhookTarget(ctx context.Context, t *model.WebhookTarget) (*model.WebhookTarget, error) {
	if t == nil || t.Name == "" || t.URL == "" {
		return nil, errors.New("sqlite: webhook target missing name/url")
	}
	enabled := 0
	if t.Enabled {
		enabled = 1
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO webhook_targets (name, url, secret, events, enabled)
		 VALUES (?,?,?,?,?)`,
		t.Name, t.URL, t.Secret, t.Events, enabled)
	if err != nil {
		return nil, fmt.Errorf("sqlite: insert webhook target: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetWebhookTarget(ctx, id)
}

// GetWebhookTarget returns a single target by id.
func (s *Store) GetWebhookTarget(ctx context.Context, id int64) (*model.WebhookTarget, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, url, secret, events, enabled, created_at, last_status, last_error, last_delivery_at
		 FROM webhook_targets WHERE id=?`, id)
	return scanWebhookTarget(row)
}

// ListWebhookTargets returns every target ordered by id. Enabled/event
// filtering happens in the notify dispatcher, not in SQL — the table is
// tiny and the admin list needs disabled rows too.
func (s *Store) ListWebhookTargets(ctx context.Context) ([]*model.WebhookTarget, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, url, secret, events, enabled, created_at, last_status, last_error, last_delivery_at
		 FROM webhook_targets ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list webhook targets: %w", err)
	}
	defer rows.Close()
	var out []*model.WebhookTarget
	for rows.Next() {
		t, err := scanWebhookTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateWebhookTarget replaces the mutable columns of a target row.
func (s *Store) UpdateWebhookTarget(ctx context.Context, t *model.WebhookTarget) error {
	if t == nil || t.ID == 0 || t.Name == "" || t.URL == "" {
		return errors.New("sqlite: webhook target missing id/name/url")
	}
	enabled := 0
	if t.Enabled {
		enabled = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE webhook_targets SET name=?, url=?, secret=?, events=?, enabled=? WHERE id=?`,
		t.Name, t.URL, t.Secret, t.Events, enabled, t.ID)
	if err != nil {
		return fmt.Errorf("sqlite: update webhook target: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteWebhookTarget removes a target row.
func (s *Store) DeleteWebhookTarget(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhook_targets WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("sqlite: delete webhook target: %w", err)
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
		`UPDATE webhook_targets SET last_status=?, last_error=?, last_delivery_at=? WHERE id=?`,
		httpStatus, errVal, at.UTC(), id)
	if err != nil {
		return fmt.Errorf("sqlite: update webhook target delivery: %w", err)
	}
	return nil
}

// scanWebhookTarget accepts both *sql.Row and *sql.Rows. Enabled is
// scanned through an int so the same code serves SQLite (INTEGER) and
// MySQL (TINYINT) via the wrapping driver.
func scanWebhookTarget(rs interface {
	Scan(...any) error
}) (*model.WebhookTarget, error) {
	t := &model.WebhookTarget{}
	var (
		enabled int
		lastSt  sql.NullInt64
		lastErr sql.NullString
		lastAt  sql.NullTime
	)
	if err := rs.Scan(&t.ID, &t.Name, &t.URL, &t.Secret, &t.Events, &enabled, &t.CreatedAt, &lastSt, &lastErr, &lastAt); err != nil {
		return nil, err
	}
	t.Enabled = enabled != 0
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
		s.upsert(`INSERT INTO notification_settings (user_id, in_app_enabled, muted_events, updated_at)
		 VALUES (?,?,?, CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id) DO UPDATE SET
		   in_app_enabled = excluded.in_app_enabled,
		   muted_events   = excluded.muted_events,
		   updated_at     = CURRENT_TIMESTAMP`),
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
		s.upsert(`INSERT INTO replica_failures (path, op, error_code, error_msg, attempts, last_attempt_at)
		 VALUES (?,?,?,?,1, CURRENT_TIMESTAMP)
		 ON CONFLICT(path, op) DO UPDATE SET
		   error_code      = excluded.error_code,
		   error_msg       = excluded.error_msg,
		   attempts        = replica_failures.attempts + 1,
		   last_attempt_at = CURRENT_TIMESTAMP,
		   resolved_at     = NULL`),
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
		s.upsert(`INSERT INTO replica_status_reports (id, generated_at, total_files, failed_count, repaired_count, summary_json)
		 VALUES (1, CURRENT_TIMESTAMP, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   generated_at   = CURRENT_TIMESTAMP,
		   total_files    = excluded.total_files,
		   failed_count   = excluded.failed_count,
		   repaired_count = excluded.repaired_count,
		   summary_json   = excluded.summary_json`),
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
		s.upsert(`INSERT INTO replica_settings (id, report_cron, report_enabled, default_mode, updated_at)
		 VALUES (1, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(id) DO UPDATE SET
		   report_cron    = excluded.report_cron,
		   report_enabled = excluded.report_enabled,
		   default_mode   = excluded.default_mode,
		   updated_at     = CURRENT_TIMESTAMP`),
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

// scanNotificationForReader scans a list row: the stored columns plus the
// reader's mark on a broadcast (NULL when there is none, or no reader). The
// mark wins over the row's own read_at — for the reader it is the answer.
func scanNotificationForReader(rs interface {
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

/* calisma:d3 comments */

// ─────────────────── Node comments (v0.6 "Çalışma" (Work)) ───────────────────

// CreateNodeComment inserts a comment row and returns the stored row
// (with id + timestamps filled in). Body validation (length, emptiness)
// belongs to internal/comments — the store persists what it is given.
func (s *Store) CreateNodeComment(ctx context.Context, c *model.NodeComment) (*model.NodeComment, error) {
	if c == nil || c.NodeID == 0 || c.UserID == 0 || c.Body == "" {
		return nil, errors.New("sqlite: node comment missing node/user/body")
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO node_comments (node_id, user_id, body) VALUES (?,?,?)`,
		c.NodeID, c.UserID, c.Body)
	if err != nil {
		return nil, fmt.Errorf("sqlite: insert node comment: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetNodeComment(ctx, id)
}

// GetNodeComment returns a single live (not soft-deleted) comment by id.
func (s *Store) GetNodeComment(ctx context.Context, id int64) (*model.NodeComment, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT c.id, c.node_id, c.user_id, c.body, c.created_at, c.updated_at,
		        COALESCE(NULLIF(u.display_name, ''), u.email)
		 FROM node_comments c
		 LEFT JOIN users u ON u.id = c.user_id
		 WHERE c.id=? AND c.deleted_at IS NULL`, id)
	return scanNodeComment(row)
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
		 WHERE c.node_id=? AND c.deleted_at IS NULL
		 ORDER BY c.created_at ASC, c.id ASC`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list node comments: %w", err)
	}
	defer rows.Close()
	var out []*model.NodeComment
	for rows.Next() {
		c, err := scanNodeComment(rows)
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
		`UPDATE node_comments SET deleted_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP
		 WHERE id=? AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("sqlite: soft delete node comment: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteNodeCommentsByNode hard-deletes every comment row of a node —
// the trash purge hook (belt and suspenders next to the nodes FK
// CASCADE, which engines without FK enforcement may skip).
func (s *Store) DeleteNodeCommentsByNode(ctx context.Context, nodeID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM node_comments WHERE node_id=?`, nodeID)
	if err != nil {
		return fmt.Errorf("sqlite: delete node comments by node: %w", err)
	}
	return nil
}

// scanNodeComment accepts both *sql.Row and *sql.Rows (7 columns:
// comment row + joined author name).
func scanNodeComment(rs interface {
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
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO plugins (name, kind, binary_path, sha256, address, token_sealed, enabled, version, driver, last_error)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.Kind, p.Binary, p.SHA256, p.Address, p.TokenSealed, p.Enabled, p.Version, p.Driver, p.LastError)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetPlugin(ctx, id)
}

func (s *Store) GetPlugin(ctx context.Context, id int64) (*model.Plugin, error) {
	return scanPlugin(s.db.QueryRowContext(ctx, `SELECT `+pluginCols+` FROM plugins WHERE id=?`, id))
}

func (s *Store) GetPluginByName(ctx context.Context, name string) (*model.Plugin, error) {
	return scanPlugin(s.db.QueryRowContext(ctx, `SELECT `+pluginCols+` FROM plugins WHERE name=?`, name))
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
		`UPDATE plugins SET kind=?, binary_path=?, sha256=?, address=?, token_sealed=?, enabled=?, version=?, driver=?, last_error=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		p.Kind, p.Binary, p.SHA256, p.Address, p.TokenSealed, p.Enabled, p.Version, p.Driver, p.LastError, p.ID)
	return err
}

func (s *Store) DeletePlugin(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM plugins WHERE id=?`, id)
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
// ⚠ This file also serves MySQL (NewMySQLStore): `?` placeholders only, no
// RETURNING, no ON CONFLICT (replace-all sets are written as DELETE + INSERT
// inside one transaction), and the `key` column is backtick-quoted because it
// is a reserved word on MySQL — SQLite accepts the backticks too.

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
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO app_plugins (name, version, label_json, manifest_json, wasm_path, sha256, source, source_url, signed, permissions_json, enabled, last_error)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.Version, orJSON(p.LabelJSON, "{}"), orJSON(p.ManifestJSON, "{}"), p.WasmPath, p.SHA256,
		p.Source, p.SourceURL, p.Signed, orJSON(p.PermissionsJSON, "[]"), p.Enabled, p.LastError)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetAppPlugin(ctx, id)
}

func (s *Store) GetAppPlugin(ctx context.Context, id int64) (*model.AppPlugin, error) {
	return scanAppPlugin(s.db.QueryRowContext(ctx, `SELECT `+appPluginCols+` FROM app_plugins WHERE id=?`, id))
}

func (s *Store) GetAppPluginByName(ctx context.Context, name string) (*model.AppPlugin, error) {
	return scanAppPlugin(s.db.QueryRowContext(ctx, `SELECT `+appPluginCols+` FROM app_plugins WHERE name=?`, name))
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
		`UPDATE app_plugins SET version=?, label_json=?, manifest_json=?, wasm_path=?, sha256=?, source=?, source_url=?, signed=?, permissions_json=?, enabled=?, last_error=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
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
		`DELETE FROM app_plugin_schedule WHERE plugin_id=?`,
		`DELETE FROM app_plugin_jobs WHERE plugin_id=?`,
		`DELETE FROM app_plugin_state WHERE plugin_id=?`,
		`DELETE FROM app_plugin_locks WHERE plugin_id=?`,
		`DELETE FROM app_plugin_overrides WHERE plugin_id=?`,
		`DELETE FROM app_plugin_settings WHERE plugin_id=?`,
		`DELETE FROM app_plugins WHERE id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetAppPluginSettings(ctx context.Context, pluginID int64) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT `key`, value FROM app_plugin_settings WHERE plugin_id=?", pluginID)
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_plugin_settings WHERE plugin_id=?`, pluginID); err != nil {
		return err
	}
	for k, v := range values {
		if _, err := tx.ExecContext(ctx, "INSERT INTO app_plugin_settings (plugin_id, `key`, value) VALUES (?,?,?)", pluginID, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListAppPluginOverrides(ctx context.Context, pluginID int64) ([]*model.AppPluginOverride, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT plugin_id, action_id, enabled, admin_only, applies_json FROM app_plugin_overrides WHERE plugin_id=? ORDER BY action_id`, pluginID)
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_plugin_overrides WHERE plugin_id=?`, pluginID); err != nil {
		return err
	}
	for _, o := range list {
		if o == nil || strings.TrimSpace(o.ActionID) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO app_plugin_overrides (plugin_id, action_id, enabled, admin_only, applies_json) VALUES (?,?,?,?,?)`,
			pluginID, o.ActionID, o.Enabled, o.AdminOnly, o.AppliesJSON); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetAppPluginState(ctx context.Context, pluginID, storageID int64, pathHash, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx,
		"SELECT value FROM app_plugin_state WHERE plugin_id=? AND storage_id=? AND path_hash=? AND `key`=?",
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
		"DELETE FROM app_plugin_state WHERE plugin_id=? AND storage_id=? AND path_hash=? AND `key`=?",
		pluginID, storageID, pathHash, key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO app_plugin_state (plugin_id, storage_id, path_hash, rel, `key`, value) VALUES (?,?,?,?,?,?)",
		pluginID, storageID, pathHash, rel, key, value); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteAppPluginState(ctx context.Context, pluginID, storageID int64, pathHash, key string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM app_plugin_state WHERE plugin_id=? AND storage_id=? AND path_hash=? AND `key`=?",
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
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.OpID, j.PluginID, j.PluginName, j.ActionID, j.StorageID, orJSON(j.PathsJSON, "[]"), orJSON(j.ParamsJSON, "{}"),
		j.ActorID, j.Locale, j.Label, j.Status, j.Message, orJSON(j.OutputsJSON, "[]"), j.Error)
	return err
}

func (s *Store) GetAppPluginJob(ctx context.Context, id string) (*model.AppPluginJob, error) {
	return scanAppPluginJob(s.db.QueryRowContext(ctx, `SELECT `+appPluginJobCols+` FROM app_plugin_jobs WHERE id=?`, id))
}

func (s *Store) ListAppPluginJobsByOp(ctx context.Context, opIDs []int64) (map[int64]*model.AppPluginJob, error) {
	out := map[int64]*model.AppPluginJob{}
	if len(opIDs) == 0 {
		return out, nil
	}
	ph := make([]string, len(opIDs))
	args := make([]any, len(opIDs))
	for i, id := range opIDs {
		ph[i] = "?"
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
	_, err := s.db.ExecContext(ctx, `UPDATE app_plugin_jobs SET op_id=? WHERE id=?`, opID, jobID)
	return err
}

func (s *Store) UpdateAppPluginJob(ctx context.Context, j *model.AppPluginJob) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE app_plugin_jobs SET op_id=?, status=?, message=?, outputs_json=?, error=?, finished_at=? WHERE id=?`,
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
		`INSERT INTO app_plugin_signing_keys (id, tenant_id, plugin_id, purpose, subject, cert_pem, key_sealed, expires_at) VALUES (?,?,?,?,?,?,?,?)`,
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

func (s *Store) GetAppPluginSealKey(ctx context.Context, tenantID, pluginID int64) (*model.AppPluginSigningKey, error) {
	k, err := scanAppPluginSigningKey(s.db.QueryRowContext(ctx,
		`SELECT `+appPluginSigningKeyCols+` FROM app_plugin_signing_keys WHERE tenant_id=? AND plugin_id=? AND purpose='platform' AND destroyed_at IS NULL ORDER BY created_at DESC LIMIT 1`, tenantID, pluginID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return k, err
}

func (s *Store) RetireAppPluginSigningCA(ctx context.Context, tenantID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE app_plugin_signing_keys SET retired_at=CURRENT_TIMESTAMP WHERE tenant_id=? AND purpose='ca' AND retired_at IS NULL`, tenantID)
	return err
}

func (s *Store) ListAppPluginSigningCAs(ctx context.Context, tenantID int64) ([]*model.AppPluginSigningKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+appPluginSigningKeyCols+` FROM app_plugin_signing_keys WHERE tenant_id=? AND purpose='ca' ORDER BY created_at DESC`, tenantID)
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
	_, err := s.db.ExecContext(ctx, `UPDATE app_plugin_signing_keys SET key_sealed='', destroyed_at=CURRENT_TIMESTAMP WHERE id=? AND destroyed_at IS NULL`, id)
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
	q := "SELECT st.storage_id, s.name, st.rel, st.`key`, st.value" +
		" FROM app_plugin_state st" +
		" JOIN storages s ON s.id = st.storage_id" +
		" LEFT JOIN nodes n ON n.storage_id = st.storage_id AND n.path_hash = st.path_hash" +
		" WHERE st.plugin_id = ? AND st.rel <> '' AND (n.id IS NULL OR n.deleted_at IS NULL)"
	args := []any{pluginID}
	if key != "" {
		q += " AND st.`key` = ?"
		args = append(args, key)
	}
	q += " ORDER BY st.rel LIMIT ?"
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
			ph[i] = "?"
			args = append(args, h)
		}
		rows, err := s.db.QueryContext(ctx,
			"SELECT st.path_hash, p.name, st.`key` FROM app_plugin_state st JOIN app_plugins p ON p.id=st.plugin_id WHERE st.storage_id=? AND st.path_hash IN ("+strings.Join(ph, ",")+")",
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_plugin_locks WHERE storage_id=? AND path_hash=?`, l.StorageID, l.PathHash); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO app_plugin_locks (storage_id, path_hash, rel, plugin_id, plugin_name, reason, until, created_by) VALUES (?,?,?,?,?,?,?,?)`,
		l.StorageID, l.PathHash, l.Rel, l.PluginID, l.PluginName, l.Reason, l.Until, l.CreatedBy); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetAppPluginLock(ctx context.Context, storageID int64, pathHash string) (*model.AppPluginLock, error) {
	l, err := scanAppPluginLock(s.db.QueryRowContext(ctx, `SELECT `+appPluginLockCols+` FROM app_plugin_locks WHERE storage_id=? AND path_hash=?`, storageID, pathHash))
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
		rows, err = s.db.QueryContext(ctx, `SELECT `+appPluginLockCols+` FROM app_plugin_locks WHERE storage_id=? ORDER BY created_at`, storageID)
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
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_plugin_locks WHERE storage_id=? AND path_hash=?`, storageID, pathHash)
	return err
}

func (s *Store) DeleteExpiredAppPluginLocks(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM app_plugin_locks WHERE until IS NOT NULL AND until < ?`, now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ── App-plugin schedule (00050) ────────────────────────────────────────
//
// `key` is backquoted throughout: it is a reserved word on MySQL, which
// shares this implementation, and SQLite accepts the quoting.

const appPluginScheduleCols = "plugin_id, `key`, due_at, action_id, storage_id, paths_json, params_json, status, attempts, claimed_by, claimed_at, job_id, error, created_at, updated_at"

func scanAppPluginScheduleItem(r rowScanner) (*model.AppPluginScheduleItem, error) {
	it := &model.AppPluginScheduleItem{}
	if err := r.Scan(&it.PluginID, &it.Key, &it.DueAt, &it.ActionID, &it.StorageID, &it.PathsJSON, &it.ParamsJSON,
		&it.Status, &it.Attempts, &it.ClaimedBy, &it.ClaimedAt, &it.JobID, &it.Error, &it.CreatedAt, &it.UpdatedAt); err != nil {
		return nil, err
	}
	return it, nil
}

// PutAppPluginScheduleItem writes the item, replacing an existing one unless
// it is RUNNING — a row another process holds the lease on is its business,
// not ours, and stomping it would let the same work run twice.
//
// Delete-then-insert rather than an upsert: the two engines this file serves
// spell upserts differently, and the read is needed anyway to see the lease.
func (s *Store) PutAppPluginScheduleItem(ctx context.Context, it *model.AppPluginScheduleItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRowContext(ctx, "SELECT status FROM app_plugin_schedule WHERE plugin_id=? AND `key`=?", it.PluginID, it.Key).Scan(&status)
	switch {
	case err == nil && status == model.AppPluginScheduleRunning:
		return nil
	case err == nil:
		if _, derr := tx.ExecContext(ctx, "DELETE FROM app_plugin_schedule WHERE plugin_id=? AND `key`=?", it.PluginID, it.Key); derr != nil {
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
		"INSERT INTO app_plugin_schedule (plugin_id, `key`, due_at, action_id, storage_id, paths_json, params_json, status, attempts, claimed_by, job_id, error, updated_at)"+
			` VALUES (?,?,?,?,?,?,?,?,0,'','','',CURRENT_TIMESTAMP)`,
		it.PluginID, it.Key, it.DueAt.UTC(), it.ActionID, it.StorageID, orJSON(it.PathsJSON, "[]"), orJSON(it.ParamsJSON, "{}"), status); err != nil {
		return err
	}
	return tx.Commit()
}

// ClaimAppPluginScheduleItem is the lease: one conditional UPDATE, true only
// for the process whose UPDATE actually moved the row out of `due`.
func (s *Store) ClaimAppPluginScheduleItem(ctx context.Context, pluginID int64, key, owner string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		"UPDATE app_plugin_schedule SET status=?, claimed_by=?, claimed_at=?, attempts=attempts+1, updated_at=CURRENT_TIMESTAMP"+
			" WHERE plugin_id=? AND `key`=? AND status=? AND due_at<=?",
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
			"UPDATE app_plugin_schedule SET status=?, due_at=?, claimed_by='', claimed_at=NULL, job_id=?, error=?, updated_at=CURRENT_TIMESTAMP"+
				" WHERE plugin_id=? AND `key`=?",
			model.AppPluginScheduleDue, rearmAt.UTC(), jobID, errMsg, pluginID, key)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		"UPDATE app_plugin_schedule SET status=?, job_id=?, error=?, updated_at=CURRENT_TIMESTAMP WHERE plugin_id=? AND `key`=?",
		status, jobID, errMsg, pluginID, key)
	return err
}

func (s *Store) DueAppPluginScheduleItems(ctx context.Context, now time.Time, limit int) ([]*model.AppPluginScheduleItem, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+appPluginScheduleCols+" FROM app_plugin_schedule WHERE status=? AND due_at<=? ORDER BY due_at ASC, plugin_id ASC, `key` ASC LIMIT ?",
		model.AppPluginScheduleDue, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAppPluginScheduleItems(rows)
}

// NextAppPluginScheduleDue answers with ORDER BY … LIMIT 1 rather than
// MIN(due_at): MIN over no rows is one NULL row, which scans into a
// time.Time as a type error instead of ErrNoRows, and "nothing is due" is
// the ordinary state of a quiet instance, not a failure to report.
func (s *Store) NextAppPluginScheduleDue(ctx context.Context) (*time.Time, error) {
	var t time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT due_at FROM app_plugin_schedule WHERE status=? ORDER BY due_at ASC LIMIT 1`, model.AppPluginScheduleDue).Scan(&t)
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
		`SELECT `+appPluginScheduleCols+" FROM app_plugin_schedule WHERE plugin_id=? ORDER BY due_at ASC, `key` ASC", pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAppPluginScheduleItems(rows)
}

func (s *Store) ReapAppPluginScheduleItems(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM app_plugin_schedule WHERE status IN (?,?,?) AND due_at < ?`,
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
