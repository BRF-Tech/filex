package wasmplugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/officecmd"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── The office engine ──────────────────────────────────────────────────
//
// Since 0.50 office documents are converted by the OnlyOffice Document
// Server filex is connected to, through its conversion API. LibreOffice left
// the images, and a soffice installed next to a bare binary is not run either:
// one office engine, the same on every install.
//
// An app reaches it as `engines:office`, or as `engines:libreoffice`, the
// name it had until 0.50 (enginebin.Canonical). Both take the request an app
// already sent to LibreOffice - `--convert-to <format>[:<filter>[:<options>]]
// <file>` - so the Convert app and any third-party app built for LibreOffice
// run unchanged: the host reads the target format out of it, hands the file
// to the document server, and puts the result in the run directory under the
// name soffice would have given it (`in.docx` → `in.pdf`).
//
// What the document server cannot do is said, not guessed at: a conversion it
// refuses comes back as a failed run (exit 1, the reason in stderr_tail),
// exactly as a failed soffice did, and an office engine with no document
// server configured is `unavailable` with a sentence that says what to
// connect.

// OfficeConverter is the office engine's back end: the connected document
// server (wired by the server from internal/onlyoffice; a fake in tests).
type OfficeConverter interface {
	// Ready reports whether a document server is configured right now. It is
	// read on every availability question, so it must be cheap (no network).
	Ready(ctx context.Context) bool
	// Convert turns one file into another format and writes the result to
	// req.Dst. A refusal by the document server is an *OfficeError; no server
	// configured is ErrOfficeUnconfigured; a result over req.MaxBytes is
	// ErrOfficeTooLarge. Nothing is left at req.Dst on any error.
	Convert(ctx context.Context, req OfficeRequest) error
}

// OfficeRequest is one conversion.
type OfficeRequest struct {
	// Src is the input on this machine (inside the run directory); Name is
	// its file name, which the document server is told as the title.
	Src  string
	Name string
	// From is the input's format (its extension, lower case, no dot); To is
	// the conversion API's output type: an extension, or "pdfa".
	From string
	To   string
	// Delimiter and CodePage are the conversion API's CSV knobs (0 = not
	// said: the document server's default).
	Delimiter int
	CodePage  int
	// Dst is where the result goes; MaxBytes its ceiling (0 = none).
	Dst      string
	MaxBytes int64
}

// ErrOfficeUnconfigured: no document server is configured.
var ErrOfficeUnconfigured = errors.New("office engine: no document server is configured")

// ErrOfficeTooLarge: the result is over the per-file limit.
var ErrOfficeTooLarge = errors.New("office engine: the result exceeds the per-file limit")

// OfficeError is the document server failing or refusing a conversion. Code
// is the conversion API's error (-1 … -10), 0 when it gave none (the result
// could not be fetched, say); Reason is English, for the log.
type OfficeError struct {
	Code   int
	Reason string
}

func (e *OfficeError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("document server error %d: %s", e.Code, e.Reason)
	}
	return "document server: " + e.Reason
}

// officeUnconfiguredMessage is the host's answer when the office engine is
// asked for and no document server is connected. It names the remedy, which
// is not "install a program": classifyJobError reads it as
// `office_unconfigured`, and the client says it in the person's language.
func officeUnconfiguredMessage(engine string) string {
	return "engine " + engine + " is not configured on this host: office documents are converted by ONLYOFFICE Document Server, and none is connected (an administrator connects one under External services)"
}

var officeUnconfiguredRe = regexp.MustCompile(`engine ([A-Za-z0-9_.-]+) is not configured on this host`)

// parseOfficeArgs reads a soffice command line (officecmd, the one reader
// the test host shares). It is judged before the engine's availability, like
// every other argument rule: an app that sends what the office engine cannot
// do learns it on every host.
func parseOfficeArgs(args []string) (*officecmd.Command, error) {
	cmd, err := officecmd.Parse(args)
	if err != nil {
		return nil, hostErr(wire.ErrInvalid, err.Error())
	}
	for _, in := range cmd.Inputs {
		if safeName(in) != in {
			return nil, hostErr(wire.ErrInvalid, "office engine: bad input name: "+clip(in, 40))
		}
	}
	return cmd, nil
}

// runOffice converts every input of job through the document server. A
// refusal is a failed run (exit 1, the reasons in stderr) - the shape a
// failed soffice had, which every app already handles; only "no document
// server", the job's own clock and an oversized result are host errors.
func (e *engineSet) runOffice(ctx context.Context, s *Scope, engine, runDir string, job *officecmd.Command, staged map[string]bool, res *engineResult) error {
	var failures []string
	for _, in := range job.Inputs {
		if !staged[in] {
			res.Exit = 1
			failures = append(failures, "source file could not be loaded: "+in+" is not one of the inputs")
			continue
		}
		from := strings.ToLower(strings.TrimPrefix(filepath.Ext(in), "."))
		if from == "" {
			res.Exit = 1
			failures = append(failures, in+": the file has no extension, so its format is unknown")
			continue
		}
		out := officecmd.OutputName(in, job.To)
		if out == in {
			res.Exit = 1
			failures = append(failures, in+" is already "+job.To)
			continue
		}
		err := e.office.Convert(ctx, OfficeRequest{
			Src: filepath.Join(runDir, in), Name: in, From: from, To: job.OutputType(),
			Delimiter: job.Delimiter, CodePage: job.CodePage,
			Dst: filepath.Join(runDir, out), MaxBytes: s.reg.opts.MaxOutputBytes,
		})
		if err == nil {
			continue
		}
		_ = os.Remove(filepath.Join(runDir, out))
		var oe *OfficeError
		switch {
		case errors.Is(err, ErrOfficeUnconfigured):
			return hostErr(wire.ErrUnavailable, officeUnconfiguredMessage(engine))
		case ctx.Err() != nil:
			return hostErr(wire.ErrTimeout, engine+" exceeded its time budget")
		case errors.Is(err, ErrOfficeTooLarge):
			return hostErr(wire.ErrTooLarge, out+" exceeds the per-file limit")
		case errors.As(err, &oe) && oe.Code == -2:
			return hostErr(wire.ErrTimeout, "the document server ran out of time converting "+in)
		case errors.As(err, &oe) && oe.Code == -10:
			return hostErr(wire.ErrTooLarge, in+" is larger than the document server converts")
		}
		res.Exit = 1
		failures = append(failures, officeFailure(in, from, job.To, err))
	}
	res.StderrTail = strings.Join(failures, "\n")
	if len(res.StderrTail) > engineTailBytes {
		res.StderrTail = res.StderrTail[len(res.StderrTail)-engineTailBytes:]
	}
	return nil
}

// officeFailure is one refused conversion, for the app's log (English, like
// any engine's stderr).
func officeFailure(in, from, to string, err error) string {
	var oe *OfficeError
	if errors.As(err, &oe) {
		why := ""
		switch oe.Code {
		case -7:
			why = "ONLYOFFICE does not convert " + from + " to " + to
		case -3:
			why = "ONLYOFFICE could not read it (damaged, or content it does not support)"
		case -5:
			why = "it is password-protected"
		case -4:
			why = "the document server could not download it from filex (check the callback address under External services)"
		case -8:
			why = "the document server refused filex's token (the JWT secrets differ)"
		case -9:
			why = "the document server could not tell the CSV's separator"
		default:
			why = oe.Reason
		}
		if oe.Code != 0 {
			return fmt.Sprintf("converting %s to %s failed: %s (error %d)", in, to, why, oe.Code)
		}
		return "converting " + in + " to " + to + " failed: " + why
	}
	return "converting " + in + " to " + to + " failed: " + err.Error()
}

// isOffice reports whether an engine id names the office engine (or its
// alias).
func isOffice(engine string) bool { return enginebin.Canonical(engine) == enginebin.Office }
