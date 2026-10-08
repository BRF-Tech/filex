package handlers

import (
	"net/url"
	"strings"
)

// shareDownloadCommand is the one line that fetches a download link's file
// from a terminal - `curl` for a POSIX shell, `Invoke-WebRequest` for
// PowerShell - answered with the link it belongs to (POST /api/files/share,
// POST /api/ai/share and the MCP file_share tool).
//
// ⚠ Built HERE, not in the browser. The share dialog used to assemble the
// curl itself (packages/core lib/shareCli.ts) and so had to know the
// server's own rules for a link - a folder's archive is behind `?zip=wait`,
// a PIN rides as `?pin=`, an S3-backed install answers with a redirect to a
// presigned address - while an agent that made the same link over MCP got
// no command at all and had to work the rules out for itself. The upload
// ticket's command was always the server's (upload_ticket.go); the download
// link's is now too, and every client shows the same line.
type shareDownloadCommand struct {
	// Curl is for a POSIX shell. -L is not optional: an S3-backed instance
	// answers a download with a 302 to a presigned address, and without it
	// curl saves the redirect page instead of the file. -f makes a refused
	// link (expired, wrong PIN) an exit status instead of a saved error page.
	Curl string `json:"curl"`
	// PowerShell is for Windows PowerShell 5.1 and PowerShell 7 alike:
	// Invoke-WebRequest follows the S3 redirect itself, and -UseBasicParsing
	// keeps 5.1 from needing the Internet Explorer engine.
	PowerShell string `json:"powershell"`
}

// downloadCommandFor builds the command for a download link. linkURL is the
// public /s/<token> address, pin the PIN when this answer may carry it (only
// the creator's own answer, at creation), name the shared item's name and
// isDir whether it is a folder (whose archive is what gets downloaded).
// nil for an empty address.
func downloadCommandFor(linkURL, pin, name string, isDir bool) *shareDownloadCommand {
	if strings.TrimSpace(linkURL) == "" {
		return nil
	}
	q := url.Values{}
	if isDir {
		// A folder link serves a browse PAGE at the bare address; the
		// archive is behind ?zip=wait, which blocks until the ZIP is built
		// and then streams it (Share.serveFolderZip).
		q.Set("zip", "wait")
	}
	if pin != "" {
		q.Set("pin", pin)
	}
	target := linkURL
	if enc := q.Encode(); enc != "" {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + enc
	}
	out := strings.TrimSpace(name)
	if isDir && out != "" {
		out += ".zip"
	}
	cmd := &shareDownloadCommand{}
	if out == "" {
		// No name to give: curl takes the server's Content-Disposition name.
		cmd.Curl = "curl -fSL -OJ " + shQuote(target)
		cmd.PowerShell = "Invoke-WebRequest -UseBasicParsing -Uri " + psQuote(target) + " -OutFile " + psQuote("download")
		return cmd
	}
	cmd.Curl = "curl -fSL -o " + shQuote(out) + " " + shQuote(target)
	cmd.PowerShell = "Invoke-WebRequest -UseBasicParsing -Uri " + psQuote(target) + " -OutFile " + psQuote(out)
	return cmd
}

// shQuote wraps s in single quotes for a POSIX shell; an embedded single
// quote becomes the standard close-escape-reopen sequence.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// psQuote wraps s in single quotes for PowerShell, where a single quote
// inside a single-quoted string is written twice.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
