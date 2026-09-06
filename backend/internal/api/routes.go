// Package api wires the HTTP routes onto a chi router.
//
// All handlers receive a Deps struct so they have access to the same
// shared services (Store, Worker, Search, Capability, Pipeline). New
// handlers should be added here in BuildRouter — never spawned via
// init() or globals.
package api

import (
	"context"
	"embed"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/capability"
	cloudpkg "github.com/brf-tech/filex/backend/internal/cloud" /* kimlik:e3 cloud */
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/dav"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/external"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/filecache"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/metrics"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/replica"
	"github.com/brf-tech/filex/backend/internal/s3api"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/staging"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/update"
	"github.com/brf-tech/filex/backend/internal/versioning"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Deps is the bundle of services every handler needs.
type Deps struct {
	Cfg    config.Config
	Store  db.Store
	Worker *syncpkg.Worker
	Index  *search.Index
	Caps   *capability.Service
	// External resolves the live `external_services` rows. It is what makes an
	// admin-UI change to OnlyOffice / drawio / the converter take effect
	// without a restart (issue #17). Constructed in BuildRouter when nil.
	External        *external.Resolver
	Thumbs          *thumb.Pipeline
	Share           *share.Service
	OnlyOffice      *onlyoffice.Service
	Ops             *ops.Service
	Trash           *trash.Service
	Quota           *quota.Service
	Versions        *versioning.Service
	Queue           queue.Driver
	Notify          notify.Service
	ReplicaService  *replica.Service
	ReplicaCron     *replica.CronScheduler
	ReplicaReloader *replica.RulesReloader
	StorageResolver func(int64) (storage.Driver, error)
	// Plugins manages out-of-process storage drivers (internal/plugin). Nil
	// when FILEX_PLUGINS_DISABLED — the admin routes then answer 503.
	Plugins   *plugin.Manager
	Embed     embed.FS // web/dist + admin
	LocalAuth auth.LoginDriver
	OIDCAuth  auth.OIDCDriver
	// Directory is the external password authority the FILE PROTOCOLS consult
	// (the LDAP driver, when configured and not switched off with
	// auth.ldap.protocol_login). Nil = local passwords only.
	//
	// ⚠ It is set on ProtocolAuth below rather than passed to the login
	// handler: the browser path reaches the same driver through LocalAuth's
	// login chain, and a directory login there mints a session, which is
	// precisely what a per-request protocol login must NOT do.
	Directory protocolauth.Directory
	// ACL resolves per-user/per-item grants (RBAC feature). Constructed in
	// BuildRouter from Store when nil.
	ACL *acl.Resolver
	// ProtocolAuth is the one door every non-HTTP protocol resolves its caller
	// through (WebDAV today; S3, SFTP, FTPS, NFS and FUSE next). ONE instance is
	// shared on purpose: a resolver per protocol would mean a credential cache
	// per protocol, and therefore a different answer to "how long does a revoked
	// password keep working" on each of them. Constructed in BuildRouter when
	// nil.
	ProtocolAuth *protocolauth.Resolver
	// FTPSAddr reports the address the FTPS listener actually bound.
	//
	// ⚠⚠ A function, not a string, and read at REQUEST time. Config may say
	// `:0` ("any free port") — and it does in the test harness — so the only
	// place the real port exists is the running listener, which is created
	// after this router. A guide that printed the configured value would tell
	// somebody to connect to port 0. Nil falls back to the configured address.
	FTPSAddr func() string
	// Mailer sends invite/share notices (optional; nil → links shown on-screen).
	Mailer *mailer.Service
	// ZipCache caches folder-share ZIPs (shared with the background warmer).
	ZipCache *sharezip.Cache
	// FileCache holds the locally prepared copies of big files that live on
	// slow storages (internal/filecache). Nil = no caching; every read then
	// goes to the driver exactly as it did before the cache existed.
	FileCache *filecache.Cache
	/* koru:k2 av */
	// AVScan enqueues an async ClamAV scan for a freshly written node
	// (v0.4 "Koru"). Wired by the server bootstrap only when a ClamAV
	// binary and the persistent queue are both available; nil disables
	// scanning entirely.
	AVScan func(ctx context.Context, n *model.Node)

	// AVScanAfterSave is the DEBOUNCED twin of AVScan, used only by the text
	// editor's save-over-an-existing-file path: it schedules one scan per
	// file per editing window instead of one per Ctrl+S. nil falls back to
	// AVScan (scan immediately) rather than to no scan.
	AVScanAfterSave func(ctx context.Context, n *model.Node)
	// Updater tracks published releases and (on installs that own their
	// binary) applies them. Nil = the feature is off; the admin endpoints then
	// report a "disabled" status instead of disappearing. See docs/UPDATES.md.
	Updater *update.Service
	// Staging is the on-disk staging area for resumable uploads. Constructed
	// in BuildRouter from Cfg.Upload.StagingDir when nil.
	Staging *staging.Area
	// StagedUploads is filled in BY BuildRouter so the server bootstrap can
	// run the staging sweeper against the same handler the routes use.
	StagedUploads *handlers.StagedUpload
	/* wiring:e2 */
	// E2EEscrow is the installation's E2E key-escrow PUBLIC key, or nil when
	// escrow is off (the default). It is published in /api/capabilities so the
	// browser can wrap new folders' master keys to it, and it seals the
	// proof-of-possession challenge in handlers.E2E. The server never has the
	// private half — see internal/e2e/escrow.go.
	E2EEscrow *e2e.EscrowKey
	// Body resolves where a file's bytes are — the storage driver, or the
	// staging area while a staged upload is still being transferred. Built in
	// BuildRouter from Store+Staging when nil, and filled back in here so the
	// bootstrap can hand the SAME resolver to the surfaces that live outside
	// the router (WebDAV, thumbnails, versioning, the queue workers).
	Body *filebody.Resolver
}

// BuildRouter constructs the chi router with all routes wired up.
func BuildRouter(d *Deps) http.Handler {
	r := chi.NewRouter()

	// RBAC/ACL resolver — the identity-driven complement to confine. Every
	// file handler consults it to filter listings and gate reads/mutations by
	// the caller's grants + account-role ceiling.
	if d.ACL == nil {
		d.ACL = acl.New(d.Store)
	}
	if d.External == nil {
		d.External = external.New(d.Store)
	}
	if d.ProtocolAuth == nil {
		d.ProtocolAuth = protocolauth.New(d.Store, d.ACL, d.Cfg.MultiTenant)
		d.ProtocolAuth.Directory = d.Directory
		// The box is what lets an S3 access key be issued at all: SigV4 needs a
		// recoverable secret, so with no FILEX_SECRET_KEY configured, issuing
		// fails loudly instead of storing one in the clear.
		if box, err := protocolauth.SecretsFrom(d.Cfg.SecretKey); err == nil {
			d.ProtocolAuth.Secrets = box
		} else {
			slog.Error("protocolauth: secret box unavailable; S3 access keys cannot be issued", slog.Any("err", err))
		}
	}

	// The S3 endpoint, built here because it has to be reachable at the ROOT
	// of a dedicated host: every S3 client talks to `/` (ListBuckets) and
	// `/bucket/key` (objects). Mounted only under /s3, `GET /` reaches the web
	// app and the client parses an HTML redirect as XML.
	//
	// ⚠ chi requires middleware before any route is registered, which is why
	// this sits at the top rather than next to the other mounts.
	s3h := s3api.NewHandler(s3api.Config{
		Enabled:     d.Cfg.S3.Enabled,
		Store:       d.Store,
		Auth:        d.ProtocolAuth,
		ACL:         d.ACL,
		Resolver:    d.StorageResolver,
		Body:        d.Body,
		Quota:       d.Quota,
		Index:       d.Index,
		Thumbs:      d.Thumbs,
		Staging:     d.Staging,
		PublicURL:   d.Cfg.PublicURL,
		MultiTenant: d.Cfg.MultiTenant,
		Domain:      d.Cfg.S3.Domain,
	})
	if d.Cfg.S3.Enabled && d.Cfg.S3.Domain != "" {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				// ⚠ /healthz stays with the app even on the S3 host. A load
				// balancer or uptime probe pointed at the endpoint would
				// otherwise get a signed-request refusal and call the service
				// down. The cost is that a bucket named "healthz" is
				// unreachable by name, which is a trade worth making once.
				if req.URL.Path != "/healthz" && s3api.HostMatches(d.Cfg.S3.Domain, req.Host) {
					s3h.ServeHTTP(w, req)
					return
				}
				next.ServeHTTP(w, req)
			})
		})
	}

	r.Use(Logger)
	r.Use(Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   d.Cfg.CORS.AllowedOrigins,
		AllowedMethods:   d.Cfg.CORS.AllowedMethods,
		AllowedHeaders:   d.Cfg.CORS.AllowedHeaders,
		ExposedHeaders:   []string{"Content-Length", "Content-Disposition"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Existing user-facing handlers.
	mh := handlers.NewManager(d.Store, d.StorageResolver)
	mh.AttachACL(d.ACL)
	// The per-user ceiling on the synchronous write paths (vfUpload, and the
	// IngestFile fallback that drop/ShareX/AI take for small files). The staged
	// path checks at `begin`; before this, everything below the staging
	// threshold had no ceiling at all.
	mh.Quota = d.Quota
	if d.Index != nil {
		// Wire Bleve so vfSearch consults the index before falling
		// back to SQL LIKE.
		mh.AttachSearchIndex(d.Index)
	}
	if d.Thumbs != nil {
		// Async thumb generation after vfUpload commits — without
		// this every new upload starts with no preview and a
		// `filex thumb backfill` is required to fill the grid.
		mh.AttachThumbPipeline(d.Thumbs)
	}
	uh := handlers.NewUpload(d.Store, d.StorageResolver, d.Thumbs)
	uh.AttachACL(d.ACL)
	// Staged uploads — the driver-agnostic resumable path (docs/UPLOADS.md).
	// It is handed the manager so the committed bytes fire the same post-write
	// hooks (search index, thumbnail, writehook, realtime) as vfUpload rather
	// than a parallel set that can drift.
	if d.Staging == nil {
		stagingDir := d.Cfg.Upload.StagingDir
		if stagingDir == "" && d.Cfg.DataDir != "" {
			// config.Load fills this in; a Deps assembled by hand (tests,
			// embedders) gets the same default rather than a silently
			// disabled feature.
			stagingDir = filepath.Join(d.Cfg.DataDir, "uploads")
		}
		d.Staging = staging.New(stagingDir)
	}
	// One byte-source resolver, shared by EVERY read surface. A staged upload
	// publishes its node the moment the client commits and transfers the bytes
	// afterwards, so "ask the driver" stopped being the whole answer; this is
	// where the other half lives. Deliberately built once and handed to all of
	// them rather than re-derived per handler — a read that is staging-aware on
	// one surface and not another is the same file behaving as two products.
	// The local copy prepared for a big file on a slow storage. Defaulted from
	// Cfg the same way Staging is, so a Deps assembled by hand (tests,
	// embedders) gets the feature rather than a silently disabled one — the
	// server bootstrap passes its own so that both resolvers share ONE cache
	// and therefore one view of the global cap.
	if d.FileCache == nil {
		cacheDir := d.Cfg.Cache.Dir
		if cacheDir == "" && d.Cfg.DataDir != "" {
			cacheDir = filepath.Join(d.Cfg.DataDir, "cache")
		}
		if d.Cfg.Cache.Disabled {
			cacheDir = ""
		}
		d.FileCache = filecache.New(filecache.Config{
			Dir:             cacheDir,
			MinSize:         d.Cfg.Cache.MinSize,
			MaxBytes:        d.Cfg.Cache.MaxBytes,
			SlowBytesPerSec: d.Cfg.Cache.SlowBytesPerSec,
		})
	}
	if d.Body == nil {
		d.Body = filebody.New(d.Store, d.Staging).WithCache(d.FileCache)
	}
	mh.AttachBody(d.Body)
	if d.Thumbs != nil {
		// The bootstrap already wires its own pipeline, but a Deps assembled by
		// hand (tests, embedders) must not end up with a thumbnailer that is
		// the one read surface still blind to staging.
		d.Thumbs.AttachBody(d.Body)
	}
	suh := handlers.NewStagedUpload(d.Store, mh, d.Staging, d.Ops, d.Quota, d.Cfg.Upload.ChunkSize, d.Cfg.Upload.StagingTTL)
	suh.AttachACL(d.ACL)
	d.StagedUploads = suh
	// …and back into the manager, so the whole-body surfaces that write through
	// IngestFile (the public drop link) stage a large file instead of holding
	// the request open for the driver write. The chunked protocol above and
	// this share one staging area, one transfer op and one set of hooks.
	mh.AttachStaged(suh)
	ah := handlers.NewArchive(d.Store, d.StorageResolver)
	ah.AttachACL(d.ACL)
	ah.AttachBody(d.Body)
	// ⚠ Without these two, Extract and Add put bytes on the storage and told
	// nobody: no node row, no search document, no realtime frame.
	ah.AttachSearchIndex(d.Index)
	ah.AttachThumbs(d.Thumbs)
	// ⚠ Every absolute URL filex hands out (share + file-request links, the
	// wss:// endpoint, upload-ticket URLs, every link inside an e-mail) is
	// built from THIS resolver, so a multi-tenant install names the tenant's
	// host and not the operator's. See internal/tenanturl — new absolute-URL
	// builders belong on it too.
	tenants := tenanturl.New(d.Store, d.Cfg.PublicURL, d.Cfg.MultiTenant)

	sh := handlers.NewShare(d.Share, d.Store, d.StorageResolver, d.Cfg.PublicURL, d.ZipCache)
	sh.AttachTenants(tenants)
	sh.AttachACL(d.ACL)
	sh.AttachBody(d.Body)
	// The public folder page draws its gallery tiles from the same thumbnail
	// cache the app uses. Unwired, those tiles fall back to the full-size
	// originals — which is what made a shared photo folder crawl on open.
	sh.AttachThumbs(d.Thumbs)
	// Fallback language for the public pages when the visitor's browser asks
	// for neither of the two we ship (see publicLocale).
	sh.AttachLocale(d.Cfg.DefaultLocale)
	// File-drop (public upload link) handler — the inverse of Share. Reuses
	// the manager's ingest path (IngestFile/EnsureDir) so dropped files land
	// exactly like authenticated uploads (mime, node cache, thumbnails).
	dh := handlers.NewDrop(d.Store, mh, d.Share, d.Notify, d.Mailer, d.Cfg.PublicURL)
	dh.AttachTenants(tenants)

	// Upload tickets: minted on the token-authenticated AI/MCP surfaces below,
	// redeemed on the credential-free /u/{ticket} route. The store is shared by
	// both halves, so it is built here — before either mounts.
	uploadTickets := handlers.NewUploadTicketStore()
	tuh := handlers.NewTicketUpload(d.Store, d.StorageResolver, uploadTickets)
	tuh.AttachACL(d.ACL)
	tuh.AttachThumbs(d.Thumbs)
	tuh.AttachSearchIndex(d.Index)
	tuh.AttachStaged(suh)
	tuh.AttachBody(d.Body)
	// Same fallback language as the share pages — without this a drop page
	// renders in whatever the two-language table falls back to, independently
	// of the server's own default.
	dh.AttachLocale(d.Cfg.DefaultLocale)
	oh := handlers.NewOps(d.Ops, d.Store)
	oh.AttachACL(d.ACL)
	if d.Ops != nil {
		// The async ops worker must mirror its filesystem moves/deletes/copies
		// into the DB node index (listings read the DB). The manager handler
		// owns that DB logic, so inject it as the worker's DBSync hook —
		// without this, async move/delete/copy don't reflect in the UI.
		d.Ops.SetSync(mh)
		// …and the staged-upload transfer, for the same reason: the bytes move
		// in the worker, but the node/index/thumb/writehook side of a write
		// lives in the handler layer.
		d.Ops.SetUploadCommitter(suh)
	}
	ooh := handlers.NewOnlyOffice(d.OnlyOffice, d.Store, d.StorageResolver)
	ooh.AttachACL(d.ACL)
	ooh.AttachBody(d.Body)
	// The save callback fans out through the same post-write gate as every
	// other write surface: row + index + thumbnail + realtime frame +
	// `file.updated` + antivirus. ⚠ Without this wire an office document keeps
	// its pre-edit text in content search and is the one write ClamAV never
	// sees (docs/ONLYOFFICE.md, "What a save does").
	if d.OnlyOffice != nil {
		d.OnlyOffice.AttachSync(protocolsync.New(d.Store, d.Index, d.Thumbs, writehook.OriginOnlyOffice))
	}
	th := handlers.NewThumb(d.Store, d.Thumbs)
	ch := handlers.NewCapabilities(d.Caps, d.Store, d.Cfg.MultiTenant)
	ch.E2EEscrow = d.E2EEscrow /* wiring:e2 */
	stg := handlers.NewStorages(d.Store, d.Worker)
	// The plugin conformance gate on storage save (handlers/plugin_gate.go).
	stg.Plugins = d.Plugins
	stg.StorageResolver = d.StorageResolver
	stg.DemoMode = d.Cfg.Demo.Mode
	ush := handlers.NewUsers(d.Store)
	seth := handlers.NewSettings(d.Store)
	seth.AttachMailer(d.Mailer)
	authh := handlers.NewAuth(d.Store, d.LocalAuth, d.OIDCAuth, d.Cfg.PublicURL, d.Cfg.MultiTenant, d.Cfg.CookieDomain)
	provH := handlers.NewProviders(d.Store, d.Cfg.MultiTenant)
	sxh := handlers.NewSearch(d.Index, d.Store)
	sxh.AttachACL(d.ACL)

	// New self-service + admin handlers.
	authSelf := handlers.NewAuthSelf(d.Store)
	dashH := handlers.NewDashboard(d.Store, d.Caps, d.Worker)
	auditH := handlers.NewAudit(d.Store)
	syncAdmH := handlers.NewSyncAdmin(d.Store)
	sharesAdmH := handlers.NewSharesAdmin(d.Store)
	externalH := handlers.NewExternalAdmin(d.Store, d.Caps, d.External, envManagedExternal(d.Cfg))
	authProvH := handlers.NewAuthProviders(d.Store)
	storagesAdmH := handlers.NewStoragesAdmin(d.Store)
	// Test probes a plugin driver properly (handlers/storages_admin.go).
	storagesAdmH.Plugins = d.Plugins
	usersAdmH := handlers.NewUsersAdmin(d.Store)
	searchAdmH := handlers.NewSearchAdmin(d.Index, d.Store)
	queueH := handlers.NewQueue(d.Queue)
	notifH := handlers.NewNotifications(d.Notify)
	replicaH := handlers.NewReplica(d.Store, d.ReplicaService, d.ReplicaCron, d.ReplicaReloader)
	trashH := handlers.NewTrash(d.Trash, d.Store)
	trashH.AttachSearchIndex(d.Index)
	trashH.AttachACL(d.ACL)
	metaH := handlers.NewMeta(d.Store)
	sharedH := handlers.NewShared(d.Store)
	quotaH := handlers.NewQuota(d.Quota)
	saveTextH := handlers.NewSaveText(d.Store, d.StorageResolver)
	saveTextH.AttachACL(d.ACL)
	saveTextH.AttachSearchIndex(d.Index)
	if d.Versions != nil {
		// Snapshot the pre-edit bytes into version history before
		// every save-text write (Ada, translated from Turkish: "not a
		// damn thing showed up in the version history after a change"
		// — handler never tapped the versioning service).
		saveTextH.AttachVersions(d.Versions)
		// The pre-write gate: every destructive write surface calls
		// writehook.BeforeOverwrite, and it lands here.
		if d.Cfg.VersionsOnOverwrite {
			guard := d.Versions.GuardOverwrite
			if d.Cfg.VersionsFailOpen {
				// A one-shot boot line, because the per-overwrite WARN below
				// only fires when a write actually hits a failed snapshot. An
				// operator who flips this during a full-disk incident and then
				// forgets has no other way to notice the instance is still
				// running fail-open once the incident is over.
				slog.Warn("versions: fail-open is ON at boot -- a snapshot failure will let the overwrite through and log, instead of refusing the write (FILEX_VERSIONS_FAIL_OPEN=1)")
				inner := guard
				guard = func(ctx context.Context, storageID int64, rel string) error {
					if err := inner(ctx, storageID, rel); err != nil {
						slog.Warn("overwrite proceeding without a snapshot",
							slog.Int64("storage", storageID),
							slog.String("path", rel),
							slog.String("err", err.Error()))
					}
					return nil
				}
			}
			writehook.ConfigureOverwriteGuard(guard)
		} else {
			// ⚠ Explicitly nil, not merely "skip wiring". The guard is
			// process-wide state, so leaving whatever a previous BuildRouter
			// installed would let one instance keep snapshotting through
			// another instance's (possibly closed) store -- which is exactly
			// what happens in a test binary, where several routers are built
			// in one process.
			writehook.ConfigureOverwriteGuard(nil)
			// The loudest of the two non-default states: the guard is not
			// wired at all, so every overwrite on this instance destroys the
			// prior bytes with no snapshot and no refusal. One line at boot,
			// not per-overwrite -- there is no per-write event to hang a log
			// line on when nothing runs.
			slog.Warn("versions: pre-write overwrite guard is DISABLED at boot -- an overwrite now destroys the prior bytes with no snapshot (FILEX_VERSIONS_ON_OVERWRITE=0)")
		}
	} else {
		// No versioning service at all: same reasoning as the branch above --
		// clear the process-wide guard rather than inheriting a stale one.
		writehook.ConfigureOverwriteGuard(nil)
	}
	versionsH := handlers.NewVersions(d.Store, d.Versions)
	versionsH.AttachSearchIndex(d.Index)
	grantsH := handlers.NewGrants(d.Store, d.ACL)
	grantsH.AttachInvite(d.Share, d.Mailer, d.Cfg.PublicURL)
	grantsH.AttachTenants(tenants)
	selfTokensH := handlers.NewSelfTokens(d.Store, d.ACL, d.ProtocolAuth)
	desktopAuthH := handlers.NewDesktopAuth(d.Store)

	// ────── public viewer ──────
	r.Get("/api/files/share/{token}", sh.HandleMetadata)
	r.Get("/s/{token}", sh.HandleDownload)
	r.Post("/s/{token}", sh.HandleDownload)      // PIN form posts to same URL
	r.Get("/s/{token}/f/*", sh.HandleBrowseFile) /* wiring:d2 — folder-share sub-file (gallery/list page) */

	// ────── public file-drop (upload link) ──────
	// GET renders the upload page (PIN gate first when protected); POST is
	// both the PIN-form submit and the multipart drop. No auth: the target
	// folder is resolved server-side from the token, and existing contents
	// are never listed ("blind drop").
	r.Get("/d/{token}", dh.Page)
	r.Post("/d/{token}", dh.Upload)

	// ────── upload ticket redeem (credential-free, single-use) ──────
	// PUT is what `curl -T` sends; POST accepts the same transfer as multipart
	// `file`. No auth by design: the destination was fixed when an authorized
	// caller minted the ticket, and the URL can do nothing but that one write.
	r.Put("/u/{ticket}", tuh.Upload)
	r.Post("/u/{ticket}", tuh.Upload)

	// ────── onlyoffice public endpoints (HMAC/JWT signed) ──────
	r.Get("/api/files/onlyoffice/fetch", ooh.Fetch)
	r.Post("/api/files/onlyoffice/callback", ooh.Callback)

	// ────── auth (always public) ──────
	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/login", authh.Login)
		r.Post("/logout", authh.Logout)
		r.Get("/oidc/start", authh.OIDCStart)
		r.Get("/oidc/callback", authh.OIDCCallback)
		r.Get("/whoami", authh.WhoAmI)
		// Desktop authorization, app half. Unauthenticated on purpose: the
		// desktop has no session yet — holding the PKCE verifier IS the proof.
		r.Post("/desktop/exchange", desktopAuthH.Exchange)
	})

	// ────── thumbs (auth-light: signed URL accepted without session) ──────
	r.Get("/api/files/thumb/{id}", th.Serve)

	// ────── public capabilities ──────
	// Embedders + the SPA both call /api/files/capabilities; keep the
	// historical /api/capabilities working for older callers, but make
	// the file-namespaced path the documented one.
	// ⚠ Wrapped in auth.AnnotateToken so the answer can carry `caller_kind`:
	// the explorer asks capabilities whether this caller is a person or an app
	// token (migration 00030), and an unwrapped route sees no token at all and
	// would report every embed as a person.
	//
	// ⚠ AnnotateToken, NOT MiddlewareWithToken(store, false). The latter is
	// "optional auth" but it still REJECTS in three cases (disabled account,
	// unknown X-Filex-Token-User, and it runs the session drivers) — and this
	// route is fetched by the login screen and the public share/drop pages. A
	// disabled user's browser still holds a cookie, so that chain would have
	// made the login page's capabilities probe answer 403. This one cannot
	// reject at all: anything unusable is simply anonymous, which reports as
	// "user".
	r.Group(func(r chi.Router) {
		r.Use(auth.AnnotateToken(d.Store))
		r.Get("/api/capabilities", ch.Get)
		r.Get("/api/files/capabilities", ch.Get)
	})

	/* ────── wiring:e1 — branding ──────
	   Settings-driven identity for the public share/drop/PIN pages + the
	   admin SPA login. One shared source: the Settings handler invalidates
	   it on branding.* writes; Share/Drop render through it; GET
	   /api/branding is public (the login page fetches it pre-session). */
	brandingSrc := handlers.NewBrandingSource(d.Store, d.Cfg.MultiTenant)
	sh.AttachBranding(brandingSrc)
	dh.AttachBranding(brandingSrc)
	seth.AttachBranding(brandingSrc)
	r.Get("/api/branding", handlers.NewBranding(brandingSrc).Get)
	/* ────── /wiring:e1 ────── */

	/* kimlik:e3 cloud */
	// Cloud self-signup PREPARATION (v0.7 "Kimlik", docs/CLOUD.md). Master-
	// gated by FILEX_CLOUD: while the flag is off — the default — this block
	// is skipped entirely, so no /api/cloud route registers, capabilities
	// carry no cloud field and behavior is byte-identical to a build without
	// the feature. Signup reuses the same provider-provisioning primitive as
	// /api/admin/providers (a tenant IS a provider row).
	if d.Cfg.Cloud.Enabled {
		cloudSvc := cloudpkg.New(d.Store, d.Mailer, d.Cfg.Cloud.PlansJSON, d.Cfg.Cloud.BaseHost)
		cloudH := handlers.NewCloud(cloudSvc, cloudpkg.NewStripe(d.Cfg.Cloud.StripeSecret), d.Cfg.MultiTenant)
		r.Route("/api/cloud", cloudH.Register)
		ch.CloudEnabled = true
	}

	// Realtime hub for live folder updates + presence over WebSocket.
	// SetChangeEmitter wires the file-mutation handlers to broadcast into it;
	// a nil emitter (unwired) is a safe no-op.
	hub := realtime.NewHub()
	handlers.SetChangeEmitter(hub)
	// The protocol servers (WebDAV, S3, SFTP, FTPS, NFS) reach the catalogue
	// through internal/protocolsync rather than through these handlers, so the
	// same hub has to be wired there too — otherwise a file written over any
	// of them lands in the DB and the index while every open explorer keeps
	// showing the old listing. Package-level on purpose: s3api is constructed
	// at the top of this function and the SFTP/FTPS/NFS servers live in
	// internal/server, so a field would make this depend on wiring order.
	protocolsync.SetChangeEmitter(hub)

	/* bag:b3 event */
	// Wire the notify sink so the mutation handlers can emit canonical
	// file/share events (file.uploaded, file.updated, share.created, …) to webhook v2
	// targets. Nil-safe: an unwired sink disables emission.
	handlers.SetNotifySink(d.Notify)

	/* koru:k2 av */
	// Wire the async antivirus-scan sink the upload surfaces call after a
	// write (upload finalize, manager vfUpload, public drop). Nil-safe:
	// unwired (no ClamAV binary / no queue) disables scanning.
	handlers.SetAntivirusEnqueue(d.AVScan)
	handlers.SetAntivirusEnqueueAfterSave(d.AVScanAfterSave)

	// Writehook — the single post-write side-effect gate (AV enqueue +
	// canonical file event) every write surface routes through with its
	// origin stamp (manager/ai/sharex/dav/ops). Same sinks as above; both
	// nil-safe.
	writehook.Configure(d.AVScan, d.Notify)
	// …and the debounced twin, for the surfaces that save repeatedly during a
	// single editing session (writehook.OnFileSaved). nil falls back to the
	// immediate scan, never to no scan.
	writehook.ConfigureSaveScan(d.AVScanAfterSave)
	wsTickets := realtime.NewTicketStore()
	wsh := handlers.NewWS(d.Store, d.ACL, hub, wsTickets, d.Cfg.PublicURL)
	wsh.AttachPublicURLConfigured(d.Cfg.PublicURLSet)
	wsh.AttachTenants(tenants)

	// Live-collaboration WebSocket (folder change events + presence). OPTIONAL
	// auth (required=false): a session cookie / API token sets the user for the
	// native panel, while a ticket-only cross-origin upgrade from the embedded
	// webcomponent passes through so wsh.Handle can authenticate it via ?ticket
	// (a required-auth group would 401 the cookieless embedded connection before
	// the handler ever sees the ticket). Outside /api/files → no confine.
	r.Group(func(r chi.Router) {
		r.Use(auth.MiddlewareWithToken(d.Store, false))
		r.Use(auth.TenantResolver(d.Store, d.Cfg.MultiTenant))
		r.Get("/api/ws", wsh.Handle)
	})

	// ────── authenticated user routes ──────
	r.Group(func(r chi.Router) {
		// Accept EITHER a cookie/JWT session (native panel) OR a root-confined
		// API token (host apps proxying the embedded explorer). Token absent →
		// falls through to the session chain, so existing auth is unchanged.
		r.Use(auth.MiddlewareWithToken(d.Store, true))
		// Resolve the tenant (provider) scope from the user (no-op unless
		// multi-tenant mode is on). See docs/MULTI-TENANCY.md.
		r.Use(auth.TenantResolver(d.Store, d.Cfg.MultiTenant))
		// Audit curated self-service + file mutations (profile, password,
		// TOTP, shares, file deletes — shouldAudit() filters the rest).
		r.Use(auth.AuditMiddleware(d.Store))

		// Self-service profile/password/TOTP.
		// Avoid `r.Route("/api/auth", …)` here because chi forbids
		// re-mounting an already-mounted path (the public /api/auth Route
		// above owns it). We declare each leaf path inline instead.
		r.Get("/api/auth/me", authSelf.Me)
		r.Patch("/api/auth/profile", authSelf.UpdateProfile)
		r.Post("/api/auth/password", authSelf.ChangePassword)
		r.Post("/api/auth/totp/enroll", authSelf.TotpEnroll)
		r.Post("/api/auth/totp/verify", authSelf.TotpVerify)
		r.Post("/api/auth/totp/disable", authSelf.TotpDisable)

		// ────── self-service CREDENTIAL surfaces ──────
		// Every route in this group answers the same question — "show me, and
		// let me mint, the credentials of the person calling" — so they share
		// one gate: an APP token has no such person and is refused here (403,
		// handlers.RequirePersonalCaller). Grouping beats four copied checks;
		// a new credential surface joins the rule by being registered inside
		// this block. ⚠ Confinement and role are different axes and are NOT
		// touched: a `root:`-scoped token may be a person's, a viewer is still
		// a person, and each surface keeps its own ceiling.
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePersonalCaller)

			// S3 access keys — self-service, because a credential only an admin can
			// mint is one most people never get. The permission ceiling is enforced
			// by protocolauth.Issue (a key can only narrow its owner), not by who
			// is asking.
			s3keys := handlers.NewS3Keys(d.Store, d.ProtocolAuth, d.Cfg.S3, d.Cfg.PublicURL)
			r.Get("/api/auth/s3-keys", s3keys.List)
			r.Post("/api/auth/s3-keys", s3keys.Create)
			r.Post("/api/auth/s3-keys/{id}/state", s3keys.SetState)
			r.Delete("/api/auth/s3-keys/{id}", s3keys.Delete)

			// Self-service SSH keys, for the SFTP endpoint.
			//
			// ⚠ Not a follow-up to shipping SFTP: `ssh-copy-id` appends to
			// ~/.ssh/authorized_keys over a shell and filex has none, so without
			// this screen public-key authentication is unreachable and everybody
			// sends their account password to a file server instead.
			sshkeys := handlers.NewSSHKeys(d.Store, d.ProtocolAuth, d.Cfg.SFTP.Enabled,
				sftpHost(d.Cfg.PublicURL), sftpPort(d.Cfg.SFTP.Addr),
				ftpsFacts(d.Cfg, d.FTPSAddr))
			r.Get("/api/auth/ssh-keys", sshkeys.List)
			r.Post("/api/auth/ssh-keys", sshkeys.Create)
			r.Post("/api/auth/ssh-keys/{id}/state", sshkeys.SetState)
			r.Delete("/api/auth/ssh-keys/{id}", sshkeys.Delete)

			// Self-service NFS exports. ⚠ The path a POST returns IS the
			// credential: NFSv3 cannot authenticate a request, so filex binds the
			// identity to a high-entropy export path instead (see
			// model.NFSExport). It is shown once and stored hashed.
			nfsexports := handlers.NewNFSExports(d.Store, d.ProtocolAuth, d.Cfg.NFS.Enabled,
				sftpHost(d.Cfg.PublicURL), portOf(d.Cfg.NFS.Addr, 2049))
			r.Get("/api/auth/nfs-exports", nfsexports.List)
			r.Post("/api/auth/nfs-exports", nfsexports.Create)
			r.Post("/api/auth/nfs-exports/{id}/state", nfsexports.SetState)
			r.Delete("/api/auth/nfs-exports/{id}", nfsexports.Delete)

			// Self-service API tokens — any user (incl. non-admin user/viewer) may
			// mint tokens bound to themselves, capped to their role ceiling + own
			// grants (see handlers.SelfTokens). Admins also have /api/admin/ai-tokens.
			r.Get("/api/tokens", selfTokensH.List)
			r.Post("/api/tokens", selfTokensH.Create)
			r.Patch("/api/tokens/{id}", selfTokensH.Update)
			r.Delete("/api/tokens/{id}", selfTokensH.Delete)
		})

		// Desktop authorization, browser half. Session-authenticated: the SPA
		// calls this AFTER the user signed in however this install does it
		// (local, OIDC, passkey), which is the whole point — a native form in
		// the desktop app could never reach an SSO identity.
		r.Post("/api/auth/desktop/complete", desktopAuthH.Complete)

		// Per-user notifications (bell + history + read/unread).
		r.Route("/api/notifications", func(r chi.Router) {
			r.Get("/", notifH.List)
			r.Get("/unread-count", notifH.UnreadCount)
			r.Post("/{id}/read", notifH.MarkRead)
			r.Post("/read-all", notifH.MarkAllRead)
			r.Get("/settings", notifH.GetSettings)
			r.Patch("/settings", notifH.UpdateSettings)
		})

		r.Route("/api/files", func(r chi.Router) {
			// Root confinement: a token's `root:` scope / X-Filex-Root header
			// locks every path-bearing request to one sub-folder (multi-tenant
			// isolation). No-op for unconfined (admin/native) callers.
			r.Use(confine.Middleware)
			// Mint a short-lived WebSocket auth ticket. Lives under /api/files so
			// the embedded webcomponent reaches it through the host's existing
			// proxy (which injects the token); returns {ticket, ws_url} for a
			// direct cross-origin wss:// connection. Confinement is inherited.
			r.Post("/ws-ticket", wsh.Ticket)
			r.Get("/manager", mh.List)
			r.Post("/manager", mh.Mutate)
			r.Get("/manager/trash", trashH.List)
			// What OTHER people shared with me. Sits beside the trash listing
			// because it is the same kind of surface: a virtual folder the
			// navigation panel opens, not a path under a storage.
			r.Get("/manager/shared-with-me", sharedH.SharedWithMe)
			r.Post("/manager/restore", trashH.Restore)
			r.Get("/stat", mh.Stat)
			r.Get("/read", mh.Read)
			// Search — POST is the canonical body-carrying endpoint;
			// GET is provided for the SPA's `?q=` polling form.
			r.Post("/search", sxh.Search)
			r.Get("/search", sxh.Search)

			// Ops queue. POST submits a new op, GET ?status=running
			// returns the polling tray's list. /ops/{id} is the per-row
			// status check used by `opsApi.get`.
			r.Get("/ops", oh.List)
			r.Post("/ops", oh.Submit)
			r.Get("/ops/{id}", oh.Status)

			// SFC's per-verb async endpoints — translate to ops.Submit.
			r.Post("/copy", oh.SubmitCopy)
			r.Post("/move", oh.SubmitMove)
			r.Post("/delete", oh.SubmitDelete)

			// Legacy S3-presigned chunked upload — untouched. It is what the
			// current web client speaks, and it still works wherever the
			// driver is S3.
			r.Post("/upload/init", uh.Init)
			r.Post("/upload/finalize", uh.Finalize)
			r.Post("/upload/abort", uh.Abort)

			// Staged uploads — driver-agnostic and resumable (docs/UPLOADS.md).
			// `begin` is declared before the `{id}` routes so chi cannot route
			// the literal segment into the parameter.
			r.Post("/upload/begin", suh.Begin)
			r.Put("/upload/{id}", suh.Put)
			r.Get("/upload/{id}", suh.Status)
			r.Post("/upload/{id}/commit", suh.Commit)
			r.Delete("/upload/{id}", suh.Abort)

			r.Post("/archive/list", ah.List)
			r.Post("/archive/extract", ah.Extract)
			r.Post("/archive/add", ah.Add)

			r.Get("/share", sh.HandleList)
			r.Post("/share", sh.HandleCreate)
			r.Delete("/share/{id}", sh.HandleDelete)

			// OnlyOffice editor config. The SFC's PreviewModal posts a
			// JSON body when it has the file context handy; the Editor
			// route falls back to GET with `?path=…`. Accept both so the
			// SFC's preview/"Aç" (Open) handoff doesn't 405.
			r.Get("/onlyoffice/config", ooh.Config)
			r.Post("/onlyoffice/config", ooh.Config)

			// Plain-text save target for the SFC's code/markdown editor.
			r.Post("/save-text", saveTextH.Save)

			// Per-file/per-folder permissions panel (RBAC). Owner/admin only —
			// enforced inside the handler, not the route.
			r.Get("/permissions", grantsH.List)
			r.Post("/permissions", grantsH.Create)
			r.Patch("/permissions/{id}", grantsH.Update)
			r.Delete("/permissions/{id}", grantsH.Delete)
			r.Get("/permissions/resolve", grantsH.Resolve)
			r.Get("/permissions/users", grantsH.SearchUsers)
			r.Post("/permissions/invite", grantsH.Invite)
			r.Post("/permissions/share-mail", grantsH.ShareMail)

			// Per-user metadata: tags, starred flag, recently-opened.
			r.Route("/manager/tags", func(r chi.Router) {
				r.Get("/", metaH.GetTags)
				r.Post("/", metaH.SetTags)
				// All distinct tags across every storage (Tagged files page).
				r.Get("/all", metaH.ListAllTags)
			})
			// Nodes carrying a given tag (?tag=…&limit=…).
			r.Get("/manager/tagged", metaH.TaggedNodes)
			r.Route("/manager/star", func(r chi.Router) {
				r.Post("/", metaH.SetStar)
				r.Get("/list", metaH.ListStarred)
			})
			r.Route("/manager/recent", func(r chi.Router) {
				r.Get("/", metaH.ListRecent)
				r.Post("/", metaH.SetRecent)
			})

			/* calisma:d3 comments */
			// Node comments — flat chronological threads on files/folders
			// (v0.6 "Çalışma" (Work)). Read+write = anyone who can SEE the node;
			// delete = author-or-admin (both enforced in the handler).
			cmtH := handlers.NewComments(d.Store)
			cmtH.AttachACL(d.ACL)
			r.Get("/comments", cmtH.List)
			r.Post("/comments", cmtH.Create)
			r.Delete("/comments/{id}", cmtH.Delete)

			/* wiring:e2 */
			// E2E escrow: prove you hold the escrow private key, then the
			// folder's owner is told it was used. Registered even when escrow
			// is off, so the answer is an explaining 404 rather than a bare
			// "no such route".
			e2eH := handlers.NewE2E(d.Store, d.E2EEscrow)
			e2eH.AttachACL(d.ACL)
			r.Post("/e2e/escrow/challenge", e2eH.EscrowChallenge)
			r.Post("/e2e/escrow/used", e2eH.EscrowUsed)

			// Quota — current user's usage + limit.
			r.Get("/quota/me", quotaH.Me)

			// Version history — list + restore. Admin-only HardDelete is
			// mounted under /api/admin/versions/{id} below. The GET takes
			// `?node_id=N`; POST /restore accepts {node_id, version_id,
			// snapshot_current}.
			r.Route("/versions", func(r chi.Router) {
				r.Get("/", versionsH.List)
				r.Post("/restore", versionsH.Restore)
				r.Post("/snapshot", versionsH.Snapshot)
			})
		})
	})

	// ────── admin-only routes ──────
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(true))
		r.Use(auth.RequireAdmin)
		// Scope admin to its tenant (no-op unless multi-tenant mode is on). A
		// tenant-admin then only sees its own storages/users; the supertenant
		// sees all. See docs/MULTI-TENANCY.md.
		r.Use(auth.TenantResolver(d.Store, d.Cfg.MultiTenant))
		// Record every successful mutating admin action. The middleware is
		// otherwise defined but never installed anywhere, which left the
		// Audit page empty even after real changes.
		r.Use(auth.AuditMiddleware(d.Store))

		// Prometheus exposition. Inside the admin group on purpose: filex is
		// routinely on the public internet, and an open /metrics hands out
		// storage names, user counts and traffic shape to anyone who asks.
		// The scrape job authenticates as an admin — docs/METRICS.md has the
		// job config. Not under /api/admin because scrapers and dashboards
		// expect the conventional path.
		r.Handle("/metrics", metrics.Handler())

		r.Route("/api/admin", func(r chi.Router) {
			r.Get("/dashboard", dashH.Get)
			// Duplicate-file report — same (size, etag) grouping (v0.2 "Bul").
			r.Get("/duplicates", handlers.NewDuplicates(d.Store).Report)

			// Driver config contracts — what fields each storage driver
			// needs, straight from the driver registry. Every admin
			// surface that builds a driver config (new storage, edit,
			// replication target) renders from this instead of carrying
			// its own hardcoded field list. See handlers/storage_drivers.go.
			r.Get("/storage-drivers", handlers.NewStorageDrivers().List)

			// Storage plugins — out-of-process drivers the admin installs.
			// Their descriptors show up in /storage-drivers above the moment
			// a plugin is running. See handlers/plugins.go, docs/PLUGINS.md.
			pluginsH := handlers.NewPlugins(d.Plugins, d.Cfg.MultiTenant)
			r.Route("/plugins", func(r chi.Router) {
				r.Get("/", pluginsH.List)
				r.Post("/", pluginsH.Install)
				r.Get("/{id}", pluginsH.Get)
				r.Patch("/{id}", pluginsH.Patch)
				r.Post("/{id}/restart", pluginsH.Restart)
				r.Post("/{id}/upgrade", pluginsH.Upgrade)
				r.Delete("/{id}", pluginsH.Delete)
			})

			r.Route("/storages", func(r chi.Router) {
				r.Get("/", stg.List)
				r.Post("/", stg.Create)
				r.Post("/test", storagesAdmH.Test)
				r.Get("/{id}", stg.Get)
				r.Patch("/{id}", stg.Update)
				r.Delete("/{id}", stg.Delete)
				r.Post("/{id}/sync", stg.TriggerSync)
				r.Get("/{id}/sync-runs", storagesAdmH.SyncRuns)
				r.Get("/{id}/drift", storagesAdmH.Drift)
			})

			// Replication targets — separate entity (backup-only sinks).
			// See handlers/replication_targets.go for the rationale.
			repTargetsH := handlers.NewReplicationTargets(d.Store)
			r.Route("/replication-targets", func(r chi.Router) {
				r.Get("/", repTargetsH.List)
				r.Post("/", repTargetsH.Create)
				r.Get("/{id}", repTargetsH.Get)
				r.Patch("/{id}", repTargetsH.Update)
				r.Delete("/{id}", repTargetsH.Delete)
			})

			r.Route("/users", func(r chi.Router) {
				r.Get("/", ush.List)
				r.Post("/", ush.Create)
				r.Get("/{id}", ush.Get)
				r.Patch("/{id}", ush.Update)
				r.Delete("/{id}", ush.Delete)
				r.Post("/{id}/reset-password", usersAdmH.ResetPassword)
				// Per-user quota, nested where callers look for it first.
				// The flat /api/admin/quota/{user_id} below predates this and
				// still works; handlers/quota.go has documented this nested
				// shape since before it existed, which sent olivov hunting
				// for a provider-quota endpoint that was never there (G2).
				r.Get("/{id}/quota", quotaH.AdminGet)
				r.Post("/{id}/quota", quotaH.AdminSet)
				r.Patch("/{id}/quota", quotaH.AdminSet)
				r.Post("/{id}/quota/recompute", quotaH.AdminRecompute)
			})

			r.Route("/settings", func(r chi.Router) {
				r.Get("/", seth.List)
				r.Patch("/", seth.Update)
				r.Post("/smtp-test", seth.SMTPTest)
				r.Put("/{key}", seth.Set)
			})

			// Protection settings ("Koru" v0.4): trash retention + version
			// keep count + antivirus status, frozen contract for the admin
			// SPA (see handlers/protection.go).
			protH := handlers.NewProtection(d.Store)
			r.Get("/protection", protH.Get)
			r.Patch("/protection", protH.Patch)

			// Tenant lifecycle (multi-tenancy). In multi-tenant mode only the
			// supertenant's admins pass the handler's internal gate.
			r.Route("/providers", func(r chi.Router) {
				r.Get("/", provH.List)
				r.Post("/", provH.Create)
				r.Patch("/{id}", provH.Update)
				r.Delete("/{id}", provH.Delete)
				r.Post("/{id}/storages", provH.LinkStorage)
				r.Delete("/{id}/storages/{storageID}", provH.UnlinkStorage)
			})

			// AI / MCP / FilexClient bearer tokens. POST returns the
			// plaintext token ONCE; only its sha256 hash is stored.
			aiTokensH := handlers.NewAITokens(d.Store, d.ProtocolAuth)
			r.Route("/ai-tokens", func(r chi.Router) {
				r.Get("/", aiTokensH.List)
				r.Post("/", aiTokensH.Create)
				r.Patch("/{id}", aiTokensH.Update)
				r.Delete("/{id}", aiTokensH.Delete)
			})

			// Release awareness / self-upgrade. GET is cached (never touches
			// the network), /check forces a fetch, /apply installs.
			updateH := handlers.NewUpdate(d.Updater)
			r.Get("/update", updateH.Status)
			r.Post("/update/check", updateH.Check)
			r.Post("/update/apply", updateH.Apply)

			// Global RBAC permissions overview — who has what, where.
			r.Get("/grants", grantsH.AdminList)
			r.Delete("/grants/{id}", grantsH.AdminDelete)

			r.Route("/audit", func(r chi.Router) {
				r.Get("/", auditH.List)
			})

			r.Route("/sync-runs", func(r chi.Router) {
				r.Get("/", syncAdmH.List)
				r.Get("/{id}", syncAdmH.Detail)
			})

			r.Route("/shares", func(r chi.Router) {
				r.Get("/", sharesAdmH.List)
				r.Post("/{id}/revoke", sharesAdmH.Revoke)
				r.Delete("/{id}", sharesAdmH.Delete)
			})

			r.Route("/trash", func(r chi.Router) {
				r.Post("/empty", trashH.AdminEmpty)
				r.Delete("/{id}", trashH.Purge)
			})

			r.Route("/quota", func(r chi.Router) {
				r.Get("/{user_id}", quotaH.AdminGet)
				r.Post("/{user_id}", quotaH.AdminSet)
				r.Post("/{user_id}/recompute", quotaH.AdminRecompute)
			})

			r.Route("/versions", func(r chi.Router) {
				r.Delete("/{id}", versionsH.HardDelete)
			})

			r.Route("/external", func(r chi.Router) {
				r.Get("/", externalH.List)
				r.Patch("/{name}", externalH.Update)
				r.Post("/{name}/test", externalH.Test)
			})

			r.Route("/auth-providers", func(r chi.Router) {
				r.Get("/", authProvH.List)
				r.Patch("/{name}", authProvH.Update)
				r.Post("/{name}/test", authProvH.Test)
			})

			r.Route("/search", func(r chi.Router) {
				r.Get("/stats", searchAdmH.Stats)
				r.Post("/rebuild", searchAdmH.Rebuild)
			})

			r.Route("/queue", func(r chi.Router) {
				r.Get("/stats", queueH.Stats)
				r.Get("/", queueH.List)
				r.Get("/{id}", queueH.Get)
				r.Post("/{id}/retry", queueH.Retry)
				r.Delete("/{id}", queueH.Cancel)
			})

			r.Route("/notifications", func(r chi.Router) {
				r.Get("/", notifH.AdminList)
				r.Post("/test", notifH.AdminTest)
				r.Get("/webhook-config", notifH.AdminWebhookConfig)
				r.Patch("/webhook-config", notifH.AdminUpdateWebhookConfig)
			})

			// Webhook v2 targets — multi-destination, event-filtered,
			// HMAC-signed deliveries (migration 00017). The legacy single
			// global webhook stays on /notifications/webhook-config above.
			webhooksAdmH := handlers.NewWebhooksAdmin(d.Store, d.Notify)
			r.Route("/webhooks", func(r chi.Router) {
				r.Get("/", webhooksAdmH.List)
				r.Post("/", webhooksAdmH.Create)
				r.Patch("/{id}", webhooksAdmH.Update)
				r.Delete("/{id}", webhooksAdmH.Delete)
				r.Post("/{id}/test", webhooksAdmH.Test)
			})

			r.Route("/replica", func(r chi.Router) {
				r.Route("/rules", func(r chi.Router) {
					r.Get("/", replicaH.ListRules)
					r.Post("/", replicaH.CreateRule)
					r.Patch("/{id}", replicaH.UpdateRule)
					r.Delete("/{id}", replicaH.DeleteRule)
				})
				r.Route("/failures", func(r chi.Router) {
					r.Get("/", replicaH.ListFailures)
					r.Get("/count", replicaH.CountFailures)
				})
				r.Post("/fix", replicaH.FixAll)
				r.Post("/fix-one", replicaH.FixOne)
				r.Get("/report", replicaH.GetReport)
				r.Post("/report/run-now", replicaH.RunReportNow)
				r.Get("/settings", replicaH.GetSettings)
				r.Patch("/settings", replicaH.UpdateSettings)
			})
		})
	})

	// ────── AI / MCP (token-authenticated) ──────
	// Token-only namespace consumed by AI agents, the work.example.com
	// FilexClient, and MCP clients. auth.APITokenMiddleware validates
	// X-Filex-Token / Bearer and attaches the bound principal + token;
	// RequireScope gates verbs (read/write/delete/mcp). A token with no
	// scopes set grants everything.
	convertURL := func(ctx context.Context) string { return d.External.URL(ctx, external.Convert) }
	aiH := handlers.NewAI(d.Store, d.StorageResolver, d.Share, d.Cfg.PublicURL, convertURL)
	aiH.AttachTenants(tenants)
	// An agent-written file must be searchable at once, not when the next
	// storage sync walks the folder. Nil index is a no-op.
	aiH.AttachSearchIndex(d.Index)
	aiH.AttachACL(d.ACL)
	aiH.AttachThumbs(d.Thumbs)
	aiH.AttachStaged(suh)
	aiH.AttachBody(d.Body)
	aiH.AttachTickets(uploadTickets)
	aiAdmin := handlers.NewAIAdmin(handlers.AIAdminDeps{
		Store:           d.Store,
		Caps:            d.Caps,
		Worker:          d.Worker,
		Queue:           d.Queue,
		Notify:          d.Notify,
		Trash:           d.Trash,
		Index:           d.Index,
		ReplicaService:  d.ReplicaService,
		ReplicaCron:     d.ReplicaCron,
		ReplicaReloader: d.ReplicaReloader,

		External:           d.External,
		EnvManagedExternal: envManagedExternal(d.Cfg),
	})
	aiMCP := handlers.NewAIMCP(d.Store, d.StorageResolver, aiAdmin, d.Share, d.Cfg.PublicURL, convertURL)
	aiMCP.AttachTenants(tenants)
	aiMCP.AttachACL(d.ACL)
	aiMCP.AttachThumbs(d.Thumbs)
	aiMCP.AttachSearchIndex(d.Index)
	aiMCP.AttachStaged(suh)
	aiMCP.AttachBody(d.Body)
	aiMCP.AttachTickets(uploadTickets)
	r.Route("/api/ai", func(r chi.Router) {
		r.Use(auth.APITokenMiddleware(d.Store))
		// Agents are tenant-scoped too — resolve the token user's provider
		// (no-op unless multi-tenant mode is on). See docs/MULTI-TENANCY.md.
		r.Use(auth.TenantResolver(d.Store, d.Cfg.MultiTenant))
		// Attribute every AI write to its token + username in the audit log
		// (reads are GET and never audited). Runs after the token middleware so
		// TokenFrom/TokenUserFrom are on the context.
		r.Use(auth.AuditMiddleware(d.Store))

		// Discovery: any valid token may learn its confinement root + reachable
		// storages (no verb scope needed) so a confined agent stops guessing.
		r.Get("/root", aiH.Root)

		// Read surface.
		r.With(auth.RequireScope("read")).Get("/files", aiH.List)
		r.With(auth.RequireScope("read")).Get("/info", aiH.Info)
		r.With(auth.RequireScope("read")).Get("/download", aiH.Download)
		r.With(auth.RequireScope("read")).Get("/search", aiH.Search)

		// Write surface.
		r.With(auth.RequireScope("write")).Post("/upload", aiH.Upload)
		// Mint a credential-free URL for a file too large to travel inside a
		// call body (the redeem half is the public /u/{ticket} route).
		r.With(auth.RequireScope("write")).Post("/upload/ticket", aiH.UploadTicket)
		r.With(auth.RequireScope("write")).Post("/mkdir", aiH.Mkdir)
		r.With(auth.RequireScope("write")).Post("/move", aiH.Move)
		r.With(auth.RequireScope("delete")).Post("/delete", aiH.Delete)

		// Share surface — public /s/<token> links (folders zip on download).
		r.With(auth.RequireScope("write")).Post("/share", aiH.Share)
		r.With(auth.RequireScope("write")).Post("/unshare", aiH.Unshare)

		// Archive surface — server-side zip/unzip (result lands in storage; the
		// archive bytes never cross the wire — share `dest` to download it).
		r.With(auth.RequireScope("write")).Post("/zip", aiH.Zip)
		r.With(auth.RequireScope("write")).Post("/unzip", aiH.Unzip)

		// MCP streamable HTTP (JSON-RPC). Both POST (requests) and GET
		// (SSE stream open) are part of the transport contract.
		r.With(auth.RequireScope("mcp")).Handle("/mcp", aiMCP)

		// Admin surface — the full admin panel as token-auth REST endpoints.
		// Gated by the `admin` scope; the bound user is then elevated to an
		// admin principal so the reused admin handler logic runs authorized.
		// AuditMiddleware runs AFTER apitoken + RequireScope("admin") so the
		// bound principal is on the context — every successful mutating
		// /api/ai/admin/* write lands in the audit log (action prefixed "ai.").
		r.With(auth.RequireScope("admin"), auth.AuditMiddleware(d.Store)).Route("/admin", aiAdmin.Register)
	})

	// ────── ShareX uploader (token-authenticated) ──────
	// POST a file/image/text from ShareX → stored + indexed via the AI path,
	// a public /s/<token> share is minted, and {"url":"<link>?inline=1"} is
	// returned (inline so images/text render in-browser).
	sxUploadH := handlers.NewShareX(d.Store, d.StorageResolver, d.Share, d.Cfg.PublicURL)
	sxUploadH.AttachTenants(tenants)
	sxUploadH.AttachACL(d.ACL)
	sxUploadH.AttachThumbs(d.Thumbs)
	sxUploadH.AttachSearchIndex(d.Index)
	sxUploadH.AttachStaged(suh)
	sxUploadH.AttachBody(d.Body)
	r.Route("/api/sharex", func(r chi.Router) {
		r.Use(auth.APITokenMiddleware(d.Store))
		r.Use(auth.TenantResolver(d.Store, d.Cfg.MultiTenant))
		r.Use(auth.AuditMiddleware(d.Store))
		r.With(auth.RequireScope("write")).Post("/upload", sxUploadH.Upload)
	})

	// ────── WebDAV (/dav/<storage>/<path>, Basic auth — see internal/dav) ──────
	// The path-prefixed mount, for installs without a dedicated S3 host. A
	// client then needs /s3 in its endpoint URL and cannot use virtual-hosted
	// addressing — which is why the host route above is the real answer.
	r.Mount(s3api.Prefix, s3h)
	r.Mount("/dav", dav.NewHandler(dav.Config{Enabled: d.Cfg.DAV.Enabled, Store: d.Store, Resolver: d.StorageResolver, ACL: d.ACL, Index: d.Index, Thumbs: d.Thumbs, Body: d.Body, Quota: d.Quota, MultiTenant: d.Cfg.MultiTenant, Auth: d.ProtocolAuth, LockDir: filepath.Join(d.Cfg.DataDir, "dav")}))

	// ────── healthz ──────
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// ────── root → admin SPA ──────
	// Bare `/` would otherwise return chi's stock 404. The admin SPA
	// lives at /admin/, so 302 anyone landing on the apex URL there.
	// Demo deployments render a public landing on /admin/login;
	// non-demo deployments render a sign-in form. Either way the SPA
	// owns the user-facing entry.
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/admin/", http.StatusFound)
	})

	// ────── embedded static ──────
	wireStatic(r, d.Embed)

	return r
}

// UserUIPrefix is the neutral, end-user mount point for the same SPA the
// admin panel is served from. See wireStatic and GitHub #14.
const UserUIPrefix = "/drive"

// wireStatic mounts the embedded /admin SPA and the per-asset Web
// Component bundle at /embed.js (+ neighbouring assets).
//
// Layout inside the embed.FS:
//
//	admin/  ← Vite-built Vue 3 admin SPA (index.html + assets/...)
//	web/    ← @brftech/filex Web Component bundle (filex.iife.js +
//	          style.css + LICENSE)
//
// SPA fallback: any /admin/* request that doesn't map to a real file
// falls back to admin/index.html so vue-router's client routes work.
//
// /embed.js + /embed.css + neighbouring map files are served from the
// `web/` subtree so consumers can <script src="/embed.js"> regardless
// of where the iife was actually filed.
func wireStatic(r chi.Router, fs embed.FS) {
	adminFS, err := stripPrefix(fs, "admin")
	if err != nil {
		// embed/admin missing entirely (likely local dev where the
		// frontend hasn't been built). Surface the error so the
		// operator knows to run pnpm build:web.
		r.Get("/admin/*", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "admin SPA not bundled — frontend build missing", http.StatusNotFound)
		})
	} else {
		spa := spaHandler{root: adminFS, urlPrefix: "/admin"}
		r.Handle("/admin", http.RedirectHandler("/admin/", http.StatusMovedPermanently))
		r.Handle("/admin/", spa)
		r.Handle("/admin/*", spa)
		// vue-router carves out a few "shareable" URLs outside the
		// /admin/ prefix so the editor lives at /files/edit?path=…
		// (FileExplorer's `openPageBase` config). These need the same
		// SPA fallback so a fresh browser tab loads index.html and
		// vue-router takes over.
		filesSPA := spaHandler{root: adminFS, urlPrefix: ""}
		r.Handle("/files/edit", filesSPA)
		r.Handle("/files/edit/*", filesSPA)

		// The end-user front door. Same bundle, neutral URL.
		//
		// Reported as GitHub #14: filex already has a browser-first client
		// for ordinary accounts — a non-admin who signs in gets the file
		// manager, not the panel — but it was reached at /admin/explore, so
		// every user of a deployment was told by the address bar that they
		// were inside an administrator's tool. /admin/ is untouched and every
		// existing bookmark still resolves; vue-router picks its history base
		// from whichever prefix served the document (web/src/router/index.ts).
		//
		// ⚠ /drive, not /files: `files` is already a route INSIDE the SPA
		// (the admin file-lookup page, and /files/edit above), so a top-level
		// /files would collide with both.
		userSPA := spaHandler{root: adminFS, urlPrefix: UserUIPrefix}
		r.Handle(UserUIPrefix, http.RedirectHandler(UserUIPrefix+"/", http.StatusMovedPermanently))
		r.Handle(UserUIPrefix+"/", userSPA)
		r.Handle(UserUIPrefix+"/*", userSPA)
	}

	webFS, err := stripPrefix(fs, "web")
	if err != nil {
		r.Get("/embed.js", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "embed.js not bundled — packages/webcomponent build missing", http.StatusNotFound)
		})
		return
	}

	// Web Component bundle. /embed.js + /embed.css are aliases for
	// the entry chunks; everything else (lazy chunks like
	// PdfViewer-*.js, *.map, fonts, …) is served verbatim from
	// /embed/<file>.
	//
	// Vite's lib build emits ES module entry as `filex.js` (not
	// `filex.iife.js`); aliasing keeps consumer pages on the
	// stable /embed.js URL.
	mountWebFile := func(public, internal string) {
		r.Get(public, func(w http.ResponseWriter, _ *http.Request) {
			data, err := webFS.ReadFile(internal)
			if err != nil {
				http.NotFound(w, nil)
				return
			}
			if ct := contentTypeForName(internal); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.Header().Set("Cache-Control", "public, max-age=300")
			_, _ = w.Write(data)
		})
	}
	mountWebFile("/embed.js", "filex.js")
	mountWebFile("/embed.css", "style.css")

	// /embed/<file> — direct file lookup for code-split chunks +
	// source maps. Chunked imports inside filex.js use the entry's
	// own URL as the import.meta.url base, so chunks resolve to
	// /embed/<chunk>.js when /embed.js itself lives at the root.
	// To make that work we ALSO expose chunk basenames at /
	// (Vite's default base; consumers can change it via
	// `<script src="/embed/filex.js">` if they prefer namespacing).
	r.Get("/embed/*", func(w http.ResponseWriter, req *http.Request) {
		rel := strings.TrimPrefix(req.URL.Path, "/embed/")
		if rel == "" || strings.Contains(rel, "..") {
			http.NotFound(w, nil)
			return
		}
		data, err := webFS.ReadFile(rel)
		if err != nil {
			http.NotFound(w, nil)
			return
		}
		if ct := contentTypeForName(rel); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(data)
	})

	// Chunk basenames at the root (e.g. /PdfViewer-B96aE3Uu.js).
	// Vite's default `base: '/'` lib build emits chunk URLs as
	// "/<chunk>.js" relative to the document; without these the
	// browser fetches them from the host page's root and 404s.
	// We only honor the hashed-filename convention so we don't
	// shadow real routes.
	r.Get("/{chunk:[A-Za-z0-9_]+-[A-Za-z0-9_-]+\\.(js|css)}", func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "chunk")
		data, err := webFS.ReadFile(name)
		if err != nil {
			http.NotFound(w, nil)
			return
		}
		if ct := contentTypeForName(name); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(data)
	})
}

// spaHandler serves files under root with an index.html fallback.
type spaHandler struct {
	root      *embedSubFS
	urlPrefix string
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Strip the URL prefix to get a path inside admin/.
	rel := strings.TrimPrefix(r.URL.Path, h.urlPrefix)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		rel = "index.html"
	}

	// Try the requested file; fall through to index.html for SPA routes.
	data, err := h.root.ReadFile(rel)
	if err != nil {
		// .map / .json missing → 404 (don't return index.html for these
		// or the browser tries to parse HTML as JS).
		if hasAssetExt(rel) {
			http.NotFound(w, r)
			return
		}
		data, err = h.root.ReadFile("index.html")
		if err != nil {
			http.Error(w, "admin SPA missing index.html", http.StatusInternalServerError)
			return
		}
		rel = "index.html"
	}

	ct := contentTypeForName(rel)
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// Hashed Vite assets get long cache; everything else (index.html,
	// favicon) stays short-lived so a redeploy is picked up promptly.
	if strings.HasPrefix(rel, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	_, _ = w.Write(data)
}

// embedSubFS is a thin wrapper around embed.FS that prepends a directory
// prefix to every ReadFile call. We can't use fs.Sub because embed.FS's
// reflection layer doesn't compose cleanly here — a manual wrapper is
// 6 lines and gives us the path-strip behavior the SPA handler needs.
type embedSubFS struct {
	root   embed.FS
	prefix string
}

func stripPrefix(fs embed.FS, prefix string) (*embedSubFS, error) {
	// Probe: does the prefix exist + contain at least one entry?
	entries, err := fs.ReadDir(prefix)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, &emptyEmbedErr{prefix: prefix}
	}
	return &embedSubFS{root: fs, prefix: prefix}, nil
}

func (e *embedSubFS) ReadFile(name string) ([]byte, error) {
	return e.root.ReadFile(e.prefix + "/" + name)
}

type emptyEmbedErr struct{ prefix string }

func (e *emptyEmbedErr) Error() string {
	return "embed/" + e.prefix + " is empty (frontend build missing)"
}

// contentTypeForName picks a sensible Content-Type. We deliberately
// avoid net/http's DetectContentType for .js + .css because it returns
// text/plain for those, which breaks ESM in modern browsers.
func contentTypeForName(name string) string {
	ext := name[strings.LastIndex(name, ".")+1:]
	switch strings.ToLower(ext) {
	case "html":
		return "text/html; charset=utf-8"
	case "css":
		return "text/css; charset=utf-8"
	case "js", "mjs":
		return "application/javascript; charset=utf-8"
	case "json":
		return "application/json"
	case "webmanifest":
		// ⚠ Named explicitly rather than left to the sniffer. With no
		// Content-Type set, net/http falls back to http.DetectContentType,
		// which sees JSON text and answers `text/plain; charset=utf-8` —
		// measured on the built binary. Chrome installs the PWA anyway, but
		// the header is wrong, a strict client is entitled to refuse it, and
		// 90-pwa-install.cy.ts had to weaken its assertion to a "not HTML"
		// check because the answer looked host-dependent. It is not, now: it
		// is this line.
		return "application/manifest+json"
	case "svg":
		return "image/svg+xml"
	case "woff":
		return "font/woff"
	case "woff2":
		return "font/woff2"
	case "ttf":
		return "font/ttf"
	case "ico":
		return "image/x-icon"
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "map":
		return "application/json"
	}
	return ""
}

// hasAssetExt reports whether the path looks like a static asset
// reference (so the SPA handler does NOT fall through to index.html
// for missing ones).
func hasAssetExt(name string) bool {
	// ⚠ `.webmanifest` belongs here for the same reason as `.json`: without it
	// a missing manifest falls through to index.html and the browser is handed
	// HTML with a 200, which reads as a corrupt manifest rather than a missing
	// file. That is the break 90-pwa-install.cy.ts guards against.
	for _, ext := range []string{".js", ".css", ".map", ".json", ".webmanifest", ".png", ".jpg", ".jpeg", ".svg", ".webp", ".ico", ".woff", ".woff2", ".ttf"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			return true
		}
	}
	return false
}

// sftpHost is the hostname a client should be pointed at: the deployment's own,
// taken from the public URL rather than invented.
func sftpHost(publicURL string) string {
	u, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}

// sftpPort is the TCP port the endpoint listens on.
//
// ⚠ It is NOT the web port. SFTP is raw TCP on a port of its own (2022 by
// convention), and a guide that printed 443 would send every client to a
// reverse proxy that speaks only HTTP.
func sftpPort(addr string) int { return portOf(addr, 2022) }

// portOf reads the port out of a listen address, falling back to a protocol's
// conventional one. ⚠ Port 0 is not a port — it means "pick one" — so it falls
// back rather than being printed in an instruction.
func portOf(addr string, fallback int) int {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fallback
	}
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		if n, err := strconv.Atoi(addr[i+1:]); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// ftpsFacts describes the FTPS endpoint to the connection guide.
//
// ⚠ Computed here rather than in the page: the passive port range and whether
// the certificate is self-signed are server facts, and a UI that guessed at
// either would produce instructions that fail as a hang (blocked passive
// ports) or as an unexplained client warning (an unverified certificate).
func ftpsFacts(cfg config.Config, live func() string) handlers.FTPSFacts {
	addr := strings.TrimSpace(cfg.FTPS.Addr)
	// The listener's own address wins: with `:0` the configured value names no
	// port at all, and even with a fixed one the running server is the only
	// thing that knows what actually bound.
	if live != nil {
		if a := strings.TrimSpace(live()); a != "" {
			addr = a
		}
	}
	f := handlers.FTPSFacts{
		Enabled:    cfg.FTPS.Enabled,
		Host:       sftpHost(cfg.PublicURL),
		Port:       portOf(addr, 2121),
		PasvMin:    cfg.FTPS.PassivePortMin,
		PasvMax:    cfg.FTPS.PassivePortMax,
		SelfSigned: cfg.FTPS.CertFile == "" || cfg.FTPS.KeyFile == "",
	}
	if cfg.FTPS.PublicHost != "" {
		f.Host = cfg.FTPS.PublicHost
	}
	if f.PasvMin == 0 || f.PasvMax == 0 {
		f.PasvMin, f.PasvMax = 30000, 30100
	}
	return f
}

// envManagedExternal reports which external services are PINNED by env/YAML.
//
// A pinned service is re-asserted onto its `external_services` row at every
// boot (server.seedExternalDefaults), so an edit made in the admin UI applies
// live but is reverted the next time filex starts. The admin API says so
// instead of letting the operator discover it after a restart — the whole point
// of issue #17 was a UI that looked like it had saved something it had not.
func envManagedExternal(cfg config.Config) map[string]bool {
	return map[string]bool{
		external.OnlyOffice: cfg.ExternalServices.OnlyOffice.URL != "",
		external.Drawio:     cfg.ExternalServices.Drawio.URL != "",
		external.Convert:    cfg.ExternalServices.Convert.URL != "",
	}
}
