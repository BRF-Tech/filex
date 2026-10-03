package model

import "time"

// FileAssociation is the administrator's rule for one kind of file and one
// capability (migration 00077, internal/assoc): which handlers are asked, in
// which order, and which are switched off. A kind with no row keeps the
// default order (assoc.DefaultOrder).
type FileAssociation struct {
	// Capability is "open" or "thumbnail".
	Capability string `json:"capability"`
	// Ext is the kind: a file name's extension, lower-case, no dot.
	Ext string `json:"ext"`
	// Handlers are the handler ids in the order they are asked
	// ("builtin", "app:<app>/<view>", "app:<app>").
	Handlers []string `json:"handlers"`
	// Off are the handler ids switched off for this kind.
	Off       []string  `json:"off"`
	UpdatedBy *int64    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AppThumbLimits are the administrator's limits for one app's thumbnail
// calls (migration 00077). A zero field is "the default".
type AppThumbLimits struct {
	PluginID    int64     `json:"plugin_id"`
	MaxInputMB  int       `json:"max_input_mb"`
	TimeoutS    int       `json:"timeout_s"`
	MemoryMB    int       `json:"memory_mb"`
	Concurrency int       `json:"concurrency"`
	UpdatedAt   time.Time `json:"updated_at"`
}
