package pluginkit

import (
	"encoding/json"
	"errors"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Public routes, the link-shortener role, reset (filex 0.60.0) ───────────

// RouteFunc answers one visit to one of the app's public routes
// (wire.RouteInput): a redirect, a page filex draws, or a miss.
//
// ⚠⚠ It runs on a WARM instance: filex keeps a few instances of the module
// started and hands each visit to a free one, because starting an instance
// per visit costs milliseconds and megabytes and a short link is opened by
// every chat preview it is pasted into. So package-level variables survive
// from one visit to the next (a cache is fine), and the visit before this one
// may have been somebody else's: never keep one visit's facts in a global.
// The same holds for ShortenFunc.
type RouteFunc func(in *wire.RouteInput) (*wire.RouteOutput, error)

// ShortenFunc answers filex's "a short address for this link"
// (wire.ShortenInput): the app's role when its manifest has a
// `link_shortener` block. With in.Existing it answers only an address the
// link already has.
type ShortenFunc func(in *wire.ShortenInput) (*wire.ShortenOutput, error)

// ResetFunc is told, just before filex erases the app's data (Reconfigure, or
// removing the app), so it can undo anything it did outside filex. filex
// erases the data whatever it answers.
type ResetFunc func(in *wire.ResetInput) error

func dispatchRoute(input []byte) ([]byte, error) {
	if registered == nil {
		return nil, ErrNotRegistered
	}
	if registered.Route == nil {
		return nil, errors.New("pluginkit: this plugin has routes but registers no Route")
	}
	var in wire.RouteInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	out, err := registered.Route(&in)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = &wire.RouteOutput{Miss: true}
	}
	return json.Marshal(out)
}

func dispatchShorten(input []byte) ([]byte, error) {
	if registered == nil {
		return nil, ErrNotRegistered
	}
	if registered.Shorten == nil {
		return nil, errors.New("pluginkit: this plugin has a link_shortener block but registers no Shorten")
	}
	var in wire.ShortenInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	out, err := registered.Shorten(&in)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = &wire.ShortenOutput{}
	}
	return json.Marshal(out)
}

func dispatchReset(input []byte) ([]byte, error) {
	if registered == nil {
		return nil, ErrNotRegistered
	}
	if registered.Reset == nil {
		return []byte(`{}`), nil
	}
	var in wire.ResetInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if err := registered.Reset(&in); err != nil {
		return nil, err
	}
	return []byte(`{}`), nil
}
