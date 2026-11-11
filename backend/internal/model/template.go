package model

import "time"

// Template is a mail or page template an administrator replaced (table
// templates, migration 00137; internal/templates owns what the names and the
// text mean). TenantID 0 is the install's own (the instance administrator's);
// any other is the provider id of the tenant whose administrator saved it.
type Template struct {
	ID       int64  `json:"id"`
	TenantID int64  `json:"tenant_id"`
	Key      string `json:"key"`
	// Subject is a mail's subject line (Liquid, text); "" for a page.
	Subject string `json:"subject"`
	// Body is the HTML: a mail's body, a page, a layout, a text slot.
	Body string `json:"body"`
	// TextBody is a mail's own plain-text version (Liquid, text); "" = the
	// plain text is made from the HTML.
	TextBody string `json:"text_body"`
	// Revision counts the saves (1 for the first). A save that names an
	// older revision is refused (db.ErrTemplateChanged).
	Revision  int64     `json:"revision"`
	UpdatedBy *int64    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	// LastError is the last failure of this template at render time (the
	// default was used instead); "" when it has not failed since it was saved.
	LastError   string     `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
}
