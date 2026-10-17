package officeengine

import (
	"strconv"
	"strings"
)

// workDir is where a job's files are in x2t's file system: the child mounts
// the job's own directory there, so two jobs never see each other's files.
const workDir = "/w"

// x2tJob is one run of x2t: its conversion order (params.xml) in the words a
// Document Server writes it. Every path in it is one this package chose
// under workDir, never a document's name.
type x2tJob struct {
	// from and to are file names inside workDir.
	from, to string
	// formatTo is x2t's m_nFormatTo (0: by to's extension).
	formatTo int
	// noBase64: the editor's format is written as bytes, not base64
	// (m_bIsNoBase64; set when the target is the editor's format).
	noBase64 bool
	// fonts: the fonts the pages were drawn with are in workDir/fonts/.
	fonts bool
	// json is the editor's json parameters (m_sJsonParams).
	json string
	// text: a txt's or csv's encoding and delimiter.
	text *textOptions
	// thumb makes an image target a picture of the first page
	// (m_oThumbnail).
	thumb *thumbOptions
}

// thumbOptions is x2t's m_oThumbnail: format 3 is JPEG and 4 PNG; aspect 1
// keeps the page's proportions inside width x height, 2 draws the page at
// its own size (the Document Server's default for an image conversion).
type thumbOptions struct {
	format, aspect, width, height int
	first                         bool
}

var xmlEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// params is the conversion order.
func (j x2tJob) params() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	b.WriteString(`<TaskQueueDataConvert xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">`)
	el := func(name, value string) {
		b.WriteString("<" + name + ">" + xmlEscape.Replace(value) + "</" + name + ">")
	}
	el("m_sFileFrom", workDir+"/"+j.from)
	el("m_sThemeDir", workDir+"/themes")
	el("m_sFileTo", workDir+"/"+j.to)
	el("m_sTempDir", workDir+"/tmp")
	el("m_bIsNoBase64", strconv.FormatBool(j.noBase64))
	if j.formatTo > 0 {
		el("m_nFormatTo", strconv.Itoa(j.formatTo))
	}
	if j.fonts {
		el("m_sFontDir", workDir+"/fonts/")
	}
	if j.json != "" {
		el("m_sJsonParams", j.json)
	}
	if t := j.thumb; t != nil {
		b.WriteString("<m_oThumbnail>")
		el("format", strconv.Itoa(t.format))
		el("aspect", strconv.Itoa(t.aspect))
		el("first", strconv.FormatBool(t.first))
		el("zip", "false")
		el("width", strconv.Itoa(t.width))
		el("height", strconv.Itoa(t.height))
		b.WriteString("</m_oThumbnail>")
	}
	if t := j.text; t != nil {
		el("m_nCsvTxtEncoding", strconv.Itoa(t.codepage))
		if t.delimiter > 0 {
			el("m_nCsvDelimiter", strconv.Itoa(t.delimiter))
		}
	}
	b.WriteString(`</TaskQueueDataConvert>`)
	return b.String()
}
