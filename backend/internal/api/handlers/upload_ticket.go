package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// Upload tickets exist for the one file an agent CANNOT hand to filex: a large
// local one. Every agent-facing write surface here — /api/ai/upload's JSON
// body, the MCP file_write tool — carries its bytes inside the call itself, so
// on an MCP transport those bytes travel through the model's context. A 130 MB
// file is ~173 MB of base64 there: not slow, impossible. The agent is left
// telling its user "I can't upload this", which is exactly what happened for an
// hour with one spreadsheet (2026-08-27).
//
// A ticket splits the operation in two: the AUTHORIZED call (mint) resolves and
// pins the destination under the caller's own token, and the UNAUTHENTICATED
// transfer (redeem) streams the bytes straight to storage over plain HTTP:
//
//	agent → POST /api/ai/upload/ticket {"path":"s3://…/big.xlsx"}   (its token)
//	      ← {"url":"https://fm.example.com/u/<ticket>", "expires_at":…}
//	agent → curl -T big.xlsx <url>                                   (no token)
//	      ← {"entry":{…}}
//
// The redeem URL needs no credentials ON PURPOSE: the agent that must run the
// upload frequently has no filex token (a Coder workspace, a fleet agent), and
// handing it one to copy a file would be a far worse trade. What keeps that
// safe is that the ticket is not a credential for filex — it is a credential
// for ONE write:
//
//   - the destination is fixed at mint time and the redeemer cannot name a path
//   - it grants no read, no list, no delete — nothing but that one write
//   - it is single-use (a successful write consumes it) and short-lived
//   - the bytes are billed to and written as the MINTER, not the redeemer
//
// Tickets live in memory only. They are minutes-lived by design, so a restart
// dropping them costs a re-mint (one MCP call) and buys us no schema change.
const (
	uploadTicketDefaultTTL = 30 * time.Minute
	uploadTicketMaxTTL     = 24 * time.Hour
	// A redeem that never completes must not pin the ticket forever; after
	// this long an in-flight claim is considered abandoned and reusable.
	uploadTicketClaimTTL = 2 * time.Hour
)

// uploadTicket is one minted, not-yet-redeemed upload authorization.
type uploadTicket struct {
	// Path is the canonical adapter://rel destination, already resolved
	// against the minter's confinement root — the redeemer never supplies it.
	Path string
	// Owner is the minting user. The redeem writes AS this user so grants,
	// quota and ownership match a normal upload; the redeemer stays anonymous.
	Owner     *model.User
	MaxBytes  int64
	ExpiresAt time.Time
	// claimedAt is non-zero while a redeem is in flight. A failed transfer
	// clears it so the agent can retry with the same URL.
	claimedAt time.Time
}

func (t *uploadTicket) expired(now time.Time) bool { return now.After(t.ExpiresAt) }

// uploadTicketStore holds live tickets. Shared by the mint surfaces (REST +
// MCP) and the public redeem handler.
type uploadTicketStore struct {
	mu sync.Mutex
	m  map[string]*uploadTicket
}

// NewUploadTicketStore builds the in-memory ticket store.
func NewUploadTicketStore() *uploadTicketStore {
	return &uploadTicketStore{m: map[string]*uploadTicket{}}
}

// mint stores t under a fresh random token and returns it.
func (s *uploadTicketStore) mint(t *uploadTicket) (string, error) {
	var raw [24]byte
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

// claim marks a ticket in flight and returns it. The error distinguishes the
// three refusals the redeem endpoint must report differently.
func (s *uploadTicketStore) claim(tok string) (*uploadTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	// Deliberately NOT pruning first: sweeping the map here would delete the
	// lapsed ticket before the lookup and answer "never existed" (404) for
	// something that DID exist and merely aged out — the agent would go
	// hunting for a typo instead of re-minting. Expiry is reported below;
	// the sweep happens on mint.
	t := s.m[tok]
	if t == nil {
		return nil, errTicketUnknown
	}
	if t.expired(now) {
		delete(s.m, tok)
		return nil, errTicketExpired
	}
	if !t.claimedAt.IsZero() && now.Sub(t.claimedAt) < uploadTicketClaimTTL {
		return nil, errTicketInFlight
	}
	t.claimedAt = now
	return t, nil
}

// consume removes a redeemed ticket (single use).
func (s *uploadTicketStore) consume(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, tok)
}

// release clears an in-flight claim after a failed transfer so the same URL
// can be retried — a 130 MB upload dying mid-flight must not force a re-mint.
func (s *uploadTicketStore) release(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.m[tok]; t != nil {
		t.claimedAt = time.Time{}
	}
}

// prune drops expired tickets. Caller holds mu.
func (s *uploadTicketStore) prune(now time.Time) {
	for k, v := range s.m {
		if v.expired(now) {
			delete(s.m, k)
		}
	}
}

var (
	errTicketUnknown  = errors.New("unknown ticket")
	errTicketExpired  = errors.New("ticket expired")
	errTicketInFlight = errors.New("ticket already in use")
)

// ─────────────────── mint (authorized) ───────────────────

// uploadTicketRequest is the JSON body of POST /api/ai/upload/ticket and the
// input of the file_upload_ticket MCP tool.
type uploadTicketRequest struct {
	Path             string `json:"path"`
	ExpiresInSeconds int    `json:"expires_in_seconds,omitempty"`
	MaxBytes         int64  `json:"max_bytes,omitempty"`
}

// uploadTicketInfo is what a minting caller gets back.
type uploadTicketInfo struct {
	URL       string `json:"url"`        // ready-to-use, credential-free
	Ticket    string `json:"ticket"`     // token alone (for /u/<ticket>)
	Path      string `json:"path"`       // resolved destination
	MaxBytes  int64  `json:"max_bytes"`  // hard ceiling for this ticket
	ExpiresAt string `json:"expires_at"` // RFC3339
	// Curl is the exact command that finishes the job. Handing an agent a
	// ready line is the whole point of the feature: the agent that reaches
	// for this tool is one that already failed to move the bytes itself.
	Curl string `json:"curl"`
	// PowerShell is the same transfer for a caller with no curl (a Windows
	// box, a restricted image). Without it "run this curl line" is a dead end
	// on the machines where it happens to be missing.
	PowerShell string `json:"powershell"`
	// Next says what to do with the two lines above — including the case that
	// has no shell at all (an MCP-only client), where the answer is to hand
	// the line to the user rather than to give up.
	Next string `json:"next"`
}

// CreateUploadTicket resolves and authorizes `p` under the CALLER's token,
// then mints a credential-free URL that will accept exactly one upload to it.
//
// Authorization happens here and only here (lesson #3: every id/name-taking
// entry point posts its own guard): resolveStorage applies the token's
// confinement root and the tenant scope, and the editor-level grant check
// mirrors WriteStream's. A caller who could not write the path itself cannot
// mint a ticket for it.
func (a *aiOps) CreateUploadTicket(ctx context.Context, req uploadTicketRequest) (*uploadTicketInfo, error) {
	if a.tickets == nil {
		return nil, storage.ErrUnsupported
	}
	s, rel, err := a.resolveStorage(ctx, req.Path)
	if err != nil {
		return nil, err
	}
	if rel == "" {
		return nil, errors.New("path required")
	}
	if s.ReadOnly {
		return nil, storage.ErrReadOnly
	}
	if !a.allow(ctx, s, rel, acl.LevelEditor) {
		return nil, errAIForbidden
	}
	// A folder as `path` would write a file at the folder's own key — the
	// kind conflict EnsureFileTarget exists to prevent. Catch it at mint time
	// so the agent learns before it transfers 130 MB, not after.
	drv, err := a.resolver(s.ID)
	if err != nil {
		return nil, err
	}
	if err := storage.EnsureFileTarget(ctx, drv, rel); err != nil {
		// "path exists with a different kind" is true and useless to a caller
		// that just handed us a folder. Say what it did and what to send.
		if errors.Is(err, storage.ErrKindConflict) {
			return nil, denied(storage.ErrKindConflict,
				"%q already exists as a FOLDER. A ticket uploads one file, so `path` must be the full destination file path (e.g. %q), not the folder to put it in",
				req.Path, strings.TrimRight(req.Path, "/")+"/<filename>")
		}
		return nil, err
	}

	ttl := uploadTicketDefaultTTL
	if req.ExpiresInSeconds > 0 {
		ttl = time.Duration(req.ExpiresInSeconds) * time.Second
		if ttl > uploadTicketMaxTTL {
			ttl = uploadTicketMaxTTL
		}
	}
	max := int64(aiMaxUploadBytes)
	if req.MaxBytes > 0 && req.MaxBytes < max {
		max = req.MaxBytes
	}

	dest := joinAdapterPath(s.Name, rel)
	tok, err := a.tickets.mint(&uploadTicket{
		Path:      dest,
		Owner:     auth.UserFrom(ctx),
		MaxBytes:  max,
		ExpiresAt: time.Now().Add(ttl),
	})
	if err != nil {
		return nil, err
	}

	url := a.tenants.ForStorage(ctx, s.ID) + "/u/" + tok
	return &uploadTicketInfo{
		URL:        url,
		Ticket:     tok,
		Path:       dest,
		MaxBytes:   max,
		ExpiresAt:  time.Now().Add(ttl).UTC().Format(time.RFC3339),
		Curl:       "curl -T <local-file> " + url,
		PowerShell: "Invoke-WebRequest -Method Put -InFile <local-file> -Uri " + url,
		Next: "Run one of the lines above ON THE MACHINE THAT HOLDS THE FILE, replacing <local-file> with its full path. " +
			"No token or header is needed. If you cannot run shell commands, give the line to the user and ask them to run it. " +
			"A successful upload answers {\"entry\":{...}}; confirm the size with file_info afterwards.",
	}, nil
}

// UploadTicket → POST /api/ai/upload/ticket (scope: write).
func (h *AI) UploadTicket(w http.ResponseWriter, r *http.Request) {
	var body uploadTicketRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	info, err := h.ops.CreateUploadTicket(r.Context(), body)
	if err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// AttachTickets wires the shared ticket store into the AI REST surface.
func (h *AI) AttachTickets(s *uploadTicketStore) { h.ops.tickets = s }

// ─────────────────── redeem (public, credential-free) ───────────────────

// TicketUpload serves PUT/POST /u/{ticket}: the credential-free half of the
// ticket flow. It is deliberately the thinnest possible surface — one write to
// one pinned path — and it never lists, reads or reveals anything about the
// destination.
type TicketUpload struct {
	ops     *aiOps
	tickets *uploadTicketStore
	limiter *ipLimiter
}

// NewTicketUpload builds the public redeem handler over the same ops core the
// authorized surfaces use, so a ticket upload gets identical staging,
// thumbnailing, write-hook and DB-cache behaviour.
func NewTicketUpload(store db.Store, resolver func(int64) (storage.Driver, error), tickets *uploadTicketStore) *TicketUpload {
	return &TicketUpload{
		ops:     newAIOps(store, resolver, nil, "", nil),
		tickets: tickets,
		limiter: newIPLimiter(120, time.Hour),
	}
}

// AttachACL wires the RBAC resolver so the write runs under the minter's grants.
func (h *TicketUpload) AttachACL(r *acl.Resolver) { h.ops.acl = r }

// AttachThumbs mirrors the manager-upload thumbnail dispatch.
func (h *TicketUpload) AttachThumbs(p *thumb.Pipeline) { h.ops.thumbs = p }

// AttachSearchIndex wires the search index. ⚠ Easy to forget, and the symptom
// is quiet: a ticket upload without it lands a row and a change frame — so the
// folder listing and every open explorer look right — while the file is
// findable by name only through the search endpoint's SQL LIKE fallback, and
// not at all by its content.
func (h *TicketUpload) AttachSearchIndex(idx *search.Index) { h.ops.attachSearchIndex(idx) }

// AttachStaged routes big transfers through the staging area — the whole point
// of tickets is big files, so this is the common path, not the exception.
func (h *TicketUpload) AttachStaged(s *StagedUpload) { h.ops.staged = s }

// AttachBody wires the byte-source resolver.
func (h *TicketUpload) AttachBody(b *filebody.Resolver) { h.ops.attachBody(b) }

// ticketRefusal answers a refused redeem. Every reply carries a `hint` saying
// what to DO, because the three refusals an agent actually hits need three
// DIFFERENT reactions and a bare code cannot tell them apart: an expired
// ticket needs a new one, an oversize file must be retried against the SAME
// (still valid) ticket, and a chunked body just needs `curl -T`. A code alone
// leaves the caller guessing, which is how an agent ends up either giving up
// or minting tickets it did not need (lesson #39, applied to this endpoint).
func ticketRefusal(w http.ResponseWriter, status int, code, hint string, extra map[string]any) {
	body := map[string]any{"error": code, "hint": hint}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

// Upload handles PUT /u/{ticket} (raw body, what `curl -T` sends) and
// POST /u/{ticket} (multipart `file`, what a browser or `curl -F` sends).
func (h *TicketUpload) Upload(w http.ResponseWriter, r *http.Request) {
	if !h.limiter.allow(clientIP(r)) {
		ticketRefusal(w, http.StatusTooManyRequests, "rate_limited",
			"Too many upload attempts from this address. Wait a minute, then retry the same URL — the ticket is still valid.", nil)
		return
	}
	tok := chi.URLParam(r, "ticket")
	t, err := h.tickets.claim(tok)
	if err != nil {
		switch {
		case errors.Is(err, errTicketExpired):
			ticketRefusal(w, http.StatusGone, "ticket_expired",
				"This ticket has expired. Mint a new one (file_upload_ticket / POST /api/ai/upload/ticket) and upload to the new URL.", nil)
		case errors.Is(err, errTicketInFlight):
			ticketRefusal(w, http.StatusConflict, "ticket_in_use",
				"Another transfer is already using this ticket. Wait for it to finish — do not start a second upload to the same URL.", nil)
		default:
			// Unknown and already-redeemed are the SAME answer: a consumed
			// ticket must not be distinguishable from one that never existed.
			ticketRefusal(w, http.StatusNotFound, "ticket_not_found",
				"Unknown ticket, or it was already used (a ticket accepts exactly one upload). Mint a new one and upload to the new URL.", nil)
		}
		return
	}

	src, size, cleanup, err := ticketBody(r)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		h.tickets.release(tok)
		ticketRefusal(w, http.StatusBadRequest, err.Error(),
			"Send the file either as the raw request body (`curl -T <file> <url>`) or as multipart with a field named `file` (`curl -F file=@<file> <url>`). This ticket is still valid.", nil)
		return
	}
	if size < 0 {
		h.tickets.release(tok)
		// Without a length the staging decision and the driver write have
		// nothing to size the object by. curl -T always sends one.
		ticketRefusal(w, http.StatusLengthRequired, "content_length_required",
			"The body arrived chunked, with no Content-Length. Upload with `curl -T <file> <url>`, which always sends a length. This ticket is still valid.", nil)
		return
	}
	if size > t.MaxBytes {
		h.tickets.release(tok)
		ticketRefusal(w, http.StatusRequestEntityTooLarge, "file_too_large",
			"The file is larger than this ticket allows. THE TICKET IS STILL VALID — retry the same URL with a file of at most max_bytes; you do not need a new ticket.",
			map[string]any{"max_bytes": t.MaxBytes, "sent_bytes": size})
		return
	}

	// Run the write as the minter: their grants decide, their quota is billed,
	// and the file lands owned by them. The uploader stays anonymous.
	ctx := r.Context()
	if t.Owner != nil {
		ctx = auth.WithUser(ctx, t.Owner)
		ctx = quotastore.WithOwner(ctx, t.Owner.ID)
	}

	entry, err := h.ops.WriteStream(ctx, t.Path, src, size)
	if err != nil {
		h.tickets.release(tok)
		h.failWrite(w, err, t.Path, size)
		return
	}
	h.tickets.consume(tok)
	writeJSON(w, http.StatusOK, map[string]any{"entry": entry})
}

// failWrite reports a failed ticket upload the way lesson #39 requires: a
// storage outage is a 503 with its own code (never a bare 500 that reads as
// "your request was wrong"), a full disk is a 507, and either way the cause
// lands in the log with enough context to find it.
func (h *TicketUpload) failWrite(w http.ResponseWriter, err error, dest string, size int64) {
	code, status := "storage_unavailable", http.StatusServiceUnavailable
	switch {
	case errors.Is(err, quota.ErrQuotaExceeded):
		code, status = "quota_exceeded", http.StatusInsufficientStorage
	case errors.Is(err, storage.ErrReadOnly):
		code, status = "read_only", http.StatusForbidden
	case errors.Is(err, errAIForbidden):
		code, status = "forbidden", http.StatusForbidden
	}
	slog.Error("upload ticket: write failed",
		slog.String("dest", dest),
		slog.Int64("size", size),
		slog.String("code", code),
		slog.String("err", err.Error()),
	)
	hint := map[string]string{
		"storage_unavailable": "The storage backend refused the write — this is not your request. The ticket is still valid: retry it later, and tell the user storage is down if it keeps failing.",
		"quota_exceeded":      "The ticket owner is out of storage. Free space or raise the quota; retrying will not help until then.",
		"read_only":           "The destination storage is read-only. Mint a ticket for a writable storage instead.",
		"forbidden":           "The ticket owner no longer has permission to write there. Ask for access, or mint a ticket for a path you can write.",
	}[code]
	ticketRefusal(w, status, code, hint, nil)
}

// ticketBody picks the bytes out of either request shape and reports the exact
// size, which WriteStream needs. The multipart part is returned as an
// io.ReadCloser the caller closes via cleanup.
func ticketBody(r *http.Request) (src io.Reader, size int64, cleanup func(), err error) {
	if hasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		// Parts above the in-memory limit spill to $TMPDIR and outlive the
		// response unless removed here — the same leak that filled fm.example.com
		// with 29 GB of multipart-* temp files (2026-08-09).
		if perr := r.ParseMultipartForm(32 << 20); perr != nil {
			return nil, 0, func() {
				if r.MultipartForm != nil {
					_ = r.MultipartForm.RemoveAll()
				}
			}, errors.New("bad multipart")
		}
		clean := func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}
		f, fh, ferr := r.FormFile("file")
		if ferr != nil {
			return nil, 0, clean, errors.New("missing file field")
		}
		return f, fh.Size, func() { _ = f.Close(); clean() }, nil
	}
	return r.Body, r.ContentLength, nil, nil
}
