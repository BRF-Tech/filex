package model

import "time"

// Login throttle scopes: what a counter row counts.
const (
	// LoginThrottleAccount counts wrong attempts against one normalized
	// identifier — whether or not an account by that name exists.
	LoginThrottleAccount = "account"
	// LoginThrottleIP counts wrong attempts from one client address.
	LoginThrottleIP = "ip"
)

// LoginThrottle is one sign-in attempt counter (table login_throttle, migration
// 00072; internal/loginguard owns what the numbers mean).
type LoginThrottle struct {
	ID int64 `json:"id"`
	// Scope is LoginThrottleAccount or LoginThrottleIP.
	Scope string `json:"scope"`
	// Subject is the normalized identifier, or the address.
	Subject string `json:"subject"`
	// Fails is the wrong attempts inside the current window.
	Fails int `json:"fails"`
	// LockLevel is how many locks in a row this subject has earned; the next
	// lock lasts twice as long as the previous, up to a ceiling.
	LockLevel   int        `json:"lock_level"`
	WindowStart time.Time  `json:"window_start"`
	LockedUntil *time.Time `json:"locked_until,omitempty"`
	LastFailAt  time.Time  `json:"last_fail_at"`
	// LastIP is the address of the most recent wrong attempt.
	LastIP string `json:"last_ip,omitempty"`
	// LastProtocol is the door it came through: web, dav, ftp, sftp, s3.
	LastProtocol string    `json:"last_protocol,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Locked reports whether the lock is in force at now.
func (t *LoginThrottle) Locked(now time.Time) bool {
	return t != nil && t.LockedUntil != nil && t.LockedUntil.After(now)
}
