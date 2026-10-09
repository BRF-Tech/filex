// Package handlers - app_store_view.go
//
// The embedded store screen (#162, docs/APP-PLUGINS.md → The store screen):
// filex draws a trusted store's catalog with its own components, read on the
// SERVER from the store's signed index (the browser never talks to the store;
// the Content-Security-Policy does not change), and a person who sees it
// leaves a request on the same Install requests list an API key uses.
//
// The administrator's routes, inside /api/admin/app-plugins (the platform
// operator, signed in to the panel - AppStore.gate, as every store route):
//
//	GET    /store-view[?tenant=<id>]             - who sees the screen in a scope
//	PUT    /store-view                           - {tenant?, settings}
//	GET    /stores/connection?store=<origin>     - is this filex connected to the store
//	POST   /stores/connection                    - {store, code}: connect with the store's one-time code
//	DELETE /stores/connection?store=<origin>     - disconnect
//
// A person's routes, /api/app-store (signed in to filex in a browser, or the
// desktop app's own pairing; any other API key is refused - this is a
// person's screen, its requests a person's words; personCaller):
//
//	GET  /api/app-store                          - {visible, stores}
//	GET  /api/app-store/catalog?store=<origin>   - the catalog, as filex verified it
//	GET  /api/app-store/media?store=&file=       - an icon of that catalog
//	GET  /api/app-store/requests                 - the requests this person left
//	POST /api/app-store/requests                 - {store, app, reason}: leave one
//
// ⚠ Nothing here installs, trusts or asks a store for an install link: those
// stay the administrator's (app_store_intent.go, plugin_requests.go).
package handlers

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/pluginreq"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// StoreGroups is the part of db.Store the store screen asks: a person's
// groups, and the groups a setting may name.
type StoreGroups interface {
	ListUserGroupMemberships(ctx context.Context, userID int64) ([]*model.GroupMember, error)
	GetGroup(ctx context.Context, id int64) (*model.Group, error)
}

// MountUser registers a person's store routes (at /api/app-store).
func (h *AppStore) MountUser(r chi.Router) {
	r.Get("/", h.Status)
	r.Get("/catalog", h.UserCatalog)
	r.Get("/media", h.UserMedia)
	r.Get("/requests", h.MyRequests)
	r.Post("/requests", h.LeaveRequest)
}

// viewScope is the scope of a request's tenant: (multi-tenant mode, key).
// A request in multi-tenant mode without a tenant it can be held to is
// answered with a scope nobody set (fails closed).
func viewScope(r *http.Request) (bool, int64, string) {
	sc, ok := tenant.FromContext(r.Context())
	if !ok {
		return false, 0, appstore.ScopeDefault
	}
	if sc == nil {
		return true, 0, appstore.Scope(true, 0)
	}
	return true, sc.ProviderID, appstore.Scope(true, sc.ProviderID)
}

// ── The administrator's settings ───────────────────────────────────────

// storeRow is a trusted store as the settings show it.
type storeRow struct {
	Origin    string `json:"origin"`
	Source    string `json:"source"`
	Connected bool   `json:"connected"`
}

func (h *AppStore) trustedRows(ctx context.Context) ([]storeRow, error) {
	list, err := h.Svc.ListTrust(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]storeRow, 0, len(list))
	for _, t := range list {
		out = append(out, storeRow{Origin: t.Origin, Source: t.Source, Connected: h.Svc.Connected(ctx, t.Origin)})
	}
	return out, nil
}

// settingsScope reads the tenant an administrator's request names (multi-
// tenant mode; their own by default).
func settingsScope(r *http.Request, raw string) (bool, int64, string, bool) {
	multi, own, scope := viewScope(r)
	if !multi {
		return false, 0, scope, true
	}
	if strings.TrimSpace(raw) == "" {
		return true, own, scope, true
	}
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return true, 0, "", false
	}
	return true, id, appstore.Scope(true, id), true
}

// GetView answers a scope's store screen settings.
func (h *AppStore) GetView(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "reading the store screen's settings") {
		return
	}
	multi, tid, scope, ok := settingsScope(r, r.URL.Query().Get("tenant"))
	if !ok {
		writeErrorSaid(w, r, http.StatusBadRequest, "bad_request", "bad_tenant", nil)
		return
	}
	v, err := h.Svc.View(r.Context(), scope)
	if err != nil {
		storeFail(w, r, err)
		return
	}
	if v == nil {
		v = &appstore.ViewSettings{Audience: appstore.AudienceEveryone, Stores: []string{}, Roles: []string{}, Groups: []int64{}}
	}
	rows, err := h.trustedRows(r.Context())
	if err != nil {
		storeFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"multi_tenant": multi, "tenant": tid, "settings": v, "stores": rows})
}

// PutView saves a scope's store screen settings.
func (h *AppStore) PutView(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "changing the store screen's settings") {
		return
	}
	var req struct {
		Tenant   string                `json:"tenant"`
		Settings appstore.ViewSettings `json:"settings"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	multi, tid, scope, ok := settingsScope(r, req.Tenant)
	if !ok {
		writeErrorSaid(w, r, http.StatusBadRequest, "bad_request", "bad_tenant", nil)
		return
	}
	// The groups must exist, and in multi-tenant mode belong to that tenant
	// (or to the whole install): a setting cannot name another tenant's people.
	for _, id := range req.Settings.Groups {
		g, err := h.groupOf(r.Context(), id)
		if err != nil || g == nil || (multi && !perm.GroupInScope(g, &tid)) {
			writeErrorSaid(w, r, http.StatusBadRequest, "bad_request", "bad_group", apierr.Params{"id": strconv.FormatInt(id, 10)})
			return
		}
	}
	auth.SkipAuditRow(r.Context())
	v, err := h.Svc.SetView(r.Context(), scope, req.Settings, actorIDOf(r), actorName(r))
	if err != nil {
		storeFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"multi_tenant": multi, "tenant": tid, "settings": v})
}

func (h *AppStore) groupOf(ctx context.Context, id int64) (*model.Group, error) {
	if h.Groups == nil {
		return nil, nil
	}
	return h.Groups.GetGroup(ctx, id)
}

// ── The connection ─────────────────────────────────────────────────────

// GetConnection answers whether this filex is connected to a store.
func (h *AppStore) GetConnection(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "reading a store connection") {
		return
	}
	origin, ok := h.origin(w, r, r.URL.Query().Get("store"))
	if !ok {
		return
	}
	v, err := h.Svc.Connection(r.Context(), origin)
	if err != nil {
		storeFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// Connect connects this filex to a trusted store with the one-time code the
// store's "My instances" page made.
func (h *AppStore) Connect(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r, "connecting a store") {
		return
	}
	if h.Demo {
		writeErrorSaid(w, r, http.StatusForbidden, "demo", "demo_store", nil)
		return
	}
	var req struct {
		Store string `json:"store"`
		Code  string `json:"code"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	origin, ok := h.origin(w, r, req.Store)
	if !ok {
		return
	}
	self, err := h.instance(r)
	if err != nil {
		storeFail(w, r, err)
		return
	}
	auth.SkipAuditRow(r.Context())
	v, err := h.Svc.Connect(r.Context(), origin, req.Code, self, actorIDOf(r), actorName(r))
	if err != nil {
		storeFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// Disconnect forgets this filex's key for a store (and tells the store).
func (h *AppStore) Disconnect(w http.ResponseWriter, r *http.Request) {
	h.dropFromStore(w, r, "disconnecting a store", h.Svc.Disconnect)
}

// ── A person's screen ──────────────────────────────────────────────────

// viewer answers the settings of the caller's scope and whether the caller
// sees the screen.
func (h *AppStore) viewer(r *http.Request) (*appstore.ViewSettings, bool) {
	u := auth.UserFrom(r.Context())
	if u == nil || h.Svc == nil || h.Admin == nil || h.Admin.Registry == nil || !personCaller(r) {
		return nil, false
	}
	_, _, scope := viewScope(r)
	v, err := h.Svc.View(r.Context(), scope)
	if err != nil || v == nil {
		return nil, false
	}
	var groups []int64
	if h.Groups != nil && v.Audience == appstore.AudienceGroups {
		if ms, err := h.Groups.ListUserGroupMemberships(r.Context(), u.ID); err == nil {
			for _, m := range ms {
				groups = append(groups, m.GroupID)
			}
		}
	}
	return v, v.Allows(u.Role, groups)
}

// personGate: a person signed in to filex, never an API key.
func (h *AppStore) personGate(w http.ResponseWriter, r *http.Request) bool {
	if auth.UserFrom(r.Context()) == nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", nil)
		return false
	}
	if personCaller(r) {
		return true
	}
	writeErrorSaid(w, r, http.StatusForbidden, "session_required", "store_screen_person", nil)
	return false
}

// personCaller reports whether the request is a PERSON's: a browser session
// (no API key on it), or the desktop app's own pairing - the key
// /api/auth/desktop/complete mints for one person's client (Source desktop,
// kind user; using it is access.desktop). Every other API key - a script, an
// agent, an embed's app token - is not: the store screen's requests are a
// person's words (#162; the desktop shows the same screen, docs/DESKTOP.md).
func personCaller(r *http.Request) bool {
	tok := auth.TokenFrom(r.Context())
	return tok == nil || (tok.Source == model.TokenSourceDesktop && !tok.IsApp())
}

// visibleStore: the caller sees the screen and origin is one of its stores.
func (h *AppStore) visibleStore(w http.ResponseWriter, r *http.Request, raw string) (string, bool) {
	if !h.personGate(w, r) {
		return "", false
	}
	v, ok := h.viewer(r)
	if !ok {
		// 404, not 403: for this account there is no store screen at all. (A
		// 403 here would read as an administrator's door to the route table's
		// walk - shop_window_route_table_test.go - which it is not: the screen
		// is the product's, shown or not by a setting.)
		writeError(w, r, http.StatusNotFound, "store_screen_hidden", nil)
		return "", false
	}
	origin, err := appstore.NormalizeOrigin(raw, h.Svc.Loopback())
	if err != nil || !slices.Contains(v.Stores, origin) {
		writeErrorSaid(w, r, http.StatusNotFound, "not_found", "store_unknown", nil)
		return "", false
	}
	return origin, true
}

// Status answers whether the caller sees the store screen, and its stores.
// An API key, or a filex with the store off, is answered "not visible".
func (h *AppStore) Status(w http.ResponseWriter, r *http.Request) {
	v, ok := h.viewer(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"visible": false, "stores": []string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"visible": true, "stores": v.Stores})
}

// catalogRow is a catalog app as the screen draws it, with what is
// installed here.
type catalogRow struct {
	appstore.CatalogApp
	// InstalledVersion is the version installed here. For a storage plugin
	// only the server's administrator is told it (storageOperator): a
	// storage plugin is the whole server's, and its version is no other
	// person's business - nor, in a multi-tenant filex, another tenant's.
	InstalledVersion string `json:"installed_version,omitempty"`
	// PermissionRows are the permissions in the reader's language: the rows
	// the install review shows (the reasons come with the review itself).
	PermissionRows []wasmplugin.PermissionRow `json:"permission_rows"`
	// State is the server's answer for this person and this entry:
	// installed (this version is here), update (an older one is), pending
	// (their request waits for an administrator) or none. For a storage
	// plugin, a person who is not the administrator hears only installed
	// (some version is here), pending or none - never update.
	State string `json:"state"`
	// Storage is a storage plugin's part of the row (#215), in the reader's
	// words; absent for an app.
	Storage *catalogStorage `json:"storage,omitempty"`
}

// catalogStorage is what the store screen says of a storage plugin: whether
// the store has a build for this server, what its run proved. Platform (this
// server's operating system and processor) is said to the administrator
// only.
type catalogStorage struct {
	Platform     string              `json:"platform,omitempty"`
	ForHere      bool                `json:"for_here"`
	Summary      string              `json:"summary"`
	Capabilities []storageCapability `json:"capabilities"`
}

// Catalog row states (catalogRow.State).
const (
	rowInstalled = "installed"
	rowUpdate    = "update"
	rowPending   = "pending"
	rowNone      = "none"
)

// UserCatalog answers a store's catalog, as filex verified it.
func (h *AppStore) UserCatalog(w http.ResponseWriter, r *http.Request) {
	origin, ok := h.visibleStore(w, r, r.URL.Query().Get("store"))
	if !ok {
		return
	}
	c, err := h.Svc.Catalog(r.Context(), origin)
	if err != nil {
		storeFail(w, r, err)
		return
	}
	lang := langOf(r)
	pending := h.pendingStoreApps(r, origin)
	operator := storageOperator(r)
	rows := make([]catalogRow, 0, len(c.Apps))
	for _, a := range c.Apps {
		row := catalogRow{CatalogApp: a, PermissionRows: []wasmplugin.PermissionRow{}}
		installedHere := false
		if a.Kind == appstore.KindStorage {
			// A storage plugin: shown only where storage plugins run.
			if h.Plugins == nil {
				continue
			}
			row.Storage = h.catalogStorageOf(lang, a, operator)
			if st, _ := h.Plugins.ByName(r.Context(), a.Name); st != nil {
				if operator {
					row.InstalledVersion = st.Version
				} else {
					// The least a person needs: it is here, nothing more.
					installedHere = true
				}
			}
		} else {
			for _, id := range a.Permissions {
				label := id
				if p, err := wasmplugin.ParsePermission(id); err == nil {
					label = p.Label(lang)
				}
				row.PermissionRows = append(row.PermissionRows, wasmplugin.PermissionRow{ID: id, Label: label})
			}
			if p, ok := h.Admin.Registry.ByName(a.Name); ok {
				row.InstalledVersion = p.Row.Version
			}
		}
		switch {
		case installedHere:
			row.State = rowInstalled
		case row.InstalledVersion != "" && row.InstalledVersion == a.Version:
			row.State = rowInstalled
		case pending[a.Name]:
			row.State = rowPending
		case row.InstalledVersion != "":
			row.State = rowUpdate
		default:
			row.State = rowNone
		}
		rows = append(rows, row)
	}
	body := map[string]any{"store": c.Store, "serial": c.Serial, "fetched_at": c.FetchedAt, "stale": c.Stale, "apps": rows}
	if h.Plugins != nil {
		// What the storage tab says first: a storage plugin is a program an
		// administrator installs on the server, outside any sandbox.
		body["storage_note"] = srvtext.Text(lang, "server.store_storage.screen_note", nil)
	}
	writeJSON(w, http.StatusOK, body)
}

// storageOperator reports whether the reader of the store screen is the one
// who administers this server's storage plugins: an administrator, of the
// platform (in a multi-tenant filex, the supertenant's - the same people the
// storage plugin pages and the store install answer, requireSupertenant).
// Only they are told which version of a storage plugin is installed and
// what this server runs on.
func storageOperator(r *http.Request) bool {
	u := auth.UserFrom(r.Context())
	if u == nil || u.Role != model.RoleAdmin {
		return false
	}
	scope, scoped := tenant.FromContext(r.Context())
	return !scoped || (scope != nil && scope.IsSupertenant)
}

// pendingStoreApps are the entries of a store the caller has a request
// waiting for.
func (h *AppStore) pendingStoreApps(r *http.Request, origin string) map[string]bool {
	out := map[string]bool{}
	u := auth.UserFrom(r.Context())
	if h.Requests == nil || u == nil {
		return out
	}
	mine, err := h.Requests.Mine(r.Context(), u.ID)
	if err != nil {
		return out
	}
	for _, req := range mine {
		if req.Status != model.PluginRequestPending {
			continue
		}
		if store, app, _, ok := pluginreq.StoreSourceOf(req); ok && store == origin {
			out[app] = true
		}
	}
	return out
}

// catalogStorageOf is a storage plugin row's own part: whether the store has
// a build for this server, and what its run proved, said by the server.
// operator (storageOperator): the reader is the server's administrator, who
// is also told the server's platform; anybody else hears only whether there
// is a build for here.
func (h *AppStore) catalogStorageOf(lang string, a appstore.CatalogApp, operator bool) *catalogStorage {
	plat := h.Plugins.Platform()
	cs := &catalogStorage{Capabilities: []storageCapability{}}
	if operator {
		cs.Platform = plat
	}
	_, cs.ForHere = a.Builds[plat]
	switch c := a.Conformance; {
	case !cs.ForHere && !operator:
		cs.Summary = srvtext.Text(lang, "server.store_storage.not_for_here_user", nil)
	case !cs.ForHere:
		cs.Summary = srvtext.Text(lang, "server.store_storage.not_for_here", srvtext.Vars{"platform": plat})
	case c != nil:
		cs.Summary = srvtext.Text(lang, "server.store_storage.conformance_short", srvtext.Vars{"passed": strconv.Itoa(c.Passed), "platform": c.Platform})
	default:
		cs.Summary = srvtext.Text(lang, "server.store_storage.conformance_none", nil)
	}
	if c := a.Conformance; c != nil {
		proved := map[string]bool{}
		for _, id := range c.Capabilities {
			proved[id] = true
		}
		for _, id := range storageCapabilityIDs {
			if proved[id] {
				cs.Capabilities = append(cs.Capabilities, storageCapability{ID: id, Label: srvtext.Text(lang, "server.store_storage.cap."+id, nil)})
			}
		}
	}
	return cs
}

// UserMedia answers an icon of a store's catalog, through filex.
func (h *AppStore) UserMedia(w http.ResponseWriter, r *http.Request) {
	origin, ok := h.visibleStore(w, r, r.URL.Query().Get("store"))
	if !ok {
		return
	}
	b, ctype, err := h.Svc.Media(r.Context(), origin, r.URL.Query().Get("file"))
	if err != nil {
		storeFail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	_, _ = w.Write(b)
}

// MyRequests answers the requests the caller left from the store screen.
func (h *AppStore) MyRequests(w http.ResponseWriter, r *http.Request) {
	if !h.personGate(w, r) {
		return
	}
	if h.Requests == nil {
		writeJSON(w, http.StatusOK, map[string]any{"requests": []pluginRequestWire{}})
		return
	}
	rows, err := h.Requests.Mine(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	out := make([]pluginRequestWire, 0, len(rows))
	for _, row := range rows {
		v := pluginRequestView(row, langOf(r), false)
		// The decider's note is the administrator's to the requester; who
		// decided and the result's detail stay in the panel.
		v.DecidedBy, v.Decider, v.Result, v.TokenLabel = nil, "", nil, ""
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out})
}

// LeaveRequest records a person's request for an app of a store's catalog:
// the same Install requests list an API key leaves requests on, decided the
// same way by an administrator signed in to the panel.
func (h *AppStore) LeaveRequest(w http.ResponseWriter, r *http.Request) {
	if !h.personGate(w, r) {
		return
	}
	var req struct {
		Store  string `json:"store"`
		App    string `json:"app"`
		Reason string `json:"reason"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	origin, ok := h.visibleStore(w, r, req.Store)
	if !ok {
		return
	}
	if h.Requests == nil {
		writeError(w, r, http.StatusServiceUnavailable, "app_plugins_disabled", nil)
		return
	}
	c, err := h.Svc.Catalog(r.Context(), origin)
	if err != nil {
		storeFail(w, r, err)
		return
	}
	a, ok := c.App(strings.TrimSpace(req.App))
	if !ok || (a.Kind == appstore.KindStorage && h.Plugins == nil) {
		writeErrorSaid(w, r, http.StatusNotFound, "not_found", "store_app_unknown", nil)
		return
	}
	if a.Kind == appstore.KindStorage {
		if _, forHere := a.Builds[h.Plugins.Platform()]; !forHere {
			storeFail(w, r, &appstore.Error{Code: appstore.CodeNoBuild,
				Message: srvtext.Text(langOf(r), "server.store_storage.not_for_here", srvtext.Vars{"platform": h.Plugins.Platform()})})
			return
		}
	}
	auth.SkipAuditRow(r.Context())
	row, created, err := h.Requests.CreateStore(r.Context(), storeEntryOf(origin, a), req.Reason, actorOf(r))
	if err != nil {
		(&PluginRequests{Svc: h.Requests}).fail(w, err, nil, langOf(r))
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	v := pluginRequestView(row, langOf(r), false)
	v.DecidedBy, v.Decider, v.Result, v.TokenLabel = nil, "", nil, ""
	writeJSON(w, status, map[string]any{"request": v, "created": created})
}

func storeEntryOf(origin string, a appstore.CatalogApp) pluginreq.StoreEntry {
	return pluginreq.StoreEntry{Store: origin, App: a.Name, Kind: a.Kind, Version: a.Version, Label: a.Label, Summary: a.Summary,
		Publisher: a.Publisher, Repo: a.Repo, Permissions: a.Permissions, ManifestSHA256: a.ManifestSHA256,
		WasmSHA256: a.WasmSHA256, UISHA256: a.UISHA256, Builds: a.Builds}
}
