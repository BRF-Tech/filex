// Package share manages public download tokens with optional PIN
// protection, expiry, and download caps.
package share

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// ErrExpired is returned by Resolve when a share is past its TTL or
// download cap.
var ErrExpired = errors.New("share: expired")

// ErrBadPIN is returned when an incorrect PIN is supplied.
var ErrBadPIN = errors.New("share: bad pin")

// SettingKeyMaxTTLDays is the settings row holding the longest life a NEW
// share link may be given, in days. Absent = DefaultMaxTTLDays; "0" = no
// ceiling. Written by the admin Protection page (PATCH /api/admin/protection
// {share_max_ttl_days}) and seeded once from FILEX_SHARE_MAX_TTL.
const SettingKeyMaxTTLDays = "share.max_ttl_days"

// DefaultMaxTTLDays applies when the setting has never been written. Seven
// days: a link that is meant to be opened this week, and a folder archive
// that does not have to be kept warm for a month.
const DefaultMaxTTLDays = 7

// MaxTTLDaysLimit bounds the setting itself.
const MaxTTLDaysLimit = 3650

// Service provides high-level share operations.
type Service struct {
	store db.Store
	// secret signs the unlock cookie a visitor gets for answering a PIN
	// (pin.go). Empty falls back to a per-process key; AttachSecret wires the
	// instance secret.
	secret string
	// box seals the PIN so it can be handed back to the link's owner and to an
	// administrator (migration 00049, RevealPIN in pin.go). Built by
	// AttachSecret from the SAME FILEX_SECRET_KEY the app-plugin secret
	// settings (wasmplugin/registry.go) and the S3 access keys are sealed
	// under — one scheme, not a second one invented here.
	//
	// Never nil: NewService gives it a keyless Box, which refuses to Seal and
	// therefore degrades honestly on an instance with no key configured.
	box *secretbox.Box
}

// NewService constructs a share Service.
//
// The PIN box starts KEYLESS. secretbox.New("") returns a Box that refuses to
// Seal (ErrNoKey) rather than one that quietly stores plaintext, so a Service
// nobody called AttachSecret on mints links whose PIN can never be shown —
// which is exactly the truth about such an instance, and is said in those words
// by the endpoint rather than guessed at by the screen.
func NewService(store db.Store) *Service {
	box, _ := secretbox.New("")
	return &Service{store: store, box: box}
}

// CreateOpts is the set of fields a caller may supply when minting a share.
type CreateOpts struct {
	NodeID       int64
	PIN          string     // optional, hashed before persist
	ExpiresAt    *time.Time // optional
	MaxDownloads *int       // optional
	CreatedBy    *int64     // user ID
	CreatedVia   string     // token username of the creating API call (display-only)

	// Drop-link options (Kind == model.ShareKindDrop). Ignored/zero for a
	// normal download share.
	Kind         string  // "" defaults to download
	MaxUploads   *int    // cap on total files a drop link may receive
	DropSettings *string // JSON limits blob

	// App-plugin page options (migration 00046). Set only by the
	// `share_create` host function: an app's public page IS a share, so it is
	// minted here and inherits the expiry ceiling, the PIN gate, the visit
	// counter and the administrator's revoke like every other link. Zero here
	// means an ordinary share.
	PluginID  int64
	PageID    string
	Subject   string
	StateJSON string // the plugin's own durable record (≤ 64 KiB)
	FilesJSON string // [{ref,name,file,size,mime}] — the exposed copies
	// PurposeJSON is what the app says this link IS (wire.PagePurpose,
	// migration 00052) — how a list of links names it.
	PurposeJSON string

	// Token is the link's token when the caller ALREADY MINTED one and has
	// handed it out. Empty — every other caller — mints a fresh one here.
	//
	// ⚠ It exists for one case: a plugin may promise a share of a file its
	// own job is still writing (wasmplugin/public.go). Such a row cannot be
	// written until the bytes are committed and the node exists, but the
	// plugin needs the link NOW, to put in the mail it is composing. So the
	// token is minted with NewToken at the promise and spent here when the
	// output lands. Refused unless it has the shape every token has, so a
	// caller cannot choose a guessable one.
	Token string
}

// NewToken mints a share token in the shape every link carries: 32 lowercase
// hex characters from crypto/rand.
//
// Exported because a caller sometimes has to KNOW a link's token before the
// row that carries it can exist — see CreateOpts.Token. Nothing else should
// need it: Create mints its own.
func NewToken() (string, error) { return randomToken(16) }

// validToken is the shape NewToken produces, and the only shape Create will
// accept from a caller.
func validToken(tok string) bool {
	if len(tok) != 32 {
		return false
	}
	for _, r := range tok {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// MaxTTLDays returns the configured ceiling on a new link's life in days
// (0 = none). A missing or unparsable setting is the default, never "no
// ceiling": a typo in the settings table must not quietly mint permanent
// links.
func (s *Service) MaxTTLDays(ctx context.Context) int {
	v, err := s.store.GetSetting(ctx, SettingKeyMaxTTLDays)
	if err != nil || strings.TrimSpace(v) == "" {
		return DefaultMaxTTLDays
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 || n > MaxTTLDaysLimit {
		return DefaultMaxTTLDays
	}
	return n
}

// ClampExpiry applies the max-TTL ceiling to a requested expiry: nil (never)
// becomes now+max, a later date is pulled back to now+max, anything within
// the ceiling is returned as is. The bool reports whether the request was
// changed, so the caller can tell the user the link's real life. With no
// ceiling (maxDays == 0) the request is returned untouched.
func ClampExpiry(requested *time.Time, maxDays int, now time.Time) (*time.Time, bool) {
	if maxDays <= 0 {
		return requested, false
	}
	limit := now.Add(time.Duration(maxDays) * 24 * time.Hour)
	if requested == nil || requested.After(limit) {
		return &limit, true
	}
	return requested, false
}

// Create issues a fresh share token.
//
// The link's expiry is clamped to the max-TTL setting (see ClampExpiry): a
// request with no expiry gets one, a request past the ceiling is shortened.
// The returned Share carries the expiry that was actually stored. Links that
// already exist are never touched by this — only new ones.
func (s *Service) Create(ctx context.Context, opts CreateOpts) (*model.Share, error) {
	if opts.NodeID == 0 {
		return nil, errors.New("share: missing node_id")
	}
	opts.ExpiresAt, _ = ClampExpiry(opts.ExpiresAt, s.MaxTTLDays(ctx), time.Now())
	tok := strings.ToLower(strings.TrimSpace(opts.Token))
	if tok == "" {
		var err error
		if tok, err = NewToken(); err != nil {
			return nil, err
		}
	} else if !validToken(tok) {
		return nil, errors.New("share: token is not in the minted shape")
	}
	// The PIN is stored TWICE, for two different jobs (migration 00049):
	//
	//   pin_hash  bcrypt — what the visitor's guess is checked against. Still
	//             the only thing the gate consults (pin.go CheckPIN), so making
	//             the PIN recoverable did not make it cheaper to guess.
	//   pin_enc   secretbox — what the OWNER and an ADMIN can be shown again,
	//             through one audited endpoint. Sealed under FILEX_SECRET_KEY,
	//             never in clear, never in a listing.
	//
	// ⚠ No key configured ⇒ pin_enc stays empty and the link still works. It is
	// the one case where a missing key must NOT fail the write: refusing to
	// mint a share on an instance with no FILEX_SECRET_KEY would break sharing
	// outright to add a convenience.
	pinHash, pinEnc := "", ""
	if opts.PIN != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(opts.PIN), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		pinHash = string(h)
		if s.box.Enabled() {
			sealed, err := s.box.Seal(opts.PIN)
			if err != nil {
				return nil, err
			}
			pinEnc = sealed
		}
	}
	kind := opts.Kind
	if kind == "" {
		kind = model.ShareKindDownload
	}
	sh := &model.Share{
		NodeID:       opts.NodeID,
		Token:        tok,
		PinHash:      pinHash,
		PinEnc:       pinEnc,
		ExpiresAt:    opts.ExpiresAt,
		MaxDownloads: opts.MaxDownloads,
		CreatedBy:    opts.CreatedBy,
		CreatedVia:   opts.CreatedVia,
		Kind:         kind,
		MaxUploads:   opts.MaxUploads,
		DropSettings: opts.DropSettings,
		PluginID:     opts.PluginID,
		PageID:       opts.PageID,
		Subject:      opts.Subject,
		StateJSON:    opts.StateJSON,
		FilesJSON:    opts.FilesJSON,
		PurposeJSON:  opts.PurposeJSON,
	}
	return s.store.CreateShare(ctx, sh)
}

// Resolve looks up a share by token and applies expiry/PIN checks.
//
// pin may be empty when the share has no PIN configured. Returns
// ErrExpired or ErrBadPIN as appropriate.
func (s *Service) Resolve(ctx context.Context, token, pin string) (*model.Share, error) {
	sh, err := s.store.GetShareByToken(ctx, strings.ToLower(token))
	if err != nil {
		return nil, err
	}
	if sh.IsExpired(time.Now()) {
		return nil, ErrExpired
	}
	if sh.PinHash != "" {
		if err := bcrypt.CompareHashAndPassword([]byte(sh.PinHash), []byte(pin)); err != nil {
			return nil, ErrBadPIN
		}
	}
	return sh, nil
}

// CountOverMaxTTL counts the EXISTING live links that outlive the current
// ceiling — no expiry at all, or an expiry later than now+max. They are
// reported, never modified: the ceiling is a rule for new links, and a
// customer's link that was minted under the old rule keeps working. The
// number is what an operator needs to decide whether to revoke any by hand.
// 0 when there is no ceiling.
func (s *Service) CountOverMaxTTL(ctx context.Context, now time.Time) (int, error) {
	maxDays := s.MaxTTLDays(ctx)
	if maxDays <= 0 {
		return 0, nil
	}
	limit := now.Add(time.Duration(maxDays) * 24 * time.Hour)
	const page, maxShares = 500, 100000
	over := 0
	for offset := 0; offset < maxShares; offset += page {
		rows, total, err := s.store.ListAllShares(ctx, nil, true, page, offset)
		if err != nil {
			return 0, err
		}
		for _, sm := range rows {
			if sm.Share == nil {
				continue
			}
			if sm.Share.ExpiresAt == nil || sm.Share.ExpiresAt.After(limit) {
				over++
			}
		}
		if len(rows) < page || int64(offset+len(rows)) >= total {
			break
		}
	}
	return over, nil
}

// IncrementDownload bumps the counter — caller decides whether to call
// before or after streaming the file.
//
// ⚠ Not the cap's enforcement point. Serving first and counting afterwards
// leaves a window in which every concurrent request reads the same
// pre-download count and is waved through: measured on fm.example.com, a link
// capped at ONE download handed three complete files to three overlapping
// clients. Anything that is about to hand bytes to a visitor must call
// ReserveDownload instead.
func (s *Service) IncrementDownload(ctx context.Context, id int64) error {
	return s.store.IncrementShareDownload(ctx, id)
}

// ReserveDownload claims one download against the link's cap BEFORE the bytes
// are served, and reports whether it got one. False means the cap is spent and
// the caller must serve nothing.
//
// The context is detached from the request on purpose: the claim is the record
// that a file was handed out, and a client that hangs up mid-request must not
// be able to make that record disappear.
func (s *Service) ReserveDownload(ctx context.Context, id int64) (bool, error) {
	return s.store.ReserveShareDownload(context.WithoutCancel(ctx), id)
}

// ReleaseDownload returns a reserved slot. Call it ONLY when the serve failed
// before any byte reached the client — a partial download is still a download.
func (s *Service) ReleaseDownload(ctx context.Context, id int64) error {
	return s.store.ReleaseShareDownload(context.WithoutCancel(ctx), id)
}

// IncrementUpload bumps a drop link's received-file counter by n. Feeds the
// MaxUploads cap enforced by Share.IsExpired.
func (s *Service) IncrementUpload(ctx context.Context, id int64, n int) error {
	return s.store.IncrementShareUpload(ctx, id, n)
}

// ListByNode returns all shares pointing at a given node.
func (s *Service) ListByNode(ctx context.Context, nodeID int64) ([]*model.Share, error) {
	return s.store.ListSharesByNode(ctx, nodeID)
}

// Delete removes a share.
func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.store.DeleteShare(ctx, id)
}

// Cleanup deletes all expired shares — call from a periodic janitor.
func (s *Service) Cleanup(ctx context.Context) error {
	return s.store.DeleteExpiredShares(ctx)
}

// randomToken returns a hex-encoded random string of nBytes*2 length.
func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
