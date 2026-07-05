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
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/replica"
	"github.com/brf-tech/filex/backend/internal/search"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/trash"
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
	grants      *Grants
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
	Index           *search.Index
	ReplicaService  *replica.Service
	ReplicaCron     *replica.CronScheduler
	ReplicaReloader *replica.RulesReloader
}

// NewAIAdmin constructs the admin AI surface from shared deps. Each wrapped
// handler is the same type the native /admin routes use, so behaviour is
// identical — only the auth front-door differs.
func NewAIAdmin(d AIAdminDeps) *AIAdmin {
	return &AIAdmin{
		store:       d.Store,
		dash:        NewDashboard(d.Store, d.Caps, d.Worker),
		settings:    NewSettings(d.Store),
		users:       NewUsers(d.Store),
		usersAdm:    NewUsersAdmin(d.Store),
		storages:    NewStorages(d.Store, d.Worker),
		storagesAdm: NewStoragesAdmin(d.Store),
		syncAdm:     NewSyncAdmin(d.Store),
		sharesAdm:   NewSharesAdmin(d.Store),
		trash:       NewTrash(d.Trash, d.Store),
		searchAdm:   NewSearchAdmin(d.Index, d.Store),
		authProv:    NewAuthProviders(d.Store),
		external:    NewExternalAdmin(d.Store, d.Caps),
		replica:     NewReplica(d.Store, d.ReplicaService, d.ReplicaCron, d.ReplicaReloader),
		repTargets:  NewReplicationTargets(d.Store),
		queue:       NewQueue(d.Queue),
		notif:       NewNotifications(d.Notify),
		audit:       NewAudit(d.Store),
		grants:      NewGrants(d.Store, acl.New(d.Store)),
	}
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
	cp := model.User{Role: model.RoleAdmin, Email: "ai-admin-token"}
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
		r.Delete("/{id}", a.trash.Purge)
	})

	r.Route("/search", func(r chi.Router) {
		r.Get("/stats", a.searchAdm.Stats)
		r.Post("/rebuild", a.searchAdm.Rebuild)
	})

	r.Route("/auth-providers", func(r chi.Router) {
		r.Get("/", a.authProv.List)
		r.Patch("/{name}", a.authProv.Update)
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

	r.Route("/audit", func(r chi.Router) {
		r.Get("/", a.audit.List)
	})

	// RBAC grants — the elevated admin principal is owner-exempt, so Create's
	// requireOwner passes and it can set any grant.
	r.Route("/grants", func(r chi.Router) {
		r.Get("/", a.grants.AdminList)
		r.Post("/", a.grants.Create)
		r.Delete("/{id}", a.grants.AdminDelete)
	})
}

// ───── in-process invoker (powers the MCP admin tools) ─────

// invoke runs an admin http.HandlerFunc in-process and returns the captured
// status code + raw response body. It builds a synthetic request carrying
// the admin principal, the chi URL params, the query string, and the JSON
// body, then drives the handler with a buffered recorder. No socket, no
// re-auth, no duplicated handler logic.
func (a *AIAdmin) invoke(ctx context.Context, principal *model.User, h http.HandlerFunc, method, path string, urlParams map[string]string, query url.Values, body any) (int, []byte) {
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

	rec := newBufRecorder()
	h(rec, req)
	a.auditInvoke(principal, method, path, urlParams, rec.status)
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
func (a *AIAdmin) auditInvoke(principal *model.User, method, path string, urlParams map[string]string, status int) {
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
	action, targetType, targetID := auth.AIAdminAction(method, path, urlParams["id"], urlParams["name"])
	if action == "" {
		return
	}
	entry := &model.AuditEntry{
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		CreatedAt:  time.Now(),
	}
	// elevatedPrincipal preserves the bound user's real ID (only the role is
	// lifted to admin), so the audit row attributes the change correctly.
	if principal != nil && principal.ID > 0 {
		uid := principal.ID
		entry.UserID = &uid
	}
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
type adminOut struct {
	Status int             `json:"status"`
	Result json.RawMessage `json:"result"`
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
	regAdminTool(r, "admin_users_create", "Create a user. body: {email, password, display_name?, role?, locale?, timezone?}.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.users.Create, method: http.MethodPost, path: "/api/ai/admin/users", body: in.Body}
		})
	regAdminTool(r, "admin_users_update", "Update a user by id. body may include any of {password, display_name, role, locale, timezone}.",
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
	regAdminTool(r, "admin_storages_sync", "Trigger an immediate sync run for a storage by id.",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.storages.TriggerSync, method: http.MethodPost, path: "/api/ai/admin/storages/" + itoa(in.ID) + "/sync",
				urlParams: idParam(in.ID)}
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
	regAdminTool(r, "admin_shares_revoke", "Revoke a share by id (soft — keeps audit trail).",
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
	regAdminTool(r, "admin_trash_restore", "Restore a trashed node. body: {node_id}.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.trash.Restore, method: http.MethodPost, path: "/api/ai/admin/trash/restore", body: in.Body}
		})
	regAdminTool(r, "admin_trash_empty", "Purge trash. body: {older_than_days?} (0/omitted wipes everything soft-deleted).",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.trash.AdminEmpty, method: http.MethodPost, path: "/api/ai/admin/trash/empty", body: in.Body}
		})
	regAdminTool(r, "admin_trash_purge", "Hard-delete a single trashed node by id.",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.trash.Purge, method: http.MethodDelete, path: "/api/ai/admin/trash/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
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
	regAdminTool(r, "admin_auth_providers_update", "Update an auth provider's config by name. body: the full config object.",
		func(in adminNameBodyIn) reqSpec {
			return reqSpec{handler: a.authProv.Update, method: http.MethodPatch, path: "/api/ai/admin/auth-providers/" + in.Name,
				urlParams: nameParam(in.Name), body: in.Body}
		})
	regAdminTool(r, "admin_auth_providers_test", "Test an auth provider by name.",
		func(in adminNameIn) reqSpec {
			return reqSpec{handler: a.authProv.Test, method: http.MethodPost, path: "/api/ai/admin/auth-providers/" + in.Name + "/test",
				urlParams: nameParam(in.Name)}
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
	regAdminTool(r, "admin_external_test", "Run a health probe against an external service by name.",
		func(in adminNameIn) reqSpec {
			return reqSpec{handler: a.external.Test, method: http.MethodPost, path: "/api/ai/admin/external/" + in.Name + "/test",
				urlParams: nameParam(in.Name)}
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

	// ── audit ──
	regAdminTool(r, "admin_audit_list", "List audit log entries. filters: {user_id, action, from, to, limit, offset}.",
		func(in adminFiltersIn) reqSpec {
			return reqSpec{handler: a.audit.List, method: http.MethodGet, path: "/api/ai/admin/audit", query: filtersToQuery(in.Filters)}
		})

	// ── RBAC grants (per-file/folder permissions) ──
	regAdminTool(r, "admin_grants_list", "List every per-file/folder RBAC grant (who has what level, on which path, in which storage).",
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.grants.AdminList, method: http.MethodGet, path: "/api/ai/admin/grants"}
		})
	regAdminTool(r, "admin_grant_set", "Grant a user access to a path. body: {path:\"<adapter>://<rel>\", user_id, level: viewer|editor|owner}. The storage must have RBAC enabled; a viewer account may only be granted viewer.",
		func(in adminBodyIn) reqSpec {
			return reqSpec{handler: a.grants.Create, method: http.MethodPost, path: "/api/ai/admin/grants", body: in.Body}
		})
	regAdminTool(r, "admin_grant_revoke", "Revoke a grant by its id (from admin_grants_list).",
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.grants.AdminDelete, method: http.MethodDelete, path: "/api/ai/admin/grants/" + itoa(in.ID),
				urlParams: idParam(in.ID)}
		})
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
