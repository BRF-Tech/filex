package plugintest

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/officecmd"
)

// OfficeConversion is one file the fake office engine was asked to convert
// (Host.OfficeFn): what the real host hands the document server.
type OfficeConversion struct {
	// Name is the input's name in the run directory ("in.docx"); From its
	// format, To the conversion API's output type ("pdf", "pdfa", "csv").
	Name string
	From string
	To   string
	// Delimiter and CodePage are the CSV knobs read from LibreOffice's CSV
	// options (0 = not said).
	Delimiter int
	CodePage  int
	// Data is the input's bytes.
	Data []byte
}

// OfficeRefusal is the document server refusing a conversion, with its
// error code: -7 (it does not make that format from this one), -3 (it could
// not read the file), -5 (password-protected).
type OfficeRefusal struct {
	Code int
}

func (e *OfficeRefusal) Error() string {
	return fmt.Sprintf("document server error %d", e.Code)
}

// officeRunLocked is the fake office engine: every input of the command line
// through OfficeFn (or the default), the result named the way soffice named
// it. A refusal is a failed run - exit 1, the reason in stderr_tail - the
// shape the real host answers.
func (h *Host) officeRunLocked(req pluginkit.EngineRequest, cmd *officecmd.Command) (*pluginkit.EngineResult, error) {
	res := &pluginkit.EngineResult{DurationMS: 1}
	var failures []string
	for _, in := range cmd.Inputs {
		ref, ok := req.Inputs[in]
		if !ok {
			res.Exit = 1
			failures = append(failures, "source file could not be loaded: "+in+" is not one of the inputs")
			continue
		}
		from := strings.ToLower(strings.TrimPrefix(filepath.Ext(in), "."))
		c := OfficeConversion{Name: in, From: from, To: cmd.OutputType(), Delimiter: cmd.Delimiter,
			CodePage: cmd.CodePage, Data: append([]byte(nil), h.files[ref].data...)}
		var (
			out []byte
			err error
		)
		if h.OfficeFn != nil {
			out, err = h.OfficeFn(c)
		} else {
			out = append([]byte("office:"+from+"->"+c.To+":"), c.Data...)
		}
		if err != nil {
			res.Exit = 1
			var r *OfficeRefusal
			if errors.As(err, &r) {
				failures = append(failures, fmt.Sprintf("converting %s to %s failed (error %d)", in, cmd.To, r.Code))
			} else {
				failures = append(failures, "converting "+in+" to "+cmd.To+" failed: "+err.Error())
			}
			continue
		}
		name := officecmd.OutputName(in, cmd.To)
		if len(req.Outputs) > 0 && !contains(req.Outputs, name) {
			continue
		}
		res.Outputs = append(res.Outputs, h.artefactLocked(name, out))
	}
	res.StderrTail = strings.Join(failures, "\n")
	return res, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
