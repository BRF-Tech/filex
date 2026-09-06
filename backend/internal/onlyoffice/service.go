// Package onlyoffice integrates a self-hosted OnlyOffice Document Server
// with filex storage backends.
//
// Three pieces cooperate:
//
//   - Config: builds a JWT-signed editor descriptor for the embed iframe.
//     Includes a signed fetch URL the document server uses to pull source
//     bytes from filex.
//   - Fetch: serves source bytes back to the document server (HMAC-signed
//     URL). Public — no session required, but the URL is unguessable and
//     short-lived.
//   - Callback: receives save events from the document server, validates
//     the JWT it sends, downloads the saved revision, and writes back to
//     storage.
//
// All signing uses HS256 (HMAC-SHA256) with the JWTSecret loaded from
// config (FILEX_ONLYOFFICE_JWT). That same secret is what the document
// server has in its `local.json` -> `services.CoAuthoring.token.enable`
// stanza.
package onlyoffice

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Service builds editor configs and validates document-server callbacks.
type Service struct {
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)

	// DocumentServerURL / JWTSecret are the BOOT-TIME values (env/YAML). They
	// are the fallback only: when Live is wired the running configuration is
	// read from the `external_services` row on every call, so an operator who
	// configures OnlyOffice in the admin UI does not have to restart filex.
	// See internal/external and issue #17.
	DocumentServerURL string // e.g. https://docs.example.com
	JWTSecret         string // shared secret with the doc server
	// Live, when non-nil, returns the current url + secret. Nil means "use the
	// fields", which is what the unit tests and any no-DB caller want.
	Live      func(ctx context.Context) (url, secret string)
	PublicURL string // filex public base URL — used to build callbacks
	FetchTTL  time.Duration

	// Sync is the shared post-write gate every other write surface in filex
	// goes through: it upserts the node row, re-indexes the document,
	// dispatches a thumbnail, tells open explorers, emits the canonical
	// `file.updated` webhook event and schedules the antivirus scan.
	//
	// ⚠ Optional, and its absence degrades gracefully rather than silently:
	// an unwired Service falls back to a Syncer with no search index (see
	// syncer), so a save still announces itself and is still scanned — only
	// the re-index is skipped. Wire it with AttachSync at boot.
	Sync *protocolsync.Syncer
}

// AttachSync wires the post-write gate. ⚠ Without it an edited document keeps
// its PRE-EDIT text in content search, because nothing else ever revisits a
// file whose path did not change.
func (s *Service) AttachSync(sy *protocolsync.Syncer) { s.Sync = sy }

// syncer returns the gate to fan out through. The fallback carries no index
// (there is none to carry) but keeps the other three side effects, because a
// missing wire must not be the difference between a document being scanned and
// not being scanned.
func (s *Service) syncer() *protocolsync.Syncer {
	if s.Sync != nil {
		return s.Sync
	}
	return protocolsync.New(s.Store, nil, nil, writehook.OriginOnlyOffice)
}

// settings resolves the configuration in force RIGHT NOW.
//
// ⚠ Every path that signs, verifies or hands out a document-server URL goes
// through here. A path that reads the struct fields directly would keep working
// on an env-configured install and silently 503 on a UI-configured one — which
// is exactly the shape of the bug this replaced.
func (s *Service) settings(ctx context.Context) (string, string) {
	if s == nil {
		return "", ""
	}
	if s.Live != nil {
		if u, sec := s.Live(ctx); u != "" || sec != "" {
			return strings.TrimRight(u, "/"), sec
		}
	}
	return s.DocumentServerURL, s.JWTSecret
}

// New constructs a Service. fetchTTL defaults to 1 hour when zero.
func New(store db.Store, resolver func(int64) (storage.Driver, error), docURL, jwtSecret, publicURL string, fetchTTL time.Duration) *Service {
	if fetchTTL <= 0 {
		fetchTTL = time.Hour
	}
	return &Service{
		Store:             store,
		StorageResolver:   resolver,
		DocumentServerURL: strings.TrimRight(docURL, "/"),
		JWTSecret:         jwtSecret,
		PublicURL:         strings.TrimRight(publicURL, "/"),
		FetchTTL:          fetchTTL,
	}
}

// Enabled reports whether the service has the minimum config to run, as of
// this instant — not as of the last restart.
func (s *Service) Enabled() bool { return s.EnabledCtx(context.Background()) }

// EnabledCtx is Enabled with a caller-supplied context, so the DB read that
// backs it inherits the request's deadline and cancellation.
func (s *Service) EnabledCtx(ctx context.Context) bool {
	if s == nil {
		return false
	}
	u, sec := s.settings(ctx)
	return u != "" && sec != ""
}

// EditorConfig is the descriptor we send to the embed iframe.
type EditorConfig struct {
	DocumentServerURL string         `json:"documentServerUrl"`
	Config            map[string]any `json:"config"`
}

// BuildConfigForNode resolves the node, presigns the fetch URL, and signs
// the JSON descriptor with HS256. `mode` selects "edit" or "view"; any
// value other than "edit" is treated as read-only and toggles the
// permissions block so OnlyOffice renders the document with toolbars
// disabled.
func (s *Service) BuildConfigForNode(ctx context.Context, node *model.Node, user *model.User, lang, mode string) (*EditorConfig, error) {
	docURL, secret := s.settings(ctx)
	if docURL == "" || secret == "" {
		return nil, errors.New("onlyoffice: not configured")
	}
	if node == nil {
		return nil, errors.New("onlyoffice: nil node")
	}
	fileType := strings.TrimPrefix(strings.ToLower(path.Ext(node.Name)), ".")
	if fileType == "" {
		return nil, errors.New("onlyoffice: cannot determine file type")
	}
	docType := DocumentType(fileType)
	if docType == "" {
		return nil, fmt.Errorf("onlyoffice: unsupported file type %q", fileType)
	}

	mtime := int64(0)
	if node.BackendMtime != nil {
		mtime = node.BackendMtime.Unix()
	}
	keyInput := fmt.Sprintf("%d|%s|%d|%d", node.ID, node.PathHash, mtime, node.Size)
	hash := md5.Sum([]byte(keyInput))
	key := hex.EncodeToString(hash[:])

	exp := time.Now().Add(s.FetchTTL).Unix()
	fetchURL := s.signedFetchURL(node.ID, exp, secret)
	callbackURL := fmt.Sprintf("%s/api/files/onlyoffice/callback?node=%d", s.PublicURL, node.ID)

	userID := "anon"
	userName := "anonymous"
	if user != nil {
		userID = strconv.FormatInt(user.ID, 10)
		userName = user.Email
	}

	effectiveMode := "edit"
	if mode != "" && mode != "edit" {
		effectiveMode = "view"
	}
	canEdit := effectiveMode == "edit"
	body := map[string]any{
		"document": map[string]any{
			"key":      key,
			"title":    node.Name,
			"url":      fetchURL,
			"fileType": fileType,
			"permissions": map[string]any{
				"edit":     canEdit,
				"download": true,
				"print":    true,
				"comment":  canEdit,
				"review":   canEdit,
			},
		},
		"documentType": docType,
		"editorConfig": map[string]any{
			"callbackUrl": callbackURL,
			"user": map[string]any{
				"id":   userID,
				"name": userName,
			},
			"lang": fallback(lang, "en"),
			"mode": effectiveMode,
		},
	}

	token, err := signHS256(body, secret)
	if err != nil {
		return nil, fmt.Errorf("sign config: %w", err)
	}
	body["token"] = token

	return &EditorConfig{
		DocumentServerURL: docURL,
		Config:            body,
	}, nil
}

// SignedFetchURL returns an HMAC-signed URL the document server can use to
// pull document bytes from filex without authenticating.
//
// The URL embeds node id + expiry + sig=base64-url(HMAC-SHA256(secret, "n=<id>&exp=<exp>")).
func (s *Service) SignedFetchURL(nodeID, exp int64) string {
	_, secret := s.settings(context.Background())
	return s.signedFetchURL(nodeID, exp, secret)
}

func (s *Service) signedFetchURL(nodeID, exp int64, secret string) string {
	v := url.Values{}
	v.Set("n", strconv.FormatInt(nodeID, 10))
	v.Set("exp", strconv.FormatInt(exp, 10))
	v.Set("sig", fetchSignature(nodeID, exp, secret))
	return s.PublicURL + "/api/files/onlyoffice/fetch?" + v.Encode()
}

// VerifyFetchSignature validates a query against the fetch HMAC.
func (s *Service) VerifyFetchSignature(nodeID, exp int64, sig string) error {
	return s.VerifyFetchSignatureCtx(context.Background(), nodeID, exp, sig)
}

// VerifyFetchSignatureCtx is VerifyFetchSignature with the request context, so
// the secret is the one in force now.
func (s *Service) VerifyFetchSignatureCtx(ctx context.Context, nodeID, exp int64, sig string) error {
	if exp < time.Now().Unix() {
		return errors.New("onlyoffice: signature expired")
	}
	_, secret := s.settings(ctx)
	want := fetchSignature(nodeID, exp, secret)
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return errors.New("onlyoffice: bad signature")
	}
	return nil
}

func fetchSignature(nodeID, exp int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "n=%d&exp=%d", nodeID, exp)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// CallbackPayload is the body the document server posts on save / status
// transitions. See https://api.onlyoffice.com/editors/callback for the
// full schema; we use the fields filex actually needs.
type CallbackPayload struct {
	Key    string `json:"key"`
	Status int    `json:"status"`
	URL    string `json:"url"`
	Token  string `json:"token,omitempty"`
}

// Status codes per OnlyOffice callback spec.
const (
	StatusBeingEdited    = 1
	StatusReadyForSaving = 2
	StatusSavingError    = 3
	StatusClosedNoChange = 4
	StatusForceSave      = 6
	StatusForceSaveError = 7
)

// HandleCallback validates the JWT, downloads the saved version, and writes
// it back to storage.
//
// Returns the OnlyOffice expected JSON envelope.
func (s *Service) HandleCallback(r *http.Request, nodeID int64) (map[string]any, error) {
	_, secret := s.settings(r.Context())
	if !s.EnabledCtx(r.Context()) {
		return nil, errors.New("onlyoffice: not configured")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	defer r.Body.Close()

	var p CallbackPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("bad json: %w", err)
	}

	// JWT verification — token may live in body or Authorization header.
	tok := p.Token
	if tok == "" {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			tok = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	if tok != "" {
		if _, err := verifyHS256(tok, secret); err != nil {
			return nil, fmt.Errorf("token: %w", err)
		}
	}

	// Status 1 (being edited) and 4 (closed no change) are no-ops.
	if p.Status != StatusReadyForSaving && p.Status != StatusForceSave {
		return map[string]any{"error": 0}, nil
	}
	if p.URL == "" {
		return map[string]any{"error": 1, "message": "missing url"}, nil
	}

	node, err := s.Store.GetNode(r.Context(), nodeID)
	if err != nil {
		return map[string]any{"error": 1, "message": "node not found"}, nil
	}
	drv, err := s.StorageResolver(node.StorageID)
	if err != nil {
		return map[string]any{"error": 1, "message": "no driver"}, nil
	}
	writer, ok := drv.(storage.Writer)
	if !ok {
		return map[string]any{"error": 1, "message": "storage not writable"}, nil
	}

	client := &http.Client{Timeout: 60 * time.Second}
	req, _ := http.NewRequestWithContext(r.Context(), "GET", p.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		return map[string]any{"error": 1, "message": "fetch saved doc: " + err.Error()}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return map[string]any{"error": 1, "message": "fetch saved doc http " + strconv.Itoa(resp.StatusCode)}, nil
	}

	// Saving back onto a name that has since become a folder would leave `X`
	// and `X/…` side by side on an object store (storage.ErrKindConflict).
	if err := storage.EnsureFileTarget(r.Context(), drv, node.Path); err != nil {
		return map[string]any{"error": 1, "message": "write back: " + err.Error()}, nil
	}
	// The last moment at which the bytes this save is about to replace still
	// exist -- see writehook/overwrite.go.
	//
	// ⚠⚠ The message handed back to the document server is a CONSTANT, never
	// the guard error itself. That error wraps the driver error and the
	// storage-relative path, and this callback sits on the PUBLIC route block
	// with its JWT checked only when a token is configured -- so an
	// unauthenticated caller could otherwise walk ?node=1,2,3... and read path
	// fragments back out of every tenant's storage. The detail still has to
	// reach an operator, so it goes to the log, the same shape every other
	// guarded site uses.
	if err := writehook.BeforeOverwrite(r.Context(), node.StorageID, node.Path); err != nil {
		slog.Warn("onlyoffice callback refused: snapshot",
			slog.Int64("storage", node.StorageID),
			slog.String("path", node.Path),
			slog.String("err", err.Error()))
		return map[string]any{"error": 1, "message": "could not preserve the existing file"}, nil
	}
	if err := writer.Write(r.Context(), node.Path, resp.Body, resp.ContentLength); err != nil {
		return map[string]any{"error": 1, "message": "write back: " + err.Error()}, nil
	}

	// ⚠ The driver Stat has to land on the row BEFORE the gate below runs, and
	// the etag is the reason. search.ContentFingerprint prefers the etag, and
	// the index re-extracts a document's text only when that fingerprint
	// drifted — so indexing while the row still carries the pre-edit etag
	// would re-index the metadata and leave the OLD TEXT searchable, which is
	// most of the symptom this write is fixing. It is also the only place the
	// backend's own mtime is available.
	size, mime := node.Size, node.Mime
	if obj, err := drv.Stat(r.Context(), node.Path); err == nil {
		_ = s.Store.UpdateNodeMeta(r.Context(), node.ID, obj.Size, obj.Mime, obj.Etag, obj.Mtime)
		size, mime = obj.Size, obj.Mime
	}

	s.announceSave(r.Context(), node, size, mime, p.Status)

	return map[string]any{"error": 0}, nil
}

// announceSave runs the shared post-write gate for a revision that has just
// landed on storage: node row, search index, thumbnail, realtime frame,
// canonical `file.updated` event and the antivirus scan.
//
// # Immediate or debounced, and why the status code decides
//
// The browser's text editor cannot tell a mid-session Ctrl+S from the last
// one, so it approximates the end of an editing session with a timer. The
// document server does not make filex guess — it says which kind of save this
// is, and the two kinds want opposite answers:
//
//	status 2 (ready for saving) — every editor has CLOSED the document and the
//	  server has assembled the final revision, which it posts about ten seconds
//	  later. It arrives once per editing session, after the editing is over, and
//	  the bytes are final. That is the shape of an upload, so it is scanned like
//	  one: deferring it would buy no coalescing at all and would leave the
//	  finished document unscanned for up to a full save window.
//
//	status 6 (force save) — an INTERIM save with the document still open. filex
//	  never asks for one and the document server does not send them by default
//	  (`autoAssembly` is off), but an operator can switch them on, and then they
//	  repeat for as long as somebody keeps the document open. That is the shape
//	  of a Ctrl+S burst, so it takes the same debounced window: one scan per
//	  file per window, reading the file as it stands when the scan finally runs.
//
// A session with force-save enabled therefore costs one scan per window while
// it is open, plus one immediate scan of the final revision when it closes.
// ⚠ That last one is deliberate belt-and-braces: it is what covers a session
// whose interim scan has not come due yet, and a document server that dies
// mid-session never sends status 2 at all, which is exactly when the debounced
// scan of the interim bytes is the only one there will ever be.
func (s *Service) announceSave(ctx context.Context, node *model.Node, size int64, mime string, status int) {
	st, err := s.Store.GetStorage(ctx, node.StorageID)
	if err != nil || st == nil {
		// The bytes are on storage and the row is refreshed; only the fan-out
		// is lost. Warn rather than fail the callback — telling the document
		// server the save failed would make it retry a write that succeeded.
		slog.Warn("onlyoffice callback: storage row for the post-write gate",
			slog.Int64("storage", node.StorageID),
			slog.String("path", node.Path))
		return
	}
	sy := s.syncer()
	if status == StatusForceSave {
		sy.WriteSaved(ctx, st, node.Path, size, mime)
		return
	}
	sy.Write(ctx, st, node.Path, size, mime)
}

// DocumentType returns "word", "cell", "slide", or "" for an extension.
//
// Matches the official OnlyOffice mapping. If the extension is unknown we
// return "" so callers can 415.
func DocumentType(ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "doc", "docm", "docx", "dot", "dotm", "dotx", "epub", "fodt", "htm", "html", "mht", "mhtml", "odt", "ott", "pdf", "rtf", "stw", "sxw", "txt", "wps", "wpt", "xml", "xps":
		return "word"
	case "csv", "et", "ett", "fods", "ods", "ots", "sxc", "xls", "xlsb", "xlsm", "xlsx", "xlt", "xltm", "xltx":
		return "cell"
	case "dps", "dpt", "fodp", "odp", "otp", "pot", "potm", "potx", "pps", "ppsm", "ppsx", "ppt", "pptm", "pptx", "sxi":
		return "slide"
	}
	return ""
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
