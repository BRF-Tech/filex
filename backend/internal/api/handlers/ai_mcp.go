package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/version"
)

// AIMCP exposes filex as a Model Context Protocol server over streamable
// HTTP (JSON-RPC). The same aiOps core that backs the REST handler powers
// each MCP tool, so AI agents can drive filex directly while work.example.com's
// FilexClient uses the REST surface.
//
// Transport: streamable HTTP in stateless + JSON-response mode (one
// JSON-RPC request → one JSON response), which is what laravel/mcp's HTTP
// client speaks. Mounted at POST/GET /api/ai/mcp behind
// auth.APITokenMiddleware + RequireScope("mcp").
//
// Auth model: the route's middleware has already validated the AI token and
// stashed the principal on the request context by the time getServer runs.
// getServer reads that principal and builds a per-request server whose tools
// close over an aiOps bound to the store + resolver. If the principal is
// absent (should never happen behind the middleware) getServer returns nil
// and the SDK serves 400.
type AIMCP struct {
	store     db.Store
	resolver  func(int64) (storage.Driver, error)
	admin     *AIAdmin
	share     *share.Service
	publicURL string
	tenants   tenanturl.Resolver
	acl       *acl.Resolver
	thumbs    *thumb.Pipeline
	staged    *StagedUpload
	// index is held rather than pushed into the core once, because a fresh
	// aiOps is built per tool call below.
	index   *search.Index
	body    *filebody.Resolver
	tickets *uploadTicketStore
	// doors: the explorer's handlers the copy, app, operations, trash,
	// versions, archive and link tools run (ai_doors.go).
	doors   *AIDoors
	handler http.Handler
}

// AttachDoors wires the explorer's handlers the door tools run in process.
func (h *AIMCP) AttachDoors(d *AIDoors) { h.doors = d }

// AttachACL wires the RBAC resolver so every per-request MCP tool op is gated
// by the bound user's grants + role ceiling (same enforcement as the REST AI).
func (h *AIMCP) AttachACL(r *acl.Resolver) { h.acl = r }

// AttachThumbs wires the thumbnail pipeline so MCP tool writes dispatch
// generation like manager uploads (nil = thumbnails skipped).
func (h *AIMCP) AttachThumbs(p *thumb.Pipeline) { h.thumbs = p }

// AttachSearchIndex wires the search index; every aiOps this handler builds
// per tool call gets it.
func (h *AIMCP) AttachSearchIndex(idx *search.Index) { h.index = idx }

// AttachStaged routes MCP tool writes above the chunk threshold through the
// staging area, so an agent gets the same acknowledge-then-transfer behaviour
// as the browser and the CLI (nil = synchronous writes).
func (h *AIMCP) AttachStaged(s *StagedUpload) { h.staged = s }

// AttachBody wires the byte-source resolver so MCP reads serve a file that is
// still being transferred out of staging.
func (h *AIMCP) AttachBody(b *filebody.Resolver) { h.body = b }

// AttachTickets wires the shared upload-ticket store so the MCP surface can
// mint credential-free upload URLs for large local files.
func (h *AIMCP) AttachTickets(s *uploadTicketStore) { h.tickets = s }

// AttachTenants wires the shared origin resolver (internal/tenanturl). The
// per-call ops core below is rebuilt for every request, so the resolver is
// held here and stamped onto each one.
func (h *AIMCP) AttachTenants(rv tenanturl.Resolver) { h.tenants = rv }

// NewAIMCP builds the MCP HTTP handler. `admin` powers the admin_* tools,
// which are only registered for tokens carrying the `admin` scope; pass nil
// to disable the admin tool surface entirely. shareSvc + publicURL power the
// file_share / file_unshare tools.
func NewAIMCP(store db.Store, resolver func(int64) (storage.Driver, error), admin *AIAdmin, shareSvc *share.Service, publicURL string) *AIMCP {
	h := &AIMCP{
		store: store, resolver: resolver, admin: admin, share: shareSvc,
		publicURL: publicURL,
		tenants:   tenanturl.New(store, publicURL, false),
	}
	h.handler = mcp.NewStreamableHTTPHandler(h.getServer, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
	return h
}

// ServeHTTP delegates to the SDK's streamable handler. The client's address
// is resolved here, once, and carried on the context: a tool call has no
// *http.Request of its own, and the audit rows its writes leave name the
// address like every other door's (mcpAuditWrite, AIAdmin.auditInvoke).
func (h *AIMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := clientip.WithIP(r.Context(), clientip.FromRequest(r))
	// …and the origin a door tool's handler reads (the host a link is minted
	// on, the language a job's label is in): ai_doors.go withDoorOrigin.
	r = r.WithContext(withDoorOrigin(ctx, r))
	h.handler.ServeHTTP(w, r)
}

// getServer constructs a fresh MCP server per request, with tools bound to
// the AI token's principal. Returning nil yields a 400 from the SDK.
func (h *AIMCP) getServer(r *http.Request) *mcp.Server {
	if auth.UserFrom(r.Context()) == nil {
		return nil
	}
	ops := newAIOps(h.store, h.resolver, h.share, h.publicURL)
	ops.attachSearchIndex(h.index)
	ops.tenants = h.tenants
	ops.acl = h.acl
	ops.thumbs = h.thumbs
	ops.staged = h.staged
	ops.body = h.body
	ops.tickets = h.tickets
	ops.doors = h.doors
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "filex",
		Title:   "filex file manager",
		Version: version.String(),
	}, nil)
	registerFilexTools(srv, ops, h.searchIndex())
	// The file tools need the verbs their /api/ai twins need; a tool the token
	// cannot use is not offered at all, like an admin_* tool to a non-admin.
	withdrawUngrantedFileTools(srv, auth.TokenFrom(r.Context()))

	// Admin tools are gated by the `admin` token scope (on top of the route's
	// `mcp` scope) and by the token not being confined to a folder — the same
	// rule as the /api/ai/admin routes, from the same place
	// (auth.TokenMayAdminister). A token that fails it never sees admin_* in
	// tools/list, and so cannot call one.
	if tok := auth.TokenFrom(r.Context()); h.admin != nil && auth.TokenMayAdminister(tok) {
		principal := h.admin.elevatedPrincipal(auth.UserFrom(r.Context()))
		registerAdminTools(srv, h.admin, principal)
	}
	return srv
}

// fileToolVerb is the token verb each file tool needs: the verb its REST twin
// under /api/ai asks (routes.go). "" = discovery, which needs none, like
// GET /api/ai/root.
//
// ⚠⚠ The route that serves these tools asks only `mcp`; the verb of each
// operation is asked here, so a `read,mcp` token is read-only on MCP exactly
// as on /api/ai. A new file tool needs a row: ai_mcp_verbs_internal_test lists
// what registerFilexTools offers and goes red for a tool without one.
var fileToolVerb = map[string]string{
	"file_root":          "",
	"file_list":          auth.VerbRead,
	"file_info":          auth.VerbRead,
	"file_read":          auth.VerbRead,
	"file_search":        auth.VerbRead,
	"file_tags":          auth.VerbRead, // setting tags also asks `write` (aiOps.Tags)
	"file_write":         auth.VerbWrite,
	"file_upload_ticket": auth.VerbWrite,
	"file_mkdir":         auth.VerbWrite,
	"file_move":          auth.VerbWrite,
	"file_share":         auth.VerbWrite,
	"file_unshare":       auth.VerbWrite,
	"file_zip":           auth.VerbWrite,
	"file_unzip":         auth.VerbWrite,
	"file_delete":        auth.VerbDelete,

	// The explorer's operations (ai_doors.go, registerDoorTools).
	"file_copy":            auth.VerbWrite,
	"app_actions":          auth.VerbRead,
	"app_run":              auth.VerbWrite,
	"file_convert":         auth.VerbWrite,
	"ops_list":             auth.VerbRead,
	"op_get":               auth.VerbRead,
	"op_cancel":            auth.VerbWrite,
	"trash_list":           auth.VerbRead,
	"trash_restore":        auth.VerbWrite,
	"file_versions":        auth.VerbRead,
	"file_version_restore": auth.VerbWrite,
	"file_snapshot":        auth.VerbWrite,
	"archive_create":       auth.VerbWrite,
	"archive_extract":      auth.VerbWrite,
	"share_list":           auth.VerbRead,
	"file_request_create":  auth.VerbWrite,

	// The bell, stars, comments and item permissions (ai_doors_people.go).
	// Marking one's own notices read is bookkeeping (`read`, as the bell's
	// route); a star, a comment and a grant change something (`write`).
	"notifications_list":     auth.VerbRead,
	"notification_read":      auth.VerbRead,
	"file_star":              auth.VerbWrite,
	"file_comments":          auth.VerbRead,
	"file_comment_add":       auth.VerbWrite,
	"file_comment_delete":    auth.VerbWrite,
	"file_permissions":       auth.VerbRead,
	"file_permission_users":  auth.VerbRead,
	"file_permission_set":    auth.VerbWrite,
	"file_permission_revoke": auth.VerbWrite,
}

// withdrawUngrantedFileTools removes every file tool whose verb tok does not
// hold. The MCP route is token-only, so tok is never nil behind it; a nil tok
// (a server built by hand) withdraws everything that needs a verb.
func withdrawUngrantedFileTools(srv *mcp.Server, tok *model.APIToken) {
	var drop []string
	for name, verb := range fileToolVerb {
		if verb != "" && !tok.HasScope(verb) {
			drop = append(drop, name)
		}
	}
	if len(drop) > 0 {
		srv.RemoveTools(drop...)
	}
}

// ───── tool input/output types ─────

type mcpListIn struct {
	Path string `json:"path,omitempty" jsonschema:"adapter://dir path to list; empty = first storage root"`
}
type mcpEntriesOut struct {
	Entries []aiEntry `json:"entries"`
}

// mcpRootIn is the (empty) input for file_root.
type mcpRootIn struct{}

type mcpReadIn struct {
	Path string `json:"path" jsonschema:"adapter://file path to read"`
}
type mcpReadOut struct {
	Path     string `json:"path"`
	Mime     string `json:"mime"`
	Encoding string `json:"encoding"` // "utf-8" | "base64"
	Content  string `json:"content"`
}

type mcpWriteIn struct {
	Path          string `json:"path" jsonschema:"adapter://file path to create or overwrite"`
	Content       string `json:"content,omitempty" jsonschema:"UTF-8 text content (use content_base64 for binary)"`
	ContentBase64 string `json:"content_base64,omitempty" jsonschema:"base64-encoded binary content"`
	// AllowPlaintext lets a write land in an end-to-end encrypted folder
	// unencrypted (ai_e2e.go plaintextRefusal).
	AllowPlaintext bool `json:"allow_plaintext,omitempty" jsonschema:"only for a path inside an end-to-end encrypted folder: store the bytes there UNENCRYPTED on purpose (filex holds no key). Without it such a write fails with E2E_PLAINTEXT_REFUSED"`
}
type mcpEntryOut struct {
	Entry *aiEntry `json:"entry"`
}

type mcpUploadTicketIn struct {
	Path             string `json:"path" jsonschema:"adapter://file path the upload will land at (a FILE path, not a folder)"`
	ExpiresInSeconds int    `json:"expires_in_seconds,omitempty" jsonschema:"how long the URL stays valid (default 1800, max 86400)"`
	MaxBytes         int64  `json:"max_bytes,omitempty" jsonschema:"optional lower ceiling than the server maximum"`
	AllowPlaintext   bool   `json:"allow_plaintext,omitempty" jsonschema:"only for a path inside an end-to-end encrypted folder: store the upload there UNENCRYPTED on purpose. Without it the ticket is refused with E2E_PLAINTEXT_REFUSED"`
}
type mcpUploadTicketOut struct {
	URL        string `json:"url"`
	Path       string `json:"path"`
	MaxBytes   int64  `json:"max_bytes"`
	ExpiresAt  string `json:"expires_at"`
	Curl       string `json:"curl"`
	PowerShell string `json:"powershell"`
	Next       string `json:"next"`
}

type mcpPathIn struct {
	Path string `json:"path" jsonschema:"adapter://path"`
}
type mcpOKOut struct {
	OK bool `json:"ok"`
}

type mcpMoveIn struct {
	Src string `json:"src" jsonschema:"source adapter://path"`
	Dst string `json:"dst" jsonschema:"destination adapter://path (same storage)"`
}

type mcpSearchIn struct {
	Path  string `json:"path,omitempty" jsonschema:"adapter:// scope for the search; empty = first storage"`
	Query string `json:"query" jsonschema:"text to match against file/dir names (and file contents unless content=false); separators and typos are forgiven, and tag:NAME / -tag:NAME filter by tag"`
	// Content is a *bool so an omitted argument defaults to TRUE (the
	// frozen v0.2 contract) while an explicit false still turns it off.
	Content *bool `json:"content,omitempty" jsonschema:"also match inside extracted file contents and return snippets (default true)"`
}

// mcpTagsIn is the file_tags input. `Set` is a pointer so "not given" (read)
// and "given, empty" (clear every tag you can see) are different calls.
type mcpTagsIn struct {
	Path string     `json:"path" jsonschema:"adapter://path of the file or folder"`
	Set  *[]tagItem `json:"set,omitempty" jsonschema:"omit to only read. When given, the file's tags that YOU can see become exactly this list ([] clears them). Every item is {name, kind}: kind personal = only you see it; kind team = everyone in your tenant who can see the file, and adding or removing one needs edit permission on it. Names keep their capitals; matching ignores case (Turkish I/ı/İ/i count as one letter)."`
}

// mcpSearchEntry is one file_search hit: the classic entry plus the v0.2
// content-search fields.
type mcpSearchEntry struct {
	aiEntry
	// Snippet is a short plain-text fragment around a content match with
	// matched terms wrapped in « » (empty for name-only hits, never HTML).
	Snippet string `json:"snippet,omitempty"`
	// Matched reports which side(s) hit: "name" | "content" | "both".
	Matched string `json:"matched,omitempty"`
}

type mcpSearchOut struct {
	Entries []mcpSearchEntry `json:"entries"`
}

type mcpShareIn struct {
	Path          string `json:"path" jsonschema:"adapter://file-or-folder to share (folders download as a zip)"`
	Pin           bool   `json:"pin,omitempty" jsonschema:"generate a random PIN to protect the link"`
	ExpiresInDays int    `json:"expires_in_days,omitempty" jsonschema:"link expiry in days (0 = never)"`
	MaxDownloads  int    `json:"max_downloads,omitempty" jsonschema:"max downloads (0 = unlimited)"`
}

type mcpUnshareIn struct {
	Token string `json:"token" jsonschema:"the share token to revoke"`
}

type mcpZipIn struct {
	Sources        []string `json:"sources" jsonschema:"adapter:// paths to pack (files and/or folders; folders are zipped recursively)"`
	Dest           string   `json:"dest" jsonschema:"adapter:// path of the .zip to create (same storage as the sources)"`
	AllowPlaintext bool     `json:"allow_plaintext,omitempty" jsonschema:"only for a dest inside an end-to-end encrypted folder: store the zip there UNENCRYPTED on purpose"`
}

type mcpUnzipIn struct {
	Src            string `json:"src" jsonschema:"adapter:// path of the .zip to extract"`
	DestDir        string `json:"dest_dir" jsonschema:"adapter:// directory to extract into (same storage as src)"`
	AllowPlaintext bool   `json:"allow_plaintext,omitempty" jsonschema:"only when files would land inside an end-to-end encrypted folder: extract them there UNENCRYPTED on purpose"`
}
type mcpUnzipOut struct {
	Extracted int `json:"extracted"` // number of files written
	// Refused: members the pre-write snapshot guard turned away (transient,
	// system-caused) rather than skipped for a permanent reason such as a
	// zip-slip entry or a kind conflict.
	Refused int `json:"refused,omitempty"`
}

// searchIndex digs the shared Bleve index out of the admin surface — AIMCP's
// constructor (frozen in routes.go for this wave) doesn't carry the index
// directly, and the admin wrapper is built unconditionally with the same
// *search.Index instance the whole server uses. nil = name-only search.
func (h *AIMCP) searchIndex() *search.Index {
	if h.admin == nil || h.admin.searchAdm == nil {
		return nil
	}
	return h.admin.searchAdm.Index
}

// registerFilexTools wires every MCP tool onto srv, bound to ops. idx (may
// be nil) powers file_search's content mode.
func registerFilexTools(srv *mcp.Server, ops *aiOps, idx *search.Index) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_root",
		Description: "Report your access scope FIRST: the confinement root you're locked to (if any) and the storage adapter names you can address. If confined, address files with bare relative paths (they resolve UNDER your root) or full adapter://root/... paths - never guess adapter names.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ mcpRootIn) (*mcp.CallToolResult, aiRootInfo, error) {
		return nil, ops.RootInfo(ctx), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "file_list",
		Description: "List files and folders in a directory. Path is adapter://dir (adapter = storage name); empty path lists the first storage's root. " +
			"An entry with encrypted: true is end-to-end encrypted (inside the encrypted folder named by e2e_root, or a single encrypted .fxe file): filex holds no key, so its content cannot be read here.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpListIn) (*mcp.CallToolResult, mcpEntriesOut, error) {
		entries, err := ops.List(ctx, in.Path)
		if err != nil {
			return toolErr[mcpEntriesOut](err)
		}
		return nil, mcpEntriesOut{Entries: entries}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_info",
		Description: "Get metadata (size, mime, type, modified time) for a single file or folder, including encrypted / e2e_root as file_list reports them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPathIn) (*mcp.CallToolResult, mcpEntryOut, error) {
		e, err := ops.Info(ctx, in.Path)
		if err != nil {
			return toolErr[mcpEntryOut](err)
		}
		return nil, mcpEntryOut{Entry: e}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "file_read",
		Description: "Read a file's contents. Returns UTF-8 text when the bytes are valid UTF-8, otherwise base64. Files above 8 MiB are rejected - use the REST download endpoint for those. " +
			"An end-to-end encrypted file (encrypted: true) is refused with E2E_ENCRYPTED: filex holds no key; the person decrypts it in the filex web UI or with `filex decrypt`.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpReadIn) (*mcp.CallToolResult, mcpReadOut, error) {
		data, mime, err := ops.ReadBytes(ctx, in.Path)
		if err != nil {
			return toolErr[mcpReadOut](err)
		}
		out := mcpReadOut{Path: in.Path, Mime: mime}
		if utf8.Valid(data) {
			out.Encoding = "utf-8"
			out.Content = string(data)
		} else {
			out.Encoding = "base64"
			out.Content = base64.StdEncoding.EncodeToString(data)
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "file_write",
		Description: "Create or overwrite a file from content you produce here: UTF-8 text in `content`, or small binary as base64 in `content_base64`. These bytes travel inside the tool call, so a file that already exists on YOUR disk - anything more than ~1 MB - must NOT be sent this way: call `file_upload_ticket` instead and stream it with curl. " +
			"A path inside an end-to-end encrypted folder is refused with E2E_PLAINTEXT_REFUSED, because filex would store the bytes there unencrypted; set allow_plaintext only when that is what the person wants.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpWriteIn) (*mcp.CallToolResult, mcpEntryOut, error) {
		var data []byte
		if in.ContentBase64 != "" {
			b, derr := base64.StdEncoding.DecodeString(in.ContentBase64)
			if derr != nil {
				return toolErr[mcpEntryOut](errors.New("bad base64: " + derr.Error()))
			}
			data = b
		} else {
			data = []byte(in.Content)
		}
		e, err := ops.Write(withPlaintextConsent(ctx, in.AllowPlaintext), in.Path, data)
		if err != nil {
			return toolErr[mcpEntryOut](err)
		}
		mcpAuditWrite(ctx, ops.store, "file_write")
		return nil, mcpEntryOut{Entry: e}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "file_upload_ticket",
		Description: "Upload a LOCAL file of any size (video, dataset, spreadsheet, archive) without its bytes " +
			"passing through this conversation. Returns a short-lived, credential-free URL plus the exact `curl` " +
			"line to run: `curl -T <local-file> <url>`. The destination is fixed by this call, the URL accepts " +
			"exactly one upload and needs NO token, so an agent without filex credentials can still finish the " +
			"transfer. Use this whenever the file is already on disk - never base64 it into file_write. Run the " +
			"returned line on the machine that HOLDS the file (a `powershell` variant is returned too); if you " +
			"cannot run commands at all, hand the line to the user. Confirm the result with file_info afterwards.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpUploadTicketIn) (*mcp.CallToolResult, mcpUploadTicketOut, error) {
		info, err := ops.CreateUploadTicket(ctx, uploadTicketRequest{
			Path:             in.Path,
			ExpiresInSeconds: in.ExpiresInSeconds,
			MaxBytes:         in.MaxBytes,
			AllowPlaintext:   in.AllowPlaintext,
		})
		if err != nil {
			return toolErr[mcpUploadTicketOut](err)
		}
		mcpAuditWrite(ctx, ops.store, "file_upload_ticket")
		return nil, mcpUploadTicketOut{
			URL:        info.URL,
			Path:       info.Path,
			MaxBytes:   info.MaxBytes,
			ExpiresAt:  info.ExpiresAt,
			Curl:       info.Curl,
			PowerShell: info.PowerShell,
			Next:       info.Next,
		}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_delete",
		Description: "Soft-delete a file or folder (moved to filex trash, recoverable from the UI).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPathIn) (*mcp.CallToolResult, mcpOKOut, error) {
		if err := ops.Delete(ctx, in.Path); err != nil {
			return toolErr[mcpOKOut](err)
		}
		mcpAuditWrite(ctx, ops.store, "file_delete")
		return nil, mcpOKOut{OK: true}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "file_move",
		// ⚠⚠ "Never overwrites" is here, in the text the model actually reads,
		// and not only in docs/MCP.md. An agent that believes a move replaces
		// the destination reports the path it asked for, and after a
		// de-collision that path holds somebody else's file — it would tell its
		// user the file is somewhere it is not. The stale "within the same
		// storage" this line used to say was the same kind of lie in the other
		// direction: cross-storage moves have worked since v0.27.0.
		Description: "Move or rename a file/folder, within a storage or across two (the bytes are copied and verified, then the source is removed). Never overwrites: if dst is taken the item lands on a free name beside it (rapor-copy.txt), so use the returned entry.path - it may differ from what you asked for. entry.type says what moved: \"dir\" for a folder, \"file\" for a file. Moving an item onto its own path does nothing. A folder moved across storages that holds links filex cannot follow is copied WITHOUT them and its source is KEPT: then entry.source_kept is true and entry.left_behind names each one - a success, do not retry.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpMoveIn) (*mcp.CallToolResult, mcpEntryOut, error) {
		e, err := ops.Move(ctx, in.Src, in.Dst)
		if err != nil {
			return toolErr[mcpEntryOut](err)
		}
		mcpAuditWrite(ctx, ops.store, "file_move")
		return nil, mcpEntryOut{Entry: e}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_mkdir",
		Description: "Create a directory at the given adapter://path.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPathIn) (*mcp.CallToolResult, mcpEntryOut, error) {
		e, err := ops.Mkdir(ctx, in.Path)
		if err != nil {
			return toolErr[mcpEntryOut](err)
		}
		mcpAuditWrite(ctx, ops.store, "file_mkdir")
		return nil, mcpEntryOut{Entry: e}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_search",
		Description: "Search file/folder names AND (by default) inside extracted file contents within a storage. Name matching is forgiving: `.`, `-`, `_` and a space are interchangeable (`invoice 2026` finds `invoice_2026.pdf`), every word must match, and one typo is tolerated. A query may carry `tag:<name>` / `-tag:<name>` filters, which narrow to (or exclude) files carrying that tag - your personal tag of that name or your team's, both count; a tag that does not exist returns nothing. Results are ranked: exact filename, prefix, name, path, fuzzy, then content-only. Content hits include a plain-text snippet with matches wrapped in « ». Pass content=false for the old name-only behavior.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSearchIn) (*mcp.CallToolResult, mcpSearchOut, error) {
		withContent := in.Content == nil || *in.Content
		entries, err := mcpSearch(ctx, ops, idx, in.Path, in.Query, withContent)
		if err != nil {
			return toolErr[mcpSearchOut](err)
		}
		return nil, mcpSearchOut{Entries: entries}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_tags",
		Description: "Read or set a file's tags. Tags come in two kinds and every tag says which: `personal` (only you - the token's user - see it, like a star) and `team` (shared with everyone in your tenant who can see the file; adding or removing one needs edit permission, see can_edit_team). Without `set` it only reads. With `set` the tags you can see become exactly that list - always name the kind of each; there is no default. Other people's personal tags and other tenants' tags are never shown or touched.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpTagsIn) (*mcp.CallToolResult, aiTagsResult, error) {
		res, err := ops.Tags(ctx, in.Path, in.Set)
		if err != nil {
			return toolErr[aiTagsResult](err)
		}
		if in.Set != nil {
			mcpAuditWrite(ctx, ops.store, "file_tags")
		}
		return nil, *res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "file_share",
		Description: "Create a public share link for a file or folder (folders download as a ZIP). Returns the URL + a one-time PIN if pin=true. Use this to hand a file to someone without filex access - do NOT stream large files back through file_read. " +
			"An end-to-end encrypted folder and anything inside it is never shared (E2E_ENCRYPTED). A single encrypted file (.fxe) is: the result says encrypted: true, and its recipient needs the file's password to open it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpShareIn) (*mcp.CallToolResult, aiShareResult, error) {
		res, err := ops.CreateShare(ctx, in.Path, in.Pin, in.ExpiresInDays, in.MaxDownloads)
		if err != nil {
			return toolErr[aiShareResult](err)
		}
		mcpAuditWrite(ctx, ops.store, "file_share")
		return nil, *res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "file_unshare",
		Description: "Revoke a public link or a file request by its token (file_share and file_request_create answer it; share_list lists yours).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpUnshareIn) (*mcp.CallToolResult, mcpOKOut, error) {
		if err := ops.RevokeShare(ctx, in.Token); err != nil {
			return toolErr[mcpOKOut](err)
		}
		mcpAuditWrite(ctx, ops.store, "file_unshare")
		return nil, mcpOKOut{OK: true}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "file_zip",
		Description: "Pack one or more files/folders into a .zip ON THE SERVER (folders recurse). The archive is written to storage at `dest` - the bytes never travel over MCP. To let someone download a big zip, call file_share on `dest`; do NOT file_read it. " +
			"Packing needs download permission on every source. Files inside an end-to-end encrypted folder cannot be packed into a zip outside it (E2E_BOUNDARY); the encrypted folder itself can, with its key file.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpZipIn) (*mcp.CallToolResult, mcpEntryOut, error) {
		e, err := ops.Zip(withPlaintextConsent(ctx, in.AllowPlaintext), in.Sources, in.Dest)
		if err != nil {
			return toolErr[mcpEntryOut](err)
		}
		mcpAuditWrite(ctx, ops.store, "file_zip")
		return nil, mcpEntryOut{Entry: e}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "file_unzip",
		Description: "Extract a .zip already in storage into dest_dir ON THE SERVER (zip-slip protected; every entry stays within your confinement root). Returns the number of files written. " +
			"Extracting into an end-to-end encrypted folder is refused with E2E_PLAINTEXT_REFUSED unless allow_plaintext is set.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpUnzipIn) (*mcp.CallToolResult, mcpUnzipOut, error) {
		n, refused, err := ops.Unzip(withPlaintextConsent(ctx, in.AllowPlaintext), in.Src, in.DestDir)
		if err != nil {
			return toolErr[mcpUnzipOut](err)
		}
		mcpAuditWrite(ctx, ops.store, "file_unzip")
		return nil, mcpUnzipOut{Extracted: n, Refused: refused}, nil
	})

	registerDoorTools(srv, ops)
}

// mcpWriteTwin maps every file tool that CHANGES something to the /api/ai
// route that does the same thing over REST. The tool's audit row is the one
// that route writes - the same action name, the same target type - so a write
// reads the same in the audit log whichever door the agent used; only `via`
// says which (auth.ViaMCP here, auth.ViaAPI there).
//
// ⚠⚠ A write tool missing here leaves no audit row at all: the MCP
// transport itself is not audited (auth.shouldAudit), since one POST carries a
// read or a write alike. ai_mcp_audit_internal_test goes red for a tool of
// fileToolVerb that needs a write verb and has no twin.
var mcpWriteTwin = map[string]string{
	"file_write":         "/api/ai/upload",
	"file_upload_ticket": "/api/ai/upload/ticket",
	"file_mkdir":         "/api/ai/mkdir",
	"file_move":          "/api/ai/move",
	"file_delete":        "/api/ai/delete",
	"file_tags":          "/api/ai/tags", // only with `set`; reading tags is a read
	"file_share":         "/api/ai/share",
	"file_unshare":       "/api/ai/unshare",
	"file_zip":           "/api/ai/zip",
	"file_unzip":         "/api/ai/unzip",

	"file_copy":            "/api/ai/copy",
	"app_run":              "/api/ai/apps/run", // app_plugin.action_run once queued
	"file_convert":         "/api/ai/convert",  // app_plugin.action_run once queued
	"op_cancel":            "/api/ai/ops/{id}/cancel",
	"trash_restore":        "/api/ai/trash/restore",
	"file_version_restore": "/api/ai/versions/restore",
	"file_snapshot":        "/api/ai/versions/snapshot",
	"archive_create":       "/api/ai/archive/create",
	"archive_extract":      "/api/ai/archive/extract",
	"file_request_create":  "/api/ai/share/request",

	"file_star":              "/api/ai/star",
	"file_comment_add":       "/api/ai/comments",
	"file_comment_delete":    "/api/ai/comments/{id}/delete",
	"file_permission_set":    "/api/ai/permissions",
	"file_permission_revoke": "/api/ai/permissions/{id}/revoke",
}

// mcpAuditWrite writes the audit row of one successful write tool call.
//
// Exactly what the REST twin's row holds: the action and target type
// auth.ActionForPath names for it, the token's user, the client's address,
// `token_id` / `token_username` - and `via: mcp`. No path is added: the REST
// row carries none either, so neither door can put a name outside a confined
// token's root into a log the administrator reads as that token's work.
// Best-effort, like the middleware: a failed insert never fails the tool.
func mcpAuditWrite(ctx context.Context, store db.Store, tool string) {
	mcpAuditRow(ctx, store, tool, "", nil)
}

// mcpAuditRow is the row the /api/ai group's AuditMiddleware writes for the
// tool's REST twin, written for the tool: the action its route maps to (id
// fills a `{id}` in the twin, as chi would), renamed and detailed by the
// handler when it said more (SetAuditAction, SetAuditTarget, AddAuditDetail -
// the explorer's handlers the door tools run do, ai_doors.go), and stamped
// `via: mcp`. detail may be nil.
func mcpAuditRow(ctx context.Context, store db.Store, tool, id string, detail *auth.AuditDetail) {
	twin, ok := mcpWriteTwin[tool]
	if !ok || store == nil {
		return
	}
	twin = strings.Replace(twin, "{id}", id, 1)
	action, targetType, targetID := auth.ActionForPath(http.MethodPost, twin, id, "")
	if a, tt := detail.Action(); a != "" {
		action, targetType = auth.DoorAction(a, false), tt
	}
	if action == "" {
		return
	}
	entry := &model.AuditEntry{
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		IP:         clientip.FromContext(ctx),
		CreatedAt:  time.Now(),
	}
	if u := auth.UserFrom(ctx); u != nil && u.ID > 0 {
		uid := u.ID
		entry.UserID = &uid
	}
	entry.Metadata = detail.Into(map[string]interface{}{"via": auth.ViaMCP})
	entry.TargetID, entry.Metadata = detail.ApplyTarget(entry.TargetID, entry.Metadata)
	entry.Metadata = auth.StampTokenDoor(ctx, entry.Metadata, auth.ViaMCP)
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := store.InsertAuditEntry(wctx, entry); err != nil {
		slog.Warn("mcp: audit insert failed", slog.String("tool", tool), slog.String("err", err.Error()))
	}
}

// mcpSearch backs the file_search tool. Name-only mode (content=false, or
// no live Bleve index) reuses aiOps.Search's SQL LIKE path unchanged; the
// content mode consults the index with scope=all and re-applies the SAME
// access filters aiOps.Search enforces — storage scoping via resolveStorage,
// the token's confinement root, and the bound user's RBAC grants — so a
// snippet can never leak text the caller couldn't reach by browsing.
func mcpSearch(ctx context.Context, ops *aiOps, idx *search.Index, p, query string, withContent bool) ([]mcpSearchEntry, error) {
	s, _, err := ops.resolveStorage(ctx, p)
	if err != nil {
		return nil, err
	}
	parsed := search.ParseQuery(query)
	tagFilter, err := aiTagFilter(ctx, ops, s.Name, parsed)
	if err != nil {
		return nil, err
	}
	nameEntries, err := aiNameSearch(ctx, ops, p, parsed, tagFilter)
	if err != nil {
		return nil, err
	}

	out := make([]mcpSearchEntry, 0, len(nameEntries))
	if !withContent || idx == nil || !idx.Enabled() {
		for _, e := range nameEntries {
			out = append(out, mcpSearchEntry{aiEntry: e, Matched: search.MatchedName})
		}
		return out, nil
	}

	root, confined := confine.RootFromToken(ctx)
	var set *acl.Set
	if ops.acl != nil {
		set, _ = ops.acl.LoadSet(ctx, auth.UserFrom(ctx), s)
	}

	seen := map[string]bool{}
	roots := newE2eRoots(ops.store)
	for _, hit := range idx.SafeSearchFiltered(ctx, parsed.Text, 200, search.ScopeAll, tagFilter.index) {
		n, gerr := ops.store.GetNode(ctx, hit.NodeID)
		if gerr != nil || n == nil || n.DeletedAt != nil || n.StorageID != s.ID {
			continue
		}
		// The index holds filex's own rows too (version snapshots, the
		// desktop's open-with working copies) and encrypted folders' key
		// files; they are never a result.
		if syspath.Hidden(n.Path) || syspath.Unlisted(n.Name) {
			continue
		}
		if confined && !root.Within(s.Name, n.Path) {
			continue
		}
		if set != nil && !set.CanSee(n.Path) {
			continue
		}
		e := aiEntry{
			Path: joinAdapterPath(s.Name, n.Path),
			Name: n.Name,
			Type: aiTypeOfNode(n.Type),
			Size: n.Size,
			Mime: n.Mime,
		}
		if n.BackendMtime != nil {
			e.LastModified = n.BackendMtime.UnixMilli()
		}
		markE2e(ctx, roots, s, n.Path, &e)
		out = append(out, mcpSearchEntry{aiEntry: e, Snippet: hit.Snippet, Matched: hit.Matched})
		seen[e.Path] = true
	}
	// Merge SQL LIKE name hits the index missed (e.g. rows written moments
	// ago that Bleve hasn't flushed) so content mode stays a superset of
	// the pre-v0.2 behavior.
	for _, e := range nameEntries {
		if !seen[e.Path] {
			out = append(out, mcpSearchEntry{aiEntry: e, Matched: search.MatchedName})
		}
	}
	return out, nil
}

// toolErr packs an error into an MCP tool error result (IsError=true) rather
// than a protocol error, so the model sees a readable message and can retry.
// A refusal with a wire code (aiErrCode) leads with it, `E2E_ENCRYPTED: …` or
// `NO_FREE_NAME: …`: the same word its /api/ai twin answers in `code`, for an
// agent to match on.
func toolErr[T any](err error) (*mcp.CallToolResult, T, error) {
	var zero T
	text := err.Error()
	if code := aiErrCode(err); code != "" {
		text = code + ": " + text
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, zero, nil
}

// compile-time guard: AIMCP is an http.Handler.
var _ http.Handler = (*AIMCP)(nil)

// aiTagFilterSet is a resolved `tag:` filter in the two shapes the AI
// surface needs it: the filter itself (node IDs to keep and to drop), for the
// index and for the database rows alike, and the tagged nodes a bare `tag:x`
// lists.
type aiTagFilterSet struct {
	index *search.Filter
	// tagged is every node carrying the inclusive tags; nil when no
	// inclusive tag was given.
	tagged []*model.Node
}

// aiTagFilter resolves the parsed tags against the database.
func aiTagFilter(ctx context.Context, ops *aiOps, storageName string, parsed search.Parsed) (aiTagFilterSet, error) {
	f, tagged, err := resolveTagFilter(ctx, ops.store, parsed)
	if err != nil {
		return aiTagFilterSet{}, err
	}
	return aiTagFilterSet{index: f, tagged: tagged}, nil
}

// aiNameSearch is the name half of every AI-surface search: GET
// /api/ai/search and the MCP file_search tool both go through it.
//
// It exists so the two cannot drift. They used to call aiOps.Search
// directly with the raw query, which meant `invoice 2026` found nothing
// and `tag:source` was read as a filename — the same product answering
// the same question differently depending on which door an agent came
// through.
//
// aiOps.Search fetches the plan's candidate rows — every word of the query
// is a condition in the database query — and the whole query is re-checked
// here by the scorer: the same two steps the index-less HTTP path takes. The
// tag filter is applied to the rows by node, include and exclude alike, the
// way tagFilterAccepts applies it on the HTTP fallback.
//
// ⚠ A bare `tag:x` is a listing, not a search: the tagged nodes ARE the
// answer, as on /api/files/search and the toolbar. It used to be the first
// 200 rows of the storage by name, filtered by the tag afterwards, so a
// tagged file that sorted past row 200 was never found. And a `-tag:x` was
// never applied here at all: entries carried no node id to test.
func aiNameSearch(ctx context.Context, ops *aiOps, p string, parsed search.Parsed, tags aiTagFilterSet) ([]aiEntry, error) {
	if parsed.Text == "" {
		return ops.Listed(ctx, p, tags.tagged, tags.index)
	}
	plan := search.PlanFallback(parsed.Text)
	entries, err := ops.Search(ctx, p, plan, tags.index)
	if err != nil {
		return nil, err
	}
	kept := entries[:0]
	for _, e := range entries {
		if plan.Accepts(e.Name, e.Path) {
			kept = append(kept, e)
		}
	}
	return kept, nil
}
