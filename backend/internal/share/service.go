// Package share manages public download tokens with optional PIN
// protection, expiry, and download caps.
package share

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ErrExpired is returned by Resolve when a share is past its TTL or
// download cap.
var ErrExpired = errors.New("share: expired")

// ErrBadPIN is returned when an incorrect PIN is supplied.
var ErrBadPIN = errors.New("share: bad pin")

// Service provides high-level share operations.
type Service struct {
	store db.Store
}

// NewService constructs a share Service.
func NewService(store db.Store) *Service { return &Service{store: store} }

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
}

// Create issues a fresh share token.
func (s *Service) Create(ctx context.Context, opts CreateOpts) (*model.Share, error) {
	if opts.NodeID == 0 {
		return nil, errors.New("share: missing node_id")
	}
	tok, err := randomToken(16)
	if err != nil {
		return nil, err
	}
	pinHash := ""
	if opts.PIN != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(opts.PIN), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		pinHash = string(h)
	}
	kind := opts.Kind
	if kind == "" {
		kind = model.ShareKindDownload
	}
	sh := &model.Share{
		NodeID:       opts.NodeID,
		Token:        tok,
		PinHash:      pinHash,
		ExpiresAt:    opts.ExpiresAt,
		MaxDownloads: opts.MaxDownloads,
		CreatedBy:    opts.CreatedBy,
		CreatedVia:   opts.CreatedVia,
		Kind:         kind,
		MaxUploads:   opts.MaxUploads,
		DropSettings: opts.DropSettings,
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
