package model

import "time"

// Capabilities is the runtime feature snapshot returned by /api/capabilities.
// The frontend uses this to enable/disable UI affordances.
type Capabilities struct {
	// Storage write operations.
	Upload  bool `json:"upload"`
	Move    bool `json:"move"`
	Copy    bool `json:"copy"`
	Delete  bool `json:"delete"`
	Mkdir   bool `json:"mkdir"`
	Presign bool `json:"presign"` // browser-direct uploads

	// Search & versioning.
	Search   bool `json:"search"`
	Versions bool `json:"versions"`

	// Thumbnail backends present in the runtime.
	Thumbs ThumbCapabilities `json:"thumbs"`

	// Per-storage capability probe — keyed by storage ID (string for JSON).
	Storage map[string]StorageCapabilities `json:"storage,omitempty"`

	// Plug-and-play external services.
	External map[string]ExternalServiceState `json:"external"`

	// Backend-effective limits.
	MaxUploadSize int64 `json:"max_upload_size"`
	ChunkSize     int64 `json:"chunk_size"`

	// Driver inventory (UI uses these to render the right login form,
	// drop-down for storage create wizard, etc.). Frontend has matching
	// defaults in stores/capabilities.ts so a missing field never
	// crashes a caller.
	AuthDrivers    []string `json:"auth_drivers"`
	StorageDrivers []string `json:"storage_drivers"`
	DBDriver       string   `json:"db_driver"`
	SearchEnabled  bool     `json:"search_enabled"`

	// Build metadata.
	Version string `json:"version"`
	Build   string `json:"build"`

	// Demo-mode toggles a public landing on Login.vue with an "Open
	// the demo" CTA that auto-submits the supplied creds.
	DemoMode bool   `json:"demo_mode"`
	DemoUser string `json:"demo_user,omitempty"`

	// DefaultLocale, when set (FILEX_DEFAULT_LOCALE), pins the initial UI
	// language for users who haven't picked one — overriding browser detection.
	DefaultLocale string `json:"default_locale,omitempty"`
}

// StorageCapabilities describes a single backend's optional features. Used
// by the frontend to decide whether to show the chunked upload UI, the
// browser-direct upload button, or the live event indicator.
type StorageCapabilities struct {
	Read      bool `json:"read"`
	Write     bool `json:"write"`
	Move      bool `json:"move"`
	Copy      bool `json:"copy"`
	Delete    bool `json:"delete"`
	Mkdir     bool `json:"mkdir"`
	Presign   bool `json:"presign"`
	Multipart bool `json:"multipart"`
	Events    bool `json:"events"`
}

// ThumbCapabilities indicates which media types can be thumbnailed.
type ThumbCapabilities struct {
	Image       bool `json:"image"`
	ImageMagick bool `json:"imagemagick"` // `magick`/`convert` binary present
	Video       bool `json:"video"`
	Audio       bool `json:"audio"`
	PDF         bool `json:"pdf"`
	Office      bool `json:"office"`
	SVG         bool `json:"svg"`
}

// ExternalServiceState describes a plug-and-play integration's runtime status.
type ExternalServiceState struct {
	Enabled   bool       `json:"enabled"`
	URL       string     `json:"url,omitempty"`
	State     string     `json:"state"` // "ok", "unreachable", "unauthorized", "disabled"
	LastCheck *time.Time `json:"last_check,omitempty"`
}
