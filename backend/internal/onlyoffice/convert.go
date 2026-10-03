package onlyoffice

// The document server's conversion service, for every caller (filex 0.50).
//
// Three things in filex hand a file to OnlyOffice's ConvertService and wait
// for what it makes of it: the reverse-path probe (reverse.go, "can the
// document server reach filex?"), the thumbnail of an office document (a PNG
// of its first page, docs/thumbnails.md → Office through OnlyOffice), and the
// office engine apps convert with (docx to pdf and the like). They share this
// one client, so a request is signed, polled, classified and fetched back the
// same way whoever asked.
//
// # What a request looks like
//
// One POST to ConvertService.ashx with a JSON body: where to download the
// source (`url`, filex's own signed fetch address, PurposeFetchURL), its type
// (`filetype`), what to make (`outputtype`), and a `key`. With a JWT secret the
// body carries `token` (the payload, signed) and the Authorization header the
// same payload wrapped in {"payload": ...}. ⚠ Everything the document server
// is asked for goes INSIDE the signed payload: with tokenRequiredParams on
// (the default since Docs 7.1) a `thumbnail` or `spreadsheetLayout` outside
// the token is ignored and the answer is a full-size page.
//
// # Asynchronous, polled
//
// `async: true`, and the same request posted again until it answers
// `endConvert` or an error. ⚠ Never synchronous: a synchronous request is held
// INSIDE the document server until the conversion ends, up to its own 30
// minute limit, whatever filex's context says - a stuck x2t would keep a
// connection and a slot of filex's open for half an hour. Polling ends when
// the caller's context does.
//
// # The key, and the document server's cache
//
// The document server keeps every result under `conv_<key>_<outputtype>` for
// about a day, and the thumbnail and layout parameters are NOT part of that
// name: the same key asked for a 640 pixel picture answers yesterday's 320
// pixel one (measured on Docs 9.4, 53 ms). A failure is cached too (-3 and the
// other errors it calls Err), only a download failure, a size limit, a
// dead-lettered task and a password error are not. So a key names the file's
// content, the parameters and the attempt (ConvertKey): a new picture size, a
// new version of the file or a retry is a new key, and a retry never gets the
// failure it is retrying back from the cache.
//
// # Where the result comes from
//
// The answer names a URL (`fileUrl`) to download the result from. It is only
// ever downloaded from the document server's own origin, the address filex is
// configured with (sameOrigin): a URL anywhere else is refused, never fetched,
// because filex would otherwise fetch whatever address the answer named. The
// download is bounded (FetchResult's max).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// convertPath is the document server's conversion endpoint. It is the only
// documented way to hand OnlyOffice a URL and have it fetch it. Docs 7 and
// later also answer /converter; the old name works on every version.
const convertPath = "/ConvertService.ashx"

// Conversion error codes the document server answers (`error`). The names are
// filex's; the numbers are the document server's (Common/sources/utils.js,
// mapAscServerErrorToOldError).
const (
	DSUnknown       = -1  // unknown error, a storage or queue failure
	DSTimeout       = -2  // the conversion took too long, or the task was dead-lettered
	DSConvert       = -3  // the converter could not read or convert the file
	DSDownload      = -4  // the document server could not download the source
	DSPassword      = -5  // the file is protected by a password (or DRM)
	DSResultDB      = -6  // the conversion result database could not be reached
	DSParams        = -7  // the request's input is wrong for this file
	DSToken         = -8  // the token is missing or wrong
	DSDetect        = -9  // the output format could not be determined
	DSLimit         = -10 // a size limit: the download, or the file's content
	dsCodeUndefined = 0
)

// ErrorClass is what a conversion failure means for whoever asked: whether
// asking again can help, and what to tell the person looking at the file.
type ErrorClass string

const (
	// ClassTransient: asking again later may work - the network, the
	// document server's own trouble (-1, -2, -6), a token it refused (-8,
	// a secret being changed), a download that did not reach filex (-4), a
	// timeout, an answer filex could not read, a result it could not fetch.
	ClassTransient ErrorClass = "transient"
	// ClassCorrupt: the file cannot be converted (-3, -7, -9). The same
	// bytes will fail again.
	ClassCorrupt ErrorClass = "corrupt"
	// ClassPassword: the file is protected by a password (-5).
	ClassPassword ErrorClass = "password"
	// ClassTooLarge: over the document server's own size limits (-10).
	ClassTooLarge ErrorClass = "too_large"
)

// ClassOf classifies a document server error code.
func ClassOf(code int) ErrorClass {
	switch code {
	case DSConvert, DSParams, DSDetect:
		return ClassCorrupt
	case DSPassword:
		return ClassPassword
	case DSLimit:
		return ClassTooLarge
	}
	return ClassTransient
}

// ConvertError is why a conversion did not give a result.
type ConvertError struct {
	// Code is the document server's error number (-1 to -10), 0 when the
	// failure was not its answer (the network, an HTTP error, a timeout).
	Code int
	// Class says what it means (ClassOf, or transient for everything that is
	// not the document server's answer).
	Class ErrorClass
	// HTTPStatus is the status of an answer that was not a conversion answer.
	HTTPStatus int
	// What is the short name a record keeps: "ds-3", "http-502", "net",
	// "timeout", "answer", "origin", "result". Never a URL, never a secret.
	What string
	Err  error
}

func (e *ConvertError) Error() string {
	msg := "onlyoffice convert: " + e.What
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *ConvertError) Unwrap() error { return e.Err }

// AsConvertError returns err as a *ConvertError, and false for any other
// error.
func AsConvertError(err error) (*ConvertError, bool) {
	var ce *ConvertError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}

func dsError(code int) *ConvertError {
	return &ConvertError{Code: code, Class: ClassOf(code), What: "ds" + strconv.Itoa(code)}
}

func transient(what string, err error) *ConvertError {
	return &ConvertError{Class: ClassTransient, What: what, Err: err}
}

// Thumbnail asks for a picture of the first page instead of a document
// (outputtype png, jpg, bmp or gif). Aspect 1 keeps the page's proportions
// inside Width x Height; First is the first page only.
type Thumbnail struct {
	Aspect int  `json:"aspect"`
	First  bool `json:"first"`
	Width  int  `json:"width,omitempty"`
	Height int  `json:"height,omitempty"`
}

// SpreadsheetLayout lays a spreadsheet out on a page before it is converted
// to a page format (a PDF, a picture). Without it a sheet is cut where the
// default A4 page ends and a thumbnail shows a corner of the first columns.
type SpreadsheetLayout struct {
	IgnorePrintArea bool          `json:"ignorePrintArea"`
	Orientation     string        `json:"orientation,omitempty"`
	FitToWidth      int           `json:"fitToWidth"`
	FitToHeight     int           `json:"fitToHeight"`
	GridLines       bool          `json:"gridLines"`
	Headings        bool          `json:"headings"`
	PageSize        *LayoutSize   `json:"pageSize,omitempty"`
	Margins         *LayoutMargin `json:"margins,omitempty"`
}

// LayoutSize is a page size, with its unit ("120mm").
type LayoutSize struct {
	Width  string `json:"width"`
	Height string `json:"height"`
}

// LayoutMargin is a page's margins, with their unit ("2mm").
type LayoutMargin struct {
	Left   string `json:"left"`
	Right  string `json:"right"`
	Top    string `json:"top"`
	Bottom string `json:"bottom"`
}

// ConvertRequest is one file to convert.
type ConvertRequest struct {
	// FetchURL is where the document server downloads the source: filex's
	// signed fetch address for a purpose (PurposeFetchURL).
	FetchURL string
	// FileType is the source's type: its extension, lower case, no dot.
	FileType string
	// OutputType is what to make: "png" for a thumbnail, "pdf", "docx", ...
	OutputType string
	// Title is the file's name, for the document server's log and the
	// result's name.
	Title string
	// Key is the conversion's key (ConvertKey). Required.
	Key string
	// Thumbnail and SpreadsheetLayout are optional.
	Thumbnail         *Thumbnail
	SpreadsheetLayout *SpreadsheetLayout
	// CodePage and Delimiter are the conversion API's CSV and text knobs
	// (`codePage`, `delimiter`); zero leaves them out.
	CodePage  int
	Delimiter int
}

func (r ConvertRequest) payload(async bool) map[string]any {
	p := map[string]any{
		"async":      async,
		"filetype":   r.FileType,
		"key":        r.Key,
		"outputtype": r.OutputType,
		"title":      r.Title,
		"url":        r.FetchURL,
	}
	if r.Thumbnail != nil {
		p["thumbnail"] = r.Thumbnail
	}
	if r.SpreadsheetLayout != nil {
		p["spreadsheetLayout"] = r.SpreadsheetLayout
	}
	if r.CodePage != 0 {
		p["codePage"] = r.CodePage
	}
	if r.Delimiter != 0 {
		p["delimiter"] = r.Delimiter
	}
	return p
}

// ConvertResult is a finished conversion: where its result is, and its type.
type ConvertResult struct {
	FileURL  string
	FileType string
}

// convertAnswer is the part of a ConvertService reply filex reads.
type convertAnswer struct {
	Error      int    `json:"error"`
	FileURL    string `json:"fileUrl"`
	FileType   string `json:"fileType"`
	EndConvert bool   `json:"endConvert"`
	Percent    int    `json:"percent"`
}

// Converter talks to one document server.
type Converter struct {
	// DocumentServerURL is the document server's base address, as filex is
	// configured with it. Results are only fetched from its origin.
	DocumentServerURL string
	// Secret signs every request; empty sends them unsigned (only the
	// reverse-path probe's JWT question does).
	Secret string
	// HTTP is the client requests go through (nil: a default one without a
	// timeout of its own; the caller's context bounds every request).
	HTTP *http.Client
	// PollFirst and PollMax are the wait before the first poll and the
	// longest wait between two (zero: 200 ms and 1 s).
	PollFirst, PollMax time.Duration
}

func (c *Converter) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Converter) base() string { return strings.TrimRight(c.DocumentServerURL, "/") }

// post sends one conversion request and reads the answer. answered is false
// when the reply was not a ConvertService JSON answer (an HTML page, a 404 of
// whatever sits at that address).
func (c *Converter) post(ctx context.Context, payload map[string]any) (ans convertAnswer, status int, answered bool, err error) {
	body := map[string]any{}
	for k, v := range payload {
		body[k] = v
	}
	if c.Secret != "" {
		// The document server rejects an unsigned request when it enforces
		// JWT, and it rejects it BEFORE downloading anything.
		if tok, serr := signHS256(payload, c.Secret); serr == nil {
			body["token"] = tok
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return ans, 0, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+convertPath, bytes.NewReader(raw))
	if err != nil {
		return ans, 0, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.Secret != "" {
		if hdr, serr := signHS256(map[string]any{"payload": payload}, c.Secret); serr == nil {
			req.Header.Set("Authorization", "Bearer "+hdr)
		}
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return ans, 0, false, err
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	answered = json.Unmarshal(reply, &ans) == nil
	return ans, resp.StatusCode, answered, nil
}

// Convert asks for one conversion and polls until it ends: the result, or a
// *ConvertError. The caller's context bounds it (a per-file deadline belongs
// there): when it ends first the error is a transient "timeout".
func (c *Converter) Convert(ctx context.Context, r ConvertRequest) (*ConvertResult, error) {
	if c == nil || c.base() == "" {
		return nil, transient("unconfigured", errors.New("no document server address"))
	}
	if r.Key == "" || r.FetchURL == "" || r.FileType == "" || r.OutputType == "" {
		return nil, &ConvertError{Class: ClassTransient, What: "request", Err: errors.New("key, url, filetype and outputtype are required")}
	}
	payload := r.payload(true)
	wait := c.PollFirst
	if wait <= 0 {
		wait = 200 * time.Millisecond
	}
	maxWait := c.PollMax
	if maxWait <= 0 {
		maxWait = time.Second
	}
	for {
		ans, status, answered, err := c.post(ctx, payload)
		switch {
		case err != nil && ctx.Err() != nil:
			return nil, transient("timeout", ctx.Err())
		case err != nil:
			return nil, transient("net", err)
		case status >= 400:
			return nil, &ConvertError{Class: ClassTransient, HTTPStatus: status, What: "http-" + strconv.Itoa(status)}
		case !answered:
			return nil, transient("answer", fmt.Errorf("the reply (HTTP %d) is not a conversion answer", status))
		case ans.Error != dsCodeUndefined:
			return nil, dsError(ans.Error)
		case ans.EndConvert:
			if ans.FileURL == "" {
				return nil, transient("answer", errors.New("the conversion ended without a result address"))
			}
			return &ConvertResult{FileURL: ans.FileURL, FileType: ans.FileType}, nil
		}
		select {
		case <-ctx.Done():
			return nil, transient("timeout", ctx.Err())
		case <-time.After(wait):
		}
		if wait *= 2; wait > maxWait {
			wait = maxWait
		}
	}
}

// ErrForeignOrigin is a result address outside the document server's origin.
var ErrForeignOrigin = errors.New("the result is not on the document server's address")

// ErrResultTooLarge is a result over FetchResult's maxBytes.
var ErrResultTooLarge = errors.New("the result is larger than allowed")

// FetchResult downloads a finished conversion's result, at most maxBytes of
// it, and only from the document server's own origin (see the package
// comment). A result over maxBytes is an error, not a truncated file.
func (c *Converter) FetchResult(ctx context.Context, res *ConvertResult, maxBytes int64) ([]byte, error) {
	if res == nil || res.FileURL == "" {
		return nil, transient("result", errors.New("no result"))
	}
	if !sameOrigin(res.FileURL, c.base()) {
		return nil, transient("origin", ErrForeignOrigin)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, res.FileURL, nil)
	if err != nil {
		return nil, transient("result", err)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, transient("timeout", ctx.Err())
		}
		return nil, transient("result", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, &ConvertError{Class: ClassTransient, HTTPStatus: resp.StatusCode, What: "result",
			Err: fmt.Errorf("the result answered HTTP %d", resp.StatusCode)}
	}
	if maxBytes <= 0 {
		maxBytes = 32 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, transient("result", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, transient("result", fmt.Errorf("%w (%d bytes)", ErrResultTooLarge, maxBytes))
	}
	return body, nil
}

// sameOrigin: raw is on the origin of base (scheme, host and port, the
// default port spelled or not).
func sameOrigin(raw, base string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	b, err := url.Parse(base)
	if err != nil || b.Host == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, b.Scheme) &&
		strings.EqualFold(u.Hostname(), b.Hostname()) &&
		effectivePort(u) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

// ConvertKey is a conversion key made of parts: the same parts, the same key.
// Callers name everything the result depends on - which filex and which file
// (the fetch address's base, the storage, the node), the content (its
// fingerprint), the parameters (a version string the caller bumps when it
// changes what it asks for) and the attempt - because the document server
// answers a key it has seen with what it made then (see the package comment).
//
// The key is "fx" and 40 hex digits: the document server takes 128 of
// [0-9A-Za-z.=_-].
func ConvertKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = io.WriteString(h, strconv.Itoa(len(p)))
		_, _ = io.WriteString(h, ":")
		_, _ = io.WriteString(h, p)
	}
	return "fx" + hex.EncodeToString(h.Sum(nil))[:40]
}
