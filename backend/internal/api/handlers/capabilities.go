package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/archivecli"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/multioidc"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/newdoc"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
)

// Capabilities exposes /api/capabilities.
type Capabilities struct {
	Service *capability.Service
	// Store + MultiTenant power the per-tenant branding block: in multi-tenant
	// mode the (pre-auth, host-resolved) capabilities answer carries only THIS
	// host's tenant identity — never the existence of other tenants
	// (docs/MULTI-TENANCY.md §12 + isolation checklist).
	Store       db.Store
	MultiTenant bool
	// Mail, when wired, answers whether outgoing mail can be sent right now
	// (configured and verified). Published as `mail.ready`, a boolean with no
	// host in it. Nil = the field is absent and clients keep offering mail.
	Mail interface {
		Ready(ctx context.Context) bool
	}
	/* kimlik:e3 cloud */
	// CloudEnabled mirrors FILEX_CLOUD (set by BuildRouter only when the flag
	// is on). While false — the default — the capabilities payload carries NO
	// cloud field at all, keeping the flag-off wire format byte-identical.
	CloudEnabled bool
	/* wiring:e2 */
	// E2EEscrow is the installation escrow PUBLIC key, or nil when escrow is
	// off. It is published deliberately: the browser needs it to wrap a new
	// encrypted folder's master key to the operator, and a user about to
	// create such a folder is entitled to know, before they create it, that
	// their operator holds a second key to it.
	E2EEscrow *e2e.EscrowKey
	// Tenants + PublicURLSet feed `public_url`: the address this deployment is
	// reached at, for the connection guides (see Get). Zero values publish
	// nothing, which is what every test that builds this handler by hand gets.
	Tenants      tenanturl.Resolver
	PublicURLSet bool
	// Archive publishes the non-sensitive creation policy used by the explorer
	// so the create dialog honours the operator's configured default.
	Archive *archivecli.Service
	// Queued names the changes this server runs as jobs of its operations queue
	// when asked with `queued=1`: "rename" on POST /api/files/manager?action=rename,
	// "restore" on POST /api/files/manager/restore. Published as `queued`;
	// empty publishes nothing, and the explorer changes inside the request as
	// it always did.
	Queued []string
}

// NewCapabilities constructs a Capabilities handler.
func NewCapabilities(svc *capability.Service, store db.Store, multiTenant bool) *Capabilities {
	return &Capabilities{Service: svc, Store: store, MultiTenant: multiTenant}
}

// Get returns the runtime feature snapshot.
//
// We emit BOTH the rich nested shape (filex-core admin SPA) AND a flat
// alias set (legacy embed.js + filex-core SFC fallback expected:
// `ffmpeg / ghostscript / libreoffice / max_chunk_mb / upload_limit_mb /
// onlyoffice_url / drawio_url`). Cheap to ship both — keeps the SFC
// happy without breaking the existing admin UI bindings.
func (h *Capabilities) Get(w http.ResponseWriter, r *http.Request) {
	c, err := h.Service.Get(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Build flat aliases.
	const mb = int64(1024 * 1024)
	flat := map[string]any{
		"ffmpeg":       c.Thumbs.Video,
		"imagemagick":  c.Thumbs.ImageMagick,
		"ghostscript":  c.Thumbs.PDF,
		"libreoffice":  c.Thumbs.Office,
		"max_chunk_mb": int64(0),
		"upload_limit_mb": func() int64 {
			if c.MaxUploadSize <= 0 {
				return 0
			}
			return c.MaxUploadSize / mb
		}(),
		"onlyoffice_url": "",
		"drawio_url":     "",
		"convert_url":    "",
	}
	if c.ChunkSize > 0 {
		flat["max_chunk_mb"] = c.ChunkSize / mb
	}
	if oo, ok := c.External["onlyoffice"]; ok && oo.Enabled {
		flat["onlyoffice_url"] = oo.URL
	}
	if dr, ok := c.External["drawio"]; ok && dr.Enabled {
		flat["drawio_url"] = dr.URL
	}
	if cv, ok := c.External["convert"]; ok && cv.Enabled {
		flat["convert_url"] = cv.URL
	}

	// Marshal the rich snapshot to a generic map so we can layer the
	// flat aliases on top (no struct tag wrestling).
	raw, _ := json.Marshal(c)
	merged := map[string]any{}
	_ = json.Unmarshal(raw, &merged)
	for k, v := range flat {
		// Don't clobber an existing nested field of the same name.
		if _, exists := merged[k]; !exists {
			merged[k] = v
		}
	}

	// ⚠ An anonymous caller is told WHETHER a capability is on, never WHERE it
	// lives. Measured on the public demo (2026-09-07): an unauthenticated GET
	// /api/files/capabilities answered 200 carrying
	// `"url":"https://docs.example.com"` — the operator's internal OnlyOffice host,
	// handed to anyone who asked. Not a demo bug: every install leaks whatever
	// its operator configured.
	//
	// The endpoint stays public on purpose (docs/INTEGRATION.md: embedders
	// probe it before they log in), and every consumer that needs a HOST is
	// behind a login already — the drawio iframe and the convert modal only
	// open on a file the caller can already read, and OnlyOffice's real host
	// arrives from the authenticated POST /api/files/onlyoffice/config
	// (`documentServerUrl`), never from here. So the hostnames travel with the
	// credential and the booleans travel without one.
	if anonymousCaller(r) {
		redactExternalHosts(merged)
	}

	// Per-tenant branding: identify only the tenant this host belongs to.
	if h.MultiTenant && h.Store != nil {
		if p, _ := h.Store.GetProviderByHost(r.Context(), multioidc.RequestHost(r)); p != nil {
			merged["tenant"] = map[string]any{"slug": p.Slug, "name": p.Name}
		}
	}
	/* kimlik:e3 cloud */
	if h.CloudEnabled {
		merged["cloud"] = map[string]any{"enabled": true, "signup_url": "/api/cloud/signup"}
	}
	// The longest life a new share link may be given (0 = no ceiling). The
	// share dialog reads it to offer only expiries the server will honour,
	// instead of letting someone pick "30 days" and get 7.
	if h.Store != nil {
		merged["share_max_ttl_days"] = share.NewService(h.Store).MaxTTLDays(r.Context())
	}
	if h.Archive != nil {
		// ⚠ What this server can MAKE, not only what the policy allows: every
		// format but a plain ZIP needs 7-Zip, and so does a password. Offering
		// 7z on a server without it (the slim image, a desktop install) let
		// the dialog be filled in and then fail with PROVIDER_UNAVAILABLE.
		policy := h.Archive.Policy(r.Context())
		seven := h.Archive.SevenZipAvailable(r.Context())
		formats := make([]string, 0, len(policy.AllowedFormats))
		for _, f := range policy.AllowedFormats {
			if seven || f == "zip" {
				formats = append(formats, f)
			}
		}
		def := policy.DefaultFormat
		if !containsString(formats, def) {
			def = ""
			if len(formats) > 0 {
				def = formats[0]
			}
		}
		merged["archive"] = map[string]any{
			"enabled":         policy.Enabled,
			"default_format":  def,
			"allowed_formats": formats,
			"encryption":      seven,
		}
	}

	// Who is asking — a person, or an integration (migration 00030)?
	//
	// The explorer needs this to decide whether to draw the identity-bearing
	// surfaces (its own API keys, Recent, Starred, Shared with me). It cannot
	// work it out for itself: an API token authenticates AS its owner, so from
	// the browser's side a shared embed token and a person's own token look
	// identical, and in the embeds we run ONE proxy-injected token serves every
	// visitor.
	//
	// The field is caller_KIND, not token_kind, because the answer must cover
	// the cookie/OIDC case too — a session has no token at all, and it is
	// always a person. Same reason the fallback is "user": /api/files/
	// capabilities is a public route, so an anonymous pre-login fetch (the
	// share and drop pages make one) must not read as an app.
	//
	// ⚠ Suppression is per-KIND, never per-role. A viewer is still a person.
	callerKind := model.TokenKindUser
	if auth.TokenFrom(r.Context()).IsApp() {
		callerKind = model.TokenKindApp
	}
	merged["caller_kind"] = callerKind
	merged["caller_admin"] = h.callerCanConfigure(r)
	if len(h.Queued) > 0 {
		merged["queued"] = h.Queued
	}
	// Can "Send by email" work at all? ⚠ A typed nil *mailer.Service inside
	// the interface is not == nil, so Ready is nil-safe itself.
	if h.Mail != nil {
		merged["mail"] = map[string]any{"ready": h.Mail.Ready(r.Context())}
	}

	/* wiring:e2 — say plainly whether this installation holds a second key.
	 * Fixed at install (FILEX_INSTALLATION_E2E_ESCROW_KEY) and immutable
	 * afterwards, so this answer never changes for a running installation. */
	esc := map[string]any{"enabled": false}
	if h.E2EEscrow != nil {
		esc = map[string]any{
			"enabled":    true,
			"kid":        h.E2EEscrow.KID,
			"alg":        e2e.EscrowAlg,
			"public_key": h.E2EEscrow.SPKI,
		}
	}
	merged["e2e_escrow"] = esc

	// The document types a "New document" picker may offer, from the template
	// registry compiled into this binary (internal/newdoc).
	//
	// ⚠ It answers "can the SERVER make these bytes", not "can the client open
	// them". Each row carries a `requires` field naming the external service
	// its editor needs ("onlyoffice", "drawio", or empty for the built-in code
	// and markdown editors); the client crosses that against the `external`
	// block above and offers only what is satisfied. Splitting it this way is
	// what keeps a client from carrying its own hardcoded extension list —
	// which is the list that rots the moment the registry grows a type — and
	// what stops an install with no document server from offering a .docx
	// nobody there can then open.
	//
	// Published to anonymous callers too. It is a static property of the
	// build, identical on every install of this version, and names no host.
	merged["newdoc_types"] = newdoc.Types()

	// The address a client PROGRAM should be pointed at — the WebDAV URL, the
	// `filex mount` / rclone lines in the connection guides.
	//
	// ⚠⚠ The guides used to build it from where the PAGE was loaded (the
	// explorer's apiBase, else window.location.origin). That is right for the
	// admin SPA, which the same binary serves, and wrong for every host that
	// proxies /api to filex under its OWN origin: an embed inside another app
	// printed `https://<that app>/dav/`, an address that reaches the host, not
	// filex. It also disagreed with the same page's S3 endpoint and SFTP host,
	// which have always come from here.
	//
	// ⚠ Published only when it is TRUE: the operator set public_url, or this
	// request arrived on a tenant's own host. The built-in guess
	// (http://localhost:5212) is never announced — a guide that printed it
	// would send every client to the reader's own machine; with nothing here
	// the client falls back to the address it loaded from, as before.
	// It is not a secret (every share link carries it), so an anonymous caller
	// gets it too.
	if origin := h.Tenants.FromRequest(r); origin != "" && (h.PublicURLSet || origin != h.Tenants.Fallback()) {
		merged["public_url"] = origin
	}
	// Said separately from the address itself, so the admin panel can put up a
	// sign when the operator never chose one: every share link and every mailed
	// link is then built on the built-in guess, and the person who finds out is
	// whoever receives the link (issue #32 — a compose file setting a variable
	// filex has never read, and links to localhost:5212 on a public host).
	merged["public_url_configured"] = h.PublicURLSet

	writeJSON(w, http.StatusOK, merged)
}

// anonymousCaller reports that nothing on this request identified anybody.
//
// Both halves matter: a browser session arrives as a user (auth.AnnotateUser),
// an integration arrives as an API token (auth.AnnotateToken), and a token
// whose owner the session drivers also resolved shows up as both. Neither
// annotation ever rejects, so "anonymous" here means exactly "no usable
// credential was presented", not "authentication failed".
// callerCanConfigure reports whether this caller could SET UP a missing
// optional service — i.e. reach /admin/external and the admin settings.
//
// ⚠⚠ Why the explorer needs to know, and why it asks here rather than reading
// a role: the owner's rule for a service that is not configured (2026-09-21) is
// "disabled with a reason for administrators, hidden for everybody else". For
// a person who can fix it, a greyed "Open with ONLYOFFICE — set it up under
// External services" is useful; for everybody else it is a button that can
// never work and only invites a click. The explorer is embedded in hosts that
// know nothing about filex roles, so the answer comes from the server that
// enforces the admin routes, with the same three conditions they apply:
//
//   - an administrator account (`CallerMayAdminister`, which also refuses an
//     API token without the admin scope — a shared embed token is not a
//     person who can go and configure anything);
//   - in multi-tenant mode, the SUPERTENANT: the external services and the
//     instance settings are instance-wide and a tenant admin is refused them
//     (`requireSupertenant`, `allowSettingWrite`), so telling a tenant admin
//     to go and set up ONLYOFFICE would send them to a 403.
//
// It names nothing and grants nothing: it only decides which of two honest
// sentences the menu shows.
func (h *Capabilities) callerCanConfigure(r *http.Request) bool {
	ctx := r.Context()
	if !auth.CallerMayAdminister(ctx) {
		return false
	}
	if h.MultiTenant && h.Store != nil {
		s := auth.ScopeForUser(ctx, h.Store, auth.UserFrom(ctx))
		if s == nil || !s.IsSupertenant {
			return false
		}
	}
	return true
}

func anonymousCaller(r *http.Request) bool {
	return auth.UserFrom(r.Context()) == nil && auth.TokenFrom(r.Context()) == nil
}

// redactExternalHosts strips the operator's addresses from a capabilities
// payload, leaving every flag that says whether a feature is available.
//
// It removes the `url` from each entry of the nested `external` map and blanks
// the three flat aliases. `enabled`, `state` and `last_check` stay: a public
// embedder legitimately asks "can this instance preview a .docx", and that is
// answered without naming a host.
func redactExternalHosts(merged map[string]any) {
	for _, alias := range []string{"onlyoffice_url", "drawio_url", "convert_url"} {
		if _, ok := merged[alias]; ok {
			merged[alias] = ""
		}
	}
	ext, ok := merged["external"].(map[string]any)
	if !ok {
		return
	}
	for name, svc := range ext {
		entry, ok := svc.(map[string]any)
		if !ok {
			continue
		}
		delete(entry, "url")
		ext[name] = entry
	}
}
