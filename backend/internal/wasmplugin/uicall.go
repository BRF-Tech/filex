package wasmplugin

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── ui_call: an app's own interface asking its module ───────────────────
//
// The interface has no network; `engine.call` over the bridge reaches the
// explorer, which posts it here with the PERSON's session
// (handlers.AppPlugins.UICall), and the module answers through its `ui_call`
// export. Screen mode, like a view event: the module reads the files the
// interface was opened with (as refs, never paths), settings and state; it
// writes nothing — a write is the interface's own save, or a job.

// maxUICallBytes caps one answer: it crosses to the browser as JSON and into
// the frame by structured clone. A result larger than this is a file, and a
// file is what `file.read` / a job output is for.
const maxUICallBytes = 4 << 20

// UICall runs `ui_call` for view (an interface view of plugin) with method
// and params, on the files the interface was opened with.
func (r *Registry) UICall(ctx context.Context, plugin, view string, storageID int64, rels []string, actor *model.User, locale, method string, params json.RawMessage) (json.RawMessage, error) {
	p, ok := r.ByName(plugin)
	if !ok {
		return nil, &CallError{Code: CodeUnsupported, Message: "no such plugin"}
	}
	if !p.HasModule() {
		return nil, &CallError{Code: CodeUnsupported, Message: "this app is only an interface; it has no module to call"}
	}
	c, err := p.running()
	if err != nil {
		return nil, err
	}
	v, ok := p.Manifest.View(view)
	if !ok || v.UI == "" {
		return nil, &CallError{Code: CodeUnsupported, Message: "no such interface"}
	}
	if has, herr := c.HasExport(ctx, "ui_call"); herr != nil || !has {
		return nil, &CallError{Code: CodeUnsupported, Export: "ui_call", Message: "the app's module has no ui_call export (built with a pluginkit older than the interface bridge)"}
	}
	method = strings.TrimSpace(method)
	if method == "" || len(method) > 128 {
		return nil, &CallError{Code: CodeRefused, Message: "method is 1-128 characters"}
	}
	// A call from the interface is a screen call like a view event: it takes
	// one of the app's slots (enterCall), so an interface cannot start more
	// module instances at once than the ceiling allows.
	release, err := p.enterCall(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	scope, err := r.screenScope(ctx, p, storageID, rels, actor, locale)
	if err != nil {
		return nil, err
	}
	defer scope.Close()
	in := wire.UICallInput{ViewID: view, Method: method, Params: params,
		Context: wire.CallContext{Inputs: scope.Inputs(), Locale: locale, Settings: r.publicSettings(ctx, p), Engines: r.enginesFor(p)}}
	if actor != nil {
		a := r.wireActor(ctx, p, actor, actorIPFrom(ctx))
		in.Context.Actor = &a
	}
	inb, _ := json.Marshal(in)
	outb, err := c.Call(WithScope(ctx, scope), "ui_call", inb, 0)
	if err != nil {
		return nil, err
	}
	if len(outb) > maxUICallBytes {
		return nil, &CallError{Code: CodePluginError, Export: "ui_call", Message: "the answer is larger than 4 MiB; hand the interface a file instead"}
	}
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  wire.Text       `json:"error"`
	}
	if err := json.Unmarshal(outb, &out); err != nil {
		return nil, &CallError{Code: CodePluginError, Export: "ui_call", Message: "ui_call returned malformed JSON"}
	}
	if len(out.Error) > 0 {
		return nil, &CallError{Code: CodePluginError, Export: "ui_call", Message: out.Error.Get(locale)}
	}
	if len(out.Result) == 0 {
		return json.RawMessage("null"), nil
	}
	return out.Result, nil
}
