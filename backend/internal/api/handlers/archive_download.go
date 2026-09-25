package handlers

// Downloading a SELECTION as one archive.
//
// # The gap this closes
//
// filex could hand you one file (`?q=download`, one body, one tab) and it could
// hand you one shared folder (`serveFolderZip`, behind a public link). It could
// not hand you the four files and two folders you had just selected. The client
// papered over it by hiding Download whenever more than one row was selected,
// because the only thing it could have done was call window.open() five times,
// and the fifth one is where the popup blocker steps in.
//
// What did NOT exist, and is the reason this is a new endpoint rather than an
// unhide:
//
//   - `sharezip` is the cache for a folder SHARE's archive. It is keyed by node
//     id and swept when the share dies. A selection has no node and no share.
//   - `ops.Zip` writes a .zip INTO the user's storage. That is a mutation, it
//     needs the write scope, it costs quota, and a read-only viewer cannot do
//     it. "Download these" must not leave a file behind.
//
// # Why a ticket instead of one request
//
// A download has to be a NAVIGATION. Fetching an archive with fetch() and
// handing the browser a Blob buffers the whole thing in the tab's memory, which
// is the one thing a 4 GB selection cannot survive; only a navigation streams
// to disk. But a navigation is a GET, a GET carries its arguments in the URL,
// and a selection of 300 paths does not fit in a URL. It also cannot carry an
// Authorization header, which is how the embedded hosts (work.example.com, fishapp)
// authenticate.
//
// So the operation is split, the same way upload tickets split theirs (see
// upload_ticket.go):
//
//	POST /api/files/archive/download {"paths":[…]}   ← authenticated
//	  ← {"url":"/z/<ticket>", "name":"Belgeler.zip", "files":12, …}
//	GET  /z/<ticket>                                  ← navigation, streams
//
// The mint is where all the authority lives. Every path is resolved, checked
// against the caller's tenancy and their ≥viewer grant, and every folder is
// WALKED SERVER-SIDE with that same grant applied to each descendant. What the
// ticket carries is the finished member list — the client's list is an opening
// request, never the answer. The redeem needs no credential because it is not a
// credential for filex: it is unguessable, it lives for minutes, it is consumed
// on use, and the only thing it can do is produce that one archive.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/httpx"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/zipstream"
)

const (
	// archiveTicketTTL is how long a minted archive URL stays redeemable.
	// Long enough for a slow click and a re-try, short enough that a URL
	// caught in a proxy log or a browser history is worthless by the time
	// anyone reads it.
	archiveTicketTTL = 10 * time.Minute
	// archiveTicketClaimTTL bounds an in-flight redeem. A transfer that dies
	// releases its claim so the same URL can be retried — a 3 GB archive
	// dropping at 90% must not force the user back to the selection.
	archiveTicketClaimTTL = 6 * time.Hour
	// archiveMaxMembers caps how many files one archive may name. This is a
	// bound on the TICKET (a resolved member list held in memory), not on the
	// bytes: 20k members is a couple of megabytes of bookkeeping, and a
	// selection larger than that is a folder, which the user can select
	// directly and which costs one member here.
	archiveMaxMembers = 20000
	// incompleteArchiveNote is the member zipstream adds when something had to
	// be left out. Named so it sorts to the top of most listings and reads as
	// a warning rather than as one of the user's own files.
	incompleteArchiveNote = "_FILEX-INCOMPLETE.txt"
)

// archiveMember is one resolved file, ready to be read. Resolution — which
// storage, which driver path, whether the caller may read it — has already
// happened by the time one of these exists.
type archiveMember struct {
	StorageID int64
	// Path is the driver path (no adapter prefix).
	Path string
	// Name is the entry name inside the archive.
	Name  string
	Size  int64
	Mtime time.Time
}

// archiveTicket is one minted, not-yet-redeemed archive.
type archiveTicket struct {
	// Name is the filename offered to the browser.
	Name    string
	Members []archiveMember
	Bytes   int64
	// Owner is who minted it. Kept for the audit line, not consulted at
	// redeem: the authorization decision was made at mint and is baked into
	// Members.
	Owner     *model.User
	ExpiresAt time.Time
	claimedAt time.Time
	// File, when set, makes this a ONE-FILE link (`"mode":"file"`): the
	// redeem streams that file's own bytes instead of an archive, and — unlike
	// an archive — re-checks the owner's reach at the redeem. download_link.go.
	File *fileLink
}

func (t *archiveTicket) expired(now time.Time) bool { return now.After(t.ExpiresAt) }

// archiveTicketStore holds live archive tickets in memory.
//
// ⚠ In memory, therefore per-process. A restart costs a re-mint (one POST, and
// the client already knows how to make it); a multi-replica deployment behind a
// round-robin needs sticky sessions for the redeem, exactly as it already does
// for upload tickets and staged uploads. Persisting them would mean a schema
// for something whose whole life is ten minutes.
type archiveTicketStore struct {
	mu sync.Mutex
	m  map[string]*archiveTicket
}

// NewArchiveTicketStore builds the in-memory store.
func NewArchiveTicketStore() *archiveTicketStore {
	return &archiveTicketStore{m: map[string]*archiveTicket{}}
}

func (s *archiveTicketStore) mint(t *archiveTicket) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(time.Now())
	s.m[tok] = t
	return tok, nil
}

// claim marks a ticket in flight. The three refusals are kept apart because
// they mean different things to the person looking at the browser: "that link
// was never real", "you waited too long", "it is already downloading".
func (s *archiveTicketStore) claim(tok string) (*archiveTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	// Not pruning first, for the same reason upload tickets do not: sweeping
	// here would delete the lapsed ticket before the lookup and report
	// "never existed" for something that merely aged out.
	t := s.m[tok]
	if t == nil {
		return nil, errTicketUnknown
	}
	if t.expired(now) {
		delete(s.m, tok)
		return nil, errTicketExpired
	}
	if !t.claimedAt.IsZero() && now.Sub(t.claimedAt) < archiveTicketClaimTTL {
		return nil, errTicketInFlight
	}
	t.claimedAt = now
	return t, nil
}

func (s *archiveTicketStore) consume(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, tok)
}

func (s *archiveTicketStore) release(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.m[tok]; t != nil {
		t.claimedAt = time.Time{}
	}
}

func (s *archiveTicketStore) prune(now time.Time) {
	for k, v := range s.m {
		if v.expired(now) {
			delete(s.m, k)
		}
	}
}

// AttachDownloadTickets wires the ticket store. Without it the mint endpoint
// answers 503 rather than panicking — a handler assembled without its store is
// a wiring bug, and a nil map write is a worse way to find out.
func (a *Archive) AttachDownloadTickets(s *archiveTicketStore) { a.Tickets = s }

// archiveDownloadRequest is the body of POST /api/files/archive/download.
type archiveDownloadRequest struct {
	// Paths are `<adapter>://<rel>` (or bare relative, resolved against the
	// first storage) — the same wire form every other selection verb takes.
	Paths []string `json:"paths"`
	// Name overrides the archive filename. Optional; the server names the
	// archive when this is empty, and sanitizes it when it is not.
	Name string `json:"name,omitempty"`
	// Mode is "zip" (the default: one archive of the selection) or "file"
	// (exactly one file, served as itself — the browser's drag-out link,
	// download_link.go).
	Mode string `json:"mode,omitempty"`
	// ExpiresInSeconds shortens a "file" link's life. It can only shorten it:
	// the ceiling is fileLinkTTL.
	ExpiresInSeconds int `json:"expires_in_seconds,omitempty"`
}

// archiveDownloadInfo is what a successful mint returns.
type archiveDownloadInfo struct {
	URL       string `json:"url"`
	Ticket    string `json:"ticket"`
	Name      string `json:"name"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
	ExpiresAt string `json:"expires_at"`
	// Mode echoes what was minted, "zip" or "file". A client asking for a
	// file link reads it to tell a server that knows the mode from an older
	// one that ignored the field and minted a zip.
	Mode string `json:"mode"`
	// TTLSeconds is the link's life from now, so a client can keep one without
	// trusting its own clock against expires_at.
	TTLSeconds int `json:"ttl_seconds"`
}

// DownloadTicket resolves a selection into an archive and returns the URL that
// streams it.
//
// Refusals, and why each is its own answer rather than a shared 400:
//
//	403  one of the named paths is not readable by this caller
//	404  the storage is not this tenant's
//	409  the selection resolved to no readable file at all
//	413  more members than archiveMaxMembers
//
// The 409 matters: a selection of three empty folders is not an error the user
// made, but an empty ZIP arriving as a "successful" download is the kind of
// thing people file bugs about six months later. Say so at the click.
func (a *Archive) DownloadTicket(w http.ResponseWriter, r *http.Request) {
	if a.Tickets == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "archive downloads unavailable"})
		return
	}
	var req archiveDownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	switch req.Mode {
	case "", "zip":
	case "file":
		a.mintFileLink(w, r, req)
		return
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown mode: " + req.Mode})
		return
	}
	if len(req.Paths) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing paths"})
		return
	}

	ctx := r.Context()
	var members []archiveMember
	var total int64
	// One ACL set per storage, loaded once. A selection is usually one
	// storage; a folder walk asks CanSee for every descendant, and doing that
	// against a freshly loaded set per node would turn a listing walk into a
	// query storm.
	sets := map[int64]*acl.Set{}

	for _, raw := range req.Paths {
		storageID, rel, err := a.resolveStorage(ctx, 0, raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// Tenancy BEFORE the ACL, for the reason ops.Submit spells out: the
		// ACL is tenant-blind and answers "editor" for a plain user on an
		// rbac_enabled=false storage, so asking it first would let a member of
		// one customer name another customer's storage id.
		if !ownsStorage(w, r, storageID, "storage") {
			return
		}
		// ⚠ The token's `root:` confinement. confine.Middleware rewrites the
		// path fields of a body it knows (`path`, `source`, `items`…) and
		// `paths` is not one of them, so without this a token confined to one
		// folder could archive — and download — any file its account reads.
		if !rootAllows(ctx, a.Store, storageID, rel) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": confine.ErrOutOfRoot.Error()})
			return
		}
		if !aclAllowID(ctx, a.ACL, a.Store, storageID, rel, acl.LevelViewer) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission: " + raw})
			return
		}
		set, ok := sets[storageID]
		if !ok {
			set = a.aclSetFor(ctx, storageID)
			sets[storageID] = set
		}
		drv, err := a.StorageResolver(storageID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage"})
			return
		}
		st, err := drv.Stat(ctx, rel)
		if err != nil {
			// A path that is already gone is skipped, not fatal: the user
			// selected five things and one of them was renamed by somebody
			// else two seconds ago. Refusing the whole download for that is
			// worse than handing over the other four.
			slog.Info("archive download: source skipped at mint",
				slog.String("path", raw), slog.String("err", err.Error()))
			continue
		}
		if st.Kind == storage.KindDirectory {
			found, err := a.expandDir(ctx, drv, storageID, rel, set)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not list " + raw + ": " + err.Error()})
				return
			}
			members = append(members, found...)
		} else {
			members = append(members, archiveMember{
				StorageID: storageID, Path: rel, Name: path.Base(rel),
				Size: st.Size, Mtime: st.Mtime,
			})
		}
		if len(members) > archiveMaxMembers {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
				"error": "too many files for one archive",
				"max":   archiveMaxMembers,
			})
			return
		}
	}
	for _, m := range members {
		total += m.Size
	}
	if len(members) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "nothing to download: the selection contains no readable file",
		})
		return
	}

	name := ArchiveName(req.Paths, req.Name)
	tok, err := a.Tickets.mint(&archiveTicket{
		Name:      name,
		Members:   members,
		Bytes:     total,
		Owner:     auth.UserFrom(ctx),
		ExpiresAt: time.Now().Add(archiveTicketTTL),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, archiveDownloadInfo{
		URL:        "/z/" + tok,
		Ticket:     tok,
		Name:       name,
		Files:      len(members),
		Bytes:      total,
		ExpiresAt:  time.Now().Add(archiveTicketTTL).UTC().Format(time.RFC3339),
		Mode:       "zip",
		TTLSeconds: int(archiveTicketTTL / time.Second),
	})
}

// aclSetFor loads the caller's grants for one storage, or nil when RBAC is not
// wired at all (dev/tests), which every consumer reads as "no filtering".
func (a *Archive) aclSetFor(ctx context.Context, storageID int64) *acl.Set {
	if a.ACL == nil {
		return nil
	}
	st, err := a.Store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return nil
	}
	set, err := a.ACL.LoadSet(ctx, auth.UserFrom(ctx), st)
	if err != nil {
		return nil
	}
	return set
}

// expandDir walks a selected folder and returns every file under it the caller
// may read, named relative to the folder's PARENT so the archive unpacks as
// `<folder>/…` rather than dumping the contents at the top level.
//
// ⚠ The per-descendant CanSee is the whole point of doing this server-side. A
// folder the caller can see may contain a subtree they were never granted; a
// client-side expansion would have asked for those paths by name and an
// endpoint that trusted the list would have served them.
func (a *Archive) expandDir(ctx context.Context, drv storage.Driver, storageID int64, rel string, set *acl.Set) ([]archiveMember, error) {
	files, err := sharezip.CollectFiles(ctx, drv, rel)
	if err != nil {
		return nil, err
	}
	base := path.Base(strings.Trim(rel, "/"))
	if base == "" || base == "." || base == "/" {
		base = ""
	}
	out := make([]archiveMember, 0, len(files))
	for _, f := range files {
		if set != nil && set.Effective(acl.CleanRel(f.Path)) < acl.LevelViewer {
			continue
		}
		name := f.Rel
		if base != "" {
			name = base + "/" + f.Rel
		}
		out = append(out, archiveMember{
			StorageID: storageID, Path: f.Path, Name: name,
			Size: f.Size, Mtime: f.Mtime,
		})
	}
	return out, nil
}

// DownloadArchive streams a minted archive. Public route (`GET /z/{ticket}`) —
// the ticket is the authority, see the package comment.
func (a *Archive) DownloadArchive(w http.ResponseWriter, r *http.Request) {
	if a.Tickets == nil {
		http.Error(w, "archive downloads unavailable", http.StatusServiceUnavailable)
		return
	}
	tok := chi.URLParam(r, "ticket")
	t, err := a.Tickets.claim(tok)
	if err != nil {
		switch {
		case errors.Is(err, errTicketExpired):
			http.Error(w, "this download link has expired — select the files again", http.StatusGone)
		case errors.Is(err, errTicketInFlight):
			http.Error(w, "this download is already running", http.StatusConflict)
		default:
			http.Error(w, "unknown download link", http.StatusNotFound)
		}
		return
	}
	if t.File != nil {
		a.redeemFileLink(w, r, tok, t)
		return
	}

	// ⚠ No Content-Length, and there must never be one. The size of a
	// deflated archive is not known until its last member is written; the
	// sum of the members' sizes is the UNCOMPRESSED input, and declaring it
	// would make Go truncate the body at that count. This repo has already
	// shipped that bug once with a length taken from the catalogue — the
	// failure surfaced inside the recipient's program as a nameless
	// "Download failed". Chunked is the honest answer.
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", httpx.ContentDisposition("attachment", t.Name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")

	members := make([]zipstream.Member, 0, len(t.Members))
	for _, m := range t.Members {
		m := m
		members = append(members, zipstream.Member{
			Name:  m.Name,
			Size:  m.Size,
			Mtime: m.Mtime,
			Open: func(c context.Context) (io.ReadCloser, error) {
				drv, err := a.StorageResolver(m.StorageID)
				if err != nil {
					return nil, err
				}
				// filebody, not drv.Read: a file whose newer bytes are still
				// in staging must archive as the version the user committed,
				// which is the same rule the folder-share archive follows.
				src, err := a.Body.Resolve(c, drv, m.StorageID, m.Path, nil)
				if err != nil {
					return nil, err
				}
				return src.Open(c)
			},
		})
	}

	skips, err := zipstream.Write(r.Context(), w, members, zipstream.Options{
		Flush:    flusher(w),
		Manifest: incompleteArchiveNote,
	})
	for _, s := range skips {
		slog.Warn("archive download: member left out",
			slog.String("entry", s.Name), slog.Bool("partial", s.Partial),
			slog.String("err", s.Err.Error()))
	}
	if err != nil {
		// The body is already part-written, so there is no status left to
		// send: the client sees a short archive, which is the signal. Release
		// the claim so a retry of the same URL works.
		a.Tickets.release(tok)
		slog.Warn("archive download: stream ended early",
			slog.String("name", t.Name), slog.String("err", err.Error()))
		return
	}
	a.Tickets.consume(tok)
}

// flusher returns a "push what you have to the client" func for w, or nil when
// the writer cannot flush.
//
// Without it a slow storage looks like a hung download: Go buffers the first
// few KB and the browser shows nothing at all until either the buffer fills or
// the whole archive is done. Flushing per member is what turns that into a
// download whose byte count climbs.
func flusher(w http.ResponseWriter) func() {
	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		return nil
	}
	return func() { _ = rc.Flush() }
}

// ArchiveName picks the filename for an archive of `paths`.
//
// The rule, in order:
//
//   - an explicit name from the caller, sanitized;
//   - one source → that source's own basename, so downloading the folder
//     "Faturalar" gives you "Faturalar.zip";
//   - many sources sharing a parent → the parent's basename, because that is
//     the folder the person was standing in;
//   - anything else → "filex-N-items".
//
// A name that says something is not decoration. "download.zip" in a downloads
// folder next to four other "download (3).zip" is a file nobody can identify a
// week later.
func ArchiveName(paths []string, override string) string {
	if n := sanitizeArchiveName(override); n != "" {
		return n + ".zip"
	}
	if len(paths) == 1 {
		_, rel := splitAdapterPath(paths[0])
		if b := sanitizeArchiveName(path.Base(strings.Trim(rel, "/"))); b != "" {
			return b + ".zip"
		}
	}
	if len(paths) > 1 {
		parent := ""
		same := true
		for i, p := range paths {
			_, rel := splitAdapterPath(p)
			d := path.Dir(strings.Trim(rel, "/"))
			if d == "." || d == "/" {
				d = ""
			}
			if i == 0 {
				parent = d
				continue
			}
			if d != parent {
				same = false
				break
			}
		}
		if same && parent != "" {
			if b := sanitizeArchiveName(path.Base(parent)); b != "" {
				return b + ".zip"
			}
		}
	}
	return fmt.Sprintf("filex-%d-items.zip", len(paths))
}

// sanitizeArchiveName strips what does not belong in a filename. It keeps
// non-ASCII letters: the header builder (httpx.ContentDisposition) already
// carries them correctly in `filename*`, so there is no reason to turn
// "Faturalar (Eylül)" into "Faturalar (Eyl_l)".
func sanitizeArchiveName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".zip")
	s = strings.Map(func(rn rune) rune {
		switch rn {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\r', '\n', 0:
			return -1
		}
		if rn < 0x20 {
			return -1
		}
		return rn
	}, s)
	s = strings.Trim(strings.TrimSpace(s), ".")
	if len(s) > 120 {
		s = strings.TrimSpace(s[:120])
	}
	return s
}
