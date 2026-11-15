// Package watchproto is the wire contract between filex and filex-watch, the
// small program that runs on the machine that owns a storage's disk and tells
// filex which folders changed there (docs/FILEX-WATCH.md).
//
// Both sides import THIS package - the server's door
// (handlers/storage_watch.go) and the agent (cmd/filex-watch) - so the shape
// of a report, its limits and the rule for a folder path are written once.
// It imports nothing but the standard library: the agent is a small binary
// and must stay one.
//
// # Why it exists
//
// inotify (and kqueue, and ReadDirectoryChangesW) only sees what the machine
// it runs on writes. A storage whose disk is somewhere else - a NAS share
// mounted over NFS or SMB, the folder an SFTP storage points at - changes
// without filex's own watch ever hearing of it, and the change reaches the
// catalogue only at the next scan. The agent runs where the writes happen and
// says which folders to look at; filex looks at those folders alone.
//
// # The report
//
//	POST /api/storages/{id}/watch/events
//	Authorization: Bearer fxw_…            (a filex-watch key, KeyPrefix)
//	{"protocol":1,"agent":{…},"seq":42,"folders":["/photos/2026"],"resync":false}
//	→ 202 {"accepted":true,"seq":42,"queued":1,"ignored":0}
//
// A folder is a path relative to the storage's root, slash-separated, with a
// leading slash ("/" is the root). It names the folder whose DIRECT contents
// changed: a file written in /a/b is reported as /a/b, a folder made under /a
// as /a (and, once it has contents, as itself).
//
// `resync` says the agent may have missed changes - it just started, it got
// its connection back, or its queue ran over - and filex should bring the
// whole storage up to date. `subtrees` names folders the agent cannot watch
// (the operating system's watch limit): filex re-reads each one whole when the
// agent asks, every few minutes. An empty report is a heartbeat: it keeps the
// key's "last seen" current on the storage page.
package watchproto

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Protocol is the version of the report this package describes. A server
// refuses a report from a newer protocol it does not know with 400
// (watch_report_invalid), so an agent upgraded ahead of its server says so
// instead of being misread.
const Protocol = 1

// AgentVersion is the release of filex-watch this source tree builds, and the
// one the storage page's install commands download. It is filex-watch's own
// version line (tags `filex-watch-v<AgentVersion>`), not filex's: the agent
// changes rarely, and a filex release does not make a new one. The release
// workflow refuses a tag that does not match it.
const AgentVersion = "0.1.0"

// TagPrefix is the prefix of filex-watch's release tags in the filex
// repository (`filex-watch-v0.1.0`). filex's own tags are `vX.Y.Z`; the
// update manifest and the docs' Releases page leave this line out.
const TagPrefix = "filex-watch-v"

// KeyPrefix starts every filex-watch key, so a person (or a secret scanner)
// reading one in a config file knows what it is and what it opens: one
// storage's change reports, nothing else.
const KeyPrefix = "fxw_"

// EventsPath is the door's path for storage id.
func EventsPath(storageID int64) string {
	return fmt.Sprintf("/api/storages/%d/watch/events", storageID)
}

// Limits of one report. A busier disk sends more reports, not bigger ones;
// a change bigger than MaxFolders is a resync.
const (
	// MaxBodyBytes caps the request body the server reads.
	MaxBodyBytes = 1 << 20
	// MaxFolders caps `folders` and `subtrees` together.
	MaxFolders = 1000
	// MaxPathBytes caps one folder path.
	MaxPathBytes = 4096
	// MaxAgentField caps each string of `agent`.
	MaxAgentField = 200
)

// Agent says which program sent a report. Shown on the storage page, never
// trusted for anything else.
type Agent struct {
	// ID is chosen by the agent at start (random); a new process is a new ID.
	ID      string `json:"id"`
	Version string `json:"version"`
	Host    string `json:"host,omitempty"`
	OS      string `json:"os,omitempty"`
	Arch    string `json:"arch,omitempty"`
	// Root is the folder the agent watches on its own machine, as it wrote
	// it in its configuration.
	Root string `json:"root,omitempty"`
}

// Report is one POST to the door.
type Report struct {
	Protocol int   `json:"protocol"`
	Agent    Agent `json:"agent"`
	// Seq numbers the agent's reports from 1, once per batch (a retry of the
	// same batch keeps its number). A report the server already took is not
	// queued twice.
	Seq uint64 `json:"seq"`
	// Folders whose direct contents changed.
	Folders []string `json:"folders,omitempty"`
	// Resync: bring the whole storage up to date.
	Resync bool `json:"resync,omitempty"`
	// Subtrees to re-read whole: folders the agent cannot watch.
	Subtrees []string `json:"subtrees,omitempty"`
}

// Answer is what the door answers to an accepted report (202).
type Answer struct {
	Accepted bool   `json:"accepted"`
	Seq      uint64 `json:"seq"`
	// Queued is how many folders and subtrees filex will look at.
	Queued int `json:"queued"`
	// Ignored is how many it will not: folders inside filex's own trees
	// (its trash, version history and thumbnails). A folder the storage
	// excludes from scanning is dropped later, when the catalogue looks.
	Ignored int  `json:"ignored"`
	Resync  bool `json:"resync,omitempty"`
	// Storage is the storage's name, for the agent's log.
	Storage string `json:"storage,omitempty"`
	// Instance names the filex process that took the report, made at its
	// start. Folders filex has taken but not yet handed to its catalogue live
	// in that process alone; when the instance an agent hears changes, filex
	// started again and may have lost some, so the agent's next report asks
	// for one resync. An agent remembers the last one it heard.
	Instance string `json:"instance,omitempty"`
}

// ErrInvalid wraps every reason a report is refused.
var ErrInvalid = errors.New("watchproto: invalid report")

// CleanFolder checks raw as a folder path and returns it in its one canonical
// form: a leading slash, single slashes, no trailing slash ("/" alone for the
// root). It refuses rather than repairs anything that could name a place
// outside the storage or be read two ways: "..", ".", a backslash, a NUL or
// any other control character, text that is not UTF-8, and a path longer than
// MaxPathBytes.
func CleanFolder(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%w: an empty folder path", ErrInvalid)
	}
	if len(raw) > MaxPathBytes {
		return "", fmt.Errorf("%w: a folder path longer than %d bytes", ErrInvalid, MaxPathBytes)
	}
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("%w: a folder path that is not UTF-8", ErrInvalid)
	}
	if raw[0] != '/' {
		return "", fmt.Errorf("%w: %q does not start with /", ErrInvalid, raw)
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%w: %q carries a control character", ErrInvalid, raw)
		}
		if r == '\\' {
			return "", fmt.Errorf("%w: %q carries a backslash; separate folders with /", ErrInvalid, raw)
		}
	}
	var segs []string
	for _, s := range strings.Split(raw, "/") {
		switch s {
		case "":
			continue
		case ".", "..":
			return "", fmt.Errorf("%w: %q has a %q segment", ErrInvalid, raw, s)
		}
		segs = append(segs, s)
	}
	return "/" + strings.Join(segs, "/"), nil
}

// Validate checks a report as the server reads it and the agent sends it:
// the protocol it speaks, the counts, every path (CleanFolder), the agent's
// fields. It rewrites Folders and Subtrees to their canonical forms, with
// duplicates removed.
func (r *Report) Validate() error {
	if r.Protocol < 1 || r.Protocol > Protocol {
		return fmt.Errorf("%w: protocol %d (this side speaks 1 to %d)", ErrInvalid, r.Protocol, Protocol)
	}
	if n := len(r.Folders) + len(r.Subtrees); n > MaxFolders {
		return fmt.Errorf("%w: %d folders in one report, at most %d", ErrInvalid, n, MaxFolders)
	}
	for _, f := range []string{r.Agent.ID, r.Agent.Version, r.Agent.Host, r.Agent.OS, r.Agent.Arch, r.Agent.Root} {
		if len(f) > MaxAgentField || !utf8.ValidString(f) {
			return fmt.Errorf("%w: an agent field longer than %d bytes or not UTF-8", ErrInvalid, MaxAgentField)
		}
	}
	var err error
	if r.Folders, err = cleanAll(r.Folders); err != nil {
		return err
	}
	r.Subtrees, err = cleanAll(r.Subtrees)
	return err
}

func cleanAll(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		p, err := CleanFolder(raw)
		if err != nil {
			return nil, err
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// Parent is the folder that holds p ("/" for a top-level entry and for "/"
// itself). p must be canonical (CleanFolder).
func Parent(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

// Within reports whether p is dir or inside it. Both canonical.
func Within(p, dir string) bool {
	if dir == "/" || p == dir {
		return true
	}
	return strings.HasPrefix(p, dir+"/")
}
