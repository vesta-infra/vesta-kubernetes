package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/lib/pq"
)

func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (d *DB) CreateAPIToken(ctx context.Context, userID, name, tokenHash string, scopes []string, expiresAt *time.Time) (*APIToken, error) {
	t := &APIToken{}
	var exp sql.NullTime
	if expiresAt != nil {
		exp = sql.NullTime{Time: *expiresAt, Valid: true}
	}

	err := d.QueryRowContext(ctx,
		`INSERT INTO api_tokens (user_id, name, token_hash, scopes, expires_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, user_id, name, scopes, expires_at, created_at`,
		userID, name, tokenHash, pq.Array(scopes), exp,
	).Scan(&t.ID, &t.UserID, &t.Name, pq.Array(&t.Scopes), &exp, &t.CreatedAt)

	if err != nil {
		return nil, err
	}
	if exp.Valid {
		t.ExpiresAt = &exp.Time
	}
	return t, nil
}

func (d *DB) GetAPITokenByHash(ctx context.Context, tokenHash string) (*APIToken, error) {
	t := &APIToken{}
	var exp, lastUsed sql.NullTime

	err := d.QueryRowContext(ctx,
		`SELECT id, user_id, name, token_hash, scopes, expires_at, last_used_at, created_at
		 FROM api_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, pq.Array(&t.Scopes), &exp, &lastUsed, &t.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if exp.Valid {
		t.ExpiresAt = &exp.Time
	}
	if lastUsed.Valid {
		t.LastUsedAt = &lastUsed.Time
	}
	return t, nil
}

func (d *DB) ListAPITokens(ctx context.Context, userID string) ([]APIToken, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, user_id, name, scopes, expires_at, last_used_at, created_at
		 FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []APIToken
	for rows.Next() {
		var t APIToken
		var exp, lastUsed sql.NullTime
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, pq.Array(&t.Scopes), &exp, &lastUsed, &t.CreatedAt); err != nil {
			return nil, err
		}
		if exp.Valid {
			t.ExpiresAt = &exp.Time
		}
		if lastUsed.Valid {
			t.LastUsedAt = &lastUsed.Time
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

func (d *DB) RevokeAPIToken(ctx context.Context, id, userID string) error {
	result, err := d.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) TouchAPIToken(ctx context.Context, id string) {
	d.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = now() WHERE id = $1`, id)
}
