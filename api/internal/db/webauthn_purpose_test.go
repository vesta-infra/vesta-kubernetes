package db

import (
	"regexp"
	"strings"
	"testing"
)

// The bug this guards against: 'reauth' was used by the reauth handler but the
// webauthn_sessions.purpose CHECK only allowed 'register' and 'authenticate', so
// every step-up ceremony failed with webauthn_sessions_purpose_check. Adding a
// purpose without widening both the CREATE TABLE and the reconciling ALTER now
// fails here instead of in production.
func TestWebAuthnPurposesAllowedBySchema(t *testing.T) {
	purposes := []string{PurposeRegister, PurposeAuthenticate, PurposeReauth}

	checks := regexp.MustCompile(`(?s)purpose (?:TEXT NOT NULL )?(?:IN|CHECK \(purpose IN) \(([^)]*)\)`).FindAllStringSubmatch(schema, -1)
	if len(checks) < 2 {
		t.Fatalf("expected the CREATE TABLE check and the reconciling ALTER to both list purposes, found %d", len(checks))
	}

	for i, check := range checks {
		allowed := check[1]
		for _, purpose := range purposes {
			if !strings.Contains(allowed, "'"+purpose+"'") {
				t.Errorf("purpose %q is used in code but missing from purpose list %d: %s", purpose, i+1, strings.TrimSpace(allowed))
			}
		}
	}
}
