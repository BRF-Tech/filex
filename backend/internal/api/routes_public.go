package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/s3api"
	"github.com/brf-tech/filex/backend/internal/shortlink"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// ── An app's public routes: what the router tells the registry ─────────────
//
// docs/APP-PLUGINS-API.md → Public routes. An administrator gives a route an
// address in the app's setup; the registry asks the router two things about
// it - is this host one filex itself answers on, is this path one of filex's
// own - and hands the router the addresses it serves whenever they change.

// alwaysReserved are first path segments no route prefix may take on filex's
// own address whatever the router has today: the paths a browser, a crawler
// or a proxy asks every host for, and the SPA's own mount points.
var alwaysReserved = func() map[string]bool {
	out := map[string]bool{}
	for _, seg := range strings.Fields(`api admin drive files s d p z embed dav s3 healthz metrics
		assets static _appui _print filex-frame .well-known favicon.ico robots.txt
		manifest.webmanifest sw.js`) {
		out[seg] = true
	}
	return out
}()

// routerFirstSegments are the literal first segments of every route the
// router has (`/api/...` → api). A route whose first segment is a
// parameter (`/{chunk:...}`) names no segment of its own.
func routerFirstSegments(mux chi.Routes) map[string]bool {
	out := map[string]bool{}
	_ = chi.Walk(mux, func(_ string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		p := strings.TrimPrefix(route, "/")
		if i := strings.IndexByte(p, '/'); i >= 0 {
			p = p[:i]
		}
		if p == "" || strings.HasPrefix(p, "{") || p == "*" {
			return nil
		}
		out[strings.ToLower(strings.TrimSuffix(p, "*"))] = true
		return nil
	})
	return out
}

// wireRouteHosts hands the registry the router's answers and takes its
// served addresses into routes (which the host split, the own-address prefix
// and the certificate questions read).
func wireRouteHosts(d *Deps, mux chi.Routes, tenants tenanturl.Resolver, routes *shortlink.Snapshot) {
	reg := d.AppPlugins
	var once sync.Once
	var segs map[string]bool
	own := ownHosts(d)
	reg.SetRouteHosts(wasmplugin.RouteHosts{
		IsFilexHost: func(ctx context.Context, host string) bool {
			if own[host] {
				return true
			}
			if d.Cfg.S3.Enabled && d.Cfg.S3.Domain != "" && s3api.HostMatches(d.Cfg.S3.Domain, host) {
				return true
			}
			if td := db.TenantDomain(); td != "" && (host == td || strings.HasSuffix(host, "."+td)) {
				return true
			}
			p, err := d.Store.GetProviderByHost(ctx, host)
			return err == nil && p != nil
		},
		Reserved: func(first string) bool {
			once.Do(func() { segs = routerFirstSegments(mux) })
			first = strings.ToLower(first)
			return alwaysReserved[first] || segs[first]
		},
		OwnOrigin: func(ctx context.Context, tenant int64) string {
			if tenant == 0 {
				return tenants.Fallback()
			}
			return tenants.ForProvider(ctx, tenant)
		},
		TenantExists: func(ctx context.Context, id int64) bool {
			p, err := d.Store.GetProvider(ctx, id)
			return err == nil && p != nil && !p.IsSupertenant
		},
	})
	reg.SetRoutesChanged(func() {
		routes.Store(shortlink.NewRoutes(reg.RouteBases()))
	})
}

// ownHosts are the hosts filex's own configuration names: its address, the
// apps' interface origin, the office editor's frame origin.
func ownHosts(d *Deps) map[string]bool {
	out := map[string]bool{}
	for _, raw := range []string{d.Cfg.PublicURL, d.Cfg.AppUIOrigin, d.Cfg.ExternalServices.OnlyOffice.FrameOrigin} {
		if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.Hostname() != "" {
			out[strings.ToLower(u.Hostname())] = true
		}
	}
	return out
}

// routeMissAlarm is told when the misses of every client together pass the
// limiter's hourly alarm: many addresses guessing at once.
func routeMissAlarm(_ *Deps) func(int) {
	return func(n int) {
		slog.Warn("app routes: many visits to addresses that lead nowhere in the last hour; something may be guessing", slog.Int("misses", n))
	}
}
