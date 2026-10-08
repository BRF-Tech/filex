package model

// VaultLock is one row of vault_locks (migration 00096, internal/vaultlock,
// docs/E2E-VAULT-FORMAT.md → The write lock): the write lock of one vault,
// by tenant and vault id. The row outlives the locks taken on it - it says
// who held the vault last, and how that ended - so a lock is held exactly
// while TokenHash is set and the clock says so (vaultlock.Service decides).
//
// Every time is Unix MILLISECONDS in an integer column: the lease is a minute
// long and is compared on every vault write, on three engines that store and
// compare timestamps three different ways.
type VaultLock struct {
	TenantID int64
	// VaultID is the key file's vault.id as 32 lower-case hex digits.
	VaultID string
	// Rev is the compare-and-set counter: every write of the row names the
	// revision it read, and loses when another writer came first.
	Rev int64
	// TokenHash is the hex SHA-256 of the live lock's token; "" when no lock
	// is recorded as held.
	TokenHash string
	// Holder: the person, the kind of client (web, desktop, cli, mount) and
	// the client's own short label ("Firefox, ofis").
	HolderUserID int64
	HolderName   string
	HolderClient string
	HolderLabel  string
	// StorageID and Path are where the lock was taken: the vault folder the
	// holder named (a copy of a vault shares its id, and so its lock).
	StorageID int64
	Path      string
	// TakenMs: when the lock was taken; LeaseMs: when it runs out unless
	// renewed; ActiveMs: the last vault write or active renewal; IdleSeconds:
	// the holder's idle time, read when the lock was taken.
	TakenMs     int64
	LeaseMs     int64
	ActiveMs    int64
	IdleSeconds int64
	// IndexStartedMs: an index write of the holder is running since then
	// (0 = none). The lock is not free again until it ends or 60 seconds
	// pass.
	IndexStartedMs int64
	// FirstGen and LastGen are the first and last generation committed
	// under the current (or, once it ended, the last) lock.
	FirstGen int64
	LastGen  int64
	// EndedTokenHash, EndedReason and EndedMs record how the last lock
	// ended: released, expired, idle, broken or locked_idle. The holder of
	// that token is told the reason on its next call - and, for a break,
	// EndedBy: the name of who broke it.
	EndedTokenHash string
	EndedReason    string
	EndedBy        string
	EndedMs        int64
}
