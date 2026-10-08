package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/model"
)

// VaultLockSQL implements the vault_locks and vault_prefs half of Store
// (migration 00096, internal/vaultlock, docs/E2E-VAULT-FORMAT.md → The write
// lock), written ONCE for every engine and embedded in each driver's Store -
// the arrangement of OfficeSessionSQL.
//
// Every column is an integer or text: times are Unix milliseconds, so nothing
// here depends on how an engine stores or compares a timestamp. Taking and
// renewing a lock are compare-and-set updates on `rev`: two processes on one
// database that race for the same vault cannot both win.
type VaultLockSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (p *VaultLockSQL) q(query string) string { return rebind(p.Placeholders, query) }

const vaultLockCols = `tenant_id, vault_id, rev, token_hash, holder_user_id, holder_name, holder_client, holder_label,
	storage_id, path, taken_ms, lease_ms, active_ms, idle_seconds, index_started_ms, first_gen, last_gen,
	ended_token_hash, ended_reason, ended_by, ended_ms`

func scanVaultLock(r interface{ Scan(dst ...any) error }) (*model.VaultLock, error) {
	var l model.VaultLock
	if err := r.Scan(&l.TenantID, &l.VaultID, &l.Rev, &l.TokenHash, &l.HolderUserID, &l.HolderName, &l.HolderClient, &l.HolderLabel,
		&l.StorageID, &l.Path, &l.TakenMs, &l.LeaseMs, &l.ActiveMs, &l.IdleSeconds, &l.IndexStartedMs, &l.FirstGen, &l.LastGen,
		&l.EndedTokenHash, &l.EndedReason, &l.EndedBy, &l.EndedMs); err != nil {
		return nil, err
	}
	return &l, nil
}

// GetVaultLock returns the row of one vault, or (nil, nil) when there is none.
func (p *VaultLockSQL) GetVaultLock(ctx context.Context, tenantID int64, vaultID string) (*model.VaultLock, error) {
	l, err := scanVaultLock(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT `+vaultLockCols+` FROM vault_locks WHERE tenant_id=? AND vault_id=?`), tenantID, vaultID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get vault lock: %w", err)
	}
	return l, nil
}

// InsertVaultLock creates the row of a vault that has none, at rev 1. ok is
// false when a row is already there - another writer created it first, and
// the caller reads that one and tries again.
func (p *VaultLockSQL) InsertVaultLock(ctx context.Context, l *model.VaultLock) (bool, error) {
	if l == nil || l.VaultID == "" {
		return false, errors.New("vault lock: missing vault id")
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO vault_locks (`+vaultLockCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
		l.TenantID, l.VaultID, 1, l.TokenHash, l.HolderUserID, l.HolderName, l.HolderClient, l.HolderLabel,
		l.StorageID, l.Path, l.TakenMs, l.LeaseMs, l.ActiveMs, l.IdleSeconds, l.IndexStartedMs, l.FirstGen, l.LastGen,
		l.EndedTokenHash, l.EndedReason, l.EndedBy, l.EndedMs); err != nil {
		// The unique key on (tenant_id, vault_id): somebody was first. The
		// engines word that error three ways, so it is recognised by looking.
		if again, gerr := p.GetVaultLock(ctx, l.TenantID, l.VaultID); gerr == nil && again != nil {
			return false, nil
		}
		return false, fmt.Errorf("insert vault lock: %w", err)
	}
	l.Rev = 1
	return true, nil
}

// UpdateVaultLock writes every column of l over the row - only while the row
// is still at rev (the revision the caller read), and moves it to rev+1. ok
// is false when another writer came first: the caller has lost the race and
// reads the row again.
func (p *VaultLockSQL) UpdateVaultLock(ctx context.Context, l *model.VaultLock, rev int64) (bool, error) {
	if l == nil || l.VaultID == "" {
		return false, errors.New("vault lock: missing vault id")
	}
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE vault_locks SET rev=?, token_hash=?, holder_user_id=?, holder_name=?, holder_client=?, holder_label=?,
		   storage_id=?, path=?, taken_ms=?, lease_ms=?, active_ms=?, idle_seconds=?, index_started_ms=?, first_gen=?, last_gen=?,
		   ended_token_hash=?, ended_reason=?, ended_by=?, ended_ms=?, updated_at=CURRENT_TIMESTAMP
		 WHERE tenant_id=? AND vault_id=? AND rev=?`),
		rev+1, l.TokenHash, l.HolderUserID, l.HolderName, l.HolderClient, l.HolderLabel,
		l.StorageID, l.Path, l.TakenMs, l.LeaseMs, l.ActiveMs, l.IdleSeconds, l.IndexStartedMs, l.FirstGen, l.LastGen,
		l.EndedTokenHash, l.EndedReason, l.EndedBy, l.EndedMs,
		l.TenantID, l.VaultID, rev)
	if err != nil {
		return false, fmt.Errorf("update vault lock: %w", err)
	}
	// rev changes on every successful update, so a matched row is always a
	// changed row - MySQL counts changed rows, and the count is the same.
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update vault lock: %w", err)
	}
	if n != 1 {
		return false, nil
	}
	l.Rev = rev + 1
	return true, nil
}

// GetVaultIdleMinutes returns the idle time a person set (1 to 10), or 0 when
// they never set one.
func (p *VaultLockSQL) GetVaultIdleMinutes(ctx context.Context, userID int64) (int, error) {
	var n int
	err := Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT idle_minutes FROM vault_prefs WHERE user_id=?`), userID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get vault idle minutes: %w", err)
	}
	return n, nil
}

// SetVaultIdleMinutes stores a person's idle time. The range (1 to 10) is the
// caller's to check.
//
// ⚠ UPDATE-then-INSERT, as PutOfficeSession, because the upsert spellings
// differ per engine and this file is written once.
func (p *VaultLockSQL) SetVaultIdleMinutes(ctx context.Context, userID int64, minutes int) error {
	update := func() (int64, error) {
		res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
			`UPDATE vault_prefs SET idle_minutes=?, updated_at=CURRENT_TIMESTAMP WHERE user_id=?`), minutes, userID)
		if err != nil {
			return 0, fmt.Errorf("update vault idle minutes: %w", err)
		}
		n, _ := res.RowsAffected()
		return n, nil
	}
	if n, err := update(); err != nil || n > 0 {
		return err
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO vault_prefs (user_id, idle_minutes) VALUES (?, ?)`), userID, minutes); err != nil {
		// A row that is already there: a second writer, or an UPDATE that
		// changed nothing (MySQL counts changed rows, not matched ones).
		if _, gerr := p.GetVaultIdleMinutes(ctx, userID); gerr == nil {
			_, uerr := update()
			return uerr
		}
		return fmt.Errorf("insert vault idle minutes: %w", err)
	}
	return nil
}
