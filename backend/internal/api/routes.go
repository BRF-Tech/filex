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

	"gitlab.com/brftech/filemanager/backend/internal/api/handlers"
	"gitlab.com/brftech/filemanager/backend/internal/auth"
	"gitlab.com/brftech/filemanager/backend/internal/capability"
	"gitlab.com/brftech/filemanager/backend/internal/config"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/notify"
	"gitlab.com/brftech/filemanager/backend/internal/onlyoffice"
	"gitlab.com/brftech/filemanager/backend/internal/ops"
	"gitlab.com/brftech/filemanager/backend/internal/queue"
	"gitlab.com/brftech/filemanager/backend/internal/replica"
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
	OnlyOffice       *onlyoffice.Service
	Ops              *ops.Service
	Queue            queue.Driver
	Notify           notify.Service
	ReplicaService   *replica.Service
	ReplicaCron      *replica.CronScheduler
	ReplicaReloader  *replica.RulesReloader
	StorageResolver  func(int64) (storage.Driver, error)
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
	mh := handlers.NewManager(d.Store, d.StorageResolver)
	uh := handlers.NewUpload(d.Store, d.StorageResolver, d.Thumbs)
	ah := handlers.NewArchive(d.Store, d.StorageResolver)
	sh := handlers.NewShare(d.Share, d.Store, d.StorageResolver, d.Cfg.PublicURL)
	oh := handlers.NewOps(d.Ops)
	ooh := handlers.NewOnlyOffice(d.OnlyOffice, d.Store, d.StorageResolver)
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
	queueH := handlers.NewQueue(d.Queue)
	notifH := handlers.NewNotifications(d.Notify)
	replicaH := handlers.NewReplica(d.Store, d.ReplicaService, d.ReplicaCron, d.ReplicaReloader)

	// ────── public viewer ──────
	r.Get("/api/files/share/{token}", sh.HandleMetadata)
	r.Get("/s/{token}", sh.HandleDownload)
	r.Post("/s/{token}", sh.HandleDownload) // PIN form posts to same URL

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
			r.Get("/manager", mh.List)
			r.Post("/manager", mh.Mutate)
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

			r.Get("/onlyoffice/config", ooh.Config)
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
				r.Get("/{id}", stg.Get)
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
