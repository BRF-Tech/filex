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

func (p *MCPOAuthSQL) q(query string) string { return rebind(p.Placeholders, query) }

// lock serializes record mutations across processes. Its counter bounds live
// storage even when an outer MySQL transaction already has a read snapshot.
func (p *MCPOAuthSQL) lock(ctx context.Context) error {
	_, err := Conn(ctx, p.Pool).ExecContext(ctx,
		`UPDATE mcp_oauth_record_lock SET record_count=record_count WHERE lock_id=1`)
	return err
}

// CreateMCPOAuthRecord inserts a new immutable record. Expired records are
// pruned first; at most 10000 records may remain, including at most 1000
// registered clients, leaving capacity for existing clients to exchange grants.
func (p *MCPOAuthSQL) CreateMCPOAuthRecord(ctx context.Context, kind, key, payload string, expiresAt int64) error {
	if strings.TrimSpace(kind) == "" || len(kind) > 32 || strings.TrimSpace(key) == "" || len(key) > 128 {
		return errors.New("MCP OAuth record: invalid kind or key")
	}
	client := int64(0)
	if kind == "client" {
		client = 1
	}
	return RunInTx(ctx, p.Pool, func(ctx context.Context) error {
		if err := p.lock(ctx); err != nil {
			return fmt.Errorf("lock MCP OAuth records: %w", err)
		}
		conn := Conn(ctx, p.Pool)
		now := time.Now().Unix()
		res, err := conn.ExecContext(ctx, p.q(`DELETE FROM mcp_oauth_records WHERE kind='client' AND expires_at<=?`), now)
		if err != nil {
			return fmt.Errorf("prune MCP OAuth clients: %w", err)
		}
		clientsPruned, err := res.RowsAffected()
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
		if _, err := conn.ExecContext(ctx, p.q(`UPDATE mcp_oauth_record_lock SET record_count=record_count-?,client_count=client_count-? WHERE lock_id=1`), pruned+clientsPruned, clientsPruned); err != nil {
			return err
		}
		res, err = conn.ExecContext(ctx, p.q(`UPDATE mcp_oauth_record_lock SET record_count=record_count+1,client_count=client_count+? WHERE lock_id=1 AND record_count<10000 AND client_count+?<=1000`), client, client)
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
			_, _ = conn.ExecContext(ctx, p.q(`UPDATE mcp_oauth_record_lock SET record_count=record_count-1,client_count=client_count-? WHERE lock_id=1`), client)
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
	client := int64(0)
	if kind == "client" {
		client = 1
	}
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
		_, err = conn.ExecContext(ctx, p.q(`UPDATE mcp_oauth_record_lock SET record_count=record_count-1,client_count=client_count-? WHERE lock_id=1`), client)
		return err
	})
	if err != nil {
		return "", err
	}
	return payload, nil
}
