package db

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

type DB struct {
	*sql.DB
}

func New(databaseURL string) (*DB, error) {
	conn, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(5)

	d := &DB{conn}
	if err := d.migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Println("Database connected and migrated")
	return d, nil
}

func (d *DB) migrate() error {
	_, err := d.Exec(schema)
	return err
}

const schema = `
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT UNIQUE NOT NULL,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT 'developer',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS teams (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT UNIQUE NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS team_members (
    team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'member',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);

CREATE TABLE IF NOT EXISTS api_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash TEXT UNIQUE NOT NULL,
    scopes TEXT[] NOT NULL DEFAULT '{}',
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notification_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id TEXT NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('slack', 'discord', 'google_chat', 'webhook', 'email')),
    config JSONB NOT NULL DEFAULT '{}',
    events TEXT[] NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notification_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    app_id TEXT NOT NULL DEFAULT '',
    environment TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('sent', 'failed')),
    payload JSONB NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_team_members_user ON team_members(user_id);
CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_notification_channels_project ON notification_channels(project_id);
CREATE INDEX IF NOT EXISTS idx_notification_history_channel ON notification_history(channel_id);
CREATE INDEX IF NOT EXISTS idx_notification_history_project ON notification_history(project_id);

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_hash ON password_reset_tokens(token_hash);

CREATE TABLE IF NOT EXISTS audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL DEFAULT '',
    resource_name TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    environment TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}',
    ip_address TEXT NOT NULL DEFAULT '',
    auth_method TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_log_created ON audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_project ON audit_log(project_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_user ON audit_log(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_resource ON audit_log(resource_type, resource_id);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider TEXT NOT NULL,
    event_type TEXT NOT NULL DEFAULT '',
    delivery_id TEXT NOT NULL DEFAULT '',
    repository TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    commit_sha TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'received',
    processing_result TEXT NOT NULL DEFAULT '',
    apps_triggered TEXT[] NOT NULL DEFAULT '{}',
    duration_ms INT NOT NULL DEFAULT 0,
    ip_address TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_created ON webhook_deliveries(created_at);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_provider ON webhook_deliveries(provider);

CREATE TABLE IF NOT EXISTS builds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    environment TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'success', 'failed', 'cancelled')),
    strategy TEXT NOT NULL DEFAULT 'dockerfile',
    commit_sha TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    repository TEXT NOT NULL DEFAULT '',
    image TEXT NOT NULL DEFAULT '',
    job_name TEXT NOT NULL DEFAULT '',
    triggered_by TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    duration_ms INT NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_builds_app ON builds(app_id);
CREATE INDEX IF NOT EXISTS idx_builds_project ON builds(project_id);
CREATE INDEX IF NOT EXISTS idx_builds_status ON builds(status);
CREATE INDEX IF NOT EXISTS idx_builds_created ON builds(created_at);

CREATE TABLE IF NOT EXISTS invite_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_invite_tokens_hash ON invite_tokens(token_hash);

CREATE TABLE IF NOT EXISTS project_members (
    project_id TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'owner',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_project_members_user ON project_members(user_id);
CREATE INDEX IF NOT EXISTS idx_project_members_project ON project_members(project_id);

CREATE TABLE IF NOT EXISTS alert_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id TEXT NOT NULL,
    app_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    metric TEXT NOT NULL CHECK (metric IN ('cpu', 'memory', 'restarts', 'replicas', 'http_error_rate')),
    operator TEXT NOT NULL CHECK (operator IN ('>', '<', '>=', '<=', '=')),
    threshold DOUBLE PRECISION NOT NULL,
    duration_seconds INT NOT NULL DEFAULT 60,
    environment TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    last_triggered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_alert_rules_project ON alert_rules(project_id);
CREATE INDEX IF NOT EXISTS idx_alert_rules_app ON alert_rules(app_id);

CREATE TABLE IF NOT EXISTS scheduled_deployments (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    app_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    environment TEXT NOT NULL DEFAULT '',
    image TEXT NOT NULL DEFAULT '',
    tag TEXT NOT NULL DEFAULT '',
    scheduled_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','running','completed','failed','cancelled')),
    result_message TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_scheduled_deployments_project ON scheduled_deployments(project_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_deployments_status ON scheduled_deployments(status, scheduled_at);

-- Two-factor authentication.
--
-- All 2FA state lives in tables keyed by user_id rather than in columns on users.
-- migrate() is a single Exec of this whole string with no ALTER TABLE anywhere, so
-- CREATE TABLE IF NOT EXISTS is a no-op against an existing database: adding a column to
-- users here would silently never appear. New tables are the only shape that migrates.

CREATE TABLE IF NOT EXISTS user_mfa_totp (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_ciphertext TEXT NOT NULL,
    digits SMALLINT NOT NULL DEFAULT 6,
    period_seconds SMALLINT NOT NULL DEFAULT 30,
    algorithm TEXT NOT NULL DEFAULT 'SHA1',
    last_counter BIGINT NOT NULL DEFAULT 0,
    confirmed_at TIMESTAMPTZ,
    pending_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_webauthn_credentials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id TEXT UNIQUE NOT NULL,
    public_key BYTEA NOT NULL,
    aaguid BYTEA NOT NULL DEFAULT ''::bytea,
    sign_count BIGINT NOT NULL DEFAULT 0,
    transports TEXT[] NOT NULL DEFAULT '{}',
    attestation_type TEXT NOT NULL DEFAULT '',
    backup_eligible BOOLEAN NOT NULL DEFAULT false,
    backup_state BOOLEAN NOT NULL DEFAULT false,
    user_verified BOOLEAN NOT NULL DEFAULT false,
    rp_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_user_webauthn_credentials_user ON user_webauthn_credentials(user_id);

CREATE TABLE IF NOT EXISTS user_mfa_backup_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT UNIQUE NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_user_mfa_backup_codes_user ON user_mfa_backup_codes(user_id);

CREATE TABLE IF NOT EXISTS webauthn_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN ('register', 'authenticate')),
    session_data JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_webauthn_sessions_expires ON webauthn_sessions(expires_at);

CREATE TABLE IF NOT EXISTS mfa_lockouts (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    failed_count INT NOT NULL DEFAULT 0,
    window_started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_until TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_refresh_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT UNIQUE NOT NULL,
    family_id UUID NOT NULL,
    amr TEXT[] NOT NULL DEFAULT '{}',
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    ip_address TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_user_refresh_tokens_family ON user_refresh_tokens(family_id);
CREATE INDEX IF NOT EXISTS idx_user_refresh_tokens_user ON user_refresh_tokens(user_id);

-- Upgrades of Vesta itself. Kept so the UI can show what happened across the restart
-- that an upgrade causes: the API process that started the Job is not the one that
-- reports the outcome, so the record has to outlive it.
CREATE TABLE IF NOT EXISTS update_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_version TEXT NOT NULL DEFAULT '',
    to_version TEXT NOT NULL,
    job_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running',
    message TEXT NOT NULL DEFAULT '',
    triggered_by UUID REFERENCES users(id) ON DELETE SET NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_update_history_started ON update_history(started_at DESC);

-- A recent proof of identity, spent on one destructive change to a user's own factors.
--
-- Single-use and short-lived on purpose: a session cookie proves who logged in, not who
-- is at the keyboard now. Without this, anyone who finds an unlocked laptop can strip
-- 2FA off the account, and the second factor protects nothing after the first login.
CREATE TABLE IF NOT EXISTS mfa_reauth_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mfa_reauth_grants_expires ON mfa_reauth_grants(expires_at);

-- Instance-wide settings an admin can change without a redeploy. A key/value table
-- rather than columns because the alternative -- adding a column to an existing table --
-- silently never migrates here, as the comment above user_mfa_totp explains.
CREATE TABLE IF NOT EXISTS instance_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL
);
`
