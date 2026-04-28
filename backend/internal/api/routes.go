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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"gitlab.com/brftech/filemanager/backend/internal/api/handlers"
	"gitlab.com/brftech/filemanager/backend/internal/auth"
	"gitlab.com/brftech/filemanager/backend/internal/capability"
	"gitlab.com/brftech/filemanager/backend/internal/config"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/search"
	"gitlab.com/brftech/filemanager/backend/internal/share"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
	syncpkg "gitlab.com/brftech/filemanager/backend/internal/sync"
	"gitlab.com/brftech/filemanager/backend/internal/thumb"
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
	StorageResolver func(int64) (storage.Driver, error)
	Embed           embed.FS // web/dist + admin
	LocalAuth       auth.LoginDriver
	OIDCAuth        auth.OIDCDriver
}

// BuildRouter constructs the chi router with all routes wired up.
func BuildRouter(d *Deps) http.Handler {
	r := chi.NewRouter()

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
	mh := handlers.NewManager(d.Store)
	uh := handlers.NewUpload(d.Store, d.StorageResolver)
	ah := handlers.NewArchive(d.Store, d.StorageResolver)
	sh := handlers.NewShare(d.Share, d.Store, d.StorageResolver)
	oh := handlers.NewOps(d.Store, d.StorageResolver)
	th := handlers.NewThumb(d.Store, d.Thumbs)
	ch := handlers.NewCapabilities(d.Caps)
	stg := handlers.NewStorages(d.Store, d.Worker)
	ush := handlers.NewUsers(d.Store)
	seth := handlers.NewSettings(d.Store)
	authh := handlers.NewAuth(d.Store, d.LocalAuth, d.OIDCAuth, d.Cfg.PublicURL)
	sxh := handlers.NewSearch(d.Index, d.Store)

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

	// ────── public viewer ──────
	r.Get("/api/files/share/{token}", sh.HandleMetadata)
	r.Get("/s/{token}", sh.HandleDownload)

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
	r.Get("/api/capabilities", ch.Get)

	// ────── authenticated user routes ──────
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(true))

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

		r.Route("/api/files", func(r chi.Router) {
			r.Get("/manager", mh.List)
			r.Get("/stat", mh.Stat)
			r.Get("/read", mh.Read)
			r.Post("/search", sxh.Search)
			r.Post("/ops", oh.Submit)
			r.Get("/ops/{id}", oh.Status)

			r.Post("/upload/init", uh.Init)
			r.Post("/upload/finalize", uh.Finalize)
			r.Post("/upload/abort", uh.Abort)

			r.Post("/archive/list", ah.List)
			r.Post("/archive/extract", ah.Extract)
			r.Post("/archive/add", ah.Add)

			r.Post("/share", sh.HandleCreate)
			r.Delete("/share/{id}", sh.HandleDelete)
		})
	})

	// ────── admin-only routes ──────
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(true))
		r.Use(auth.RequireAdmin)

		r.Route("/api/admin", func(r chi.Router) {
			r.Get("/dashboard", dashH.Get)

			r.Route("/storages", func(r chi.Router) {
				r.Get("/", stg.List)
				r.Post("/", stg.Create)
				r.Post("/test", storagesAdmH.Test)
				r.Patch("/{id}", stg.Update)
				r.Delete("/{id}", stg.Delete)
				r.Post("/{id}/sync", stg.TriggerSync)
				r.Get("/{id}/sync-runs", storagesAdmH.SyncRuns)
				r.Get("/{id}/drift", storagesAdmH.Drift)
			})

			r.Route("/users", func(r chi.Router) {
				r.Get("/", ush.List)
				r.Post("/", ush.Create)
				r.Patch("/{id}", ush.Update)
				r.Delete("/{id}", ush.Delete)
				r.Post("/{id}/reset-password", usersAdmH.ResetPassword)
			})

			r.Route("/settings", func(r chi.Router) {
				r.Get("/", seth.List)
				r.Put("/{key}", seth.Set)
			})

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
		})
	})

	// ────── healthz ──────
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// ────── embedded static ──────
	wireStatic(r, d.Embed)

	return r
}

// wireStatic mounts the embedded /admin and /embed.js routes.
func wireStatic(r chi.Router, fs embed.FS) {
	// /admin → admin SPA. Falls back to index.html for client routing.
	r.Get("/admin/*", func(w http.ResponseWriter, r *http.Request) {
		// TODO: implement embed FS subdir + SPA fallback.
		http.Error(w, "admin not embedded yet", http.StatusNotFound)
	})
	r.Get("/embed.js", func(w http.ResponseWriter, r *http.Request) {
		// TODO: implement embed.js bundling.
		http.Error(w, "embed.js not embedded yet", http.StatusNotFound)
	})
}
