package shortlink

// live is the instance's short-link addresses, for the readers that are not
// handed the service: the certificate questions (/api/tls/ask and filex's own
// ACME host policy) ask whether a name is a short-link host of its own.
//
// ⚠ A process-wide value, like db.TenantDomain: the router and the TLS server
// are built in different packages long before either could be handed the
// other's state, and they must answer the same. The short-link service
// replaces it whole whenever the bases change.
var live Snapshot

// Live is the instance's live snapshot.
func Live() *Snapshot { return &live }
