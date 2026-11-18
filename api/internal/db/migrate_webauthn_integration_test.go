package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Runs only when VESTA_TEST_DATABASE_URL points at a throwaway database, e.g.
//
//	VESTA_TEST_DATABASE_URL=postgres://postgres@127.0.0.1:5433/vesta_test?sslmode=disable go test ./internal/db/
//
// It recreates the database state that broke step-up reauth in production:
// webauthn_sessions.purpose allowed only 'register' and 'authenticate', while the
// reauth ceremony inserts 'reauth'. migrate() must repair that on an existing
// database, where CREATE TABLE IF NOT EXISTS does nothing.
func TestMigrateAllowsReauthOnAnExistingDatabase(t *testing.T) {
	url := os.Getenv("VESTA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set VESTA_TEST_DATABASE_URL to run this against a throwaway Postgres")
	}

	d, err := New(url)
	if err != nil {
		t.Fatalf("connect and migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()

	// This test narrows a live constraint, so it needs a database of its own —
	// clear any ceremonies a previous run left behind before doing so.
	if _, err := d.ExecContext(ctx, `DELETE FROM webauthn_sessions WHERE purpose NOT IN ('register', 'authenticate')`); err != nil {
		t.Fatalf("could not clear earlier ceremonies: %v", err)
	}

	// The state a database created before 'reauth' existed is in.
	if _, err := d.ExecContext(ctx, `
		ALTER TABLE webauthn_sessions DROP CONSTRAINT IF EXISTS webauthn_sessions_purpose_check;
		ALTER TABLE webauthn_sessions ADD CONSTRAINT webauthn_sessions_purpose_check
			CHECK (purpose IN ('register', 'authenticate'))`); err != nil {
		t.Fatalf("could not put the old constraint back: %v", err)
	}

	unique := fmt.Sprintf("reauth-test-%d", time.Now().UnixNano())
	var userID string
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users (email, username, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		unique+"@example.test", unique).Scan(&userID); err != nil {
		t.Fatalf("could not create a user: %v", err)
	}
	// Deleting the user cascades to its ceremonies, leaving the database as found.
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM users WHERE id = $1`, userID) })
	expires := time.Now().Add(time.Minute)

	// Before: exactly the production failure.
	_, err = d.CreateWebAuthnSession(ctx, userID, PurposeReauth, []byte(`{}`), expires)
	if err == nil {
		t.Fatal("expected the old constraint to reject 'reauth'")
	}
	if !strings.Contains(err.Error(), "webauthn_sessions_purpose_check") {
		t.Fatalf("expected the purpose check to fail, got: %v", err)
	}
	t.Logf("reproduced: %v", err)

	// What deploying the fix does.
	if err := d.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// After: every purpose the code uses is accepted, and each ceremony is still
	// scoped to its own purpose.
	for _, purpose := range []string{PurposeRegister, PurposeAuthenticate, PurposeReauth} {
		id, err := d.CreateWebAuthnSession(ctx, userID, purpose, []byte(`{}`), expires)
		if err != nil {
			t.Fatalf("purpose %q still rejected after migrate: %v", purpose, err)
		}
		if purpose != PurposeRegister {
			if _, err := d.TakeWebAuthnSession(ctx, id, userID, PurposeRegister); err == nil {
				t.Fatalf("a %q ceremony was consumable as %q", purpose, PurposeRegister)
			}
		}
	}

	// Deploying again changes nothing.
	for i := 0; i < 2; i++ {
		if err := d.migrate(); err != nil {
			t.Fatalf("migrate is not repeatable: %v", err)
		}
	}
	var def string
	if err := d.QueryRowContext(ctx,
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'webauthn_sessions_purpose_check'`).Scan(&def); err != nil {
		t.Fatalf("could not read the constraint: %v", err)
	}
	for _, purpose := range []string{PurposeRegister, PurposeAuthenticate, PurposeReauth} {
		if !strings.Contains(def, purpose) {
			t.Fatalf("constraint no longer allows %q: %s", purpose, def)
		}
	}
	t.Logf("constraint now: %s", def)
}
