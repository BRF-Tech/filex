package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/archivecli"
	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/external"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/pluginreq"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/replica"
	"github.com/brf-tech/filex/backend/internal/search"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// AIAdmin exposes the full admin panel over the token-authenticated AI
// surface — both as REST endpoints under /api/ai/admin/* and as admin_*
// MCP tools — so a single bearer token (scope `admin`) lets an AI agent
// drive users, storages, settings, replica, queue, notifications, audit …
// exactly like the native /admin SPA does.
//
// Design: AIAdmin owns one instance of every existing admin handler
// (Dashboard, Users, Storages, …). The REST routes mount those handler
// methods directly behind an admin-elevating middleware (chi fills the
// URL params, the body/query pass straight through). The MCP tools invoke
// the very same handler methods IN-PROCESS via `invoke` — a synthetic
// request + buffered recorder, no network round-trip, no logic duplicated.
//
// Authorization: the admin surface is gated by RequireScope("admin") at
// the route / getServer level. Once past that gate the bound user is
// elevated to an admin principal (elevatedPrincipal) so the underlying
// handler logic runs with an admin-authorized context — we deliberately
// call the handler logic directly rather than the RequireAdmin-wrapped
// HTTP route.
type AIAdmin struct {
	store db.Store

	// demoMode: a public playground - the admin tools' writes are refused
	// (invoke), as api.DemoGuard refuses the routes'.
	demoMode bool

	dash        *Dashboard
	settings    *Settings
	users       *Users
	usersAdm    *UsersAdmin
	storages    *Storages
	storagesAdm *StoragesAdmin
	syncAdm     *SyncAdmin
	sharesAdm   *SharesAdmin
	trash       *Trash
	searchAdm   *SearchAdmin
	authProv    *AuthProviders
	external    *ExternalAdmin
	replica     *Replica
	repTargets  *ReplicationTargets
	queue       *Queue
	notif       *Notifications
	audit       *Audit
	loginSec    *LoginSecurity
	grants      *Grants
	// Plugins: READ and REQUEST only (plugin_requests.go). The install,
	// upgrade, remove, switch and approve handlers are deliberately not
	// mounted here and have no tool — they need a signed-in administrator.
	plugins    *Plugins
	appPlugins *AppPluginsAdmin
	pluginReqs *PluginRequests
	// tenants is the tenant lifecycle (handlers/providers.go): the same
	// handler as Admin → Tenants, platform operator only, in-handler.
	tenants *Providers
	// fileTypes: READ only - which app opens and draws which kind. A change
	// needs an administrator signed in to the panel (file_types_admin.go).
	fileTypes *FileTypesAdmin
	// webhooks: the webhook TARGETS (v2, Admin → Notifications → Webhooks),
	// beside the older single webhook config notif carries.
	webhooks *WebhooksAdmin
	// protection: trash retention, versions kept, link lifetime, drafts and
	// antivirus - the Protection page's handler, with its validation.
	protection *Protection
	// archives: the archive engine's settings (formats, limits) - the Archives
	// page's handler. Nil when no engine is wired.
	archives *ArchiveAdmin
}

// AIAdminDeps carries the shared services the wrapped admin handlers need.
// Mirrors the subset of api.Deps relevant to the admin surface (declared
// here to avoid an api→handlers→api import cycle).
type AIAdminDeps struct {
	Store           db.Store
	Caps            *capability.Service
	Worker          *syncpkg.Worker
	Queue           queue.Driver
	Notify          notify.Service
	Trash           *trash.Service
	Ops             *ops.Service
	Index           *search.Index
	ReplicaService  *replica.Service
	ReplicaCron     *replica.CronScheduler
	ReplicaReloader *replica.RulesReloader
	// External + EnvManagedExternal mirror what the native /admin routes get,
	// so the MCP admin surface reports and applies external-service changes the
	// same way the UI does.
	External           *external.Resolver
	EnvManagedExternal map[string]bool
	// DemoMode marks a public playground, so the wrapped handlers redact the
	// same things they redact on the native /admin routes. The token surface
	// is not a way around a demo's rules.
	DemoMode bool
	// PublicURL + PublicURLSet feed the external-service advisories: the MCP
	// admin surface must report the same three-address picture the UI does,
	// or an agent reading it back gets the narrower answer the UI stopped
	// giving (issue #17).
	PublicURL    string
	PublicURLSet bool
	// ReversePath is the panel's third-leg check (ExternalAdmin.ReversePath),
	// so admin_external_test measures the document server's route back to
	// filex and its JWT the way the page's Test does. Nil = not measured.
	ReversePath func(ctx context.Context, t *onlyoffice.Target) onlyoffice.ReverseResult
	// AuthLive is the running set of sign-in providers, so the admin MCP
	// tools change sign-in through the same handler, with the same guards, as
	// the page (handlers/auth_providers.go).
	AuthLive *authsetup.Live
	// Plugins / AppPlugins / PluginRequests back the plugin tools: reading
	// the installed plugins and leaving install requests. Nil = that kind is
	// off (the handlers answer 503, as on the panel's routes).
	Plugins                  *plugin.Manager
	AppPlugins               *wasmplugin.Registry
	AppPluginsDisabledReason string
	PluginRequests           *pluginreq.Service
	// Assoc backs the Default apps tool (read only): which app opens and
	// draws which kind of file. Nil = app plugins are off.
	Assoc *assoc.Service
	// LoginGuard + EnvTrustedProxies back the sign-in security tools: the same
	// handler the panel uses, over the same running limiter.
	LoginGuard        *loginguard.Guard
	EnvTrustedProxies string
	// MultiTenant mirrors FILEX_MULTI_TENANT for the tenant tools: the list
	// says whether tenants can sign in at all (maintenance mode when off).
	MultiTenant bool
	// ArchiveEngine backs the archive settings tools: the same engine the
	// panel's Archives page reads and probes. Nil = those tools answer 503.
	ArchiveEngine *archivecli.Service
}

// newTrashWithOps is the trash handler with the queue "empty the trash now"
// runs on, so the MCP tool starts the same ops job the page does.
func newTrashWithOps(svc *trash.Service, store db.Store, o *ops.Service) *Trash {
	h := NewTrash(svc, store)
	h.AttachOps(o)
	return h
}

// NewAIAdmin constructs the admin AI surface from shared deps. Each wrapped
// handler is the same type the native /admin routes use, so behaviour is
// identical — only the auth front-door differs.
func NewAIAdmin(d AIAdminDeps) *AIAdmin {
	return &AIAdmin{
		store:       d.Store,
		demoMode:    d.DemoMode,
		dash:        newDemoAwareDashboard(d),
		settings:    newDemoAwareSettings(d),
		users:       NewUsers(d.Store),
		usersAdm:    NewUsersAdmin(d.Store),
		storages:    newDemoAwareStorages(d),
		storagesAdm: NewStoragesAdmin(d.Store),
		syncAdm:     NewSyncAdmin(d.Store),
		sharesAdm:   NewSharesAdmin(d.Store),
		trash:       newTrashWithOps(d.Trash, d.Store, d.Ops),
		searchAdm:   NewSearchAdmin(d.Index, d.Store),
		authProv:    newDemoAwareAuthProviders(d),
		external:    newExternalAdminWithPublicURL(d),
		replica:     NewReplica(d.Store, d.ReplicaService, d.ReplicaCron, d.ReplicaReloader),
		repTargets:  NewReplicationTargets(d.Store),
		queue:       newQueueWithStore(d.Queue, d.Store),
		notif:       NewNotifications(d.Notify, d.Store, acl.New(d.Store)),
		audit:       newDemoAwareAudit(d),
		loginSec:    newDemoAwareLoginSecurity(d),
		grants:      NewGrants(d.Store, acl.New(d.Store)),
		plugins:     NewPlugins(d.Plugins, false),
		appPlugins:  NewAppPluginsAdmin(d.AppPlugins, d.AppPluginsDisabledReason),
		pluginReqs:  newAIPluginRequests(d),
		tenants:     newDemoAwareProviders(d),
		fileTypes:   NewFileTypesAdmin(d.Assoc, false),
		webhooks:    NewWebhooksAdmin(d.Store, d.Notify),
		protection:  NewProtection(d.Store),
		archives:    newAIArchiveAdmin(d),
	}
}

// newAIArchiveAdmin is the Archives page's handler over the panel's engine,
// nil when no engine is wired (archivesOr answers 503 then).
func newAIArchiveAdmin(d AIAdminDeps) *ArchiveAdmin {
	if d.ArchiveEngine == nil {
		return nil
	}
	return NewArchiveAdmin(d.Store, d.ArchiveEngine)
}

// archivesOr is the archive settings handler pick names, or a 503 when this
// server has no archive engine.
func (a *AIAdmin) archivesOr(pick func(*ArchiveAdmin) http.HandlerFunc) http.HandlerFunc {
	if a.archives == nil {
		return func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "archive settings are not available on this server"})
		}
	}
	return pick(a.archives)
}

// newAIPluginRequests is the request handler the admin tools use: the
// panel's service when one is wired, else one built from the same parts.
func newAIPluginRequests(d AIAdminDeps) *PluginRequests {
	svc := d.PluginRequests
	if svc == nil {
		svc = pluginreq.New(pluginreq.Options{Store: d.Store, Apps: d.AppPlugins, Plugins: d.Plugins, Notify: d.Notify})
	}
	return NewPluginRequests(svc)
}

// elevatedPrincipal returns an admin-authorized principal for in-process
// admin calls. The `admin` token scope — not the bound user's DB role — is
// what authorizes the AI admin surface, so we present an admin principal to
// the underlying handler logic. The bound user's id is preserved (valid FK
// for any audit/ownership write); only the role is lifted to admin.
func (a *AIAdmin) elevatedPrincipal(u *model.User) *model.User {
	if u != nil && u.IsAdmin() {
		return u
	}
	cp := model.User{Role: model.RoleAdmin, Email: "ai-admin-token", Enabled: true}
	if u != nil {
		cp = *u
		cp.Role = model.RoleAdmin
	}
	return &cp
}

// elevate is the REST middleware that lifts the token's bound user to an
// admin principal for the wrapped handlers. Runs after RequireScope("admin")
// so it only ever fires for genuinely admin-scoped tokens.
func (a *AIAdmin) elevate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFrom(r.Context())
		if u == nil || !u.IsAdmin() {
			r = r.WithContext(auth.WithUser(r.Context(), a.elevatedPrincipal(u)))
		}
		next.ServeHTTP(w, r)
	})
}

// Register wires every admin REST route onto r (mounted at /api/ai/admin by
// the caller). chi populates {id}/{name}/{key} URL params automatically and
// the request body/query stream straight into the reused handler methods.
func (a *AIAdmin) Register(r chi.Router) {
	r.Use(a.elevate)

	r.Get("/dashboard", a.dash.Get)

	r.Route("/settings", func(r chi.Router) {
		r.Get("/", a.settings.List)
		r.Patch("/", a.settings.Update)
		r.Put("/{key}", a.settings.Set)
	})

	r.Route("/users", func(r chi.Router) {
		r.Get("/", a.users.List)
		r.Post("/", a.users.Create)
		r.Get("/{id}", a.users.Get)
		r.Patch("/{id}", a.users.Update)
		r.Delete("/{id}", a.users.Delete)
		r.Post("/{id}/reset-password", a.usersAdm.ResetPassword)
	})

	r.Route("/storages", func(r chi.Router) {
		r.Get("/", a.storages.List)
		r.Post("/", a.storages.Create)
		r.Post("/test", a.storagesAdm.Test)
		r.Get("/{id}", a.storages.Get)
		r.Patch("/{id}", a.storages.Update)
		r.Delete("/{id}", a.storages.Delete)
		r.Post("/{id}/sync", a.storages.TriggerSync)
		r.Get("/{id}/sync-runs", a.storagesAdm.SyncRuns)
		r.Get("/{id}/drift", a.storagesAdm.Drift)
		// The order storages are listed in (issue #57); static, so chi
		// matches it before /{id}.
		r.Put("/order", a.storages.SetOrder)
	})

	r.Route("/sync-runs", func(r chi.Router) {
		r.Get("/", a.syncAdm.List)
		r.Get("/{id}", a.syncAdm.Detail)
	})

	r.Route("/shares", func(r chi.Router) {
		r.Get("/", a.sharesAdm.List)
		r.Post("/{id}/revoke", a.sharesAdm.Revoke)
		r.Delete("/{id}", a.sharesAdm.Delete)
	})

	r.Route("/trash", func(r chi.Router) {
		r.Get("/", a.trash.List)
		r.Post("/restore", a.trash.Restore)
		r.Post("/empty", a.trash.AdminEmpty)
		r.Get("/empty", a.trash.EmptyStatus)
		r.Delete("/{id}", a.trash.Purge)
	})

	r.Route("/search", func(r chi.Router) {
		r.Get("/stats", a.searchAdm.Stats)
		r.Post("/rebuild", a.searchAdm.Rebuild)
	})

	r.Route("/auth-providers", func(r chi.Router) {
		r.Get("/", a.authProv.List)
		r.Post("/", a.authProv.Create)
		r.Patch("/{name}", a.authProv.Update)
		r.Delete("/{name}", a.authProv.Delete)
		r.Put("/{name}/tenants", a.authProv.SetTenants)
		r.Post("/{name}/test", a.authProv.Test)
	})

	r.Route("/external", func(r chi.Router) {
		r.Get("/", a.external.List)
		r.Patch("/{name}", a.external.Update)
		r.Post("/{name}/test", a.external.Test)
	})

	r.Route("/replica", func(r chi.Router) {
		r.Route("/rules", func(r chi.Router) {
			r.Get("/", a.replica.ListRules)
			r.Post("/", a.replica.CreateRule)
			r.Patch("/{id}", a.replica.UpdateRule)
			r.Delete("/{id}", a.replica.DeleteRule)
		})
		r.Route("/failures", func(r chi.Router) {
			r.Get("/", a.replica.ListFailures)
			r.Get("/count", a.replica.CountFailures)
		})
		r.Post("/fix", a.replica.FixAll)
		r.Post("/fix-one", a.replica.FixOne)
		r.Get("/report", a.replica.GetReport)
		r.Post("/report/run-now", a.replica.RunReportNow)
		r.Get("/settings", a.replica.GetSettings)
		r.Patch("/settings", a.replica.UpdateSettings)
	})

	r.Route("/replication-targets", func(r chi.Router) {
		r.Get("/", a.repTargets.List)
		r.Post("/", a.repTargets.Create)
		r.Get("/{id}", a.repTargets.Get)
		r.Patch("/{id}", a.repTargets.Update)
		r.Delete("/{id}", a.repTargets.Delete)
	})

	r.Route("/queue", func(r chi.Router) {
		r.Get("/stats", a.queue.Stats)
		r.Get("/", a.queue.List)
		r.Get("/{id}", a.queue.Get)
		r.Post("/{id}/retry", a.queue.Retry)
		r.Delete("/{id}", a.queue.Cancel)
	})

	r.Route("/notifications", func(r chi.Router) {
		r.Get("/", a.notif.AdminList)
		r.Post("/test", a.notif.AdminTest)
		r.Get("/webhook-config", a.notif.AdminWebhookConfig)
		r.Patch("/webhook-config", a.notif.AdminUpdateWebhookConfig)
		r.Post("/{id}/read", a.notif.MarkRead)
	})

	// Webhook targets (v2): the platform operator's, asked in the handler
	// (requireSupertenant), as on /api/admin/webhooks.
	r.Route("/webhooks", func(r chi.Router) {
		r.Get("/", a.webhooks.List)
		r.Post("/", a.webhooks.Create)
		r.Patch("/{id}", a.webhooks.Update)
		r.Delete("/{id}", a.webhooks.Delete)
		r.Post("/{id}/test", a.webhooks.Test)
	})

	// Protection and archive settings: the panel's handlers, with their
	// validation - not admin_settings_set's raw keys.
	r.Get("/protection", a.protection.Get)
	r.Patch("/protection", a.protection.Patch)
	r.Get("/archives", a.archivesOr(func(h *ArchiveAdmin) http.HandlerFunc { return h.Get }))
	r.Patch("/archives", a.archivesOr(func(h *ArchiveAdmin) http.HandlerFunc { return h.Patch }))
	r.Post("/archives/test", a.archivesOr(func(h *ArchiveAdmin) http.HandlerFunc { return h.Test }))

	r.Route("/audit", func(r chi.Router) {
		r.Get("/", a.audit.List)
	})

	// Sign-in security: settings, locks, the sign-in trail.
	r.Route("/login-security", func(r chi.Router) {
		r.Get("/", a.loginSec.Get)
		r.Patch("/", a.loginSec.Patch)
		r.Get("/locks", a.loginSec.Locks)
		r.Post("/unlock", a.loginSec.Unlock)
		r.Get("/attempts", a.loginSec.Attempts)
	})

	// RBAC grants — the elevated admin principal is owner-exempt, so Create's
	// requireOwner passes and it can set any grant.
	r.Route("/grants", func(r chi.Router) {
		r.Get("/", a.grants.AdminList)
		r.Post("/", a.grants.Create)
		r.Delete("/{id}", a.grants.AdminDelete)
		// A group's grant (PR #78) is numbered apart from a person's: the
		// same id can name both, so it has its own route, as on /api/admin.
		r.Delete("/groups/{id}", a.grants.AdminDeleteGroup)
	})

	// Plugins: read them, and leave install requests. ⚠⚠ Nothing here
	// installs, upgrades, removes, switches or approves — those need an
	// administrator signed in to the panel (plugin_session_gate.go), and a
	// key that could approve its own request would be the old hole again.
	r.Route("/plugins", func(r chi.Router) {
		r.Get("/", a.plugins.List)
		r.Post("/updates/check", a.plugins.CheckUpdates)
		r.Get("/{id}", a.plugins.Get)
		r.Get("/{id}/logs", a.plugins.Logs)
	})
	r.Route("/app-plugins", func(r chi.Router) {
		r.Get("/", a.appPlugins.List)
		r.Post("/updates/check", a.appPlugins.CheckUpdates)
		r.Get("/{id}", a.appPlugins.Get)
		r.Get("/{id}/logs", a.appPlugins.Logs)
		// The files apps have frozen (a document out for signature), and
		// lifting one by force - audited as app_plugin.unlock. Static, so chi
		// matches it before /{id}.
		r.Get("/locks", a.appPlugins.Locks)
		r.Delete("/locks", a.appPlugins.Unlock)
	})
	r.Route("/plugin-requests", func(r chi.Router) {
		r.Get("/", a.pluginReqs.List)
		r.Post("/", a.pluginReqs.Create)
		r.Get("/{id}", a.pluginReqs.Get)
	})

	// Tenants (providers): the platform operator's, asked inside the handler
	// (requireSupertenant), so a tenant administrator's token is refused here
	// exactly as on /api/admin/providers.
	r.Route("/providers", func(r chi.Router) {
		r.Get("/", a.tenants.List)
		r.Post("/", a.tenants.Create)
		r.Get("/realm-suggestion", a.tenants.RealmSuggestion)
		r.Get("/{id}", a.tenants.Get)
		r.Patch("/{id}", a.tenants.Update)
		r.Delete("/{id}", a.tenants.Delete)
		r.Post("/{id}/storages", a.tenants.LinkStorage)
		r.Delete("/{id}/storages/{storageID}", a.tenants.UnlinkStorage)
	})

	// Default apps: read only. Changing who opens or draws a kind is the
	// panel's, with a signed-in administrator (file_types_admin.go).
	r.Get("/file-types", a.fileTypes.List)
}

// ───── in-process invoker (powers the MCP admin tools) ─────

// invoke runs an admin http.HandlerFunc in-process and returns the captured
// status code + raw response body. It builds a synthetic request carrying
// the admin principal, the chi URL params, the query string, and the JSON
// body, then drives the handler with a buffered recorder. No socket, no
// re-auth, no duplicated handler logic.
func (a *AIAdmin) invoke(ctx context.Context, principal *model.User, h http.HandlerFunc, method, path string, urlParams map[string]string, query url.Values, body any) (int, []byte) {
	// ⚠⚠ The admin_* MCP tools run the admin handlers IN-PROCESS - past every
	// route, so past api.DemoGuard. On a public demo /api/admin and
	// /api/ai/admin refuse every state-changing request; this is the same
	// refusal for the third door onto the same surface (2026-10-01: an
	// admin-scoped token's admin_login_security_unlock and admin_settings_set
	// went through on a demo).
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		if a.demoMode {
			b, _ := json.Marshal(DemoRefusal())
			return http.StatusForbidden, b
		}
	}
	var rdr io.Reader
	hasBody := false
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
		hasBody = true
	}

	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	c := auth.WithUser(ctx, principal)
	c, detail := auth.WithAuditDetail(c)
	if len(urlParams) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range urlParams {
			rctx.URLParams.Add(k, v)
		}
		c = context.WithValue(c, chi.RouteCtxKey, rctx)
	}

	req, err := http.NewRequestWithContext(c, method, target, rdr)
	if err != nil {
		return http.StatusBadRequest, []byte(`{"error":"` + err.Error() + `"}`)
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	// The caller's address (resolved once, by AIMCP.ServeHTTP) is this
	// request's too, so a handler that reads it - "your address" on the sign-in
	// security answer - reads the agent's, not an empty one.
	if ip := clientip.FromContext(ctx); ip != "" {
		req.RemoteAddr = ip
	}

	rec := newBufRecorder()
	h(rec, req)
	a.auditInvoke(ctx, principal, method, path, urlParams, rec.status, detail)
	return rec.status, rec.buf.Bytes()
}

// auditInvoke records a best-effort audit_log entry for a successful, mutating
// MCP admin tool call. The admin_* MCP tools run their handler in-process via
// invoke, bypassing the HTTP AuditMiddleware that covers the /api/ai/admin REST
// surface — so we replicate its (mutating-verb + 2xx-only) audit write here.
// GET reads and non-2xx responses are skipped; failures never affect the tool
// result. The action mirrors the REST path's name (prefixed "ai." via
// auth.AIAdminAction) so panel / AI-REST / AI-MCP writes are indistinguishable
// in the Audit page beyond that single "ai." marker.
func (a *AIAdmin) auditInvoke(callCtx context.Context, principal *model.User, method, path string, urlParams map[string]string, status int, detail *auth.AuditDetail) {
	if a.store == nil {
		return
	}
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return // reads are never audited
	}
	if status < 200 || status >= 300 {
		return // only successful writes
	}
	if detail.Skipped() {
		return // the handler recorded it itself (auth.SkipAuditRow)
	}
	action, targetType, targetID := auth.AIAdminAction(method, path, urlParams["id"], urlParams["name"])
	if a, tt := detail.Action(); a != "" {
		action, targetType = auth.DoorAction(a, true), tt
	}
	if action == "" {
		return
	}
	entry := &model.AuditEntry{
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		// The address the MCP call came from (AIMCP.ServeHTTP carries it down),
		// as every other door's row has it.
		IP:        clientip.FromContext(callCtx),
		CreatedAt: time.Now(),
	}
	// elevatedPrincipal preserves the bound user's real ID (only the role is
	// lifted to admin), so the audit row attributes the change correctly.
	if principal != nil && principal.ID > 0 {
		uid := principal.ID
		entry.UserID = &uid
	}
	// Stamp which token + username acted, and that the door was MCP - MCP
	// calls ride the API-token middleware, so both live on the tool-call
	// context.
	entry.Metadata = detail.Into(entry.Metadata)
	entry.TargetID, entry.Metadata = detail.ApplyTarget(entry.TargetID, entry.Metadata)
	entry.Metadata = auth.StampTokenDoor(callCtx, entry.Metadata, auth.ViaMCP)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.store.InsertAuditEntry(ctx, entry); err != nil {
		slog.Warn("ai admin mcp audit insert failed",
			slog.String("action", action),
			slog.String("err", err.Error()))
	}
}

// bufRecorder is a minimal in-memory http.ResponseWriter (we avoid importing
// httptest into production code). It records the status and buffers the body.
type bufRecorder struct {
	status int
	header http.Header
	buf    bytes.Buffer
	wrote  bool
}

func newBufRecorder() *bufRecorder {
	return &bufRecorder{status: http.StatusOK, header: http.Header{}}
}

func (b *bufRecorder) Header() http.Header { return b.header }

func (b *bufRecorder) WriteHeader(code int) {
	if !b.wrote {
		b.status = code
		b.wrote = true
	}
}

func (b *bufRecorder) Write(p []byte) (int, error) {
	b.wrote = true
	return b.buf.Write(p)
}

// ───── MCP admin tools ─────

// adminOut is the structured result every admin_* MCP tool returns: the
// underlying HTTP status plus the handler's raw JSON body.
//
// ⚠⚠ Result is `any` holding a json.RawMessage, never a json.RawMessage
// field. The SDK derives the tool's OUTPUT SCHEMA from this type and
// validates every answer against it: a json.RawMessage is a []byte, so the
// schema allowed only null or an array, and every tool whose handler answers
// an object - most of them - came back as a JSON-RPC error "validating tool
// output" although the handler had run (measured 2026-10-01; an agent was told
// the change it had just made failed). `any` is any JSON; the RawMessage
// inside still marshals as the handler's bytes.
type adminOut struct {
	Status int `json:"status"`
	Result any `json:"result" jsonschema:"the handler's JSON answer (an object or a list)"`
}

// Shared MCP tool input shapes. `map[string]any` fields infer to a permissive
// JSON `object` schema, letting the model pass arbitrary filter/body keys.
type adminVoidIn struct{}

type adminIDIn struct {
	ID int64 `json:"id" jsonschema:"numeric id of the target row"`
}

type adminNameIn struct {
	Name string `json:"name" jsonschema:"name of the target (provider/external service)"`
}

type adminKeyBodyIn struct {
	Key  string         `json:"key" jsonschema:"setting key"`
	Body map[string]any `json:"body" jsonschema:"request body, e.g. {\"value\":\"…\"}"`
}

type adminFiltersIn struct {
	Filters map[string]any `json:"filters,omitempty" jsonschema:"optional query filters (e.g. limit, offset, status, storage_id, unresolved, active, role, action, unread)"`
}

type adminBodyIn struct {
	Body map[string]any `json:"body" jsonschema:"JSON request body object"`
}

type adminIDBodyIn struct {
	ID   int64          `json:"id" jsonschema:"numeric id of the target row"`
	Body map[string]any `json:"body" jsonschema:"JSON request body object"`
}

type adminNameBodyIn struct {
	Name string         `json:"name" jsonschema:"name of the target (provider/external service)"`
	Body map[string]any `json:"body" jsonschema:"JSON request body object"`
}

// adminNameOptBodyIn is adminNameBodyIn with the body optional: a tool that
// used to take a name alone and now MAY carry a request body.
type adminNameOptBodyIn struct {
	Name string         `json:"name" jsonschema:"name of the target (provider/external service)"`
	Body map[string]any `json:"body,omitempty" jsonschema:"optional JSON request body object"`
}

// reqSpec is the request an admin tool maps its typed input to.
type reqSpec struct {
	handler   http.HandlerFunc
	method    string
	path      string
	urlParams map[string]string
	query     url.Values
	body      any
}

// adminReg bundles the per-request state shared by every admin tool closure.
type adminReg struct {
	srv       *mcp.Server
	a         *AIAdmin
	principal *model.User
}

// regAdminTool registers one admin_* MCP tool whose typed input is mapped to
// a request spec by `spec`, then executed in-process via AIAdmin.invoke.
func regAdminTool[In any](r *adminReg, name, desc string, spec func(In) reqSpec) {
	mcp.AddTool(r.srv, &mcp.Tool{Name: name, Description: desc},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, adminOut, error) {
			s := spec(in)
			code, raw := r.a.invoke(ctx, r.principal, s.handler, s.method, s.path, s.urlParams, s.query, s.body)
			return adminResult(code, raw)
		})
}

// adminResult packs a handler's (status, body) into the MCP result, flipping
// IsError on a 4xx/5xx so the model sees the failure but still gets the body.
func adminResult(code int, raw []byte) (*mcp.CallToolResult, adminOut, error) {
	body := json.RawMessage(raw)
	if len(raw) == 0 {
		body = json.RawMessage("null")
	}
	out := adminOut{Status: code, Result: body}
	if code >= 400 {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("admin op failed (HTTP %d): %s", code, string(raw))}},
		}, out, nil
	}
	return nil, out, nil
}

// registerAdminTools wires the full admin_* MCP tool set onto srv, bound to
// the admin principal. Only invoked from getServer when the token carries the
// `admin` scope — so admin tools are invisible to non-admin tokens.
func registerAdminTools(srv *mcp.Server, a *AIAdmin, principal *model.User) {
	r := &adminReg{srv: srv, a: a, principal: principal}

	// ── dashboard ──
	regAdminTool(r, "admin_dashboard", "Admin dashboard: per-storage summary, user/share/session counts, queue depth, recent activity.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.dash.Get, method: http.MethodGet, path: "/api/ai/admin/dashboard"}
		})

	// ── settings ──
	regAdminTool(r, "admin_settings_get", "List all instance settings (secrets redacted).",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.settings.List, method: http.MethodGet, path: "/api/ai/admin/settings"}
		})
	regAdminTool(r, "admin_settings_update", "Update multiple settings at once. body is a flat object {key: value, …}.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.settings.Update, method: http.MethodPatch, path: "/api/ai/admin/settings", body: in.Body}
		})
	regAdminTool(r, "admin_settings_set", "Upsert a single setting by key. body is {\"value\":\"…\"}.",
		func(in adminKeyBodyIn) reqSpec {
			return reqSpec{handler: a.settings.Set, method: http.MethodPut, path: "/api/ai/admin/settings/" + in.Key,
				urlParams: map[string]string{"key": in.Key}, body: in.Body}
		})

	// ── users ──
	regAdminTool(r, "admin_users_list", "List all users.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.users.List, method: http.MethodGet, path: "/api/ai/admin/users"}
		})
	regAdminTool(r, "admin_users_get", "Get a single user by id.",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.users.Get, method: http.MethodGet, path: "/api/ai/admin/users/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})
	regAdminTool(r, "admin_users_create", "Create a user. body: {email, password?, display_name?, role?, locale?, timezone?}. Without a password the account signs in through SSO or an API token only.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.users.Create, method: http.MethodPost, path: "/api/ai/admin/users", body: in.Body}
		})
	regAdminTool(r, "admin_users_update", "Update a user by id. body may include any of {password, display_name, role, locale, timezone, enabled, sso_unlink}. "+
		"sso_unlink: true removes the account's SSO bind (issuer + subject): its next SSO sign-in is matched by its email address again. "+
		"Switching an account on (enabled: true) approves one an SSO sign-in opened switched off (disabled_reason pending_approval).",
		func(in adminIDBodyIn) reqSpec {
			return reqSpec{handler: a.users.Update, method: http.MethodPatch, path: "/api/ai/admin/users/" + itoa(in.ID),
				urlParams: idParam(in.ID), body: in.Body}
		})
	regAdminTool(r, "admin_users_delete", "Delete a user by id (the last admin can never be deleted).",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.users.Delete, method: http.MethodDelete, path: "/api/ai/admin/users/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})
	regAdminTool(r, "admin_users_reset_password", "Reset a user's password to a fresh random one (returned ONCE).",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.usersAdm.ResetPassword, method: http.MethodPost, path: "/api/ai/admin/users/" + itoa(in.ID) + "/reset-password",
				urlParams: idParam(in.ID)}
		})

	// ── storages ──
	regAdminTool(r, "admin_storages_list", "List configured storages with stats. filters: {role: primary|replica}.",
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.storages.List, method: http.MethodGet, path: "/api/ai/admin/storages", query: filtersToQuery(in.Filters)}
		})
	regAdminTool(r, "admin_storages_get", "Get a single storage by id (with stats).",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.storages.Get, method: http.MethodGet, path: "/api/ai/admin/storages/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})
	regAdminTool(r, "admin_storages_create", "Create a storage. body: a storage object {name, driver, mount_path, config_json, …}.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.storages.Create, method: http.MethodPost, path: "/api/ai/admin/storages", body: in.Body}
		})
	regAdminTool(r, "admin_storages_update", "Update a storage by id. body: the full storage object.",
		func(in adminIDBodyIn) reqSpec {
			return reqSpec{handler: a.storages.Update, method: http.MethodPatch, path: "/api/ai/admin/storages/" + itoa(in.ID),
				urlParams: idParam(in.ID), body: in.Body}
		})
	regAdminTool(r, "admin_storages_delete", "Delete a storage by id (cascades descendant nodes).",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.storages.Delete, method: http.MethodDelete, path: "/api/ai/admin/storages/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})
	regAdminTool(r, "admin_storages_sync", "Trigger an immediate sync run for a storage by id - the whole storage, or with path one folder of it (a rescan of that folder, the panel's Rescan this folder).",
		func(in adminStorageSyncIn) reqSpec {
			var q url.Values
			if in.Path != "" {
				q = url.Values{"path": {in.Path}}
			}
			return reqSpec{handler: a.storages.TriggerSync, method: http.MethodPost, path: "/api/ai/admin/storages/" + itoa(in.ID) + "/sync",
				urlParams: idParam(in.ID), query: q}
		})
	regAdminTool(r, "admin_storages_sync_runs", "A storage's own sync runs, newest first. filters: {limit, offset}.",
		func(in adminIDFiltersIn) reqSpec {
			return reqSpec{handler: a.storagesAdm.SyncRuns, method: http.MethodGet, path: "/api/ai/admin/storages/" + itoa(in.ID) + "/sync-runs",
				urlParams: idParam(in.ID), query: filtersToQuery(in.Filters)}
		})
	regAdminTool(r, "admin_storages_drift", "A storage's drift report: what its syncs found changed outside filex. filters: {limit}.",
		func(in adminIDFiltersIn) reqSpec {
			return reqSpec{handler: a.storagesAdm.Drift, method: http.MethodGet, path: "/api/ai/admin/storages/" + itoa(in.ID) + "/drift",
				urlParams: idParam(in.ID), query: filtersToQuery(in.Filters)}
		})
	regAdminTool(r, "admin_storages_order", "Set the order storages are listed in for everyone (issue #57): ids is every storage's id in the new order; [] goes back to the default. A person's own order (Settings) still comes first for them.",
		func(in adminStorageOrderIn) reqSpec {
			return reqSpec{handler: a.storages.SetOrder, method: http.MethodPut, path: "/api/ai/admin/storages/order",
				body: map[string]any{"ids": in.IDs}}
		})
	regAdminTool(r, "admin_storages_test", "Test a driver+config connection without saving. body: {driver, config}.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.storagesAdm.Test, method: http.MethodPost, path: "/api/ai/admin/storages/test", body: in.Body}
		})

	// ── sync runs ──
	regAdminTool(r, "admin_sync_runs_list", "List recent sync runs across all storages. filters: {storage_id, status, limit, offset}.",
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.syncAdm.List, method: http.MethodGet, path: "/api/ai/admin/sync-runs", query: filtersToQuery(in.Filters)}
		})
	regAdminTool(r, "admin_sync_runs_get", "Get a single sync run (with conflicts) by id.",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.syncAdm.Detail, method: http.MethodGet, path: "/api/ai/admin/sync-runs/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})

	// ── shares ──
	regAdminTool(r, "admin_shares_list", "List all shares across users. filters: {creator_id, active, limit, offset}.",
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.sharesAdm.List, method: http.MethodGet, path: "/api/ai/admin/shares", query: filtersToQuery(in.Filters)}
		})
	regAdminTool(r, "admin_shares_revoke", "Revoke a share by id (soft - keeps audit trail).",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.sharesAdm.Revoke, method: http.MethodPost, path: "/api/ai/admin/shares/" + itoa(in.ID) + "/revoke",
				urlParams: idParam(in.ID)}
		})
	regAdminTool(r, "admin_shares_delete", "Hard-delete a share row by id.",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.sharesAdm.Delete, method: http.MethodDelete, path: "/api/ai/admin/shares/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})

	// ── trash ──
	regAdminTool(r, "admin_trash_list", "List soft-deleted (trashed) nodes. filters: {storage_id, limit, offset}.",
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.trash.List, method: http.MethodGet, path: "/api/ai/admin/trash", query: filtersToQuery(in.Filters)}
		})
	regAdminTool(r, "admin_trash_restore", "Restore trashed nodes. body: {node_id} restores one at once; with queued: true, body: {node_ids: [...]} (at most 1000) is judged entry by entry and queued as a job of the operations queue - 202 {ops}, follow them with op_get - which is what a folder on an object store needs (the panel's Trash page asks it that way).",
		func(in adminQueuedBodyIn) reqSpec {
			return reqSpec{handler: a.trash.Restore, method: http.MethodPost, path: "/api/ai/admin/trash/restore", body: in.Body, query: queuedQuery(in.Queued)}
		})
	regAdminTool(r, "admin_trash_empty", "Purge trash. body: {older_than_days?, storage_id?} (0/omitted wipes everything soft-deleted). "+
		"Answers with the final counts when the purge finishes within a few seconds; otherwise 202 {running: true, total, purged, …} "+
		"while it goes on in the background - follow it with admin_trash_empty_status.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.trash.AdminEmpty, method: http.MethodPost, path: "/api/ai/admin/trash/empty", body: in.Body}
		})
	regAdminTool(r, "admin_trash_empty_status", "Progress of the latest admin_trash_empty: {op_id, running, queued, cancelled, total, purged, failed, bytes, started_at, finished_at, error}. Stop it with the op_cancel tool {id: op_id} (a token holding write), or Cancel in the queue tray.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.trash.EmptyStatus, method: http.MethodGet, path: "/api/ai/admin/trash/empty"}
		})
	regAdminTool(r, "admin_trash_purge", "Hard-delete a single trashed node by id. With queued: true it is a job of the operations queue - 202 {op}, follow it with op_get - as a large folder on an object store needs.",
		func(in adminQueuedIDIn) reqSpec {
			return reqSpec{handler: a.trash.Purge, method: http.MethodDelete, path: "/api/ai/admin/trash/" + itoa(in.ID),
				urlParams: idParam(in.ID), query: queuedQuery(in.Queued)}
		})

	// ── search index ──
	regAdminTool(r, "admin_search_stats", "Full-text (Bleve) index stats: document count + size.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.searchAdm.Stats, method: http.MethodGet, path: "/api/ai/admin/search/stats"}
		})
	regAdminTool(r, "admin_search_rebuild", "Drop and rebuild the full-text index from node rows (runs in background).",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.searchAdm.Rebuild, method: http.MethodPost, path: "/api/ai/admin/search/rebuild"}
		})

	// ── auth providers ──
	regAdminTool(r, "admin_auth_providers_list", "List auth drivers + their (redacted) config.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.authProv.List, method: http.MethodGet, path: "/api/ai/admin/auth-providers"}
		})
	regAdminTool(r, "admin_auth_providers_update", "Change an identity provider managed on the Identity providers page (oidc, ldap, proxy-header, windows, pam) and apply it at once. body: {enabled?, config?: {field: value}, confirm_failed_test?, test_account?: {username, password}}. A provider the environment defines is read-only; switching one on whose test fails needs confirm_failed_test; the last way an administrator can sign in cannot be switched off. The operating-system providers (windows, pam = Linux PAM) are tested by signing a real account in: send test_account (used once, never stored or logged); their test must pass - confirm_failed_test does not override it - and the account that passed becomes a super administrator. Fields: windows = auto_create (default false), allowed_groups, domain, protocol_login, show_refusal_reason (default false; on, a person whose password was right but who is refused is told why, which also confirms the password to anybody guessing - ldap and pam take it too); pam = see the Identity providers page; oidc = trust_email (default false; on, every email address the provider sends counts as verified - email_verified or not - so anybody who can set an address there can sign in to the account with it; off, an unverified address opens no account not yet bound to its SSO identity and a new account opens switched off for an administrator to approve). A provider's set_by_upgrade names fields whose value the upgrade to 0.50 set.",
		func(in adminNameBodyIn) reqSpec {
			return reqSpec{handler: a.authProv.Update, method: http.MethodPatch, path: "/api/ai/admin/auth-providers/" + in.Name,
				urlParams: nameParam(in.Name), body: in.Body}
		})
	regAdminTool(r, "admin_auth_providers_create", "Make another sign-in provider of a kind (a second LDAP, an OIDC for some tenants only). body: {driver: oidc|ldap|pam|windows|proxy-header, slug?, label?, enabled?, config?: {field: value}, tenants?: [tenant ids], confirm_failed_test?, test_account?}. Saved like admin_auth_providers_update (the real test first); it serves the platform's own tenant unless tenants names others. The first provider of each kind is the one named by the driver (oidc, ldap, ...): change it with admin_auth_providers_update.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.authProv.Create, method: http.MethodPost, path: "/api/ai/admin/auth-providers", body: in.Body}
		})
	regAdminTool(r, "admin_auth_providers_delete", "Delete a sign-in provider made with admin_auth_providers_create (by its slug). The first provider of a kind is switched off instead, and an environment provider is the environment's. Refused when it is the last way an administrator can sign in (a tenant's: pass confirm_tenant_lockout in body).",
		func(in adminNameOptBodyIn) reqSpec {
			var q url.Values
			if v, _ := in.Body["confirm_tenant_lockout"].(bool); v {
				q = url.Values{"confirm_tenant_lockout": {"1"}}
			}
			return reqSpec{handler: a.authProv.Delete, method: http.MethodDelete, path: "/api/ai/admin/auth-providers/" + in.Name,
				urlParams: nameParam(in.Name), query: q}
		})
	regAdminTool(r, "admin_auth_providers_set_tenants", "Say which tenants sign in through a sign-in provider (multi-tenant installs). body: {tenants: [tenant ids], confirm_tenant_lockout?}. The list replaces the provider's bindings. A tenant's own provider serves that tenant only. Removing the last way an administrator of the platform's own tenant can sign in is refused; a tenant's needs confirm_tenant_lockout.",
		func(in adminNameBodyIn) reqSpec {
			return reqSpec{handler: a.authProv.SetTenants, method: http.MethodPut, path: "/api/ai/admin/auth-providers/" + in.Name + "/tenants",
				urlParams: nameParam(in.Name), body: in.Body}
		})
	regAdminTool(r, "admin_auth_providers_test", "Test an auth provider by name. Optional body: {config?: {field: value} (unsaved edits), test_account?: {username, password}} - the operating-system providers (windows, pam) sign in for real with test_account as their last step (used once, never stored or logged, nothing is changed).",
		func(in adminNameOptBodyIn) reqSpec {
			return reqSpec{handler: a.authProv.Test, method: http.MethodPost, path: "/api/ai/admin/auth-providers/" + in.Name + "/test",
				urlParams: nameParam(in.Name), body: in.Body}
		})

	// ── external services ──
	regAdminTool(r, "admin_external_list", "List external services (OnlyOffice, Drawio, …), secrets redacted.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.external.List, method: http.MethodGet, path: "/api/ai/admin/external"}
		})
	regAdminTool(r, "admin_external_update", "Update an external service by name. body: {enabled?, url?, secret?, options_json?}.",
		func(in adminNameBodyIn) reqSpec {
			return reqSpec{handler: a.external.Update, method: http.MethodPatch, path: "/api/ai/admin/external/" + in.Name,
				urlParams: nameParam(in.Name), body: in.Body}
		})
	regAdminTool(r, "admin_external_test", "Run a health probe against an external service by name. Optional body: {enabled?, url?, secret?, callback_url?} - values that are not saved, tested as they are and stored nowhere (the answer says unsaved: true); without a body the saved configuration is tested. For onlyoffice the answer also measures the document server's route back to filex (service_to_filex) and whether it enforces JWT.",
		func(in adminNameOptBodyIn) reqSpec {
			return reqSpec{handler: a.external.Test, method: http.MethodPost, path: "/api/ai/admin/external/" + in.Name + "/test",
				urlParams: nameParam(in.Name), body: in.Body}
		})

	// ── replica ──
	regAdminTool(r, "admin_replica_rules_list", "List replica rules.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.replica.ListRules, method: http.MethodGet, path: "/api/ai/admin/replica/rules"}
		})
	regAdminTool(r, "admin_replica_rules_create", "Create a replica rule. body: a ReplicaRuleInput object.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.replica.CreateRule, method: http.MethodPost, path: "/api/ai/admin/replica/rules", body: in.Body}
		})
	regAdminTool(r, "admin_replica_rules_update", "Update a replica rule by id. body: a ReplicaRuleInput object.",
		func(in adminIDBodyIn) reqSpec {
			return reqSpec{handler: a.replica.UpdateRule, method: http.MethodPatch, path: "/api/ai/admin/replica/rules/" + itoa(in.ID),
				urlParams: idParam(in.ID), body: in.Body}
		})
	regAdminTool(r, "admin_replica_rules_delete", "Delete a replica rule by id.",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.replica.DeleteRule, method: http.MethodDelete, path: "/api/ai/admin/replica/rules/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})
	regAdminTool(r, "admin_replica_failures_list", "List replica failures. filters: {unresolved, limit, offset}.",
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.replica.ListFailures, method: http.MethodGet, path: "/api/ai/admin/replica/failures", query: filtersToQuery(in.Filters)}
		})
	regAdminTool(r, "admin_replica_failures_count", "How many replica failures are unresolved: {count}.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.replica.CountFailures, method: http.MethodGet, path: "/api/ai/admin/replica/failures/count"}
		})
	regAdminTool(r, "admin_replica_fix", "Queue a retry for every unresolved replica failure (the panel's Fix all). A failure whose retry is still waiting is not queued twice (already_queued counts them).",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.replica.FixAll, method: http.MethodPost, path: "/api/ai/admin/replica/fix"}
		})
	regAdminTool(r, "admin_replica_fix_one", "Queue a retry for one replica failure: {path, op} as admin_replica_failures_list names it (op: write | delete | move | copy). queued: false = a retry of it was already waiting.",
		func(in adminReplicaFixOneIn) reqSpec {
			return reqSpec{handler: a.replica.FixOne, method: http.MethodPost, path: "/api/ai/admin/replica/fix-one",
				body: map[string]any{"path": in.Path, "op": in.Op}}
		})
	regAdminTool(r, "admin_replica_report_get", "Get the latest replica status report.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.replica.GetReport, method: http.MethodGet, path: "/api/ai/admin/replica/report"}
		})
	regAdminTool(r, "admin_replica_report_run", "Generate the replica status report now (synchronous).",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.replica.RunReportNow, method: http.MethodPost, path: "/api/ai/admin/replica/report/run-now"}
		})
	regAdminTool(r, "admin_replica_settings_get", "Get the singleton replica settings row.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.replica.GetSettings, method: http.MethodGet, path: "/api/ai/admin/replica/settings"}
		})
	regAdminTool(r, "admin_replica_settings_update", "Update replica settings. body: {report_cron?, report_enabled?, default_mode?}.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.replica.UpdateSettings, method: http.MethodPatch, path: "/api/ai/admin/replica/settings", body: in.Body}
		})

	// ── replication targets ──
	regAdminTool(r, "admin_replication_targets_list", "List replication targets (backup-only sinks).",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.repTargets.List, method: http.MethodGet, path: "/api/ai/admin/replication-targets"}
		})
	regAdminTool(r, "admin_replication_targets_get", "Get a replication target by id.",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.repTargets.Get, method: http.MethodGet, path: "/api/ai/admin/replication-targets/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})
	regAdminTool(r, "admin_replication_targets_create", "Create a replication target. body: {name, driver, mode?, enabled?, config…}.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.repTargets.Create, method: http.MethodPost, path: "/api/ai/admin/replication-targets", body: in.Body}
		})
	regAdminTool(r, "admin_replication_targets_update", "Update a replication target by id. body: the full target object.",
		func(in adminIDBodyIn) reqSpec {
			return reqSpec{handler: a.repTargets.Update, method: http.MethodPatch, path: "/api/ai/admin/replication-targets/" + itoa(in.ID),
				urlParams: idParam(in.ID), body: in.Body}
		})
	regAdminTool(r, "admin_replication_targets_delete", "Delete a replication target by id.",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.repTargets.Delete, method: http.MethodDelete, path: "/api/ai/admin/replication-targets/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})

	// ── queue ──
	regAdminTool(r, "admin_queue_stats", "Queue dashboard counters.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.queue.Stats, method: http.MethodGet, path: "/api/ai/admin/queue/stats"}
		})
	regAdminTool(r, "admin_queue_list", "List queue ops. filters: {status, limit, offset}.",
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.queue.List, method: http.MethodGet, path: "/api/ai/admin/queue", query: filtersToQuery(in.Filters)}
		})
	regAdminTool(r, "admin_queue_get", "Get a single queue op by id.",
		func(in adminStrIDIn) reqSpec {
			return reqSpec{handler: a.queue.Get, method: http.MethodGet, path: "/api/ai/admin/queue/" + in.ID,
				urlParams: map[string]string{"id": in.ID}}
		})
	regAdminTool(r, "admin_queue_retry", "Retry a failed queue op by id.",
		func(in adminStrIDIn) reqSpec {
			return reqSpec{handler: a.queue.Retry, method: http.MethodPost, path: "/api/ai/admin/queue/" + in.ID + "/retry",
				urlParams: map[string]string{"id": in.ID}}
		})
	regAdminTool(r, "admin_queue_cancel", "Cancel a pending queue op by id.",
		func(in adminStrIDIn) reqSpec {
			return reqSpec{handler: a.queue.Cancel, method: http.MethodDelete, path: "/api/ai/admin/queue/" + in.ID,
				urlParams: map[string]string{"id": in.ID}}
		})

	// ── notifications ──
	regAdminTool(r, "admin_notifications_list", "List notifications (global admin view). filters: {unread, limit, offset}.",
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.notif.AdminList, method: http.MethodGet, path: "/api/ai/admin/notifications", query: filtersToQuery(in.Filters)}
		})
	regAdminTool(r, "admin_notifications_test", "Emit a test notification through the in-app bell + webhook.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.notif.AdminTest, method: http.MethodPost, path: "/api/ai/admin/notifications/test"}
		})
	regAdminTool(r, "admin_notifications_webhook_get", "Get the notification webhook config (URL + token-set flag).",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.notif.AdminWebhookConfig, method: http.MethodGet, path: "/api/ai/admin/notifications/webhook-config"}
		})
	regAdminTool(r, "admin_notifications_webhook_update", "Set the notification webhook config. body: {url, token}.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.notif.AdminUpdateWebhookConfig, method: http.MethodPatch, path: "/api/ai/admin/notifications/webhook-config", body: in.Body}
		})
	regAdminTool(r, "admin_notifications_mark_read", "Mark a notification read by id.",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.notif.MarkRead, method: http.MethodPost, path: "/api/ai/admin/notifications/" + itoa(in.ID) + "/read",
				urlParams: idParam(in.ID)}
		})

	// ── webhook targets (v2) ──
	regAdminTool(r, "admin_webhooks_list", "List the webhook targets (Admin, Notifications, Webhooks): {targets: [{id, name, url, secret_set, events, enabled, last_status, ...}]}. A secret is never shown. The platform operator's only in multi-tenant mode.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.webhooks.List, method: http.MethodGet, path: "/api/ai/admin/webhooks"}
		})
	regAdminTool(r, "admin_webhooks_create", "Add a webhook target. body: {name, url, secret?, events: [event names], enabled?} - the events are the ones NOTIFICATIONS.md lists. The url is checked as the page checks it.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.webhooks.Create, method: http.MethodPost, path: "/api/ai/admin/webhooks", body: in.Body}
		})
	regAdminTool(r, "admin_webhooks_update", "Change a webhook target by id. body: any of {name, url, secret, events, enabled}; a field left out stays as it is.",
		func(in adminIDBodyIn) reqSpec {
			return reqSpec{handler: a.webhooks.Update, method: http.MethodPatch, path: "/api/ai/admin/webhooks/" + itoa(in.ID),
				urlParams: idParam(in.ID), body: in.Body}
		})
	regAdminTool(r, "admin_webhooks_delete", "Delete a webhook target by id.",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.webhooks.Delete, method: http.MethodDelete, path: "/api/ai/admin/webhooks/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})
	regAdminTool(r, "admin_webhooks_test", "Send a test event to one webhook target by id now and answer what came back.",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.webhooks.Test, method: http.MethodPost, path: "/api/ai/admin/webhooks/" + itoa(in.ID) + "/test",
				urlParams: idParam(in.ID)}
		})

	// ── audit ──
	regAdminTool(r, "admin_audit_list", "List audit log entries. filters: {user_id, action, from, to, limit, offset}.",
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.audit.List, method: http.MethodGet, path: "/api/ai/admin/audit", query: filtersToQuery(in.Filters)}
		})

	// ── sign-in security ──
	regAdminTool(r, "admin_login_security_get", "Sign-in attempt limits: settings (per-account and per-address limits, window, lock lengths, IP allow-list, trusted proxies), the bounds, the trusted-proxy list in force and its source (setting, env or auto), what `auto` resolved to and why (trusted_proxies_auto: environment, networks, excluded gateways, interfaces, warning), the peers that send X-Forwarded-For without being trusted (untrusted_forwarders), and the address filex sees this call coming from.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.loginSec.Get, method: http.MethodGet, path: "/api/ai/admin/login-security"}
		})
	regAdminTool(r, "admin_login_security_update", "Change sign-in attempt limits. body is a partial object: {enabled, account_max_fails, ip_max_fails, window_seconds, lock_base_seconds, lock_max_seconds, ip_allowlist:[addresses or CIDR], trusted_proxies:[addresses, CIDR, auto, loopback, private, link-local, none]} - a trusted_proxies list replaces the default (auto), so keep `auto` in it to add a proxy on top of the automatic set (e.g. [\"auto\", \"192.0.2.10\"]); an empty list removes the saved one.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.loginSec.Patch, method: http.MethodPatch, path: "/api/ai/admin/login-security", body: in.Body}
		})
	regAdminTool(r, "admin_login_security_locks", "List the sign-in counters: who is being counted and who is locked, until when. filters: {locked:1, scope:account|ip, limit}.",
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.loginSec.Locks, method: http.MethodGet, path: "/api/ai/admin/login-security/locks", query: filtersToQuery(in.Filters)}
		})
	regAdminTool(r, "admin_login_security_unlock", "Lift a sign-in lock. body is {scope:'account'|'ip', subject:'<identifier or address>'} or {all:true} for every lock in force.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.loginSec.Unlock, method: http.MethodPost, path: "/api/ai/admin/login-security/unlock", body: in.Body}
		})
	regAdminTool(r, "admin_login_security_attempts", "The sign-in trail: wrong attempts, locks, releases and allow-list passes, newest first. filters: {action:login.failed|login.locked|login.unlocked|login.allowlist_pass, from, to, limit, offset}.",
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.loginSec.Attempts, method: http.MethodGet, path: "/api/ai/admin/login-security/attempts", query: filtersToQuery(in.Filters)}
		})

	// ── RBAC grants (per-file/folder permissions) ──
	regAdminTool(r, "admin_grants_list", "List every per-file/folder RBAC grant (who has what level, on which path, in which storage). Each row has kind \"user\" (a person's, user_id) or \"group\" (a group's, group_id): the two kinds are numbered apart, so revoke a user row with admin_grant_revoke and a group row with admin_group_grant_revoke.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.grants.AdminList, method: http.MethodGet, path: "/api/ai/admin/grants"}
		})
	regAdminTool(r, "admin_grant_set", "Grant a user - or, with group_id instead of user_id, a group (every member) - access to a path. body: {path:\"<adapter>://<rel>\", user_id | group_id, level: viewer|editor|owner}. The storage must have RBAC enabled; a viewer account may only be granted viewer (a group takes any level: each member stays capped by their own account).",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.grants.Create, method: http.MethodPost, path: "/api/ai/admin/grants", body: in.Body}
		})
	regAdminTool(r, "admin_grant_revoke", "Revoke a person's grant by its id (a row of admin_grants_list with kind \"user\"). A group's row has its own numbering: use admin_group_grant_revoke for it.",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.grants.AdminDelete, method: http.MethodDelete, path: "/api/ai/admin/grants/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})
	regAdminTool(r, "admin_group_grant_revoke", "Revoke a group's grant by its id (a row of admin_grants_list with kind \"group\").",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.grants.AdminDeleteGroup, method: http.MethodDelete, path: "/api/ai/admin/grants/groups/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})

	// ── protection and archive settings (validated, as on their pages) ──
	regAdminTool(r, "admin_protection_get", "The Protection settings (Admin, Protection): trash_retention_days, versions_keep_n, share_max_ttl_days (with shares_over_max_ttl), drafts_limit (with its bounds) and the antivirus status. The platform operator's only in multi-tenant mode.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.protection.Get, method: http.MethodGet, path: "/api/ai/admin/protection"}
		})
	regAdminTool(r, "admin_protection_update", "Change Protection settings, validated as the page validates them (a value out of bounds is refused, not stored - unlike admin_settings_set's raw keys). body: any of {trash_retention_days (1-3650), versions_keep_n (0-1000, 0 = unlimited), share_max_ttl_days (0-3650), drafts_limit, av_enabled, av_mode, av_clamd_addr, av_save_scan_window_minutes, av_max_scan_mb}; the three av_enabled/av_mode/av_clamd_addr take effect at the next restart (restart_pending).",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.protection.Patch, method: http.MethodPatch, path: "/api/ai/admin/protection", body: in.Body}
		})
	regAdminTool(r, "admin_archives_get", "The archive settings (Admin, Archives): enabled, default_format, allowed_formats, max_entries, max_expanded_bytes, timeout_seconds, and the providers (the built-in reader, 7-Zip) with what each can create and extract.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.archivesOr(func(h *ArchiveAdmin) http.HandlerFunc { return h.Get }), method: http.MethodGet, path: "/api/ai/admin/archives"}
		})
	regAdminTool(r, "admin_archives_update", "Change the archive settings, validated as the page validates them. body: any of {enabled, default_format, allowed_formats, max_entries, max_expanded_bytes, timeout_seconds}.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.archivesOr(func(h *ArchiveAdmin) http.HandlerFunc { return h.Patch }), method: http.MethodPatch, path: "/api/ai/admin/archives", body: in.Body}
		})
	regAdminTool(r, "admin_archives_test", "Probe the archive provider now: pack and unpack a small password-protected 7z in the work folder, and answer what happened.",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.archivesOr(func(h *ArchiveAdmin) http.HandlerFunc { return h.Test }), method: http.MethodPost, path: "/api/ai/admin/archives/test"}
		})

	registerPluginTools(r, a)
	registerTenantTools(r, a)
}

// pluginModel is said in every plugin tool's description, so an agent learns
// the rule from the catalogue rather than from a refusal.
const pluginModel = " Plugins are installed by an administrator, never by an API key: you can read them and LEAVE A REQUEST " +
	"(admin_plugin_request_install / admin_plugin_request_upgrade), which waits for an administrator signed in to the " +
	"admin panel (Plugins → Install requests) to approve or reject. There is no tool to approve, reject, install, upgrade, remove " +
	"or switch a plugin."

// pluginRequestIn is a plugin request as an agent leaves it. The source is
// the install endpoints' own shape: an app from a GitHub repository or from
// addresses; a storage plugin from its source feed or from an address.
type pluginRequestIn struct {
	Kind        string `json:"kind" jsonschema:"app (a WebAssembly app: actions, screens, a language pack) or storage (a storage plugin: a driver process)"`
	Name        string `json:"name,omitempty" jsonschema:"storage install: the name to install it under (default: the name its feed gives); upgrade: the installed plugin's name"`
	PluginID    int64  `json:"plugin_id,omitempty" jsonschema:"upgrade: the installed plugin's id, instead of name"`
	GitHubRepo  string `json:"github_repo,omitempty" jsonschema:"app: owner/name of a public GitHub repository whose root holds filex-app.json"`
	Ref         string `json:"ref,omitempty" jsonschema:"app: git tag or branch (default: main, then master)"`
	ManifestURL string `json:"manifest_url,omitempty" jsonschema:"app: the https address of its filex-app.json"`
	URL         string `json:"url,omitempty" jsonschema:"app: the module's address (with manifest_url); storage: the plugin binary's https address"`
	SHA256      string `json:"sha256,omitempty" jsonschema:"the sha256 the bytes at url must have; optional - the server hashes what it finds and freezes that"`
	Source      string `json:"source,omitempty" jsonschema:"storage: owner/name of a GitHub repository whose latest release has filex-storage.json, or the https address of a filex-storage.json"`
	Reason      string `json:"reason" jsonschema:"why the plugin is needed, in a sentence or two - the administrator reads it before deciding (required)"`
}

func (in pluginRequestIn) body(op string) map[string]any {
	b := map[string]any{"kind": in.Kind, "op": op, "reason": in.Reason}
	for k, v := range map[string]string{
		"name": in.Name, "github_repo": in.GitHubRepo, "ref": in.Ref, "manifest_url": in.ManifestURL,
		"url": in.URL, "sha256": in.SHA256, "source": in.Source,
	} {
		if v != "" {
			b[k] = v
		}
	}
	if in.PluginID > 0 {
		b["plugin_id"] = in.PluginID
	}
	return b
}

// adminAppLogsIn reads an app's log from a line on.
type adminAppLogsIn struct {
	ID    int64 `json:"id" jsonschema:"the app's numeric id (from admin_app_plugins_list)"`
	After int64 `json:"after,omitempty" jsonschema:"only lines after this sequence number (the previous answer's next)"`
}

// registerPluginTools wires the plugin tools: reading, and leaving requests.
func registerPluginTools(r *adminReg, a *AIAdmin) {
	regAdminTool(r, "admin_plugins_list", "List the installed STORAGE plugins (driver processes): state, version, capabilities, "+
		"conformance, and what the last update check found (`update`)."+pluginModel,
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.plugins.List, method: http.MethodGet, path: "/api/ai/admin/plugins"}
		})
	regAdminTool(r, "admin_plugin_get", "One storage plugin by id: state, version, capabilities, conformance report, pending update."+pluginModel,
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.plugins.Get, method: http.MethodGet, path: "/api/ai/admin/plugins/" + itoa(in.ID), urlParams: idParam(in.ID)}
		})
	regAdminTool(r, "admin_plugins_check_updates", "Ask every storage plugin's source for a newer version now and answer the list with "+
		"what was found. Installs nothing: a newer version waits for an administrator - request it with admin_plugin_request_upgrade."+pluginModel,
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.plugins.CheckUpdates, method: http.MethodPost, path: "/api/ai/admin/plugins/updates/check"}
		})
	regAdminTool(r, "admin_app_plugins_list", "List the installed APPS (WebAssembly app plugins, language packs) and the runtime they "+
		"run in: state, version, granted permissions, source, and what the last update check found (`update`)."+pluginModel,
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.appPlugins.List, method: http.MethodGet, path: "/api/ai/admin/app-plugins"}
		})
	regAdminTool(r, "admin_app_plugin_get", "One app by id: state, version, its manifest, the permissions it was granted (each with "+
		"its label and the app's reason), settings, action overrides, schedule, pending update."+pluginModel,
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.appPlugins.Get, method: http.MethodGet, path: "/api/ai/admin/app-plugins/" + itoa(in.ID), urlParams: idParam(in.ID)}
		})
	regAdminTool(r, "admin_app_plugin_logs", "An app's recent log lines ({lines, next}); pass `after` = the previous answer's next to follow it.",
		func(in adminAppLogsIn) reqSpec {
			var q url.Values
			if in.After > 0 {
				q = url.Values{"after": {strconv.FormatInt(in.After, 10)}}
			}
			return reqSpec{handler: a.appPlugins.Logs, method: http.MethodGet, path: "/api/ai/admin/app-plugins/" + itoa(in.ID) + "/logs",
				urlParams: idParam(in.ID), query: q}
		})
	regAdminTool(r, "admin_app_locks_list", "The files apps have frozen (a document out for signature): {locks: [{storage_id, storage, path, plugin_name, reason, ...}]}. filters: {storage_id}.",
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.appPlugins.Locks, method: http.MethodGet, path: "/api/ai/admin/app-plugins/locks", query: filtersToQuery(in.Filters)}
		})
	regAdminTool(r, "admin_app_unlock", "Lift an app's lock on a file by force: {storage_id, path} as admin_app_locks_list names it. The app is not asked - a signing it was waiting for may not finish - and the unlock is audited (app_plugin.unlock).",
		func(in adminAppUnlockIn) reqSpec {
			return reqSpec{handler: a.appPlugins.Unlock, method: http.MethodDelete, path: "/api/ai/admin/app-plugins/locks",
				body: map[string]any{"storage_id": in.StorageID, "path": in.Path}}
		})
	regAdminTool(r, "admin_file_types_list", "Default apps: every kind of file (extension) something besides filex handles, and every kind "+
		"with an administrator's rule - for each, who OPENS it and who draws its THUMBNAILS, in the order they are asked (`on`), "+
		"the ones switched off (`off`), and whether that order is the default or a rule (`custom`). Handlers are `builtin`, "+
		"`app:<app>/<view>` (opens) and `app:<app>` (thumbnails). Read only: changing it needs an administrator signed in to the "+
		"admin panel (Plugins → Default apps).",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.fileTypes.List, method: http.MethodGet, path: "/api/ai/admin/file-types"}
		})
	regAdminTool(r, "admin_app_plugins_check_updates", "Ask every app's source for a newer version now and answer the list with "+
		"what was found. Installs nothing: every newer version waits for an administrator - request it with admin_plugin_request_upgrade."+pluginModel,
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.appPlugins.CheckUpdates, method: http.MethodPost, path: "/api/ai/admin/app-plugins/updates/check"}
		})

	regAdminTool(r, "admin_plugin_request_install", "Ask an administrator to INSTALL a plugin. filex fetches the source now "+
		"(the install review's dry run) and freezes what it found - the manifest, the sha256, the permissions it asks for - "+
		"then the request waits for an administrator signed in to the admin panel (Plugins → Install requests). Nothing is installed "+
		"by this call, and you cannot approve it: the administrator installs exactly the frozen bytes, or the request becomes "+
		"`superseded` when the source has changed by then. Asking again for the same source answers the pending request. "+
		"Requests expire after 14 days (FILEX_PLUGIN_REQUEST_TTL_DAYS). An app: github_repo (+ ref), or manifest_url (+ url "+
		"[+ sha256]). A storage plugin: source (owner/name or a filex-storage.json address) or url (+ sha256), and name. "+
		"Answers {request: {id, status: pending, permissions, sha256, …}, created, message}; follow it with admin_plugin_request_get.",
		func(in pluginRequestIn) reqSpec {
			return reqSpec{handler: a.pluginReqs.Create, method: http.MethodPost, path: "/api/ai/admin/plugin-requests", body: in.body("install")}
		})
	regAdminTool(r, "admin_plugin_request_upgrade", "Ask an administrator to UPGRADE an installed plugin (name or plugin_id) - by "+
		"default to the newer version its own source has (what admin_app_plugins_check_updates / admin_plugins_check_updates "+
		"reported); an app may name another github_repo or manifest_url. Like admin_plugin_request_install, it freezes what "+
		"the source answers now (version, sha256, the permissions - the ones it ADDS are what the administrator approves) and "+
		"waits for an administrator in the admin panel; nothing is upgraded by this call and you cannot approve it.",
		func(in pluginRequestIn) reqSpec {
			return reqSpec{handler: a.pluginReqs.Create, method: http.MethodPost, path: "/api/ai/admin/plugin-requests", body: in.body("upgrade")}
		})
	regAdminTool(r, "admin_plugin_requests_list", "List plugin requests: filters {status: pending (default) | approved | rejected | "+
		"expired | superseded | all}. Each says who asked, why, what was frozen and - once decided - who decided and why."+pluginModel,
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.pluginReqs.List, method: http.MethodGet, path: "/api/ai/admin/plugin-requests", query: filtersToQuery(in.Filters)}
		})
	regAdminTool(r, "admin_plugin_request_get", "One plugin request by id, with its frozen manifest and review: status "+
		"(pending | approved | rejected | expired | superseded), decision_note (a rejection's reason, or why it was superseded) "+
		"and, once approved, the installed plugin (`result`)."+pluginModel,
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.pluginReqs.Get, method: http.MethodGet, path: "/api/ai/admin/plugin-requests/" + itoa(in.ID), urlParams: idParam(in.ID)}
		})
}

// adminStorageSyncIn is admin_storages_sync's input: a storage, and
// optionally one folder of it to rescan.
type adminStorageSyncIn struct {
	ID   int64  `json:"id" jsonschema:"numeric id of the storage"`
	Path string `json:"path,omitempty" jsonschema:"rescan only this folder (storage-relative, e.g. projects/2026); empty = the whole storage"`
}

// adminIDFiltersIn is a row id plus query filters.
type adminIDFiltersIn struct {
	ID      int64          `json:"id" jsonschema:"numeric id of the target row"`
	Filters map[string]any `json:"filters,omitempty" jsonschema:"optional query filters (e.g. limit, offset)"`
}

// adminStorageOrderIn is the storage order: every id, in order.
type adminStorageOrderIn struct {
	IDs []int64 `json:"ids" jsonschema:"every storage id in the order they are listed; [] = the default order"`
}

// adminQueuedBodyIn is a body that may be run as a job of the queue.
type adminQueuedBodyIn struct {
	Body   map[string]any `json:"body" jsonschema:"JSON request body object"`
	Queued bool           `json:"queued,omitempty" jsonschema:"run it as a job of the operations queue (202; follow it with op_get)"`
}

// adminQueuedIDIn is a row id that may be acted on as a job of the queue.
type adminQueuedIDIn struct {
	ID     int64 `json:"id" jsonschema:"numeric id of the target row"`
	Queued bool  `json:"queued,omitempty" jsonschema:"run it as a job of the operations queue (202; follow it with op_get)"`
}

// queuedQuery is `?queued=1` when asked.
func queuedQuery(queued bool) url.Values {
	if !queued {
		return nil
	}
	return url.Values{"queued": {"1"}}
}

// adminReplicaFixOneIn names one replica failure.
type adminReplicaFixOneIn struct {
	Path string `json:"path" jsonschema:"the failure's path, as admin_replica_failures_list names it"`
	Op   string `json:"op" jsonschema:"the failure's operation: write | delete | move | copy"`
}

// adminAppUnlockIn names one app lock.
type adminAppUnlockIn struct {
	StorageID int64  `json:"storage_id" jsonschema:"the lock's storage id"`
	Path      string `json:"path" jsonschema:"the locked file's storage-relative path"`
}

// adminStrIDIn is the input for queue tools (the queue uses opaque string ids,
// not numeric ones).
type adminStrIDIn struct {
	ID string `json:"id" jsonschema:"string id of the queue op"`
}

// ───── small helpers ─────

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func idParam(id int64) map[string]string { return map[string]string{"id": itoa(id)} }

func nameParam(name string) map[string]string { return map[string]string{"name": name} }

// filtersToQuery flattens a filter map into url.Values, stringifying each
// scalar value (JSON numbers arrive as float64; %v renders them cleanly).
func filtersToQuery(m map[string]any) url.Values {
	if len(m) == 0 {
		return nil
	}
	q := url.Values{}
	for k, v := range m {
		if v == nil {
			continue
		}
		switch t := v.(type) {
		case float64:
			// Render integers without a trailing ".0".
			if t == float64(int64(t)) {
				q.Set(k, strconv.FormatInt(int64(t), 10))
			} else {
				q.Set(k, strconv.FormatFloat(t, 'f', -1, 64))
			}
		case string:
			q.Set(k, t)
		case bool:
			q.Set(k, strconv.FormatBool(t))
		default:
			q.Set(k, fmt.Sprintf("%v", t))
		}
	}
	return q
}

// newExternalAdminWithPublicURL builds the external handler the MCP admin
// surface wraps, with the same public-URL context the native /admin routes
// give it. Without this the agent-facing surface would report a green probe
// and none of the advisories the UI shows.
func newExternalAdminWithPublicURL(d AIAdminDeps) *ExternalAdmin {
	h := NewExternalAdmin(d.Store, d.Caps, d.External, d.EnvManagedExternal)
	h.AttachPublicURL(d.PublicURL, d.PublicURLSet)
	h.ReversePath = d.ReversePath
	return h
}
