package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// UploaderSQL implements the uploaders and upload_deletions half of Store
// (migration 00136), written ONCE for every engine and embedded in each
// driver's Store - the arrangement of CaptchaMarkSQL. Times are unix seconds,
// so no engine's timestamp spelling is involved.
//
// It reads and writes rows; who may make, use or delete an uploader - and
// what a key may do - is handlers/uploaders.go's.
type UploaderSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (p *UploaderSQL) q(query string) string { return rebind(p.Placeholders, query) }

const uploaderSelect = `SELECT id, user_id, name, storage_id, folder, visibility, link_days, short_link, tenant_id,
        key_hash, created_at, key_set_at, last_used_at
   FROM uploaders`

func scanUploader(row interface{ Scan(dst ...any) error }) (*model.Uploader, error) {
	u := &model.Uploader{}
	var created, keySet int64
	var used sql.NullInt64
	if err := row.Scan(&u.ID, &u.UserID, &u.Name, &u.StorageID, &u.Folder, &u.Visibility, &u.LinkDays,
		&u.ShortLink, &u.TenantID, &u.KeyHash, &created, &keySet, &used); err != nil {
		return nil, err
	}
	u.Visibility = model.NormalizeUploaderVisibility(u.Visibility)
	u.CreatedAt, u.KeySetAt = time.Unix(created, 0).UTC(), time.Unix(keySet, 0).UTC()
	if used.Valid {
		t := time.Unix(used.Int64, 0).UTC()
		u.LastUsedAt = &t
	}
	return u, nil
}

func (p *UploaderSQL) oneUploader(ctx context.Context, tail string, args ...any) (*model.Uploader, error) {
	return scanUploader(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(uploaderSelect+tail), args...))
}

// CreateUploader inserts an uploader and returns it as the store reads it
// back (found again by its key hash, which is unique - no engine's
// LastInsertId or RETURNING is needed).
func (p *UploaderSQL) CreateUploader(ctx context.Context, in *model.Uploader) (*model.Uploader, error) {
	switch {
	case in == nil || in.UserID <= 0 || in.StorageID <= 0:
		return nil, errors.New("uploader: missing account or storage")
	case strings.TrimSpace(in.KeyHash) == "":
		return nil, errors.New("uploader: missing key")
	}
	now := time.Now().Unix()
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`INSERT INTO uploaders
		  (user_id, name, storage_id, folder, visibility, link_days, short_link, tenant_id, key_hash, created_at, key_set_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`),
		in.UserID, in.Name, in.StorageID, in.Folder, model.NormalizeUploaderVisibility(in.Visibility),
		in.LinkDays, in.ShortLink, in.TenantID, in.KeyHash, now, now); err != nil {
		return nil, fmt.Errorf("insert uploader: %w", err)
	}
	return p.oneUploader(ctx, ` WHERE key_hash=?`, in.KeyHash)
}

// GetUploader returns one uploader, sql.ErrNoRows when there is none.
func (p *UploaderSQL) GetUploader(ctx context.Context, id int64) (*model.Uploader, error) {
	return p.oneUploader(ctx, ` WHERE id=?`, id)
}

// GetUploaderByKey returns the uploader whose key hashes to keyHash,
// sql.ErrNoRows when there is none.
func (p *UploaderSQL) GetUploaderByKey(ctx context.Context, keyHash string) (*model.Uploader, error) {
	if strings.TrimSpace(keyHash) == "" {
		return nil, sql.ErrNoRows
	}
	return p.oneUploader(ctx, ` WHERE key_hash=?`, keyHash)
}

// ListUploaders returns an account's uploaders, the newest first.
func (p *UploaderSQL) ListUploaders(ctx context.Context, userID int64) ([]*model.Uploader, error) {
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, p.q(uploaderSelect+` WHERE user_id=? ORDER BY created_at DESC, id DESC`), userID)
	if err != nil {
		return nil, fmt.Errorf("list uploaders: %w", err)
	}
	defer rows.Close()
	out := []*model.Uploader{}
	for rows.Next() {
		u, err := scanUploader(rows)
		if err != nil {
			return nil, fmt.Errorf("scan uploader: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUploader writes an uploader's settings - name, folder, link rule and
// the tenant stamp that goes with the folder - and reports whether the
// account had such an uploader. The key is SetUploaderKey's.
func (p *UploaderSQL) UpdateUploader(ctx context.Context, u *model.Uploader) (bool, error) {
	if u == nil || u.ID <= 0 || u.UserID <= 0 || u.StorageID <= 0 {
		return false, errors.New("uploader: missing id, account or storage")
	}
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`UPDATE uploaders
		   SET name=?, storage_id=?, folder=?, visibility=?, link_days=?, short_link=?, tenant_id=?
		 WHERE id=? AND user_id=?`),
		u.Name, u.StorageID, u.Folder, model.NormalizeUploaderVisibility(u.Visibility), u.LinkDays, u.ShortLink, u.TenantID,
		u.ID, u.UserID)
	if err != nil {
		return false, fmt.Errorf("update uploader: %w", err)
	}
	return affectedOrThere(ctx, p, res, u.ID, u.UserID)
}

// SetUploaderKey replaces an uploader's key: the old one stops working the
// moment this returns. ok = the account had such an uploader.
func (p *UploaderSQL) SetUploaderKey(ctx context.Context, id, userID int64, keyHash string) (bool, error) {
	if strings.TrimSpace(keyHash) == "" {
		return false, errors.New("uploader: missing key")
	}
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE uploaders SET key_hash=?, key_set_at=? WHERE id=? AND user_id=?`),
		keyHash, time.Now().Unix(), id, userID)
	if err != nil {
		return false, fmt.Errorf("set uploader key: %w", err)
	}
	return affectedOrThere(ctx, p, res, id, userID)
}

// affectedOrThere reads an UPDATE's answer. ⚠ MySQL counts CHANGED rows, so
// an update to the values the row already has reports 0 although the row is
// there: when nothing changed, the row is looked up before saying "none".
func affectedOrThere(ctx context.Context, p *UploaderSQL, res sql.Result, id, userID int64) (bool, error) {
	if n, _ := res.RowsAffected(); n > 0 {
		return true, nil
	}
	var one int
	err := Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(`SELECT 1 FROM uploaders WHERE id=? AND user_id=?`), id, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// TouchUploader stamps an uploader's last use. Best effort for the caller.
func (p *UploaderSQL) TouchUploader(ctx context.Context, id int64) error {
	_, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`UPDATE uploaders SET last_used_at=? WHERE id=?`), time.Now().Unix(), id)
	return err
}

// DeleteUploader removes an account's uploader - its key with it. ok = there
// was one. The deletion links it handed out are kept (migration 00136).
func (p *UploaderSQL) DeleteUploader(ctx context.Context, id, userID int64) (bool, error) {
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`DELETE FROM uploaders WHERE id=? AND user_id=?`), id, userID)
	if err != nil {
		return false, fmt.Errorf("delete uploader: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

const uploadDeletionSelect = `SELECT id, token_hash, user_id, uploader_id, storage_id, node_id, share_id, tenant_id, name, created_at
   FROM upload_deletions`

func scanUploadDeletion(row interface{ Scan(dst ...any) error }) (*model.UploadDeletion, error) {
	d := &model.UploadDeletion{}
	var created int64
	if err := row.Scan(&d.ID, &d.TokenHash, &d.UserID, &d.UploaderID, &d.StorageID, &d.NodeID, &d.ShareID,
		&d.TenantID, &d.Name, &created); err != nil {
		return nil, err
	}
	d.CreatedAt = time.Unix(created, 0).UTC()
	return d, nil
}

// CreateUploadDeletion writes one upload's deletion link.
func (p *UploaderSQL) CreateUploadDeletion(ctx context.Context, d *model.UploadDeletion) error {
	switch {
	case d == nil || d.UserID <= 0 || d.StorageID <= 0 || d.NodeID <= 0:
		return errors.New("upload deletion: missing account, storage or file")
	case strings.TrimSpace(d.TokenHash) == "":
		return errors.New("upload deletion: missing token")
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`INSERT INTO upload_deletions
		  (token_hash, user_id, uploader_id, storage_id, node_id, share_id, tenant_id, name, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`),
		d.TokenHash, d.UserID, d.UploaderID, d.StorageID, d.NodeID, d.ShareID, d.TenantID, d.Name,
		time.Now().Unix()); err != nil {
		return fmt.Errorf("insert upload deletion: %w", err)
	}
	return nil
}

// GetUploadDeletion returns the deletion link whose token hashes to
// tokenHash, sql.ErrNoRows when there is none (never made, or used).
func (p *UploaderSQL) GetUploadDeletion(ctx context.Context, tokenHash string) (*model.UploadDeletion, error) {
	if strings.TrimSpace(tokenHash) == "" {
		return nil, sql.ErrNoRows
	}
	return scanUploadDeletion(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(uploadDeletionSelect+` WHERE token_hash=?`), tokenHash))
}

// DeleteUploadDeletion spends a deletion link. ok = this call removed it:
// two requests racing on one link, one of them is told true.
func (p *UploaderSQL) DeleteUploadDeletion(ctx context.Context, id int64) (bool, error) {
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`DELETE FROM upload_deletions WHERE id=?`), id)
	if err != nil {
		return false, fmt.Errorf("delete upload deletion: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
