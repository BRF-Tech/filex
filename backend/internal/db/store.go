package db

import (
	"context"
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

	// Storages
	CreateStorage(ctx context.Context, s *model.Storage) (*model.Storage, error)
	GetStorage(ctx context.Context, id int64) (*model.Storage, error)
	GetStorageByName(ctx context.Context, name string) (*model.Storage, error)
	ListStorages(ctx context.Context) ([]*model.Storage, error)
	ListEnabledStorages(ctx context.Context) ([]*model.Storage, error)
	UpdateStorage(ctx context.Context, s *model.Storage) error
	UpdateStorageSyncCursor(ctx context.Context, id int64, at time.Time, token string) error
	DeleteStorage(ctx context.Context, id int64) error

	// Nodes
	CreateNode(ctx context.Context, n *model.Node) (*model.Node, error)
	GetNode(ctx context.Context, id int64) (*model.Node, error)
	GetNodeByPath(ctx context.Context, storageID int64, pathHash string) (*model.Node, error)
	// GetNodeByPathIncludingDeleted is used by the sync worker to detect a
	// previously soft-deleted row at the same path so it can resurrect it
	// (UNIQUE(storage_id, path_hash) blocks a fresh insert otherwise).
	// `RestoreNode` (declared further down for trash recovery) flips
	// deleted_at back to NULL.
	GetNodeByPathIncludingDeleted(ctx context.Context, storageID int64, pathHash string) (*model.Node, error)
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
	SearchNodes(ctx context.Context, storageID int64, like string, limit int) ([]*model.Node, error)

	// Users
	CreateUser(ctx context.Context, email, passwordHash, role, locale, tz string) (*model.User, error)
	GetUser(ctx context.Context, id int64) (*model.User, error)
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
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
	DeleteSessionsForUser(ctx context.Context, userID int64, exceptToken string) error
	CountActiveSessions(ctx context.Context) (int64, error)
	DeleteExpiredSessions(ctx context.Context) error

	// API tokens — long-lived bearer credentials for AI / MCP / FilexClient.
	CreateAPIToken(ctx context.Context, t *model.APIToken) (*model.APIToken, error)
	GetAPITokenByHash(ctx context.Context, tokenHash string) (*model.APIToken, error)
	ListAPITokens(ctx context.Context) ([]*model.APIToken, error)
	ListAPITokensByUser(ctx context.Context, userID int64) ([]*model.APIToken, error)
	TouchAPIToken(ctx context.Context, id int64) error
	DeleteAPIToken(ctx context.Context, id int64) error

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
	IncrementShareUpload(ctx context.Context, id int64, n int) error
	DeleteShare(ctx context.Context, id int64) error
	DeleteExpiredShares(ctx context.Context) error

	// Chunked uploads
	CreateChunkedUpload(ctx context.Context, u *model.ChunkedUpload) error
	GetChunkedUpload(ctx context.Context, id string) (*model.ChunkedUpload, error)
	UpdateChunkedUploadParts(ctx context.Context, id string, parts []model.UploadPart) error
	DeleteChunkedUpload(ctx context.Context, id string) error
	DeleteExpiredChunkedUploads(ctx context.Context) error

	// Sync runs / conflicts
	CreateSyncRun(ctx context.Context, storageID int64, cursorBefore string) (*model.SyncRun, error)
	FinishSyncRun(ctx context.Context, id int64, cursorAfter string, seen, added, updated, deleted int, status, errMsg string) error
	GetSyncRun(ctx context.Context, id int64) (*model.SyncRun, error)
	GetLastSyncRun(ctx context.Context, storageID int64) (*model.SyncRun, error)
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

	// Cross-storage analytics for the dashboard
	SumNodesBytesByStorage(ctx context.Context, storageID int64) (int64, error)
	CountNodesAddedSince(ctx context.Context, storageID int64, since time.Time) (int64, error)
	CountNodesDeletedSince(ctx context.Context, storageID int64, since time.Time) (int64, error)
	CountTotalShares(ctx context.Context) (int64, error)

	// Thumbnails
	GetThumbnail(ctx context.Context, nodeID int64) (*model.Thumbnail, error)
	UpsertThumbnail(ctx context.Context, t *model.Thumbnail) error
	SetThumbnailState(ctx context.Context, nodeID int64, state, errMsg string) error

	// Node versions
	CreateNodeVersion(ctx context.Context, v *model.NodeVersion) (*model.NodeVersion, error)
	ListNodeVersions(ctx context.Context, nodeID int64) ([]*model.NodeVersion, error)
	GetNodeVersion(ctx context.Context, id int64) (*model.NodeVersion, error)
	NextNodeVersionNumber(ctx context.Context, nodeID int64) (int, error)
	DeleteNodeVersion(ctx context.Context, id int64) error
	DeleteOldNodeVersions(ctx context.Context, nodeID int64, keep int) ([]*model.NodeVersion, error)

	// Sync conflicts (admin views)
	ListSyncConflictsByRun(ctx context.Context, runID int64) ([]*model.SyncConflict, error)
	ListSyncConflictsByStorage(ctx context.Context, storageID int64, limit int) ([]*model.SyncConflict, error)

	// Search rebuild
	AllNodesForIndex(ctx context.Context) ([]*model.Node, error)

	// Quota
	GetUserUsage(ctx context.Context, userID int64) (used, limit int64, err error)
	IncrementUserUsage(ctx context.Context, userID int64, delta int64) error
	SetUserQuota(ctx context.Context, userID int64, bytes int64) error
	RecomputeUserUsage(ctx context.Context, userID int64) (int64, error)

	// Node owner
	SetNodeOwner(ctx context.Context, nodeID int64, ownerID *int64) error
	GetNodeOwner(ctx context.Context, nodeID int64) (*int64, error)

	// Trash retention
	ListTrashedExpired(ctx context.Context, before time.Time, limit int) ([]*model.Node, error)
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

	// Per-user metadata (tags, starred, last_opened)
	SetUserNodeMeta(ctx context.Context, userID, nodeID int64, key, value string) error
	DeleteUserNodeMeta(ctx context.Context, userID, nodeID int64, key string) error
	GetUserNodeMeta(ctx context.Context, userID, nodeID int64, key string) (string, error)
	ListUserNodeMetaForNode(ctx context.Context, userID, nodeID int64, prefix string) (map[string]string, error)
	ListNodesByUserMeta(ctx context.Context, userID int64, key string, limit int) ([]*model.Node, error)

	// Tags use the shared node_meta table (key='tag:<name>', value='1').
	SetNodeTags(ctx context.Context, nodeID int64, tags []string) error
	GetNodeTags(ctx context.Context, nodeID int64) ([]string, error)
	ListAllTagsForStorage(ctx context.Context, storageID int64) ([]string, error)
	// ListAllTags returns every distinct tag across all storages (alphabetical).
	ListAllTags(ctx context.Context) ([]string, error)
	// ListNodesByTag returns non-deleted nodes carrying the given tag,
	// newest-first (by node updated_at), capped at limit.
	ListNodesByTag(ctx context.Context, tag string, limit int) ([]*model.Node, error)

	// Notifications (in-app bell + webhook delivery audit)
	InsertNotification(ctx context.Context, n *model.NotificationInput) (int64, error)
	GetNotification(ctx context.Context, id int64) (*model.Notification, error)
	ListNotifications(ctx context.Context, userID *int64, onlyUnread bool, limit, offset int) ([]*model.Notification, int64, error)
	MarkNotificationRead(ctx context.Context, id int64, userID *int64) error
	MarkAllNotificationsRead(ctx context.Context, userID *int64) error
	UnreadNotificationCount(ctx context.Context, userID *int64) (int64, error)
	UpdateWebhookStatus(ctx context.Context, id int64, status, errMsg string) error
	GetNotificationSettings(ctx context.Context, userID int64) (*model.NotificationSettings, error)
	UpsertNotificationSettings(ctx context.Context, s *model.NotificationSettings) error

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
	NodePath     string       `json:"node_path,omitempty"`
	StorageName  string       `json:"storage_name,omitempty"`
}

// AuditEntryWithUser is an audit row joined with the user.email column
// for nicer admin UI rendering.
type AuditEntryWithUser struct {
	Entry     *model.AuditEntry `json:"entry"`
	UserEmail string            `json:"user_email,omitempty"`
}
