package model

import "time"

// PluginRequest is a request to install or upgrade a plugin, left by somebody
// who may not do it themselves — an API key (an agent, a script, the CLI) —
// for an administrator signed in to the admin panel to approve or reject
// (migration 00070, internal/pluginreq, docs/APP-PLUGINS.md → Install
// requests).
//
// What the source answered when the request was made is FROZEN here: the
// manifest, the hash approval holds the bytes to, and the permissions it asks
// to grant. An approval installs exactly that; a source that answers other
// bytes by then makes the request `superseded`.
type PluginRequest struct {
	ID int64
	// Key is random hex, unique: what the store reads a new row back by.
	Key string
	// Kind is PluginRequestKindApp or PluginRequestKindStorage.
	Kind string
	// Op is PluginRequestOpInstall or PluginRequestOpUpgrade.
	Op string
	// Name is the plugin's name: an app's manifest name, or the name a
	// storage plugin is (to be) installed under.
	Name string
	// PluginID is the installed plugin an upgrade replaces (app_plugins.id or
	// plugins.id, per Kind); nil for an install.
	PluginID *int64
	// SourceKind is github | url | source | from_source; SourceJSON the source
	// as given (pluginreq.Source), SourceKey the sha256 of its normalized form.
	SourceKind string
	SourceJSON string
	SourceKey  string
	// Version is what the source answered; FromVersion the version an upgrade
	// replaces.
	Version     string
	FromVersion string
	// ManifestJSON is the resolved snapshot (an app's filex-app.json, a
	// storage plugin's filex-storage.json feed); ReviewJSON the dry run's
	// review, for the approval screen.
	ManifestJSON string
	ReviewJSON   string
	// SHA256 is what approval holds the bytes to (an app's module, or its
	// manifest when it has none; a storage plugin's binary). ManifestSHA256
	// pins an app's manifest, which the module's hash does not.
	SHA256         string
	ManifestSHA256 string
	// PermissionsJSON is the JSON array of permission ids the request asks to
	// grant, frozen at request time.
	PermissionsJSON string
	// RequestedBy is the account that asked (nil once deleted); Requester its
	// name as shown when it asked; TokenID/TokenLabel the API key it came
	// through (nil/"" from a session).
	RequestedBy *int64
	Requester   string
	TokenID     *int64
	TokenLabel  string
	// Reason is the requester's own words.
	Reason string
	// Status is one of the PluginRequestStatus* values.
	Status string
	// DecidedBy/Decider/DecidedAt: who closed the request and when (an
	// expired one: nobody, at its expiry).
	DecidedBy *int64
	Decider   string
	DecidedAt *time.Time
	// DecisionNote is a rejection's reason, or why the request was superseded.
	DecisionNote string
	// ResultJSON is what approval did — the installed plugin — or, while the
	// request stays pending, the last attempt's refusal.
	ResultJSON string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Plugin request kinds, operations and states (plugin_requests columns).
const (
	PluginRequestKindApp     = "app"
	PluginRequestKindStorage = "storage"

	PluginRequestOpInstall = "install"
	PluginRequestOpUpgrade = "upgrade"

	PluginRequestPending    = "pending"
	PluginRequestApproved   = "approved"
	PluginRequestRejected   = "rejected"
	PluginRequestExpired    = "expired"
	PluginRequestSuperseded = "superseded"
)
