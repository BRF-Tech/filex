// Package api wires the HTTP routes onto a chi router.
//
// All handlers receive a Deps struct so they have access to the same
// shared services (Store, Worker, Search, Capability, Pipeline). New
// handlers should be added here in BuildRouter — never spawned via
// init() or globals.
package api

import (
	"embed"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/replica"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// Deps is the bundle of services every handler needs.
type Deps struct {
	Cfg             config.Config
	Store           db.Store
	Worker          *syncpkg.Worker
	Index           *search.Index
	Caps            *capability.Service
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
	Embed           embed.FS // web/dist + admin
	LocalAuth       auth.LoginDriver
	OIDCAuth        auth.OIDCDriver
	// ACL resolves per-user/per-item grants (RBAC feature). Constructed in
	// BuildRouter from Store when nil.
	ACL *acl.Resolver
	// Mailer sends invite/share notices (optional; nil → links shown on-screen).
	Mailer *mailer.Service
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
	ah := handlers.NewArchive(d.Store, d.StorageResolver)
	ah.AttachACL(d.ACL)
	sh := handlers.NewShare(d.Share, d.Store, d.StorageResolver, d.Cfg.PublicURL)
	sh.AttachACL(d.ACL)
	// File-drop (public upload link) handler — the inverse of Share. Reuses
	// the manager's ingest path (IngestFile/EnsureDir) so dropped files land
	// exactly like authenticated uploads (mime, node cache, thumbnails).
	dh := handlers.NewDrop(d.Store, mh, d.Share, d.Notify, d.Mailer, d.Cfg.PublicURL)
	oh := handlers.NewOps(d.Ops, d.Store)
	oh.AttachACL(d.ACL)
	if d.Ops != nil {
		// The async ops worker must mirror its filesystem moves/deletes/copies
		// into the DB node index (listings read the DB). The manager handler
		// owns that DB logic, so inject it as the worker's DBSync hook —
		// without this, async move/delete/copy don't reflect in the UI.
		d.Ops.SetSync(mh)
	}
	ooh := handlers.NewOnlyOffice(d.OnlyOffice, d.Store, d.StorageResolver)
	ooh.AttachACL(d.ACL)
	th := handlers.NewThumb(d.Store, d.Thumbs)
	ch := handlers.NewCapabilities(d.Caps, d.Store, d.Cfg.MultiTenant)
	stg := handlers.NewStorages(d.Store, d.Worker)
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
	externalH := handlers.NewExternalAdmin(d.Store, d.Caps)
	authProvH := handlers.NewAuthProviders(d.Store)
	storagesAdmH := handlers.NewStoragesAdmin(d.Store)
	usersAdmH := handlers.NewUsersAdmin(d.Store)
	searchAdmH := handlers.NewSearchAdmin(d.Index, d.Store)
	queueH := handlers.NewQueue(d.Queue)
	notifH := handlers.NewNotifications(d.Notify)
	replicaH := handlers.NewReplica(d.Store, d.ReplicaService, d.ReplicaCron, d.ReplicaReloader)
	trashH := handlers.NewTrash(d.Trash, d.Store)
	trashH.AttachACL(d.ACL)
	metaH := handlers.NewMeta(d.Store)
	quotaH := handlers.NewQuota(d.Quota)
	saveTextH := handlers.NewSaveText(d.Store, d.StorageResolver)
	saveTextH.AttachACL(d.ACL)
	if d.Versions != nil {
		// Snapshot the pre-edit bytes into version history before
		// every save-text write (Burak: "değişiklik sonrası sürüm
		// geçmişine bir bok gelmedi" — handler never tapped the
		// versioning service).
		saveTextH.AttachVersions(d.Versions)
	}
	versionsH := handlers.NewVersions(d.Store, d.Versions)
	grantsH := handlers.NewGrants(d.Store, d.ACL)
	grantsH.AttachInvite(d.Share, d.Mailer, d.Cfg.PublicURL)
	selfTokensH := handlers.NewSelfTokens(d.Store, d.ACL)

	// ────── public viewer ──────
	r.Get("/api/files/share/{token}", sh.HandleMetadata)
	r.Get("/s/{token}", sh.HandleDownload)
	r.Post("/s/{token}", sh.HandleDownload) // PIN form posts to same URL

	// ────── public file-drop (upload link) ──────
	// GET renders the upload page (PIN gate first when protected); POST is
	// both the PIN-form submit and the multipart drop. No auth: the target
	// folder is resolved server-side from the token, and existing contents
	// are never listed ("blind drop").
	r.Get("/d/{token}", dh.Page)
	r.Post("/d/{token}", dh.Upload)

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
	})

	// ────── thumbs (auth-light: signed URL accepted without session) ──────
	r.Get("/api/files/thumb/{id}", th.Serve)

	// ────── public capabilities ──────
	// Embedders + the SPA both call /api/files/capabilities; keep the
	// historical /api/capabilities working for older callers, but make
	// the file-namespaced path the documented one.
	r.Get("/api/capabilities", ch.Get)
	r.Get("/api/files/capabilities", ch.Get)

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

		// Self-service API tokens — any user (incl. non-admin user/viewer) may
		// mint tokens bound to themselves, capped to their role ceiling + own
		// grants (see handlers.SelfTokens). Admins also have /api/admin/ai-tokens.
		r.Get("/api/tokens", selfTokensH.List)
		r.Post("/api/tokens", selfTokensH.Create)
		r.Delete("/api/tokens/{id}", selfTokensH.Delete)

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
			r.Get("/manager", mh.List)
			r.Post("/manager", mh.Mutate)
			r.Get("/manager/trash", trashH.List)
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

			r.Post("/upload/init", uh.Init)
			r.Post("/upload/finalize", uh.Finalize)
			r.Post("/upload/abort", uh.Abort)

			r.Post("/archive/list", ah.List)
			r.Post("/archive/extract", ah.Extract)
			r.Post("/archive/add", ah.Add)

			r.Get("/share", sh.HandleList)
			r.Post("/share", sh.HandleCreate)
			r.Delete("/share/{id}", sh.HandleDelete)

			// OnlyOffice editor config. The SFC's PreviewModal posts a
			// JSON body when it has the file context handy; the Editor
			// route falls back to GET with `?path=…`. Accept both so the
			// SFC's preview/Aç handoff doesn't 405.
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

			// Quota — current user's usage + limit.
			r.Get("/quota/me", quotaH.Me)

			// Version history — list + restore. Admin-only HardDelete is
			// mounted under /api/admin/versions/{id} below. The GET takes
			// `?node_id=N`; POST /restore accepts {node_id, version_id,
			// snapshot_current}.
			r.Route("/versions", func(r chi.Router) {
				r.Get("/", versionsH.List)
				r.Post("/restore", versionsH.Restore)
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

		r.Route("/api/admin", func(r chi.Router) {
			r.Get("/dashboard", dashH.Get)

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
			})

			r.Route("/settings", func(r chi.Router) {
				r.Get("/", seth.List)
				r.Patch("/", seth.Update)
				r.Post("/smtp-test", seth.SMTPTest)
				r.Put("/{key}", seth.Set)
			})

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
			aiTokensH := handlers.NewAITokens(d.Store)
			r.Route("/ai-tokens", func(r chi.Router) {
				r.Get("/", aiTokensH.List)
				r.Post("/", aiTokensH.Create)
				r.Delete("/{id}", aiTokensH.Delete)
			})

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
	// Token-only namespace consumed by AI agents, the work.brf.sh
	// FilexClient, and MCP clients. auth.APITokenMiddleware validates
	// X-Filex-Token / Bearer and attaches the bound principal + token;
	// RequireScope gates verbs (read/write/delete/mcp). A token with no
	// scopes set grants everything.
	aiH := handlers.NewAI(d.Store, d.StorageResolver, d.Share, d.Cfg.PublicURL, d.Cfg.ExternalServices.Convert.URL)
	aiH.AttachACL(d.ACL)
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
	})
	aiMCP := handlers.NewAIMCP(d.Store, d.StorageResolver, aiAdmin, d.Share, d.Cfg.PublicURL, d.Cfg.ExternalServices.Convert.URL)
	aiMCP.AttachACL(d.ACL)
	r.Route("/api/ai", func(r chi.Router) {
		r.Use(auth.APITokenMiddleware(d.Store))
		// Agents are tenant-scoped too — resolve the token user's provider
		// (no-op unless multi-tenant mode is on). See docs/MULTI-TENANCY.md.
		r.Use(auth.TenantResolver(d.Store, d.Cfg.MultiTenant))

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
	for _, ext := range []string{".js", ".css", ".map", ".json", ".png", ".jpg", ".jpeg", ".svg", ".webp", ".ico", ".woff", ".woff2", ".ttf"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			return true
		}
	}
	return false
}
