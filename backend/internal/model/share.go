package model

import "time"

// Share kinds. A "download" share grants outbound read access to a node
// (the classic /s/{token} link). A "drop" share is the inverse: a public
// upload link that lets anonymous visitors write files INTO a folder
// without ever seeing its contents (the /d/{token} file-drop link).
const (
	ShareKindDownload = "download"
	ShareKindDrop     = "drop"
)

// Share is a public token granting limited access to a node. For a
// download share this is read (download); for a drop share it is blind
// upload into the node (a directory) — see Kind.
type Share struct {
	ID      int64  `json:"id"`
	NodeID  int64  `json:"node_id"`
	Token   string `json:"token"`
	PinHash string `json:"-"`       // never serialized
	HasPin  bool   `json:"has_pin"` // computed
	// PinEnc is the SEALED PIN (internal/secretbox, AES-GCM under
	// FILEX_SECRET_KEY) — migration 00049. It exists so the link's owner and an
	// administrator can be told the PIN again; nothing ever verifies against
	// it, because PinHash above is still the gate (share/pin.go CheckPIN).
	//
	// ⚠ `json:"-"`, like the hash and for a stronger reason: a hash handed out
	// is a hash to crack offline, but a ciphertext handed out is one guess away
	// from the PIN for anyone who also gets the key. It leaves this process
	// only as PLAINTEXT, only through GET /api/shares/{id}/pin, only to the
	// creator or an admin, and only with an audit row written.
	PinEnc string `json:"-"`
	// PinRecoverable is computed: can this link's PIN still be shown? False
	// for a link minted before 00049, for one minted while the instance had no
	// FILEX_SECRET_KEY, and for one with no PIN at all. It carries no secret —
	// it is what lets a row say "this one cannot be shown" instead of offering
	// a copy button with nothing behind it.
	PinRecoverable bool       `json:"pin_recoverable"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	MaxDownloads   *int       `json:"max_downloads,omitempty"`
	DownloadCount  int        `json:"download_count"`
	// VisitCount is how many times an APP page was opened (00052). Kept
	// apart from DownloadCount because a page view is not a download: a
	// signing link somebody had only looked at twice said "İndirme 2" in
	// My shares (2026-09-21). Always 0 for an ordinary share.
	VisitCount int    `json:"visit_count"`
	CreatedBy  *int64 `json:"created_by,omitempty"`
	// CreatedVia is the token username the creating API call acted under
	// ("work", "fishapp"…). Empty for browser-session shares. Display-only —
	// ownership/authorization stay on CreatedBy.
	CreatedVia string    `json:"created_via,omitempty"`
	CreatedAt  time.Time `json:"created_at"`

	// Drop-link fields (Kind == ShareKindDrop). Empty/zero for a normal
	// download share.
	Kind         string  `json:"kind"`                    // "download" | "drop"
	MaxUploads   *int    `json:"max_uploads,omitempty"`   // cap on total files received
	UploadCount  int     `json:"upload_count"`            // files received so far
	DropSettings *string `json:"drop_settings,omitempty"` // JSON limits blob (max_files, max_file_size_mb, allowed_ext, ask_name)

	// ── App-plugin page fields (migration 00046) ──
	//
	// An app plugin's public page IS a share: the plugin opens one through the
	// `share_create` host function and the visitor's link is /s/<token> like
	// every other, so the administrator sees and revokes it in Shares, the
	// expiry ceiling is the instance's, and there is ONE PIN implementation to
	// get right. Zero here means an ordinary share and every path behaves
	// exactly as it did before.
	PluginID int64  `json:"plugin_id,omitempty"`
	PageID   string `json:"page_id,omitempty"`
	// Subject is the line the public shell puts in its header ("Please sign
	// contract.pdf"). Host-owned; the plugin supplies it at creation.
	Subject string `json:"subject,omitempty"`
	// StateJSON is the PLUGIN's durable record for this link, read and
	// replaced by the share_state host function. Never serialized: it is the
	// app's own bookkeeping, not the visitor's business.
	StateJSON string `json:"-"`
	// FilesJSON lists the copies the plugin exposed to the visitor —
	// [{ref,name,file,size,mime}] — where `file` is the BASENAME inside the
	// share's directory under <data-dir>/app-plugins/public/<id>/. Never
	// serialized: the public API answers with refs, not with the layout on
	// disk.
	FilesJSON string `json:"-"`
	// PurposeJSON is what the app said this link IS when it opened it
	// (wire.PagePurpose, migration 00052): the only way a page-less link —
	// a plain share of a finished document — can be named for what it is in
	// a list of links. Not serialized: lists carry the resolved `app`
	// (db.AppLink) instead.
	PurposeJSON string `json:"-"`

	// PinFails / LockedUntil are the five-strikes-then-ten-minutes PIN lock,
	// which from 00046 covers EVERY public link rather than only an app page.
	// Never serialized: a strike counter a client can read is one it can pace
	// itself against.
	PinFails    int        `json:"-"`
	LockedUntil *time.Time `json:"-"`

	// RevokedAt is when a person (or the app that opened it) ENDED the link
	// (00053). Revoking also sets ExpiresAt to the same moment, which is what
	// stops the link working; this is only the word for it, so a screen can
	// say "revoked" rather than "expired" (QA, 2026-09-21: My shares said
	// "Süresi doldu" for a link revoked a minute earlier).
	//
	// ⚠ Filled by the LISTINGS (shareMetaCols), not by the single-row reads
	// (shareCols): those decide whether a link works, and that is the expiry.
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// IsDrop reports whether this is a public upload (file-drop) share.
func (s *Share) IsDrop() bool { return s != nil && s.Kind == ShareKindDrop }

// IsApp reports whether an app plugin answers this link — the visitor gets
// that app's surface instead of a download or a folder listing.
func (s *Share) IsApp() bool { return s != nil && s.PluginID > 0 && s.PageID != "" }

// CappedCount is the counter `max_downloads` is measured against: the
// visits of an app page (its `max_visits`), the downloads of anything else.
func (s *Share) CappedCount() int {
	if s.IsApp() {
		return s.VisitCount
	}
	return s.DownloadCount
}

// PinLocked reports whether the PIN gate is shut because of wrong guesses.
func (s *Share) PinLocked(now time.Time) bool {
	return s != nil && s.LockedUntil != nil && now.Before(*s.LockedUntil)
}

// IsExpired reports whether the share has lapsed. Covers time expiry, the
// download cap (download shares) and the upload cap (drop shares).
func (s *Share) IsExpired(now time.Time) bool {
	if s == nil {
		return true
	}
	if s.ExpiresAt != nil && now.After(*s.ExpiresAt) {
		return true
	}
	if s.MaxDownloads != nil && s.CappedCount() >= *s.MaxDownloads {
		return true
	}
	if s.MaxUploads != nil && s.UploadCount >= *s.MaxUploads {
		return true
	}
	return false
}

// ChunkedUpload tracks an in-flight multipart upload.
type ChunkedUpload struct {
	ID         string       `json:"id"`
	StorageID  int64        `json:"storage_id"`
	StorageKey string       `json:"storage_key"`
	UploadID   string       `json:"upload_id"`
	TotalSize  int64        `json:"total_size"`
	Parts      []UploadPart `json:"parts"`
	ExpiresAt  time.Time    `json:"expires_at"`
}

// UploadPart represents one chunk of a multipart upload.
type UploadPart struct {
	PartNumber int    `json:"part_number"`
	Etag       string `json:"etag"`
	Size       int64  `json:"size"`
	URL        string `json:"url,omitempty"` // only on init response
}
