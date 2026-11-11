// Command app-shortlinks is a small, complete link shortener: an app that
// holds filex's link-shortener role (filex 0.60). It is the smallest app that
// uses every part of that role, and the one the screenshots of the setup
// wizard, "Get short link" and Reconfigure are taken with
// (e2e/shots/shortlinks.mjs).
//
//   - `setup` (a view with placement `setup`): one question, the alphabet a
//     code is drawn from, kept in the app's own data;
//   - `shorten`: one code per filex link, drawn at random
//     (pkg/pluginkit/shortcode) on the route's default address;
//   - `route`: a code answers a redirect to its link, `<code>+` a page that
//     says where it goes, anything else a miss - and a link that has ended
//     (link_live) is a miss too;
//   - the data is the app's own (`data_*`): Reconfigure and removing the app
//     erase it, so `reset` has nothing left to do.
//
// It exists to be read, and to be built:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o shortlinks.wasm ./examples/app-shortlinks
//	# Admin → Plugins → Apps → Install an app → Upload files:
//	#   shortlinks.wasm and examples/app-shortlinks/filex-app.json
//
// docs/APP-PLUGINS-API.md → The link-shortener role is the contract.
package main

import (
	"errors"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/shortcode"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// The keys the app keeps. The alphabet is the administrator's choice for the
// whole server (app scope); a code and a link's code are the tenant's.
const (
	keyAlphabet = "alphabet"
	codePrefix  = "code/" // code/<shortcode.Key> = "<link token>\n<target>"
	linkPrefix  = "link/" // link/<link token>    = the code
)

// manifest must equal filex-app.json next to this file (main_test.go checks).
var manifest = wire.Manifest{
	ManifestVersion: 1,
	Name:            "shortlinks",
	Version:         "0.1.0",
	Label:           wire.Text{"en": "Short links", "tr": "Kısa bağlantılar"},
	Description: wire.Text{
		"en": "Short addresses for filex's links: a share, a file request or an upload's link becomes a few random symbols on the address you give the app.",
		"tr": "filex bağlantıları için kısa adresler: bir paylaşımın, dosya isteğinin ya da yüklemenin bağlantısı, uygulamaya verdiğiniz adreste birkaç rastgele sembol olur.",
	},
	Filex:       ">=0.60.0",
	Languages:   []string{"en", "tr"},
	Permissions: []string{"data", "public_routes", "link_shortener"},
	UserPermissions: []wire.UserPermission{{
		ID:          "use",
		Label:       wire.Text{"en": "Get short links", "tr": "Kısa bağlantı almak"},
		Description: wire.Text{"en": "\"Get short link\" on the links the person may make.", "tr": "Kişinin açabildiği bağlantılarda \"Kısa bağlantı al\"."},
		Default:     "user",
	}},
	Views:  []wire.View{{ID: "setup", Placement: "setup", Label: wire.Text{"en": "Set up short links", "tr": "Kısa bağlantıları kur"}}},
	Routes: []wire.Route{{ID: "go", Label: wire.Text{"en": "Short links", "tr": "Kısa bağlantılar"}, DefaultPrefix: "l"}},
	Setup: &wire.SetupSpec{View: "setup", Reset: &wire.SetupReset{Warning: wire.Text{
		"en": "Every short link this app made stops working: the codes and where they went are erased.",
		"tr": "Bu uygulamanın ürettiği her kısa bağlantı çalışmaz olur: kodlar ve gittikleri yerler silinir.",
	}}},
	LinkShortener: &wire.LinkShortenerSpec{Route: "go", Requires: "use"},
}

// ⚠ Registration happens in init(): a wasip1 module built with
// -buildmode=c-shared is a reactor, and main() never runs.
func main() {}

func init() { pluginkit.Run(newPlugin(sdk{})) }

// host is what the app needs of filex: its own data, and whether one of
// filex's links is still open. pluginkit's host functions answer only inside
// wasm, so the app takes them as a parameter: sdk is filex, and the tests
// hand it plugintest.Host, which has the same methods.
type host interface {
	DataGet(scope, key string) (string, int64, bool, error)
	DataPut(scope, key, value string, opt pluginkit.DataPutOptions) (int64, error)
	LinkLive(link string) (bool, error)
}

type sdk struct{}

func (sdk) DataGet(scope, key string) (string, int64, bool, error) {
	return pluginkit.DataGet(scope, key)
}

func (sdk) DataPut(scope, key, value string, opt pluginkit.DataPutOptions) (int64, error) {
	return pluginkit.DataPut(scope, key, value, opt)
}

func (sdk) LinkLive(link string) (bool, error) { return pluginkit.LinkLive(link) }

// app answers filex's calls with the data of h.
type app struct{ h host }

// newPlugin is what the module registers (main_test.go drives the same one).
func newPlugin(h host) *pluginkit.Plugin {
	a := app{h: h}
	return &pluginkit.Plugin{
		Manifest: manifest,
		Views:    map[string]pluginkit.ViewFunc{"setup": a.setup},
		Route:    a.route,
		Shorten:  a.shorten,
		Reset:    func(*wire.ResetInput) error { return nil },
	}
}

// presets are the alphabets the setup offers, in its order.
var presets = []struct {
	id    string
	label wire.Text
}{
	{shortcode.PresetAlnum, wire.Text{"en": "Letters and digits", "tr": "Harfler ve rakamlar"}},
	{shortcode.PresetDigits, wire.Text{"en": "Digits", "tr": "Rakamlar"}},
	{shortcode.PresetEmoji, wire.Text{"en": "Emoji", "tr": "Emoji"}},
}

// alphabet is the one the administrator chose (letters and digits until
// then). Case never matters: a code read aloud or typed on a phone still
// works.
func (a app) alphabet() (shortcode.Alphabet, error) {
	preset, _, found, err := a.h.DataGet(wire.DataApp, keyAlphabet)
	if err != nil {
		return shortcode.Alphabet{}, err
	}
	if !found {
		preset = shortcode.PresetAlnum
	}
	return shortcode.NewAlphabet(preset, "", false)
}

// setup is the wizard's first step: the alphabet, then done.
func (a app) setup(in *wire.ViewEventInput) (*wire.Surface, error) {
	// Save is the primary button: filex posts it as `submit`, with its id.
	if in.ActionID == "save" {
		values, _ := in.Data["values"].(map[string]any)
		choice, _ := values["alphabet"].(string)
		alpha, err := shortcode.NewAlphabet(choice, "", false)
		if err != nil {
			return ask(choice, wire.Text{"en": "Pick one of the alphabets.", "tr": "Alfabelerden birini seçin."}), nil
		}
		if _, err := a.h.DataPut(wire.DataApp, keyAlphabet, choice, pluginkit.DataPutOptions{}); err != nil {
			return nil, err
		}
		n := alpha.MinLengthFor(shortcode.GoodBits)
		return &wire.Surface{Done: true, Nodes: []wire.Node{text(wire.Text{
			"en": "Each code is " + strconv.Itoa(n) + " symbols long, drawn at random.",
			"tr": "Her kod rastgele çekilmiş " + strconv.Itoa(n) + " sembolden oluşur.",
		})}}, nil
	}
	return ask(shortcode.PresetAlnum, nil), nil
}

func ask(current string, problem wire.Text) *wire.Surface {
	opts := make([]wire.FieldOption, len(presets))
	for i, p := range presets {
		opts[i] = wire.FieldOption{Value: p.id, Label: p.label["en"], LabelI18n: p.label}
	}
	s := &wire.Surface{
		Title: wire.Text{"en": "Short links", "tr": "Kısa bağlantılar"},
		Nodes: []wire.Node{
			text(wire.Text{
				"en": "Which symbols are codes made of? The app makes each code long enough that guessing one is hopeless.",
				"tr": "Kodlar hangi sembollerden oluşsun? Uygulama her kodu, tahmin edilmesi umutsuz olacak kadar uzun yapar.",
			}),
			{Type: "form", Props: map[string]any{"fields": []wire.Field{{
				Key: "alphabet", Type: "select", Label: "Alphabet", Required: true, Default: current, Options: opts,
				I18n: &wire.FieldI18n{Label: wire.Text{"en": "Alphabet", "tr": "Alfabe"}},
			}}}},
		},
		Actions: []wire.SurfaceAction{{ID: "save", Label: wire.Text{"en": "Save", "tr": "Kaydet"}, Primary: true}},
	}
	if problem != nil {
		s.Errors = map[string]wire.Text{"alphabet": problem}
	}
	return s
}

// shorten answers the link's code, drawing one the first time.
func (a app) shorten(in *wire.ShortenInput) (*wire.ShortenOutput, error) {
	base, ok := defaultBase(in.Bases)
	if !ok {
		return &wire.ShortenOutput{Error: wire.Text{"en": "Short links have no address yet.", "tr": "Kısa bağlantıların henüz adresi yok."}}, nil
	}
	code, _, found, err := a.h.DataGet(wire.DataTenant, linkPrefix+in.Target.Link)
	if err != nil {
		return nil, err
	}
	if found {
		return &wire.ShortenOutput{URL: base.URL + "/" + shortcode.EscapePath(code)}, nil
	}
	if in.Existing {
		return &wire.ShortenOutput{}, nil
	}
	alpha, err := a.alphabet()
	if err != nil {
		return nil, err
	}
	// A drawn code that is already taken is drawn again: at 50 bits that is
	// a once-in-a-lifetime event, so three tries are plenty.
	for try := 0; try < 3; try++ {
		code, err = alpha.Generate(alpha.MinLengthFor(shortcode.GoodBits))
		if err != nil {
			return nil, err
		}
		canon, _ := shortcode.Canonical(code, false)
		_, err = a.h.DataPut(wire.DataTenant, codePrefix+shortcode.Key(canon), in.Target.Link+"\n"+in.Target.URL,
			pluginkit.DataPutOptions{IfAbsent: true})
		if pluginkit.IsBusy(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, err := a.h.DataPut(wire.DataTenant, linkPrefix+in.Target.Link, code, pluginkit.DataPutOptions{}); err != nil {
			return nil, err
		}
		return &wire.ShortenOutput{URL: base.URL + "/" + shortcode.EscapePath(code)}, nil
	}
	return nil, errors.New("shortlinks: no free code in three draws")
}

func defaultBase(bases []wire.RouteBase) (wire.RouteBase, bool) {
	for _, b := range bases {
		if b.Default {
			return b, true
		}
	}
	if len(bases) > 0 {
		return bases[0], true
	}
	return wire.RouteBase{}, false
}

// route answers one visit: the code's link, `<code>+` a page naming it.
func (a app) route(in *wire.RouteInput) (*wire.RouteOutput, error) {
	seg, preview := strings.CutSuffix(in.Path, "+")
	raw, ok := shortcode.DecodeSegment(seg)
	if !ok {
		return &wire.RouteOutput{Miss: true}, nil
	}
	canon, ok := shortcode.Canonical(raw, false)
	if !ok {
		return &wire.RouteOutput{Miss: true}, nil
	}
	v, _, found, err := a.h.DataGet(wire.DataTenant, codePrefix+shortcode.Key(canon))
	if err != nil || !found {
		return &wire.RouteOutput{Miss: true}, err
	}
	link, target, _ := strings.Cut(v, "\n")
	if live, err := a.h.LinkLive(link); err != nil || !live {
		return &wire.RouteOutput{Miss: true}, err
	}
	if preview {
		return &wire.RouteOutput{Page: &wire.RoutePage{
			Title:     wire.Text{"en": "Where this short link goes", "tr": "Bu kısa bağlantı nereye gidiyor"},
			Text:      wire.Text{"en": target, "tr": target},
			Link:      target,
			LinkLabel: wire.Text{"en": "Open", "tr": "Aç"},
		}}, nil
	}
	return &wire.RouteOutput{Redirect: target}, nil
}

func text(t wire.Text) wire.Node { return wire.Node{Type: "text", Props: map[string]any{"text": t}} }
