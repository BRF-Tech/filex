package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/model"
)

// PushSQL implements the Web Push half of Store (task #191, internal/notify
// push.go): the devices people receive pushes on, and the instance's VAPID
// key. Written ONCE for every engine and embedded in each driver's Store - the
// arrangement of DigestSQL.
//
// It only reads and writes rows: what is pushed to whom is internal/notify's.
//
// ⚠ No upsert (three engines, three spellings) and no timestamp is ever bound:
// every time written here is the database's own CURRENT_TIMESTAMP.
type PushSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (p *PushSQL) q(query string) string { return rebind(p.Placeholders, query) }

const pushCols = `id, user_id, endpoint, endpoint_hash, p256dh, auth_secret, label, through_id, failures, created_at, last_ok_at`

func scanPush(r interface{ Scan(dst ...any) error }) (*model.PushSubscription, error) {
	out := &model.PushSubscription{}
	var ok sql.NullTime
	if err := r.Scan(&out.ID, &out.UserID, &out.Endpoint, &out.EndpointHash, &out.P256dh, &out.Auth,
		&out.Label, &out.ThroughID, &out.Failures, &out.CreatedAt, &ok); err != nil {
		return nil, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	if ok.Valid {
		t := ok.Time.UTC()
		out.LastOKAt = &t
	}
	return out, nil
}

func (p *PushSQL) pushByHash(ctx context.Context, hash string) (*model.PushSubscription, error) {
	row, err := scanPush(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT `+pushCols+` FROM push_subscriptions WHERE endpoint_hash=?`), hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("push subscription: %w", err)
	}
	return row, nil
}

// SavePushSubscription records s (keyed by s.EndpointHash) and answers the row
// as stored. The same person saving the same device again keeps its row and
// its mark, with the keys and the label replaced; a device another person had
// (a second account signed in on the same browser) is theirs no longer - the
// row is replaced, so nothing of the first person's is pushed to it again.
func (p *PushSQL) SavePushSubscription(ctx context.Context, s *model.PushSubscription) (*model.PushSubscription, error) {
	if s == nil || s.UserID == 0 || s.Endpoint == "" || s.EndpointHash == "" {
		return nil, errors.New("push subscription: missing user, endpoint or hash")
	}
	conn := Conn(ctx, p.Pool)
	cur, err := p.pushByHash(ctx, s.EndpointHash)
	if err != nil {
		return nil, err
	}
	switch {
	case cur != nil && cur.UserID == s.UserID:
		if _, err := conn.ExecContext(ctx, p.q(
			`UPDATE push_subscriptions SET endpoint=?, p256dh=?, auth_secret=?, label=?, failures=0 WHERE id=?`),
			s.Endpoint, s.P256dh, s.Auth, s.Label, cur.ID); err != nil {
			return nil, fmt.Errorf("update push subscription: %w", err)
		}
	default:
		if cur != nil {
			if _, err := conn.ExecContext(ctx, p.q(`DELETE FROM push_subscriptions WHERE id=?`), cur.ID); err != nil {
				return nil, fmt.Errorf("drop the device's earlier subscription: %w", err)
			}
		}
		if _, err := conn.ExecContext(ctx, p.q(
			`INSERT INTO push_subscriptions (user_id, endpoint, endpoint_hash, p256dh, auth_secret, label, through_id)
			 VALUES (?,?,?,?,?,?,?)`),
			s.UserID, s.Endpoint, s.EndpointHash, s.P256dh, s.Auth, s.Label, s.ThroughID); err != nil {
			return nil, fmt.Errorf("insert push subscription: %w", err)
		}
	}
	saved, err := p.pushByHash(ctx, s.EndpointHash)
	if err != nil {
		return nil, err
	}
	if saved == nil {
		return nil, errors.New("push subscription: not found after saving it")
	}
	return saved, nil
}

// ListPushSubscriptions answers one person's devices, the oldest first.
func (p *PushSQL) ListPushSubscriptions(ctx context.Context, userID int64) ([]*model.PushSubscription, error) {
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, p.q(
		`SELECT `+pushCols+` FROM push_subscriptions WHERE user_id=? ORDER BY id`), userID)
	if err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	defer rows.Close()
	out := []*model.PushSubscription{}
	for rows.Next() {
		s, err := scanPush(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListPushSubscribers answers every person with at least one device.
func (p *PushSQL) ListPushSubscribers(ctx context.Context) ([]int64, error) {
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, `SELECT DISTINCT user_id FROM push_subscriptions ORDER BY user_id`)
	if err != nil {
		return nil, fmt.Errorf("list push subscribers: %w", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CountPushSubscriptions is how many devices there are, everybody's.
func (p *PushSQL) CountPushSubscriptions(ctx context.Context) (int64, error) {
	var n int64
	if err := Conn(ctx, p.Pool).QueryRowContext(ctx, `SELECT COUNT(*) FROM push_subscriptions`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count push subscriptions: %w", err)
	}
	return n, nil
}

// DeletePushSubscription removes one of userID's own devices; ok is false
// when there was no such device OF THEIRS (somebody else's id is answered
// exactly like an id that does not exist).
func (p *PushSQL) DeletePushSubscription(ctx context.Context, userID, id int64) (bool, error) {
	return p.deleteOwn(ctx, `id=?`, userID, id)
}

// DeletePushSubscriptionByHash removes userID's device with this endpoint
// hash (the browser that is turning push off, or signing out).
func (p *PushSQL) DeletePushSubscriptionByHash(ctx context.Context, userID int64, hash string) (bool, error) {
	return p.deleteOwn(ctx, `endpoint_hash=?`, userID, hash)
}

// deleteOwn looks the row up first rather than reading RowsAffected: MySQL
// reports CHANGED rows, and the answer must not depend on the engine.
func (p *PushSQL) deleteOwn(ctx context.Context, where string, userID int64, key any) (bool, error) {
	var id int64
	err := Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT id FROM push_subscriptions WHERE user_id=? AND `+where), userID, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find push subscription: %w", err)
	}
	return true, p.DropPushSubscription(ctx, id)
}

// DropPushSubscription removes a device by id, whoever's it is: the push
// service said it is gone, or it refused too many pushes in a row.
func (p *PushSQL) DropPushSubscription(ctx context.Context, id int64) error {
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`DELETE FROM push_subscriptions WHERE id=?`), id); err != nil {
		return fmt.Errorf("drop push subscription: %w", err)
	}
	return nil
}

// AdvancePushMark moves a device's mark from `from` to `to` and answers
// whether it did: false when the mark is no longer `from` (another server,
// or another pass, pushed these rows first). Compare-and-set, so a row is
// pushed to a device once however many servers try. ⚠ Called with to > from
// only, so the row always changes and MySQL's "changed rows" count is right.
func (p *PushSQL) AdvancePushMark(ctx context.Context, id, from, to int64) (bool, error) {
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE push_subscriptions SET through_id=? WHERE id=? AND through_id=?`), to, id, from)
	if err != nil {
		return false, fmt.Errorf("advance push mark: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RecordPushResult notes the push service's answer to a push to device id:
// taken (the refusal count goes back to 0) or refused (it goes up). It
// answers the count after the write.
func (p *PushSQL) RecordPushResult(ctx context.Context, id int64, taken bool) (int, error) {
	conn := Conn(ctx, p.Pool)
	stmt := `UPDATE push_subscriptions SET failures=failures+1 WHERE id=?`
	if taken {
		stmt = `UPDATE push_subscriptions SET failures=0, last_ok_at=CURRENT_TIMESTAMP WHERE id=?`
	}
	if _, err := conn.ExecContext(ctx, p.q(stmt), id); err != nil {
		return 0, fmt.Errorf("record push result: %w", err)
	}
	var n int
	err := conn.QueryRowContext(ctx, p.q(`SELECT failures FROM push_subscriptions WHERE id=?`), id).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read push failures: %w", err)
	}
	return n, nil
}

// pushKeyID is the one row of push_vapid_keys.
const pushKeyID = 1

// GetPushVAPIDKey answers the instance's VAPID key, nil when none was made.
func (p *PushSQL) GetPushVAPIDKey(ctx context.Context) (*model.PushVAPIDKey, error) {
	k := &model.PushVAPIDKey{}
	err := Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT public_key, private_key, created_at FROM push_vapid_keys WHERE id=?`), pushKeyID).
		Scan(&k.PublicKey, &k.PrivateSealed, &k.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("push key: %w", err)
	}
	k.CreatedAt = k.CreatedAt.UTC()
	return k, nil
}

// CreatePushVAPIDKey stores the first key. It fails when there is one already
// (two servers started at once): the caller reads the one that won.
func (p *PushSQL) CreatePushVAPIDKey(ctx context.Context, k *model.PushVAPIDKey) error {
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO push_vapid_keys (id, public_key, private_key) VALUES (?,?,?)`),
		pushKeyID, k.PublicKey, k.PrivateSealed); err != nil {
		return fmt.Errorf("create push key: %w", err)
	}
	return nil
}

// ReplacePushVAPIDKey puts k in place of the key and forgets every device -
// a subscription is bound to the key it was made with, and a push signed with
// another is refused. It answers how many devices were forgotten. Run it in a
// transaction (Store.WithTx): the key and the devices change together.
func (p *PushSQL) ReplacePushVAPIDKey(ctx context.Context, k *model.PushVAPIDKey) (int64, error) {
	dropped, err := p.CountPushSubscriptions(ctx)
	if err != nil {
		return 0, err
	}
	conn := Conn(ctx, p.Pool)
	if _, err := conn.ExecContext(ctx, `DELETE FROM push_subscriptions`); err != nil {
		return 0, fmt.Errorf("forget push subscriptions: %w", err)
	}
	if _, err := conn.ExecContext(ctx, p.q(`DELETE FROM push_vapid_keys WHERE id=?`), pushKeyID); err != nil {
		return 0, fmt.Errorf("drop push key: %w", err)
	}
	if err := p.CreatePushVAPIDKey(ctx, k); err != nil {
		return 0, err
	}
	return dropped, nil
}
