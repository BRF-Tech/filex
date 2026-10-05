package auth

import (
	"context"
	"fmt"
	"time"
)

// DirectorySyncer is a provider that can read its whole directory — every
// person and their groups — without anybody signing in: the LDAP driver's
// directory sync (docs/LDAP.md → Directory sync). authsetup runs it on the
// provider's interval and when an administrator presses Sync now.
type DirectorySyncer interface {
	// SyncDirectory makes the accounts and the directory-linked group
	// memberships match the directory. A failure that stops the whole run
	// is returned as the error, with the report of what was done before it.
	SyncDirectory(ctx context.Context) (*DirectorySyncReport, error)
	// SyncInterval is how often to run it on its own; 0 is never.
	SyncInterval() time.Duration
}

// DirectorySyncReport is what one directory sync did.
type DirectorySyncReport struct {
	Provider   string    `json:"provider"`
	Trigger    string    `json:"trigger"` // "manual" or "schedule"
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	// Found is how many people the directory search returned with an
	// e-mail; Created how many of them had no account yet.
	Found   int `json:"found"`
	Created int `json:"created"`
	// Updated is how many accounts' directory-linked group memberships
	// changed.
	Updated int `json:"updated"`
	// Skipped: an entry with no e-mail, or an account that could not be
	// made (another tenant's address, no tenant to home it in).
	Skipped int `json:"skipped"`
	// Missing: accounts the directory made that it no longer lists. Their
	// directory memberships end.
	Missing int `json:"missing"`
	// Disabled: accounts switched off — their person switched off in the
	// directory, or no longer listed (sync_disable_missing). Enabled:
	// accounts sync had switched off, on again because the directory let
	// the person back in.
	Disabled int `json:"disabled"`
	Enabled  int `json:"enabled"`
	// EmailsChanged: accounts whose e-mail followed a change in the
	// directory (found by the person's permanent id).
	EmailsChanged int `json:"emails_changed"`
	// Groups (sync_groups): how many the directory listed, how many became
	// new filex groups, were renamed with their directory group, came back
	// after being removed, were flagged removed, or were skipped because a
	// filex group already links to them by hand.
	GroupsFound        int `json:"groups_found"`
	GroupsCreated      int `json:"groups_created"`
	GroupsRenamed      int `json:"groups_renamed"`
	GroupsRestored     int `json:"groups_restored"`
	GroupsRemoved      int `json:"groups_removed"`
	GroupsLinkedByHand int `json:"groups_linked_by_hand"`
	// Problems names up to MaxSyncProblems entries that went wrong; Error
	// is set when the run stopped.
	Problems []string `json:"problems,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// MaxSyncProblems bounds the problems one report keeps.
const MaxSyncProblems = 20

// Problem records one entry that went wrong.
func (r *DirectorySyncReport) Problem(who string, err error) {
	if len(r.Problems) < MaxSyncProblems {
		r.Problems = append(r.Problems, fmt.Sprintf("%s: %v", who, err))
	}
}
