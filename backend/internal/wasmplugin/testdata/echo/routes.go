package main

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Public routes, the shortener role, reset and setup (filex 0.60) ────────
//
// The routes tests install this module with a manifest widened by `routes`,
// `setup`, `link_shortener` and the data, public_routes and link_shortener
// permissions (describe only has to be a subset of what was installed). The
// route answers by the path it is handed:
//
//   - hit-<x>:   a redirect to https://example.com/<x>, counted (n/<x>) when
//     the visit is countable;
//   - page:      a page filex draws;
//   - sleep:     runs past any budget (the pool's time limit);
//   - crash:     traps (the instance must be replaced);
//   - bad:       answers a javascript: redirect (filex must refuse it);
//   - write:     tries to write its data from a visit (must be refused);
//   - s-<code>:  a code shorten made: its target, or a miss;
//   - anything else: a miss.

func route(in *wire.RouteInput) (*wire.RouteOutput, error) {
	p := in.Path
	switch {
	case strings.HasPrefix(p, "hit-"):
		x := strings.TrimPrefix(p, "hit-")
		if in.Countable {
			if _, err := pluginkit.DataIncr(wire.DataTenant, "n/"+x, 1); err != nil {
				return nil, err
			}
		}
		return &wire.RouteOutput{Redirect: "https://example.com/" + x}, nil
	case p == "page":
		return &wire.RouteOutput{Page: &wire.RoutePage{Title: wire.Text{"en": "A page"}, Text: wire.Text{"en": "Drawn by filex"},
			Link: "https://example.com/next", LinkLabel: wire.Text{"en": "Next"}}}, nil
	case p == "sleep":
		start := time.Now()
		for time.Since(start) < 10*time.Second {
		}
		return &wire.RouteOutput{Miss: true}, nil
	case p == "crash":
		var m map[string]int
		m["x"] = 1
		return nil, nil
	case p == "bad":
		return &wire.RouteOutput{Redirect: "javascript:alert(1)"}, nil
	case p == "write":
		if _, err := pluginkit.DataPut(wire.DataTenant, "w", "x", pluginkit.DataPutOptions{}); err != nil {
			return &wire.RouteOutput{Page: &wire.RoutePage{Title: wire.Text{"en": "refused: " + err.Error()}}}, nil
		}
		return &wire.RouteOutput{Page: &wire.RoutePage{Title: wire.Text{"en": "written"}}}, nil
	case strings.HasPrefix(p, "s-"):
		v, _, found, err := pluginkit.DataGet(wire.DataTenant, "c/"+strings.TrimPrefix(p, "s-"))
		if err != nil {
			return nil, err
		}
		if !found {
			return &wire.RouteOutput{Miss: true}, nil
		}
		link, target, _ := strings.Cut(v, " ")
		if link != "" {
			live, err := pluginkit.LinkLive(link)
			if err != nil {
				return nil, err
			}
			if !live {
				return &wire.RouteOutput{Miss: true}, nil
			}
		}
		return &wire.RouteOutput{Redirect: target}, nil
	}
	return &wire.RouteOutput{Miss: true}, nil
}

// shorten answers one code per link, kept as c/<code> = "<link> <target>"
// and s/<link> = <code>.
func shorten(in *wire.ShortenInput) (*wire.ShortenOutput, error) {
	if len(in.Bases) == 0 {
		return nil, errors.New("no bases")
	}
	base := in.Bases[0]
	for _, b := range in.Bases {
		if b.Default {
			base = b
		}
	}
	if code, _, found, err := pluginkit.DataGet(wire.DataTenant, "s/"+in.Target.Link); err != nil {
		return nil, err
	} else if found {
		return &wire.ShortenOutput{URL: base.URL + "/s-" + code}, nil
	}
	if in.Existing {
		return &wire.ShortenOutput{}, nil
	}
	n, err := pluginkit.DataIncr(wire.DataApp, "seq", 1)
	if err != nil {
		return nil, err
	}
	code := "c" + strconv.FormatInt(n, 10)
	if _, err := pluginkit.DataPut(wire.DataTenant, "c/"+code, in.Target.Link+" "+in.Target.URL, pluginkit.DataPutOptions{IfAbsent: true}); err != nil {
		return nil, err
	}
	if _, err := pluginkit.DataPut(wire.DataTenant, "s/"+in.Target.Link, code, pluginkit.DataPutOptions{}); err != nil {
		return nil, err
	}
	return &wire.ShortenOutput{URL: base.URL + "/s-" + code}, nil
}

func reset(*wire.ResetInput) error {
	pluginkit.Log("info", "reset: told")
	return nil
}

// setupView is the app's own setup step: `open` asks, `finish` keeps the
// answer in the app scope and says done.
func setupView(in *wire.ViewEventInput) (*wire.Surface, error) {
	if in.Event == "action" && in.ActionID == "finish" {
		if _, err := pluginkit.DataPut(wire.DataApp, "config", "set up", pluginkit.DataPutOptions{}); err != nil {
			return nil, err
		}
		return &wire.Surface{Done: true, Nodes: []wire.Node{{Type: "text", Props: map[string]any{"text": wire.Text{"en": "Done"}}}}}, nil
	}
	return &wire.Surface{Title: wire.Text{"en": "Set up"}, Nodes: []wire.Node{{Type: "text", Props: map[string]any{"text": wire.Text{"en": "One question"}}}},
		Actions: []wire.SurfaceAction{{ID: "finish", Label: wire.Text{"en": "Finish"}, Primary: true}}}, nil
}
