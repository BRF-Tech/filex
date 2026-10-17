package officeengine

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ── The wire between filex and an engine process ───────────────────────
//
// The child's FIRST line on stdout is a Hello. After it, the parent writes one
// Request per line on the child's stdin and reads one Answer per line from its
// stdout, in order: a process takes one job at a time. Closing the child's
// stdin ends it. Nothing else is ever written to the child's stdout (its log
// goes to stderr, x2t's and the layout's own output to buffers).

// ConfigEnv carries the ChildConfig (JSON) to an engine process: the command
// line stays short and carries no path a process listing would show.
const ConfigEnv = "FILEX_OFFICE_ENGINE_CONFIG"

// ChildConfig is what an engine process is started with.
type ChildConfig struct {
	// Engine is the app's engine.zip, EngineSHA256 the hash it must have.
	Engine       string `json:"engine"`
	EngineSHA256 string `json:"engine_sha256"`
	// Package is the app's interface bundle (ui.zip), which holds the
	// editor's sdkjs/ and fonts/ under EditorDir; mounted read-only.
	Package       string `json:"package"`
	PackageSHA256 string `json:"package_sha256"`
	EditorDir     string `json:"editor_dir"`
	// Cache is the compilation cache's root (wazero's, and the layout's
	// bytecode), shared by every engine process of this filex.
	Cache string `json:"cache"`
	// Work is the process's own temporary directory; the parent removes it
	// when the process ends.
	Work string `json:"work"`
	// MaxMB caps every instance's memory (MiB).
	MaxMB int `json:"max_mb"`
	// Prepare: compile, start a worker of each kind once, say Prepared and
	// end - no jobs.
	Prepare bool `json:"prepare,omitempty"`
}

// Hello is an engine process's first line.
type Hello struct {
	// Ready: the modules are compiled, jobs may come.
	Ready bool `json:"ready,omitempty"`
	// Prepared: a Prepare run finished (the process ends after it).
	Prepared bool `json:"prepared,omitempty"`
	// Error: the process could not start (a package that does not hash to
	// its pin, a module that does not compile); it ends after saying so.
	Error     string `json:"error,omitempty"`
	CompileMs int64  `json:"compile_ms,omitempty"`
}

// The operations.
const (
	// OpConvert turns In (From) into To, written at Out.
	OpConvert = "convert"
	// OpThumbnail draws In's first page as a PNG fitted into Size x Size, at
	// Out.
	OpThumbnail = "thumbnail"
)

// Request is one job.
type Request struct {
	ID int64  `json:"id"`
	Op string `json:"op"`
	// In is the input on this machine, From its format (the extension, lower
	// case, no dot).
	In   string `json:"in"`
	From string `json:"from"`
	// To is the conversion API's output type, as the Document Server is asked
	// for it: an extension, or "pdfa".
	To string `json:"to,omitempty"`
	// Out is where the result goes. It must not exist: the child creates it.
	Out string `json:"out"`
	// MaxBytes is the largest result accepted (0: no limit). A larger one is
	// not written and the answer says Over.
	MaxBytes int64 `json:"max_bytes,omitempty"`
	// Delimiter and CodePage are the conversion API's CSV knobs (0: not said):
	// the delimiter as the Document Server numbers it (1 tab, 2 semicolon,
	// 3 colon, 4 comma, 5 space), the code page as Windows does (65001).
	Delimiter int `json:"delimiter,omitempty"`
	CodePage  int `json:"code_page,omitempty"`
	// Size is a thumbnail's box, in pixels.
	Size int `json:"size,omitempty"`
	// Params are the editor's json parameters (a Document Server's
	// m_sJsonParams): a spreadsheet's page layout for its thumbnail.
	Params json.RawMessage `json:"params,omitempty"`
}

// Answer is what came of one Request.
type Answer struct {
	ID int64 `json:"id"`
	OK bool  `json:"ok"`
	// Bytes is the result's size; Pages the pages the layout drew.
	Bytes int64 `json:"bytes,omitempty"`
	Pages int   `json:"pages,omitempty"`
	// Code is a failure as the Document Server's conversion API numbers it
	// (the Code* constants), so a caller says both back ends' failures the
	// same way; What is its short name for a record ("x2t-86", "trap",
	// "memory"); Error the longer English, for a log.
	Code  int    `json:"code,omitempty"`
	What  string `json:"what,omitempty"`
	Error string `json:"error,omitempty"`
	// Over: the result was larger than MaxBytes, and nothing was written.
	Over bool `json:"over,omitempty"`
	// Ms is where the time went (to_bin, layout, from_bin, convert).
	Ms map[string]int64 `json:"ms,omitempty"`
}

// The failures, as the Document Server's conversion API numbers them
// (internal/onlyoffice DS*): the server maps both back ends' failures with
// one table (onlyoffice.ClassOf) and says them with one set of sentences.
const (
	// CodeUnknown: the engine process ended without an answer, or a failure
	// that may pass (asked again later).
	CodeUnknown = -1
	// CodeTimeout: the job's deadline passed (the parent ended the process).
	CodeTimeout = -2
	// CodeCorrupt: the document could not be read or converted (x2t's 80, 84,
	// 86, a trap, a layout that failed). The same bytes fail again.
	CodeCorrupt = -3
	// CodePassword: the document is protected by a password (x2t's 90, 91)
	// or by DRM (85).
	CodePassword = -5
	// CodeParams: this format is not made from that one (x2t's 82, 88).
	CodeParams = -7
	// CodeDetect: what the input is could not be told (x2t's 89, 95).
	CodeDetect = -9
	// CodeTooLarge: the memory cap, or x2t's own size limits (93, 94, 96).
	CodeTooLarge = -10
)

// x2t's return codes (AVS_FILEUTILS_ERROR_CONVERT_*, ONLYOFFICE core
// Common/OfficeFileErrorDescription.h).
const (
	x2tConvert        = 80
	x2tDownload       = 81
	x2tUnknownFormat  = 82
	x2tTimeout        = 83
	x2tReadFile       = 84
	x2tDRMUnsupported = 85
	x2tCorrupted      = 86
	x2tLibreOffice    = 87
	x2tParams         = 88
	x2tNeedParams     = 89
	x2tDRM            = 90
	x2tPassword       = 91
	x2tICU            = 92
	x2tLimits         = 93
	x2tRowLimits      = 94
	x2tDetect         = 95
	x2tCellLimits     = 96
)

// codeOfExit maps x2t's return code to the conversion API's error, as the
// Document Server does for its own x2t.
func codeOfExit(exit uint32) int {
	switch exit {
	case x2tDRM, x2tPassword, x2tDRMUnsupported:
		return CodePassword
	case x2tUnknownFormat, x2tParams:
		return CodeParams
	case x2tNeedParams, x2tDetect:
		return CodeDetect
	case x2tLimits, x2tRowLimits, x2tCellLimits:
		return CodeTooLarge
	case x2tTimeout:
		return CodeTimeout
	case x2tDownload:
		return CodeUnknown
	}
	// 80, 84, 86, 87, 92 and anything x2t did not name: the bytes are what
	// they are, and asking again gives the same answer.
	return CodeCorrupt
}

// Error is a job the engine did not do.
type Error struct {
	// Code is the conversion API's number (Code*), What its short name for a
	// record ("x2t-86", "trap", "memory", "timeout", "crash").
	Code int
	What string
	// Over: the result was larger than the request's MaxBytes.
	Over bool
	Err  error
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("office engine: %s (%d)", e.What, e.Code)
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// AsError returns err as an *Error.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// Why a job was not even started.
var (
	// ErrNoEngine: no running app brings an office engine.
	ErrNoEngine = errors.New("office engine: no app brings one")
	// ErrPreparing: the engine is being compiled (Status says since when).
	ErrPreparing = errors.New("office engine: being prepared")
	// ErrFailed: the engine could not be prepared (Status says why).
	ErrFailed = errors.New("office engine: could not be prepared")
	// ErrClosed: the runner was closed (filex is stopping).
	ErrClosed = errors.New("office engine: closed")
)

// answerError is the Error an answer carries.
func answerError(a *Answer) error {
	if a.OK {
		return nil
	}
	code := a.Code
	if code == 0 {
		code = CodeUnknown
	}
	what := a.What
	if what == "" {
		what = "failed"
	}
	var err error
	if a.Error != "" {
		err = errors.New(a.Error)
	}
	return &Error{Code: code, What: what, Over: a.Over, Err: err}
}
