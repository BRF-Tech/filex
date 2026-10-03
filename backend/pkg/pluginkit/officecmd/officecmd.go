// Package officecmd reads the command line an app hands the office engine.
//
// Until filex 0.50 the office engine was LibreOffice, and an app ran it the
// way one runs soffice: `--convert-to <format>[:<filter>[:<options>]] <file>`.
// Since 0.50 the office engine is the OnlyOffice Document Server filex is
// connected to, and filex reads that same command line as a conversion: the
// target format, PDF/A when LibreOffice's PDF options ask for it, the CSV
// separator and character set when its CSV options name them. An app built
// for LibreOffice runs unchanged; a new app writes the same line.
//
// The host (internal/wasmplugin) and the test host (plugintest) both read it
// here, so an app's tests refuse exactly what filex refuses.
package officecmd

import (
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Command is a command line read as a conversion.
type Command struct {
	// To is the target format, an extension ("pdf", "docx", "csv").
	To string
	// PDFA: LibreOffice's PDF options asked for a PDF/A version (1, 2 or 3).
	// The document server makes one kind of PDF/A (PDF/A-2a on 9.4).
	PDFA bool
	// Delimiter and CodePage are the conversion API's CSV knobs, read from
	// LibreOffice's CSV options (0 = not said).
	Delimiter int
	CodePage  int
	// Inputs are the files to convert, by their names in the run directory.
	Inputs []string
}

// OutputType is what the document server is asked to make: To, or "pdfa".
func (c *Command) OutputType() string {
	if c.To == "pdf" && c.PDFA {
		return "pdfa"
	}
	return c.To
}

// OutputName is the name a result gets: the input's stem and the target
// extension, the name soffice gave it (`in.docx` → `in.pdf`).
func OutputName(in, to string) string {
	return strings.TrimSuffix(in, filepath.Ext(in)) + "." + to
}

// ignored are soffice switches that set up a desktop session and mean
// nothing to a conversion; they are accepted so a LibreOffice command line
// keeps working.
var ignored = map[string]bool{
	"headless": true, "invisible": true, "norestore": true, "nologo": true, "nofirststartwizard": true,
	"nodefault": true, "nolockcheck": true, "minimized": true, "nocrashreport": true,
}

var formatRe = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// Parse reads a command line. The error says what is wrong in a sentence an
// app's author can act on.
func Parse(args []string) (*Command, error) {
	c := &Command{}
	spec := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			if strings.TrimSpace(a) == "" {
				return nil, errors.New("office engine: an empty argument is not a file name")
			}
			c.Inputs = append(c.Inputs, a)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch {
		case name == "convert-to":
			if !hasValue {
				if i+1 >= len(args) {
					return nil, errors.New("office engine: --convert-to needs a format")
				}
				i++
				value = args[i]
			}
			spec = value
		case name == "outdir":
			if !hasValue {
				if i+1 >= len(args) {
					return nil, errors.New("office engine: --outdir needs a directory")
				}
				i++
				value = args[i]
			}
			if value != "." {
				return nil, errors.New(`office engine: results are written to the run directory; --outdir may only be "."`)
			}
		case ignored[name] && !hasValue:
		case strings.HasPrefix(a, "-env:"):
		default:
			return nil, errors.New("office engine: it converts documents through ONLYOFFICE and takes `--convert-to <format> <file>`; " + clip(a, 40) + " is not supported")
		}
	}
	if spec == "" {
		return nil, errors.New("office engine: --convert-to <format> is required")
	}
	if len(c.Inputs) == 0 {
		return nil, errors.New("office engine: name the file to convert")
	}
	parts := strings.SplitN(spec, ":", 3)
	c.To = strings.ToLower(strings.TrimSpace(parts[0]))
	if !formatRe.MatchString(c.To) {
		return nil, errors.New("office engine: bad target format: " + clip(parts[0], 20))
	}
	options := ""
	if len(parts) == 3 {
		options = parts[2]
	}
	switch c.To {
	case "pdf":
		c.PDFA = asksPDFA(options)
	case "csv":
		c.Delimiter, c.CodePage = csvOptions(options)
	}
	return c, nil
}

var (
	pdfVersionJSONRe = regexp.MustCompile(`SelectPdfVersion"?\s*:\s*\{[^}]*"value"\s*:\s*"?([0-9]+)`)
	pdfVersionKVRe   = regexp.MustCompile(`SelectPdfVersion\s*=\s*([0-9]+)`)
)

// asksPDFA reads LibreOffice's PDF export option: SelectPdfVersion 1, 2 or 3
// is PDF/A-1b, -2b or -3b (15, 16 and 17 are plain PDF versions).
func asksPDFA(options string) bool {
	for _, re := range []*regexp.Regexp{pdfVersionJSONRe, pdfVersionKVRe} {
		if m := re.FindStringSubmatch(options); m != nil {
			v, _ := strconv.Atoi(m[1])
			return v >= 1 && v <= 3
		}
	}
	return false
}

// csvOptions reads LibreOffice's CSV filter options ("44,34,76,1,...": the
// field separator's character code, the text delimiter's, the character set)
// into the conversion API's delimiter and code page. What the API cannot say
// is left to its default (a comma, UTF-8).
func csvOptions(options string) (delimiter, codePage int) {
	if options == "" {
		return 0, 0
	}
	tok := strings.Split(options, ",")
	switch strings.TrimSpace(tok[0]) {
	case "9":
		delimiter = 1
	case "59":
		delimiter = 2
	case "58":
		delimiter = 3
	case "44":
		delimiter = 4
	case "32":
		delimiter = 5
	}
	if len(tok) > 2 && strings.TrimSpace(tok[2]) == "76" {
		codePage = 65001
	}
	return delimiter, codePage
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// The office engine's names: Engine since 0.50, LegacyEngine (its name while
// it was LibreOffice) still accepted for the same engine - the permission
// `engines:libreoffice` grants `engines:office` and the other way round.
const (
	Engine       = "office"
	LegacyEngine = "libreoffice"
)

// IsEngine reports whether name is the office engine under either name.
func IsEngine(name string) bool { return name == Engine || name == LegacyEngine }
