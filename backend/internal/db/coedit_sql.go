package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/model"
)

// CoEditSQL implements the editing-together half of Store (migration 00107,
// internal/coedit, task #189), written ONCE for every engine and embedded in
// each driver's Store - the arrangement of VaultLockSQL.
//
// Every column is an integer, text or bytes: times are Unix milliseconds, so
// nothing here depends on how an engine stores or compares a timestamp. A
// session's state moves by compare-and-set on `rev` (UpdateCoEditSession):
// the relay reads the session, decides, and writes it back only if nobody
// else wrote it in between, in the same transaction as the entry, member or
// blob row the decision adds. The relay never reads a sealed payload; this
// file only stores and returns it.
type CoEditSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (p *CoEditSQL) q(query string) string { return rebind(p.Placeholders, query) }

func coeditFlag(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

const coeditSessionCols = `sid, doc_key, storage_id, path, app, build, base_sig, saved_sig, saved_to, key_mode, sealed_key, user_salt,
	rev, head, changes_head, saved_through, log_bytes, blob_bytes, has_base, members, next_client, next_index,
	lease_holder, lease_until_ms, created_ms, active_ms`

func scanCoEditSession(r interface{ Scan(dst ...any) error }) (*model.CoEditSession, error) {
	var (
		s       model.CoEditSession
		hasBase int64
		members int64
	)
	if err := r.Scan(&s.SID, &s.DocKey, &s.StorageID, &s.Path, &s.App, &s.Build, &s.BaseSig, &s.SavedSig, &s.SavedTo, &s.KeyMode, &s.SealedKey, &s.UserSalt,
		&s.Rev, &s.Head, &s.ChangesHead, &s.SavedThrough, &s.LogBytes, &s.BlobBytes, &hasBase, &members, &s.NextClient, &s.NextIndex,
		&s.LeaseHolder, &s.LeaseUntilMs, &s.CreatedMs, &s.ActiveMs); err != nil {
		return nil, err
	}
	s.HasBase = hasBase != 0
	s.Members = int(members)
	return &s, nil
}

func (p *CoEditSQL) getSession(ctx context.Context, where string, arg any) (*model.CoEditSession, error) {
	s, err := scanCoEditSession(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT `+coeditSessionCols+` FROM coedit_sessions WHERE `+where+`=?`), arg))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get coedit session: %w", err)
	}
	return s, nil
}

// GetCoEditSession returns the session sid, or (nil, nil).
func (p *CoEditSQL) GetCoEditSession(ctx context.Context, sid string) (*model.CoEditSession, error) {
	return p.getSession(ctx, "sid", sid)
}

// GetCoEditSessionByDoc returns the session of a file, or (nil, nil).
func (p *CoEditSQL) GetCoEditSessionByDoc(ctx context.Context, docKey string) (*model.CoEditSession, error) {
	return p.getSession(ctx, "doc_key", docKey)
}

// InsertCoEditSession creates a session at rev 1. ok is false when the file
// already has one (or the id is taken): another writer was first, and the
// caller reads that one.
func (p *CoEditSQL) InsertCoEditSession(ctx context.Context, s *model.CoEditSession) (bool, error) {
	if s == nil || s.SID == "" || s.DocKey == "" {
		return false, errors.New("coedit session: missing id or document")
	}
	// The unique keys (sid, doc_key): somebody was first when either is there.
	taken := func() bool {
		if again, gerr := p.GetCoEditSessionByDoc(ctx, s.DocKey); gerr == nil && again != nil {
			return true
		}
		again, gerr := p.GetCoEditSession(ctx, s.SID)
		return gerr == nil && again != nil
	}
	return insertFirst(ctx, p.Pool, "insert coedit session", &s.Rev, taken, p.q(
		`INSERT INTO coedit_sessions (`+coeditSessionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
		s.SID, s.DocKey, s.StorageID, s.Path, s.App, s.Build, s.BaseSig, s.SavedSig, s.SavedTo, s.KeyMode, s.SealedKey, s.UserSalt,
		1, s.Head, s.ChangesHead, s.SavedThrough, s.LogBytes, s.BlobBytes, coeditFlag(s.HasBase), s.Members, s.NextClient, s.NextIndex,
		s.LeaseHolder, s.LeaseUntilMs, s.CreatedMs, s.ActiveMs)
}

// UpdateCoEditSession writes s over its row - only while the row is still at
// rev (the revision the caller read) - and moves it to rev+1. ok is false
// when another writer came first.
func (p *CoEditSQL) UpdateCoEditSession(ctx context.Context, s *model.CoEditSession, rev int64) (bool, error) {
	if s == nil || s.SID == "" {
		return false, errors.New("coedit session: missing id")
	}
	return updateAtRev(ctx, p.Pool, "update coedit session", &s.Rev, rev, p.q(
		`UPDATE coedit_sessions SET rev=?, build=?, base_sig=?, saved_sig=?, saved_to=?, sealed_key=?, head=?, changes_head=?, saved_through=?,
		   log_bytes=?, blob_bytes=?, has_base=?, members=?, next_client=?, next_index=?, lease_holder=?, lease_until_ms=?, active_ms=?,
		   updated_at=CURRENT_TIMESTAMP
		 WHERE sid=? AND rev=?`),
		rev+1, s.Build, s.BaseSig, s.SavedSig, s.SavedTo, s.SealedKey, s.Head, s.ChangesHead, s.SavedThrough,
		s.LogBytes, s.BlobBytes, coeditFlag(s.HasBase), s.Members, s.NextClient, s.NextIndex, s.LeaseHolder, s.LeaseUntilMs, s.ActiveMs,
		s.SID, rev)
}

// DeleteCoEditSession removes a session and everything kept for it: its
// members, its log and its blobs' rows (the blob files are the caller's).
func (p *CoEditSQL) DeleteCoEditSession(ctx context.Context, sid string) error {
	for _, table := range []string{"coedit_entries", "coedit_members", "coedit_blobs", "coedit_cursors", "coedit_sessions"} {
		if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`DELETE FROM `+table+` WHERE sid=?`), sid); err != nil {
			return fmt.Errorf("delete coedit session (%s): %w", table, err)
		}
	}
	return nil
}

// ListIdleCoEditSessions returns up to limit sessions nobody is in, the
// least recently active first: what the sweep judges.
func (p *CoEditSQL) ListIdleCoEditSessions(ctx context.Context, limit int) ([]*model.CoEditSession, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, p.q(
		`SELECT `+coeditSessionCols+` FROM coedit_sessions WHERE members=0 ORDER BY active_ms LIMIT ?`), limit)
	if err != nil {
		return nil, fmt.Errorf("list idle coedit sessions: %w", err)
	}
	defer rows.Close()
	var out []*model.CoEditSession
	for rows.Next() {
		s, err := scanCoEditSession(rows)
		if err != nil {
			return nil, fmt.Errorf("list idle coedit sessions: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Members
// ---------------------------------------------------------------------------

const coeditMemberCols = `sid, client, token_hash, user_id, user_ref, name, index_user, can_edit, join_seq, last_ctr, seen_ms`

func scanCoEditMember(r interface{ Scan(dst ...any) error }) (*model.CoEditMember, error) {
	var (
		m       model.CoEditMember
		canEdit int64
	)
	if err := r.Scan(&m.SID, &m.Client, &m.TokenHash, &m.UserID, &m.UserRef, &m.Name, &m.IndexUser, &canEdit, &m.JoinSeq, &m.LastCtr, &m.SeenMs); err != nil {
		return nil, err
	}
	m.CanEdit = canEdit != 0
	return &m, nil
}

func (p *CoEditSQL) listMembers(ctx context.Context, where string, args ...any) ([]*model.CoEditMember, error) {
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, p.q(`SELECT `+coeditMemberCols+` FROM coedit_members WHERE `+where), args...)
	if err != nil {
		return nil, fmt.Errorf("list coedit members: %w", err)
	}
	defer rows.Close()
	var out []*model.CoEditMember
	for rows.Next() {
		m, err := scanCoEditMember(rows)
		if err != nil {
			return nil, fmt.Errorf("list coedit members: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListCoEditMembers returns a session's members, the first to join first.
func (p *CoEditSQL) ListCoEditMembers(ctx context.Context, sid string) ([]*model.CoEditMember, error) {
	return p.listMembers(ctx, `sid=? ORDER BY join_seq`, sid)
}

// GetCoEditMember returns one member, or (nil, nil).
func (p *CoEditSQL) GetCoEditMember(ctx context.Context, sid, client string) (*model.CoEditMember, error) {
	m, err := scanCoEditMember(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT `+coeditMemberCols+` FROM coedit_members WHERE sid=? AND client=?`), sid, client))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get coedit member: %w", err)
	}
	return m, nil
}

// InsertCoEditMember adds a member. The client id is the session's to hand
// out (next_client, under the session's compare-and-set), so it is new.
func (p *CoEditSQL) InsertCoEditMember(ctx context.Context, m *model.CoEditMember) error {
	if m == nil || m.SID == "" || m.Client == "" {
		return errors.New("coedit member: missing session or client")
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO coedit_members (`+coeditMemberCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`),
		m.SID, m.Client, m.TokenHash, m.UserID, m.UserRef, m.Name, m.IndexUser, coeditFlag(m.CanEdit), m.JoinSeq, m.LastCtr, m.SeenMs); err != nil {
		return fmt.Errorf("insert coedit member: %w", err)
	}
	return nil
}

// SetCoEditMemberCounter records a writer's counter (under the session's
// compare-and-set, in its transaction).
func (p *CoEditSQL) SetCoEditMemberCounter(ctx context.Context, sid, client string, ctr int64) error {
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE coedit_members SET last_ctr=? WHERE sid=? AND client=?`), ctr, sid, client); err != nil {
		return fmt.Errorf("set coedit member counter: %w", err)
	}
	return nil
}

// TouchCoEditMember records a sign of life of a member's page. ok is false
// when the member is not (or no longer) in the session.
func (p *CoEditSQL) TouchCoEditMember(ctx context.Context, sid, client string, seenMs int64) (bool, error) {
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE coedit_members SET seen_ms=? WHERE sid=? AND client=?`), seenMs, sid, client)
	if err != nil {
		return false, fmt.Errorf("touch coedit member: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return true, nil
	}
	// MySQL counts changed rows: a touch in the same millisecond changes
	// nothing and counts 0. Ask.
	m, err := p.GetCoEditMember(ctx, sid, client)
	return m != nil, err
}

// DeleteCoEditMember removes a member, and its cursor row; ok is false when
// it was not there.
func (p *CoEditSQL) DeleteCoEditMember(ctx context.Context, sid, client string) (bool, error) {
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`DELETE FROM coedit_members WHERE sid=? AND client=?`), sid, client)
	if err != nil {
		return false, fmt.Errorf("delete coedit member: %w", err)
	}
	n, _ := res.RowsAffected()
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`DELETE FROM coedit_cursors WHERE sid=? AND client=?`), sid, client); err != nil {
		return n > 0, fmt.Errorf("delete coedit cursor: %w", err)
	}
	return n > 0, nil
}

// ListStaleCoEditMembers returns up to limit members last seen before
// beforeMs, of every session: the ones whose page has gone.
func (p *CoEditSQL) ListStaleCoEditMembers(ctx context.Context, beforeMs int64, limit int) ([]*model.CoEditMember, error) {
	if limit <= 0 {
		limit = 100
	}
	return p.listMembers(ctx, `seen_ms<? ORDER BY seen_ms LIMIT ?`, beforeMs, limit)
}

// ---------------------------------------------------------------------------
// The log
// ---------------------------------------------------------------------------

const coeditEntryCols = `sid, seq, kind, client, at_ms, ctr, ct, member, through, sig`

// InsertCoEditEntry writes one entry at its seq. The (sid, seq) key refuses
// a second entry at the same place.
func (p *CoEditSQL) InsertCoEditEntry(ctx context.Context, e *model.CoEditEntry) error {
	if e == nil || e.SID == "" || e.Seq <= 0 {
		return errors.New("coedit entry: missing session or seq")
	}
	var ct any
	if len(e.CT) > 0 {
		ct = e.CT
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO coedit_entries (`+coeditEntryCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`),
		e.SID, e.Seq, e.Kind, e.Client, e.AtMs, e.Ctr, ct, e.Member, e.Through, e.Sig); err != nil {
		return fmt.Errorf("insert coedit entry: %w", err)
	}
	return nil
}

// ListCoEditEntries returns up to limit entries after `after`, in order.
func (p *CoEditSQL) ListCoEditEntries(ctx context.Context, sid string, after int64, limit int) ([]*model.CoEditEntry, error) {
	if limit <= 0 {
		limit = 256
	}
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, p.q(
		`SELECT `+coeditEntryCols+` FROM coedit_entries WHERE sid=? AND seq>? ORDER BY seq LIMIT ?`), sid, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list coedit entries: %w", err)
	}
	defer rows.Close()
	var out []*model.CoEditEntry
	for rows.Next() {
		var e model.CoEditEntry
		if err := rows.Scan(&e.SID, &e.Seq, &e.Kind, &e.Client, &e.AtMs, &e.Ctr, &e.CT, &e.Member, &e.Through, &e.Sig); err != nil {
			return nil, fmt.Errorf("list coedit entries: %w", err)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Blobs
// ---------------------------------------------------------------------------

// InsertCoEditBlob records a stored blob. ok is false when the name is
// taken: a blob is written once.
func (p *CoEditSQL) InsertCoEditBlob(ctx context.Context, b *model.CoEditBlob) (bool, error) {
	if b == nil || b.SID == "" || b.Name == "" {
		return false, errors.New("coedit blob: missing session or name")
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO coedit_blobs (sid, name, size, sha256, created_ms) VALUES (?,?,?,?,?)`),
		b.SID, b.Name, b.Size, b.SHA256, b.CreatedMs); err != nil {
		if again, gerr := p.GetCoEditBlob(ctx, b.SID, b.Name); gerr == nil && again != nil {
			return false, nil
		}
		return false, fmt.Errorf("insert coedit blob: %w", err)
	}
	return true, nil
}

// GetCoEditBlob returns a stored blob's row, or (nil, nil).
func (p *CoEditSQL) GetCoEditBlob(ctx context.Context, sid, name string) (*model.CoEditBlob, error) {
	var b model.CoEditBlob
	err := Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT sid, name, size, sha256, created_ms FROM coedit_blobs WHERE sid=? AND name=?`), sid, name).
		Scan(&b.SID, &b.Name, &b.Size, &b.SHA256, &b.CreatedMs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get coedit blob: %w", err)
	}
	return &b, nil
}

// ---------------------------------------------------------------------------
// Cursors (migration 00109)
// ---------------------------------------------------------------------------

// PutCoEditCursor writes a member's latest sealed cursor over its row.
//
// ⚠ UPDATE-then-INSERT, as PutOfficeSession: the upsert spellings differ per
// engine and this file is written once. Two writers racing on a new row:
// the loser's INSERT meets the unique key and it writes with a second UPDATE.
func (p *CoEditSQL) PutCoEditCursor(ctx context.Context, c *model.CoEditCursor) error {
	if c == nil || c.SID == "" || c.Client == "" {
		return errors.New("coedit cursor: missing session or client")
	}
	update := func() (int64, error) {
		res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
			`UPDATE coedit_cursors SET ctr=?, ct=?, at_ms=? WHERE sid=? AND client=?`), c.Ctr, c.CT, c.AtMs, c.SID, c.Client)
		if err != nil {
			return 0, fmt.Errorf("update coedit cursor: %w", err)
		}
		n, _ := res.RowsAffected()
		return n, nil
	}
	if n, err := update(); err != nil || n > 0 {
		return err
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO coedit_cursors (sid, client, ctr, ct, at_ms) VALUES (?,?,?,?,?)`), c.SID, c.Client, c.Ctr, c.CT, c.AtMs); err != nil {
		// The row is there after all (the race above, or an UPDATE that
		// changed nothing - MySQL counts changed rows).
		_, uerr := update()
		return uerr
	}
	return nil
}

// ListCoEditCursors returns a session's cursors written after sinceMs.
func (p *CoEditSQL) ListCoEditCursors(ctx context.Context, sid string, sinceMs int64) ([]*model.CoEditCursor, error) {
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, p.q(
		`SELECT sid, client, ctr, ct, at_ms FROM coedit_cursors WHERE sid=? AND at_ms>? ORDER BY at_ms`), sid, sinceMs)
	if err != nil {
		return nil, fmt.Errorf("list coedit cursors: %w", err)
	}
	defer rows.Close()
	var out []*model.CoEditCursor
	for rows.Next() {
		var c model.CoEditCursor
		if err := rows.Scan(&c.SID, &c.Client, &c.Ctr, &c.CT, &c.AtMs); err != nil {
			return nil, fmt.Errorf("list coedit cursors: %w", err)
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}
