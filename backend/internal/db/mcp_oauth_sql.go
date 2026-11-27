package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MCPOAuthSQL stores opaque OAuth JSON using the caller's transaction, when
// present. Secret record keys are hashed by the OAuth layer before storage.
type MCPOAuthSQL struct {
	Pool         *sql.DB
	Placeholders func(string) string
	// CurrentRead uses MySQL's locking read so Take does not return a stale
	// payload from an outer repeatable-read transaction's earlier snapshot.
	CurrentRead bool
}

// The record ceilings. MCPOAuthMaxRecords bounds the table; at most
// MCPOAuthMaxAnonymous of the rows may be of a kind anybody can create
// without signing in (MCPOAuthAnonymousKind), so a stranger who fills those
// leaves the room the signed-in grants, codes and consents live in.
const (
	MCPOAuthMaxRecords   = 10000
	MCPOAuthMaxAnonymous = 2000
)

// MCPOAuthAnonymousKind reports whether a record kind is one an
// unauthenticated caller can make: a client registration nobody has used yet
// ("client") and an authorization request waiting for its sign-in
// ("request"). handlers.MCPOAuth names the kinds; the ceiling is kept here,
// under the lock, where it holds across replicas.
func MCPOAuthAnonymousKind(kind string) bool {
	for _, k := range mcpOAuthAnonymousKinds {
		if k == kind {
			return true
		}
	}
	return false
}

var mcpOAuthAnonymousKinds = []string{"client", "request"}

// anonymousKindsSQL is the IN list of the anonymous kinds: constants of this
// file, never a caller's value.
func anonymousKindsSQL() string {
	return "('" + strings.Join(mcpOAuthAnonymousKinds, "','") + "')"
}

func (p *MCPOAuthSQL) q(query string) string { return rebind(p.Placeholders, query) }

// lock serializes record mutations across processes. Its counters bound live
// storage even when an outer MySQL transaction already has a read snapshot.
func (p *MCPOAuthSQL) lock(ctx context.Context) error {
	_, err := Conn(ctx, p.Pool).ExecContext(ctx,
		`UPDATE mcp_oauth_record_lock SET record_count=record_count WHERE lock_id=1`)
	return err
}

func anonymousUnit(kind string) int64 {
	if MCPOAuthAnonymousKind(kind) {
		return 1
	}
	return 0
}

// CreateMCPOAuthRecord inserts a new immutable record. Expired records are
// pruned first; at most MCPOAuthMaxRecords may remain, of which at most
// MCPOAuthMaxAnonymous of an anonymous kind.
func (p *MCPOAuthSQL) CreateMCPOAuthRecord(ctx context.Context, kind, key, payload string, expiresAt int64) error {
	if strings.TrimSpace(kind) == "" || len(kind) > 32 || strings.TrimSpace(key) == "" || len(key) > 128 {
		return errors.New("MCP OAuth record: invalid kind or key")
	}
	anon := anonymousUnit(kind)
	return RunInTx(ctx, p.Pool, func(ctx context.Context) error {
		if err := p.lock(ctx); err != nil {
			return fmt.Errorf("lock MCP OAuth records: %w", err)
		}
		conn := Conn(ctx, p.Pool)
		now := time.Now().Unix()
		res, err := conn.ExecContext(ctx, p.q(`DELETE FROM mcp_oauth_records WHERE kind IN `+anonymousKindsSQL()+` AND expires_at<=?`), now)
		if err != nil {
			return fmt.Errorf("prune anonymous MCP OAuth records: %w", err)
		}
		anonPruned, err := res.RowsAffected()
		if err != nil {
			return err
		}
		res, err = conn.ExecContext(ctx, p.q(`DELETE FROM mcp_oauth_records WHERE expires_at<=?`), now)
		if err != nil {
			return fmt.Errorf("prune MCP OAuth records: %w", err)
		}
		pruned, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, p.q(`UPDATE mcp_oauth_record_lock SET record_count=record_count-?,anonymous_count=anonymous_count-? WHERE lock_id=1`), pruned+anonPruned, anonPruned); err != nil {
			return err
		}
		res, err = conn.ExecContext(ctx, p.q(`UPDATE mcp_oauth_record_lock SET record_count=record_count+1,anonymous_count=anonymous_count+? WHERE lock_id=1 AND record_count<? AND anonymous_count+?<=?`),
			anon, MCPOAuthMaxRecords, anon, MCPOAuthMaxAnonymous)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("MCP OAuth record capacity reached")
		}
		if _, err := conn.ExecContext(ctx, p.q(`INSERT INTO mcp_oauth_records (kind,record_key,payload,expires_at) VALUES (?,?,?,?)`), kind, key, payload, expiresAt); err != nil {
			// Restore the reservation even if an outer transaction handles this
			// error and continues (PostgreSQL instead marks it aborted).
			_, _ = conn.ExecContext(ctx, p.q(`UPDATE mcp_oauth_record_lock SET record_count=record_count-1,anonymous_count=anonymous_count-? WHERE lock_id=1`), anon)
			return fmt.Errorf("create MCP OAuth record: %w", err)
		}
		return nil
	})
}

// GetMCPOAuthRecord returns only live records. Missing and expired records
// return sql.ErrNoRows.
func (p *MCPOAuthSQL) GetMCPOAuthRecord(ctx context.Context, kind, key string, now int64) (string, error) {
	var payload string
	err := Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(`SELECT payload FROM mcp_oauth_records WHERE kind=? AND record_key=? AND expires_at>?`), kind, key, now).Scan(&payload)
	return payload, err
}

// TakeMCPOAuthRecord consumes a live record once. Conditional deletion and its
// affected-row count ensure only the deleting caller receives the payload.
func (p *MCPOAuthSQL) TakeMCPOAuthRecord(ctx context.Context, kind, key string, now int64) (string, error) {
	var payload string
	anon := anonymousUnit(kind)
	err := RunInTx(ctx, p.Pool, func(ctx context.Context) error {
		if err := p.lock(ctx); err != nil {
			return err
		}
		conn := Conn(ctx, p.Pool)
		query := `SELECT payload FROM mcp_oauth_records WHERE kind=? AND record_key=? AND expires_at>?`
		if p.CurrentRead {
			query += ` FOR UPDATE`
		}
		if err := conn.QueryRowContext(ctx, p.q(query), kind, key, now).Scan(&payload); err != nil {
			return err
		}
		res, err := conn.ExecContext(ctx, p.q(`DELETE FROM mcp_oauth_records WHERE kind=? AND record_key=? AND expires_at>?`), kind, key, now)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return sql.ErrNoRows
		}
		_, err = conn.ExecContext(ctx, p.q(`UPDATE mcp_oauth_record_lock SET record_count=record_count-1,anonymous_count=anonymous_count-? WHERE lock_id=1`), anon)
		return err
	})
	if err != nil {
		return "", err
	}
	return payload, nil
}
