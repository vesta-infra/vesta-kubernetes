package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/lib/pq"
	"kubernetes.getvesta.sh/api/internal/mfa"
)

// TOTPEnrollment is a user's authenticator-app factor. The secret stays encrypted here
// and is only decrypted at the point of verification.
type TOTPEnrollment struct {
	UserID           string
	SecretCiphertext string
	LastCounter      uint64
	ConfirmedAt      *time.Time
	PendingExpiresAt *time.Time
	CreatedAt        time.Time
}

// Confirmed reports whether enrollment finished. An unconfirmed row is a QR code that was
// generated but never proved, and must not count as a factor.
func (t *TOTPEnrollment) Confirmed() bool { return t != nil && t.ConfirmedAt != nil }

// WebAuthnCredential is one registered passkey.
type WebAuthnCredential struct {
	ID              string
	UserID          string
	CredentialID    []byte
	PublicKey       []byte
	AAGUID          []byte
	SignCount       uint32
	Transports      []string
	AttestationType string
	BackupEligible  bool
	BackupState     bool
	UserVerified    bool
	RPID            string
	Name            string
	LastUsedAt      *time.Time
	CreatedAt       time.Time
}

// credential_id is a TEXT column with a UNIQUE constraint, so the raw credential bytes
// are stored base64url-encoded. Raw bytes in a text column would depend on the database's
// encoding to round-trip, and a credential id is arbitrary binary.
func encodeCredentialID(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCredentialID(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// ---------- TOTP ----------

func (d *DB) GetTOTP(ctx context.Context, userID string) (*TOTPEnrollment, error) {
	var t TOTPEnrollment
	var lastCounter int64
	err := d.QueryRowContext(ctx, `
		SELECT user_id, secret_ciphertext, last_counter, confirmed_at, pending_expires_at, created_at
		FROM user_mfa_totp WHERE user_id = $1`, userID).
		Scan(&t.UserID, &t.SecretCiphertext, &lastCounter, &t.ConfirmedAt, &t.PendingExpiresAt, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.LastCounter = uint64(lastCounter)
	return &t, nil
}

// UpsertPendingTOTP stores an unconfirmed secret, replacing any previous attempt.
//
// Restarting enrollment must overwrite rather than fail: a user who abandons a QR code
// and starts again would otherwise be stuck with a secret no authenticator holds. The
// WHERE guard is what stops that overwriting a *confirmed* factor, which would silently
// lock the user out of their own account.
func (d *DB) UpsertPendingTOTP(ctx context.Context, userID, ciphertext string, expiresAt time.Time) error {
	res, err := d.ExecContext(ctx, `
		INSERT INTO user_mfa_totp (user_id, secret_ciphertext, pending_expires_at, confirmed_at, last_counter, updated_at)
		VALUES ($1, $2, $3, NULL, 0, now())
		ON CONFLICT (user_id) DO UPDATE
		SET secret_ciphertext = $2, pending_expires_at = $3, last_counter = 0, updated_at = now()
		WHERE user_mfa_totp.confirmed_at IS NULL`,
		userID, ciphertext, expiresAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("an authenticator app is already enrolled; remove it first")
	}
	return nil
}

// ConfirmTOTP marks enrollment complete and records the step that proved it.
func (d *DB) ConfirmTOTP(ctx context.Context, userID string, counter uint64) error {
	_, err := d.ExecContext(ctx, `
		UPDATE user_mfa_totp
		SET confirmed_at = now(), pending_expires_at = NULL, last_counter = $2, updated_at = now()
		WHERE user_id = $1`, userID, int64(counter))
	return err
}

// AdvanceTOTPCounter records the step a code matched, so the same code cannot be replayed
// while it is still inside the validity window.
//
// The counter only ever moves forward. Without the guard, two requests carrying the same
// code could interleave between the verify and the write, and the second would be
// accepted -- exactly the replay the counter exists to prevent.
func (d *DB) AdvanceTOTPCounter(ctx context.Context, userID string, counter uint64) error {
	_, err := d.ExecContext(ctx, `
		UPDATE user_mfa_totp SET last_counter = $2, updated_at = now()
		WHERE user_id = $1 AND last_counter < $2`, userID, int64(counter))
	return err
}

func (d *DB) DeleteTOTP(ctx context.Context, userID string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM user_mfa_totp WHERE user_id = $1`, userID)
	return err
}

// ---------- WebAuthn credentials ----------

func (d *DB) ListWebAuthnCredentials(ctx context.Context, userID string) ([]WebAuthnCredential, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT id, user_id, credential_id, public_key, aaguid, sign_count, transports,
		       attestation_type, backup_eligible, backup_state, user_verified, rp_id, name,
		       last_used_at, created_at
		FROM user_webauthn_credentials WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	creds := make([]WebAuthnCredential, 0)
	for rows.Next() {
		var c WebAuthnCredential
		var credentialID string
		var signCount int64
		if err := rows.Scan(&c.ID, &c.UserID, &credentialID, &c.PublicKey, &c.AAGUID, &signCount,
			pq.Array(&c.Transports), &c.AttestationType, &c.BackupEligible, &c.BackupState,
			&c.UserVerified, &c.RPID, &c.Name, &c.LastUsedAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		// credential_id is TEXT, holding the raw id base64url-encoded -- see InsertWebAuthnCredential.
		if raw, err := decodeCredentialID(credentialID); err == nil {
			c.CredentialID = raw
		}
		c.SignCount = uint32(signCount)
		creds = append(creds, c)
	}
	return creds, rows.Err()
}

func (d *DB) InsertWebAuthnCredential(ctx context.Context, c *WebAuthnCredential) (string, error) {
	var id string
	err := d.QueryRowContext(ctx, `
		INSERT INTO user_webauthn_credentials
			(user_id, credential_id, public_key, aaguid, sign_count, transports,
			 attestation_type, backup_eligible, backup_state, user_verified, rp_id, name)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id`,
		c.UserID, encodeCredentialID(c.CredentialID), c.PublicKey, c.AAGUID, int64(c.SignCount),
		pq.Array(c.Transports), c.AttestationType, c.BackupEligible, c.BackupState,
		c.UserVerified, c.RPID, c.Name).Scan(&id)
	return id, err
}

// TouchWebAuthnCredential records a successful assertion.
func (d *DB) TouchWebAuthnCredential(ctx context.Context, credentialID []byte, signCount uint32) error {
	_, err := d.ExecContext(ctx, `
		UPDATE user_webauthn_credentials SET sign_count = $2, last_used_at = now()
		WHERE credential_id = $1`, encodeCredentialID(credentialID), int64(signCount))
	return err
}

// DeleteWebAuthnCredential removes one passkey, scoped to its owner so an id guessed from
// elsewhere cannot delete another user's factor.
func (d *DB) DeleteWebAuthnCredential(ctx context.Context, userID, id string) error {
	res, err := d.ExecContext(ctx,
		`DELETE FROM user_webauthn_credentials WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) RenameWebAuthnCredential(ctx context.Context, userID, id, name string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE user_webauthn_credentials SET name = $3 WHERE id = $1 AND user_id = $2`, id, userID, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- WebAuthn ceremony sessions ----------

func (d *DB) CreateWebAuthnSession(ctx context.Context, userID, purpose string, data []byte, expiresAt time.Time) (string, error) {
	var id string
	err := d.QueryRowContext(ctx, `
		INSERT INTO webauthn_sessions (user_id, purpose, session_data, expires_at)
		VALUES ($1, $2, $3, $4) RETURNING id`, userID, purpose, data, expiresAt).Scan(&id)
	return id, err
}

// TakeWebAuthnSession consumes a ceremony session, returning its data exactly once.
//
// The DELETE ... RETURNING is deliberate: a challenge must be single-use, and doing the
// read and the delete as two statements would let two concurrent requests both claim the
// same challenge. An expired row is deleted without being returned.
func (d *DB) TakeWebAuthnSession(ctx context.Context, id, userID, purpose string) ([]byte, error) {
	var data []byte
	err := d.QueryRowContext(ctx, `
		DELETE FROM webauthn_sessions
		WHERE id = $1 AND user_id = $2 AND purpose = $3 AND expires_at > now()
		RETURNING session_data`, id, userID, purpose).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return data, err
}

// DeleteExpiredWebAuthnSessions clears abandoned ceremonies.
func (d *DB) DeleteExpiredWebAuthnSessions(ctx context.Context) error {
	_, err := d.ExecContext(ctx, `DELETE FROM webauthn_sessions WHERE expires_at <= now()`)
	return err
}

// ---------- Backup codes ----------

// HashBackupCode is the storage form of a recovery code: unsalted SHA-256, matching how
// API tokens are already stored. Sound only because mfa.BackupCodeLength gives the input
// 80 bits of entropy, which puts a precomputed table out of reach.
func HashBackupCode(code string) string {
	sum := sha256.Sum256([]byte(mfa.NormalizeBackupCode(code)))
	return hex.EncodeToString(sum[:])
}

// ReplaceBackupCodes swaps a user's whole set atomically. Regenerating must not leave the
// old codes usable, and a partial write would do exactly that.
func (d *DB) ReplaceBackupCodes(ctx context.Context, userID string, codes []string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM user_mfa_backup_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, code := range codes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_mfa_backup_codes (user_id, code_hash) VALUES ($1, $2)`,
			userID, HashBackupCode(code)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ConsumeBackupCode marks a code used and reports whether it was valid and unused.
//
// The used_at IS NULL guard inside the UPDATE is what makes a code single-use even when
// two requests arrive together: exactly one of them updates a row.
func (d *DB) ConsumeBackupCode(ctx context.Context, userID, code string) (bool, error) {
	res, err := d.ExecContext(ctx, `
		UPDATE user_mfa_backup_codes SET used_at = now()
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`, userID, HashBackupCode(code))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (d *DB) CountUnusedBackupCodes(ctx context.Context, userID string) (int, error) {
	var n int
	err := d.QueryRowContext(ctx,
		`SELECT count(*) FROM user_mfa_backup_codes WHERE user_id = $1 AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}

func (d *DB) DeleteBackupCodes(ctx context.Context, userID string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM user_mfa_backup_codes WHERE user_id = $1`, userID)
	return err
}

// ---------- Lockout ----------

func (d *DB) GetLockout(ctx context.Context, userID string) (mfa.LockoutState, error) {
	var s mfa.LockoutState
	err := d.QueryRowContext(ctx, `
		SELECT failed_count, window_started_at, locked_until FROM mfa_lockouts WHERE user_id = $1`, userID).
		Scan(&s.FailedCount, &s.WindowStartedAt, &s.LockedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return mfa.LockoutState{}, nil
	}
	return s, err
}

func (d *DB) SaveLockout(ctx context.Context, userID string, s mfa.LockoutState) error {
	_, err := d.ExecContext(ctx, `
		INSERT INTO mfa_lockouts (user_id, failed_count, window_started_at, locked_until, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (user_id) DO UPDATE
		SET failed_count = $2, window_started_at = $3, locked_until = $4, updated_at = now()`,
		userID, s.FailedCount, s.WindowStartedAt, s.LockedUntil)
	return err
}

// ---------- Enrollment view ----------

// ListEnrollments derives a user's factors from the tables that hold them, rather than
// from a flag that could drift out of step with reality.
func (d *DB) ListEnrollments(ctx context.Context, userID string) ([]mfa.Enrollment, error) {
	out := make([]mfa.Enrollment, 0, 2)

	totp, err := d.GetTOTP(ctx, userID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if totp.Confirmed() {
		out = append(out, mfa.Enrollment{
			Method:    mfa.MethodTOTP,
			Name:      "Authenticator app",
			CreatedAt: totp.CreatedAt,
		})
	}

	creds, err := d.ListWebAuthnCredentials(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, c := range creds {
		out = append(out, mfa.Enrollment{
			Method:     mfa.MethodWebAuthn,
			ID:         c.ID,
			Name:       c.Name,
			CreatedAt:  c.CreatedAt,
			LastUsedAt: c.LastUsedAt,
		})
	}
	return out, nil
}

// ---------- Re-authentication grants ----------

// CreateReauthGrant records a fresh proof of identity.
func (d *DB) CreateReauthGrant(ctx context.Context, userID, method string, expiresAt time.Time) (string, error) {
	var id string
	err := d.QueryRowContext(ctx, `
		INSERT INTO mfa_reauth_grants (user_id, method, expires_at)
		VALUES ($1, $2, $3) RETURNING id`, userID, method, expiresAt).Scan(&id)
	return id, err
}

// TakeReauthGrant spends a grant, returning the method that produced it.
//
// DELETE ... RETURNING so one proof buys exactly one destructive change: reading and
// deleting separately would let two requests spend the same grant, which is the whole
// difference between "confirm each removal" and "confirm once, then remove everything".
func (d *DB) TakeReauthGrant(ctx context.Context, id, userID string) (string, error) {
	var method string
	err := d.QueryRowContext(ctx, `
		DELETE FROM mfa_reauth_grants
		WHERE id = $1 AND user_id = $2 AND expires_at > now()
		RETURNING method`, id, userID).Scan(&method)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return method, err
}

func (d *DB) DeleteExpiredReauthGrants(ctx context.Context) error {
	_, err := d.ExecContext(ctx, `DELETE FROM mfa_reauth_grants WHERE expires_at <= now()`)
	return err
}

// ResetUserMFA removes every factor a user holds, in one transaction.
//
// This is the escape hatch for someone who has lost both their device and their recovery
// codes; before it existed there was no way back into such an account at all. Partial
// success would be the worst outcome -- an account with its passkeys gone but a TOTP
// secret nobody can produce codes for is still locked out -- so it is all or nothing.
func (d *DB) ResetUserMFA(ctx context.Context, userID string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`DELETE FROM user_mfa_totp WHERE user_id = $1`,
		`DELETE FROM user_webauthn_credentials WHERE user_id = $1`,
		`DELETE FROM user_mfa_backup_codes WHERE user_id = $1`,
		`DELETE FROM webauthn_sessions WHERE user_id = $1`,
		`DELETE FROM mfa_reauth_grants WHERE user_id = $1`,
		// Clear the lockout too: a user whose factors were reset because they were
		// locked out would otherwise still be serving the timer.
		`DELETE FROM mfa_lockouts WHERE user_id = $1`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
