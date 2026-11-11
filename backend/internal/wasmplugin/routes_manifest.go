package wasmplugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/brf-tech/filex/backend/internal/shortlink"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── routes, setup and link_shortener in a manifest (filex 0.60.0) ──────────
//
// docs/APP-PLUGINS-API.md → Public routes, Setting an app up, The
// link-shortener role. The three blocks lean on each other: a route has no
// address until an administrator gives it one in the setup wizard, so an app
// with routes is set up before it runs; the shortener's addresses are one of
// its routes'.

// maxRoutes bounds a manifest's routes.
const maxRoutes = 8

// checkRoutes validates `routes`, `setup` and `link_shortener`. seen is the
// manifest's own permissions.
func (m *Manifest) checkRoutes(seen map[Permission]bool) error {
	text := func(what string, t wire.Text) error {
		if strings.TrimSpace(t["en"]) == "" {
			return fmt.Errorf("manifest: %s: en is required", what)
		}
		for _, lang := range m.Languages {
			if strings.TrimSpace(t[lang]) == "" {
				return fmt.Errorf("manifest: %s has no %q text, but the plugin declares that language", what, lang)
			}
		}
		return nil
	}
	if len(m.Routes) > maxRoutes {
		return fmt.Errorf("manifest: at most %d routes", maxRoutes)
	}
	routes := map[string]bool{}
	for i := range m.Routes {
		r := &m.Routes[i]
		if !idRe.MatchString(r.ID) {
			return fmt.Errorf("manifest: routes[%d]: id %q must match %s", i, r.ID, idRe)
		}
		if routes[r.ID] {
			return fmt.Errorf("manifest: routes: duplicate id %q", r.ID)
		}
		routes[r.ID] = true
		if err := text(fmt.Sprintf("routes[%d] (%s) label", i, r.ID), r.Label); err != nil {
			return err
		}
		if r.DefaultPrefix != "" {
			p, err := shortlink.CheckPrefix(r.DefaultPrefix, true, nil)
			if err != nil {
				return fmt.Errorf("manifest: routes[%d] (%s): default_prefix %q: one to three segments of a-z, 0-9 and -", i, r.ID, r.DefaultPrefix)
			}
			r.DefaultPrefix = p
		}
	}
	if len(m.Routes) > 0 {
		// The permission is not one an interface can use (uiOnlyPerm), so
		// NeedsModule holds: an app with routes installs with a module.
		if !seen[PermPublicRoutes] {
			return fmt.Errorf("manifest: routes need the public_routes permission")
		}
	} else if seen[PermPublicRoutes] {
		return fmt.Errorf("manifest: the public_routes permission without routes")
	}
	if ls := m.LinkShortener; ls != nil {
		if !seen[PermLinkShortener] {
			return fmt.Errorf("manifest: link_shortener needs the link_shortener permission")
		}
		if !routes[ls.Route] {
			return fmt.Errorf("manifest: link_shortener: route %q is not declared in routes", ls.Route)
		}
		if ls.Requires != "" && !m.declaresUserPerm(ls.Requires) {
			return fmt.Errorf("manifest: link_shortener: requires %q, which is not declared in user_permissions", ls.Requires)
		}
	} else if seen[PermLinkShortener] {
		return fmt.Errorf("manifest: the link_shortener permission without a link_shortener block")
	}
	if st := m.Setup; st != nil {
		if st.View != "" {
			v, ok := m.View(st.View)
			if !ok {
				return fmt.Errorf("manifest: setup: view %q is not declared in views", st.View)
			}
			if v.Placement != wire.PlacementSetup {
				return fmt.Errorf("manifest: setup: view %q has placement %q, not setup", st.View, v.Placement)
			}
		}
		if st.Reset != nil {
			if err := text("setup.reset.warning", st.Reset.Warning); err != nil {
				return err
			}
		}
	}
	for i := range m.Views {
		v := &m.Views[i]
		if v.Placement != wire.PlacementSetup {
			continue
		}
		if m.Setup == nil || m.Setup.View != v.ID {
			return fmt.Errorf("manifest: views[%d] (%s): a setup view is the one setup.view names", i, v.ID)
		}
		if v.Requires != "" {
			return fmt.Errorf("manifest: views[%d] (%s): a setup view is an administrator's - leave requires out", i, v.ID)
		}
	}
	for i := range m.Actions {
		if v, ok := m.View(m.Actions[i].View); ok && v.Placement == wire.PlacementSetup {
			return fmt.Errorf("manifest: actions[%d] (%s): the setup view is opened by the setup wizard, not by an action", i, m.Actions[i].ID)
		}
	}
	return nil
}

func (m *Manifest) declaresUserPerm(id string) bool {
	for _, up := range m.UserPermissions {
		if up.ID == id {
			return true
		}
	}
	return false
}

// NeedsSetup reports whether the app is set up by an administrator before it
// runs: it says so (`setup`), or it has routes, which have no address until
// it is given one.
func (m *Manifest) NeedsSetup() bool {
	return m != nil && (m.Setup != nil || len(m.Routes) > 0)
}

// Route is the manifest's route by id.
func (m *Manifest) Route(id string) (wire.Route, bool) {
	for _, r := range m.Routes {
		if r.ID == id {
			return r, true
		}
	}
	return wire.Route{}, false
}

// checkRouteExports refuses a module whose manifest has routes, or the
// shortener role, but which exports no `route` / `shorten` - found at
// install, not at the first visit.
func checkRouteExports(ctx context.Context, m *Manifest, c exportProbe) error {
	need := []string{}
	if len(m.Routes) > 0 {
		need = append(need, "route")
	}
	if m.LinkShortener != nil {
		need = append(need, "shorten")
	}
	for _, export := range need {
		has, err := c.HasExport(ctx, export)
		if err != nil {
			return err
		}
		if !has {
			return &CallError{Code: CodeRefused, Export: export,
				Message: "the manifest needs a " + export + " export, but the module has none"}
		}
	}
	return nil
}
