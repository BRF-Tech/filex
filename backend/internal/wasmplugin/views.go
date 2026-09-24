package wasmplugin

import (
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Surfaces: the declarative UI a plugin may ask filex to draw ─────────
//
// A plugin never ships markup or script. It returns a Surface: a tree of
// nodes whose types come from THIS catalogue, drawn by filex's own
// components. The host drops anything not in the catalogue before the
// surface reaches a browser, so a plugin cannot smuggle a node type the
// renderer would treat as raw HTML, and caps the size so a runaway plugin
// cannot hand the browser a million rows.

// SurfaceNodeTypes is the v1 catalogue.
var SurfaceNodeTypes = map[string]bool{
	"text": true, "form": true, "steps": true, "list": true, "progress": true,
	"people-picker": true, "pin-input": true, "file-chooser": true, "pdf-fields": true,
	"signature-pad": true, "preview": true, "divider": true, "row": true,
}

const (
	maxSurfaceNodes   = 500
	maxSurfaceDepth   = 8
	maxSurfaceActions = 8
)

// SanitizeSurface prunes unknown node types, caps counts and depth.
func SanitizeSurface(s *wire.Surface) {
	if s == nil {
		return
	}
	budget := maxSurfaceNodes
	s.Nodes = pruneNodes(s.Nodes, 0, &budget)
	if s.Nodes == nil {
		s.Nodes = []wire.Node{}
	}
	if len(s.Actions) > maxSurfaceActions {
		s.Actions = s.Actions[:maxSurfaceActions]
	}
	switch s.Size {
	case "", "sm", "md", "lg", "xl":
	default:
		s.Size = ""
	}
	if s.Open != nil && strings.TrimSpace(s.Open.Path) == "" {
		s.Open = nil
	}
}

// CheckOpen validates a surface's `open` against the plugin that answered:
// the screen it names must be that plugin's own, and it may not name both.
// The PATH is checked by the handler, which is where a caller's permissions
// are known — a screen must not become a way to look at a file the person
// asking could not open themselves.
func CheckOpen(p *Installed, s *wire.Surface) error {
	if s == nil || s.Open == nil {
		return nil
	}
	if s.Open.Action != "" && s.Open.View != "" {
		return &CallError{Code: CodePluginError, Message: "open names both an action and a view"}
	}
	if s.Open.Action != "" {
		if _, ok := p.Manifest.Action(s.Open.Action); !ok {
			return &CallError{Code: CodePluginError, Message: "open names an action this app does not have: " + s.Open.Action}
		}
	}
	if s.Open.View != "" {
		if _, ok := p.Manifest.View(s.Open.View); !ok {
			return &CallError{Code: CodePluginError, Message: "open names a view this app does not have: " + s.Open.View}
		}
	}
	return nil
}

func pruneNodes(nodes []wire.Node, depth int, budget *int) []wire.Node {
	if depth >= maxSurfaceDepth {
		return nil
	}
	out := make([]wire.Node, 0, len(nodes))
	for _, n := range nodes {
		if *budget <= 0 {
			break
		}
		if !SurfaceNodeTypes[n.Type] {
			continue
		}
		*budget--
		if len(n.Children) > 0 {
			n.Children = pruneNodes(n.Children, depth+1, budget)
		}
		out = append(out, n)
	}
	return out
}
