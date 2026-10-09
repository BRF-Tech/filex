package appstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Intent is what an install link stands for, as the store signed it
// (`GET <store>/v1/install/{token}`, signed with an `index` key).
type Intent struct {
	Store          string    `json:"store"`
	TokenID        string    `json:"token_id"`
	App            string    `json:"app"`
	Kind           string    `json:"kind"`
	Version        string    `json:"version"`
	Repo           string    `json:"repo"`
	Ref            string    `json:"ref"`
	Commit         string    `json:"commit"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	WasmSHA256     string    `json:"wasm_sha256,omitempty"`
	UISHA256       string    `json:"ui_sha256,omitempty"`
	Permissions    []string  `json:"permissions"`
	FilexRange     string    `json:"filex_range"`
	Paid           bool      `json:"paid"`
	LicenseKey     string    `json:"license_key,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	// FilexOrigin is the filex the link was made for (the store signs it):
	// another filex refuses the link (CodeWrongInstance). That stops a link
	// opened by mistake on the wrong filex, and a link sent to an
	// administrator of another filex to get an app installed there. It is
	// no protection against the administrator of the filex receiving it:
	// without FILEX_PUBLIC_URL the filex's own origin is the request's Host
	// (and X-Forwarded-Proto), which that person controls.
	FilexOrigin string `json:"filex_origin"`

	// A storage plugin's link (Kind == KindStorage, #215): the release's feed
	// the store reviewed (its sha256 is ManifestSHA256), every build by
	// platform with the store's artifact signature over the build (its name,
	// version, platform and sha256 - plugin.VerifyBuild), and what
	// the store's plugin validator measured. Absent for an app.
	FeedURL     string                 `json:"feed_url,omitempty"`
	Binaries    map[string]IntentBuild `json:"binaries,omitempty"`
	Conformance *Conformance           `json:"conformance,omitempty"`
}

// IntentBuild is one platform's build of a storage plugin, as a link pins it.
type IntentBuild struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
	// Sig is the store's artifact key's signature over the build's text -
	// the link's app and version, this platform, SHA256 (plugin.BuildClaim):
	// what FILEX_PLUGIN_TRUSTED_KEYS checks when it lists that key.
	Sig string `json:"sig"`
}

// Conformance is the store's plugin validator's run of a storage plugin
// build: filex's own conformance probes against the plugin's selftest area.
type Conformance struct {
	Platform     string   `json:"platform"`
	Filex        string   `json:"filex"`
	Verified     bool     `json:"verified"`
	Passed       int      `json:"passed"`
	Failed       int      `json:"failed"`
	Skipped      int      `json:"skipped"`
	Driver       string   `json:"driver,omitempty"`
	Capabilities []string `json:"capabilities"`
}

// Intent kinds a link may stand for.
const (
	KindApp          = "app"
	KindLanguagePack = "language_pack"
	// KindStorage is a storage plugin (#215): installed "from its source",
	// held to the link's pins (handlers/app_store_storage.go).
	KindStorage = "storage"
)

// IsStorage reports whether the link stands for a storage plugin.
func (in *Intent) IsStorage() bool { return in.Kind == KindStorage }

// LicenseID is the row a link's license is kept under: the app's name, and
// for a storage plugin "storage:<name>" - so a storage plugin and an app of
// the same name, from two stores, never share one license.
func (in *Intent) LicenseID() string { return LicenseID(in.Kind, in.App) }

// LicenseID is the license row of a store entry of kind called name.
func LicenseID(kind, name string) string {
	if kind == KindStorage {
		return StoragePrefix + name
	}
	return name
}

// StoragePrefix starts every license id (and Holder name) of a storage
// plugin; no app name holds a colon.
const StoragePrefix = "storage:"

var (
	repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// commitRe: a full git object id, SHA-1 or SHA-256, lower-case.
	commitRe = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
)

// InstanceOrigin is the one spelling of a filex's own address that a link's
// filex_origin is compared in: scheme and host in lower case, the default
// port dropped, no path, no trailing slash. A configured public URL with a
// base path (https://example.com/filex) is its origin (https://example.com).
func InstanceOrigin(raw string) (string, error) {
	return instanceOrigin(raw, true)
}

// instanceOrigin normalises raw; with allowPath false a path is refused (a
// signed filex_origin is an origin, nothing more).
func instanceOrigin(raw string, allowPath bool) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", errf(CodeIntentInvalid, "%q is not a filex address", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (!allowPath && u.Path != "" && u.Path != "/") {
		return "", errf(CodeIntentInvalid, "a filex is named by its origin alone (scheme, host, port), not %q", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	switch {
	case scheme == "https" && port == "443", scheme == "http" && port == "80":
		port = ""
	case scheme != "https" && scheme != "http":
		return "", errf(CodeIntentInvalid, "a filex address is http or https, not %q", raw)
	}
	if host == "" {
		return "", errf(CodeIntentInvalid, "%q is not a filex address", raw)
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	origin := scheme + "://" + host
	if port != "" {
		origin += ":" + port
	}
	return origin, nil
}

// maxIntentLife: a link that says it lives longer than this is refused - a
// store hands out links for a person who is about to press a button.
const maxIntentLife = 7 * 24 * time.Hour

// validate checks an intent's fields, before anything is fetched for it.
// instance is this filex's own origin (InstanceOrigin).
func (in *Intent) validate(origin, instance string, now time.Time) error {
	store, err := NormalizeOrigin(in.Store, true)
	if err != nil || store != origin {
		return errf(CodeIntentInvalid, "the link was signed for the store %q, not for %s", in.Store, origin)
	}
	switch {
	case strings.TrimSpace(in.TokenID) == "":
		return errf(CodeIntentInvalid, "the link has no token_id")
	case strings.TrimSpace(in.App) == "":
		return errf(CodeIntentInvalid, "the link names no app")
	case strings.TrimSpace(in.Version) == "":
		return errf(CodeIntentInvalid, "the link names no version")
	case !repoRe.MatchString(in.Repo):
		return errf(CodeIntentInvalid, "the link's repository %q is not owner/name", in.Repo)
	case strings.TrimSpace(in.Ref) == "":
		return errf(CodeIntentInvalid, "the link names no ref (the release tag)")
	case !commitRe.MatchString(in.Commit):
		return errf(CodeIntentInvalid, "the link names no commit (the full id of the commit the store approved), got %q", in.Commit)
	case !shaRe.MatchString(in.ManifestSHA256):
		return errf(CodeIntentInvalid, "the link does not pin the manifest (manifest_sha256, a lower-case SHA-256)")
	case in.ExpiresAt.IsZero():
		return errf(CodeIntentInvalid, "the link has no expires_at")
	case strings.TrimSpace(in.FilexOrigin) == "":
		return errf(CodeIntentInvalid, "the link does not name the filex it was made for (filex_origin)")
	}
	target, err := instanceOrigin(in.FilexOrigin, false)
	if err != nil {
		return err
	}
	if instance == "" || target != instance {
		return &Error{Code: CodeWrongInstance,
			Message: "this install link was made for " + target + ", not for this filex (" + instance + "); open the store's Install again from this filex",
			Detail:  map[string]any{"filex_origin": target, "this_filex": instance}}
	}
	if in.Kind != KindApp && in.Kind != KindLanguagePack && in.Kind != KindStorage {
		return errf(CodeIntentInvalid, "the link is for a %q; filex installs apps, language packs and storage plugins from a store", in.Kind)
	}
	if in.Kind == KindStorage {
		if err := in.validateStorage(); err != nil {
			return err
		}
	}
	for _, h := range []string{in.ManifestSHA256, in.WasmSHA256, in.UISHA256} {
		if h != "" && !shaRe.MatchString(h) {
			return errf(CodeIntentInvalid, "a pinned hash in the link is not a lower-case SHA-256")
		}
	}
	if in.ManifestSHA256 == "" && in.WasmSHA256 == "" && in.UISHA256 == "" {
		return errf(CodeIntentInvalid, "the link pins nothing (manifest, module or interface SHA-256)")
	}
	if !now.Before(in.ExpiresAt) {
		return errf(CodeIntentExpired, "this install link expired at %s", in.ExpiresAt.UTC().Format(time.RFC3339))
	}
	if in.ExpiresAt.Sub(now) > maxIntentLife {
		return errf(CodeIntentInvalid, "the link says it lives until %s, longer than a store's install link may", in.ExpiresAt.UTC().Format(time.RFC3339))
	}
	return nil
}

// ReadIntent fetches and checks an install link: the store must be trusted
// (or an administrator is asked, CodeTrustRequired / CodeKeyChanged), the
// answer signed by one of its `index` keys, the payload this store's, made
// for this filex (instance, its own origin: InstanceOrigin), naming its
// commit and pinning its manifest, not expired and not used here before.
func (s *Service) ReadIntent(ctx context.Context, origin, token, instance string) (*Intent, error) {
	// The trust first: an untrusted store's link is never even fetched.
	if _, err := s.trustFor(ctx, origin, true); err != nil {
		return nil, err
	}
	env, err := s.opts.Client.Intent(ctx, origin, token)
	if err != nil {
		return nil, err
	}
	if _, err := s.verifyWith(ctx, origin, env, UseIndex, false); err != nil {
		return nil, err
	}
	var in Intent
	dec := json.NewDecoder(bytes.NewReader(env.Payload))
	if err := dec.Decode(&in); err != nil {
		return nil, errf(CodeIntentInvalid, "the signed link does not read: %v", err)
	}
	// ⚠ The wall clock, not a store's proven time: a link's few minutes are
	// the store's to enforce too (410 once expired), and a license answer of
	// any store must not move a link into the past.
	if err := in.validate(origin, instance, s.opts.Now().UTC()); err != nil {
		return nil, err
	}
	if used, err := s.intentUsed(ctx, origin, in.TokenID); err != nil {
		return nil, err
	} else if used != nil {
		return nil, errf(CodeIntentUsed, "this install link was already used here (%s, %s)", used.Result, used.At.UTC().Format(time.RFC3339))
	}
	return &in, nil
}

// validateStorage checks a storage plugin link's own fields: a feed read from
// https, at least one build, and every build pinned and addressed over https.
func (in *Intent) validateStorage() error {
	if !strings.HasPrefix(strings.TrimSpace(in.FeedURL), "https://") {
		return errf(CodeIntentInvalid, "the link names no feed (feed_url, an https address)")
	}
	if len(in.Binaries) == 0 {
		return errf(CodeIntentInvalid, "the link names no build")
	}
	for plat, b := range in.Binaries {
		if !shaRe.MatchString(b.SHA256) {
			return errf(CodeIntentInvalid, "the %s build in the link is not pinned by a lower-case SHA-256", plat)
		}
		if !strings.HasPrefix(strings.TrimSpace(b.URL), "https://") {
			return errf(CodeIntentInvalid, "the %s build in the link is not an https address", plat)
		}
	}
	return nil
}

// usedIntent is the row an install link leaves once it is done.
type usedIntent struct {
	Result  string    `json:"result"`
	App     string    `json:"app"`
	Version string    `json:"version"`
	At      time.Time `json:"at"`
	Expires time.Time `json:"expires"`
}

func intentKey(origin, tokenID string) string { return keyIntentPrefix + origin + "/" + tokenID }

func (s *Service) intentUsed(ctx context.Context, origin, tokenID string) (*usedIntent, error) {
	var u usedIntent
	ok, err := s.getJSON(ctx, intentKey(origin, tokenID), &u)
	if err != nil || !ok {
		return nil, err
	}
	return &u, nil
}

// markUsed records that a link ended (installed / cancelled), so the same
// link is refused here whatever the store says later. Rows of links long
// expired are pruned on the way.
func (s *Service) markUsed(ctx context.Context, origin string, in *Intent, result string) {
	now := s.opts.Now().UTC()
	if err := s.putJSON(ctx, intentKey(origin, in.TokenID), usedIntent{Result: result, App: in.App, Version: in.Version, At: now, Expires: in.ExpiresAt}); err != nil {
		s.log.Warn("app-store: used install link not recorded", slog.Any("err", err))
	}
	rows, err := s.opts.Store.ListAppStoreState(ctx, keyIntentPrefix)
	if err != nil {
		return
	}
	for k, raw := range rows {
		var u usedIntent
		// Kept well past the link's own expiry: an expired link is refused
		// for that reason anyway, so the row only has to outlive it.
		if unmarshal(raw, &u) == nil && !u.Expires.IsZero() && now.Sub(u.Expires) > 30*24*time.Hour {
			_, _ = s.opts.Store.DeleteAppStoreState(ctx, k)
		}
	}
	s.pruneReqLinks(ctx, now)
}

// ── Reviewed links, between the review and the administrator's decision ──

// Pending is a link that passed every check and whose review the
// administrator is reading. The browser holds its Handle, never the link's
// license key.
type Pending struct {
	Handle  string
	Origin  string
	Token   string
	Intent  *Intent
	UserID  int64
	Created time.Time
	// RequestID is the plugin request (pluginreq) the link was asked for by
	// an approval in the embedded store (RequestFor); 0 for a magic link.
	RequestID int64
	// LegacySignature: the storage plugin's build was taken with a
	// signature in the old sha256-only form (plugin.VerifyBuild; accepted
	// during 0.55 with a warning). Finish says so in the audit row.
	LegacySignature bool
}

// pendingLife bounds how long a reviewed link waits (its own expiry, if
// sooner, decides).
const pendingLife = time.Hour

// Hold keeps a reviewed link under a new handle for the administrator who
// reviewed it.
func (s *Service) Hold(origin, token string, in *Intent, userID int64) *Pending {
	return s.HoldFor(origin, token, in, userID, 0)
}

// HoldFor is Hold for a link an approval asked for: requestID is the plugin
// request it ends (0: none).
func (s *Service) HoldFor(origin, token string, in *Intent, userID, requestID int64) *Pending {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	p := &Pending{Handle: hex.EncodeToString(b), Origin: origin, Token: token, Intent: in, UserID: userID, Created: s.opts.Now().UTC(),
		RequestID: requestID}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prunePendingLocked()
	s.pending[p.Handle] = p
	return p
}

// Pending answers the reviewed link under handle for this administrator.
func (s *Service) Pending(handle string, userID int64) (*Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prunePendingLocked()
	p, ok := s.pending[handle]
	if !ok || p.UserID != userID {
		return nil, errf(CodeIntentNotFound, "this review is no longer open; open the store's install link again")
	}
	return p, nil
}

// Take answers the reviewed link under handle for this administrator AND
// removes it, in one step: of two installs of one review at once (a double
// click, or on purpose) only one gets it; the other is told the review is
// not open (CodeIntentNotFound). An install that fails before anything is
// installed puts it back (PutBack), so the administrator can try again.
func (s *Service) Take(handle string, userID int64) (*Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prunePendingLocked()
	p, ok := s.pending[handle]
	if !ok || p.UserID != userID {
		return nil, errf(CodeIntentNotFound, "this review is no longer open; open the store's install link again")
	}
	delete(s.pending, handle)
	return p, nil
}

// PutBack returns a taken review (its install failed), unless it has
// expired meanwhile.
func (s *Service) PutBack(p *Pending) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[p.Handle] = p
	s.prunePendingLocked()
}

// LockApp serialises the store installs of one app name (the license, the
// install, the record of where it came from): answers the unlock.
func (s *Service) LockApp(app string) (unlock func()) {
	return s.appLocks.Lock(app)
}

// Drop forgets a reviewed link.
func (s *Service) Drop(handle string) {
	s.mu.Lock()
	delete(s.pending, handle)
	s.mu.Unlock()
}

func (s *Service) prunePendingLocked() {
	now := s.opts.Now().UTC()
	for h, p := range s.pending {
		if now.Sub(p.Created) > pendingLife || !now.Before(p.Intent.ExpiresAt) {
			delete(s.pending, h)
		}
	}
}

// installSource is where an app installed from a store came from: the
// store's origin and the repository its link named (app_store_state
// `source:<app>`). A later link for an app of the same name is held to it
// (CodeSourceChanged).
type installSource struct {
	Store   string    `json:"store"`
	Repo    string    `json:"repo"`
	Version string    `json:"version,omitempty"`
	At      time.Time `json:"at"`
}

// InstalledFrom answers the store an installed app came from: the one its
// install link was signed by, or the one its license is with; "" when it was
// not installed from a store.
func (s *Service) InstalledFrom(ctx context.Context, app string) (string, error) {
	return s.installedFrom(ctx, app)
}

// StorageInstalledFrom is InstalledFrom for a storage plugin.
func (s *Service) StorageInstalledFrom(ctx context.Context, name string) (string, error) {
	return s.installedFrom(ctx, LicenseID(KindStorage, name))
}

// installedFrom is InstalledFrom by the id a store entry is kept under
// (LicenseID).
func (s *Service) installedFrom(ctx context.Context, app string) (string, error) {
	var src installSource
	if ok, err := s.getJSON(ctx, keySourcePrefix+app, &src); err != nil {
		return "", err
	} else if ok && src.Store != "" {
		return src.Store, nil
	}
	l, err := s.license(ctx, app)
	if err != nil || l == nil {
		return "", err
	}
	return l.Store, nil
}

// Finish ends a reviewed link: the store is told (installed / cancelled) and
// the link is recorded as used here. A store that cannot be told is logged;
// the install stands.
func (s *Service) Finish(ctx context.Context, p *Pending, result string, actorID *int64) {
	s.Drop(p.Handle)
	s.markUsed(ctx, p.Origin, p.Intent, result)
	if result == "installed" {
		src := installSource{Store: p.Origin, Repo: p.Intent.Repo, Version: p.Intent.Version, At: s.opts.Now().UTC()}
		if err := s.putJSON(ctx, keySourcePrefix+p.Intent.LicenseID(), src); err != nil {
			s.log.Warn("app-store: where the app came from was not recorded", slog.String("app", p.Intent.App), slog.Any("err", err))
		}
	}
	id, err := s.InstanceID(ctx)
	if err == nil {
		err = s.opts.Client.Complete(ctx, p.Origin, p.Token, id, result)
	}
	if err != nil {
		s.log.Warn("app-store: the store was not told how the install link ended",
			slog.String("store", p.Origin), slog.String("app", p.Intent.App), slog.String("result", result), slog.Any("err", err))
	}
	action := "app_store.install"
	if result != "installed" {
		action = "app_store.cancel"
	}
	target := "app_plugin"
	if p.Intent.IsStorage() {
		target = "plugin"
	}
	meta := map[string]any{
		"store": p.Origin, "app": p.Intent.App, "kind": p.Intent.Kind, "version": p.Intent.Version, "repo": p.Intent.Repo, "ref": p.Intent.Ref,
		"token_id": p.Intent.TokenID, "paid": p.Intent.Paid,
	}
	if p.LegacySignature {
		meta["legacy_signature"] = true
	}
	s.audit(ctx, actorID, action, target, p.Intent.App, meta)
}

// ── What the source serves, against what the link pinned ───────────────

// Source is what filex fetched for a link from the app's repository: the
// manifest's bytes and what it says.
type Source struct {
	ManifestBytes []byte
	Name          string
	Version       string
	Kind          string
	// DeclaredPermissions is the manifest's own `permissions` list (not the
	// ones filex derives from its interface).
	DeclaredPermissions []string
	WasmSHA256          string // the manifest's wasm.sha256 (the module filex fetched hashes to it)
	UISHA256            string // the manifest's ui.bundle.sha256
}

// Mismatch is one pin the source does not keep.
type Mismatch struct {
	Field  string `json:"field"`
	Link   string `json:"link"`
	Source string `json:"source"`
}

// ComparePins holds the source to the link: the same app and version, the
// same manifest bytes, module and interface hashes, and the same declared
// permissions. Every difference is listed; any one refuses the install.
func ComparePins(in *Intent, src *Source) error {
	var out []Mismatch
	add := func(field, link, source string) {
		out = append(out, Mismatch{Field: field, Link: link, Source: source})
	}
	if src.Name != in.App {
		add("app", in.App, src.Name)
	}
	if src.Version != in.Version {
		add("version", in.Version, src.Version)
	}
	if src.Kind != "" && src.Kind != in.Kind {
		add("kind", in.Kind, src.Kind)
	}
	if in.ManifestSHA256 != "" {
		if got := sha256Hex(src.ManifestBytes); got != in.ManifestSHA256 {
			add("manifest_sha256", in.ManifestSHA256, got)
		}
	}
	if in.WasmSHA256 != "" && strings.ToLower(src.WasmSHA256) != in.WasmSHA256 {
		add("wasm_sha256", in.WasmSHA256, strings.ToLower(src.WasmSHA256))
	}
	if in.WasmSHA256 == "" && src.WasmSHA256 != "" && in.ManifestSHA256 == "" {
		add("wasm_sha256", "", strings.ToLower(src.WasmSHA256))
	}
	if in.UISHA256 != "" && strings.ToLower(src.UISHA256) != in.UISHA256 {
		add("ui_sha256", in.UISHA256, strings.ToLower(src.UISHA256))
	}
	if in.UISHA256 == "" && src.UISHA256 != "" && in.ManifestSHA256 == "" {
		add("ui_sha256", "", strings.ToLower(src.UISHA256))
	}
	a, b := normalizePerms(in.Permissions), normalizePerms(src.DeclaredPermissions)
	if strings.Join(a, ",") != strings.Join(b, ",") {
		add("permissions", strings.Join(a, ","), strings.Join(b, ","))
	}
	if len(out) == 0 {
		return nil
	}
	return &Error{Code: CodePinMismatch,
		Message: "what the repository serves is not what the store approved; nothing was installed",
		Detail:  map[string]any{"mismatches": out}}
}

// normalizePerms is the store's own normalisation (trimmed, an http: host in
// lower case, no repeats), sorted - minus the permissions filex derives from
// an interface, which a manifest never declares.
func normalizePerms(ids []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if h, ok := strings.CutPrefix(id, "http:"); ok {
			id = "http:" + strings.ToLower(h)
		}
		if id == "" || seen[id] || derivedPermission(id) {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func derivedPermission(id string) bool {
	switch id {
	case "ui", "ui:eval", "ui:wasm-eval", "ui:package-fetch", "ui:download", "ui:frame-package", "ui:connect-blob", "ui:print":
		return true
	}
	for _, p := range []string{"ui:", "ui-net:", "ui-viewer:", "ui-new:", "thumbnail:"} {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}
