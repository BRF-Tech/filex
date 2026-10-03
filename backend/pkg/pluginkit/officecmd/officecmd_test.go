package officecmd

import (
	"strings"
	"testing"
)

// Every command line the Convert app sends (filex-convert internal/engines
// sofficeFilter, up to 0.1.x) reads as the conversion it meant.
func TestParse_TheConvertAppsCommandLines(t *testing.T) {
	cases := []struct {
		spec      string
		to        string
		pdfa      bool
		delimiter int
		codePage  int
	}{
		{"pdf:writer_pdf_Export", "pdf", false, 0, 0},
		{"pdf:calc_pdf_Export", "pdf", false, 0, 0},
		{`pdf:writer_pdf_Export:{"SelectPdfVersion":{"type":"long","value":"2"}}`, "pdf", true, 0, 0},
		{`pdf:impress_pdf_Export:{"SelectPdfVersion":{"type":"long","value":"1"}}`, "pdf", true, 0, 0},
		{`pdf:writer_pdf_Export:{"SelectPdfVersion":{"type":"long","value":"17"}}`, "pdf", false, 0, 0},
		{"pdf", "pdf", false, 0, 0},
		{"docx:MS Word 2007 XML", "docx", false, 0, 0},
		{"odt", "odt", false, 0, 0},
		{"rtf:Rich Text Format", "rtf", false, 0, 0},
		{"txt:Text (encoded):UTF8", "txt", false, 0, 0},
		{"html:HTML (StarWriter)", "html", false, 0, 0},
		{"epub:EPUB", "epub", false, 0, 0},
		{"xlsx:Calc MS Excel 2007 XML", "xlsx", false, 0, 0},
		{"csv:Text - txt - csv (StarCalc):44,34,76,1,,0,false,true,false,false,false,-1", "csv", false, 4, 65001},
		{"csv:Text - txt - csv (StarCalc):59,34,76", "csv", false, 2, 65001},
		{"csv:Text - txt - csv (StarCalc):9,34,0", "csv", false, 1, 0},
		{"PPTX:Impress MS PowerPoint 2007 XML", "pptx", false, 0, 0},
	}
	for _, c := range cases {
		cmd, err := Parse([]string{"--convert-to", c.spec, "in.docx"})
		if err != nil {
			t.Fatalf("%q: %v", c.spec, err)
		}
		if cmd.To != c.to || cmd.PDFA != c.pdfa || cmd.Delimiter != c.delimiter || cmd.CodePage != c.codePage {
			t.Errorf("%q → %+v, want to=%s pdfa=%v delimiter=%d codePage=%d", c.spec, cmd, c.to, c.pdfa, c.delimiter, c.codePage)
		}
		if len(cmd.Inputs) != 1 || cmd.Inputs[0] != "in.docx" {
			t.Errorf("%q: inputs %v", c.spec, cmd.Inputs)
		}
	}
}

// The switches soffice needed for a desktop session are accepted and mean
// nothing; `=` spellings and several inputs read too.
func TestParse_SofficeSwitchesAndForms(t *testing.T) {
	cmd, err := Parse([]string{"--headless", "-norestore", "--nologo", "--nofirststartwizard", "-env:Foo=bar",
		"--convert-to=pdf", "--outdir", ".", "a.docx", "b.odt"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.To != "pdf" || len(cmd.Inputs) != 2 || cmd.Inputs[0] != "a.docx" || cmd.Inputs[1] != "b.odt" {
		t.Fatalf("%+v", cmd)
	}
	if cmd.OutputType() != "pdf" {
		t.Errorf("output type %q", cmd.OutputType())
	}
	pdfa, _ := Parse([]string{"--convert-to", `pdf:x:{"SelectPdfVersion":{"type":"long","value":"3"}}`, "a.docx"})
	if pdfa.OutputType() != "pdfa" {
		t.Errorf("PDF/A asked: output type %q", pdfa.OutputType())
	}
}

// What the office engine cannot do is refused with a sentence that says so.
func TestParse_Refusals(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--cat", "a.docx"}, "--cat is not supported"},
		{[]string{"--print-to-file", "a.docx"}, "is not supported"},
		{[]string{"--convert-to", "pdf", "--infilter=x", "a.docx"}, "is not supported"},
		{[]string{"a.docx"}, "--convert-to <format> is required"},
		{[]string{"--convert-to", "pdf"}, "name the file to convert"},
		{[]string{"--convert-to"}, "needs a format"},
		{[]string{"--convert-to", "pdf", "--outdir", "out", "a.docx"}, `may only be "."`},
		{[]string{"--convert-to", "p d f", "a.docx"}, "bad target format"},
		{[]string{"--headless=1", "--convert-to", "pdf", "a.docx"}, "is not supported"},
	} {
		_, err := Parse(c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
}

func TestOutputName(t *testing.T) {
	for in, want := range map[string]string{"in.docx": "in.pdf", "Quarterly Report.xlsx": "Quarterly Report.pdf", "noext": "noext.pdf", "a.b.pptx": "a.b.pdf"} {
		if got := OutputName(in, "pdf"); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}

func TestIsEngine(t *testing.T) {
	if !IsEngine("office") || !IsEngine("libreoffice") || IsEngine("ffmpeg") || IsEngine("soffice") {
		t.Fatal("IsEngine")
	}
}
