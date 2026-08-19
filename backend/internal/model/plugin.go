package model

import "time"

// Plugin kinds.
const (
	// PluginKindBinary is a program filex launches and supervises from
	// <data-dir>/plugins/<name>/.
	PluginKindBinary = "binary"
	// PluginKindRemote is a service the operator runs; filex only connects.
	PluginKindRemote = "remote"
)

// Plugin is one registered storage plugin (migration 00029) — see
// internal/plugin for what a plugin is and how it is spoken to.
//
// The row is the ADMIN's intent (which plugin, from where, on or off); what
// the plugin is doing right now (running, crashed, refused, which driver it
// registered) is runtime state the manager reports next to it and never
// persists — a restart re-derives it by starting the plugin again.
type Plugin struct {
	ID int64 `json:"id"`
	// Name is the operator-chosen identifier, unique, [a-z0-9][a-z0-9_-]{0,31}.
	// It names the plugin's directory and appears in logs; the DRIVER name is
	// what the plugin itself describes, and may differ.
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Binary is the file name inside <data-dir>/plugins/<name>/ (kind binary).
	Binary string `json:"binary,omitempty"`
	// SHA256 of the binary as installed. Checked again on every start: a file
	// that changed under filex is refused, not run.
	SHA256 string `json:"sha256,omitempty"`
	// Address is the remote base URL (kind remote), http(s)://host:port[/path].
	Address string `json:"address,omitempty"`
	// TokenSealed is the bearer token, sealed with the instance secret key
	// (secretbox). For a binary plugin filex minted it and hands it to the
	// process at start; for a remote one the admin supplied it. Never on the
	// wire.
	TokenSealed string `json:"-"`
	Enabled     bool   `json:"enabled"`

	// Version and Driver are the last values the plugin described, kept so
	// the list can show them while a plugin is stopped.
	Version string `json:"version,omitempty"`
	Driver  string `json:"driver,omitempty"`
	// LastError is the last start/validation failure, cleared on success.
	LastError string `json:"last_error,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
