// Package plugintest is the test kit that ships with the filex plugin SDK.
//
// A plugin is a wasm module filex loads, so the obvious way to test one is
// to build it and install it — which is slow, needs a host, and happens
// after the mistake is already in the module. plugintest runs the plugin's
// own Go functions in-process, behind a fake filex, so `go test` answers
// the questions that matter before `plugin.wasm` exists:
//
//   - does the action do the right thing, and does it degrade when a
//     permission is missing or an engine is absent?
//   - is every screen drawable by filex's renderer — known node types, a
//     readable choice instead of a dropdown, one primary button per step,
//     conditions that point at fields that exist?
//   - does every string carry every language the manifest promises?
//   - did a screen change shape without anybody noticing (golden screens)?
//
// # The shape of a test
//
//	func harness(t *testing.T) *plugintest.Harness {
//		h := plugintest.New(myapp.Plugin())      // manifest + actions + views
//		h.Host.InstallEngine("ffmpeg")           // what this fake server has
//		return h
//	}
//
//	func TestOptionsScreen(t *testing.T) {
//		h := harness(t)
//		s, err := h.Open("options", plugintest.File{Name: "clip.mov"})
//		if err != nil {
//			t.Fatal(err)
//		}
//		plugintest.CheckSurface(t, h.Manifest(), s)   // structure
//		plugintest.CheckLanguages(t, h.Manifest(), s) // every declared language
//		plugintest.Golden(t, "options", s)            // shape, vs testdata/golden
//	}
//
// # The fake host
//
// Host is filex's host side in memory: files, settings, per-file state,
// locks, engines, signing, notifications, mail, HTTP and shares. It uses
// the manifest's permissions as the administrator's grants and answers a
// call it was not granted with the real code (`permission_denied`), and it
// refuses from a screen what only an action job may do. Anything else would
// make the kit lie: the plugin would pass its tests and fail on a real
// instance.
//
// # What it cannot do
//
// The host functions in pluginkit only answer inside wasm; off-wasm they
// return ErrNotWasm. So a plugin reaches the fake host the way it reaches
// the real one in tests everywhere else — through a small interface it
// takes as a parameter (see the `Host` interface in filex-convert's
// internal/job). *Host satisfies such an interface method-for-method, with
// the same names and signatures as pluginkit's own functions.
package plugintest

import (
	"errors"
	"strconv"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// Harness is a plugin under test with a fake filex behind it.
type Harness struct {
	// Plugin is what the module registers with pluginkit.Run.
	Plugin *pluginkit.Plugin
	// Host is the fake instance. Wire it before a call: settings, engines,
	// directory entries, scripted engine answers.
	Host *Host
	// Locale is the language a call carries when the event does not name
	// one (default "en").
	Locale string
	// Actor is the person the call runs as.
	Actor wire.Actor

	selection []wire.FileRef
	jobs      int
}

// New builds a harness around a registered plugin. For the manifest-only
// checks a bare `&pluginkit.Plugin{Manifest: m}` is enough.
func New(p *pluginkit.Plugin) *Harness {
	if p == nil {
		p = &pluginkit.Plugin{}
	}
	return &Harness{
		Plugin: p,
		Host:   NewHost(p.Manifest),
		Locale: "en",
		Actor:  wire.Actor{ID: 1, Email: "admin@local", Name: "Admin", Role: "owner"},
	}
}

// NewFor is New for a plugin that takes its host as a parameter — the
// shape a plugin needs anyway, because pluginkit's host functions only
// answer inside wasm. The fake host is built from the manifest first and
// handed to the plugin, so the grants are the manifest's own:
//
//	h := plugintest.NewFor(myapp.Manifest(), func(host *plugintest.Host) *pluginkit.Plugin {
//		return myapp.Plugin(host)
//	})
func NewFor(m wire.Manifest, build func(*Host) *pluginkit.Plugin) *Harness {
	host := NewHost(m)
	p := build(host)
	if p == nil {
		p = &pluginkit.Plugin{Manifest: m}
	}
	return &Harness{
		Plugin: p,
		Host:   host,
		Locale: "en",
		Actor:  wire.Actor{ID: 1, Email: "admin@local", Name: "Admin", Role: "owner"},
	}
}

// Manifest is what describe would answer: the registered manifest with
// manifest_version filled in.
func (h *Harness) Manifest() wire.Manifest {
	m := h.Plugin.Manifest
	if m.ManifestVersion == 0 {
		m.ManifestVersion = wire.ProtocolVersion
	}
	return m
}

// Languages is what the manifest promises to speak (["en"] when it says
// nothing).
func (h *Harness) Languages() []string { return Languages(h.Manifest()) }

// Languages answers a manifest's declared languages, English by default.
func Languages(m wire.Manifest) []string {
	if len(m.Languages) == 0 {
		return []string{"en"}
	}
	out := make([]string, 0, len(m.Languages))
	seen := map[string]bool{}
	for _, l := range m.Languages {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// ── the selection a call is made on ────────────────────────────────────

// Select registers the files a call is made on and answers their refs.
// Call it once per flow: Open, Submit and Input reuse the selection, so a
// wizard's three screens do not register the same file three times.
func (h *Harness) Select(files ...File) []wire.FileRef {
	h.selection = nil
	for _, f := range files {
		h.selection = append(h.selection, h.Host.AddInput(f))
	}
	return h.selection
}

// Selection is what Select registered.
func (h *Harness) Selection() []wire.FileRef { return h.selection }

func (h *Harness) selectIfGiven(files []File) {
	if len(files) > 0 {
		h.Select(files...)
	}
}

// ── actions ────────────────────────────────────────────────────────────

// Input builds an ActionRunInput with the manifest's own defaults filled
// in: the action's output mode, the current selection, the actor, the
// locale, the non-secret settings and the engine map.
func (h *Harness) Input(actionID string, files ...File) *wire.ActionRunInput {
	h.selectIfGiven(files)
	h.jobs++
	in := &wire.ActionRunInput{
		JobID:    "job-" + strconv.Itoa(h.jobs),
		ActionID: actionID,
		Inputs:   append([]wire.FileRef(nil), h.selection...),
		Actor:    h.Actor,
		Locale:   h.Locale,
		Settings: h.Host.CallSettings(),
		Engines:  h.Host.InstalledEngines(),
	}
	for _, a := range h.Manifest().Actions {
		if a.ID == actionID {
			in.Output = a.Output
		}
	}
	return in
}

// Run calls the action as filex would: the fake host is in job mode, so
// outputs, state, locks, engines and shares are allowed.
func (h *Harness) Run(actionID string, in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
	if !hasAction(h.Manifest(), actionID) {
		return nil, errors.New("plugintest: the manifest declares no action " + actionID)
	}
	fn := h.Plugin.Actions[actionID]
	if fn == nil {
		return nil, errors.New("plugintest: no action registered as " + actionID)
	}
	if in == nil {
		in = h.Input(actionID)
	}
	if in.ActionID == "" {
		in.ActionID = actionID
	}
	h.Host.EnterJob()
	defer h.Host.EnterScreen()
	out, err := fn(in)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = &wire.ActionRunOutput{OK: true}
	}
	return out, nil
}

// Do is Run with the parameters a screen would have collected.
func (h *Harness) Do(actionID string, params map[string]any, files ...File) (*wire.ActionRunOutput, error) {
	in := h.Input(actionID, files...)
	in.Params = params
	return h.Run(actionID, in)
}

// Queue runs the job a surface asked for (`{job: …}`), with that job's
// params and output override — the second half of a wizard, without the
// host in between.
func (h *Harness) Queue(s *wire.Surface, files ...File) (*wire.ActionRunOutput, error) {
	if s == nil || s.Job == nil {
		return nil, errors.New("plugintest: this surface queues no job")
	}
	in := h.Input(s.Job.ActionID, files...)
	in.Params = s.Job.Params
	if s.Job.Output != nil {
		in.Output = *s.Job.Output
	}
	return h.Run(s.Job.ActionID, in)
}

// ── screens ────────────────────────────────────────────────────────────

// Event builds a view event on the current selection.
func (h *Harness) Event(event string, files ...File) *wire.ViewEventInput {
	h.selectIfGiven(files)
	return &wire.ViewEventInput{
		Event: event,
		Context: wire.CallContext{
			Inputs:   append([]wire.FileRef(nil), h.selection...),
			Actor:    &h.Actor,
			Locale:   h.Locale,
			Settings: h.Host.CallSettings(),
			Engines:  h.Host.InstalledEngines(),
		},
	}
}

// View answers a view event. The fake host is in screen mode: the plugin
// may read its inputs, but a screen that tries to write a file, take a
// lock or run an engine is refused exactly as filex refuses it.
func (h *Harness) View(viewID string, ev *wire.ViewEventInput) (*wire.Surface, error) {
	if !hasView(h.Manifest(), viewID) {
		return nil, errors.New("plugintest: the manifest declares no view " + viewID)
	}
	fn := h.Plugin.Views[viewID]
	if fn == nil {
		return nil, errors.New("plugintest: no view registered as " + viewID)
	}
	if ev == nil {
		ev = h.Event("open")
	}
	ev.ViewID = viewID
	if ev.Context.Locale == "" {
		ev.Context.Locale = h.Locale
	}
	h.Host.EnterScreen()
	s, err := fn(ev)
	if err != nil {
		return nil, err
	}
	if s == nil {
		s = &wire.Surface{Done: true}
	}
	return s, nil
}

// Open is View with an `open` event.
func (h *Harness) Open(viewID string, files ...File) (*wire.Surface, error) {
	return h.View(viewID, h.Event("open", files...))
}

// Change is View with a `change` event carrying the form's values.
func (h *Harness) Change(viewID string, state, values map[string]any) (*wire.Surface, error) {
	ev := h.Event("change")
	ev.State = state
	ev.Data = map[string]any{"values": values}
	return h.View(viewID, ev)
}

// Submit is View with a `submit` event carrying the form's values.
func (h *Harness) Submit(viewID string, state, values map[string]any) (*wire.Surface, error) {
	ev := h.Event("submit")
	ev.State = state
	ev.Data = map[string]any{"values": values}
	return h.View(viewID, ev)
}

// Act is View with an `action` event: a footer button that is not the
// primary one, or a row action (pass `row_id` in data).
func (h *Harness) Act(viewID, actionID string, state, values map[string]any) (*wire.Surface, error) {
	ev := h.Event("action")
	ev.ActionID = actionID
	ev.State = state
	ev.Data = map[string]any{"values": values}
	return h.View(viewID, ev)
}

// Page answers a public-page event on a link the plugin opened. The fake
// host is in share mode: not writable, but `state_set` and `share_state("")`
// answer for that link, as they do on a real page event.
func (h *Harness) Page(pageID, token string, ev *wire.ViewEventInput) (*wire.Surface, error) {
	if _, ok := pageByID(h.Manifest(), pageID); !ok {
		return nil, errors.New("plugintest: the manifest declares no public page " + pageID)
	}
	fn := h.Plugin.Pages[pageID]
	if fn == nil {
		return nil, errors.New("plugintest: no page registered as " + pageID)
	}
	sh, ok := h.Host.ShareByToken(token)
	if !ok {
		return nil, errors.New("plugintest: no such share " + token)
	}
	if sh.Revoked {
		return nil, errors.New("plugintest: share " + token + " is revoked")
	}
	if ev == nil {
		ev = &wire.ViewEventInput{Event: "open"}
	}
	ev.ViewID = pageID
	if ev.Context.Locale == "" {
		ev.Context.Locale = h.Locale
	}
	if len(ev.Context.Inputs) == 0 {
		for _, f := range sh.Files {
			size := int64(0)
			if b, ok := h.Host.Bytes(f.Ref); ok {
				size = int64(len(b))
			}
			ev.Context.Inputs = append(ev.Context.Inputs, wire.FileRef{Ref: f.Ref, Name: f.Name, Size: size})
		}
	}
	sh.Visits++
	h.Host.EnterShare(token)
	defer h.Host.EnterScreen()
	s, err := fn(ev)
	if err != nil {
		return nil, err
	}
	if s == nil {
		s = &wire.Surface{Done: true}
	}
	return s, nil
}

// ── locales ────────────────────────────────────────────────────────────

// InLocales draws the same screen once per declared language. What comes
// back is what CheckLocaleParity reads: a string that is empty in one
// language, or a section that only one language has, is a screen half in
// the wrong language — the complaint this kit exists for.
func (h *Harness) InLocales(viewID string, ev *wire.ViewEventInput) (map[string]*wire.Surface, error) {
	out := map[string]*wire.Surface{}
	for _, lang := range h.Languages() {
		e := cloneEvent(ev)
		if e == nil {
			e = h.Event("open")
		}
		e.Context.Locale = lang
		s, err := h.View(viewID, e)
		if err != nil {
			return nil, err
		}
		out[lang] = s
	}
	return out, nil
}

// OpenInLocales is InLocales with an `open` event.
func (h *Harness) OpenInLocales(viewID string, files ...File) (map[string]*wire.Surface, error) {
	h.selectIfGiven(files)
	return h.InLocales(viewID, h.Event("open"))
}

func cloneEvent(ev *wire.ViewEventInput) *wire.ViewEventInput {
	if ev == nil {
		return nil
	}
	c := *ev
	c.Context.Inputs = append([]wire.FileRef(nil), ev.Context.Inputs...)
	if ev.State != nil {
		c.State = map[string]any{}
		for k, v := range ev.State {
			c.State[k] = v
		}
	}
	if ev.Data != nil {
		c.Data = map[string]any{}
		for k, v := range ev.Data {
			c.Data[k] = v
		}
	}
	return &c
}

// CallSettings is the settings map a call carries: everything the
// administrator entered EXCEPT the secret fields, which a plugin may only
// read through `settings_get`.
func (h *Host) CallSettings() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	secret := map[string]bool{}
	for _, f := range h.manifest.Settings {
		if f.Secret {
			secret[f.Key] = true
		}
	}
	out := map[string]string{}
	for k, v := range h.settings {
		if !secret[k] {
			out[k] = v
		}
	}
	return out
}
