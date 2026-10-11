package handlers

import (
	"github.com/brf-tech/filex/backend/internal/newdoc"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// newdocWithheld is `newdoc_withheld` in /api/files/capabilities: the line
// the New document dialog shows under its types when a whole family of them
// cannot be offered, one sentence per service (`onlyoffice`, `drawio`), plus
// `admin` - where to set it up - for a caller who can (caller_admin).
//
// ⚠⚠ Why the server says it (#217). The dialog built the sentence from its
// own locale table, and it said "Office documents need a document server
// (ONLYOFFICE)" beside an app's own .docx/.xlsx/.pptx rows: the app made the
// kind, the sentence denied it. Which family is missing is still the
// client's to decide - only it knows whether the browser can reach the
// document server (an embedder may point it elsewhere) and which app rows it
// offers - but the words are the server's, in the reader's language, the
// same for every client that draws the dialog.
func newdocWithheld(lang string, admin bool) map[string]string {
	out := map[string]string{
		string(newdoc.RequiresOnlyOffice): srvtext.Text(lang, "server.newdoc.withheld.onlyoffice", nil),
		string(newdoc.RequiresDrawio):     srvtext.Text(lang, "server.newdoc.withheld.drawio", nil),
	}
	if admin {
		out["admin"] = srvtext.Text(lang, "server.newdoc.withheld.admin", nil)
	}
	return out
}
