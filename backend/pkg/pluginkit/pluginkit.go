// Package pluginkit is the guest SDK for filex app plugins written in Go.
//
// A plugin is an ordinary Go program compiled for WebAssembly:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
//
// It registers what it offers with Run (from init(), see there) and never
// blocks: filex calls its
// exports (describe, action_run, view_event, page_event, tick, on_event) one
// at a time, on a fresh instance per call, and the plugin answers through the host
// functions filex hands it (files, progress, settings, engines, …), each one
// gated by a permission the administrator granted at install.
//
// The wire shapes live in pluginkit/wire; a plugin that only needs describe
// and one action needs nothing else from here than Run and the helpers.
package pluginkit

import (
	"encoding/json"
	"errors"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ActionFunc runs one action. It returns the outputs it created through
// CreateOutput plus an optional message; returning an error fails the job
// with that message.
type ActionFunc func(in *wire.ActionRunInput) (*wire.ActionRunOutput, error)

// ViewFunc answers a view event with the next screen.
type ViewFunc func(in *wire.ViewEventInput) (*wire.Surface, error)

// PageFunc answers a public-page event.
type PageFunc func(in *wire.ViewEventInput) (*wire.Surface, error)

// TickFunc answers the hourly wake-up: what this app wants done, and when.
//
// It runs with nobody waiting and nobody watching, so it is READ-ONLY —
// the same scope a screen gets. It may read settings and state
// (StateGet/StateList), look people up, notify, mail and make granted HTTP
// calls; it may NOT write files, write state, take locks or sign. The work
// itself happens in the actions it schedules, which are ordinary jobs.
//
// Answer with nothing when there is nothing to do; that is the cheap case
// and the common one.
type TickFunc func(in *wire.TickInput) (*wire.TickOutput, error)

// Plugin is what a program registers with Run.
type Plugin struct {
	// Manifest is echoed by describe. It must equal the filex-app.json the
	// administrator installed (name, version, permissions); filex refuses the
	// plugin otherwise.
	Manifest wire.Manifest
	Actions  map[string]ActionFunc
	Views    map[string]ViewFunc
	Pages    map[string]PageFunc
	// Tick is the hourly wake-up. It is called only when the manifest asks
	// for the `schedule` permission AND the administrator granted it; an
	// app that leaves this nil but asks for the permission answers every
	// wake-up with an error, which lands in its log in the admin panel.
	Tick TickFunc
}

var registered *Plugin

// Run registers the plugin.
//
// ⚠ Call it from an init() function, not from main(). A wasip1 module built
// with -buildmode=c-shared is a reactor: filex calls the exports directly,
// package initialisers run, main() does not. A plugin that registers in
// main() answers every call with ErrNotRegistered.
func Run(p *Plugin) { registered = p }

// ErrNotRegistered is returned when an export is called before Run.
var ErrNotRegistered = errors.New("pluginkit: no plugin registered (call pluginkit.Run in main)")

// The dispatchers below are what the wasm exports call; they are plain Go so
// the host-side tests of the SDK can run them without a wasm toolchain.

func dispatchDescribe(_ []byte) ([]byte, error) {
	if registered == nil {
		return nil, ErrNotRegistered
	}
	m := registered.Manifest
	if m.ManifestVersion == 0 {
		m.ManifestVersion = wire.ProtocolVersion
	}
	return json.Marshal(m)
}

func dispatchAction(input []byte) ([]byte, error) {
	if registered == nil {
		return nil, ErrNotRegistered
	}
	var in wire.ActionRunInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	fn, ok := registered.Actions[in.ActionID]
	if !ok {
		return nil, errors.New("pluginkit: unknown action " + in.ActionID)
	}
	out, err := fn(&in)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = &wire.ActionRunOutput{OK: true}
	}
	return json.Marshal(out)
}

func dispatchView(input []byte, pages bool) ([]byte, error) {
	if registered == nil {
		return nil, ErrNotRegistered
	}
	var in wire.ViewEventInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	var s *wire.Surface
	var err error
	if pages {
		fn, ok := registered.Pages[in.ViewID]
		if !ok {
			return nil, errors.New("pluginkit: unknown page " + in.ViewID)
		}
		s, err = fn(&in)
	} else {
		fn, ok := registered.Views[in.ViewID]
		if !ok {
			return nil, errors.New("pluginkit: unknown view " + in.ViewID)
		}
		s, err = fn(&in)
	}
	if err != nil {
		return nil, err
	}
	if s == nil {
		s = &wire.Surface{Done: true}
	}
	return json.Marshal(s)
}

func dispatchTick(input []byte) ([]byte, error) {
	if registered == nil {
		return nil, ErrNotRegistered
	}
	if registered.Tick == nil {
		return nil, errors.New("pluginkit: this plugin asks for the schedule permission but registers no Tick")
	}
	var in wire.TickInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	out, err := registered.Tick(&in)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = &wire.TickOutput{}
	}
	return json.Marshal(out)
}
