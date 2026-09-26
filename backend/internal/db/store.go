package db

import (
	"context"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// NodeAgg is a lightweight node row used for folder-size aggregation
// (internal/sync.RecomputeFolderSizes): just enough to walk the tree and sum
// descendant file sizes into each folder's cached size.
type NodeAgg struct {
	ID       int64
	ParentID *int64
	IsDir    bool
	Size     int64
	// Mtime is the node's backend_mtime (nullable). For folders it feeds the
	// "last activity" date = newest descendant mtime (see RecomputeFolderSizes).
	Mtime *time.Time
}

// Store is the interface implemented by every dialect-specific query
// adapter. Methods are intentionally tiny domain operations — handlers
// should never reach into *sql.DB directly.
//
// In a fully-generated setup this surface would be sqlc's Querier
// interface; here we hand-roll it so the skeleton compiles without a
// `sqlc generate` step.
type Store interface {
	// Lifecycle
	Ping(ctx context.Context) error
	Close() error

	// WithTx runs fn in ONE transaction: every statement a Store method runs
	// with the context fn is handed is part of it — through the wrappers
	// (quotastore, identitystore) too, since they pass the context on. It
	// commits when fn returns nil and rolls back otherwise. See tx.go for
	// what must NOT run inside it (anything reaching the database through
	// another handle, the job queue above all).
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// Storages
	CreateStorage(ctx context.Context, s *model.Storage) (*model.Storage, error)
	GetStorage(ctx context.Context, id int64) (*model.Storage, error)
	GetStorageByName(ctx context.Context, name string) (*model.Storage, error)
	// GetStorageByUID resolves a storage by the identifier it keeps for life.
	// The file protocols accept it in place of the name so a mount survives a
	// rename; see model.Storage.UID.
	GetStorageByUID(ctx context.Context, uid string) (*model.Storage, error)
	// ListStorages and ListEnabledStorages list in the admin's order: placed
	// storages by sort_order, then unplaced ones by id (model.Storage.SortOrder).
	ListStorages(ctx context.Context) ([]*model.Storage, error)
	ListEnabledStorages(ctx context.Context) ([]*model.Storage, error)
	// UpdateStorage writes every column the edit form owns — not sort_order.
	UpdateStorage(ctx context.Context, s *model.Storage) error
	UpdateStorageSyncCursor(ctx context.Context, id int64, at time.Time, token string) error
	DeleteStorage(ctx context.Context, id int64) error
	// SetStorageOrder places ordered[i] at position i+1 and clears (NULL) the
	// position of every id in cleared, in one transaction (issue #57;
	// StorageOrderSQL). It does not check the ids: the caller decides which
	// storages it may order.
	SetStorageOrder(ctx context.Context, ordered []int64, cleared []int64) error

	// Nodes
	CreateNode(ctx context.Context, n *model.Node) (*model.Node, error)
	GetNode(ctx context.Context, id int64) (*model.Node, error)
	GetNodeByPath(ctx context.Context, storageID int64, pathHash string) (*model.Node, error)
	// GetNodeByPathIncludingDeleted answers "is there ANY row at this path",
	// live or trashed. The sync worker asks it so that an object appearing
	// where a trashed row still sits is reported rather than silently
	// conflated with it.
	//
	// Since migration 00032 the unique index over (storage_id, path_hash) is
	// partial on live rows, so several trashed rows may share a path with at
	// most one live one. Implementations MUST return the live row when there
	// is one, and otherwise the most recently trashed row -- an arbitrary
	// pick would make the sync's decision depend on row order.
	GetNodeByPathIncludingDeleted(ctx context.Context, storageID int64, pathHash string) (*model.Node, error)
	// ListLiveNodesInTrash returns every LIVE node of a storage whose path is
	// the trash bucket or sits inside it. Nothing should ever be live in
	// there: it is either a trashed item an older sync worker un-deleted, or
	// a row that same worker minted for the trash's own bytes. The sync pass
	// uses it to clear both up. trashPrefix is given without a leading slash
	// (`trash.Prefix`); implementations match the stored path with and
	// without one, because drivers differ on that.
	ListLiveNodesInTrash(ctx context.Context, storageID int64, trashPrefix string) ([]*model.Node, error)
	// ListNodesUnder returns the row at dir and every row below it, in both
	// path spellings drivers produce ("/a/b" and "a/b"); includeDeleted adds
	// soft-deleted rows. The match is EXACT: names compare byte for byte, and
	// the prefix bound is counted in characters, so a folder whose name is not
	// ASCII matches as reliably as one that is. The storage root ("" or "/")
	// is never a subtree and returns nothing.
	ListNodesUnder(ctx context.Context, storageID int64, dir string, includeDeleted bool) ([]*model.Node, error)
	ListNodesByParent(ctx context.Context, storageID int64, parentID *int64) ([]*model.Node, error)
	// AggNodes returns a lightweight {id, parent_id, is_dir, size} row for every
	// live node of a storage — the input to folder-size aggregation.
	AggNodes(ctx context.Context, storageID int64) ([]NodeAgg, error)
	// SetNodeSize overwrites a node's cached size. Used to store recursive folder
	// totals (see internal/sync.RecomputeFolderSizes).
	SetNodeSize(ctx context.Context, id int64, size int64) error
	// SetNodeMtime overwrites a node's cached backend_mtime. Used to give folders
	// a "last activity" date (newest descendant mtime) so the explorer can show a
	// date for directories whose driver reports none (e.g. synthetic S3 prefixes).
	SetNodeMtime(ctx context.Context, id int64, mtime *time.Time) error
	UpdateNodeMeta(ctx context.Context, id int64, size int64, mime, etag string, mtime time.Time) error
	TouchNodeSeen(ctx context.Context, id int64) error
	SoftDeleteNode(ctx context.Context, id int64) error
	// SoftDeleteAndRetag flips deleted_at + rewrites path/path_hash to a
	// trash key in one shot, while saving the original path in
	// storage_key. Used by vfDelete after the on-disk rename so the
	// original-path slot is freed (UNIQUE(storage_id, path_hash)).
	SoftDeleteAndRetag(ctx context.Context, id int64, trashPath, trashHash, origPath string) error
	HardDeleteNode(ctx context.Context, id int64) error
	MoveNode(ctx context.Context, id int64, parentID *int64, name, path, pathHash string) error
	ListStaleNodes(ctx context.Context, storageID int64, before time.Time) ([]*model.Node, error)
	// ListStaleNodesUnder is ListStaleNodes bounded to the rows strictly BELOW
	// dir (the folder's own row excluded), matched exactly as ListNodesUnder
	// matches — the tombstone candidates of a folder rescan.
	ListStaleNodesUnder(ctx context.Context, storageID int64, dir string, before time.Time) ([]*model.Node, error)
	// CountLiveNodesUnder counts the live rows strictly below dir — the
	// baseline a folder rescan's 70% guard compares what it saw against.
	CountLiveNodesUnder(ctx context.Context, storageID int64, dir string) (int64, error)
	CountNodesByStorage(ctx context.Context, storageID int64) (int64, error)

	// Replication targets — separate entity. Storages.replica_target_id
	// is the FK linking a primary to one of these.
	ListReplicationTargets(ctx context.Context) ([]*model.ReplicationTarget, error)
	GetReplicationTarget(ctx context.Context, id int64) (*model.ReplicationTarget, error)
	CreateReplicationTarget(ctx context.Context, rt *model.ReplicationTarget) (*model.ReplicationTarget, error)
	UpdateReplicationTarget(ctx context.Context, rt *model.ReplicationTarget) error
	DeleteReplicationTarget(ctx context.Context, id int64) error
	// StorageStats returns (file_count, total_size_bytes) for a storage,
	// excluding directories and soft-deleted nodes. Used by the admin
	// storages list page so each row can show "N files, 1.2 GB" without
	// the SPA looping every node row.
	StorageStats(ctx context.Context, storageID int64) (fileCount int64, totalBytes int64, err error)

	// ── Lazy catalogue: per-folder state (migration 00059) ──
	//
	// What a lazily catalogued storage knows about each of its folders
	// (docs/LAZY-CATALOGUE.md). Paths are canonical ("/" for the root,
	// "/a/b" below it) and keyed by pathkey.Hash like nodes.
	//
	// GetCatalogueFolder returns (nil, nil) when the folder has no row.
	GetCatalogueFolder(ctx context.Context, storageID int64, pathHash string) (*model.CatalogueFolder, error)
	// DiscoverCatalogueFolders records folders seen in a parent's listing as
	// uncatalogued; a folder that already has a row keeps it untouched.
	DiscoverCatalogueFolders(ctx context.Context, storageID int64, folders []model.CatalogueFolder) error
	// RecordCatalogueFolder writes every column of one folder's row.
	RecordCatalogueFolder(ctx context.Context, f *model.CatalogueFolder) error
	// SetCatalogueFolderWatch records a watch placed (watchedAt set: state
	// watched) or removed (nil: state catalogued) on a folder that has been
	// catalogued. An uncatalogued or missing row is left alone.
	SetCatalogueFolderWatch(ctx context.Context, storageID int64, pathHash string, watchedAt *time.Time, reconcileOnOpen bool) error
	// TouchCatalogueFolderVisit stamps visited_at on an existing row.
	TouchCatalogueFolderVisit(ctx context.Context, storageID int64, pathHash string, at time.Time) error
	// ListCatalogueFolders returns the rows a filter selects: by default the
	// shallowest first; with ReconciledBefore, catalogued (unwatched) rows
	// oldest first.
	ListCatalogueFolders(ctx context.Context, storageID int64, f model.CatalogueFolderFilter) ([]*model.CatalogueFolder, error)
	// CountCatalogueFolders counts a storage's rows per state.
	CountCatalogueFolders(ctx context.Context, storageID int64) (model.CatalogueCounts, error)
	// ResetCatalogueWatches demotes every watched row to catalogued with
	// reconcile_on_open set — the watches died with the previous process.
	ResetCatalogueWatches(ctx context.Context, storageID int64) (int64, error)
	// DeleteCatalogueFoldersUnder drops the folder's row and every row below
	// it (the folder is gone from the storage).
	DeleteCatalogueFoldersUnder(ctx context.Context, storageID int64, dir string) error
	// HasUncataloguedUnder reports whether any folder strictly below dir is
	// still uncatalogued ("/" asks about the whole storage).
	HasUncataloguedUnder(ctx context.Context, storageID int64, dir string) (bool, error)
	// SearchNodes returns up to `limit` live nodes of a storage whose NAME
	// answers m — every word in it, compared through internal/namefold (see
	// model.NameMatch) — ranked by m.Prefer before the LIMIT. It is the
	// search an install without the index runs, and every caller reaches it
	// through search.Fallback.Candidates. No words, no rows.
	//
	// ⚠ EVERY word is a condition, never one of them: the fallback used to
	// send only the longest word and check the rest in Go over the first rows
	// by name, and a word most files shared pushed the file past the LIMIT
	// (GitHub PR #46, measured on a 169k-file catalogue: the file at row
	// 33 623 of a 1 000-row window).
	//
	// ⚠ Rank BEFORE the limit, never after: a LIMIT on `ORDER BY name` keeps
	// the alphabetically first rows, and a search for `report` among a
	// thousand `a-report-…` files lost `report.txt` itself.
	//
	// ⚠ The stored name goes through the same normaliser as the words, spelt
	// in each dialect: fx_match/fx_rank (Go, registered on SQLite),
	// normalize+lower on PostgreSQL, and on MySQL, which can compose nothing,
	// its accent- and case-insensitive collation over both the composed and
	// the decomposed form of each word. TestSearchNodesOnEveryEngine holds
	// the three to one answer.
	SearchNodes(ctx context.Context, storageID int64, m model.NameMatch, limit int) ([]*model.Node, error)

	// Users
	CreateUser(ctx context.Context, email, passwordHash, role, locale, tz string) (*model.User, error)
	GetUser(ctx context.Context, id int64) (*model.User, error)
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	// Dual-side login (migration 00025): an account answers to its e-mail or
	// its username. Reach these through identity.Resolve, not directly — the
	// rule for deciding which one an identifier is belongs in one place.
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
	SetUserUsername(ctx context.Context, id int64, username string) error
	// Multi-tenancy (docs/MULTI-TENANCY.md): look a user up within one provider
	// (tenant); re-home a user to a provider + record its OIDC subject (JIT).
	GetUserByProviderEmail(ctx context.Context, providerID int64, email string) (*model.User, error)
	SetUserProvider(ctx context.Context, userID, providerID int64, oidcSubject string) error
	ListUsersByProvider(ctx context.Context, providerID int64) ([]*model.User, error)
	ListUsers(ctx context.Context) ([]*model.User, error)
	CountUsers(ctx context.Context) (int64, error)
	UpdateUserPassword(ctx context.Context, id int64, hash string) error
	UpdateUserEmail(ctx context.Context, id int64, email string) error
	UpdateUserDisplayName(ctx context.Context, id int64, displayName string) error
	// UpdateUserAvatar sets (or clears, with "") the profile picture — a small
	// data: URI or an http(s)/site-relative URL. Validation lives at the API
	// boundary; the store only persists what it is handed.
	UpdateUserAvatar(ctx context.Context, id int64, avatarURL string) error
	UpdateUserLocale(ctx context.Context, id int64, locale, tz string) error
	UpdateUserRole(ctx context.Context, id int64, role string) error
	TouchLastLogin(ctx context.Context, id int64) error
	DeleteUser(ctx context.Context, id int64) error

	// TOTP / 2FA
	SetTotpPendingSecret(ctx context.Context, id int64, secret string, recoveryCodes []string) error
	ActivateTotp(ctx context.Context, id int64) error
	ClearTotp(ctx context.Context, id int64) error

	// Sessions
	CreateSession(ctx context.Context, userID int64, token string, expiresAt time.Time, ip, ua string) (*model.Session, error)
	GetSessionByToken(ctx context.Context, token string) (*model.Session, error)
	DeleteSession(ctx context.Context, token string) error
	// SetSessionIDToken / GetSessionIDToken keep the IdP's id_token beside an
	// OIDC session, so sign-out can end the IdP's session too (id_token_hint).
	// Get returns "" — never an error — for a session that has none or does
	// not exist: sign-out falls back to local-only and must not fail on it.
	SetSessionIDToken(ctx context.Context, token, idToken string) error
	GetSessionIDToken(ctx context.Context, token string) (string, error)
	DeleteSessionsForUser(ctx context.Context, userID int64, exceptToken string) error
	CountActiveSessions(ctx context.Context) (int64, error)
	DeleteExpiredSessions(ctx context.Context) error

	// API tokens — long-lived bearer credentials for AI / MCP / FilexClient.
	CreateAPIToken(ctx context.Context, t *model.APIToken) (*model.APIToken, error)
	GetAPITokenByHash(ctx context.Context, tokenHash string) (*model.APIToken, error)
	GetAPITokenByID(ctx context.Context, id int64) (*model.APIToken, error)
	ListAPITokens(ctx context.Context) ([]*model.APIToken, error)
	ListAPITokensByUser(ctx context.Context, userID int64) ([]*model.APIToken, error)
	TouchAPIToken(ctx context.Context, id int64) error
	// UpdateAPITokenMeta edits a token's display metadata (label / username
	// allow-list) and its kind ("user" / "app", migration 00030); nil leaves a
	// field unchanged. The credential itself is immutable.
	//
	// Kind is editable because migration 00030 defaults every pre-existing row
	// to "app": a personal token minted before the split needs one admin edit
	// to become a "user" token again.
	UpdateAPITokenMeta(ctx context.Context, id int64, label, usernames, kind *string) error
	DeleteAPIToken(ctx context.Context, id int64) error

	// S3 access keys (migration 00026) — the credential an S3 client signs
	// with. Unlike an API token these hold a RECOVERABLE secret, because SigV4
	// verifies by recomputing an HMAC chain rather than by comparing a hash;
	// internal/secretbox is what keeps that out of a database dump. A key
	// minted from a token INHERITS that token's permissions and never widens
	// them (see model.S3AccessKey).
	CreateS3AccessKey(ctx context.Context, k *model.S3AccessKey) (*model.S3AccessKey, error)
	GetS3AccessKey(ctx context.Context, accessKeyID string) (*model.S3AccessKey, error)
	GetS3AccessKeyByID(ctx context.Context, id int64) (*model.S3AccessKey, error)
	ListS3AccessKeys(ctx context.Context, userID int64) ([]*model.S3AccessKey, error)
	TouchS3AccessKey(ctx context.Context, id int64) error
	SetS3AccessKeyDisabled(ctx context.Context, id int64, disabled bool) error
	DeleteS3AccessKey(ctx context.Context, id, userID int64) error

	// SSH public keys (migration 00027) — how an account reaches the SFTP
	// endpoint without sending a password. The signature is verified by
	// x/crypto/ssh before any of these are called; what the lookup decides is
	// which ACCOUNT the key belongs to, which is why GetSSHPublicKey takes a
	// fingerprint and returns exactly one row.
	CreateSSHPublicKey(ctx context.Context, k *model.SSHPublicKey) (*model.SSHPublicKey, error)
	GetSSHPublicKey(ctx context.Context, fingerprint string) (*model.SSHPublicKey, error)
	GetSSHPublicKeyByID(ctx context.Context, id int64) (*model.SSHPublicKey, error)
	ListSSHPublicKeys(ctx context.Context, userID int64) ([]*model.SSHPublicKey, error)
	TouchSSHPublicKey(ctx context.Context, id int64) error
	SetSSHPublicKeyDisabled(ctx context.Context, id int64, disabled bool) error
	DeleteSSHPublicKey(ctx context.Context, id, userID int64) error

	// NFS exports (migration 00028) — an NFSv3 mount bound to one account by a
	// high-entropy export PATH, because NFSv3 has no authentication filex can
	// use. GetNFSExport takes the sha256 of that path: the server only ever
	// compares, so the plaintext is never stored.
	CreateNFSExport(ctx context.Context, e *model.NFSExport) (*model.NFSExport, error)
	GetNFSExport(ctx context.Context, tokenHash string) (*model.NFSExport, error)
	GetNFSExportByID(ctx context.Context, id int64) (*model.NFSExport, error)
	ListNFSExports(ctx context.Context, userID int64) ([]*model.NFSExport, error)
	TouchNFSExport(ctx context.Context, id int64) error
	SetNFSExportDisabled(ctx context.Context, id int64, disabled bool) error
	DeleteNFSExport(ctx context.Context, id, userID int64) error

	// Storage plugins (migration 00029) — the admin's registration of an
	// out-of-process storage driver; see internal/plugin. Runtime state is
	// NOT here (the manager re-derives it by starting the plugin).
	CreatePlugin(ctx context.Context, p *model.Plugin) (*model.Plugin, error)
	GetPlugin(ctx context.Context, id int64) (*model.Plugin, error)
	GetPluginByName(ctx context.Context, name string) (*model.Plugin, error)
	ListPlugins(ctx context.Context) ([]*model.Plugin, error)
	UpdatePlugin(ctx context.Context, p *model.Plugin) error
	DeletePlugin(ctx context.Context, id int64) error

	// App plugins (migration 00042) — in-process WebAssembly plugins; see
	// internal/wasmplugin. The row is the admin's intent + the approved grant;
	// runtime state is derived by loading the module.
	CreateAppPlugin(ctx context.Context, p *model.AppPlugin) (*model.AppPlugin, error)
	GetAppPlugin(ctx context.Context, id int64) (*model.AppPlugin, error)
	GetAppPluginByName(ctx context.Context, name string) (*model.AppPlugin, error)
	ListAppPlugins(ctx context.Context) ([]*model.AppPlugin, error)
	UpdateAppPlugin(ctx context.Context, p *model.AppPlugin) error
	// DeleteAppPlugin removes the row and everything keyed on it (settings,
	// overrides, state, jobs).
	DeleteAppPlugin(ctx context.Context, id int64) error
	GetAppPluginSettings(ctx context.Context, pluginID int64) (map[string]string, error)
	// PutAppPluginSettings replaces the whole set.
	PutAppPluginSettings(ctx context.Context, pluginID int64, values map[string]string) error
	ListAppPluginOverrides(ctx context.Context, pluginID int64) ([]*model.AppPluginOverride, error)
	// PutAppPluginOverrides replaces the whole set.
	PutAppPluginOverrides(ctx context.Context, pluginID int64, rows []*model.AppPluginOverride) error
	GetAppPluginState(ctx context.Context, pluginID, storageID int64, pathHash, key string) (string, bool, error)
	// SetAppPluginState records one key. rel is the file's storage-relative
	// path, stored beside the hash so a listing can name the file.
	SetAppPluginState(ctx context.Context, pluginID, storageID int64, pathHash, rel, key, value string) error
	DeleteAppPluginState(ctx context.Context, pluginID, storageID int64, pathHash, key string) error
	CreateAppPluginJob(ctx context.Context, j *model.AppPluginJob) error
	GetAppPluginJob(ctx context.Context, id string) (*model.AppPluginJob, error)
	// ListAppPluginJobsByOp returns the jobs behind the given ops rows, keyed
	// by op id — what the ops tray uses to decorate plugin rows.
	ListAppPluginJobsByOp(ctx context.Context, opIDs []int64) (map[int64]*model.AppPluginJob, error)
	UpdateAppPluginJob(ctx context.Context, j *model.AppPluginJob) error
	// SetAppPluginJobOp writes ONLY the ops-row id onto a job.
	//
	// The whole-row update cannot be used for this: submitting the job wakes
	// the worker immediately, a short action can finish before the handler
	// gets its answer back, and writing the handler's copy of the row then
	// erases the status, the message and the outputs the worker just wrote.
	// The symptom is silent and awful — the ops row says "ok" and the
	// message the plugin produced (a public link and its PIN, say) is gone.
	SetAppPluginJobOp(ctx context.Context, jobID string, opID int64) error
	CreateAppPluginSigningKey(ctx context.Context, k *model.AppPluginSigningKey) error
	GetAppPluginSigningKey(ctx context.Context, id string) (*model.AppPluginSigningKey, error)
	// GetAppPluginSigningCA returns the tenant's live CA row, or nil.
	GetAppPluginSigningCA(ctx context.Context, tenantID int64) (*model.AppPluginSigningKey, error)
	// GetAppPluginSealKey returns the live platform seal of one app for one
	// tenant (purpose "platform", not destroyed), or nil.
	GetAppPluginSealKey(ctx context.Context, tenantID, pluginID int64) (*model.AppPluginSigningKey, error)
	RetireAppPluginSigningCA(ctx context.Context, tenantID int64) error
	// ListAppPluginSigningCAs returns every CA a tenant has ever signed
	// with, retired ones included, newest first. Nothing deletes these: a
	// signature made years ago is only verifiable while the certificate
	// that issued it is still here.
	ListAppPluginSigningCAs(ctx context.Context, tenantID int64) ([]*model.AppPluginSigningKey, error)
	// DestroyAppPluginSigningKey empties the sealed key and stamps destroyed_at.
	DestroyAppPluginSigningKey(ctx context.Context, id string) error
	// ListAppPluginStateFiles returns the files this plugin keeps the given
	// state key on — the key joined back to the node rows, so a file that
	// was deleted is simply not in the answer. An empty key lists every key.
	ListAppPluginStateFiles(ctx context.Context, pluginID int64, key string, limit int) ([]*model.AppPluginStateFile, error)
	// ListAppPluginStateKeys returns, per path hash, the "<plugin name>:<key>"
	// pairs kept on those files — what a listing shows so the menu can offer
	// state-aware actions (applies.state). Values are never returned.
	ListAppPluginStateKeys(ctx context.Context, storageID int64, pathHashes []string) (map[string][]string, error)
	// PutAppPluginLock inserts or replaces the lock on (storage, path).
	PutAppPluginLock(ctx context.Context, l *model.AppPluginLock) error
	GetAppPluginLock(ctx context.Context, storageID int64, pathHash string) (*model.AppPluginLock, error)
	// ListAppPluginLocks returns every lock on a storage (0 = all storages),
	// expired ones included; callers filter with Live.
	ListAppPluginLocks(ctx context.Context, storageID int64) ([]*model.AppPluginLock, error)
	DeleteAppPluginLock(ctx context.Context, storageID int64, pathHash string) error
	// DeleteExpiredAppPluginLocks drops locks whose until passed before now.
	DeleteExpiredAppPluginLocks(ctx context.Context, now time.Time) (int64, error)

	// App-plugin schedule (migration 00050) — the hourly wake-up of an app
	// that holds the `schedule` permission, and the work that wake-up asked
	// for. One row per (plugin, key); the empty key is the wake-up itself.
	//
	// PutAppPluginScheduleItem inserts the item or moves the existing one.
	// A row another process is RUNNING is left alone and no error is
	// returned: the running process owns it and will finish it.
	PutAppPluginScheduleItem(ctx context.Context, it *model.AppPluginScheduleItem) error
	// ClaimAppPluginScheduleItem takes the lease on a due row: one
	// conditional UPDATE from due to running, true only for the process
	// whose UPDATE matched. This is what stops two filex processes on one
	// database from running the same item twice.
	ClaimAppPluginScheduleItem(ctx context.Context, pluginID int64, key, owner string, now time.Time) (bool, error)
	// FinishAppPluginScheduleItem ends a claimed row. A non-nil rearmAt puts
	// it back to `due` at that time instead (the wake-up re-arming itself
	// for the next hour); status is then ignored and errMsg becomes the
	// note the row carries until the next wake-up.
	FinishAppPluginScheduleItem(ctx context.Context, pluginID int64, key, status, jobID, errMsg string, rearmAt *time.Time) error
	// DueAppPluginScheduleItems returns rows that are `due` at or before
	// now, oldest first, capped at limit.
	DueAppPluginScheduleItems(ctx context.Context, now time.Time, limit int) ([]*model.AppPluginScheduleItem, error)
	// NextAppPluginScheduleDue is when the earliest `due` row comes due, so
	// the scheduler can sleep exactly that long instead of polling.
	NextAppPluginScheduleDue(ctx context.Context) (*time.Time, error)
	// ListAppPluginScheduleItems returns every row of one app, soonest first.
	ListAppPluginScheduleItems(ctx context.Context, pluginID int64) ([]*model.AppPluginScheduleItem, error)
	// ReapAppPluginScheduleItems drops finished rows older than before.
	ReapAppPluginScheduleItems(ctx context.Context, before time.Time) (int64, error)

	// File grants — per-user/per-folder ACL (RBAC feature, migration 00012).
	ListFileGrantsByStorageUser(ctx context.Context, storageID, userID int64) ([]*model.FileGrant, error)
	ListFileGrantsByStorage(ctx context.Context, storageID int64) ([]*model.FileGrant, error)
	ListAllFileGrants(ctx context.Context) ([]*model.FileGrant, error)
	GetFileGrant(ctx context.Context, id int64) (*model.FileGrant, error)
	CreateFileGrant(ctx context.Context, g *model.FileGrant) (*model.FileGrant, error)
	UpdateFileGrantLevel(ctx context.Context, id int64, level string) error
	DeleteFileGrant(ctx context.Context, id int64) error

	// Shares
	CreateShare(ctx context.Context, share *model.Share) (*model.Share, error)
	GetShareByID(ctx context.Context, id int64) (*model.Share, error)
	GetShareByToken(ctx context.Context, token string) (*model.Share, error)
	ListSharesByNode(ctx context.Context, nodeID int64) ([]*model.Share, error)
	ListAllShares(ctx context.Context, creatorID *int64, activeOnly bool, limit, offset int) ([]*ShareWithMeta, int64, error)
	RevokeShare(ctx context.Context, id int64) error
	IncrementShareDownload(ctx context.Context, id int64) error
	// IncrementShareVisit counts one opening of an app page (00052) — the
	// counter an app page's visit ceiling is measured against. Page views
	// are not downloads (the owner's ruling, 2026-09-21).
	IncrementShareVisit(ctx context.Context, id int64) error
	// ReserveShareDownload claims ONE download against the link's cap and
	// reports whether it got one. This is the cap's only real enforcement
	// point: a check that reads the counter and a serve that bumps it
	// afterwards are two separate steps, and every request that starts inside
	// that gap passes the check (measured on fm.example.com: a 1-download link
	// handed three full files to three overlapping clients). Claim, then serve.
	ReserveShareDownload(ctx context.Context, id int64) (bool, error)
	// ReleaseShareDownload hands a reserved slot back. Used only when the serve
	// fails before a single byte reaches the client, so a storage error does
	// not silently eat one of the downloads the owner granted.
	ReleaseShareDownload(ctx context.Context, id int64) error
	IncrementShareUpload(ctx context.Context, id int64, n int) error
	DeleteShare(ctx context.Context, id int64) error
	DeleteExpiredShares(ctx context.Context) error
	// ── App-plugin page shares (00046) ──
	//
	// An app plugin's public page is a share carrying plugin_id/page_id plus
	// its own two documents. These are the only writers of those columns after
	// CreateShare, so the ordinary share paths never have to know about them.

	// UpdateShareAppState replaces the plugin's durable record for one link
	// (the share_state host function). Bounded by the caller at 64 KiB.
	UpdateShareAppState(ctx context.Context, id int64, stateJSON string) error
	// UpdateSharePinLock writes the PIN strike counter and the lock deadline.
	// Separate from every other update so a failed guess cannot rewrite the
	// link's expiry, caps or exposed files by riding along in a whole-row save.
	UpdateSharePinLock(ctx context.Context, id int64, fails int, until *time.Time) error
	// ListAppPluginShares returns the links an installed app opened, newest
	// first. pluginID 0 means every app (never the ordinary shares);
	// activeOnly drops the expired and revoked ones.
	ListAppPluginShares(ctx context.Context, pluginID int64, activeOnly bool, limit, offset int) ([]*ShareWithMeta, int64, error)

	// Chunked uploads
	CreateChunkedUpload(ctx context.Context, u *model.ChunkedUpload) error
	GetChunkedUpload(ctx context.Context, id string) (*model.ChunkedUpload, error)
	UpdateChunkedUploadParts(ctx context.Context, id string, parts []model.UploadPart) error
	DeleteChunkedUpload(ctx context.Context, id string) error
	DeleteExpiredChunkedUploads(ctx context.Context) error

	// Staged uploads — the driver-agnostic resumable path (docs/UPLOADS.md).
	// A separate table from chunked_uploads on purpose: see the comment in
	// db/migrations/sqlite/00024_staged_uploads.sql.
	CreateStagedUpload(ctx context.Context, u *model.StagedUpload) error
	GetStagedUpload(ctx context.Context, id string) (*model.StagedUpload, error)
	// GetStagedUploadByNode is the reverse index chunk 5 (read-during-transfer)
	// uses to find the staging directory holding a node's bytes.
	GetStagedUploadByNode(ctx context.Context, nodeID int64) (*model.StagedUpload, error)
	UpdateStagedUploadProgress(ctx context.Context, id string, receivedBytes int64) error
	UpdateStagedUploadState(ctx context.Context, id, state, errMsg string) error
	AttachStagedUploadTarget(ctx context.Context, id string, nodeID, opID int64) error
	DeleteStagedUpload(ctx context.Context, id string) error
	ListStagedUploads(ctx context.Context, state string, limit int) ([]*model.StagedUpload, error)
	// ListIdleStagedUploads returns rows whose last activity is older than
	// `before` — the staging sweeper's input.
	ListIdleStagedUploads(ctx context.Context, before time.Time, limit int) ([]*model.StagedUpload, error)
	// SumOpenStagedUploadBytes is the quota RESERVATION: the declared size of
	// every not-yet-committed upload the user owns. Derived rather than stored,
	// so it can never drift from the rows it describes and a row leaving the
	// open set releases its reservation by construction.
	SumOpenStagedUploadBytes(ctx context.Context, userID int64) (int64, error)

	// SetNodeTransferState sets nodes.transfer_state: "staged" while the bytes
	// are in filex's staging area, "stored" once they are on the driver, and
	// "failed" when the transfer to the driver did not succeed.
	SetNodeTransferState(ctx context.Context, nodeID int64, state string) error
	// ListUnstoredNodes pages through every LIVE row whose transfer_state is
	// "staged" or "failed" — bytes a staged upload committed that were never
	// confirmed on the storage — in id order, after afterID. The staged-upload
	// boot pass settles the ones whose bytes did land.
	ListUnstoredNodes(ctx context.Context, afterID int64, limit int) ([]*model.Node, error)

	// Sync runs / conflicts
	CreateSyncRun(ctx context.Context, storageID int64, cursorBefore string) (*model.SyncRun, error)
	FinishSyncRun(ctx context.Context, id int64, cursorAfter string, seen, added, updated, deleted int, status, errMsg string) error
	GetSyncRun(ctx context.Context, id int64) (*model.SyncRun, error)
	GetLastSyncRun(ctx context.Context, storageID int64) (*model.SyncRun, error)
	// GetLastSyncRunByStatus is the most recent FINISHED run of a storage with
	// the given status (sql.ErrNoRows when there is none) — the tombstone
	// guard's baseline is the last run that finished "ok", never one that
	// was cut short with whatever it had counted by then.
	GetLastSyncRunByStatus(ctx context.Context, storageID int64, status string) (*model.SyncRun, error)
	// AbortUnfinishedSyncRuns closes every run with no finished_at as
	// "aborted", with errMsg as its error, and reports how many it closed.
	// Called once when the sync worker starts, before any run of its own: a
	// row still open then belongs to a process that is gone.
	AbortUnfinishedSyncRuns(ctx context.Context, errMsg string) (int64, error)
	ListSyncRuns(ctx context.Context, storageID int64, limit int) ([]*model.SyncRun, error)
	ListSyncRunsAcrossAll(ctx context.Context, storageID int64, status string, limit, offset int) ([]*model.SyncRun, int64, error)
	CreateSyncConflict(ctx context.Context, c *model.SyncConflict) error
	ListUnresolvedConflicts(ctx context.Context) ([]*model.SyncConflict, error)
	ListConflictsByStorage(ctx context.Context, storageID int64, limit int) ([]*model.SyncConflict, error)
	CountSyncConflictsByRun(ctx context.Context, runID int64) (int64, error)
	ResolveConflict(ctx context.Context, id int64, resolution string) error
	CountQueueDepth(ctx context.Context) (int64, error)

	// Audit
	InsertAuditEntry(ctx context.Context, e *model.AuditEntry) error
	ListAuditRecent(ctx context.Context, limit int) ([]*model.AuditEntry, error)
	ListAuditFiltered(ctx context.Context, userID *int64, action string, from, to *time.Time, limit, offset int) ([]*AuditEntryWithUser, int64, error)

	// Settings
	GetSetting(ctx context.Context, key string) (string, error)
	UpsertSetting(ctx context.Context, key, value string) error
	ListSettings(ctx context.Context) (map[string]string, error)

	// External services
	UpsertExternalService(ctx context.Context, name string, enabled bool, url, secretEnc, optionsJSON string, lastCheck time.Time, lastState string) error
	GetExternalService(ctx context.Context, name string) (*ExternalService, error)
	ListExternalServices(ctx context.Context) ([]*ExternalService, error)
	UpdateExternalServiceState(ctx context.Context, name string, lastCheck time.Time, state string) error

	// Duplicate report — every live file node whose (size, non-empty etag)
	// pair occurs more than once, minSize filtering applied in SQL. Rows come
	// back ordered (size DESC, etag, id) so the handler can group them
	// contiguously. Powers GET /api/admin/duplicates (v0.2 "Bul").
	ListDuplicateNodes(ctx context.Context, minSize int64) ([]DuplicateNode, error)

	// Cross-storage analytics for the dashboard
	SumNodesBytesByStorage(ctx context.Context, storageID int64) (int64, error)
	CountNodesAddedSince(ctx context.Context, storageID int64, since time.Time) (int64, error)
	CountNodesDeletedSince(ctx context.Context, storageID int64, since time.Time) (int64, error)
	CountTotalShares(ctx context.Context) (int64, error)

	// Thumbnails
	GetThumbnail(ctx context.Context, nodeID int64) (*model.Thumbnail, error)
	UpsertThumbnail(ctx context.Context, t *model.Thumbnail) error
	SetThumbnailState(ctx context.Context, nodeID int64, state, errMsg string) error
	// DeleteThumbnail drops the row for one node. The cached JPEG on disk is
	// the pipeline's business (internal/thumb); this is only the catalogue
	// half, and it exists so a purge does not have to wait for the FK cascade
	// to be the only thing that ever removed it.
	DeleteThumbnail(ctx context.Context, nodeID int64) error
	// ExistingNodeIDs reports which of the given ids still have a `nodes` row —
	// TRASHED ROWS INCLUDED, because a trashed file is restorable and must keep
	// its thumbnail. It is the safety interlock of the thumbnail-cache sweeper:
	// a cached file is deleted only when this positively says its node is gone.
	ExistingNodeIDs(ctx context.Context, ids []int64) (map[int64]bool, error)

	// Node versions
	CreateNodeVersion(ctx context.Context, v *model.NodeVersion) (*model.NodeVersion, error)
	ListNodeVersions(ctx context.Context, nodeID int64) ([]*model.NodeVersion, error)
	GetNodeVersion(ctx context.Context, id int64) (*model.NodeVersion, error)
	NextNodeVersionNumber(ctx context.Context, nodeID int64) (int, error)
	DeleteNodeVersion(ctx context.Context, id int64) error
	DeleteOldNodeVersions(ctx context.Context, nodeID int64, keep int) ([]*model.NodeVersion, error)
	// ListNodeIDsWithVersions returns the distinct node ids that have at
	// least one node_versions row — the work list for the daily version
	// retention job (v0.4 "Koru").
	ListNodeIDsWithVersions(ctx context.Context) ([]int64, error)

	// Sync conflicts (admin views)
	ListSyncConflictsByRun(ctx context.Context, runID int64) ([]*model.SyncConflict, error)
	ListSyncConflictsByStorage(ctx context.Context, storageID int64, limit int) ([]*model.SyncConflict, error)

	// Search rebuild
	AllNodesForIndex(ctx context.Context) ([]*model.Node, error)

	// Quota
	GetUserUsage(ctx context.Context, userID int64) (used, limit int64, err error)
	IncrementUserUsage(ctx context.Context, userID int64, delta int64) error
	SetUserQuota(ctx context.Context, userID int64, bytes int64) error
	// SetUserEnabled flips the account on/off (migration 00022). A disabled
	// user cannot start a session; nothing they own is touched.
	SetUserEnabled(ctx context.Context, userID int64, enabled bool) error
	RecomputeUserUsage(ctx context.Context, userID int64) (int64, error)

	// Node owner / attribution (migration 00004 + 00038)
	//
	// owner_id is who PUT THE THING HERE; last_actor_id is who touched it
	// last; external_upload says it arrived through an anonymous drop link.
	// NULL means SYSTEM in both id columns — see model.Node.
	//
	// A CREATE does not use these: internal/quotastore stamps the model and
	// the INSERT carries the values, so a bulk write costs no extra queries.
	// They exist for the mutations that change attribution on a row that
	// already exists (overwrite, move, restore).
	SetNodeOwner(ctx context.Context, nodeID int64, ownerID *int64) error
	GetNodeOwner(ctx context.Context, nodeID int64) (*int64, error)
	SetNodeActor(ctx context.Context, nodeID int64, actorID *int64) error
	SetNodeExternalUpload(ctx context.Context, nodeID int64, external bool) error
	// SetNodeDeletedBy names who put a trashed row in the trash: the row and
	// every trashed row under its path that names nobody yet (a folder's
	// contents, trashed with it). A live row is left alone. Every soft delete
	// clears the name first; internal/quotastore writes it after the delete,
	// from the acting identity.
	SetNodeDeletedBy(ctx context.Context, nodeID int64, by *int64) error
	// GetUserDisplayNames resolves a batch of user ids to the label an Owner
	// column shows. One query for a whole listing page, names only — never
	// full user rows. See the sqlite driver for why.
	GetUserDisplayNames(ctx context.Context, ids []int64) (map[int64]string, error)

	// Trash retention
	//
	// ListTrashedExpired returns up to `limit` soft-deleted nodes whose
	// deleted_at is older than `before`, in id order, strictly after `afterID`
	// (0 starts at the first), narrowed to storageIDs: nil means every
	// storage, and an EMPTY, non-nil slice matches nothing (a scope that
	// reaches no storage must not read as "no restriction"). A sweep passes
	// the last id it saw back in, so every row is met once per run whatever
	// the caller did with it.
	//
	// ⚠ The narrowing is in the SQL on purpose. The purge used to read every
	// storage's rows and skip the foreign ones in Go; a skipped row is never
	// removed, so behind a full batch of somebody else's rows it re-read the
	// same batch forever.
	//
	// ⚠ The cursor is the id, never deleted_at. SQLite keeps CURRENT_TIMESTAMP
	// as `YYYY-MM-DD HH:MM:SS` and the driver writes a time.Time parameter in
	// another spelling, so a timestamp cursor compared unequal to the rows that
	// share its second — and a trash emptied in one burst shares very few
	// seconds between tens of thousands of rows.
	ListTrashedExpired(ctx context.Context, before time.Time, storageIDs []int64, afterID int64, limit int) ([]*model.Node, error)
	// CountTrashedExpired tallies, per storage, the rows ListTrashedExpired
	// walks for the same `before`: how many, and the bytes their files hold
	// (a folder's size is a cached total of its files, so it is not added).
	CountTrashedExpired(ctx context.Context, before time.Time) (map[int64]TrashTally, error)
	// ListTrashed returns soft-deleted nodes (paginated). storage filter optional.
	ListTrashed(ctx context.Context, storageID *int64, limit, offset int) ([]*model.Node, int, error)
	RestoreNode(ctx context.Context, id int64) error
	// RestoreNodeAt restores a soft-deleted node, simultaneously reverting its
	// path/path_hash to the supplied original-path values and re-attaching it
	// to the resolved parent_id (nil = root). Used by trash.Service.Restore
	// to undo the `.filex-trash/` rename.
	RestoreNodeAt(ctx context.Context, id int64, parentID *int64, origPath string) error
	// LookupParentByPath returns the parent_id (nil at root) for a path's
	// parent dir, or an error if the parent dir doesn't exist in the cache.
	LookupParentByPath(ctx context.Context, storageID int64, fullPath string) (*int64, error)

	// Per-user metadata (starred, last_opened). Tags have their own tables
	// since 00055 — see the Tags block below.
	SetUserNodeMeta(ctx context.Context, userID, nodeID int64, key, value string) error
	DeleteUserNodeMeta(ctx context.Context, userID, nodeID int64, key string) error
	GetUserNodeMeta(ctx context.Context, userID, nodeID int64, key string) (string, error)
	ListUserNodeMetaForNode(ctx context.Context, userID, nodeID int64, prefix string) (map[string]string, error)
	ListNodesByUserMeta(ctx context.Context, userID int64, key string, limit int) ([]*model.Node, error)

	// Per-user VIEW preferences (00039): how this person left each folder, as
	// one JSON document read whole and written whole. Not per-node and not
	// queried by folder, so it is deliberately not user_node_meta — see
	// db/migrations/sqlite/00039_user_view_prefs.sql. "" = nothing stored yet,
	// which is not an error.
	GetUserViewPrefs(ctx context.Context, userID int64) (string, error)
	SetUserViewPrefs(ctx context.Context, userID int64, doc string) error

	// ── Surface preferences (00047) ──
	//
	// What a person chose about the interface itself — theme, palette,
	// density, language — one JSON document per person PER SURFACE ("web" or
	// "desktop"). Distinct from the view prefs above, which are how each
	// FOLDER was left. "" and "no row" are the same answer and neither is an
	// error: nothing chosen yet is every account's first day.
	GetUserPrefs(ctx context.Context, userID int64, surface string) (string, error)
	SetUserPrefs(ctx context.Context, userID int64, surface, doc string) error
	// Operator-defined themes (00051): a name plus two `--fe-*` token maps,
	// served to every browser beside the built-in palettes. Instance-wide —
	// the table has no tenant column, which is exactly why the admin routes
	// that write them are behind requireSupertenant.
	//
	// ⚠ ListCustomThemes is on the PUBLIC appearance path (an anonymous
	// visitor's share page resolves the instance default through it), so a row
	// whose token JSON cannot be parsed is DROPPED from the list rather than
	// failing the call: one bad row must not take the login page down with it.
	ListCustomThemes(ctx context.Context) ([]*model.CustomTheme, error)
	GetCustomTheme(ctx context.Context, key string) (*model.CustomTheme, error)
	UpsertCustomTheme(ctx context.Context, t *model.CustomTheme) error
	// DeleteCustomTheme reports whether a row was actually removed, so the
	// handler can answer 404 instead of a cheerful 200 for a key that was
	// never there.
	DeleteCustomTheme(ctx context.Context, key string) (bool, error)

	// ── Tags (00055): personal and team vocabularies + the files they are on ──
	//
	// ⚠ The store does not decide who may SEE a tag. Visibility needs the
	// caller, the tenant scope, the token's `root:` confinement and the ACL,
	// none of which a store knows — handlers/tags.go owns it, and every read
	// below returns rows for that code to judge. What the store does own is
	// the vocabulary boundary: TagQuery (rendered once, by TagQueryWhere) says
	// WHOSE tags a query may even consider.
	//
	// ListTags returns the vocabulary rows a query selects, oldest first.
	ListTags(ctx context.Context, q model.TagQuery) ([]*model.Tag, error)
	// CreateTag inserts a vocabulary row. A UNIQUE violation (a concurrent
	// create of the same name) is returned as an error; the caller re-reads.
	CreateTag(ctx context.Context, t *model.Tag) (*model.Tag, error)
	// ListNodeTags returns EVERY tag on a node, of every owner and tenant —
	// the caller filters to what the person may see.
	ListNodeTags(ctx context.Context, nodeID int64) ([]*model.Tag, error)
	// LinkNodeTags adds and removes tag links on one node in one transaction,
	// and deletes a removed tag that no file carries any more (a tag exists
	// while it is on something, as it always has).
	LinkNodeTags(ctx context.Context, nodeID int64, add, remove []int64) error
	// ListNodesByTagIDs returns live nodes carrying ANY of the tags,
	// newest-first (by node updated_at), capped at limit.
	ListNodesByTagIDs(ctx context.Context, tagIDs []int64, limit int) ([]*model.Node, error)
	// ListTagPlacements returns every (tag, live file) pair for the tags a
	// query selects — what the tag listings judge visibility on, file by file.
	ListTagPlacements(ctx context.Context, q model.TagQuery) ([]model.TagPlacement, error)

	// Notifications (in-app bell + webhook delivery audit)
	InsertNotification(ctx context.Context, n *model.NotificationInput) (int64, error)
	GetNotification(ctx context.Context, id int64) (*model.Notification, error)
	// ListNotifications and UnreadNotificationCount take mutedEvents: the
	// caller's already-resolved per-user mute list (see
	// model.NotificationSettings.MutedList). It is applied IN SQL rather than
	// by filtering the returned page, because a page filtered afterwards comes
	// back short and its total still counts the rows the caller then hides.
	// Nil/empty means "no mute filter", which is what the admin/global view
	// (userID == nil) always passes.
	//
	// hiddenBodies are LIKE patterns a row's `body` must NOT match — the
	// notify service's read-time filter for rows recorded about filex's own
	// directories before those stopped being announced (notify.hiddenBodies).
	// SQL for the same reason as the mute list: the unread badge is a COUNT,
	// and a row the list hides but the count still counts is a badge that says
	// "3" over an empty bell. Nil/empty filters nothing. The rows are not
	// changed or deleted — the filter is on the read, never on the history.
	//
	// broadcasts decides how the read treats rows with no user: which of them
	// a bell takes at all (see notify.Bell), and whose read state they carry.
	// It is in SQL for the same reason as the mute list.
	ListNotifications(ctx context.Context, userID *int64, onlyUnread bool, mutedEvents, hiddenBodies []string, broadcasts model.BroadcastFilter, limit, offset int) ([]*model.Notification, int64, error)
	// MarkNotificationRead and MarkAllNotificationsRead stamp rows ADDRESSED
	// to userID and nothing else: a broadcast is many readers' row, and is
	// marked per reader through MarkBroadcastsRead. nil userID stamps the
	// rows' own column regardless of owner (an instance-wide sweep).
	MarkNotificationRead(ctx context.Context, id int64, userID *int64) error
	MarkAllNotificationsRead(ctx context.Context, userID *int64) error
	// MarkBroadcastsRead records that readerID has read these broadcasts
	// (migration 00056). Ids that are not broadcasts are ignored, and marking
	// one twice is not an error. Whether the reader may see them is the
	// caller's question — the store cannot answer it.
	MarkBroadcastsRead(ctx context.Context, readerID int64, ids []int64) error
	// MarkAllBroadcastsRead moves readerID's "mark all read" point to the
	// newest notification: every broadcast up to it is read for that reader,
	// and nobody else. One write however many broadcasts there are.
	MarkAllBroadcastsRead(ctx context.Context, readerID int64) error
	UnreadNotificationCount(ctx context.Context, userID *int64, mutedEvents, hiddenBodies []string, broadcasts model.BroadcastFilter) (int64, error)
	UpdateWebhookStatus(ctx context.Context, id int64, status, errMsg string) error
	GetNotificationSettings(ctx context.Context, userID int64) (*model.NotificationSettings, error)
	UpsertNotificationSettings(ctx context.Context, s *model.NotificationSettings) error

	// Webhook targets (webhook v2, migration 00017) — additional POST
	// destinations next to the legacy single global webhook. Update
	// replaces the full mutable row (name/url/secret/events/enabled);
	// partial-PATCH merging happens in the handler.
	CreateWebhookTarget(ctx context.Context, t *model.WebhookTarget) (*model.WebhookTarget, error)
	GetWebhookTarget(ctx context.Context, id int64) (*model.WebhookTarget, error)
	ListWebhookTargets(ctx context.Context) ([]*model.WebhookTarget, error)
	UpdateWebhookTarget(ctx context.Context, t *model.WebhookTarget) error
	DeleteWebhookTarget(ctx context.Context, id int64) error
	// UpdateWebhookTargetDelivery records the outcome of the most recent
	// delivery attempt to one target (migration 00019): the final
	// attempt's HTTP status (0 = no response at all), the aggregated
	// error message ("" on success → stored NULL) and the attempt time.
	UpdateWebhookTargetDelivery(ctx context.Context, id int64, httpStatus int, errMsg string, at time.Time) error

	// Replica rules + failures + report + settings
	ListReplicaRules(ctx context.Context) ([]*model.ReplicaRule, error)
	GetReplicaRule(ctx context.Context, id int64) (*model.ReplicaRule, error)
	CreateReplicaRule(ctx context.Context, in *model.ReplicaRuleInput) (*model.ReplicaRule, error)
	UpdateReplicaRule(ctx context.Context, id int64, in *model.ReplicaRuleInput) (*model.ReplicaRule, error)
	DeleteReplicaRule(ctx context.Context, id int64) error

	UpsertReplicaFailure(ctx context.Context, path, op, errCode, errMsg string) error
	ResolveReplicaFailure(ctx context.Context, path, op string) error
	ListReplicaFailures(ctx context.Context, onlyUnresolved bool, limit, offset int) ([]*model.ReplicaFailure, int64, error)
	CountUnresolvedReplicaFailures(ctx context.Context) (int64, error)
	CountRecentlyResolvedReplicaFailures(ctx context.Context, since time.Time) (int64, error)

	UpsertReplicaStatusReport(ctx context.Context, total, failed, repaired int64, summaryJSON []byte) error
	GetReplicaStatusReport(ctx context.Context) (*model.ReplicaStatusReport, error)

	GetReplicaSettings(ctx context.Context) (*model.ReplicaSettings, error)
	UpsertReplicaSettings(ctx context.Context, s *model.ReplicaSettings) error

	/* calisma:d3 comments */
	// Node comments (migration 00020) — flat chronological threads on
	// file/folder nodes. List excludes soft-deleted rows and joins the
	// author display name; hard removal happens via the nodes FK CASCADE
	// plus DeleteNodeCommentsByNode in the trash purge hook.
	CreateNodeComment(ctx context.Context, c *model.NodeComment) (*model.NodeComment, error)
	GetNodeComment(ctx context.Context, id int64) (*model.NodeComment, error)
	ListNodeComments(ctx context.Context, nodeID int64) ([]*model.NodeComment, error)
	SoftDeleteNodeComment(ctx context.Context, id int64) error
	DeleteNodeCommentsByNode(ctx context.Context, nodeID int64) error

	// Providers (tenants). See docs/MULTI-TENANCY.md. Inert while multi-tenant
	// mode is off; a single "default" provider always exists (migration 00014).
	CreateProvider(ctx context.Context, p *model.Provider) (*model.Provider, error)
	GetProvider(ctx context.Context, id int64) (*model.Provider, error)
	GetProviderBySlug(ctx context.Context, slug string) (*model.Provider, error)
	// GetProviderByHost resolves a request Host to its tenant; returns nil if no
	// enabled provider claims that host.
	GetProviderByHost(ctx context.Context, host string) (*model.Provider, error)
	ListProviders(ctx context.Context) ([]*model.Provider, error)
	UpdateProvider(ctx context.Context, p *model.Provider) error
	DeleteProvider(ctx context.Context, id int64) error
	// GetSupertenant returns the single is_supertenant provider, or nil.
	GetSupertenant(ctx context.Context) (*model.Provider, error)

	// Provider ↔ storage links (M:N; 1:1 in the first UI).
	LinkProviderStorage(ctx context.Context, providerID, storageID int64) error
	UnlinkProviderStorage(ctx context.Context, providerID, storageID int64) error
	ListProviderStorageIDs(ctx context.Context, providerID int64) ([]int64, error)
	// GetProviderIDForStorage returns the (first) provider a storage is linked
	// to — used by background workers to derive tenancy from a storage.
	GetProviderIDForStorage(ctx context.Context, storageID int64) (int64, bool, error)

	/* kimlik:e3 cloud */
	// Provider plan metadata (cloud preparation, migration 00021 — see
	// docs/CLOUD.md). The columns are nullable and PASSIVE: only the
	// FILEX_CLOUD signup skeleton reads/writes them; with the flag off
	// (default) nothing touches these methods.
	SetProviderPlan(ctx context.Context, providerID int64, plan, limitsJSON, billingRef string) error
	GetProviderPlan(ctx context.Context, providerID int64) (plan, limitsJSON, billingRef string, err error)
}

// TrashTally is one storage's share of the trash a purge sweep will walk.
type TrashTally struct {
	Count int
	Bytes int64
}

// ExternalService is the DB row representation. Lives in the db package so
// model can stay pure-domain.
type ExternalService struct {
	Name        string
	Enabled     bool
	URL         string
	SecretEnc   string
	OptionsJSON string
	LastCheck   *time.Time
	LastState   string
}

// ShareWithMeta is the admin-list row that joins shares + creator email +
// node path + storage name so the admin UI doesn't have to issue N
// follow-up queries.
type ShareWithMeta struct {
	Share        *model.Share `json:"share"`
	CreatorEmail string       `json:"creator_email,omitempty"`
	// CreatorName is the creator as every screen names a person
	// (model.PersonLabel), filled by the admin handler — never by the store.
	CreatorName string `json:"creator_name,omitempty"`
	NodePath    string `json:"node_path,omitempty"`
	StorageName string `json:"storage_name,omitempty"`
	// PluginName names the app that opened this link, when one did (00046).
	// Empty for an ordinary share. It is joined in rather than derived by the
	// caller so the admin Shares table can carry a "plugin / page" column
	// without a lookup per row.
	PluginName string `json:"plugin_name,omitempty"`
	// URL is the canonical public link (`<origin>/s/<token>`), filled by the
	// admin handler from the configured public origin — never by the store.
	// The admin Shares page used to build it from the browser's address, so an
	// operator signed in on localhost copied a localhost link (issue #32).
	URL string `json:"url,omitempty"`
	// App says what an app's link IS (its page's declared purpose), filled
	// by the handlers from the app's manifest — never by the store. Nil for
	// an ordinary share and for a page that declares no purpose.
	App *AppLink `json:"app,omitempty"`
}

// AppLink is what a list of links needs to show an app's link for what it
// is (wire.PagePurpose): the owner's decision of 2026-09-21 — a signing link
// in My shares is marked as a signing request, opens the request's page in
// the app, and its revoke says that it cancels the request.
type AppLink struct {
	Plugin string `json:"plugin"`
	Page   string `json:"page"`
	// Label names the kind of link ({lang: …}): "Signing request".
	Label map[string]string `json:"label"`
	// Revoke is what revoking it does ({lang: …}); empty when the page says
	// nothing, and the list then asks its ordinary question.
	Revoke map[string]string `json:"revoke,omitempty"`
	// View / Section: where in the app the row opens (`/app/<plugin>/<view>
	// ?section=`). Empty View when the app has no home page to open.
	View    string `json:"view,omitempty"`
	Section string `json:"section,omitempty"`
}

// AuditEntryWithUser is an audit row joined with the user.email column
// for nicer admin UI rendering.
type AuditEntryWithUser struct {
	Entry     *model.AuditEntry `json:"entry"`
	UserEmail string            `json:"user_email,omitempty"`
	// TargetName is the thing the row is about, in words (a user's e-mail,
	// a storage's name, a file's path) — filled by the audit handler, not
	// by the query (handlers/audit_targets.go).
	TargetName string `json:"target_name,omitempty"`
	// UserName is who acted, as every screen names a person
	// (model.PersonLabel), filled by the audit handler — never by the store.
	UserName string `json:"user_name,omitempty"`
}

// AuditActionPrefixes reads ListAuditFiltered's action filter: a value ending
// in "." ("user.") asks for every action of that resource, and a comma-
// separated list of them ("app_plugin.,app-plugins.") for every action of
// any of them — one resource can be written under two spellings (a handler's
// own row and the admin route's generic one). The answer is one LIKE pattern
// per prefix, with `\` as the escape character, so the underscore in
// "auth_provider." is a literal underscore. ok=false means an exact action
// (or no filter at all).
//
// ⚠ The Audit page's action filter was a free-text box matched EXACTLY against
// wire names ("user.create"): a person typing what the page shows them
// ("Kullanıcı") found nothing. The page now offers the resources by name and
// sends their prefixes.
func AuditActionPrefixes(action string) ([]string, bool) {
	if len(action) < 2 || action[len(action)-1] != '.' {
		return nil, false
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	var out []string
	for _, p := range strings.Split(action, ",") {
		p = strings.TrimSpace(p)
		if len(p) < 2 || p[len(p)-1] != '.' {
			return nil, false
		}
		out = append(out, r.Replace(p)+"%")
	}
	return out, true
}

// DuplicateNode is one row of the duplicate-file report query — a slim
// projection of nodes (no timestamps/sync state) since the report only
// needs identity + grouping fields.
type DuplicateNode struct {
	ID        int64
	StorageID int64
	Path      string
	Name      string
	Size      int64
	Etag      string
}
