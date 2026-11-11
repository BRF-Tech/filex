package wire

// ── Public routes, setup, the link-shortener role, the app's data (0.60.0) ──
//
// docs/APP-PLUGINS-API.md → Public routes, Setting an app up, The
// link-shortener role, An app's own data.

// PlacementSetup is the placement of the view an app's setup wizard opens
// with (SetupSpec.View): drawn by filex, for an administrator, while the app
// is being set up.
const PlacementSetup = "setup"

// Route is one public address family an app answers.
type Route struct {
	// ID names the route in RouteInput and LinkShortenerSpec.Route.
	ID string `json:"id"`
	// Label is what the setup wizard calls it ("Short links").
	Label Text `json:"label"`
	// DefaultPrefix is the path the wizard suggests on filex's own address
	// ("l" gives files.example.com/l/<code>). The administrator decides.
	DefaultPrefix string `json:"default_prefix,omitempty"`
}

// SetupSpec is the `setup` block.
type SetupSpec struct {
	// View is the app's own wizard: a view whose placement is `setup`, drawn
	// by filex. Its surface answers `done: true` when the app's part is
	// finished. Empty: the wizard is filex's steps alone.
	View string `json:"view,omitempty"`
	// Reset is what Reconfigure (and removing the app) says before it erases
	// the app's data, in the app's words, beside filex's own count of what
	// goes.
	Reset *SetupReset `json:"reset,omitempty"`
}

// SetupReset is the app's own warning before its data is erased.
type SetupReset struct {
	Warning Text `json:"warning"`
}

// LinkShortenerSpec is the `link_shortener` block.
type LinkShortenerSpec struct {
	// Route is the id of the route the short addresses are served on: every
	// address `shorten` answers must lie on one of that route's bases.
	Route string `json:"route"`
	// Requires is the app's own user permission a person needs for "Get
	// short link"; empty: whoever may make the link may shorten it.
	Requires string `json:"requires,omitempty"`
}

// RouteBase is one address a route is served at: the base URL a path is
// appended to (`https://l.example.com`, `https://files.example.com/l`).
type RouteBase struct {
	ID  int64  `json:"id"`
	URL string `json:"url"`
	// Default is the base the administrator picked for new addresses.
	Default bool `json:"default,omitempty"`
}

// RouteInput is what the host hands `route`: one visit.
//
// Nobody is signed in to a visit, and the visitor's address, cookies and
// headers are not handed over. The call reads and counts in the app's data
// (data_get, data_list, data_incr), asks whether one of filex's own links is
// still open (link_live) and reads its settings; nothing else.
type RouteInput struct {
	Route string    `json:"route"`
	Base  RouteBase `json:"base"`
	// Path is what follows the base, as the request spelled it (still
	// percent-encoded), without the leading slash: one segment or more.
	Path string `json:"path"`
	// Query is the raw query string (at most 1 KiB), "" for none.
	Query string `json:"query,omitempty"`
	// Method is GET or HEAD.
	Method string `json:"method"`
	// Countable is filex's word on whether this visit is a person opening
	// the address: a GET, not a prefetch, not a link previewer or a
	// crawler, and not the same client's second visit of the same address
	// within a minute. Count a click only when it is true.
	Countable bool              `json:"countable"`
	Locale    string            `json:"locale,omitempty"`
	Settings  map[string]string `json:"settings,omitempty"`
}

// RouteOutput is what `route` answers: exactly one of the three.
type RouteOutput struct {
	// Redirect sends the visitor on: an absolute http(s) address, with no
	// user name or password in it, at most 2048 bytes. filex answers 302
	// with no-store, no referrer and no cookies.
	Redirect string `json:"redirect,omitempty"`
	// Page is a short page filex draws with its own template (a short
	// link's `+` preview): no HTML or script from the app.
	Page *RoutePage `json:"page,omitempty"`
	// Miss says this address leads nowhere. filex draws ONE page for every
	// miss, whatever the reason (never existed, deleted, ended, switched
	// off), and counts it against the visitor: past the limit the visitor
	// is held back.
	Miss bool `json:"miss,omitempty"`
}

// RoutePage is a page filex draws for a route.
type RoutePage struct {
	Title Text `json:"title"`
	Text  Text `json:"text,omitempty"`
	// Link is an absolute http(s) address drawn as a button labelled
	// LinkLabel; filex never follows it itself.
	Link      string `json:"link,omitempty"`
	LinkLabel Text   `json:"link_label,omitempty"`
}

// The kinds of filex link `shorten` is handed.
const (
	ShortenShare       = "share"        // a public link to a file or a folder (/s/)
	ShortenFileRequest = "file_request" // a file request, a link to upload into a folder (/d/)
	ShortenUpload      = "upload"       // the link an upload answered with (a share, or the file in filex)
)

// ShortenTarget is the filex link to shorten.
type ShortenTarget struct {
	Kind string `json:"kind"`
	// URL is the link itself, on the address its tenant is reached at.
	URL string `json:"url"`
	// Link identifies a public link (share or file request) across calls,
	// so the app answers the same short address for it again and can ask
	// link_live whether it is still open. "" for a link that is not a
	// public link (an upload's private address).
	Link string `json:"link,omitempty"`
	// Name is the shared item's name, for the app's own lists.
	Name string `json:"name,omitempty"`
}

// ShortenInput is what the host hands `shorten`.
type ShortenInput struct {
	Target ShortenTarget `json:"target"`
	// Existing asks only for the short address the link already has: answer
	// it, or an empty URL, and make nothing (filex asks this when it writes
	// a link into a mail).
	Existing bool              `json:"existing,omitempty"`
	Bases    []RouteBase       `json:"bases"`
	Actor    *Actor            `json:"actor,omitempty"`
	Locale   string            `json:"locale,omitempty"`
	Settings map[string]string `json:"settings,omitempty"`
}

// ShortenOutput is what `shorten` answers: the short address, on one of the
// bases it was handed, or an empty URL for none. Error refuses, with a
// sentence for the person.
type ShortenOutput struct {
	URL   string `json:"url,omitempty"`
	Error Text   `json:"error,omitempty"`
}

// ResetInput is what the host hands the optional `reset` export, just before
// it erases the app's data (Reconfigure, or removing the app): the moment to
// undo anything the app did outside filex. filex erases its data whatever
// the answer.
type ResetInput struct {
	Removing bool              `json:"removing,omitempty"`
	Settings map[string]string `json:"settings,omitempty"`
}

// The scopes of an app's own data (data_*). A key lives in one scope; the
// host decides which tenant a call's `tenant` scope is, so an app cannot
// read or write another tenant's keys.
const (
	// DataTenant is the call's tenant: the person's on a call made for a
	// person, the address's on a route visit. The default.
	DataTenant = "tenant"
	// DataApp is shared by every tenant: the app's own configuration.
	DataApp = "app"
)

// The limits on an app's own data.
const (
	DataMaxKeyBytes   = 512
	DataMaxValueBytes = 64 << 10
	DataMaxList       = 500
)
