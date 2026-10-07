package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// OfficeSessionSQL implements the office_sessions half of Store (migration
// 00092, internal/onlyoffice session_base.go, #184), written ONCE for every
// engine and embedded in each driver's Store - the arrangement of
// LoginThrottleSQL.
//
// Every column is an integer or text: the modification time is Unix
// nanoseconds and the expiry Unix seconds, so nothing here depends on how an
// engine stores or compares a timestamp, and a value comes back exactly as it
// went in.
type OfficeSessionSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (p *OfficeSessionSQL) q(query string) string { return rebind(p.Placeholders, query) }

const officeSessionCols = `doc_key, node_id, file_size, file_mtime_ns, file_etag, version_unknown, dropped, expires_unix`

func scanOfficeSession(r interface{ Scan(dst ...any) error }) (*model.OfficeSession, error) {
	var (
		out              model.OfficeSession
		unknown, dropped int64
	)
	if err := r.Scan(&out.DocKey, &out.NodeID, &out.Size, &out.MtimeNs, &out.Etag, &unknown, &dropped, &out.ExpiresUnix); err != nil {
		return nil, err
	}
	out.Unknown = unknown != 0
	out.Dropped = dropped != 0
	return &out, nil
}

func officeFlag(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// GetOfficeSession returns the session with this key, or (nil, nil) when
// there is none. An expired row is returned as it is: the caller decides
// (ExpiresUnix), and the prune removes it.
func (p *OfficeSessionSQL) GetOfficeSession(ctx context.Context, key string) (*model.OfficeSession, error) {
	s, err := scanOfficeSession(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT `+officeSessionCols+` FROM office_sessions WHERE doc_key=?`), key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

// PutOfficeSession creates the row for s.DocKey or overwrites it.
//
// ⚠ UPDATE-then-INSERT and not an upsert, because the upsert spellings differ
// per engine and this file is written once. The loser of two writers racing
// on a new key reaches the INSERT, gets the unique-key error, and writes its
// row with a second UPDATE: the last writer's row stands, as an upsert's
// would.
func (p *OfficeSessionSQL) PutOfficeSession(ctx context.Context, s *model.OfficeSession) error {
	if s == nil || s.DocKey == "" {
		return errors.New("office session: missing key")
	}
	update := func() (int64, error) {
		res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
			`UPDATE office_sessions SET node_id=?, file_size=?, file_mtime_ns=?, file_etag=?, version_unknown=?, dropped=?, expires_unix=?, updated_at=CURRENT_TIMESTAMP
			  WHERE doc_key=?`),
			s.NodeID, s.Size, s.MtimeNs, s.Etag, officeFlag(s.Unknown), officeFlag(s.Dropped), s.ExpiresUnix, s.DocKey)
		if err != nil {
			return 0, fmt.Errorf("update office session: %w", err)
		}
		n, _ := res.RowsAffected()
		return n, nil
	}
	if n, err := update(); err != nil || n > 0 {
		return err
	}
	if err := p.insert(ctx, s); err != nil {
		// A row that is already there: the race above, or an UPDATE that
		// changed nothing (MySQL counts changed rows, not matched ones).
		if existing, gerr := p.GetOfficeSession(ctx, s.DocKey); gerr == nil && existing != nil {
			_, uerr := update()
			return uerr
		}
		return err
	}
	return nil
}

// AddOfficeSession records s unless an unexpired row for its key is already
// there, and returns the row that stands - the FIRST writer's. An expired row
// is replaced. now is the clock the expiry is read against.
//
// It is how a session's base is recorded: a second person handed the same
// key (or a second instance asked for it) joins the running session, which
// shows them ITS version, so the first record must stand.
func (p *OfficeSessionSQL) AddOfficeSession(ctx context.Context, s *model.OfficeSession, now time.Time) (*model.OfficeSession, error) {
	if s == nil || s.DocKey == "" {
		return nil, errors.New("office session: missing key")
	}
	existing, err := p.GetOfficeSession(ctx, s.DocKey)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.ExpiresUnix > now.Unix() {
			return existing, nil
		}
		if err := p.PutOfficeSession(ctx, s); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err := p.insert(ctx, s); err != nil {
		// Another writer was first: theirs stands.
		if again, gerr := p.GetOfficeSession(ctx, s.DocKey); gerr == nil && again != nil {
			return again, nil
		}
		return nil, err
	}
	return s, nil
}

func (p *OfficeSessionSQL) insert(ctx context.Context, s *model.OfficeSession) error {
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO office_sessions (`+officeSessionCols+`) VALUES (?,?,?,?,?,?,?,?)`),
		s.DocKey, s.NodeID, s.Size, s.MtimeNs, s.Etag, officeFlag(s.Unknown), officeFlag(s.Dropped), s.ExpiresUnix); err != nil {
		return fmt.Errorf("insert office session: %w", err)
	}
	return nil
}

// DeleteOfficeSession removes the row for key (the session is over).
func (p *OfficeSessionSQL) DeleteOfficeSession(ctx context.Context, key string) error {
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM office_sessions WHERE doc_key=?`), key); err != nil {
		return fmt.Errorf("delete office session: %w", err)
	}
	return nil
}

// PruneOfficeSessions removes the rows that expired before `before`; n = rows
// removed.
func (p *OfficeSessionSQL) PruneOfficeSessions(ctx context.Context, before time.Time) (int64, error) {
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM office_sessions WHERE expires_unix < ?`), before.Unix())
	if err != nil {
		return 0, fmt.Errorf("prune office sessions: %w", err)
	}
	return res.RowsAffected()
}
