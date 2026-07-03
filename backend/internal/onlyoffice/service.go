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
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Service builds editor configs and validates document-server callbacks.
type Service struct {
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)

	DocumentServerURL string // e.g. https://docs.example.com
	JWTSecret         string // shared secret with the doc server
	PublicURL         string // filex public base URL — used to build callbacks
	FetchTTL          time.Duration
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

// Enabled reports whether the service has the minimum config to run.
func (s *Service) Enabled() bool {
	return s != nil && s.DocumentServerURL != "" && s.JWTSecret != ""
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
func (s *Service) BuildConfigForNode(node *model.Node, user *model.User, lang, mode string) (*EditorConfig, error) {
	if !s.Enabled() {
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
	fetchURL := s.SignedFetchURL(node.ID, exp)
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

	token, err := signHS256(body, s.JWTSecret)
	if err != nil {
		return nil, fmt.Errorf("sign config: %w", err)
	}
	body["token"] = token

	return &EditorConfig{
		DocumentServerURL: s.DocumentServerURL,
		Config:            body,
	}, nil
}

// SignedFetchURL returns an HMAC-signed URL the document server can use to
// pull document bytes from filex without authenticating.
//
// The URL embeds node id + expiry + sig=base64-url(HMAC-SHA256(secret, "n=<id>&exp=<exp>")).
func (s *Service) SignedFetchURL(nodeID, exp int64) string {
	v := url.Values{}
	v.Set("n", strconv.FormatInt(nodeID, 10))
	v.Set("exp", strconv.FormatInt(exp, 10))
	v.Set("sig", s.fetchSignature(nodeID, exp))
	return s.PublicURL + "/api/files/onlyoffice/fetch?" + v.Encode()
}

// VerifyFetchSignature validates a query against the fetch HMAC.
func (s *Service) VerifyFetchSignature(nodeID, exp int64, sig string) error {
	if exp < time.Now().Unix() {
		return errors.New("onlyoffice: signature expired")
	}
	want := s.fetchSignature(nodeID, exp)
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return errors.New("onlyoffice: bad signature")
	}
	return nil
}

func (s *Service) fetchSignature(nodeID, exp int64) string {
	mac := hmac.New(sha256.New, []byte(s.JWTSecret))
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
	if !s.Enabled() {
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
		if _, err := verifyHS256(tok, s.JWTSecret); err != nil {
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

	if err := writer.Write(r.Context(), node.Path, resp.Body, resp.ContentLength); err != nil {
		return map[string]any{"error": 1, "message": "write back: " + err.Error()}, nil
	}

	if obj, err := drv.Stat(r.Context(), node.Path); err == nil {
		_ = s.Store.UpdateNodeMeta(r.Context(), node.ID, obj.Size, obj.Mime, obj.Etag, obj.Mtime)
	}

	return map[string]any{"error": 0}, nil
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
