package onlyoffice

// Fetches for a purpose (filex 0.50): the document server downloading a file
// to CONVERT it - a thumbnail of an office document, an app's conversion -
// rather than to open it in an editor.
//
// They walk the same door an editor's download does (FetchPath), with an
// address that names its purpose: `p=thumb` or `p=convert`. The purpose is
// part of what is signed (purposeSignature), so an address handed out for a
// thumbnail cannot be turned into an editor's, nor the other way round, and it
// lives PurposeFetchTTL (10 minutes) instead of an editor's hour: a conversion
// is downloaded at once or not at all.
//
// Two things differ at the door (handlers.OnlyOffice.Fetch):
//
//   - An end-to-end encrypted file is refused (415). The thumbnail pipeline
//     does not ask for one in the first place; this is the second defence, for
//     a file whose bytes changed between the two (or a caller that forgot).
//   - What the door answered is recorded apart from the editor's record
//     (fetchlog.go). A thumbnail drawn while somebody's editor said "Download
//     failed" must not turn that diagnosis into "the document server got the
//     document": it was another request, for another reason. The purpose
//     record answers one question only - did the document server's download
//     reach filex, and what did filex answer - which is how a -4 is told apart
//     (the document server never reached filex, or filex refused it).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The purposes a fetch address may name.
const (
	PurposeThumb   = "thumb"
	PurposeConvert = "convert"
)

// PurposeFetchTTL is how long a purpose's fetch address is good for.
const PurposeFetchTTL = 10 * time.Minute

// ValidPurpose reports whether p is a purpose a fetch address may name.
func ValidPurpose(p string) bool { return p == PurposeThumb || p == PurposeConvert }

// ErrNotConfigured: OnlyOffice is not configured in filex right now (no
// document server address, or no secret).
var ErrNotConfigured = errors.New("onlyoffice: not configured")

// ErrUnknownPurpose: a fetch address names a purpose filex does not hand out.
var ErrUnknownPurpose = errors.New("onlyoffice: unknown fetch purpose")

// FetchEncrypted is the reason code of a purpose fetch refused because the
// file is end-to-end encrypted.
const FetchEncrypted = "encrypted"

func purposeSignature(nodeID, exp int64, purpose, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "n=%d&exp=%d&p=%s", nodeID, exp, purpose)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// PurposeFetchURL is the address the document server downloads node from for
// a purpose: the fetch endpoint on the address the document server reaches
// filex at (callbackBase), signed with the secret in force and the purpose.
func (s *Service) PurposeFetchURL(ctx context.Context, nodeID int64, purpose string) (string, error) {
	if !ValidPurpose(purpose) {
		return "", ErrUnknownPurpose
	}
	u, secret := s.settings(ctx)
	if u == "" || secret == "" {
		return "", ErrNotConfigured
	}
	exp := time.Now().Add(PurposeFetchTTL).Unix()
	v := url.Values{}
	v.Set("n", strconv.FormatInt(nodeID, 10))
	v.Set("exp", strconv.FormatInt(exp, 10))
	v.Set("p", purpose)
	v.Set("sig", purposeSignature(nodeID, exp, purpose, secret))
	return s.callbackBase(ctx) + FetchPath + "?" + v.Encode(), nil
}

// VerifyPurposeFetchCtx checks a purpose's fetch address against the secret
// in force: ErrUnknownPurpose, ErrSignatureExpired or ErrBadSignature.
func (s *Service) VerifyPurposeFetchCtx(ctx context.Context, nodeID, exp int64, purpose, sig string) error {
	if !ValidPurpose(purpose) {
		return ErrUnknownPurpose
	}
	if exp < time.Now().Unix() {
		return ErrSignatureExpired
	}
	_, secret := s.settings(ctx)
	if secret == "" || !hmac.Equal([]byte(purposeSignature(nodeID, exp, purpose, secret)), []byte(sig)) {
		return ErrBadSignature
	}
	return nil
}

// Converter is a conversion client for the document server in force, or
// ErrNotConfigured.
func (s *Service) Converter(ctx context.Context) (*Converter, error) {
	u, secret := s.settings(ctx)
	if u == "" || secret == "" {
		return nil, ErrNotConfigured
	}
	return &Converter{DocumentServerURL: u, Secret: secret}, nil
}

// ── what the door answered a purpose's fetch ────────────────────────────

// PurposeOutcome is what filex answered one purpose fetch.
type PurposeOutcome struct {
	At     time.Time
	Status int
	Code   string
}

type purposeKey struct {
	node    int64
	purpose string
}

type purposeLog struct {
	mu      sync.Mutex
	entries map[purposeKey]PurposeOutcome
}

// purposeLogSize bounds the record, like the editor's (fetchLogSize).
const purposeLogSize = 512

func (s *Service) purposes() *purposeLog {
	s.purposeOnce.Do(func() { s.purposeLogV = &purposeLog{entries: map[purposeKey]PurposeOutcome{}} })
	return s.purposeLogV
}

// NotePurposeFetch records what the fetch endpoint answered a SIGNED purpose
// fetch (an unsigned one is refused before it is anybody's). The oldest
// entry goes when the record is full.
func (s *Service) NotePurposeFetch(nodeID int64, purpose string, status int, code string) {
	if s == nil || nodeID <= 0 || !ValidPurpose(purpose) {
		return
	}
	l := s.purposes()
	l.mu.Lock()
	defer l.mu.Unlock()
	k := purposeKey{nodeID, purpose}
	if _, ok := l.entries[k]; !ok && len(l.entries) >= purposeLogSize {
		var oldest purposeKey
		var at time.Time
		first := true
		for kk, e := range l.entries {
			if first || e.At.Before(at) {
				oldest, at, first = kk, e.At, false
			}
		}
		delete(l.entries, oldest)
	}
	l.entries[k] = PurposeOutcome{At: time.Now(), Status: status, Code: code}
}

// LastPurposeFetch is what filex last answered the document server's fetch of
// node for purpose (false: it never asked, as far as this process knows).
func (s *Service) LastPurposeFetch(nodeID int64, purpose string) (PurposeOutcome, bool) {
	if s == nil {
		return PurposeOutcome{}, false
	}
	l := s.purposes()
	l.mu.Lock()
	defer l.mu.Unlock()
	o, ok := l.entries[purposeKey{nodeID, purpose}]
	return o, ok
}

// ── a thumbnail of an office document ───────────────────────────────────

// ThumbSize is the box a thumbnail's page is drawn into, in pixels: the
// pipeline's own JPEG is at most this large on either side, and asking for
// more would only be scaled away.
const ThumbSize = 320

// thumbParams names what a thumbnail asks the document server for. It is part
// of the conversion key: change it whenever the size, the type or the layout
// below change, or the document server answers with the pictures it cached
// for the old parameters (see convert.go, the key).
const thumbParams = "thumb-v1:png:320x320:aspect1:sheet-120mm-fit1-grid-2mm"

// sheetLayout is how a spreadsheet's thumbnail is laid out: as wide as its
// used columns (fitToWidth 1, any height), gridlines on, on a small square page
// with thin margins. Measured on Docs 9.4 (docs/thumbnails.md, Design notes:
// Office through OnlyOffice): without it the page is a default A4 cut after the
// first columns, and the picture is a few unreadable cells in a white margin.
var sheetLayout = SpreadsheetLayout{
	IgnorePrintArea: true,
	FitToWidth:      1,
	FitToHeight:     0,
	GridLines:       true,
	PageSize:        &LayoutSize{Width: "120mm", Height: "120mm"},
	Margins:         &LayoutMargin{Left: "2mm", Right: "2mm", Top: "2mm", Bottom: "2mm"},
}

// ThumbRequest is one document to draw a thumbnail of.
type ThumbRequest struct {
	NodeID    int64
	StorageID int64
	// Name is the file's name; its extension is the type the document server
	// is told.
	Name string
	// ContentSig is the content's fingerprint (what the thumbnail row
	// records): a new version of the file is a new conversion key.
	ContentSig string
	// Attempt counts the tries at this content, from 1: a retry is a new key,
	// so a failure the document server cached is not answered again.
	Attempt int
	// MaxBytes is the largest result accepted (0: 32 MB).
	MaxBytes int64
}

// ClassEncrypted: filex refused to send the document (it is end-to-end
// encrypted), so the document server answered -4.
const ClassEncrypted ErrorClass = "encrypted"

// ThumbKey is the conversion key of a thumbnail (ConvertKey): which filex
// (the address the document server reaches it at), which file, which
// content, which parameters and which attempt.
func (s *Service) ThumbKey(ctx context.Context, r ThumbRequest) string {
	return ConvertKey(PurposeThumb, s.callbackBase(ctx),
		strconv.FormatInt(r.StorageID, 10), strconv.FormatInt(r.NodeID, 10),
		r.ContentSig, thumbParams, strconv.Itoa(r.Attempt))
}

// DrawThumbnail asks the document server for a PNG of the document's first
// page, ThumbSize on its longer side, and downloads it. A failure is a
// *ConvertError (ErrNotConfigured when OnlyOffice is not configured): a -4
// whose download filex itself refused because the file is end-to-end
// encrypted comes back as ClassEncrypted.
func (s *Service) DrawThumbnail(ctx context.Context, r ThumbRequest) ([]byte, error) {
	conv, err := s.Converter(ctx)
	if err != nil {
		return nil, err
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(r.Name)), ".")
	if ext == "" {
		return nil, &ConvertError{Class: ClassCorrupt, What: "type", Err: errors.New("the file has no extension to name its type")}
	}
	fetch, err := s.PurposeFetchURL(ctx, r.NodeID, PurposeThumb)
	if err != nil {
		return nil, err
	}
	req := ConvertRequest{
		FetchURL: fetch, FileType: ext, OutputType: "png", Title: r.Name, Key: s.ThumbKey(ctx, r),
		Thumbnail: &Thumbnail{Aspect: 1, First: true, Width: ThumbSize, Height: ThumbSize},
	}
	if DocumentType(ext) == "cell" || isSheetOnly(ext) {
		layout := sheetLayout
		req.SpreadsheetLayout = &layout
	}
	asked := time.Now()
	res, err := conv.Convert(ctx, req)
	if err != nil {
		if ce, ok := AsConvertError(err); ok && ce.Code == DSDownload {
			if o, seen := s.LastPurposeFetch(r.NodeID, PurposeThumb); seen && !o.At.Before(asked) && o.Code == FetchEncrypted {
				return nil, &ConvertError{Code: DSDownload, Class: ClassEncrypted, What: "encrypted", Err: err}
			}
		}
		return nil, err
	}
	return conv.FetchResult(ctx, res, r.MaxBytes)
}

// isSheetOnly: spreadsheet kinds DocumentType does not list (the editor does
// not open them) that the converter still reads.
func isSheetOnly(ext string) bool { return ext == "numbers" }
