package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/model"
)

// AssistantSQL implements the AI assistant's conversations half of Store
// (migration 00126, task #269), written once for every engine and embedded in
// each driver's Store - the arrangement of RecentSearchSQL.
//
// ⚠ Every read and every write names the person: a conversation id that is
// not theirs is answered exactly like one that does not exist.
type AssistantSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (p *AssistantSQL) q(query string) string { return rebind(p.Placeholders, query) }

const assistantCols = `id, user_id, conv_key, title, state, rev, created_ms, updated_ms`

// assistantListCols leaves the state out: a list shows titles and times.
const assistantListCols = `id, user_id, conv_key, title, '', rev, created_ms, updated_ms`

func scanAssistantConversation(r rowScanner) (*model.AssistantConversation, error) {
	c := &model.AssistantConversation{}
	if err := r.Scan(&c.ID, &c.UserID, &c.Key, &c.Title, &c.State, &c.Rev, &c.CreatedMs, &c.UpdatedMs); err != nil {
		return nil, err
	}
	return c, nil
}

// CreateAssistantConversation writes c (UserID, Key, Title, State and the
// times; Rev starts at 1) and answers the row as stored.
func (p *AssistantSQL) CreateAssistantConversation(ctx context.Context, c *model.AssistantConversation) (*model.AssistantConversation, error) {
	if c == nil || c.UserID == 0 || c.Key == "" {
		return nil, errors.New("assistant conversation: missing user or key")
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO assistant_conversations (user_id, conv_key, title, state, rev, created_ms, updated_ms) VALUES (?,?,?,?,1,?,?)`),
		c.UserID, c.Key, c.Title, c.State, c.CreatedMs, c.UpdatedMs); err != nil {
		return nil, fmt.Errorf("insert assistant conversation: %w", err)
	}
	return queryRow(ctx, p.Pool, scanAssistantConversation, "read assistant conversation back",
		p.q(`SELECT `+assistantCols+` FROM assistant_conversations WHERE conv_key=? AND user_id=?`), c.Key, c.UserID)
}

// GetAssistantConversation is one of userID's conversations, state
// included; nil (no error) when there is no such conversation OF THEIRS.
func (p *AssistantSQL) GetAssistantConversation(ctx context.Context, userID, id int64) (*model.AssistantConversation, error) {
	return queryRow(ctx, p.Pool, scanAssistantConversation, "get assistant conversation",
		p.q(`SELECT `+assistantCols+` FROM assistant_conversations WHERE id=? AND user_id=?`), id, userID)
}

// ListAssistantConversations is userID's conversations, the most recently
// used first, without their state; at most limit (limit <= 0: 100).
func (p *AssistantSQL) ListAssistantConversations(ctx context.Context, userID int64, limit int) ([]*model.AssistantConversation, error) {
	if limit <= 0 {
		limit = 100
	}
	return queryRows(ctx, p.Pool, scanAssistantConversation, "list assistant conversations",
		p.q(`SELECT `+assistantListCols+` FROM assistant_conversations WHERE user_id=? ORDER BY updated_ms DESC, id DESC LIMIT ?`), userID, limit)
}

// UpdateAssistantConversation writes c's title, state and updated time if
// the row is still at revision from, and answers whether it was: false (no
// error) when another writer came first or the row is gone. c.Rev is then
// from+1.
func (p *AssistantSQL) UpdateAssistantConversation(ctx context.Context, c *model.AssistantConversation, from int64) (bool, error) {
	return updateAtRev(ctx, p.Pool, "update assistant conversation", &c.Rev, from, p.q(
		`UPDATE assistant_conversations SET title=?, state=?, updated_ms=?, rev=rev+1 WHERE id=? AND user_id=? AND rev=?`),
		c.Title, c.State, c.UpdatedMs, c.ID, c.UserID, from)
}

// DeleteAssistantConversation removes one of userID's conversations; false
// when there was no such conversation of theirs.
func (p *AssistantSQL) DeleteAssistantConversation(ctx context.Context, userID, id int64) (bool, error) {
	// Looked up first rather than read off RowsAffected: the answer must
	// not depend on how an engine counts.
	found, err := p.GetAssistantConversation(ctx, userID, id)
	if err != nil || found == nil {
		return false, err
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`DELETE FROM assistant_conversations WHERE id=? AND user_id=?`), id, userID); err != nil {
		return false, fmt.Errorf("delete assistant conversation: %w", err)
	}
	return true, nil
}

// DeleteAssistantConversations removes every conversation of userID.
func (p *AssistantSQL) DeleteAssistantConversations(ctx context.Context, userID int64) error {
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`DELETE FROM assistant_conversations WHERE user_id=?`), userID); err != nil {
		return fmt.Errorf("delete assistant conversations: %w", err)
	}
	return nil
}

// DeleteAssistantConversationsBefore removes every conversation last used
// before beforeMs (unix milliseconds): the retention sweep.
func (p *AssistantSQL) DeleteAssistantConversationsBefore(ctx context.Context, beforeMs int64) error {
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`DELETE FROM assistant_conversations WHERE updated_ms < ?`), beforeMs); err != nil {
		return fmt.Errorf("sweep assistant conversations: %w", err)
	}
	return nil
}
