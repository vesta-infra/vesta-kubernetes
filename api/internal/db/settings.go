package db

import (
	"context"
	"database/sql"
	"errors"
)

// SettingMFARequireAdmin is the key holding "true"/"false" for whether admins must carry
// a second factor.
const SettingMFARequireAdmin = "mfa.require_admin"

// GetSetting reads an instance setting. A missing key returns ErrNotFound so callers can
// tell "never configured" from "explicitly set to empty" and apply their own default.
func (d *DB) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := d.QueryRowContext(ctx, `SELECT value FROM instance_settings WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return value, err
}

// GetBoolSetting reads a boolean setting, falling back to def when it has never been set.
func (d *DB) GetBoolSetting(ctx context.Context, key string, def bool) bool {
	value, err := d.GetSetting(ctx, key)
	if err != nil {
		// A read failure must not silently relax a security policy, so an unreachable
		// database falls back to the caller's default rather than to false.
		return def
	}
	return value == "true"
}

// SetSetting writes an instance setting, recording who changed it.
func (d *DB) SetSetting(ctx context.Context, key, value, updatedBy string) error {
	var by interface{}
	if updatedBy != "" {
		by = updatedBy
	}
	_, err := d.ExecContext(ctx, `
		INSERT INTO instance_settings (key, value, updated_by, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (key) DO UPDATE SET value = $2, updated_by = $3, updated_at = now()`,
		key, value, by)
	return err
}
