package plugintest_test

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

func officeHost(perm string) *plugintest.Host {
	m := manifest()
	m.Permissions = []string{"files:read", "files:write", perm}
	h := plugintest.NewHost(m)
	h.EnterJob()
	return h
}

// The test host answers the office engine the way filex 0.50 does: a
// LibreOffice command line converted by a (fake) document server, the result
// under the name soffice gave it, the two names one grant.
//
// Red before 0.50: the fake ran any engine by its exact name, so the default
// answer was `engine:in.pdf` whatever was asked, and an app granted
// `engines:libreoffice` could not use `office`.
func TestOfficeEngine_AnswersALibreOfficeCommandLine(t *testing.T) {
	for _, c := range []struct{ perm, engine string }{
		{"engines:libreoffice", "libreoffice"},
		{"engines:libreoffice", "office"},
		{"engines:office", "libreoffice"},
		{"engines:office", "office"},
	} {
		h := officeHost(c.perm)
		h.InstallEngine("office")
		if !h.EngineAvailable(c.engine) {
			t.Fatalf("%+v: the office engine is connected and granted", c)
		}
		if got := h.InstalledEngines(); !got["office"] || !got["libreoffice"] {
			t.Fatalf("%+v: a call carries both names: %v", c, got)
		}
		ref := h.AddInput(plugintest.File{Name: "in.docx", Data: []byte("DOCX")})
		var seen plugintest.OfficeConversion
		h.OfficeFn = func(conv plugintest.OfficeConversion) ([]byte, error) {
			seen = conv
			return []byte("%PDF-1.7"), nil
		}
		res, err := h.EngineRun(pluginkit.EngineRequest{
			Engine: c.engine,
			Args:   []string{"--convert-to", `pdf:writer_pdf_Export:{"SelectPdfVersion":{"type":"long","value":"2"}}`, "in.docx"},
			Inputs: map[string]string{"in.docx": ref.Ref}, Outputs: []string{"in.pdf"},
		})
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if res.Exit != 0 || len(res.Outputs) != 1 || res.Outputs[0].Name != "in.pdf" {
			t.Fatalf("%+v: %+v", c, res)
		}
		if b, _ := h.ReadInput(res.Outputs[0].Ref); string(b) != "%PDF-1.7" {
			t.Fatalf("%+v: output %q", c, b)
		}
		if seen.From != "docx" || seen.To != "pdfa" || string(seen.Data) != "DOCX" {
			t.Fatalf("%+v: the document server was asked %+v", c, seen)
		}
	}
}

// Not connected, refused, unreadable: the three answers an app must handle,
// with the real host's codes and sentences.
func TestOfficeEngine_TheAnswersAnAppMustHandle(t *testing.T) {
	h := officeHost("engines:libreoffice")
	ref := h.AddInput(plugintest.File{Name: "in.xlsx", Data: []byte("XLSX")})
	run := func(args ...string) (*pluginkit.EngineResult, error) {
		return h.EngineRun(pluginkit.EngineRequest{Engine: "libreoffice", Args: args, Inputs: map[string]string{"in.xlsx": ref.Ref}})
	}

	if h.EngineAvailable("libreoffice") {
		t.Fatal("no document server is connected")
	}
	_, err := run("--convert-to", "pdf", "in.xlsx")
	if !plugintest.IsCode(err, wire.ErrUnavailable) || !strings.Contains(err.Error(), "not configured") || !strings.Contains(err.Error(), "ONLYOFFICE") {
		t.Fatalf("not connected: %v", err)
	}
	if _, err := run("--cat", "in.xlsx"); !plugintest.IsCode(err, wire.ErrInvalid) {
		t.Fatalf("a switch the office engine cannot do is invalid on every host, got %v", err)
	}

	h.InstallEngine("libreoffice")
	h.OfficeFn = func(conv plugintest.OfficeConversion) ([]byte, error) {
		if conv.To == "html" {
			return nil, &plugintest.OfficeRefusal{Code: -7}
		}
		return []byte("ok"), nil
	}
	res, err := run("--convert-to", "html:HTML (StarCalc)", "in.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != 1 || len(res.Outputs) != 0 || !strings.Contains(res.StderrTail, "error -7") {
		t.Fatalf("a refusal is a failed run: %+v", res)
	}
	res, err = run("--headless", "--convert-to", "csv:Text - txt - csv (StarCalc):44,34,76,1", "in.xlsx")
	if err != nil || res.Exit != 0 || len(res.Outputs) != 1 || res.Outputs[0].Name != "in.csv" {
		t.Fatalf("csv: %+v %v", res, err)
	}
}

func TestOfficeEngine_IsAKnownEngine(t *testing.T) {
	if !plugintest.KnownEngines["office"] || !plugintest.KnownEngines["libreoffice"] {
		t.Fatal("both names of the office engine are known")
	}
}
