package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SettingMFARequireAdmin is the key holding "true"/"false" for whether admins must carry
// a second factor.
const SettingMFARequireAdmin = "mfa.require_admin"

// SettingWebhooksAllowUnsigned holds "true"/"false" for whether an inbound webhook with no
// signature is processed.
//
// It exists only to keep upgrades from breaking. Verification used to be skipped whenever
// the signature header was absent, so any install with hand-created hooks and no secret has
// been relying on unsigned deliveries working. A fresh install writes "false" during setup;
// an upgrade leaves the key unset, which reads as true, and Settings shows the count of
// unsigned deliveries next to the switch so the cutover is a decision rather than an outage.
const SettingWebhooksAllowUnsigned = "webhooks.allow_unsigned"

// SettingRBACEnforcement holds "true"/"false" for whether project and environment
// memberships are enforced.
//
// Default false, and deliberately so. Before this existed a global developer could touch
// every project on the instance, and no project memberships were ever created -- the only
// way to get one was a hard-coded "owner". Enforcing on upgrade would lock every non-admin
// out of every project simultaneously. Settings shows what would be denied so the cutover
// is a decision rather than an incident.
const SettingRBACEnforcement = "rbac.enforcement"

// Update-check settings. The latest version and the time it was seen are cached here
// rather than re-fetched per request, so the UI reads a row instead of the network and a
// restart does not immediately hit GitHub again.
const (
	SettingUpdateCheckEnabled  = "update.check_enabled"
	SettingUpdateLatestVersion = "update.latest_version"
	SettingUpdateCheckedAt     = "update.checked_at"
)

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

// RecordUpdateCheck stores the newest known release and when it was observed.
func (d *DB) RecordUpdateCheck(ctx context.Context, latest string) error {
	if err := d.SetSetting(ctx, SettingUpdateLatestVersion, latest, ""); err != nil {
		return err
	}
	return d.SetSetting(ctx, SettingUpdateCheckedAt, time.Now().UTC().Format(time.RFC3339), "")
}
